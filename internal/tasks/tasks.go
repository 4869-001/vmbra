// Package tasks 提供带实时日志流的后台任务队列。
package tasks

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/4869-001/vmbra/internal/store"
)

const (
	maxBuffer = 512 << 10 // 单任务内存日志上限

	// DefaultConcurrency 是同时运行的任务数上限，超出的任务保持「排队中」。
	DefaultConcurrency = 3

	// maxHistory 是保留的历史任务条数（含排队与运行中）。
	maxHistory = 200
)

// ErrNotFound 表示任务不存在。
var ErrNotFound = errors.New("任务不存在")

// Item 是一个任务的运行时视图。
type Item struct {
	mu       sync.Mutex
	rec      store.TaskRecord
	buf      bytes.Buffer
	file     *os.File
	subs     map[chan string]struct{}
	cancel   context.CancelFunc
	finished chan struct{}
	run      func(ctx context.Context, it *Item) error
}

func (i *Item) Write(p []byte) (int, error) {
	i.mu.Lock()
	if i.buf.Len() < maxBuffer {
		i.buf.Write(p)
	}
	if i.file != nil {
		_, _ = i.file.Write(p)
	}
	subs := make([]chan string, 0, len(i.subs))
	for ch := range i.subs {
		subs = append(subs, ch)
	}
	i.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- string(p):
		default: // 订阅者消费不过来时丢弃，避免阻塞任务
		}
	}
	return len(p), nil
}

// Infof 写入一行带时间戳的日志。
func (i *Item) Infof(format string, args ...any) {
	fmt.Fprintf(i, "[%s] %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
}

// Subscribe 订阅实时日志，返回通道与取消函数。
func (i *Item) Subscribe() (<-chan string, func()) {
	ch := make(chan string, 256)
	i.mu.Lock()
	if i.subs == nil {
		i.subs = map[chan string]struct{}{}
	}
	i.subs[ch] = struct{}{}
	i.mu.Unlock()
	return ch, func() {
		i.mu.Lock()
		if _, ok := i.subs[ch]; ok {
			delete(i.subs, ch)
			close(ch)
		}
		i.mu.Unlock()
	}
}

// Snapshot 返回任务记录与当前完整日志。
func (i *Item) Snapshot() (store.TaskRecord, string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.rec, i.buf.String()
}

// Record 返回任务记录。
func (i *Item) Record() store.TaskRecord {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.rec
}

// Done 返回任务结束信号。
func (i *Item) Done() <-chan struct{} { return i.finished }

// Manager 管理任务生命周期与历史，并按并发上限调度排队任务。
type Manager struct {
	mu         sync.RWMutex
	items      map[string]*Item
	order      []string
	queue      []*Item
	running    int
	maxRunning int
	store      *store.Store
	logs       string
}

// New 创建任务管理器，并从持久化状态恢复历史任务元信息。
func New(st *store.Store, logDir string) *Manager {
	_ = os.MkdirAll(logDir, 0o700)
	m := &Manager{items: map[string]*Item{}, store: st, logs: logDir, maxRunning: DefaultConcurrency}
	state := st.Copy()
	for _, rec := range state.Tasks {
		if rec.Status == "running" || rec.Status == "queued" {
			rec.Status = "failed"
			rec.Error = "管理服务重启，任务已中断"
		}
		it := &Item{rec: rec, finished: make(chan struct{})}
		close(it.finished)
		if b, err := os.ReadFile(filepath.Join(logDir, rec.ID+".log")); err == nil {
			it.buf.Write(b)
		}
		m.items[rec.ID] = it
		m.order = append(m.order, rec.ID)
	}
	return m
}

// Start 提交任务：未达到并发上限时立即运行，否则保持排队。
func (m *Manager) Start(kind, title string, fn func(ctx context.Context, it *Item) error) *Item {
	id := newID()
	rec := store.TaskRecord{
		ID:        id,
		Kind:      kind,
		Title:     title,
		Status:    "queued",
		CreatedAt: time.Now().UTC(),
	}
	it := &Item{rec: rec, finished: make(chan struct{}), subs: map[chan string]struct{}{}, run: fn}
	if f, err := os.Create(filepath.Join(m.logs, id+".log")); err == nil {
		it.file = f
	}

	m.mu.Lock()
	m.items[id] = it
	m.order = append([]string{id}, m.order...)
	m.queue = append(m.queue, it)
	if len(m.order) > maxHistory {
		for _, old := range m.order[maxHistory:] {
			if stale, ok := m.items[old]; ok {
				m.retireLocked(stale)
			}
			delete(m.items, old)
		}
		m.order = m.order[:maxHistory]
	}
	m.mu.Unlock()
	_ = m.store.UpsertTask(rec)

	m.dispatch()
	return it
}

// dispatch 按 FIFO 提升排队任务，直到达到并发上限。调用方不得持有 m.mu。
func (m *Manager) dispatch() {
	for {
		m.mu.Lock()
		if m.running >= m.maxRunning || len(m.queue) == 0 {
			m.mu.Unlock()
			return
		}
		it := m.queue[0]
		m.queue = m.queue[1:]
		m.running++

		ctx, cancel := context.WithCancel(context.Background())
		it.mu.Lock()
		it.cancel = cancel
		it.rec.Status = "running"
		now := time.Now().UTC()
		it.rec.StartedAt = &now
		rec := it.rec
		it.mu.Unlock()
		m.mu.Unlock()

		_ = m.store.UpsertTask(rec)
		go m.exec(it, ctx)
	}
}

// exec 执行任务并在结束后归还并发额度。
func (m *Manager) exec(it *Item, ctx context.Context) {
	err := it.run(ctx, it)

	end := time.Now().UTC()
	it.mu.Lock()
	it.rec.FinishedAt = &end
	switch {
	case err != nil && errors.Is(err, context.Canceled):
		it.rec.Status = "canceled"
		it.rec.Error = "任务已取消"
	case err != nil:
		it.rec.Status = "failed"
		it.rec.Error = err.Error()
	default:
		it.rec.Status = "succeeded"
	}
	if it.file != nil {
		it.file.Close()
		it.file = nil
	}
	recSnapshot := it.rec
	subs := make([]chan string, 0, len(it.subs))
	for ch := range it.subs {
		subs = append(subs, ch)
	}
	it.subs = map[chan string]struct{}{}
	it.mu.Unlock()

	// 先落盘再广播结束，保证等待方看到终态时状态文件已经一致。
	_ = m.store.UpsertTask(recSnapshot)
	for _, ch := range subs {
		close(ch)
	}
	close(it.finished)

	m.mu.Lock()
	m.running--
	m.mu.Unlock()
	m.dispatch()
}

// retireLocked 处理被历史容量裁剪的任务：排队中的直接取消，运行中的留给它自然结束。
// 调用方需持有 m.mu。
func (m *Manager) retireLocked(it *Item) {
	if !m.dropFromQueueLocked(it) {
		return
	}
	it.mu.Lock()
	it.rec.Status = "canceled"
	it.rec.Error = "任务已超出历史保留上限，未执行"
	end := time.Now().UTC()
	it.rec.FinishedAt = &end
	if it.file != nil {
		it.file.Close()
		it.file = nil
	}
	rec := it.rec
	it.mu.Unlock()
	_ = m.store.UpsertTask(rec)
	close(it.finished)
}

// dropFromQueueLocked 从排队队列移除任务。调用方需持有 m.mu。
func (m *Manager) dropFromQueueLocked(it *Item) bool {
	for i, q := range m.queue {
		if q == it {
			m.queue = append(m.queue[:i], m.queue[i+1:]...)
			return true
		}
	}
	return false
}

// Get 返回任务项。
func (m *Manager) Get(id string) (*Item, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if it, ok := m.items[id]; ok {
		return it, nil
	}
	return nil, ErrNotFound
}

// Cancel 取消任务：排队中的直接出队并标记取消，运行中的通过 context 终止。
func (m *Manager) Cancel(id string) error {
	it, err := m.Get(id)
	if err != nil {
		return err
	}

	m.mu.Lock()
	if m.dropFromQueueLocked(it) {
		m.mu.Unlock()
		m.cancelQueued(it)
		return nil
	}
	m.mu.Unlock()

	it.mu.Lock()
	cancel := it.cancel
	status := it.rec.Status
	it.mu.Unlock()
	switch status {
	case "running":
		if cancel != nil {
			cancel()
		}
		return nil
	case "queued":
		// 刚被调度取走但尚未标记运行，属于极短窗口，让调用方重试。
		return errors.New("任务正在启动，请稍后重试")
	default:
		return fmt.Errorf("任务已结束，状态为 %s", status)
	}
}

// cancelQueued 结束一个尚未执行的任务。
func (m *Manager) cancelQueued(it *Item) {
	it.mu.Lock()
	it.rec.Status = "canceled"
	it.rec.Error = "任务已取消"
	end := time.Now().UTC()
	it.rec.FinishedAt = &end
	if it.file != nil {
		it.file.Close()
		it.file = nil
	}
	rec := it.rec
	it.mu.Unlock()

	_ = m.store.UpsertTask(rec)
	close(it.finished)
}

// List 返回最近的任务记录（按创建时间倒序）。
func (m *Manager) List() []store.TaskRecord {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]store.TaskRecord, 0, len(m.order))
	for _, id := range m.order {
		if it, ok := m.items[id]; ok {
			out = append(out, it.Record())
		}
	}
	return out
}

// Wait 等待任务结束或超时。
func (m *Manager) Wait(id string, timeout time.Duration) (store.TaskRecord, bool) {
	it, err := m.Get(id)
	if err != nil {
		return store.TaskRecord{}, false
	}
	select {
	case <-it.Done():
		return it.Record(), true
	case <-time.After(timeout):
		return it.Record(), false
	}
}

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "t" + hex.EncodeToString(b)
}

// Tail 返回任务日志的最后 n 行。
func (m *Manager) Tail(id string, n int) (string, error) {
	it, err := m.Get(id)
	if err != nil {
		return "", err
	}
	_, out := it.Snapshot()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n"), nil
}

var _ io.Writer = (*Item)(nil)

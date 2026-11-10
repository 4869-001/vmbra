package tasks

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/4869-001/vmbra/internal/store"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	return New(st, filepath.Join(dir, "logs"))
}

func waitUntil(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("等待条件超时")
}

func countStatus(m *Manager, status string) int {
	n := 0
	for _, rec := range m.List() {
		if rec.Status == status {
			n++
		}
	}
	return n
}

func TestQueueRespectsConcurrency(t *testing.T) {
	m := newTestManager(t)
	m.maxRunning = 2

	release := make(chan struct{})
	var started atomic.Int32
	submit := func() *Item {
		return m.Start("test", "阻塞任务", func(ctx context.Context, it *Item) error {
			started.Add(1)
			<-release
			return nil
		})
	}

	items := []*Item{submit(), submit(), submit(), submit()}
	waitUntil(t, 2*time.Second, func() bool { return started.Load() == 2 })

	if got := countStatus(m, "running"); got != 2 {
		t.Fatalf("应只有 2 个任务在执行，实际 %d", got)
	}
	if got := countStatus(m, "queued"); got != 2 {
		t.Fatalf("应有 2 个任务排队，实际 %d", got)
	}

	// 排队中的任务可以直接取消，且不会被执行。
	cancelID := items[2].Record().ID
	if err := m.Cancel(cancelID); err != nil {
		t.Fatalf("取消排队任务失败：%v", err)
	}
	rec, err := m.Get(cancelID)
	if err != nil {
		t.Fatal(err)
	}
	if got := rec.Record().Status; got != "canceled" {
		t.Fatalf("排队任务应标记为 canceled，实际 %s", got)
	}
	if started.Load() != 2 {
		t.Fatalf("被取消的排队任务不应执行，已启动 %d", started.Load())
	}

	close(release)
	for _, it := range items {
		if _, ok := m.Wait(it.Record().ID, 5*time.Second); !ok {
			t.Fatalf("任务未在超时内结束：%s", it.Record().Title)
		}
	}
	if got := countStatus(m, "succeeded"); got != 3 {
		t.Fatalf("应有 3 个任务成功，实际 %d", got)
	}
	if got := countStatus(m, "canceled"); got != 1 {
		t.Fatalf("应有 1 个任务被取消，实际 %d", got)
	}
}

func TestCancelRunningTask(t *testing.T) {
	m := newTestManager(t)
	item := m.Start("test", "可取消任务", func(ctx context.Context, it *Item) error {
		<-ctx.Done()
		return ctx.Err()
	})
	id := item.Record().ID
	waitUntil(t, 2*time.Second, func() bool {
		return item.Record().Status == "running"
	})
	if err := m.Cancel(id); err != nil {
		t.Fatalf("取消运行中任务失败：%v", err)
	}
	rec, ok := m.Wait(id, 3*time.Second)
	if !ok {
		t.Fatal("任务未在超时内结束")
	}
	if rec.Status != "canceled" {
		t.Fatalf("任务应为 canceled，实际 %s（%s）", rec.Status, rec.Error)
	}
}

func TestHistoryTrimRetiresQueuedTask(t *testing.T) {
	m := newTestManager(t)
	m.maxRunning = 0 // 全部排队，便于验证裁剪行为

	first := m.Start("test", "最早的任务", func(ctx context.Context, it *Item) error { return nil })
	for i := 0; i < maxHistory; i++ {
		m.Start("test", "填充任务", func(ctx context.Context, it *Item) error { return nil })
	}

	select {
	case <-first.Done():
	default:
		t.Fatal("被裁剪的排队任务应当立即结束")
	}
	if got := first.Record().Status; got != "canceled" {
		t.Fatalf("被裁剪的排队任务应为 canceled，实际 %s", got)
	}
	if _, err := m.Get(first.Record().ID); err == nil {
		t.Fatal("超出历史容量的任务应从索引中移除")
	}
}

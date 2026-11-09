// Package forward 实现工作站端口转发：在宿主机回环地址上监听端口，把连接中继到
// 工作站内部端口，并统计连接数、流量与错误。规则的生命周期跟随工作站。
package forward

import (
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Stats 是一条转发规则的运行状态。
type Stats struct {
	Listening   bool   `json:"listening"`
	Addr        string `json:"addr,omitempty"`
	Connections int64  `json:"connections"`
	Active      int64  `json:"active"`
	BytesUp     int64  `json:"bytesUp"`   // 客户端 → 工作站
	BytesDown   int64  `json:"bytesDown"` // 工作站 → 客户端
	Error       string `json:"error,omitempty"`
	StartedAt   string `json:"startedAt,omitempty"`
}

type rule struct {
	id       string
	listener net.Listener
	stats    Stats
	started  time.Time
	closing  atomic.Bool
}

// Manager 管理全部转发规则。
type Manager struct {
	mu   sync.Mutex
	kids map[string]*rule
}

// New 创建转发管理器。
func New() *Manager {
	return &Manager{kids: map[string]*rule{}}
}

// Start 启动一条转发：监听 127.0.0.1:hostPort，转发到 guestHost:guestPort。
func (m *Manager) Start(id string, hostPort int, guestHost string, guestPort int) error {
	if hostPort < 1 || hostPort > 65535 || guestPort < 1 || guestPort > 65535 {
		return fmt.Errorf("端口需在 1-65535 之间")
	}
	if guestHost == "" {
		guestHost = "127.0.0.1"
	}
	addr := fmt.Sprintf("127.0.0.1:%d", hostPort)
	target := net.JoinHostPort(guestHost, fmt.Sprintf("%d", guestPort))

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		m.setError(id, addr, err)
		return fmt.Errorf("监听 %s 失败：%v", addr, err)
	}

	r := &rule{id: id, listener: ln, started: time.Now()}
	r.stats = Stats{Listening: true, Addr: addr, StartedAt: r.started.UTC().Format(time.RFC3339)}

	m.mu.Lock()
	if old, ok := m.kids[id]; ok && old.listener != nil {
		_ = old.listener.Close()
	}
	m.kids[id] = r
	m.mu.Unlock()

	go m.accept(r, target)
	return nil
}

func (m *Manager) setError(id, addr string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.kids[id]
	if !ok {
		r = &rule{id: id}
		m.kids[id] = r
	}
	r.stats.Listening = false
	r.stats.Addr = addr
	r.stats.Error = err.Error()
}

func (m *Manager) accept(r *rule, target string) {
	for {
		conn, err := r.listener.Accept()
		if err != nil {
			if r.closing.Load() {
				return
			}
			m.mu.Lock()
			r.stats.Listening = false
			r.stats.Error = err.Error()
			m.mu.Unlock()
			return
		}
		go m.pipe(r, conn, target)
	}
}

func (m *Manager) pipe(r *rule, client net.Conn, target string) {
	upstream, err := net.DialTimeout("tcp", target, 5*time.Second)
	if err != nil {
		m.mu.Lock()
		r.stats.Error = "连接工作站端口失败：" + err.Error()
		m.mu.Unlock()
		_ = client.Close()
		return
	}
	m.mu.Lock()
	r.stats.Connections++
	r.stats.Active++
	m.mu.Unlock()

	done := make(chan struct{}, 2)
	go func() {
		n, _ := io.Copy(upstream, client)
		atomic.AddInt64(&r.stats.BytesUp, n)
		_ = upstream.Close()
		done <- struct{}{}
	}()
	go func() {
		n, _ := io.Copy(client, upstream)
		atomic.AddInt64(&r.stats.BytesDown, n)
		_ = client.Close()
		done <- struct{}{}
	}()
	<-done
	<-done
	m.mu.Lock()
	r.stats.Active--
	m.mu.Unlock()
}

// Stop 停止一条转发规则。
func (m *Manager) Stop(id string) {
	m.mu.Lock()
	r, ok := m.kids[id]
	delete(m.kids, id)
	m.mu.Unlock()
	if !ok || r.listener == nil {
		return
	}
	r.closing.Store(true)
	_ = r.listener.Close()
}

// StopAll 停止全部转发规则（工作站停止时调用）。
func (m *Manager) StopAll() {
	m.mu.Lock()
	ids := make([]string, 0, len(m.kids))
	for id := range m.kids {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.Stop(id)
	}
}

// Stats 返回某条规则的运行状态；未启动时返回 Listening=false。
func (m *Manager) Stats(id string) Stats {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.kids[id]; ok {
		s := r.stats
		s.Active = atomic.LoadInt64(&r.stats.Active)
		s.BytesUp = atomic.LoadInt64(&r.stats.BytesUp)
		s.BytesDown = atomic.LoadInt64(&r.stats.BytesDown)
		s.Listening = r.listener != nil && s.Error == ""
		return s
	}
	return Stats{}
}

// Running 返回当前是否有同端口监听（用于冲突提示）。
func (m *Manager) Running(id string) bool {
	return m.Stats(id).Listening
}

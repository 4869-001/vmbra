// Package dnsproxy 提供工作站内使用的本地 DNS 转发器。
//
// 设计目标：让隔离环境拥有一条与宿主/VPS 一致的解析出口——固定监听
// 127.0.0.1:<port>，把查询转发到配置的上游服务器，并记录最近查询，供
// 控制台展示与排查。转发器只做透明 UDP 中继，不缓存、不改写报文。
package dnsproxy

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

// Query 是一次解析请求的记录。
type Query struct {
	At       time.Time `json:"at"`
	Name     string    `json:"name"`
	Upstream string    `json:"upstream"`
	OK       bool      `json:"ok"`
	MS       int64     `json:"ms"`
	Detail   string    `json:"detail,omitempty"`
}

// Stats 汇总转发器运行情况。
type Stats struct {
	Running   bool   `json:"running"`
	Addr      string `json:"addr"`
	Total     int64  `json:"total"`
	Failed    int64  `json:"failed"`
	LastError string `json:"lastError,omitempty"`
}

// Server 是 UDP DNS 转发器。
type Server struct {
	mu        sync.Mutex
	conn      *net.UDPConn
	upstreams []string
	port      int
	running   bool
	total     int64
	failed    int64
	lastErr   string
	recent    []Query
}

// New 创建一个转发器，upstreams 形如 1.1.1.1 或 1.1.1.1:53。
func New(upstreams []string) *Server {
	return &Server{upstreams: normalizeUpstreams(upstreams), recent: []Query{}}
}

func normalizeUpstreams(list []string) []string {
	out := make([]string, 0, len(list))
	for _, u := range list {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		if _, _, err := net.SplitHostPort(u); err != nil {
			u = net.JoinHostPort(u, "53")
		}
		out = append(out, u)
	}
	return out
}

// Start 在 127.0.0.1:port 启动转发器；重复调用会先停止旧实例。
func (s *Server) Start(port int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		s.stopLocked()
	}
	if port < 0 {
		port = 1053
	}
	// port=0 表示由系统分配空闲端口（用于默认端口被占用时的回退）。
	addr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		s.lastErr = err.Error()
		return err
	}
	s.conn = conn
	if local, ok := conn.LocalAddr().(*net.UDPAddr); ok {
		s.port = local.Port
	} else {
		s.port = port
	}
	s.running = true
	s.lastErr = ""
	go s.serve(conn)
	return nil
}

// Stop 关闭转发器。
func (s *Server) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopLocked()
}

func (s *Server) stopLocked() {
	if s.conn != nil {
		_ = s.conn.Close()
		s.conn = nil
	}
	s.running = false
}

// SetUpstreams 更新上游服务器列表（无需重启监听）。
func (s *Server) SetUpstreams(list []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.upstreams = normalizeUpstreams(list)
}

// Stats 返回当前运行状态。
func (s *Server) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Stats{
		Running:   s.running,
		Total:     s.total,
		Failed:    s.failed,
		LastError: s.lastErr,
	}
	if s.running {
		st.Addr = fmt.Sprintf("127.0.0.1:%d", s.port)
	}
	return st
}

// Recent 返回最近的解析记录（最多 30 条，最新在前）。
func (s *Server) Recent() []Query {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Query, len(s.recent))
	copy(out, s.recent)
	return out
}

func (s *Server) record(q Query) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.total++
	if !q.OK {
		s.failed++
	}
	s.recent = append([]Query{q}, s.recent...)
	if len(s.recent) > 30 {
		s.recent = s.recent[:30]
	}
}

func (s *Server) serve(conn *net.UDPConn) {
	buf := make([]byte, 4096)
	for {
		n, client, err := conn.ReadFromUDP(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		pkt := make([]byte, n)
		copy(pkt, buf[:n])
		go s.handle(conn, client, pkt)
	}
}

func (s *Server) handle(conn *net.UDPConn, client *net.UDPAddr, pkt []byte) {
	name := qname(pkt)
	s.mu.Lock()
	ups := append([]string{}, s.upstreams...)
	s.mu.Unlock()

	start := time.Now()
	var lastErr error
	for _, upstream := range ups {
		reply, err := exchange(upstream, pkt, 2*time.Second)
		if err != nil {
			lastErr = err
			continue
		}
		if _, err := conn.WriteToUDP(reply, client); err != nil {
			lastErr = err
			continue
		}
		s.record(Query{
			At:       start.UTC(),
			Name:     name,
			Upstream: upstream,
			OK:       true,
			MS:       time.Since(start).Milliseconds(),
		})
		return
	}
	detail := "没有可用的上游服务器"
	if lastErr != nil {
		detail = lastErr.Error()
	}
	s.record(Query{
		At:       start.UTC(),
		Name:     name,
		Upstream: strings.Join(ups, ","),
		OK:       false,
		MS:       time.Since(start).Milliseconds(),
		Detail:   detail,
	})
}

func exchange(upstream string, pkt []byte, timeout time.Duration) ([]byte, error) {
	conn, err := net.DialTimeout("udp", upstream, timeout)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if _, err := conn.Write(pkt); err != nil {
		return nil, err
	}
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, err
	}
	return buf[:n], nil
}

// qname 从 DNS 报文中提取查询域名，解析失败时返回空串。
func qname(pkt []byte) string {
	if len(pkt) < 13 {
		return ""
	}
	var b strings.Builder
	i := 12
	for i < len(pkt) {
		l := int(pkt[i])
		if l == 0 {
			break
		}
		if l&0xC0 == 0xC0 {
			break
		}
		i++
		if i+l > len(pkt) {
			return ""
		}
		if b.Len() > 0 {
			b.WriteByte('.')
		}
		b.Write(pkt[i : i+l])
		i += l
	}
	return b.String()
}

package service

import (
	"errors"
	"fmt"
	"math/rand"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/4869-001/vmbra/internal/forward"
	"github.com/4869-001/vmbra/internal/store"
)

// NetworkInfo 返回网络面板数据（含转发规则运行状态）。
func (s *Service) NetworkInfo() map[string]any {
	st := s.Store.Copy()
	note := "端口转发把宿主回环端口中继到工作站内部端口，规则跟随工作站启动；策略默认为 (deny network*)，工作站内要监听或访问回环端口需先在「工作站出网」放行网络。"
	type row struct {
		store.PortForward
		Runtime forward.Stats `json:"runtime"`
	}
	rows := make([]row, 0, len(st.Forwards))
	for _, f := range st.Forwards {
		rows = append(rows, row{PortForward: f, Runtime: s.Fwd.Stats(f.ID)})
	}
	return map[string]any{
		"forwards": rows,
		"proxy":    st.Proxy,
		"network":  st.Settings.NetworkEnabled,
		"note":     note,
		"driver":   st.Current().Driver,
		"egress":   s.Gate.Snapshot(),
	}
}

// syncForwards 按当前规则启停转发（工作站运行中才真正监听）。
func (s *Service) syncForwards() {
	st := s.Store.Copy()
	running := st.Current().State == "running"
	for _, f := range st.Forwards {
		if !running || !f.Enabled || f.Proto != "tcp" {
			s.Fwd.Stop(f.ID)
			continue
		}
		if s.Fwd.Running(f.ID) {
			continue
		}
		_ = s.Fwd.Start(f.ID, f.HostPort, "127.0.0.1", f.GuestPort)
	}
}

// TestForward 检查转发状态与目标端口可达性。
func (s *Service) TestForward(id string) (map[string]any, error) {
	st := s.Store.Copy()
	var target store.PortForward
	for _, f := range st.Forwards {
		if f.ID == id {
			target = f
			break
		}
	}
	if target.ID == "" {
		return nil, errors.New("规则不存在")
	}
	stats := s.Fwd.Stats(id)
	out := map[string]any{
		"listening":   stats.Listening,
		"addr":        stats.Addr,
		"connections": stats.Connections,
		"error":       stats.Error,
	}
	start := time.Now()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(target.GuestPort)), 2*time.Second)
	if err != nil {
		out["guestOk"] = false
		out["guestError"] = "工作站端口未监听：" + err.Error()
		return out, nil
	}
	_ = conn.Close()
	out["guestOk"] = true
	out["guestLatencyMs"] = time.Since(start).Milliseconds()
	return out, nil
}

// AddForward 新增端口转发规则。
func (s *Service) AddForward(label string, hostPort, guestPort int, proto string) (store.PortForward, error) {
	if hostPort < 1 || hostPort > 65535 || guestPort < 1 || guestPort > 65535 {
		return store.PortForward{}, errors.New("端口需在 1-65535 之间")
	}
	if proto == "" {
		proto = "tcp"
	}
	if proto != "tcp" {
		return store.PortForward{}, errors.New("本地进程隔离模式仅支持 TCP 转发")
	}
	if label == "" {
		label = fmt.Sprintf("%d → %d", hostPort, guestPort)
	}
	var created store.PortForward
	err := s.Store.Update(func(st *store.State) {
		for _, f := range st.Forwards {
			if f.HostPort == hostPort && f.Proto == proto {
				created = f
				return
			}
		}
		f := store.PortForward{
			ID:        fmt.Sprintf("f%04x", rand.Intn(0xffff)),
			Label:     label,
			HostPort:  hostPort,
			GuestPort: guestPort,
			Proto:     proto,
			Enabled:   true,
		}
		st.Forwards = append(st.Forwards, f)
		created = f
	})
	if err != nil {
		return store.PortForward{}, err
	}
	s.syncForwards()
	return created, nil
}

// UpdateForward 修改规则（启停或改名）。
func (s *Service) UpdateForward(id string, enabled *bool, label *string) (store.PortForward, error) {
	var out store.PortForward
	found := false
	err := s.Store.Update(func(st *store.State) {
		for i := range st.Forwards {
			if st.Forwards[i].ID != id {
				continue
			}
			if enabled != nil {
				st.Forwards[i].Enabled = *enabled
			}
			if label != nil && strings.TrimSpace(*label) != "" {
				st.Forwards[i].Label = strings.TrimSpace(*label)
			}
			out = st.Forwards[i]
			found = true
		}
	})
	if err != nil {
		return store.PortForward{}, err
	}
	if !found {
		return store.PortForward{}, errors.New("规则不存在")
	}
	s.syncForwards()
	return out, nil
}

// RemoveForward 删除规则。
func (s *Service) RemoveForward(id string) error {
	found := false
	err := s.Store.Update(func(st *store.State) {
		out := make([]store.PortForward, 0, len(st.Forwards))
		for _, f := range st.Forwards {
			if f.ID == id {
				found = true
				continue
			}
			out = append(out, f)
		}
		st.Forwards = out
	})
	if err != nil {
		return err
	}
	if !found {
		return errors.New("规则不存在")
	}
	s.Fwd.Stop(id)
	return nil
}

// UpdateProxy 更新出网代理配置。
func (s *Service) UpdateProxy(p store.Proxy) (store.Proxy, error) {
	switch p.Mode {
	case "off", "system", "manual":
	default:
		return store.Proxy{}, errors.New("代理模式仅支持 off/system/manual")
	}
	if p.Mode == "manual" && p.HTTP == "" && p.HTTPS == "" {
		return store.Proxy{}, errors.New("手动模式需要至少填写一个代理地址")
	}
	err := s.Store.Update(func(st *store.State) {
		st.Proxy = p
		s.mirrorActiveEnv(st)
	})
	if err != nil {
		return store.Proxy{}, err
	}
	_ = s.WriteShellEnv()
	return p, nil
}

// ConnectionResult 是一次连通性探测结果。
type ConnectionResult struct {
	OK      bool   `json:"ok"`
	Target  string `json:"target"`
	Latency int64  `json:"latencyMs"`
	Error   string `json:"error,omitempty"`
}

// TestConnection 探测主机/端口可达性。
func (s *Service) TestConnection(host string, port int) ConnectionResult {
	host = strings.TrimSpace(host)
	if host == "" {
		host = "127.0.0.1"
	}
	if port <= 0 {
		return ConnectionResult{OK: false, Target: host, Error: "端口无效"}
	}
	target := net.JoinHostPort(host, strconv.Itoa(port))
	start := time.Now()
	conn, err := net.DialTimeout("tcp", target, 3*time.Second)
	if err != nil {
		return ConnectionResult{OK: false, Target: target, Error: err.Error()}
	}
	conn.Close()
	return ConnectionResult{OK: true, Target: target, Latency: time.Since(start).Milliseconds()}
}

// ToggleNetwork 开关工作站出网。
// 开启时会立即过一次闸门：检查不通过就自动回滚为关闭，避免「看起来开了其实全断」。
func (s *Service) ToggleNetwork(enabled bool) map[string]any {
	if !enabled {
		s.Gate.Invalidate("用户关闭工作站出网")
	}
	_ = s.Store.Update(func(st *store.State) {
		st.Settings.NetworkEnabled = enabled
		s.mirrorActiveEnv(st)
	})
	_ = s.WriteShellEnv()
	if enabled {
		s.CheckEgressNow()
	}
	return s.NetworkInfo()
}

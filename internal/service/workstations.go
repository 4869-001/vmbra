package service

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"anywhere/internal/store"
)

// WorkstationInput 是新建工作站的参数。
type WorkstationInput struct {
	Name     string `json:"name"`
	Image    string `json:"image"`
	CPUs     int    `json:"cpus"`
	MemoryMB int    `json:"memoryMB"`
	DiskGB   int    `json:"diskGB"`
	Network  bool   `json:"network"`
	Lang     string `json:"lang"`
	TimeZone string `json:"timeZone"`
	UseVPS   bool   `json:"useVps"`
}

// Workstations 返回全部工作站与当前激活项。
func (s *Service) Workstations() map[string]any {
	st := s.Store.Copy()
	active := st.Current()
	tunnelUp := s.VPSTunnel != nil && s.VPSTunnel.Running()
	rows := make([]map[string]any, 0, len(st.Workstations))
	for _, ws := range st.Workstations {
		rows = append(rows, map[string]any{
			"id":           ws.ID,
			"name":         ws.Name,
			"state":        ws.State,
			"image":        ws.Image,
			"cpus":         ws.CPUs,
			"memoryMB":     ws.MemoryMB,
			"diskGB":       ws.DiskGB,
			"network":      ws.Network,
			"lang":         ws.Lang,
			"timeZone":     ws.TimeZone,
			"proxyMode":    ws.ProxyMode,
			"useVps":       ws.UseVPS,
			"root":         s.Cfg.WorkstationRootDir(ws.Root),
			"workspace":    filepath.Join(s.Cfg.WorkstationRootDir(ws.Root), "workspace"),
			"active":       ws.ID == active.ID,
			"createdAt":    ws.CreatedAt,
			"message":      ws.Message,
			"networkLabel": egressLabel(ws.Network, ws.UseVPS, tunnelUp),
		})
	}
	return map[string]any{"workstations": rows, "activeId": active.ID}
}

func egressLabel(network, useVPS, tunnelUp bool) string {
	switch {
	case !network:
		return "不出网"
	case useVPS && tunnelUp:
		return "经 VPS 出口"
	case useVPS:
		return "经 VPS 出口（隧道未启动）"
	default:
		return "宿主直连"
	}
}

// CreateWorkstation 新增一套隔离环境：独立根目录 + 独立运行参数。
func (s *Service) CreateWorkstation(in WorkstationInput) (map[string]any, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, errors.New("请填写工作站名称")
	}
	st := s.Store.Copy()
	base := st.Current()

	id := newWorkstationID(name)
	ws := store.Workstation{
		ID:        id,
		Name:      name,
		Root:      id,
		State:     "stopped",
		Driver:    "local",
		Image:     firstNonEmpty(strings.TrimSpace(in.Image), base.Image, "debian-12-arm64"),
		CPUs:      clamp(in.CPUs, 1, 32, base.CPUs),
		MemoryMB:  clamp(in.MemoryMB, 512, 65536, base.MemoryMB),
		DiskGB:    clamp(in.DiskGB, 5, 512, base.DiskGB),
		Network:   in.Network,
		Lang:      firstNonEmpty(strings.TrimSpace(in.Lang), base.Lang, "en_US.UTF-8"),
		TimeZone:  firstNonEmpty(strings.TrimSpace(in.TimeZone), base.TimeZone, "America/Los_Angeles"),
		ProxyMode: firstNonEmpty(base.ProxyMode, "system"),
		UseVPS:    in.UseVPS,
		CreatedAt: time.Now().UTC(),
		Message:   "新建完成，等待启动",
	}
	if err := os.MkdirAll(s.Cfg.WorkstationRootDir(ws.Root), 0o700); err != nil {
		return nil, err
	}
	if err := s.Store.Update(func(st *store.State) {
		st.Workstations = append(st.Workstations, ws)
	}); err != nil {
		return nil, err
	}
	return s.Workstations(), nil
}

// ActivateWorkstation 切换当前工作站：停旧环境 → 换根目录 → 应用该环境参数。
func (s *Service) ActivateWorkstation(id string) (map[string]any, error) {
	st := s.Store.Copy()
	target, ok := st.Find(id)
	if !ok {
		return nil, errors.New("工作站不存在")
	}
	if st.Current().ID == id {
		return s.Workstations(), nil
	}
	if st.Current().State == "running" {
		if _, err := s.StopWorkstation(); err != nil {
			return nil, err
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	if err := s.Drv.Rebind(s.Cfg.WorkstationRootDir(target.Root), home); err != nil {
		return nil, fmt.Errorf("切换工作站失败：%w", err)
	}
	if err := s.Store.Update(func(st *store.State) {
		st.ActiveID = id
		s.applyWorkstationEnv(st, target)
	}); err != nil {
		return nil, err
	}
	_ = s.WriteShellEnv()
	s.syncForwards()
	return s.Workstations(), nil
}

// UpdateWorkstation 修改某台工作站的环境参数（名称、出网、语言、时区、出口）。
func (s *Service) UpdateWorkstation(id string, patch map[string]any) (map[string]any, error) {
	found := false
	err := s.Store.Update(func(st *store.State) {
		for i := range st.Workstations {
			ws := &st.Workstations[i]
			if ws.ID != id {
				continue
			}
			found = true
			if v, ok := patch["name"].(string); ok && strings.TrimSpace(v) != "" {
				ws.Name = strings.TrimSpace(v)
			}
			if v, ok := patch["image"].(string); ok && strings.TrimSpace(v) != "" {
				ws.Image = strings.TrimSpace(v)
			}
			if v, ok := patch["cpus"].(float64); ok && v >= 1 && v <= 32 {
				ws.CPUs = int(v)
			}
			if v, ok := patch["memoryMB"].(float64); ok && v >= 512 && v <= 65536 {
				ws.MemoryMB = int(v)
			}
			if v, ok := patch["diskGB"].(float64); ok && v >= 5 && v <= 512 {
				ws.DiskGB = int(v)
			}
			if v, ok := patch["network"].(bool); ok {
				ws.Network = v
			}
			if v, ok := patch["useVps"].(bool); ok {
				ws.UseVPS = v
			}
			if v, ok := patch["lang"].(string); ok && strings.TrimSpace(v) != "" {
				ws.Lang = strings.TrimSpace(v)
			}
			if v, ok := patch["timeZone"].(string); ok && strings.TrimSpace(v) != "" {
				ws.TimeZone = strings.TrimSpace(v)
			}
			// 正在使用的环境，改动立刻作用于当前会话环境。
			if st.ActiveID == ws.ID {
				s.applyWorkstationEnv(st, *ws)
			}
		}
	})
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, errors.New("工作站不存在")
	}
	_ = s.WriteShellEnv()
	return s.Workstations(), nil
}

// DeleteWorkstation 删除工作站定义（工作区文件保留在磁盘上）。
func (s *Service) DeleteWorkstation(id string) (map[string]any, error) {
	st := s.Store.Copy()
	if len(st.Workstations) <= 1 {
		return nil, errors.New("至少需要保留一台工作站")
	}
	if _, ok := st.Find(id); !ok {
		return nil, errors.New("工作站不存在")
	}
	if st.Current().ID == id {
		var next string
		for _, ws := range st.Workstations {
			if ws.ID != id {
				next = ws.ID
				break
			}
		}
		if _, err := s.ActivateWorkstation(next); err != nil {
			return nil, err
		}
	}
	if err := s.Store.Update(func(st *store.State) {
		kept := make([]store.Workstation, 0, len(st.Workstations))
		for _, ws := range st.Workstations {
			if ws.ID != id {
				kept = append(kept, ws)
			}
		}
		st.Workstations = kept
	}); err != nil {
		return nil, err
	}
	return s.Workstations(), nil
}

// applyWorkstationEnv 把工作站记录里的环境参数同步到全局设置。
// 下游（终端、脚本、任务、浏览器、代理）只读全局设置，避免到处判断「当前是哪台」。
func (s *Service) applyWorkstationEnv(st *store.State, ws store.Workstation) {
	st.Settings.NetworkEnabled = ws.Network
	if ws.Lang != "" {
		st.Locale.Lang = ws.Lang
	}
	if ws.TimeZone != "" {
		st.Locale.Timezone = ws.TimeZone
	}
	if ws.ProxyMode != "" {
		st.Proxy.Mode = ws.ProxyMode
	}
	st.Browser.UseVPSEgress = ws.UseVPS
}

// mirrorActiveEnv 把全局设置的改动写回当前工作站，保证切换回来时参数不丢。
func (s *Service) mirrorActiveEnv(st *store.State) {
	ws := st.Active()
	if ws == nil {
		return
	}
	ws.Network = st.Settings.NetworkEnabled
	ws.Lang = st.Locale.Lang
	ws.TimeZone = st.Locale.Timezone
	ws.ProxyMode = st.Proxy.Mode
	ws.UseVPS = st.Browser.UseVPSEgress
}

// newWorkstationID 生成稳定可读的目录名：名称的 ASCII 片段 + 随机后缀。
func newWorkstationID(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteByte('-')
		}
	}
	slug := strings.Trim(b.String(), "-")
	if len(slug) > 16 {
		slug = slug[:16]
	}
	buf := make([]byte, 3)
	_, _ = rand.Read(buf)
	suffix := hex.EncodeToString(buf)
	if slug == "" {
		return "ws-" + suffix
	}
	return slug + "-" + suffix
}

func clamp(v, min, max, fallback int) int {
	if v == 0 {
		if fallback > 0 {
			return fallback
		}
		return min
	}
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

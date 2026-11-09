package service

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"anywhere/internal/config"
	"anywhere/internal/store"
)

var envKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

// SettingsInfo 返回设置页数据。
func (s *Service) SettingsInfo() map[string]any {
	st := s.Store.Copy()
	tokenTail := ""
	if b, err := os.ReadFile(s.Cfg.TokenPath()); err == nil {
		t := strings.TrimSpace(string(b))
		if len(t) > 6 {
			tokenTail = "…" + t[len(t)-6:]
		}
	}
	return map[string]any{
		"settings": st.Settings,
		"daemon": map[string]any{
			"version":   config.Version,
			"listen":    s.Cfg.Listen,
			"dataDir":   s.Cfg.DataDir,
			"token":     tokenTail,
			"uptime":    humanDuration(time.Since(s.startedAt)),
			"startedAt": s.startedAt.UTC(),
			"pid":       os.Getpid(),
		},
		"storage": s.StorageInfo(),
		"engine":  detectBackends(),
	}
}

// UpdateSettings 更新控制台开关。
func (s *Service) UpdateSettings(patch map[string]any) (store.Settings, error) {
	boolField := func(k string) (*bool, bool) {
		if v, ok := patch[k]; ok {
			if b, ok2 := v.(bool); ok2 {
				return &b, true
			}
		}
		return nil, false
	}

	err := s.Store.Update(func(st *store.State) {
		if b, ok := boolField("macControlEnabled"); ok {
			st.Settings.MacControlEnabled = *b
			if !*b {
				st.Settings.AllowMacRunCommand = false
			}
		}
		if b, ok := boolField("allowMacRunCommand"); ok {
			if *b && !st.Settings.MacControlEnabled {
				// 依赖主开关，自动联动开启
				st.Settings.MacControlEnabled = true
			}
			st.Settings.AllowMacRunCommand = *b
		}
		if b, ok := boolField("autoStart"); ok {
			st.Settings.AutoStart = *b
		}
		if b, ok := boolField("networkEnabled"); ok {
			st.Settings.NetworkEnabled = *b
		}
		if v, ok := patch["theme"].(string); ok && (v == "light" || v == "dark") {
			st.Settings.Theme = v
		}
		s.mirrorActiveEnv(st)
	})
	if err != nil {
		return store.Settings{}, err
	}
	// 出网/代理变化会改变终端与脚本环境，立即刷新共享环境文件。
	_ = s.WriteShellEnv()
	return s.Store.Copy().Settings, nil
}

// RotateToken 轮换访问令牌。
func (s *Service) RotateToken() (string, error) {
	return s.Cfg.RotateToken()
}

// EnvInfo 返回环境页数据。
func (s *Service) EnvInfo() map[string]any {
	st := s.Store.Copy()
	return map[string]any{
		"vars":    st.Env,
		"mounts":  st.Mounts,
		"desktop": st.Desktop,
		"workstation": map[string]any{
			"image":    st.Current().Image,
			"cpus":     st.Current().CPUs,
			"memoryMB": st.Current().MemoryMB,
			"diskGB":   st.Current().DiskGB,
			"driver":   st.Current().Driver,
		},
		"images":   []string{"debian-12-arm64", "ubuntu-24.04-arm64", "alpine-3.20-arm64", "fedora-40-arm64"},
		"backends": detectBackends(),
		"policy": map[string]any{
			"noNetwork": s.Drv.PolicyPath(false),
			"network":   s.Drv.PolicyPath(true),
			"root":      s.Drv.Root,
			"workspace": s.Drv.Workspace,
			"home":      s.Drv.Home,
		},
		"platform": fmt.Sprintf("%v", s.Drv.SandboxAvailable()),
	}
}

// UpdateEnvVars 覆盖写入环境变量。
func (s *Service) UpdateEnvVars(vars []store.EnvVar) ([]store.EnvVar, error) {
	if len(vars) > 64 {
		return nil, errors.New("环境变量数量上限为 64")
	}
	seen := map[string]bool{}
	clean := make([]store.EnvVar, 0, len(vars))
	for _, kv := range vars {
		k := strings.TrimSpace(kv.Key)
		if k == "" {
			continue
		}
		if !envKeyPattern.MatchString(k) {
			return nil, fmt.Errorf("非法的变量名：%s", k)
		}
		if reservedEnvKeys[strings.ToUpper(k)] {
			return nil, fmt.Errorf("%s 由「系统语言文字」统一管理，请在该处修改", k)
		}
		if seen[k] {
			return nil, fmt.Errorf("变量名重复：%s", k)
		}
		if len(kv.Value) > 4096 {
			return nil, fmt.Errorf("变量 %s 的值过长（上限 4KB）", k)
		}
		seen[k] = true
		clean = append(clean, store.EnvVar{Key: k, Value: kv.Value})
	}
	if err := s.Store.Update(func(st *store.State) { st.Env = clean }); err != nil {
		return nil, err
	}
	_ = s.WriteShellEnv()
	return clean, nil
}

// UpdateMounts 覆盖写入挂载目录（只读共享给工作站的宿主目录）。
func (s *Service) UpdateMounts(mounts []string) ([]string, error) {
	if len(mounts) > 8 {
		return nil, errors.New("挂载目录上限为 8 个")
	}
	clean := make([]string, 0, len(mounts))
	for _, m := range mounts {
		m = strings.TrimSpace(m)
		if m == "" {
			continue
		}
		if !strings.HasPrefix(m, "/") {
			return nil, fmt.Errorf("挂载目录需为绝对路径：%s", m)
		}
		info, err := os.Stat(m)
		if err != nil || !info.IsDir() {
			return nil, fmt.Errorf("目录不存在：%s", m)
		}
		clean = append(clean, m)
	}
	if err := s.Store.Update(func(st *store.State) { st.Mounts = clean }); err != nil {
		return nil, err
	}
	_ = s.WriteShellEnv()
	return clean, nil
}

// UpdateDesktop 更新图形桌面（VNC）接入配置。
func (s *Service) UpdateDesktop(host string, port int, password, scale string) (store.Desktop, error) {
	host = strings.TrimSpace(host)
	if host != "" {
		if port <= 0 || port > 65535 {
			return store.Desktop{}, errors.New("VNC 端口需在 1-65535 之间")
		}
	}
	switch scale {
	case "", "fit", "100":
	default:
		return store.Desktop{}, errors.New("缩放仅支持 fit / 100")
	}
	if scale == "" {
		scale = "fit"
	}
	var out store.Desktop
	err := s.Store.Update(func(st *store.State) {
		st.Desktop.VNCHost = host
		if host != "" {
			st.Desktop.VNCPort = port
		}
		st.Desktop.Password = password
		st.Desktop.Scale = scale
		if host == "" {
			st.Desktop.Enabled = false
		}
		out = st.Desktop
	})
	if err != nil {
		return store.Desktop{}, err
	}
	// 重新探测可用性，若配置失效则把桌面状态归零
	info := s.DesktopStatus()
	if ok, _ := info["available"].(bool); !ok {
		s.mu.Lock()
		s.desktopState = "stopped"
		s.desktopMsg = ""
		s.mu.Unlock()
	}
	return out, nil
}

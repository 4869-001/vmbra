package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/4869-001/vmbra/internal/store"
	"github.com/4869-001/vmbra/internal/vps"
)

const vpsKeyComment = "vmbra-workstation"

var vpsLangPattern = regexp.MustCompile(`^[a-z]{2,3}(-[A-Z]{2})?$`)

// vpsKeyPath 返回 VPS 专用私钥路径；未显式配置时落在数据目录内。
func (s *Service) vpsKeyPath(cfg store.VPS) string {
	if p := strings.TrimSpace(cfg.KeyPath); p != "" {
		return expandTilde(p)
	}
	return filepath.Join(s.Cfg.DataDir, "vps", "id_ed25519")
}

func expandTilde(p string) string {
	if !strings.HasPrefix(p, "~/") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, p[2:])
}

// EnsureVPSKey 生成（或复用）VPS 专用 SSH 密钥，返回公钥内容。
// 私钥只保存在本机数据目录，权限 0600，绝不经过控制台。
func (s *Service) EnsureVPSKey() (string, error) {
	st := s.Store.Copy()
	keyPath := s.vpsKeyPath(st.VPS)
	pubPath := keyPath + ".pub"
	if b, err := os.ReadFile(pubPath); err == nil {
		if pub := strings.TrimSpace(string(b)); pub != "" {
			return pub, nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		return "", err
	}
	_ = os.Remove(pubPath)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ssh-keygen",
		"-q", "-t", "ed25519", "-N", "", "-C", vpsKeyComment, "-f", keyPath).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("生成 SSH 密钥失败：%v %s", err, strings.TrimSpace(string(out)))
	}
	b, err := os.ReadFile(pubPath)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(st.VPS.KeyPath) == "" {
		_ = s.Store.Update(func(state *store.State) { state.VPS.KeyPath = keyPath })
	}
	return strings.TrimSpace(string(b)), nil
}

// vpsConfig 把持久化配置转换成隧道参数。
func (s *Service) vpsConfig(st store.State) vps.Config {
	cfg := vps.Config{
		Host:           strings.TrimSpace(st.VPS.Host),
		Port:           st.VPS.Port,
		User:           strings.TrimSpace(st.VPS.User),
		KeyPath:        s.vpsKeyPath(st.VPS),
		SOCKSPort:      st.VPS.SOCKSPort,
		GatewayPorts:   st.VPS.GatewayPorts,
		KnownHostsPath: filepath.Join(s.Cfg.DataDir, "vps", "known_hosts"),
	}
	for _, f := range st.VPS.Forwards {
		if !f.Enabled {
			continue
		}
		cfg.RemoteForwards = append(cfg.RemoteForwards, vps.RemoteForward{
			RemotePort: f.RemotePort,
			LocalPort:  f.LocalPort,
		})
	}
	return cfg
}

// VPSInfo 汇总「类 VPS 出入口」的配置、密钥与隧道状态。
func (s *Service) VPSInfo() map[string]any {
	st := s.Store.Copy()
	status := s.VPSTunnel.Status()
	keyPath := s.vpsKeyPath(st.VPS)
	pub := ""
	if b, err := os.ReadFile(keyPath + ".pub"); err == nil {
		pub = strings.TrimSpace(string(b))
	}
	install := ""
	if pub != "" && st.VPS.Host != "" {
		install = fmt.Sprintf(`ssh -p %d %s@%s "mkdir -p ~/.ssh && chmod 700 ~/.ssh && echo '%s' >> ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys"`,
			st.VPS.Port, st.VPS.User, st.VPS.Host, pub)
	}
	return map[string]any{
		"config":         st.VPS,
		"status":         status,
		"keyPath":        keyPath,
		"keyReady":       pub != "",
		"publicKey":      pub,
		"installCommand": install,
		"hint":           "出口经 VPS 的 SOCKS5 隧道离开，入站端口由 VPS 远程转发回落到工作站。远端绑定 0.0.0.0 需要 VPS 的 sshd 开启 GatewayPorts。",
	}
}

// UpdateVPS 更新 VPS 出入口配置。
func (s *Service) UpdateVPS(patch map[string]any) (store.VPS, error) {
	cur := s.Store.Copy().VPS
	next := cur

	if v, ok := patch["enabled"].(bool); ok {
		next.Enabled = v
	}
	if v, ok := patch["host"].(string); ok {
		next.Host = strings.TrimSpace(v)
	}
	if v, ok := patch["user"].(string); ok {
		next.User = strings.TrimSpace(v)
	}
	if v, ok := patch["keyPath"].(string); ok {
		next.KeyPath = strings.TrimSpace(v)
	}
	if v, ok := patch["gatewayPorts"].(bool); ok {
		next.GatewayPorts = v
	}
	if v, ok := patch["lang"].(string); ok {
		next.Lang = strings.TrimSpace(v)
	}
	if v, ok := patch["timeZone"].(string); ok {
		next.TimeZone = strings.TrimSpace(v)
	}
	if v, ok := patch["port"].(float64); ok {
		if v < 1 || v > 65535 {
			return cur, errors.New("SSH 端口需在 1-65535 之间")
		}
		next.Port = int(v)
	}
	if v, ok := patch["socksPort"].(float64); ok {
		if v < 1024 || v > 65535 {
			return cur, errors.New("SOCKS 端口需在 1024-65535 之间")
		}
		next.SOCKSPort = int(v)
	}
	if raw, ok := patch["forwards"].([]any); ok {
		forwards := make([]store.VPSForward, 0, len(raw))
		seen := map[int]bool{}
		for _, item := range raw {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			remote, _ := m["remotePort"].(float64)
			local, _ := m["localPort"].(float64)
			if remote < 1 || remote > 65535 || local < 1 || local > 65535 {
				return cur, errors.New("入站转发端口需在 1-65535 之间")
			}
			if seen[int(remote)] {
				return cur, fmt.Errorf("VPS 端口重复：%d", int(remote))
			}
			seen[int(remote)] = true
			label, _ := m["label"].(string)
			enabled, hasEnabled := m["enabled"].(bool)
			if !hasEnabled {
				enabled = true
			}
			id, _ := m["id"].(string)
			if id == "" {
				id = fmt.Sprintf("vp%04x", int(remote)%0xffff)
			}
			forwards = append(forwards, store.VPSForward{
				ID:         id,
				Label:      strings.TrimSpace(label),
				RemotePort: int(remote),
				LocalPort:  int(local),
				Enabled:    enabled,
			})
		}
		next.Forwards = forwards
	}
	if next.Host != "" && !dnsNamePattern.MatchString(next.Host) {
		return cur, fmt.Errorf("VPS 主机地址不合法：%s", next.Host)
	}
	if next.Lang != "" && !vpsLangPattern.MatchString(next.Lang) {
		return cur, fmt.Errorf("语言代码不合法：%s", next.Lang)
	}
	if next.TimeZone != "" && !validTimezone(next.TimeZone) {
		return cur, fmt.Errorf("时区不存在：%s", next.TimeZone)
	}
	if next.User == "" {
		next.User = "root"
	}
	if next.Port == 0 {
		next.Port = 22
	}
	if next.SOCKSPort == 0 {
		next.SOCKSPort = 1080
	}
	if next.Enabled && next.Host == "" {
		return cur, errors.New("启用前请先填写 VPS 主机地址")
	}
	changed := false
	if err := s.Store.Update(func(st *store.State) {
		st.VPS = next
		changed = syncLocaleFromVPS(st)
	}); err != nil {
		return cur, err
	}
	_ = s.WriteBrowserLauncher() // SOCKS 端口可能变化，桌面脚本需同步
	if changed {
		_ = s.WriteShellEnv()
	}
	return next, nil
}

// StartVPS 建立 SSH 隧道并把出口/入站切到 VPS。
func (s *Service) StartVPS(ctx context.Context) (map[string]any, error) {
	st := s.Store.Copy()
	keyPath := s.vpsKeyPath(st.VPS)
	if _, err := os.Stat(keyPath); err != nil {
		return nil, fmt.Errorf("尚未生成 VPS 密钥，请先在「网络 → VPS 出入口」生成并安装公钥")
	}
	if strings.TrimSpace(st.VPS.Host) == "" {
		return nil, errors.New("请先填写 VPS 主机地址")
	}
	cfg := s.vpsConfig(st)
	if err := s.VPSTunnel.Start(ctx, cfg); err != nil {
		return nil, err
	}
	_ = s.Store.Update(func(state *store.State) { state.VPS.Enabled = true })
	_ = s.WriteShellEnv()
	return s.VPSInfo(), nil
}

// StopVPS 断开隧道并恢复为宿主出入口。
func (s *Service) StopVPS() map[string]any {
	s.VPSTunnel.Stop()
	_ = s.Store.Update(func(state *store.State) { state.VPS.Enabled = false })
	_ = s.WriteShellEnv()
	return s.VPSInfo()
}

// VPSRunning 返回隧道是否已建立。
func (s *Service) VPSRunning() bool { return s.VPSTunnel.Running() }

// TestVPS 用当前密钥做一次 SSH 连通性验证。
func (s *Service) TestVPS(ctx context.Context) (map[string]any, error) {
	st := s.Store.Copy()
	cfg := s.vpsConfig(st)
	if strings.TrimSpace(cfg.Host) == "" {
		return nil, errors.New("请先填写 VPS 主机地址")
	}
	if _, err := os.Stat(cfg.KeyPath); err != nil {
		return nil, errors.New("尚未生成 VPS 密钥，请先生成并安装公钥")
	}
	if cfg.Port == 0 {
		cfg.Port = 22
	}
	// 复用隧道的参数校验，但测试不建立任何转发，只执行一次远端 echo。
	if _, err := vps.BuildArgs(cfg); err != nil {
		return nil, err
	}
	args := []string{
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "UserKnownHostsFile=" + cfg.KnownHostsPath,
		"-p", strconv.Itoa(cfg.Port),
		"-i", cfg.KeyPath,
		"-l", cfg.User,
		cfg.Host,
		"echo vmbra-vps-ok",
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ssh", args...).CombinedOutput()
	text := strings.TrimSpace(string(out))
	res := map[string]any{"ok": err == nil, "output": text}
	if err != nil {
		res["error"] = err.Error()
		return res, nil
	}
	if !strings.Contains(text, "vmbra-vps-ok") {
		res["ok"] = false
		res["error"] = "登录成功但未收到预期回显"
	}
	return res, nil
}

// TestVPSEgress 对比宿主直连出口与 VPS 隧道出口的 IP。
func (s *Service) TestVPSEgress(ctx context.Context) (map[string]any, error) {
	if !s.VPSTunnel.Running() {
		return nil, errors.New("SSH 隧道未运行，请先启动 VPS 出入口")
	}
	socks := s.VPSTunnel.SOCKSAddr()
	hostIP, hostErr := probeEgressIP(ctx, "")
	vpsIP, vpsErr := probeEgressIP(ctx, socks)
	out := map[string]any{
		"hostIP": hostIP,
		"socks":  socks,
	}
	if hostErr != nil {
		out["hostError"] = hostErr.Error()
	}
	if vpsErr != nil {
		out["error"] = vpsErr.Error()
		return out, nil
	}
	out["vpsIP"] = vpsIP
	out["effective"] = vpsIP != "" && vpsIP != hostIP
	return out, nil
}

func probeEgressIP(ctx context.Context, socks string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	args := []string{"-sS", "--max-time", "8"}
	if socks != "" {
		args = append(args, "--socks5-hostname", socks)
	}
	args = append(args, "https://api.ipify.org")
	out, err := exec.CommandContext(ctx, "curl", args...).CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		return "", fmt.Errorf("%v %s", err, text)
	}
	if ip := net.ParseIP(text); ip == nil {
		return "", fmt.Errorf("出口探测返回了非 IP 内容：%s", text)
	}
	return text, nil
}

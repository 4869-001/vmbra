package service

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/4869-001/vmbra/internal/config"
	"github.com/4869-001/vmbra/internal/sandbox"
)

// Check 是一项诊断结果。
type Check struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"` // ok | warn | fail
	Detail string `json:"detail"`
	Hint   string `json:"hint,omitempty"`
}

// Diagnostics 执行一组只读自检。
func (s *Service) Diagnostics(ctx context.Context) map[string]any {
	checks := []Check{}
	now := time.Now()

	state := s.Store.Copy()
	ws := state.Current()
	checks = append(checks, Check{
		ID: "daemon", Title: "管理服务",
		Status: "ok",
		Detail: fmt.Sprintf("v%s · pid %d · 已运行 %s", config.Version, os.Getpid(), humanDuration(time.Since(s.startedAt))),
	})

	// 令牌权限
	if info, err := os.Stat(s.Cfg.TokenPath()); err != nil {
		checks = append(checks, Check{ID: "token", Title: "访问令牌", Status: "fail", Detail: "令牌文件不可读：" + err.Error(), Hint: "删除 ~/.vmbra/token 后重启服务可重新生成"})
	} else if perm := info.Mode().Perm(); perm&0o077 != 0 {
		checks = append(checks, Check{ID: "token", Title: "访问令牌", Status: "warn", Detail: "令牌文件权限过于宽松：" + perm.String(), Hint: "执行 chmod 600 ~/.vmbra/token"})
	} else {
		checks = append(checks, Check{ID: "token", Title: "访问令牌", Status: "ok", Detail: "权限 0600，仅当前用户可读"})
	}

	// 隔离引擎
	if s.Drv.SandboxAvailable() {
		checks = append(checks, Check{ID: "sandbox", Title: "策略隔离引擎", Status: "ok", Detail: "sandbox-exec 策略生效，写入被限制在工作站目录内"})
	} else {
		checks = append(checks, Check{ID: "sandbox", Title: "策略隔离引擎", Status: "fail", Detail: s.Drv.SandboxError(), Hint: "建议安装 Lima 虚拟机后端以获得强隔离"})
	}

	// 策略文件
	if b, err := os.ReadFile(s.Drv.PolicyPath(ws.State == "running")); err == nil && len(b) > 0 {
		checks = append(checks, Check{ID: "policy", Title: "隔离策略文件", Status: "ok", Detail: fmt.Sprintf("%d 字节，位于 %s", len(b), filepath.Base(s.Drv.PolicyPath(false)))})
	} else {
		checks = append(checks, Check{ID: "policy", Title: "隔离策略文件", Status: "warn", Detail: "策略文件缺失，将于下次启动时重建"})
	}

	// 隔离边界：在真实策略里跑一遍能力探测。
	// 引擎项只能证明 sandbox-exec 能起进程，边界是否与设计一致必须实测。
	for _, cap := range s.Drv.Capabilities(ctx, state.Settings.NetworkEnabled) {
		status := "ok"
		hint := ""
		switch {
		case cap.OK:
		case cap.Fatal:
			status = "fail"
			hint = "隔离边界已被破坏，先停用出网并在「环境 → 隔离策略」核对策略文件，确认无误后再继续使用工作站"
		case cap.Expected == "allow":
			status = "warn"
			hint = "策略拒绝了下述能力：出网关闭时工作站内无法监听/访问回环，端口转发目标与 DNS 转发器因此不可达"
		default:
			status = "warn"
			hint = "实测比设计更宽松，请确认是否符合预期"
		}
		checks = append(checks, Check{
			ID:     "boundary-" + cap.ID,
			Title:  "隔离边界 · " + cap.Title,
			Status: status,
			Detail: fmt.Sprintf("期望 %s / 实测 %s · %s", cap.Expected, cap.Actual, cap.Detail),
			Hint:   hint,
		})
	}

	// 工作区可写
	probe := filepath.Join(s.Drv.Workspace, ".diag-probe")
	if err := os.WriteFile(probe, []byte(strconv.FormatInt(now.UnixNano(), 10)), 0o600); err != nil {
		checks = append(checks, Check{ID: "workspace", Title: "工作区可写", Status: "fail", Detail: err.Error()})
	} else {
		_ = os.Remove(probe)
		checks = append(checks, Check{ID: "workspace", Title: "工作区可写", Status: "ok", Detail: s.Drv.Workspace})
	}

	// PTY
	if _, err := os.Stat("/dev/ptmx"); err != nil {
		checks = append(checks, Check{ID: "pty", Title: "终端 PTY", Status: "fail", Detail: "系统缺少 /dev/ptmx，终端不可用"})
	} else {
		checks = append(checks, Check{ID: "pty", Title: "终端 PTY", Status: "ok", Detail: fmt.Sprintf("可用，当前会话数 %d", s.Drv.SessionCount())})
	}

	// 系统语言文字
	st := state
	if strings.Contains(strings.ToUpper(st.Locale.Encoding), "UTF-8") {
		checks = append(checks, Check{ID: "locale", Title: "系统语言文字", Status: "ok",
			Detail: fmt.Sprintf("%s · %s · 终端 UTF-8 生效", st.Locale.Lang, st.Locale.Timezone)})
	} else {
		checks = append(checks, Check{ID: "locale", Title: "系统语言文字", Status: "warn",
			Detail: "编码不是 UTF-8：" + st.Locale.Encoding, Hint: "在「环境 → 系统语言文字」改为 UTF-8，避免中文乱码"})
	}

	// Web 终端环境
	termEnv := s.SystemEnv(st)
	checks = append(checks, Check{ID: "webterm", Title: "Web 终端环境", Status: "ok",
		Detail: fmt.Sprintf("%s · LANG=%s · TZ=%s · 会话 %d",
			termEnv["TERM"], termEnv["LANG"], termEnv["TZ"], s.Drv.SessionCount())})

	// DNS 转发器
	dnsStats := s.DNS.Stats()
	switch {
	case !st.DNS.Enabled:
		checks = append(checks, Check{ID: "dnsproxy", Title: "DNS 转发器", Status: "warn",
			Detail: "已关闭，终端沿用宿主机解析器", Hint: "在「网络 → DNS」中启用可固定解析出口"})
	case dnsStats.Running:
		checks = append(checks, Check{ID: "dnsproxy", Title: "DNS 转发器", Status: "ok",
			Detail: fmt.Sprintf("%s → %s", dnsStats.Addr, strings.Join(st.DNS.Servers, ", "))})
	default:
		detail := "监听失败：" + dnsStats.LastError
		checks = append(checks, Check{ID: "dnsproxy", Title: "DNS 转发器", Status: "fail", Detail: detail,
			Hint: "端口可能被占用，可在「网络 → DNS」更换端口"})
	}

	// DNS 解析
	dctx, dcancel := context.WithTimeout(ctx, 3*time.Second)
	resolveResults, _ := s.TestDNS(dctx, "example.com", 3*time.Second)
	dcancel()
	if len(resolveResults) > 0 && resolveResults[0].OK {
		checks = append(checks, Check{ID: "dns", Title: "域名解析", Status: "ok",
			Detail: fmt.Sprintf("example.com → %s（%dms，系统解析器）", strings.Join(shortList(resolveResults[0].Addresses), ", "), resolveResults[0].MS)})
	} else {
		detail := "系统解析器无法解析 example.com"
		if len(resolveResults) > 0 && resolveResults[0].Error != "" {
			detail += "：" + resolveResults[0].Error
		}
		checks = append(checks, Check{ID: "dns", Title: "域名解析", Status: "warn", Detail: detail,
			Hint: "工作站出网关闭时属正常现象；需要联网时在「网络」中开启出网"})
	}

	// 磁盘
	if free, total, err := diskUsage(s.Cfg.DataDir); err == nil {
		status := "ok"
		hint := ""
		if free < 2<<30 {
			status = "warn"
			hint = "剩余空间不足 2GB，快照与构建可能失败"
		}
		checks = append(checks, Check{ID: "disk", Title: "磁盘空间", Status: status,
			Detail: fmt.Sprintf("可用 %.1f GB / 共 %.1f GB", float64(free)/(1<<30), float64(total)/(1<<30)), Hint: hint})
	}

	// 隔离后端
	backends := detectBackends()
	names := []string{}
	for _, b := range backends {
		if b.Available {
			names = append(names, b.Name)
		}
	}
	checks = append(checks, Check{ID: "backend", Title: "隔离后端", Status: "ok",
		Detail: "已启用：" + strings.Join(names, "、"),
		Hint:   "安装 lima 可获得完整 Linux 虚拟机隔离与图形桌面"})

	// 图形桌面
	desktop := s.DesktopStatus()
	if ok, _ := desktop["available"].(bool); ok {
		checks = append(checks, Check{ID: "desktop", Title: "图形桌面", Status: "ok", Detail: "VNC 服务可达"})
	} else {
		checks = append(checks, Check{ID: "desktop", Title: "图形桌面", Status: "warn", Detail: fmt.Sprint(desktop["reason"])})
	}

	// 屏幕录制权限
	mac := s.MacStatus()
	perm := fmt.Sprint(mac["screenPermission"])
	switch perm {
	case "ok":
		checks = append(checks, Check{ID: "screen", Title: "屏幕录制权限", Status: "ok", Detail: "已授权，可查看 Mac 屏幕"})
	case "denied":
		checks = append(checks, Check{ID: "screen", Title: "屏幕录制权限", Status: "fail", Detail: "截屏内容为空", Hint: "系统设置 → 隐私与安全性 → 屏幕录制，勾选终端/管理服务"})
	default:
		checks = append(checks, Check{ID: "screen", Title: "屏幕录制权限", Status: "warn", Detail: "尚未测试", Hint: "在「应用 → 操控 Mac」中点击刷新以检测"})
	}

	// 端口转发冲突
	conflicts := []string{}
	active := []string{}
	for _, f := range st.Forwards {
		if !f.Enabled {
			continue
		}
		if s.Fwd.Running(f.ID) {
			active = append(active, fmt.Sprintf("%d→%d", f.HostPort, f.GuestPort))
			continue
		}
		if isPortListening(f.HostPort) {
			conflicts = append(conflicts, strconv.Itoa(f.HostPort))
		}
	}
	switch {
	case len(conflicts) > 0:
		checks = append(checks, Check{ID: "forwards", Title: "端口转发", Status: "warn",
			Detail: "以下主机端口已被其他进程占用：" + strings.Join(conflicts, "、"), Hint: "调整端口或停用冲突规则"})
	case len(active) > 0:
		checks = append(checks, Check{ID: "forwards", Title: "端口转发", Status: "ok",
			Detail: "监听中：" + strings.Join(active, "、")})
	default:
		checks = append(checks, Check{ID: "forwards", Title: "端口转发", Status: "ok", Detail: "无启用规则"})
	}

	// 出网能力
	start := time.Now()
	proxyNote := "直连"
	switch {
	case !st.Settings.NetworkEnabled:
		proxyNote = "工作站出网关闭"
	case st.Proxy.Mode == "manual":
		proxyNote = "代理 " + firstNonEmpty(st.Proxy.HTTPS, st.Proxy.HTTP)
	case st.Proxy.Mode == "system":
		proxyNote = "继承宿主机代理"
	}
	if conn, err := net.DialTimeout("tcp", "1.1.1.1:443", 2*time.Second); err == nil {
		conn.Close()
		checks = append(checks, Check{ID: "egress", Title: "出网链路", Status: "ok",
			Detail: fmt.Sprintf("宿主可达，延迟 %dms · 工作站：%s", time.Since(start).Milliseconds(), proxyNote)})
	} else {
		checks = append(checks, Check{ID: "egress", Title: "出网链路", Status: "warn", Detail: "无法连接公网：" + err.Error()})
	}

	// VPS 出入口（仅在配置后检查）
	if st.VPS.Host != "" {
		status := s.VPSTunnel.Status()
		switch {
		case s.VPSTunnel.Running():
			checks = append(checks, Check{ID: "vps", Title: "VPS 出入口", Status: "ok",
				Detail: fmt.Sprintf("%s · SOCKS5 %s · 出口经 VPS", st.VPS.Host, status.SOCKSAddr)})
		case st.VPS.Enabled:
			detail := "已启用但隧道未运行"
			if status.Error != "" {
				detail += "：" + status.Error
			}
			checks = append(checks, Check{ID: "vps", Title: "VPS 出入口", Status: "fail", Detail: detail,
				Hint: "在「网络 → VPS 出入口」重新启动，或检查 VPS 上的 SSH 公钥"})
		default:
			checks = append(checks, Check{ID: "vps", Title: "VPS 出入口", Status: "warn",
				Detail: fmt.Sprintf("已配置 %s 但未启用，出口仍是宿主 IP", st.VPS.Host),
				Hint:   "需要独立公网出口时在「网络」页启动 VPS 出入口"})
		}
	}

	ok, warn, fail := 0, 0, 0
	for _, c := range checks {
		switch c.Status {
		case "ok":
			ok++
		case "warn":
			warn++
		case "fail":
			fail++
		}
	}
	return map[string]any{
		"generatedAt": now.UTC(),
		"summary":     map[string]int{"ok": ok, "warn": warn, "fail": fail},
		"checks":      checks,
		"runtime":     runtime.GOOS + "/" + runtime.GOARCH,
		"version":     config.Version,
	}
}

// DiagnosticsBundle 打包一份诊断包（状态 + 自检 + 最近任务日志）。
func (s *Service) DiagnosticsBundle(ctx context.Context) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	diag := s.Diagnostics(ctx)
	diagJSON, _ := json.MarshalIndent(diag, "", "  ")
	if err := writeZip(zw, "diagnostics.json", diagJSON); err != nil {
		return nil, err
	}

	st := s.Store.Copy()
	stateJSON, _ := json.MarshalIndent(map[string]any{
		"workstation": st.Current(),
		"settings":    st.Settings,
		"proxy":       st.Proxy,
		"locale":      st.Locale,
		"dns":         st.DNS,
		"dnsRecent":   s.DNS.Recent(),
		"desktop":     st.Desktop,
		"snapshots":   st.Snapshots,
		"env":         st.Env,
		"audit":       st.Audit,
	}, "", "  ")
	if err := writeZip(zw, "state.json", stateJSON); err != nil {
		return nil, err
	}

	for _, t := range s.Tasks.List() {
		if len(t.ID) == 0 {
			continue
		}
		logPath := filepath.Join(s.Cfg.LogDir(), t.ID+".log")
		if b, err := os.ReadFile(logPath); err == nil {
			_ = writeZip(zw, "tasks/"+t.ID+"-"+sanitize(t.Kind)+".log", b)
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeZip(zw *zip.Writer, name string, data []byte) error {
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == ' ' {
			return '-'
		}
		return r
	}, s)
}

func shortList(list []string) []string {
	if len(list) > 3 {
		return list[:3]
	}
	return list
}

func firstNonEmpty(list ...string) string {
	for _, v := range list {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func humanDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%d 秒", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%d 分 %d 秒", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%d 时 %d 分", int(d.Hours()), int(d.Minutes())%60)
}

func diskUsage(path string) (free, total uint64, err error) {
	var stat syscall.Statfs_t
	if err = syscall.Statfs(path, &stat); err != nil {
		return 0, 0, err
	}
	return stat.Bavail * uint64(stat.Bsize), stat.Blocks * uint64(stat.Bsize), nil
}

func isPortListening(port int) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 300*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

var _ = sandbox.DirSize

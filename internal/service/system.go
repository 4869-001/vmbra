package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/4869-001/vmbra/internal/config"
	"github.com/4869-001/vmbra/internal/sandbox"
	"github.com/4869-001/vmbra/internal/store"
)

// supportedLocales 是控制台提供的语言/文字选项，全部为 UTF-8，保证中文不乱码。
var supportedLocales = []string{
	"zh_CN.UTF-8", "zh_TW.UTF-8", "en_US.UTF-8", "ja_JP.UTF-8",
	"ko_KR.UTF-8", "de_DE.UTF-8", "fr_FR.UTF-8", "ru_RU.UTF-8",
}

// reservedEnvKeys 由系统层托管，不允许通过自定义环境变量覆盖。
var reservedEnvKeys = map[string]bool{
	"LANG": true, "LC_ALL": true, "LC_CTYPE": true, "LC_MESSAGES": true,
	"LC_COLLATE": true, "LANGUAGE": true, "TZ": true, "TERM": true,
	"COLORTERM": true, "TERM_PROGRAM": true, "HOME": true, "PATH": true,
}

var localePattern = regexp.MustCompile(`^[a-z]{2,3}(_[A-Za-z]{2,8})?(\.[A-Za-z0-9-]+)?$`)
var dnsNamePattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$`)

// SystemInfo 返回工作站系统层（语言文字、DNS、终端）的完整视图。
func (s *Service) SystemInfo() map[string]any {
	st := s.Store.Copy()
	locale := st.Locale
	env := s.SystemEnv(st)
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	preview := make([]map[string]string, 0, len(keys))
	for _, k := range keys {
		preview = append(preview, map[string]string{"key": k, "value": env[k]})
	}
	stats := s.DNS.Stats()
	return map[string]any{
		"locale":        locale,
		"locales":       supportedLocales,
		"dns":           st.DNS,
		"dnsStats":      stats,
		"dnsRecent":     s.DNS.Recent(),
		"dnsError":      s.dnsError(),
		"env":           preview,
		"shellEnvPath":  filepath.Join(s.Drv.Home, ".vmbra.env"),
		"hostDNS":       hostResolvers(),
		"terminal":      s.TerminalInfo(st),
		"network":       st.Settings.NetworkEnabled,
		"egressSummary": s.egressSummary(st),
	}
}

func (s *Service) dnsError() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dnsErr
}

// TerminalInfo 描述 Web 终端的会话环境。
func (s *Service) TerminalInfo(st store.State) map[string]any {
	return map[string]any{
		"term":      "xterm-256color",
		"colorterm": "truecolor",
		"lang":      st.Locale.Lang,
		"tz":        st.Locale.Timezone,
		"encoding":  st.Locale.Encoding,
		"dns":       s.dnsEndpoint(st),
		"proxy":     s.proxySummary(st),
	}
}

func (s *Service) dnsEndpoint(st store.State) string {
	if !st.DNS.Enabled {
		return ""
	}
	stats := s.DNS.Stats()
	if !stats.Running {
		return ""
	}
	return stats.Addr
}

func (s *Service) proxySummary(st store.State) string {
	if !st.Settings.NetworkEnabled {
		return ""
	}
	if st.VPS.Enabled && s.VPSTunnel.Running() {
		return "VPS SOCKS5 · " + st.VPS.Host
	}
	switch st.Proxy.Mode {
	case "manual":
		if st.Proxy.HTTPS != "" {
			return st.Proxy.HTTPS
		}
		return st.Proxy.HTTP
	case "system":
		return "继承宿主机代理环境变量"
	}
	return ""
}

func (s *Service) egressSummary(st store.State) map[string]any {
	out := map[string]any{
		"enabled": st.Settings.NetworkEnabled,
		"proxy":   s.proxySummary(st),
		"dns":     s.dnsEndpoint(st),
		"policy":  s.Drv.PolicyPath(st.Settings.NetworkEnabled),
	}
	if st.VPS.Enabled && s.VPSTunnel.Running() {
		out["vps"] = map[string]any{
			"host":  st.VPS.Host,
			"socks": s.VPSTunnel.SOCKSAddr(),
		}
	}
	return out
}

// SystemEnv 组合工作站的统一运行时环境：语言、时区、终端、DNS 与代理。
// 终端、脚本、任务与诊断全部复用这一份，保证「同一出入口」。
func (s *Service) SystemEnv(st store.State) map[string]string {
	lang := st.Locale.Lang
	if lang == "" {
		lang = "en_US.UTF-8"
	}
	base := lang
	if idx := strings.Index(lang, "."); idx > 0 {
		base = lang[:idx]
	}
	env := map[string]string{
		"LANG":                 lang,
		"LC_ALL":               lang,
		"LC_CTYPE":             lang,
		"LC_MESSAGES":          lang,
		"LC_COLLATE":           lang,
		"LANGUAGE":             base,
		"TZ":                   st.Locale.Timezone,
		"TERM":                 "xterm-256color",
		"COLORTERM":            "truecolor",
		"TERM_PROGRAM":         "Vmbra",
		"TERM_PROGRAM_VERSION": config.Version,
		"VMBRA":                "1",
		"VMBRA_WORKSTATION":    st.Current().Name,
		"VMBRA_ENCODING":       st.Locale.Encoding,
	}
	if dns := s.dnsEndpoint(st); dns != "" {
		env["VMBRA_DNS_SERVER"] = dns
		env["RES_OPTIONS"] = "timeout:2 attempts:2"
	}
	if len(st.DNS.Servers) > 0 {
		env["VMBRA_DNS_UPSTREAM"] = strings.Join(st.DNS.Servers, " ")
	}
	for _, kv := range st.Env {
		key := strings.TrimSpace(kv.Key)
		if key == "" || reservedEnvKeys[strings.ToUpper(key)] {
			continue // 语言/时区/终端由系统层统一管理，避免用户变量覆盖
		}
		env[key] = kv.Value
	}
	if len(st.Mounts) > 0 {
		env["VMBRA_MOUNTS"] = strings.Join(st.Mounts, ":")
	}
	if st.Settings.NetworkEnabled {
		switch st.Proxy.Mode {
		case "manual":
			if st.Proxy.HTTP != "" {
				env["HTTP_PROXY"], env["http_proxy"] = st.Proxy.HTTP, st.Proxy.HTTP
			}
			if st.Proxy.HTTPS != "" {
				env["HTTPS_PROXY"], env["https_proxy"] = st.Proxy.HTTPS, st.Proxy.HTTPS
				env["ALL_PROXY"], env["all_proxy"] = st.Proxy.HTTPS, st.Proxy.HTTPS
			}
			if st.Proxy.NoProxy != "" {
				env["NO_PROXY"], env["no_proxy"] = st.Proxy.NoProxy, st.Proxy.NoProxy
			}
		case "system":
			for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY"} {
				if v := os.Getenv(k); v != "" {
					env[k] = v
					env[strings.ToLower(k)] = v
				}
			}
		}
		// VPS 隧道开启时，出口统一走本地 SOCKS5：ALL_PROXY 覆盖宿主代理，
		// 显式配置的 HTTP(S)_PROXY 仍然保留（部分工具只认这两个变量）。
		if st.VPS.Enabled && s.VPSTunnel.Running() {
			if addr := s.VPSTunnel.SOCKSAddr(); addr != "" {
				uri := "socks5h://" + addr
				env["ALL_PROXY"], env["all_proxy"] = uri, uri
				if _, ok := env["NO_PROXY"]; !ok {
					env["NO_PROXY"] = "localhost,127.0.0.1,::1"
					env["no_proxy"] = env["NO_PROXY"]
				}
				env["VMBRA_VPS_EXIT"] = st.VPS.Host
			}
		}
		// Clash 内核运行时，它才是唯一出口（与 VPS 隧道互斥，取后者更明确的配置）。
		if st.ProxyCore.Running && st.ProxyCore.SOCKSProxy != "" {
			env["ALL_PROXY"], env["all_proxy"] = st.ProxyCore.SOCKSProxy, st.ProxyCore.SOCKSProxy
			if st.ProxyCore.HTTPProxy != "" {
				env["HTTP_PROXY"], env["http_proxy"] = st.ProxyCore.HTTPProxy, st.ProxyCore.HTTPProxy
				env["HTTPS_PROXY"], env["https_proxy"] = st.ProxyCore.HTTPProxy, st.ProxyCore.HTTPProxy
			}
			env["NO_PROXY"], env["no_proxy"] = "localhost,127.0.0.1,::1", "localhost,127.0.0.1,::1"
			env["VMBRA_PROXY_NODE"] = st.ProxyCore.NodeName
		}
	}
	return env
}

// WriteShellEnv 把统一环境落到沙箱家目录，终端与脚本共用同一份导出。
func (s *Service) WriteShellEnv() error {
	st := s.Store.Copy()
	env := s.SystemEnv(st)
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("# 由 Vmbra WorkStation 自动生成，修改设置后会被覆盖\n")
	for _, k := range keys {
		b.WriteString("export " + k + "=" + shellQuote(env[k]) + "\n")
	}
	path := filepath.Join(s.Drv.Home, ".vmbra.env")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return err
	}
	return nil
}

func shellQuote(v string) string {
	return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
}

// localeLangFromTag 把 en-US 这类 BCP-47 标签转换成 POSIX locale（en_US.UTF-8）。
func localeLangFromTag(tag string) string {
	v := strings.ReplaceAll(strings.TrimSpace(tag), "-", "_")
	if v == "" {
		return "en_US.UTF-8"
	}
	if !strings.Contains(v, ".") {
		v += ".UTF-8"
	}
	return v
}

// syncLocaleFromVPS 在「跟随 VPS」开启时把环境语言/时区对齐 VPS 地区，
// 与宿主 Mac 的设置完全无关。返回是否发生了变更。
func syncLocaleFromVPS(st *store.State) bool {
	if !st.Locale.FollowVPS || strings.TrimSpace(st.VPS.Host) == "" {
		return false
	}
	lang := localeLangFromTag(st.VPS.Lang)
	tz := strings.TrimSpace(st.VPS.TimeZone)
	if tz == "" {
		tz = "UTC"
	}
	if st.Locale.Lang == lang && st.Locale.Timezone == tz {
		return false
	}
	st.Locale.Lang = lang
	st.Locale.Timezone = tz
	return true
}

// syncVPSLocale 立即把 Locale 对齐 VPS 地区并刷新统一环境文件。
func (s *Service) syncVPSLocale() {
	changed := false
	if err := s.Store.Update(func(st *store.State) { changed = syncLocaleFromVPS(st) }); err != nil {
		return
	}
	if changed {
		_ = s.WriteShellEnv()
	}
}

// UpdateLocale 更新系统语言文字、编码与时区，并立即刷新终端环境。
func (s *Service) UpdateLocale(patch map[string]any) (store.Locale, error) {
	cur := s.Store.Copy().Locale
	next := cur
	if v, ok := patch["followVps"].(bool); ok {
		next.FollowVPS = v
	}
	if v, ok := patch["lang"].(string); ok {
		v = strings.TrimSpace(v)
		if v != "" {
			if !localePattern.MatchString(v) {
				return cur, fmt.Errorf("语言格式不合法：%s", v)
			}
			if !strings.Contains(v, ".") {
				v += ".UTF-8"
			}
			next.Lang = v
		}
	}
	if v, ok := patch["encoding"].(string); ok && strings.TrimSpace(v) != "" {
		enc := strings.ToUpper(strings.TrimSpace(v))
		if enc != "UTF-8" {
			return cur, fmt.Errorf("仅支持 UTF-8 编码，以保证中文与多语言不乱码")
		}
		next.Encoding = enc
	}
	if v, ok := patch["timezone"].(string); ok && strings.TrimSpace(v) != "" {
		tz := strings.TrimSpace(v)
		if !validTimezone(tz) {
			return cur, fmt.Errorf("时区不存在：%s", tz)
		}
		next.Timezone = tz
	}
	if next.FollowVPS {
		vps := s.Store.Copy().VPS
		if strings.TrimSpace(vps.Host) == "" {
			return cur, errors.New("尚未配置 VPS 主机，无法跟随 VPS 地区")
		}
		next.Lang = localeLangFromTag(vps.Lang)
		next.Timezone = vps.TimeZone
		if next.Timezone == "" {
			next.Timezone = "UTC"
		}
	}
	if next.Encoding == "" {
		next.Encoding = "UTF-8"
	}
	if err := s.Store.Update(func(st *store.State) {
		st.Locale = next
		s.mirrorActiveEnv(st)
	}); err != nil {
		return next, err
	}
	_ = s.WriteShellEnv()
	return next, nil
}

var tzPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_+-]*(/[A-Za-z0-9_+-]+)*$`)

func validTimezone(tz string) bool {
	if tz == "UTC" || tz == "GMT" {
		return true
	}
	if !tzPattern.MatchString(tz) || strings.Contains(tz, "..") {
		return false
	}
	_, err := os.Stat(filepath.Join("/usr/share/zoneinfo", tz))
	return err == nil
}

// UpdateDNS 更新 DNS 配置；自定义模式下本地转发器立即切换到新的上游。
func (s *Service) UpdateDNS(patch map[string]any) (store.DNS, error) {
	cur := s.Store.Copy().DNS
	next := cur
	if v, ok := patch["enabled"].(bool); ok {
		next.Enabled = v
	}
	if v, ok := patch["mode"].(string); ok {
		v = strings.TrimSpace(v)
		if v != "system" && v != "custom" {
			return cur, fmt.Errorf("DNS 模式只能是 system 或 custom")
		}
		next.Mode = v
	}
	if raw, ok := patch["servers"].([]any); ok {
		servers := make([]string, 0, len(raw))
		for _, item := range raw {
			v, _ := item.(string)
			v = strings.TrimSpace(v)
			if v == "" {
				continue
			}
			normalized, err := normalizeDNSServer(v)
			if err != nil {
				return cur, err
			}
			servers = append(servers, normalized)
		}
		if len(servers) > 4 {
			return cur, fmt.Errorf("最多配置 4 个上游 DNS")
		}
		next.Servers = servers
	}
	if raw, ok := patch["search"].([]any); ok {
		search := make([]string, 0, len(raw))
		for _, item := range raw {
			v, _ := item.(string)
			v = strings.Trim(strings.TrimSpace(v), ".")
			if v == "" {
				continue
			}
			if !dnsNamePattern.MatchString(v) {
				return cur, fmt.Errorf("搜索域不合法：%s", v)
			}
			search = append(search, v)
		}
		next.Search = search
	}
	if v, ok := patch["port"].(float64); ok {
		if v < 1024 || v > 65535 {
			return cur, fmt.Errorf("转发器端口需在 1024-65535 之间")
		}
		next.Port = int(v)
	}
	if next.Mode == "custom" && len(next.Servers) == 0 {
		return cur, fmt.Errorf("自定义 DNS 模式需要至少一个上游服务器")
	}
	if err := s.Store.Update(func(st *store.State) { st.DNS = next }); err != nil {
		return next, err
	}
	s.syncDNS(next)
	_ = s.WriteShellEnv()
	return next, nil
}

func normalizeDNSServer(v string) (string, error) {
	host, port, err := net.SplitHostPort(v)
	if err != nil {
		host, port = v, "53"
	}
	if ip := net.ParseIP(host); ip == nil {
		if !dnsNamePattern.MatchString(host) {
			return "", fmt.Errorf("DNS 服务器不合法：%s", v)
		}
	}
	if port == "" {
		port = "53"
	}
	return net.JoinHostPort(host, port), nil
}

// StartDNSProxy 依据当前配置启动/停止本地转发器。
func (s *Service) StartDNSProxy() {
	s.syncDNS(s.Store.Copy().DNS)
}

func (s *Service) syncDNS(cfg store.DNS) {
	s.DNS.SetUpstreams(cfg.Servers)
	if !cfg.Enabled {
		s.DNS.Stop()
		s.setDNSError("")
		return
	}
	if err := s.DNS.Start(cfg.Port); err != nil {
		// 端口被占用（例如同时运行了多个实例）时回退到系统分配的空闲端口，
		// 保证终端仍有一条可用的解析出口。
		if fallbackErr := s.DNS.Start(0); fallbackErr != nil {
			s.setDNSError(err.Error())
			return
		}
		s.setDNSError("")
		return
	}
	s.setDNSError("")
}

func (s *Service) setDNSError(msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dnsErr = msg
}

// DNSResult 是单个解析器的探测结果。
type DNSResult struct {
	Resolver  string   `json:"resolver"`
	Kind      string   `json:"kind"` // system | upstream | proxy
	OK        bool     `json:"ok"`
	MS        int64    `json:"ms"`
	Addresses []string `json:"addresses,omitempty"`
	Error     string   `json:"error,omitempty"`
}

// TestDNS 用系统解析器、上游服务器与本地转发器分别解析同一个域名。
func (s *Service) TestDNS(ctx context.Context, name string, timeout time.Duration) ([]DNSResult, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("请填写要解析的域名")
	}
	if timeout <= 0 || timeout > 10*time.Second {
		timeout = 4 * time.Second
	}
	st := s.Store.Copy()
	results := []DNSResult{lookup(ctx, "system", "system", net.DefaultResolver, name, timeout)}

	for _, server := range st.DNS.Servers {
		upstream, err := normalizeDNSServer(server)
		if err != nil {
			continue
		}
		r := &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
				d := net.Dialer{Timeout: timeout}
				return d.DialContext(ctx, "udp", upstream)
			},
		}
		results = append(results, lookup(ctx, upstream, "upstream", r, name, timeout))
	}
	if addr := s.dnsEndpoint(st); addr != "" {
		r := &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
				d := net.Dialer{Timeout: timeout}
				return d.DialContext(ctx, "udp", addr)
			},
		}
		results = append(results, lookup(ctx, addr+"（本地转发器）", "proxy", r, name, timeout))
	}
	return results, nil
}

func lookup(ctx context.Context, label, kind string, r *net.Resolver, name string, timeout time.Duration) DNSResult {
	start := time.Now()
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	addrs, err := r.LookupHost(cctx, name)
	res := DNSResult{Resolver: label, Kind: kind, MS: time.Since(start).Milliseconds()}
	if err != nil {
		res.Error = err.Error()
		return res
	}
	res.OK = true
	res.Addresses = addrs
	return res
}

// EgressResult 是一次出口探测的结果。
type EgressResult struct {
	IP        string `json:"ip"`
	Location  string `json:"location,omitempty"`
	LatencyMS int64  `json:"latencyMs"`
	DNSServer string `json:"dnsServer,omitempty"`
	Proxy     string `json:"proxy,omitempty"`
	Error     string `json:"error,omitempty"`
}

// Egress 在隔离环境里探测实际出口 IP 与解析链路，验证「同一出入口」。
func (s *Service) Egress(ctx context.Context) (EgressResult, error) {
	st := s.Store.Copy()
	if !st.Settings.NetworkEnabled {
		return EgressResult{}, fmt.Errorf("%w：工作站出网未开启", ErrConflict)
	}
	res := EgressResult{
		DNSServer: s.dnsEndpoint(st),
		Proxy:     s.proxySummary(st),
	}
	var out bytes.Buffer
	start := time.Now()
	r := s.Drv.Exec(ctx, sandbox.ExecRequest{
		Argv: []string{"/bin/sh", "-c", `
D=$(mktemp -d)
(curl -sS --max-time 4 https://api.ipify.org >"$D/ip" 2>/dev/null || true) &
(curl -sS --max-time 4 https://www.cloudflare.com/cdn-cgi/trace >"$D/trace" 2>/dev/null || true) &
wait
echo "ip=$(head -c 64 "$D/ip" 2>/dev/null | tr -d '\n')"
grep -E '^(ip|loc)=' "$D/trace" 2>/dev/null | head -2 || true
rm -rf "$D"
`},
		Env:     s.SystemEnv(st),
		Network: true,
		Timeout: 12 * time.Second,
		Stdout:  &out,
		Stderr:  &out,
		Dir:     s.Drv.Workspace,
	})
	res.LatencyMS = time.Since(start).Milliseconds()
	if r.Err != nil || r.ExitCode != 0 {
		detail := strings.TrimSpace(out.String())
		res.Error = "探测失败：" + detail
		if detail == "" {
			detail = fmt.Sprintf("退出码 %d", r.ExitCode)
		}
		return res, fmt.Errorf("出口探测失败：%s", detail)
	}
	text := out.String()
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, "ip="); ok {
			res.IP = v
		}
		if v, ok := strings.CutPrefix(line, "loc="); ok {
			res.Location = v
		}
	}
	if res.IP == "" {
		res.IP = strings.TrimSpace(text)
	}
	if res.IP == "" {
		return res, fmt.Errorf("未能取得出口 IP")
	}
	return res, nil
}

// hostResolvers 读取宿主机当前生效的 DNS 服务器（macOS 用 scutil，其他平台回退 /etc/resolv.conf）。
func hostResolvers() []string {
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, "scutil", "--dns")
	if out, err := cmd.Output(); err == nil {
		set := map[string]bool{}
		list := []string{}
		for _, line := range strings.Split(string(out), "\n") {
			idx := strings.Index(line, "nameserver[")
			if idx < 0 {
				continue
			}
			parts := strings.SplitN(line, ":", 2)
			if len(parts) != 2 {
				continue
			}
			ip := strings.TrimSpace(parts[1])
			if ip == "" || set[ip] {
				continue
			}
			set[ip] = true
			list = append(list, ip)
		}
		if len(list) > 0 {
			return list
		}
	}
	b, err := os.ReadFile("/etc/resolv.conf")
	if err != nil {
		return []string{}
	}
	list := []string{}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "nameserver"); ok {
			list = append(list, strings.TrimSpace(v))
		}
	}
	return list
}

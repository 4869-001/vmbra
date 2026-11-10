package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/4869-001/vmbra/internal/browser"
	"github.com/4869-001/vmbra/internal/store"
)

// browserProfileDir 返回受控浏览器的独立配置目录。
func (s *Service) browserProfileDir(cfg store.Browser) string {
	if p := strings.TrimSpace(cfg.ProfileDir); p != "" {
		return expandTilde(p)
	}
	return filepath.Join(s.Cfg.DataDir, "browser", strings.ToLower(strings.ReplaceAll(cfg.Lang, "-", "_")))
}

// WriteBrowserLauncher 生成 ~/.vmbra/bin/chrome-us，供桌面图标或脚本直接调用，
// 逻辑与「应用 → 受控浏览器」完全一致。
func (s *Service) WriteBrowserLauncher() error {
	st := s.Store.Copy()
	cfg := st.Browser
	profile := s.browserProfileDir(cfg)
	chrome, err := browser.FindChrome()
	if err != nil {
		return err
	}
	dir := filepath.Join(s.Cfg.DataDir, "bin")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, "chrome-us")
	_, _ = browser.WriteStartPage(profile, cfg.Lang, cfg.TimeZone)
	script := fmt.Sprintf(`#!/bin/bash
# 由 Vmbra 自动生成，请勿手改：受控浏览器 %[1]s · %[2]s
set -e
CHROME=%[3]s
PROFILE=%[4]s
mkdir -p "$PROFILE/Default"
/usr/bin/python3 - "$PROFILE/Default/Preferences" %[5]s <<'PY'
import json, sys
path, accept = sys.argv[1], sys.argv[2]
try:
    data = json.load(open(path))
except Exception:
    data = {}
intl = data.get("intl") or {}
intl["accept_languages"] = accept
intl["app_locale"] = accept.split(",")[0]
data["intl"] = intl
open(path, "w").write(json.dumps(data))
PY
ARGS=(--user-data-dir="$PROFILE" --lang=%[1]s --accept-lang=%[5]s --no-first-run --no-default-browser-check --new-window)
PROXY_ARGS=(--proxy-bypass-list="localhost;127.0.0.1;[::1];<local>" "--host-resolver-rules=MAP * ~NOTFOUND , EXCLUDE localhost" --disable-quic "--force-webrtc-ip-handling-policy=disable_non_proxied_udp" --dns-prefetch-disable)
if [ %[8]d -gt 0 ] && /usr/bin/nc -z 127.0.0.1 %[8]d >/dev/null 2>&1; then
  ARGS+=(--proxy-server="http://127.0.0.1:%[8]d" "${PROXY_ARGS[@]}")
elif [ %[6]s = "1" ] && /usr/bin/nc -z 127.0.0.1 %[7]d >/dev/null 2>&1; then
  ARGS+=(--proxy-server="socks5://127.0.0.1:%[7]d" "${PROXY_ARGS[@]}")
fi
if ! /usr/bin/pgrep -f -- "--user-data-dir=$PROFILE" >/dev/null 2>&1 && [ -f "$PROFILE/start.html" ]; then
  ARGS+=("file://$PROFILE/start.html")
fi
TZ=%[2]s exec "$CHROME" "${ARGS[@]}"
`,
		shellQuote(cfg.Lang),
		shellQuote(cfg.TimeZone),
		shellQuote(chrome),
		shellQuote(profile),
		shellQuote(browser.AcceptLanguage(cfg.Lang)),
		boolFlag(cfg.UseVPSEgress),
		st.VPS.SOCKSPort,
		s.clashBrowserPort(),
	)
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		return err
	}
	return nil
}

// clashBrowserPort 返回内核给浏览器开的免认证端口；内核未运行时为 0。
func (s *Service) clashBrowserPort() int {
	if !s.Clash.Running() {
		return 0
	}
	u, err := url.Parse(s.Clash.BrowserProxy())
	if err != nil {
		return 0
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		return 0
	}
	return port
}

// browserProxyURL 决定浏览器走哪条出口：Clash 内核优先，其次 VPS 隧道。
func (s *Service) browserProxyURL(st store.State) (string, string) {
	if s.Clash.Running() {
		if p := s.Clash.BrowserProxy(); p != "" {
			return p, ""
		}
	}
	if st.Browser.UseVPSEgress && s.VPSTunnel.Running() {
		return "socks5://" + s.VPSTunnel.SOCKSAddr(), ""
	}
	if st.Browser.UseVPSEgress {
		return "", "VPS 隧道未运行：本次浏览器走宿主网络，时区与语言已按配置固定"
	}
	return "", ""
}

func boolFlag(v bool) string {
	if v {
		return "1"
	}
	return "0"
}

// BrowserStatus 汇总受控浏览器的可用性、出口与配置。
func (s *Service) BrowserStatus() map[string]any {
	st := s.Store.Copy()
	profile := s.browserProfileDir(st.Browser)
	proxy, _ := s.browserProxyURL(st)
	out := map[string]any{
		"config":         st.Browser,
		"profile":        profile,
		"running":        browser.Running(profile),
		"proxy":          proxy,
		"acceptLanguage": browser.AcceptLanguage(st.Browser.Lang),
		"vps": map[string]any{
			"running": s.VPSTunnel.Running(),
			"socks":   s.VPSTunnel.SOCKSAddr(),
		},
	}
	if chrome, err := browser.FindChrome(); err != nil {
		out["available"] = false
		out["reason"] = err.Error()
	} else {
		out["available"] = true
		out["chrome"] = chrome
	}
	return out
}

// UpdateBrowser 更新受控浏览器配置。
func (s *Service) UpdateBrowser(patch map[string]any) (store.Browser, error) {
	cur := s.Store.Copy().Browser
	next := cur
	if v, ok := patch["lang"].(string); ok {
		next.Lang = strings.TrimSpace(v)
	}
	if v, ok := patch["timeZone"].(string); ok {
		next.TimeZone = strings.TrimSpace(v)
	}
	if v, ok := patch["useVpsEgress"].(bool); ok {
		next.UseVPSEgress = v
	}
	if v, ok := patch["profileDir"].(string); ok {
		next.ProfileDir = strings.TrimSpace(v)
	}
	cfg := browser.Config{
		Lang:       next.Lang,
		TimeZone:   next.TimeZone,
		ProfileDir: s.browserProfileDir(next),
	}
	if err := browser.Validate(cfg); err != nil {
		return cur, err
	}
	if err := s.Store.Update(func(st *store.State) { st.Browser = next }); err != nil {
		return cur, err
	}
	_ = s.WriteBrowserLauncher()
	return next, nil
}

// VerifyBrowser 用 headless Chrome 验证语言与时区是否真正生效。
func (s *Service) VerifyBrowser(ctx context.Context) (map[string]any, error) {
	st := s.Store.Copy()
	cfg := browser.Config{
		Lang:       st.Browser.Lang,
		TimeZone:   st.Browser.TimeZone,
		ProfileDir: s.browserProfileDir(st.Browser),
	}
	res, err := browser.Verify(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"ok":         res.OK,
		"expected":   map[string]string{"lang": st.Browser.Lang, "timeZone": st.Browser.TimeZone},
		"actual":     res,
		"acceptLang": browser.AcceptLanguage(st.Browser.Lang),
	}, nil
}

// OpenBrowser 以固定语言/时区启动独立 profile 的 Chrome；出口可跟随 VPS。
// target 为空时打开内置起始页，否则在同一个受控 profile 里打开该地址。
// 隧道未运行时仍允许启动（语言/时区已固定），但会返回 warning 而不是静默降级。
func (s *Service) OpenBrowser(target string) (map[string]any, error) {
	target = strings.TrimSpace(target)
	if target != "" && !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
		return nil, errors.New("只支持 http/https 地址")
	}
	st := s.Store.Copy()
	proxy, warn := s.browserProxyURL(st)
	profile := s.browserProfileDir(st.Browser)
	res, err := browser.Launch(browser.Config{
		Lang:       st.Browser.Lang,
		TimeZone:   st.Browser.TimeZone,
		ProxyURL:   proxy,
		ProfileDir: profile,
		OpenURL:    target,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"ok":             true,
		"pid":            res.PID,
		"alreadyRunning": res.PID == 0,
		"profile":        res.Profile,
		"lang":           res.Lang,
		"timeZone":       res.TimeZone,
		"proxy":          res.Proxy,
		"url":            target,
		"warning":        warn,
		"status":         s.BrowserStatus(),
	}, nil
}

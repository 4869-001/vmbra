// Package browser 以固定语言、时区和独立 profile 启动 Chrome。
//
// 为什么不能直接复用系统 Chrome：
//   - 系统 Chrome 通过 LaunchServices 启动，拿不到调用方的 TZ，时区始终跟随 macOS 系统设置；
//   - 界面语言与 Accept-Language 存在 profile 里，命令行 --lang 只影响界面语言；
//   - 日常 profile 已经在运行，改配置也不会重启。
//
// 因此这里用独立的 user-data-dir 直接执行 Chrome 二进制，启动前写入 intl 偏好，
// 并通过环境变量覆盖时区。日常 Chrome 不受影响，两者可以并存。
package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var (
	langPattern = regexp.MustCompile(`^[a-z]{2,3}(-[A-Z]{2})?$`)
	tzPattern   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_+-]*(/[A-Za-z0-9_+-]+)*$`)
)

// Config 是一次启动所需的参数。
type Config struct {
	Lang       string
	TimeZone   string
	ProxyURL   string // 为空表示直连
	ProfileDir string
	OpenURL    string // 启动时打开的页面；为空则用内置起始页
}

// LaunchResult 描述一次启动结果。
type LaunchResult struct {
	PID      int      `json:"pid"`
	Chrome   string   `json:"chrome"`
	Profile  string   `json:"profile"`
	Args     []string `json:"args"`
	Lang     string   `json:"lang"`
	TimeZone string   `json:"timeZone"`
	Proxy    string   `json:"proxy,omitempty"`
}

// FindChrome 返回可用的 Chromium 系浏览器可执行文件。
func FindChrome() (string, error) {
	home, _ := os.UserHomeDir()
	candidates := []string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		filepath.Join(home, "Applications/Google Chrome.app/Contents/MacOS/Google Chrome"),
		"/Applications/Chromium.app/Contents/MacOS/Chromium",
		"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		"/Applications/Google Chrome Beta.app/Contents/MacOS/Google Chrome Beta",
	}
	for _, path := range candidates {
		if st, err := os.Stat(path); err == nil && !st.IsDir() {
			return path, nil
		}
	}
	return "", errors.New("未找到 Chrome / Chromium / Edge，请先安装 Chrome")
}

// AcceptLanguage 由语言推导 Accept-Language，例如 en-US → en-US,en。
func AcceptLanguage(lang string) string {
	if lang == "" {
		return ""
	}
	if i := strings.Index(lang, "-"); i > 0 {
		return lang + "," + lang[:i]
	}
	return lang
}

// Validate 校验语言、时区与代理地址。
func Validate(cfg Config) error {
	if !langPattern.MatchString(cfg.Lang) {
		return fmt.Errorf("语言代码不合法：%q", cfg.Lang)
	}
	if !tzPattern.MatchString(cfg.TimeZone) {
		return fmt.Errorf("时区不合法：%q", cfg.TimeZone)
	}
	if strings.TrimSpace(cfg.ProfileDir) == "" {
		return errors.New("浏览器 profile 目录不能为空")
	}
	if cfg.ProxyURL != "" {
		u, err := url.Parse(cfg.ProxyURL)
		if err != nil || u.Host == "" {
			return fmt.Errorf("代理地址不合法：%q", cfg.ProxyURL)
		}
		switch u.Scheme {
		case "socks5", "socks5h", "http", "https":
		default:
			return fmt.Errorf("不支持的代理协议：%q", u.Scheme)
		}
		if u.Port() == "" {
			return errors.New("代理地址缺少端口")
		}
		if p, err := strconv.Atoi(u.Port()); err != nil || p < 1 || p > 65535 {
			return errors.New("代理端口不合法")
		}
	}
	if cfg.OpenURL != "" {
		u, err := url.Parse(cfg.OpenURL)
		if err != nil || u.Host == "" {
			return fmt.Errorf("打开的地址不合法：%q", cfg.OpenURL)
		}
		switch u.Scheme {
		case "http", "https":
		default:
			return fmt.Errorf("只支持 http/https 地址：%q", cfg.OpenURL)
		}
	}
	return nil
}

// BuildArgs 生成 Chrome 启动参数。
func BuildArgs(cfg Config) ([]string, error) {
	if err := Validate(cfg); err != nil {
		return nil, err
	}
	args := []string{
		"--user-data-dir=" + cfg.ProfileDir,
		"--lang=" + cfg.Lang,
		"--accept-lang=" + AcceptLanguage(cfg.Lang),
		"--no-first-run",
		"--no-default-browser-check",
		"--new-window",
	}
	if cfg.ProxyURL != "" {
		args = append(args,
			"--proxy-server="+cfg.ProxyURL,
			"--proxy-bypass-list=localhost;127.0.0.1;[::1];<local>",
			// 让 DNS 也走代理，避免本地解析暴露真实位置
			"--host-resolver-rules=MAP * ~NOTFOUND , EXCLUDE localhost",
			// QUIC 走 UDP，绕过 HTTP 代理，直接禁掉
			"--disable-quic",
			// WebRTC 不走代理的 UDP 通道同样会泄漏
			"--force-webrtc-ip-handling-policy=disable_non_proxied_udp",
			"--dns-prefetch-disable",
		)
	}
	if cfg.OpenURL != "" {
		args = append(args, cfg.OpenURL)
	}
	return args, nil
}

// EnsurePrefs 把界面语言与 Accept-Language 写入 profile 偏好，保留其它设置。
func EnsurePrefs(profileDir, lang string) error {
	if !langPattern.MatchString(lang) {
		return fmt.Errorf("语言代码不合法：%q", lang)
	}
	dir := filepath.Join(profileDir, "Default")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, "Preferences")

	data := map[string]any{}
	if body, err := os.ReadFile(path); err == nil && len(body) > 0 {
		if err := json.Unmarshal(body, &data); err != nil {
			// 偏好损坏时直接重建，避免把坏 JSON 写回。
			data = map[string]any{}
		}
	}
	intl, _ := data["intl"].(map[string]any)
	if intl == nil {
		intl = map[string]any{}
	}
	intl["accept_languages"] = AcceptLanguage(lang)
	intl["app_locale"] = lang
	data["intl"] = intl

	body, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Running 判断该 profile 是否已有 Chrome 实例在运行。
func Running(profileDir string) bool {
	// pgrep 会把以 -- 开头的模式当成自己的选项，必须先用 -- 结束选项解析，
	// 否则每次都会返回「未运行」，启动逻辑就会重复拉起一个立刻退出的 Chrome。
	out, err := exec.Command("pgrep", "-f", "--", "--user-data-dir="+profileDir).Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) != ""
}

// VerifyResult 是 headless 自检得到的实际语言与时区。
type VerifyResult struct {
	TimeZone  string   `json:"timeZone"`
	Offset    int      `json:"offset"`
	Language  string   `json:"language"`
	Languages []string `json:"languages"`
	OK        bool     `json:"ok"`
}

// Verify 用一次性 profile 启动 headless Chrome，读取实际生效的 Intl 时区与语言。
func Verify(ctx context.Context, cfg Config) (VerifyResult, error) {
	if err := Validate(cfg); err != nil {
		return VerifyResult{}, err
	}
	chrome, err := FindChrome()
	if err != nil {
		return VerifyResult{}, err
	}
	profile, err := os.MkdirTemp("", "vmbra-browser-verify-*")
	if err != nil {
		return VerifyResult{}, err
	}
	defer os.RemoveAll(profile)
	if err := EnsurePrefs(profile, cfg.Lang); err != nil {
		return VerifyResult{}, err
	}

	page := "data:text/html,<body><script>document.body.textContent=[" +
		"Intl.DateTimeFormat().resolvedOptions().timeZone," +
		"new Date().getTimezoneOffset()," +
		"navigator.language," +
		"(navigator.languages||[]).join(',')" +
		"].join('|')</script></body>"
	args := []string{
		"--headless=new",
		"--disable-gpu",
		"--no-first-run",
		"--no-default-browser-check",
		"--user-data-dir=" + profile,
		"--lang=" + cfg.Lang,
		"--accept-lang=" + AcceptLanguage(cfg.Lang),
		"--dump-dom",
		page,
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, chrome, args...)
	cmd.Env = append(os.Environ(), "TZ="+cfg.TimeZone)
	out, err := cmd.Output()
	if err != nil {
		return VerifyResult{}, fmt.Errorf("自检启动失败：%w", err)
	}
	body := string(out)
	if i := strings.Index(body, "<body>"); i >= 0 {
		body = body[i+len("<body>"):]
	}
	if i := strings.Index(body, "</body>"); i >= 0 {
		body = body[:i]
	}
	parts := strings.Split(strings.TrimSpace(body), "|")
	if len(parts) != 4 {
		return VerifyResult{}, fmt.Errorf("自检输出无法解析：%q", strings.TrimSpace(body))
	}
	offset, _ := strconv.Atoi(parts[1])
	res := VerifyResult{
		TimeZone:  parts[0],
		Offset:    offset,
		Language:  parts[2],
		Languages: strings.Split(parts[3], ","),
	}
	res.OK = res.TimeZone == cfg.TimeZone && (res.Language == cfg.Lang || strings.HasPrefix(res.Language, cfg.Lang+"-"))
	return res, nil
}

// WriteStartPage 生成受控浏览器的起始页，用实际 Intl 值自证时区与语言，
// 方便一眼区分「受控窗口」和日常 Chrome。
func WriteStartPage(profileDir, lang, timeZone string) (string, error) {
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		return "", err
	}
	langJSON, _ := json.Marshal(lang)
	tzJSON, _ := json.Marshal(timeZone)
	page := `<!doctype html>
<html lang="en"><head><meta charset="utf-8" />
<title>Vmbra · Controlled Browser</title>
<style>
 :root { color-scheme: dark; }
 body { margin:0; font: 15px/1.6 -apple-system, "SF Pro Text", "Helvetica Neue", sans-serif;
        background:#0f1115; color:#e8eaf0; display:flex; min-height:100vh; align-items:center; justify-content:center; }
 .box { width:min(560px, 88vw); padding:32px 36px; background:#151821; border:1px solid #252a36; border-radius:16px; }
 h1 { margin:0 0 6px; font-size:20px; letter-spacing:.2px; }
 .sub { color:#8b93a7; font-size:13px; margin-bottom:22px; }
 .row { display:flex; justify-content:space-between; padding:9px 0; border-top:1px solid #222836; }
 .row span:first-child { color:#8b93a7; }
 .mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
 .ok { color:#5dd39e; } .bad { color:#ff6b6b; }
 .hint { margin-top:20px; padding:12px 14px; border-radius:10px; background:#10141d; color:#8b93a7; font-size:12.5px; }
</style></head>
<body><div class="box">
 <h1>Vmbra 受控浏览器</h1>
 <div class="sub">独立 profile · 语言与时区按控制台配置固定 · 日常 Chrome 不受影响</div>
 <div class="row"><span>配置时区</span><span class="mono" id="cfg-tz"></span></div>
 <div class="row"><span>实际时区</span><span class="mono" id="tz"></span></div>
 <div class="row"><span>配置语言</span><span class="mono" id="cfg-lang"></span></div>
 <div class="row"><span>实际语言</span><span class="mono" id="lang"></span></div>
 <div class="row"><span>Accept-Language</span><span class="mono" id="langs"></span></div>
 <div class="hint" id="verdict"></div>
 <div class="hint">字体、Canvas、WebGL 等属于操作系统级指纹，本工具不做伪造。macOS 全球版本都内置 PingFang / Heiti / Songti，命中简体中文字体并不等于中国环境。</div>
</div>
<script>
 var CFG_TZ = ` + string(tzJSON) + `, CFG_LANG = ` + string(langJSON) + `;
 var tz = Intl.DateTimeFormat().resolvedOptions().timeZone;
 var langs = (navigator.languages || []).join(", ");
 document.getElementById("cfg-tz").textContent = CFG_TZ;
 document.getElementById("cfg-lang").textContent = CFG_LANG;
 document.getElementById("langs").textContent = langs;
 var tzEl = document.getElementById("tz"), langEl = document.getElementById("lang");
 tzEl.textContent = tz;
 langEl.textContent = navigator.language;
 var tzOK = tz === CFG_TZ, langOK = navigator.language === CFG_LANG;
 tzEl.className = "mono " + (tzOK ? "ok" : "bad");
 langEl.className = "mono " + (langOK ? "ok" : "bad");
 var v = document.getElementById("verdict");
 v.textContent = (tzOK && langOK)
   ? "时区与语言都已生效。此窗口的出口由「网络 → VPS 出入口」决定。"
   : "实际值与配置不一致：请退出该 profile 的全部窗口后重新启动。";
 v.className = "hint " + ((tzOK && langOK) ? "ok" : "bad");
</script>
</body></html>`
	path := filepath.Join(profileDir, "start.html")
	if err := os.WriteFile(path, []byte(page), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// Launch 启动浏览器；profile 已在运行时直接返回，不做重复启动。
func Launch(cfg Config) (LaunchResult, error) {
	chrome, err := FindChrome()
	if err != nil {
		return LaunchResult{}, err
	}
	args, err := BuildArgs(cfg)
	if err != nil {
		return LaunchResult{}, err
	}
	if err := EnsurePrefs(cfg.ProfileDir, cfg.Lang); err != nil {
		return LaunchResult{}, err
	}
	res := LaunchResult{
		Chrome:   chrome,
		Profile:  cfg.ProfileDir,
		Args:     args,
		Lang:     cfg.Lang,
		TimeZone: cfg.TimeZone,
		Proxy:    cfg.ProxyURL,
	}
	startPage, _ := WriteStartPage(cfg.ProfileDir, cfg.Lang, cfg.TimeZone)
	if Running(cfg.ProfileDir) {
		// profile 已在运行时，Chrome 会把新命令转交给已有实例；
		// 带着地址再发一次，让「在 Chrome 中打开」对已开着的窗口同样有效。
		if cfg.OpenURL != "" {
			handoff := exec.Command(chrome, args...)
			handoff.Env = append(os.Environ(), "TZ="+cfg.TimeZone)
			if err := handoff.Start(); err != nil {
				return res, fmt.Errorf("在已运行的 Chrome 中打开页面失败：%w", err)
			}
			// 转交完成后该进程立刻退出，必须回收，否则会留下僵尸进程。
			go func() { _ = handoff.Wait() }()
		}
		return res, nil
	}

	// 没有指定地址时用内置起始页（BuildArgs 已经处理过 OpenURL）。
	if cfg.OpenURL == "" && startPage != "" {
		args = append(args, "file://"+startPage)
		res.Args = args
	}

	cmd := exec.Command(chrome, args...)
	cmd.Env = append(os.Environ(), "TZ="+cfg.TimeZone)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return res, err
	}
	defer devNull.Close()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = devNull, devNull, devNull
	if err := cmd.Start(); err != nil {
		return res, fmt.Errorf("启动 Chrome 失败：%w", err)
	}
	res.PID = cmd.Process.Pid
	_ = cmd.Process.Release()
	return res, nil
}

package browser

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAcceptLanguage(t *testing.T) {
	cases := map[string]string{
		"en-US": "en-US,en",
		"fr":    "fr",
		"":      "",
	}
	for in, want := range cases {
		if got := AcceptLanguage(in); got != want {
			t.Fatalf("AcceptLanguage(%q) = %q，期望 %q", in, got, want)
		}
	}
}

func TestBuildArgs(t *testing.T) {
	cfg := Config{
		Lang:       "en-US",
		TimeZone:   "America/Los_Angeles",
		ProxyURL:   "socks5://127.0.0.1:1080",
		ProfileDir: "/tmp/vmbra-profile",
	}
	args, err := BuildArgs(cfg)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Join(args, " ")
	for _, want := range []string{
		"--user-data-dir=/tmp/vmbra-profile",
		"--lang=en-US",
		"--accept-lang=en-US,en",
		"--proxy-server=socks5://127.0.0.1:1080",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("缺少参数 %q：%s", want, line)
		}
	}
}

func TestBuildArgsRejectsInvalid(t *testing.T) {
	base := Config{Lang: "en-US", TimeZone: "America/Los_Angeles", ProfileDir: "/tmp/x"}
	cases := map[string]func(c *Config){
		"语言含空格":     func(c *Config) { c.Lang = "en US" },
		"时区缺区域":     func(c *Config) { c.TimeZone = "../etc/passwd" },
		"空 profile": func(c *Config) { c.ProfileDir = "" },
		"代理无端口":     func(c *Config) { c.ProxyURL = "socks5://127.0.0.1" },
		"代理协议不支持":   func(c *Config) { c.ProxyURL = "ftp://127.0.0.1:21" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := base
			mutate(&cfg)
			if _, err := BuildArgs(cfg); err == nil {
				t.Fatalf("应当拒绝：%s", name)
			}
		})
	}
}

func TestEnsurePrefsMergesExisting(t *testing.T) {
	dir := t.TempDir()
	prefsPath := filepath.Join(dir, "Default", "Preferences")
	if err := os.MkdirAll(filepath.Dir(prefsPath), 0o700); err != nil {
		t.Fatal(err)
	}
	seed := map[string]any{
		"profile": map[string]any{"name": "Work"},
		"intl":    map[string]any{"accept_languages": "zh-CN,zh", "charset_default": "UTF-8"},
	}
	body, _ := json.Marshal(seed)
	if err := os.WriteFile(prefsPath, body, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := EnsurePrefs(dir, "en-US"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(prefsPath)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	intl, _ := got["intl"].(map[string]any)
	if intl["accept_languages"] != "en-US,en" || intl["app_locale"] != "en-US" {
		t.Fatalf("intl 未更新：%+v", intl)
	}
	if intl["charset_default"] != "UTF-8" {
		t.Fatal("不应覆盖无关的 intl 设置")
	}
	if prof, _ := got["profile"].(map[string]any); prof["name"] != "Work" {
		t.Fatal("不应丢失其它偏好")
	}
}

func TestBuildArgsOpensTargetURL(t *testing.T) {
	base := Config{Lang: "en-US", TimeZone: "America/Los_Angeles", ProfileDir: "/tmp/vmbra-profile"}
	args, err := BuildArgs(base)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(args, " "), "http") {
		t.Fatal("未指定地址时不应带 URL")
	}

	withURL := base
	withURL.OpenURL = "http://127.0.0.1:7788/?token=abc"
	args, err = BuildArgs(withURL)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(args, " "), withURL.OpenURL) {
		t.Fatalf("缺少待打开地址：%v", args)
	}

	// 只允许 http/https，避免把 file:// 或 javascript: 塞进来。
	for _, bad := range []string{"file:///etc/passwd", "javascript:alert(1)", "127.0.0.1:7788"} {
		cfg := base
		cfg.OpenURL = bad
		if _, err := BuildArgs(cfg); err == nil {
			t.Fatalf("应当拒绝地址：%s", bad)
		}
	}
}

func TestRunningDetectsProfile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "profile")
	// 用 shell 起一个带同样命令行标记的进程：pgrep -f 匹配的是完整命令行。
	// 末尾保留一条命令，避免 sh 用 exec 替换自己而丢掉标记。
	cmd := exec.Command("/bin/sh", "-c", "sleep 5; : # --user-data-dir="+dir)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if Running(dir) {
			if Running(filepath.Join(dir, "other")) {
				t.Fatal("不应把别的 profile 判为运行中")
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("未识别到运行中的 profile（pgrep 参数解析可能又退化成选项）")
}

func TestVerifyReportsRealTimeZoneAndLanguage(t *testing.T) {
	if _, err := FindChrome(); err != nil {
		t.Skip("未安装 Chrome，跳过 headless 自检")
	}
	res, err := Verify(context.Background(), Config{
		Lang:       "en-US",
		TimeZone:   "America/Los_Angeles",
		ProfileDir: filepath.Join(t.TempDir(), "profile"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.TimeZone != "America/Los_Angeles" {
		t.Fatalf("时区未生效：%s", res.TimeZone)
	}
	if res.Language != "en-US" || len(res.Languages) == 0 {
		t.Fatalf("语言未生效：%s %v", res.Language, res.Languages)
	}
	if !res.OK {
		t.Fatalf("自检应判定为通过：%+v", res)
	}
}

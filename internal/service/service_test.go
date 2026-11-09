package service

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"anywhere/internal/browser"
	"anywhere/internal/config"
	"anywhere/internal/sandbox"
	"anywhere/internal/store"
	"anywhere/internal/tasks"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("ANYWHERE_DATA_DIR", dir)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(cfg.StatePath())
	if err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	drv, err := sandbox.New(cfg.WorkstationDir(), home)
	if err != nil {
		t.Fatal(err)
	}
	return New(cfg, st, drv, tasks.New(st, cfg.LogDir()))
}

func TestWorkstationLifecycle(t *testing.T) {
	s := newTestService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	info, err := s.StartWorkstation(ctx)
	if err != nil {
		t.Fatalf("启动失败：%v", err)
	}
	if info["state"] != "running" {
		t.Fatalf("状态应为 running，实际 %v", info["state"])
	}
	if _, err := os.Stat(filepath.Join(s.Drv.Home, ".anywhere-init")); err != nil {
		t.Fatalf("初始化标记缺失：%v", err)
	}
	if _, err := os.Stat(filepath.Join(s.Drv.Home, ".zshrc")); err != nil {
		t.Fatalf("shell 配置缺失：%v", err)
	}

	info, err = s.StopWorkstation()
	if err != nil {
		t.Fatalf("停止失败：%v", err)
	}
	if info["state"] != "stopped" {
		t.Fatalf("状态应为 stopped，实际 %v", info["state"])
	}
}

func TestSpecsLockedWhileRunning(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	if _, err := s.StartWorkstation(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateSpecs(ctx, map[string]any{"cpus": float64(8)}); err == nil {
		t.Fatal("运行中修改规格应当失败")
	}
	if _, err := s.StopWorkstation(); err != nil {
		t.Fatal(err)
	}
	info, err := s.UpdateSpecs(ctx, map[string]any{"cpus": float64(8), "memoryMB": float64(8192)})
	if err != nil {
		t.Fatalf("停止后修改规格失败：%v", err)
	}
	if info["cpus"] != 8 {
		t.Fatalf("规格未生效：%v", info["cpus"])
	}
}

func TestMacRunRequiresAllow(t *testing.T) {
	s := newTestService(t)
	// 出厂默认允许执行脚本与命令（无需确认），关闭后才应拒绝。
	if !s.Store.Copy().Settings.AllowMacRunCommand {
		t.Fatal("默认应允许执行命令")
	}
	if _, err := s.UpdateSettings(map[string]any{"allowMacRunCommand": false}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MacRun(context.Background(), MacRunRequest{Command: "echo hi"}); err == nil {
		t.Fatal("关闭允许执行后应被拒绝")
	}
	if _, err := s.UpdateSettings(map[string]any{"allowMacRunCommand": true}); err != nil {
		t.Fatal(err)
	}
	res, err := s.MacRun(context.Background(), MacRunRequest{Command: "echo MAC_OK", TimeoutSec: 10})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 || !strings.Contains(res.Output, "MAC_OK") {
		t.Fatalf("执行结果异常：%+v", res)
	}
	audit := s.MacAudit()
	if len(audit) != 1 || audit[0].Digest == "" {
		t.Fatalf("审计记录异常：%+v", audit)
	}
	if strings.Contains(audit[0].Digest, "MAC_OK") {
		t.Fatal("审计不应包含命令内容")
	}
}

func TestLocaleAppliedToShellEnv(t *testing.T) {
	s := newTestService(t)
	if _, err := s.UpdateLocale(map[string]any{"lang": "zh_CN.UTF-8", "timezone": "Asia/Shanghai"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateLocale(map[string]any{"encoding": "GBK"}); err == nil {
		t.Fatal("非 UTF-8 编码应被拒绝")
	}
	if _, err := s.UpdateLocale(map[string]any{"timezone": "../etc/passwd"}); err == nil {
		t.Fatal("非法时区应被拒绝")
	}
	env := s.SystemEnv(s.Store.Copy())
	if env["LANG"] != "zh_CN.UTF-8" || env["LC_ALL"] != "zh_CN.UTF-8" || env["TZ"] != "Asia/Shanghai" {
		t.Fatalf("语言环境未生效：%v / %v / %v", env["LANG"], env["LC_ALL"], env["TZ"])
	}
	if env["TERM"] != "xterm-256color" || env["COLORTERM"] != "truecolor" {
		t.Fatalf("终端环境不正确：%v / %v", env["TERM"], env["COLORTERM"])
	}
	b, err := os.ReadFile(filepath.Join(s.Drv.Home, ".anywhere.env"))
	if err != nil {
		t.Fatalf("统一环境文件缺失：%v", err)
	}
	if !strings.Contains(string(b), "export LANG='zh_CN.UTF-8'") {
		t.Fatalf("环境文件内容不正确：%s", string(b))
	}
}

func TestDNSConfigAndProxy(t *testing.T) {
	s := newTestService(t)
	if _, err := s.UpdateDNS(map[string]any{"mode": "bogus"}); err == nil {
		t.Fatal("非法模式应被拒绝")
	}
	if _, err := s.UpdateDNS(map[string]any{"mode": "custom", "servers": []any{}}); err == nil {
		t.Fatal("自定义模式缺少上游应被拒绝")
	}
	if _, err := s.UpdateDNS(map[string]any{"servers": []any{"not a host!"}}); err == nil {
		t.Fatal("非法上游地址应被拒绝")
	}
	if _, err := s.UpdateDNS(map[string]any{"port": float64(80)}); err == nil {
		t.Fatal("特权端口应被拒绝")
	}

	port := 15300 + os.Getpid()%400
	cfg, err := s.UpdateDNS(map[string]any{
		"mode":    "custom",
		"servers": []any{"1.1.1.1", "223.5.5.5:53"},
		"port":    float64(port),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Servers) != 2 || !strings.Contains(cfg.Servers[0], ":53") {
		t.Fatalf("上游未规范化：%+v", cfg.Servers)
	}
	stats := s.DNS.Stats()
	if !stats.Running {
		t.Fatalf("转发器未启动：%s", stats.LastError)
	}
	defer s.DNS.Stop()

	results, err := s.TestDNS(context.Background(), "example.com", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]bool{}
	for _, r := range results {
		kinds[r.Kind] = true
	}
	if !kinds["proxy"] {
		t.Fatalf("缺少本地转发器探测：%+v", results)
	}
}

func TestRunScriptAllowedByDefault(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	if _, err := s.StartWorkstation(ctx); err != nil {
		t.Fatal(err)
	}
	defer s.StopWorkstation()

	script := "#!/bin/sh\necho \"LANG=$LANG\"\necho run-ok\nexit 0\n"
	if err := s.Drv.Write("scripts/hello.sh", []byte(script)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunScript(ctx, RunScriptOptions{Path: "../../etc/passwd"}); err == nil {
		t.Fatal("越界路径应被拒绝")
	}
	if _, err := s.RunScript(ctx, RunScriptOptions{Path: "scripts/missing.sh"}); err == nil {
		t.Fatal("不存在的脚本应报错")
	}
	item, err := s.RunScript(ctx, RunScriptOptions{Path: "scripts/hello.sh", TimeoutSec: 30})
	if err != nil {
		t.Fatal(err)
	}
	rec, done := s.Tasks.Wait(item.Record().ID, 15*time.Second)
	if !done || rec.Status != "succeeded" {
		t.Fatalf("脚本执行失败：%+v", rec)
	}
	out, err := s.Tasks.Tail(item.Record().ID, 4000)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "run-ok") || !strings.Contains(out, "LANG=") {
		t.Fatalf("脚本输出缺失：%s", out)
	}
}

func TestEnvVarValidation(t *testing.T) {
	s := newTestService(t)
	if _, err := s.UpdateEnvVars([]store.EnvVar{{Key: "1BAD", Value: "x"}}); err == nil {
		t.Fatal("非法变量名应被拒绝")
	}
	if _, err := s.UpdateEnvVars([]store.EnvVar{{Key: "A", Value: "1"}, {Key: "A", Value: "2"}}); err == nil {
		t.Fatal("重复变量名应被拒绝")
	}
	vars, err := s.UpdateEnvVars([]store.EnvVar{{Key: "DEMO", Value: "1"}})
	if err != nil || len(vars) != 1 {
		t.Fatalf("合法写入失败：%v %+v", err, vars)
	}
}

func TestForwardsFollowWorkstation(t *testing.T) {
	s := newTestService(t)
	guestLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer guestLn.Close()
	go func() {
		for {
			c, err := guestLn.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}(c)
		}
	}()
	guestPort := guestLn.Addr().(*net.TCPAddr).Port

	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	hostPort := probe.Addr().(*net.TCPAddr).Port
	_ = probe.Close()

	if _, err := s.AddForward("udp 测试", hostPort, guestPort, "udp"); err == nil {
		t.Fatal("非 TCP 协议应被拒绝")
	}
	f, err := s.AddForward("本地预览", hostPort, guestPort, "tcp")
	if err != nil {
		t.Fatal(err)
	}
	if s.Fwd.Running(f.ID) {
		t.Fatal("工作站未运行时不应建立监听")
	}

	ctx := context.Background()
	if _, err := s.StartWorkstation(ctx); err != nil {
		t.Fatal(err)
	}
	defer s.StopWorkstation()
	if !s.Fwd.Running(f.ID) {
		t.Fatalf("工作站启动后应监听端口：%+v", s.Fwd.Stats(f.ID))
	}

	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(hostPort)), 2*time.Second)
	if err != nil {
		t.Fatalf("连接转发端口失败：%v", err)
	}
	_, _ = conn.Write([]byte("hello"))
	buf := make([]byte, 5)
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(conn, buf); err != nil || string(buf) != "hello" {
		t.Fatalf("转发链路异常：%v %q", err, string(buf))
	}
	_ = conn.Close()

	if _, err := s.StopWorkstation(); err != nil {
		t.Fatal(err)
	}
	if s.Fwd.Running(f.ID) {
		t.Fatal("工作站停止后应释放监听端口")
	}
}

func TestForwardRules(t *testing.T) {
	s := newTestService(t)
	f, err := s.AddForward("demo", 18080, 80, "tcp")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddForward("bad", 0, 80, "tcp"); err == nil {
		t.Fatal("非法端口应被拒绝")
	}
	disabled := false
	if _, err := s.UpdateForward(f.ID, &disabled, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveForward(f.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveForward(f.ID); err == nil {
		t.Fatal("删除不存在的规则应报错")
	}
}

func TestProxyValidation(t *testing.T) {
	s := newTestService(t)
	if _, err := s.UpdateProxy(store.Proxy{Mode: "manual"}); err == nil {
		t.Fatal("手动模式缺少地址应被拒绝")
	}
	if _, err := s.UpdateProxy(store.Proxy{Mode: "nope"}); err == nil {
		t.Fatal("非法模式应被拒绝")
	}
	if _, err := s.UpdateProxy(store.Proxy{Mode: "off"}); err != nil {
		t.Fatal(err)
	}
}

func TestStatusIncludesSettings(t *testing.T) {
	s := newTestService(t)
	if _, err := s.UpdateSettings(map[string]any{"theme": "dark", "autoStart": true}); err != nil {
		t.Fatal(err)
	}
	raw, ok := s.Status()["settings"]
	if !ok {
		t.Fatal("概览缺少设置快照，控制台无法恢复主题与开关")
	}
	settings, ok := raw.(store.Settings)
	if !ok {
		t.Fatalf("设置快照类型异常：%T", raw)
	}
	if settings.Theme != "dark" || !settings.AutoStart {
		t.Fatalf("设置快照未生效：%+v", settings)
	}
}

func TestWorkspaceExportStreamsSnapshot(t *testing.T) {
	s := newTestService(t)
	if err := s.Drv.Write("notes/hello.txt", []byte("export-ok")); err != nil {
		t.Fatal(err)
	}
	// 导出是只读操作，工作站未运行时也应可用。
	var buf bytes.Buffer
	n, err := s.ExportWorkspace(context.Background(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 || n != int64(buf.Len()) {
		t.Fatalf("导出的字节数不一致：n=%d len=%d", n, buf.Len())
	}

	gz, err := gzip.NewReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("导出内容不是合法的 gzip：%v", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	found := false
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("读取归档失败：%v", err)
		}
		if hdr.Name != "notes/hello.txt" {
			continue
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != "export-ok" {
			t.Fatalf("归档内容不正确：%q", string(body))
		}
		found = true
	}
	if !found {
		t.Fatal("导出归档中缺少 notes/hello.txt")
	}
}

func TestLocaleFollowsVPS(t *testing.T) {
	s := newTestService(t)

	// 没有 VPS 主机时不允许「跟随」。
	if _, err := s.UpdateLocale(map[string]any{"followVps": true}); err == nil {
		t.Fatal("未配置 VPS 时不应允许跟随")
	}

	if _, err := s.UpdateVPS(map[string]any{
		"host": "38.58.108.195", "port": float64(1841), "user": "root",
	}); err != nil {
		t.Fatal(err)
	}
	loc, err := s.UpdateLocale(map[string]any{"followVps": true})
	if err != nil {
		t.Fatal(err)
	}
	if loc.Lang != "en_US.UTF-8" || loc.Timezone != "America/Los_Angeles" {
		t.Fatalf("未按 VPS 地区对齐：%+v", loc)
	}
	env := s.SystemEnv(s.Store.Copy())
	if env["TZ"] != "America/Los_Angeles" || env["LANG"] != "en_US.UTF-8" {
		t.Fatalf("统一环境未生效：TZ=%q LANG=%q", env["TZ"], env["LANG"])
	}

	// VPS 换区后环境应自动跟随。
	if _, err := s.UpdateVPS(map[string]any{"timeZone": "Europe/London", "lang": "en-GB"}); err != nil {
		t.Fatal(err)
	}
	loc = s.Store.Copy().Locale
	if loc.Timezone != "Europe/London" || loc.Lang != "en_GB.UTF-8" {
		t.Fatalf("VPS 换区后未同步：%+v", loc)
	}

	// 关闭跟随后允许手动配置，且保留当前值。
	loc, err = s.UpdateLocale(map[string]any{
		"followVps": false, "lang": "zh_CN.UTF-8", "timezone": "Asia/Taipei",
	})
	if err != nil {
		t.Fatal(err)
	}
	if loc.FollowVPS || loc.Lang != "zh_CN.UTF-8" || loc.Timezone != "Asia/Taipei" {
		t.Fatalf("关闭跟随后手动配置未生效：%+v", loc)
	}
}

func TestBrowserLauncherScript(t *testing.T) {
	if _, err := browser.FindChrome(); err != nil {
		t.Skip("未安装 Chrome，跳过启动脚本测试")
	}
	s := newTestService(t)
	if err := s.WriteBrowserLauncher(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.Cfg.DataDir, "bin", "chrome-us")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	script := string(body)
	for _, want := range []string{"--user-data-dir=", "--lang=", "--accept-lang=", "TZ=", "socks5://127.0.0.1:1080"} {
		if !strings.Contains(script, want) {
			t.Fatalf("启动脚本缺少 %q：\n%s", want, script)
		}
	}
	if out, err := exec.Command("bash", "-n", path).CombinedOutput(); err != nil {
		t.Fatalf("启动脚本语法错误：%v %s", err, out)
	}
}

func TestBrowserConfig(t *testing.T) {
	s := newTestService(t)

	if _, err := s.UpdateBrowser(map[string]any{"timeZone": "../../etc/passwd"}); err == nil {
		t.Fatal("非法时区应被拒绝")
	}
	if _, err := s.UpdateBrowser(map[string]any{"lang": "en US"}); err == nil {
		t.Fatal("非法语言应被拒绝")
	}

	cfg, err := s.UpdateBrowser(map[string]any{"lang": "en-GB", "timeZone": "Europe/London"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Lang != "en-GB" || cfg.TimeZone != "Europe/London" {
		t.Fatalf("浏览器配置未生效：%+v", cfg)
	}
	status := s.BrowserStatus()
	if got, _ := status["acceptLanguage"].(string); got != "en-GB,en" {
		t.Fatalf("Accept-Language 推导错误：%q", got)
	}

	// 默认跟随 VPS 出口；隧道未运行时状态里不应出现代理地址。
	if got, _ := status["proxy"].(string); got != "" {
		t.Fatalf("隧道未运行时不应有代理：%q", got)
	}
}

func TestVPSConfigAndKey(t *testing.T) {
	s := newTestService(t)

	if _, err := s.UpdateVPS(map[string]any{"host": "bad host"}); err == nil {
		t.Fatal("非法主机应被拒绝")
	}
	if _, err := s.UpdateVPS(map[string]any{"enabled": true}); err == nil {
		t.Fatal("未填写主机时启用应被拒绝")
	}
	if _, err := s.UpdateVPS(map[string]any{"port": float64(70000)}); err == nil {
		t.Fatal("非法 SSH 端口应被拒绝")
	}
	if _, err := s.UpdateVPS(map[string]any{"socksPort": float64(80)}); err == nil {
		t.Fatal("特权 SOCKS 端口应被拒绝")
	}
	if _, err := s.UpdateVPS(map[string]any{"forwards": []any{
		map[string]any{"remotePort": float64(8080), "localPort": float64(0)},
	}}); err == nil {
		t.Fatal("非法入站端口应被拒绝")
	}

	cfg, err := s.UpdateVPS(map[string]any{
		"host":      "38.58.108.195",
		"port":      float64(1841),
		"user":      "root",
		"socksPort": float64(1080),
		"forwards": []any{
			map[string]any{"label": "web", "remotePort": float64(8080), "localPort": float64(8080)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Host != "38.58.108.195" || cfg.Port != 1841 || len(cfg.Forwards) != 1 {
		t.Fatalf("VPS 配置未生效：%+v", cfg)
	}

	if _, err := s.StartVPS(context.Background()); err == nil {
		t.Fatal("尚未生成密钥时启动隧道应被拒绝")
	}

	pub, err := s.EnsureVPSKey()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(pub, "ssh-ed25519 ") {
		t.Fatalf("公钥格式异常：%q", pub)
	}
	pub2, err := s.EnsureVPSKey()
	if err != nil || pub2 != pub {
		t.Fatalf("重复生成应复用同一密钥：%v", err)
	}

	// 隧道未运行时不应把出口改到 VPS。
	env := s.SystemEnv(s.Store.Copy())
	if strings.Contains(env["ALL_PROXY"], "socks5h") {
		t.Fatalf("隧道未运行时不应注入 VPS 代理：%s", env["ALL_PROXY"])
	}

	info := s.VPSInfo()
	if ok, _ := info["keyReady"].(bool); !ok {
		t.Fatal("VPSInfo 应报告密钥就绪")
	}
	if cmd, _ := info["installCommand"].(string); !strings.Contains(cmd, "authorized_keys") {
		t.Fatalf("缺少公钥安装命令：%q", cmd)
	}
}

func TestPendingTaskInterruptedOnRestart(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ANYWHERE_DATA_DIR", dir)
	cfg, _ := config.Load()
	st, _ := store.Open(cfg.StatePath())
	_ = st.UpsertTask(store.TaskRecord{ID: "tdead", Kind: "snapshot", Title: "遗留", Status: "running", CreatedAt: time.Now().UTC()})
	m := tasks.New(st, cfg.LogDir())
	list := m.List()
	if len(list) != 1 || list[0].Status != "failed" {
		t.Fatalf("重启后遗留任务应标记失败：%+v", list)
	}
}

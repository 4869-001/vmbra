package vps

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testKey(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(path, []byte("test-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestBuildArgsIncludesTunnelAndForwards(t *testing.T) {
	key := testKey(t)
	args, err := BuildArgs(Config{
		Host:           "38.58.108.195",
		Port:           1841,
		User:           "root",
		KeyPath:        key,
		SOCKSPort:      1080,
		GatewayPorts:   true,
		KnownHostsPath: "/tmp/known_hosts",
		RemoteForwards: []RemoteForward{
			{RemotePort: 8080, LocalPort: 8080},
			{RemotePort: 9000, LocalPort: 80},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Join(args, " ")
	for _, want := range []string{
		"-D 127.0.0.1:1080",
		"-R 0.0.0.0:8080:127.0.0.1:8080",
		"-R 0.0.0.0:9000:127.0.0.1:80",
		"-p 1841",
		"-i " + key,
		"-l root",
		"StrictHostKeyChecking=accept-new",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("参数缺少 %q：%s", want, line)
		}
	}
	if args[len(args)-1] != "38.58.108.195" {
		t.Fatalf("主机应位于参数末尾：%s", line)
	}
}

func TestBuildArgsLoopbackBindWithoutGatewayPorts(t *testing.T) {
	args, err := BuildArgs(Config{
		Host:           "vps.example.com",
		Port:           22,
		User:           "root",
		KeyPath:        testKey(t),
		SOCKSPort:      1081,
		RemoteForwards: []RemoteForward{{RemotePort: 8080, LocalPort: 3000}},
	})
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Join(args, " ")
	if !strings.Contains(line, "-R 127.0.0.1:8080:127.0.0.1:3000") {
		t.Fatalf("未开启 GatewayPorts 时应只绑定回环：%s", line)
	}
}

func TestBuildArgsRejectsInvalid(t *testing.T) {
	key := testKey(t)
	base := Config{
		Host: "vps.example.com", Port: 22, User: "root", KeyPath: key, SOCKSPort: 1080,
	}
	cases := map[string]func(c *Config){
		"空主机":      func(c *Config) { c.Host = "" },
		"主机含空格":    func(c *Config) { c.Host = "vps host" },
		"非法用户":     func(c *Config) { c.User = "root;rm" },
		"端口越界":     func(c *Config) { c.Port = 70000 },
		"特权 SOCKS": func(c *Config) { c.SOCKSPort = 80 },
		"私钥不存在":    func(c *Config) { c.KeyPath = "/nonexistent/key" },
		"远端端口为 0": func(c *Config) {
			c.RemoteForwards = []RemoteForward{{RemotePort: 0, LocalPort: 80}}
		},
		"远端端口重复": func(c *Config) {
			c.RemoteForwards = []RemoteForward{{RemotePort: 8080, LocalPort: 80}, {RemotePort: 8080, LocalPort: 81}}
		},
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

func TestStatusBeforeStart(t *testing.T) {
	tunnel := New()
	st := tunnel.Status()
	if st.Running {
		t.Fatal("新建隧道不应处于运行状态")
	}
	if tunnel.Running() {
		t.Fatal("Running 应为 false")
	}
	if tunnel.SOCKSAddr() != "" {
		t.Fatalf("未配置时 SOCKS 地址应为空：%s", tunnel.SOCKSAddr())
	}
}

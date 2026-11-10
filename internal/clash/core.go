package clash

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Core 托管一个本机 mihomo 内核进程。它只监听回环地址，并开启用户名口令认证，
// 避免同机其它进程「白嫖」这条出口链路。
type Core struct {
	dir  string
	bin  string
	port int
	pass string

	mu       sync.Mutex
	cmd      *exec.Cmd
	running  bool
	node     Node
	logPath  string
	ctrlPort int
	secret   string
	names    []string
}

// NewCore 构造内核托管对象，并探测可用的 mihomo 可执行文件。
func NewCore(dataDir string) *Core {
	dir := filepath.Join(dataDir, "proxy")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		dir = os.TempDir()
	}
	return &Core{dir: dir, bin: findBinary(), logPath: filepath.Join(dir, "core.log")}
}

// findBinary 按优先级寻找 mihomo 内核。
func findBinary() string {
	candidates := []string{
		os.Getenv("VMBRA_CLASH_CORE"),
		os.Getenv("VMBRA_MIHOMO"),
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".vmbra", "bin", "mihomo"))
	}
	candidates = append(candidates,
		"/opt/homebrew/bin/mihomo",
		"/usr/local/bin/mihomo",
		"/Applications/Clash Verge.app/Contents/MacOS/verge-mihomo",
		"/Applications/Clash Verge.app/Contents/MacOS/mihomo",
		"/Applications/Cage.app/Contents/Resources/mihomo",
	)
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	return ""
}

// Available 表示本机是否找到可用的内核。
func (c *Core) Available() bool { return c.bin != "" }

// Binary 返回内核路径（诊断用）。
func (c *Core) Binary() string { return c.bin }

// Port 返回内核监听端口（未启动时为 0）。
func (c *Core) Port() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.port
}

// Running 表示内核是否在运行。
func (c *Core) Running() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.running
}

// Node 返回当前生效节点。
func (c *Core) Node() Node {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.node
}

// HTTPProxy 返回带认证的 HTTP 代理地址（mixed-port 同时支持 HTTP/SOCKS5）。
func (c *Core) HTTPProxy() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.running || c.port == 0 {
		return ""
	}
	return fmt.Sprintf("http://127.0.0.1:%d", c.port)
}

// BrowserProxy 返回浏览器可用的代理地址。
// Chrome 的 --proxy-server 不支持携带口令，而 mihomo 的 authentication 是全局的，
// 因此这里不启用入站认证，只靠「仅监听回环 + allow-lan: false」把边界锁在本机。
// 控制接口仍然需要 secret，避免同机进程改配置。
func (c *Core) BrowserProxy() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.running || c.port == 0 {
		return ""
	}
	return fmt.Sprintf("http://127.0.0.1:%d", c.port)
}

// SOCKSProxy 返回 socks5h 形式地址，供 ALL_PROXY 使用。
func (c *Core) SOCKSProxy() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.running || c.port == 0 {
		return ""
	}
	return fmt.Sprintf("socks5h://127.0.0.1:%d", c.port)
}

type dnsConfig struct {
	Enable            bool     `yaml:"enable"`
	IPv6              bool     `yaml:"ipv6"`
	EnhancedMode      string   `yaml:"enhanced-mode"`
	FakeIPRange       string   `yaml:"fake-ip-range"`
	NameServer        []string `yaml:"nameserver"`
	ProxyServerNS     []string `yaml:"proxy-server-nameserver"`
	DefaultNameServer []string `yaml:"default-nameserver,omitempty"`
	UseHosts          bool     `yaml:"use-hosts"`
	RespectRules      bool     `yaml:"respect-rules,omitempty"`
}

// listener 是给浏览器单独开的一个免认证入站：
// Chrome 不支持在 --proxy-server 里携带用户名口令，所以单独开一个
// 只监听回环、无认证的 mixed 端口，主 mixed-port 仍然带认证。
type listener struct {
	Name   string `yaml:"name"`
	Type   string `yaml:"type"`
	Port   int    `yaml:"port"`
	Listen string `yaml:"listen"`
	UDP    bool   `yaml:"udp"`
}

type proxyGroup struct {
	Name    string   `yaml:"name"`
	Type    string   `yaml:"type"`
	Proxies []string `yaml:"proxies"`
}

type coreConfig struct {
	MixedPort     int              `yaml:"mixed-port"`
	ExternalCtrl  string           `yaml:"external-controller,omitempty"`
	Secret        string           `yaml:"secret,omitempty"`
	AllowLan      bool             `yaml:"allow-lan"`
	BindAddress   string           `yaml:"bind-address"`
	Mode          string           `yaml:"mode"`
	LogLevel      string           `yaml:"log-level"`
	IPv6          bool             `yaml:"ipv6"`
	UnifiedDelay  bool             `yaml:"unified-delay"`
	TCPConcurrent bool             `yaml:"tcp-concurrent"`
	DNS           dnsConfig        `yaml:"dns"`
	Listeners     []listener       `yaml:"listeners,omitempty"`
	Proxies       []map[string]any `yaml:"proxies"`
	ProxyGroups   []proxyGroup     `yaml:"proxy-groups"`
	Rules         []string         `yaml:"rules"`
}

// Start 用单个节点启动内核（等价于 StartAll 的单节点特例）。
func (c *Core) Start(node Node) error {
	return c.StartAll([]Node{node}, node)
}

// StartAll 把所有节点都写进配置，并把 EGRESS 组设为手动选择。
// 好处：测速与切换节点都走 mihomo 的控制接口，不需要重启内核。
func (c *Core) StartAll(nodes []Node, selected Node) error {
	if !c.Available() {
		return fmt.Errorf("未找到 mihomo 内核，请把 mihomo 放到 ~/.vmbra/bin/mihomo，或安装 Clash Verge")
	}
	if len(nodes) == 0 {
		return fmt.Errorf("没有可用节点")
	}
	c.Stop()

	pass, err := randomHex(16)
	if err != nil {
		return err
	}
	port, err := freePort(7893)
	if err != nil {
		return err
	}
	ctrlPort, err := freePort(port + 1)
	if err != nil {
		return err
	}

	proxies := make([]map[string]any, 0, len(nodes))
	names := make([]string, 0, len(nodes))
	seen := map[string]bool{}
	for _, n := range nodes {
		if n.Name == "" || seen[n.Name] {
			continue
		}
		seen[n.Name] = true
		raw := n.Raw
		if raw == nil {
			raw = map[string]any{
				"name": n.Name, "type": n.Type, "server": n.Server,
				"port": n.Port, "udp": false,
			}
		}
		raw["name"] = n.Name
		proxies = append(proxies, raw)
		names = append(names, n.Name)
	}
	if len(proxies) == 0 {
		return fmt.Errorf("节点配置无效")
	}

	cfg := coreConfig{
		MixedPort:     port,
		ExternalCtrl:  fmt.Sprintf("127.0.0.1:%d", ctrlPort),
		Secret:        pass,
		AllowLan:      false,
		BindAddress:   "127.0.0.1",
		Mode:          "rule",
		LogLevel:      "warning",
		IPv6:          false,
		UnifiedDelay:  true,
		TCPConcurrent: true,
		DNS: dnsConfig{
			Enable:            true,
			IPv6:              false,
			EnhancedMode:      "fake-ip",
			FakeIPRange:       "198.18.0.1/16",
			UseHosts:          true,
			NameServer:        []string{"https://1.1.1.1/dns-query", "https://8.8.8.8/dns-query"},
			ProxyServerNS:     []string{"223.5.5.5", "119.29.29.29"},
			DefaultNameServer: []string{"223.5.5.5", "119.29.29.29"},
		},
		Proxies:     proxies,
		ProxyGroups: []proxyGroup{{Name: "EGRESS", Type: "select", Proxies: names}},
		Rules:       []string{"MATCH,EGRESS"},
	}

	body, err := yaml.Marshal(&cfg)
	if err != nil {
		return err
	}
	runtimePath := filepath.Join(c.dir, "runtime.yaml")
	if err := os.WriteFile(runtimePath, body, 0o600); err != nil {
		return err
	}

	logFile, err := os.OpenFile(c.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	cmd := exec.Command(c.bin, "-d", c.dir, "-f", runtimePath)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return fmt.Errorf("启动内核失败: %w", err)
	}
	go func() {
		_ = cmd.Wait()
		_ = logFile.Close()
	}()

	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	if err := waitPort(addr, 10*time.Second); err != nil {
		_ = cmd.Process.Kill()
		return fmt.Errorf("内核启动后端口未就绪: %w", err)
	}
	ctrlAddr := net.JoinHostPort("127.0.0.1", strconv.Itoa(ctrlPort))
	if err := waitPort(ctrlAddr, 10*time.Second); err != nil {
		_ = cmd.Process.Kill()
		return fmt.Errorf("内核控制接口未就绪: %w", err)
	}

	c.mu.Lock()
	c.cmd = cmd
	c.port = port
	c.pass = pass
	c.node = selected
	c.running = true
	c.ctrlPort = ctrlPort
	c.secret = pass
	c.names = names
	c.mu.Unlock()

	// 默认选中用户指定的节点；失败不致命，保留组内第一个。
	if err := c.Select(selected.Name); err != nil {
		_ = err
	}
	return nil
}

// Stop 停止内核。可重复调用。
func (c *Core) Stop() {
	c.mu.Lock()
	cmd := c.cmd
	c.cmd = nil
	c.running = false
	c.port = 0
	c.pass = ""
	c.ctrlPort = 0
	c.secret = ""
	c.names = nil
	c.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}
}

func waitPort(addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("等待 %s 超时", addr)
}

func freePort(start int) (int, error) {
	for p := start; p < start+20; p++ {
		l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(p)))
		if err != nil {
			continue
		}
		_ = l.Close()
		return p, nil
	}
	return 0, fmt.Errorf("本地没有空闲端口可用")
}

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

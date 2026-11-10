package sandbox

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// probeNC 是系统自带的 netcat，用于在隔离环境内做 TCP 探测（macOS 默认存在）。
const probeNC = "/usr/bin/nc"

// Capability 是一项隔离边界的实测结果。
//
// Expected / Actual 取值：allow（应当放行）、deny（应当拒绝）、unknown（无法判定）。
// Fatal 表示不符时属于硬边界破坏（诊断页按失败处理），否则按告警处理。
type Capability struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
	OK       bool   `json:"ok"`
	Fatal    bool   `json:"fatal"`
	Detail   string `json:"detail"`
}

// Capabilities 在真实策略下实测一组能力，验证「策略文件生效」而不是只验证「引擎能起进程」。
//
// network 应与当前出网开关一致：关闭时用 .policy.nonet.sbpl，开启时用 .policy.net.sbpl。
// sandbox-exec 不可用时返回 nil（此时诊断里的引擎项已经失败，无需重复报告）。
func (d *Driver) Capabilities(ctx context.Context, network bool) []Capability {
	if !d.sandboxAvailable {
		return nil
	}
	caps := []Capability{
		d.capExec(ctx, network),
		d.capWrite(ctx, network, true),
		d.capWrite(ctx, network, false),
		d.capLoopbackConnect(ctx, network),
		d.capLoopbackBind(ctx, network),
	}
	if c, ok := d.capEgress(ctx, network); ok {
		caps = append(caps, c)
	}
	return caps
}

// probe 在隔离环境内执行一条命令，返回合并后的输出与执行结果。
func (d *Driver) runProbe(ctx context.Context, network bool, timeout time.Duration, argv ...string) (string, ExecResult) {
	stdout, stderr, res := d.RunOutput(ctx, ExecRequest{
		Argv:       argv,
		Network:    network,
		Timeout:    timeout,
		CPUSeconds: int(timeout.Seconds()) + 5,
		FileSizeMB: 8,
	})
	return stdout + stderr, res
}

func (d *Driver) capExec(ctx context.Context, network bool) Capability {
	out, res := d.runProbe(ctx, network, 8*time.Second, "/bin/echo", "vmbra-probe")
	c := Capability{ID: "exec", Title: "进程启动", Expected: "allow", Fatal: true}
	if res.ExitCode == 0 && strings.Contains(out, "vmbra-probe") {
		c.Actual, c.OK = "allow", true
		c.Detail = "隔离环境内可执行命令"
		return c
	}
	c.Actual = "deny"
	c.Detail = "隔离环境内无法启动进程：" + summarize(out, res)
	return c
}

// capWrite 探测写入边界：inside=true 验证工作区可写，false 验证工作区外被拒。
func (d *Driver) capWrite(ctx context.Context, network, inside bool) Capability {
	dir, id, title := d.UserHome, "write-outside", "工作区外写入"
	c := Capability{ID: id, Title: title, Expected: "deny", Fatal: true}
	if inside {
		dir, id, title = d.Workspace, "write-inside", "工作区内写入"
		c = Capability{ID: id, Title: title, Expected: "allow", Fatal: true}
	}
	path := filepath.Join(dir, fmt.Sprintf(".vmbra-probe-%d", os.Getpid()))

	// 探针文件用 pid 命名，结束后无论如何都清掉，避免污染用户目录或工作区。
	_ = os.Remove(path)
	out, res := d.runProbe(ctx, network, 8*time.Second, "/bin/sh", "-c",
		`printf probe > "$1" && test -s "$1" && rm -f "$1"`, "sh", path)
	_, statErr := os.Stat(path)
	_ = os.Remove(path)

	wrote := res.ExitCode == 0 || statErr == nil
	c.Actual = "deny"
	if wrote {
		c.Actual = "allow"
	}
	c.OK = c.Actual == c.Expected
	switch {
	case inside && c.OK:
		c.Detail = "工作区内可正常创建文件"
	case inside:
		c.Detail = "工作区内无法写入：" + summarize(out, res)
	case c.OK:
		c.Detail = "工作区外写入被内核拒绝"
	default:
		c.Detail = "隔离失效：成功写入了 " + path
	}
	return c
}

// capLoopbackConnect 验证工作站内能否访问宿主回环端口。
// 宿主侧临时开一个真实监听端口，隔离环境内用 nc 连它：连上=放行，连不上=被策略拒绝。
func (d *Driver) capLoopbackConnect(ctx context.Context, network bool) Capability {
	c := Capability{ID: "loopback-connect", Title: "访问宿主回环", Expected: "allow"}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		c.Actual = "unknown"
		c.Detail = "无法在宿主回环开探测端口：" + err.Error()
		return c
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	out, res := d.runProbe(ctx, network, 6*time.Second,
		probeNC, "-z", "-G", "2", "127.0.0.1", strconv.Itoa(port))

	c.Actual = "deny"
	if res.ExitCode == 0 {
		c.Actual = "allow"
	}
	c.OK = c.Actual == "allow"
	if c.OK {
		c.Detail = fmt.Sprintf("工作站内可连接 127.0.0.1:%d", port)
		return c
	}
	c.Detail = "工作站内被禁止连接 127.0.0.1：" + summarize(out, res)
	return c
}

// capLoopbackBind 验证工作站内能否监听回环端口（端口转发的目标端口依赖这一能力）。
// 从宿主侧回连隔离环境内刚监听的端口，能连上说明监听见效。
func (d *Driver) capLoopbackBind(ctx context.Context, network bool) Capability {
	c := Capability{ID: "loopback-bind", Title: "工作站内回环监听", Expected: "allow"}
	port, ok := freeLoopbackPort()
	if !ok {
		c.Actual = "unknown"
		c.Detail = "无法获取空闲回环端口"
		return c
	}
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	reached := make(chan bool, 1)
	go func() {
		deadline := time.Now().Add(1200 * time.Millisecond)
		for time.Now().Before(deadline) {
			if conn, err := net.DialTimeout("tcp", addr, 150*time.Millisecond); err == nil {
				_ = conn.Close()
				reached <- true
				return
			}
			time.Sleep(40 * time.Millisecond)
		}
		reached <- false
	}()
	// 监听成功时会一直阻塞，因此这里用短超时主动结束；被回收视为「已进入监听」。
	out, _ := d.runProbe(ctx, network, 1500*time.Millisecond,
		probeNC, "-l", "127.0.0.1", strconv.Itoa(port))
	listening := <-reached

	lower := strings.ToLower(out)
	switch {
	case listening:
		c.Actual = "allow"
	case strings.Contains(lower, "not permitted") || strings.Contains(lower, "permission denied"):
		c.Actual = "deny"
	default:
		c.Actual = "unknown"
	}
	c.OK = c.Actual == "allow"
	switch {
	case c.OK:
		c.Detail = fmt.Sprintf("工作站内可监听 127.0.0.1:%d", port)
	case c.Actual == "deny":
		c.Detail = "工作站内无法监听回环端口：" + summarize(out, ExecResult{})
	default:
		c.Detail = "无法判定回环监听能力：" + summarize(out, ExecResult{})
	}
	return c
}

// capEgress 验证非回环网络是否被拦截。
// 只在出网关闭时断言「必须拒绝」；开启出网时交给「出网链路」检查，避免离线环境误报。
func (d *Driver) capEgress(ctx context.Context, network bool) (Capability, bool) {
	if network {
		return Capability{}, false
	}
	ip := firstExternalIPv4()
	if ip == "" {
		return Capability{}, false
	}
	c := Capability{ID: "egress", Title: "非回环网络", Expected: "deny", Fatal: true}
	ln, err := net.Listen("tcp", net.JoinHostPort(ip, "0"))
	if err != nil {
		return Capability{}, false
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	out, res := d.runProbe(ctx, network, 6*time.Second,
		probeNC, "-z", "-G", "2", ip, strconv.Itoa(port))

	c.Actual = "deny"
	if res.ExitCode == 0 {
		c.Actual = "allow"
	}
	c.OK = c.Actual == c.Expected
	if c.OK {
		c.Detail = fmt.Sprintf("工作站内无法连接非回环地址 %s:%d", ip, port)
		return c, true
	}
	c.Detail = fmt.Sprintf("隔离失效：成功连接了非回环地址 %s:%d %s", ip, port, summarize(out, res))
	return c, true
}

// freeLoopbackPort 取一个当前空闲的回环端口（取到后立刻释放）。
func freeLoopbackPort() (int, bool) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, false
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port, true
}

// firstExternalIPv4 返回第一个非回环 IPv4 地址，用于验证「出网」这一侧是否被策略拦截。
func firstExternalIPv4() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip := ipnet.IP.To4()
		if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			continue
		}
		return ip.String()
	}
	return ""
}

// summarize 把一次探测输出压成一行，便于放进诊断详情。
func summarize(out string, res ExecResult) string {
	msg := strings.TrimSpace(out)
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		msg = strings.TrimSpace(msg[:i])
	}
	if msg == "" {
		switch {
		case res.TimedOut:
			msg = "执行超时"
		case res.ExitCode != 0:
			msg = fmt.Sprintf("退出码 %d", res.ExitCode)
		default:
			msg = "无输出"
		}
	}
	if len(msg) > 120 {
		msg = msg[:120] + "…"
	}
	return msg
}

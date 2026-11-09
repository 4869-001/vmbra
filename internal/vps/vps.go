// Package vps 通过 SSH 隧道让工作站借用远端 VPS 的公网出入口。
//
// 一条 ssh 进程同时承担两件事：
//   - -D 在本地开 SOCKS5，工作站的出网流量从 VPS 出口离开；
//   - -R 把 VPS 上的端口回落到工作站本地端口，形成公网入站。
//
// 隧道只做转发，不修改 VPS 的 sshd 配置：远端绑定地址是否生效取决于 VPS 的
// GatewayPorts 设置，失败原因会原样回传给控制台。
package vps

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// RemoteForward 是一条远端端口回落规则。
type RemoteForward struct {
	RemotePort int
	LocalPort  int
}

// Config 是启动隧道所需的参数。
type Config struct {
	Host           string
	Port           int
	User           string
	KeyPath        string
	SOCKSPort      int
	GatewayPorts   bool
	KnownHostsPath string
	RemoteForwards []RemoteForward
}

// Status 是隧道的运行状态。
type Status struct {
	Running   bool      `json:"running"`
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"startedAt,omitempty"`
	SOCKSAddr string    `json:"socksAddr"`
	Error     string    `json:"error,omitempty"`
	Log       string    `json:"log,omitempty"`
}

var (
	hostPattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9._-]{0,252})$`)
	userPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,32}$`)
)

// Tunnel 管理一条 SSH 隧道进程，可重复启动与停止。
type Tunnel struct {
	mu       sync.Mutex
	cmd      *exec.Cmd
	cfg      Config
	started  time.Time
	errMsg   string
	stopping bool
	log      *logBuffer
}

// New 创建隧道管理器。
func New() *Tunnel {
	return &Tunnel{log: &logBuffer{limit: 8 << 10}}
}

// BuildArgs 生成 ssh 命令行参数，纯函数便于测试。
func BuildArgs(cfg Config) ([]string, error) {
	if !hostPattern.MatchString(cfg.Host) {
		return nil, errors.New("VPS 主机地址不合法")
	}
	if !userPattern.MatchString(cfg.User) {
		return nil, errors.New("VPS 登录用户不合法")
	}
	if cfg.Port <= 0 || cfg.Port > 65535 {
		return nil, errors.New("SSH 端口需在 1-65535 之间")
	}
	if cfg.SOCKSPort < 1024 || cfg.SOCKSPort > 65535 {
		return nil, errors.New("SOCKS 端口需在 1024-65535 之间")
	}
	if cfg.KeyPath == "" {
		return nil, errors.New("请先配置 SSH 私钥")
	}
	if _, err := os.Stat(cfg.KeyPath); err != nil {
		return nil, fmt.Errorf("SSH 私钥不可用：%v", err)
	}

	knownHosts := cfg.KnownHostsPath
	if knownHosts == "" {
		knownHosts = os.DevNull
	}
	args := []string{
		"-N",
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "UserKnownHostsFile=" + knownHosts,
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=3",
		"-o", "TCPKeepAlive=yes",
		"-D", net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.SOCKSPort)),
	}

	bind := "127.0.0.1"
	if cfg.GatewayPorts {
		bind = "0.0.0.0"
	}
	seen := map[int]bool{}
	for _, f := range cfg.RemoteForwards {
		if f.RemotePort <= 0 || f.RemotePort > 65535 || f.LocalPort <= 0 || f.LocalPort > 65535 {
			return nil, errors.New("远端转发端口需在 1-65535 之间")
		}
		if seen[f.RemotePort] {
			return nil, fmt.Errorf("远端端口重复：%d", f.RemotePort)
		}
		seen[f.RemotePort] = true
		args = append(args,
			"-R", fmt.Sprintf("%s:%d:127.0.0.1:%d", bind, f.RemotePort, f.LocalPort))
	}

	args = append(args,
		"-p", strconv.Itoa(cfg.Port),
		"-i", cfg.KeyPath,
		"-l", cfg.User,
		cfg.Host,
	)
	return args, nil
}

// Start 启动隧道。已运行时会先停止旧进程。
func (t *Tunnel) Start(ctx context.Context, cfg Config) error {
	args, err := BuildArgs(cfg)
	if err != nil {
		return err
	}
	t.Stop()

	t.mu.Lock()
	t.cfg = cfg
	t.errMsg = ""
	t.stopping = false
	t.log = &logBuffer{limit: 8 << 10}
	t.mu.Unlock()

	cmd := exec.Command("ssh", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdout = io.Discard
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 ssh 失败：%w", err)
	}

	t.mu.Lock()
	t.cmd = cmd
	t.started = time.Now()
	t.mu.Unlock()

	go func() {
		buf := make([]byte, 4<<10)
		for {
			n, err := stderr.Read(buf)
			if n > 0 {
				t.log.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()

	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()

	// 给 ssh 一点时间建立连接：进程提前退出或 SOCKS 端口起来都算有结论。
	deadline := time.After(2500 * time.Millisecond)
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case err := <-waitCh:
			t.mu.Lock()
			detail := t.log.String()
			t.cmd = nil
			msg := "SSH 隧道已退出"
			if err != nil {
				msg = "SSH 隧道启动失败：" + err.Error()
			}
			if detail != "" {
				msg += "；" + detail
			}
			t.errMsg = msg
			t.mu.Unlock()
			return errors.New(msg)
		case <-tick.C:
			if conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.SOCKSPort)), 300*time.Millisecond); err == nil {
				_ = conn.Close()
				go t.monitor(cmd, waitCh)
				return nil
			}
		case <-deadline:
			// 端口暂未就绪时返回成功但继续观察，状态接口会给出实际结果。
			t.mu.Lock()
			running := t.cmd != nil
			msg := t.errMsg
			t.mu.Unlock()
			if !running {
				if msg == "" {
					msg = "SSH 隧道未能建立"
				}
				return errors.New(msg)
			}
			go t.monitor(cmd, waitCh)
			return nil
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		}
	}
}

// monitor 在隧道后台运行期间跟踪进程退出，把断线原因记录到状态里。
func (t *Tunnel) monitor(cmd *exec.Cmd, waitCh <-chan error) {
	err := <-waitCh
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cmd != cmd {
		return
	}
	t.cmd = nil
	if t.stopping {
		t.errMsg = ""
		return
	}
	msg := "SSH 隧道已断开"
	if err != nil {
		msg += "：" + err.Error()
	}
	if detail := t.log.String(); detail != "" {
		msg += "；" + detail
	}
	t.errMsg = msg
}

// Stop 结束隧道进程及其子进程。
func (t *Tunnel) Stop() {
	t.mu.Lock()
	cmd := t.cmd
	t.cmd = nil
	t.stopping = true
	t.errMsg = ""
	t.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return
	}
	if cmd.Process.Pid > 0 {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
	_ = cmd.Process.Kill()
}

// Running 返回隧道进程是否仍在运行。
func (t *Tunnel) Running() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cmd != nil
}

// SOCKSAddr 返回本地 SOCKS5 地址。
func (t *Tunnel) SOCKSAddr() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cfg.SOCKSPort == 0 {
		return ""
	}
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(t.cfg.SOCKSPort))
}

// Status 返回当前状态与最近的 ssh 日志。
func (t *Tunnel) Status() Status {
	t.mu.Lock()
	defer t.mu.Unlock()
	st := Status{
		Running:   t.cmd != nil,
		SOCKSAddr: "",
		Error:     t.errMsg,
		Log:       t.log.String(),
	}
	if t.cfg.SOCKSPort > 0 {
		st.SOCKSAddr = net.JoinHostPort("127.0.0.1", strconv.Itoa(t.cfg.SOCKSPort))
	}
	if t.cmd != nil && t.cmd.Process != nil {
		st.PID = t.cmd.Process.Pid
		st.StartedAt = t.started
	}
	return st
}

// logBuffer 是并发安全的有界日志缓冲。
type logBuffer struct {
	mu    sync.Mutex
	limit int
	buf   []byte
}

func (b *logBuffer) Write(p []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if len(b.buf) > b.limit {
		b.buf = b.buf[len(b.buf)-b.limit:]
	}
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

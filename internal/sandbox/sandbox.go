// Package sandbox 提供本地隔离执行内核：策略化进程隔离、资源限制、PTY 会话与工作区文件系统。
package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
)

const sandboxExecPath = "/usr/bin/sandbox-exec"

// ErrRootUnsupported 表示当前隔离后端不支持 root 会话。
var ErrRootUnsupported = errors.New("本地进程隔离模式不提供 root 提权，请切换到虚拟机后端后重试")

// Driver 是本地隔离工作站的内核，负责受限执行、终端会话与工作区管理。
type Driver struct {
	Root      string // 工作站根目录
	Workspace string // 工作区（可读写）
	Home      string // 沙箱内家目录
	Tmp       string // 沙箱内临时目录
	UserHome  string // 真实用户 home

	sandboxAvailable bool
	sandboxError     string

	mu       sync.Mutex
	netOff   string
	netOn    string
	sessions map[string]*Session
	seq      int
}

// New 创建隔离内核并完成能力探测。
func New(root, userHome string) (*Driver, error) {
	d := &Driver{Root: root, UserHome: userHome, sessions: map[string]*Session{}}
	if err := d.setup(); err != nil {
		return nil, err
	}
	return d, nil
}

// Rebind 把隔离内核切到另一套工作站根目录（切换工作站时调用）。
// 调用方需先停止全部会话与端口转发；切换后策略文件按新目录重新生成。
func (d *Driver) Rebind(root, userHome string) error {
	d.CloseAll()
	d.Root = root
	d.UserHome = userHome
	return d.setup()
}

// setup 初始化目录结构、shell 配置与策略文件，并重新探测隔离能力。
func (d *Driver) setup() error {
	d.Workspace = filepath.Join(d.Root, "workspace")
	d.Home = filepath.Join(d.Root, "home")
	d.Tmp = filepath.Join(d.Root, "tmp")
	for _, dir := range []string{d.Root, d.Workspace, d.Home, d.Tmp} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	// macOS 上 /var 等目录是软链接，统一解析为真实路径，避免后续路径校验误判。
	if real, err := filepath.EvalSymlinks(d.Root); err == nil {
		d.Root = real
		d.Workspace = filepath.Join(real, "workspace")
		d.Home = filepath.Join(real, "home")
		d.Tmp = filepath.Join(real, "tmp")
	}
	// 工作区初始化：放置示例文件，便于用户首次进入即可验证读写。
	readme := filepath.Join(d.Workspace, "README.md")
	if _, err := os.Stat(readme); os.IsNotExist(err) {
		_ = os.WriteFile(readme, []byte("# 工作站工作区\n\n该目录位于本机隔离工作站的私有文件系统中，可由“文件”页管理与宿主机共享。\n"), 0o644)
	}
	_ = os.MkdirAll(filepath.Join(d.Workspace, "projects"), 0o755)
	_ = os.MkdirAll(filepath.Join(d.Workspace, "scripts"), 0o755)
	if err := d.seedShellConfig(); err != nil {
		return err
	}

	d.netOff = filepath.Join(d.Root, ".policy.nonet.sbpl")
	d.netOn = filepath.Join(d.Root, ".policy.net.sbpl")
	if err := d.refreshPolicies(); err != nil {
		return err
	}
	d.probe()
	return nil
}

// seedShellConfig 写入沙箱家目录的 shell 配置，让终端呈现一致的工作站身份。
func (d *Driver) seedShellConfig() error {
	zshrc := strings.Join([]string{
		"# 由 Vmbra WorkStation 管理，请勿手工修改",
		`export PATH="/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin"`,
		`export HISTFILE="$HOME/.zsh_history"`,
		`export HISTSIZE=2000`,
		// 统一环境由管理服务写入，语言/时区/代理/DNS 与脚本、任务完全一致。
		`[ -f "$HOME/.vmbra.env" ] && . "$HOME/.vmbra.env"`,
		`PROMPT='%F{blue}workstation%f@vmbra:%~ %# '`,
		`RPROMPT='%F{240}%D{%H:%M}%f'`,
		"alias ll='ls -la'",
		"alias gs='git status -sb'",
		"unset http_proxy https_proxy all_proxy HTTP_PROXY HTTPS_PROXY ALL_PROXY",
		`[ -f "$HOME/.vmbra-init" ] || true`,
		"",
	}, "\n")
	profile := strings.Join([]string{
		`export PATH="/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin"`,
		`export PS1='workstation@vmbra:\\w \\$ '`,
		"",
	}, "\n")
	for name, content := range map[string]string{".zshrc": zshrc, ".zprofile": profile, ".profile": profile} {
		if err := os.WriteFile(filepath.Join(d.Home, name), []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func (d *Driver) refreshPolicies() error {
	base := Policy{RootDir: d.Root, HomeDir: d.UserHome}
	off := base
	off.AllowNetwork = false
	if err := WritePolicy(d.netOff, off); err != nil {
		return err
	}
	on := base
	on.AllowNetwork = true
	return WritePolicy(d.netOn, on)
}

// probe 检测 sandbox-exec 是否可用（macOS 上该工具已被标记为废弃，但仍可用）。
func (d *Driver) probe() {
	if runtime.GOOS != "darwin" {
		d.sandboxAvailable = false
		d.sandboxError = "当前系统非 macOS，已回退为受限进程模式"
		return
	}
	if _, err := os.Stat(sandboxExecPath); err != nil {
		d.sandboxAvailable = false
		d.sandboxError = "未找到 sandbox-exec，已回退为受限进程模式"
		return
	}
	probeFile := filepath.Join(d.Root, ".probe.sbpl")
	pol := Policy{RootDir: d.Root, HomeDir: d.UserHome}
	if err := WritePolicy(probeFile, pol); err != nil {
		d.sandboxAvailable = false
		d.sandboxError = err.Error()
		return
	}
	defer os.Remove(probeFile)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, sandboxExecPath, "-f", probeFile, "/bin/echo", "ok")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "ok") {
		d.sandboxAvailable = false
		msg := strings.TrimSpace(string(out))
		if msg == "" && err != nil {
			msg = err.Error()
		}
		d.sandboxError = "sandbox-exec 自检失败：" + msg
		return
	}
	d.sandboxAvailable = true
}

// SandboxAvailable 返回策略隔离是否生效。
func (d *Driver) SandboxAvailable() bool { return d.sandboxAvailable }

// SandboxError 返回探测失败原因。
func (d *Driver) SandboxError() string { return d.sandboxError }

// PolicyPath 返回给定网络策略对应的 SBPL 文件路径。
func (d *Driver) PolicyPath(network bool) string {
	if network {
		return d.netOn
	}
	return d.netOff
}

// BaseEnv 返回隔离执行的基础环境变量。
func (d *Driver) BaseEnv() []string {
	path := "/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin"
	return []string{
		"PATH=" + path,
		"HOME=" + d.Home,
		"TMPDIR=" + d.Tmp + "/",
		"USER=workstation",
		"LOGNAME=workstation",
		"SHELL=/bin/zsh",
		"TERM=xterm-256color",
		"LANG=C.UTF-8",
		"VMBRA=1",
	}
}

// ExecRequest 描述一次受限执行。
type ExecRequest struct {
	Argv       []string
	Env        map[string]string
	Dir        string
	Network    bool
	Timeout    time.Duration
	CPUSeconds int
	FileSizeMB int
	Stdin      io.Reader
	Stdout     io.Writer
	Stderr     io.Writer
}

// ExecResult 是执行结果。
type ExecResult struct {
	ExitCode int           `json:"exitCode"`
	TimedOut bool          `json:"timedOut"`
	Duration time.Duration `json:"duration"`
	Err      error         `json:"-"`
}

func (d *Driver) buildCommand(ctx context.Context, req ExecRequest) *exec.Cmd {
	argv := append([]string{}, req.Argv...)
	if d.sandboxAvailable {
		argv = append([]string{sandboxExecPath, "-f", d.PolicyPath(req.Network)}, argv...)
	}
	// 资源限制通过 shell 内建 ulimit 施加（macOS 不支持 RLIMIT_AS）。
	cpu := req.CPUSeconds
	if cpu <= 0 {
		cpu = 900
	}
	fsize := req.FileSizeMB
	if fsize <= 0 {
		fsize = 512
	}
	script := fmt.Sprintf(`ulimit -t %d 2>/dev/null; ulimit -f %d 2>/dev/null; ulimit -n 1024 2>/dev/null; exec "$@"`, cpu, fsize*1024)
	full := append([]string{"/bin/sh", "-c", script, "sh"}, argv...)

	cmd := exec.CommandContext(ctx, full[0], full[1:]...)
	cmd.Env = d.mergeEnv(req.Env)
	dir := req.Dir
	if dir == "" {
		dir = d.Workspace
	}
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdin = req.Stdin
	cmd.Stdout = req.Stdout
	cmd.Stderr = req.Stderr
	return cmd
}

func (d *Driver) mergeEnv(extra map[string]string) []string {
	if d.sandboxAvailable {
		// sandbox-exec 会把策略继承给子进程，环境变量由我们显式控制。
	}
	env := d.BaseEnv()
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	return env
}

// Exec 在隔离环境中执行命令，支持超时与进程组回收。
func (d *Driver) Exec(ctx context.Context, req ExecRequest) ExecResult {
	start := time.Now()
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}
	cmd := d.buildCommand(ctx, req)

	if err := cmd.Start(); err != nil {
		return ExecResult{ExitCode: -1, Err: err, Duration: time.Since(start)}
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	var err error
	select {
	case err = <-done:
	case <-ctx.Done():
		// 超时：回收整个进程组，避免孙进程逃逸。
		killGroup(cmd)
		err = <-done
		res := ExecResult{ExitCode: -1, TimedOut: true, Duration: time.Since(start)}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			res.Err = context.DeadlineExceeded
		} else {
			res.Err = ctx.Err()
		}
		return res
	}

	res := ExecResult{Duration: time.Since(start)}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			res.ExitCode = ee.ExitCode()
			res.Err = fmt.Errorf("命令以退出码 %d 结束", res.ExitCode)
		} else {
			res.ExitCode = -1
			res.Err = err
		}
		return res
	}
	return res
}

func killGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	_ = cmd.Process.Kill()
}

// RunOutput 执行命令并返回 stdout / stderr 文本。
func (d *Driver) RunOutput(ctx context.Context, req ExecRequest) (string, string, ExecResult) {
	var out, errb bytes.Buffer
	req.Stdout = &out
	req.Stderr = &errb
	res := d.Exec(ctx, req)
	return out.String(), errb.String(), res
}

// Session 是一个交互式 PTY 会话。
type Session struct {
	ID   string
	cmd  *exec.Cmd
	pt   *os.File
	once sync.Once
	mu   sync.Mutex
	dead bool
}

// StartShell 启动一个隔离的交互式 shell 会话。
func (d *Driver) StartShell(kind string, env map[string]string, network bool) (*Session, error) {
	shell := "/bin/zsh"
	if _, err := os.Stat(shell); err != nil {
		shell = "/bin/sh"
	}
	argv := []string{shell, "-i"}
	if kind == "root" {
		return nil, ErrRootUnsupported
	}
	if d.sandboxAvailable {
		argv = append([]string{sandboxExecPath, "-f", d.PolicyPath(network)}, argv...)
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = d.Workspace
	cmd.Env = append(d.mergeEnv(env),
		"PS1=workstation@vmbra:\\w$ ",
		"PROMPT=workstation@vmbra:%~ %# ",
		"VMBRA_SESSION="+kind,
	)
	pt, err := pty.Start(cmd)
	if err != nil {
		return nil, err
	}
	_ = pty.Setsize(pt, &pty.Winsize{Cols: 120, Rows: 32})

	d.mu.Lock()
	d.seq++
	id := "sess-" + strconv.Itoa(d.seq)
	s := &Session{ID: id, cmd: cmd, pt: pt}
	d.sessions[id] = s
	d.mu.Unlock()

	go func() {
		_ = cmd.Wait()
		d.mu.Lock()
		delete(d.sessions, id)
		d.mu.Unlock()
	}()
	return s, nil
}

// Read 读取 PTY 输出。
func (s *Session) Read(p []byte) (int, error) { return s.pt.Read(p) }

// Write 写入 PTY 输入。
func (s *Session) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dead {
		return 0, os.ErrClosed
	}
	return s.pt.Write(p)
}

// Resize 调整 PTY 窗口尺寸。
func (s *Session) Resize(cols, rows uint16) error {
	if cols == 0 || rows == 0 {
		return nil
	}
	return pty.Setsize(s.pt, &pty.Winsize{Cols: cols, Rows: rows})
}

// Close 关闭会话并回收进程组。
func (s *Session) Close() {
	s.once.Do(func() {
		s.mu.Lock()
		s.dead = true
		s.mu.Unlock()
		_ = s.pt.Close()
		if s.cmd.Process != nil {
			_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGHUP)
			_ = s.cmd.Process.Kill()
		}
	})
}

// SessionCount 返回当前活跃会话数。
func (d *Driver) SessionCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.sessions)
}

// CloseAll 关闭全部会话（工作站停止时调用）。
func (d *Driver) CloseAll() {
	d.mu.Lock()
	list := make([]*Session, 0, len(d.sessions))
	for _, s := range d.sessions {
		list = append(list, s)
	}
	d.sessions = map[string]*Session{}
	d.mu.Unlock()
	for _, s := range list {
		s.Close()
	}
}

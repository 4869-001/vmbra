package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"anywhere/internal/store"
)

// ErrMacControlDisabled 表示“操控 Mac”能力未开启。
var ErrMacControlDisabled = errors.New("操控 Mac 未开启，请先在「应用」页启用")

const maxMacOutput = 256 << 10

type cappedBuffer struct {
	mu        sync.Mutex
	buf       []byte
	truncated bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.buf) < maxMacOutput {
		remain := maxMacOutput - len(c.buf)
		if len(p) > remain {
			c.buf = append(c.buf, p[:remain]...)
			c.truncated = true
		} else {
			c.buf = append(c.buf, p...)
		}
	} else {
		c.truncated = true
	}
	return len(p), nil
}

func (c *cappedBuffer) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return string(c.buf)
}

// macState 缓存屏幕录制权限探测结果。
var macState struct {
	mu          sync.Mutex
	screenPerm  string // ok | denied | unknown
	lastCapture time.Time
}

// MacStatus 返回“操控 Mac”开关与权限状态。
func (s *Service) MacStatus() map[string]any {
	st := s.Store.Copy()
	macState.mu.Lock()
	perm := macState.screenPerm
	if perm == "" {
		perm = "unknown"
	}
	macState.mu.Unlock()
	return map[string]any{
		"enabled":          st.Settings.MacControlEnabled,
		"allowRunCommand":  st.Settings.AllowMacRunCommand,
		"screenPermission": perm,
		"detail":           "以你的 Mac 用户身份运行命令，命令内容不会被记录。",
	}
}

// MacScreen 截取宿主屏幕（仅查看，不注入输入）。
func (s *Service) MacScreen(ctx context.Context) ([]byte, error) {
	st := s.Store.Copy()
	if !st.Settings.MacControlEnabled {
		return nil, ErrMacControlDisabled
	}
	tmp, err := os.CreateTemp(s.Cfg.DataDir, "screen-*.png")
	if err != nil {
		return nil, err
	}
	path := tmp.Name()
	_ = tmp.Close()
	defer os.Remove(path)

	cmd := exec.CommandContext(ctx, "/usr/sbin/screencapture", "-x", "-t", "png", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("截屏失败：%v %s", err, strings.TrimSpace(string(out)))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	macState.mu.Lock()
	if len(data) < 30_000 {
		macState.screenPerm = "denied"
	} else {
		macState.screenPerm = "ok"
	}
	macState.lastCapture = time.Now()
	macState.mu.Unlock()
	if len(data) < 30_000 {
		return data, errors.New("截屏内容为空，可能缺少「屏幕录制」权限：系统设置 → 隐私与安全性 → 屏幕录制")
	}
	return data, nil
}

// MacRunRequest 是一次 Mac 命令执行请求。
type MacRunRequest struct {
	Command    string `json:"command"`
	Cwd        string `json:"cwd"`
	TimeoutSec int    `json:"timeoutSec"`
}

// MacRunResult 是执行结果。
type MacRunResult struct {
	Output     string `json:"output"`
	ExitCode   int    `json:"exitCode"`
	DurationMS int64  `json:"durationMs"`
	Truncated  bool   `json:"truncated"`
}

// MacRun 以宿主用户身份执行命令，并写入不含命令内容的审计记录。
func (s *Service) MacRun(ctx context.Context, req MacRunRequest) (MacRunResult, error) {
	st := s.Store.Copy()
	if !st.Settings.MacControlEnabled {
		return MacRunResult{}, ErrMacControlDisabled
	}
	if !st.Settings.AllowMacRunCommand {
		return MacRunResult{}, errors.New("「允许执行 Mac 命令」未开启，请先在「应用」页启用")
	}
	cmdText := strings.TrimSpace(req.Command)
	if cmdText == "" {
		return MacRunResult{}, errors.New("命令不能为空")
	}
	timeout := req.TimeoutSec
	if timeout <= 0 {
		timeout = 30
	}
	if timeout > 120 {
		timeout = 120
	}

	home, _ := os.UserHomeDir()
	dir := strings.TrimSpace(req.Cwd)
	if dir == "" || !dirExists(dir) {
		dir = home
	}

	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	out := &cappedBuffer{}
	cmd := exec.CommandContext(runCtx, "/bin/zsh", "-lc", cmdText)
	cmd.Dir = dir
	cmd.Stdout = out
	cmd.Stderr = out
	cmd.Env = os.Environ()

	start := time.Now()
	err := cmd.Run()
	dur := time.Since(start)

	res := MacRunResult{Output: out.String(), DurationMS: dur.Milliseconds(), Truncated: out.truncated}
	audit := store.AuditEntry{
		ID:         fmt.Sprintf("a%d", time.Now().UnixNano()),
		At:         time.Now().UTC(),
		Action:     "mac.run_command",
		Digest:     digest(cmdText),
		DurationMS: dur.Milliseconds(),
		Allowed:    true,
	}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			res.ExitCode = ee.ExitCode()
		} else if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			res.ExitCode = 124
			res.Output += fmt.Sprintf("\n[超时] 命令超过 %d 秒被终止\n", timeout)
		} else {
			res.ExitCode = -1
			res.Output += "\n" + err.Error() + "\n"
		}
	}
	audit.ExitCode = res.ExitCode
	_ = s.Store.AddAudit(audit)
	return res, nil
}

// MacAudit 返回最近审计记录。
func (s *Service) MacAudit() []store.AuditEntry {
	return s.Store.Copy().Audit
}

func digest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// MacOpenPath 在 Finder 中打开工作区或指定文件，便于在宿主侧查看产物。
func (s *Service) MacOpenPath(rel string) error {
	st := s.Store.Copy()
	if !st.Settings.MacControlEnabled {
		return ErrMacControlDisabled
	}
	abs, err := s.Drv.Resolve(rel)
	if err != nil {
		return err
	}
	target := abs
	if info, err := os.Stat(abs); err == nil && !info.IsDir() {
		target = filepath.Dir(abs)
	}
	return exec.Command("/usr/bin/open", target).Start()
}

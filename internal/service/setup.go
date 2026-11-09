package service

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"anywhere/internal/tasks"
)

// setupEvent 对应 setup.sh --json 输出的 NDJSON 事件。
type setupEvent struct {
	Event   string `json:"event"`
	ID      string `json:"id"`
	Title   string `json:"title"`
	Status  string `json:"status"`
	Detail  string `json:"detail"`
	Level   string `json:"level"`
	Message string `json:"message"`
	Command string `json:"command"`
	OK      bool   `json:"ok"`
	Error   string `json:"error"`
}

// SetupScriptPath 定位一键配置脚本：优先环境变量，其次安装目录，最后源码树。
func SetupScriptPath() (string, error) {
	if v := strings.TrimSpace(os.Getenv("VMBRA_SETUP_SCRIPT")); v != "" {
		if fileExists(v) {
			return v, nil
		}
	}

	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates := []string{
			filepath.Join(dir, "scripts", "vmbra-setup", "setup.sh"),
			filepath.Join(dir, "..", "Resources", "scripts", "vmbra-setup", "setup.sh"),
		}
		for _, p := range candidates {
			if fileExists(p) {
				return filepath.Clean(p), nil
			}
		}
	}

	if wd, err := os.Getwd(); err == nil {
		p := filepath.Join(wd, "scripts", "vmbra-setup", "setup.sh")
		if fileExists(p) {
			return p, nil
		}
	}

	return "", fmt.Errorf("找不到环境配置脚本，请确认已安装 scripts/vmbra-setup/setup.sh")
}

// SetupPreflight 返回一键配置的前置条件，供界面在点按钮前判断。
func (s *Service) SetupPreflight() map[string]any {
	script, err := SetupScriptPath()
	out := map[string]any{
		"scriptPath":  script,
		"scriptReady": err == nil,
		"licensed":    s.LicenseUsable(),
	}
	if err != nil {
		out["error"] = err.Error()
	}

	host := s.Store.Copy().License.VPS.Host
	out["vpsHost"] = host
	out["vpsReady"] = host != ""
	return out
}

// StartSetup 提交一键配置任务，返回的任务可通过 /api/tasks/{id}/ws 跟踪进度。
func (s *Service) StartSetup(force bool) (*tasks.Item, error) {
	st := s.Store.Copy()
	if !force && !s.LicenseUsable() {
		return nil, fmt.Errorf("请先完成授权激活，再配置环境")
	}

	script, err := SetupScriptPath()
	if err != nil {
		return nil, err
	}

	server := s.LicenseServer()
	vpsHost := st.License.VPS.Host
	vpsPort := st.License.VPS.Port

	item := s.Tasks.Start("setup", "一键配置环境", func(ctx context.Context, it *tasks.Item) error {
		args := []string{script, "--json", "--skip-license", "--yes"}
		if vpsHost != "" {
			if vpsPort == 0 {
				vpsPort = 443
			}
			args = append(args, "--vps", fmt.Sprintf("%s:%d", vpsHost, vpsPort))
		}

		it.Infof("开始配置环境：%s", script)
		if vpsHost != "" {
			it.Infof("出口节点：%s:%d", vpsHost, vpsPort)
		}

		cmd := exec.CommandContext(ctx, "bash", args...)
		cmd.Env = append(os.Environ(),
			"VMBRA_LICENSE_SERVER="+server,
			"VMBRA_APP_VERSION="+s.AppVersion(),
		)

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return err
		}
		cmd.Stderr = it // 脚本的 stderr 直接进任务日志

		if err := cmd.Start(); err != nil {
			return fmt.Errorf("启动配置脚本失败：%w", err)
		}

		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)

		// 脚本固定 11 个阶段（license/clt/brew/git/node/vps/vm/guest/claude/profile/verify），
		// 跳过（--skip-vm 等）也算一步，因此分母恒定。
		const setupTotalSteps = 11
		var done int
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			if !strings.HasPrefix(line, "{") {
				it.Infof("%s", line)
				continue
			}

			var ev setupEvent
			if err := json.Unmarshal([]byte(line), &ev); err != nil {
				it.Infof("%s", line)
				continue
			}

			switch ev.Event {
			case "step":
				switch ev.Status {
				case "running":
					if ev.Title != "" {
						it.Infof("▸ %s", ev.Title)
					}
				case "ok", "skip", "warn", "fail":
					if done < setupTotalSteps {
						done++
					}
					// 机器可读进度行，前端据此更新进度条
					fmt.Fprintf(it, "@@step %d/%d %s %s\n", done, setupTotalSteps, ev.ID, ev.Status)
					if ev.Detail != "" {
						it.Infof("  %s %s", statusMark(ev.Status), ev.Detail)
					}
				}
			case "cmd":
				if ev.Command != "" {
					it.Infof("  $ %s", ev.Command)
				}
			case "log":
				if ev.Level == "warn" {
					it.Infof("  ! %s", ev.Message)
				}
			case "done":
				if !ev.OK {
					return fmt.Errorf("%s", firstNonEmptyStr(ev.Error, "配置未完成"))
				}
			}
		}

		if err := scanner.Err(); err != nil {
			return fmt.Errorf("读取脚本输出失败：%w", err)
		}
		return cmd.Wait()
	})

	return item, nil
}

func statusMark(status string) string {
	switch status {
	case "ok":
		return "✓"
	case "skip":
		return "–"
	case "warn":
		return "!"
	case "fail":
		return "✗"
	default:
		return "·"
	}
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func firstNonEmptyStr(list ...string) string {
	for _, v := range list {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

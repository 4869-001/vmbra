package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/4869-001/vmbra/internal/deb"
	"github.com/4869-001/vmbra/internal/sandbox"
	"github.com/4869-001/vmbra/internal/store"
	"github.com/4869-001/vmbra/internal/tasks"
)

// DesktopStatus 汇总图形桌面后端状态。
func (s *Service) DesktopStatus() map[string]any {
	st := s.Store.Copy()
	s.mu.Lock()
	state := s.desktopState
	msg := s.desktopMsg
	s.mu.Unlock()

	host := strings.TrimSpace(st.Desktop.VNCHost)
	port := st.Desktop.VNCPort
	if port == 0 {
		port = 5900
	}
	available := false
	reason := ""
	if host != "" {
		if err := probeTCP(net.JoinHostPort(host, strconv.Itoa(port)), 1500*time.Millisecond); err == nil {
			available = true
		} else {
			reason = fmt.Sprintf("无法连接 VNC 服务 %s:%d：%v", host, port, err)
		}
	} else if sandbox.CommandExists("limactl") {
		reason = "已检测到 Lima，但尚未填写 VNC 地址。请在「环境」页填写工作站的 VNC 地址（例如 127.0.0.1:5900）。"
	} else {
		reason = "本地进程隔离模式不包含图形栈。安装虚拟机后端（Lima/UTM）并启动 VNC 服务后，在「环境」页填写 VNC 地址即可接入图形桌面。"
	}
	return map[string]any{
		"state":     state,
		"available": available,
		"reason":    reason,
		"message":   msg,
		"vnc": map[string]any{
			"host":     host,
			"port":     port,
			"password": st.Desktop.Password,
			"scale":    st.Desktop.Scale,
		},
		"wsPath": "/api/apps/desktop/ws",
	}
}

// StartDesktop 启动图形桌面。
func (s *Service) StartDesktop() (map[string]any, error) {
	info := s.DesktopStatus()
	if ok, _ := info["available"].(bool); !ok {
		return nil, fmt.Errorf("%w：%v", ErrConflict, info["reason"])
	}
	s.mu.Lock()
	s.desktopState = "running"
	s.desktopMsg = "桌面会话已建立"
	s.mu.Unlock()
	return s.DesktopStatus(), nil
}

// StopDesktop 停止图形桌面。
func (s *Service) StopDesktop() map[string]any {
	s.mu.Lock()
	s.desktopState = "stopped"
	s.desktopMsg = "桌面会话已结束"
	s.mu.Unlock()
	return s.DesktopStatus()
}

// VNCTarget 返回桌面 WebSocket 需要桥接的 VNC 地址。
func (s *Service) VNCTarget() (string, error) {
	st := s.Store.Copy()
	host := strings.TrimSpace(st.Desktop.VNCHost)
	if host == "" {
		return "", errors.New("未配置 VNC 地址")
	}
	port := st.Desktop.VNCPort
	if port == 0 {
		port = 5900
	}
	return net.JoinHostPort(host, strconv.Itoa(port)), nil
}

// DesktopCredentials 返回 VNC 认证信息。
func (s *Service) DesktopCredentials() (string, string) {
	st := s.Store.Copy()
	return st.Desktop.Password, st.Desktop.Scale
}

// RootShellAvailable 说明当前隔离后端是否提供 root 终端。
func (s *Service) RootShellAvailable() (bool, string) {
	if sandbox.CommandExists("limactl") {
		return false, "已检测到 Lima，但 root 直通需要在「环境」中接入虚拟机实例后启用（当前版本尚未接入 lima 驱动）。"
	}
	return false, "本地进程隔离模式没有 root 映射：工作站以你的宿主用户身份运行，提权会破坏隔离边界。安装虚拟机后端（Lima/UTM）后可获得真正的 root。"
}

// OpenShell 创建一个隔离终端会话。
func (s *Service) OpenShell(kind string) (*sandbox.Session, error) {
	st := s.Store.Copy()
	if st.Current().State != "running" {
		return nil, fmt.Errorf("%w：工作站未运行，请先启动工作站", ErrConflict)
	}
	if kind == "" {
		kind = "user"
	}
	if kind != "user" && kind != "root" {
		return nil, fmt.Errorf("未知的终端类型：%s", kind)
	}
	return s.Drv.StartShell(kind, s.envMap(st), st.Settings.NetworkEnabled)
}

// RunScriptOptions 描述一次「运行脚本」请求。
type RunScriptOptions struct {
	Path       string
	Args       []string
	TimeoutSec int
}

var scriptInterpreters = map[string][]string{
	".sh":   {"/bin/sh"},
	".zsh":  {"/bin/zsh"},
	".bash": {"/bin/bash"},
	".py":   {"python3"},
	".js":   {"node"},
	".mjs":  {"node"},
	".rb":   {"ruby"},
	".pl":   {"perl"},
}

// RunScript 在隔离环境中运行工作区脚本，默认允许、无需确认；输出实时推送到任务日志。
func (s *Service) RunScript(ctx context.Context, opt RunScriptOptions) (*tasks.Item, error) {
	opt.Path = strings.TrimSpace(opt.Path)
	if opt.Path == "" {
		return nil, errors.New("请指定要运行的脚本路径")
	}
	abs, err := s.Drv.Resolve(opt.Path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("脚本不存在：%s", opt.Path)
	}
	if info.IsDir() {
		return nil, errors.New("目标是一个目录，无法作为脚本运行")
	}
	st := s.Store.Copy()
	if st.Current().State != "running" {
		return nil, fmt.Errorf("%w：工作站未运行，请先启动工作站", ErrConflict)
	}
	if len(opt.Args) > 32 {
		return nil, errors.New("参数过多（上限 32 个）")
	}
	for _, a := range opt.Args {
		if len(a) > 512 {
			return nil, errors.New("单个参数过长（上限 512 字节）")
		}
	}
	timeout := opt.TimeoutSec
	if timeout <= 0 {
		timeout = 300
	}
	if timeout > 3600 {
		timeout = 3600
	}

	argv := []string{}
	ext := strings.ToLower(filepath.Ext(abs))
	if runner, okok := scriptInterpreters[ext]; okok {
		argv = append(argv, runner...)
	} else if info.Mode()&0o111 != 0 {
		argv = append(argv, abs)
	} else {
		argv = append(argv, "/bin/sh")
	}
	argv = append(argv, abs)
	argv = append(argv, opt.Args...)
	display := filepath.Base(abs)
	return s.Tasks.Start("run-script", "运行 "+display, func(ctx context.Context, it *tasks.Item) error {
		it.Infof("脚本：%s", opt.Path)
		if len(opt.Args) > 0 {
			it.Infof("参数：%s", strings.Join(opt.Args, " "))
		}
		it.Infof("环境：%s · %s · 出网=%v", st.Locale.Lang, st.Locale.Timezone, st.Settings.NetworkEnabled)
		res := s.Drv.Exec(ctx, sandbox.ExecRequest{
			Argv:       argv,
			Env:        s.SystemEnv(st),
			Dir:        filepath.Dir(abs),
			Network:    st.Settings.NetworkEnabled,
			Timeout:    time.Duration(timeout) * time.Second,
			CPUSeconds: timeout,
			Stdout:     it,
			Stderr:     it,
		})
		it.Infof("退出码 %d，耗时 %s", res.ExitCode, res.Duration.Round(time.Millisecond))
		if res.TimedOut {
			return fmt.Errorf("脚本执行超时（%d 秒）", timeout)
		}
		if res.Err != nil && res.ExitCode != 0 {
			return res.Err
		}
		return nil
	}), nil
}

// InstallDebOptions 描述一次 .deb 安装请求。
type InstallDebOptions struct {
	Filename string
	Size     int64
	SavePath string
}

// InstallDeb 解析上传的 .deb 并解包到工作区。
// 本地进程隔离模式不执行 dpkg 安装，只把 data.tar.* 安全展开供查看与后续在虚拟机内安装。
func (s *Service) InstallDeb(ctx context.Context, opt InstallDebOptions) (*tasks.Item, error) {
	if !strings.HasSuffix(strings.ToLower(opt.Filename), ".deb") {
		return nil, errors.New("仅支持 .deb 安装包")
	}
	st := s.Store.Copy()
	if st.Current().State != "running" {
		return nil, fmt.Errorf("%w：工作站未运行", ErrConflict)
	}
	return s.Tasks.Start("deb-install", "安装应用 "+opt.Filename, func(ctx context.Context, it *tasks.Item) error {
		it.Infof("安装包：%s（%.1f MB）", opt.Filename, float64(opt.Size)/(1<<20))
		defer os.Remove(opt.SavePath) // 上传的临时包用完即清

		if sandbox.CommandExists("limactl") {
			it.Infof("检测到 Lima，但当前版本尚未接入 lima 驱动，改为在工作区解析并解包")
		}

		base := strings.TrimSuffix(filepath.Base(opt.Filename), ".deb")
		dir := filepath.Join(s.Drv.Workspace, "packages", base)
		if err := os.RemoveAll(dir); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Join(dir, "root"), 0o755); err != nil {
			return err
		}

		if ctl, err := deb.Control(ctx, opt.SavePath); err != nil {
			it.Infof("读取主控信息失败：%v", err)
		} else if ctl != "" {
			it.Infof("主控信息：\n%s", ctl)
		}

		rep, err := deb.ExtractData(ctx, opt.SavePath, filepath.Join(dir, "root"))
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			if errors.Is(err, deb.ErrUnsupportedCompression) {
				// 包本身合法，只是压缩格式不认识：保留工作区骨架并给出明确提示。
				it.Infof("未解包：%v", err)
				it.Infof("提示：可用 dpkg-deb -x 在虚拟机后端展开，或换用 gz/xz/zstd 压缩的包。")
				return nil
			}
			return fmt.Errorf("解包失败：%w", err)
		}
		it.Infof("已解包到 packages/%s/root：%d 个条目 / %d 个文件 / %.1f MB",
			base, rep.Entries, rep.Files, float64(rep.Bytes)/(1<<20))
		if rep.Skipped > 0 {
			it.Infof("已跳过 %d 个越界路径、符号链接或设备条目", rep.Skipped)
		}
		it.Infof("说明：本地进程隔离模式只解包不安装；安装到系统请在「环境」页配置虚拟机后端（Lima/UTM）后重试。")
		return nil
	}), nil
}

func probeTCP(addr string, timeout time.Duration) error {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return err
	}
	return conn.Close()
}

var _ = store.Desktop{}

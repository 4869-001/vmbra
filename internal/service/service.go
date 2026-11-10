// Package service 实现管理服务的业务逻辑：工作站生命周期、应用、文件、网络、维护、诊断等。
package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/4869-001/vmbra/internal/clash"
	"github.com/4869-001/vmbra/internal/config"
	"github.com/4869-001/vmbra/internal/dnsproxy"
	"github.com/4869-001/vmbra/internal/egress"
	"github.com/4869-001/vmbra/internal/forward"
	"github.com/4869-001/vmbra/internal/sandbox"
	"github.com/4869-001/vmbra/internal/store"
	"github.com/4869-001/vmbra/internal/tasks"
	"github.com/4869-001/vmbra/internal/vps"
)

// ErrConflict 表示当前状态不允许该操作。
var ErrConflict = errors.New("当前状态不允许该操作")

// Service 聚合管理服务的全部能力。
type Service struct {
	Cfg   *config.Config
	Store *store.Store
	Drv   *sandbox.Driver
	Tasks *tasks.Manager
	DNS   *dnsproxy.Server
	Fwd   *forward.Manager

	// VPSTunnel 让工作站借用远端 VPS 的公网出入口。
	VPSTunnel *vps.Tunnel

	// Clash 托管本机 mihomo 内核，承载 Clash 订阅导入后的出口链路。
	Clash *clash.Core

	// Gate 是出网闸门：检查通过才有租约，失败或过期立即断网。
	Gate *egress.Gate

	startedAt time.Time

	mu           sync.Mutex
	desktopState string // stopped | running
	desktopMsg   string
	dnsErr       string
}

// New 构造业务服务。
func New(cfg *config.Config, st *store.Store, drv *sandbox.Driver, tm *tasks.Manager) *Service {
	d := st.Copy().DNS
	svc := &Service{
		Cfg:          cfg,
		Store:        st,
		Drv:          drv,
		Tasks:        tm,
		DNS:          dnsproxy.New(d.Servers),
		Fwd:          forward.New(),
		VPSTunnel:    vps.New(),
		Clash:        clash.NewCore(cfg.DataDir),
		Gate:         egress.New(egressTTL),
		startedAt:    time.Now(),
		desktopState: "stopped",
	}
	// 若开启了「跟随 VPS 地区」，启动时立即把环境对齐一次，避免沿用旧值。
	svc.syncVPSLocale()
	return svc
}

// Backend 描述一种可选隔离后端。
type Backend struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Available bool   `json:"available"`
	Hint      string `json:"hint"`
}

func detectBackends() []Backend {
	probe := func(bin, kind, hint string) Backend {
		return Backend{Name: bin, Kind: kind, Available: sandbox.CommandExists(bin), Hint: hint}
	}
	return []Backend{
		probe("limactl", "vm", "Lima 虚拟机后端（推荐，提供完整 Linux 用户态与图形栈）"),
		probe("colima", "vm", "Colima 容器/虚拟机后端"),
		probe("docker", "container", "Docker 容器后端"),
		{"sandbox-exec", "process", true, "macOS 进程级策略隔离（内置，无需安装）"},
	}
}

// Engine 描述隔离内核能力。
type Engine struct {
	Sandbox      bool      `json:"sandbox"`
	SandboxError string    `json:"sandboxError"`
	Driver       string    `json:"driver"`
	Platform     string    `json:"platform"`
	Backends     []Backend `json:"backends"`
	PolicyPath   string    `json:"policyPath"`
}

// Status 是控制台首屏需要的汇总信息。
func (s *Service) Status() map[string]any {
	st := s.Store.Copy()
	s.mu.Lock()
	desktop := s.desktopState
	s.mu.Unlock()
	return map[string]any{
		"version":   config.Version,
		"uptimeSec": int(time.Since(s.startedAt).Seconds()),
		"daemon": map[string]any{
			"pid":       os.Getpid(),
			"listen":    s.Cfg.Listen,
			"dataDir":   s.Cfg.DataDir,
			"startedAt": s.startedAt.UTC(),
		},
		"engine": Engine{
			Sandbox:      s.Drv.SandboxAvailable(),
			SandboxError: s.Drv.SandboxError(),
			Driver:       st.Current().Driver,
			Platform:     runtime.GOOS + "/" + runtime.GOARCH,
			Backends:     detectBackends(),
			PolicyPath:   s.Drv.PolicyPath(st.Settings.NetworkEnabled),
		},
		"workstation":  s.WorkstationInfo(),
		"settings":     st.Settings,
		"desktop":      s.DesktopStatus(),
		"mac":          s.MacStatus(),
		"tasks":        s.TaskSummary(),
		"desktopState": desktop,
	}
}

// WorkstationInfo 汇总工作站运行信息。
func (s *Service) WorkstationInfo() map[string]any {
	st := s.Store.Copy()
	ws := st.Current()
	workspaceSize := sandbox.DirSize(s.Drv.Workspace)
	homeSize := sandbox.DirSize(s.Drv.Home)
	uptime := int64(0)
	if ws.StartedAt != nil && ws.State == "running" {
		uptime = int64(time.Since(*ws.StartedAt).Seconds())
	}
	return map[string]any{
		"id":             ws.ID,
		"name":           ws.Name,
		"state":          ws.State,
		"driver":         ws.Driver,
		"image":          ws.Image,
		"cpus":           ws.CPUs,
		"memoryMB":       ws.MemoryMB,
		"diskGB":         ws.DiskGB,
		"createdAt":      ws.CreatedAt,
		"startedAt":      ws.StartedAt,
		"uptimeSec":      uptime,
		"message":        ws.Message,
		"sessions":       s.Drv.SessionCount(),
		"workspaceBytes": workspaceSize,
		"homeBytes":      homeSize,
		"workspacePath":  s.Drv.Workspace,
		"homePath":       s.Drv.Home,
		"network":        st.Settings.NetworkEnabled,
		"networkLabel":   egressLabel(ws.Network, ws.UseVPS, s.VPSTunnel != nil && s.VPSTunnel.Running()),
		"lang":           ws.Lang,
		"timeZone":       ws.TimeZone,
		"useVps":         ws.UseVPS,
		"autoStart":      st.Settings.AutoStart,
	}
}

// TaskSummary 返回任务统计。
func (s *Service) TaskSummary() map[string]any {
	list := s.Tasks.List()
	running, queued, failed := 0, 0, 0
	for _, t := range list {
		switch t.Status {
		case "running":
			running++
		case "queued":
			queued++
		case "failed":
			failed++
		}
	}
	return map[string]any{"total": len(list), "running": running, "queued": queued, "failed": failed}
}

func (s *Service) setWorkstation(fn func(*store.Workstation)) {
	_ = s.Store.Update(func(st *store.State) {
		if w := st.Active(); w != nil {
			fn(w)
		}
	})
}

// StartWorkstation 启动工作站（同步完成目录初始化与会话准备）。
func (s *Service) StartWorkstation(ctx context.Context) (map[string]any, error) {
	ws := s.Store.Copy().Current()
	if ws.State == "running" {
		return s.WorkstationInfo(), nil
	}
	s.setWorkstation(func(w *store.Workstation) {
		w.State = "starting"
		w.Message = "正在初始化隔离环境"
	})

	st := s.Store.Copy()
	// 启动前落盘统一环境文件，保证首个终端会话即拿到正确的语言/时区/DNS。
	_ = s.WriteShellEnv()
	env := s.envMap(st)
	_, errOut, res := s.Drv.RunOutput(ctx, sandbox.ExecRequest{
		Argv: []string{"/bin/sh", "-c", strings.Join([]string{
			"set -e",
			"mkdir -p \"$HOME/projects\" \"$HOME/scripts\" \"$HOME/.config\"",
			"printf '{\"managed_by\":\"vmbra\",\"version\":\"%s\"}\\n' > \"$HOME/.vmbra-init\"",
			"date -u +%Y-%m-%dT%H:%M:%SZ > \"$HOME/.boot-time\"",
			"printf 'boot ok\\n'",
		}, "; ")},
		Env:        env,
		Network:    st.Settings.NetworkEnabled,
		Timeout:    20 * time.Second,
		CPUSeconds: 20,
	})
	if res.Err != nil {
		s.setWorkstation(func(w *store.Workstation) {
			w.State = "error"
			w.Message = "启动失败：" + res.Err.Error()
		})
		return nil, fmt.Errorf("启动工作站失败：%v %s", res.Err, strings.TrimSpace(errOut))
	}

	now := time.Now().UTC()
	s.setWorkstation(func(w *store.Workstation) {
		w.State = "running"
		w.StartedAt = &now
		w.Message = "隔离环境已就绪"
	})
	// 端口转发跟随工作站生命周期：启动后按已启用规则建立监听。
	s.syncForwards()
	return s.WorkstationInfo(), nil
}

// StopWorkstation 停止工作站并回收全部会话。
func (s *Service) StopWorkstation() (map[string]any, error) {
	s.setWorkstation(func(w *store.Workstation) {
		w.State = "stopping"
		w.Message = "正在停止"
	})
	s.Drv.CloseAll()
	s.Fwd.StopAll()
	s.mu.Lock()
	s.desktopState = "stopped"
	s.desktopMsg = ""
	s.mu.Unlock()
	s.setWorkstation(func(w *store.Workstation) {
		w.State = "stopped"
		w.StartedAt = nil
		w.Message = "已停止"
	})
	return s.WorkstationInfo(), nil
}

// RestartWorkstation 重启工作站。
func (s *Service) RestartWorkstation(ctx context.Context) (map[string]any, error) {
	if _, err := s.StopWorkstation(); err != nil {
		return nil, err
	}
	return s.StartWorkstation(ctx)
}

// UpdateSpecs 调整工作站规格（仅停止状态可改）。
func (s *Service) UpdateSpecs(ctx context.Context, patch map[string]any) (map[string]any, error) {
	ws := s.Store.Copy().Current()
	if ws.State == "running" || ws.State == "starting" {
		return nil, fmt.Errorf("%w：请先停止工作站再修改规格", ErrConflict)
	}
	_ = s.Store.Update(func(st *store.State) {
		w := st.Active()
		if v, ok := patch["name"].(string); ok && strings.TrimSpace(v) != "" {
			w.Name = strings.TrimSpace(v)
		}
		if v, ok := patch["cpus"].(float64); ok && v >= 1 && v <= 32 {
			w.CPUs = int(v)
		}
		if v, ok := patch["memoryMB"].(float64); ok && v >= 512 && v <= 65536 {
			w.MemoryMB = int(v)
		}
		if v, ok := patch["diskGB"].(float64); ok && v >= 5 && v <= 512 {
			w.DiskGB = int(v)
		}
		if v, ok := patch["image"].(string); ok && strings.TrimSpace(v) != "" {
			w.Image = strings.TrimSpace(v)
		}
		w.Message = "规格已更新，下次启动生效"
	})
	return s.WorkstationInfo(), nil
}

// envMap 返回工作站统一运行时环境（语言/时区/终端/代理/DNS 由 SystemEnv 统一装配）。
func (s *Service) envMap(st store.State) map[string]string {
	return s.SystemEnv(st)
}

// WorkspacePath 返回工作区绝对路径。
func (s *Service) WorkspacePath() string { return s.Drv.Workspace }

// SnapshotPath 返回快照文件路径。
func (s *Service) SnapshotPath(id string) string {
	return filepath.Join(s.Cfg.SnapshotDir(), id+".tar.gz")
}

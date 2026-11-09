package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"anywhere/internal/importlink"
	"anywhere/internal/store"
	"anywhere/internal/vps"
)

// ImportOptions 控制一次开通链接导入要执行的动作。
// 默认只写入配置（不安装、不建隧道），由界面上的勾选项逐步打开。
type ImportOptions struct {
	// Tools 覆盖链接里的工具选择；为空时用链接内容（再为空则用默认清单）。
	Tools []string
	// InstallKey 用链接里的口令登录 VPS，把本机公钥写入 authorized_keys。
	InstallKey bool
	// StartTunnel 导入后立即建立出口隧道。
	StartTunnel bool
	// RunSetup 导入后立即提交「一键配置环境」任务（按勾选的工具安装）。
	RunSetup bool
	// SkipVPS 不写入 VPS 配置，只保存工具选择。
	SkipVPS bool
}

// ImportStep 是导入过程中的一步结果，直接用于界面回显。
type ImportStep struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Status string `json:"status"` // ok | skip | fail
	Detail string `json:"detail,omitempty"`
}

// PreviewImport 解析链接并给出安装清单与前置条件，不修改任何状态。
func (s *Service) PreviewImport(raw string) (map[string]any, error) {
	p, err := importlink.Decode(raw)
	if err != nil {
		return nil, err
	}
	st := s.Store.Copy()

	pick := normalizeToolIDs(p.Tools)
	if len(pick) == 0 {
		pick = importlink.DefaultTools()
	}
	selected := map[string]bool{}
	for _, id := range pick {
		selected[id] = true
	}

	tools := make([]map[string]any, 0, len(importlink.Tools))
	for _, t := range importlink.Tools {
		installed, version := probeTool(t.ID)
		tools = append(tools, map[string]any{
			"id":        t.ID,
			"label":     t.Label,
			"desc":      t.Desc,
			"size":      t.Size,
			"needVm":    t.NeedVM,
			"selected":  selected[t.ID],
			"installed": installed,
			"version":   version,
		})
	}

	script, scriptErr := SetupScriptPath()

	warnings := make([]string, 0, 3)
	if p.VPS.Password != "" {
		warnings = append(warnings, "链接携带 SSH 口令，属于敏感凭据：只发给使用者本人，导入完成后建议在面板更换口令。")
	}
	if st.VPS.Host != "" && (st.VPS.Host != p.VPS.Host || st.VPS.Port != p.VPS.Port) {
		warnings = append(warnings, fmt.Sprintf("导入会覆盖现有出口节点 %s:%d。", st.VPS.Host, st.VPS.Port))
	}
	if !s.LicenseUsable() {
		warnings = append(warnings, "本机尚未激活授权：可以先导入配置，真正安装前需要完成激活。")
	}

	return map[string]any{
		"payload": map[string]any{
			"name":        firstNonEmptyStr(p.Name, "Vmbra 工作站"),
			"note":        p.Note,
			"host":        p.VPS.Host,
			"port":        p.VPS.Port,
			"user":        p.VPS.User,
			"region":      p.VPS.Region,
			"lang":        p.VPS.Lang,
			"timeZone":    p.VPS.TimeZone,
			"hasPassword": p.VPS.Password != "",
			"createdAt":   p.CreatedAt,
		},
		"tools": tools,
		"preflight": map[string]any{
			"licensed":    s.LicenseUsable(),
			"keyReady":    s.vpsKeyReady(),
			"currentVps":  map[string]any{"host": st.VPS.Host, "port": st.VPS.Port, "user": st.VPS.User},
			"scriptReady": scriptErr == nil,
			"scriptPath":  script,
		},
		"warnings": warnings,
	}, nil
}

// ApplyImport 按选项落配置、装公钥、建隧道，必要时提交安装任务。
func (s *Service) ApplyImport(ctx context.Context, raw string, opts ImportOptions) (map[string]any, error) {
	p, err := importlink.Decode(raw)
	if err != nil {
		return nil, err
	}

	tools := normalizeToolIDs(opts.Tools)
	if len(tools) == 0 {
		tools = normalizeToolIDs(p.Tools)
	}
	if len(tools) == 0 {
		tools = importlink.DefaultTools()
	}
	wantVM := containsStr(tools, "vm")

	if err := s.Store.Update(func(st *store.State) {
		st.Setup = store.SetupSelection{Tools: tools, VM: wantVM, At: time.Now()}
	}); err != nil {
		return nil, err
	}

	steps := make([]ImportStep, 0, 6)
	steps = append(steps, ImportStep{ID: "select", Label: "保存安装清单", Status: "ok", Detail: strings.Join(tools, "、")})
	out := map[string]any{}

	if !opts.SkipVPS {
		patch := map[string]any{
			"host": p.VPS.Host,
			"port": float64(p.VPS.Port),
			"user": p.VPS.User,
		}
		if p.VPS.Lang != "" {
			patch["lang"] = p.VPS.Lang
		}
		if p.VPS.TimeZone != "" {
			patch["timeZone"] = p.VPS.TimeZone
		}
		if _, err := s.UpdateVPS(patch); err != nil {
			steps = append(steps, ImportStep{ID: "vps", Label: "写入出口节点", Status: "fail", Detail: err.Error()})
			out["steps"] = steps
			return out, err
		}
		steps = append(steps, ImportStep{
			ID: "vps", Label: "写入出口节点", Status: "ok",
			Detail: fmt.Sprintf("%s@%s:%d", p.VPS.User, p.VPS.Host, p.VPS.Port),
		})

		pub, err := s.EnsureVPSKey()
		if err != nil {
			steps = append(steps, ImportStep{ID: "key", Label: "准备本机密钥", Status: "fail", Detail: err.Error()})
			out["steps"] = steps
			return out, err
		}
		steps = append(steps, ImportStep{ID: "key", Label: "准备本机密钥", Status: "ok", Detail: s.vpsKeyPath(s.Store.Copy().VPS)})

		switch {
		case opts.InstallKey && p.VPS.Password != "":
			kh := filepath.Join(s.Cfg.DataDir, "vps", "known_hosts")
			installCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			keyErr := vps.InstallKey(installCtx, p.VPS.Host, p.VPS.Port, p.VPS.User, p.VPS.Password, pub, kh)
			cancel()
			if keyErr != nil {
				steps = append(steps, ImportStep{ID: "install-key", Label: "写入 VPS 公钥", Status: "fail", Detail: keyErr.Error()})
				if opts.StartTunnel {
					out["steps"] = steps
					return out, keyErr
				}
			} else {
				steps = append(steps, ImportStep{ID: "install-key", Label: "写入 VPS 公钥", Status: "ok", Detail: "已追加到 authorized_keys"})
			}
		case p.VPS.Password == "":
			steps = append(steps, ImportStep{ID: "install-key", Label: "写入 VPS 公钥", Status: "skip", Detail: "链接不含口令，请在「网络 → VPS 出入口」按提示手动安装公钥"})
		default:
			steps = append(steps, ImportStep{ID: "install-key", Label: "写入 VPS 公钥", Status: "skip", Detail: "未选择自动安装公钥"})
		}

		if opts.StartTunnel {
			if _, err := s.StartVPS(ctx); err != nil {
				steps = append(steps, ImportStep{ID: "tunnel", Label: "建立出口隧道", Status: "fail", Detail: err.Error()})
				out["steps"] = steps
				out["vps"] = s.VPSInfo()
				return out, err
			}
			steps = append(steps, ImportStep{ID: "tunnel", Label: "建立出口隧道", Status: "ok", Detail: "SOCKS5 已就绪"})
		}
	}

	if opts.RunSetup {
		item, err := s.StartSetup(false)
		if err != nil {
			steps = append(steps, ImportStep{ID: "setup", Label: "提交安装任务", Status: "fail", Detail: err.Error()})
			out["steps"] = steps
			out["vps"] = s.VPSInfo()
			return out, err
		}
		taskID := item.Record().ID
		steps = append(steps, ImportStep{ID: "setup", Label: "提交安装任务", Status: "ok", Detail: taskID})
		out["taskId"] = taskID
	} else {
		steps = append(steps, ImportStep{ID: "setup", Label: "提交安装任务", Status: "skip", Detail: "未选择立即安装，可稍后在设置页执行"})
	}

	out["steps"] = steps
	out["tools"] = tools
	out["vps"] = s.VPSInfo()
	out["setup"] = s.SetupPreflight()
	return out, nil
}

// SetSetupSelection 更新下一次「一键配置环境」的安装清单。
func (s *Service) SetSetupSelection(tools []string, vm bool) error {
	norm := normalizeToolIDs(tools)
	if containsStr(norm, "vm") {
		vm = true
	}
	return s.Store.Update(func(st *store.State) {
		st.Setup = store.SetupSelection{Tools: norm, VM: vm, At: time.Now()}
	})
}

// ImportLink 用当前配置反向生成一条链接（不含口令），便于分享配置或另存为模板。
func (s *Service) ImportLink() (map[string]any, error) {
	st := s.Store.Copy()
	if strings.TrimSpace(st.VPS.Host) == "" {
		return nil, errors.New("当前还没有配置出口节点，请先在「网络 → VPS 出入口」填写")
	}
	tools := normalizeToolIDs(st.Setup.Tools)
	if len(tools) == 0 {
		tools = importlink.DefaultTools()
	}
	p := importlink.Payload{
		V:     importlink.Version,
		Name:  "Vmbra 工作站",
		VPS:   importlink.VPSSpec{Host: st.VPS.Host, Port: st.VPS.Port, User: st.VPS.User, Lang: st.VPS.Lang, TimeZone: st.VPS.TimeZone},
		Tools: tools,
	}
	link, err := importlink.Encode(p)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"link":       link,
		"consoleUrl": importlink.ConsoleURL("http://"+s.Cfg.Listen, link),
		"payload":    p,
	}, nil
}

// ---- 内部工具 ----

func (s *Service) vpsKeyReady() bool {
	st := s.Store.Copy()
	_, err := os.Stat(s.vpsKeyPath(st.VPS) + ".pub")
	return err == nil
}

// probeTool 探测某个工具是否已安装，返回版本摘要。
func probeTool(id string) (bool, string) {
	switch id {
	case "chrome":
		paths := []string{
			"/Applications/Google Chrome.app",
			filepath.Join(os.Getenv("HOME"), "Applications", "Google Chrome.app"),
		}
		for _, p := range paths {
			if _, err := os.Stat(p); err == nil {
				return true, "已安装"
			}
		}
		return false, ""

	case "node":
		if p := lookPathWith("node", "~/.vmbra/toolchain/node/bin"); p != "" {
			return true, strings.TrimSpace(runShort(p, "-v"))
		}
		return false, ""

	case "git":
		if p := lookPathWith("git", ""); p != "" {
			return true, strings.TrimSpace(runShort(p, "--version"))
		}
		return false, ""

	case "claude":
		if p := lookPathWith("claude", "~/.vmbra/npm-global/bin", "~/.vmbra/toolchain/node/bin"); p != "" {
			return true, strings.TrimSpace(runShort(p, "--version"))
		}
		return false, ""

	case "vm":
		if p := lookPathWith("limactl", ""); p != "" {
			return true, strings.TrimSpace(runShort(p, "--version"))
		}
		return false, ""
	}
	return false, ""
}

// lookPathWith 在 PATH 之外再查几个约定目录。
func lookPathWith(name string, dirs ...string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	for _, dir := range dirs {
		dir = expandTilde(strings.TrimSpace(dir))
		if dir == "" {
			continue
		}
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p
		}
	}
	return ""
}

func runShort(bin string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, args...).Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// normalizeToolIDs 过滤未知项、去重并保持目录顺序。
func normalizeToolIDs(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	want := map[string]bool{}
	for _, id := range ids {
		id = strings.ToLower(strings.TrimSpace(id))
		if _, ok := importlink.ToolByID(id); ok {
			want[id] = true
		}
	}
	out := make([]string, 0, len(want))
	for _, t := range importlink.Tools {
		if want[t.ID] {
			out = append(out, t.ID)
		}
	}
	return out
}

func containsStr(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}

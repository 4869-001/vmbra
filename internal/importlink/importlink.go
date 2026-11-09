// Package importlink 定义「开通链接」的格式：把 VPS 出入口信息与工具清单
// 编码成一条可以直接导入 Vmbra 的链接（vmbra://import/<base64url>）。
//
// 同一条载荷也能以 .vmbrapkg（纯 JSON）落盘，供离线交付或人工核对。
// 链接包含 SSH 口令时属于敏感凭据：只在交付双方之间传递，导入后客户端会
// 立即用它换取本机密钥登录，口令不写入状态文件。
package importlink

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

const (
	// Scheme 是桌面程序注册的 URL scheme。
	Scheme = "vmbra"
	// LinkPrefix 是完整链接的前缀。
	LinkPrefix = "vmbra://import/"
	// Version 是当前载荷版本，未来不兼容变更时递增。
	Version = 1
)

// ToolSpec 描述一个可选安装项。ID 与 setup.sh 的 --tools 取值一致。
type ToolSpec struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Desc    string `json:"desc"`
	Size    string `json:"size"`
	Default bool   `json:"default"`
	// NeedVM 表示该工具依赖隔离虚拟机（lima）后端。
	NeedVM bool `json:"needVm,omitempty"`
}

// Tools 是导入界面展示的安装清单，顺序即展示顺序。
var Tools = []ToolSpec{
	{ID: "chrome", Label: "Google Chrome", Desc: "受控浏览器、网页调试与独立 profile（时区/语言/出口对齐）", Size: "约 180 MB", Default: true},
	{ID: "node", Label: "Node.js LTS", Desc: "Claude Code、npm 脚本与前端工具链的运行时", Size: "约 50 MB", Default: true},
	{ID: "git", Label: "Git", Desc: "代码拉取、版本管理与提交", Size: "约 60 MB", Default: true},
	{ID: "claude", Label: "Claude Code CLI", Desc: "终端内的 AI 编程助手，安装到用户级 npm 全局目录", Size: "约 30 MB", Default: true},
	{ID: "vm", Label: "Linux 虚拟机终端", Desc: "lima + Ubuntu 镜像，用于需要 root 或内核级隔离的场景", Size: "约 1 GB", Default: false, NeedVM: true},
}

// ToolByID 查找工具定义。
func ToolByID(id string) (ToolSpec, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, t := range Tools {
		if t.ID == id {
			return t, true
		}
	}
	return ToolSpec{}, false
}

// ToolIDs 返回全部工具 ID（按展示顺序）。
func ToolIDs() []string {
	out := make([]string, 0, len(Tools))
	for _, t := range Tools {
		out = append(out, t.ID)
	}
	return out
}

// DefaultTools 返回默认勾选的工具 ID。
func DefaultTools() []string {
	out := make([]string, 0, len(Tools))
	for _, t := range Tools {
		if t.Default {
			out = append(out, t.ID)
		}
	}
	return out
}

// VPSSpec 是开通链接里携带的出口节点信息。
type VPSSpec struct {
	Host     string `json:"host"`
	Port     int    `json:"port,omitempty"`
	User     string `json:"user,omitempty"`
	Password string `json:"password,omitempty"`
	Lang     string `json:"lang,omitempty"`
	TimeZone string `json:"timezone,omitempty"`
	Region   string `json:"region,omitempty"`
}

// Payload 是一条开通链接的完整内容。
type Payload struct {
	V         int       `json:"v"`
	Name      string    `json:"name,omitempty"`
	Note      string    `json:"note,omitempty"`
	VPS       VPSSpec   `json:"vps"`
	Tools     []string  `json:"tools,omitempty"`
	CreatedAt time.Time `json:"createdAt,omitempty"`
}

var (
	hostPattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9._-]{0,252})$`)
	userPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,32}$`)
)

// Normalize 补齐默认值并去重。
func (p *Payload) Normalize() {
	if p.V == 0 {
		p.V = Version
	}
	if p.VPS.Port == 0 {
		p.VPS.Port = 22
	}
	if p.VPS.User == "" {
		p.VPS.User = "root"
	}
	p.VPS.Host = strings.TrimSpace(p.VPS.Host)
	p.VPS.User = strings.TrimSpace(p.VPS.User)
	p.Name = strings.TrimSpace(p.Name)

	seen := map[string]bool{}
	out := make([]string, 0, len(p.Tools))
	for _, t := range p.Tools {
		id := strings.ToLower(strings.TrimSpace(t))
		if _, ok := ToolByID(id); !ok || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	p.Tools = out
}

// Validate 校验载荷是否可执行；错误信息直接面向使用者。
func (p Payload) Validate() error {
	if p.V > Version {
		return fmt.Errorf("链接版本 %d 高于当前客户端支持的 %d，请先升级 Vmbra", p.V, Version)
	}
	if !hostPattern.MatchString(p.VPS.Host) {
		return errors.New("VPS 主机地址不合法")
	}
	if !userPattern.MatchString(p.VPS.User) {
		return errors.New("VPS 登录用户不合法")
	}
	if p.VPS.Port < 1 || p.VPS.Port > 65535 {
		return errors.New("SSH 端口需在 1-65535 之间")
	}
	if len(p.VPS.Password) > 128 {
		return errors.New("口令过长")
	}
	for _, t := range p.Tools {
		if _, ok := ToolByID(t); !ok {
			return fmt.Errorf("未知工具：%s", t)
		}
	}
	if p.VPS.Lang != "" && !regexp.MustCompile(`^[a-z]{2,3}([-_][A-Za-z]{2,4})?$`).MatchString(p.VPS.Lang) {
		return fmt.Errorf("语言代码不合法：%s", p.VPS.Lang)
	}
	return nil
}

// Encode 把载荷编码成 vmbra://import/... 链接。
func Encode(p Payload) (string, error) {
	p.Normalize()
	if err := p.Validate(); err != nil {
		return "", err
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now().UTC()
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	return LinkPrefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

// Decode 解析链接、控制台 URL、裸载荷或 JSON 文本。
func Decode(input string) (Payload, error) {
	var p Payload
	s := strings.TrimSpace(input)
	if s == "" {
		return p, errors.New("链接为空")
	}

	switch {
	case strings.HasPrefix(s, "{"):
		if err := json.Unmarshal([]byte(s), &p); err != nil {
			return p, fmt.Errorf("JSON 解析失败：%w", err)
		}
	case strings.Contains(s, "import="):
		idx := strings.Index(s, "import=")
		v := s[idx+len("import="):]
		if cut := strings.IndexAny(v, "&#"); cut >= 0 {
			v = v[:cut]
		}
		return Decode(v)
	default:
		body := s
		if i := strings.Index(body, "//import/"); i >= 0 {
			body = body[i+len("//import/"):]
		} else if strings.HasPrefix(body, LinkPrefix) {
			body = strings.TrimPrefix(body, LinkPrefix)
		}
		body = strings.TrimSuffix(strings.TrimSpace(body), "/")
		if cut := strings.IndexAny(body, "?#"); cut >= 0 {
			body = body[:cut]
		}
		raw, err := base64.RawURLEncoding.DecodeString(body)
		if err != nil {
			// 兼容标准 base64（带 +/= 的复制粘贴）
			raw, err = base64.URLEncoding.DecodeString(body)
			if err != nil {
				return p, errors.New("无法识别的配置链接")
			}
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return p, fmt.Errorf("链接内容损坏：%w", err)
		}
	}

	p.Normalize()
	if err := p.Validate(); err != nil {
		return p, err
	}
	return p, nil
}

// ConsoleURL 生成控制台导入地址：在本机打开即弹窗预览。
func ConsoleURL(base, link string) string {
	link = strings.Replace(link, LinkPrefix, "", 1)
	base = strings.TrimRight(base, "/")
	return base + "/#/settings?import=" + link
}

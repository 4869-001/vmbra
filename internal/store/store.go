// Package store 维护管理服务的持久化状态（工作站、设置、网络、环境、快照、审计等）。
package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Workstation 描述一台本地隔离工作站（一套独立环境）。
//
// 每台工作站有自己的根目录（工作区/家目录/临时目录/策略文件）和运行参数，
// 同一时刻只有 ActiveID 指向的那台处于激活状态——本地进程隔离共享内核与网络栈，
// 同时跑多台不会带来额外隔离，只会让「当前是谁」变得含混。
type Workstation struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Root      string     `json:"root"`   // 数据目录下的相对目录名
	State     string     `json:"state"`  // stopped | starting | running | stopping | error
	Driver    string     `json:"driver"` // local（进程级隔离）| lima（虚拟机）
	Image     string     `json:"image"`
	CPUs      int        `json:"cpus"`
	MemoryMB  int        `json:"memoryMB"`
	DiskGB    int        `json:"diskGB"`
	Network   bool       `json:"network"`   // 该环境是否出网
	Lang      string     `json:"lang"`      // 该环境的语言，如 en_US.UTF-8
	TimeZone  string     `json:"timeZone"`  // 该环境的时区
	ProxyMode string     `json:"proxyMode"` // off | system | manual
	UseVPS    bool       `json:"useVps"`    // 出口是否走 VPS 隧道
	CreatedAt time.Time  `json:"createdAt"`
	StartedAt *time.Time `json:"startedAt,omitempty"`
	Message   string     `json:"message,omitempty"`
}

// Settings 是控制台可见的开关集合。
type Settings struct {
	MacControlEnabled  bool   `json:"macControlEnabled"`
	AllowMacRunCommand bool   `json:"allowMacRunCommand"`
	AutoStart          bool   `json:"autoStart"`
	NetworkEnabled     bool   `json:"networkEnabled"`
	Theme              string `json:"theme"`
}

// PortForward 是一条主机到工作站的端口转发规则。
type PortForward struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	HostPort  int    `json:"hostPort"`
	GuestPort int    `json:"guestPort"`
	Proto     string `json:"proto"`
	Enabled   bool   `json:"enabled"`
}

// Proxy 是工作站出网代理配置。
type Proxy struct {
	Mode     string `json:"mode"` // off | system | manual
	HTTP     string `json:"http"`
	HTTPS    string `json:"https"`
	NoProxy  string `json:"noProxy"`
	DNSPatch bool   `json:"dnsPatch"`
}

// ProxyCore 记录本机 Clash 内核（mihomo）的出口状态与探测到的地区信息。
// 它是「导出订阅 → 自动对齐语言/时区」这条链路的落盘结果，
// 界面只读它就能回答「我现在从哪出网、环境跟出口一致吗」。
type ProxyCore struct {
	Running     bool      `json:"running"`
	NodeID      string    `json:"nodeId"`
	NodeName    string    `json:"nodeName"`
	NodeType    string    `json:"nodeType"`
	ExitIP      string    `json:"exitIp"`
	Country     string    `json:"country"`
	CountryCode string    `json:"countryCode"`
	City        string    `json:"city"`
	TimeZone    string    `json:"timeZone"`
	HTTPProxy   string    `json:"httpProxy"`
	SOCKSProxy  string    `json:"socksProxy"`
	CoreBinary  string    `json:"coreBinary"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// EnvVar 是注入到工作站会话的环境变量。
type EnvVar struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Desktop 是图形桌面（VNC）后端配置。本地进程模式无图形栈时需外接 VNC 服务。
type Desktop struct {
	Enabled  bool   `json:"enabled"`
	VNCHost  string `json:"vncHost"`
	VNCPort  int    `json:"vncPort"`
	Password string `json:"password"`
	Scale    string `json:"scale"`
}

// Locale 是工作站的系统语言文字、编码与时区，决定终端与脚本的运行时语言环境。
// 它与宿主 Mac 的设置无关；开启 FollowVPS 后自动对齐 VPS 所在地区。
type Locale struct {
	Lang      string `json:"lang"`      // 例如 en_US.UTF-8
	Encoding  string `json:"encoding"`  // 例如 UTF-8
	Timezone  string `json:"timezone"`  // 例如 America/Los_Angeles
	FollowVPS bool   `json:"followVps"` // 跟随 VPS 地区（VPS.Lang / VPS.TimeZone）
}

// DNS 是工作站的域名解析配置。系统模式下沿用宿主解析器；自定义模式下由本地转发器
// 把查询转发到指定上游，两者都走同一条出口链路。
type DNS struct {
	Enabled bool     `json:"enabled"` // 是否启用本地 DNS 转发器
	Mode    string   `json:"mode"`    // system | custom
	Servers []string `json:"servers"` // 上游服务器，例如 1.1.1.1:53
	Search  []string `json:"search"`  // 搜索域，例如 .local
	Port    int      `json:"port"`    // 本地转发器监听端口（127.0.0.1）
}

// VPSForward 是一条 VPS 入站规则：VPS 上的公网端口回落到工作站本地端口。
type VPSForward struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	RemotePort int    `json:"remotePort"`
	LocalPort  int    `json:"localPort"`
	Enabled    bool   `json:"enabled"`
}

// Browser 是受控浏览器（独立 profile）的启动配置。
// 语言与时区固定，出口可跟随 VPS 隧道，避免浏览器指纹与出口地区不一致。
type Browser struct {
	Lang         string `json:"lang"`         // 界面语言与 Accept-Language
	TimeZone     string `json:"timeZone"`     // IANA 时区，如 America/Los_Angeles
	UseVPSEgress bool   `json:"useVpsEgress"` // 是否强制走 VPS SOCKS5
	ProfileDir   string `json:"profileDir"`   // 留空使用数据目录下的默认位置
}

// VPS 是「类 VPS 出入口」配置：借助一台 SSH 可达的 VPS，
// 用 SOCKS 隧道获得独立公网出口，用远程转发获得公网入站端口。
type VPS struct {
	Enabled      bool         `json:"enabled"`
	Host         string       `json:"host"`
	Port         int          `json:"port"`      // SSH 端口
	User         string       `json:"user"`      // SSH 用户
	KeyPath      string       `json:"keyPath"`   // 私钥路径
	SOCKSPort    int          `json:"socksPort"` // 本地 SOCKS5 监听端口
	GatewayPorts bool         `json:"gatewayPorts"`
	Forwards     []VPSForward `json:"forwards"`
	// 地区信息：工作站环境与浏览器跟随该地区，而不是宿主 Mac。
	Lang     string `json:"lang"`     // 例如 en-US
	TimeZone string `json:"timeZone"` // 例如 America/Los_Angeles
}

// License 保存本机的软件授权状态。
// 只有拿到服务端签发的 token 才视为已授权；离线时按 GraceUntil 宽限使用。
type License struct {
	Key         string    `json:"key"`         // 授权码（用户输入，用于重新激活）
	Token       string    `json:"token"`       // 服务端签发的访问令牌
	Plan        string    `json:"plan"`        // lifetime / yearly / agent
	Fingerprint string    `json:"fingerprint"` // 本机设备指纹（sha256）
	TokenExpire time.Time `json:"tokenExpire"` // token 到期时间
	GraceUntil  time.Time `json:"graceUntil"`  // 离线宽限截止时间
	LastOKAt    time.Time `json:"lastOkAt"`    // 上次成功心跳时间
	LastError   string    `json:"lastError"`   // 上次失败原因（仅展示）
	VPS         VPS       `json:"vps"`         // 授权中心下发的出口节点
	ActivatedAt time.Time `json:"activatedAt"` // 首次激活时间
}

// Snapshot 是工作站工作目录的一次归档快照。
type Snapshot struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
	SizeBytes int64     `json:"sizeBytes"`
}

// AuditEntry 记录一次敏感操作（操控 Mac）。命令内容不入库。
type AuditEntry struct {
	ID         string    `json:"id"`
	At         time.Time `json:"at"`
	Action     string    `json:"action"`
	Digest     string    `json:"digest"`
	ExitCode   int       `json:"exitCode"`
	DurationMS int64     `json:"durationMs"`
	Allowed    bool      `json:"allowed"`
	Detail     string    `json:"detail,omitempty"`
}

// SetupSelection 记录「开通链接」里勾选的安装项，供一键配置脚本按需安装。
// Tools 为空表示按默认清单安装（chrome/node/git/claude）。
type SetupSelection struct {
	Tools []string  `json:"tools"`
	VM    bool      `json:"vm"`
	At    time.Time `json:"at,omitempty"`
}

// TaskRecord 是任务队列中可持久化的元信息。
type TaskRecord struct {
	ID         string     `json:"id"`
	Kind       string     `json:"kind"`
	Title      string     `json:"title"`
	Status     string     `json:"status"` // queued | running | succeeded | failed | canceled
	CreatedAt  time.Time  `json:"createdAt"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	ExitCode   int        `json:"exitCode"`
	Error      string     `json:"error,omitempty"`
}

// State 是完整的状态文档。
type State struct {
	Workstations []Workstation  `json:"workstations"`
	ActiveID     string         `json:"activeId"`
	Settings     Settings       `json:"settings"`
	Locale       Locale         `json:"locale"`
	DNS          DNS            `json:"dns"`
	Forwards     []PortForward  `json:"forwards"`
	Proxy        Proxy          `json:"proxy"`
	ProxyCore    ProxyCore      `json:"proxyCore"`
	Desktop      Desktop        `json:"desktop"`
	Browser      Browser        `json:"browser"`
	VPS          VPS            `json:"vps"`
	License      License        `json:"license"`
	Setup        SetupSelection `json:"setup"`
	Env          []EnvVar       `json:"env"`
	Mounts       []string       `json:"mounts"`
	Snapshots    []Snapshot     `json:"snapshots"`
	Audit        []AuditEntry   `json:"audit"`
	Tasks        []TaskRecord   `json:"tasks"`

	// LegacyWorkstation 只用于读取旧版单工作站状态，迁移后置空，不再写出。
	LegacyWorkstation *Workstation `json:"workstation,omitempty"`
}

// Active 返回当前工作站（写锁内使用）。集合为空时返回 nil。
func (s *State) Active() *Workstation {
	for i := range s.Workstations {
		if s.Workstations[i].ID == s.ActiveID {
			return &s.Workstations[i]
		}
	}
	if len(s.Workstations) == 0 {
		return nil
	}
	s.ActiveID = s.Workstations[0].ID
	return &s.Workstations[0]
}

// Current 返回当前工作站的值拷贝（只读场景使用）。
func (s State) Current() Workstation {
	for _, ws := range s.Workstations {
		if ws.ID == s.ActiveID {
			return ws
		}
	}
	if len(s.Workstations) > 0 {
		return s.Workstations[0]
	}
	return Workstation{}
}

// Find 按 ID 查找工作站。
func (s State) Find(id string) (Workstation, bool) {
	for _, ws := range s.Workstations {
		if ws.ID == id {
			return ws, true
		}
	}
	return Workstation{}, false
}

// Default 返回带有出厂默认值的状态。
func Default() State {
	now := time.Now().UTC()
	loc := DefaultLocale()
	return State{
		Workstations: []Workstation{{
			ID:        "default",
			Name:      "workstation",
			Root:      "workstation",
			State:     "stopped",
			Driver:    "local",
			Image:     "debian-12-arm64",
			CPUs:      4,
			MemoryMB:  4096,
			DiskGB:    20,
			Network:   false,
			Lang:      loc.Lang,
			TimeZone:  loc.Timezone,
			ProxyMode: "system",
			UseVPS:    true,
			CreatedAt: now,
			Message:   "等待启动",
		}},
		ActiveID: "default",
		Settings: Settings{
			MacControlEnabled:  true,
			AllowMacRunCommand: true,
			AutoStart:          false,
			NetworkEnabled:     false,
			Theme:              "light",
		},
		Locale:    DefaultLocale(),
		DNS:       DefaultDNS(),
		Forwards:  []PortForward{},
		Proxy:     Proxy{Mode: "system"},
		Desktop:   Desktop{Enabled: false, VNCHost: "", VNCPort: 5900, Scale: "fit"},
		Browser:   Browser{Lang: "en-US", TimeZone: "America/Los_Angeles", UseVPSEgress: true},
		VPS:       VPS{Port: 22, User: "root", SOCKSPort: 1080, Forwards: []VPSForward{}, Lang: "en-US", TimeZone: "America/Los_Angeles"},
		Env:       []EnvVar{},
		Mounts:    []string{},
		Snapshots: []Snapshot{},
		Audit:     []AuditEntry{},
		Tasks:     []TaskRecord{},
	}
}

// DefaultLocale 返回与宿主一致的语言、编码与时区，作为工作站的出厂值。
func DefaultLocale() Locale {
	return Locale{Lang: detectLang(), Encoding: "UTF-8", Timezone: detectTimezone()}
}

// DefaultDNS 返回与宿主同源的解析配置。
func DefaultDNS() DNS {
	return DNS{
		Enabled: true,
		Mode:    "system",
		Servers: []string{"223.5.5.5", "1.1.1.1"},
		Search:  []string{},
		Port:    1053,
	}
}

var detectOnce sync.Once
var detectedLang, detectedTZ string

func detectHost() {
	lang := ""
	// macOS 上 GUI 进程的 LANG 往往是 C.UTF-8，系统语言以 AppleLocale 为准。
	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	if out, err := exec.CommandContext(ctx, "defaults", "read", "-g", "AppleLocale").Output(); err == nil {
		v := strings.ToLower(strings.TrimSpace(string(out)))
		switch {
		case strings.Contains(v, "zh"):
			lang = "zh_CN.UTF-8"
		case strings.HasPrefix(v, "ja"):
			lang = "ja_JP.UTF-8"
		case strings.HasPrefix(v, "ko"):
			lang = "ko_KR.UTF-8"
		case strings.Contains(v, "_") || len(v) >= 2:
			lang = v[:2] + "_" + strings.ToUpper(v[:2]) + ".UTF-8"
		}
	}
	cancel()
	if lang == "" {
		for _, k := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
			v := strings.ToLower(os.Getenv(k))
			switch {
			case strings.Contains(v, "zh"):
				lang = "zh_CN.UTF-8"
			case strings.Contains(v, "utf-8") || strings.Contains(v, "utf8"):
				lang = "en_US.UTF-8"
			}
			if lang != "" {
				break
			}
		}
	}
	if lang == "" || strings.HasPrefix(lang, "c_") {
		lang = "en_US.UTF-8"
	}

	tz := ""
	if target, err := os.Readlink("/etc/localtime"); err == nil {
		if idx := strings.Index(target, "zoneinfo/"); idx >= 0 {
			tz = target[idx+len("zoneinfo/"):]
		}
	}
	if tz == "" {
		tz = time.Now().Location().String()
	}
	if tz == "" || tz == "Local" {
		tz = "Asia/Shanghai"
	}
	detectedLang, detectedTZ = lang, tz
}

func detectLang() string {
	detectOnce.Do(detectHost)
	return detectedLang
}

func detectTimezone() string {
	detectOnce.Do(detectHost)
	return detectedTZ
}

// Store 是并发安全的状态容器，写入时原子落盘。
type Store struct {
	mu    sync.RWMutex
	path  string
	state State
}

// Open 加载状态文件，不存在时写入默认状态。
func Open(path string) (*Store, error) {
	s := &Store{path: path, state: Default()}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, s.flush()
		}
		return nil, err
	}
	if len(b) > 0 {
		if err := json.Unmarshal(b, &s.state); err != nil {
			return nil, err
		}
	}
	// 迁移旧格式后立刻落盘，避免每次启动都重复迁移。
	migrated := s.state.LegacyWorkstation != nil
	s.normalize()
	if migrated {
		return s, s.flush()
	}
	return s, nil
}

func (s *Store) normalize() {
	d := Default()

	// 旧版单工作站状态 → 工作站集合；沿用原目录，避免工作区文件「消失」。
	// 旧格式不可能同时带新字段，所以只要读到 legacy 就整体覆盖。
	if old := s.state.LegacyWorkstation; old != nil && old.Name != "" {
		ws := d.Workstations[0]
		ws.Name = old.Name
		ws.State = old.State
		ws.Image = old.Image
		ws.Driver = old.Driver
		ws.CPUs, ws.MemoryMB, ws.DiskGB = old.CPUs, old.MemoryMB, old.DiskGB
		ws.CreatedAt = old.CreatedAt
		ws.StartedAt = old.StartedAt
		ws.Message = old.Message
		// 用当前全局配置回填环境参数，迁移后切回来行为不变。
		if s.state.Locale.Lang != "" {
			ws.Lang = s.state.Locale.Lang
		}
		if s.state.Locale.Timezone != "" {
			ws.TimeZone = s.state.Locale.Timezone
		}
		ws.Network = s.state.Settings.NetworkEnabled
		if s.state.Proxy.Mode != "" {
			ws.ProxyMode = s.state.Proxy.Mode
		}
		ws.UseVPS = s.state.Browser.UseVPSEgress
		s.state.Workstations = []Workstation{ws}
	}
	s.state.LegacyWorkstation = nil
	if len(s.state.Workstations) == 0 {
		s.state.Workstations = d.Workstations
	}
	if s.state.ActiveID == "" {
		s.state.ActiveID = s.state.Workstations[0].ID
	}
	for i := range s.state.Workstations {
		ws := &s.state.Workstations[i]
		if ws.ID == "" {
			ws.ID = fmt.Sprintf("ws-%d", i+1)
		}
		if ws.Root == "" {
			ws.Root = ws.ID
		}
		if ws.Name == "" {
			ws.Name = "workstation"
		}
		if ws.Driver == "" {
			ws.Driver = "local"
		}
		if ws.State == "" {
			ws.State = "stopped"
		}
		if ws.CPUs == 0 {
			ws.CPUs = d.Workstations[0].CPUs
		}
		if ws.MemoryMB == 0 {
			ws.MemoryMB = d.Workstations[0].MemoryMB
		}
		if ws.DiskGB == 0 {
			ws.DiskGB = d.Workstations[0].DiskGB
		}
		if ws.Image == "" {
			ws.Image = d.Workstations[0].Image
		}
		if ws.Lang == "" {
			ws.Lang = firstNonEmpty(s.state.Locale.Lang, d.Locale.Lang)
		}
		if ws.TimeZone == "" {
			ws.TimeZone = firstNonEmpty(s.state.Locale.Timezone, d.Locale.Timezone)
		}
		if ws.ProxyMode == "" {
			ws.ProxyMode = "system"
		}
	}
	if _, ok := s.state.Find(s.state.ActiveID); !ok {
		s.state.ActiveID = s.state.Workstations[0].ID
	}

	if s.state.Proxy.Mode == "" {
		s.state.Proxy.Mode = "system"
	}
	if s.state.Desktop.Scale == "" {
		s.state.Desktop.Scale = "fit"
	}
	if s.state.Desktop.VNCPort == 0 {
		s.state.Desktop.VNCPort = 5900
	}
	if s.state.VPS.Port == 0 {
		s.state.VPS.Port = 22
	}
	if s.state.VPS.User == "" {
		s.state.VPS.User = "root"
	}
	if s.state.VPS.SOCKSPort == 0 {
		s.state.VPS.SOCKSPort = 1080
	}
	if s.state.VPS.Forwards == nil {
		s.state.VPS.Forwards = []VPSForward{}
	}
	if s.state.Browser.Lang == "" {
		s.state.Browser.Lang = "en-US"
	}
	if s.state.Browser.TimeZone == "" {
		s.state.Browser.TimeZone = "America/Los_Angeles"
	}
	if s.state.VPS.Lang == "" {
		s.state.VPS.Lang = "en-US"
	}
	if s.state.VPS.TimeZone == "" {
		s.state.VPS.TimeZone = "America/Los_Angeles"
	}
	if s.state.Forwards == nil {
		s.state.Forwards = []PortForward{}
	}
	if s.state.Snapshots == nil {
		s.state.Snapshots = []Snapshot{}
	}
	if s.state.Audit == nil {
		s.state.Audit = []AuditEntry{}
	}
	if s.state.Tasks == nil {
		s.state.Tasks = []TaskRecord{}
	}
	if s.state.Settings.Theme == "" {
		s.state.Settings.Theme = "light"
	}
	if s.state.Locale.Lang == "" {
		s.state.Locale = d.Locale
	}
	if s.state.Locale.Encoding == "" {
		s.state.Locale.Encoding = "UTF-8"
	}
	if s.state.Locale.Timezone == "" {
		s.state.Locale.Timezone = d.Locale.Timezone
	}
	if s.state.DNS.Mode == "" {
		s.state.DNS.Mode = "system"
	}
	if s.state.DNS.Port == 0 {
		s.state.DNS.Port = 1053
	}
	if s.state.DNS.Servers == nil {
		s.state.DNS.Servers = d.DNS.Servers
	}
	if s.state.DNS.Search == nil {
		s.state.DNS.Search = []string{}
	}
	// 语言环境改由 Locale 统一注入，迁移旧状态里的重复项。
	kept := make([]EnvVar, 0, len(s.state.Env))
	for _, kv := range s.state.Env {
		switch strings.ToUpper(kv.Key) {
		case "LANG", "LC_ALL", "LC_CTYPE", "LC_MESSAGES", "LC_COLLATE", "LANGUAGE", "TZ":
			continue
		}
		kept = append(kept, kv)
	}
	s.state.Env = kept
}

// Read 在只读锁内访问状态，回调内不得再调用 Store 方法。
func (s *Store) Read(fn func(*State)) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	fn(&s.state)
}

// Copy 返回状态深拷贝。
func (s *Store) Copy() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, _ := json.Marshal(s.state)
	var out State
	_ = json.Unmarshal(b, &out)
	return out
}

// Update 在写锁内修改状态并落盘。回调返回错误时不落盘但保留修改。
func (s *Store) Update(fn func(*State)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.state)
	return s.flushLocked()
}

func (s *Store) flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.flushLocked()
}

func (s *Store) flushLocked() error {
	b, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// AddAudit 追加一条审计记录，最多保留 200 条。
func (s *Store) AddAudit(e AuditEntry) error {
	return s.Update(func(st *State) {
		st.Audit = append([]AuditEntry{e}, st.Audit...)
		if len(st.Audit) > 200 {
			st.Audit = st.Audit[:200]
		}
	})
}

// SaveLicense 覆盖写入授权状态。
func (s *Store) SaveLicense(l License) error {
	return s.Update(func(st *State) { st.License = l })
}

// UpsertTask 写入或更新任务元信息。
func (s *Store) UpsertTask(t TaskRecord) error {
	return s.Update(func(st *State) {
		for i := range st.Tasks {
			if st.Tasks[i].ID == t.ID {
				st.Tasks[i] = t
				return
			}
		}
		st.Tasks = append([]TaskRecord{t}, st.Tasks...)
		if len(st.Tasks) > 60 {
			st.Tasks = st.Tasks[:60]
		}
	})
}

func firstNonEmpty(list ...string) string {
	for _, v := range list {
		if v != "" {
			return v
		}
	}
	return ""
}

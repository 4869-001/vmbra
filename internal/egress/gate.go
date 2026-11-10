// Package egress 实现「出口闸门」：出网不是布尔开关，而是一张有期限的租约。
//
// 语义：
//   - 关闭（closed）：默认状态，工作站内的新进程拿不到网络策略。
//   - 检查中（checking）：正在跑出口检查。
//   - 放行（open）：检查通过，发一张 lease（默认 90 秒）的租约。
//
// 租约到期、检查失败、或配置发生变更时，闸门立即回到 closed，
// 由调用方据此切断网络。这样即使管理进程被 kill，最坏情况也只是多放行一个租约周期。
package egress

import (
	"sync"
	"time"
)

// State 是闸门状态。
type State string

const (
	StateClosed   State = "closed"
	StateChecking State = "checking"
	StateOpen     State = "open"
)

// Check 是单项检查结果。
type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// Report 是一次出口检查的完整证据。
type Report struct {
	At          time.Time `json:"at"`
	OK          bool      `json:"ok"`
	ExitIP      string    `json:"exitIp,omitempty"`
	Country     string    `json:"country,omitempty"`
	CountryCode string    `json:"countryCode,omitempty"`
	TimeZone    string    `json:"timeZone,omitempty"`
	Checks      []Check   `json:"checks"`
	Error       string    `json:"error,omitempty"`
}

// Snapshot 是给界面/诊断用的只读视图。
type Snapshot struct {
	State       State     `json:"state"`
	Allowed     bool      `json:"allowed"`
	TTLSeconds  int       `json:"ttlSeconds"`
	RemainingMS int64     `json:"remainingMs"`
	ExpiresAt   time.Time `json:"expiresAt,omitempty"`
	Reason      string    `json:"reason,omitempty"`
	Failures    int       `json:"failures"`
	Report      Report    `json:"report"`
}

// Gate 是出口闸门本体，可并发使用。
type Gate struct {
	mu       sync.Mutex
	state    State
	until    time.Time
	ttl      time.Duration
	report   Report
	reason   string
	failures int
}

// New 构造闸门，ttl 为单张租约的有效期。
func New(ttl time.Duration) *Gate {
	if ttl <= 0 {
		ttl = 90 * time.Second
	}
	return &Gate{state: StateClosed, ttl: ttl, reason: "尚未检查"}
}

// TTL 返回租约有效期。
func (g *Gate) TTL() time.Duration {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.ttl
}

// State 返回当前状态。
func (g *Gate) State() State {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.currentStateLocked()
}

func (g *Gate) currentStateLocked() State {
	if g.state == StateOpen && time.Now().After(g.until) {
		return StateClosed
	}
	return g.state
}

// Allows 表示当前是否允许出网。
func (g *Gate) Allows() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.currentStateLocked() == StateOpen
}

// BeginCheck 把状态置为 checking，避免检查期间被当成已放行。
func (g *Gate) BeginCheck() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.state = StateChecking
}

// Open 用一次成功的检查结果续期。
func (g *Gate) Open(rep Report) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.state = StateOpen
	g.until = time.Now().Add(g.ttl)
	g.report = rep
	g.reason = ""
	g.failures = 0
}

// Close 关闭闸门并记录原因。
func (g *Gate) Close(reason string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.state = StateClosed
	g.until = time.Time{}
	g.reason = reason
}

// Fail 记录一次失败：关闭闸门并累加失败次数。
func (g *Gate) Fail(rep Report, reason string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.state = StateClosed
	g.until = time.Time{}
	g.report = rep
	g.reason = reason
	g.failures++
}

// Invalidate 用于配置变更（换节点、改语言/时区、开关出网）后立即失效。
func (g *Gate) Invalidate(reason string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.state = StateClosed
	g.until = time.Time{}
	g.reason = reason
}

// Snapshot 返回只读快照。
func (g *Gate) Snapshot() Snapshot {
	g.mu.Lock()
	defer g.mu.Unlock()
	st := g.currentStateLocked()
	snap := Snapshot{
		State:      st,
		Allowed:    st == StateOpen,
		TTLSeconds: int(g.ttl / time.Second),
		Reason:     g.reason,
		Failures:   g.failures,
		Report:     g.report,
	}
	if st == StateOpen {
		snap.ExpiresAt = g.until
		if left := time.Until(g.until); left > 0 {
			snap.RemainingMS = left.Milliseconds()
		}
	}
	return snap
}

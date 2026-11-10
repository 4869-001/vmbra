package service

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/4869-001/vmbra/internal/egress"
	"github.com/4869-001/vmbra/internal/store"
)

// egressTTL 是单张放行租约的有效期。就算管理进程被 kill，
// 最坏也只是多放行这么久，之后工作站内的新进程拿不到网络策略。
const egressTTL = 90 * time.Second

// egressRenew 是复核周期：必须显著小于 TTL，留出一次失败重试的余量。
const egressRenew = 30 * time.Second

// EgressSnapshot 返回闸门快照，供界面与诊断展示。
func (s *Service) EgressSnapshot() egress.Snapshot {
	return s.Gate.Snapshot()
}

// egressProxy 选出当前实际生效的出口代理地址：
// Clash 内核优先，其次是 VPS 隧道，最后退回手动/直连。
func (s *Service) egressProxy(st store.State) (proxyURL, label string) {
	if st.ProxyCore.Running && st.ProxyCore.HTTPProxy != "" {
		return st.ProxyCore.HTTPProxy, "Clash 内核 · " + st.ProxyCore.NodeName
	}
	if st.VPS.Enabled && s.VPSTunnel.Running() {
		if addr := s.VPSTunnel.SOCKSAddr(); addr != "" {
			return "socks5://" + addr, "VPS 隧道 · " + st.VPS.Host
		}
	}
	if st.Proxy.Mode == "manual" {
		if st.Proxy.HTTPS != "" {
			return st.Proxy.HTTPS, "手动代理"
		}
		if st.Proxy.HTTP != "" {
			return st.Proxy.HTTP, "手动代理"
		}
	}
	return "", "未配置出口（直连）"
}

// CheckEgress 在主机侧执行一次完整出口检查，返回可归档的证据。
func (s *Service) CheckEgress() egress.Report {
	rep := egress.Report{At: time.Now()}
	add := func(name string, ok bool, detail string) {
		rep.Checks = append(rep.Checks, egress.Check{Name: name, OK: ok, Detail: detail})
	}

	st := s.Store.Copy()
	proxyURL, label := s.egressProxy(st)

	if !st.Settings.NetworkEnabled {
		add("出网开关", false, "工作站出网已关闭")
		rep.OK = false
		return rep
	}
	add("出网开关", true, "已开启")

	if proxyURL == "" {
		add("出口链路", false, "没有可用的出口（既未配置 Clash 节点，也没有 VPS 隧道或手动代理）")
		rep.OK = false
		rep.Error = "缺少出口链路"
		return rep
	}
	add("出口链路", true, label)

	client, err := ProxyClient(proxyURL, 12*time.Second)
	if err != nil {
		add("出口可达", false, err.Error())
		rep.OK = false
		rep.Error = err.Error()
		return rep
	}

	geo, err := probeExitWith(client)
	if err != nil {
		add("出口可达", false, "无法通过该链路访问外网："+err.Error())
		rep.OK = false
		rep.Error = err.Error()
		return rep
	}
	rep.ExitIP = geo.IP
	rep.Country = geo.Country
	rep.CountryCode = geo.CountryCode
	rep.TimeZone = geo.TimeZone
	add("出口可达", true, fmt.Sprintf("%s · %s", geo.IP, strings.TrimSpace(geo.Country+" "+geo.City)))

	// ---- 出入口一致性 ----
	wantLang, wantBrowser := localeForCountry(geo.CountryCode)
	if geo.CountryCode == "" {
		add("出口地区", false, "未能识别出口地区，无法校对语言/时区")
	} else {
		add("出口地区", true, strings.ToUpper(geo.CountryCode))
		langOK := st.Locale.Lang == wantLang
		detail := st.Locale.Lang
		if !langOK {
			detail = fmt.Sprintf("当前 %s，建议 %s", st.Locale.Lang, wantLang)
		}
		add("系统语言", langOK, detail)

		wantTZ := geo.TimeZone
		if wantTZ == "" {
			wantTZ = timezoneForCountry(geo.CountryCode)
		}
		tzOK := st.Locale.Timezone == wantTZ
		tzDetail := st.Locale.Timezone
		if !tzOK {
			tzDetail = fmt.Sprintf("当前 %s，建议 %s", st.Locale.Timezone, wantTZ)
		}
		add("系统时区", tzOK, tzDetail)

		browserOK := st.Browser.Lang == wantBrowser
		bDetail := st.Browser.Lang
		if !browserOK {
			bDetail = fmt.Sprintf("当前 %s，建议 %s", st.Browser.Lang, wantBrowser)
		}
		add("浏览器语言", browserOK, bDetail)
	}

	// ---- DNS：转发器在跑就认为解析走同一条链路 ----
	if st.DNS.Enabled {
		if addr := s.dnsEndpoint(st); addr != "" {
			add("DNS 转发", true, addr)
		} else {
			add("DNS 转发", false, "已启用但转发器未运行")
		}
	} else {
		add("DNS 转发", true, "系统解析")
	}

	// ---- IPv6 泄漏：代理只承载 IPv4 TCP，主机能直连 v6 就说明有旁路 ----
	if conn, err := net.DialTimeout("tcp6", "[2606:4700:4700::1111]:443", 2*time.Second); err == nil {
		_ = conn.Close()
		add("IPv6 泄漏", false, "主机可直连 IPv6，存在绕过代理的旁路")
	} else {
		add("IPv6 泄漏", true, "未发现 IPv6 直连旁路")
	}

	rep.OK = true
	for _, c := range rep.Checks {
		if !c.OK {
			rep.OK = false
			break
		}
	}
	if !rep.OK && rep.Error == "" {
		rep.Error = "出口与本地环境不一致"
	}
	return rep
}

// CheckEgressNow 手动触发一次检查并同步闸门状态（界面上的「立即检查」）。
func (s *Service) CheckEgressNow() egress.Snapshot {
	s.Gate.BeginCheck()
	rep := s.CheckEgress()
	if rep.OK {
		s.Gate.Open(rep)
		return s.Gate.Snapshot()
	}
	s.Gate.Fail(rep, rep.Error)
	s.enforceEgressClosed("出口检查未通过：" + rep.Error)
	return s.Gate.Snapshot()
}

// RenewEgress 是后台复核：通过就续租，不通过就立刻切断。
func (s *Service) RenewEgress() {
	st := s.Store.Copy()
	if !st.Settings.NetworkEnabled {
		s.Gate.Close("工作站出网未开启")
		return
	}
	s.Gate.BeginCheck()
	rep := s.CheckEgress()
	if rep.OK {
		s.Gate.Open(rep)
		return
	}
	s.Gate.Fail(rep, rep.Error)
	s.enforceEgressClosed("出口检查未通过：" + rep.Error)
}

// StartEgressLoop 启动租约复核循环：每 30 秒续一次，失败即断网。
func (s *Service) StartEgressLoop(ctx context.Context) {
	go func() {
		// 启动即检查一次，避免沿用上一个进程留下的「已放行」印象。
		s.RenewEgress()
		ticker := time.NewTicker(egressRenew)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				s.Gate.Close("管理服务退出")
				return
			case <-ticker.C:
				s.RenewEgress()
			}
		}
	}()
}

// enforceEgressClosed 在检查失败或租约到期后切断网络：
// 关闭出网开关、清掉导出给工作站的代理变量，并留下审计记录。
func (s *Service) enforceEgressClosed(reason string) {
	st := s.Store.Copy()
	if !st.Settings.NetworkEnabled {
		return
	}
	_ = s.Store.Update(func(st *store.State) {
		st.Settings.NetworkEnabled = false
		s.mirrorActiveEnv(st)
		st.Audit = append(st.Audit, store.AuditEntry{
			At:      time.Now(),
			Action:  "egress.close",
			Detail:  reason,
			Allowed: false,
		})
		if len(st.Audit) > 200 {
			st.Audit = st.Audit[len(st.Audit)-200:]
		}
	})
	_ = s.WriteShellEnv()
}

// RepairEgress 是「一键修复」：重新按当前节点探测并回写语言/时区/代理。
func (s *Service) RepairEgress() (ApplyResult, error) {
	st := s.Store.Copy()
	if st.ProxyCore.NodeID == "" {
		return ApplyResult{}, fmt.Errorf("还没有选择节点，请先在「出口」页导入订阅并选一个节点")
	}
	res, err := s.ApplyClashNode(st.ProxyCore.NodeID)
	if err != nil {
		return ApplyResult{}, err
	}
	s.CheckEgressNow()
	return res, nil
}

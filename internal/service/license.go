package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"anywhere/internal/license"
	"anywhere/internal/store"
)

// 授权中心地址可通过环境变量覆盖，打包时默认指向线上。
const defaultLicenseServer = "https://license.vmbra.app"

// 心跳间隔：6 小时，失败后由前台手动重试或等下次心跳。
const licenseHeartbeatInterval = 6 * time.Hour

// LicenseServer 返回授权中心地址。
func (s *Service) LicenseServer() string {
	if v := strings.TrimSpace(os.Getenv("VMBRA_LICENSE_SERVER")); v != "" {
		return v
	}
	return defaultLicenseServer
}

// licenseClient 构造授权中心客户端。
func (s *Service) licenseClient() *license.Client {
	return license.NewClient(s.LicenseServer(), s.AppVersion())
}

// AppVersion 返回当前程序版本，缺省视为 1.0.0。
func (s *Service) AppVersion() string {
	if v := strings.TrimSpace(os.Getenv("VMBRA_APP_VERSION")); v != "" {
		return v
	}
	return "1.0.0"
}

// LicenseStatus 汇总授权状态，供控制台展示。
// 不返回 token 本身，避免令牌通过接口泄露。
func (s *Service) LicenseStatus() map[string]any {
	st := s.Store.Copy()
	lic := st.License
	now := time.Now()

	status := "unlicensed"
	usable := false
	switch {
	case lic.Token == "":
		status = "unlicensed"
	case now.Before(lic.TokenExpire):
		status = "active"
		usable = true
	case now.Before(lic.GraceUntil):
		status = "grace"
		usable = true
	default:
		status = "expired"
	}

	fp := lic.Fingerprint
	if fp == "" {
		fp = license.MachineFingerprint()
	}
	shortFP := fp
	if len(shortFP) > 12 {
		shortFP = shortFP[:12] + "…"
	}

	return map[string]any{
		"status":          status,
		"usable":          usable,
		"plan":            lic.Plan,
		"planLabel":       planLabel(lic.Plan),
		"keyMasked":       maskKey(lic.Key),
		"fingerprint":     shortFP,
		"fingerprintFull": fp,
		"tokenExpire":     timeOrNil(lic.TokenExpire),
		"graceUntil":      timeOrNil(lic.GraceUntil),
		"lastOkAt":        timeOrNil(lic.LastOKAt),
		"activatedAt":     timeOrNil(lic.ActivatedAt),
		"lastError":       lic.LastError,
		"server":          s.LicenseServer(),
		"vps":             lic.VPS,
		"appVersion":      s.AppVersion(),
	}
}

// LicenseUsable 判断当前是否允许使用产品。
// 未激活或已过期返回 false；离线宽限期内返回 true。
func (s *Service) LicenseUsable() bool {
	st := s.Store.Copy().License
	if st.Token == "" {
		return false
	}
	now := time.Now()
	return now.Before(st.TokenExpire) || now.Before(st.GraceUntil)
}

// ActivateLicense 用授权码激活本机。
func (s *Service) ActivateLicense(ctx context.Context, key string) error {
	key = strings.ToUpper(strings.TrimSpace(key))
	if key == "" {
		return errors.New("请输入授权码")
	}

	fp := license.MachineFingerprint()
	res, err := s.licenseClient().Activate(ctx, key, fp)
	if err != nil {
		s.recordLicenseError(err)
		return err
	}
	if err := s.saveLicenseResult(res, key, fp); err != nil {
		return err
	}
	_ = s.Store.AddAudit(store.AuditEntry{
		ID:      fmt.Sprintf("lic-%d", time.Now().UnixNano()),
		At:      time.Now().UTC(),
		Action:  "license.activate",
		Allowed: true,
		Detail:  "授权激活成功",
	})
	return nil
}

// HeartbeatLicense 续期令牌并刷新出口节点。
func (s *Service) HeartbeatLicense(ctx context.Context) error {
	cur := s.Store.Copy().License
	if cur.Token == "" {
		return errors.New("尚未激活")
	}
	res, err := s.licenseClient().Heartbeat(ctx, cur.Token)
	if err != nil {
		s.recordLicenseError(err)
		return err
	}
	return s.saveLicenseResult(res, cur.Key, cur.Fingerprint)
}

// saveLicenseResult 把服务端结果写入状态，并回填 VPS 出口。
func (s *Service) saveLicenseResult(res *license.Result, key, fp string) error {
	now := time.Now().UTC()
	prev := s.Store.Copy().License

	next := store.License{
		Key:         key,
		Token:       res.Token,
		Plan:        res.Plan,
		Fingerprint: fp,
		TokenExpire: res.TokenExpiresAt,
		GraceUntil:  now.Add(res.OfflineGrace),
		LastOKAt:    now,
		VPS:         prev.VPS,
		ActivatedAt: prev.ActivatedAt,
	}
	if next.ActivatedAt.IsZero() {
		next.ActivatedAt = now
	}

	// 授权中心下发的第一个节点即为默认出口。
	var endpoint *license.Endpoint
	if len(res.Endpoints) > 0 {
		endpoint = &res.Endpoints[0]
		next.VPS = store.VPS{
			Enabled: true,
			Host:    endpoint.Host,
			Port:    endpoint.Port,
		}
	}

	return s.Store.Update(func(st *store.State) {
		st.License = next
		// 用户尚未配置 VPS 时，用下发的节点填空；已配置则不覆盖。
		if endpoint != nil && st.VPS.Host == "" {
			st.VPS.Host = endpoint.Host
			st.VPS.Port = endpoint.Port
		}
	})
}

// recordLicenseError 记录最近一次失败原因，不清空既有令牌。
func (s *Service) recordLicenseError(err error) {
	var apiErr *license.APIError
	msg := err.Error()
	if errors.As(err, &apiErr) {
		msg = apiErr.Message
	}
	_ = s.Store.Update(func(st *store.State) { st.License.LastError = msg })
}

// StartLicenseHeartbeat 启动后台心跳；ctx 取消即退出。
func (s *Service) StartLicenseHeartbeat(ctx context.Context) {
	go func() {
		timer := time.NewTimer(30 * time.Second)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}

			if cur := s.Store.Copy().License; cur.Token != "" {
				hctx, cancel := context.WithTimeout(ctx, 20*time.Second)
				if err := s.HeartbeatLicense(hctx); err != nil {
					log.Printf("[license] 心跳失败：%v", err)
				}
				cancel()
			}
			timer.Reset(licenseHeartbeatInterval)
		}
	}()
}

func planLabel(plan string) string {
	switch plan {
	case "lifetime":
		return "永久授权"
	case "yearly":
		return "年付授权"
	case "agent":
		return "代理商授权"
	case "":
		return "未激活"
	default:
		return plan
	}
}

// maskKey 只保留授权码首尾，用于界面展示。
func maskKey(key string) string {
	if key == "" {
		return ""
	}
	if len(key) <= 9 {
		return key
	}
	return key[:8] + "…" + key[len(key)-4:]
}

func timeOrNil(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

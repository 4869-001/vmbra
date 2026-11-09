package api

import (
	"context"
	"net/http"
	"time"
)

// handleLicenseStatus 返回授权状态与一键配置的前置条件。
func (s *Server) handleLicenseStatus(w http.ResponseWriter, r *http.Request) {
	noCache(w)
	ok(w, map[string]any{
		"license": s.svc.LicenseStatus(),
		"setup":   s.svc.SetupPreflight(),
	})
}

// handleLicenseActivate 用授权码激活本机。
func (s *Server) handleLicenseActivate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key string `json:"key"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	if err := s.svc.ActivateLicense(ctx, body.Key); err != nil {
		fail(w, err)
		return
	}
	ok(w, s.svc.LicenseStatus())
}

// handleLicenseHeartbeat 手动触发一次心跳续期。
func (s *Server) handleLicenseHeartbeat(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	if err := s.svc.HeartbeatLicense(ctx); err != nil {
		fail(w, err)
		return
	}
	ok(w, s.svc.LicenseStatus())
}

// handleSetupPreflight 单独查询一键配置的前置条件。
func (s *Server) handleSetupPreflight(w http.ResponseWriter, r *http.Request) {
	noCache(w)
	ok(w, s.svc.SetupPreflight())
}

// handleSetupRun 提交一键配置任务，返回 taskId 供 WS 跟踪进度。
func (s *Server) handleSetupRun(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Force bool     `json:"force"`
		Tools []string `json:"tools"`
		VM    *bool    `json:"vm"`
	}
	_ = decode(r, &body)

	// 界面可在提交前覆盖安装清单（例如客户当场取消勾选某个工具）
	if body.Tools != nil || body.VM != nil {
		vm := body.VM != nil && *body.VM
		if err := s.svc.SetSetupSelection(body.Tools, vm); err != nil {
			fail(w, err)
			return
		}
	}

	item, err := s.svc.StartSetup(body.Force)
	if err != nil {
		fail(w, err)
		return
	}
	rec := item.Record()
	ok(w, map[string]any{"taskId": rec.ID, "title": rec.Title})
}

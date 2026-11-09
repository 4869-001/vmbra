package api

import (
	"context"
	"net/http"
	"time"

	"anywhere/internal/service"
)

// importRequest 同时用于预览与应用：预览只读 link，其余字段可留空。
type importRequest struct {
	Link        string   `json:"link"`
	Tools       []string `json:"tools"`
	InstallKey  bool     `json:"installKey"`
	StartTunnel bool     `json:"startTunnel"`
	RunSetup    bool     `json:"runSetup"`
	SkipVPS     bool     `json:"skipVps"`
}

// handleImportPreview 解析开通链接，返回安装清单与前置条件（不改状态）。
func (s *Server) handleImportPreview(w http.ResponseWriter, r *http.Request) {
	var body importRequest
	if err := decode(r, &body); err != nil {
		fail(w, err)
		return
	}
	noCache(w)
	res, err := s.svc.PreviewImport(body.Link)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, res)
}

// handleImportApply 落配置 / 装公钥 / 建隧道 / 提交安装任务。
func (s *Server) handleImportApply(w http.ResponseWriter, r *http.Request) {
	var body importRequest
	if err := decode(r, &body); err != nil {
		fail(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()

	res, err := s.svc.ApplyImport(ctx, body.Link, service.ImportOptions{
		Tools:       body.Tools,
		InstallKey:  body.InstallKey,
		StartTunnel: body.StartTunnel,
		RunSetup:    body.RunSetup,
		SkipVPS:     body.SkipVPS,
	})
	if err != nil {
		// 失败时也把步骤明细带回去，界面才能显示卡在哪一步
		if res != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "detail": res})
			return
		}
		fail(w, err)
		return
	}
	ok(w, res)
}

// handleImportLink 用当前配置生成一条可分享的链接（不含口令）。
func (s *Server) handleImportLink(w http.ResponseWriter, r *http.Request) {
	noCache(w)
	res, err := s.svc.ImportLink()
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, res)
}

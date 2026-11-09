package api

import (
	"context"
	"net/http"
	"time"
)

// handleVPS 返回 VPS 出入口配置、密钥与隧道状态。
func (s *Server) handleVPS(w http.ResponseWriter, r *http.Request) {
	noCache(w)
	ok(w, s.svc.VPSInfo())
}

func (s *Server) handleVPSUpdate(w http.ResponseWriter, r *http.Request) {
	var patch map[string]any
	if err := decode(r, &patch); err != nil {
		fail(w, err)
		return
	}
	if _, err := s.svc.UpdateVPS(patch); err != nil {
		fail(w, err)
		return
	}
	ok(w, s.svc.VPSInfo())
}

// handleVPSKey 生成（或读取）VPS 专用公钥，供用户粘贴到 VPS。
func (s *Server) handleVPSKey(w http.ResponseWriter, r *http.Request) {
	pub, err := s.svc.EnsureVPSKey()
	if err != nil {
		fail(w, err)
		return
	}
	noCache(w)
	ok(w, map[string]any{"publicKey": pub, "info": s.svc.VPSInfo()})
}

func (s *Server) handleVPSStart(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	info, err := s.svc.StartVPS(ctx)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, info)
}

func (s *Server) handleVPSStop(w http.ResponseWriter, r *http.Request) {
	ok(w, s.svc.StopVPS())
}

// handleVPSTest 验证 SSH 登录是否可用（不建立隧道）。
func (s *Server) handleVPSTest(w http.ResponseWriter, r *http.Request) {
	res, err := s.svc.TestVPS(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, res)
}

// handleVPSEgress 对比宿主直连出口与 VPS 隧道出口。
func (s *Server) handleVPSEgress(w http.ResponseWriter, r *http.Request) {
	res, err := s.svc.TestVPSEgress(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, res)
}

package api

import (
	"net/http"
	"time"

	"github.com/4869-001/vmbra/internal/service"
)

// ---- 系统层（语言文字 / DNS / 出口 / 运行脚本） ----

func (s *Server) handleSystem(w http.ResponseWriter, r *http.Request) {
	noCache(w)
	ok(w, s.svc.SystemInfo())
}

func (s *Server) handleSystemLocale(w http.ResponseWriter, r *http.Request) {
	var req map[string]any
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	locale, err := s.svc.UpdateLocale(req)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"locale": locale, "system": s.svc.SystemInfo()})
}

func (s *Server) handleSystemDNS(w http.ResponseWriter, r *http.Request) {
	var req map[string]any
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	dns, err := s.svc.UpdateDNS(req)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"dns": dns, "stats": s.svc.DNS.Stats()})
}

func (s *Server) handleSystemDNSTest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name       string `json:"name"`
		TimeoutSec int    `json:"timeoutSec"`
	}
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	timeout := time.Duration(req.TimeoutSec) * time.Second
	results, err := s.svc.TestDNS(r.Context(), req.Name, timeout)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"name": req.Name, "results": results, "at": time.Now().UTC()})
}

func (s *Server) handleEgress(w http.ResponseWriter, r *http.Request) {
	res, err := s.svc.Egress(r.Context())
	if err != nil && res.IP == "" {
		fail(w, err)
		return
	}
	ok(w, res)
}

func (s *Server) handleRunScript(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path       string   `json:"path"`
		Args       []string `json:"args"`
		TimeoutSec int      `json:"timeoutSec"`
	}
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	item, err := s.svc.RunScript(r.Context(), service.RunScriptOptions{
		Path:       req.Path,
		Args:       req.Args,
		TimeoutSec: req.TimeoutSec,
	})
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"taskId": item.Record().ID, "path": req.Path})
}

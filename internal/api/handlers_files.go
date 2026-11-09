package api

import (
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"anywhere/internal/store"
)

const maxPreview = 2 << 20

func (s *Server) handleFileList(w http.ResponseWriter, r *http.Request) {
	noCache(w)
	p := r.URL.Query().Get("path")
	entries, err := s.svc.Drv.List(p)
	if err != nil {
		fail(w, err)
		return
	}
	parent := path.Dir(strings.TrimPrefix(p, "/"))
	if parent == "." {
		parent = ""
	}
	ok(w, map[string]any{
		"path":    strings.TrimPrefix(p, "/"),
		"parent":  parent,
		"entries": entries,
		"root":    s.svc.WorkspacePath(),
	})
}

func (s *Server) handleFileRead(w http.ResponseWriter, r *http.Request) {
	noCache(w)
	p := r.URL.Query().Get("path")
	data, err := s.svc.Drv.Read(p, maxPreview)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{
		"path":    p,
		"content": string(data),
		"size":    len(data),
		"binary":  !utf8.Valid(data),
	})
}

func (s *Server) handleFileWrite(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	if strings.TrimSpace(req.Path) == "" {
		fail(w, fmt.Errorf("缺少文件路径"))
		return
	}
	if err := s.svc.Drv.Write(req.Path, []byte(req.Content)); err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"path": req.Path, "size": len(req.Content)})
}

func (s *Server) handleFileMkdir(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	if strings.TrimSpace(req.Path) == "" {
		fail(w, fmt.Errorf("缺少目录路径"))
		return
	}
	if err := s.svc.Drv.Mkdir(req.Path); err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"path": req.Path})
}

func (s *Server) handleFileDelete(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	if strings.TrimSpace(p) == "" {
		fail(w, fmt.Errorf("缺少路径参数"))
		return
	}
	if err := s.svc.Drv.Remove(p); err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"path": p, "deleted": true})
}

func (s *Server) handleFileDownload(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	abs, err := s.svc.Drv.Resolve(p)
	if err != nil {
		fail(w, err)
		return
	}
	f, err := os.Open(abs)
	if err != nil {
		fail(w, err)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		fail(w, fmt.Errorf("目标是目录，无法下载"))
		return
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(abs)))
	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
}

func (s *Server) handleFileUpload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(128 << 20); err != nil {
		fail(w, fmt.Errorf("解析上传失败：%w", err))
		return
	}
	dir := r.URL.Query().Get("path")
	file, hdr, err := r.FormFile("file")
	if err != nil {
		fail(w, fmt.Errorf("缺少文件：%w", err))
		return
	}
	defer file.Close()
	name := filepath.Base(hdr.Filename)
	target := path.Join(strings.TrimPrefix(dir, "/"), name)
	n, err := s.svc.Drv.Save(target, file)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"path": target, "size": n})
}

// ---- 环境 ----

func (s *Server) handleEnv(w http.ResponseWriter, r *http.Request) {
	noCache(w)
	ok(w, s.svc.EnvInfo())
}

func (s *Server) handleEnvVars(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Vars []store.EnvVar `json:"vars"`
	}
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	vars, err := s.svc.UpdateEnvVars(req.Vars)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"vars": vars})
}

func (s *Server) handleEnvMounts(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mounts []string `json:"mounts"`
	}
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	mounts, err := s.svc.UpdateMounts(req.Mounts)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"mounts": mounts})
}

func (s *Server) handleEnvDesktop(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Host     string `json:"host"`
		Port     int    `json:"port"`
		Password string `json:"password"`
		Scale    string `json:"scale"`
	}
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	d, err := s.svc.UpdateDesktop(req.Host, req.Port, req.Password, req.Scale)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"desktop": d, "status": s.svc.DesktopStatus()})
}

// Package api 提供管理服务的 HTTP/WebSocket 接口与静态控制台资源。
package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/4869-001/vmbra/internal/config"
	"github.com/4869-001/vmbra/internal/service"
)

// Server 承载路由与鉴权。
type Server struct {
	svc *service.Service
	cfg *config.Config
	web fs.FS
	mux *http.ServeMux
}

// New 构造 API 服务。
func New(svc *service.Service, cfg *config.Config, web fs.FS) *Server {
	s := &Server{svc: svc, cfg: cfg, web: web, mux: http.NewServeMux()}
	s.routes()
	return s
}

// Handler 返回带中间件的最终 handler。
func (s *Server) Handler() http.Handler {
	return s.guard(s.mux)
}

func (s *Server) routes() {
	m := s.mux

	// 概览
	m.HandleFunc("GET /api/status", s.handleStatus)

	// 工作站
	m.HandleFunc("GET /api/workstation", s.handleWorkstation)
	m.HandleFunc("PATCH /api/workstation", s.handleWorkstationPatch)
	m.HandleFunc("POST /api/workstation/start", s.handleWorkstationStart)
	m.HandleFunc("POST /api/workstation/stop", s.handleWorkstationStop)
	m.HandleFunc("POST /api/workstation/restart", s.handleWorkstationRestart)

	// 工作站集合（多套隔离环境）
	m.HandleFunc("GET /api/workstations", s.handleWorkstations)
	m.HandleFunc("POST /api/workstations", s.handleWorkstationCreate)
	m.HandleFunc("PATCH /api/workstations/{id}", s.handleWorkstationUpdate)
	m.HandleFunc("DELETE /api/workstations/{id}", s.handleWorkstationDelete)
	m.HandleFunc("POST /api/workstations/{id}/activate", s.handleWorkstationActivate)

	// 应用
	m.HandleFunc("GET /api/apps", s.handleApps)
	m.HandleFunc("POST /api/apps/desktop/start", s.handleDesktopStart)
	m.HandleFunc("POST /api/apps/desktop/stop", s.handleDesktopStop)
	m.HandleFunc("GET /api/apps/desktop/ws", s.handleDesktopWS)
	m.HandleFunc("GET /api/apps/terminal", s.handleTerminalWS)
	m.HandleFunc("POST /api/apps/deb", s.handleDebUpload)
	m.HandleFunc("POST /api/apps/run", s.handleRunScript)

	// 系统层：语言文字、DNS、出口探测
	m.HandleFunc("GET /api/system", s.handleSystem)
	m.HandleFunc("PATCH /api/system/locale", s.handleSystemLocale)
	m.HandleFunc("PATCH /api/system/dns", s.handleSystemDNS)
	m.HandleFunc("POST /api/system/dns/test", s.handleSystemDNSTest)
	m.HandleFunc("POST /api/network/egress", s.handleEgress)

	// 出口闸门（租约 + 证据 + 一键修复）
	m.HandleFunc("GET /api/egress", s.handleEgressGate)
	m.HandleFunc("POST /api/egress/check", s.handleEgressCheck)
	m.HandleFunc("POST /api/egress/repair", s.handleEgressRepair)

	// 文件
	m.HandleFunc("GET /api/files", s.handleFileList)
	m.HandleFunc("GET /api/files/content", s.handleFileRead)
	m.HandleFunc("PUT /api/files/content", s.handleFileWrite)
	m.HandleFunc("GET /api/files/download", s.handleFileDownload)
	m.HandleFunc("POST /api/files/mkdir", s.handleFileMkdir)
	m.HandleFunc("POST /api/files/upload", s.handleFileUpload)
	m.HandleFunc("DELETE /api/files", s.handleFileDelete)

	// 网络
	m.HandleFunc("GET /api/network", s.handleNetwork)
	m.HandleFunc("POST /api/network/forwards", s.handleForwardCreate)
	m.HandleFunc("PATCH /api/network/forwards/{id}", s.handleForwardPatch)
	m.HandleFunc("DELETE /api/network/forwards/{id}", s.handleForwardDelete)
	m.HandleFunc("PUT /api/network/proxy", s.handleProxyUpdate)

	// 代理订阅（Clash / Mihomo）：导入链接 → 选节点 → 自动对齐环境
	m.HandleFunc("GET /api/proxy", s.handleClashView)
	m.HandleFunc("POST /api/proxy/import", s.handleClashImport)
	m.HandleFunc("POST /api/proxy/apply", s.handleClashApply)
	m.HandleFunc("POST /api/proxy/select", s.handleClashSelect)
	m.HandleFunc("POST /api/proxy/delay", s.handleClashDelay)
	m.HandleFunc("POST /api/proxy/refresh", s.handleClashRefresh)
	m.HandleFunc("POST /api/proxy/stop", s.handleClashStop)
	m.HandleFunc("DELETE /api/proxy/sources/{id}", s.handleClashSourceDelete)
	m.HandleFunc("DELETE /api/proxy/nodes/{id}", s.handleClashNodeDelete)
	m.HandleFunc("PUT /api/network/toggle", s.handleNetworkToggle)
	m.HandleFunc("POST /api/network/test", s.handleNetworkTest)
	m.HandleFunc("POST /api/network/forwards/{id}/test", s.handleForwardTest)

	// 环境
	m.HandleFunc("GET /api/env", s.handleEnv)
	m.HandleFunc("PUT /api/env/vars", s.handleEnvVars)
	m.HandleFunc("PUT /api/env/mounts", s.handleEnvMounts)
	m.HandleFunc("PUT /api/env/desktop", s.handleEnvDesktop)

	// 维护与恢复
	m.HandleFunc("GET /api/maintenance", s.handleMaintenance)
	m.HandleFunc("POST /api/maintenance/snapshots", s.handleSnapshotCreate)
	m.HandleFunc("POST /api/maintenance/snapshots/{id}/restore", s.handleSnapshotRestore)
	m.HandleFunc("DELETE /api/maintenance/snapshots/{id}", s.handleSnapshotDelete)
	m.HandleFunc("POST /api/maintenance/reset", s.handleReset)
	m.HandleFunc("POST /api/maintenance/open-finder", s.handleOpenFinder)
	m.HandleFunc("GET /api/maintenance/export", s.handleWorkspaceExport)

	// 任务
	m.HandleFunc("GET /api/tasks", s.handleTasks)
	m.HandleFunc("GET /api/tasks/{id}", s.handleTask)
	m.HandleFunc("POST /api/tasks/{id}/cancel", s.handleTaskCancel)
	m.HandleFunc("GET /api/tasks/{id}/ws", s.handleTaskWS)

	// 诊断
	m.HandleFunc("GET /api/diagnostics", s.handleDiagnostics)
	m.HandleFunc("GET /api/diagnostics/bundle", s.handleDiagnosticsBundle)

	// 设置
	m.HandleFunc("GET /api/settings", s.handleSettings)
	m.HandleFunc("PATCH /api/settings", s.handleSettingsPatch)
	m.HandleFunc("POST /api/settings/token/rotate", s.handleTokenRotate)

	// 授权
	m.HandleFunc("GET /api/license/status", s.handleLicenseStatus)
	m.HandleFunc("POST /api/license/activate", s.handleLicenseActivate)
	m.HandleFunc("POST /api/license/heartbeat", s.handleLicenseHeartbeat)

	// 一键配置环境
	m.HandleFunc("GET /api/setup/preflight", s.handleSetupPreflight)
	m.HandleFunc("POST /api/setup/run", s.handleSetupRun)

	// 开通链接（交付时生成，客户机导入）
	m.HandleFunc("POST /api/import/preview", s.handleImportPreview)
	m.HandleFunc("POST /api/import/apply", s.handleImportApply)
	m.HandleFunc("GET /api/import/link", s.handleImportLink)

	// 受控浏览器
	m.HandleFunc("GET /api/browser", s.handleBrowser)
	m.HandleFunc("PUT /api/browser", s.handleBrowserUpdate)
	m.HandleFunc("POST /api/browser/open", s.handleBrowserOpen)
	m.HandleFunc("POST /api/browser/verify", s.handleBrowserVerify)

	// VPS 出入口
	m.HandleFunc("GET /api/vps", s.handleVPS)
	m.HandleFunc("PUT /api/vps", s.handleVPSUpdate)
	m.HandleFunc("POST /api/vps/key", s.handleVPSKey)
	m.HandleFunc("POST /api/vps/start", s.handleVPSStart)
	m.HandleFunc("POST /api/vps/stop", s.handleVPSStop)
	m.HandleFunc("POST /api/vps/test", s.handleVPSTest)
	m.HandleFunc("POST /api/vps/egress", s.handleVPSEgress)

	// 操控 Mac
	m.HandleFunc("GET /api/mac", s.handleMacStatus)
	m.HandleFunc("GET /api/mac/screen", s.handleMacScreen)
	m.HandleFunc("POST /api/mac/run", s.handleMacRun)
	m.HandleFunc("GET /api/mac/audit", s.handleMacAudit)

	// 静态控制台
	m.HandleFunc("/", s.handleStatic)
}

// guard 负责 Host 校验、令牌鉴权与通用响应头。
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")

		if !hostAllowed(r.Host) {
			http.Error(w, "仅允许通过本机回环地址访问", http.StatusForbidden)
			return
		}

		if strings.HasPrefix(r.URL.Path, "/api/") {
			if !s.tokenOK(r) {
				writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "令牌无效，请重新打开控制台"})
				return
			}
			if r.Header.Get("Upgrade") != "" && !originAllowed(r) {
				http.Error(w, "跨站来源被拒绝", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func hostAllowed(host string) bool {
	h := host
	if strings.Contains(h, ":") {
		if hh, _, err := net.SplitHostPort(h); err == nil {
			h = hh
		}
	}
	h = strings.Trim(h, "[]")
	switch h {
	case "127.0.0.1", "localhost", "::1", "0.0.0.0":
		return true
	}
	return false
}

func (s *Server) tokenOK(r *http.Request) bool {
	candidate := r.Header.Get("X-Vmbra-Token")
	if candidate == "" {
		candidate = r.URL.Query().Get("token")
	}
	if candidate == "" {
		if c, err := r.Cookie("vmbra_token"); err == nil {
			candidate = c.Value
		}
	}
	if candidate == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(candidate), []byte(s.cfg.Token)) == 1
}

func originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return hostAllowed(u.Host)
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if p == "" || p == "." {
		html, err := fs.ReadFile(s.web, "index.html")
		if err != nil {
			http.Error(w, "控制台资源缺失", http.StatusInternalServerError)
			return
		}
		body := strings.ReplaceAll(string(html), "__VMBRA_TOKEN__", s.cfg.Token)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		http.SetCookie(w, &http.Cookie{
			Name:     "vmbra_token",
			Value:    s.cfg.Token,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteStrictMode,
		})
		_, _ = w.Write([]byte(body))
		return
	}
	data, err := fs.ReadFile(s.web, p)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	switch {
	case strings.HasSuffix(p, ".css"):
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	case strings.HasSuffix(p, ".js"), strings.HasSuffix(p, ".mjs"):
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	case strings.HasSuffix(p, ".html"):
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	case strings.HasSuffix(p, ".svg"):
		w.Header().Set("Content-Type", "image/svg+xml")
	case strings.HasSuffix(p, ".json"):
		w.Header().Set("Content-Type", "application/json")
	case strings.HasSuffix(p, ".png"):
		w.Header().Set("Content-Type", "image/png")
	case strings.HasSuffix(p, ".woff2"):
		w.Header().Set("Content-Type", "font/woff2")
	}
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(data)
}

// ---- 通用工具 ----

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func ok(w http.ResponseWriter, v any) { writeJSON(w, http.StatusOK, v) }

func fail(w http.ResponseWriter, err error) {
	code := http.StatusBadRequest
	switch {
	case errors.Is(err, service.ErrConflict):
		code = http.StatusConflict
	case errors.Is(err, service.ErrMacControlDisabled):
		code = http.StatusForbidden
	}
	writeJSON(w, code, map[string]any{"error": err.Error()})
}

func decode(r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 4<<20))
	return dec.Decode(v)
}

func noCache(w http.ResponseWriter) { w.Header().Set("Cache-Control", "no-store") }

func nowMS() int64 { return time.Now().UnixMilli() }

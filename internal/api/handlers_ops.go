package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/4869-001/vmbra/internal/service"
	"github.com/4869-001/vmbra/internal/store"
	"github.com/4869-001/vmbra/internal/wsx"
)

// ---- 网络 ----

func (s *Server) handleNetwork(w http.ResponseWriter, r *http.Request) {
	noCache(w)
	ok(w, s.svc.NetworkInfo())
}

func (s *Server) handleForwardCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Label     string `json:"label"`
		HostPort  int    `json:"hostPort"`
		GuestPort int    `json:"guestPort"`
		Proto     string `json:"proto"`
	}
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	f, err := s.svc.AddForward(req.Label, req.HostPort, req.GuestPort, req.Proto)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, f)
}

func (s *Server) handleForwardPatch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled *bool   `json:"enabled"`
		Label   *string `json:"label"`
	}
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	f, err := s.svc.UpdateForward(r.PathValue("id"), req.Enabled, req.Label)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, f)
}

func (s *Server) handleForwardTest(w http.ResponseWriter, r *http.Request) {
	res, err := s.svc.TestForward(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, res)
}

func (s *Server) handleForwardDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.RemoveForward(r.PathValue("id")); err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"deleted": true})
}

func (s *Server) handleProxyUpdate(w http.ResponseWriter, r *http.Request) {
	var p store.Proxy
	if err := decode(r, &p); err != nil {
		fail(w, err)
		return
	}
	out, err := s.svc.UpdateProxy(p)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, out)
}

func (s *Server) handleNetworkToggle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	ok(w, s.svc.ToggleNetwork(req.Enabled))
}

func (s *Server) handleNetworkTest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Host string `json:"host"`
		Port int    `json:"port"`
	}
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	ok(w, s.svc.TestConnection(req.Host, req.Port))
}

// ---- 维护与恢复 ----

func (s *Server) handleMaintenance(w http.ResponseWriter, r *http.Request) {
	noCache(w)
	ok(w, map[string]any{
		"snapshots":   s.svc.Snapshots(),
		"storage":     s.svc.StorageInfo(),
		"workstation": s.svc.WorkstationInfo(),
	})
}

func (s *Server) handleSnapshotCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	_ = decode(r, &req)
	item, err := s.svc.CreateSnapshot(req.Name)
	if err != nil {
		fail(w, err)
		return
	}
	rec := item.Record()
	writeJSON(w, http.StatusAccepted, map[string]any{"taskId": rec.ID})
}

func (s *Server) handleSnapshotRestore(w http.ResponseWriter, r *http.Request) {
	item, err := s.svc.RestoreSnapshot(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"taskId": item.Record().ID})
}

func (s *Server) handleSnapshotDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.DeleteSnapshot(r.PathValue("id")); err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"deleted": true})
}

func (s *Server) handleReset(w http.ResponseWriter, r *http.Request) {
	item, err := s.svc.ResetWorkspace()
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"taskId": item.Record().ID})
}

func (s *Server) handleOpenFinder(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.OpenWorkspaceInFinder(); err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"opened": true})
}

// handleWorkspaceExport 生成工作区归档并作为下载返回。
// 先在数据目录落一份临时 tar.gz，再用 ServeContent 输出，这样打包失败能返回明确的错误，
// 也支持浏览器断点续传所需的长度与 Range 语义。
func (s *Server) handleWorkspaceExport(w http.ResponseWriter, r *http.Request) {
	f, err := os.CreateTemp(s.svc.Cfg.DataDir, "workspace-export-*.tar.gz")
	if err != nil {
		fail(w, fmt.Errorf("创建导出文件失败：%w", err))
		return
	}
	path := f.Name()
	defer os.Remove(path)

	if _, err := s.svc.ExportWorkspace(r.Context(), f); err != nil {
		_ = f.Close()
		fail(w, fmt.Errorf("导出失败：%w", err))
		return
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		_ = f.Close()
		fail(w, fmt.Errorf("读取导出文件失败：%w", err))
		return
	}
	defer f.Close()

	name := "vmbra-workspace-" + time.Now().Format("20060102-150405") + ".tar.gz"
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, name, time.Now(), f)
}

// ---- 任务 ----

func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request) {
	noCache(w)
	ok(w, map[string]any{"tasks": s.svc.Tasks.List(), "summary": s.svc.TaskSummary()})
}

func (s *Server) handleTask(w http.ResponseWriter, r *http.Request) {
	item, err := s.svc.Tasks.Get(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	rec, output := item.Snapshot()
	noCache(w)
	ok(w, map[string]any{"task": rec, "output": output})
}

func (s *Server) handleTaskCancel(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.Tasks.Cancel(r.PathValue("id")); err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"canceled": true})
}

func (s *Server) handleTaskWS(w http.ResponseWriter, r *http.Request) {
	item, err := s.svc.Tasks.Get(r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	conn, err := wsx.Upgrade(w, r)
	if err != nil {
		return
	}
	defer conn.Close()

	rec, output := item.Snapshot()
	payload, _ := json.Marshal(map[string]any{"type": "snapshot", "task": rec, "output": output})
	_ = conn.WriteText(string(payload))

	ch, cancel := item.Subscribe()
	defer cancel()
	done := item.Done()
	for {
		select {
		case chunk, alive := <-ch:
			if !alive {
				rec, _ := item.Snapshot()
				payload, _ := json.Marshal(map[string]any{"type": "done", "task": rec})
				_ = conn.WriteText(string(payload))
				return
			}
			b, _ := json.Marshal(map[string]any{"type": "output", "data": chunk})
			if err := conn.WriteText(string(b)); err != nil {
				return
			}
		case <-done:
			rec, _ := item.Snapshot()
			payload, _ := json.Marshal(map[string]any{"type": "done", "task": rec})
			_ = conn.WriteText(string(payload))
			return
		case <-time.After(30 * time.Second):
			if err := conn.WriteText(`{"type":"ping"}`); err != nil {
				return
			}
		}
	}
}

// ---- 诊断 ----

func (s *Server) handleDiagnostics(w http.ResponseWriter, r *http.Request) {
	noCache(w)
	ok(w, s.svc.Diagnostics(r.Context()))
}

func (s *Server) handleDiagnosticsBundle(w http.ResponseWriter, r *http.Request) {
	data, err := s.svc.DiagnosticsBundle(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", "vmbra-diagnostics-"+time.Now().Format("20060102-150405")+".zip"))
	_, _ = w.Write(data)
}

// ---- 设置 ----

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	noCache(w)
	ok(w, s.svc.SettingsInfo())
}

func (s *Server) handleSettingsPatch(w http.ResponseWriter, r *http.Request) {
	var patch map[string]any
	if err := decode(r, &patch); err != nil {
		fail(w, err)
		return
	}
	st, err := s.svc.UpdateSettings(patch)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"settings": st})
}

func (s *Server) handleTokenRotate(w http.ResponseWriter, r *http.Request) {
	token, err := s.svc.RotateToken()
	if err != nil {
		fail(w, err)
		return
	}
	// 旧令牌立即失效，重新下发 cookie 以保持当前会话可用
	http.SetCookie(w, &http.Cookie{
		Name:     "vmbra_token",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	ok(w, map[string]any{"token": token})
}

// ---- 操控 Mac ----

func (s *Server) handleMacStatus(w http.ResponseWriter, r *http.Request) {
	noCache(w)
	ok(w, s.svc.MacStatus())
}

func (s *Server) handleMacScreen(w http.ResponseWriter, r *http.Request) {
	data, err := s.svc.MacScreen(r.Context())
	if err != nil && len(data) == 0 {
		fail(w, err)
		return
	}
	noCache(w)
	w.Header().Set("Content-Type", "image/png")
	if err != nil {
		w.Header().Set("X-Vmbra-Warning", err.Error())
	}
	_, _ = w.Write(data)
}

func (s *Server) handleMacRun(w http.ResponseWriter, r *http.Request) {
	var req service.MacRunRequest
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	res, err := s.svc.MacRun(r.Context(), req)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, res)
}

func (s *Server) handleMacAudit(w http.ResponseWriter, r *http.Request) {
	noCache(w)
	ok(w, map[string]any{"entries": s.svc.MacAudit()})
}

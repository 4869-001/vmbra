package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/4869-001/vmbra/internal/sandbox"
	"github.com/4869-001/vmbra/internal/service"
	"github.com/4869-001/vmbra/internal/wsx"
)

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	noCache(w)
	ok(w, s.svc.Status())
}

func (s *Server) handleWorkstation(w http.ResponseWriter, r *http.Request) {
	noCache(w)
	ok(w, s.svc.WorkstationInfo())
}

func (s *Server) handleWorkstationPatch(w http.ResponseWriter, r *http.Request) {
	var patch map[string]any
	if err := decode(r, &patch); err != nil {
		fail(w, err)
		return
	}
	info, err := s.svc.UpdateSpecs(r.Context(), patch)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, info)
}

func (s *Server) handleWorkstationStart(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	info, err := s.svc.StartWorkstation(ctx)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, info)
}

func (s *Server) handleWorkstationStop(w http.ResponseWriter, r *http.Request) {
	info, err := s.svc.StopWorkstation()
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, info)
}

func (s *Server) handleWorkstationRestart(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	info, err := s.svc.RestartWorkstation(ctx)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, info)
}

func (s *Server) handleWorkstations(w http.ResponseWriter, r *http.Request) {
	noCache(w)
	ok(w, s.svc.Workstations())
}

func (s *Server) handleWorkstationCreate(w http.ResponseWriter, r *http.Request) {
	var in service.WorkstationInput
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	res, err := s.svc.CreateWorkstation(in)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, res)
}

func (s *Server) handleWorkstationUpdate(w http.ResponseWriter, r *http.Request) {
	var patch map[string]any
	if err := decode(r, &patch); err != nil {
		fail(w, err)
		return
	}
	res, err := s.svc.UpdateWorkstation(r.PathValue("id"), patch)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, res)
}

func (s *Server) handleWorkstationDelete(w http.ResponseWriter, r *http.Request) {
	res, err := s.svc.DeleteWorkstation(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, res)
}

func (s *Server) handleWorkstationActivate(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	_ = ctx
	res, err := s.svc.ActivateWorkstation(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, res)
}

func (s *Server) handleApps(w http.ResponseWriter, r *http.Request) {
	noCache(w)
	ws := s.svc.WorkstationInfo()
	running := ws["state"] == "running"
	rootOK, rootReason := s.svc.RootShellAvailable()
	ok(w, map[string]any{
		"workstation": ws,
		"desktop":     s.svc.DesktopStatus(),
		"mac":         s.svc.MacStatus(),
		"terminal": map[string]any{
			"available":     running,
			"note":          terminalNote(running),
			"rootAvailable": rootOK,
			"rootReason":    rootReason,
		},
		"deb": map[string]any{
			"supported": sandbox.CommandExists("dpkg"),
			"hint":      "本地进程隔离模式仅解析并解包 .deb；安装到系统需要虚拟机后端（Lima/UTM）。",
		},
	})
}

func terminalNote(running bool) string {
	if running {
		return "交互式 shell 与脚本、任务共用同一套环境：UTF-8 语言、时区、DNS 与代理出口。"
	}
	return "工作站未运行，启动后即可打开终端。"
}

func (s *Server) handleDesktopStart(w http.ResponseWriter, r *http.Request) {
	info, err := s.svc.StartDesktop()
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, info)
}

func (s *Server) handleDesktopStop(w http.ResponseWriter, r *http.Request) {
	ok(w, s.svc.StopDesktop())
}

// handleDesktopWS 把浏览器的 noVNC WebSocket 桥接到 VNC 服务。
func (s *Server) handleDesktopWS(w http.ResponseWriter, r *http.Request) {
	target, err := s.svc.VNCTarget()
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	upstream, err := net.DialTimeout("tcp", target, 3*time.Second)
	if err != nil {
		http.Error(w, "无法连接 VNC 服务："+err.Error(), http.StatusBadGateway)
		return
	}
	conn, err := wsx.Upgrade(w, r)
	if err != nil {
		upstream.Close()
		return
	}
	defer conn.Close()
	defer upstream.Close()

	done := make(chan struct{})
	// WS → TCP
	go func() {
		defer close(done)
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if _, err := upstream.Write(data); err != nil {
				return
			}
		}
	}()

	// TCP → WS
	buf := make([]byte, 32<<10)
	for {
		n, err := upstream.Read(buf)
		if n > 0 {
			if werr := conn.WriteBinary(buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			if err != io.EOF {
				_ = conn.WriteText(`{"type":"error","message":"VNC 连接中断"}`)
			}
			return
		}
	}
}

// handleTerminalWS 建立 WebSocket ↔ PTY 的双向通道。
func (s *Server) handleTerminalWS(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	sess, err := s.svc.OpenShell(kind)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	conn, err := wsx.Upgrade(w, r)
	if err != nil {
		sess.Close()
		return
	}
	defer conn.Close()
	defer sess.Close()

	_ = conn.WriteText(`{"type":"ready"}`)

	go func() {
		buf := make([]byte, 16<<10)
		for {
			n, err := sess.Read(buf)
			if n > 0 {
				if werr := conn.WriteBinary(buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				_ = conn.WriteText(`{"type":"exit"}`)
				conn.Close()
				return
			}
		}
	}()

	for {
		op, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if op == wsx.OpBinary {
			if _, err := sess.Write(data); err != nil {
				return
			}
			continue
		}
		var msg struct {
			Type string `json:"type"`
			Cols uint16 `json:"cols"`
			Rows uint16 `json:"rows"`
			Data string `json:"data"`
		}
		if json.Unmarshal(data, &msg) != nil {
			continue
		}
		switch msg.Type {
		case "resize":
			_ = sess.Resize(msg.Cols, msg.Rows)
		case "input":
			_, _ = sess.Write([]byte(msg.Data))
		case "ping":
			_ = conn.WriteText(`{"type":"pong"}`)
		}
	}
}

func (s *Server) handleDebUpload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(128 << 20); err != nil {
		fail(w, fmt.Errorf("解析上传失败：%w", err))
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		fail(w, fmt.Errorf("缺少文件：%w", err))
		return
	}
	defer file.Close()

	saveDir := filepath.Join(s.svc.Drv.Root, "tmp")
	if err := os.MkdirAll(saveDir, 0o700); err != nil {
		fail(w, err)
		return
	}
	safeName := strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' {
			return '-'
		}
		return r
	}, filepath.Base(hdr.Filename))
	savePath := filepath.Join(saveDir, safeName)
	dst, err := os.Create(savePath)
	if err != nil {
		fail(w, err)
		return
	}
	n, err := io.Copy(dst, file)
	dst.Close()
	if err != nil {
		fail(w, err)
		return
	}

	item, err := s.svc.InstallDeb(r.Context(), service.InstallDebOptions{
		Filename: safeName,
		Size:     n,
		SavePath: savePath,
	})
	if err != nil {
		fail(w, err)
		return
	}
	rec := item.Record()
	ok(w, map[string]any{"taskId": rec.ID, "filename": safeName, "size": n})
}

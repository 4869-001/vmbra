package api

import "net/http"

// ---- 代理订阅（Clash / Mihomo）----

// handleClashView 返回订阅库 + 内核对状态，供「代理」页一次性渲染。
func (s *Server) handleClashView(w http.ResponseWriter, r *http.Request) {
	noCache(w)
	view, err := s.svc.ClashView()
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, view)
}

// handleClashImport 导入订阅：url 与 content 二选一，content 优先。
func (s *Server) handleClashImport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name    string `json:"name"`
		URL     string `json:"url"`
		Content string `json:"content"`
	}
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	view, err := s.svc.ImportClashSource(req.Name, req.URL, req.Content)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, view)
}

// handleClashApply 选中节点 → 启动内核 → 探测出口 → 自动对齐环境。
func (s *Server) handleClashApply(w http.ResponseWriter, r *http.Request) {
	var req struct {
		NodeID string `json:"nodeId"`
	}
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	res, err := s.svc.ApplyClashNode(req.NodeID)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, res)
}

// handleClashStop 停止内核并回收代理变量。
func (s *Server) handleClashStop(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.StopClash(); err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"stopped": true})
}

// handleClashSourceDelete 删除整个订阅。
func (s *Server) handleClashSourceDelete(w http.ResponseWriter, r *http.Request) {
	view, err := s.svc.DeleteClashSource(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, view)
}

// handleClashNodeDelete 删除单个节点。
func (s *Server) handleClashNodeDelete(w http.ResponseWriter, r *http.Request) {
	view, err := s.svc.DeleteClashNode(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, view)
}

// handleClashSelect 切换节点：内核在跑时热切换，不中断已有会话。
func (s *Server) handleClashSelect(w http.ResponseWriter, r *http.Request) {
	var req struct {
		NodeID string `json:"nodeId"`
	}
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	res, err := s.svc.SelectClashNode(req.NodeID)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, res)
}

// handleClashDelay 对订阅内全部节点测延迟。
func (s *Server) handleClashDelay(w http.ResponseWriter, r *http.Request) {
	res, err := s.svc.TestClashDelays()
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"results": res})
}

// handleClashRefresh 手动刷新一条订阅。
func (s *Server) handleClashRefresh(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SourceID string `json:"sourceId"`
	}
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	view, err := s.svc.RefreshClashSource(req.SourceID)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, view)
}

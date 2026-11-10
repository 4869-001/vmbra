package api

import "net/http"

// ---- 出口闸门 ----

// handleEgressGate 返回租约状态与最近一次检查证据。
func (s *Server) handleEgressGate(w http.ResponseWriter, r *http.Request) {
	noCache(w)
	ok(w, s.svc.EgressSnapshot())
}

// handleEgressCheck 立即执行一次出口检查（界面上的「立即检查」）。
func (s *Server) handleEgressCheck(w http.ResponseWriter, r *http.Request) {
	ok(w, s.svc.CheckEgressNow())
}

// handleEgressRepair 一键修复：按当前节点重新探测并回写语言/时区/代理。
func (s *Server) handleEgressRepair(w http.ResponseWriter, r *http.Request) {
	res, err := s.svc.RepairEgress()
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, res)
}

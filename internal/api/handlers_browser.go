package api

import "net/http"

// handleBrowser 返回受控浏览器配置与运行状态。
func (s *Server) handleBrowser(w http.ResponseWriter, r *http.Request) {
	noCache(w)
	ok(w, s.svc.BrowserStatus())
}

func (s *Server) handleBrowserUpdate(w http.ResponseWriter, r *http.Request) {
	var patch map[string]any
	if err := decode(r, &patch); err != nil {
		fail(w, err)
		return
	}
	if _, err := s.svc.UpdateBrowser(patch); err != nil {
		fail(w, err)
		return
	}
	ok(w, s.svc.BrowserStatus())
}

// handleBrowserVerify 用 headless Chrome 校验语言与时区是否生效。
func (s *Server) handleBrowserVerify(w http.ResponseWriter, r *http.Request) {
	res, err := s.svc.VerifyBrowser(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, res)
}

// handleBrowserOpen 启动（或激活）固定语言/时区的浏览器实例。
// 可选 body：{"url":"http://127.0.0.1:7788/?token=..."}，在同一个受控 profile 里打开该页面。
func (s *Server) handleBrowserOpen(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL string `json:"url"`
	}
	if r.ContentLength > 0 {
		if err := decode(r, &body); err != nil {
			fail(w, err)
			return
		}
	}
	res, err := s.svc.OpenBrowser(body.URL)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, res)
}

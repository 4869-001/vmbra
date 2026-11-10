package service

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/4869-001/vmbra/internal/clash"
	"github.com/4869-001/vmbra/internal/store"
)

// refreshTick 是订阅刷新的扫描周期：到期与否由每个来源自己的 intervalHours 决定。
const refreshTick = 10 * time.Minute

// RefreshClashSource 重新下载并解析指定订阅；节点集合整体替换，
// 若当前选中节点消失则自动落到第一个可用节点。
func (s *Service) RefreshClashSource(id string) (ClashView, error) {
	doc, err := clash.LoadStore(s.Cfg.DataDir)
	if err != nil {
		return ClashView{}, err
	}
	var target *clash.Source
	for i := range doc.Sources {
		if doc.Sources[i].ID == id {
			target = &doc.Sources[i]
			break
		}
	}
	if target == nil {
		return ClashView{}, fmt.Errorf("订阅不存在")
	}
	if target.URL == "" {
		return ClashView{}, fmt.Errorf("该订阅是粘贴导入的，没有可刷新的链接")
	}
	body, err := fetchSubscription(target.URL)
	if err != nil {
		return ClashView{}, err
	}
	nodes, err := clash.Parse([]byte(body))
	if err != nil {
		return ClashView{}, err
	}
	if len(nodes) == 0 {
		return ClashView{}, fmt.Errorf("刷新后没有可用节点")
	}

	keepSelected := ""
	if prev, ok := doc.Node(doc.Selected); ok {
		for _, n := range nodes {
			if n.ID == prev.ID {
				keepSelected = n.ID
				break
			}
		}
	}
	target.Nodes = nodes
	target.UpdatedAt = time.Now()
	if keepSelected != "" {
		doc.Selected = keepSelected
	} else if len(nodes) > 0 {
		doc.Selected = nodes[0].ID
	}
	if err := doc.Save(s.Cfg.DataDir); err != nil {
		return ClashView{}, err
	}
	// 选中节点变了就顺手切过去，用户不需要再点一次。
	if keepSelected == "" && doc.Selected != "" {
		if _, err := s.SelectClashNode(doc.Selected); err != nil {
			return s.ClashView()
		}
	}
	return s.ClashView()
}

// RefreshDueSources 刷新所有到期的订阅；单个失败不影响其它来源。
func (s *Service) RefreshDueSources() {
	doc, err := clash.LoadStore(s.Cfg.DataDir)
	if err != nil {
		return
	}
	for _, src := range doc.Sources {
		if src.URL == "" || src.IntervalHours <= 0 {
			continue
		}
		if time.Since(src.UpdatedAt) < time.Duration(src.IntervalHours)*time.Hour {
			continue
		}
		if _, err := s.RefreshClashSource(src.ID); err != nil {
			s.audit("proxy.refresh.failed", src.Name+"："+err.Error())
			continue
		}
		s.audit("proxy.refresh", src.Name)
	}
}

// StartProxyRefreshLoop 在后台按周期刷新订阅。
func (s *Service) StartProxyRefreshLoop(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(refreshTick)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.RefreshDueSources()
			}
		}
	}()
}

// TestClashDelays 对订阅内全部节点测延迟，按从快到慢返回。
// 内核未运行时先用当前选中节点拉起（配置里已含全部节点），测完可直接热切换。
func (s *Service) TestClashDelays() ([]clash.DelayResult, error) {
	doc, err := clash.LoadStore(s.Cfg.DataDir)
	if err != nil {
		return nil, err
	}
	nodes := doc.AllNodes()
	if len(nodes) == 0 {
		return nil, fmt.Errorf("还没有导入订阅")
	}
	if !s.Clash.Running() {
		selected, ok := doc.Node(doc.Selected)
		if !ok {
			selected = nodes[0]
		}
		if err := s.Clash.StartAll(nodes, selected); err != nil {
			return nil, err
		}
	}

	results := make([]clash.DelayResult, len(nodes))
	sem := make(chan struct{}, 6)
	var wg sync.WaitGroup
	for i, n := range nodes {
		wg.Add(1)
		go func(i int, n clash.Node) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			delay, err := s.Clash.Delay(n.Name, 5*time.Second)
			if err != nil {
				results[i] = clash.DelayResult{Name: n.Name, Error: "超时"}
				return
			}
			results[i] = clash.DelayResult{Name: n.Name, Delay: delay}
		}(i, n)
	}
	wg.Wait()

	sort.SliceStable(results, func(i, j int) bool {
		ai, aj := results[i], results[j]
		if ai.Error != "" || aj.Error != "" {
			return ai.Error == ""
		}
		return ai.Delay < aj.Delay
	})
	return results, nil
}

// audit 写一条轻量审计（失败不阻断主流程）。
func (s *Service) audit(action, detail string) {
	_ = s.Store.Update(func(st *store.State) {
		st.Audit = append(st.Audit, store.AuditEntry{
			At:     time.Now(),
			Action: action,
			Detail: detail,
		})
		if len(st.Audit) > 200 {
			st.Audit = st.Audit[len(st.Audit)-200:]
		}
	})
}

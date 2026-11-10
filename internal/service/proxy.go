package service

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/4869-001/vmbra/internal/clash"
	"github.com/4869-001/vmbra/internal/config"
	"github.com/4869-001/vmbra/internal/store"
)

// ClashView 是「代理」页面的完整数据：内核对不对、来源有哪些、当前选中谁、出口是什么。
type ClashView struct {
	Available  bool            `json:"available"`
	CoreBinary string          `json:"coreBinary"`
	Selected   string          `json:"selected"`
	Sources    []clash.Source  `json:"sources"`
	Core       store.ProxyCore `json:"core"`
}

// ClashView 汇总当前订阅库与内核对状态。
func (s *Service) ClashView() (ClashView, error) {
	doc, err := clash.LoadStore(s.Cfg.DataDir)
	if err != nil {
		return ClashView{}, err
	}
	st := s.Store.Copy()
	if doc.Selected == "" {
		doc.Selected = st.ProxyCore.NodeID
	}
	return ClashView{
		Available:  s.Clash.Available(),
		CoreBinary: s.Clash.Binary(),
		Selected:   doc.Selected,
		Sources:    doc.Sources,
		Core:       st.ProxyCore,
	}, nil
}

// ImportClashSource 导入一条订阅：link 与 content 二选一，
// content 优先（支持用户直接粘贴 YAML / 节点列表）。导入后若尚未选择节点，
// 自动选中第一个节点，减少一次点击。
func (s *Service) ImportClashSource(name, link, content string) (ClashView, error) {
	name = strings.TrimSpace(name)
	link = strings.TrimSpace(link)
	content = strings.TrimSpace(content)
	if content == "" && link == "" {
		return ClashView{}, errors.New("请提供订阅链接或直接粘贴节点内容")
	}
	if content == "" {
		body, err := fetchSubscription(link)
		if err != nil {
			return ClashView{}, err
		}
		content = body
	}

	nodes, err := clash.Parse([]byte(content))
	if err != nil {
		return ClashView{}, err
	}
	if len(nodes) == 0 {
		return ClashView{}, errors.New("订阅里没有解析到可用节点")
	}
	if name == "" {
		if link != "" {
			if u, err := url.Parse(link); err == nil && u.Host != "" {
				name = u.Host
			}
		}
		if name == "" {
			name = "订阅 " + time.Now().Format("01-02 15:04")
		}
	}

	doc, err := clash.LoadStore(s.Cfg.DataDir)
	if err != nil {
		return ClashView{}, err
	}
	interval := 6
	for _, src := range doc.Sources {
		if src.ID == sourceID(name, link) && src.IntervalHours > 0 {
			interval = src.IntervalHours // 刷新时沿用用户设置的周期
		}
	}
	doc.UpsertSource(clash.Source{
		ID:            sourceID(name, link),
		Name:          name,
		URL:           link,
		IntervalHours: interval,
		UpdatedAt:     time.Now(),
		Nodes:         nodes,
	})
	if doc.Selected == "" {
		doc.Selected = nodes[0].ID
	}
	if err := doc.Save(s.Cfg.DataDir); err != nil {
		return ClashView{}, err
	}
	return s.ClashView()
}

// DeleteClashSource 删除整个订阅来源。
func (s *Service) DeleteClashSource(id string) (ClashView, error) {
	doc, err := clash.LoadStore(s.Cfg.DataDir)
	if err != nil {
		return ClashView{}, err
	}
	if !doc.RemoveSource(id) {
		return ClashView{}, errors.New("订阅不存在")
	}
	if err := doc.Save(s.Cfg.DataDir); err != nil {
		return ClashView{}, err
	}
	return s.ClashView()
}

// DeleteClashNode 删除单个节点。
func (s *Service) DeleteClashNode(id string) (ClashView, error) {
	doc, err := clash.LoadStore(s.Cfg.DataDir)
	if err != nil {
		return ClashView{}, err
	}
	if !doc.RemoveNode(id) {
		return ClashView{}, errors.New("节点不存在")
	}
	if err := doc.Save(s.Cfg.DataDir); err != nil {
		return ClashView{}, err
	}
	return s.ClashView()
}

// ApplyResult 是「一键对齐」的结果：出口地区 + 自动写入的语言/时区。
type ApplyResult struct {
	Core        store.ProxyCore `json:"core"`
	Lang        string          `json:"lang"`
	BrowserLang string          `json:"browserLang"`
	TimeZone    string          `json:"timeZone"`
	ExitIP      string          `json:"exitIp"`
	Country     string          `json:"country"`
	City        string          `json:"city"`
}

// ApplyClashNode 用指定节点启动内核并探测出口，然后一次性把工作站的
// 语言、文字、时区、代理与浏览器出口全部对齐到该地区。
// 订阅里的全部节点都会写进内核配置，之后测速与切换都不需要重启。
func (s *Service) ApplyClashNode(nodeID string) (ApplyResult, error) {
	doc, err := clash.LoadStore(s.Cfg.DataDir)
	if err != nil {
		return ApplyResult{}, err
	}
	node, ok := doc.Node(nodeID)
	if !ok {
		return ApplyResult{}, errors.New("节点不存在，请先导入订阅")
	}
	if err := s.Clash.StartAll(doc.AllNodes(), node); err != nil {
		return ApplyResult{}, err
	}
	return s.alignEgress(node, doc)
}

// SelectClashNode 切换节点：内核已在跑就热切换（不中断），否则整体重启。
func (s *Service) SelectClashNode(nodeID string) (ApplyResult, error) {
	doc, err := clash.LoadStore(s.Cfg.DataDir)
	if err != nil {
		return ApplyResult{}, err
	}
	node, ok := doc.Node(nodeID)
	if !ok {
		return ApplyResult{}, errors.New("节点不存在，请先导入订阅")
	}
	if s.Clash.Running() {
		if err := s.Clash.SelectNode(node); err == nil {
			return s.alignEgress(node, doc)
		}
	}
	if err := s.Clash.StartAll(doc.AllNodes(), node); err != nil {
		return ApplyResult{}, err
	}
	return s.alignEgress(node, doc)
}

// alignEgress 是「内核对已在运行」之后的共同收尾：探测出口 → 回写环境 → 复核闸门。
func (s *Service) alignEgress(node clash.Node, doc *clash.StoreDoc) (ApplyResult, error) {
	geo, err := probeExit(s.Clash.HTTPProxy())
	if err != nil {
		s.Clash.Stop()
		return ApplyResult{}, fmt.Errorf("节点无法出网：%w", err)
	}

	lang, browserLang := localeForCountry(geo.CountryCode)
	tz := geo.TimeZone
	if tz == "" {
		tz = timezoneForCountry(geo.CountryCode)
	}
	httpProxy := s.Clash.HTTPProxy()
	socksProxy := s.Clash.SOCKSProxy()

	// 单出口原则：启用 Clash 内核时关闭 VPS 隧道，避免两条链路打架。
	s.VPSTunnel.Stop()

	core := store.ProxyCore{
		Running:     true,
		NodeID:      node.ID,
		NodeName:    node.Name,
		NodeType:    node.Type,
		ExitIP:      geo.IP,
		Country:     geo.Country,
		CountryCode: geo.CountryCode,
		City:        geo.City,
		TimeZone:    tz,
		HTTPProxy:   httpProxy,
		SOCKSProxy:  socksProxy,
		CoreBinary:  s.Clash.Binary(),
		UpdatedAt:   time.Now(),
	}

	err = s.Store.Update(func(st *store.State) {
		st.ProxyCore = core
		st.Locale.Lang = lang
		st.Locale.Encoding = "UTF-8"
		st.Locale.Timezone = tz
		st.Locale.FollowVPS = false
		st.Browser.Lang = browserLang
		st.Browser.TimeZone = tz
		st.Browser.UseVPSEgress = false
		st.Proxy = store.Proxy{
			Mode:    "manual",
			HTTP:    httpProxy,
			HTTPS:   httpProxy,
			NoProxy: "localhost,127.0.0.1,::1",
		}
		st.Settings.NetworkEnabled = true
		st.VPS.Enabled = false
		s.mirrorActiveEnv(st)
	})
	if err != nil {
		return ApplyResult{}, err
	}
	doc.Selected = node.ID
	if err := doc.Save(s.Cfg.DataDir); err != nil {
		return ApplyResult{}, err
	}
	if err := s.WriteShellEnv(); err != nil {
		return ApplyResult{}, err
	}
	// 换节点等于换了出口，旧租约立即作废并按新链路重检一次。
	s.Gate.Invalidate("切换出口节点")
	s.CheckEgressNow()
	return ApplyResult{
		Core:        core,
		Lang:        lang,
		BrowserLang: browserLang,
		TimeZone:    tz,
		ExitIP:      geo.IP,
		Country:     geo.Country,
		City:        geo.City,
	}, nil
}

// StopClash 停止内核并把工作站恢复到「未配置代理」的状态。
func (s *Service) StopClash() error {
	s.Clash.Stop()
	err := s.Store.Update(func(st *store.State) {
		st.ProxyCore.Running = false
		st.ProxyCore.HTTPProxy = ""
		st.ProxyCore.SOCKSProxy = ""
		st.Proxy = store.Proxy{Mode: "system"}
		s.mirrorActiveEnv(st)
	})
	if err != nil {
		return err
	}
	return s.WriteShellEnv()
}

// exitInfo 是出口探测结果。
type exitInfo struct {
	IP          string
	Country     string
	CountryCode string
	City        string
	TimeZone    string
}

// ProxyClient 构造一个走指定代理的 HTTP 客户端。proxyURL 支持 http:// 与 socks5://。
// 传空字符串表示直连。出口检查、节点测速与地区探测共用这一份实现。
func ProxyClient(proxyURL string, timeout time.Duration) (*http.Client, error) {
	transport := &http.Transport{
		ForceAttemptHTTP2:   true,
		TLSHandshakeTimeout: 8 * time.Second,
	}
	if proxyURL != "" {
		pu, err := url.Parse(proxyURL)
		if err != nil {
			return nil, err
		}
		transport.Proxy = http.ProxyURL(pu)
	}
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return &http.Client{Timeout: timeout, Transport: transport}, nil
}

// probeExit 通过刚启动的内核探测真实出口，依次尝试多个免费接口。
func probeExit(httpProxy string) (exitInfo, error) {
	if httpProxy == "" {
		return exitInfo{}, errors.New("本地代理未就绪")
	}
	client, err := ProxyClient(httpProxy, 15*time.Second)
	if err != nil {
		return exitInfo{}, err
	}
	return probeExitWith(client)
}

// probeExitWith 用一个已配置好代理的客户端探测出口地区。
func probeExitWith(client *http.Client) (exitInfo, error) {
	var lastErr error
	// ip-api.com 只支持 http，但对国内网络更稳；失败再走 https 接口。
	for _, ep := range []string{
		"http://ip-api.com/json/?fields=status,country,countryCode,city,timezone,query",
		"https://ipapi.co/json/",
		"https://ipinfo.io/json",
	} {
		info, err := fetchExit(client, ep)
		if err == nil && info.IP != "" {
			return info, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errors.New("探测接口均不可用")
	}
	return exitInfo{}, lastErr
}

func fetchExit(client *http.Client, endpoint string) (exitInfo, error) {
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return exitInfo{}, err
	}
	req.Header.Set("User-Agent", "Vmbra/"+config.Version)
	resp, err := client.Do(req)
	if err != nil {
		return exitInfo{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return exitInfo{}, err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return exitInfo{}, err
	}
	pick := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := m[k].(string); ok && v != "" {
				return v
			}
		}
		return ""
	}
	if id, ok := m["ip"].(string); ok && id != "" {
		// ipapi.co / ipinfo.io 形态
		cc := pick("country_code", "country")
		return exitInfo{
			IP:          id,
			Country:     pick("country_name", "country"),
			CountryCode: strings.ToUpper(cc),
			City:        pick("city"),
			TimeZone:    pick("timezone"),
		}, nil
	}
	if status, _ := m["status"].(string); status == "success" {
		return exitInfo{
			IP:          pick("query"),
			Country:     pick("country"),
			CountryCode: strings.ToUpper(pick("countryCode")),
			City:        pick("city"),
			TimeZone:    pick("timezone"),
		}, nil
	}
	return exitInfo{}, fmt.Errorf("探测接口未返回有效数据")
}

func fetchSubscription(link string) (string, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest(http.MethodGet, link, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "clash.meta/1.18.0")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("下载订阅失败：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("订阅返回状态 %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func sourceID(name, link string) string {
	sum := sha1.Sum([]byte(name + "|" + link))
	return hex.EncodeToString(sum[:])[:12]
}

// localeForCountry 把国家码映射为「系统语言 + 浏览器语言」。
// 目标不是穷举，而是常见节点地区的合理默认；未知地区退回 en_US。
func localeForCountry(cc string) (posix, browser string) {
	switch strings.ToUpper(cc) {
	case "CN":
		return "zh_CN.UTF-8", "zh-CN"
	case "TW":
		return "zh_TW.UTF-8", "zh-TW"
	case "HK":
		return "zh_HK.UTF-8", "zh-HK"
	case "MO":
		return "zh_MO.UTF-8", "zh-MO"
	case "SG":
		return "en_SG.UTF-8", "en-SG"
	case "MY":
		return "en_MY.UTF-8", "en-MY"
	case "JP":
		return "ja_JP.UTF-8", "ja-JP"
	case "KR":
		return "ko_KR.UTF-8", "ko-KR"
	case "GB":
		return "en_GB.UTF-8", "en-GB"
	case "US":
		return "en_US.UTF-8", "en-US"
	case "CA":
		return "en_CA.UTF-8", "en-CA"
	case "AU":
		return "en_AU.UTF-8", "en-AU"
	case "NZ":
		return "en_NZ.UTF-8", "en-NZ"
	case "DE":
		return "de_DE.UTF-8", "de-DE"
	case "FR":
		return "fr_FR.UTF-8", "fr-FR"
	case "NL":
		return "nl_NL.UTF-8", "nl-NL"
	case "ES":
		return "es_ES.UTF-8", "es-ES"
	case "IT":
		return "it_IT.UTF-8", "it-IT"
	case "PT":
		return "pt_PT.UTF-8", "pt-PT"
	case "BR":
		return "pt_BR.UTF-8", "pt-BR"
	case "RU":
		return "ru_RU.UTF-8", "ru-RU"
	case "TR":
		return "tr_TR.UTF-8", "tr-TR"
	case "PL":
		return "pl_PL.UTF-8", "pl-PL"
	case "SE":
		return "sv_SE.UTF-8", "sv-SE"
	case "CH":
		return "de_CH.UTF-8", "de-CH"
	case "IN":
		return "en_IN.UTF-8", "en-IN"
	case "AE":
		return "en_AE.UTF-8", "en-AE"
	default:
		return "en_US.UTF-8", "en-US"
	}
}

// timezoneForCountry 在探测接口没给时区时兜底，取该国最常用的时区。
func timezoneForCountry(cc string) string {
	switch strings.ToUpper(cc) {
	case "CN":
		return "Asia/Shanghai"
	case "TW":
		return "Asia/Taipei"
	case "HK", "MO":
		return "Asia/Hong_Kong"
	case "SG":
		return "Asia/Singapore"
	case "MY":
		return "Asia/Kuala_Lumpur"
	case "JP":
		return "Asia/Tokyo"
	case "KR":
		return "Asia/Seoul"
	case "GB":
		return "Europe/London"
	case "DE":
		return "Europe/Berlin"
	case "FR":
		return "Europe/Paris"
	case "NL":
		return "Europe/Amsterdam"
	case "ES":
		return "Europe/Madrid"
	case "IT":
		return "Europe/Rome"
	case "RU":
		return "Europe/Moscow"
	case "TR":
		return "Europe/Istanbul"
	case "AU":
		return "Australia/Sydney"
	case "CA":
		return "America/Toronto"
	case "BR":
		return "America/Sao_Paulo"
	case "IN":
		return "Asia/Kolkata"
	default:
		return "America/Los_Angeles"
	}
}

// Package clash 负责 Clash/Mihomo 订阅与节点的导入、持久化与内核托管。
//
// 设计目标：用户只粘贴一条订阅链接，Vmbra 自动挑选节点、拉起内核、
// 探测出口地区，并把工作站的语言/时区/代理一次性对齐，做到「零配置」。
package clash

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Node 是一个可被 mihomo 直接使用的代理节点。
type Node struct {
	ID     string         `json:"id"`
	Name   string         `json:"name"`
	Type   string         `json:"type"`
	Server string         `json:"server"`
	Port   int            `json:"port"`
	Raw    map[string]any `json:"raw,omitempty"`
}

// Endpoint 返回 server:port，用于界面展示。
func (n Node) Endpoint() string {
	if n.Server == "" || n.Port == 0 {
		return ""
	}
	return net.JoinHostPort(n.Server, strconv.Itoa(n.Port))
}

// supportedTypes 是 mihomo 能直接消费的代理类型；同时用于屏蔽
// DIRECT/REJECT/策略组等「不是节点」的条目。
var supportedTypes = map[string]bool{
	"ss": true, "ssr": true, "vmess": true, "vless": true, "trojan": true,
	"hysteria": true, "hysteria2": true, "tuic": true, "snell": true,
	"socks5": true, "http": true, "wireguard": true, "anytls": true,
	"mieru": true, "ssh": true,
}

// Parse 解析订阅内容，支持三种常见形态：
//  1. Clash/Mihomo YAML（顶层 proxies 或 payload 列表）
//  2. 纯节点列表 YAML（顶层就是 - {name: ...}）
//  3. URI 列表（ss:// trojan:// vmess:// socks5:// http://），
//     常见于 V2Ray 订阅；若整体被 base64 包裹会先解码。
func Parse(data []byte) ([]Node, error) {
	body := strings.TrimSpace(string(data))
	if body == "" {
		return nil, fmt.Errorf("订阅内容为空")
	}
	if decoded, ok := tryBase64(body); ok {
		body = decoded
	}

	if nodes := parseYAML([]byte(body)); len(nodes) > 0 {
		return nodes, nil
	}
	if nodes := parseURIs(body); len(nodes) > 0 {
		return nodes, nil
	}
	return nil, fmt.Errorf("无法识别的订阅格式（既不是 Clash YAML，也不是节点 URI 列表）")
}

func parseYAML(data []byte) []Node {
	var doc any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil
	}
	var list []any
	switch v := doc.(type) {
	case map[string]any:
		for _, key := range []string{"proxies", "payload", "Proxy"} {
			if arr, ok := v[key].([]any); ok {
				list = arr
				break
			}
		}
	case []any:
		list = v
	}
	if len(list) == 0 {
		return nil
	}
	out := make([]Node, 0, len(list))
	for _, item := range list {
		raw, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if n, ok := nodeFromRaw(raw); ok {
			out = append(out, n)
		}
	}
	return dedupe(out)
}

func nodeFromRaw(raw map[string]any) (Node, bool) {
	name := strings.TrimSpace(asString(raw["name"]))
	typ := strings.ToLower(strings.TrimSpace(asString(raw["type"])))
	if name == "" || !supportedTypes[typ] {
		return Node{}, false
	}
	server := strings.TrimSpace(asString(raw["server"]))
	port := asInt(raw["port"])
	if server == "" || port <= 0 || port > 65535 {
		return Node{}, false
	}
	n := Node{Name: name, Type: typ, Server: server, Port: port, Raw: raw}
	n.ID = nodeID(n)
	return n, true
}

func parseURIs(body string) []Node {
	var out []Node
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if n, ok := nodeFromURI(line); ok {
			out = append(out, n)
		}
	}
	return dedupe(out)
}

func nodeFromURI(uri string) (Node, bool) {
	switch {
	case strings.HasPrefix(uri, "ss://"):
		return parseSS(uri)
	case strings.HasPrefix(uri, "trojan://"):
		return parseTrojan(uri)
	case strings.HasPrefix(uri, "vmess://"):
		return parseVMess(uri)
	case strings.HasPrefix(uri, "socks5://"), strings.HasPrefix(uri, "socks://"),
		strings.HasPrefix(uri, "http://"):
		return parsePlainProxy(uri)
	}
	return Node{}, false
}

// parseSS 兼容 SIP002（base64(method:pass)@host:port#name）与整段 base64 两种写法。
func parseSS(uri string) (Node, bool) {
	body := strings.TrimPrefix(uri, "ss://")
	name := ""
	if idx := strings.Index(body, "#"); idx >= 0 {
		if v, err := url.PathUnescape(body[idx+1:]); err == nil {
			name = v
		}
		body = body[:idx]
	}
	body = strings.SplitN(body, "?", 2)[0]

	var method, pass, host string
	var port int
	if at := strings.LastIndex(body, "@"); at >= 0 {
		userInfo := body[:at]
		if decoded, ok := tryBase64(userInfo); ok {
			userInfo = decoded
		} else if v, err := url.PathUnescape(userInfo); err == nil {
			userInfo = v
		}
		parts := strings.SplitN(userInfo, ":", 2)
		if len(parts) != 2 {
			return Node{}, false
		}
		method, pass = parts[0], parts[1]
		host, port = splitHostPort(body[at+1:])
	} else {
		decoded, ok := tryBase64(body)
		if !ok {
			return Node{}, false
		}
		inner := strings.TrimPrefix(decoded, "ss://")
		at := strings.LastIndex(inner, "@")
		if at < 0 {
			return Node{}, false
		}
		parts := strings.SplitN(inner[:at], ":", 2)
		if len(parts) != 2 {
			return Node{}, false
		}
		method, pass = parts[0], parts[1]
		host, port = splitHostPort(inner[at+1:])
	}
	if method == "" || host == "" || port == 0 {
		return Node{}, false
	}
	if name == "" {
		name = host + ":" + strconv.Itoa(port)
	}
	raw := map[string]any{
		"name": name, "type": "ss", "server": host, "port": port,
		"cipher": method, "password": pass, "udp": false,
	}
	n, ok := nodeFromRaw(raw)
	return n, ok
}

func parseTrojan(uri string) (Node, bool) {
	u, err := url.Parse(uri)
	if err != nil || u.Host == "" {
		return Node{}, false
	}
	host, port := splitHostPort(u.Host)
	if host == "" || port == 0 {
		return Node{}, false
	}
	name := u.Fragment
	if name == "" {
		name = host + ":" + strconv.Itoa(port)
	}
	raw := map[string]any{
		"name": name, "type": "trojan", "server": host, "port": port,
		"password": u.User.Username(), "udp": false,
	}
	if sni := u.Query().Get("sni"); sni != "" {
		raw["sni"] = sni
	}
	if strings.EqualFold(u.Query().Get("allowInsecure"), "1") {
		raw["skip-cert-verify"] = true
	}
	n, ok := nodeFromRaw(raw)
	return n, ok
}

func parseVMess(uri string) (Node, bool) {
	payload := strings.TrimPrefix(uri, "vmess://")
	decoded, ok := tryBase64(payload)
	if !ok {
		return Node{}, false
	}
	var vm struct {
		PS   string `json:"ps"`
		Add  string `json:"add"`
		Port any    `json:"port"`
		ID   string `json:"id"`
		AID  any    `json:"aid"`
		Net  string `json:"net"`
		Host string `json:"host"`
		Path string `json:"path"`
		TLS  string `json:"tls"`
		SNI  string `json:"sni"`
	}
	if err := json.Unmarshal([]byte(decoded), &vm); err != nil {
		return Node{}, false
	}
	port := asInt(vm.Port)
	if vm.Add == "" || port == 0 || vm.ID == "" {
		return Node{}, false
	}
	name := vm.PS
	if name == "" {
		name = vm.Add + ":" + strconv.Itoa(port)
	}
	raw := map[string]any{
		"name": name, "type": "vmess", "server": vm.Add, "port": port,
		"uuid": vm.ID, "alterId": asInt(vm.AID), "cipher": "auto", "udp": false,
	}
	if vm.Net != "" && vm.Net != "tcp" {
		raw["network"] = vm.Net
	}
	if vm.TLS == "tls" {
		raw["tls"] = true
		if vm.SNI != "" {
			raw["servername"] = vm.SNI
		} else if vm.Host != "" {
			raw["servername"] = vm.Host
		}
	}
	if vm.Net == "ws" {
		opts := map[string]any{}
		if vm.Path != "" {
			opts["path"] = vm.Path
		}
		if vm.Host != "" {
			opts["headers"] = map[string]any{"Host": vm.Host}
		}
		if len(opts) > 0 {
			raw["ws-opts"] = opts
		}
	}
	n, ok := nodeFromRaw(raw)
	return n, ok
}

func parsePlainProxy(uri string) (Node, bool) {
	u, err := url.Parse(uri)
	if err != nil || u.Host == "" {
		return Node{}, false
	}
	typ := strings.ToLower(u.Scheme)
	if typ == "socks" {
		typ = "socks5"
	}
	if typ != "socks5" && typ != "http" {
		return Node{}, false
	}
	host, port := splitHostPort(u.Host)
	if host == "" || port == 0 {
		return Node{}, false
	}
	name := u.Fragment
	if name == "" {
		name = host + ":" + strconv.Itoa(port)
	}
	raw := map[string]any{
		"name": name, "type": typ, "server": host, "port": port, "udp": false,
	}
	if u.User != nil {
		raw["username"] = u.User.Username()
		if pw, ok := u.User.Password(); ok {
			raw["password"] = pw
		}
	}
	n, ok := nodeFromRaw(raw)
	return n, ok
}

// dedupe 去掉重复节点，并为重名节点补后缀（mihomo 要求 name 全局唯一）。
func dedupe(nodes []Node) []Node {
	seenID := map[string]bool{}
	seenName := map[string]int{}
	out := make([]Node, 0, len(nodes))
	for _, n := range nodes {
		if seenID[n.ID] {
			continue
		}
		if cnt, dup := seenName[n.Name]; dup {
			seenName[n.Name] = cnt + 1
			n.Name = fmt.Sprintf("%s #%d", n.Name, cnt+1)
			n.ID = nodeID(n)
			if seenID[n.ID] {
				continue
			}
		} else {
			seenName[n.Name] = 1
		}
		seenID[n.ID] = true
		out = append(out, n)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func nodeID(n Node) string {
	sum := sha1.Sum([]byte(n.Type + "|" + n.Server + "|" + strconv.Itoa(n.Port) + "|" + n.Name))
	return hex.EncodeToString(sum[:])[:12]
}

func tryBase64(s string) (string, bool) {
	cleaned := strings.Map(func(r rune) rune {
		switch r {
		case '\n', '\r', ' ', '\t':
			return -1
		}
		return r
	}, s)
	if len(cleaned) < 8 {
		return "", false
	}
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding,
	} {
		raw, err := enc.DecodeString(cleaned)
		if err != nil {
			continue
		}
		text := string(raw)
		if strings.ContainsAny(text, "\n:") && isPrintable(text) {
			return text, true
		}
	}
	return "", false
}

func isPrintable(s string) bool {
	for _, r := range s {
		if r < 0x09 || (r > 0x0d && r < 0x20) {
			return false
		}
	}
	return true
}

func splitHostPort(s string) (string, int) {
	s = strings.TrimSpace(strings.SplitN(s, "/", 2)[0])
	if s == "" {
		return "", 0
	}
	host, portStr, err := net.SplitHostPort(s)
	if err != nil {
		return "", 0
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return "", 0
	}
	return host, port
}

func asString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return ""
	default:
		return fmt.Sprint(t)
	}
}

func asInt(v any) int {
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(t))
		if err != nil {
			return 0
		}
		return n
	default:
		return 0
	}
}

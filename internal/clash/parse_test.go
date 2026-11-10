package clash

import (
	"encoding/base64"
	"strings"
	"testing"
)

const clashYAML = `
port: 7890
proxies:
  - name: "LA-01"
    type: ss
    server: 1.2.3.4
    port: 8443
    cipher: aes-256-gcm
    password: secret
  - name: "JP-02"
    type: vmess
    server: 5.6.7.8
    port: 443
    uuid: 11111111-2222-3333-4444-555555555555
    alterId: 0
    cipher: auto
    tls: true
    network: ws
    ws-opts:
      path: /ray
      headers:
        Host: example.com
  - name: "DIRECT"
    type: direct
proxy-groups:
  - name: PROXY
    type: select
    proxies: ["LA-01", "JP-02"]
`

func TestParseClashYAML(t *testing.T) {
	nodes, err := Parse([]byte(clashYAML))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("期望 2 个节点（direct 应被过滤），实际 %d", len(nodes))
	}
	seen := map[string]Node{}
	for _, n := range nodes {
		seen[n.Name] = n
	}
	la, ok := seen["LA-01"]
	if !ok {
		t.Fatal("缺少 LA-01")
	}
	if la.Type != "ss" || la.Port != 8443 || la.Endpoint() != "1.2.3.4:8443" {
		t.Fatalf("LA-01 解析错误: %+v", la)
	}
	if la.Raw["password"] != "secret" {
		t.Fatalf("原始字段应保留，便于交给 mihomo: %+v", la.Raw)
	}
	jp, ok := seen["JP-02"]
	if !ok {
		t.Fatal("缺少 JP-02")
	}
	if jp.Type != "vmess" || jp.Raw["uuid"] == "" {
		t.Fatalf("JP-02 解析错误: %+v", jp)
	}
	if len(la.ID) != 12 {
		t.Fatalf("节点 ID 长度应为 12，实际 %q", la.ID)
	}
}

func TestParseBase64Subscription(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte(clashYAML))
	nodes, err := Parse([]byte(encoded))
	if err != nil {
		t.Fatalf("Parse(base64): %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("base64 订阅应解出 2 个节点，实际 %d", len(nodes))
	}
}

func TestParseURIList(t *testing.T) {
	body := strings.Join([]string{
		"ss://YWVzLTI1Ni1nY206c2VjcmV0@1.2.3.4:8443#LA-SS",
		"trojan://pass123@5.6.7.8:443?sni=example.com#JP-Trojan",
		"socks5://127.0.0.1:7892#Local",
		"vmess://" + base64.StdEncoding.EncodeToString([]byte(
			`{"ps":"HK","add":"9.9.9.9","port":"443","id":"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee","aid":"0","net":"ws","host":"h.example.com","path":"/x","tls":"tls"}`)),
	}, "\n")

	nodes, err := Parse([]byte(body))
	if err != nil {
		t.Fatalf("Parse(uri): %v", err)
	}
	if len(nodes) != 4 {
		t.Fatalf("期望 4 个节点，实际 %d: %+v", len(nodes), nodes)
	}
	byName := map[string]Node{}
	for _, n := range nodes {
		byName[n.Name] = n
	}
	if n := byName["LA-SS"]; n.Type != "ss" || n.Server != "1.2.3.4" || n.Raw["cipher"] != "aes-256-gcm" {
		t.Fatalf("ss URI 解析错误: %+v", n)
	}
	if n := byName["JP-Trojan"]; n.Type != "trojan" || n.Raw["password"] != "pass123" {
		t.Fatalf("trojan URI 解析错误: %+v", n)
	}
	if n := byName["Local"]; n.Type != "socks5" || n.Port != 7892 {
		t.Fatalf("socks5 URI 解析错误: %+v", n)
	}
	if n := byName["HK"]; n.Type != "vmess" || n.Raw["servername"] != "h.example.com" {
		t.Fatalf("vmess URI 解析错误: %+v", n)
	}
}

func TestParseDedupeRename(t *testing.T) {
	body := `
proxies:
  - {name: DUP, type: ss, server: 1.1.1.1, port: 1001, cipher: aes-128-gcm, password: a}
  - {name: DUP, type: ss, server: 1.1.1.1, port: 1001, cipher: aes-128-gcm, password: a}
  - {name: DUP, type: ss, server: 2.2.2.2, port: 2002, cipher: aes-128-gcm, password: b}
`
	nodes, err := Parse([]byte(body))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("完全重复的节点应合并，期望 2 个，实际 %d", len(nodes))
	}
	names := map[string]bool{}
	for _, n := range nodes {
		if names[n.Name] {
			t.Fatalf("节点名重复: %q", n.Name)
		}
		names[n.Name] = true
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	if _, err := Parse([]byte("这不是订阅 <html>404</html>")); err == nil {
		t.Fatal("垃圾内容应报错")
	}
}

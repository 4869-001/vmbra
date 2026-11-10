package clash

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// ctrlEndpoint 返回控制接口地址与鉴权密钥。
func (c *Core) ctrlEndpoint() (string, string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.running || c.ctrlPort == 0 {
		return "", "", fmt.Errorf("内核未运行")
	}
	return "127.0.0.1:" + strconv.Itoa(c.ctrlPort), c.secret, nil
}

func (c *Core) ctrlDo(method, path string, body any, out any) error {
	addr, secret, err := c.ctrlEndpoint()
	if err != nil {
		return err
	}
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, "http://"+addr+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+secret)

	client := &http.Client{Timeout: 12 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("内核返回 %d：%s", resp.StatusCode, string(bytes.TrimSpace(raw)))
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return err
		}
	}
	return nil
}

// Select 通过控制接口切换 EGRESS 组当前节点（热切换，不重启内核）。
func (c *Core) Select(name string) error {
	if name == "" {
		return fmt.Errorf("节点名为空")
	}
	if err := c.ctrlDo(http.MethodPut, "/proxies/EGRESS", map[string]string{"name": name}, nil); err != nil {
		return err
	}
	c.mu.Lock()
	c.node = Node{Name: name}
	c.mu.Unlock()
	return nil
}

// SelectNode 热切换到指定节点，并保留节点完整信息。
func (c *Core) SelectNode(n Node) error {
	if err := c.Select(n.Name); err != nil {
		return err
	}
	c.mu.Lock()
	c.node = n
	c.mu.Unlock()
	return nil
}

// Current 返回当前选中的节点名。
func (c *Core) Current() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.node.Name
}

// Names 返回本次内核配置里包含的全部节点名。
func (c *Core) Names() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.names))
	copy(out, c.names)
	return out
}

// DelayResult 是单个节点的测速结果。
type DelayResult struct {
	Name  string `json:"name"`
	Delay int    `json:"delayMs"`
	Error string `json:"error,omitempty"`
}

// delayTestURL 是通用 204 探测地址，与 Clash 默认一致。
const delayTestURL = "http://www.gstatic.com/generate_204"

// Delay 通过控制接口测量单个节点延迟（毫秒）。失败返回 error。
func (c *Core) Delay(name string, timeout time.Duration) (int, error) {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	q := url.Values{}
	q.Set("url", delayTestURL)
	q.Set("timeout", strconv.Itoa(int(timeout/time.Millisecond)))
	var out struct {
		Delay int `json:"delay"`
	}
	if err := c.ctrlDo(http.MethodGet, "/proxies/"+url.PathEscape(name)+"/delay?"+q.Encode(), nil, &out); err != nil {
		return 0, err
	}
	if out.Delay <= 0 {
		return 0, fmt.Errorf("延迟无效")
	}
	return out.Delay, nil
}

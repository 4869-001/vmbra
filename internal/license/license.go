// Package license 实现客户端的软件授权：设备指纹、激活、心跳与离线宽限。
//
// 服务端是独立部署的授权中心（IP 白名单 + 授权码管理），客户端只持有令牌。
package license

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Endpoint 是授权中心下发的出口节点。
type Endpoint struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Host   string `json:"host"`
	Port   int    `json:"port"`
	Region string `json:"region"`
}

// ClientInfo 是授权中心给出的版本信息，用于提示升级。
type ClientInfo struct {
	MinVersion    string `json:"minVersion"`
	LatestVersion string `json:"latestVersion"`
	DownloadURL   string `json:"downloadUrl"`
}

// Result 是一次激活/心跳的成功结果。
type Result struct {
	Token          string
	TokenExpiresAt time.Time
	OfflineGrace   time.Duration
	Plan           string
	Endpoints      []Endpoint
	Client         ClientInfo
}

// APIError 是服务端明确拒绝（4xx）的响应，与「网络不通」区分开。
type APIError struct {
	Code    string
	Message string
	Status  int
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("授权中心返回 %d", e.Status)
}

// ErrUnreachable 表示授权中心不可达（可走离线宽限）。
var ErrUnreachable = errors.New("无法连接授权中心")

// Client 是授权中心客户端。
type Client struct {
	Server     string
	AppVersion string
	HTTP       *http.Client
}

// NewClient 创建客户端；server 形如 https://license.example.com。
func NewClient(server, appVersion string) *Client {
	return &Client{
		Server:     strings.TrimRight(server, "/"),
		AppVersion: appVersion,
		HTTP:       &http.Client{Timeout: 15 * time.Second},
	}
}

type activateRequest struct {
	LicenseKey  string `json:"licenseKey"`
	Fingerprint string `json:"fingerprint"`
	Hostname    string `json:"hostname"`
	OS          string `json:"os"`
	AppVersion  string `json:"appVersion"`
}

type apiResponse struct {
	OK             bool   `json:"ok"`
	Token          string `json:"token"`
	TokenExpiresAt string `json:"tokenExpiresAt"`
	OfflineGraceMs int64  `json:"offlineGraceMs"`
	License        struct {
		ID        string `json:"id"`
		Plan      string `json:"plan"`
		ExpiresAt string `json:"expiresAt"`
	} `json:"license"`
	VPSEndpoints []Endpoint `json:"vpsEndpoints"`
	Client       ClientInfo `json:"client"`
	Error        struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// Activate 用授权码换取令牌。
func (c *Client) Activate(ctx context.Context, key, fingerprint string) (*Result, error) {
	host, _ := os.Hostname()
	body, err := json.Marshal(activateRequest{
		LicenseKey:  strings.ToUpper(strings.TrimSpace(key)),
		Fingerprint: fingerprint,
		Hostname:    host,
		OS:          fmt.Sprintf("%s %s %s", runtime.GOOS, runtime.GOARCH, runtime.Version()),
		AppVersion:  c.AppVersion,
	})
	if err != nil {
		return nil, err
	}
	return c.do(ctx, "/api/v1/activate", body, "")
}

// Heartbeat 用已有令牌续期，并重新拉取出口节点。
func (c *Client) Heartbeat(ctx context.Context, token string) (*Result, error) {
	if token == "" {
		return nil, &APIError{Code: "NO_TOKEN", Message: "本地没有授权令牌", Status: 401}
	}
	return c.do(ctx, "/api/v1/heartbeat", []byte("{}"), token)
}

func (c *Client) do(ctx context.Context, path string, body []byte, token string) (*Result, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Server+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	defer resp.Body.Close()

	var out apiResponse
	_ = json.NewDecoder(resp.Body).Decode(&out)

	if resp.StatusCode != http.StatusOK || !out.OK {
		code := out.Error.Code
		if code == "" {
			code = http.StatusText(resp.StatusCode)
		}
		return nil, &APIError{
			Code:    code,
			Message: out.Error.Message,
			Status:  resp.StatusCode,
		}
	}

	res := &Result{
		Token:     out.Token,
		Plan:      out.License.Plan,
		Client:    out.Client,
		Endpoints: out.VPSEndpoints,
	}
	if t, err := time.Parse(time.RFC3339, out.TokenExpiresAt); err == nil {
		res.TokenExpiresAt = t
	} else {
		res.TokenExpiresAt = time.Now().Add(7 * 24 * time.Hour)
	}
	if out.OfflineGraceMs > 0 {
		res.OfflineGrace = time.Duration(out.OfflineGraceMs) * time.Millisecond
	} else {
		res.OfflineGrace = 72 * time.Hour
	}
	return res, nil
}

// MachineFingerprint 返回本机设备指纹（sha256 十六进制）。
// macOS 用硬件 UUID；Linux 用 machine-id；都取不到时退化为主机名。
func MachineFingerprint() string {
	raw := ""

	switch runtime.GOOS {
	case "darwin":
		raw = darwinPlatformUUID()
	case "linux":
		for _, p := range []string{"/etc/machine-id", "/var/lib/dbus/machine-id"} {
			if b, err := os.ReadFile(p); err == nil {
				if v := strings.TrimSpace(string(b)); v != "" {
					raw = v
					break
				}
			}
		}
	}

	if raw == "" {
		if h, err := os.Hostname(); err == nil {
			raw = h
		}
	}

	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func darwinPlatformUUID() string {
	out, err := exec.Command("ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
	if err != nil {
		return ""
	}
	return extractQuotedValue(string(out), "IOPlatformUUID")
}

// extractQuotedValue 从 ioreg 输出里取 `"KEY" = "VALUE"` 的 VALUE。
func extractQuotedValue(text, key string) string {
	i := strings.Index(text, `"`+key+`"`)
	if i < 0 {
		return ""
	}
	rest := text[i+len(key)+2:]
	eq := strings.Index(rest, "=")
	if eq < 0 {
		return ""
	}
	rest = rest[eq+1:]
	start := strings.Index(rest, `"`)
	if start < 0 {
		return ""
	}
	rest = rest[start+1:]
	end := strings.Index(rest, `"`)
	if end < 0 {
		return ""
	}
	return rest[:end]
}

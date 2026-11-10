// Package config 负责本地管理服务的运行时配置与访问令牌管理。
package config

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Version 为当前管理服务版本号。
const Version = "0.9.0"

// Config 是进程级配置，来源于环境变量与默认值。
type Config struct {
	DataDir string // 数据目录，默认 ~/.vmbra
	Listen  string // 监听地址，默认 127.0.0.1:7788
	Token   string // 访问令牌，首次启动自动生成
}

// Load 读取配置。仅允许绑定回环地址，避免把管理面暴露到局域网。
func Load() (*Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	c := &Config{
		DataDir: filepath.Join(home, ".vmbra"),
		Listen:  "127.0.0.1:7788",
	}
	// 一次性的历史数据迁移：老版本数据目录 ~/.anywhere，改名后搬到 ~/.vmbra。
	// 只在用户没有显式指定数据目录、且新目录尚未创建时执行，避免覆盖已有数据。
	if v := firstEnv("VMBRA_DATA_DIR", "ANYWHERE_DATA_DIR"); v != "" {
		c.DataDir = expandHome(v, home)
	} else {
		migrateLegacyDataDir(filepath.Join(home, ".anywhere"), c.DataDir)
	}
	if v := firstEnv("VMBRA_LISTEN", "ANYWHERE_LISTEN"); v != "" {
		c.Listen = v
	}
	if v := firstEnv("VMBRA_PORT", "ANYWHERE_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 && p < 65536 {
			c.Listen = "127.0.0.1:" + strconv.Itoa(p)
		}
	}
	if err := c.prepare(); err != nil {
		return nil, err
	}
	return c, nil
}

// firstEnv 返回第一个非空的环境变量值，用于兼容旧前缀（ANYWHERE_*）。
func firstEnv(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}

// migrateLegacyDataDir 把旧数据目录整体搬到新目录（同卷 rename，原子且瞬时）。
// 失败时保持旧目录不动，调用方会继续在旧路径下工作，不会丢状态。
func migrateLegacyDataDir(legacy, target string) {
	if legacy == target {
		return
	}
	if _, err := os.Stat(legacy); err != nil {
		return
	}
	if _, err := os.Stat(target); err == nil {
		return
	}
	_ = os.Rename(legacy, target)
}

func expandHome(p, home string) string {
	if strings.HasPrefix(p, "~/") || p == "~" {
		if p == "~" {
			return home
		}
		return filepath.Join(home, p[2:])
	}
	return p
}

func (c *Config) prepare() error {
	for _, dir := range []string{c.DataDir, c.WorkstationDir(), c.SnapshotDir(), c.LogDir()} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	return c.loadOrCreateToken()
}

func (c *Config) loadOrCreateToken() error {
	path := filepath.Join(c.DataDir, "token")
	if b, err := os.ReadFile(path); err == nil {
		if t := strings.TrimSpace(string(b)); t != "" {
			c.Token = t
			return nil
		}
	}
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return err
	}
	token := hex.EncodeToString(buf)
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return err
	}
	c.Token = token
	return nil
}

// RotateToken 重新生成访问令牌并落盘。
func (c *Config) RotateToken() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := hex.EncodeToString(buf)
	if err := os.WriteFile(filepath.Join(c.DataDir, "token"), []byte(token+"\n"), 0o600); err != nil {
		return "", err
	}
	c.Token = token
	return token, nil
}

// WorkstationDir 返回默认工作站的数据根目录（兼容旧布局）。
func (c *Config) WorkstationDir() string { return filepath.Join(c.DataDir, "workstation") }

// WorkstationRootDir 返回指定工作站的数据根目录。
// root 为空或 "workstation" 时沿用旧路径，其余落在 workstations/<root>。
func (c *Config) WorkstationRootDir(root string) string {
	root = strings.TrimSpace(root)
	if root == "" || root == "workstation" {
		return c.WorkstationDir()
	}
	return filepath.Join(c.DataDir, "workstations", root)
}

// SnapshotDir 返回快照目录。
func (c *Config) SnapshotDir() string { return filepath.Join(c.DataDir, "snapshots") }

// LogDir 返回日志目录。
func (c *Config) LogDir() string { return filepath.Join(c.DataDir, "logs") }

// StatePath 返回状态文件路径。
func (c *Config) StatePath() string { return filepath.Join(c.DataDir, "state.json") }

// TokenPath 返回令牌文件路径。
func (c *Config) TokenPath() string { return filepath.Join(c.DataDir, "token") }

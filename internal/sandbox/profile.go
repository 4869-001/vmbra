package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Policy 描述一次隔离执行的沙箱策略，渲染为 macOS sandbox 的 SBPL 描述文件。
//
// 设计原则：
//   - 默认拒绝一切，只白名单放行；
//   - 文件写入仅限工作站根目录（工作区/家目录/临时目录）与系统临时区；
//   - 读取上屏蔽个人隐私目录（文稿/桌面/下载/密钥/配置），但放行系统运行时；
//   - 网络默认关闭，仅在显式开启时放行。
type Policy struct {
	RootDir      string   // 工作站隔离根目录（可读写）
	HomeDir      string   // 真实用户 home，用于生成隐私目录拒绝规则
	AllowNetwork bool     // 是否允许出网
	DenyRead     []string // 额外禁止读取的路径
}

func esc(p string) string {
	p = strings.ReplaceAll(p, `\`, `\\`)
	return strings.ReplaceAll(p, `"`, `\"`)
}

func sbplLiteral(p string) string { return fmt.Sprintf(`(literal "%s")`, esc(p)) }
func sbplSubpath(p string) string { return fmt.Sprintf(`(subpath "%s")`, esc(p)) }

// Render 生成 SBPL 策略文本。
func (p Policy) Render() string {
	var b strings.Builder
	b.WriteString("(version 1)\n")
	b.WriteString("(deny default)\n")
	b.WriteString("(allow sysctl-read)\n")
	b.WriteString("(allow mach-lookup)\n")
	b.WriteString("(allow ipc-posix-shm)\n")
	b.WriteString("(allow process-fork)\n")
	b.WriteString("(allow process-exec)\n")
	b.WriteString("(allow signal (target self))\n")
	b.WriteString("(allow pseudo-tty)\n")
	b.WriteString("(allow file-ioctl)\n")
	// 基础读取：系统运行时、工具链与共享库
	b.WriteString("(allow file-read*)\n")

	// 隐私目录：在基础放行之后显式拒绝（SBPL 最后匹配生效）。
	deny := []string{
		filepath.Join(p.HomeDir, "Documents"),
		filepath.Join(p.HomeDir, "Desktop"),
		filepath.Join(p.HomeDir, "Downloads"),
		filepath.Join(p.HomeDir, "Pictures"),
		filepath.Join(p.HomeDir, "Movies"),
		filepath.Join(p.HomeDir, "Music"),
		filepath.Join(p.HomeDir, ".ssh"),
		filepath.Join(p.HomeDir, ".aws"),
		filepath.Join(p.HomeDir, ".gnupg"),
		filepath.Join(p.HomeDir, ".kube"),
		filepath.Join(p.HomeDir, "Library", "Keychains"),
		filepath.Join(p.HomeDir, "Library", "Messages"),
		filepath.Join(p.HomeDir, "Library", "Mail"),
		filepath.Join(p.HomeDir, "Library", "Cookies"),
		filepath.Join(p.HomeDir, "Library", "Application Support", "Google"),
	}
	deny = append(deny, p.DenyRead...)
	for _, d := range deny {
		b.WriteString("(deny file-read* " + sbplSubpath(d) + ")\n")
	}

	// 工作站根目录：读写放行
	b.WriteString("(allow file-read* file-write* " + sbplSubpath(p.RootDir) + ")\n")
	b.WriteString(`(allow file-read* file-write* (regex #"^/dev/(tty|pty|fd|stdin|stdout|stderr|null|zero|random|urandom|dtracehelper)"))` + "\n")
	b.WriteString(`(allow file-read* file-write* (subpath "/private/tmp") (subpath "/private/var/folders"))` + "\n")
	b.WriteString(`(allow file-write* (literal "/dev/null") (literal "/dev/stdout") (literal "/dev/stderr"))` + "\n")

	if p.AllowNetwork {
		b.WriteString("(allow network*)\n")
	} else {
		b.WriteString("(deny network*)\n")
	}
	return b.String()
}

// WritePolicy 将策略渲染并写入文件。
func WritePolicy(path string, p Policy) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(p.Render()), 0o600)
}

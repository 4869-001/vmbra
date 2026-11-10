// Command vmbra-link 生成「开通链接」：把 VPS 信息与安装清单编码成
// 一条可以直接导入 Vmbra 的链接，同时可落一份 .vmbrapkg（JSON）做留档。
//
// 用法：
//
//	vmbra-link --host 203.0.113.10 --port 22 --user root --password '***' \
//	           --name "客户 A 工作站" --tools chrome,node,git,claude --vm false
//
// 链接包含口令时属敏感凭据，只发给使用者本人。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/4869-001/vmbra/internal/importlink"
)

func main() {
	var (
		host     = flag.String("host", "", "VPS 地址（必填）")
		port     = flag.Int("port", 22, "SSH 端口")
		user     = flag.String("user", "root", "SSH 用户")
		password = flag.String("password", "", "SSH 口令（可选，导入时可自动安装公钥）")
		name     = flag.String("name", "", "工作站名称")
		note     = flag.String("note", "", "备注，展示在导入确认页")
		region   = flag.String("region", "", "地区标签，如 US / JP")
		lang     = flag.String("lang", "", "语言，如 en_US.UTF-8")
		tz       = flag.String("tz", "", "时区，如 America/Los_Angeles")
		tools    = flag.String("tools", "", "工具清单，逗号分隔；留空用默认（"+strings.Join(importlink.DefaultTools(), ",")+"）")
		withVM   = flag.Bool("vm", false, "包含 Linux 虚拟机终端（会下载约 1GB 镜像）")
		out      = flag.String("out", "", "写出 .vmbrapkg 文件路径")
		asJSON   = flag.Bool("json", false, "以 JSON 输出，便于脚本消费")
		base     = flag.String("base", "http://127.0.0.1:7788", "控制台地址，用于生成本机导入 URL")
	)
	flag.Parse()

	if strings.TrimSpace(*host) == "" {
		fmt.Fprintln(os.Stderr, "缺少 --host")
		flag.Usage()
		os.Exit(2)
	}

	list := normalize(*tools)
	if len(list) == 0 {
		list = importlink.DefaultTools()
	}
	if *withVM && !contains(list, "vm") {
		list = append(list, "vm")
	}

	p := importlink.Payload{
		V:     importlink.Version,
		Name:  *name,
		Note:  *note,
		Tools: list,
		VPS: importlink.VPSSpec{
			Host:     *host,
			Port:     *port,
			User:     *user,
			Password: *password,
			Region:   *region,
			Lang:     *lang,
			TimeZone: *tz,
		},
	}
	p.Normalize()
	if err := p.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "参数不合法：%v\n", err)
		os.Exit(1)
	}

	link, err := importlink.Encode(p)
	if err != nil {
		fmt.Fprintf(os.Stderr, "生成链接失败：%v\n", err)
		os.Exit(1)
	}

	if *out != "" {
		raw, _ := json.MarshalIndent(p, "", "  ")
		if err := os.WriteFile(*out, append(raw, '\n'), 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "写出文件失败：%v\n", err)
			os.Exit(1)
		}
	}

	consoleURL := importlink.ConsoleURL(*base, link)

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(map[string]any{
			"link":       link,
			"consoleUrl": consoleURL,
			"payload":    p,
			"tools":      toolLines(list),
		})
		return
	}

	fmt.Println("开通链接（发给客户，导入 Vmbra 即可）：")
	fmt.Println()
	fmt.Println("  " + link)
	fmt.Println()
	fmt.Println("本机预览地址（控制台打开后自动弹窗）：")
	fmt.Println("  " + consoleURL)
	fmt.Println()
	fmt.Println("出口节点：" + fmt.Sprintf("%s@%s:%d", p.VPS.User, p.VPS.Host, p.VPS.Port))
	fmt.Println("安装清单（客户可取消勾选）：")
	for _, line := range toolLines(list) {
		fmt.Println("  - " + line)
	}
	if p.VPS.Password != "" {
		fmt.Println()
		fmt.Println("注意：链接内含 SSH 口令，属敏感凭据；导入成功后建议在面板更换口令。")
	}
}

func normalize(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	out := make([]string, 0, 4)
	for _, p := range strings.Split(raw, ",") {
		id := strings.ToLower(strings.TrimSpace(p))
		if _, ok := importlink.ToolByID(id); ok {
			out = append(out, id)
		} else if id != "" {
			fmt.Fprintf(os.Stderr, "忽略未知工具：%s\n", id)
		}
	}
	return out
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func toolLines(list []string) []string {
	out := make([]string, 0, len(list))
	for _, id := range list {
		if t, ok := importlink.ToolByID(id); ok {
			out = append(out, fmt.Sprintf("%s（%s）", t.Label, t.Size))
		}
	}
	return out
}

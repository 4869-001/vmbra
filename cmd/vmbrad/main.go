// Command vmbrad 是 Vmbra WorkStation 的本地管理服务。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/4869-001/vmbra/internal/api"
	"github.com/4869-001/vmbra/internal/config"
	"github.com/4869-001/vmbra/internal/sandbox"
	"github.com/4869-001/vmbra/internal/service"
	"github.com/4869-001/vmbra/internal/store"
	"github.com/4869-001/vmbra/internal/tasks"
	"github.com/4869-001/vmbra/internal/webui"
)

func main() {
	showVersion := flag.Bool("version", false, "打印版本号后退出")
	port := flag.Int("port", 0, "覆盖监听端口（默认 7788）")
	dataDir := flag.String("data-dir", "", "覆盖数据目录（默认 ~/.vmbra）")
	flag.Parse()

	if *showVersion {
		fmt.Println("vmbrad", config.Version)
		return
	}
	if *port > 0 {
		_ = os.Setenv("VMBRA_PORT", fmt.Sprintf("%d", *port))
	}
	if *dataDir != "" {
		_ = os.Setenv("VMBRA_DATA_DIR", *dataDir)
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("加载配置失败：%v", err)
	}
	st, err := store.Open(cfg.StatePath())
	if err != nil {
		log.Fatalf("加载状态失败：%v", err)
	}
	// 上次进程退出前若工作站仍标记为运行中，这里做一次状态回收，避免出现"假运行"。
	if cur := st.Copy().Current(); cur.State == "running" || cur.State == "starting" || cur.State == "stopping" {
		_ = st.Update(func(s *store.State) {
			for i := range s.Workstations {
				if s.Workstations[i].State != "running" {
					continue
				}
				s.Workstations[i].State = "stopped"
				s.Workstations[i].StartedAt = nil
				s.Workstations[i].Message = "管理服务重启，工作站会话已释放"
			}
		})
	}
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("读取用户目录失败：%v", err)
	}
	active := st.Copy().Current()
	drv, err := sandbox.New(cfg.WorkstationRootDir(active.Root), home)
	if err != nil {
		log.Fatalf("初始化隔离内核失败：%v", err)
	}
	tm := tasks.New(st, cfg.LogDir())
	svc := service.New(cfg, st, drv, tm)

	if !drv.SandboxAvailable() {
		log.Printf("警告：策略隔离未生效（%s），已回退为受限进程模式", drv.SandboxError())
	}

	// 系统层：落地统一环境文件，并按配置启动本地 DNS 转发器。
	if err := svc.WriteShellEnv(); err != nil {
		log.Printf("写入终端环境失败：%v", err)
	}
	svc.StartDNSProxy()
	if stats := svc.DNS.Stats(); stats.Running {
		log.Printf("DNS 转发器：%s → %s", stats.Addr, strings.Join(st.Copy().DNS.Servers, ", "))
	} else if st.Copy().DNS.Enabled {
		log.Printf("DNS 转发器未启动：%s", st.Copy().DNS.Mode)
	}

	_ = svc.WriteBrowserLauncher()

	// 出口闸门：启动即检一次，之后每 30 秒复核；不通过会自动切断工作站网络。
	gateCtx, gateCancel := context.WithCancel(context.Background())
	defer gateCancel()
	svc.StartEgressLoop(gateCtx)
	svc.StartProxyRefreshLoop(gateCtx)

	// 上次退出前若在用某个 Clash 节点，这里恢复内核并把环境重新对齐。
	if core := st.Copy().ProxyCore; core.NodeID != "" {
		go func() {
			if _, err := svc.ApplyClashNode(core.NodeID); err != nil {
				log.Printf("恢复 Clash 出口失败：%v", err)
				return
			}
			log.Printf("Clash 出口已恢复：%s", core.NodeName)
		}()
	}

	// VPS 出入口：按上次的启用状态恢复 SSH 隧道。
	if st.Copy().VPS.Enabled {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if _, err := svc.StartVPS(ctx); err != nil {
				log.Printf("VPS 出入口启动失败：%v", err)
				return
			}
			log.Printf("VPS 出入口已连接：%s", st.Copy().VPS.Host)
		}()
	}

	// 授权：启动后台心跳（首轮延迟 30 秒，之后每 6 小时续期一次）。
	hbCtx, hbCancel := context.WithCancel(context.Background())
	defer hbCancel()
	svc.StartLicenseHeartbeat(hbCtx)
	if st.Copy().License.Token == "" {
		log.Printf("软件尚未授权：打开控制台输入授权码后即可使用")
	}

	handler := api.New(svc, cfg, webui.Dir()).Handler()
	httpSrv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		log.Fatalf("监听 %s 失败：%v", cfg.Listen, err)
	}

	log.Printf("Vmbra WorkStation 管理服务 v%s 已启动", config.Version)
	log.Printf("数据目录：%s", cfg.DataDir)
	log.Printf("控制台：http://%s/?token=%s", cfg.Listen, cfg.Token)

	// 自动启动工作站
	if st.Copy().Settings.AutoStart {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if _, err := svc.StartWorkstation(ctx); err != nil {
				log.Printf("自动启动工作站失败：%v", err)
			} else {
				log.Printf("工作站已自动启动")
			}
		}()
	}

	errCh := make(chan error, 1)
	go func() {
		if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		log.Fatalf("服务异常退出：%v", err)
	case sig := <-sigCh:
		log.Printf("收到 %s，正在关闭", sig)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(ctx)
	svc.Clash.Stop()
	svc.VPSTunnel.Stop()
	drv.CloseAll()
	log.Printf("已关闭")
}

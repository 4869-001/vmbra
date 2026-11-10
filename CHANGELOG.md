# 更新日志

本文件记录 Vmbra 自初始提交以来的全部功能演进。

- 版本号与 `internal/config.Version` 对齐；仓库目前未打 tag 的历史条目按提交时间倒序排列，并在标题后标注提交号。
- 条目类型固定为：**新增 / 变更 / 修复 / 安全 / 移除 / 验证**。
- 新增功能时请同步更新：本文件 → `docs/app-manual.md`（功能手册）→ `docs/ARCHITECTURE.md`（架构与状态机）。

---

## [0.9.0] - 2026-11-10 · `cf8d706`

主题：**出口（Exit）体系**——把「代理能用」升级为「出口可信、环境与出口一致」。

### 新增

- **Clash / Mihomo 订阅导入**（`internal/clash`）
  - 解析 Clash YAML（`proxies` / `payload` / 纯节点列表）、base64 包裹的订阅，以及 URI 列表（`ss://` `trojan://` `vmess://` `socks5://` `http://`）。
  - 过滤 `DIRECT` / `REJECT` / 策略组等非节点条目；重名节点自动加序号（mihomo 要求 `name` 全局唯一）。
  - 订阅库落盘 `~/.vmbra/proxy/sources.json`，支持多来源、原子写入。
- **本机 mihomo 内核托管**（`internal/clash/core.go`）
  - 按优先级探测内核：`VMBRA_CLASH_CORE` → `~/.vmbra/bin/mihomo` → Homebrew → Clash Verge → Cage 内置。
  - 生成 `runtime.yaml`：仅监听回环、`allow-lan: false`、DoH 解析、`EGRESS` 手动选择组、`MATCH,EGRESS`。
  - 全部订阅节点一次写入，开 `external-controller`（回环 + secret），节点切换**不重启内核**。
- **一键对齐**（`Service.ApplyClashNode` / `SelectClashNode`）
  - 起内核 → 经内核探测真实出口（ip-api → ipapi.co → ipinfo 依次回退）→ 按出口国家映射系统语言、编码、时区、浏览器语言与 UA 语言 → 回写 `.vmbra.env`。
  - 单出口原则：启用内核时自动停掉 VPS 隧道，避免双链路。
- **租约式出口闸门**（`internal/egress`）
  - 状态机 `closed → checking → open`；检查通过发 90 秒租约，每 30 秒复核续租。
  - 过期、检查失败、配置变更（换节点/关出网）即失效；失败时自动关闭出网开关、清空代理变量并写 `egress.close` 审计。
  - 9 项检查即证据：出网开关 / 出口链路 / 出口可达 / 出口地区 / 系统语言 / 系统时区 / 浏览器语言 / DNS 转发 / IPv6 泄漏。
- **订阅刷新与节点测速**
  - 订阅默认 6 小时自动刷新（每 10 分钟扫描到期项），刷新后原节点消失会自动落到第一个并热切换。
  - 并发（6 路）调用 mihomo `/proxies/{name}/delay` 测速，按延迟升序返回；支持按测速结果一键切换。
- **一键修复环境**（`POST /api/egress/repair`）：按当前节点重新探测并回写语言、时区、代理与环境文件。
- **控制台新增「出口」页**
  - 一页给全：内核状态、出口 IP/地区、系统语言/时区、闸门剩余时间、检查证据、订阅来源、节点选择、测速、立即检查/一键修复/停止出口。

### 变更

- **浏览器出口统一走内核**：`browserProxyURL` 固化为「内核 → VPS → 宿主」的降级顺序，并补齐防泄漏参数
  `--host-resolver-rules=MAP * ~NOTFOUND`、`--disable-quic`、`--force-webrtc-ip-handling-policy=disable_non_proxied_udp`、`--dns-prefetch-disable`。
- **内核入站取消口令认证**：mihomo 的 `authentication` 是全局配置，会给 Chrome 返回 `407` 而 Chrome 无法在 `--proxy-server` 中携带凭据；因此入站改为「仅监听回环 + 无认证」，控制接口仍以 secret 保护。
- **导航分层**：「出口」成为一级入口，「网络」「环境」降级为高级视图（`网络 · 高级` / `环境 · 高级`）。
- **出网开关语义**：`ToggleNetwork(true)` 先过一次闸门，检查不过自动回滚为关闭，不再出现「看起来开了其实全断」。
- **模块路径统一**：`anywhere` → `github.com/4869-001/vmbra`，主进程 `anywhered` → `vmbrad`，数据目录 `~/.anywhere` → `~/.vmbra`（首次启动自动迁移）。
- 依赖新增 `gopkg.in/yaml.v3`。

### 安全

- 出口检查包含 IPv6 旁路检测；浏览器侧封锁 QUIC 与 WebRTC 非代理 UDP。
- 闸门失败即断网的策略使「管理进程被 kill」的最坏影响限定在一个租约周期（90 秒）内。

### 验证

```
gofmt -l internal cmd     # 无输出
go build ./...            # 通过
go test ./...             # 通过（新增 egress 5 例、clash 4 例、service 3 例）
go vet ./...              # 通过
```

端到端（临时数据目录 + 独立端口）：

```
导入 socks5 节点 → 一键对齐     → exit 117.32.166.2 / CN / Asia/Shanghai / zh_CN.UTF-8
GET  /api/egress                → open，9 项全通过，remaining ≈ 90s
POST /api/proxy/delay           → [{local-direct, 74ms}]
故意断开上游 → POST /api/egress/check
                                → closed / allowed=false / networkEnabled=false
重启管理服务                    → "Clash 出口已恢复：local-direct"，闸门自动重开
curl -x 127.0.0.1:7893          → 200（浏览器通道可用，无需认证）
```

---

## [0.8.0] - 2026-11-09 · `a358a1d` `1bab004`

主题：**客户交付与导入链路**。

### 新增

- **开通链接（交付导入）**
  - `cmd/vmbra-link` 把 VPS 信息（主机、端口、登录用户、私钥、出口语言/时区）编码为 `vmbra://import/<base64url>` 链接或二维码，客户点开即可导入，无需手填配置。
  - 服务端 `internal/importlink` 负责编解码与校验；导入后落盘为本地工作站的 VPS 配置。
- 控制台新增导入页与深链处理逻辑，支持运行中接收 `vmbra://` 链接。

### 修复

- 控制台已在运行时收到 `vmbra://` 深链，也能正常弹出导入对话框（此前只有冷启动阶段生效）。

---

## [0.1.0] - 2026-11-09 · `06d156b`

主题：**初始基线**——隔离工作站 + 本地管理服务 + 商业授权中心三件套。

### 新增

- **隔离执行面**
  - 基于 macOS `sandbox-exec`（SBPL）的进程级隔离，默认 `(deny network*)`，按需放行网络；工作区固定在 `~/.vmbra/workstation`。
  - PTY 会话、工作区文件系统（读/写/上传/下载/快照）、运行脚本与后台任务队列。
- **本地管理服务 `vmbrad`**
  - 仅监听 `127.0.0.1:7788`，Host / Origin / 令牌三重校验；内嵌 Web 控制台（`embed.FS`，无构建步骤）。
  - 九个功能板块：工作站、文件、应用、网络、环境、维护与恢复、任务、诊断、设置。
- **网络能力**
  - VPS 出入口：SSH 动态转发（SOCKS5）+ 远程端口转发，获得独立公网 IP 与入站端口。
  - 本地 DNS 转发器（UDP 中继、上游可切换）、入站端口转发（TCP 中继）。
  - 受控 Chrome：独立 profile、固定界面语言/Accept-Language/时区、可指定出口。
  - `.deb` 应用包安装（纯 Go 解析 ar + tar.{gz,xz,zst,bz2}）。
- **授权体系**
  - 独立部署的授权中心（Node ≥18，零第三方依赖）：签发、激活、心跳、套餐与机器指纹绑定、离线宽限。
  - 客户端 `internal/license`：设备指纹、激活/续期、离线降级。
- **交付与分发**
  - 桌面外壳 `Vmbra.app`（Swift / WKWebView），`bin/` 双击入口（激活 / 一键配置）。
  - `scripts/vmbra-setup`：幂等的一键环境配置（NDJSON 进度）。
  - `deploy/`：docker compose + Caddy 自动 HTTPS 的云服务器部署方案。
- **文档**
  - `docs/app-manual.md`（功能手册）、`docs/ARCHITECTURE.md`（架构详解）、`license-service/README.md`、`deploy/README.md`。

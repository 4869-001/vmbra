# Vmbra

macOS 上的**隔离工作站**：在本机提供一个"可运行不可信内容"的隔离执行环境，配一个只监听回环地址的本地管理服务、一个内嵌 Web 控制台

> 全链路命名统一为 **Vmbra**：Go 模块 `github.com/4869-001/vmbra`、主进程 `vmbrad`、数据目录 `~/.vmbra`、桌面壳 `Vmbra.app`。
> 从旧版（`anywhere` / `anywhered` / `~/.anywhere`）升级时，首次启动会自动把数据目录整体迁移到 `~/.vmbra`，授权与工作站数据不丢。

---

## 仓库结构

```
vmbra/
├── cmd/vmbrad/          管理服务入口（Go 单二进制）
├── cmd/vmbra-link/         交付工具：把 VPS 信息生成客户可导入的开通链接
├── internal/               服务端全部实现（见下方模块表）
│   ├── api/                HTTP 路由、鉴权、WebSocket 通道
│   ├── service/            业务编排：工作站/文件/应用/网络/环境/维护/诊断/设置/授权/开通导入/一键配置
│   ├── importlink/         开通链接编解码（vmbra://import/<base64url>）
│   ├── sandbox/            sandbox-exec 策略、PTY 会话、工作区文件系统、快照
│   ├── tasks/              后台任务队列（实时日志、取消、持久化）
│   ├── store/              状态持久化（原子写 ~/.vmbra/state.json）
│   ├── license/            授权中心客户端（设备指纹、激活、心跳、离线宽限）
│   ├── clash/              Clash/Mihomo 订阅解析 + 本机内核托管（多节点、热切换、测速）
│   ├── egress/             出口闸门（90 秒租约、复核、失败即断网）
│   ├── vps/                SSH 隧道出口（SOCKS + 远程转发）
│   ├── browser/            受控 Chrome（独立 profile、语言/时区/出口对齐）
│   ├── dnsproxy/           本地 DNS 转发器（UDP 中继、上游切换）
│   ├── forward/            入站端口转发（TCP 中继）
│   ├── deb/                纯 Go 解析 .deb（ar + tar.{gz,xz,zst,bz2}）
│   ├── config/             环境变量与路径
│   ├── webui/              内嵌控制台（原生 JS + xterm.js + noVNC，无构建步骤）
│   └── wsx/                WebSocket 读写小工具
├── desktop/                桌面外壳（Swift / WKWebView，swiftc 直接编译）+ Vmbra.icns
├── assets/brand/           品牌母版：vmbra-1024.png（App 图标源）· vmbra-glyph.png（单色 V 形）
├── scripts/make-icon.sh    母版 PNG → .icns + 控制台 logo（sips / iconutil，无第三方依赖）
├── scripts/vmbra-setup/    客户端一键环境配置（幂等 bash，NDJSON 进度）
├── license-service/        授权中心（Node ≥18，零第三方依赖）
├── bin/                    双击入口：激活 / 一键配置
├── docs/                   README.md（索引） · app-manual.md（功能手册） · ARCHITECTURE.md（架构详解）
├── CHANGELOG.md            全量更新日志（按版本记录新增/变更/修复/安全/验证）
├── Makefile · go.mod
```

---

## 架构框架

Vmbra 是 **四层 + 两条旁路** 的结构：本地一侧是被隔离的执行面，服务端一侧是授权与出口的管控面。

```
┌──────────────────────────── 客户端（用户 Mac）─────────────────────────────┐
│  ① 接入层                                                                  │
│     桌面外壳 Vmbra.app (Swift/WKWebView)   ← 拉起/守护 →   浏览器控制台  │
│            └────────────── HTTP + WebSocket（127.0.0.1:7788）─────────────┐ │
│  ② 接口层  internal/api + internal/wsx                                     │ │
│     Host 校验 · 令牌鉴权 · Origin 校验 · 静态资源 · 终端/VNC/任务 WS       │ │
│  ③ 业务层  internal/service                                                │ │
│     工作站 · 文件 · 应用 · 网络 · 环境 · 维护 · 诊断 · 设置 · 授权 · 配置  │ │
│  ④ 能力层  sandbox(PTY/SBPL/快照) · tasks · vps · browser · dnsproxy ·     │ │
│           forward · deb                                                    │ │
│  ⑤ 基础层  store(原子写 JSON) · config · webui(embed.FS)                   │ │
└───────────────┬───────────────────────────────┬────────────────────────────┘
                │ HTTPS（授权/心跳）            │ SSH（出口隧道）
┌───────────────▼───────────────┐   ┌───────────▼────────────────────────────┐
│ 授权中心 license-service      │   │ 出口节点 VPS                            │
│ 授权码 · 设备指纹 · IP 策略   │   │ SOCKS5 + 远程端口转发（-R）             │
│ HMAC 令牌 · 心跳续期 · 管理台 │   │ 客户端网络面 / 浏览器 / 环境的统一出口  │
└───────────────────────────────┘   └────────────────────────────────────────┘
```

### 模块职责

| 层 | 模块 | 职责 |
| --- | --- | --- |
| 接入 | `desktop/main.swift` | WKWebView 外壳；探测 `127.0.0.1:7788`，未响应则用包内 `vmbrad` 拉起；状态灯轮询、菜单快捷键 |
| 接入 | `internal/webui` | 九个板块的控制台（工作站/文件/应用/网络/环境/维护/任务/诊断/设置），`embed.FS` 打进二进制 |
| 接口 | `internal/api` | 路由 + `guard` 中间件（Host 白名单、令牌、Origin）、JSON 响应、WS 升级 |
| 业务 | `internal/service` | 唯一装配业务状态的层：读写 `store`、调 `sandbox`/`vps`/`browser`、提交 `tasks` |
| 能力 | `internal/sandbox` | 生成 `.policy.nonet.sbpl` / `.policy.net.sbpl`，以 `sandbox-exec` + `ulimit` 起进程；PTY 会话；工作区快照 |
| 能力 | `internal/tasks` | 串行队列 + 单任务实时日志环形缓冲 + 取消 + 状态落库 |
| 能力 | `internal/clash` | 解析 Clash/V2Ray 订阅，托管本机 mihomo，全部节点入配置，控制接口热切换与测速 |
| 能力 | `internal/egress` | 出口闸门：检查通过发 90 秒租约，30 秒复核，失败或过期立即收回 |
| 能力 | `internal/vps` | 维护出口隧道进程（ssh `-D`/`-R`）、日志缓冲、连通与出口 IP 探测 |
| 能力 | `internal/license` | 设备指纹（sha256）、激活、心跳、离线宽限判定 |
| 基础 | `internal/store` | `state.json` 原子写 + 进程内快照，归一化旧结构 |
| 商业 | `license-service` | 独立的授权中心：管理台、授权码、IP 白名单、HMAC 会话、心跳续期 |
| 交付 | `scripts/vmbra-setup` | 客户端环境安装：CLT → Homebrew → Git → Node → VPS 校验 → lima → 虚拟机内工具链 → 自检 |

### 关键链路

**1. 控制台请求**
浏览器/桌面壳 → `api`(guard) → `service` → `sandbox` / `store` / `tasks` → JSON 或 WS 帧回传。
控制台无构建步骤：`boot.js` 拉状态，`views-*.js` 分板块渲染，终端走 xterm.js，桌面走 noVNC。

**2. 隔离执行**
`service` 组装环境（`.vmbra.env`：语言/时区/DNS/代理）→ `sandbox.Exec/StartShell` →
`sandbox-exec -f .policy.*.sbpl` 内起 `sh`/PTY/脚本 → 输出经 `tasks` 环形缓冲推给 WS 或落日志。
超时回收整个进程组，避免孙进程逃逸。

**3. 授权**
`bin/vmbra-activate.command` 或设置页 → `POST /api/license/activate` → `internal/license`
（指纹 = machine-id + 主机信息 sha256）→ HTTPS 到 `license-service` → 校验授权码/IP 策略/设备绑定 →
签发 HMAC 令牌 + 下发套餐与出口节点 → 客户端落库并启动后台心跳；令牌过期后进入离线宽限期。

**4. 出口**
两条链路二选一，均由 `internal/egress` 统一裁决：
① 订阅链路：控制台粘贴 Clash 订阅 → `internal/clash` 解析并托管 mihomo（回环 + 控制接口 secret）→
探测出口 IP/地区 → 按地区回写语言、时区、代理与 `.vmbra.env`；
② 隧道链路：授权中心下发 `host:port` → `internal/vps` 建立 SSH 隧道。
放行前必须过闸门 9 项检查（出口可达、地区、语言、时区、DNS、IPv6 泄漏等），通过后发放 90 秒租约；
租约到期、检查失败或换节点即切断，并写 `egress.close` 审计。浏览器、终端、任务、诊断共享同一出口。

### 隔离与安全边界

| 维度 | 策略 |
| --- | --- |
| 文件读 | 放行系统运行时，显式拒绝文稿/桌面/下载/图片/`.ssh`/`.aws`/`Keychains`/`Cookies` |
| 文件写 | 仅 `~/.vmbra/workstation` 与系统临时区 |
| 网络 | 默认 `deny network*`；放行由出口闸门控制（90 秒租约 + 30 秒复核），失败即自动断开 |
| 进程 | 允许 `process-exec`/`process-fork`，信号仅限自身，超时回收进程组 |
| 资源 | `ulimit -t/-f/-n`；内存上限需虚拟机后端提供 |
| 身份 | 以宿主用户运行，**不提供 root** |
| 管理面 | 只监听回环，校验 `Host`（防 DNS rebinding）+ 令牌（0600）+ WS `Origin` |
| 授权中心 | 密码 scrypt 存储、会话 HMAC 签名、授权码可绑定设备、IP 白名单默认拒绝 |

### 技术栈

| 部分 | 选型 |
| --- | --- |
| 管理服务 | Go 1.23，标准库 `net/http` + 少量第三方（`creack/pty`、`klauspost/compress`、`ulikunitz/xz`） |
| 控制台 | 原生 ES 模块 + `embed.FS`，xterm.js / noVNC 内置，无 node 构建 |
| 桌面壳 | Swift + AppKit + WebKit，`swiftc` 直接编，无 Xcode 工程 |
| 隔离 | macOS `sandbox-exec`（SBPL）+ `ulimit` + PTY |
| 授权中心 | Node ≥18 标准库（`http`/`crypto`），数据落 `data/db.json` |
| 安装脚本 | bash（幂等，NDJSON 事件），可选 lima 虚拟机后端 |
| 品牌 | `assets/brand/vmbra-1024.png` 为唯一母版，`make icon` 生成 `.icns` 与控制台各尺寸 |

---

## 快速开始

```bash
# 客户端（依赖 macOS + Go 1.23+，不需要 Docker / 虚拟机）
make build                 # → bin/vmbrad
make run                   # 前台启动，终端打印带令牌的控制台地址
make app                   # → dist/Vmbra.app（含图标，双击即用）

# 授权中心（本地调试；上线走 deploy/ 的 docker compose）
cd license-service
export ADMIN_PASSWORD='强密码'
export HMAC_SECRET="$(openssl rand -hex 32)"
node src/server.js         # 管理台 http://127.0.0.1:8787/admin

# 一键配置客户环境（图形）
open bin/vmbra-setup.command
# 命令行
./scripts/vmbra-setup/setup.sh --license VMB-XXXX-XXXX-XXXX-XXXX --server https://license.example.com
```

测试与冒烟：

```bash
make test    # go test ./...（含沙箱逃逸测试）
make smoke   # 起临时实例跑接口冒烟
```

## 客户交付：开通链接

服务商侧填好 VPS 信息，生成一条链接发给客户；客户在 Vmbra 里粘贴（或直接点开 `vmbra://` 深链）即完成导入，
再按自己的需要勾选要下载的工具。

```bash
make link HOST=203.0.113.10 PORT=1841 USER=root PASSWORD='***' NAME='客户 A'
# 等价于
./bin/vmbra-link --host 203.0.113.10 --port 1841 --user root --password '***' \
  --name '客户 A' --note 'Ubuntu 26.04 · LAX' --tools chrome,node,git,claude --out /tmp/a.vmbrapkg
```

```
服务商                                客户机
vmbra-link ── vmbra://import/<payload> ──▶ 桌面壳（注册 vmbra:// 协议）/ 控制台粘贴
                                             │ POST /api/import/preview
                                             │  出口节点 · 风险提示 · 本机已装探测
                                             │ 客户逐项勾选/取消
                                             ▼ POST /api/import/apply
                        写入 VPS 配置 → 生成本机密钥 → 用口令装公钥 → 可选建隧道 → 可选提交安装
```

| 工具 | 默认 | 说明 |
| --- | --- | --- |
| Google Chrome | 勾选 | 受控浏览器、独立 profile（时区/语言/出口对齐） |
| Node.js LTS | 勾选 | Claude Code 与 npm 工具链的运行时 |
| Git | 勾选 | 代码拉取与版本管理 |
| Claude Code CLI | 勾选 | 终端里的 AI 编程助手 |
| Linux 虚拟机终端 | 不勾选 | lima + Ubuntu 镜像，约 1GB，需要 root/内核级隔离时再装 |

取消勾选即不下载：`setup.sh --tools chrome,node --skip-vm` 语义相同，未勾选的阶段输出 `skip`，
进度分母固定 12，客户随时能看到哪些装了、哪些跳过。导入只写配置与密钥，口令不落盘。

## 文档

- [`docs/README.md`](docs/README.md) —— 文档索引与目录/提交规范（先读这个）
- [`CHANGELOG.md`](CHANGELOG.md) —— 全量更新日志（含每个版本的验证方式）
- [`docs/app-manual.md`](docs/app-manual.md) —— 客户端功能手册（隔离模型、九个板块、开通链接、接口清单、数据目录）
- [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) —— 架构详解（包依赖、状态机、协议、授权时序、构建分发）
- [`license-service/README.md`](license-service/README.md) —— 授权中心接口与运营
- [`deploy/README.md`](deploy/README.md) —— 云服务器部署（docker compose + Caddy 自动 HTTPS）

## 分发前必须做

- `.app` 用 Developer ID 签名 + `notarytool` 公证，否则客户双击报"无法验证开发者"（`CODESIGN_IDENTITY` 传给 `desktop/build.sh`）
- 授权中心按 [`deploy/README.md`](deploy/README.md) 部署在带 HTTPS 的机器上，`TRUST_PROXY=true`
- `HMAC_SECRET` / `ADMIN_PASSWORD` 必须替换，不能用默认值
- `~/.vmbra`（客户端）与授权中心的数据卷每日备份

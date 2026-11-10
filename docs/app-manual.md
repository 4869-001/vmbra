# Vmbra 客户端功能手册

> 本手册对应客户端主程序 `vmbrad`。架构说明见 [`ARCHITECTURE.md`](ARCHITECTURE.md)。

本地隔离工作站：在本机提供一个「可运行不可信内容」的隔离环境，配一个本地管理服务（`vmbrad`）和一个浏览器控制台。

- **隔离内核**：macOS `sandbox-exec` 策略 + `ulimit` 资源限制 + PTY 会话 + 工作区私有文件系统
- **管理面**：Go 单二进制守护进程，仅监听 `127.0.0.1`，接口用访问令牌鉴权
- **控制台**：内嵌静态页面（无构建步骤），覆盖工作站、文件、应用、网络、环境、维护、任务、诊断、设置九个板块

```
┌──────────────────────────── 浏览器控制台 ────────────────────────────┐
│ 工作站 · 文件 · 应用 · 网络 · 环境 · 维护与恢复 · 任务 · 诊断 · 设置 │
└───────────────┬──────────────────────────────────────────────────────┘
                │ HTTP + WebSocket（令牌鉴权 / 回环 + Origin 校验）
┌───────────────▼───────────────  vmbrad  ──────────────────────────┐
│ api       路由、鉴权、静态资源、终端/任务/VNC 的 WS 通道              │
│ service   工作站生命周期、文件、应用、网络、环境、维护、诊断、设置    │
│ tasks     后台任务队列（实时日志、取消、持久化元信息）                │
│ sandbox   策略沙箱执行 · PTY 会话 · 工作区文件系统 · 快照归档         │
│ store     状态持久化（原子写入 ~/.vmbra/state.json）               │
└──────────────────────────────────────────────────────────────────────┘
```

## 桌面程序

不想每次都敲 curl 或翻浏览器标签页时，用桌面外壳：

```bash
make app                 # 产出 dist/Vmbra.app（内置 vmbrad）
open dist/Vmbra.app
```

它是一个 WKWebView 窗口，直接内嵌控制台，并把常用动作放到顶部工具条与菜单栏：

- **自动拉起管理服务**：启动时探测 `127.0.0.1:7788`，没响应就用包内的 `vmbrad` 起一个；
- **自动启动工作站**：连上后若工作站处于 `stopped`，直接调 `/api/workstation/start`；
- **状态灯**：每 3 秒轮询 `/api/status`，显示运行中/已停止/异常与隔离引擎是否生效；
- **一套界面**：窗口里就是控制台本体，保留原有侧边栏、卡片与深色样式，不再叠加第二套导航；
  窗口标题跟随工作站状态（运行中 / 已停止 / 切换中）；
- **快捷访问**：侧边栏或菜单里的 终端 / 桌面 / 网络 / 文件（⌘1 / ⌘2 / ⌘3 / ⌘4）直接切到对应面板，
  终端会就地打开一个**沙箱内的** PTY 会话，文件落在**工作站工作区**，桌面接**虚拟机 VNC**，
  未启动时先自动启动工作站再进入；
- **一键动作**：启动、停止、打开受控浏览器、刷新控制台、在受控 Chrome 中打开控制台（⌘S / ⌘. / ⌘B / ⌘R / ⌘O）。

> 外部打开一律走 Chrome：桌面程序不带系统默认浏览器（Safari）分支，⌘O 把控制台地址交给
> `/api/browser/open`，由受控 Chrome（独立 profile、固定语言/时区、可选 VPS 出口）载入；
> 该 profile 未启动时先拉起，已启动时在同一个实例里新开窗口。

它只是管理服务的客户端，不自己实现隔离逻辑；换端口或指向别的实例用 `VMBRA_BASE`、换内置二进制用 `VMBRA_BIN`。
放到 `/Applications` 即可日常使用（`cp -R dist/Vmbra.app /Applications/`）。

## 快速开始

```bash
# 依赖：macOS + Go 1.23+（不需要 Docker / 虚拟机）
make build          # 产出 bin/vmbrad
make run            # 前台启动，日志会打印带令牌的控制台地址
# 或
go run ./cmd/vmbrad
```

启动后终端会输出：

```
控制台：http://127.0.0.1:7788/?token=<你的令牌>
```

直接打开该地址即可。令牌保存在 `~/.vmbra/token`（权限 0600），接口需要
`X-Vmbra-Token` 头、`?token=` 查询参数或同名 Cookie。

常用参数：

```bash
vmbrad --port 7999 --data-dir /tmp/aw      # 自定义端口与数据目录
vmbrad --version
```

## 隔离模型

| 维度 | 策略 | 说明 |
| --- | --- | --- |
| 文件读 | 默认放行系统运行时，显式拒绝隐私目录 | 文稿/桌面/下载/图片/`.ssh`/`.aws`/`Keychains`/`Cookies` 等 |
| 文件写 | 仅限 `~/.vmbra/workstation` 与系统临时区 | 其余路径写入会被内核直接拒绝 |
| 网络 | 默认 `deny network*` | 在「网络 → 工作站出网」显式开启后才放行；关闭时**回环也一并被拒**（工作站内无法监听/访问 `127.0.0.1`） |
| 进程 | `allow process-exec` / `process-fork`，信号仅限自身 | 超时会回收整个进程组，避免孙进程逃逸 |
| 资源 | `ulimit -t` CPU 秒、`-f` 单文件大小、`-n` 文件句柄 | macOS 不支持 `RLIMIT_AS`，内存上限需由虚拟机后端提供 |
| 身份 | 以宿主用户运行，**不提供 root** | 提权会破坏隔离边界；root 会话需虚拟机后端 |

策略文件在启动时生成到工作站目录：

- `.policy.nonet.sbpl`：默认（无网络）
- `.policy.net.sbpl`：开启出网后使用

自检可验证边界：「诊断」页的「隔离边界」一组会拿当前策略**真实跑一遍**六项能力探测——进程启动、
工作区内写入、工作区外写入、访问宿主回环、工作站内回环监听、非回环网络——逐项比对「期望 / 实测」，
而不是只看引擎能否起进程。`go test ./internal/sandbox/` 另有逃逸测试（工作区外写文件、默认网络拒绝、
路径穿越、快照投毒）。

## 系统层：与 VPS 一致的出入口

工作站启用后，终端、脚本、任务、诊断共用同一套系统环境，由管理服务统一装配并落到
`~/.vmbra/workstation/home/.vmbra.env`（`.zshrc` 会自动 source，改设置后即时重写）：

| 维度 | 默认值 | 说明 |
| --- | --- | --- |
| 语言 / 文字 | 跟随 VPS 地区 `en_US.UTF-8`（可关闭） | `LANG/LC_ALL/LC_CTYPE/LC_COLLATE/LC_MESSAGES/LANGUAGE` 全部注入，中文与多语言不乱码 |
| 编码 | `UTF-8`（强制，不接受其他值） | 终端、脚本、文件读写统一 UTF-8 |
| 时区 | 跟随 VPS 地区 `America/Los_Angeles`（可关闭） | 导出 `TZ`，`date` 直接显示该地区时间，与宿主 Mac 无关 |
| 终端 | `TERM=xterm-256color`、`COLORTERM=truecolor` | 与 VPS SSH 会话一致的色彩与宽度行为 |
| DNS | 宿主机解析器 + 本地转发器 `127.0.0.1:1053` | 转发器把查询送往配置的上游；`VMBRA_DNS_SERVER` 可用于 `dig @…` 等工具 |
| 代理 | 继承宿主机环境变量 / 手动 / 关闭 | 仅在「工作站出网」开启时注入，`HTTP(S)_PROXY/ALL_PROXY/NO_PROXY` 全量导出 |

「环境 → 系统语言文字」里的**跟随 VPS 地区**开关决定语言与时区来源：开启后取自 VPS 配置
（`VPS.Lang` / `VPS.TimeZone`，默认 `en-US` / `America/Los_Angeles`），与宿主 Mac 完全无关；
VPS 换区时环境会自动同步并重写 `.vmbra.env`，已打开的旧终端需要新开会话才会生效。

对应接口：

```bash
GET   /api/system                 # 语言、DNS、终端环境、宿主 DNS、出口摘要
PATCH /api/system/locale          # {"followVps":true} 或 {"lang":"en_US.UTF-8","timezone":"America/Los_Angeles"}
PATCH /api/system/dns             # {"mode":"custom","servers":["223.5.5.5"],"port":1053}
POST  /api/system/dns/test        # {"name":"example.com"} → 系统/上游/转发器三路对比
POST  /api/network/egress         # 在隔离环境内探测出口 IP、地区、耗时
POST  /api/apps/run               # {"path":"scripts/hello.sh","args":["--x"]} → taskId
```

本地 DNS 转发器实现位于 `internal/dnsproxy`：UDP 透明中继、上游失败自动切换、保留最近
30 条查询供「网络」页展示。它服务于显式指定解析器的工具（`dig @127.0.0.1 -p 1053`）、
出口探测与诊断；macOS 的 `getaddrinfo` 仍走系统解析器（修改需系统级权限）。

> 说明：本地进程隔离与宿主共享网络栈，因此「出口」就是宿主出口（含宿主 VPN/代理）。
> 若要像独立 VPS 一样拥有自己的公网 IP，需要接入虚拟机后端或在外层加隧道。

## 运行脚本（默认允许，无需确认）

「应用 → 运行脚本」把工作区脚本交给隔离内核执行：

- 按扩展名自动选择解释器（`.sh/.zsh/.bash/.py/.js/.rb/.pl`），无解释器的可执行文件直接运行；
- 参数、超时（默认 300s，上限 3600s）可调，输出实时推送到任务日志；
- 工作目录为脚本所在目录，路径必须落在工作区内（穿越/软链越界会被拒绝）；
- 出厂默认允许执行，不需要二次确认；「操控 Mac」的 `run_command` 同样默认允许，
  两道开关都可以在「应用」页随时关闭。

## 应用包（.deb）

「应用 → 安装应用」上传的 `.deb` 会被解析并解包到工作区 `packages/<包名>/root`：

- 纯 Go 解析 ar 归档，支持 `data.tar`、`.gz`、`.xz`、`.zst`、`.bz2`，不依赖系统 `ar` / `dpkg`；
- 只展开普通文件与目录：符号链接、硬链接、设备节点与越界路径（`/`、`..`）一律跳过；
- 条目数与解包体积有上限（20 万条 / 2 GiB），避免超大包撑满工作区；
- 不执行 `postinst` 等维护脚本，也不写系统目录；安装到系统需要在虚拟机后端内进行。

## 受控浏览器（语言/时区与出口一致）

系统 Chrome 由 LaunchServices 启动，拿不到调用方的 `TZ`；界面语言与 `Accept-Language` 又存在日常
profile 里。所以即使流量走了 VPS，浏览器时区与语言仍是本机设置，指纹和出口地区对不上。
「应用 → 受控浏览器」用独立 profile 解决这个问题：

- 独立配置目录（`~/.vmbra/browser/<语言>`），不影响日常 Chrome，两个实例可以并存；
- 直接执行 Chrome 二进制并注入 `TZ=<时区>`，JS 的 `Intl` 与页面时间同时生效；
- 启动前写入 `intl.accept_languages` / `intl.app_locale`，使 `navigator.language`
  与 `Accept-Language` 和配置一致（只加 `--lang` 不会改变这两个值）；
- 出口可强制 `--proxy-server=socks5://127.0.0.1:1080`，HTTP 与 DNS 都从 VPS 离开；
- 「验证语言/时区」用一次性 headless profile 读取实际值，确认真正生效而不是只写了配置；
- 启动逻辑会同时写入 `~/.vmbra/bin/chrome-us`，桌面图标（如 `Chrome US.app`）直接调用它，
  不会与控制台配置漂移。脚本在隧道端口可连通时自动加 `--proxy-server`，与页面行为一致。

默认 `en-US` + `America/Los_Angeles`，可在页面修改；要换区时同时调整 VPS 隧道所在地，
出口 IP、时区与语言三者保持一致，浏览器体检才不会报「时区与出口地区不一致」。

边界：只对齐**时区 / 语言 / Accept-Language / 出口**。字体、Canvas、WebGL、屏幕尺寸等属于
操作系统级指纹，本工具不做伪造——macOS 全球版本都内置 PingFang / Heiti / Songti，
命中简体中文字体并不等于中国环境；强行隐藏反而会变成「缺字体的异常 Mac」。

## 端口转发（入站）

「网络 → 端口转发」是真实生效的 TCP 中继，而不是仅记录规则：

- 在 `127.0.0.1:<主机端口>` 监听，把连接中继到 `127.0.0.1:<工作站端口>`；
- 规则跟随工作站生命周期：工作站启动时按启用规则建立监听，停止时全部释放；
- 每条规则记录监听状态、连接数、上下行字节数，可在页面「测试」；
- 端口被占用时不静默失败，状态直接显示「端口被占用」并在诊断页提示；
- **前提**：工作站内的目标端口必须能监听回环，而默认策略 `(deny network*)` 连回环一起拒绝，
  所以「工作站出网」关闭时 `guestOk` 会是 `false`；需要转发时先开启出网；
- 仅支持 TCP：本地进程隔离与宿主共享网络栈，UDP 中继语义不可靠，接口会明确拒绝。

链路自检示例：

```bash
curl -X POST -H "X-Vmbra-Token: $TOKEN" \
  http://127.0.0.1:7788/api/network/forwards/<id>/test
# {"addr":"127.0.0.1:18080","connections":3,"guestOk":true,"guestLatencyMs":0,"listening":true}
```

## VPS 出入口（独立公网 IP）

本地进程隔离与宿主共享网络栈，默认出口就是宿主出口。需要「像 VPS 一样」拥有独立出口与公网入站时，
在「网络 → VPS 出入口」绑定一台 SSH 可达的 VPS：

- **出口**：`ssh -D` 在本机开一个 SOCKS5（默认 `127.0.0.1:1080`），工作站终端、脚本、任务统一注入
  `ALL_PROXY=socks5h://127.0.0.1:1080`，对外请求从 VPS 的公网 IP 离开；
- **入站**：`ssh -R` 把 VPS 上的端口回落到工作站本地端口（例如 `VPS:8080 → 127.0.0.1:8080`），
  访问 VPS 端口即到达工作站内的服务；
- **认证**：控制台生成专用 ed25519 密钥，私钥只留在 `~/.vmbra/vps/`，页面给出一次性安装命令，
  不保存也不传输 VPS 密码；
- **自检**：`POST /api/vps/egress` 同时探测宿主与隧道出口 IP，确认隧道真正生效；
  配置后诊断页会多一项「VPS 出入口」检查。

准备步骤：

```bash
# 1) 控制台点「生成/查看公钥」，复制安装命令，在 VPS 上执行一次（会提示输入密码）
# 2) 保存主机/端口/用户 → 「测试 SSH」→「启动隧道」
# 3) 需要公网入站时添加「VPS 端口 → 工作站端口」规则，并在 VPS 安全组放行
```

> 远端绑定 `0.0.0.0` 需要 VPS 的 `sshd_config` 开启 `GatewayPorts yes`；
> 关闭时端口只绑在 VPS 回环上，适合配合 Nginx 反向代理使用。

## 开通链接（交付导入）

服务商把 VPS 信息打包成一条链接，客户机导入即完成配置，不用手抄主机/端口/密码：

```bash
./bin/vmbra-link --host 203.0.113.10 --port 1841 --user root --password '***' \
  --name '客户 A' --tools chrome,node,git,claude --out /tmp/customer-a.vmbrapkg
```

输出 `vmbra://import/<payload>`；桌面程序注册了 `vmbra` 协议，点击即打开「设置 → 配置导入」弹窗。
链接也可粘进控制台，或用 `http://127.0.0.1:7788/#/settings?import=<payload>` 直达。

导入分两步，客户全程可选：

1. **预览**（`POST /api/import/preview`）：显示出口节点、备注、风险提示（含口令 / 会覆盖现有出口 / 未激活），
   并用本机实际探测标出哪些工具**已安装**；
2. **应用**（`POST /api/import/apply`）：写入 VPS 配置 → 生成本机密钥 →（可选）用口令一次性装公钥 →
   （可选）建隧道 →（可选）按勾选提交安装任务。

可下载的工具与默认勾选：

| 工具 | 默认 | 落地方式 |
| --- | --- | --- |
| Google Chrome | 勾选 | `brew install --cask google-chrome`；已装则跳过 |
| Node.js LTS | 勾选 | Homebrew 或官方压缩包（免 brew） |
| Git | 勾选 | Xcode CLT 自带或 Homebrew |
| Claude Code CLI | 勾选 | `npm install -g` 到用户级目录 |
| Linux 虚拟机终端 | **不勾选** | lima + Ubuntu，约 1GB，需要时再勾 |

取消勾选即不下载；脚本对未勾选的阶段输出 `skip`，进度分母固定 12，客户随时能看到「哪些跳过、哪些装了」。
导入只写配置与密钥：口令不落盘，装完公钥后隧道/终端/校验全部改走密钥。

## 功能板块

| 板块 | 能力 |
| --- | --- |
| 工作站 | 启动/停止/重启、规格（vCPU/内存/磁盘/镜像）、运行时长与占用、隔离引擎与后端探测；
**可新增多台**，每台独立工作区与语言/时区/出口，一键切换（同一时刻只激活一台） |
| 文件 | 浏览/新建/上传（含拖拽）/下载/在线编辑/删除，路径穿越与软链越界防护 |
| 应用 | 图形桌面（VNC 桥接）、Web 终端（xterm.js + PTY，含重连/字号/真彩色/UTF-8）、受控浏览器（固定语言/时区，出口跟随 VPS）、运行脚本（默认允许）、操控 Mac（Beta）、`.deb` 解析与安全解包 |
| 网络 | 出网开关、代理（继承宿主机/手动/关闭）、VPS 出入口（SSH 隧道 / 独立出口 / 远程入站）、DNS 模式与上游、解析测试、出口探测、端口转发、TCP 连通性测试 |
| 环境 | 系统语言文字（语言/编码/时区）与终端环境预览、环境变量、挂载目录、VNC 接入配置、隔离策略路径 |
| 维护与恢复 | 工作区快照创建/恢复/删除、导出工作区 tar.gz、重置工作站、存储占用 |
| 任务 | 任务队列（默认 3 个并发，其余排队）、实时日志流、运行中与排队中均可取消 |
| 诊断 | 22 项自检（令牌权限/引擎/策略/PTY/语言编码/Web 终端/DNS 转发器/域名解析/磁盘/后端/桌面/屏幕权限/端口冲突/出网链路，外加 6 项隔离边界实测：进程启动/工作区内写入/工作区外写入/访问宿主回环/工作站内回环监听/非回环网络；配置 VPS 出入口后再加一项）、导出诊断包 |
| 设置 | 自动启动、主题、令牌轮换、后端列表、版本信息 |

### 图形桌面

本地进程隔离模式只隔离文件与网络，不含图形栈。接入真实桌面的方式：

1. 安装虚拟机后端（Lima / UTM）并启动 Linux 桌面（如 Xfce）；
2. 在虚拟机内启动 VNC 服务（TigerVNC / x11vnc，默认 5900）；
3. 在「环境 → 图形桌面接入」填写 `127.0.0.1:5900`，回到「应用」点「启动桌面」。

控制台通过 `/api/apps/desktop/ws` 把 noVNC 的 WebSocket 桥接到 VNC 服务，
画面不出本机。

### 操控 Mac（Beta）

- 主开关「操控 Mac」控制是否允许工作站内的工具查看 Mac 屏幕；
- 「允许执行 Mac 命令」是独立的第二道开关，开启时会二次确认；
- 命令以当前 Mac 用户身份执行，超时上限 120 秒，输出截断到 256KB；
- 审计只记录 **时间 / 命令摘要哈希 / 退出码 / 耗时**，不记录命令内容，可在「应用 → 操控 Mac → 审计记录」
  中查看最近 200 条，也会一并写入诊断包；
- 屏幕查看走 `/api/mac/screen`（`screencapture`），需要「屏幕录制」权限，仅查看不注入输入。

## HTTP API

所有接口都需要令牌。返回 JSON，错误为 `{"error": "..."}`。

控制台支持深链，便于桌面程序或快捷指令直达面板：`#/terminal`、`#/desktop`、`#/network`、`#/files`，
也可写 `#/apps?open=terminal`；动作只执行一次，消费后 hash 会回落为对应板块。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/status` | 汇总状态（守护进程/引擎/工作站/桌面/Mac/任务） |
| GET PATCH | `/api/workstation` | 当前工作站信息 / 修改规格（停止状态） |
| GET POST | `/api/workstations` | 工作站集合 / 新增一台（名称、镜像、规格、出网、语言、时区、出口） |
| PATCH DELETE | `/api/workstations/{id}` | 修改某台环境参数 / 删除（仅删配置，工作区文件保留） |
| POST | `/api/workstations/{id}/activate` | 切换当前工作站（会自动停止旧环境并重建隔离内核） |
| POST | `/api/workstation/start·stop·restart` | 生命周期 |
| GET | `/api/apps` | 应用页聚合数据 |
| POST | `/api/apps/desktop/start·stop` | 桌面会话 |
| GET(WS) | `/api/apps/desktop/ws` | noVNC ↔ VNC 桥接 |
| GET(WS) | `/api/apps/terminal?kind=user` | PTY 终端（二进制帧=输入输出，文本帧=控制指令） |
| POST | `/api/apps/deb` | 上传 `.deb`（multipart `file`），返回 taskId |
| GET PUT DELETE | `/api/files`、`/api/files/content` | 列目录 / 读写文件 / 删除 / 下载 / 上传 / 新建目录 |
| GET POST PATCH DELETE | `/api/network/...` | 出网开关、代理、端口转发（含 `/forwards/{id}/test`）、连通性测试 |
| GET PUT POST | `/api/vps`、`/api/vps/key·start·stop·test·egress` | VPS 出入口配置、公钥、隧道启停、SSH 连通性与出口 IP 对比 |
| POST | `/api/import/preview` | 解析开通链接，返回安装清单与本机已装探测（不写状态） |
| POST | `/api/import/apply` | 落 VPS 配置 / 装公钥 / 建隧道 / 提交安装任务，返回逐步结果 |
| GET | `/api/import/link` | 用当前配置反向生成链接（不含口令） |
| GET PUT POST | `/api/browser`、`/api/browser/open·verify` | 受控浏览器配置、启动独立 profile（`POST /open` 可带 `{"url":"…"}` 在同 profile 内打开页面）、headless 校验语言与时区 |
| GET PUT | `/api/env`、`/api/env/vars·mounts·desktop` | 环境配置 |
| GET POST DELETE | `/api/maintenance/...` | 快照、恢复、重置、Finder、导出工作区（`/export` 下载 tar.gz） |
| GET POST | `/api/tasks`、`/api/tasks/{id}`、`/api/tasks/{id}/cancel` | 任务与取消 |
| GET(WS) | `/api/tasks/{id}/ws` | 任务实时日志 |
| GET | `/api/diagnostics`、`/api/diagnostics/bundle` | 自检 / 诊断包 zip |
| GET PATCH POST | `/api/settings`、`/api/settings/token/rotate` | 设置与令牌轮换 |
| GET PATCH | `/api/system`、`/api/system/locale`、`/api/system/dns` | 系统语言文字与 DNS 配置 |
| POST | `/api/system/dns/test`、`/api/network/egress` | 解析对比 / 出口探测 |
| POST | `/api/apps/run` | 运行工作区脚本（默认允许） |
| GET POST | `/api/mac`、`/api/mac/screen`、`/api/mac/run`、`/api/mac/audit` | 操控 Mac |

## 安全模型

- 只监听回环地址，并校验 `Host` 必须是 `127.0.0.1 / localhost / ::1`，防止 DNS rebinding；
- 接口要求令牌（`X-Vmbra-Token` 头、`?token=` 或 Cookie），WebSocket 额外校验 `Origin`；
- 令牌文件权限 0600，可在「设置 → 轮换令牌」一键失效全部旧会话；
- 信任边界是**当前系统用户**：能读 `~/.vmbra/token` 的本机进程/个人可以控制本服务，
  这与「以当前用户身份运行本地工具」的前提一致。若需要多用户隔离，请勿把服务暴露到局域网；
- 「操控 Mac」两道开关默认只开查看、不开命令执行，执行前有二次确认，审计不落命令原文。

## 配置

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `VMBRA_DATA_DIR` | `~/.vmbra` | 数据目录（工作站、快照、日志、状态、令牌） |
| `VMBRA_LISTEN` | `127.0.0.1:7788` | 监听地址，仅供本机访问 |
| `VMBRA_PORT` | — | 只覆盖端口 |

数据目录结构：

```
~/.vmbra/
├── state.json            # 工作站/设置/网络/环境/快照/审计
├── token                 # 访问令牌（0600）
├── workstation/
│   ├── workspace/        # 工作区（唯一可写区域）
│   ├── home/             # 沙箱家目录（shell 配置、历史）
│   ├── tmp/              # 沙箱临时目录
│   └── .policy.*.sbpl    # 两条隔离策略
├── snapshots/            # 工作区快照（tar.gz）
├── vps/                  # VPS 出入口专用密钥与 known_hosts
├── browser/              # 受控浏览器的独立 profile
├── bin/chrome-us         # 自动生成的浏览器启动脚本（供桌面图标调用）
└── logs/                 # 任务日志
```

## 开发与测试

```bash
make test        # go test ./...（含沙箱逃逸测试）
make smoke       # 起临时实例，跑一遍接口冒烟
make build       # 编译到 bin/vmbrad
```

前端没有构建步骤：`internal/webui/web/` 下的 HTML/CSS/JS 通过 `go:embed`
打进二进制；`vendor/` 里是 xterm.js 5.3.0 与 noVNC 1.5.0（均为本地文件，离线可用）。

## 已知边界

- 本地进程隔离不是内核级虚拟机：与宿主共享内核、共享网络栈，隔离强度低于 VM；
- 端口转发仅支持 TCP；本地规则监听在回环地址，需要公网入站时走「VPS 出入口」的远程转发；
- 关闭网络后，工作站内进程无法出网，**且无法监听或连接 `127.0.0.1`**（`deny network*` 覆盖回环），
  因此工作站内跑不了本地服务，`VMBRA_DNS_SERVER`（`127.0.0.1:1053`）在该状态下也不可达；
  诊断页的「隔离边界 · 访问宿主回环 / 工作站内回环监听」两项会如实标为告警；
- 不提供 root 会话与系统级 `dpkg` 安装，`.deb` 只解析并解包到工作区，不执行维护脚本；
- 图形桌面依赖外接 VNC 服务；未接入时「启动桌面」会给出启用指引；
- 自定义 DNS 上游由本地转发器（`127.0.0.1:1053`）承担，显式指定解析器的工具与出口探测
  走这条链路；macOS 系统级 `getaddrinfo` 不受影响（改它需要系统权限）；
- 未接入 VPS 时出口 IP 与宿主一致；接上「VPS 出入口」后出口才变成 VPS 公网 IP，
  依赖 VPS 的 SSH 可达性与带宽，隧道断开时会回退为宿主出口。

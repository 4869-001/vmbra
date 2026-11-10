# Vmbra 架构详解

本文描述 Vmbra 的分层、包依赖、运行状态机、协议与授权链路。功能层面请配合 [`app-manual.md`](app-manual.md) 阅读。

## 1. 设计取舍

| 决策 | 理由 |
| --- | --- |
| 单二进制 + 内嵌控制台 | 客户机上没有构建链、没有前端依赖漂移，`copy` 一个文件即可运行 |
| 进程级隔离优先，虚拟机可选 | macOS `sandbox-exec` 不需要虚拟化开销，秒级启动；需要 root/内核态的场景才上 lima |
| 状态落 JSON 而非数据库 | 单个用户、单实例、结构简单；原子写 + 内存快照已足够，免运维 |
| 授权中心独立部署 | 授权与发行解耦，客户端只持有短周期令牌，换套餐/吊销不必发版 |
| 一键配置走幂等 bash | 面向客户的安装动作必须可重复执行、可 dry-run、可观测（NDJSON） |

## 2. 包依赖

```
cmd/vmbrad
   └── config ─ store ─ sandbox ─ tasks ─ webui ─ api ─ service
                                                 │
   service ──┬── sandbox   (策略/PTY/文件/快照)
             ├── tasks     (队列/日志/取消)
             ├── clash     (订阅解析 / mihomo 内核托管)
             ├── egress    (出口闸门 / 租约)
             ├── vps       (ssh 隧道)
             ├── browser   (受控 Chrome)
             ├── dnsproxy  (UDP 中继)
             ├── forward   (TCP 中继)
             ├── deb       (ar/tar + 解压)
             ├── license   (授权中心客户端)
             ├── importlink(开通链接编解码)
             └── store     (state.json)

api ── service（唯一业务入口，不直接碰 sandbox 细节）
webui ── embed.FS（静态资源，无运行时依赖）
```

约束：`api` 只做参数解析与响应，业务判断全在 `service`；`sandbox`/`vps`/`browser` 只暴露能力，不读 `store`。

## 3. 请求生命周期

```
HTTP 请求
  → guard：Host 白名单（防 DNS rebinding）
           → 令牌校验（X-Vmbra-Token / ?token= / Cookie）
           → WS 追加 Origin 校验
  → mux 路由（GET/POST/PATCH/DELETE，Go 1.22 pattern）
  → handler：decode + 调 service
  → ok()/fail()：统一 JSON 信封 { ok, data } / { ok:false, error }
```

WebSocket 三条通道：

| 通道 | 用途 | 帧格式 |
| --- | --- | --- |
| `/api/apps/terminal` | 交互式 shell | 二进制帧 = PTY 输入输出；文本帧 = 控制指令（resize 等） |
| `/api/apps/desktop/ws` | noVNC ↔ VNC | 透传 RFB 帧 |
| `/api/tasks/{id}/ws` | 任务实时日志 | 文本帧，逐行推送 |

## 4. 工作站状态机

```
stopped ──start──▶ starting ──就绪──▶ running ──stop──▶ stopping ──▶ stopped
   ▲                   │                  │                │
   └──── 失败/回收 ─────┴───── error ◀────┴────────────────┘
```

- 只有 `ActiveID` 指向的工作站处于激活态：进程隔离共享内核与网络栈，并行多台只会让"当前是谁"含混。
- 管理服务启动时回收上次进程遗留的 `running/starting/stopping` 标记，避免"假运行"。
- 工作站字段（`store.Workstation`）承载：根目录、驱动（`local`/`lima`）、规格（CPU/内存/磁盘）、出网开关、语言/时区、代理模式、是否走 VPS。

## 5. 隔离实现

`internal/sandbox` 在启动时生成两份策略文件到工作站目录：

| 文件 | 场景 |
| --- | --- |
| `.policy.nonet.sbpl` | 默认，`deny network*` |
| `.policy.net.sbpl` | 开启"工作站出网"后使用 |

执行管线：

```
ExecRequest{Script, Args, Env, Timeout, Network}
  → buildCommand：sandbox-exec -f <policy> → sh -c ...（或直接可执行文件）
  → ulimit -t/-f/-n + 注入 BaseEnv（TERM/COLORTERM/LANG/TZ/PATH）
  → exec.Cmd + Setpgid  → 超时 killGroup（整个进程组）
  → ExecResult{ExitCode, Duration, Output}
StartShell → PTY（creack/pty）→ Session{Read/Write/Resize/Close}
```

自检（`probe`）不是"能否起进程"，而是拿当前策略真实跑六项能力探测：进程启动、工作区内写入、工作区外写入、访问宿主回环、工作站内回环监听、非回环网络，逐项比对期望与实测。`go test ./internal/sandbox/` 另有逃逸测试（工作区外写、默认网络拒绝、路径穿越、快照投毒）。

快照：工作区打包为 `snapshots/<id>.tar.gz`，恢复走先校验再解包（拒绝越界条目）。

## 6. 任务系统

```
Manager.Start(kind,title,fn) → Item
  Item：日志环形缓冲 + 订阅者（WS）+ 状态 + 耗时
  Manager：串行 dispatch（同时只跑一个后台任务），排队可取消
  记录：store.TaskRecord（id/kind/title/status/时间/日志文件）
```

典型来源：`.deb` 安装、运行脚本、一键配置环境、导出工作区、诊断包。日志文件落在 `~/.vmbra/logs/`，控制台通过 WS 实时跟随，重连后先回放快照。

## 7. 授权链路

```
客户端                                   授权中心 license-service
  │  POST /api/v1/activate                  │
  │  { key, fingerprint, appVersion }       │ 1. 授权码存在 & 未吊销
  ├────────────────────────────────────────▶│ 2. IP 策略：免 IP / 白名单命中
  │                                         │ 3. 设备绑定：首激活写指纹，换机需解绑
  │                                         │ 4. 下限版本校验
  │  { token, expireAt, offlineGrace,       │ 5. 签发 HMAC(sha256) 会话令牌
  │    plan, endpoints[], client }          │ 6. 下发出口节点与套餐
  │◀────────────────────────────────────────┤
  │ 落 store.License（key 掩码展示）        │
  │ 后台心跳 POST /api/v1/heartbeat（周期）  │ 续期 + 吊销检测 + 出口变更下发
  │ 不可达 → 进入离线宽限，超过 GraceUntil 视为不可用
```

边界职责：

| 关注点 | 客户端 | 授权中心 |
| --- | --- | --- |
| 授权码校验 | — | 生成/吊销/绑定设备/换套餐 |
| IP 策略 | — | 全局白名单 + 单码独立 IP，默认拒绝 |
| 会话令牌 | 保存与续期 | 签发与验签（HMAC） |
| 离线宽限 | 判定与提示 | 下发给定时长 |
| 出口节点 | 消费 `host:port` 建隧道 | 编排与下发 |

## 8. 一键配置协议

`scripts/vmbra-setup/setup.sh` 是脚本唯一源（`desktop/build.sh` 会打进 `.app/Contents/Resources`），`service/setup.go` 以 `--json --skip-license --yes` 调用并解析 NDJSON：

```
{"event":"step","id":"node","title":"安装 Node.js","status":"running"}
{"event":"step","id":"node","title":"安装 Node.js","status":"ok","detail":"v20.11.0"}
{"event":"log","level":"info","message":"..."}
{"event":"done","ok":true}
```

固定 11 个阶段（license / clt / brew / git / node / vps / vm / guest / claude / profile / verify），跳过也计入，因此进度分母恒定。`stderr` 直接进任务日志；脚本幂等，产物写入 `~/.vmbra/logs/`。

客户端侧前置条件由 `SetupPreflight` 给出（授权是否可用、脚本是否存在、是否已有 lima 实例），`StartSetup` 在未授权且非 `force` 时直接拒绝。

## 9. 数据模型与目录

```
~/.vmbra/
├── state.json        # Workstation[] / Settings / Network / Env / DNS / Locale / VPS / Browser / License / Snapshots / Tasks / 审计
├── token             # 访问令牌 0600
├── workstation/
│   ├── workspace/    # 唯一可写区域
│   ├── home/         # 沙箱家目录（.vmbra.env 由服务端重写）
│   ├── tmp/
│   └── .policy.*.sbpl
├── snapshots/
├── vps/              # 专用密钥与 known_hosts
├── browser/          # 受控 Chrome 独立 profile
├── bin/chrome-us     # 生成的启动脚本，供桌面图标调用
└── logs/
```

`store` 的写入策略：`Update(fn)` 在锁内改内存副本 → 临时文件 + rename 原子替换 → 通知订阅者。旧结构在 `normalize()` 中补默认值与迁移。

## 10. 构建与分发

```
make build ──▶ bin/vmbrad（-trimpath -ldflags "-s -w"）
make app   ──▶ swiftc 编 desktop/main.swift
              → dist/Vmbra.app/Contents/MacOS/Vmbra
              → 复制 bin/vmbrad 到 Contents/Resources/
              → 复制 scripts/vmbra-setup/ 到 Contents/Resources/scripts/
              → codesign -（本地自用 ad-hoc）
```

分发要求：Developer ID 签名 + `notarytool` 公证；授权中心独立部署（HTTPS + `TRUST_PROXY=true` + `data/db.json` 备份）；`HMAC_SECRET`、`ADMIN_PASSWORD` 必须替换。

## 11. 开通链接（交付导入）

`internal/importlink` 定义载荷，`cmd/vmbra-link` 负责生成，`service.ApplyImport` 负责落地，控制台 `views-import.js` 负责勾选与回显。

```
Payload{v, name, note, vps{host,port,user,password,lang,timezone,region}, tools[], createdAt}
        │ base64url（无填充）
        ▼
vmbra://import/<payload>   ← 桌面壳 Info.plist 注册的 scheme，点击直达
http://127.0.0.1:7788/#/settings?import=<payload>   ← 已开控制台时可直接粘地址
.vmbrapkg（纯 JSON，同一份载荷）                     ← 离线交付/留档
```

三条入口共用 `importlink.Decode`，因此链接、控制台 URL、JSON 文件互为等价物。

导入状态机：

```
preview ──▶ 客户勾选 ──▶ apply
              │            ├─ select   保存 SetupSelection（tools / vm）
              │            ├─ vps      UpdateVPS(host,port,user,lang,tz)
              │            ├─ key      EnsureVPSKey()（ed25519，落在数据目录 0600）
              │            ├─ install-key  vps.InstallKey()：SSH_ASKPASS 一次性口令登录，追加公钥
              │            ├─ tunnel   StartVPS()（可选）
              │            └─ setup    StartSetup()（可选，按勾选生成 --tools 参数）
              ▼
        逐项 steps[{id,label,status,detail}] 回显；失败只标记该步，不吞掉其它结果
```

几个刻意设计：

- **口令只活一次**：`InstallKey` 把口令写进 0600 临时文件，交给 `SSH_ASKPASS_REQUIRE=force` 的 ssh 读取，
  调用结束即删；`store.VPS` 里没有任何密码字段，隧道与终端都走密钥。
- **不下载是默认**：`ApplyImport` 只写配置与密钥，`runSetup` 需要客户明确打开，且受授权校验约束
  （未授权时按钮置灰，提示先激活）。
- **失败可解释**：装公钥失败不会中断导入（除非同时要求建隧道），步骤条会写明「口令被拒 / 超时 / 端口不通」，
  用户仍可手工执行页面给出的一次性安装命令。
- **版本可演进**：`v` 高于客户端支持版本时直接拒绝并提示升级，避免旧客户端半解析新载荷。

## 12. 出口与闸门

```
订阅(链接/粘贴) → clash.Parse → ~/.vmbra/proxy/sources.json
                                     │
                        选中节点 → clash.Core.StartAll(全部节点, 选中)
                                     │  生成 runtime.yaml（回环 + external-controller + secret）
                                     ▼
                        probeExit(经内核) → exitInfo{ip, country, city, timezone}
                                     │
              localeForCountry(cc) → Locale/Browser 回写 + .vmbra.env 重写
                                     ▼
                        egress.Gate.Open(report) ── 90s 租约 ──┐
                                     ▲                        │ 每 30s RenewEgress
                                     └── CheckEgress 9 项 ────┘
                          失败/过期 → Close + 关闭 NetworkEnabled + 审计
```

要点：

- **单出口**：启用内核会停掉 VPS 隧道，避免两条链路同时生效导致出口漂移。
- **热切换**：全部节点一次写进 `runtime.yaml`，切节点走控制接口 `PUT /proxies/EGRESS`，不重启进程。
- **测速**：`GET /proxies/{name}/delay`，并发上限 6，结果按延迟升序返回。
- **失败即断网**：`enforceEgressClosed` 是唯一的收口点，保证「管理进程被 kill」的最坏影响只有一个租约周期。
- **无口令入站**：mihomo 的 `authentication` 是全局的，会给 Chrome 返回 `407`；因此入站只监听回环、不设口令，
  控制接口仍用 secret，浏览器与终端共用同一出口。

## 13. 扩展点与已知边界

- **虚拟机后端**：`Workstation.Driver = lima` 已留位；内存上限、root 场景、独立网络栈需该后端提供。
- **网络出口**：本地进程隔离共享宿主网络栈，因此"出口"默认是宿主出口；要独立公网 IP 需 VPS 隧道或虚拟机后端。
- **DNS**：`internal/dnsproxy` 只服务显式指定解析器的工具与诊断；`getaddrinfo` 仍走系统解析器（改系统配置需权限）。
- **端口转发**：仅 TCP；目标端口在工作站内必须能监听回环，出网关闭时 `guestOk=false`。
- **指纹对齐**：只对齐时区/语言/Accept-Language/出口，不伪造 Canvas/WebGL/字体等 OS 级指纹。
- **信任边界**：当前系统用户。能读令牌的本机进程即可控制服务；不要暴露到局域网。

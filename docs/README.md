# 文档索引

Vmbra 的文档按「读什么」组织，不按文件类型堆砌。第一次接触建议按 **1 → 2 → 3** 的顺序读。

## 1. 想先跑起来

| 文档 | 内容 |
|---|---|
| [`../README.md`](../README.md) | 一分钟了解定位、仓库结构、四层架构、快速开始与交付方式 |
| [`app-manual.md`](app-manual.md) | 功能手册：隔离模型、九个板块、受控浏览器、端口转发、VPS 出入口、开通链接、HTTP API、安全模型、配置项 |

## 2. 想改代码

| 文档 | 内容 |
|---|---|
| [`ARCHITECTURE.md`](ARCHITECTURE.md) | 架构详解：设计取舍、包依赖、请求生命周期、工作站状态机、隔离实现、任务系统、授权链路、数据模型、构建分发、扩展点与已知边界 |
| [`../CHANGELOG.md`](../CHANGELOG.md) | 全部功能演进（含每个版本的验证方式与端到端结论） |

## 3. 想部署或运营

| 文档 | 内容 |
|---|---|
| [`../deploy/README.md`](../deploy/README.md) | 授权中心的云服务器部署（docker compose + Caddy 自动 HTTPS） |
| [`../license-service/README.md`](../license-service/README.md) | 授权中心接口、套餐与运营操作 |
| [`../scripts/vmbra-setup/`](../scripts/vmbra-setup/) | 客户端一键环境配置脚本（幂等，NDJSON 进度） |

---

## 目录约定

```
docs/
├── README.md          本索引
├── ARCHITECTURE.md    架构与内部实现（面向开发者）
└── app-manual.md      功能手册（面向使用者与支持）
```

- 面向使用者的功能说明只写进 `app-manual.md`，不散落到根目录。
- 面向实现的机制、状态机、协议只写进 `ARCHITECTURE.md`。
- 功能变更必须同时更新 `CHANGELOG.md`；影响使用方式的还要更新 `app-manual.md`。
- 交付/部署类文档跟随各自目录（`deploy/`、`license-service/`），不重复搬进 `docs/`。

## 提交信息约定

采用 Conventional Commits，作用域写模块名：

```
feat(exit): Clash 订阅导入与租约式出口闸门
fix(webui): 运行中收到 vmbra:// 深链也能弹出导入框
docs(changelog): 补充 0.9.0 全量记录
```

常用类型：`feat` / `fix` / `refactor` / `perf` / `docs` / `test` / `chore`。
一个提交只做一件事；跨模块改动在正文里列要点，便于回溯到 `CHANGELOG.md` 的条目。

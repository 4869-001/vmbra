# 授权中心部署（云服务器）

客户端 `Vmbra.app` 装在客户自己的 Mac 上，**不上服务器**。
服务器上只跑一样东西：授权中心 `license-service`（Node，1 核 1G 足够）。

```
客户 Mac                                    你的云服务器
┌──────────────────────────┐   HTTPS 443   ┌───────────────────────────┐
│ Vmbra.app                │ ────────────▶ │ Caddy（自动 HTTPS）        │
│  └ vmbrad（本地 127.0.0.1）│               │  └ license-service :8787  │
└──────────────────────────┘               │     授权码 / 设备绑定 / 心跳 │
                                           └───────────────────────────┘
```

没有授权中心就没有令牌，客户端进不了工作站；授权中心挂了，客户端还能靠离线宽限期撑一阵，不至于当场停摆。

---

## 1. 准备

| 项 | 要求 |
| --- | --- |
| 云服务器 | 1 核 1G 起，Ubuntu 22.04 / 24.04 即可，安全组放行 80、443 |
| 域名 | 一条 A 记录指向服务器公网 IP，例如 `license.vmbra.app` |
| Docker | 服务器上装 Docker Engine + compose 插件 |

```bash
# 官方脚本，装完重新登录一次让 docker 组生效
curl -fsSL https://get.docker.com | sh
```

## 2. 起服务

```bash
git clone https://github.com/4869-001/vmbra.git
cd vmbra/deploy

cp .env.example .env
# 填 3 个值：VMBRA_DOMAIN / ADMIN_PASSWORD / HMAC_SECRET
# HMAC_SECRET 生成：openssl rand -hex 32
vi .env

docker compose up -d --build
docker compose logs -f caddy      # 等到证书签发成功
```

验证：

```bash
curl https://你的域名/api/v1/health     # {"ok":true,...}
```

## 3. 开场

1. 浏览器打开 `https://你的域名/admin`，用 `.env` 里的账号登录，**立刻改密码**。
2. 生成授权码：个人客户选「免 IP 限制」（家庭宽带 IP 会变），企业/代理选「IP 白名单」。
3. 加「出口节点」：客户要固定出口 IP 时才需要，把 VPS 的 host/port/账号填进去，激活时随令牌下发。
4. 客户拿到授权码：在 Vmbra 控制台「设置 → 软件授权」粘贴，或直接 `open bin/vmbra-activate.command`。

客户端默认连 `https://license.vmbra.app`。要用别的域名，在客户机设环境变量，
或改 `internal/service/license.go` 的 `defaultLicenseServer` 后重新打包。

## 4. 日常运维

```bash
# 备份（授权码 + 设备绑定 + 事件，丢了就得重新发码）
docker run --rm -v vmbra-license_license-data:/data -v "$PWD":/backup alpine \
  tar czf /backup/license-$(date +%F).tgz -C /data .

# 升级授权中心
git pull && docker compose up -d --build

# 看谁在激活、谁被拒
docker compose logs -f license
```

换机解绑、禁用授权码、改 IP 策略都在管理台里点，不用重启。

## 5. 安全底线

- `HMAC_SECRET` 只存在于服务器 `.env`，不要提交、不要发客户。泄露 = 所有令牌可伪造。
- 管理台只走 HTTPS；`.env` 权限 `chmod 600`。
- 授权中心与出口 VPS 分开部署：授权中心被扫不影响客户网络。
- 服务器时间必须是同步的（`timedatectl`），令牌按时间窗校验。

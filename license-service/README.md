# Vmbra 授权中心

独立部署的授权服务：授权码管理、设备指纹绑定、IP 策略、HMAC 会话令牌、心跳续期、Web 管理台。

Node ≥ 18，**零第三方依赖**，数据落 `data/db.json`。

## 启动

```bash
export ADMIN_PASSWORD='强密码'
export HMAC_SECRET="$(openssl rand -hex 32)"
export TRUST_PROXY=true          # 部署在 Nginx/CDN 之后时开启，否则拿不到真实来源 IP
node src/server.js               # 管理台 http://127.0.0.1:8787/admin
```

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `PORT` | `8787` | 监听端口 |
| `HOST` | `0.0.0.0` | 监听地址（本地调试用 `127.0.0.1`） |
| `DATA_DIR` | `../data` | 数据目录 |
| `HMAC_SECRET` | `change-me-in-production` | 会话令牌签名密钥，**必须替换** |
| `ADMIN_USER` | `admin` | 首次启动创建的管理员账号 |
| `ADMIN_PASSWORD` | `admin123` | 首次启动的管理员密码，**必须替换** |
| `TRUST_PROXY` | `false` | 信任 `X-Forwarded-For` / `X-Real-IP` |

首次启动会引导管理员账号并落库（`scrypt` 加盐哈希），登录后请在管理台修改密码。

## 客户端接口

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| `POST` | `/api/v1/activate` | 授权码 + 设备指纹激活，返回令牌/套餐/出口节点 |
| `POST` | `/api/v1/heartbeat` | 心跳续期、吊销检测、出口变更下发 |
| `GET` | `/api/v1/setup/manifest` | 一键配置所需的版本与出口信息 |
| `GET` | `/api/v1/health` | 健康检查 |

## 管理台接口

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| `POST` | `/api/admin/login` · `/api/admin/password` | 登录 / 改密 |
| `GET` | `/api/admin/overview` · `/api/admin/events` | 概览与操作事件流 |
| `GET POST PATCH DELETE` | `/api/admin/licenses...` | 授权码生成、改套餐/IP 策略、解绑、吊销 |
| `GET POST PATCH DELETE` | `/api/admin/ips...` | 全局 IP 白名单 |
| `POST DELETE` | `/api/admin/vps...` | 出口节点维护 |

## IP 策略

授权码生成时二选一，之后可在列表里点标签切换：

- **免 IP 限制（个人）**：只校验授权码 + 设备指纹。家庭宽带公网 IP 会变，个人客户必须用这个。
- **IP 白名单（企业/代理）**：来源 IP 必须命中全局白名单或该授权码的独立 IP。

没有标记免 IP 的授权码，在全局白名单为空时一律拒绝（安全默认）。

## 运维

- `data/db.json` 每日备份；改动前后各留一份
- 反向代理要透传 `X-Forwarded-For`，并开启 `TRUST_PROXY=true`
- 换 `HMAC_SECRET` 会让所有已签发令牌失效，需客户端重新激活

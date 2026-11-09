# MailSender

把多个 SMTP 账号绑在一起，对外提供一个统一的 SMTP 服务。其他程序像配置普通邮箱一样接入（密码填 API Key），每封邮件按策略轮询选择真实账号发出，避免单个邮箱发信过于频繁而触发风控，同时提供统计和日志。

- **单二进制**：Go 后端 + 内嵌 Vue 管理后台 + 纯 Go SQLite，无需 CGO，也不依赖外部数据库
- **轮询策略**：加权轮询、顺序轮询、最少使用、加权随机
- **限额**：每个账号可分别设置每分钟、每小时、每天的上限，到达上限自动跳过
- **失败切换**：发送失败自动换号重试；收件人不存在这类错误不会重试
- **熔断**：连续失败达到阈值后冷却（认证失败立即冷却），并定时做登录健康检测
- **防刷**：限制同一收件人的发送间隔和每日上限；API Key 级别限流和 IP 白名单
- **分组与路由**：账号可打分组标签，API Key 只用指定分组的账号；账号可设「优先收件域名」，例如发往 qq.com 优先用 QQ 邮箱
- **出口代理**：账号可指定 SOCKS5 代理，不同账号从不同 IP 登录邮箱，避免同一服务商看到多个账号来自同一 IP；代理不可用时换号重试，不会回退到服务器本机 IP
- **退信名单**：收件地址被永久拒收（地址不存在等）后自动拉黑，之后在 RCPT 阶段直接返回 550，保护发件账号信誉
- **告警**：账号冷却和恢复、没有可用账号、失败率过高、IP 被封禁时，推送到企业微信、钉钉、飞书、Telegram 或通用 Webhook
- **投递回调**：每封信有结果后，向 API Key 配置的地址 POST 一个带 HMAC 签名的 JSON，可用 `X-MS-Ref` 头关联业务记录
- **SMTP 接入**：程序把本服务当作邮件服务器即可，不用改代码；HTML 和附件原样转发，发出后才返回成功
- **HTTPS / TLS**：支持指定证书文件（更新后自动重新加载）或自动申请 Let's Encrypt 证书，HTTPS 和 SMTP 共用
- **统计**：今日和趋势概览，按账号、按调用方统计，发送日志可看到每次尝试的明细
- **安全**：SMTP 密码用 AES-GCM 加密存储，API Key 只保存哈希，邮件正文不落盘；SMTP 和后台登录连续失败自动封禁 IP；后台支持两步验证（TOTP）

## 快速开始

```bash
make build            # 构建前端并编译 bin/mailsender
./bin/mailsender      # 管理后台 :8080，SMTP 接入 :2525，数据目录 ./data
```

首次启动时，日志里会打印管理员密码（用户名 `admin`）。打开 `http://localhost:8080` 登录后：

1. 在「SMTP 账号」中添加真实邮箱，点「测试连接」确认可用
2. 在「API Key」中为每个使用方创建一个 Key
3. 在程序的邮件设置里按下文填写，「接入文档」页面有 Python、Node.js、PHP 示例

## SMTP 接入

在程序里这样配置：

| 配置项 | 填写 |
|---|---|
| 服务器 | MailSender 的域名或 IP |
| 端口 | `MS_SMTPS_LISTEN` 端口（选 SSL/TLS），或 `MS_SMTP_LISTEN` 端口（有证书时选 STARTTLS，没有证书时选不加密） |
| 用户名 | 任意，比如 `apikey` |
| 密码 | API Key |

- 发出邮件的 From 会被替换成轮询选中的真实账号，否则 SPF/DMARC 校验不通过。原来的显示名称会保留，原地址写入 Reply-To
- 每个收件人（包括抄送、密送）单独发送，分别计入统计和限额
- 发送方会等到邮件真正发出才收到成功响应；暂时没有可用账号时返回 451（对方会自动重试），收件人不存在时返回 554，同一收件人过于频繁时在 RCPT 阶段返回 450，收件人在退信名单中时在 RCPT 阶段返回 550
- 同一 IP 15 分钟内 API Key 错误达到次数（默认 5 次）后封禁一段时间（默认 60 分钟），期间连接直接返回 554；本机地址不受影响
- API Key 的 IP 白名单和每分钟邮件上限对 SMTP 生效
- 已配置证书时，未加密的连接上不提供 AUTH，必须先 STARTTLS（除非设置 `MS_SMTP_INSECURE_AUTH=true`）；没有证书时允许明文 AUTH，只适合在内网或本机使用

## HTTPS / 证书

二选一：

```bash
# 1) 已有证书（例如 certbot 申请的），文件更新后程序会自动重新加载
MS_TLS_CERT=/etc/letsencrypt/live/mail.example.com/fullchain.pem \
MS_TLS_KEY=/etc/letsencrypt/live/mail.example.com/privkey.pem \
MS_SMTPS_LISTEN=:465 MS_SMTP_LISTEN=:587 ./bin/mailsender

# 2) 自动申请 Let's Encrypt 证书（80 和 443 端口需要能从公网访问）
MS_TLS_DOMAIN=mail.example.com MS_LISTEN=:80 \
MS_SMTPS_LISTEN=:465 MS_SMTP_LISTEN=:587 ./bin/mailsender
```

配置证书后，HTTPS 监听在 `MS_HTTPS_LISTEN`（默认 `:443`），SMTP 端口也会启用加密。后台「系统设置 → 服务与证书」可以查看当前状态。也可以不用内置 TLS，改在 Caddy、Nginx 等反向代理上处理 HTTPS（只对 HTTP 有效，SMTP 端口仍然需要证书）。

## 配置

| 环境变量 | 参数 | 默认值 | 说明 |
|---|---|---|---|
| `MS_LISTEN` | `-listen` | `:8080` | 管理后台的 HTTP 监听地址，`off` 表示关闭 |
| `MS_HTTPS_LISTEN` | `-https-listen` | `:443` | HTTPS 监听地址，只在配置了证书时生效 |
| `MS_TLS_CERT` / `MS_TLS_KEY` | `-tls-cert` / `-tls-key` | | 证书和私钥文件（PEM，完整证书链） |
| `MS_TLS_DOMAIN` | `-tls-domain` | | 自动申请证书的域名，多个用逗号分隔；证书缓存在 `data/acme` |
| `MS_ACME_EMAIL` | `-acme-email` | | Let's Encrypt 联系邮箱（可选） |
| `MS_SMTP_LISTEN` | `-smtp-listen` | `:2525` | SMTP 接入端口（有证书时支持 STARTTLS），`off` 表示关闭 |
| `MS_SMTPS_LISTEN` | `-smtps-listen` | | SMTPS 端口（SSL），例如 `:465`，需要证书 |
| `MS_SMTP_HOSTNAME` | `-smtp-hostname` | 证书域名或本机名 | SMTP 问候语中的主机名 |
| `MS_SMTP_INSECURE_AUTH` | `-smtp-insecure-auth` | `false` | 已有证书时，是否仍然允许在未加密的连接上 AUTH |
| `MS_DATA_DIR` | `-data` | `./data` | 数据库和密钥所在目录 |
| `MS_SECRET` | | 自动生成 `data/secret.key` | 主密钥，用于加密 SMTP 密码和签发登录令牌。**丢失后已保存的 SMTP 密码无法解密** |
| `MS_ADMIN_PASSWORD` | | 随机生成 | 首次启动时的管理员密码 |
| `MS_TRUSTED_PROXIES` | `-trusted-proxies` | 空 | 管理后台前面有反向代理时，填代理的 IP/CIDR |
| `MS_DISABLE_2FA` | | | 设为 `true` 时启动时关闭后台两步验证（身份验证器丢失时使用），登录后记得去掉 |

轮询策略、重试次数、熔断阈值、收件人限频、退信名单、告警、登录封禁、健康检测间隔、日志保留天数等运行参数，都在后台「系统设置」和「安全」中修改，保存后立即生效。

### 投递回调

在 API Key 上填「投递回调地址」后，每个收件人的最终结果会 POST 到该地址（失败时 10 秒、1 分钟后各重试一次）：

```json
{"event":"message.sent","id":128,"ref":"order-42","message_id":"<abc@163.com>","to":"user@example.com",
 "subject":"您的验证码","status":"sent","permanent":false,"attempts":1,"error":"","from":"noreply@163.com",
 "duration_ms":2360,"created_at":"…","sent_at":"…"}
```

`ref` 是邮件头 `X-MS-Ref` 的值（发出前会删掉）。请求头 `X-MailSender-Signature: sha256=<hex>` 是用 Key 的签名密钥对 `X-MailSender-Timestamp + "." + 请求体` 做的 HMAC-SHA256，接收端应校验签名并拒绝过旧的时间戳。

### 出口代理

在后台「出口代理」中添加 SOCKS5 代理（可粘贴 `socks5://用户名:密码@主机:端口`），再在账号的「出口」中选择。检测按钮会通过代理访问 IP 回显服务（api.ipify.org 等），显示实际出口 IP。代理端负责解析域名。

不花钱的做法是用 SSH 隧道，代理只监听服务器本机：

```bash
# 家里的电脑或路由器（内网也可以）把出口提供给服务器：服务器上出现 127.0.0.1:1081
ssh -N -o ServerAliveInterval=30 -o ExitOnForwardFailure=yes -R 127.0.0.1:1081 root@服务器

# 服务器借用一台免费云主机的 IP：服务器上出现 127.0.0.1:1082
ssh -N -o ServerAliveInterval=30 -o ExitOnForwardFailure=yes -D 127.0.0.1:1082 user@云主机
```

用 autossh 或 systemd 保持隧道。隧道断开时，用它的账号会发送失败并切换到其他账号。

## Docker

```bash
docker build -t mailsender .
docker run -d -p 8080:8080 -p 2525:2525 -v $PWD/data:/data mailsender
```

## 本地开发

```bash
make dev-smtp                 # 启动 3 个假的上游 SMTP（2600-2602，账号/密码 test/test，2602 固定失败）
go run ./cmd/mailsender       # 后台 :8080，SMTP 接入 :2525
cd web && npm run dev         # 前端热更新 :5173，已代理 /admin/api
```

在后台添加 `127.0.0.1:2600` 等账号（加密方式选「不加密」），再用任意 SMTP 客户端连 `127.0.0.1:2525` 发信，就能观察轮询、失败切换和熔断。

## 目录结构

```
cmd/mailsender     入口
cmd/devsmtp        本地测试用假 SMTP 服务器
internal/core      调度器（轮询/分组/限额/熔断）、限流、发送服务、退信名单、告警、回调、登录封禁
internal/mailer    SMTP 发送与错误分类
internal/server    管理后台 HTTP 接口（/admin/api）
internal/smtpd     SMTP 接入（API Key 认证，转发进轮询）
internal/tlsutil   证书：文件加载与自动续期、Let's Encrypt
internal/store     数据模型与 SQLite
internal/secure    加密、API Key、登录令牌
web/               Vue 3 + Naive UI 管理后台（构建产物嵌入二进制）
```

## 注意事项

- 发件地址必须与 SMTP 账号一致，否则 SPF/DMARC 校验不通过，邮件容易进垃圾箱
- QQ、163 等个人邮箱每天的发信上限较低，建议给它们设置保守的日限额；量大的话加入阿里云邮件推送、SES 等正规发信服务作为兜底
- 计数保存在单进程内存中，重启后从发送记录恢复。目前只支持单实例部署
- IP 封禁记录只在内存中，重启服务会清空

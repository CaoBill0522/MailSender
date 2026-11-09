# MailSender

把多个 SMTP 账号绑在一起，对外提供一个统一的 HTTP 发信接口。每次请求时按策略轮询选择账号，避免单个邮箱发信过于频繁而触发风控，同时提供统计和日志。

- **单二进制**：Go 后端 + 内嵌 Vue 管理后台 + 纯 Go SQLite，无需 CGO，也不依赖外部数据库
- **轮询策略**：加权轮询、顺序轮询、最少使用、加权随机
- **限额**：每个账号可分别设置每分钟、每小时、每天的上限，到达上限自动跳过
- **失败切换**：发送失败自动换号重试；收件人不存在这类错误不会重试
- **熔断**：连续失败达到阈值后冷却（认证失败立即冷却），并定时做登录健康检测
- **防刷**：限制同一收件人的发送间隔和每日上限；API Key 级别限流和 IP 白名单
- **同步和异步**：异步请求在所有账号都忙时排队等待，服务重启后自动恢复队列
- **统计**：今日和趋势概览，按账号、按调用方统计，发送日志可看到每次尝试的明细
- **安全**：SMTP 密码用 AES-GCM 加密存储，API Key 只保存哈希，日志中的验证码打码

## 快速开始

```bash
make build            # 构建前端并编译 bin/mailsender
./bin/mailsender      # 默认监听 :8080，数据目录 ./data
```

首次启动时，日志里会打印管理员密码（用户名 `admin`）。打开 `http://localhost:8080` 登录后：

1. 在「SMTP 账号」中添加账号，点「测试连接」确认可用
2. 在「API Key」中为每个调用方创建一个 Key
3. 调用方按「接入文档」页面中的示例发信

```bash
curl -X POST http://localhost:8080/api/v1/send/code \
  -H "Authorization: Bearer ms_xxx" \
  -d '{"to": "user@example.com"}'
# => {"code":"ok","data":{"id":1,"status":"sent","code":"604213", ...}}
```

## 配置

| 环境变量 | 参数 | 默认值 | 说明 |
|---|---|---|---|
| `MS_LISTEN` | `-listen` | `:8080` | 监听地址 |
| `MS_DATA_DIR` | `-data` | `./data` | 数据库和密钥所在目录 |
| `MS_SECRET` | | 自动生成 `data/secret.key` | 主密钥，用于加密 SMTP 密码和签发登录令牌。**丢失后已保存的 SMTP 密码无法解密** |
| `MS_ADMIN_PASSWORD` | | 随机生成 | 首次启动时的管理员密码 |
| `MS_TRUSTED_PROXIES` | `-trusted-proxies` | 空 | 反向代理的 IP/CIDR，用于获取真实客户端 IP（IP 白名单依赖它） |

轮询策略、重试次数、熔断阈值、收件人限频、健康检测间隔、日志保留天数等运行参数，都在后台「系统设置」中修改，保存后立即生效。

## Docker

```bash
docker build -t mailsender .
docker run -d -p 8080:8080 -v $PWD/data:/data mailsender
```

## 本地开发

```bash
make dev-smtp                 # 启动 3 个假 SMTP（2525-2527，账号/密码 test/test，2527 固定失败）
go run ./cmd/mailsender       # 后端 :8080
cd web && npm run dev         # 前端热更新 :5173，已代理 /api 和 /admin/api
```

在后台添加 `127.0.0.1:2525` 等账号（加密方式选「不加密」），就能观察轮询、失败切换和熔断。

## 目录结构

```
cmd/mailsender     入口
cmd/devsmtp        本地测试用假 SMTP 服务器
internal/core      调度器（轮询/限额/熔断）、限流、发送服务、设置
internal/mailer    SMTP 发送与错误分类
internal/server    HTTP 接口（对外 /api/v1，后台 /admin/api）
internal/store     数据模型与 SQLite
internal/secure    加密、API Key、登录令牌
web/               Vue 3 + Naive UI 管理后台（构建产物嵌入二进制）
```

## 注意事项

- 发件地址必须与 SMTP 账号一致，否则 SPF/DMARC 校验不通过，邮件容易进垃圾箱
- QQ、163 等个人邮箱每天的发信上限较低，建议给它们设置保守的日限额；量大的话加入阿里云邮件推送、SES 等正规发信服务作为兜底
- 计数保存在单进程内存中，重启后从发送记录恢复。目前只支持单实例部署

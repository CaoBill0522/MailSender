<template>
  <div class="docs">
    <div class="page-head"><h2>接入文档</h2></div>

    <n-card size="small" title="鉴权">
      <p>在「API Key」页面创建 Key，然后在请求头中携带下面任意一种：</p>
      <pre class="code">Authorization: Bearer ms_xxxxxxxx
X-API-Key: ms_xxxxxxxx</pre>
      <p class="muted">服务地址：<span class="mono">{{ origin }}</span></p>
    </n-card>

    <n-card size="small" title="发送验证码  POST /api/v1/send/code">
      <p>最常用的接口。不传 <code>code</code> 时由服务端生成 6 位数字，并在响应中返回，供调用方保存和校验。</p>
      <pre class="code">{{ codeCurl }}</pre>
      <n-table size="small" :single-line="false">
        <thead><tr><th>参数</th><th>类型</th><th>说明</th></tr></thead>
        <tbody>
          <tr><td>to</td><td>string</td><td>收件人，必填，单个邮箱</td></tr>
          <tr><td>code</td><td>string</td><td>验证码，可选，不传则自动生成</td></tr>
          <tr><td>length</td><td>int</td><td>自动生成的验证码长度，默认 6</td></tr>
          <tr><td>minutes</td><td>int</td><td>邮件中显示的有效期（分钟），默认 10</td></tr>
          <tr><td>template</td><td>string</td><td>模板名，默认 verify_code</td></tr>
          <tr><td>vars</td><td>object</td><td>额外模板变量</td></tr>
          <tr><td>from_name</td><td>string</td><td>覆盖发件人显示名称</td></tr>
          <tr><td>async</td><td>bool</td><td>true 时立即返回，后台发送</td></tr>
        </tbody>
      </n-table>
      <p>响应示例：</p>
      <pre class="code">{
  "code": "ok",
  "data": { "id": 128, "status": "sent", "to": "user@example.com", "from": "noreply1@example.com",
            "attempts": 1, "duration_ms": 412, "code": "604213" }
}</pre>
    </n-card>

    <n-card size="small" title="通用发信  POST /api/v1/send">
      <p>二选一：直接传 <code>subject</code> 和 <code>html</code>/<code>text</code>，或者传 <code>template</code> 和 <code>vars</code>。</p>
      <pre class="code">{{ sendCurl }}</pre>
    </n-card>

    <n-card size="small" title="查询状态  GET /api/v1/messages/:id">
      <p>用于查询异步发送的结果，只能查到当前 Key 发出的消息。</p>
      <pre class="code">curl {{ origin }}/api/v1/messages/128 -H "Authorization: Bearer $KEY"</pre>
    </n-card>

    <n-card size="small" title="SMTP 接入（像普通邮箱一样配置）">
      <p>只支持填 SMTP 的程序（WordPress、Gitea、各种框架自带的邮件模块等）可以直接把本服务当作 SMTP 服务器，不用改代码。</p>
      <n-alert v-if="!info?.smtp" type="warning" :show-icon="false">SMTP 接入未启用。启动时设置 <code>MS_SMTP_LISTEN</code>（如 :587）或 <code>MS_SMTPS_LISTEN</code>（如 :465）。</n-alert>
      <template v-else>
        <n-table size="small" :single-line="false" style="margin: 8px 0">
          <tbody>
            <tr><td style="width: 140px">SMTP 服务器</td><td class="mono">{{ smtpHost }}</td></tr>
            <tr v-if="smtpsPort"><td>端口（推荐）</td><td><span class="mono">{{ smtpsPort }}</span>，加密方式选 <b>SSL/TLS</b></td></tr>
            <tr v-if="smtpPort">
              <td>端口</td>
              <td>
                <span class="mono">{{ smtpPort }}</span>，
                <template v-if="info.smtp.starttls">加密方式选 <b>STARTTLS</b></template>
                <template v-else>加密方式选 <b>无</b></template>
              </td>
            </tr>
            <tr><td>用户名</td><td>任意，例如 <span class="mono">apikey</span></td></tr>
            <tr><td>密码</td><td>在「API Key」页面创建的 Key（<span class="mono">ms_…</span>）</td></tr>
            <tr><td>发件人地址</td><td>随意填写，发出时会被替换（见下文）</td></tr>
            <tr><td>单封大小上限</td><td>{{ info.smtp.max_mib }} MB，最多 50 个收件人</td></tr>
          </tbody>
        </n-table>
        <n-alert v-if="!info.smtp.starttls && smtpPort" type="warning" :show-icon="false" style="margin-bottom: 8px">
          当前没有配置 TLS 证书，端口 {{ smtpPort }} 不加密，API Key 会以明文传输，只能在内网或本机使用。配置证书的方法见「系统设置 → 服务与证书」。
        </n-alert>
        <ul class="notes">
          <li><b>发件人会被替换</b>：实际发出的 From 是轮询选中的真实账号（否则 SPF/DMARC 不通过、进垃圾箱）。你填写的显示名称会保留，原地址写入 Reply-To，收件人回复时仍回到原地址。</li>
          <li>每个收件人（含抄送、密送）单独发送一次，分别计入统计和限额；附件和 HTML 原样转发。</li>
          <li>程序会等到真正发出才返回成功；没有可用账号时返回 451（发送方会自动稍后重试），收件人不存在等永久错误返回 554。</li>
          <li>同一收件人发送过于频繁时，在 RCPT 阶段返回 450，可稍后重试。</li>
        </ul>
        <p>示例（Python）：</p>
        <pre class="code">{{ pySmtp }}</pre>
        <p>示例（Node.js / Nodemailer）：</p>
        <pre class="code">{{ nodeSmtp }}</pre>
      </template>
    </n-card>

    <n-card size="small" title="状态码">
      <n-table size="small" :single-line="false">
        <thead><tr><th>HTTP</th><th>code</th><th>含义</th></tr></thead>
        <tbody>
          <tr><td>200</td><td>ok</td><td>发送成功，或异步模式已入队（status=queued）</td></tr>
          <tr><td>400</td><td>bad_request / template_not_found / template_error</td><td>参数错误</td></tr>
          <tr><td>401</td><td>unauthorized</td><td>Key 无效或已停用</td></tr>
          <tr><td>403</td><td>ip_forbidden</td><td>IP 不在白名单</td></tr>
          <tr><td>429</td><td>recipient_too_frequent / recipient_daily_limit / key_rate_limited</td><td>触发频率限制，不会发送</td></tr>
          <tr><td>502</td><td>send_failed</td><td>所有尝试的账号都发送失败，详见 message</td></tr>
          <tr><td>503</td><td>no_available_account</td><td>当前没有可用账号（都已限额、冷却或停用）</td></tr>
        </tbody>
      </n-table>
    </n-card>
  </div>
</template>

<script setup>
import { computed, onMounted, ref } from 'vue'
import { NCard, NTable, NAlert } from 'naive-ui'
import { api } from '../api'

const origin = window.location.origin
const info = ref(null)
onMounted(async () => {
  try { info.value = await api.get('/server-info') } catch {}
})
const portOf = (addr) => (addr ? addr.slice(addr.lastIndexOf(':') + 1) : '')
const smtpHost = computed(() => info.value?.tls_domains?.[0] || info.value?.smtp?.hostname || window.location.hostname)
const smtpPort = computed(() => portOf(info.value?.smtp?.listen))
const smtpsPort = computed(() => portOf(info.value?.smtp?.listen_tls))
const pySmtp = computed(() =>
  smtpsPort.value
    ? `import smtplib
from email.message import EmailMessage

msg = EmailMessage()
msg["From"] = "我的应用 <noreply@myapp.com>"
msg["To"] = "user@example.com"
msg["Subject"] = "测试"
msg.set_content("你好")

with smtplib.SMTP_SSL("${smtpHost.value}", ${smtpsPort.value}) as s:
    s.login("apikey", "ms_xxx")
    s.send_message(msg)`
    : `import smtplib
from email.message import EmailMessage

msg = EmailMessage()
msg["From"] = "我的应用 <noreply@myapp.com>"
msg["To"] = "user@example.com"
msg["Subject"] = "测试"
msg.set_content("你好")

with smtplib.SMTP("${smtpHost.value}", ${smtpPort.value}) as s:
${info.value?.smtp?.starttls ? '    s.starttls()\n' : ''}    s.login("apikey", "ms_xxx")
    s.send_message(msg)`,
)
const nodeSmtp = computed(() => {
  const secure = !!smtpsPort.value
  const port = secure ? smtpsPort.value : smtpPort.value
  return `const transporter = nodemailer.createTransport({
  host: "${smtpHost.value}",
  port: ${port},
  secure: ${secure},${!secure && info.value?.smtp?.starttls ? '\n  requireTLS: true,' : ''}
  auth: { user: "apikey", pass: "ms_xxx" },
})
await transporter.sendMail({ from: "我的应用 <noreply@myapp.com>", to: "user@example.com", subject: "测试", text: "你好" })`
})
const codeCurl = `curl -X POST ${origin}/api/v1/send/code \\
  -H "Authorization: Bearer $KEY" \\
  -H "Content-Type: application/json" \\
  -d '{"to": "user@example.com", "minutes": 10}'`
const sendCurl = `curl -X POST ${origin}/api/v1/send \\
  -H "Authorization: Bearer $KEY" \\
  -H "Content-Type: application/json" \\
  -d '{"to": "user@example.com", "subject": "欢迎注册", "html": "<p>欢迎！</p>", "text": "欢迎！", "async": true}'

# 或使用模板
curl -X POST ${origin}/api/v1/send \\
  -H "Authorization: Bearer $KEY" \\
  -d '{"to": "user@example.com", "template": "verify_code", "vars": {"code": "123456", "minutes": 5}}'`
</script>

<style scoped>
.docs :deep(.n-card) { margin-bottom: 16px; }
.code { background: rgba(127, 127, 127, 0.1); padding: 12px 14px; border-radius: 6px; overflow-x: auto; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 12.5px; line-height: 1.6; margin: 8px 0; }
code { background: rgba(127, 127, 127, 0.12); padding: 1px 5px; border-radius: 4px; font-size: 12.5px; }
p { margin: 6px 0; font-size: 14px; }
.notes { margin: 8px 0; padding-left: 18px; line-height: 1.8; font-size: 13.5px; }
</style>

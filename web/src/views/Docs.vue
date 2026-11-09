<template>
  <div class="docs">
    <div class="page-head"><h2>接入文档</h2></div>

    <n-card size="small" title="SMTP 连接设置">
      <p>在程序的邮件设置里，把本服务当作普通的 SMTP 服务器填写即可，不用改代码。</p>
      <n-alert v-if="info && !info.smtp" type="warning" :show-icon="false">
        SMTP 接入未启用。启动时设置 <code>MS_SMTP_LISTEN</code>（如 :587）或 <code>MS_SMTPS_LISTEN</code>（如 :465）。
      </n-alert>
      <template v-else-if="info">
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
        <n-alert v-if="!info.smtp.starttls && smtpPort" type="warning" :show-icon="false">
          当前没有配置 TLS 证书，端口 {{ smtpPort }} 不加密，API Key 会以明文传输，只能在内网或本机使用。配置证书的方法见「系统设置 → 服务与证书」。
        </n-alert>
      </template>
    </n-card>

    <n-card size="small" title="发信规则">
      <ul class="notes">
        <li><b>发件人会被替换</b>：实际发出的 From 是轮询选中的真实账号，否则 SPF/DMARC 校验不通过，邮件会进垃圾箱。你填写的显示名称会保留，原地址写入 Reply-To，收件人回复时仍回到原地址。</li>
        <li>每个收件人（包括抄送、密送）单独发送一次，分别计入统计和限额。HTML、附件原样转发。</li>
        <li>程序会等到邮件真正发出后才收到成功响应。一封信有多个收件人时，只要有一个发送成功就返回成功，失败的收件人在「发送日志」中查看。</li>
        <li>API Key 的 IP 白名单和「每分钟邮件上限」对 SMTP 生效；同一收件人的发送间隔和每日上限在「系统设置」中配置。</li>
        <li><b>账号分组</b>：API Key 绑定了分组时，只会用带这些分组标签的账号发信，例如验证码和营销通知分开走不同的账号。账号设置了「优先收件域名」时，发往这些域名的邮件优先用它，失败后再换其他账号。</li>
        <li><b>退信名单</b>：收件服务器明确拒收的地址（如邮箱不存在）会自动拉黑，之后发往它的邮件在 RCPT 阶段直接返回 550，避免反复发给无效地址拉低账号信誉。可在「退信名单」页移除。</li>
        <li>连续登录失败（API Key 错误）的 IP 会被临时封禁，阈值在「安全」页设置。</li>
      </ul>
    </n-card>

    <n-card size="small" title="示例代码">
      <p>Python：</p>
      <pre class="code">{{ pySmtp }}</pre>
      <p>Node.js（Nodemailer）：</p>
      <pre class="code">{{ nodeSmtp }}</pre>
      <p>PHP（PHPMailer）：</p>
      <pre class="code">{{ phpSmtp }}</pre>
    </n-card>

    <n-card size="small" title="投递回调（可选）">
      <p>在 API Key 上设置「投递回调地址」后，每封信（每个收件人）有最终结果时，会向该地址 POST 一个 JSON。接收端返回 2xx 视为成功，否则在 10 秒和 1 分钟后各重试一次。</p>
      <p>想把回调和自己的业务记录对应起来，可以在邮件里加一个 <code>X-MS-Ref</code> 头（例如订单号或用户 ID），它会原样出现在回调的 <code>ref</code> 字段里，发出前会被删掉，收件人看不到。</p>
      <pre class="code">{{ callbackBody }}</pre>
      <p>请求头 <code>X-MailSender-Timestamp</code> 是 Unix 秒，<code>X-MailSender-Signature</code> 是 <code>sha256=</code> 加上 HMAC-SHA256(签名密钥, 时间戳 + "." + 原始请求体) 的十六进制。验证示例（Python）：</p>
      <pre class="code">{{ verifyPy }}</pre>
    </n-card>

    <n-card size="small" title="SMTP 响应码">
      <n-table size="small" :single-line="false">
        <thead><tr><th style="width: 90px">响应码</th><th style="width: 110px">阶段</th><th>含义</th></tr></thead>
        <tbody>
          <tr><td>250</td><td>DATA</td><td>发送成功（多个收件人时至少一个成功）</td></tr>
          <tr><td>535</td><td>AUTH</td><td>API Key 无效、已停用，或 IP 不在白名单</td></tr>
          <tr><td>502</td><td>MAIL</td><td>未登录。有证书时，必须先 STARTTLS 才能登录</td></tr>
          <tr><td>450</td><td>MAIL</td><td>超过该 Key 的每分钟邮件上限，稍后重试</td></tr>
          <tr><td>450</td><td>RCPT</td><td>该收件人发送过于频繁，或已达每日上限</td></tr>
          <tr><td>550</td><td>RCPT</td><td>收件人在退信名单中（之前被收件服务器永久拒收）</td></tr>
          <tr><td>554</td><td>连接</td><td>该 IP 登录失败次数过多，已被临时封禁</td></tr>
          <tr><td>501 / 452</td><td>RCPT</td><td>收件人地址无效，或单封超过 50 个收件人</td></tr>
          <tr><td>451</td><td>DATA</td><td>暂时没有可用账号（都在限额或冷却中），或上游临时错误。发送方会自动稍后重试</td></tr>
          <tr><td>554</td><td>DATA</td><td>永久失败，例如收件人邮箱不存在</td></tr>
          <tr><td>552</td><td>DATA</td><td>邮件超过大小上限</td></tr>
        </tbody>
      </n-table>
    </n-card>
  </div>
</template>

<script setup>
import { computed, onMounted, ref } from 'vue'
import { NCard, NTable, NAlert } from 'naive-ui'
import { api } from '../api'

const info = ref(null)
onMounted(async () => {
  try { info.value = await api.get('/server-info') } catch {}
})
const portOf = (addr) => (addr ? addr.slice(addr.lastIndexOf(':') + 1) : '')
const smtpHost = computed(() => info.value?.tls_domains?.[0] || window.location.hostname)
const smtpPort = computed(() => portOf(info.value?.smtp?.listen))
const smtpsPort = computed(() => portOf(info.value?.smtp?.listen_tls))
const starttls = computed(() => !!info.value?.smtp?.starttls)
const useSSL = computed(() => !!smtpsPort.value)
const port = computed(() => (useSSL.value ? smtpsPort.value : smtpPort.value) || '587')

const pySmtp = computed(() => `import smtplib
from email.message import EmailMessage

msg = EmailMessage()
msg["From"] = "我的应用 <noreply@myapp.com>"
msg["To"] = "user@example.com"
msg["Subject"] = "您的验证码：123456"
msg.set_content("您的验证码是 123456，10 分钟内有效。")

${
  useSSL.value
    ? `with smtplib.SMTP_SSL("${smtpHost.value}", ${port.value}) as s:`
    : `with smtplib.SMTP("${smtpHost.value}", ${port.value}) as s:${starttls.value ? '\n    s.starttls()' : ''}`
}
    s.login("apikey", "ms_xxx")
    s.send_message(msg)`)

const nodeSmtp = computed(() => `const transporter = nodemailer.createTransport({
  host: "${smtpHost.value}",
  port: ${port.value},
  secure: ${useSSL.value},${!useSSL.value && starttls.value ? '\n  requireTLS: true,' : ''}
  auth: { user: "apikey", pass: "ms_xxx" },
})
await transporter.sendMail({
  from: "我的应用 <noreply@myapp.com>",
  to: "user@example.com",
  subject: "您的验证码：123456",
  text: "您的验证码是 123456，10 分钟内有效。",
})`)

const callbackBody = `{
  "event": "message.sent",          // 或 message.failed；测试按钮发送 message.test
  "id": 128,                        // 发送日志中的消息 ID
  "ref": "order-42",                // 邮件头 X-MS-Ref 的值
  "message_id": "<abc@163.com>",    // 实际发出的 Message-ID
  "to": "user@example.com",
  "subject": "您的验证码：123456",
  "status": "sent",                 // sent | failed
  "permanent": false,               // true 表示收件地址被永久拒收
  "attempts": 1,
  "error": "",
  "from": "noreply@163.com",
  "duration_ms": 2360,
  "created_at": "2026-11-09T10:24:45+08:00",
  "sent_at": "2026-11-09T10:24:48+08:00"
}`
const verifyPy = `import hmac, hashlib, time

def verify(secret: str, headers, body: bytes) -> bool:
    ts = headers["X-MailSender-Timestamp"]
    if abs(time.time() - int(ts)) > 300:      # 拒绝 5 分钟前的请求，防重放
        return False
    want = "sha256=" + hmac.new(secret.encode(), ts.encode() + b"." + body, hashlib.sha256).hexdigest()
    return hmac.compare_digest(want, headers["X-MailSender-Signature"])`

const phpSmtp = computed(() => `$mail = new PHPMailer\\PHPMailer\\PHPMailer(true);
$mail->isSMTP();
$mail->Host       = '${smtpHost.value}';
$mail->Port       = ${port.value};
$mail->SMTPSecure = ${useSSL.value ? "'ssl'" : starttls.value ? "'tls'" : "''"};
$mail->SMTPAuth   = true;
$mail->Username   = 'apikey';
$mail->Password   = 'ms_xxx';
$mail->CharSet    = 'UTF-8';
$mail->setFrom('noreply@myapp.com', '我的应用');
$mail->addAddress('user@example.com');
$mail->Subject = '您的验证码：123456';
$mail->Body    = '您的验证码是 123456，10 分钟内有效。';
$mail->send();`)
</script>

<style scoped>
.docs :deep(.n-card) { margin-bottom: 16px; }
.code { background: rgba(127, 127, 127, 0.1); padding: 12px 14px; border-radius: 6px; overflow-x: auto; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 12.5px; line-height: 1.6; margin: 8px 0; }
code { background: rgba(127, 127, 127, 0.12); padding: 1px 5px; border-radius: 4px; font-size: 12.5px; }
p { margin: 6px 0; font-size: 14px; }
.notes { margin: 0; padding-left: 18px; line-height: 1.8; font-size: 13.5px; }
</style>

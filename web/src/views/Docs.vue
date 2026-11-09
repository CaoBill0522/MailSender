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
import { NCard, NTable } from 'naive-ui'

const origin = window.location.origin
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
</style>

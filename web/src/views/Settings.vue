<template>
  <div>
    <div class="page-head"><h2>系统设置</h2></div>
    <n-grid cols="1 l:2" responsive="screen" :x-gap="16" :y-gap="16">
      <n-gi>
        <n-card size="small" title="轮询与风控">
          <n-form v-if="s" label-placement="left" label-width="150">
            <n-form-item label="轮询策略">
              <n-select v-model:value="s.strategy" :options="strategyOptions" />
            </n-form-item>
            <n-form-item label="单封最多尝试账号数">
              <n-input-number v-model:value="s.max_attempts" :min="1" :max="10" />
              <span class="hint">失败时自动换号重试</span>
            </n-form-item>
            <n-form-item label="连续失败熔断阈值">
              <n-input-number v-model:value="s.fail_threshold" :min="1" />
              <span class="hint">次（认证失败立即熔断）</span>
            </n-form-item>
            <n-form-item label="熔断冷却时长">
              <n-input-number v-model:value="s.cooldown_minutes" :min="1" />
              <span class="hint">分钟</span>
            </n-form-item>
            <n-form-item label="同一收件人最小间隔">
              <n-input-number v-model:value="s.recipient_interval_sec" :min="0" />
              <span class="hint">秒，0 关闭</span>
            </n-form-item>
            <n-form-item label="同一收件人每日上限">
              <n-input-number v-model:value="s.recipient_daily_max" :min="0" />
              <span class="hint">封，0 关闭</span>
            </n-form-item>
            <n-form-item label="自动健康检测间隔">
              <n-input-number v-model:value="s.health_check_minutes" :min="0" />
              <span class="hint">分钟，0 关闭</span>
            </n-form-item>
            <n-form-item label="SMTP 超时">
              <n-input-number v-model:value="s.send_timeout_sec" :min="3" :max="120" />
              <span class="hint">秒</span>
            </n-form-item>
            <n-form-item label="日志保留">
              <n-input-number v-model:value="s.log_retention_days" :min="0" />
              <span class="hint">天，0 永久（统计数据不受影响）</span>
            </n-form-item>
            <n-button type="primary" :loading="saving" @click="save">保存设置</n-button>
          </n-form>
        </n-card>
      </n-gi>
      <n-gi>
        <n-card size="small" title="修改管理员密码">
          <n-form label-placement="left" label-width="90">
            <n-form-item label="原密码">
              <n-input v-model:value="pw.old_password" type="password" show-password-on="click" />
            </n-form-item>
            <n-form-item label="新密码">
              <n-input v-model:value="pw.new_password" type="password" show-password-on="click" placeholder="至少 8 位" />
            </n-form-item>
            <n-form-item label="确认新密码">
              <n-input v-model:value="pw.confirm" type="password" show-password-on="click" />
            </n-form-item>
            <n-button :loading="pwSaving" @click="changePw">修改密码</n-button>
          </n-form>
        </n-card>
        <n-card v-if="info" size="small" title="服务与证书" style="margin-top: 16px">
          <n-descriptions :column="1" label-placement="left" size="small" bordered>
            <n-descriptions-item label="TLS 证书">
              <n-tag v-if="info.tls_mode === 'none'" size="small" type="warning" :bordered="false">◐ 未配置</n-tag>
              <n-tag v-else size="small" type="success" :bordered="false">● {{ info.tls_mode === 'acme' ? "Let's Encrypt 自动证书" : '证书文件' }}</n-tag>
              <span v-if="info.tls_domains?.length" class="mono" style="margin-left: 8px; font-size: 12px">{{ info.tls_domains.join(', ') }}</span>
            </n-descriptions-item>
            <n-descriptions-item label="HTTP">{{ info.http_listen || '关闭' }}<span v-if="info.http_redirect" class="hint">仅跳转到 HTTPS</span></n-descriptions-item>
            <n-descriptions-item label="HTTPS">{{ info.https_listen || '关闭' }}</n-descriptions-item>
            <n-descriptions-item label="SMTP（STARTTLS）">
              <template v-if="info.smtp?.listen">{{ info.smtp.listen }}<span v-if="!info.smtp.starttls" class="hint" style="color: #d03b3b">无 TLS，仅限内网</span></template>
              <template v-else>关闭</template>
            </n-descriptions-item>
            <n-descriptions-item label="SMTPS（SSL）">{{ info.smtp?.listen_tls || '关闭' }}</n-descriptions-item>
          </n-descriptions>
          <div class="muted" style="margin-top: 10px; line-height: 1.8">
            以上通过启动参数或环境变量配置，修改后需重启：<br />
            证书文件：<span class="mono">MS_TLS_CERT</span> / <span class="mono">MS_TLS_KEY</span>（文件更新后自动重新加载）<br />
            自动证书：<span class="mono">MS_TLS_DOMAIN=mail.example.com</span>（需 80/443 端口可从公网访问）<br />
            端口：<span class="mono">MS_HTTPS_LISTEN</span>、<span class="mono">MS_SMTP_LISTEN</span>、<span class="mono">MS_SMTPS_LISTEN</span>
          </div>
        </n-card>
        <n-card size="small" title="策略说明" style="margin-top: 16px">
          <ul class="explain">
            <li><b>加权轮询</b>：按权重均匀交替，权重 2 的账号发信量约为权重 1 的两倍（推荐）。</li>
            <li><b>顺序轮询</b>：忽略权重，按账号顺序依次使用。</li>
            <li><b>最少使用</b>：优先使用今天发得最少的账号，适合各账号日限额接近的场景。</li>
            <li><b>加权随机</b>：按权重随机挑选。</li>
            <li>达到分钟、小时或日限额，以及处于冷却中的账号会被自动跳过。异步请求在所有账号都忙时会排队等待，最长 30 分钟。</li>
          </ul>
        </n-card>
      </n-gi>
    </n-grid>
  </div>
</template>

<script setup>
import { onMounted, reactive, ref } from 'vue'
import { NButton, NCard, NForm, NFormItem, NInput, NInputNumber, NSelect, NGrid, NGi, NDescriptions, NDescriptionsItem, NTag } from 'naive-ui'
import { api, setToken } from '../api'
import { message } from '../utils'

const s = ref(null)
const saving = ref(false)
const strategyOptions = [
  { label: '加权轮询（weighted）', value: 'weighted' },
  { label: '顺序轮询（round_robin）', value: 'round_robin' },
  { label: '最少使用（least_used）', value: 'least_used' },
  { label: '加权随机（random）', value: 'random' },
]

async function save() {
  saving.value = true
  try {
    s.value = await api.put('/settings', s.value)
    message.success('已保存，立即生效')
  } catch (e) {
    message.error(e.message)
  } finally {
    saving.value = false
  }
}

const pw = reactive({ old_password: '', new_password: '', confirm: '' })
const pwSaving = ref(false)
async function changePw() {
  if (pw.new_password !== pw.confirm) return message.error('两次输入的新密码不一致')
  pwSaving.value = true
  try {
    const r = await api.post('/password', pw)
    setToken(r.token)
    Object.assign(pw, { old_password: '', new_password: '', confirm: '' })
    message.success('密码已修改')
  } catch (e) {
    message.error(e.message)
  } finally {
    pwSaving.value = false
  }
}

const info = ref(null)
onMounted(async () => {
  ;[s.value, info.value] = await Promise.all([api.get('/settings'), api.get('/server-info')])
})
</script>

<style scoped>
.hint { margin-left: 10px; opacity: 0.6; font-size: 12px; white-space: nowrap; }
.explain { margin: 0; padding-left: 18px; line-height: 1.9; font-size: 13px; }
</style>

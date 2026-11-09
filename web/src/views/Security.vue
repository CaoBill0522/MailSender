<template>
  <div>
    <div class="page-head"><h2>安全</h2></div>
    <n-grid cols="1 l:2" responsive="screen" :x-gap="16" :y-gap="16">
      <n-gi>
        <n-card size="small" title="两步验证（后台登录）">
          <template #header-extra>
            <n-tag v-if="tfa" size="small" :type="tfa.enabled ? 'success' : 'warning'" :bordered="false">{{ tfa.enabled ? '● 已开启' : '◐ 未开启' }}</n-tag>
          </template>
          <template v-if="tfa && !tfa.enabled">
            <div v-if="!setup" class="muted" style="margin-bottom: 12px; line-height: 1.8">
              开启后，登录时除了密码还要输入手机身份验证器（Google Authenticator、Microsoft Authenticator、1Password 等）里的 6 位动态码。
            </div>
            <n-button v-if="!setup" type="primary" :loading="busy" @click="startSetup">开始设置</n-button>
            <div v-else class="setup">
              <n-qr-code :value="setup.uri" :size="168" :padding="8" background-color="#fff" color="#000" />
              <div class="setup-steps">
                <div>1. 用身份验证器扫描二维码，或手动输入密钥：</div>
                <n-input-group style="margin: 6px 0 14px">
                  <n-input :value="setup.secret" readonly class="mono" size="small" />
                  <n-button size="small" @click="copy(setup.secret)">复制</n-button>
                </n-input-group>
                <div>2. 输入验证器显示的 6 位数字：</div>
                <n-input-group style="margin-top: 6px">
                  <n-input v-model:value="code" placeholder="123456" maxlength="6" :input-props="{ inputmode: 'numeric' }" @keyup.enter="enable" />
                  <n-button type="primary" :loading="busy" @click="enable">开启</n-button>
                </n-input-group>
              </div>
            </div>
          </template>
          <template v-else-if="tfa">
            <div class="muted" style="margin-bottom: 12px; line-height: 1.8">
              关闭需要验证密码和当前动态码。如果手机丢失无法登录，可在服务器的 <span class="mono">/etc/mailsender.env</span> 中加入
              <span class="mono">MS_DISABLE_2FA=true</span> 后重启，登录后再删掉这一行。
            </div>
            <n-form label-placement="left" label-width="80" style="max-width: 360px">
              <n-form-item label="密码"><n-input v-model:value="off.password" type="password" show-password-on="click" /></n-form-item>
              <n-form-item label="动态码"><n-input v-model:value="off.code" maxlength="6" :input-props="{ inputmode: 'numeric' }" /></n-form-item>
              <n-button :loading="busy" @click="disable">关闭两步验证</n-button>
            </n-form>
          </template>
        </n-card>

        <n-card size="small" title="修改管理员密码" style="margin-top: 16px">
          <n-form label-placement="left" label-width="90" style="max-width: 400px">
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
      </n-gi>

      <n-gi>
        <n-card size="small" title="登录防爆破">
          <n-form v-if="s" label-placement="left" label-width="150">
            <n-form-item label="失败多少次封禁">
              <n-input-number v-model:value="s.auth_max_fails" :min="0" />
              <span class="hint">次 / 15 分钟，0 关闭</span>
            </n-form-item>
            <n-form-item label="封禁时长">
              <n-input-number v-model:value="s.auth_ban_minutes" :min="1" />
              <span class="hint">分钟</span>
            </n-form-item>
            <n-button type="primary" :loading="saving" @click="saveGuard">保存</n-button>
          </n-form>
          <div class="muted" style="margin-top: 12px; line-height: 1.8">
            同时作用于 SMTP 登录（API Key 错误）和后台登录（密码或动态码错误），两者分开计数。被封禁的 IP 连接 SMTP 时直接收到 554。封禁记录保存在内存中，服务重启后清空；本机地址（127.0.0.1）不会被封禁。
          </div>
        </n-card>

        <n-card size="small" title="当前封禁" style="margin-top: 16px">
          <template #header-extra><n-button size="tiny" quaternary @click="loadBans">刷新</n-button></template>
          <n-data-table size="small" :columns="banColumns" :data="bans" :row-key="(r) => r.scope + r.ip" />
        </n-card>
      </n-gi>
    </n-grid>
  </div>
</template>

<script setup>
import { h, onMounted, reactive, ref } from 'vue'
import { NButton, NCard, NDataTable, NForm, NFormItem, NGi, NGrid, NInput, NInputGroup, NInputNumber, NQrCode, NTag } from 'naive-ui'
import { api, setToken } from '../api'
import { copy, fmtTime, fromNow, message } from '../utils'

// ---- 2FA ----
const tfa = ref(null)
const setup = ref(null)
const code = ref('')
const busy = ref(false)
const off = reactive({ password: '', code: '' })

async function run(fn) {
  busy.value = true
  try {
    await fn()
  } catch (e) {
    message.error(e.message)
  } finally {
    busy.value = false
  }
}
const startSetup = () => run(async () => (setup.value = await api.post('/2fa/setup')))
const enable = () =>
  run(async () => {
    const r = await api.post('/2fa/enable', { code: code.value })
    setToken(r.token)
    setup.value = null
    code.value = ''
    tfa.value = { enabled: true }
    message.success('两步验证已开启，其他已登录的设备需要重新登录')
  })
const disable = () =>
  run(async () => {
    const r = await api.post('/2fa/disable', off)
    setToken(r.token)
    Object.assign(off, { password: '', code: '' })
    tfa.value = { enabled: false }
    message.success('两步验证已关闭')
  })

// ---- password ----
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

// ---- brute-force protection ----
const s = ref(null)
const saving = ref(false)
async function saveGuard() {
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

const bans = ref([])
async function loadBans() {
  bans.value = await api.get('/security/bans')
}
async function unban(b) {
  await api.post('/security/unban', { scope: b.scope, ip: b.ip })
  message.success('已解封 ' + b.ip)
  loadBans()
}
const banColumns = [
  { title: 'IP', key: 'ip', render: (b) => h('span', { class: 'mono' }, b.ip) },
  { title: '类型', key: 'scope', width: 90, render: (b) => (b.scope === 'smtp' ? 'SMTP' : '后台') },
  { title: '失败', key: 'fails', width: 60 },
  { title: '解封时间', key: 'until', width: 140, render: (b) => h('span', { title: fmtTime(b.until, true) }, fromNow(b.until)) },
  { title: '', key: 'a', width: 70, render: (b) => h(NButton, { size: 'tiny', secondary: true, onClick: () => unban(b) }, () => '解封') },
]

onMounted(async () => {
  try {
    ;[tfa.value, s.value] = await Promise.all([api.get('/2fa'), api.get('/settings')])
    await loadBans()
  } catch (e) {
    message.error(e.message)
  }
})
</script>

<style scoped>
.hint { margin-left: 10px; opacity: 0.6; font-size: 12px; white-space: nowrap; }
.setup { display: flex; gap: 20px; flex-wrap: wrap; align-items: flex-start; }
.setup-steps { flex: 1; min-width: 220px; font-size: 13px; }
</style>

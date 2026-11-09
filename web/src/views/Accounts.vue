<template>
  <div>
    <div class="page-head">
      <h2>SMTP 账号</h2>
      <n-space>
        <n-button :loading="checking" @click="checkAll">全部检测</n-button>
        <n-button type="primary" @click="openCreate">添加账号</n-button>
      </n-space>
    </div>

    <n-card size="small">
      <n-data-table :columns="columns" :data="rows" :loading="loading" :row-key="(r) => r.id" :scroll-x="1200" size="small" />
    </n-card>

    <!-- create / edit -->
    <n-modal v-model:show="showForm" preset="card" :title="form.id ? '编辑账号' : '添加账号'" style="width: 720px; max-width: calc(100vw - 32px)">
      <n-form label-placement="left" label-width="108" :model="form">
        <n-form-item v-if="!form.id" label="服务商预设">
          <n-select :options="presetOptions" placeholder="选择后自动填写服务器和端口" clearable @update:value="applyPreset" />
        </n-form-item>
        <n-alert v-if="presetHint" type="info" :show-icon="false" style="margin: -8px 0 16px">{{ presetHint }}</n-alert>
        <n-grid :cols="2" :x-gap="16" item-responsive responsive="self">
          <n-form-item-gi span="2 500:1" label="名称">
            <n-input v-model:value="form.name" placeholder="默认使用发件地址" />
          </n-form-item-gi>
          <n-form-item-gi span="2 500:1" label="启用">
            <n-switch v-model:value="form.enabled" />
          </n-form-item-gi>
          <n-form-item-gi span="2 500:1" label="SMTP 服务器" required>
            <n-input v-model:value="form.host" placeholder="smtp.qq.com" />
          </n-form-item-gi>
          <n-form-item-gi span="2 500:1" label="端口" required>
            <n-input-number v-model:value="form.port" :min="1" :max="65535" style="width: 100%" @update:value="onPort" />
          </n-form-item-gi>
          <n-form-item-gi span="2 500:1" label="加密方式">
            <n-select v-model:value="form.security" :options="securityOptions" />
          </n-form-item-gi>
          <n-form-item-gi span="2 500:1" label="认证方式">
            <n-select v-model:value="form.auth_type" :options="authOptions" />
          </n-form-item-gi>
          <n-form-item-gi span="2 500:1" label="发件地址" required>
            <n-input v-model:value="form.from_addr" placeholder="noreply@example.com" />
          </n-form-item-gi>
          <n-form-item-gi span="2 500:1" label="发件人名称">
            <n-input v-model:value="form.from_name" placeholder="例如：XX 平台" />
          </n-form-item-gi>
          <n-form-item-gi span="2 500:1" label="用户名">
            <n-input v-model:value="form.username" placeholder="默认同发件地址" />
          </n-form-item-gi>
          <n-form-item-gi span="2 500:1" label="密码/授权码">
            <n-input v-model:value="form.password" type="password" show-password-on="click" :placeholder="form.id && form.has_password ? '留空则不修改' : 'SMTP 密码或授权码'" />
          </n-form-item-gi>
        </n-grid>
        <n-divider title-placement="left" style="margin-top: 0">轮询与限额（0 表示不限）</n-divider>
        <n-grid :cols="4" :x-gap="12" item-responsive responsive="self">
          <n-form-item-gi span="2 500:1" label="权重" label-placement="top">
            <n-input-number v-model:value="form.weight" :min="1" :max="100" />
          </n-form-item-gi>
          <n-form-item-gi span="2 500:1" label="每分钟" label-placement="top">
            <n-input-number v-model:value="form.limit_per_minute" :min="0" />
          </n-form-item-gi>
          <n-form-item-gi span="2 500:1" label="每小时" label-placement="top">
            <n-input-number v-model:value="form.limit_per_hour" :min="0" />
          </n-form-item-gi>
          <n-form-item-gi span="2 500:1" label="每天" label-placement="top">
            <n-input-number v-model:value="form.limit_per_day" :min="0" />
          </n-form-item-gi>
        </n-grid>
        <n-divider title-placement="left" style="margin-top: 0">分组与路由</n-divider>
        <n-form-item label="分组">
          <n-select v-model:value="groupsArr" multiple filterable tag :options="groupOptions" placeholder="可选，输入后回车新建，例如 verify" />
        </n-form-item>
        <n-form-item label="优先收件域名">
          <div>
            <n-dynamic-tags v-model:value="domainsArr" />
            <div class="hint" style="margin: 6px 0 0">发往这些域名（含子域名）时优先用本账号，例如 qq.com、foxmail.com；失败后仍会换其他账号</div>
          </div>
        </n-form-item>
        <n-form-item label="备注">
          <n-input v-model:value="form.remark" type="textarea" :rows="2" />
        </n-form-item>
      </n-form>
      <template #footer>
        <n-space justify="end">
          <n-button @click="showForm = false">取消</n-button>
          <n-button type="primary" :loading="saving" @click="save">保存</n-button>
        </n-space>
      </template>
    </n-modal>

    <!-- test send -->
    <n-modal v-model:show="showTestSend" preset="dialog" title="发送测试邮件" positive-text="发送" negative-text="取消" :loading="testSending" @positive-click="doTestSend">
      <div class="muted" style="margin-bottom: 8px">通过「{{ testTarget?.name }}」直接发送，不经过轮询，不计入统计。</div>
      <n-input v-model:value="testTo" placeholder="收件人邮箱" />
    </n-modal>
  </div>
</template>

<script setup>
import { computed, h, onMounted, reactive, ref } from 'vue'
import {
  NButton, NCard, NDataTable, NModal, NForm, NFormItem, NFormItemGi, NGrid, NInput, NInputNumber, NSelect, NSwitch,
  NSpace, NTag, NDivider, NAlert, NPopconfirm, NDynamicTags,
} from 'naive-ui'
import { api } from '../api'
import { message, fromNow, reasonText } from '../utils'

const rows = ref([])
const loading = ref(false)
const checking = ref(false)

// limits are conservative starting points for a fresh mailbox; raise them gradually once it sends reliably
const presets = [
  { label: 'QQ 邮箱', host: 'smtp.qq.com', port: 465, security: 'ssl', limits: [5, 40, 100], hint: '需在「设置 → 账户」开启 SMTP 服务并使用授权码。' },
  { label: '163 邮箱', host: 'smtp.163.com', port: 465, security: 'ssl', limits: [3, 30, 100], hint: '需在设置中开启 SMTP 并使用授权码；新号发得太快容易被临时封禁。' },
  { label: '126 邮箱', host: 'smtp.126.com', port: 465, security: 'ssl', limits: [3, 30, 100], hint: '需在设置中开启 SMTP 并使用授权码。' },
  { label: 'Gmail', host: 'smtp.gmail.com', port: 465, security: 'ssl', limits: [10, 100, 400], hint: '需开启两步验证后使用「应用专用密码」，个人账号每天上限约 500 封。' },
  { label: '腾讯企业邮', host: 'smtp.exmail.qq.com', port: 465, security: 'ssl', limits: [10, 200, 800], hint: '' },
  { label: '网易企业邮', host: 'smtp.qiye.163.com', port: 465, security: 'ssl', limits: [10, 200, 800], hint: '' },
  { label: '阿里企业邮', host: 'smtp.qiye.aliyun.com', port: 465, security: 'ssl', limits: [10, 200, 800], hint: '' },
  { label: '阿里云邮件推送', host: 'smtpdm.aliyun.com', port: 465, security: 'ssl', limits: [0, 0, 0], hint: '用户名为控制台配置的发信地址，密码为 SMTP 密码；限额以控制台配额为准。' },
  { label: 'Amazon SES (us-east-1)', host: 'email-smtp.us-east-1.amazonaws.com', port: 587, security: 'starttls', limits: [0, 0, 0], hint: '用户名/密码为 SES 的 SMTP 凭证，不是 IAM 密钥；其他区域请修改服务器地址；限额以 SES 配额为准。' },
]
const presetOptions = presets.map((p, i) => ({ label: p.label, value: i }))
const presetHint = ref('')
const securityOptions = [
  { label: 'SSL/TLS（465）', value: 'ssl' },
  { label: 'STARTTLS（587）', value: 'starttls' },
  { label: '不加密（25，仅内网）', value: 'none' },
]
const authOptions = [
  { label: '自动', value: 'auto' },
  { label: 'PLAIN', value: 'plain' },
  { label: 'LOGIN', value: 'login' },
  { label: 'CRAM-MD5', value: 'cram-md5' },
  { label: '无需认证', value: 'none' },
]

const empty = () => ({
  id: 0, name: '', host: '', port: 465, security: 'ssl', auth_type: 'auto', username: '', password: '', from_addr: '', from_name: '',
  weight: 1, limit_per_minute: 0, limit_per_hour: 0, limit_per_day: 0, enabled: true, remark: '', has_password: false,
  groups: '', prefer_domains: '',
})
const list = (v) => (v ? v.split(',').filter(Boolean) : [])
const groupsArr = computed({ get: () => list(form.groups), set: (v) => (form.groups = v.join(',')) })
const domainsArr = computed({ get: () => list(form.prefer_domains), set: (v) => (form.prefer_domains = v.join(',')) })
const groupOptions = computed(() => [...new Set(rows.value.flatMap((a) => list(a.groups)))].sort().map((g) => ({ label: g, value: g })))
const form = reactive(empty())
const showForm = ref(false)
const saving = ref(false)

function applyPreset(i) {
  if (i == null) return (presetHint.value = '')
  const p = presets[i]
  const [m, hr, d] = p.limits
  Object.assign(form, { host: p.host, port: p.port, security: p.security, limit_per_minute: m, limit_per_hour: hr, limit_per_day: d })
  presetHint.value = [p.hint, m ? `已填入建议限额：每分钟 ${m}、每小时 ${hr}、每天 ${d}，稳定后可逐步调高。` : ''].filter(Boolean).join(' ')
}
function onPort(p) {
  if (p === 465) form.security = 'ssl'
  else if (p === 587) form.security = 'starttls'
}
function openCreate() {
  Object.assign(form, empty())
  presetHint.value = ''
  showForm.value = true
}
function openEdit(a) {
  Object.assign(form, empty(), a, { password: '' })
  presetHint.value = ''
  showForm.value = true
}
async function save() {
  saving.value = true
  try {
    const body = { ...form }
    if (form.id) await api.put(`/accounts/${form.id}`, body)
    else await api.post('/accounts', body)
    message.success('已保存')
    showForm.value = false
    load()
  } catch (e) {
    message.error(e.message)
  } finally {
    saving.value = false
  }
}

async function toggle(a, v) {
  try {
    await api.put(`/accounts/${a.id}`, { ...a, enabled: v, password: '' })
    load()
  } catch (e) {
    message.error(e.message)
  }
}

const testingId = ref(0)
async function test(a) {
  testingId.value = a.id
  try {
    const r = await api.post(`/accounts/${a.id}/test`)
    r.ok ? message.success(`连接成功（${r.duration_ms} ms）`) : message.error('连接失败：' + r.error, { duration: 8000 })
    load()
  } catch (e) {
    message.error(e.message)
  } finally {
    testingId.value = 0
  }
}

const showTestSend = ref(false)
const testTarget = ref(null)
const testTo = ref('')
const testSending = ref(false)
function openTestSend(a) {
  testTarget.value = a
  showTestSend.value = true
}
async function doTestSend() {
  if (!testTo.value) {
    message.warning('请输入收件人')
    return false
  }
  testSending.value = true
  try {
    const r = await api.post(`/accounts/${testTarget.value.id}/test-send`, { to: testTo.value })
    r.ok ? message.success(`已发送（${r.duration_ms} ms）`) : message.error('发送失败：' + r.error, { duration: 8000 })
    return r.ok
  } catch (e) {
    message.error(e.message)
    return false
  } finally {
    testSending.value = false
  }
}

async function reset(a) {
  await api.post(`/accounts/${a.id}/reset`)
  message.success('已解除冷却')
  load()
}
async function remove(a) {
  try {
    await api.del(`/accounts/${a.id}`)
    message.success('已删除')
    load()
  } catch (e) {
    message.error(e.message)
  }
}
async function checkAll() {
  checking.value = true
  try {
    rows.value = await api.post('/accounts/check-all')
    const bad = rows.value.filter((a) => a.enabled && !a.last_check_ok).length
    bad ? message.warning(`${bad} 个账号检测失败`) : message.success('全部检测通过')
  } catch (e) {
    message.error(e.message)
  } finally {
    checking.value = false
  }
}

const usage = (used, limit) => h('span', { class: 'tabular' }, limit ? `${used}/${limit}` : `${used}`)

const columns = [
  {
    title: '账号',
    key: 'name',
    width: 220,
    fixed: 'left',
    render: (a) =>
      h('div', [
        h('div', { style: 'font-weight:500' }, a.name),
        h('div', { class: 'muted' }, `${a.from_name ? a.from_name + ' · ' : ''}${a.from_addr}`),
        a.groups || a.prefer_domains
          ? h(NSpace, { size: 4, style: 'margin-top:4px' }, () => [
              ...list(a.groups).map((g) => h(NTag, { size: 'tiny', bordered: false, type: 'info' }, () => g)),
              ...list(a.prefer_domains).map((d) => h(NTag, { size: 'tiny', bordered: false }, () => '→ ' + d)),
            ])
          : null,
      ]),
  },
  { title: '服务器', key: 'host', width: 200, render: (a) => h('div', [h('div', { class: 'mono', style: 'font-size:12px' }, `${a.host}:${a.port}`), h('div', { class: 'muted' }, a.security.toUpperCase())]) },
  {
    title: '状态',
    key: 'status',
    width: 150,
    render: (a) => {
      const r = a.runtime
      let tag
      if (r.available) tag = h(NTag, { size: 'small', type: 'success', bordered: false }, () => '● 可用')
      else {
        const type = r.reason === 'disabled' ? 'default' : r.reason === 'cooldown' || r.reason.startsWith('password') ? 'error' : 'warning'
        tag = h(NTag, { size: 'small', type, bordered: false }, () => (type === 'error' ? '✕ ' : '◐ ') + (reasonText[r.reason] || r.reason))
      }
      const sub = r.reason === 'cooldown' && a.cooldown_until ? `${fromNow(a.cooldown_until)}恢复` : a.consecutive_fails ? `连续失败 ${a.consecutive_fails}` : ''
      return h('div', [tag, sub ? h('div', { class: 'muted', style: 'margin-top:2px' }, sub) : null])
    },
  },
  { title: '分钟', key: 'm', width: 70, render: (a) => usage(a.runtime.minute_count, a.limit_per_minute) },
  { title: '小时', key: 'hr', width: 80, render: (a) => usage(a.runtime.hour_count, a.limit_per_hour) },
  { title: '今日', key: 'd', width: 90, render: (a) => usage(a.runtime.day_count, a.limit_per_day) },
  { title: '权重', key: 'weight', width: 60 },
  {
    title: '最近检测',
    key: 'check',
    width: 110,
    render: (a) => (a.last_check_at ? h('span', { style: a.last_check_ok ? '' : 'color:#d03b3b' }, (a.last_check_ok ? '✓ ' : '✕ ') + fromNow(a.last_check_at)) : h('span', { class: 'muted' }, '未检测')),
  },
  {
    title: '最近错误',
    key: 'last_error',
    minWidth: 160,
    ellipsis: { tooltip: { style: { maxWidth: '480px' } } },
    render: (a) => a.last_error || h('span', { class: 'muted' }, '-'),
  },
  { title: '启用', key: 'enabled', width: 70, render: (a) => h(NSwitch, { size: 'small', value: a.enabled, 'onUpdate:value': (v) => toggle(a, v) }) },
  {
    title: '操作',
    key: 'actions',
    width: 280,
    fixed: 'right',
    render: (a) =>
      h(NSpace, { size: 4, wrap: false }, () => [
        h(NButton, { size: 'tiny', loading: testingId.value === a.id, onClick: () => test(a) }, () => '测试连接'),
        h(NButton, { size: 'tiny', onClick: () => openTestSend(a) }, () => '发测试信'),
        a.cooldown_until || a.consecutive_fails ? h(NButton, { size: 'tiny', type: 'warning', secondary: true, onClick: () => reset(a) }, () => '解除冷却') : null,
        h(NButton, { size: 'tiny', onClick: () => openEdit(a) }, () => '编辑'),
        h(NPopconfirm, { onPositiveClick: () => remove(a) }, { trigger: () => h(NButton, { size: 'tiny', type: 'error', secondary: true }, () => '删除'), default: () => `删除账号「${a.name}」？` }),
      ]),
  },
]

async function load() {
  loading.value = !rows.value.length
  try {
    rows.value = await api.get('/accounts')
  } catch (e) {
    message.error(e.message)
  } finally {
    loading.value = false
  }
}
onMounted(load)
</script>

<style scoped>
.hint { margin-left: 10px; opacity: 0.6; font-size: 12px; }
</style>

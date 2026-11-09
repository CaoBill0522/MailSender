<template>
  <div>
    <div class="page-head">
      <h2>出口代理</h2>
      <n-space>
        <n-button :loading="checkingAll" :disabled="!rows.length" @click="checkAll">检测全部</n-button>
        <n-button type="primary" @click="openCreate">添加代理</n-button>
      </n-space>
    </div>

    <n-alert type="info" :show-icon="false" style="margin-bottom: 12px; line-height: 1.8">
      给 SMTP 账号指定 SOCKS5 代理后，这个账号登录邮箱服务器时用的是代理的 IP，不再和其他账号共用服务器的 IP。
      在「SMTP 账号」里编辑账号，选择「出口」即可。
      <div style="margin-top: 4px">
        服务器本机出口 IP：
        <span v-if="direct?.ok" class="mono">{{ direct.ip }}</span>
        <span v-else-if="direct" class="err">{{ direct.error }}</span>
        <span v-else class="muted">未检测</span>
        <n-button size="tiny" quaternary :loading="directLoading" style="margin-left: 6px" @click="checkDirect">检测</n-button>
      </div>
    </n-alert>

    <n-collapse style="margin-bottom: 12px">
      <n-collapse-item title="不花钱搭出口的几种方法" name="guide">
        <div class="guide">
          <p><b>1. 家里或公司的电脑、树莓派、OpenWrt 路由器（推荐，国内家庭宽带 IP）</b></p>
          <p>在那台设备上运行下面的命令，服务器上就会出现一个从那台设备出网的 SOCKS5 代理。设备在内网也可以，不用做端口映射：</p>
          <pre class="mono">ssh -N -o ServerAliveInterval=30 -o ExitOnForwardFailure=yes -R 127.0.0.1:1081 root@{{ host }}</pre>
          <p>然后在这里添加代理：主机 <span class="mono">127.0.0.1</span>，端口 <span class="mono">1081</span>，不填用户名密码。需要 OpenSSH 7.6 或更新版本；用 autossh 或 systemd 让它断线后自动重连。</p>
          <p><b>2. 免费云主机（Oracle Cloud 永久免费、各家云的免费试用）</b></p>
          <p>在<b>这台服务器</b>上运行，把出口放到那台云主机上，云主机只需要能 SSH 登录，不用装任何东西：</p>
          <pre class="mono">ssh -N -o ServerAliveInterval=30 -o ExitOnForwardFailure=yes -D 127.0.0.1:1082 user@云主机IP</pre>
          <p>然后添加代理：主机 <span class="mono">127.0.0.1</span>，端口 <span class="mono">1082</span>。</p>
          <p><b>3. 已有的 SOCKS5 服务</b>（比如自己在 VPS 上跑的 Dante、3proxy、gost）：直接填它的地址和用户名密码。不建议用网上公开的免费代理，它们的 IP 基本都在黑名单里，用了反而更容易封号。</p>
          <p class="muted">
            建议同一个出口上每家服务商（QQ、163、Gmail 等）只放 1～2 个账号。不同服务商之间不共享数据，共用一个 IP 没有关系。SSH 隧道只监听 127.0.0.1，外网访问不到，所以可以不设密码。
          </p>
        </div>
      </n-collapse-item>
    </n-collapse>

    <n-card size="small">
      <n-data-table :columns="columns" :data="rows" :loading="loading" :row-key="(r) => r.id" :scroll-x="900" size="small" />
    </n-card>

    <n-modal v-model:show="showForm" preset="card" :title="form.id ? '编辑代理' : '添加代理'" style="width: 520px; max-width: calc(100vw - 32px)">
      <n-form label-placement="left" label-width="90">
        <n-form-item label="快速填写">
          <n-input v-model:value="quick" placeholder="socks5://用户名:密码@主机:端口" @blur="parseQuick" @keyup.enter="parseQuick" />
        </n-form-item>
        <n-form-item label="名称">
          <n-input v-model:value="form.name" placeholder="例如：家里宽带、Oracle 东京" />
        </n-form-item>
        <n-form-item label="主机" required>
          <n-input v-model:value="form.host" placeholder="127.0.0.1" />
        </n-form-item>
        <n-form-item label="端口" required>
          <n-input-number v-model:value="form.port" :min="1" :max="65535" :show-button="false" style="width: 140px" />
        </n-form-item>
        <n-form-item label="用户名">
          <n-input v-model:value="form.username" placeholder="可选" />
        </n-form-item>
        <n-form-item label="密码">
          <div style="width: 100%">
            <n-input v-model:value="form.password" type="password" show-password-on="click" :placeholder="form.id && form.has_password ? '留空保持不变' : '可选'" :disabled="form.clear_password" />
            <n-checkbox v-if="form.id && form.has_password" v-model:checked="form.clear_password" style="margin-top: 6px">清除已保存的密码</n-checkbox>
          </div>
        </n-form-item>
        <n-form-item label="备注">
          <n-input v-model:value="form.remark" type="textarea" :rows="2" />
        </n-form-item>
      </n-form>
      <template #footer>
        <n-space justify="end">
          <n-button @click="showForm = false">取消</n-button>
          <n-button type="primary" :loading="saving" @click="save">保存并检测</n-button>
        </n-space>
      </template>
    </n-modal>
  </div>
</template>

<script setup>
import { computed, h, onMounted, reactive, ref } from 'vue'
import { NAlert, NButton, NCard, NCheckbox, NCollapse, NCollapseItem, NDataTable, NForm, NFormItem, NInput, NInputNumber, NModal, NPopconfirm, NSpace, NTag, NTooltip } from 'naive-ui'
import { api } from '../api'
import { fmtTime, fromNow, message } from '../utils'

const host = window.location.hostname
const rows = ref([])
const loading = ref(false)
const checking = reactive({})
const checkingAll = ref(false)

async function load() {
  loading.value = !rows.value.length
  try {
    rows.value = await api.get('/proxies')
  } catch (e) {
    message.error(e.message)
  } finally {
    loading.value = false
  }
}

// ---- server's own IP, to compare with ----
const direct = ref(null)
const directLoading = ref(false)
async function checkDirect() {
  directLoading.value = true
  try {
    direct.value = await api.post('/proxies/direct-ip')
  } catch (e) {
    message.error(e.message)
  } finally {
    directLoading.value = false
  }
}

async function check(p, quiet = false) {
  checking[p.id] = true
  try {
    const r = await api.post(`/proxies/${p.id}/check`)
    if (!quiet) {
      if (!r.ok) message.error(`「${p.name}」不可用：${r.error}`)
      else if (direct.value?.ok && r.ip === direct.value.ip) message.warning(`「${p.name}」的出口 IP 和服务器本机相同，没有起到分开 IP 的作用`)
      else message.success(`「${p.name}」可用，出口 IP ${r.ip}（${r.latency_ms} ms）`)
    }
    return r
  } catch (e) {
    message.error(e.message)
  } finally {
    checking[p.id] = false
  }
}
async function checkAll() {
  checkingAll.value = true
  try {
    const rs = await Promise.all(rows.value.map((p) => check(p, true)))
    const bad = rs.filter((r) => r && !r.ok).length
    bad ? message.warning(`${bad} 个代理不可用`) : message.success('全部可用')
    await load()
  } finally {
    checkingAll.value = false
  }
}

// ---- form ----
const empty = () => ({ id: 0, name: '', host: '127.0.0.1', port: 1081, username: '', password: '', remark: '', has_password: false, clear_password: false })
const form = reactive(empty())
const quick = ref('')
const showForm = ref(false)
const saving = ref(false)
function openCreate() {
  Object.assign(form, empty())
  quick.value = ''
  showForm.value = true
}
function openEdit(p) {
  Object.assign(form, empty(), p, { password: '' })
  quick.value = ''
  showForm.value = true
}
function parseQuick() {
  let v = quick.value.trim()
  if (!v) return
  if (!v.includes('://')) v = 'socks5://' + v
  try {
    const u = new URL(v.replace(/^socks5h?:/, 'http:'))
    if (!u.hostname || !u.port) throw new Error()
    Object.assign(form, { host: u.hostname.replace(/^\[|\]$/g, ''), port: Number(u.port), username: decodeURIComponent(u.username), password: decodeURIComponent(u.password) })
    if (!form.name) form.name = `${form.host}:${form.port}`
  } catch {
    message.error('格式应为 socks5://用户名:密码@主机:端口')
  }
}
async function save() {
  saving.value = true
  try {
    const body = { ...form }
    const p = form.id ? await api.put(`/proxies/${form.id}`, body) : await api.post('/proxies', body)
    showForm.value = false
    await check(p)
    load()
  } catch (e) {
    message.error(e.message)
  } finally {
    saving.value = false
  }
}
async function remove(p) {
  try {
    await api.del(`/proxies/${p.id}`)
    message.success('已删除')
    load()
  } catch (e) {
    message.error(e.message)
  }
}

// the same egress IP behind two entries means they don't actually separate anything
const dupIPs = computed(() => {
  const seen = {}
  for (const p of rows.value) if (p.last_check_ok && p.last_ip) seen[p.last_ip] = (seen[p.last_ip] || 0) + 1
  if (direct.value?.ok) seen[direct.value.ip] = (seen[direct.value.ip] || 0) + 1
  return seen
})

const columns = [
  {
    title: '名称',
    key: 'name',
    width: 180,
    render: (p) => h('div', [h('div', { style: 'font-weight:500' }, p.name), p.remark ? h('div', { class: 'muted' }, p.remark) : null]),
  },
  {
    title: '代理地址',
    key: 'host',
    width: 190,
    render: (p) => h('div', [h('div', { class: 'mono', style: 'font-size:12px' }, `${p.host}:${p.port}`), h('div', { class: 'muted' }, p.username ? `SOCKS5 · 用户 ${p.username}` : 'SOCKS5 · 无认证')]),
  },
  {
    title: '出口 IP',
    key: 'last_ip',
    width: 230,
    render: (p) => {
      if (!p.last_check_at) return h('span', { class: 'muted' }, '未检测')
      const tag = p.last_check_ok
        ? h(NTag, { size: 'small', type: 'success', bordered: false }, () => '● 可用')
        : h(NTooltip, { style: { maxWidth: '420px' } }, { trigger: () => h(NTag, { size: 'small', type: 'error', bordered: false }, () => '✕ 不可用'), default: () => p.last_error })
      const ip = p.last_ip ? h('span', { class: 'mono', style: 'margin-left:6px' }, p.last_ip) : null
      const dup = p.last_check_ok && dupIPs.value[p.last_ip] > 1 ? h('div', { class: 'warn' }, '和其他出口 IP 相同') : null
      return h('div', [
        h('div', [tag, ip]),
        dup,
        h('div', { class: 'muted', title: fmtTime(p.last_check_at, true) }, `${fromNow(p.last_check_at)}${p.last_check_ok ? ` · ${p.last_latency_ms} ms` : ''}${!p.last_check_ok && p.last_ip ? ' · 上次 IP' : ''}`),
      ])
    },
  },
  {
    title: '使用账号',
    key: 'accounts',
    minWidth: 160,
    render: (p) =>
      p.accounts.length
        ? h(NSpace, { size: 4 }, () => p.accounts.map((n) => h(NTag, { size: 'tiny', bordered: false }, () => n)))
        : h('span', { class: 'muted' }, '未使用'),
  },
  {
    title: '操作',
    key: 'actions',
    width: 170,
    render: (p) =>
      h(NSpace, { size: 6 }, () => [
        h(NButton, { size: 'tiny', loading: !!checking[p.id], onClick: () => check(p).then(load) }, () => '检测'),
        h(NButton, { size: 'tiny', onClick: () => openEdit(p) }, () => '编辑'),
        h(
          NPopconfirm,
          { onPositiveClick: () => remove(p) },
          { trigger: () => h(NButton, { size: 'tiny', type: 'error', secondary: true }, () => '删除'), default: () => `删除代理「${p.name}」？` },
        ),
      ]),
  },
]

onMounted(() => {
  load()
  checkDirect()
})
</script>

<style scoped>
.guide { font-size: 13px; line-height: 1.8; }
.guide p { margin: 0 0 6px; }
.guide pre { margin: 4px 0 10px; padding: 8px 10px; border-radius: 6px; background: rgba(128, 128, 128, 0.12); white-space: pre-wrap; word-break: break-all; font-size: 12px; }
.err { color: var(--n-error-color, #d03050); }
.warn { color: #d08000; font-size: 12px; }
</style>

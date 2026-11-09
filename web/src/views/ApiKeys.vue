<template>
  <div>
    <div class="page-head">
      <h2>API Key</h2>
      <n-button type="primary" @click="openCreate">创建 API Key</n-button>
    </div>
    <n-card size="small">
      <n-data-table :columns="columns" :data="rows" :row-key="(r) => r.id" size="small" :scroll-x="1300" />
    </n-card>

    <n-modal v-model:show="showForm" preset="card" :title="form.id ? '编辑 API Key' : '创建 API Key'" style="width: 600px; max-width: calc(100vw - 32px)">
      <n-form label-placement="left" label-width="110">
        <n-form-item label="名称" required>
          <n-input v-model:value="form.name" placeholder="使用方名称，例如 user-service" />
        </n-form-item>
        <n-form-item v-if="form.id" label="启用">
          <n-switch v-model:value="form.enabled" />
        </n-form-item>
        <n-form-item label="每分钟邮件上限">
          <n-input-number v-model:value="form.rate_limit_per_min" :min="0" placeholder="0 表示不限" style="width: 100%" />
        </n-form-item>
        <n-form-item label="IP 白名单">
          <n-input v-model:value="form.ip_whitelist" type="textarea" :rows="2" placeholder="逗号或换行分隔，支持 CIDR，例如 10.0.0.0/8。留空表示不限制" />
        </n-form-item>
        <n-form-item label="可用账号分组">
          <n-select v-model:value="groupsArr" multiple :options="groupOptions" placeholder="留空表示可用全部账号" />
        </n-form-item>
        <n-form-item label="投递回调地址">
          <n-input v-model:value="form.callback_url" placeholder="可选，每封信发送成功或失败后 POST 到这个地址" />
        </n-form-item>
        <n-form-item v-if="form.id && form.callback_url" label="签名密钥">
          <n-input-group>
            <n-input :value="form.callback_secret" readonly class="mono" />
            <n-button @click="copy(form.callback_secret)">复制</n-button>
            <n-popconfirm @positive-click="rotateSecret">
              <template #trigger><n-button>重新生成</n-button></template>
              重新生成后旧密钥立即失效，接收端需要同步更新。
            </n-popconfirm>
          </n-input-group>
        </n-form-item>
        <n-form-item label="备注">
          <n-input v-model:value="form.remark" />
        </n-form-item>
      </n-form>
      <div v-if="form.callback_url" class="muted" style="margin-top: -8px">
        回调请求头 <span class="mono">X-MailSender-Signature: sha256=HMAC(密钥, 时间戳 + "." + 请求体)</span>，签名算法和请求体格式见「接入文档」。<template v-if="!form.id">密钥在创建后可于编辑页查看。</template>
      </div>
      <template #footer>
        <n-space justify="end">
          <n-button @click="showForm = false">取消</n-button>
          <n-button type="primary" :loading="saving" @click="save">保存</n-button>
        </n-space>
      </template>
    </n-modal>

    <n-modal v-model:show="showKey" preset="card" title="API Key 已创建" style="width: 560px; max-width: calc(100vw - 32px)" :mask-closable="false">
      <n-alert type="warning" :show-icon="false" style="margin-bottom: 12px">Key 只显示这一次，关闭后无法再查看，请立即复制保存。在程序的 SMTP 设置里，用户名随意填写，密码填这个 Key。</n-alert>
      <n-input-group>
        <n-input :value="newKey" readonly class="mono" />
        <n-button type="primary" @click="copy(newKey)">复制</n-button>
      </n-input-group>
      <template #footer>
        <n-space justify="end"><n-button @click="showKey = false">我已保存</n-button></n-space>
      </template>
    </n-modal>
  </div>
</template>

<script setup>
import { computed, h, onMounted, reactive, ref } from 'vue'
import { NButton, NCard, NDataTable, NModal, NForm, NFormItem, NInput, NInputNumber, NInputGroup, NSelect, NSwitch, NSpace, NTag, NAlert, NPopconfirm } from 'naive-ui'
import { api } from '../api'
import { message, copy, fmtTime, fromNow } from '../utils'

const rows = ref([])
const empty = () => ({ id: 0, name: '', enabled: true, rate_limit_per_min: 60, ip_whitelist: '', groups: '', callback_url: '', callback_secret: '', remark: '' })
const list = (v) => (v ? v.split(',').filter(Boolean) : [])
const groups = ref([])
const groupsArr = computed({ get: () => list(form.groups), set: (v) => (form.groups = v.join(',')) })
const groupOptions = computed(() => {
  const opts = groups.value.map((g) => ({ label: `${g.name}（${g.accounts} 个账号）`, value: g.name }))
  for (const g of groupsArr.value) if (!groups.value.some((x) => x.name === g)) opts.push({ label: `${g}（无账号）`, value: g })
  return opts
})
const form = reactive(empty())
const showForm = ref(false)
const saving = ref(false)
const showKey = ref(false)
const newKey = ref('')

function openCreate() {
  Object.assign(form, empty())
  showForm.value = true
}
function openEdit(k) {
  Object.assign(form, empty(), k)
  showForm.value = true
}
async function save() {
  saving.value = true
  try {
    if (form.id) {
      await api.put(`/keys/${form.id}`, { ...form })
      message.success('已保存')
    } else {
      const r = await api.post('/keys', { ...form })
      newKey.value = r.key
      showKey.value = true
    }
    showForm.value = false
    load()
  } catch (e) {
    message.error(e.message)
  } finally {
    saving.value = false
  }
}
async function rotateSecret() {
  try {
    form.callback_secret = (await api.post(`/keys/${form.id}/callback-secret`)).callback_secret
    message.success('已重新生成')
    load()
  } catch (e) {
    message.error(e.message)
  }
}
const testingId = ref(0)
async function testCallback(k) {
  testingId.value = k.id
  try {
    const r = await api.post(`/keys/${k.id}/callback-test`)
    r.ok ? message.success(`回调成功（${r.duration_ms} ms）`) : message.error('回调失败：' + r.error, { duration: 8000 })
  } catch (e) {
    message.error(e.message)
  } finally {
    testingId.value = 0
  }
}
async function toggle(k, v) {
  await api.put(`/keys/${k.id}`, { ...k, enabled: v })
  load()
}
async function remove(k) {
  await api.del(`/keys/${k.id}`)
  message.success('已删除')
  load()
}

const columns = [
  { title: '名称', key: 'name', width: 180, render: (k) => h('div', [h('div', { style: 'font-weight:500' }, k.name), k.remark ? h('div', { class: 'muted' }, k.remark) : null]) },
  { title: 'Key', key: 'prefix', width: 170, render: (k) => h('span', { class: 'mono' }, k.prefix + '••••••') },
  { title: '频率上限', key: 'rate_limit_per_min', width: 110, render: (k) => (k.rate_limit_per_min ? `${k.rate_limit_per_min} 封/分钟` : '不限') },
  { title: 'IP 白名单', key: 'ip_whitelist', width: 150, ellipsis: { tooltip: true }, render: (k) => k.ip_whitelist || h('span', { class: 'muted' }, '不限') },
  {
    title: '账号分组',
    key: 'groups',
    width: 140,
    render: (k) => (k.groups ? h(NSpace, { size: 4 }, () => list(k.groups).map((g) => h(NTag, { size: 'tiny', bordered: false, type: 'info' }, () => g))) : h('span', { class: 'muted' }, '全部')),
  },
  { title: '回调', key: 'callback_url', minWidth: 160, ellipsis: { tooltip: true }, render: (k) => k.callback_url || h('span', { class: 'muted' }, '未设置') },
  { title: '最近使用', key: 'last_used_at', width: 110, render: (k) => fromNow(k.last_used_at) },
  { title: '创建时间', key: 'created_at', width: 140, render: (k) => fmtTime(k.created_at) },
  { title: '启用', key: 'enabled', width: 70, render: (k) => h(NSwitch, { size: 'small', value: k.enabled, 'onUpdate:value': (v) => toggle(k, v) }) },
  {
    title: '操作',
    key: 'actions',
    width: 190,
    fixed: 'right',
    render: (k) =>
      h(NSpace, { size: 4, wrap: false }, () => [
        k.callback_url ? h(NButton, { size: 'tiny', loading: testingId.value === k.id, onClick: () => testCallback(k) }, () => '测试回调') : null,
        h(NButton, { size: 'tiny', onClick: () => openEdit(k) }, () => '编辑'),
        h(NPopconfirm, { onPositiveClick: () => remove(k) }, { trigger: () => h(NButton, { size: 'tiny', type: 'error', secondary: true }, () => '删除'), default: () => `删除「${k.name}」？使用它的程序将无法发信。` }),
      ]),
  },
]

async function load() {
  ;[rows.value, groups.value] = await Promise.all([api.get('/keys'), api.get('/groups')])
}
onMounted(load)
</script>

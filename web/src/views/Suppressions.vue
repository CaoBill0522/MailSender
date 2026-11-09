<template>
  <div>
    <div class="page-head">
      <h2>退信名单</h2>
      <n-button type="primary" @click="showAdd = true">手动添加</n-button>
    </div>
    <n-alert type="info" :show-icon="false" style="margin-bottom: 12px">
      收件服务器明确拒收（地址不存在等 550 类错误）的地址会自动加入名单。之后发往这些地址的邮件在 SMTP 的 RCPT 阶段就直接返回 550，不再消耗发件账号的信誉。
      <template v-if="settings">
        当前：{{ settings.suppress_bounces ? `自动加入，保留 ${settings.suppress_days ? settings.suppress_days + ' 天' : '永久'}` : '自动加入已关闭' }}（在「系统设置」中修改）。
      </template>
    </n-alert>
    <n-card size="small">
      <n-space style="margin-bottom: 12px">
        <n-input v-model:value="q" placeholder="搜索地址" clearable style="width: 240px" @keyup.enter="search" @clear="search" />
        <n-select v-model:value="source" :options="sourceOptions" placeholder="来源" clearable style="width: 120px" @update:value="search" />
        <n-button @click="search">查询</n-button>
      </n-space>
      <n-data-table
        remote
        size="small"
        :columns="columns"
        :data="rows"
        :loading="loading"
        :pagination="pagination"
        :row-key="(r) => r.id"
        :scroll-x="900"
        @update:page="(p) => ((pagination.page = p), load())"
        @update:page-size="(s) => ((pagination.pageSize = s), (pagination.page = 1), load())"
      />
    </n-card>

    <n-modal v-model:show="showAdd" preset="card" title="添加到退信名单" style="width: 520px; max-width: calc(100vw - 32px)">
      <n-form label-placement="top">
        <n-form-item label="邮箱地址">
          <n-input v-model:value="add.addrs" type="textarea" :rows="4" placeholder="每行一个，或用逗号分隔" />
        </n-form-item>
        <n-form-item label="原因">
          <n-input v-model:value="add.reason" placeholder="例如：用户投诉、要求退订" />
        </n-form-item>
        <n-form-item label="有效期">
          <n-input-number v-model:value="add.days" :min="0" style="width: 160px" />
          <span class="hint">天，0 表示永久</span>
        </n-form-item>
      </n-form>
      <template #footer>
        <n-space justify="end">
          <n-button @click="showAdd = false">取消</n-button>
          <n-button type="primary" :loading="adding" @click="doAdd">添加</n-button>
        </n-space>
      </template>
    </n-modal>
  </div>
</template>

<script setup>
import { h, onMounted, reactive, ref } from 'vue'
import { NAlert, NButton, NCard, NDataTable, NForm, NFormItem, NInput, NInputNumber, NModal, NPopconfirm, NSelect, NSpace, NTag } from 'naive-ui'
import { api } from '../api'
import { fmtTime, message } from '../utils'

const rows = ref([])
const loading = ref(false)
const q = ref('')
const source = ref(null)
const settings = ref(null)
const sourceOptions = [
  { label: '自动退信', value: 'bounce' },
  { label: '手动添加', value: 'manual' },
]
const pagination = reactive({ page: 1, pageSize: 20, itemCount: 0, showSizePicker: true, pageSizes: [20, 50, 100], prefix: ({ itemCount }) => `共 ${itemCount} 条` })

async function load() {
  loading.value = true
  try {
    const p = new URLSearchParams({ page: pagination.page, size: pagination.pageSize })
    if (q.value) p.set('q', q.value)
    if (source.value) p.set('source', source.value)
    const r = await api.get('/suppressions?' + p)
    rows.value = r.items
    pagination.itemCount = r.total
  } catch (e) {
    message.error(e.message)
  } finally {
    loading.value = false
  }
}
function search() {
  pagination.page = 1
  load()
}

async function remove(r) {
  try {
    await api.del(`/suppressions/${r.id}`)
    message.success('已移除，可以再次给这个地址发信')
    load()
  } catch (e) {
    message.error(e.message)
  }
}

const showAdd = ref(false)
const adding = ref(false)
const add = reactive({ addrs: '', reason: '', days: 0 })
async function doAdd() {
  adding.value = true
  try {
    const r = await api.post('/suppressions', add)
    message.success(`已添加 ${r.added} 个地址`)
    Object.assign(add, { addrs: '', reason: '', days: 0 })
    showAdd.value = false
    search()
  } catch (e) {
    message.error(e.message)
  } finally {
    adding.value = false
  }
}

const columns = [
  { title: '地址', key: 'addr', width: 240, render: (r) => h('span', { class: 'mono' }, r.addr) },
  {
    title: '来源',
    key: 'source',
    width: 100,
    render: (r) => h(NTag, { size: 'small', bordered: false, type: r.source === 'bounce' ? 'warning' : 'default' }, () => (r.source === 'bounce' ? '自动退信' : '手动添加')),
  },
  { title: '原因', key: 'reason', minWidth: 220, ellipsis: { tooltip: { style: { maxWidth: '480px' } } } },
  { title: '已拦截', key: 'hits', width: 80, render: (r) => h('span', { class: 'tabular' }, r.hits) },
  { title: '加入时间', key: 'created_at', width: 130, render: (r) => fmtTime(r.created_at) },
  { title: '到期', key: 'expires_at', width: 110, render: (r) => (r.expires_at ? fmtTime(r.expires_at).slice(0, 5) : '永久') },
  {
    title: '操作',
    key: 'actions',
    width: 80,
    render: (r) =>
      h(NPopconfirm, { onPositiveClick: () => remove(r) }, { trigger: () => h(NButton, { size: 'tiny', secondary: true }, () => '移除'), default: () => `移除 ${r.addr}？之后可以再次给它发信。` }),
  },
]

onMounted(async () => {
  load()
  try {
    settings.value = await api.get('/settings')
  } catch {}
})
</script>

<style scoped>
.hint { margin-left: 10px; opacity: 0.6; font-size: 12px; }
</style>

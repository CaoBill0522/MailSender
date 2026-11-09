<template>
  <div>
    <div class="page-head">
      <h2>发送日志</h2>
    </div>
    <n-card size="small">
      <n-space style="margin-bottom: 12px" :wrap="true">
        <n-input v-model:value="f.to" placeholder="收件人" clearable style="width: 200px" @keyup.enter="search" />
        <n-select v-model:value="f.status" :options="statusOptions" placeholder="状态" clearable style="width: 120px" />
        <n-select v-model:value="f.account_id" :options="accountOptions" placeholder="发件账号" clearable filterable style="width: 200px" />
        <n-select v-model:value="f.api_key_id" :options="keyOptions" placeholder="调用方" clearable style="width: 160px" />
        <n-date-picker v-model:formatted-value="f.range" type="daterange" value-format="yyyy-MM-dd" clearable style="width: 260px" />
        <n-button type="primary" @click="search">查询</n-button>
      </n-space>
      <n-data-table
        remote
        size="small"
        :columns="columns"
        :data="rows"
        :loading="loading"
        :pagination="pagination"
        :row-key="(r) => r.id"
        :scroll-x="1100"
        @update:page="onPage"
        @update:page-size="onSize"
      />
    </n-card>

    <n-drawer v-model:show="showDetail" :width="560" placement="right">
      <n-drawer-content :title="`消息 #${detail?.message.id}`" closable>
        <template v-if="detail">
          <n-descriptions :column="1" label-placement="left" size="small" bordered>
            <n-descriptions-item label="状态"><status-tag :s="detail.message.status" /></n-descriptions-item>
            <n-descriptions-item label="收件人">{{ detail.message.to }}</n-descriptions-item>
            <n-descriptions-item label="主题">{{ detail.message.subject }}</n-descriptions-item>
            <n-descriptions-item label="模板">{{ detail.message.template || '-' }}</n-descriptions-item>
            <n-descriptions-item label="发件地址">{{ detail.message.from_addr || '-' }}</n-descriptions-item>
            <n-descriptions-item label="调用方">{{ detail.message.api_key_name }}（{{ detail.message.client_ip }}，{{ detail.message.source === 'smtp' ? 'SMTP' : 'HTTP API' }}）</n-descriptions-item>
            <n-descriptions-item label="创建时间">{{ fmtTime(detail.message.created_at, true) }}</n-descriptions-item>
            <n-descriptions-item label="发送时间">{{ fmtTime(detail.message.sent_at, true) }}</n-descriptions-item>
            <n-descriptions-item label="总耗时">{{ detail.message.duration_ms }} ms</n-descriptions-item>
            <n-descriptions-item v-if="detail.message.error" label="错误"><span style="word-break: break-all">{{ detail.message.error }}</span></n-descriptions-item>
          </n-descriptions>
          <h4 style="margin: 20px 0 8px">尝试记录（{{ detail.attempts.length }}）</h4>
          <div v-if="!detail.attempts.length" class="muted">没有可用账号，未进行任何发送尝试。</div>
          <n-timeline>
            <n-timeline-item
              v-for="a in detail.attempts"
              :key="a.id"
              :type="a.ok ? 'success' : 'error'"
              :title="`${a.ok ? '✓ 成功' : '✕ 失败'} · ${a.account_name || '#' + a.account_id}`"
              :time="`${fmtTime(a.created_at)} · ${a.duration_ms} ms`"
              :content="a.error"
            />
          </n-timeline>
        </template>
      </n-drawer-content>
    </n-drawer>
  </div>
</template>

<script setup>
import { defineComponent, h, onMounted, reactive, ref } from 'vue'
import {
  NButton, NCard, NDataTable, NInput, NSelect, NSpace, NTag, NDatePicker, NDrawer, NDrawerContent, NDescriptions, NDescriptionsItem,
  NTimeline, NTimelineItem,
} from 'naive-ui'
import { api } from '../api'
import { message, fmtTime } from '../utils'

const StatusTag = defineComponent({
  props: { s: String },
  setup: (p) => () => {
    const m = { sent: ['success', '✓ 成功'], failed: ['error', '✕ 失败'], queued: ['info', '… 排队中'] }[p.s] || ['default', p.s]
    return h(NTag, { size: 'small', type: m[0], bordered: false }, () => m[1])
  },
})

const statusOptions = [
  { label: '成功', value: 'sent' },
  { label: '失败', value: 'failed' },
  { label: '排队中', value: 'queued' },
]
const accountOptions = ref([])
const keyOptions = ref([])
const accName = ref({})

const f = reactive({ to: '', status: null, account_id: null, api_key_id: null, range: null })
const rows = ref([])
const loading = ref(false)
const pagination = reactive({ page: 1, pageSize: 20, itemCount: 0, showSizePicker: true, pageSizes: [20, 50, 100], prefix: ({ itemCount }) => `共 ${itemCount} 条` })

const columns = [
  { title: 'ID', key: 'id', width: 70 },
  { title: '时间', key: 'created_at', width: 140, render: (m) => fmtTime(m.created_at) },
  { title: '收件人', key: 'to', width: 200, ellipsis: { tooltip: true } },
  { title: '主题', key: 'subject', minWidth: 180, ellipsis: { tooltip: true } },
  { title: '状态', key: 'status', width: 100, render: (m) => h(StatusTag, { s: m.status }) },
  { title: '发件账号', key: 'account_id', width: 160, ellipsis: { tooltip: true }, render: (m) => (m.account_id ? accName.value[m.account_id] || m.from_addr : '-') },
  { title: '尝试', key: 'attempts', width: 60 },
  { title: '耗时', key: 'duration_ms', width: 80, render: (m) => `${m.duration_ms} ms` },
  { title: '调用方', key: 'api_key_name', width: 120, ellipsis: { tooltip: true } },
  { title: '来源', key: 'source', width: 70, render: (m) => h(NTag, { size: 'small', bordered: false }, () => (m.source === 'smtp' ? 'SMTP' : 'API')) },
  { title: '错误', key: 'error', minWidth: 160, ellipsis: { tooltip: { style: { maxWidth: '520px' } } } },
  { title: '', key: 'op', width: 70, fixed: 'right', render: (m) => h(NButton, { size: 'tiny', text: true, type: 'primary', onClick: () => open(m) }, () => '详情') },
]

const showDetail = ref(false)
const detail = ref(null)
async function open(m) {
  try {
    const d = await api.get(`/messages/${m.id}`)
    d.attempts = d.attempts || []
    detail.value = d
    showDetail.value = true
  } catch (e) {
    message.error(e.message)
  }
}

async function load() {
  loading.value = true
  const q = new URLSearchParams({ page: pagination.page, size: pagination.pageSize })
  if (f.to) q.set('to', f.to)
  if (f.status) q.set('status', f.status)
  if (f.account_id) q.set('account_id', f.account_id)
  if (f.api_key_id) q.set('api_key_id', f.api_key_id)
  if (f.range) {
    q.set('start', f.range[0])
    q.set('end', f.range[1])
  }
  try {
    const r = await api.get('/messages?' + q)
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
function onPage(p) {
  pagination.page = p
  load()
}
function onSize(s) {
  pagination.pageSize = s
  pagination.page = 1
  load()
}

onMounted(async () => {
  load()
  const [accs, keys] = await Promise.all([api.get('/accounts'), api.get('/keys')])
  accName.value = Object.fromEntries(accs.map((a) => [a.id, a.name]))
  accountOptions.value = accs.map((a) => ({ label: a.name, value: a.id }))
  keyOptions.value = keys.map((k) => ({ label: k.name, value: k.id }))
})
</script>

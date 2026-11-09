<template>
  <div>
    <div class="page-head">
      <h2>邮件模板</h2>
      <n-button type="primary" @click="openCreate">新建模板</n-button>
    </div>
    <n-card size="small">
      <n-data-table :columns="columns" :data="rows" :row-key="(r) => r.id" size="small" />
    </n-card>

    <n-modal v-model:show="showForm" preset="card" :title="form.id ? '编辑模板' : '新建模板'" style="width: 1080px; max-width: calc(100vw - 32px)">
      <n-grid cols="1 m:2" responsive="screen" :x-gap="20">
        <n-gi>
          <n-form label-placement="top">
            <n-form-item label="模板名（调用时的 template 参数）" required>
              <n-input v-model:value="form.name" placeholder="例如 verify_code" class="mono" />
            </n-form-item>
            <n-form-item label="主题" required>
              <n-input v-model:value="form.subject" placeholder="您的验证码：{{.code}}" />
            </n-form-item>
            <n-form-item label="HTML 正文">
              <n-input v-model:value="form.html" type="textarea" :rows="9" class="mono" />
            </n-form-item>
            <n-form-item label="纯文本正文（建议同时填写，提升送达率）">
              <n-input v-model:value="form.text" type="textarea" :rows="4" class="mono" />
            </n-form-item>
            <n-form-item label="备注">
              <n-input v-model:value="form.remark" />
            </n-form-item>
          </n-form>
        </n-gi>
        <n-gi>
          <div class="muted" style="margin-bottom: 6px">变量使用 Go 模板语法：<span class="mono">{{ '{' + '{.code}' + '}' }}</span>；预览变量（JSON）：</div>
          <n-input v-model:value="varsText" type="textarea" :rows="3" class="mono" />
          <n-button size="small" style="margin: 8px 0" @click="preview">预览</n-button>
          <n-card v-if="pv" size="small" embedded>
            <div style="font-weight: 600; margin-bottom: 8px">{{ pv.subject }}</div>
            <iframe v-if="pv.html" class="pv-frame" sandbox="" :srcdoc="pv.html" />
            <pre v-if="pv.text" class="pv-text">{{ pv.text }}</pre>
          </n-card>
        </n-gi>
      </n-grid>
      <template #footer>
        <n-space justify="end">
          <n-button @click="showForm = false">取消</n-button>
          <n-button type="primary" :loading="saving" @click="save">保存</n-button>
        </n-space>
      </template>
    </n-modal>
  </div>
</template>

<script setup>
import { h, onMounted, reactive, ref } from 'vue'
import { NButton, NCard, NDataTable, NModal, NForm, NFormItem, NInput, NGrid, NGi, NSpace, NPopconfirm } from 'naive-ui'
import { api } from '../api'
import { message, fmtTime } from '../utils'

const rows = ref([])
const empty = () => ({ id: 0, name: '', subject: '', html: '', text: '', remark: '' })
const form = reactive(empty())
const showForm = ref(false)
const saving = ref(false)
const varsText = ref('{"code": "123456", "minutes": 10, "email": "user@example.com"}')
const pv = ref(null)

function openCreate() {
  Object.assign(form, empty())
  pv.value = null
  showForm.value = true
}
function openEdit(t) {
  Object.assign(form, empty(), t)
  showForm.value = true
  preview()
}
async function preview() {
  let vars
  try {
    vars = JSON.parse(varsText.value || '{}')
  } catch {
    return message.error('变量不是合法 JSON')
  }
  try {
    pv.value = await api.post('/templates/preview', { ...form, vars })
  } catch (e) {
    message.error(e.message)
  }
}
async function save() {
  saving.value = true
  try {
    if (form.id) await api.put(`/templates/${form.id}`, { ...form })
    else await api.post('/templates', { ...form })
    message.success('已保存')
    showForm.value = false
    load()
  } catch (e) {
    message.error(e.message)
  } finally {
    saving.value = false
  }
}
async function remove(t) {
  await api.del(`/templates/${t.id}`)
  message.success('已删除')
  load()
}

const columns = [
  { title: '模板名', key: 'name', width: 180, render: (t) => h('span', { class: 'mono' }, t.name) },
  { title: '主题', key: 'subject', ellipsis: { tooltip: true } },
  { title: '备注', key: 'remark', ellipsis: { tooltip: true } },
  { title: '更新时间', key: 'updated_at', width: 140, render: (t) => fmtTime(t.updated_at) },
  {
    title: '操作',
    key: 'actions',
    width: 130,
    render: (t) =>
      h(NSpace, { size: 4 }, () => [
        h(NButton, { size: 'tiny', onClick: () => openEdit(t) }, () => '编辑'),
        h(NPopconfirm, { onPositiveClick: () => remove(t) }, { trigger: () => h(NButton, { size: 'tiny', type: 'error', secondary: true }, () => '删除'), default: () => `删除模板「${t.name}」？` }),
      ]),
  },
]

async function load() {
  rows.value = await api.get('/templates')
}
onMounted(load)
</script>

<style scoped>
.pv-frame { width: 100%; height: 320px; border: 0; background: #fff; border-radius: 4px; }
.pv-text { white-space: pre-wrap; font-size: 13px; margin: 8px 0 0; }
</style>

<template>
  <div>
    <div class="page-head">
      <h2>概览</h2>
      <n-space align="center">
        <span class="muted">每 15 秒自动刷新</span>
        <n-button size="small" @click="load">刷新</n-button>
      </n-space>
    </div>

    <n-grid cols="2 s:3 m:4 l:7" responsive="screen" :x-gap="12" :y-gap="12">
      <n-gi v-for="t in tiles" :key="t.label">
        <n-card size="small" class="tile">
          <div class="tile-label">{{ t.label }}</div>
          <div class="tile-value tabular">{{ t.value }}</div>
          <div class="tile-sub muted">{{ t.sub }}</div>
        </n-card>
      </n-gi>
    </n-grid>

    <n-card size="small" style="margin-top: 16px" title="发送趋势">
      <template #header-extra>
        <n-radio-group v-model:value="days" size="small" @update:value="loadTrend">
          <n-radio-button :value="7">近 7 天</n-radio-button>
          <n-radio-button :value="30">近 30 天</n-radio-button>
        </n-radio-group>
      </template>
      <v-chart class="chart" :option="chartOption" autoresize />
    </n-card>

    <n-grid cols="1 l:3" responsive="screen" :x-gap="16" :y-gap="16" style="margin-top: 16px">
      <n-gi span="1 l:2">
        <n-card size="small" title="账号今日用量">
          <n-data-table size="small" :columns="accCols" :data="accRows" :bordered="false" :scroll-x="640" />
        </n-card>
      </n-gi>
      <n-gi>
        <n-card size="small" title="调用方今日统计">
          <n-data-table size="small" :columns="keyCols" :data="keyRows" :bordered="false" />
        </n-card>
      </n-gi>
    </n-grid>
  </div>
</template>

<script setup>
import { computed, h, onMounted, onUnmounted, ref } from 'vue'
import { NCard, NGrid, NGi, NSpace, NButton, NRadioGroup, NRadioButton, NDataTable, NProgress, NTag } from 'naive-ui'
import VChart from 'vue-echarts'
import { use } from 'echarts/core'
import { CanvasRenderer } from 'echarts/renderers'
import { BarChart } from 'echarts/charts'
import { GridComponent, TooltipComponent, LegendComponent } from 'echarts/components'
import { api } from '../api'
import { isDark, pct, reasonText } from '../utils'

use([CanvasRenderer, BarChart, GridComponent, TooltipComponent, LegendComponent])

const overview = ref(null)
const trend = ref([])
const accounts = ref([])
const accStats = ref([])
const keyStats = ref([])
const days = ref(7)

const n = (v) => (v ?? 0).toLocaleString()

const tiles = computed(() => {
  const o = overview.value
  if (!o) return []
  const t = o.today
  return [
    { label: '今日发送成功', value: n(t.msg_sent), sub: `昨日 ${n(o.yesterday.msg_sent)}` },
    { label: '今日发送失败', value: n(t.msg_failed), sub: `重试失败 ${n(t.att_fail)} 次` },
    { label: '今日成功率', value: pct(t.msg_sent, t.msg_failed), sub: `尝试成功率 ${pct(t.att_ok, t.att_fail)}` },
    { label: '平均发送耗时', value: t.att_ok ? Math.round(t.att_ms_sum / t.att_ok) + ' ms' : '-', sub: '单次成功尝试' },
    { label: '今日被拦截', value: n(t.msg_rejected), sub: '收件人频率限制' },
    { label: '可用账号', value: `${o.accounts_available} / ${o.accounts_total}`, sub: `已启用 ${o.accounts_enabled}` },
    { label: '累计发送成功', value: n(o.total.msg_sent), sub: `累计失败 ${n(o.total.msg_failed)}` },
  ]
})

// Palette: categorical slots 1-3 of the validated reference palette, stepped per mode.
const palette = computed(() =>
  isDark.value
    ? { s: ['#3987e5', '#d95926', '#199e70'], surface: '#18181c', text: '#c3c2b7', grid: 'rgba(255,255,255,0.08)' }
    : { s: ['#2a78d6', '#eb6834', '#1baf7a'], surface: '#ffffff', text: '#52514e', grid: 'rgba(0,0,0,0.07)' },
)

const chartOption = computed(() => {
  const p = palette.value
  const keys = [
    ['msg_sent', '成功'],
    ['msg_failed', '失败'],
    ['msg_rejected', '被拦截'],
  ]
  // round only the top-most non-zero segment of each stack
  const topIdx = trend.value.map((d) => {
    for (let i = keys.length - 1; i >= 0; i--) if (d[keys[i][0]] > 0) return i
    return -1
  })
  const series = keys.map(([k, name], i) => ({
    name,
    type: 'bar',
    stack: 'total',
    barMaxWidth: 28,
    itemStyle: { color: p.s[i], borderColor: p.surface, borderWidth: 1 },
    emphasis: { focus: 'series' },
    data: trend.value.map((d, j) => ({
      value: d[k],
      itemStyle: topIdx[j] === i ? { borderRadius: [4, 4, 0, 0] } : undefined,
    })),
  }))
  return {
    backgroundColor: 'transparent',
    textStyle: { color: p.text },
    color: p.s,
    legend: { top: 0, left: 0, icon: 'roundRect', itemWidth: 12, itemHeight: 12, textStyle: { color: p.text } },
    grid: { left: 8, right: 8, top: 36, bottom: 4, containLabel: true },
    tooltip: {
      trigger: 'axis',
      axisPointer: { type: 'shadow', shadowStyle: { color: isDark.value ? 'rgba(255,255,255,0.05)' : 'rgba(0,0,0,0.04)' } },
      backgroundColor: isDark.value ? '#26262a' : '#fff',
      borderColor: isDark.value ? '#3a3a3f' : '#e5e5e3',
      textStyle: { color: isDark.value ? '#f0efec' : '#0b0b0b' },
      formatter: (items) => {
        const d = trend.value[items[0].dataIndex]
        const rows = items.map((it) => `${it.marker}${it.seriesName}<span style="float:right;margin-left:24px;font-variant-numeric:tabular-nums">${n(it.value)}</span>`)
        return `<b>${d.day}</b><br/>${rows.join('<br/>')}<br/>成功率<span style="float:right">${pct(d.msg_sent, d.msg_failed)}</span>`
      },
    },
    xAxis: {
      type: 'category',
      data: trend.value.map((d) => d.day.slice(5)),
      axisLine: { lineStyle: { color: p.grid } },
      axisTick: { show: false },
      axisLabel: { color: p.text },
    },
    yAxis: {
      type: 'value',
      minInterval: 1,
      splitLine: { lineStyle: { color: p.grid } },
      axisLabel: { color: p.text },
    },
    series,
  }
})

const accRows = computed(() => {
  const st = Object.fromEntries(accStats.value.map((r) => [r.account_id, r]))
  return accounts.value.map((a) => ({ ...a, st: st[a.id] || { att_ok: 0, att_fail: 0, att_ms_sum: 0 } }))
})

const statusTag = (a) => {
  const r = a.runtime
  if (r.available) return h(NTag, { size: 'small', type: 'success', bordered: false }, () => '● 可用')
  const type = r.reason === 'disabled' ? 'default' : r.reason === 'cooldown' ? 'error' : 'warning'
  return h(NTag, { size: 'small', type, bordered: false }, () => (r.reason === 'cooldown' ? '✕ ' : '◐ ') + (reasonText[r.reason] || r.reason))
}

const accCols = [
  { title: '账号', key: 'name', render: (a) => h('div', [h('div', a.name), h('div', { class: 'muted' }, a.from_addr)]) },
  { title: '状态', key: 'status', width: 120, render: statusTag },
  {
    title: '今日用量',
    key: 'usage',
    width: 180,
    render: (a) => {
      const used = a.runtime.day_count
      if (!a.limit_per_day) return h('span', { class: 'tabular' }, `${used} / 不限`)
      return h('div', [
        h('div', { class: 'tabular', style: 'font-size:12px' }, `${used} / ${a.limit_per_day}`),
        h(NProgress, { type: 'line', percentage: Math.min(100, Math.round((used / a.limit_per_day) * 100)), showIndicator: false, height: 6 }),
      ])
    },
  },
  { title: '成功', key: 'ok', width: 70, render: (a) => h('span', { class: 'tabular' }, a.st.att_ok) },
  { title: '失败', key: 'fail', width: 70, render: (a) => h('span', { class: 'tabular' }, a.st.att_fail) },
  { title: '成功率', key: 'rate', width: 80, render: (a) => pct(a.st.att_ok, a.st.att_fail) },
  { title: '平均耗时', key: 'lat', width: 90, render: (a) => (a.st.att_ok ? Math.round(a.st.att_ms_sum / a.st.att_ok) + ' ms' : '-') },
]

const keyRows = computed(() => keyStats.value)
const keyCols = [
  { title: '调用方', key: 'name', render: (r) => r.name || `#${r.api_key_id}（已删除）` },
  { title: '成功', key: 'msg_sent', width: 64 },
  { title: '失败', key: 'msg_failed', width: 64 },
  { title: '拦截', key: 'msg_rejected', width: 64 },
]

async function loadTrend() {
  trend.value = await api.get(`/stats/trend?days=${days.value}`)
}
async function load() {
  const [o, a, s, k] = await Promise.all([
    api.get('/stats/overview'),
    api.get('/accounts'),
    api.get('/stats/accounts?days=1'),
    api.get('/stats/keys?days=1'),
    loadTrend(),
  ])
  overview.value = o
  accounts.value = a
  accStats.value = s
  keyStats.value = k
}

let timer
onMounted(() => {
  load()
  timer = setInterval(load, 15000)
})
onUnmounted(() => clearInterval(timer))
</script>

<style scoped>
.tile { height: 100%; }
.tile-label { font-size: 13px; opacity: 0.7; }
.tile-value { font-size: 24px; font-weight: 600; margin: 4px 0 2px; }
.chart { height: 300px; }
</style>

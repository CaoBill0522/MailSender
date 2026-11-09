import { createDiscreteApi, darkTheme } from 'naive-ui'
import { computed, ref } from 'vue'

// Follow the OS colour scheme; components read `isDark` to theme themselves (e.g. charts).
const mq = window.matchMedia('(prefers-color-scheme: dark)')
export const isDark = ref(mq.matches)
mq.addEventListener?.('change', (e) => (isDark.value = e.matches))
export const theme = computed(() => (isDark.value ? darkTheme : null))

export const { message, dialog } = createDiscreteApi(['message', 'dialog'], {
  configProviderProps: computed(() => ({ theme: theme.value })),
})

const pad = (n) => String(n).padStart(2, '0')
export function fmtTime(t, withYear = false) {
  if (!t) return '-'
  const d = new Date(t)
  if (isNaN(d)) return '-'
  const s = `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
  return withYear ? `${d.getFullYear()}-${s}` : s
}

export function fromNow(t) {
  if (!t) return '-'
  const diff = (Date.now() - new Date(t).getTime()) / 1000
  if (diff < 0) {
    const m = Math.ceil(-diff / 60)
    return m < 60 ? `${m} 分钟后` : `${Math.ceil(m / 60)} 小时后`
  }
  if (diff < 60) return '刚刚'
  if (diff < 3600) return `${Math.floor(diff / 60)} 分钟前`
  if (diff < 86400) return `${Math.floor(diff / 3600)} 小时前`
  return `${Math.floor(diff / 86400)} 天前`
}

export function pct(ok, fail) {
  const t = ok + fail
  return t ? ((ok / t) * 100).toFixed(1) + '%' : '-'
}

export async function copy(text) {
  try {
    await navigator.clipboard.writeText(text)
    message.success('已复制')
  } catch {
    message.error('复制失败，请手动复制')
  }
}

export const reasonText = {
  disabled: '已停用',
  cooldown: '冷却中',
  'daily limit': '日限额已满',
  'hourly limit': '时限额已满',
  'minute limit': '分钟限额已满',
  'password decrypt failed': '密码解密失败',
}

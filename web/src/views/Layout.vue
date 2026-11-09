<template>
  <n-layout has-sider style="height: 100vh">
    <n-layout-sider
      bordered
      collapse-mode="width"
      :collapsed-width="64"
      :width="208"
      :collapsed="collapsed"
      show-trigger
      :native-scrollbar="false"
      @collapse="collapsed = true"
      @expand="collapsed = false"
    >
      <div class="logo" :class="{ mini: collapsed }">
        <svg viewBox="0 0 24 24" width="26" height="26"><rect x="2" y="4" width="20" height="16" rx="3" fill="#2a78d6" /><path d="M3 6l9 7 9-7" stroke="#fff" stroke-width="2" fill="none" /></svg>
        <span v-if="!collapsed">MailSender</span>
      </div>
      <n-menu :value="active" :options="menu" :collapsed="collapsed" :collapsed-width="64" @update:value="go" />
    </n-layout-sider>
    <n-layout>
      <n-layout-header bordered class="header">
        <span class="crumb">{{ route.meta.title }}</span>
        <n-dropdown :options="userMenu" @select="onUser">
          <n-button quaternary size="small">{{ username }} ▾</n-button>
        </n-dropdown>
      </n-layout-header>
      <n-layout-content :native-scrollbar="false" content-style="padding: 20px 24px 40px; max-width: 1400px;">
        <router-view />
      </n-layout-content>
    </n-layout>
  </n-layout>
</template>

<script setup>
import { computed, h, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NLayout, NLayoutSider, NLayoutHeader, NLayoutContent, NMenu, NDropdown, NButton } from 'naive-ui'
import { api, setToken } from '../api'

const route = useRoute()
const router = useRouter()
const collapsed = ref(window.innerWidth < 900)
const username = ref('admin')

const icon = (d) => () => h('svg', { viewBox: '0 0 24 24', width: 18, height: 18, fill: 'none', stroke: 'currentColor', 'stroke-width': 2, 'stroke-linecap': 'round', 'stroke-linejoin': 'round' }, [h('path', { d })])
const menu = [
  { label: '概览', key: '/', icon: icon('M3 13h8V3H3zM13 21h8V11h-8zM3 21h8v-6H3zM13 3v6h8V3z') },
  { label: 'SMTP 账号', key: '/accounts', icon: icon('M4 6h16v12H4zM4 7l8 6 8-6') },
  { label: 'API Key', key: '/keys', icon: icon('M15 7a4 4 0 1 1-3.9 5H3v3h3v3h3v-3h2.1A4 4 0 0 1 15 7z') },
  { label: '发送日志', key: '/messages', icon: icon('M4 5h16M4 10h16M4 15h10M4 20h7') },
  { label: '退信名单', key: '/suppressions', icon: icon('M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18zM5.6 5.6l12.8 12.8') },
  { label: '安全', key: '/security', icon: icon('M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z') },
  { label: '系统设置', key: '/settings', icon: icon('M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6zM19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z') },
  { label: '接入文档', key: '/docs', icon: icon('M4 19.5A2.5 2.5 0 0 1 6.5 17H20V3H6.5A2.5 2.5 0 0 0 4 5.5zM20 17v4H6.5') },
]
const active = computed(() => route.path)
const go = (k) => router.push(k)

const userMenu = [
  { label: '修改密码', key: 'password' },
  { label: '退出登录', key: 'logout' },
]
function onUser(k) {
  if (k === 'logout') {
    setToken(null)
    router.replace('/login')
  } else {
    router.push('/security')
  }
}

onMounted(async () => {
  try { username.value = (await api.get('/me')).username } catch {}
})
</script>

<style scoped>
.logo { display: flex; align-items: center; gap: 10px; height: 56px; padding: 0 20px; font-weight: 600; font-size: 16px; }
.logo.mini { padding: 0; justify-content: center; }
.header { height: 56px; display: flex; align-items: center; justify-content: space-between; padding: 0 24px; }
.crumb { font-weight: 500; }
</style>

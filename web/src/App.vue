<template>
  <n-config-provider :theme="theme" :locale="zhCN" :date-locale="dateZhCN" :theme-overrides="overrides">
    <n-global-style />
    <router-view />
  </n-config-provider>
</template>

<script setup>
import { NConfigProvider, NGlobalStyle, zhCN, dateZhCN } from 'naive-ui'
import { theme } from './utils'

const overrides = {
  common: { primaryColor: '#2a78d6', primaryColorHover: '#3987e5', primaryColorPressed: '#256abf', primaryColorSuppl: '#3987e5', borderRadius: '6px' },
}
</script>

<style>
body { margin: 0; }
.page-head { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-bottom: 16px; flex-wrap: wrap; }
.page-head h2 { margin: 0; font-size: 20px; font-weight: 600; }
.muted { opacity: 0.6; font-size: 12px; }
.mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
.tabular { font-variant-numeric: tabular-nums; }
</style>

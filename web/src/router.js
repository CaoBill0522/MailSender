import { createRouter, createWebHistory } from 'vue-router'
import { getToken } from './api'

const routes = [
  { path: '/login', component: () => import('./views/Login.vue'), meta: { public: true } },
  {
    path: '/',
    component: () => import('./views/Layout.vue'),
    children: [
      { path: '', component: () => import('./views/Dashboard.vue'), meta: { title: '概览' } },
      { path: 'accounts', component: () => import('./views/Accounts.vue'), meta: { title: 'SMTP 账号' } },
      { path: 'proxies', component: () => import('./views/Proxies.vue'), meta: { title: '出口代理' } },
      { path: 'keys', component: () => import('./views/ApiKeys.vue'), meta: { title: 'API Key' } },
      { path: 'messages', component: () => import('./views/Messages.vue'), meta: { title: '发送日志' } },
      { path: 'suppressions', component: () => import('./views/Suppressions.vue'), meta: { title: '退信名单' } },
      { path: 'security', component: () => import('./views/Security.vue'), meta: { title: '安全' } },
      { path: 'settings', component: () => import('./views/Settings.vue'), meta: { title: '系统设置' } },
      { path: 'docs', component: () => import('./views/Docs.vue'), meta: { title: '接入文档' } },
    ],
  },
  { path: '/:pathMatch(.*)*', redirect: '/' },
]

const router = createRouter({ history: createWebHistory(), routes })

router.beforeEach((to) => {
  if (!to.meta.public && !getToken()) return '/login'
})
router.afterEach((to) => {
  document.title = to.meta.title ? `${to.meta.title} · MailSender` : 'MailSender'
})

export default router

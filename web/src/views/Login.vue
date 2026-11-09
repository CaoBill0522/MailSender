<template>
  <div class="login-wrap">
    <n-card class="login-card" :bordered="false">
      <div class="brand">
        <svg viewBox="0 0 24 24" width="32" height="32"><rect x="2" y="4" width="20" height="16" rx="3" fill="#2a78d6" /><path d="M3 6l9 7 9-7" stroke="#fff" stroke-width="2" fill="none" /></svg>
        <div>
          <div class="title">MailSender</div>
          <div class="muted">SMTP 轮询发信服务</div>
        </div>
      </div>
      <n-form @submit.prevent="submit">
        <n-form-item label="用户名">
          <n-input v-model:value="form.username" placeholder="admin" />
        </n-form-item>
        <n-form-item label="密码">
          <n-input v-model:value="form.password" type="password" show-password-on="click" placeholder="首次启动日志中的密码" @keyup.enter="submit" />
        </n-form-item>
        <n-form-item v-if="needOtp" label="两步验证码">
          <n-input ref="otpInput" v-model:value="form.otp" placeholder="身份验证器中的 6 位数字" maxlength="6" :input-props="{ inputmode: 'numeric', autocomplete: 'one-time-code' }" @keyup.enter="submit" />
        </n-form-item>
        <n-button type="primary" block :loading="loading" attr-type="submit" @click="submit">登录</n-button>
      </n-form>
    </n-card>
  </div>
</template>

<script setup>
import { nextTick, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { NCard, NForm, NFormItem, NInput, NButton } from 'naive-ui'
import { api, setToken } from '../api'
import { message } from '../utils'

const router = useRouter()
const form = reactive({ username: 'admin', password: '', otp: '' })
const loading = ref(false)
const needOtp = ref(false)
const otpInput = ref(null)

async function submit() {
  if (loading.value) return
  loading.value = true
  try {
    const r = await api.post('/login', form)
    setToken(r.token)
    router.replace('/')
  } catch (e) {
    if (e.code === 'otp_required') {
      needOtp.value = true
      await nextTick()
      otpInput.value?.focus()
    } else message.error(e.message)
  } finally {
    loading.value = false
  }
}
</script>

<style scoped>
.login-wrap { min-height: 100vh; display: flex; align-items: center; justify-content: center; padding: 16px; box-sizing: border-box; }
.login-card { width: 100%; max-width: 380px; box-shadow: 0 8px 32px rgba(0, 0, 0, 0.08); }
.brand { display: flex; align-items: center; gap: 12px; margin-bottom: 24px; }
.title { font-size: 20px; font-weight: 600; }
</style>

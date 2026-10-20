<script setup>
// 后台登录页：无 token 时由路由守卫跳到这里；登录成功回到 redirect 指定的后台页面。
import { ref, onMounted } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { api, errMsg, getToken } from '../api'

const router = useRouter()
const route = useRoute()
const password = ref('')
const busy = ref(false)
const error = ref('')

function target() {
  const r = route.query.redirect
  return typeof r === 'string' && r.startsWith('/admin') ? r : '/admin/anime'
}

async function doLogin() {
  if (!password.value) return
  busy.value = true
  error.value = ''
  try {
    await api.login(password.value)
    router.replace(target())
  } catch (e) {
    error.value = errMsg(e)
  } finally {
    busy.value = false
  }
}

onMounted(() => {
  // 已持有 token：直接进入后台
  if (getToken()) router.replace(target())
})
</script>

<template>
  <div class="login-wrap">
    <div class="login-card">
      <div class="login-logo">⎙</div>
      <h3>后台登录</h3>
      <p class="login-sub">输入管理口令进入后台</p>
      <n-input
        v-model:value="password"
        type="password"
        placeholder="管理口令"
        size="large"
        @keyup.enter="doLogin"
      />
      <div v-if="error" class="login-err">{{ error }}</div>
      <n-button size="large" type="primary" class="grad-btn" :loading="busy" @click="doLogin">
        登 录
      </n-button>
    </div>
  </div>
</template>

<style scoped>
/* 登录页独立全屏（App 布局在 /login 隐藏侧边栏并去 padding） */
.login-wrap {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background:
    radial-gradient(60% 50% at 50% 0%, rgba(91, 140, 255, 0.08) 0%, transparent 70%),
    var(--bg);
}
.login-card {
  width: 380px;
  padding: 36px 32px;
  border-radius: 20px;
  background: linear-gradient(160deg, var(--card) 0%, #10141f 100%);
  border: 1px solid var(--border);
  box-shadow: 0 24px 60px rgba(0, 0, 0, 0.5);
  display: flex;
  flex-direction: column;
  gap: 14px;
}
.login-logo {
  font-size: 34px;
  text-align: center;
  background: linear-gradient(135deg, var(--accent), var(--accent-2));
  -webkit-background-clip: text;
  background-clip: text;
  color: transparent;
}
.login-card h3 {
  margin: 0;
  text-align: center;
  font-size: 20px;
}
.login-sub {
  margin: 0 0 8px;
  text-align: center;
  color: var(--text-3);
  font-size: 13px;
}
.login-err {
  color: var(--err);
  font-size: 13px;
  text-align: center;
}
</style>

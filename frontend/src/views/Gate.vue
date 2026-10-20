<script setup>
// 访客认证门禁：后台开启「访客口令」后，未认证的访问会由接口 401 跳到这里。
// 输入口令 → 换取访客 token（localStorage 持久化，按设置里的过期时长失效）。
import { ref, onMounted } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { api, errMsg, getVisitorToken } from '../api'

const router = useRouter()
const route = useRoute()
const password = ref('')
const busy = ref(false)
const error = ref('')

function target() {
  const n = route.query.next
  return typeof n === 'string' && n.startsWith('/') ? n : '/'
}

async function doLogin() {
  if (!password.value) return
  busy.value = true
  error.value = ''
  try {
    await api.visitorLogin(password.value)
    router.replace(target())
  } catch (e) {
    error.value = errMsg(e)
  } finally {
    busy.value = false
  }
}

onMounted(() => {
  // 已持有访客 token 直接放行（可能已过期，进页面后接口 401 会再带回这里）
  if (getVisitorToken()) router.replace(target())
})
</script>

<template>
  <div class="login-wrap">
    <div class="login-card">
      <div class="login-logo">⛨</div>
      <h3>访客验证</h3>
      <p class="login-sub">站点已开启访问口令，请输入口令继续浏览</p>
      <n-input
        v-model:value="password"
        type="password"
        placeholder="访客口令"
        size="large"
        @keyup.enter="doLogin"
      />
      <div v-if="error" class="login-err">{{ error }}</div>
      <n-button size="large" type="primary" class="grad-btn" :loading="busy" @click="doLogin">
        进 入
      </n-button>
    </div>
  </div>
</template>

<style scoped>
/* 复用登录页全屏样式（App 布局在 /gate 同样隐藏侧边栏并去 padding） */
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

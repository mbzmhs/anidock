<script setup>
// 首次初始化页：系统从未设置过后台口令时，路由守卫会把所有页面带到这里。
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { api, errMsg } from '../api'
import { toastSuccess } from '../notify'
import { markBootInitialized } from '../router'

const router = useRouter()
const adminPw = ref('')
const adminPw2 = ref('')
const visitorPw = ref('')
const visitorPw2 = ref('')
const busy = ref(false)
const error = ref('')

async function doInit() {
  if (!adminPw.value) {
    error.value = '请设置后台口令（至少 6 位）'
    return
  }
  if (adminPw.value !== adminPw2.value) {
    error.value = '后台口令两次输入不一致'
    return
  }
  if (visitorPw.value && visitorPw.value !== visitorPw2.value) {
    error.value = '访客口令两次输入不一致'
    return
  }
  busy.value = true
  error.value = ''
  try {
    await api.bootstrap(adminPw.value, visitorPw.value)
    markBootInitialized() // 刷新守卫缓存，避免跳转 /login 时被拦回 /init
    toastSuccess('初始化完成，请用后台口令登录')
    router.replace('/login')
  } catch (e) {
    error.value = errMsg(e)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="login-wrap">
    <div class="login-card init-card">
      <div class="login-logo">⎙</div>
      <h3>初始化系统</h3>
      <p class="login-sub">首次使用，请设置后台口令（至少 6 位）</p>
      <div class="init-field">
        <label>后台口令</label>
        <n-input v-model:value="adminPw" type="password" show-password-on="click" size="large" placeholder="用于登录后台管理" />
      </div>
      <div class="init-field">
        <label>确认口令</label>
        <n-input v-model:value="adminPw2" type="password" show-password-on="click" size="large" placeholder="再输入一次" @keyup.enter="doInit" />
      </div>
      <div class="init-divider"><span>访客口令（可选，开启后前台需口令才能访问）</span></div>
      <div class="init-field">
        <label>访客口令</label>
        <n-input v-model:value="visitorPw" type="password" show-password-on="click" size="large" placeholder="留空 = 站点公开" />
      </div>
      <div v-if="visitorPw" class="init-field">
        <label>确认访客口令</label>
        <n-input v-model:value="visitorPw2" type="password" show-password-on="click" size="large" placeholder="再输入一次" @keyup.enter="doInit" />
      </div>
      <div v-if="error" class="login-err">{{ error }}</div>
      <n-button size="large" type="primary" class="grad-btn" :disabled="!!visitorPw && visitorPw !== visitorPw2" :loading="busy" @click="doInit">
        完成初始化
      </n-button>
    </div>
  </div>
</template>

<style scoped>
/* 复用登录页的全屏居中布局（App 在 /init 同样隐藏侧边栏） */
.login-wrap {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background:
    radial-gradient(60% 50% at 50% 0%, rgba(91, 140, 255, 0.08) 0%, transparent 70%),
    var(--bg);
  padding: 24px;
}
.init-card {
  width: 400px;
  max-width: 100%;
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
.init-card h3 {
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
.init-field label {
  display: block;
  font-size: 12px;
  color: var(--text-3);
  margin-bottom: 5px;
}
.init-divider {
  display: flex;
  align-items: center;
  gap: 10px;
  color: var(--text-3);
  font-size: 12px;
  margin-top: 4px;
}
.init-divider::before,
.init-divider::after {
  content: '';
  flex: 1;
  height: 1px;
  background: var(--border-soft);
}
.login-err {
  color: var(--err);
  font-size: 13px;
  text-align: center;
}
</style>

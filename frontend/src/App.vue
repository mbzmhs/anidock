<script setup>
import { onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NMessageProvider, NConfigProvider, darkTheme, zhCN, dateZhCN } from 'naive-ui'
import { getToken } from './api'
import { useNavStore } from './stores/nav'

const route = useRoute()
const router = useRouter()
const nav = useNavStore()

// 设置模态窗（后台 token 可用时开放）
import SettingsModal from './components/SettingsModal.vue'
const showSettings = ref(false)

const themeOverrides = {
  common: {
    primaryColor: '#5b8cff',
    primaryColorHover: '#7aa2ff',
    primaryColorPressed: '#4670d4',
    borderRadius: '10px',
    borderRadiusMedium: '8px',
  },
  Card: { borderRadiusLarge: '14px' },
}

function pickSeason(key) {
  nav.setSeason(key)
  if (router.currentRoute.value.path !== '/') {
    router.push('/')
  }
}
function goSearch() {
  nav.bumpSearch()
  if (router.currentRoute.value.path !== '/') {
    router.push('/')
  }
}

onMounted(() => nav.load())

// 回到首页（例如后台重新导入番剧后）刷新侧边栏季度列表
watch(
  () => route.path,
  (p) => {
    if (p === '/') nav.load()
  }
)
</script>

<template>
  <n-config-provider :theme="darkTheme" :locale="zhCN" :date-locale="dateZhCN" :theme-overrides="themeOverrides">
    <n-message-provider>
      <div class="layout">
        <!-- 登录页独立全屏：不套侧边栏布局 -->
        <aside class="sidebar" v-if="route.path !== '/login' && route.path !== '/gate' && route.path !== '/init'">
          <div class="brand">
            <span class="brand-icon">⚓</span>
            <span class="brand-name">AniDock · 番港</span>
          </div>
          <nav class="nav">
            <div class="nav-section">季度</div>
            <button v-for="s in nav.seasons" :key="s.key" class="nav-item season" :class="{ active: nav.selected === s.key }" @click="pickSeason(s.key)">
              <span class="season-name">{{ s.label }}</span>
              <span class="season-cnt">{{ s.count }}</span>
            </button>
            <button class="nav-item" :class="{ active: nav.selected === 'all' }" @click="pickSeason('all')">
              <span class="season-name">全部番剧</span>
              <span class="season-cnt">{{ nav.total }}</span>
            </button>
            <button class="nav-item nav-search" @click="goSearch">
              <span class="nav-ico">⌕</span> 搜索
            </button>
          </nav>
          <div class="sidebar-foot">
            <button v-if="getToken()" class="admin-link settings-link" title="站点设置（Bangumi token / 口令 / 轮询间隔…）" @click="showSettings = true">
              ⚙ 设置
            </button>
            <router-link :to="getToken() ? '/admin/anime' : '/login'" class="admin-link">
              {{ getToken() ? '⎙ 后台管理' : '⎙ 登录后台' }}
            </router-link>
          </div>
        </aside>
        <main class="main" :class="{ 'no-pad': route.path === '/login' || route.path === '/gate' || route.path === '/init' }">
          <router-view />
        </main>
        <SettingsModal v-if="showSettings" @close="showSettings = false" />
      </div>
    </n-message-provider>
  </n-config-provider>
</template>

<style>
:root {
  --bg: #0b0e14;
  --bg-soft: #10141c;
  --card: #141926;
  --card-hover: #1a2133;
  --border: #232b3d;
  --border-soft: #1c2333;
  --text: #e6ebf4;
  --text-2: #9aa5bb;
  --text-3: #5f6b82;
  --accent: #5b8cff;
  --accent-2: #8b5cf6;
  --ok: #34d399;
  --err: #f87171;
}
* {
  box-sizing: border-box;
}
html,
body {
  margin: 0;
  background: var(--bg);
  color: var(--text);
  font-family: -apple-system, BlinkMacSystemFont, 'PingFang SC', 'Hiragino Sans GB', 'Microsoft YaHei',
    sans-serif;
}
a {
  color: inherit;
}
::-webkit-scrollbar {
  width: 10px;
  height: 10px;
}
::-webkit-scrollbar-thumb {
  background: #26304a;
  border-radius: 8px;
}
::-webkit-scrollbar-track {
  background: transparent;
}

.layout {
  display: flex;
  min-height: 100vh;
}
.sidebar {
  width: 210px;
  flex-shrink: 0;
  position: sticky;
  top: 0;
  height: 100vh;
  display: flex;
  flex-direction: column;
  background: var(--bg-soft);
  border-right: 1px solid var(--border-soft);
  padding: 22px 14px;
}
.brand {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 0 10px 20px;
}
.brand-icon {
  font-size: 20px;
  background: linear-gradient(135deg, var(--accent), var(--accent-2));
  -webkit-background-clip: text;
  background-clip: text;
  color: transparent;
}
.brand-name {
  font-size: 17px;
  font-weight: 800;
  letter-spacing: 1px;
}
.nav {
  display: flex;
  flex-direction: column;
  gap: 4px;
  flex: 1;
  overflow-y: auto;
}
.nav-section {
  font-size: 11px;
  color: var(--text-3);
  letter-spacing: 2px;
  padding: 14px 10px 6px;
}
.nav-item {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 9px 12px;
  border-radius: 10px;
  color: var(--text-2);
  text-decoration: none;
  font-size: 14px;
  transition: all 0.15s;
  border: 1px solid transparent;
  background: none;
  cursor: pointer;
  font-family: inherit;
  width: 100%;
  text-align: left;
}
.nav-item:hover {
  color: var(--text);
  background: var(--card);
}
.nav-item.active {
  color: #fff;
  background: linear-gradient(135deg, rgba(91, 140, 255, 0.18), rgba(139, 92, 246, 0.14));
  border-color: rgba(91, 140, 255, 0.35);
}
.nav-ico {
  width: 20px;
  text-align: center;
  color: var(--accent);
}
.nav-search {
  margin-top: 6px;
}
.season-name {
  flex: 1;
}
.season-cnt {
  font-size: 11px;
  color: var(--text-3);
  background: rgba(255, 255, 255, 0.05);
  border: 1px solid var(--border-soft);
  padding: 1px 8px;
  border-radius: 10px;
}
.nav-item.active .season-cnt {
  color: #c3d4ff;
  border-color: rgba(91, 140, 255, 0.5);
  background: rgba(91, 140, 255, 0.15);
}
.sidebar-foot {
  padding: 14px 10px 0;
  border-top: 1px solid var(--border-soft);
}
.admin-link {
  font-size: 12.5px;
  color: var(--text-3);
  text-decoration: none;
  transition: color 0.15s;
}
.admin-link:hover {
  color: var(--text);
}
.sidebar-foot .settings-link {
  all: unset;
  cursor: pointer;
  display: block;
  margin-bottom: 8px;
}
.main {
  flex: 1;
  min-width: 0;
  padding: 26px 30px 60px;
}
.main.no-pad {
  padding: 0;
}
@media (max-width: 900px) {
  .sidebar {
    display: none;
  }
}

/* 通用卡片/表格/按钮基调 */
.page {
  display: flex;
  flex-direction: column;
  gap: 18px;
}
.panel {
  background: var(--card);
  border: 1px solid var(--border-soft);
  border-radius: 14px;
  padding: 16px 18px;
}
.page-head {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}
.page-head h2 {
  margin: 0;
  font-size: 20px;
  display: flex;
  align-items: center;
  gap: 10px;
}
.page-head h2 .h-ico {
  color: var(--accent);
}
.hint {
  font-size: 13px;
  color: var(--text-3);
  line-height: 1.7;
  margin: 0;
}
table.tbl {
  width: 100%;
  border-collapse: collapse;
  font-size: 13.5px;
}
table.tbl th {
  text-align: left;
  color: var(--text-3);
  font-weight: 500;
  font-size: 12px;
  padding: 8px 12px;
  border-bottom: 1px solid var(--border);
}
table.tbl td {
  padding: 10px 12px;
  border-bottom: 1px solid var(--border-soft);
  vertical-align: middle;
}
table.tbl tr:hover td {
  background: rgba(91, 140, 255, 0.05);
}
.dim {
  color: var(--text-3);
  font-size: 12px;
}
.empty {
  padding: 42px 0;
  text-align: center;
  color: var(--text-3);
}
.grad-btn {
  background: linear-gradient(135deg, var(--accent), var(--accent-2)) !important;
  border: none !important;
}
.form {
  display: flex;
  flex-direction: column;
  gap: 14px;
}
/* 只命中普通 <label>（无 class）：Naive 组件根元素也是 <label>（n-radio/n-checkbox 等），
   带 class；裸 label 选择器会把单选圆圈和文字竖排（全局样式，波及所有页面）*/
.form label:not([class]) {
  font-size: 13px;
  color: var(--text-2);
  display: flex;
  flex-direction: column;
  gap: 6px;
}
</style>

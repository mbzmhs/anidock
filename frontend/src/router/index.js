import { createRouter, createWebHistory } from 'vue-router'
import { api, getToken } from '../api'

const routes = [
  { path: '/init', name: 'init', component: () => import('../views/Init.vue') }, // 首次初始化（未设后台口令时）
  { path: '/', name: 'home', component: () => import('../views/Home.vue') },
  { path: '/login', name: 'login', component: () => import('../views/Login.vue') },
  { path: '/gate', name: 'gate', component: () => import('../views/Gate.vue') }, // 访客认证门禁（防爬虫）
  { path: '/anime/:id', name: 'anime', component: () => import('../views/AnimeDetail.vue') },
  { path: '/admin', redirect: '/admin/anime' },
  { path: '/admin/anime/new', name: 'admin-anime-new', component: () => import('../views/admin/AnimeForm.vue') },
  { path: '/admin/anime/edit/:id', name: 'admin-anime-edit', component: () => import('../views/admin/AnimeForm.vue') },
  { path: '/admin/anime', name: 'admin-anime', component: () => import('../views/admin/AnimeAdmin.vue') },
  { path: '/admin/pending', name: 'admin-pending', component: () => import('../views/admin/PendingAdmin.vue') },
]

const router = createRouter({
  // BASE_URL 随构建 base 变化：'/'（根部署）或 '/anidock/'（nginx 前缀分流）
  history: createWebHistory(import.meta.env.BASE_URL),
  routes,
})

// 首次初始化守卫：系统从未设置过后台口令时，除 /init 外全部页面锁定到初始化页
let bootChecked = null // 缓存一次查询结果；初始化成功后由 markBootInitialized 置 true
export function markBootInitialized() {
  bootChecked = true
}
router.beforeEach(async (to) => {
  if (bootChecked === null) {
    try {
      bootChecked = (await api.bootstrapStatus()).initialized
    } catch {
      bootChecked = true // 查询失败（后端未就绪/旧版本）时不阻塞，交给后续接口 401 兜底
    }
  }
  if (!bootChecked && to.path !== '/init') {
    return { path: '/init' }
  }
  // 后台鉴权守卫：没有 token 直接进入登录页（无弹窗），登录后回到原页面
  if (to.path.startsWith('/admin') && !getToken()) {
    return { path: '/login', query: { redirect: to.fullPath } }
  }
  return true
})

export default router

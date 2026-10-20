import axios from 'axios'

// 统一请求层：开发期走 Vite 代理 /api → :8080；生产同源。
import { toastError } from '../notify'

// baseURL 取构建 base（'/' 或 '/anidock/'）：请求 url 形如 '/api/...'，
// axios 会自动拼成 '/anidock/api/...'，由 nginx 前缀分流剥掉 /anidock 后转发
const http = axios.create({ baseURL: import.meta.env.BASE_URL })

const TOKEN_KEY = 'la_admin_token'
const VISITOR_KEY = 'la_visitor_token'

export function getToken() {
  return localStorage.getItem(TOKEN_KEY) || ''
}
export function setToken(t) {
  if (t) localStorage.setItem(TOKEN_KEY, t)
  else localStorage.removeItem(TOKEN_KEY)
}
export function getVisitorToken() {
  return localStorage.getItem(VISITOR_KEY) || ''
}
export function setVisitorToken(t) {
  if (t) localStorage.setItem(VISITOR_KEY, t)
  else localStorage.removeItem(VISITOR_KEY)
}

http.interceptors.request.use((cfg) => {
  // 后台 token 优先，否则用访客 token（前台接口在开启访客认证后需要它）
  const t = getToken() || getVisitorToken()
  if (t) cfg.headers.Authorization = `Bearer ${t}`
  return cfg
})

http.interceptors.response.use(
  (res) => res,
  (err) => {
    if (err.response && err.response.status === 401) {
      const msg = err.response.data?.error
      const path = window.location.pathname
      // 访客认证门禁 → 跳访客登录页（带来源，登录后回跳）
      // 先清掉失效的访客 token，避免 Gate 页"检测到 token 直接放行"形成重定向循环
      if (msg === 'visitor-auth-required' && path !== '/gate' && path !== '/login') {
        setVisitorToken('')
        window.location.assign(`/gate?next=${encodeURIComponent(path + window.location.search)}`)
        return new Promise(() => {}) // 挂起：跳转后不再走后续错误处理
      }
      // 后台 401 → 清除 token，跳登录页（登录后回到原页面）
      const hadToken = !!getToken()
      setToken('')
      window.dispatchEvent(new Event('la-auth-expired'))
      if (hadToken && path.startsWith('/admin')) {
        window.location.assign(`/login?redirect=${encodeURIComponent(path + window.location.search)}`)
      }
    }
    return Promise.reject(err)
  }
)

export const api = {
  // 前台
  listAnime: (params) => http.get('/api/anime', { params }).then((r) => r.data),
  countAnime: (params) => http.get('/api/anime/count', { params }).then((r) => r.data.total),
  getAnime: (id) => http.get(`/api/anime/${id}`).then((r) => r.data),
  getEpisodes: (id) => http.get(`/api/anime/${id}/episodes`).then((r) => r.data),
  getAnimeFeeds: (id) => http.get(`/api/anime/${id}/feeds`).then((r) => r.data),
  listFeeds: () => http.get('/api/feeds').then((r) => r.data),
  latest: (limit = 50) => http.get('/api/latest', { params: { limit } }).then((r) => r.data),

  // 后台
  login: (password) =>
    http.post('/api/admin/login', { password }).then((r) => {
      setToken(r.data.token)
      return r.data
    }),
  logout: () => setToken(''),
  adminListAnime: (params) => http.get('/api/admin/anime', { params }).then((r) => r.data),
  adminCreateAnime: (body) => http.post('/api/admin/anime', body).then((r) => r.data),
  adminUpdateAnime: (id, body) => http.put(`/api/admin/anime/${id}`, body).then((r) => r.data),
  adminDeleteAnime: (id) => http.delete(`/api/admin/anime/${id}`).then((r) => r.data),
  adminImport: (source, query) => http.post('/api/admin/anime/import', { source, query }).then((r) => r.data),
  adminImportArchive: (payload) => http.post('/api/admin/anime/import-archive', payload).then((r) => r.data),
  adminArchiveStatus: () => http.get('/api/admin/anime/import-archive/status').then((r) => r.data),
  adminArchiveLatest: () => http.get('/api/admin/anime/archive-latest').then((r) => r.data),
  adminArchiveDownload: (force) => http.post('/api/admin/anime/archive-download', { force: !!force }).then((r) => r.data),
  adminArchiveDownloadStatus: () => http.get('/api/admin/anime/archive-download/status').then((r) => r.data),
  adminFetchCovers: () => http.post('/api/admin/anime/fetch-covers').then((r) => r.data),
  adminCoverStatus: () => http.get('/api/admin/anime/fetch-covers/status').then((r) => r.data),
  // RSS 自动发现（v0.2.0）：后台任务，关页不中断
  adminDiscoverRss: (id, payload) =>
    http.post(`/api/admin/anime/${id}/discover-rss`, payload || {}).then((r) => r.data),
  adminDiscoveryStatus: (id) => http.get(`/api/admin/anime/${id}/discover-rss`).then((r) => r.data),
  adminApplyDiscovery: (id, payload) =>
    http.post(`/api/admin/anime/${id}/discover-rss/apply`, payload).then((r) => r.data),
  // RSS 批量发现（v0.3.0）：串行后台任务，可随时停止，关页不中断；同一时刻仅一个任务
  adminBulkRss: (payload) => http.post('/api/admin/rss-bulk', payload).then((r) => r.data),
  adminBulkRssStatus: () => http.get('/api/admin/rss-bulk').then((r) => r.data),
  adminBulkRssStop: () => http.post('/api/admin/rss-bulk/stop').then((r) => r.data),
  purgeBlacklistedFeeds: () => http.post('/api/admin/feeds/purge-blacklisted').then((r) => r.data),
  // 只拉快照预填表单，不写库（保存与否由用户决定）
  adminPreviewAnime: (source, query) =>
    http.post('/api/admin/anime/preview', { source, query }).then((r) => r.data),
  adminListFeeds: () => http.get('/api/admin/feeds').then((r) => r.data),
  adminCreateFeed: (body) => http.post('/api/admin/feeds', body).then((r) => r.data),
  adminUpdateFeed: (id, body) => http.put(`/api/admin/feeds/${id}`, body).then((r) => r.data),
  adminDeleteFeed: (id) => http.delete(`/api/admin/feeds/${id}`).then((r) => r.data),
  adminFetchOne: (id) => http.post(`/api/admin/feeds/${id}/fetch`).then((r) => r.data),
  adminFetchAll: () => http.post('/api/admin/feeds/fetch-all').then((r) => r.data),
  adminPending: () => http.get('/api/admin/pending').then((r) => r.data),
  uploadMedia: (files) => {
    const fd = new FormData()
    ;(Array.isArray(files) ? files : [files]).forEach((f) => fd.append('file', f))
    return http.post('/api/admin/media', fd).then((r) => r.data.paths)
  },
  adminBindRelease: (id, animeId, bindSiblings) =>
    http.post(`/api/admin/releases/${id}/bind`, { anime_id: animeId, bind_siblings: bindSiblings }).then((r) => r.data),

  // 设置（后台「⚙ 设置」模态窗；DB 覆盖 .env，热生效）
  getSettings: () => http.get('/api/admin/settings').then((r) => r.data),
  saveSettings: (patch) => http.put('/api/admin/settings', patch).then((r) => r.data),
  testBangumi: () => http.post('/api/admin/settings/test-bangumi').then((r) => r.data),

  // 首次初始化（仅在未配置任何后台口令时可用）
  bootstrapStatus: () => http.get('/api/bootstrap/status').then((r) => r.data),
  bootstrap: (adminPassword, visitorPassword) =>
    http
      .post('/api/bootstrap', {
        admin_password: adminPassword,
        visitor_password: visitorPassword || '',
      })
      .then((r) => r.data),

  // 访客认证（防爬虫门禁）
  visitorLogin: (password) =>
    http.post('/api/visitor/login', { password }).then((r) => {
      setVisitorToken(r.data.token)
      return r.data
    }),
}

export function errMsg(e) {
  return e?.response?.data?.error || e?.message || '请求失败'
}

// 401 = token 无效/过期：拦截器已静默跳登录页，业务侧不应再弹任何提示
export function isAuthError(e) {
  return e?.response?.status === 401
}

// 统一错误提示：401 静默（交给拦截器跳转），其它用 toast（不弹窗）
export function errNotify(e) {
  if (isAuthError(e)) return
  toastError(errMsg(e))
}

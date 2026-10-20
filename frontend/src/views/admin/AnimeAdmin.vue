<script setup>
import { ref, onMounted, onBeforeUnmount, computed, watch } from 'vue'
import { useRouter } from 'vue-router'
import { api, errMsg, errNotify, isAuthError } from '../../api'
import { toastError, toastSuccess } from '../../notify'
import FeedBadge from '../../components/FeedBadge.vue'
import Paginate from '../../components/Paginate.vue'
import BulkDiscoverModal from '../../components/BulkDiscoverModal.vue'

const router = useRouter()

const animes = ref([])
const q = ref('')
const year = ref(Number(localStorage.getItem('la_admin_year')) || 0) // 0 = 全部（记忆上次筛选）
const quarter = ref(Number(localStorage.getItem('la_admin_quarter')) || 0) // 0 = 全部
const onlyFeeded = ref(false) // 只显示有 RSS 源的番剧
const loading = ref(false)

// 年份选项：当前年 → 1990
const years = computed(() => {
  const cur = new Date().getFullYear()
  const out = []
  for (let y = cur; y >= 1990; y--) out.push(y)
  return out
})
const quarterOpts = [
  { label: '全部季度', value: 0 },
  { label: '1月', value: 1 },
  { label: '4月', value: 2 },
  { label: '7月', value: 3 },
  { label: '10月', value: 4 },
]

// 分页/筛选持久化：页码、每页数量、年份、季度都落 localStorage——
// 进编辑页再「返回」时落回之前的页，下次打开浏览器也保持上次的状态
const page = ref(Math.max(1, Number(localStorage.getItem('la_admin_page')) || 1))
const pageSize = ref(Number(localStorage.getItem('la_admin_psize')) || 20)
const total = ref(0)
const debounce = ref(null)

function persistAdmin() {
  localStorage.setItem('la_admin_psize', String(pageSize.value))
  localStorage.setItem('la_admin_page', String(page.value))
  localStorage.setItem('la_admin_year', String(year.value))
  localStorage.setItem('la_admin_quarter', String(quarter.value))
}
watch([page, pageSize, year, quarter], persistAdmin)

function setPageSize(v) {
  pageSize.value = v
  localStorage.setItem('la_admin_psize', String(v))
  if (page.value !== 1) {
    page.value = 1 // watch(page) 触发查询
  } else {
    refresh()
  }
}

// 翻页 → 重新拉取当前页
watch(page, refresh)

function listParams() {
  const p = { sort: 'latest', limit: pageSize.value, offset: (page.value - 1) * pageSize.value }
  if (year.value) p.year = year.value
  if (quarter.value) p.quarter = quarter.value
  if (q.value) p.q = q.value
  if (onlyFeeded.value) p.has_feeds = 1
  return p
}

async function refresh() {
  loading.value = true
  try {
    const p = listParams()
    const [list, cnt] = await Promise.all([api.adminListAnime(p), api.countAnime({ ...p, sort: undefined, limit: undefined, offset: undefined })])
    animes.value = list
    total.value = cnt
    // 当前页越界 → 回退到最后一页（watch(page) 会自动重查）
    const pages = Math.max(1, Math.ceil(total.value / pageSize.value))
    if (page.value > pages) {
      page.value = pages
    }
  } catch (e) {
    errNotify(e)
  } finally {
    loading.value = false
  }
}
onMounted(refresh)
onBeforeUnmount(() => {
  if (archiveTimer) {
    clearInterval(archiveTimer)
    archiveTimer = null
  }
  if (dlTimer) clearInterval(dlTimer)
  if (dlArmTimer) clearTimeout(dlArmTimer)
})

// 搜索防抖回第 1 页；年份/季度变化立即回第 1 页
watch(
  () => q.value,
  () => {
    page.value = 1
    clearTimeout(debounce.value)
    debounce.value = setTimeout(() => {
      if (page.value !== 1) {
        page.value = 1 // watch(page) 触发查询
      } else {
        refresh()
      }
    }, 300)
  }
)
watch([year, quarter, onlyFeeded], () => {
  if (page.value !== 1) {
    page.value = 1
  } else {
    refresh()
  }
})

// ---------- RSS 批量发现（v0.3.0） ----------
const showBulk = ref(false)

// ---------- 导入番剧 ----------
const showImport = ref(false)
const importSource = ref('bangumi')
const importQuery = ref('')
const importing = ref(false)
const importResult = ref('')
async function doImport() {
  if (!importQuery.value) return
  importing.value = true
  importResult.value = ''
  try {
    const a = await api.adminImport(importSource.value, importQuery.value)
    importResult.value = `✅ 已导入：${a.title}（id=${a.id}）`
    await refresh()
  } catch (e) {
    importResult.value = '❌ ' + errMsg(e)
  } finally {
    importing.value = false
  }
}

// ---------- 归档批量导入（Bangumi Archive jsonlines） ----------
const showArchive = ref(false)
const curYear = new Date().getFullYear()
// 归档目录：默认空，用过后记忆在浏览器（localStorage）；每次仍可修改
const archiveDir = ref(localStorage.getItem('la_archive_dir') || '')
const archiveYearFrom = ref(curYear)
const archiveYearTo = ref(curYear)
const archivePlatforms = ref([1]) // 动画类型多选（tag 式）：1=TV 2=电影 3=OVA 4=特别篇 5=Web；空=全部
const platformOpts = [
  { label: 'TV', value: 1 },
  { label: '电影', value: 2 },
  { label: 'OVA', value: 3 },
  { label: '特别篇', value: 4 },
  { label: 'Web', value: 5 },
]
function togglePlatform(v) {
  const arr = [...archivePlatforms.value]
  const i = arr.indexOf(v)
  if (i >= 0) {
    arr.splice(i, 1)
  } else {
    arr.push(v)
    arr.sort((a, b) => a - b)
  }
  archivePlatforms.value = arr
}
const archiveCountries = ref(['日本']) // 国家/地区多选（tag 式，按 Bangumi 标签命中任一即入选）；空=全部
const countryOpts = ['日本', '中国', '美国', '韩国', '欧美']
function toggleCountry(v) {
  const arr = [...archiveCountries.value]
  const i = arr.indexOf(v)
  if (i >= 0) {
    arr.splice(i, 1)
  } else {
    arr.push(v)
  }
  archiveCountries.value = arr
}
const archiveCovers = ref(true) // 导入后按 bangumi id 拉封面

// ---- 数据源：本地目录 or 自动下载最新归档（Docker 部署访问不了宿主目录）----
const archiveSource = ref(['local', 'remote'].includes(localStorage.getItem('la_archive_source')) ? localStorage.getItem('la_archive_source') : 'local')
const remoteLatest = ref(null) // GitHub 最新归档信息
const remoteLatestErr = ref('')
const remoteCache = ref(null) // 本机已缓存归档（服务端数据目录 archive/ 下）
const dlBusy = ref(false)
const dlSt = ref(null)
let dlTimer = null
const dlArmed = ref(false) // 重新下载二次确认（内联，同补封面交互）
let dlArmTimer = null

function onSourceChange(v) {
  localStorage.setItem('la_archive_source', v)
  if (v === 'remote') loadArchiveLatest()
}

async function loadArchiveLatest() {
  try {
    const d = await api.adminArchiveLatest()
    remoteLatest.value = d.latest
    remoteLatestErr.value = d.latest_err || ''
    remoteCache.value = d.cache
  } catch (e) {
    remoteLatestErr.value = errMsg(e)
  }
}

const dlPct = computed(() => {
  const s = dlSt.value
  if (!s || !s.total_bytes) return 0
  return Math.min(100, Math.round((s.got_bytes / s.total_bytes) * 100))
})
function dlPhaseLabel(p) {
  return { fetching: '下载归档…', extracting: '解压归档…', done: '已完成', error: '出错' }[p] || '准备中…'
}

function dlPoll() {
  api
    .adminArchiveDownloadStatus()
    .then((d) => {
      dlSt.value = d.download
      remoteCache.value = d.cache
      if (d.download.running) return
      if (dlTimer) {
        clearInterval(dlTimer)
        dlTimer = null
      }
      dlBusy.value = false
      if (d.download.phase === 'error') {
        toastError(`归档下载失败：${d.download.error}`)
      } else if (d.download.phase === 'done') {
        toastSuccess('最新归档已下载解压完成，可直接开始导入')
      }
    })
    .catch(() => {})
}

async function startDL(force) {
  dlArmed.value = false
  try {
    const res = await api.adminArchiveDownload(force)
    if (res.reused) {
      toastSuccess('已有缓存归档，直接复用即可开始导入')
      return
    }
    if (!res.started) return
  } catch (e) {
    toastError(errMsg(e))
    return
  }
  dlBusy.value = true
  dlSt.value = null
  dlPoll()
  dlTimer = setInterval(dlPoll, 1500)
}

// 重新下载会删除旧归档：第一次点击进入确认态，3 秒内再点才执行
function armRedownload() {
  if (dlArmed.value) {
    startDL(true)
    return
  }
  dlArmed.value = true
  dlArmTimer = setTimeout(() => (dlArmed.value = false), 3000)
}

function fmtMB(n) {
  if (!n) return ''
  return n >= 1073741824 ? (n / 1073741824).toFixed(1) + ' GB' : Math.round(n / 1048576) + ' MB'
}
function fmtDT(s) {
  return (s || '').slice(0, 16).replace('T', ' ')
}

// 打开归档导入弹窗且数据源为"下载最新归档"时，拉取最新归档信息 + 缓存状态
watch(showArchive, (v) => {
  if (v && archiveSource.value === 'remote') loadArchiveLatest()
})

const archYears = []
for (let y = curYear; y >= 2000; y--) archYears.push(y)
const archYearOpts = archYears.map((y) => ({ label: String(y), value: y }))

const archiveBusy = ref(false)
const archiveSt = ref(null) // 后端状态快照
let archiveTimer = null

function archivePoll() {
  api
    .adminArchiveStatus()
    .then((st) => {
      archiveSt.value = st
      if (st.running) return
      // 任务结束
      if (archiveTimer) {
        clearInterval(archiveTimer)
        archiveTimer = null
      }
      archiveBusy.value = false
      if (st.phase === 'error') {
        toastError(`归档导入失败：${st.error}`)
      } else {
        const cf = st.covers_failed ? `，封面 ${st.covers_done}/${st.covers_total}（${st.covers_failed} 失败）` : ''
        toastSuccess(`归档导入完成：新增 ${st.imported} 部，跳过已存在 ${st.skipped} 部${cf}`)
      }
      refresh()
    })
    .catch(() => {})
}

async function startArchive() {
  let dir
  if (archiveSource.value === 'remote') {
    dir = remoteCache.value?.dir || ''
    if (!dir) {
      toastError('尚未下载最新归档（或缓存已失效），请先点「下载最新归档」')
      return
    }
  } else {
    dir = archiveDir.value.trim()
    if (!dir) {
      toastError('请先填写归档目录（bangumi/Archive 导出包所在文件夹）')
      return
    }
    localStorage.setItem('la_archive_dir', dir)
  }
  if (archiveYearTo.value < archiveYearFrom.value) {
    toastError('结束年份不能早于起始年份')
    return
  }
  archiveSt.value = null
  try {
    await api.adminImportArchive({
      dir,
      year_from: archiveYearFrom.value,
      year_to: archiveYearTo.value,
      platforms: archivePlatforms.value,
      countries: archiveCountries.value,
      fetch_covers: archiveCovers.value,
    })
  } catch (e) {
    toastError(errMsg(e))
    return
  }
  archiveBusy.value = true
  archivePoll()
  archiveTimer = setInterval(archivePoll, 1500)
}

// ---------- 批量补拉缺失封面（有 bangumi_id 但封面为空的番剧） ----------
const coverBusy = ref(false)
const coverSt = ref(null)
let coverTimer = null
// 内联二次确认：第一次点击进入确认态，3 秒内再点才执行（批量任务较久，避免误触）
const coverArmed = ref(false)
let coverArmTimer = null
function armCover() {
  if (coverArmed.value) {
    startCoverFetch()
    return
  }
  coverArmed.value = true
  coverArmTimer = setTimeout(() => (coverArmed.value = false), 3000)
}
function coverPoll() {
  api
    .adminCoverStatus()
    .then((st) => {
      coverSt.value = st
      if (st.running) return
      if (coverTimer) {
        clearInterval(coverTimer)
        coverTimer = null
      }
      coverBusy.value = false
      if (st.error) {
        toastError(`补封面失败：${st.error}`)
      } else if (st.total === 0) {
        toastSuccess('没有缺封面的番剧')
      } else {
        const f = st.failed ? `，${st.failed} 部失败` : ''
        toastSuccess(`补封面完成：${st.total - st.failed}/${st.total} 部成功${f}`)
      }
      refresh()
    })
    .catch(() => {})
}
async function startCoverFetch() {
  coverArmed.value = false
  if (coverArmTimer) {
    clearTimeout(coverArmTimer)
    coverArmTimer = null
  }
  coverSt.value = null
  try {
    await api.adminFetchCovers()
  } catch (e) {
    toastError(errMsg(e))
    return
  }
  coverBusy.value = true
  coverPoll()
  coverTimer = setInterval(coverPoll, 2000)
}
onBeforeUnmount(() => {
  if (coverTimer) clearInterval(coverTimer)
})

function archivePhaseLabel(p) {
  return {
    subjects: '解析归档（subject.jsonlines）…',
    characters: '解析角色关联…',
    insert: '批量写入数据库…',
    covers: '拉取封面…',
    done: '已完成',
    error: '出错',
  }[p] || p || '准备中…'
}

// ---------- 编辑番剧（跳转完整编辑页） ----------
function openEdit(a) {
  router.push(`/admin/anime/edit/${a.id}`)
}
// 内联二次确认（替代 confirm 弹窗）：第一次点「删除」进入确认态，3 秒内再点才真正执行
const confirmDel = ref(null) // { kind: 'anime'|'feed', id }
let confirmTimer = null
function isArmed(kind, id) {
  return !!confirmDel.value && confirmDel.value.kind === kind && confirmDel.value.id === id
}
function armDelete(kind, id) {
  if (isArmed(kind, id)) {
    confirmDel.value = null
    if (confirmTimer) clearTimeout(confirmTimer)
    return doDelete(kind, id)
  }
  confirmDel.value = { kind, id }
  if (confirmTimer) clearTimeout(confirmTimer)
  confirmTimer = setTimeout(() => (confirmDel.value = null), 3000)
}
function remove(a) {
  armDelete('anime', a.id)
}
async function doDelete(kind, id) {
  try {
    if (kind === 'anime') {
      // 删除番剧：其名下所有 RSS 源及更新记录一并删除
      await api.adminDeleteAnime(id)
      expandedId.value = null
    } else {
      // 删除 RSS 源：其历史更新记录一并删除
      await api.adminDeleteFeed(id)
      const f = Object.values(feedsOf.value).flat().find((x) => x.id === id)
      if (f?.anime_id) reloadFeedsOf(f.anime_id)
    }
    await refresh()
  } catch (e) {
    errNotify(e)
  }
}

// ---------- 番剧下属 RSS 源 ----------
const expandedId = ref(null)
const feedsOf = ref({}) // animeId -> feeds
const fetching = ref({}) // feedId -> bool
const fetchingAllA = ref({}) // animeId -> bool

async function toggleExpand(a) {
  if (expandedId.value === a.id) {
    expandedId.value = null
    return
  }
  expandedId.value = a.id
  if (!feedsOf.value[a.id]) {
    feedsOf.value = {
      ...feedsOf.value,
      [a.id]: await api.getAnimeFeeds(a.id).catch(() => []),
    }
  }
}
function reloadFeedsOf(aid) {
  api.getAnimeFeeds(aid).then((f) => (feedsOf.value = { ...feedsOf.value, [aid]: f }))
}
function feedsOfA(aid) {
  return feedsOf.value[aid] || []
}

async function fetchOne(f) {
  fetching.value = { ...fetching.value, [f.id]: true }
  try {
    await api.adminFetchOne(f.id)
  } catch (e) {
    if (!isAuthError(e)) toastError(`抓取失败：${errMsg(e)}`)
  } finally {
    fetching.value = { ...fetching.value, [f.id]: false }
    const aid = f.anime_id
    if (aid) reloadFeedsOf(aid)
    await refresh()
  }
}
async function fetchAllOf(a) {
  fetchingAllA.value = { ...fetchingAllA.value, [a.id]: true }
  try {
    await api.adminFetchAll()
    reloadFeedsOf(a.id)
  } catch (e) {
    errNotify(e)
  } finally {
    fetchingAllA.value = { ...fetchingAllA.value, [a.id]: false }
    await refresh()
  }
}

// 在番剧下新增 RSS 源
const showFeedForm = ref(false)
const feedForm = ref(null)
function openFeedForm(a, preset = {}) {
  feedForm.value = {
    id: preset.id || null, // 有 id = 编辑已有源（PUT），无 id = 新增（POST）
    animeId: a.id,
    name: preset.name || '',
    rss_url: preset.rss_url || '',
    base_url: preset.base_url || '',
    priority: preset.priority || 100,
    enabled: preset.enabled !== false,
    mirror: preset.mirror === true,
    color: preset.color || '#5b8cff',
  }
  showFeedForm.value = true
}
async function saveFeed() {
  const f = feedForm.value
  const body = {
    name: f.name,
    rss_url: f.rss_url,
    base_url: f.base_url,
    priority: f.priority,
    enabled: f.enabled,
    mirror: f.mirror,
    color: f.color,
    anime_id: f.animeId,
  }
  try {
    if (f.id) {
      await api.adminUpdateFeed(f.id, body)
    } else {
      await api.adminCreateFeed(body)
    }
    showFeedForm.value = false
    reloadFeedsOf(f.animeId)
    await refresh()
  } catch (e) {
    errNotify(e)
  }
}
function removeFeed(f) {
  armDelete('feed', f.id)
}
function toggleFeedEnabled(f, v) {
  api.adminUpdateFeed(f.id, {
    name: f.name,
    rss_url: f.rss_url,
    base_url: f.base_url || '',
    priority: f.priority || 100,
    enabled: v,
    mirror: f.mirror === true,
    color: f.color || '',
    anime_id: f.anime_id || 0,
  }).then(() => {
    if (f.anime_id) reloadFeedsOf(f.anime_id)
    refresh()
  })
}
function saveFeedPriority(f, v) {
  if (!v || v < 1) return
  api.adminUpdateFeed(f.id, {
    name: f.name,
    rss_url: f.rss_url,
    base_url: f.base_url || '',
    priority: v,
    enabled: f.enabled,
    mirror: f.mirror === true,
    color: f.color || '',
    anime_id: f.anime_id || 0,
  }).then(() => {
    if (f.anime_id) reloadFeedsOf(f.anime_id)
    refresh()
  })
}
function toggleFeedMirror(f, v) {
  api.adminUpdateFeed(f.id, {
    name: f.name,
    rss_url: f.rss_url,
    base_url: f.base_url || '',
    priority: f.priority || 100,
    enabled: f.enabled,
    mirror: v,
    color: f.color || '',
    anime_id: f.anime_id || 0,
  }).then(() => {
    if (f.anime_id) reloadFeedsOf(f.anime_id)
    refresh()
  })
}

// ---------- 自动发现 RSS 源（v0.2.0：后台 disc 算法，关页不中断） ----------
const showDiscover = ref(false)
const discoverA = ref(null) // 当前发现目标番剧
const discSt = ref(null) // 最近一次任务快照（status: running/done/error）
const discStarting = ref(false) // 启动请求进行中
const discExtra = ref('') // 人工补充宽搜词（逗号分隔）
const discNoVerify = ref(false)
const discAutoAdd = ref(false) // 发现完成后自动添加合格候选为 RSS 源（重复组自动跳过）
const discSelected = ref({}) // "组名|url" -> true
const applying = ref(false)
const applyResult = ref(null) // { created, skipped }
let discTimer = null

const discRunning = computed(() => discSt.value?.status === 'running')

function candKey(c) {
  return c.group + '|' + (c.url || '#' + (c.variant || ''))
}

// 默认勾选：有地址 + 置信非 low + 纯度 100%（回验纯净）
function isAutoSelected(c) {
  return !!c.url && c.confidence !== 'low' && (c.purity == null || c.purity >= 1)
}
function refreshSelection() {
  const st = discSt.value
  if (!st || st.status !== 'done') return
  const sel = { ...discSelected.value }
  for (const c of st.candidates || []) {
    const k = candKey(c)
    if (!(k in sel) && isAutoSelected(c)) sel[k] = true
  }
  discSelected.value = sel
}
function toggleCand(c, v) {
  const sel = { ...discSelected.value }
  if (v) sel[candKey(c)] = true
  else delete sel[candKey(c)]
  discSelected.value = sel
}
const selectedCount = computed(() => {
  const st = discSt.value
  if (!st || st.status !== 'done') return 0
  let n = 0
  for (const c of st.candidates || []) {
    if ((c.keyword || c.url) && discSelected.value[candKey(c)]) n++
  }
  return n
})

// 表头全选/取消全选（只作用于可建源的候选：有关键词或地址）
const discSelectable = computed(() => {
  const st = discSt.value
  if (!st || st.status !== 'done') return []
  return (st.candidates || []).filter((c) => c.keyword || c.url)
})
const discAllChecked = computed(() => {
  const list = discSelectable.value
  return list.length > 0 && list.every((c) => discSelected.value[candKey(c)])
})
const discSomeChecked = computed(() => {
  const list = discSelectable.value
  return !discAllChecked.value && list.some((c) => discSelected.value[candKey(c)])
})
function setAllCands(v) {
  const sel = { ...discSelected.value }
  for (const c of discSelectable.value) {
    if (v) sel[candKey(c)] = true
    else delete sel[candKey(c)]
  }
  discSelected.value = sel
}

function stopDiscPoll() {
  if (discTimer) {
    clearInterval(discTimer)
    discTimer = null
  }
}

async function loadDiscStatus() {
  try {
    const d = await api.adminDiscoveryStatus(discoverA.value.id)
    discSt.value = d
    if (d.status === 'running' && !discTimer) {
      // 上次开的任务还在跑（关页/关弹窗过）→ 恢复轮询
      discStarting.value = false
      discPoll()
      discTimer = setInterval(discPoll, 2000)
    }
    refreshSelection()
  } catch (e) {
    if (!isAuthError(e)) discSt.value = null // 404 = 未发现过
  }
}

function openDiscover(a) {
  discoverA.value = a
  discSt.value = null
  discStarting.value = false
  discExtra.value = ''
  discNoVerify.value = false
  discAutoAdd.value = false
  discSelected.value = {}
  applyResult.value = null
  showDiscover.value = true
  loadDiscStatus()
}

function discPoll() {
  if (!discoverA.value) return
  api
    .adminDiscoveryStatus(discoverA.value.id)
    .then((d) => {
      discStarting.value = false
      discSt.value = d
      if (d.status === 'running') return
      // 任务结束
      stopDiscPoll()
      refreshSelection()
      if (d.status === 'error') {
        toastError(`自动发现失败：${d.error || '未知错误'}`)
      } else if (discAutoAdd.value) {
        // 自动添加已完成（结果摘要在报告 notes 里）：刷新番剧源列表
        reloadFeedsOf(discoverA.value.id)
        refresh()
        const note = (d.notes || []).find((n) => n.startsWith('已自动添加'))
        toastSuccess(note || '自动发现完成，已自动添加 RSS 源')
      } else if ((d.candidates || []).length) {
        toastSuccess(`自动发现完成：${d.candidates.length} 个流派候选`)
      }
    })
    .catch(() => {})
}

function startDiscover() {
  if (!discoverA.value || discStarting.value || discRunning.value) return
  const queries = discExtra.value
    .split(/[,，|;]/)
    .map((s) => s.trim())
    .filter(Boolean)
  discStarting.value = true
  discSt.value = { status: 'running', phase: '启动中…' }
  api
    .adminDiscoverRss(discoverA.value.id, { queries, no_verify: discNoVerify.value, auto_add: discAutoAdd.value })
    .then(() => {
      discPoll()
      if (!discTimer) discTimer = setInterval(discPoll, 2000)
    })
    .catch((e) => {
      stopDiscPoll()
      discStarting.value = false
      discSt.value = null
      errNotify(e)
    })
}

function closeDiscover() {
  showDiscover.value = false
  stopDiscPoll()
  discStarting.value = false
  // 注意：后台任务不中断，重开弹窗时轮询状态续上
}

async function applySelected() {
  const st = discSt.value
  if (!st || st.status !== 'done' || !selectedCount.value || applying.value) return
  applying.value = true
  applyResult.value = null
  try {
    const items = (st.candidates || [])
      .filter((c) => (c.keyword || c.url) && discSelected.value[candKey(c)])
      .map((c) => ({ group: c.group, keyword: c.keyword, url: c.url, name: c.group, priority: 100, mirror: false, color: '' }))
    const res = await api.adminApplyDiscovery(discoverA.value.id, { report_id: st.id, items })
    applyResult.value = res
    const n = (res.created || []).length
    if (n) {
      toastSuccess(`已创建 ${n} 个 RSS 源（含兄弟站镜像）`)
      reloadFeedsOf(discoverA.value.id)
      refresh()
    } else {
      toastSuccess('无新增源（所选均已存在）')
    }
  } catch (e) {
    errNotify(e)
  } finally {
    applying.value = false
  }
}

function confLabel(c) {
  return { high: '高置信', medium: '中置信', low: '低置信' }[c] || c
}
function confTip(c) {
  return {
    high: '高置信：回验 feed 纯度 100% 且覆盖过半，可直接建源',
    medium: '中置信：回验基本通过，建议看一眼纯度/覆盖再决定',
    low: '低置信：纯度不足/未回验/回验无地址，请人工确认后再建源',
  }[c] || ''
}
function pct(x) {
  return x == null ? '—' : Math.round(x * 100) + '%'
}
// 集数紧凑化：连续区间 → "1~14"；零散少量 → "1,2,5"；零散大量 → 省略 + 总数（完整列表走 tooltip）
function epsLabel(eps) {
  if (!eps || !eps.length) return '未知'
  const isRange = eps.every((v, i) => i === 0 || v === eps[i - 1] + 1)
  if (isRange) {
    return eps.length > 1 ? `${eps[0]}~${eps[eps.length - 1]}` : `${eps[0]}`
  }
  if (eps.length <= 6) return eps.join(',')
  return `${eps.slice(0, 3).join(',')}…（共 ${eps.length} 集）`
}
onBeforeUnmount(stopDiscPoll)

// 集数展示：12 → "12"，12.5（总集篇）→ "12.5"，0（前篇）→ "0"
function epText(x) {
  return Number.isInteger(x) ? String(x) : String(Math.round(x * 10) / 10)
}

function fmtTime(s) {
  if (!s) return '—'
  const d = new Date(s)
  if (isNaN(d)) return s
  const p = (n) => String(n).padStart(2, '0')
  return `${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}
</script>

<template>
  <div class="page">
      <div class="page-head">
        <h2><span class="h-ico">▤</span> 番剧管理</h2>
        <div class="head-actions">
          <n-button size="small" @click="showImport = true">⚡ 快速导入</n-button>
          <n-button size="small" @click="showArchive = true">📦 归档批量导入</n-button>
          <n-button
            size="small"
            :type="coverArmed ? 'warning' : 'default'"
            :loading="coverBusy"
            :title="coverArmed ? '再点一次确认执行（批量任务，需联网）' : '为所有缺封面的番剧按 Bangumi ID 批量补拉封面'"
            @click="armCover"
          >
            {{ coverBusy ? `补封面 ${coverSt ? coverSt.done + '/' + coverSt.total : '…'}` : coverArmed ? '确认补封面？' : '🖼 补拉缺失封面' }}
          </n-button>
          <n-button
            size="small"
            title="对选中的一批番剧串行执行 RSS 自动发现并导入（后台任务，可随时停止）"
            @click="showBulk = true"
          >
            📡 RSS 批量发现
          </n-button>
          <n-button size="small" type="primary" class="grad-btn" @click="router.push('/admin/anime/new')">
            ＋ 新建番剧
          </n-button>
        </div>
        <div class="head-filters">
          <n-select
            v-model:value="year"
            :options="[{ label: '全部年份', value: 0 }, ...years.map((y) => ({ label: String(y), value: y }))]"
            size="small"
            style="width: 105px"
          />
          <n-select v-model:value="quarter" :options="quarterOpts" size="small" style="width: 105px" />
          <n-input v-model:value="q" placeholder="搜索番剧 / 关键词" clearable size="small" class="head-search" />
          <n-button
            size="small"
            quaternary
            :type="onlyFeeded ? 'primary' : 'default'"
            class="has-eps-toggle"
            :title="onlyFeeded ? '点击关闭：同时显示没有 RSS 源的番剧' : '点击开启：只显示名下有 RSS 源的番剧'"
            @click="onlyFeeded = !onlyFeeded"
          >
            {{ onlyFeeded ? '✓ ' : '＋ ' }}只看有 RSS 源
          </n-button>
          <n-button size="small" @click="refresh">刷新</n-button>
        </div>
      </div>
      <p class="hint">
        点「新建番剧」进入完整编辑页（图片可上传，文件自动存储/清理）；「快速导入」按 Bangumi ID 拉快照。
        每部番剧展开后管理它名下的 <b>RSS 源</b>（优先级在番剧内独立生效，越小越优先）；删除番剧会连同它的 RSS 源一并删除。
      </p>

      <n-spin :show="loading" description="加载中…">
        <div class="anime-list">
        <div v-if="!animes.length" class="empty">
          <div class="empty-ico">▤</div>
          还没有番剧。点右上角「导入番剧」，输入 Bangumi ID（如 622288）即可拉取快照。
        </div>

        <div v-for="a in animes" :key="a.id" class="anime-row">
          <div class="anime-main" @click="toggleExpand(a)">
            <div class="chev" :class="{ open: expandedId === a.id }">▶</div>
            <img v-if="a.cover" class="row-cover" :src="'/' + a.cover" :alt="a.title" />
            <div v-else class="row-cover placeholder">{{ (a.title || '?').slice(0, 1) }}</div>
            <div class="row-info">
              <div class="row-title">
                <router-link :to="`/anime/${a.id}`" @click.stop>{{ a.title }}</router-link>
                <span v-if="a.title_orig" class="dim"> / {{ a.title_orig }}</span>
              </div>
              <div class="row-sub dim">
                <span v-if="a.season_label">{{ a.season_label }}</span>
                <span v-else-if="a.year">{{ a.year }}<span v-if="a.quarter">-Q{{ a.quarter }}</span></span>
                <span> · #{{ a.id }}</span>
              </div>
            </div>
            <div class="row-feedcount">
              <span class="feed-dot"></span>
              {{ a.feed_count || '—' }} 个源
            </div>
            <div class="row-prog">
              <template v-if="a.latest_episode">
                <span class="prog-ep">更新至 {{ epText(a.latest_episode) }}</span>
                <FeedBadge v-if="a.latest_group" :name="a.latest_group" :color="a.latest_group_color" />
              </template>
              <span v-else class="dim">暂无更新</span>
            </div>
            <div class="row-acts" @click.stop>
              <n-button size="tiny" quaternary @click="openEdit(a)">编辑</n-button>
              <n-button size="tiny" quaternary type="error" :class="{ 'btn-armed': isArmed('anime', a.id) }" @click="remove(a)">
                  {{ isArmed('anime', a.id) ? '确认删除？(将删除全部源与记录)' : '删除' }}
                </n-button>
            </div>
          </div>

          <!-- 展开：该番剧的 RSS 源管理 -->
          <div v-if="expandedId === a.id" class="anime-feeds">
            <div class="feeds-head">
              <span class="feeds-title">RSS 源（按优先级，数字越小越优先）</span>
              <div class="feeds-actions">
                <n-button
                  size="tiny"
                  title="让系统按番剧名/别名自动在订阅站搜索各字幕组，并回验出纯净的 per-show 订阅地址"
                  @click="openDiscover(a)"
                >
                  🔍 自动发现 RSS
                </n-button>
                <n-button size="tiny" :loading="fetchingAllA[a.id]" @click="fetchAllOf(a)">全部抓取</n-button>
                <n-button size="tiny" type="primary" @click="openFeedForm(a)">＋ 添加 RSS 源</n-button>
              </div>
            </div>

            <div v-if="!feedsOfA(a.id).length" class="feeds-empty">
              该番剧还没有 RSS 源。点「🔍 自动发现 RSS」让系统自动搜索各字幕组并回验订阅地址，
              勾选后一键批量建源；也可以手动「添加 RSS 源」，填字幕组订阅地址
              （如 <code>https://…/rss-…&lt;制作组&gt;.xml</code> ），后台每 3 分钟自动轮询。
            </div>

            <table v-else class="tbl">
              <thead>
                <tr>
                  <th>名称</th>
                  <th>优先级</th>
                  <th>RSS 地址</th>
                  <th>启用</th>
                  <th>镜像</th>
                  <th>最近抓取</th>
                  <th>状态</th>
                  <th>操作</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="f in feedsOfA(a.id)" :key="f.id">
                  <td>
                    <FeedBadge :name="f.name" :color="f.color" />
                    <span
                      v-if="f.site || f.keyword"
                      class="site-tag"
                      :title="f.keyword
                        ? `逻辑源：抓取时按全局站点池自动展开各站（关键词：${f.keyword}）${f.site ? `；建源时参考站点 ${f.site}` : ''}`
                        : '手动源，单站'"
                    >{{ f.keyword ? '站点池' : f.site }}</span>
                  </td>
                  <td>
                    <n-input-number
                      :value="f.priority"
                      size="tiny"
                      :min="1"
                      style="width: 80px"
                      @update:value="(v) => saveFeedPriority(f, v)"
                    />
                  </td>
                  <td class="url" :title="f.rss_url">{{ f.rss_url }}</td>
                  <td>
                    <n-switch :value="f.enabled" size="small" @update:value="(v) => toggleFeedEnabled(f, v)" />
                  </td>
                      <td>
                        <n-switch :value="f.mirror === true" size="small" @update:value="(v) => toggleFeedMirror(f, v)" />
                      </td>
                  <td class="dim time">{{ fmtTime(f.last_fetched_at) }}</td>
                  <td>
                    <span v-if="f.last_error" class="st-err" :title="f.last_error">失败</span>
                    <span v-else-if="f.last_fetched_at" class="st-ok">正常</span>
                    <span v-else class="dim">未抓取</span>
                  </td>
                  <td class="acts">
                    <n-button size="tiny" quaternary type="primary" :loading="fetching[f.id]" @click="fetchOne(f)">
                      抓取
                    </n-button>
                    <n-button size="tiny" quaternary @click="openFeedForm(a, f)">编辑</n-button>
                    <n-button size="tiny" quaternary type="error" :class="{ 'btn-armed': isArmed('feed', f.id) }" @click="removeFeed(f)">
                      {{ isArmed('feed', f.id) ? '确认删除？' : '删除' }}
                    </n-button>
                  </td>
                </tr>
              </tbody>
            </table>

          </div>
        </div>
        </div>
      </n-spin>

      <!-- 分页（每页数量可在右侧调整） -->
      <Paginate
        v-if="total > 0"
        v-model:page="page"
        :page-size="pageSize"
        :total="total"
        :sizes="[10, 20, 50, 100]"
        @update:page-size="setPageSize"
      />

      <!-- 导入弹窗 -->
      <n-modal v-model:show="showImport" preset="card" title="导入番剧（元数据快照）" style="width: 480px">
        <div class="form">
          <label>
            元数据来源
            <n-select
              v-model:value="importSource"
              :options="[
                { label: 'Bangumi（按 ID 拉快照，自动下载封面/图片）', value: 'bangumi' },
                { label: '本地 JSON（本站 canonical 格式）', value: 'local-json' },
              ]"
            />
          </label>
          <label>
            {{ importSource === 'bangumi' ? 'Bangumi ID' : '本地 JSON 文件路径' }}
            <n-input
              v-model:value="importQuery"
              :placeholder="importSource === 'bangumi' ? '如 100001 / 100002' : '如 /Users/you/anime.json'"
            />
          </label>
          <div class="import-tip">
            不知道 ID？在 bangumi.tv 搜索番剧，URL 里的数字就是 ID（如 bangumi.tv/subject/622288）。导入后可在列表中「编辑」补充别名，提高 RSS 自动匹配率。
          </div>
          <div v-if="importResult" class="import-result">{{ importResult }}</div>
        </div>
        <template #footer>
          <n-space justify="end">
            <n-button @click="showImport = false">关闭</n-button>
            <n-button type="primary" class="grad-btn" :loading="importing" @click="doImport">导入</n-button>
          </n-space>
        </template>
      </n-modal>

      <!-- 归档批量导入弹窗（Bangumi Archive jsonlines） -->
      <n-modal
        v-model:show="showArchive"
        preset="card"
        title="归档批量导入（Bangumi Archive）"
        style="width: 560px"
      >
        <div class="form">
          <div class="plat-field">
            <span class="field-name">归档数据源</span>
            <n-radio-group v-model:value="archiveSource" :disabled="archiveBusy" @update:value="onSourceChange">
              <n-space>
                <n-radio value="local">本地目录</n-radio>
                <n-radio value="remote">自动下载最新归档</n-radio>
              </n-space>
            </n-radio-group>
          </div>
          <label v-if="archiveSource === 'local'">
            归档目录（bangumi/Archive 导出包，含 *.jsonlines）
            <n-input v-model:value="archiveDir" placeholder="bangumi/Archive 导出包所在文件夹（填写一次后自动记忆）" />
          </label>
          <div v-else class="remote-block">
            <div v-if="remoteLatestErr" class="import-tip">⚠️ {{ remoteLatestErr }}</div>
            <div v-if="remoteLatest" class="remote-line">
              最新归档：<b>{{ remoteLatest.name }}</b>
              <span class="hint">（{{ fmtMB(remoteLatest.size) }}{{ remoteLatest.created_at ? ' · 更新于 ' + remoteLatest.created_at.slice(5, 16).replace('T', ' ') : '' }}）</span>
            </div>
            <div v-if="remoteCache?.available" class="remote-line">
              ✅ 已缓存：{{ remoteCache.name }}（下载于 {{ fmtDT(remoteCache.downloaded_at) }}，可直接导入）
              <n-button size="tiny" quaternary :disabled="dlBusy" :class="{ armed: dlArmed }" @click="armRedownload">
                {{ dlArmed ? '再点一次确认重新下载（将删除旧归档）' : '↻ 重新下载' }}
              </n-button>
            </div>
            <div v-else-if="!dlSt" class="remote-line">
              <n-button size="small" type="primary" @click="startDL(false)">⬇ 下载最新归档</n-button>
              <span class="hint">下载并解压到服务端数据目录（约 400MB），Docker 部署无需宿主目录</span>
            </div>
            <!-- 下载进度 -->
            <div v-if="dlSt" class="archive-progress">
              <div class="archive-phase">
                <n-spin v-if="dlBusy" size="small" />
                <span>{{ dlPhaseLabel(dlSt.phase) }}</span>
                <span v-if="dlSt.phase === 'fetching' && dlSt.total_bytes">（{{ dlPct }}%）</span>
              </div>
              <n-progress v-if="dlSt.phase === 'fetching'" :percentage="dlPct" :show-indicator="false" :height="8" />
              <div v-if="dlSt.error" class="import-result">❌ {{ dlSt.error }}</div>
            </div>
          </div>
          <div class="form-row">
            <label>
              起始年份
              <n-select v-model:value="archiveYearFrom" :options="archYearOpts" style="width: 110px" />
            </label>
            <label>
              结束年份
              <n-select v-model:value="archiveYearTo" :options="archYearOpts" style="width: 110px" />
            </label>
          </div>
          <div class="plat-field">
            <span class="field-name">动画类型（点击多选；不选 = 全部类型）</span>
            <div class="plat-chips">
              <span
                v-for="o in platformOpts"
                :key="o.value"
                class="plat-chip"
                :class="{ on: archivePlatforms.includes(o.value) }"
                @click="togglePlatform(o.value)"
              >
                {{ o.label }}
              </span>
            </div>
          </div>
          <div class="plat-field">
            <span class="field-name">国家 / 地区（按 Bangumi 标签过滤，多选为“或”关系；不选 = 全部）</span>
            <div class="plat-chips">
              <span
                v-for="c in countryOpts"
                :key="c"
                class="plat-chip"
                :class="{ on: archiveCountries.includes(c) }"
                @click="toggleCountry(c)"
              >
                {{ c }}
              </span>
            </div>
          </div>
          <n-checkbox v-model:checked="archiveCovers">导入后自动拉取封面（按 Bangumi ID 走 API，需联网）</n-checkbox>
          <div class="import-tip">
            只导入 <b>动画</b> 分类条目，按 Bangumi ID <b>去重</b>：库中已存在的条目自动跳过，可反复重跑。
            归档不含图片，封面留空后可在详情页用「⚡ 拉取」补齐。
            官方归档数据（每周三更新，含 *.jsonlines）：<a href="https://github.com/bangumi/Archive" target="_blank" rel="noopener">github.com/bangumi/Archive</a>
            （Docker 部署拿不到宿主目录时，数据源选「自动下载最新归档」由服务端直接抓取）
          </div>

          <!-- 进度 -->
          <div v-if="archiveSt" class="archive-progress">
            <div class="archive-phase">
              <n-spin v-if="archiveBusy" size="small" />
              <span>{{ archivePhaseLabel(archiveSt.phase) }}</span>
            </div>
            <div class="archive-nums">
              <span>候选 {{ archiveSt.scanned }}</span>
              <span>新增 {{ archiveSt.imported }}</span>
              <span>跳过 {{ archiveSt.skipped }}</span>
              <span v-if="archiveSt.covers_total">
                封面 {{ archiveSt.covers_done }}/{{ archiveSt.covers_total }}
                <em v-if="archiveSt.covers_failed">（{{ archiveSt.covers_failed }} 失败）</em>
              </span>
            </div>
            <div v-if="archiveSt.error" class="import-result">❌ {{ archiveSt.error }}</div>
            <ul v-if="archiveSt.notes && archiveSt.notes.length" class="archive-notes">
              <li v-for="(n, i) in archiveSt.notes" :key="i">{{ n }}</li>
            </ul>
          </div>
        </div>
        <template #footer>
          <n-space justify="end">
            <n-button @click="showArchive = false">关闭</n-button>
            <n-button type="primary" class="grad-btn" :disabled="archiveBusy" @click="startArchive">
              {{ archiveBusy ? '导入中…' : '开始导入' }}
            </n-button>
          </n-space>
        </template>
      </n-modal>

      <!-- 番剧下新增/编辑 RSS 源 -->
      <n-modal
        v-model:show="showFeedForm"
        preset="card"
        :title="feedForm?.name ? `RSS 源：${feedForm.name}` : '添加 RSS 源'"
        style="width: 500px"
      >
        <div v-if="feedForm" class="form">
          <label>
            名称（制作组）
            <n-input v-model:value="feedForm.name" placeholder="RSS 源名称（一般填字幕组名）" />
          </label>
          <label>
            RSS 地址
            <n-input v-model:value="feedForm.rss_url" placeholder="https://…（订阅地址；部分站点要求特定后缀如 .xml）" />
          </label>
          <label>
            优先级（数字越小越优先，质量高的组给小数字；仅在本番剧内生效）
            <n-input-number v-model:value="feedForm.priority" :min="1" style="width: 100%" />
          </label>
          <label>
            徽标颜色
            <n-input v-model:value="feedForm.color" />
          </label>
          <div class="mirror-row">
            <n-switch v-model:value="feedForm.mirror" />
            <div class="mirror-text">
              <b>自动镜像资源文件</b>
              <span class="dim">开启后，新条目出现时后台自动把资源文件下载到本地；下载完成并通过校验后，前台下载链接无感切换为本地镜像（磁力等无法镜像的条目自动跳过）。</span>
            </div>
          </div>
        </div>
        <template #footer>
          <n-space justify="end">
            <n-button @click="showFeedForm = false">取消</n-button>
            <n-button type="primary" class="grad-btn" @click="saveFeed">保存</n-button>
          </n-space>
        </template>
      </n-modal>

      <!-- 自动发现 RSS 源（v0.2.0）：后台任务轮询，关弹窗不中断 -->
      <n-modal
        v-model:show="showDiscover"
        preset="card"
        :title="discoverA ? `自动发现 RSS · ${discoverA.title}` : '自动发现 RSS'"
        style="width: 800px"
        :mask-closable="false"
      >
        <div v-if="discoverA" class="form disc-modal">
          <!-- 启动区 -->
          <div class="disc-start">
            <div class="disc-target">
              <b>{{ discoverA.title }}</b>
              <span v-if="discoverA.title_orig" class="dim"> / {{ discoverA.title_orig }}</span>
              <span v-if="discoverA.season_label" class="dim"> · {{ discoverA.season_label }}</span>
              <span class="dim"> · #{{ discoverA.id }}</span>
            </div>
            <div class="disc-q">
              <n-input
                v-model:value="discExtra"
                size="small"
                :disabled="discStarting || discRunning"
                placeholder="人工补充宽搜关键词（逗号分隔；留空 = 系统按标题/别名自动生成）"
              />
            </div>
            <div class="disc-start-row">
              <n-checkbox
                v-model:checked="discNoVerify"
                :disabled="discStarting || discRunning"
                size="small"
              >
                跳过候选回验（更快，但不保证地址纯净）
              </n-checkbox>
              <n-checkbox
                v-model:checked="discAutoAdd"
                :disabled="discStarting || discRunning"
                size="small"
                title="发现完成后按「设置」的组合逻辑择优建源：黑名单命中的直接排除；其余按 偏好字幕组/关键词（越靠前越优先）→ 集数多 → 首发早 排序；数量在 最少~最多 之间（默认 1~2）；合格候选 = 有地址 ∧ 非低置信 ∧ 纯度 100%；已有同名字幕组的源自动跳过；新建源按名次分配优先级：排在已有源之后（起点 = 已有源最大优先级向上取整 +10，无已有源从 100 起），名次每后一位 +10，数值小 = 优先"
              >
                发现后自动添加 RSS 源
              </n-checkbox>
              <n-button
                size="small"
                type="primary"
                class="grad-btn"
                :loading="discStarting"
                :disabled="discRunning"
                @click="startDiscover"
              >
                {{ discSt?.status === 'done' ? '↻ 重新发现' : '🔍 开始发现' }}
              </n-button>
              <span class="dim disc-tip">约需 30 秒~2 分钟；任务在后台运行，关闭此窗不中断，回来重开即可续看</span>
            </div>
          </div>

          <!-- 进行中 -->
          <div v-if="discStarting" class="disc-run">
            <n-spin size="medium" />
            <span>正在启动任务…</span>
          </div>
          <div v-else-if="discRunning" class="disc-run">
            <n-spin size="medium" />
            <span>{{ discSt.phase || '搜索订阅站中…' }}</span>
            <span class="dim">（后台任务，关闭此窗不中断）</span>
          </div>
          <div v-else-if="discSt?.status === 'error'" class="disc-err">
            ❌ 发现失败：{{ discSt.error || '未知错误' }}
          </div>

          <!-- 结果 -->
          <div v-else-if="discSt?.status === 'done'" class="disc-result">
            <div class="disc-summary">
              <span v-if="discSt.site">站点 <b>{{ discSt.site }}</b></span>
              <span>扫描 <b>{{ discSt.scanned }}</b> 条</span>
              <span>归属命中 <b>{{ discSt.matched }}</b></span>
              <span>流派候选 <b>{{ (discSt.candidates || []).length }}</b> 个</span>
              <span v-if="(discSt.unmatched || []).length">
                未归属 <b>{{ discSt.unmatched.length }}</b> 条
              </span>
            </div>
            <div v-if="(discSt.queries || []).length" class="disc-queries">
              <span class="dim">宽搜关键词：</span>
              <span v-for="q in discSt.queries" :key="q" class="q-chip">{{ q }}</span>
            </div>
            <ul v-if="(discSt.notes || []).length" class="disc-notes">
              <li v-for="(n, i) in discSt.notes" :key="i">⚠ {{ n }}</li>
            </ul>

            <table v-if="(discSt.candidates || []).length" class="tbl disc-tbl">
              <colgroup>
                <col style="width: 38px" />
                <col style="width: 15%" />
                <col style="width: 11%" />
                <col style="width: 9%" />
                <col style="width: 8%" />
                <col style="width: 8%" />
                <col style="width: 15%" />
                <col />
              </colgroup>
              <thead>
                <tr>
                  <th class="c-sel">
                    <n-checkbox
                      :checked="discAllChecked"
                      :indeterminate="discSomeChecked"
                      @update:checked="setAllCands"
                    />
                  </th>
                  <th>字幕组</th>
                  <th>流派</th>
                  <th>置信</th>
                  <th>集数</th>
                  <th>节奏</th>
                  <th>纯度/覆盖</th>
                  <th>关键词</th>
                </tr>
              </thead>
              <tbody>
                <template v-for="c in discSt.candidates" :key="candKey(c)">
                  <tr :class="{ 'row-low': c.confidence === 'low' }">
                    <td class="c-sel">
                      <n-checkbox
                        :checked="!!discSelected[candKey(c)]"
                        :disabled="!c.keyword && !c.url"
                        @update:checked="(v) => toggleCand(c, v)"
                      />
                    </td>
                    <td class="group-cell"><FeedBadge :name="c.group" :warn="c.note" /></td>
                    <td class="dim variant" :title="c.variant || undefined">{{ c.variant || '—' }}</td>
                    <td>
                      <span class="conf" :class="c.confidence" :title="confTip(c.confidence)">{{ confLabel(c.confidence) }}</span>
                    </td>
                    <td class="dim nw" :title="c.episodes && c.episodes.length ? c.episodes.join(', ') : undefined">{{ epsLabel(c.episodes) }}</td>
                    <td class="dim nw">{{ c.pace || '—' }}</td>
                    <td class="dim nw" :title="c.url ? `纯度 ${pct(c.purity)}（回验 feed 内本组本番条目占比）｜覆盖 ${pct(c.coverage)}（已知集数被覆盖比例）` : '该候选未回验（无可用地址）'">
                      {{ c.url ? `${pct(c.purity)} / ${pct(c.coverage)}` : '未回验' }}
                    </td>
                    <td class="url" :title="c.keyword || c.url || undefined">{{ c.keyword || c.url || '—' }}</td>
                  </tr>
                </template>
              </tbody>
            </table>
            <div v-else class="feeds-empty">
              没有发现任何字幕组候选（该番可能在订阅站没有 per-show 源，或关键词未命中，可补充宽搜词后重试）。
            </div>

            <n-collapse v-if="(discSt.unmatched || []).length" :bordered="false" class="disc-un">
              <n-collapse-item
                :name="'un'"
                :title="`未归属 / 低置信条目（${discSt.unmatched.length}）— 人工确认区`"
                arrow-placement="right"
              >
                <div v-for="(it, i) in discSt.unmatched" :key="i" class="un-item">
                  <span class="dim">({{ it.score.toFixed(2) }})</span>
                  <span v-if="it.group" class="dim">[{{ it.group }}]</span>
                  {{ it.title }}
                  <a v-if="it.link" :href="it.link" target="_blank" rel="noopener" class="dim">打开</a>
                </div>
              </n-collapse-item>
            </n-collapse>

            <div v-if="applyResult" class="apply-res">
              <div v-if="(applyResult.created || []).length" class="apply-created">
                ✅ 已创建：
                <span v-for="f in applyResult.created" :key="f.id" class="apply-item">
                  {{ f.name }}（优先级 {{ f.priority }}）
                </span>
              </div>
              <div v-if="(applyResult.skipped || []).length" class="apply-skipped dim">
                ⏭ 跳过：
                <span v-for="(s, i) in applyResult.skipped" :key="i">{{ s.name || s.url }}（{{ s.reason }}）</span>
              </div>
            </div>
          </div>

          <div v-else class="disc-idle">
            还没发现过。点上方「🔍 开始发现」，系统会按本番剧的标题/原名/别名在订阅站宽搜，
            把条目按「字幕组 × 流派」分组，并为每个流派搜索并回验一个<b>纯净订阅地址</b>（feed 内只含该组该番的条目）。
          </div>
        </div>
        <template #footer>
          <n-space justify="end">
            <n-button @click="closeDiscover">关闭</n-button>
            <n-button
              v-if="discSt?.status === 'done'"
              type="primary"
              class="grad-btn"
              :loading="applying"
              :disabled="!selectedCount"
              :title="selectedCount ? '把勾选的候选创建为本番剧的 RSS 源（默认优先级 100，建后仍可调整）' : ''"
              @click="applySelected"
            >
              创建选中的 {{ selectedCount }} 个 RSS 源
            </n-button>
          </n-space>
        </template>
      </n-modal>

      <!-- RSS 批量发现（v0.3.0）：左右列表选番剧 → 串行后台发现+导入 -->
      <BulkDiscoverModal v-if="showBulk" @close="showBulk = false" @done="refresh" />
    </div>
</template>

<style scoped>
.anime-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.page-head h2 .h-ico {
  color: var(--accent);
}
/* 头部上下两排：上排 = 标题（左）+ 操作按钮（右）；
   下排 = 筛选组（flex-basis:100% 强制换行） */
.head-actions {
  display: flex;
  gap: 8px;
  align-items: center;
  flex-wrap: wrap;
  margin-left: auto; /* 操作按钮整体靠右（上排） */
}
.head-filters {
  display: flex;
  gap: 8px;
  align-items: center;
  flex-wrap: wrap;
  flex-basis: 100%; /* 独占下排 */
}
.head-search {
  flex: 0 1 170px;
  min-width: 90px;
}
/* 与首页「只看有剧集」同款切换按钮 */
.has-eps-toggle {
  font-weight: 600;
  border: 1px solid var(--border, #d8d8d8);
  white-space: nowrap;
}
.anime-row {
  background: var(--card);
  border: 1px solid var(--border-soft);
  border-radius: 14px;
  overflow: hidden;
  transition: border-color 0.15s;
}
.anime-row:hover {
  border-color: var(--border);
}
.anime-main {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 12px 16px;
  cursor: pointer;
}
.chev {
  color: var(--text-3);
  font-size: 11px;
  transition: transform 0.15s;
  width: 14px;
}
.chev.open {
  transform: rotate(90deg);
  color: var(--accent);
}
.row-cover {
  width: 74px;
  height: 50px;
  object-fit: cover;
  border-radius: 10px;
  border: 1px solid var(--border-soft);
  flex-shrink: 0;
}
.row-cover.placeholder {
  display: flex;
  align-items: center;
  justify-content: center;
  background: #0a0d13;
  color: #2a3350;
  font-weight: 700;
  font-size: 20px;
}
.row-info {
  flex: 1;
  min-width: 0;
}
.row-title {
  font-weight: 700;
  font-size: 14.5px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.row-title a {
  text-decoration: none;
}
.row-title a:hover {
  color: var(--accent);
}
.row-sub {
  margin-top: 3px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.row-feedcount {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  color: var(--text-2);
  white-space: nowrap;
}
.feed-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--accent);
}
.row-prog {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 170px;
}
.prog-ep {
  font-size: 12px;
  color: var(--text-2);
  white-space: nowrap;
}
.row-acts {
  white-space: nowrap;
}
.anime-feeds {
  border-top: 1px solid var(--border-soft);
  background: rgba(91, 140, 255, 0.03);
  padding: 14px 18px 16px 44px;
}
.feeds-head {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 10px;
}
.feeds-title {
  font-size: 13px;
  font-weight: 700;
  color: var(--text-2);
}
.feeds-actions {
  display: flex;
  gap: 8px;
}
.feeds-empty {
  font-size: 13px;
  color: var(--text-3);
  padding: 10px 0 4px;
  line-height: 1.8;
}
.feeds-empty code {
  color: var(--accent);
  font-size: 12px;
}
.url {
  max-width: 240px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  color: var(--text-3);
  font-size: 12px;
}
.time {
  white-space: nowrap;
}
.st-err {
  color: var(--err);
  font-size: 12px;
}
.st-ok {
  color: var(--ok);
  font-size: 12px;
}
.site-tag {
  margin-left: 6px;
  padding: 1px 6px;
  border-radius: 8px;
  font-size: 11px;
  line-height: 16px;
  color: var(--dim);
  border: 1px solid var(--line);
  background: color-mix(in srgb, var(--dim) 10%, transparent);
  vertical-align: middle;
  cursor: help;
}
.acts {
  white-space: nowrap;
}
.link-zone {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-top: 12px;
}
.empty-ico {
  font-size: 40px;
  margin-bottom: 8px;
  opacity: 0.4;
}
.form {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
/* 只命中普通 <label>（无 class）：Naive 组件根元素也是 <label>
   （如 n-radio），带 class，不能用裸 label 选择器，否则圆圈和文字会被竖排 */
.form label:not([class]) {
  font-size: 13px;
  color: var(--text-2);
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.mirror-row {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 10px 12px;
  border: 1px solid var(--border, #2a2f3a);
  border-radius: 8px;
}
.mirror-row .mirror-text {
  font-size: 12px;
  display: flex;
  flex-direction: column;
  gap: 2px;
  color: var(--text-2);
}
/* 删除按钮二次确认态（替代 confirm 弹窗） */
.btn-armed {
  background: rgba(248, 113, 113, 0.16) !important;
  box-shadow: inset 0 0 0 1px rgba(248, 113, 113, 0.65);
  padding: 0 8px !important;
}
.row-label {
  flex-direction: row !important;
  align-items: center;
  justify-content: space-between;
}
.import-tip {
  font-size: 12px;
  color: var(--text-3);
  line-height: 1.7;
  background: rgba(91, 140, 255, 0.06);
  border: 1px solid var(--border-soft);
  padding: 8px 12px;
  border-radius: 10px;
}
.import-result {
  font-size: 13px;
}
.form-row {
  display: flex;
  gap: 12px;
}
/* 归档导入：动画类型 chips（与关键词 tag 同款视觉） */
.plat-field {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.plat-field .field-name {
  font-size: 12.5px;
  color: var(--text-3);
}
.plat-chips {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
.plat-chip {
  cursor: pointer;
  user-select: none;
  font-size: 12.5px;
  color: var(--text-2);
  background: var(--bg-soft);
  border: 1px solid var(--border-soft);
  padding: 4px 14px;
  border-radius: 8px;
  transition: all 0.15s;
}
.plat-chip:hover {
  border-color: rgba(91, 140, 255, 0.5);
}
.plat-chip.on {
  color: #c3d4ff;
  background: linear-gradient(135deg, rgba(91, 140, 255, 0.18), rgba(139, 92, 246, 0.16));
  border-color: rgba(91, 140, 255, 0.35);
  font-weight: 600;
}
/* 归档导入进度 */
.archive-progress {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 10px 12px;
  border: 1px solid var(--border-soft);
  border-radius: 10px;
  background: rgba(91, 140, 255, 0.05);
}
.remote-block {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 10px 12px;
  border: 1px dashed var(--border);
  border-radius: 10px;
  background: var(--bg-soft);
}
.remote-line {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  font-size: 13px;
}
.remote-line .hint {
  color: var(--text-3);
  font-size: 12px;
}
.remote-line .n-button.armed {
  color: var(--warn, #f0a020);
}
.archive-phase {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  font-weight: 600;
}
.archive-nums {
  display: flex;
  flex-wrap: wrap;
  gap: 14px;
  font-size: 12.5px;
  color: var(--text-2);
}
.archive-nums em {
  color: var(--err, #f87171);
  font-style: normal;
}
.archive-notes {
  margin: 0;
  padding-left: 18px;
  font-size: 12px;
  color: var(--text-3);
  line-height: 1.7;
}
/* ---------- 自动发现 RSS（v0.2.0） ---------- */
.disc-modal {
  display: flex;
  flex-direction: column;
  gap: 14px;
}
.disc-start {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding-bottom: 12px;
  border-bottom: 1px solid var(--border-soft);
}
.disc-target {
  font-size: 13.5px;
}
.disc-start-row {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}
.disc-tip {
  font-size: 12px;
}
.disc-run {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 12px;
  padding: 34px 0;
  font-size: 14px;
  font-weight: 600;
}
.disc-err {
  color: var(--err);
  font-size: 13px;
  padding: 10px 0;
}
.disc-idle {
  font-size: 13px;
  color: var(--text-3);
  line-height: 1.8;
  padding: 10px 0;
}
.disc-result {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.disc-summary {
  display: flex;
  gap: 18px;
  flex-wrap: wrap;
  font-size: 12.5px;
  color: var(--text-2);
}
.disc-summary b {
  color: var(--accent);
}
.disc-queries {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
  font-size: 12px;
}
.q-chip {
  padding: 1px 10px;
  border-radius: 12px;
  background: rgba(91, 140, 255, 0.08);
  border: 1px solid rgba(91, 140, 255, 0.25);
  color: #9db8ff;
  font-size: 12px;
}
.disc-notes {
  margin: 0;
  padding-left: 18px;
  font-size: 12px;
  color: var(--warn, #f0a020);
  line-height: 1.7;
}
.disc-tbl .c-sel {
  width: 38px;
}
/* c-sel 格只放复选框（无文本）：去掉继承自 .disc-tbl th 的 overflow:hidden + ellipsis，
   否则窄列里复选框溢出时浏览器会在边缘画半个省略号（视觉上是一个怪点） */
.disc-tbl th.c-sel {
  overflow: visible;
  text-overflow: clip;
}
.disc-tbl {
  table-layout: fixed;
}
.disc-tbl th {
  padding: 6px 10px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.disc-tbl td {
  font-size: 12.5px;
  padding: 6px 10px;
  overflow: hidden;
}
.disc-tbl .nw {
  white-space: nowrap;
  text-overflow: ellipsis;
  font-size: 11.5px;
}
.disc-tbl .url {
  white-space: nowrap;
  text-overflow: ellipsis;
}
.disc-tbl .variant {
  white-space: nowrap;
  text-overflow: ellipsis;
}
.disc-tbl .group-cell {
  white-space: nowrap;
}
.disc-tbl .row-low {
  opacity: 0.62;
}

.conf {
  font-size: 11.5px;
  padding: 1px 9px;
  border-radius: 10px;
  border: 1px solid var(--border-soft);
  white-space: nowrap;
}
.conf.high {
  color: #9db8ff;
  background: rgba(91, 140, 255, 0.14);
  border-color: rgba(91, 140, 255, 0.4);
}
.conf.medium {
  color: #ffd9a0;
  background: rgba(255, 158, 61, 0.1);
  border-color: rgba(255, 158, 61, 0.35);
}
.conf.low {
  color: var(--text-3);
}
.disc-un {
  margin-top: 2px;
}
.un-item {
  font-size: 12.5px;
  line-height: 1.9;
  color: var(--text-2);
  word-break: break-all;
}
.un-item a {
  color: var(--accent);
  text-decoration: none;
  margin-left: 6px;
}
.apply-res {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 10px 12px;
  border-radius: 10px;
  background: rgba(91, 140, 255, 0.05);
  border: 1px solid var(--border-soft);
  font-size: 12.5px;
}
.apply-created {
  display: flex;
  flex-wrap: wrap;
  gap: 6px 14px;
  color: var(--ok, #34d399);
}
.apply-item {
  white-space: nowrap;
}
.apply-skipped {
  font-size: 12px;
}
</style>

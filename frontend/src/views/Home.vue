<script setup>
import { ref, computed, onMounted, onBeforeUnmount, watch, nextTick } from 'vue'
import { useAnimeStore } from '../stores/anime'
import { useNavStore } from '../stores/nav'
import { api, errMsg } from '../api'
import AnimeCard from '../components/AnimeCard.vue'
import FeedBadge from '../components/FeedBadge.vue'
import Paginate from '../components/Paginate.vue'

const store = useAnimeStore()
const nav = useNavStore()
const searchInput = ref(null)
const debounce = ref(null)

// 表头筛选下拉（与后台一致：0 = 全部）
const years = computed(() => {
  const cur = new Date().getFullYear()
  const out = []
  for (let y = cur; y >= 1990; y--) out.push(y)
  return out
})
const yearsOptions = computed(() => [
  { label: '全部年份', value: 0 },
  ...years.value.map((y) => ({ label: String(y), value: y })),
])
const quarterOpts = [
  { label: '全部季度', value: 0 },
  { label: '1月', value: 1 },
  { label: '4月', value: 2 },
  { label: '7月', value: 3 },
  { label: '10月', value: 4 },
]

// 分页：每页数量持久化（5 的倍数，配合一行 5 张卡片）；页码在筛选/排序/改数量时回到第 1 页
// 页码/每页数量/排序/只看有剧集 全部落 localStorage：下次打开浏览器保持上次的浏览状态
const HOME_SIZES = [5, 10, 20, 40]
const savedSize = Number(localStorage.getItem('la_home_psize'))
const page = ref(Math.max(1, Number(localStorage.getItem('la_home_page')) || 1))
const pageSize = ref(HOME_SIZES.includes(savedSize) ? savedSize : 10)
const total = ref(0)
// 恢复上次的排序与"只看有剧集"开关
if (localStorage.getItem('la_home_sort') === 'season') store.setSort('season')
if (localStorage.getItem('la_home_has_eps') === '1') store.setHasEps(true)

function persistHome() {
  localStorage.setItem('la_home_psize', String(pageSize.value))
  localStorage.setItem('la_home_page', String(page.value))
  localStorage.setItem('la_home_sort', store.sort)
  localStorage.setItem('la_home_has_eps', store.hasEps ? '1' : '0')
}
watch([page, pageSize, () => store.sort, () => store.hasEps], persistHome)

function setPageSize(v) {
  pageSize.value = v
  localStorage.setItem('la_home_psize', String(v))
  if (page.value !== 1) {
    page.value = 1 // watch(page) 触发查询
  } else {
    refresh()
  }
}

// 翻页 → 重新拉取当前页（之前漏接了这个 watch，导致翻页无响应）
watch(page, refresh)

async function refresh() {
  store.loading = true
  store.error = ''
  try {
    const params = {
      ...store.filter,
      sort: store.sort,
      limit: pageSize.value,
      offset: (page.value - 1) * pageSize.value,
    }
    const [animes, latest, cnt] = await Promise.all([
      api.listAnime(params),
      api.latest(3), // 最新更新卡片最多显示 3 条
      api.countAnime(store.filter),
    ])
    store.list = animes
    store.latest = latest
    total.value = cnt
    // 数据变化导致当前页越界（如删除/筛选收紧）→ 回退到最后一页（watch(page) 会自动重查）
    const pages = Math.max(1, Math.ceil(total.value / pageSize.value))
    if (page.value > pages) {
      page.value = pages
    }
  } catch (e) {
    store.error = errMsg(e)
  } finally {
    store.loading = false
  }
}

// 侧边栏季度选择 → 同步筛选
function applyNav(sel) {
  if (sel === 'all') {
    store.setYear(0)
    store.setQuarter(0)
  } else {
    const [y, q] = sel.split('-')
    store.setYear(Number(y))
    store.setQuarter(Number(q))
  }
}

onMounted(async () => {
  await nextTick()
  applyNav(nav.selected)
  refresh()
  window.addEventListener('la-auth-expired', refresh)
})
onBeforeUnmount(() => window.removeEventListener('la-auth-expired', refresh))

// 表头下拉变更后同步侧边栏高亮（组合不在侧边栏列表时归到"全部"）；
// syncing 标记 + flush:sync 防止"侧边栏 watch 回写"把表头刚设的年份筛掉
let syncing = false
function onHeaderFilter() {
  const key = store.year && store.quarter ? `${store.year}-${store.quarter}` : ''
  const hit = key ? nav.seasons.find((s) => s.key === key) : null
  const target = hit ? hit.key : 'all'
  if (nav.selected === target) return
  syncing = true
  nav.setSeason(target)
  syncing = false
}
watch(
  () => nav.selected,
  (sel) => {
    if (syncing) return
    applyNav(sel)
  },
  { flush: 'sync' }
)
watch(
  () => nav.searchPulse,
  () => nextTick(() => searchInput.value?.focus())
)
// 筛选/搜索/排序变化 → 回第 1 页再查（page 已是 1 时直接查，否则交给 watch(page)）
watch(
  () => [store.year, store.quarter, store.q, store.sort, store.hasEps],
  () => {
    clearTimeout(debounce.value)
    debounce.value = setTimeout(() => {
      if (page.value !== 1) {
        page.value = 1
      } else {
        refresh()
      }
    }, 300)
  }
)

function fmtTime(s) {
  if (!s) return ''
  const d = new Date(s)
  if (isNaN(d)) return s
  const p = (n) => String(n).padStart(2, '0')
  return `${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}
</script>

<template>
  <div class="home">
    <!-- 头部 -->
    <div class="hero">
      <h1>
        {{ nav.selected === 'all' ? '番剧更新' : `${nav.seasons.find((s) => s.key === nav.selected)?.label || ''} 番剧` }}
      </h1>
      <p class="sub">多字幕组 RSS 聚合 · 每集只展示优先级最高的一个结果</p>
    </div>

    <!-- 工具栏：年份/季度筛选 · 搜索 · 排序 -->
    <div class="filter-bar panel">
      <n-select
        v-model:value="store.year"
        :options="yearsOptions"
        size="small"
        style="width: 105px"
        @update:value="onHeaderFilter"
      />
      <n-select
        v-model:value="store.quarter"
        :options="quarterOpts"
        size="small"
        style="width: 105px"
        @update:value="onHeaderFilter"
      />
      <n-input
        ref="searchInput"
        v-model:value="store.q"
        placeholder="搜索番剧（标题 / 别名 / 关键词）…"
        clearable
        size="small"
        style="flex: 1; max-width: 260px"
      />
      <n-button
        size="small"
        quaternary
        :type="store.hasEps ? 'primary' : 'default'"
        class="has-eps-toggle"
        :title="store.hasEps ? '点击关闭：同时显示没有剧集的番剧' : '点击开启：只显示已有剧集的番剧'"
        @click="store.setHasEps(!store.hasEps)"
      >
        {{ store.hasEps ? '✓ ' : '＋ ' }}只看有剧集
      </n-button>
      <div class="filter-spacer"></div>
      <span class="sort-label">排序</span>
      <n-radio-group v-model:value="store.sort" size="small">
        <n-radio-button value="latest">最新更新</n-radio-button>
        <n-radio-button value="season">年度 / 季度</n-radio-button>
      </n-radio-group>
    </div>

    <!-- 最新更新流 -->
    <section v-if="store.latest.length" class="latest panel">
      <div class="latest-head">
        <span class="pulse"></span> 最新更新
      </div>
      <div class="latest-list">
        <div v-for="r in store.latest" :key="r.id" class="latest-item">
          <FeedBadge :name="r.feed_name" :color="r.feed_color" />
          <router-link v-if="r.anime_id" :to="`/anime/${r.anime_id}`" class="latest-link">
            {{ r.title }}
          </router-link>
          <span v-else class="latest-link dim">{{ r.title }}</span>
          <span v-if="r.quality" class="quality">{{ r.quality.toUpperCase() }}</span>
          <span class="time">{{ fmtTime(r.published_at) }}</span>
        </div>
      </div>
    </section>

    <!-- 番剧卡片（正方形封面） -->
    <n-spin :show="store.loading" description="加载中…">
      <div v-if="store.error" class="error">{{ store.error }}</div>
      <div v-else class="grid">
        <AnimeCard v-for="a in store.list" :key="a.id" :anime="a" />
        <div v-if="!store.list.length" class="empty wide">
          <div class="empty-ico">▤</div>
          这个季度还没有番剧数据<br />
          到 <router-link to="/admin/anime" class="link">后台</router-link>
          导入番剧并添加 RSS 源，更新会自动出现在这里。
        </div>
      </div>
    </n-spin>

    <!-- 分页（每页数量可在右侧调整，均为一行 5 张的整数倍） -->
    <Paginate
      v-if="total > 0"
      v-model:page="page"
      :page-size="pageSize"
      :total="total"
      :sizes="HOME_SIZES"
      @update:page-size="setPageSize"
    />
  </div>
</template>

<style scoped>
.home {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.hero h1 {
  margin: 0;
  font-size: 26px;
  font-weight: 800;
  background: linear-gradient(90deg, #fff 20%, #8fb0ff);
  -webkit-background-clip: text;
  background-clip: text;
  color: transparent;
}
.hero .sub {
  margin: 6px 0 0;
  color: var(--text-3);
  font-size: 13px;
}
.filter-bar {
  display: flex;
  gap: 10px;
  align-items: center;
  flex-wrap: wrap;
}
.filter-spacer {
  flex: 1;
}
.has-eps-toggle {
  font-weight: 600;
  border: 1px solid var(--border, #d8d8d8);
  white-space: nowrap;
}
.sort-label {
  font-size: 12px;
  color: var(--text-3);
  white-space: nowrap;
}
.latest {
  position: relative;
  overflow: hidden;
}
.latest::before {
  content: '';
  position: absolute;
  left: 0;
  top: 0;
  bottom: 0;
  width: 3px;
  background: linear-gradient(to bottom, var(--accent), var(--accent-2));
}
.latest-head {
  display: flex;
  align-items: center;
  gap: 8px;
  font-weight: 700;
  font-size: 14px;
  margin-bottom: 10px;
}
.pulse {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--ok);
  box-shadow: 0 0 0 0 rgba(52, 211, 153, 0.6);
  animation: pulse 2s infinite;
}
@keyframes pulse {
  0% {
    box-shadow: 0 0 0 0 rgba(52, 211, 153, 0.5);
  }
  70% {
    box-shadow: 0 0 0 8px rgba(52, 211, 153, 0);
  }
  100% {
    box-shadow: 0 0 0 0 rgba(52, 211, 153, 0);
  }
}
.latest-list {
  display: flex;
  flex-direction: column;
}
.latest-item {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 13px;
  padding: 7px 0;
  border-bottom: 1px dashed var(--border-soft);
  min-width: 0;
}
.latest-item:last-child {
  border-bottom: none;
}
.latest-link {
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  text-decoration: none;
  flex: 1;
  color: var(--text);
}
.latest-link:hover {
  color: var(--accent);
}
.latest-link.dim {
  color: var(--text-3);
}
.quality {
  font-size: 11px;
  font-weight: 700;
  color: #7ee0c3;
  background: rgba(52, 211, 153, 0.1);
  padding: 1px 8px;
  border-radius: 10px;
}
.time {
  font-size: 11.5px;
  color: var(--text-3);
  white-space: nowrap;
}
.grid {
  display: grid;
  grid-template-columns: repeat(5, minmax(0, 1fr)); /* 固定一行 5 张卡片 */
  gap: 16px;
  margin-top: 4px;
}
.empty {
  padding: 60px 0;
  text-align: center;
  color: var(--text-3);
  line-height: 2;
}
.empty.wide {
  grid-column: 1 / -1;
}
.empty-ico {
  font-size: 40px;
  margin-bottom: 8px;
  opacity: 0.4;
}
.link {
  color: var(--accent);
}
.error {
  color: var(--err);
}
</style>

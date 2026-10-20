<script setup>
// RSS 批量发现（v0.3.0）：
// 左侧按 年度季度范围 + 关键字（空 = 全部）筛选番剧，全选/单选后添加到右侧任务列表（自动去重）；
// 「开始发现并导入」= 对列表内每部番剧串行跑"自动发现（含回验）+ 组合逻辑自动导入"。
// 后台任务：进度实时落库（已完成剧逐条增量、当前剧/阶段、完整队列），可随时停止、关页不中断；
// 同一时刻仅允许一个批量任务。
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { api, isAuthError, errMsg, errNotify } from '../api'
import { toastSuccess } from '../notify'

const emit = defineEmits(['close', 'done'])

const view = ref('loading') // loading = 状态探测中（防二次进入先闪选择页）；pick = 列表选择；progress = 任务进度/结果
const all = ref([]) // 全量番剧（本地筛选用）
const listLoading = ref(false)
const listErr = ref('') // 列表加载失败原因（空 = 无错误）
const fromSeason = ref(0) // 0 = 全部季度；否则 = 季度序号
const toSeason = ref(0)
const kw = ref('')
const checked = ref(new Set()) // 左侧勾选的 id 集合
const queue = ref([]) // 右侧任务列表 [{id, title, season_label}]（自动去重）
const skipFeeded = ref(true) // 跳过已有 RSS 源的番剧
const mirror = ref(false) // 本批自动建的源开启自动镜像（新条目自动下载资源文件到本地）
const starting = ref(false)
const stopping = ref(false)
const task = ref(null) // 批量任务快照
let timer = null

// 季度 → 可排序序号（如 2026-10月 → 2026*4+3）；无标签回退 year/quarter；都没有 → 0
function seasonIdx(a) {
  const m = /^(\d{4})-(\d{1,2})月$/.exec(a.season_label || '')
  if (m) return +m[1] * 4 + Math.floor((+m[2] - 1) / 3)
  if (a.year > 0) return a.year * 4 + (a.quarter >= 1 ? a.quarter - 1 : 3)
  return 0
}

// 季度选项：按库内实际存在的 season_label 去重排序（值 = 季度序号）
// 注：选项从全量列表推导——列表加载失败时下拉只剩"全部季度"，左侧会提示错误并可重试
const seasonOpts = computed(() => {
  const m = new Map()
  for (const a of all.value) {
    const idx = seasonIdx(a)
    if (idx > 0 && !m.has(idx)) m.set(idx, a.season_label || '')
  }
  return [
    { label: '全部季度', value: 0 },
    ...[...m.entries()]
      .sort((x, y) => x[0] - y[0])
      .map(([value, label]) => ({ label, value })),
  ]
})

// 全量番剧加载（失败时记录原因，左侧列表显示"点击重试"）
async function loadList() {
  listLoading.value = true
  listErr.value = ''
  try {
    all.value = (await api.adminListAnime({ sort: 'latest', limit: 0 })) || []
  } catch (e) {
    listErr.value = errMsg(e)
  } finally {
    listLoading.value = false
  }
}

// 左侧列表 = 季度范围（两端皆"全部"时不做季度过滤）∧ 关键字（标题/原名包含，空 = 全部）
const filtered = computed(() => {
  const k = kw.value.trim().toLowerCase()
  const bothAll = fromSeason.value === 0 && toSeason.value === 0
  return all.value.filter((a) => {
    if (k && !(a.title || '').toLowerCase().includes(k) && !(a.title_orig || '').toLowerCase().includes(k)) return false
    if (!bothAll) {
      const idx = seasonIdx(a)
      if (idx <= 0) return false
      if (fromSeason.value && idx < fromSeason.value) return false
      if (toSeason.value && idx > toSeason.value) return false
    }
    return true
  })
})

const allChecked = computed(() => filtered.value.length > 0 && filtered.value.every((a) => checked.value.has(a.id)))
const someChecked = computed(() => filtered.value.some((a) => checked.value.has(a.id)))

function toggleAll() {
  const next = new Set(checked.value)
  if (allChecked.value) filtered.value.forEach((a) => next.delete(a.id))
  else filtered.value.forEach((a) => next.add(a.id))
  checked.value = next
}

function toggleOne(id, v) {
  const next = new Set(checked.value)
  if (v) next.add(id)
  else next.delete(id)
  checked.value = next
}

// 左侧勾选 → 右侧任务列表（按 id 自动去重）
function addToQueue() {
  const next = [...queue.value]
  for (const a of filtered.value) {
    if (checked.value.has(a.id) && !next.some((x) => x.id === a.id)) {
      next.push({ id: a.id, title: a.title, season_label: a.season_label })
    }
  }
  queue.value = next
  checked.value = new Set()
}

function removeFromQueue(id) {
  queue.value = queue.value.filter((x) => x.id !== id)
}

async function start() {
  starting.value = true
  try {
    await api.adminBulkRss({ anime_ids: queue.value.map((x) => x.id), skip_feeded: skipFeeded.value, mirror: mirror.value })
    view.value = 'progress'
    await pollOnce()
    startTimer()
  } catch (e) {
    errNotify(e)
  } finally {
    starting.value = false
  }
}

async function pollOnce() {
  try {
    const t = await api.adminBulkRssStatus()
    const prev = task.value
    task.value = t
    if (prev && prev.status === 'running' && t.status !== 'running') {
      stopTimer()
      toastSuccess(`批量任务${t.status === 'done' ? '完成' : '已停止'}`)
      emit('done') // 通知外层刷新（可能新建了 RSS 源）
    }
  } catch (e) {
    if (!isAuthError(e)) task.value = null
  }
}

function startTimer() {
  stopTimer()
  timer = setInterval(pollOnce, 2500)
}

function stopTimer() {
  if (timer) {
    clearInterval(timer)
    timer = null
  }
}

async function stopTask() {
  stopping.value = true
  try {
    await api.adminBulkRssStop()
  } catch (e) {
    errNotify(e)
  } finally {
    stopping.value = false
  }
}

async function backToPick() {
  stopTimer()
  view.value = 'pick'
  // 从"进度视图"进来时（打开时已有任务）从未加载全量列表，
  // 回到选择页必须补加载，否则季度下拉只剩"全部季度"、左列表为空
  if (!all.value.length && !listLoading.value) {
    await loadList()
  }
}

const pct = computed(() => {
  const t = task.value
  if (!t || !t.total) return 0
  return Math.min(100, Math.round((t.done_count / t.total) * 100))
})

const statusText = computed(() => {
  const s = task.value?.status
  if (s === 'running') return '进行中'
  if (s === 'done') return '已完成'
  if (s === 'stopped') return '已停止'
  if (s === 'error') return '出错'
  return '加载中'
})

// 进度时间线：完整队列 + 逐剧实时状态（已完成带结果 / 当前处理 / 排队 / 未处理）
const itemsMap = computed(() => new Map((task.value?.items || []).map((it) => [it.anime_id, it])))
function rowState(q) {
  const it = itemsMap.value.get(q.anime_id)
  if (it) return { kind: it.result.startsWith('失败') ? 'fail' : 'done', text: it.result }
  if (task.value?.status === 'running' && task.value.current_anime_id === q.anime_id) {
    return { kind: 'running', text: task.value.phase || '处理中' }
  }
  if (task.value?.status === 'running') return { kind: 'pending', text: '排队中' }
  return { kind: 'left', text: '未处理' }
}

async function open() {
  view.value = 'loading' // 先探测状态再决定视图，避免二次进入先闪选择页
  task.value = null
  let st = null
  try {
    st = await api.adminBulkRssStatus()
  } catch (e) {
    /* 404 = 尚无批量记录 */
  }
  if (st) {
    // 有任务（进行中或已结束）→ 进度/结果视图；进行中才继续轮询
    task.value = st
    view.value = 'progress'
    if (st.status === 'running') startTimer()
    return
  }
  if (!all.value.length && !listLoading.value) {
    await loadList()
  }
  view.value = 'pick'
}

onMounted(open)
onBeforeUnmount(stopTimer)
</script>

<template>
  <n-modal :show="true" preset="card" title="RSS 批量发现" style="width: 880px" @close="emit('close')">
    <!-- 状态探测中 -->
    <div v-if="view === 'loading'" class="bd-load">
      <n-spin size="medium" />
    </div>

    <!-- 列表选择 -->
    <div v-else-if="view === 'pick'" class="bd">
      <div class="bd-filter">
        <n-select v-model:value="fromSeason" :options="seasonOpts" size="small" style="width: 135px" />
        <span class="dim">~</span>
        <n-select v-model:value="toSeason" :options="seasonOpts" size="small" style="width: 135px" />
        <n-input v-model:value="kw" placeholder="关键字（空 = 全部）" size="small" clearable style="width: 200px" />
        <span class="dim">筛选后 {{ filtered.length }} 部</span>
      </div>
      <div class="bd-cols">
        <div class="bd-col">
          <div class="bd-col-head">
            <label class="bd-check">
              <n-checkbox :checked="allChecked" :indeterminate="someChecked && !allChecked" @update:checked="toggleAll" />
              全选
            </label>
            <span class="dim">左：待选</span>
          </div>
          <div class="bd-list">
            <label v-for="a in filtered" :key="a.id" class="bd-row">
              <n-checkbox :checked="checked.has(a.id)" @update:checked="(v) => toggleOne(a.id, v)" />
              <span class="bd-title" :title="a.title">{{ a.title }}</span>
              <span v-if="a.season_label" class="bd-season dim">{{ a.season_label }}</span>
            </label>
            <div
              v-if="listErr"
              class="bd-empty bd-retry"
              title="番剧列表加载失败（可能是后端刚重启/网络抖动），点击重新加载"
              @click="loadList"
            >
              加载失败：{{ listErr }}<br />点击重试
            </div>
            <div v-else-if="!filtered.length" class="bd-empty">
              {{ listLoading ? '加载中…' : all.length ? '没有匹配的番剧' : '库里还没有番剧' }}
            </div>
          </div>
        </div>
        <div class="bd-mid">
          <n-button size="small" :disabled="!someChecked" @click="addToQueue">→ 加入任务列表</n-button>
        </div>
        <div class="bd-col">
          <div class="bd-col-head">
            <span class="dim">右：任务列表（{{ queue.length }} 部，自动去重）</span>
            <a v-if="queue.length" class="bd-clear" @click="queue = []">清空</a>
          </div>
          <div class="bd-list">
            <div v-for="x in queue" :key="x.id" class="bd-row">
              <span class="bd-title" :title="x.title">{{ x.title }}</span>
              <span v-if="x.season_label" class="bd-season dim">{{ x.season_label }}</span>
              <a class="bd-x" title="移除" @click="removeFromQueue(x.id)">✕</a>
            </div>
            <div v-if="!queue.length" class="bd-empty">在左侧勾选后点「加入任务列表」</div>
          </div>
        </div>
      </div>
      <div class="bd-foot">
        <label class="bd-check" title="已有 RSS 源的番剧直接跳过，减少重复导入浪费的时间">
          <n-checkbox v-model:checked="skipFeeded" />
          跳过已有 RSS 源的番剧
        </label>
        <label
          class="bd-check"
          title="勾选后，本批自动建的 RSS 源自动开启镜像：新条目出现时后台自动把资源文件下载到本地，前台下载链接无感切换为本地镜像（磁力等无法镜像的条目自动跳过）"
        >
          <n-checkbox v-model:checked="mirror" />
          开启自动资源镜像
        </label>
        <div class="bd-foot-right">
          <span
            class="dim bd-hint"
            title="新建源的优先级排在番剧已有源之后（起点 = 已有源最大优先级向上取整到 10 的倍数 + 10，无已有源从 100 起），组合逻辑名次每后一位 +10；数值小 = 优先接管"
            >自动建的源按名次定优先级：排在已有源之后，每后一位 +10（数值小 = 优先）</span
          >
          <n-button type="primary" size="small" :loading="starting" :disabled="!queue.length" @click="start">
            开始发现并导入（{{ queue.length }}）
          </n-button>
        </div>
      </div>
    </div>

    <!-- 进度 / 结果 -->
    <div v-else-if="view === 'progress' && task" class="bd">
      <div class="bd-prog">
        <div class="bd-prog-top">
          <div class="bd-bar-wrap">
            <n-progress type="line" :percentage="pct" :show-indicator="false" :height="8" :fill-border-radius="4" />
          </div>
          <span class="bd-prog-num">
            {{ task.done_count }}/{{ task.total }}
          </span>
          <span class="bd-chip" :class="'bd-chip-' + (task.status === 'running' ? 'run' : task.status === 'done' ? 'ok' : task.status === 'stopped' ? 'stop' : 'err')">
            {{ statusText }}
          </span>
          <span v-if="task.mirror" class="bd-chip" title="本批自动建的源已开启自动镜像">自动镜像</span>
        </div>
        <div v-if="task.status === 'running' && task.current_title" class="bd-prog-cur">
          <n-spin size="small" class="bd-prog-spin" />
          正在处理：{{ task.current_title }}
          <span v-if="task.phase" class="dim">· {{ task.phase }}</span>
        </div>
        <div class="bd-actions">
          <n-button v-if="task.status === 'running'" size="small" type="warning" :loading="stopping" @click="stopTask">
            停止任务
          </n-button>
          <n-button v-else size="small" @click="backToPick">再选一批</n-button>
          <n-button size="small" @click="emit('close')">关闭</n-button>
        </div>
      </div>
      <div class="bd-queue">
        <div v-for="q in task.queue" :key="q.anime_id" class="bd-q-row" :class="'bd-q-' + rowState(q).kind">
          <span class="bd-q-ico">
            <n-spin v-if="rowState(q).kind === 'running'" size="small" />
            <span v-else-if="rowState(q).kind === 'done'">✓</span>
            <span v-else-if="rowState(q).kind === 'fail'">✕</span>
            <span v-else-if="rowState(q).kind === 'pending'">○</span>
            <span v-else>·</span>
          </span>
          <span class="bd-title" :title="q.title">{{ q.title }}</span>
          <span class="bd-q-text" :class="rowState(q).kind === 'fail' ? 'bd-res-err' : 'dim'" :title="rowState(q).text">
            {{ rowState(q).text }}
          </span>
        </div>
        <div v-if="!task.queue.length" class="bd-empty">（旧任务无完整队列记录）</div>
      </div>
    </div>
  </n-modal>
</template>

<style scoped>
.bd {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.bd-load {
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 60px 0;
}
.bd-filter {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.bd-cols {
  display: flex;
  gap: 10px;
  align-items: stretch;
}
.bd-col {
  flex: 1;
  min-width: 0;
  background: var(--bg-soft);
  border: 1px solid var(--border-soft);
  border-radius: 10px;
  display: flex;
  flex-direction: column;
}
.bd-col-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 10px;
  border-bottom: 1px solid var(--border-soft);
  font-size: 12px;
}
.bd-list {
  height: 300px;
  overflow-y: auto;
  padding: 4px 6px;
}
.bd-row {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 5px 6px;
  border-radius: 8px;
  font-size: 13px;
  cursor: default;
}
.bd-row:hover {
  background: var(--card);
}
.bd-title {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.bd-season {
  flex-shrink: 0;
  font-size: 11px;
}
.bd-x {
  color: var(--text-3);
  flex-shrink: 0;
  cursor: pointer;
  padding: 0 4px;
}
.bd-x:hover {
  color: var(--err);
}
.bd-clear {
  color: var(--text-3);
  cursor: pointer;
  font-size: 12px;
}
.bd-clear:hover {
  color: var(--text);
}
.bd-mid {
  display: flex;
  align-items: center;
  justify-content: center;
}
.bd-empty {
  padding: 40px 0;
  text-align: center;
  color: var(--text-3);
  font-size: 13px;
}
.bd-retry {
  cursor: pointer;
  color: var(--text-2);
  line-height: 1.9;
}
.bd-retry:hover {
  color: var(--accent);
}
.bd-check {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  color: var(--text-2);
  cursor: pointer;
}
.bd-foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}
.bd-foot-right {
  display: flex;
  align-items: center;
  gap: 10px;
}
.bd-hint {
  font-size: 12px;
  max-width: 340px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
/* 进度区 */
.bd-prog {
  display: flex;
  flex-direction: column;
  gap: 10px;
  background: var(--bg-soft);
  border: 1px solid var(--border-soft);
  border-radius: 10px;
  padding: 12px 14px;
}
.bd-prog-top {
  display: flex;
  align-items: center;
  gap: 10px;
}
.bd-bar-wrap {
  flex: 1;
  min-width: 0;
}
.bd-prog-num {
  font-size: 13px;
  color: var(--text-2);
  font-variant-numeric: tabular-nums;
  flex-shrink: 0;
}
.bd-chip {
  font-size: 12px;
  padding: 1px 10px;
  border-radius: 10px;
  border: 1px solid var(--border);
  flex-shrink: 0;
}
.bd-chip-run {
  color: var(--accent);
  border-color: rgba(91, 140, 255, 0.45);
  background: rgba(91, 140, 255, 0.1);
}
.bd-chip-ok {
  color: var(--ok);
  border-color: rgba(52, 211, 153, 0.4);
  background: rgba(52, 211, 153, 0.08);
}
.bd-chip-stop {
  color: var(--text-2);
}
.bd-chip-err {
  color: var(--err);
  border-color: rgba(248, 113, 113, 0.45);
  background: rgba(248, 113, 113, 0.08);
}
.bd-prog-cur {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  color: var(--text);
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}
.bd-prog-spin {
  flex-shrink: 0;
}
.bd-actions {
  display: flex;
  gap: 8px;
}
/* 逐剧时间线 */
.bd-queue {
  background: var(--bg-soft);
  border: 1px solid var(--border-soft);
  border-radius: 10px;
  padding: 4px 6px;
  max-height: 320px;
  overflow-y: auto;
}
.bd-q-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 6px 8px;
  border-radius: 8px;
  font-size: 13px;
}
.bd-q-row:hover {
  background: var(--card);
}
.bd-q-ico {
  width: 18px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  font-size: 12px;
}
.bd-q-running {
  background: rgba(91, 140, 255, 0.08);
}
.bd-q-running .bd-q-ico {
  color: var(--accent);
}
.bd-q-done .bd-q-ico {
  color: var(--ok);
}
.bd-q-fail .bd-q-ico {
  color: var(--err);
}
.bd-q-pending .bd-q-ico,
.bd-q-left .bd-q-ico {
  color: var(--text-3);
}
.bd-q-pending,
.bd-q-left {
  opacity: 0.75;
}
.bd-q-text {
  flex-shrink: 0;
  max-width: 52%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 12px;
  text-align: right;
}
.bd-res-err {
  color: var(--err);
}
</style>

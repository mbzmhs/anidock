<script setup>
import { ref, onMounted, watch } from 'vue'
import { useRoute } from 'vue-router'
import { api, errMsg } from '../api'
import FeedBadge from '../components/FeedBadge.vue'

const route = useRoute()
const anime = ref(null)
const episodes = ref([])
const feeds = ref([])
const error = ref('')
const loading = ref(false)
const expanded = ref({}) // 每集"其它源"折叠
const synOpen = ref(false)
const castModal = ref(null) // 点开某个角色的全部声优（chips 太多时折叠展示）

async function load() {
  const id = route.params.id
  loading.value = true
  error.value = ''
  try {
    const [a, eps] = await Promise.all([api.getAnime(id), api.getEpisodes(id)])
    anime.value = a
    episodes.value = eps
    feeds.value = await api.getAnimeFeeds(id).catch(() => [])
  } catch (e) {
    error.value = errMsg(e)
  } finally {
    loading.value = false
  }
}

onMounted(load)
watch(() => route.params.id, load)

// 集数展示：合集（episode_to 非空）→ "X~Y 合集"；无集数 → —
function fmtEp(x) {
  if (x == null) return '—'
  return Number.isInteger(x) ? String(x) : String(Math.round(x * 10) / 10)
}
// 排序键：合集的 best.episode 是区间起点，直接参与降序；无集数垫底
function epSortKey(r) {
  return r?.episode ?? -1
}
function epCell(r) {
  if (!r) return '—'
  if (r.episode_to != null) return `${fmtEp(r.episode)}~${fmtEp(r.episode_to)} 合集`
  return fmtEp(r.episode)
}

// 下载链接无感切换：有本地镜像则优先 /mirror/…，否则回退原始 download_url
function dlHref(r) {
  if (!r) return ''
  if (r.mirror_path) return '/mirror/' + r.mirror_path
  return r.download_url || ''
}
function dlLabel(r) {
  return r?.mirror_path ? '下载（本地镜像）' : '下载'
}
function fmtTime(s) {
  if (!s) return ''
  const d = new Date(s)
  if (isNaN(d)) return s
  const p = (n) => String(n).padStart(2, '0')
  return `${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}

function toggle(id) {
  expanded.value = { ...expanded.value, [id]: !expanded.value[id] }
}

function infoText(item) {
  return item.value || (item.values || []).join(' / ')
}

// RSS 订阅：点按钮复制订阅地址（聚合优选列表的 RSS 2.0 流）
const rssCopied = ref(false)
function rssUrl() {
  return location.origin + `/api/anime/${anime.value.id}/rss`
}
function copyRss(e) {
  e.preventDefault()
  const url = rssUrl()
  if (navigator.clipboard?.writeText) {
    navigator.clipboard
      .writeText(url)
      .then(() => {
        rssCopied.value = true
        setTimeout(() => (rssCopied.value = false), 2000)
      })
      .catch(() => window.open(url, '_blank'))
  } else {
    window.open(url, '_blank')
  }
}
</script>

<template>
  <div v-if="error" class="error">{{ error }}</div>
  <n-spin v-else :show="loading" description="加载中…">
    <div v-if="anime" class="detail">
      <!-- 头部 -->
      <section class="header panel">
        <div class="cover-col">
          <img v-if="anime.cover" class="cover" :src="'/' + anime.cover" :alt="anime.title" />
          <div class="thumbs" v-if="anime.images.length">
            <img v-for="img in anime.images.slice(0, 4)" :key="img" :src="'/' + img" loading="lazy" />
          </div>
        </div>
        <div class="info">
          <div class="tags">
            <span v-if="anime.season_label" class="tag season">{{ anime.season_label }}</span>
            <span v-for="g in anime.genres" :key="g" class="tag">{{ g }}</span>
          </div>
          <h1>{{ anime.title }}</h1>
          <div v-if="anime.title_orig && anime.title_orig !== anime.title" class="orig">
            {{ anime.title_orig }}
          </div>
          <div class="rss-line">
            <a class="rss-btn" href="javascript:;" :title="rssUrl()" @click="copyRss">⚡ RSS 订阅（复制地址）</a>
            <span v-if="rssCopied" class="dim">已复制，可粘贴到任意 RSS 阅读器</span>
          </div>
          <p v-if="anime.synopsis" class="synopsis" :class="{ clamped: !synOpen }" @click="synOpen = true">
            {{ anime.synopsis }}
          </p>
          <div class="stat-row">
            <div class="stat">
              <div class="stat-num">{{ anime.episode_count || 0 }}</div>
              <div class="stat-label">已更新集数</div>
            </div>
            <div class="stat">
              <div class="stat-num">{{ feeds.length || 0 }}</div>
              <div class="stat-label">RSS 源</div>
            </div>
            <div class="stat">
              <div class="stat-num small">
                <FeedBadge v-if="anime.latest_group" :name="anime.latest_group" :color="anime.latest_group_color" />
                <span v-else class="dim">—</span>
              </div>
              <div class="stat-label">最近更新组</div>
            </div>
          </div>

          <!-- 制作信息（Bangumi 导入） -->
          <div v-if="anime.studio || (anime.info || []).length" class="prod-block">
            <div class="prod-title">制作信息</div>
            <div v-if="anime.studio" class="studio-line">
              <span class="studio-label">制作</span>
              <span class="studio-chip">{{ anime.studio }}</span>
            </div>
            <div v-if="(anime.info || []).length" class="info-grid">
              <div v-for="item in anime.info" :key="item.key" class="info-row">
                <span class="info-key">{{ item.key }}</span>
                <span class="info-val">{{ infoText(item) }}</span>
              </div>
            </div>
          </div>
          <!-- 角色 / 声优（Bangumi Archive 关联产出） -->
          <div v-if="(anime.cast || []).length" class="prod-block">
            <div class="prod-title">角色 / 声优</div>
            <div class="cast-grid">
              <div class="cast-card" v-for="c in anime.cast" :key="c.name">
                <div class="cast-name">
                  {{ c.name }}
                  <i v-if="c.lead === 1" class="lead-badge primary">主</i>
                  <i v-else-if="c.lead === 2" class="lead-badge">配</i>
                  <i v-else-if="c.lead === 3" class="lead-badge guest">客</i>
                </div>
                <div v-if="(c.seiyuu || []).length" class="cast-sei">
                  <span
                    class="sei-chip"
                    v-for="s in c.seiyuu.slice(0, 2)"
                    :key="s.name + s.ver"
                    :class="{ 'other': s.ver !== 0 }"
                    :title="s.ver !== 0 ? '其它版本（真人版/配音版等）' : '正片配音'"
                  >
                    {{ s.name }}<i v-if="s.ver !== 0" class="ver-mark">其它</i>
                  </span>
                  <button
                    v-if="c.seiyuu.length > 2"
                    class="sei-more"
                    :title="'全部 ' + c.seiyuu.length + ' 位声优'"
                    @click="castModal = c"
                  >
                    +{{ c.seiyuu.length - 2 }}
                  </button>
                </div>
              </div>
            </div>
          </div>
          <div v-else-if="(anime.characters || []).length" class="char-wrap">
            <span class="char-chip" v-for="c in anime.characters" :key="c">{{ c }}</span>
          </div>
        </div>
      </section>

      <!-- 剧集列表 -->
      <section class="panel">
        <div class="ep-head">
          <h2>剧集</h2>
          <span class="dim">每集展示优先级最高的一个源，点击「其它源」查看同集全部结果</span>
        </div>
        <div v-if="!episodes.length" class="empty">
          暂无剧集。字幕组 RSS 发布并匹配到本番剧后会自动出现在这里。
        </div>
        <table v-else class="tbl ep-table">
          <thead>
            <tr>
              <th class="c-ep">集</th>
              <th>制作组</th>
              <th>规格</th>
              <th>更新时间</th>
              <th>操作</th>
            </tr>
          </thead>
          <tbody>
            <template
              v-for="e in [...episodes].sort((a, b) => epSortKey(b.best) - epSortKey(a.best))"
              :key="e.best?.id"
            >
              <tr>
                <td class="c-ep ep">{{ epCell(e.best) }}</td>
                <td>
                  <FeedBadge :name="e.best?.feed_name || e.best?.group_name" :color="e.best?.feed_color" />
                </td>
                <td class="spec">{{ e.best?.spec || e.best?.quality || '—' }}</td>
                <td class="time">{{ fmtTime(e.best?.published_at) }}</td>
                <td class="acts">
                  <a v-if="e.best?.url" :href="e.best.url" target="_blank" rel="noopener" class="btn primary">
                    打开详情
                  </a>
                  <a v-if="dlHref(e.best)" :href="dlHref(e.best)" class="btn ghost">{{ dlLabel(e.best) }}</a>
                  <a
                    v-if="e.alternatives?.length"
                    href="javascript:;"
                    class="alt-toggle"
                    @click="toggle(e.best.id)"
                  >
                    其它源({{ e.alternatives.length }})
                  </a>
                </td>
              </tr>
              <tr v-if="e.alternatives?.length && expanded[e.best.id]" class="alt-row">
                <td :colspan="5">
                  <div v-for="alt in e.alternatives" :key="alt.id" class="alt-item">
                    <FeedBadge :name="alt.feed_name || alt.group_name" :color="alt.feed_color" />
                    <span class="alt-spec dim">{{ alt.spec || alt.quality }}</span>
                    <a v-if="alt.url" :href="alt.url" target="_blank" rel="noopener">打开详情</a>
                    <a v-if="dlHref(alt)" :href="dlHref(alt)">{{ dlLabel(alt) }}</a>
                    <span class="time dim">{{ fmtTime(alt.published_at) }}</span>
                  </div>
                </td>
              </tr>
            </template>
          </tbody>
        </table>
      </section>

      <!-- 某角色的全部声优（chips 超过 2 个时点开） -->
      <n-modal
        :show="!!castModal"
        preset="card"
        :title="castModal ? `${castModal.name} · 全部声优（${(castModal.seiyuu || []).length}）` : ''"
        style="width: 440px"
        @close="castModal = null"
      >
        <div class="cast-modal-list">
          <span
            class="sei-chip"
            v-for="s in castModal?.seiyuu || []"
            :key="s.name + s.ver"
            :class="{ 'other': s.ver !== 0 }"
            :title="s.ver !== 0 ? '其它版本（真人版/配音版等）' : '正片配音'"
          >
            {{ s.name }}<i v-if="s.ver !== 0" class="ver-mark">其它</i>
          </span>
        </div>
      </n-modal>
    </div>
  </n-spin>
</template>

<style scoped>
.detail {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.header {
  display: grid;
  grid-template-columns: 300px 1fr;
  gap: 22px;
}
@media (max-width: 860px) {
  .header {
    grid-template-columns: 1fr;
  }
}
.cover-col {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.cover {
  width: 100%;
  border-radius: 14px;
  border: 1px solid var(--border);
}
.thumbs img {
  width: 100%;
  border-radius: 10px;
  border: 1px solid var(--border-soft);
}
.info h1 {
  margin: 8px 0 4px;
  font-size: 24px;
  font-weight: 800;
}
.orig {
  color: var(--text-3);
  margin-bottom: 10px;
  font-size: 13px;
}
.rss-line {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 12px;
}
.rss-btn {
  font-size: 12.5px;
  color: var(--accent);
  border: 1px solid rgba(91, 140, 255, 0.4);
  background: rgba(91, 140, 255, 0.08);
  padding: 3px 12px;
  border-radius: 14px;
  cursor: pointer;
  text-decoration: none;
  transition: all 0.15s;
}
.rss-btn:hover {
  background: rgba(91, 140, 255, 0.18);
  border-color: rgba(91, 140, 255, 0.7);
}
.tags {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
}
.tag {
  font-size: 12px;
  padding: 2px 12px;
  border-radius: 20px;
  background: rgba(91, 140, 255, 0.1);
  border: 1px solid rgba(91, 140, 255, 0.25);
  color: #9db8ff;
}
.tag.season {
  background: linear-gradient(135deg, rgba(91, 140, 255, 0.25), rgba(139, 92, 246, 0.25));
  color: #c3d4ff;
}
.synopsis {
  font-size: 13.5px;
  line-height: 1.8;
  color: var(--text-2);
  white-space: pre-wrap;
  margin: 0;
}
.synopsis.clamped {
  display: -webkit-box;
  -webkit-line-clamp: 4;
  -webkit-box-orient: vertical;
  overflow: hidden;
  cursor: pointer;
}
.stat-row {
  display: flex;
  gap: 34px;
  margin-top: 18px;
}
.stat-num {
  font-size: 22px;
  font-weight: 800;
}
.stat-num.small {
  font-size: 14px;
  display: flex;
  align-items: center;
  height: 26px;
}
.stat-label {
  font-size: 12px;
  color: var(--text-3);
  margin-top: 2px;
}
.ep-head {
  display: flex;
  align-items: baseline;
  gap: 14px;
  margin-bottom: 8px;
}
.ep-head h2 {
  margin: 0;
  font-size: 17px;
}
.ep-table .c-ep {
  width: 56px;
}
.ep-table .ep {
  font-weight: 800;
  font-size: 15px;
  color: var(--accent);
}
.ep-table .spec {
  font-size: 12px;
  color: var(--text-2);
}
.ep-table .time {
  font-size: 12px;
  color: var(--text-3);
  white-space: nowrap;
}
.acts {
  white-space: nowrap;
}
.btn {
  display: inline-block;
  padding: 4px 12px;
  border-radius: 8px;
  font-size: 12px;
  text-decoration: none;
  margin-right: 8px;
}
.btn.primary {
  background: linear-gradient(135deg, var(--accent), var(--accent-2));
  color: #fff;
}
.btn.ghost {
  border: 1px solid var(--border);
  color: var(--text-2);
}
.alt-toggle {
  font-size: 12px;
  color: var(--accent);
  text-decoration: none;
}
.alt-row td {
  background: rgba(91, 140, 255, 0.04);
}
.alt-item {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 5px 0;
  font-size: 13px;
}
.alt-item a {
  font-size: 12px;
  color: var(--accent);
}
.prod-block {
  margin-top: 18px;
  padding-top: 14px;
  border-top: 1px solid var(--border-soft);
}
.prod-title {
  font-size: 13px;
  font-weight: 700;
  color: var(--text-2);
  margin-bottom: 10px;
}
.studio-line {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 10px;
}
.studio-label {
  font-size: 13px;
  color: var(--text-2);
}
.studio-chip {
  font-size: 13px;
  color: #ffd9a0;
  background: linear-gradient(135deg, rgba(255, 158, 61, 0.14), rgba(255, 199, 116, 0.1));
  border: 1px solid rgba(255, 158, 61, 0.35);
  padding: 3px 14px;
  border-radius: 20px;
}
.info-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 2px 24px;
  max-height: 150px;
  overflow-y: auto;
}
.info-row {
  display: flex;
  gap: 8px;
  font-size: 12.5px;
  line-height: 1.7;
}
.info-key {
  flex: 0 0 72px;
  color: var(--text-3);
}
.info-val {
  color: var(--text-2);
  word-break: break-all;
}
@media (max-width: 860px) {
  .info-grid {
    grid-template-columns: 1fr;
  }
}
.char-wrap {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 12px;
}
/* 角色/声优区块配色原则：
   - 定位维度：仅「主」用 accent 强调，配/客统一中性灰
   - 版本维度：正片/其它版本同色，仅用「虚线框 + 降透明度」区分，不再引入新色相
*/
.char-chip {
  font-size: 12px;
  color: var(--text-2);
  background: rgba(154, 165, 187, 0.05);
  border: 1px solid var(--border-soft);
  padding: 2px 10px;
  border-radius: 14px;
}
.cast-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(150px, 1fr));
  gap: 8px;
  max-height: 260px;
  overflow-y: auto;
}
.cast-card {
  padding: 8px 10px;
  border: 1px solid var(--border-soft);
  border-radius: 10px;
  background: var(--bg-soft);
  display: flex;
  flex-direction: column;
  gap: 5px;
}
.cast-name {
  font-size: 13px;
  font-weight: 600;
  color: var(--text);
  display: flex;
  align-items: center;
  gap: 5px;
}
/* 角色定位徽章（Bangumi subject-characters type：1=主角 2=配角 3=客串） */
.lead-badge {
  font-style: normal;
  font-size: 10px;
  line-height: 1;
  padding: 2.5px 5px;
  border-radius: 6px;
  color: var(--text-3);
  background: transparent;
  border: 1px solid var(--border-soft);
}
/* 注意：不能用 class 名 main —— App.vue 的全局 .main 会命中这里把徽章撑开 */
.lead-badge.primary {
  color: #c3d4ff;
  background: linear-gradient(135deg, rgba(91, 140, 255, 0.28), rgba(139, 92, 246, 0.22));
  border-color: rgba(91, 140, 255, 0.55);
}
.cast-sei {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
}
.sei-chip {
  font-size: 12px;
  color: var(--text-2);
  background: rgba(154, 165, 187, 0.05);
  border: 1px solid var(--border-soft);
  padding: 1px 8px;
  border-radius: 12px;
  white-space: nowrap;
}
/* 非正片版本（真人版/配音版等）：虚线框 + 降透明度，排在正片声优之后 */
.sei-chip.other {
  color: var(--text-3);
  border-style: dashed;
}
.ver-mark {
  font-style: normal;
  font-size: 10px;
  margin-left: 4px;
  opacity: 0.8;
}
/* 声优过多时的折叠按钮（点击开模态窗看全部） */
.sei-more {
  all: unset;
  cursor: pointer;
  font-size: 12px;
  color: var(--accent);
  background: rgba(91, 140, 255, 0.12);
  border: 1px solid rgba(91, 140, 255, 0.35);
  padding: 1px 8px;
  border-radius: 12px;
  white-space: nowrap;
}
.sei-more:hover {
  background: rgba(91, 140, 255, 0.22);
}
.cast-modal-list {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  max-height: 60vh;
  overflow-y: auto;
  padding: 4px 2px;
}
</style>

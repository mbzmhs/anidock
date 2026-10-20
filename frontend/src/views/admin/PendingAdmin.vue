<script setup>
import { ref, onMounted } from 'vue'
import { api, errMsg, errNotify } from '../../api'
import { toastInfo } from '../../notify'
import FeedBadge from '../../components/FeedBadge.vue'

const pending = ref([])
const animes = ref([])
const binding = ref({}) // releaseId -> animeId 选中值
const busy = ref({})

async function refresh() {
  try {
    const [p, a] = await Promise.all([api.adminPending(), api.adminListAnime()])
    pending.value = p
    animes.value = a
  } catch (e) {
    errNotify(e)
  }
}
onMounted(refresh)

async function bind(id, animeId, siblings) {
  if (!animeId) {
    toastInfo('请选择番剧')
    return
  }
  busy.value = { ...busy.value, [id]: true }
  try {
    await api.adminBindRelease(id, animeId, siblings)
    binding.value = { ...binding.value, [id]: null, [id + '_sib']: false }
    await refresh()
  } catch (e) {
    errNotify(e)
  } finally {
    busy.value = { ...busy.value, [id]: false }
  }
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
        <h2><span class="h-ico">◔</span> 待归类 <span v-if="pending.length" class="cnt">{{ pending.length }}</span></h2>
        <n-button size="small" @click="refresh">刷新</n-button>
      </div>
      <p class="hint">
        RSS 解析后无法自动匹配到番剧的条目会进这里。绑定后（可选「同组全部集数一并绑定」）历史
        release 一并归属到该番剧。也可以在番剧管理中给番剧维护<b>别名</b>，提高自动匹配率。
      </p>

      <div v-if="!pending.length" class="panel empty ok">
        <div class="empty-ico">✓</div>
        没有待归类条目，全部 release 都已归属到番剧。
      </div>

      <div v-for="r in pending" :key="r.id" class="panel pending-item">
        <div class="info">
          <FeedBadge :name="r.feed_name" :color="r.feed_color" />
          <span class="title" :title="r.title">{{ r.title }}</span>
          <span v-if="r.episode != null" class="dim">第 {{ r.episode }} 集</span>
          <span v-if="r.spec" class="dim spec-chip">{{ r.spec }}</span>
          <span class="dim">{{ fmtTime(r.published_at) }}</span>
        </div>
        <div class="bind">
          <n-select
            :value="binding[r.id] ?? null"
            :options="animes.map((a) => ({ label: `${a.id} ${a.title}`, value: a.id }))"
            style="width: 250px"
            placeholder="选择番剧"
            size="small"
            @update:value="(v) => (binding = { ...binding, [r.id]: v })"
          />
          <n-checkbox
            :checked="!!binding[r.id + '_sib']"
            @update:checked="(v) => (binding = { ...binding, [r.id + '_sib']: v })"
          >
            同组全部集数一并绑定
          </n-checkbox>
          <n-button
            size="small"
            type="primary"
            class="grad-btn"
            :loading="busy[r.id]"
            :disabled="!binding[r.id]"
            @click="bind(r.id, binding[r.id], !!binding[r.id + '_sib'])"
          >
            绑定
          </n-button>
        </div>
      </div>
    </div>
</template>

<style scoped>
.page-head h2 .h-ico {
  color: var(--accent);
}
.cnt {
  font-size: 12px;
  font-weight: 700;
  background: linear-gradient(135deg, var(--accent), var(--accent-2));
  color: #fff;
  border-radius: 20px;
  padding: 2px 10px;
  margin-left: 4px;
}
.empty.ok {
  text-align: center;
  padding: 50px 0;
  color: var(--ok);
}
.empty-ico {
  font-size: 36px;
  margin-bottom: 10px;
}
.pending-item {
  display: flex;
  justify-content: space-between;
  gap: 14px;
  flex-wrap: wrap;
  align-items: center;
}
.info {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  font-size: 13px;
  min-width: 0;
  flex: 1;
}
.title {
  font-weight: 600;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  max-width: 340px;
}
.spec-chip {
  background: rgba(255, 255, 255, 0.05);
  padding: 1px 8px;
  border-radius: 10px;
}
.bind {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}
</style>

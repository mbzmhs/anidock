<script setup>
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import FeedBadge from './FeedBadge.vue'

const props = defineProps({ anime: { type: Object, required: true } })
const router = useRouter()

const link = computed(() => `/anime/${props.anime.id}`)

// 集数展示：12 → "12"，12.5（总集篇）→ "12.5"，0（前篇）→ "0"
function epText(x) {
  return Number.isInteger(x) ? String(x) : String(Math.round(x * 10) / 10)
}
function fmtTime(s) {
  if (!s) return ''
  const d = new Date(s)
  if (isNaN(d)) return s
  const p = (n) => String(n).padStart(2, '0')
  return `${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}
</script>

<template>
  <router-link :to="link" class="anime-card">
    <div class="cover-wrap">
      <img v-if="anime.cover" :src="'/' + anime.cover" :alt="anime.title" loading="lazy" />
      <div v-else class="no-cover">{{ (anime.title || '?').slice(0, 1) }}</div>
      <div class="cover-mask"></div>
      <span v-if="anime.season_label" class="season-chip">{{ anime.season_label }}</span>
      <span v-if="anime.latest_episode" class="ep-chip">更新至 {{ epText(anime.latest_episode) }}</span>
    </div>
    <div class="meta">
      <div class="title" :title="anime.title">{{ anime.title }}</div>
      <div v-if="anime.title_orig && anime.title_orig !== anime.title" class="orig">
        {{ anime.title_orig }}
      </div>
      <div class="progress">
        <FeedBadge v-if="anime.latest_group" :name="anime.latest_group" :color="anime.latest_group_color" />
        <span v-else class="none">暂无更新</span>
        <span v-if="anime.latest_updated_at" class="updated">{{ fmtTime(anime.latest_updated_at) }}</span>
      </div>
    </div>
  </router-link>
</template>

<style scoped>
.anime-card {
  display: block;
  text-decoration: none;
  color: inherit;
  background: var(--card);
  border: 1px solid var(--border-soft);
  border-radius: 16px;
  overflow: hidden;
  transition: transform 0.18s ease, box-shadow 0.18s ease, border-color 0.18s ease;
}
.anime-card:hover {
  transform: translateY(-4px);
  border-color: rgba(91, 140, 255, 0.5);
  box-shadow: 0 14px 34px rgba(0, 0, 0, 0.45), 0 0 0 1px rgba(91, 140, 255, 0.25);
}
.cover-wrap {
  position: relative;
  aspect-ratio: 1 / 1;
  background: #0a0d13;
  overflow: hidden;
}
.cover-wrap img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
  transition: transform 0.3s ease;
}
.anime-card:hover .cover-wrap img {
  transform: scale(1.05);
}
.cover-mask {
  position: absolute;
  inset: 0;
  background: linear-gradient(to top, rgba(10, 13, 19, 0.85) 0%, transparent 45%);
  pointer-events: none;
}
.no-cover {
  width: 100%;
  height: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 52px;
  color: #2a3350;
  font-weight: 700;
}
.season-chip {
  position: absolute;
  left: 10px;
  bottom: 10px;
  font-size: 11px;
  padding: 2px 10px;
  border-radius: 20px;
  background: rgba(10, 13, 19, 0.72);
  border: 1px solid rgba(91, 140, 255, 0.4);
  color: #a9c2ff;
  backdrop-filter: blur(4px);
}
.ep-chip {
  position: absolute;
  right: 10px;
  bottom: 10px;
  font-size: 11px;
  font-weight: 700;
  padding: 2px 10px;
  border-radius: 20px;
  background: linear-gradient(135deg, var(--accent), var(--accent-2));
  color: #fff;
}
.meta {
  padding: 12px 14px 14px;
}
.title {
  font-weight: 700;
  font-size: 14.5px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.orig {
  font-size: 12px;
  color: var(--text-3);
  margin-top: 3px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.progress {
  margin-top: 10px;
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.none {
  font-size: 12px;
  color: var(--text-3);
}
.updated {
  font-size: 11.5px;
  color: var(--text-3);
  margin-left: auto;
}
</style>

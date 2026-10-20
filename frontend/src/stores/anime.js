import { defineStore } from 'pinia'
import { ref, computed } from 'vue'

// 首页筛选/列表状态
export const useAnimeStore = defineStore('anime', () => {
  const year = ref(0)
  const quarter = ref(0)
  const q = ref('') // 关键词搜索：命中 标题/别名/关键词
  const sort = ref('latest') // latest | season
  const hasEps = ref(false) // 只显示已有剧集（至少 1 条 release）的番剧

  const filter = computed(() => {
    const f = {}
    if (year.value) f.year = year.value
    if (quarter.value) f.quarter = quarter.value
    if (q.value) f.q = q.value
    if (hasEps.value) f.has_eps = 1
    return f
  })

  const allYears = computed(() => {
    const ys = new Set()
    list.value.forEach((a) => a.year && ys.add(a.year))
    return [...ys].sort((a, b) => b - a)
  })

  const list = ref([])
  const latest = ref([])
  const loading = ref(false)
  const error = ref('')

  return {
    year, quarter, q, sort, hasEps, list, latest, loading, error,
    allYears,
    setYear: (v) => (year.value = v),
    setQuarter: (v) => (quarter.value = v),
    setQ: (v) => (q.value = v),
    setSort: (v) => (sort.value = v),
    setHasEps: (v) => (hasEps.value = !!v),
    filter,
  }
})

import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api } from '../api'

const QNAME = ['', '1月', '4月', '7月', '10月']

// 当季 key：1-3 月=1月番(1) 4-6=4月番(2) 7-9=7月番(3) 10-12=10月番(4)
function currentSeasonKey() {
  const d = new Date()
  return `${d.getFullYear()}-${Math.ceil((d.getMonth() + 1) / 3)}`
}

// 侧边栏导航：当季 + 近 4 个季度的番剧、全部番剧、搜索
// 默认选中当季（首页表头年份/季度筛选随之预置为当季）
export const useNavStore = defineStore('nav', () => {
  const seasons = ref([]) // [{ key:'2026-3', label:'2026-10月', year:2026, quarter:3, count }]
  const total = ref(0)
  // 记忆上次选中的季度（下次打开保持；无记录时默认当季）
  const selected = ref(localStorage.getItem('la_nav_selected') || currentSeasonKey()) // season.key | 'all'
  const searchPulse = ref(0) // 自增触发首页聚焦搜索框

  async function load() {
    try {
      const list = await api.listAnime({})
      total.value = list.length
      const map = {}
      list.forEach((a) => {
        if (!a.year || !a.quarter) return
        const key = `${a.year}-${a.quarter}`
        if (!map[key]) {
          map[key] = {
            key,
            label: a.season_label || `${a.year}-${QNAME[a.quarter] || ''}`,
            year: a.year,
            quarter: a.quarter,
            count: 0,
          }
        }
        map[key].count++
      })
      seasons.value = Object.values(map).sort(
        (a, b) => b.year - a.year || b.quarter - a.quarter
      ).slice(0, 5) // 当季 + 近 4 个季度
    } catch (e) {
      // 静默：侧边栏保持空
    }
  }

  return {
    seasons,
    total,
    selected,
    searchPulse,
    load,
    setSeason: (key) => {
      selected.value = key
      localStorage.setItem('la_nav_selected', key)
    },
    bumpSearch: () => {
      searchPulse.value++
    },
  }
})

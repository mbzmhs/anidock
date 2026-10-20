<script setup>
// 通用分页条：页码导航 + 每页数量选择（表头/尾部均可放，数量可调）
import { computed } from 'vue'

const props = defineProps({
  page: { type: Number, default: 1 },
  pageSize: { type: Number, default: 20 },
  total: { type: Number, default: 0 },
  sizes: { type: Array, default: () => [12, 24, 48, 96] },
})
const emit = defineEmits(['update:page', 'update:pageSize'])

const pages = computed(() => Math.max(1, Math.ceil(props.total / props.pageSize)))

function go(p) {
  if (p < 1 || p > pages.value || p === props.page) return
  emit('update:page', p)
}
</script>

<template>
  <div class="pagi">
    <span class="dim">{{ total }} 部 · 第 {{ page }} / {{ pages }} 页</span>
    <div class="pagi-nav">
      <n-button size="tiny" quaternary :disabled="page <= 1" @click="go(page - 1)">‹ 上一页</n-button>
      <n-button size="tiny" quaternary :disabled="page >= pages" @click="go(page + 1)">下一页 ›</n-button>
    </div>
    <div class="pagi-size">
      <span class="dim">每页</span>
      <n-select
        :value="pageSize"
        :options="sizes.map((s) => ({ label: String(s), value: s }))"
        size="tiny"
        style="width: 72px"
        @update:value="(v) => emit('update:pageSize', v)"
      />
      <span class="dim">部</span>
    </div>
  </div>
</template>

<style scoped>
.pagi {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 16px;
  flex-wrap: wrap;
  font-size: 13px;
  padding: 4px 2px;
}
.pagi-nav {
  display: flex;
  gap: 6px;
}
.pagi-size {
  display: flex;
  align-items: center;
  gap: 6px;
}
</style>

<script setup>
const props = defineProps({
  name: { type: String, default: '' },
  color: { type: String, default: '#5b8cff' },
  // 警告提示（如回验警告）：非空时气泡内追加 ⚠，悬停显示详情
  warn: { type: String, default: '' },
})
</script>

<template>
  <span
    v-if="name"
    class="feed-badge"
    :class="{ 'has-warn': !!warn }"
    :title="warn || undefined"
    :style="{ background: color || '#5b8cff' }"
  >{{ name }}<span v-if="warn" class="fb-warn">⚠</span></span>
</template>

<style scoped>
.feed-badge {
  display: inline-block;
  padding: 2px 10px;
  border-radius: 20px;
  font-size: 12px;
  line-height: 18px;
  color: #fff;
  white-space: nowrap;
  letter-spacing: 0.3px;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.3);
}
.feed-badge.has-warn {
  cursor: help;
}
.fb-warn {
  margin-left: 5px;
}
</style>

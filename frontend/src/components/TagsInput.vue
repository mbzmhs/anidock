<script setup>
// 标签编辑器：方块 chips，回车/「添加」新增，点 × 删除（自动去重）
import { ref } from 'vue'
import { toastInfo } from '../notify'

const props = defineProps({
  value: { type: Array, default: () => [] },
  placeholder: { type: String, default: '输入后回车添加' },
  max: { type: Number, default: 0 }, // 0 = 不限制
})
const emit = defineEmits(['update:value'])

const input = ref('')
const inputEl = ref(null)

function add() {
  const t = input.value.trim()
  if (!t) return
  if (props.value.includes(t)) {
    input.value = ''
    return
  }
  if (props.max > 0 && props.value.length >= props.max) {
    toastInfo(`最多添加 ${props.max} 个`)
    return
  }
  emit('update:value', [...props.value, t])
  input.value = ''
}

function remove(t) {
  emit('update:value', props.value.filter((x) => x !== t))
}

function onKeydown(e) {
  if (e.key === 'Enter') {
    e.preventDefault()
    add()
  } else if (e.key === 'Backspace' && input.value === '' && props.value.length) {
    // 空输入时退格删最后一个
    remove(props.value[props.value.length - 1])
  }
}
</script>

<template>
  <div class="tags-input" @click="inputEl?.focus()">
    <span v-for="t in value" :key="t" class="tag-chip">
      {{ t }}
      <!-- × 用 span 而非 button：button 是 labelable 元素，若组件被 <label> 包裹，
           label 的点击会转发给第一个 labelable 后代（第一个 ×），导致点哪都删第一个 -->
      <span class="rm" role="button" title="删除" @click.stop.prevent="remove(t)">×</span>
    </span>
    <input
      ref="inputEl"
      v-model="input"
      class="tag-add-input"
      :placeholder="value.length ? '' : placeholder"
      @keydown="onKeydown"
      @click.stop
    />
    <button type="button" class="tag-add-btn" :disabled="!input.trim()" @click.stop="add">＋ 添加</button>
  </div>
</template>

<style scoped>
.tags-input {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
  min-height: 40px;
  padding: 6px 10px;
  background: var(--bg-soft);
  border: 1px solid var(--border-soft);
  border-radius: 10px;
  cursor: text;
}
.tag-chip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 12.5px;
  color: #c3d4ff;
  background: linear-gradient(135deg, rgba(91, 140, 255, 0.18), rgba(139, 92, 246, 0.16));
  border: 1px solid rgba(91, 140, 255, 0.35);
  padding: 3px 6px 3px 12px;
  border-radius: 8px;
}
.tag-chip .rm {
  all: unset;
  cursor: pointer;
  font-size: 13px;
  line-height: 1;
  color: var(--text-3);
  width: 16px;
  height: 16px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: 50%;
  transition: all 0.15s;
}
.tag-chip .rm:hover {
  color: #fff;
  background: rgba(248, 113, 113, 0.4);
}
.tag-add-input {
  flex: 1;
  min-width: 120px;
  background: none;
  border: none;
  outline: none;
  color: var(--text);
  font-size: 13px;
  font-family: inherit;
  padding: 4px 2px;
}
.tag-add-input::placeholder {
  color: var(--text-3);
}
.tag-add-btn {
  all: unset;
  cursor: pointer;
  font-size: 12px;
  color: var(--accent);
  border: 1px dashed rgba(91, 140, 255, 0.4);
  padding: 4px 12px;
  border-radius: 8px;
  white-space: nowrap;
  transition: all 0.15s;
}
.tag-add-btn:hover:not(:disabled) {
  background: rgba(91, 140, 255, 0.15);
}
.tag-add-btn:disabled {
  opacity: 0.4;
  cursor: default;
}
</style>

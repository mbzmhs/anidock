<script setup>
// 图片上传选择器：
//  - 单图（封面/海报）: v-model 为 string
//  - 多图（相关图）:    v-model 为 array<string>
// 选择本地文件 → 上传到 /api/admin/media（哈希命名落地 media/）→ 回填路径。
import { ref } from 'vue'
import { api, errMsg, isAuthError } from '../api'
import { toastError } from '../notify'

const props = defineProps({
  value: { type: [String, Array], default: '' },
  multiple: { type: Boolean, default: false },
  placeholder: { type: String, default: '上传图片' },
})
const emit = defineEmits(['update:value'])

const fileInput = ref(null)
const uploading = ref(false)

// 图片 404 时给出可见提示（而不是裂图）
function onImgError(e) {
  e.target.style.display = 'none'
  e.target.parentElement.classList.add('broken')
  const tip = document.createElement('span')
  tip.className = 'broken-tip'
  tip.textContent = '图丢失'
  e.target.parentElement.appendChild(tip)
}

const isMulti = () => props.multiple
const paths = () => (isMulti() ? (Array.isArray(props.value) ? props.value : []) : [props.value].filter(Boolean))

function pick() {
  fileInput.value?.click()
}

async function onFiles(e) {
  const files = [...(e.target.files || [])]
  e.target.value = ''
  if (!files.length) return
  uploading.value = true
  try {
    const uploaded = await api.uploadMedia(files)
    if (isMulti()) {
      emit('update:value', [...paths(), ...uploaded])
    } else {
      // 单图：只保留最新一张
      emit('update:value', uploaded[0] || '')
    }
  } catch (err) {
    if (!isAuthError(err)) toastError('上传失败：' + errMsg(err))
  } finally {
    uploading.value = false
  }
}

function remove(p) {
  if (isMulti()) {
    emit('update:value', paths().filter((x) => x !== p))
  } else {
    emit('update:value', '')
  }
}
</script>

<template>
  <div class="img-up">
    <div v-if="paths().length" class="thumbs" :class="{ multi: multiple }">
      <div v-for="p in paths()" :key="p" class="thumb">
        <img :src="'/' + p" :alt="p" @error="onImgError" />
        <button class="rm" title="移除" @click="remove(p)">×</button>
      </div>
      <button class="add" :class="{ busy: uploading }" @click="pick">
        {{ uploading ? '上传中…' : (multiple ? '＋ 上传' : '换一张') }}
      </button>
    </div>
    <button v-else class="add wide" :class="{ busy: uploading }" @click="pick">
      {{ uploading ? '上传中…' : `＋ ${placeholder}` }}
    </button>
    <input ref="fileInput" type="file" accept="image/*" :multiple="multiple" hidden @change="onFiles" />
  </div>
</template>

<style scoped>
.img-up {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.thumbs {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}
.thumb {
  position: relative;
  width: 76px;
  height: 76px;
  border-radius: 10px;
  overflow: hidden;
  border: 1px solid var(--border);
  background: #0a0d13;
}
.thumb img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
}
.rm {
  position: absolute;
  top: 2px;
  right: 2px;
  width: 18px;
  height: 18px;
  border-radius: 50%;
  border: none;
  background: rgba(0, 0, 0, 0.7);
  color: #fff;
  font-size: 12px;
  line-height: 1;
  cursor: pointer;
  opacity: 0;
  transition: opacity 0.15s;
}
.thumb:hover .rm {
  opacity: 1;
}
.rm:hover {
  background: var(--err);
}
.thumb.broken {
  display: flex;
  align-items: center;
  justify-content: center;
}
.broken-tip {
  position: absolute;
  font-size: 11px;
  color: var(--text-3);
}
.add {
  min-width: 76px;
  height: 76px;
  border-radius: 10px;
  border: 1px dashed var(--border);
  background: rgba(91, 140, 255, 0.05);
  color: var(--text-2);
  font-size: 12px;
  cursor: pointer;
  font-family: inherit;
  transition: all 0.15s;
}
.add:hover {
  border-color: var(--accent);
  color: var(--accent);
}
.add.wide {
  width: 100%;
  height: 90px;
}
.add.busy {
  opacity: 0.6;
  cursor: wait;
}
</style>

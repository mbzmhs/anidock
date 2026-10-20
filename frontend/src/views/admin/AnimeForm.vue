<script setup>
// 番剧创建 / 编辑 页面（不是弹窗）。
//  - /admin/anime/new        创建：完整信息表；「快速导入」按钮弹模态框走 Bangumi/本地JSON
//  - /admin/anime/edit/:id   编辑：预填数据，图片可上传/移除（文件随数据持久化，失效图自动清理）
import { ref, computed, onMounted, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api, errMsg, errNotify, isAuthError } from '../../api'
import { toastInfo, toastSuccess, toastError } from '../../notify'
import ImageUploader from '../../components/ImageUploader.vue'
import TagsInput from '../../components/TagsInput.vue'

const route = useRoute()
const router = useRouter()
const isEdit = computed(() => !!route.params.id)

const form = ref(emptyForm())
const saving = ref(false)
const loading = ref(false)
const previewing = ref(false)

// 按 Bangumi ID 拉取快照预填表单（不写库）；成功后整页数据替换覆盖，失败不动
async function previewFromBangumi() {
  const q = String(form.value.bangumi_id || '').trim()
  if (!q) {
    toastInfo('先填写 Bangumi ID')
    return
  }
  previewing.value = true
  try {
    const d = await api.adminPreviewAnime('bangumi', q)
    form.value = {
      bangumi_id: d.bangumi_id || form.value.bangumi_id,
      title: d.title || '',
      title_orig: d.title_orig || '',
      year: d.year || null,
      quarter: d.quarter || null,
      genres: [...(d.genres || [])],
      aliases: [...(d.aliases || [])],
      synopsis: d.synopsis || '',
      cover: d.cover || '',
      poster: d.poster || '',
      images: [...(d.images || [])],
      studio: d.studio || '',
      info: [...(d.info || [])],
      characters: [...(d.characters || [])],
    }
  } catch (e) {
    if (!isAuthError(e)) toastError('拉取失败：' + errMsg(e) + '（页面数据未改动）')
  } finally {
    previewing.value = false
  }
}

function emptyForm() {
  return {
    bangumi_id: null,
    title: '',
    title_orig: '',
    year: null,
    quarter: null,
    genres: [],
    aliases: [],
    synopsis: '',
    cover: '',
    poster: '',
    images: [],
    studio: '',
    info: [],
    characters: [],
  }
}

// 制作信息（Bangumi 导入自动填充，只读展示）
const hasProd = computed(
  () =>
    !!form.value.studio ||
    (form.value.info || []).length > 0 ||
    (form.value.characters || []).length > 0
)
function infoText(item) {
  return item.value || (item.values || []).join(' / ')
}

const seasonLabel = computed(() => {
  if (!form.value.year || !form.value.quarter) return ''
  const name = ['', '1月', '4月', '7月', '10月'][form.value.quarter] || ''
  return `${form.value.year}-${name}`
})

// 年份范围：当前年份 → 1990（新的在前）
const currentYear = new Date().getFullYear()
const yearOptions = Array.from({ length: currentYear - 1990 + 1 }, (_, i) => ({
  label: String(currentYear - i),
  value: currentYear - i,
}))
const quarterOptions = [
  { label: '—', value: null },
  { label: '1月', value: 1 },
  { label: '4月', value: 2 },
  { label: '7月', value: 3 },
  { label: '10月', value: 4 },
]

async function loadEdit() {
  loading.value = true
  try {
    const a = await api.getAnime(route.params.id)
    form.value = {
      bangumi_id: a.bangumi_id,
      title: a.title,
      title_orig: a.title_orig || '',
      year: a.year || null,
      quarter: a.quarter || null,
      genres: [...(a.genres || [])],
      aliases: [...(a.aliases || [])],
      synopsis: a.synopsis || '',
      cover: a.cover || '',
      poster: a.poster || '',
      images: [...(a.images || [])],
      studio: a.studio || '',
      info: [...(a.info || [])],
      characters: [...(a.characters || [])],
    }
  } catch (e) {
    errNotify(e)
  } finally {
    loading.value = false
  }
}
onMounted(() => {
  if (isEdit.value) loadEdit()
})

// 路由复用：从「新建」页快速导入后跳转编辑页（同一组件不重新 mount），
// 以及编辑页之间切换时，都要按新的 :id 重新加载数据
watch(
  () => route.params.id,
  (id, oldId) => {
    if (id) loadEdit()
    else if (oldId) form.value = emptyForm()
  }
)

async function save() {
  if (!form.value.title.trim()) {
    toastInfo('标题必填')
    return
  }
  const f = form.value
  const body = {
    bangumi_id: f.bangumi_id || null,
    title: f.title.trim(),
    title_orig: f.title_orig,
    synopsis: f.synopsis,
    year: f.year || 0,
    quarter: f.quarter || 0,
    season_label: seasonLabel.value,
    genres: [...f.genres],
    aliases: [...f.aliases],
    cover: f.cover,
    poster: f.poster,
    images: f.images,
    studio: f.studio,
    info: f.info,
    characters: f.characters,
  }
  saving.value = true
  try {
    if (isEdit.value) {
      await api.adminUpdateAnime(route.params.id, body)
      toastSuccess('保存成功')
    } else {
      await api.adminCreateAnime(body)
      toastSuccess('创建成功')
      // 新建完成 → 回列表（列表刷新后该行即为新番剧）
      router.push('/admin/anime')
    }
  } catch (e) {
    errNotify(e)
  } finally {
    saving.value = false
  }
}

// 快速导入（模态框）：Bangumi ID / 本地 JSON，导入后进入该番剧编辑页
const showImport = ref(false)
const importSource = ref('bangumi')
const importQuery = ref('')
const importing = ref(false)
async function doImport() {
  if (!importQuery.value) return
  importing.value = true
  try {
    const a = await api.adminImport(importSource.value, importQuery.value)
    showImport.value = false
    router.push(`/admin/anime/edit/${a.id}`)
  } catch (e) {
    if (!isAuthError(e)) toastError('导入失败：' + errMsg(e))
  } finally {
    importing.value = false
  }
}
</script>

<template>
  <n-spin :show="loading || previewing" :description="previewing ? '正在从 Bangumi 拉取数据…' : '加载中…'">
      <div class="page">
        <div class="page-head">
          <h2>
            <span class="h-ico">{{ isEdit ? '✎' : '＋' }}</span>
            {{ isEdit ? '编辑番剧' : '创建番剧' }}
          </h2>
          <div class="head-actions">
            <n-button v-if="!isEdit" size="small" @click="showImport = true">⚡ 快速导入（Bangumi）</n-button>
            <n-button size="small" @click="router.push('/admin/anime')">返回列表</n-button>
            <n-button size="small" type="primary" class="grad-btn" :loading="saving" @click="save">
              {{ isEdit ? '保存修改' : '创建番剧' }}
            </n-button>
          </div>
        </div>

        <div class="form-grid">
          <!-- 基本信息 -->
          <section class="panel form-sec">
            <h3>基本信息</h3>
            <div class="fields">
              <label class="f-half">
                标题（中文/常用名）*
                <n-input v-model:value="form.title" placeholder="如 番剧F" />
              </label>
              <label class="f-half">
                原标题
                <n-input v-model:value="form.title_orig" placeholder="如 番剧F 假名" />
              </label>
              <label class="f-third bangumi-field">
                Bangumi ID（可空）
                <div class="bangumi-row">
                  <n-input v-model:value="form.bangumi_id" placeholder="622288" />
                  <n-button
                    size="small"
                    type="primary"
                    class="grad-btn"
                    :loading="previewing"
                    :disabled="!String(form.bangumi_id || '').trim()"
                    title="从 Bangumi 拉取数据预填本表单（不自动保存）"
                    @click="previewFromBangumi"
                  >
                    ⚡ 拉取
                  </n-button>
                </div>
              </label>
              <label class="f-third">
                年份
                <n-select v-model:value="form.year" :options="yearOptions" clearable placeholder="选择年份" />
              </label>
              <label class="f-third">
                季度
                <n-select v-model:value="form.quarter" :options="quarterOptions" placeholder="选择季度" />
              </label>
              <!-- 注意：chips 容器不能用 <label> 包（label 会把点击转发给第一个可关联控件，
                   即第一个 × 按钮 → 点哪都删第一个）。用 div + 样式模拟 label 布局。 -->
              <div class="f-full f-field">
                <span class="field-name">关键词（站内搜索命中即出结果；Bangumi 导入自动填充，可手动增删）</span>
                <TagsInput v-model:value="form.genres" placeholder="输入关键词后回车添加" />
              </div>
              <div class="f-full f-field">
                <span class="field-name">别名（RSS 标题自动匹配的关键，多写几种叫法；回车添加）</span>
                <TagsInput v-model:value="form.aliases" placeholder="如 番剧F / 番剧F 假名，回车添加" />
              </div>
            </div>
            <div v-if="seasonLabel" class="season-preview">
              将显示为 <b class="season-chip">{{ seasonLabel }}</b>（首页侧边栏按季度归类）
            </div>
          </section>

          <!-- 图片（上传，文件随数据持久化） -->
          <section class="panel form-sec">
            <h3>图片 <span class="dim">（选择本地文件上传，自动存储到 media/；移除后文件自动清理）</span></h3>
            <div class="fields">
              <label class="f-half">
                封面
                <ImageUploader v-model:value="form.cover" placeholder="上传封面" />
              </label>
              <label class="f-half">
                海报
                <ImageUploader v-model:value="form.poster" placeholder="上传海报" />
              </label>
              <label class="f-full">
                其它相关图
                <ImageUploader v-model:value="form.images" multiple placeholder="批量上传图片" />
              </label>
            </div>
          </section>

          <!-- 简介 -->
          <section class="panel form-sec">
            <h3>简介</h3>
            <n-input
              v-model:value="form.synopsis"
              type="textarea"
              :rows="8"
              placeholder="番剧简介…"
            />
          </section>

          <!-- 制作信息（Bangumi 导入自动填充，重新导入可更新） -->
          <section v-if="hasProd" class="panel form-sec">
            <h3>制作信息 <span class="dim">（Bangumi 导入自动填充，只读；重新导入可刷新）</span></h3>
            <div v-if="form.studio" class="studio-line">
              <span class="studio-label">制作</span>
              <span class="studio-chip">{{ form.studio }}</span>
            </div>
            <div v-if="(form.info || []).length" class="info-grid">
              <div v-for="item in form.info" :key="item.key" class="info-row">
                <span class="info-key">{{ item.key }}</span>
                <span class="info-val">{{ infoText(item) }}</span>
              </div>
            </div>
            <div v-if="(form.characters || []).length" class="char-wrap">
              <span class="char-chip" v-for="c in form.characters" :key="c">{{ c }}</span>
            </div>
          </section>
        </div>
      </div>
    </n-spin>

    <!-- 快速导入模态框 -->
    <n-modal v-model:show="showImport" preset="card" title="快速导入番剧" style="width: 460px">
      <div class="form">
        <label>
          元数据来源
          <n-select
            v-model:value="importSource"
            :options="[
              { label: 'Bangumi（按 ID 拉快照，自动下载封面/图片）', value: 'bangumi' },
              { label: '本地 JSON（本站 canonical 格式）', value: 'local-json' },
            ]"
          />
        </label>
        <label>
          {{ importSource === 'bangumi' ? 'Bangumi ID' : '本地 JSON 文件路径' }}
          <n-input
            v-model:value="importQuery"
            :placeholder="importSource === 'bangumi' ? '如 622288（番剧F）' : '如 /Users/you/anime.json'"
            @keyup.enter="doImport"
          />
        </label>
        <div class="import-tip">
          不知道 ID？在 bangumi.tv 搜索番剧，URL 里的数字就是 ID（如 bangumi.tv/subject/622288）。导入后可继续在页面里补充别名、上传图片。
        </div>
      </div>
      <template #footer>
        <n-space justify="end">
          <n-button @click="showImport = false">取消</n-button>
          <n-button type="primary" class="grad-btn" :loading="importing" @click="doImport">导入</n-button>
        </n-space>
      </template>
  </n-modal>
</template>

<style scoped>
.head-actions {
  display: flex;
  gap: 8px;
}
.form-grid {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.form-sec h3 {
  margin: 0 0 14px;
  font-size: 15px;
  display: flex;
  align-items: baseline;
  gap: 10px;
}
.form-sec h3 .dim {
  font-weight: 400;
}
.fields {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 14px 18px;
}
.f-full {
  grid-column: 1 / -1;
}
.f-third {
  grid-column: span 1;
}
.f-half {
  grid-column: span 1;
}
.bangumi-row {
  display: flex;
  gap: 8px;
  align-items: center;
}
.bangumi-row .n-input {
  flex: 1;
  min-width: 0;
}
.bangumi-row .n-button {
  flex-shrink: 0;
}
@media (max-width: 760px) {
  .fields {
    grid-template-columns: 1fr;
  }
  .f-half,
  .f-third {
    grid-column: 1 / -1;
  }
}
/* chips 字段的 label 替身（div 版，布局与 .fields label 一致） */
.f-field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.field-name {
  font-size: 13px;
  color: var(--text-2);
}
.season-preview {
  margin-top: 12px;
  font-size: 13px;
  color: var(--text-2);
}
.season-chip {
  color: #c3d4ff;
  background: linear-gradient(135deg, rgba(91, 140, 255, 0.25), rgba(139, 92, 246, 0.25));
  border: 1px solid rgba(91, 140, 255, 0.4);
  padding: 2px 12px;
  border-radius: 20px;
  font-size: 12px;
}
.studio-line {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 12px;
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
  gap: 4px 24px;
  max-height: 300px;
  overflow-y: auto;
  padding: 10px 14px;
  background: rgba(91, 140, 255, 0.05);
  border: 1px solid var(--border-soft);
  border-radius: 10px;
  margin-bottom: 12px;
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
@media (max-width: 760px) {
  .info-grid {
    grid-template-columns: 1fr;
  }
}
.char-wrap {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.char-chip {
  font-size: 12px;
  color: var(--text-2);
  background: rgba(139, 92, 246, 0.12);
  border: 1px solid rgba(139, 92, 246, 0.3);
  padding: 2px 10px;
  border-radius: 14px;
}
.import-tip {
  font-size: 12px;
  color: var(--text-3);
  line-height: 1.7;
  background: rgba(91, 140, 255, 0.06);
  border: 1px solid var(--border-soft);
  padding: 8px 12px;
  border-radius: 10px;
}
</style>

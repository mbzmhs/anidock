<script setup>
// 站点设置（后台侧边栏「⚙ 设置」打开）：
// 全部为"DB 覆盖 .env"的运行时热设置，保存即生效（无需重启）；留空 = 保持/回落。
// 结构上按分区组织，以后新增设置项在对应分区加行即可。
import { ref, onMounted, onBeforeUnmount, computed } from 'vue'
import { api, errMsg } from '../api'
import { toastSuccess, toastError, toastInfo } from '../notify'

const emit = defineEmits(['close'])

const s = ref(null) // 后端当前生效配置快照
const loading = ref(true)

// 表单（*New 为用户新输入值：空 = 不修改该项）
const f = ref({
  bangumi_base_url: '',
  bangumi_token: '',
  admin_password: '',
  visitor_password: '',
  clear_visitor: false,
  token_ttl: '24h',
  fetch_interval: '',
  fetch_pace: '',
  disc_max_picks: '',
  disc_min_picks: '',
  disc_pref_groups: '',
  disc_black_groups: '',
  mirror_max_size: '',
  site_pool: '',
})
const saving = ref(false)
const testing = ref(false)
const testResult = ref('') // '' | ok | error 文案

// 「按黑名单剔除已有 RSS 源」：二次确认（首次点击武装 → 再点执行；5 秒未确认自动复位）
const purgeArmed = ref(false)
const purging = ref(false)
let purgeArmTimer = null
// 黑名单有未保存的改动（剔除依据的是已保存版本，需提示）
const blackDirty = computed(() => {
  const cur = (s.value?.disc_black_groups || '').trim()
  const next = (f.value?.disc_black_groups || '').trim()
  return cur !== '' && next !== cur
})
function onPurgeClick() {
  if (purging.value) return
  if (!purgeArmed.value) {
    purgeArmed.value = true
    clearTimeout(purgeArmTimer)
    purgeArmTimer = setTimeout(() => (purgeArmed.value = false), 5000)
    return
  }
  clearTimeout(purgeArmTimer)
  purgeArmed.value = false
  doPurge()
}
async function doPurge() {
  purging.value = true
  try {
    const r = await api.purgeBlacklistedFeeds()
    const names = (r.deleted || []).map((d) => d.name)
    const shown = names.slice(0, 8).join('、') + (names.length > 8 ? ` 等 ${names.length} 个` : '')
    if (r.count === 0) {
      toastInfo('没有命中黑名单的 RSS 源')
    } else {
      toastSuccess(`已按黑名单剔除 ${r.count} 个 RSS 源${shown ? '：' + shown : ''}`)
    }
    if (r.failed?.length) toastError(`${r.failed.length} 个源删除失败：${r.failed.join('、')}`)
  } catch (e) {
    toastError('剔除失败：' + errMsg(e))
  } finally {
    purging.value = false
  }
}

const ttlOpts = computed(() => {
  const base = [
    { label: '24 小时', value: '24h' },
    { label: '72 小时', value: '72h' },
    { label: '7 天', value: '168h' },
    { label: '30 天', value: '720h' },
    { label: '90 天', value: '2160h' },
    { label: '无限制（永不过期）', value: '0' },
  ]
  const cur = s.value?.token_ttl
  if (cur && !base.some((o) => o.value === cur)) {
    base.unshift({ label: `当前：${cur}`, value: cur })
  }
  return base
})

async function load() {
  loading.value = true
  try {
    const st = await api.getSettings()
    s.value = st
    f.value = {
      bangumi_base_url: st.bangumi_base_url,
      bangumi_token: '',
      admin_password: '',
      visitor_password: '',
      clear_visitor: false,
      token_ttl: st.token_ttl || '24h',
      fetch_interval: st.fetch_interval,
      fetch_pace: st.fetch_pace || '',
      disc_max_picks: st.disc_max_picks || '',
      disc_min_picks: st.disc_min_picks || '',
      disc_pref_groups: st.disc_pref_groups || '',
      disc_black_groups: st.disc_black_groups || '',
      mirror_max_size: st.mirror_max_size,
      site_pool: st.site_pool,
    }
  } catch (e) {
    toastError('读取设置失败：' + errMsg(e))
  } finally {
    loading.value = false
  }
}

async function doTest() {
  // 先用当前表单里的新值临时保存？不——测试针对"已保存的生效值"。
  testing.value = true
  testResult.value = ''
  try {
    const r = await api.testBangumi()
    testResult.value = r.ok ? 'ok' : r.error || '连接失败'
    if (r.ok) toastSuccess('Bangumi API 连接正常')
    else toastError('测试失败：' + (r.error || '未知错误'))
  } catch (e) {
    testResult.value = errMsg(e)
    toastError('测试失败：' + errMsg(e))
  } finally {
    testing.value = false
  }
}

async function doSave() {
  const t = s.value
  const patch = {}
  // 只提交有变化的项；空串 = 删除覆盖（回落到 .env / 关闭功能）
  const url = f.value.bangumi_base_url.trim()
  if (url !== (t.bangumi_base_url || '')) patch.bangumi_base_url = url
  if (f.value.bangumi_token.trim()) patch.bangumi_token = f.value.bangumi_token.trim()
  if (f.value.admin_password) patch.admin_password = f.value.admin_password
  if (f.value.clear_visitor) {
    patch.visitor_password = '' // 关闭访客认证
  } else if (f.value.visitor_password) {
    patch.visitor_password = f.value.visitor_password
  }
  if (f.value.token_ttl !== (t.token_ttl || '24h')) patch.token_ttl = f.value.token_ttl
  if (f.value.fetch_interval.trim() !== (t.fetch_interval || ''))
    patch.fetch_interval = f.value.fetch_interval.trim()
  if (f.value.fetch_pace.trim() !== (t.fetch_pace || ''))
    patch.fetch_pace = f.value.fetch_pace.trim()
  if (f.value.disc_max_picks.trim() !== (t.disc_max_picks || ''))
    patch.disc_max_picks = f.value.disc_max_picks.trim()
  if (f.value.disc_min_picks.trim() !== (t.disc_min_picks || ''))
    patch.disc_min_picks = f.value.disc_min_picks.trim()
  if (f.value.disc_pref_groups.trim() !== (t.disc_pref_groups || ''))
    patch.disc_pref_groups = f.value.disc_pref_groups.trim()
  if (f.value.disc_black_groups.trim() !== (t.disc_black_groups || ''))
    patch.disc_black_groups = f.value.disc_black_groups.trim()
  if (f.value.mirror_max_size.trim() !== (t.mirror_max_size || ''))
    patch.mirror_max_size = f.value.mirror_max_size.trim()
  if (f.value.site_pool.trim() !== (t.site_pool || '')) patch.site_pool = f.value.site_pool.trim()

  if (!Object.keys(patch).length) {
    toastInfo('没有需要保存的更改')
    return
  }
  saving.value = true
  try {
    await api.saveSettings(patch)
    toastSuccess('设置已保存，立即生效')
    await load()
    emit('close')
  } catch (e) {
    toastError('保存失败：' + errMsg(e))
  } finally {
    saving.value = false
  }
}

onMounted(load)
onBeforeUnmount(() => clearTimeout(purgeArmTimer))
</script>

<template>
  <n-modal :show="true" preset="card" title="站点设置" style="width: 620px" @close="emit('close')">
    <div v-if="s" class="set">
      <!-- 访问控制 -->
      <section class="set-sec">
        <h4>访问控制</h4>
        <div class="set-row">
          <div class="set-field">
            <span class="set-name">访客口令（防爬虫）</span>
            <span class="set-badge" v-if="s.visitor_enabled">已开启</span>
            <span class="set-badge off" v-else>未开启（站点公开）</span>
          </div>
          <n-input
            v-model:value="f.visitor_password"
            type="password"
            show-password-on="click"
            placeholder="留空 = 不修改；设置后访客需输入口令才能浏览"
            :disabled="f.clear_visitor"
          />
        </div>
        <label class="set-check">
          <n-checkbox v-model:checked="f.clear_visitor" /> 清除访客口令（关闭访客认证，恢复公开访问）
        </label>
        <div class="set-row">
          <div class="set-field">
            <span class="set-name">登录有效期（后台 + 访客共用）</span>
          </div>
          <n-select v-model:value="f.token_ttl" :options="ttlOpts" style="width: 220px" />
        </div>
        <div class="set-row">
          <div class="set-field">
            <span class="set-name">后台管理口令</span>
            <span class="set-badge" v-if="s.admin_pw_set">已设置</span>
            <span class="set-badge" v-if="s.admin_pw_source === 'db'">来自设置页</span>
          </div>
          <n-input
            v-model:value="f.admin_password"
            type="password"
            show-password-on="click"
            placeholder="留空 = 保持当前口令；输入新值则覆盖（.env 的 ADMIN_PASSWORD 仍为兜底）"
          />
        </div>
      </section>

      <!-- Bangumi API -->
      <section class="set-sec">
        <h4>Bangumi API</h4>
        <div class="set-row">
          <div class="set-field">
            <span class="set-name">API 地址</span>
            <span class="set-badge" v-if="s.bangumi_base_source === 'db'">来自设置页</span>
          </div>
          <n-input v-model:value="f.bangumi_base_url" placeholder="https://api.bgm.tv" />
        </div>
        <div class="set-row">
          <div class="set-field">
            <span class="set-name">API Token</span>
            <span class="dim2" v-if="s.bangumi_token_masked">当前：{{ s.bangumi_token_masked }}</span>
            <span class="set-badge" v-if="s.bangumi_token_source === 'db'">来自设置页</span>
          </div>
          <div class="set-inline">
            <n-input
              v-model:value="f.bangumi_token"
              type="password"
              show-password-on="click"
              placeholder="留空 = 保持当前 token；token 过期时到 bangumi.tv → 设置 → 开放接口 重新获取"
            />
            <n-button size="small" :loading="testing" @click="doTest">测试连接</n-button>
          </div>
        </div>
        <div v-if="testResult" class="set-test" :class="{ ok: testResult === 'ok' }">
          {{ testResult === 'ok' ? '✓ 连接正常' : '✗ ' + testResult }}
        </div>
        <p class="set-tip">token/地址修改保存后立即生效（导入、拉封面、快速导入都会用新值），无需重启。</p>
      </section>

      <!-- 抓取任务 -->
      <section class="set-sec">
        <h4>抓取任务</h4>
        <div class="set-row">
          <div class="set-field">
            <span class="set-name">RSS 轮询间隔</span>
            <span class="set-desc inline">每轮前另有 0~90s 随机延迟，请求间隔叠加随机抖动</span>
          </div>
          <n-input v-model:value="f.fetch_interval" placeholder="如 3m / 30m / 2h（至少 1 分钟）" style="width: 240px" />
        </div>
        <div class="set-row">
          <div class="set-field">
            <span class="set-name">请求节流（单请求间隔）</span>
            <span class="set-desc inline">实际 = 间隔 + 0~间隔随机抖动；0 = 关闭，默认 2s；403/429 自动冷却 15 分钟</span>
          </div>
          <n-input v-model:value="f.fetch_pace" placeholder="如 1s / 2s / 5s（0 = 关闭，默认 2s）" style="width: 240px" />
        </div>
        <div class="set-row">
          <div class="set-field"><span class="set-name">镜像单文件大小上限</span></div>
          <n-input v-model:value="f.mirror_max_size" placeholder="如 512MiB / 4GiB（至少 1MiB）" style="width: 240px" />
        </div>
        <div class="set-row">
          <div class="set-field">
            <span class="set-name">自动添加 · 最多选择</span>
            <span class="set-desc inline">一次自动添加最多建几个源（新建源按选择名次定优先级：排在已有源之后，名次每后一位 +10，数值小 = 优先）</span>
          </div>
          <n-input v-model:value="f.disc_max_picks" placeholder="默认 2" style="width: 240px" />
        </div>
        <div class="set-row">
          <div class="set-field">
            <span class="set-name">自动添加 · 最少选择</span>
            <span class="set-desc inline">至少建几个（不超过最多数量）</span>
          </div>
          <n-input v-model:value="f.disc_min_picks" placeholder="默认 1" style="width: 240px" />
        </div>
        <div class="set-row stack">
          <div class="set-field col">
            <span class="set-name">自动添加 · 偏好字幕组/关键词</span>
            <p class="set-desc">
              自动添加时，组名/关键词包含这些词的候选<b>优先入选</b>，<b>越靠前的行优先级越高</b>（空格或换行分隔均可）。
              未命中任何偏好词的候选按兜底规则排序：<b>集数多的靠前 → 首发日期早的靠前</b>。留空 = 纯兜底排序。
            </p>
          </div>
          <n-input
            v-model:value="f.disc_pref_groups"
            type="textarea"
            :autosize="{ minRows: 2, maxRows: 5 }"
            placeholder="字幕组名或关键词片段，一行一个（越靠前优先级越高）"
          />
        </div>
        <div class="set-row stack">
          <div class="set-field col">
            <span class="set-name">自动添加 · 关键词黑名单</span>
            <p class="set-desc">
              组名/关键词包含这些词的候选<b>不会被自动选中</b>（空格或换行分隔，无顺序语义）。
              仅影响自动添加；手动勾选创建不受限。留空 = 不排除。
            </p>
          </div>
          <n-input
            v-model:value="f.disc_black_groups"
            type="textarea"
            :autosize="{ minRows: 2, maxRows: 5 }"
            placeholder="组名或关键词片段，一行一个（命中的不会被自动选中）"
          />
        </div>
        <div class="set-row stack">
          <div class="set-field col">
            <span class="set-name">按黑名单剔除已有源</span>
            <p class="set-desc">
              删除所有番剧中命中已保存黑名单的 RSS 源（连同更新记录）。
              <template v-if="blackDirty"
                ><b>注意：黑名单有未保存的修改，剔除将按已保存版本执行。</b></template
              >
            </p>
          </div>
          <!-- 包一层：避开 .set-row.stack > :last-child 的 width:100%（那规则是给 textarea 的），按钮保持自然宽度 -->
          <div>
            <n-button
              size="small"
              :type="purgeArmed ? 'error' : 'warning'"
              :loading="purging"
              :disabled="loading || !(s?.disc_black_groups || '').trim()"
              :title="
                purgeArmed
                  ? '再点一次确认执行：删除所有命中黑名单的 RSS 源（不可恢复）'
                  : '按已保存的关键词黑名单，批量删除所有番剧的命中 RSS 源'
              "
              @click="onPurgeClick"
            >
              {{ purging ? '剔除中…' : purgeArmed ? '确认剔除？（不可恢复）' : '🧹 按黑名单剔除已有 RSS 源' }}
            </n-button>
          </div>
        </div>
        <div class="set-row stack">
          <div class="set-field col">
            <span class="set-name">订阅站点池（自动 RSS）</span>
            <p class="set-desc">
              自动发现/建源只使用这里配置的站点：一个字幕组 = 一条逻辑源，抓取时自动向池内所有站点要同一关键词的订阅
              （兄弟站数据相同，主站挂掉备站自动接管，加站/减站即时生效、无需重新建源）。
              每行一个条目：URL 模板（<code>{}</code> = 关键词占位符，如 <code>https://example.org/rss-{}.xml</code>）或站点地址（按 <code>&lt;站点根&gt;/rss-&lt;关键词&gt;.xml</code> 形态处理）；<b>默认为空 = 不从任何站点抓自动 RSS</b>。
            </p>
          </div>
          <n-input
            v-model:value="f.site_pool"
            type="textarea"
            :autosize="{ minRows: 2, maxRows: 6 }"
            placeholder="https://example.org/rss-{}.xml&#10;https://example.com/"
          />
        </div>
      </section>

      <!-- 只读信息 -->
      <section class="set-sec">
        <h4>运行信息（只读，改这些需要 .env + 重启）</h4>
        <div class="set-readonly">
          <span>端口 <b>{{ s.readonly.port }}</b></span>
          <span>数据库 <b>{{ s.readonly.db }}</b></span>
          <span>图片目录 <b>{{ s.readonly.media }}</b></span>
          <span>镜像目录 <b>{{ s.readonly.mirror }}</b></span>
        </div>
      </section>
    </div>

    <template #footer>
      <n-space justify="end">
        <n-button @click="emit('close')">关闭</n-button>
        <n-button type="primary" class="grad-btn" :loading="saving || loading" @click="doSave">保存设置</n-button>
      </n-space>
    </template>
  </n-modal>
</template>

<style scoped>
.set {
  display: flex;
  flex-direction: column;
  gap: 18px;
  max-height: 68vh;
  overflow-y: auto;
  padding-right: 4px;
}
.set-sec {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 14px 16px;
  border: 1px solid var(--border-soft);
  border-radius: 12px;
  background: rgba(91, 140, 255, 0.04);
}
.set-sec h4 {
  margin: 0;
  font-size: 13px;
  color: var(--accent);
}
.set-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}
.set-row > :last-child {
  flex: 1;
  max-width: 420px;
}
.set-row.stack {
  flex-direction: column;
  align-items: stretch;
  gap: 8px;
}
.set-row.stack > :last-child {
  max-width: none;
  width: 100%;
}
.set-field {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  flex-shrink: 0;
}
.set-name {
  font-size: 13px;
  color: var(--text-2);
  white-space: nowrap;
}
.set-field.col {
  flex-direction: column;
  align-items: flex-start;
  gap: 4px;
}
.set-desc {
  margin: 0;
  font-size: 11.5px;
  line-height: 1.5;
  color: var(--text-3);
}
/* 行内说明（单行字段名右侧）：超长省略 + 悬停看全，避免把输入框挤出布局 */
.set-desc.inline {
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  max-width: 260px;
  cursor: help;
}
.set-inline {
  display: flex;
  align-items: center;
  gap: 8px;
}
.set-check {
  font-size: 12.5px;
  color: var(--text-2);
  user-select: none;
}
.set-badge {
  font-size: 11px;
  color: #c3d4ff;
  background: rgba(91, 140, 255, 0.15);
  border: 1px solid rgba(91, 140, 255, 0.35);
  padding: 1px 8px;
  border-radius: 20px;
  white-space: nowrap;
}
.set-badge.off {
  color: var(--text-3);
  background: var(--bg-soft);
  border-color: var(--border-soft);
}
.dim2 {
  font-size: 12px;
  color: var(--text-3);
  white-space: nowrap;
}
.set-test {
  font-size: 12.5px;
  color: var(--err);
}
.set-test.ok {
  color: var(--ok);
}
.set-tip {
  margin: 0;
  font-size: 12px;
  color: var(--text-3);
}
.set-readonly {
  display: flex;
  flex-wrap: wrap;
  gap: 10px 22px;
  font-size: 12.5px;
  color: var(--text-2);
}
.set-readonly b {
  color: var(--text);
  font-weight: 600;
}
</style>

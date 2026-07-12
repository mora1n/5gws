<template>
  <div class="panel-section flex flex-wrap items-center justify-between gap-3">
    <div><h2 class="text-lg font-semibold">日志</h2><div class="mt-1 flex items-center gap-2 text-xs text-base-content/55"><span class="size-2 rounded-full" :class="streamState === '实时' ? 'bg-success' : 'bg-warning'" />{{ streamState }} · {{ lastRefreshText }}</div></div>
    <div class="flex items-center gap-1">
      <button class="btn btn-ghost btn-square btn-sm" title="到底部" @click="scrollBottom"><ArrowDownToLine class="size-4" /></button>
      <button class="btn btn-ghost btn-square btn-sm" title="下载日志" @click="download"><Download class="size-4" /></button>
      <button class="btn btn-ghost btn-square btn-sm" title="刷新" :disabled="refreshing" @click="refresh"><RefreshCw class="size-4" :class="{ 'animate-spin': refreshing }" /></button>
    </div>
  </div>
  <section class="panel-section">
    <div class="mb-3 flex flex-wrap items-center gap-3">
      <label class="input input-sm flex min-w-0 flex-1 items-center gap-2 sm:max-w-md"><Search class="size-4 text-base-content/45" /><input v-model="query" class="min-w-0 flex-1" placeholder="搜索日志" /></label>
      <label class="flex items-center gap-2 text-sm"><input v-model="follow" type="checkbox" class="checkbox checkbox-sm" />跟随</label>
      <span class="text-xs text-base-content/50">{{ visibleLines.length }} 行</span>
    </div>
    <pre ref="viewer" class="h-[min(65vh,42rem)] overflow-auto border border-base-300 bg-neutral p-4 text-xs leading-5 text-neutral-content">{{ filteredLogs || '暂无日志' }}</pre>
  </section>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { ArrowDownToLine, Download, RefreshCw, Search } from '@lucide/vue'
import { api } from '@/api'

const logs = ref(''), refreshing = ref(false), lastRefreshed = ref(''), query = ref(''), follow = ref(true)
const streamState = ref<'实时' | '重连中'>('重连中')
const viewer = ref<HTMLElement | null>(null)
let source: EventSource | null = null
const emit = defineEmits<{ error: [value: string] }>()
const lastRefreshText = computed(() => lastRefreshed.value ? `更新于 ${lastRefreshed.value}` : '尚未更新')
const visibleLines = computed(() => {
  const lines = logs.value.split('\n')
  const value = query.value.trim().toLowerCase()
  return value ? lines.filter(line => line.toLowerCase().includes(value)) : lines
})
const filteredLogs = computed(() => visibleLines.value.join('\n'))

function report(cause: unknown) { emit('error', cause instanceof Error ? cause.message : String(cause)) }
function update(value: string) { logs.value = value; lastRefreshed.value = new Date().toLocaleTimeString() }
async function refresh() {
  if (refreshing.value) return
  refreshing.value = true
  try { update((await api.logs()).logs) } catch (cause) { report(cause) } finally { refreshing.value = false }
}
function connect() {
  source?.close()
  source = new EventSource('/api/v1/logs/stream')
  source.onopen = () => { streamState.value = '实时' }
  source.onerror = () => { streamState.value = '重连中' }
  source.onmessage = event => {
    try { update((JSON.parse(event.data) as { logs: string }).logs) } catch (cause) { report(cause) }
  }
}
function scrollBottom() { if (viewer.value) viewer.value.scrollTop = viewer.value.scrollHeight }
function download() {
  const url = URL.createObjectURL(new Blob([filteredLogs.value], { type: 'text/plain;charset=utf-8' }))
  const anchor = document.createElement('a')
  anchor.href = url; anchor.download = `5gws-${new Date().toISOString().replace(/[:.]/g, '-')}.log`; anchor.click()
  URL.revokeObjectURL(url)
}
watch(logs, async () => { if (follow.value) { await nextTick(); scrollBottom() } })
onMounted(() => { void refresh(); connect() })
onUnmounted(() => { source?.close() })
</script>

<template><div class="panel-section flex items-center justify-between"><div><h2 class="text-lg font-semibold">日志</h2><div class="mt-1 text-xs text-base-content/55">{{ lastRefreshText }}</div></div><button class="btn btn-ghost btn-square btn-sm" title="刷新" :disabled="refreshing" @click="refresh"><RefreshCw class="size-4" :class="{ 'animate-spin': refreshing }" /></button></div>
  <section class="panel-section"><pre class="h-[min(65vh,42rem)] overflow-auto border border-base-300 bg-neutral p-4 text-xs leading-5 text-neutral-content">{{ logs || '暂无日志' }}</pre></section></template>
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { RefreshCw } from '@lucide/vue'
import { api } from '@/api'

const logs = ref(''), refreshing = ref(false), lastRefreshed = ref('')
const emit = defineEmits<{ error: [value: string] }>()
const lastRefreshText = computed(() => lastRefreshed.value ? `最后刷新 ${lastRefreshed.value}` : '尚未刷新')

function report(cause: unknown){ emit('error', cause instanceof Error ? cause.message : String(cause)) }
async function refresh(){
  if(refreshing.value)return
  refreshing.value = true
  try {
    logs.value = (await api.logs()).logs
    lastRefreshed.value = new Date().toLocaleString()
  } catch (cause) { report(cause) }
  finally { refreshing.value = false }
}
onMounted(refresh)
</script>

<template>
  <div class="panel-section"><h2 class="text-lg font-semibold">运行概览</h2></div>
  <div class="grid grid-cols-3 gap-2 p-4 sm:gap-3 sm:p-6">
    <div v-for="stat in stats" :key="stat.label" class="min-w-0 border border-base-300 bg-base-100 p-3 sm:p-4">
      <div class="truncate text-xs text-base-content/60 sm:text-sm">{{ stat.label }}</div><div class="mt-2 truncate text-xl font-semibold sm:text-2xl">{{ stat.value }}</div>
    </div>
  </div>
  <section class="panel-section">
    <div class="mb-3 flex items-center justify-between"><div><h3 class="font-semibold">最近一小时</h3><div class="mt-1 text-xs text-base-content/55">{{ metricTime }}</div></div><button class="btn btn-ghost btn-square btn-sm" title="刷新运行数据" :disabled="runtimeBusy" @click="$emit('refresh-runtime')"><RefreshCw class="size-4" :class="{ 'animate-spin': runtimeBusy }" /></button></div>
    <div class="grid grid-cols-2 gap-2 sm:gap-3 xl:grid-cols-4">
      <div v-for="trend in trends" :key="trend.label" class="border border-base-300 bg-base-100 p-3">
        <div class="flex items-baseline justify-between gap-2"><span class="text-sm text-base-content/60">{{ trend.label }}</span><span class="font-medium">{{ trend.value }}</span></div>
        <SparklineChart class="mt-3 text-primary" :values="trend.values" :label="`${trend.label}趋势`" />
      </div>
    </div>
  </section>
  <section class="panel-section">
    <div class="mb-3 flex flex-wrap items-center justify-between gap-2"><div><h3 class="font-semibold">运行健康</h3><div class="mt-1 text-xs text-base-content/55">{{ diagnosticTime }}</div></div></div>
    <div class="overflow-x-auto border border-base-300"><table class="table table-sm"><thead><tr><th>项目</th><th>状态</th><th>详情</th></tr></thead><tbody>
      <tr v-for="item in health" :key="item.label"><td class="font-medium">{{ item.label }}</td><td><span class="badge badge-sm whitespace-nowrap" :class="statusClass(item.status)">{{ statusText(item.status) }}</span></td><td class="max-w-xl break-words text-sm text-base-content/65">{{ item.detail }}</td></tr>
    </tbody></table></div>
  </section>
  <section class="panel-section"><div class="mb-3 flex items-center justify-between"><h3 class="font-semibold">受管进程</h3><button class="btn btn-ghost btn-square btn-sm" title="刷新" @click="$emit('refresh')"><RefreshCw class="size-4" /></button></div>
    <div class="overflow-x-auto border border-base-300"><table class="table table-sm"><thead><tr><th>组件</th><th>PID</th><th>状态</th></tr></thead><tbody><tr v-for="process in dashboard?.processes" :key="`${process.name}-${process.pid}`"><td class="font-medium">{{ process.name }}</td><td class="mono">{{ process.pid }}</td><td><span class="badge badge-success badge-sm">运行中</span></td></tr></tbody></table></div>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { RefreshCw } from '@lucide/vue'
import SparklineChart from '@/components/SparklineChart.vue'
import type { Dashboard, Diagnostics, Metric } from '@/types'

const props = defineProps<{ dashboard: Dashboard | null; metrics: Metric[]; diagnostics: Diagnostics | null; runtimeBusy: boolean }>()
defineEmits<{ refresh: []; 'refresh-runtime': [] }>()

const stats = computed(() => [{ label: '版本', value: props.dashboard?.version || '-' }, { label: '受管进程', value: props.dashboard?.processes.length ?? '-' }, { label: '规则', value: props.dashboard?.rules ?? '-' }])
const latest = computed(() => props.metrics.at(-1))
const rates = computed(() => props.metrics.slice(1).map((metric, index) => {
  const previous = props.metrics[index]
  const seconds = metric.timestamp - previous.timestamp
  if (seconds <= 0) return 0
  const bytes = Math.max(0, metric.rx_bytes - previous.rx_bytes) + Math.max(0, metric.tx_bytes - previous.tx_bytes)
  return bytes / seconds
}))
const trends = computed(() => [
  { label: 'DNS 延迟', value: latest.value?.dns_ok ? `${latest.value.dns_latency_ms.toFixed(1)} ms` : '失败', values: props.metrics.filter(item => item.dns_ok).map(item => item.dns_latency_ms) },
  { label: '托管内存', value: formatBytes(latest.value?.rss_bytes), values: props.metrics.map(item => item.rss_bytes) },
  { label: '主机 TCP', value: String(latest.value?.tcp_connections ?? '-'), values: props.metrics.map(item => item.tcp_connections) },
  { label: latest.value?.interface ? `${latest.value.interface} 吞吐` : '接口吞吐', value: formatRate(rates.value.at(-1)), values: rates.value },
])
const metricTime = computed(() => latest.value ? `更新于 ${new Date(latest.value.timestamp * 1000).toLocaleString()}` : '暂无指标')
const diagnosticTime = computed(() => props.diagnostics ? `检测于 ${new Date(props.diagnostics.checked_at).toLocaleString()}` : '尚未检测')
const health = computed(() => {
  if (!props.diagnostics) return [{ label: '诊断', status: 'unknown', detail: '等待检测' }]
  const dns = props.diagnostics.dns || []
  const exits = props.diagnostics.exits || []
  const rows = ['cn', 'overseas_private', 'overseas_public'].map(pool => {
    const items = dns.filter(item => item.pool === pool)
    const failed = items.filter(item => item.status !== 'ok')
    return { label: `DNS · ${pool}`, status: items.length && !failed.length ? 'ok' : 'error', detail: items.length ? `${items.length - failed.length}/${items.length} 个上游可用` : '无检测结果' }
  })
  const dot = props.diagnostics.dot
  rows.unshift({ label: 'DoT', status: dot?.status || 'unknown', detail: dot?.status === 'ok' ? `${dot.latency_ms?.toFixed(1)} ms · 证书剩余 ${dot.days_remaining} 天` : dot?.error || '无检测结果' })
  const failedExits = exits.filter(item => item.status !== 'ok')
  rows.push({ label: '出口', status: exits.length && !failedExits.length ? 'ok' : 'error', detail: exits.length ? `${exits.length - failedExits.length}/${exits.length} 个出口可用` : '无检测结果' })
  return rows
})
function formatBytes(value?: number) { if (value == null) return '-'; const units = ['B', 'KB', 'MB', 'GB']; let size = value, unit = 0; while (size >= 1024 && unit < units.length - 1) { size /= 1024; unit++ } return `${size.toFixed(unit ? 1 : 0)} ${units[unit]}` }
function formatRate(value?: number) { return value == null ? '-' : `${formatBytes(value)}/s` }
function statusClass(status: string) { return status === 'ok' ? 'badge-success' : status === 'warning' ? 'badge-warning' : status === 'unknown' ? 'badge-ghost' : 'badge-error' }
function statusText(status: string) { return status === 'ok' ? '正常' : status === 'warning' ? '警告' : status === 'unknown' ? '待检测' : '异常' }
</script>

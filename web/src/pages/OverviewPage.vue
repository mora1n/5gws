<template>
  <div class="panel-section"><h2 class="text-lg font-semibold">运行概览</h2></div>
  <div class="grid gap-3 p-4 sm:grid-cols-2 xl:grid-cols-3 sm:p-6">
    <div v-for="stat in stats" :key="stat.label" class="border border-base-300 bg-base-100 p-4"><div class="text-sm text-base-content/60">{{ stat.label }}</div><div class="mt-2 text-2xl font-semibold">{{ stat.value }}</div></div>
  </div>
  <section class="panel-section"><div class="mb-3 flex items-center justify-between"><h3 class="font-semibold">受管进程</h3><button class="btn btn-ghost btn-square btn-sm" title="刷新" @click="$emit('refresh')"><RefreshCw class="size-4" /></button></div>
    <div class="overflow-x-auto border border-base-300"><table class="table table-sm"><thead><tr><th>组件</th><th>PID</th><th>状态</th></tr></thead><tbody><tr v-for="process in dashboard?.processes" :key="`${process.name}-${process.pid}`"><td class="font-medium">{{ process.name }}</td><td class="mono">{{ process.pid }}</td><td><span class="badge badge-success badge-sm">运行中</span></td></tr></tbody></table></div>
  </section>
</template>
<script setup lang="ts">
import { computed } from 'vue'; import { RefreshCw } from '@lucide/vue'; import type { Dashboard } from '@/types'
const props = defineProps<{ dashboard: Dashboard | null }>(); defineEmits<{ refresh: [] }>()
const stats = computed(() => [{ label: '版本', value: props.dashboard?.version || '-' }, { label: '活动版本', value: props.dashboard?.active_revision ?? '-' }, { label: '规则', value: props.dashboard?.rules ?? '-' }])
</script>

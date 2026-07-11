<template><div class="panel-section flex items-center justify-between"><h2 class="text-lg font-semibold">版本历史</h2><button class="btn btn-ghost btn-square btn-sm" title="刷新" @click="load"><RefreshCw class="size-4" /></button></div>
  <section class="panel-section"><div class="overflow-x-auto border border-base-300"><table class="table table-sm"><thead><tr><th>ID</th><th>状态</th><th>创建时间</th><th>错误</th><th></th></tr></thead><tbody><tr v-for="item in revisions" :key="item.id"><td class="mono">{{ item.id }}</td><td><span class="badge badge-sm" :class="item.status === 'active' ? 'badge-success' : item.status === 'failed' ? 'badge-error' : 'badge-ghost'">{{ item.status }}</span></td><td>{{ item.created_at || '-' }}</td><td class="max-w-xs truncate text-error">{{ item.error || '-' }}</td><td><button v-if="item.status === 'superseded'" class="btn btn-ghost btn-sm" @click="rollback(item.id)"><RotateCcw class="size-4" />回滚</button></td></tr></tbody></table></div></section></template>
<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { RefreshCw, RotateCcw } from '@lucide/vue'
import { api } from '@/api'
import type { Revision } from '@/types'
const revisions = ref<Revision[]>([])
const emit = defineEmits<{ changed: []; error: [value: string] }>()
function report(cause: unknown) { emit('error', cause instanceof Error ? cause.message : String(cause)) }
async function load() { try { revisions.value = (await api.revisions()).revisions } catch (cause) { report(cause) } }
async function rollback(id: number) { try { await api.rollback(id); await load(); emit('changed') } catch (cause) { report(cause) } }
onMounted(load)
</script>

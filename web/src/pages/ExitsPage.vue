<template>
  <div class="panel-section flex items-center justify-between"><h2 class="text-lg font-semibold">出口</h2><button class="btn btn-neutral btn-sm" @click="add"><Plus class="size-4" />SS 出口</button></div>
  <section class="panel-section">
    <div class="mb-3 flex flex-wrap items-center justify-between gap-2"><div><h3 class="font-semibold">已应用出口状态</h3><div class="mt-1 text-xs text-base-content/55">{{ checkedAt }}</div></div><button class="btn btn-ghost btn-square btn-sm" title="检测出口" :disabled="diagnosticsBusy" @click="$emit('refresh-diagnostics')"><RefreshCw class="size-4" :class="{ 'animate-spin': diagnosticsBusy }" /></button></div>
    <div class="hidden overflow-x-auto border border-base-300 md:block"><table class="table table-sm"><thead><tr><th>出口</th><th>节点</th><th>代理出网</th><th>出口 IP</th><th>详情</th></tr></thead><tbody>
      <tr v-for="item in diagnostics?.exits || []" :key="item.name"><td><div class="font-medium">{{ item.name }}</div><div class="text-xs text-base-content/50">{{ item.type }}</div></td><td><span v-if="item.type === 'direct'" class="text-base-content/40">-</span><span v-else class="badge badge-sm" :class="item.upstream_status === 'ok' ? 'badge-success' : 'badge-error'">{{ item.upstream_status === 'ok' ? `${item.upstream_latency_ms?.toFixed(1)} ms` : '异常' }}</span></td><td><span class="badge badge-sm" :class="item.egress_status === 'ok' ? 'badge-success' : 'badge-error'">{{ item.egress_status === 'ok' ? `${item.egress_latency_ms?.toFixed(1)} ms` : '异常' }}</span></td><td class="mono text-sm">{{ item.egress_ip || '-' }}</td><td class="max-w-sm break-words text-xs text-error">{{ item.error || '' }}</td></tr>
      <tr v-if="!diagnostics?.exits?.length"><td colspan="5" class="text-center text-base-content/50">暂无检测结果</td></tr>
    </tbody></table></div>
    <div class="divide-y divide-base-300 border border-base-300 md:hidden">
      <div v-for="item in diagnostics?.exits || []" :key="`${item.name}-mobile`" class="p-3">
        <div class="min-w-0"><div class="break-words font-medium">{{ item.name }}</div><div class="text-xs text-base-content/50">{{ item.type }}</div></div>
        <div class="mt-3 grid grid-cols-2 gap-3 text-sm"><div><div class="text-xs text-base-content/50">节点</div><div class="mt-1">{{ item.type === 'direct' ? '-' : item.upstream_status === 'ok' ? `${item.upstream_latency_ms?.toFixed(1)} ms` : '异常' }}</div></div><div><div class="text-xs text-base-content/50">代理出网</div><div class="mt-1">{{ item.egress_status === 'ok' ? `${item.egress_latency_ms?.toFixed(1)} ms` : '异常' }}</div></div></div>
        <div class="mt-3"><div class="text-xs text-base-content/50">出口 IP</div><div class="mt-1 break-all mono text-sm">{{ item.egress_ip || '-' }}</div></div>
        <div v-if="item.error" class="mt-2 break-words text-xs text-error">{{ item.error }}</div>
      </div>
      <div v-if="!diagnostics?.exits?.length" class="p-6 text-center text-sm text-base-content/50">暂无检测结果</div>
    </div>
  </section>
  <section class="panel-section"><div class="overflow-x-auto border border-base-300"><table class="table"><thead><tr><th>名称</th><th>类型</th><th>服务器</th><th>本地 SOCKS</th><th></th></tr></thead><tbody>
    <tr v-for="(exit, index) in bundle.config.exits" :key="exit.name"><td><input v-model.trim="exit.name" class="input input-sm w-36" :disabled="exit.type === 'direct'" /></td><td><span class="badge badge-ghost">{{ exit.type }}</span></td><td><span v-if="exit.type === 'direct'">-</span><div v-else class="flex gap-2"><input v-model.trim="exit.server" class="input input-sm w-40" /><input v-model.number="exit.server_port" type="number" class="input input-sm w-24" /></div></td><td class="mono text-sm">{{ exit.type === 'direct' ? '-' : `${exit.listen_address}:${exit.listen_port}` }}</td><td><button v-if="exit.type !== 'direct'" class="btn btn-ghost btn-square btn-sm text-error" title="删除" @click="bundle.config.exits.splice(index, 1)"><Trash2 class="size-4" /></button></td></tr>
  </tbody></table></div></section>
  <section v-for="exit in ssExits" :key="exit.name" class="panel-section"><h3 class="mb-4 font-semibold">{{ exit.name }}</h3><div class="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
    <label><span class="field-label">加密方法</span><input v-model.trim="exit.method" class="input w-full" /></label><label><span class="field-label">密码</span><input v-model="exit.password" type="password" class="input w-full" /></label>
    <label><span class="field-label">本地地址</span><input v-model.trim="exit.listen_address" class="input w-full mono" /></label><label><span class="field-label">本地端口</span><input v-model.number="exit.listen_port" type="number" class="input w-full" /></label>
  </div></section>
</template>
<script setup lang="ts">
import { computed } from 'vue'; import { Plus, RefreshCw, Trash2 } from '@lucide/vue'; import type { Bundle, Diagnostics } from '@/types'
const bundle = defineModel<Bundle>('bundle', { required: true }); const props = defineProps<{ diagnostics: Diagnostics | null; diagnosticsBusy: boolean }>(); defineEmits<{ 'refresh-diagnostics': [] }>(); const ssExits = computed(() => bundle.value.config.exits.filter(e => e.type === 'shadowsocks-rust'))
const checkedAt = computed(() => props.diagnostics ? `检测于 ${new Date(props.diagnostics.checked_at).toLocaleString()}` : '尚未检测')
function add() { let port = 1080; const used = new Set(bundle.value.config.exits.map(e => e.listen_port)); while (used.has(port)) port++; bundle.value.config.exits.push({ name: `ss${ssExits.value.length + 1}`, type: 'shadowsocks-rust', fwmark: 0, server: '', server_port: 8388, method: '2022-blake3-aes-128-gcm', password: '', username: 'default', listen_address: '127.0.0.1', listen_port: port, tcp: true, udp: true, timeout_seconds: 300 }) }
</script>

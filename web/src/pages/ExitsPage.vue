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
  <section class="panel-section"><div class="overflow-x-auto border border-base-300"><table class="table"><thead><tr><th>名称</th><th>类型</th><th>服务器</th><th></th></tr></thead><tbody>
    <tr v-for="(exit, index) in bundle.config.exits" :key="index"><td><input v-model="exit.name" class="input input-sm w-36" :disabled="exit.type === 'direct'" /></td><td><span class="badge badge-ghost">{{ exit.type }}</span></td><td><span v-if="exit.type === 'direct'">-</span><div v-else class="flex gap-2"><input v-model.trim="exit.server" class="input input-sm w-40" /><input v-model.number="exit.server_port" type="number" class="input input-sm w-24" /></div></td><td><button v-if="exit.type !== 'direct'" class="btn btn-ghost btn-square btn-sm text-error" title="删除" @click="bundle.config.exits.splice(index, 1)"><Trash2 class="size-4" /></button></td></tr>
  </tbody></table></div></section>
  <section v-for="(exit, index) in ssExits" :key="index" class="panel-section"><h3 class="mb-4 font-semibold">{{ exit.name }}</h3><div class="grid gap-4 md:grid-cols-2">
    <label><span class="field-label">加密方法</span><select v-model="exit.method" class="select w-full"><optgroup v-for="group in cipherGroups" :key="group.label" :label="group.label"><option v-for="method in group.methods" :key="method" :value="method">{{ method }}</option></optgroup></select></label>
    <div><span class="field-label">密码</span><div class="join flex w-full"><input v-model="exit.password" :type="passwordVisible(exit) ? 'text' : 'password'" aria-label="密码" class="input join-item min-w-0 flex-1" /><button type="button" class="btn btn-square join-item" :title="passwordVisible(exit) ? '隐藏密码' : '显示密码'" @click="togglePassword(exit)"><EyeOff v-if="passwordVisible(exit)" class="size-4" /><Eye v-else class="size-4" /></button></div><div v-if="keyHint(exit.method)" class="mt-1 text-xs text-base-content/55">{{ keyHint(exit.method) }}</div></div>
  </div></section>
  <form v-if="pendingExit" ref="exitDraftForm" class="panel-section border border-primary/50 bg-primary/5" @submit.prevent="confirmExitDraft">
    <h3 class="mb-4 font-semibold">新建 SS 出口</h3>
    <div class="grid gap-4 md:grid-cols-2">
      <label><span class="field-label">名称</span><input ref="exitDraftName" v-model="pendingExit.name" class="input w-full" aria-label="新 SS 出口名称" required /></label>
      <label><span class="field-label">服务器</span><input v-model.trim="pendingExit.server" class="input w-full" aria-label="新 SS 出口服务器" required /></label>
      <label><span class="field-label">服务器端口</span><input v-model.number="pendingExit.server_port" type="number" min="1" max="65535" class="input w-full" aria-label="新 SS 出口服务器端口" required /></label>
      <label><span class="field-label">加密方法</span><select v-model="pendingExit.method" class="select w-full" aria-label="新 SS 出口加密方法" required><optgroup v-for="group in cipherGroups" :key="group.label" :label="group.label"><option v-for="method in group.methods" :key="method" :value="method">{{ method }}</option></optgroup></select></label>
      <div class="md:col-span-2"><span class="field-label">密码</span><div class="join flex w-full"><input v-model="pendingExit.password" :type="passwordVisible(pendingExit) ? 'text' : 'password'" aria-label="新 SS 出口密码" class="input join-item min-w-0 flex-1" required /><button type="button" class="btn btn-square join-item" :title="passwordVisible(pendingExit) ? '隐藏密码' : '显示密码'" @click="togglePassword(pendingExit)"><EyeOff v-if="passwordVisible(pendingExit)" class="size-4" /><Eye v-else class="size-4" /></button></div><div v-if="keyHint(pendingExit.method)" class="mt-1 text-xs text-base-content/55">{{ keyHint(pendingExit.method) }}</div></div>
    </div>
    <div class="mt-4 flex justify-end gap-2"><button type="submit" class="btn btn-primary btn-sm">确认添加</button><button type="button" class="btn btn-ghost btn-sm" @click="cancelExitDraft">取消</button></div>
  </form>
</template>
<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'; import { Eye, EyeOff, Plus, RefreshCw, Trash2 } from '@lucide/vue'; import type { Bundle, Diagnostics, Exit } from '@/types'
const cipherGroups = [
  { label: 'AEAD 2022', methods: ['2022-blake3-aes-128-gcm', '2022-blake3-aes-256-gcm', '2022-blake3-chacha20-poly1305'] },
  { label: 'AEAD', methods: ['aes-128-gcm', 'aes-256-gcm', 'chacha20-ietf-poly1305'] },
] as const
const bundle = defineModel<Bundle>('bundle', { required: true }); const props = defineProps<{ diagnostics: Diagnostics | null; diagnosticsBusy: boolean }>(); const emit = defineEmits<{ 'refresh-diagnostics': []; 'pending-change': [value: boolean] }>(); const ssExits = computed(() => bundle.value.config.exits.filter(e => e.type === 'shadowsocks-rust'))
const visiblePasswords = ref(new Set<Exit>())
const pendingExit = ref<Exit | null>(null)
const exitDraftForm = ref<HTMLFormElement | null>(null)
const exitDraftName = ref<HTMLInputElement | null>(null)
const checkedAt = computed(() => props.diagnostics ? `检测于 ${new Date(props.diagnostics.checked_at).toLocaleString()}` : '尚未检测')
const pending = computed(() => pendingExit.value !== null)
function passwordVisible(exit: Exit) { return visiblePasswords.value.has(exit) }
function togglePassword(exit: Exit) { const next = new Set(visiblePasswords.value); if (next.has(exit)) next.delete(exit); else next.add(exit); visiblePasswords.value = next }
function keyHint(method: string) {
  if (method === '2022-blake3-aes-128-gcm') return '支持 iPSK[:...]:uPSK，每段为 Base64 编码的 16 字节密钥（通常 24 个字符）；生成：openssl rand -base64 16'
  if (method === '2022-blake3-aes-256-gcm') return '支持 iPSK[:...]:uPSK，每段为 Base64 编码的 32 字节密钥（通常 44 个字符）；生成：openssl rand -base64 32'
  if (method === '2022-blake3-chacha20-poly1305') return '仅支持单段 Base64 编码的 32 字节密钥（通常 44 个字符）；生成：openssl rand -base64 32'
  return ''
}
function add() {
  if (!pendingExit.value) {
    let port = 1080; const used = new Set(bundle.value.config.exits.map(e => e.listen_port)); while (used.has(port)) port++
    pendingExit.value = { name: `ss${ssExits.value.length + 1}`, type: 'shadowsocks-rust', fwmark: 0, server: '', server_port: 8388, method: '2022-blake3-aes-128-gcm', password: '', username: 'default', listen_address: '127.0.0.1', listen_port: port, tcp: true, udp: true, timeout_seconds: 300 }
  }
  void nextTick(() => { exitDraftForm.value?.scrollIntoView({ behavior: 'smooth', block: 'center' }); exitDraftName.value?.focus() })
}
function confirmExitDraft() { if (!pendingExit.value) return; const exit = pendingExit.value; bundle.value.config.exits.push(exit); visiblePasswords.value.delete(exit); pendingExit.value = null }
function cancelExitDraft() { if (pendingExit.value) visiblePasswords.value.delete(pendingExit.value); pendingExit.value = null }
watch(pending, value => emit('pending-change', value), { immediate: true })
onBeforeUnmount(() => emit('pending-change', false))
</script>

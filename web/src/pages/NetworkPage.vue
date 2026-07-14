<template>
  <div class="panel-section"><h2 class="text-lg font-semibold">DNS 与网络</h2></div>
  <section class="panel-section"><h3 class="mb-4 font-semibold">网络入口</h3><div class="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
    <label><span class="field-label">Gateway IP</span><input v-model.trim="bundle.config.network.gateway_ip" class="input w-full mono" /></label>
    <label><span class="field-label">内网 CIDR</span><input v-model.trim="bundle.config.network.internal_cidr" class="input w-full mono" /></label>
    <label><span class="field-label">入口网卡</span><input v-model.trim="bundle.config.network.ingress_iface" class="input w-full mono" /></label>
    <label><span class="field-label">QUIC 策略</span><select v-model="bundle.config.network.quic_policy" class="select w-full"><option value="reject">Reject</option><option value="proxy">Proxy</option></select></label>
    <label><span class="field-label">加密 DNS 策略</span><select v-model="bundle.config.network.encrypted_dns_policy" class="select w-full"><option value="reject">Reject</option><option value="allow">Allow</option></select></label>
    <label><span class="field-label">默认出口</span><select v-model="bundle.config.routing.fallback_exit" class="select w-full"><option v-for="exit in bundle.config.exits" :key="exit.name">{{ exit.name }}</option></select></label>
  </div></section>
  <section class="panel-section"><h3 class="mb-4 font-semibold">DNS</h3><div class="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
    <label><span class="field-label">DoT 域名</span><input v-model.trim="bundle.config.dns.dot_domain" class="input w-full" /></label>
    <ListField v-model="bundle.config.dns.upstreams_cn" label="国内上游" />
    <ListField v-model="bundle.config.dns.upstreams_overseas_private" label="内网海外上游" />
    <ListField v-model="bundle.config.dns.upstreams_overseas_public" label="公网海外上游" />
  </div></section>
  <section class="panel-section">
    <div class="mb-3 flex items-center justify-between gap-2"><h3 class="font-semibold">自定义 DNS 池</h3><button class="btn btn-neutral btn-sm" @click="addPool"><Plus class="size-4" />新建池</button></div>
    <div class="space-y-3">
      <div v-for="(pool, index) in bundle.config.dns.custom_pools" :key="index" class="grid gap-3 border border-base-300 bg-base-100 p-3 lg:grid-cols-[12rem_1fr_auto]">
        <label><span class="field-label">名称</span><input v-model.trim="pool.name" class="input input-sm w-full mono" placeholder="custom_pool" @focus="rememberPoolName(pool)" @change="renamePool(pool)" /></label>
        <label><span class="field-label">探测域名</span><input v-model.trim="pool.probe_domain" class="input input-sm w-full mono" placeholder="example.com" /></label>
        <button class="btn btn-ghost btn-square btn-sm self-end text-error" title="删除 DNS 池" @click="removePool(pool)"><Trash2 class="size-4" /></button>
        <div class="lg:col-span-3"><ListField v-model="pool.upstreams" label="上游" /></div>
      </div>
      <div v-if="!bundle.config.dns.custom_pools.length" class="border border-dashed border-base-300 py-10 text-center text-sm text-base-content/50">暂无自定义 DNS 池</div>
    </div>
  </section>
  <section class="panel-section">
    <div class="mb-3 flex flex-wrap items-center justify-between gap-2"><div><h3 class="font-semibold">已应用 DNS 状态</h3><div class="mt-1 text-xs text-base-content/55">{{ checkedAt }}</div></div><button class="btn btn-ghost btn-square btn-sm" title="检测 DNS 与 DoT" :disabled="diagnosticsBusy" @click="$emit('refresh-diagnostics')"><RefreshCw class="size-4" :class="{ 'animate-spin': diagnosticsBusy }" /></button></div>
    <div class="hidden overflow-x-auto border border-base-300 md:block"><table class="table table-sm"><thead><tr><th>解析池</th><th>上游</th><th>协议</th><th>状态</th><th>延迟</th><th>结果</th></tr></thead><tbody>
      <tr v-for="item in diagnostics?.dns || []" :key="`${item.pool}-${item.upstream}`"><td>{{ poolName(item.pool) }}</td><td class="max-w-xs break-all mono text-xs">{{ item.upstream }}</td><td class="uppercase">{{ item.protocol }}</td><td><span class="badge badge-sm" :class="item.status === 'ok' ? 'badge-success' : 'badge-error'">{{ item.status === 'ok' ? '正常' : '异常' }}</span></td><td>{{ item.latency_ms.toFixed(1) }} ms</td><td class="max-w-sm break-words text-xs text-base-content/65">{{ item.status === 'ok' ? (item.answers?.join(', ') || '响应正常') : item.error }}</td></tr>
      <tr v-if="!diagnostics?.dns?.length"><td colspan="6" class="text-center text-base-content/50">暂无检测结果</td></tr>
    </tbody></table></div>
    <div class="divide-y divide-base-300 border border-base-300 md:hidden">
      <div v-for="item in diagnostics?.dns || []" :key="`${item.pool}-${item.upstream}-mobile`" class="p-3">
        <div class="flex items-start justify-between gap-3"><div class="min-w-0"><div class="font-medium">{{ poolName(item.pool) }} · {{ item.protocol.toUpperCase() }}</div><div class="mt-1 break-all mono text-xs text-base-content/60">{{ item.upstream }}</div></div><span class="badge badge-sm shrink-0" :class="item.status === 'ok' ? 'badge-success' : 'badge-error'">{{ item.status === 'ok' ? `${item.latency_ms.toFixed(1)} ms` : '异常' }}</span></div>
        <div class="mt-2 break-words text-xs" :class="item.status === 'ok' ? 'text-base-content/60' : 'text-error'">{{ item.status === 'ok' ? (item.answers?.join(', ') || '响应正常') : item.error }}</div>
      </div>
      <div v-if="!diagnostics?.dns?.length" class="p-6 text-center text-sm text-base-content/50">暂无检测结果</div>
    </div>
    <div class="mt-4 border border-base-300 bg-base-100 p-3">
      <div class="flex flex-wrap items-center gap-2"><span class="font-medium">DoT</span><span class="badge badge-sm" :class="dotStatusClass">{{ dotStatusText }}</span><span class="mono text-sm">{{ diagnostics?.dot?.domain || bundle.config.dns.dot_domain }}</span></div>
      <div class="mt-2 text-sm text-base-content/65">{{ dotDetail }}</div>
    </div>
  </section>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import { Plus, RefreshCw, Trash2 } from '@lucide/vue'
import type { Bundle, Diagnostics, DNSPool, Rule } from '@/types'
import ListField from '@/components/ListField.vue'
const bundle = defineModel<Bundle>('bundle', { required: true })
const props = defineProps<{ diagnostics: Diagnostics | null; diagnosticsBusy: boolean }>()
const emit = defineEmits<{ 'refresh-diagnostics': []; error: [value: string] }>()
const originalNames = new WeakMap<DNSPool, string>()
let poolSequence = 0
const checkedAt = computed(() => props.diagnostics ? `检测于 ${new Date(props.diagnostics.checked_at).toLocaleString()}` : '尚未检测')
const dotStatusClass = computed(() => props.diagnostics?.dot?.status === 'ok' ? (props.diagnostics.dot.certificate_status === 'warning' ? 'badge-warning' : 'badge-success') : 'badge-error')
const dotStatusText = computed(() => props.diagnostics?.dot?.status === 'ok' ? (props.diagnostics.dot.certificate_status === 'warning' ? '证书即将到期' : '正常') : '异常')
const dotDetail = computed(() => {
  const dot = props.diagnostics?.dot
  if (!dot) return '暂无检测结果'
  if (dot.status !== 'ok') return dot.error || 'DoT 检测失败'
  const expires = dot.expires_at ? new Date(dot.expires_at).toLocaleDateString() : '-'
  return `${dot.latency_ms?.toFixed(1)} ms · 证书到期 ${expires} · 剩余 ${dot.days_remaining} 天 · 域名匹配`
})
function poolName(pool: string) { return pool === 'cn' ? '国内' : pool === 'overseas_private' ? '内网海外' : pool === 'overseas_public' ? '公网海外' : pool }
function addPool() {
  const existing = new Set(bundle.value.config.dns.custom_pools.map(pool => pool.name))
  let name = ''
  do { name = `custom_pool_${++poolSequence}` } while (existing.has(name))
  bundle.value.config.dns.custom_pools.push({ name, probe_domain: 'www.baidu.com', upstreams: [] })
}
function rememberPoolName(pool: DNSPool) { originalNames.set(pool, pool.name) }
function renamePool(pool: DNSPool) {
  const previous = originalNames.get(pool)
  if (!previous || !pool.name || previous === pool.name) return
  for (const rule of bundle.value.rules.rules || []) if (rule.dns_pool === previous) rule.dns_pool = pool.name
  for (const item of bundle.value.rules.imports || []) if (item.dns_pool === previous) item.dns_pool = pool.name
  originalNames.set(pool, pool.name)
}
function removePool(pool: DNSPool) {
  const localRules = (bundle.value.rules.rules || []).filter(rule => rule.dns_pool === pool.name)
  const imports = (bundle.value.rules.imports || []).filter(item => item.dns_pool === pool.name)
  if (!localRules.length && !imports.length) return deletePool(pool)
  if (imports.length || localRules.length !== 1 || !isDefaultNeteaseRule(localRules[0], pool.name)) {
    emit('error', `DNS 池 ${pool.name} 仍被规则引用，请先重新分配这些规则`)
    return
  }
  if (!window.confirm(`删除 DNS 池 ${pool.name} 及其默认网易云规则？`)) return
  const index = bundle.value.rules.rules!.indexOf(localRules[0])
  if (index >= 0) bundle.value.rules.rules!.splice(index, 1)
  deletePool(pool)
}
function deletePool(pool: DNSPool) {
  const index = bundle.value.config.dns.custom_pools.indexOf(pool)
  if (index >= 0) bundle.value.config.dns.custom_pools.splice(index, 1)
}
function isDefaultNeteaseRule(rule: Rule, poolName: string) {
  const domains = ['music.163.com', 'music.126.net', 'iplay.163.com', 'look.163.com', 'y.163.com']
  return rule.name === 'netease-music' && !rule.exit && rule.dns_pool === poolName &&
    JSON.stringify(rule.domain_suffix || []) === JSON.stringify(domains) &&
    !rule.domain?.length && !rule.domain_keyword?.length && !rule.domain_regex?.length && !rule.ip_cidr?.length && !rule.rule_set?.length
}
</script>

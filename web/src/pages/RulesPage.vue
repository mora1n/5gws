<template>
  <div class="panel-section flex items-center justify-between"><h2 class="text-lg font-semibold">规则与导入</h2><button class="btn btn-neutral btn-sm" @click="addRule"><Plus class="size-4" />规则</button></div>
  <section class="panel-section">
    <div class="mb-3 flex flex-wrap items-center justify-between gap-2">
      <div>
        <h3 class="font-semibold">当前已应用规则</h3>
        <div class="mt-1 text-sm text-base-content/60">active {{ active?.id ?? '-' }} · {{ activeTime }}</div>
      </div>
      <span class="badge badge-ghost">{{ activeRules.length }}</span>
    </div>
    <div v-if="activeRules.length" class="space-y-2">
      <div v-for="rule in activeRules" :key="`${rule.name}-${activeTarget(rule)}`" class="border border-base-300 bg-base-100 p-3">
        <div class="flex flex-wrap items-center gap-2">
          <span class="font-medium">{{ rule.name || '未命名规则' }}</span>
          <span class="badge badge-outline badge-sm">{{ activeTarget(rule) }}</span>
        </div>
        <div class="mt-2 flex flex-wrap gap-2 text-xs">
          <span v-for="item in matchers(rule)" :key="item" class="rounded border border-base-300 px-2 py-1 mono text-base-content/70">{{ item }}</span>
        </div>
      </div>
    </div>
    <div v-else class="border border-dashed border-base-300 py-8 text-center text-sm text-base-content/50">当前 revision 没有已解析规则数据</div>
  </section>
  <section class="panel-section"><div class="mb-3 flex items-center justify-between"><h3 class="font-semibold">本地规则</h3><span class="badge badge-ghost">{{ localRules.length }}</span></div>
    <div class="space-y-3"><div v-for="(rule, index) in localRules" :key="index" class="grid gap-3 border border-base-300 bg-base-100 p-3 md:grid-cols-[1fr_10rem_1fr_auto]">
      <input v-model.trim="rule.name" class="input input-sm w-full" placeholder="名称" />
      <select class="select select-sm w-full" :value="target(rule)" @change="targetChange(rule, $event)"><option v-for="value in targets" :key="value" :value="value">{{ value }}</option></select>
      <input :value="(rule.domain_suffix || []).join(', ')" class="input input-sm w-full mono" placeholder="example.com, example.net" @input="domainChange(rule, $event)" />
      <button class="btn btn-ghost btn-square btn-sm text-error" title="删除" @click="localRules.splice(index, 1)"><Trash2 class="size-4" /></button>
    </div><div v-if="!localRules.length" class="border border-dashed border-base-300 py-10 text-center text-sm text-base-content/50">暂无本地规则</div></div>
  </section>
  <section class="panel-section"><div class="mb-3 flex items-center justify-between"><h3 class="font-semibold">远程导入</h3><button class="btn btn-ghost btn-sm" @click="addImport"><Plus class="size-4" />导入</button></div>
    <div class="space-y-3"><div v-for="(item, index) in imports" :key="index" class="grid gap-3 border border-base-300 bg-base-100 p-3 lg:grid-cols-[10rem_8rem_1fr_10rem_auto]">
      <input v-model.trim="item.name" class="input input-sm w-full" placeholder="名称" /><select v-model="item.type" class="select select-sm w-full"><option value="sing-box">sing-box</option><option value="mihomo">Mihomo</option></select>
      <input v-model.trim="item.url" class="input input-sm w-full mono" placeholder="https://" /><select class="select select-sm w-full" :value="target(item)" @change="targetChange(item, $event)"><option v-for="value in targets" :key="value">{{ value }}</option></select>
      <button class="btn btn-ghost btn-square btn-sm text-error" title="删除" @click="imports.splice(index, 1)"><Trash2 class="size-4" /></button>
    </div></div>
  </section>
</template>
<script setup lang="ts">
import { computed } from 'vue'; import { Plus, Trash2 } from '@lucide/vue'; import type { Bundle, ImportRule, Revision, Rule } from '@/types'
const bundle = defineModel<Bundle>('bundle', { required: true })
const props = defineProps<{ active: Revision | null }>()
if (!bundle.value.rules.rules) bundle.value.rules.rules = []
if (!bundle.value.rules.imports) bundle.value.rules.imports = []
const localRules = computed(() => bundle.value.rules.rules!); const imports = computed(() => bundle.value.rules.imports!)
const activeRules = computed(() => props.active?.bundle.resolved_rules || [])
const activeTime = computed(() => props.active?.active_at || props.active?.created_at || '-')
const targets = computed(() => ['pool:cn', 'pool:overseas_private', 'pool:overseas_public', ...bundle.value.config.exits.map(e => `exit:${e.name}`)])
function target(item: Rule | ImportRule) { return item.exit ? `exit:${item.exit}` : `pool:${item.dns_pool}` }
function activeTarget(rule: Rule) { return rule.exit ? `exit:${rule.exit}` : rule.dns_pool ? `pool:${rule.dns_pool}` : '-' }
function matchers(rule: Rule) {
  return [
    ...formatList('domain', rule.domain),
    ...formatList('domain_suffix', rule.domain_suffix),
    ...formatList('domain_keyword', rule.domain_keyword),
    ...formatList('domain_regex', rule.domain_regex),
    ...formatList('ip_cidr', rule.ip_cidr),
    ...formatList('rule_set', rule.rule_set),
  ]
}
function formatList(label: string, values?: string[]) { return (values || []).map(value => `${label}:${value}`) }
function setTarget(item: Rule | ImportRule, value: string) { const [kind, name] = value.split(':'); item.exit = kind === 'exit' ? name : ''; item.dns_pool = kind === 'pool' ? name : '' }
function list(value: string) { return value.split(',').map(v => v.trim()).filter(Boolean) }
function targetChange(item: Rule | ImportRule, event: Event) { setTarget(item, (event.target as HTMLSelectElement).value) }
function domainChange(rule: Rule, event: Event) { rule.domain_suffix = list((event.target as HTMLInputElement).value) }
function addRule() { localRules.value.push({ name: `rule-${localRules.value.length + 1}`, exit: 'direct', dns_pool: '', domain_suffix: [] }) }
function addImport() { imports.value.push({ name: `import-${imports.value.length + 1}`, type: 'sing-box', path: '', url: '', format: '', exit: 'direct', dns_pool: '' }) }
</script>

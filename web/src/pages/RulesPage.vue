<template>
  <div class="panel-section flex items-center justify-between">
    <h2 class="text-lg font-semibold">规则</h2>
    <button class="btn btn-neutral btn-sm" @click="addRule"><Plus class="size-4" />新建规则</button>
  </div>

  <section class="panel-section">
    <div class="mb-3 flex flex-wrap items-center justify-between gap-2">
      <div>
        <h3 class="font-semibold">当前已应用规则</h3>
        <div class="mt-1 text-sm text-base-content/60">active {{ summary?.revision_id ?? '-' }} · {{ activeTime }}</div>
      </div>
      <div class="flex items-center gap-2"><span class="badge badge-ghost">{{ summary?.rule_count ?? 0 }}</span><button class="btn btn-ghost btn-square btn-sm" title="刷新已应用规则" :disabled="loading" @click="loadActiveRules"><RefreshCw class="size-4" :class="{ 'animate-spin': loading }" /></button></div>
    </div>
    <div v-if="loading && !summary" class="border border-dashed border-base-300 py-8 text-center text-sm text-base-content/50">正在加载已应用规则</div>
    <div v-else-if="summary?.groups.length" class="space-y-3">
      <details v-for="(group, groupIndex) in summary.groups" :key="group.key" class="border border-base-300 bg-base-100 p-3" :open="groupIndex < 2">
        <summary class="flex cursor-pointer list-none flex-wrap items-center gap-2">
          <span class="font-medium">{{ group.title }}</span>
          <span class="badge badge-outline badge-sm">{{ group.rule_count }} 条规则</span>
          <span class="badge badge-ghost badge-sm">{{ group.matcher_count }} 个匹配项</span>
        </summary>
        <div class="mt-3 space-y-2">
          <div v-for="rule in group.rules" :key="`${rule.name}-${rule.target}`" class="border border-base-300 bg-base-200/50 p-3">
            <div class="flex flex-wrap items-center gap-2"><span class="font-medium">{{ rule.name || '未命名规则' }}</span><span class="badge badge-ghost badge-sm">{{ rule.target }}</span></div>
            <div class="mt-2 grid gap-2 lg:grid-cols-2">
              <div v-for="matcher in rule.matchers" :key="matcher.label" class="min-w-0 rounded border border-base-300 bg-base-100 px-2 py-1.5 text-xs">
                <div class="mb-1 flex items-center justify-between gap-2 text-base-content/60"><span>{{ matcher.label }}</span><span>{{ matcher.count }}</span></div>
                <div class="flex flex-wrap gap-1"><span v-for="value in matcher.samples" :key="value" class="max-w-full truncate rounded bg-base-200 px-1.5 py-0.5 mono">{{ value }}</span><span v-if="matcher.count > matcher.samples.length" class="rounded bg-base-200 px-1.5 py-0.5 text-base-content/50">还有 {{ matcher.count - matcher.samples.length }} 项</span></div>
              </div>
            </div>
          </div>
        </div>
      </details>
    </div>
    <div v-else class="border border-dashed border-base-300 py-8 text-center text-sm text-base-content/50">当前 revision 没有已解析规则数据</div>
  </section>

  <section class="panel-section" aria-labelledby="managed-rules-heading">
    <div class="mb-3 flex items-center justify-between"><h3 id="managed-rules-heading" class="font-semibold">默认规则</h3><span class="badge badge-outline">只读</span></div>
    <div class="grid gap-3 lg:grid-cols-2">
      <div v-for="rule in managedLocalRules" :key="rule.name" class="min-w-0 border border-base-300 bg-base-100 p-3">
        <div class="flex flex-wrap items-center gap-2"><span class="font-medium">{{ rule.name }}</span><span class="badge badge-ghost badge-sm">{{ target(rule) }}</span></div>
        <div class="mt-2 flex flex-wrap gap-1"><span v-for="value in rule.domain_suffix || []" :key="value" class="max-w-full truncate rounded bg-base-200 px-1.5 py-0.5 text-xs mono">{{ value }}</span></div>
      </div>
      <div v-for="item in managedImports" :key="item.name" class="min-w-0 border border-base-300 bg-base-100 p-3">
        <div class="flex flex-wrap items-center gap-2"><span class="font-medium">{{ item.name }}</span><span class="badge badge-ghost badge-sm">{{ target(item) }}</span><span class="badge badge-outline badge-sm">{{ item.type }}</span></div>
        <div class="mt-2 break-all text-xs text-base-content/60 mono">{{ item.url || item.path }}</div>
      </div>
    </div>
  </section>

  <section class="panel-section">
    <div class="mb-3 flex items-center justify-between"><h3 class="font-semibold">自定义本地规则</h3><span class="badge badge-ghost">{{ customLocalRules.length }}</span></div>
    <div class="space-y-3">
      <div v-for="(rule, index) in customLocalRules" :key="index" class="grid gap-3 border border-base-300 bg-base-100 p-3 md:grid-cols-[1fr_10rem_1fr_auto]">
        <input v-model="rule.name" class="input input-sm w-full" placeholder="名称" />
        <select class="select select-sm w-full" :value="target(rule)" @change="targetChange(rule, $event)"><option v-for="value in targets" :key="value" :value="value">{{ value }}</option></select>
        <input :value="(rule.domain_suffix || []).join(', ')" class="input input-sm w-full mono" placeholder="example.com, example.net" @input="domainChange(rule, $event)" />
        <button class="btn btn-ghost btn-square btn-sm text-error" title="删除" @click="removeRule(rule)"><Trash2 class="size-4" /></button>
      </div>
      <form v-if="pendingRule" ref="ruleDraftForm" class="grid gap-3 border border-primary/50 bg-primary/5 p-3 md:grid-cols-[1fr_10rem_1fr_auto]" @submit.prevent="confirmRuleDraft">
        <input ref="ruleDraftName" v-model="pendingRule.name" class="input input-sm w-full" placeholder="名称" aria-label="新规则名称" required />
        <select class="select select-sm w-full" :value="pendingRuleTarget" aria-label="新规则目标" required @change="draftRuleTargetChange"><option v-for="value in targets" :key="value" :value="value">{{ value }}</option></select>
        <input :value="(pendingRule.domain_suffix || []).join(', ')" class="input input-sm w-full mono" placeholder="example.com, example.net" aria-label="新规则域名" required @input="draftRuleDomainChange" />
        <div class="flex gap-2 md:justify-end"><button type="submit" class="btn btn-primary btn-sm">确认添加</button><button type="button" class="btn btn-ghost btn-sm" @click="cancelRuleDraft">取消</button></div>
      </form>
      <div v-if="!customLocalRules.length && !pendingRule" class="border border-dashed border-base-300 py-10 text-center text-sm text-base-content/50">暂无自定义本地规则</div>
    </div>
  </section>

  <section class="panel-section">
    <div class="mb-3 flex items-center justify-between"><h3 class="font-semibold">自定义远程导入</h3><button class="btn btn-ghost btn-sm" @click="addImport"><Plus class="size-4" />导入</button></div>
    <div class="space-y-3">
      <div v-for="(item, index) in customImports" :key="index" class="grid gap-3 border border-base-300 bg-base-100 p-3 lg:grid-cols-[10rem_8rem_1fr_10rem_auto]">
        <input v-model="item.name" class="input input-sm w-full" placeholder="名称" /><select v-model="item.type" class="select select-sm w-full"><option value="sing-box">sing-box</option><option value="mihomo">Mihomo</option></select>
        <input v-model.trim="item.url" class="input input-sm w-full mono" placeholder="https://" /><select class="select select-sm w-full" :value="target(item)" @change="targetChange(item, $event)"><option v-for="value in targets" :key="value" :value="value">{{ value }}</option></select>
        <button class="btn btn-ghost btn-square btn-sm text-error" title="删除" @click="removeImport(item)"><Trash2 class="size-4" /></button>
      </div>
      <form v-if="pendingImport" ref="importDraftForm" class="grid gap-3 border border-primary/50 bg-primary/5 p-3 lg:grid-cols-[10rem_8rem_1fr_10rem_auto]" @submit.prevent="confirmImportDraft">
        <input ref="importDraftName" v-model="pendingImport.name" class="input input-sm w-full" placeholder="名称" aria-label="新导入名称" required /><select v-model="pendingImport.type" class="select select-sm w-full" aria-label="新导入类型" required><option value="sing-box">sing-box</option><option value="mihomo">Mihomo</option></select>
        <input v-model.trim="pendingImport.url" type="url" class="input input-sm w-full mono" placeholder="https://" aria-label="新导入地址" required /><select class="select select-sm w-full" :value="pendingImportTarget" aria-label="新导入目标" required @change="draftImportTargetChange"><option v-for="value in targets" :key="value" :value="value">{{ value }}</option></select>
        <div class="flex gap-2 lg:justify-end"><button type="submit" class="btn btn-primary btn-sm">确认添加</button><button type="button" class="btn btn-ghost btn-sm" @click="cancelImportDraft">取消</button></div>
      </form>
      <div v-if="!customImports.length && !pendingImport" class="border border-dashed border-base-300 py-10 text-center text-sm text-base-content/50">暂无自定义远程导入</div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { Plus, RefreshCw, Trash2 } from '@lucide/vue'
import { api } from '@/api'
import type { ActiveRules, Bundle, ImportRule, Rule, RuleFile } from '@/types'

const bundle = defineModel<Bundle>('bundle', { required: true })
const props = defineProps<{ activeRevision: number; managed: RuleFile }>()
const emit = defineEmits<{ error: [value: string]; 'pending-change': [value: boolean] }>()
if (!bundle.value.rules.rules) bundle.value.rules.rules = []
if (!bundle.value.rules.imports) bundle.value.rules.imports = []

const managedLocalRules = computed(() => props.managed.rules || [])
const managedImports = computed(() => props.managed.imports || [])
const managedNames = computed(() => new Set([...managedLocalRules.value, ...managedImports.value].map(item => item.name)))
const customLocalRules = computed(() => bundle.value.rules.rules!.filter(item => !managedNames.value.has(item.name)))
const customImports = computed(() => bundle.value.rules.imports!.filter(item => !managedNames.value.has(item.name)))
const summary = ref<ActiveRules | null>(null)
const loading = ref(false)
const pendingRule = ref<Rule | null>(null)
const pendingImport = ref<ImportRule | null>(null)
const ruleDraftForm = ref<HTMLFormElement | null>(null)
const importDraftForm = ref<HTMLFormElement | null>(null)
const ruleDraftName = ref<HTMLInputElement | null>(null)
const importDraftName = ref<HTMLInputElement | null>(null)
const activeTime = computed(() => summary.value?.active_at ? new Date(summary.value.active_at).toLocaleString() : '-')
const pending = computed(() => pendingRule.value !== null || pendingImport.value !== null)
const pendingRuleTarget = computed(() => pendingRule.value ? target(pendingRule.value) : '')
const pendingImportTarget = computed(() => pendingImport.value ? target(pendingImport.value) : '')
const targets = computed(() => [
  'pool:cn',
  'pool:overseas_private',
  'pool:overseas_public',
  ...bundle.value.config.dns.custom_pools.map(pool => `pool:${pool.name}`),
  ...bundle.value.config.exits.map(exit => `exit:${exit.name}`),
])

function target(item: Rule | ImportRule) { return item.exit ? `exit:${item.exit}` : `pool:${item.dns_pool}` }
async function loadActiveRules() {
  if (loading.value) return
  loading.value = true
  try { summary.value = await api.activeRules() }
  catch (cause) { emit('error', cause instanceof Error ? cause.message : String(cause)) }
  finally { loading.value = false }
}
function setTarget(item: Rule | ImportRule, value: string) { const [kind, name] = value.split(':'); item.exit = kind === 'exit' ? name : ''; item.dns_pool = kind === 'pool' ? name : '' }
function list(value: string) { return value.split(',').map(item => item.trim()).filter(Boolean) }
function targetChange(item: Rule | ImportRule, event: Event) { setTarget(item, (event.target as HTMLSelectElement).value) }
function domainChange(rule: Rule, event: Event) { rule.domain_suffix = list((event.target as HTMLInputElement).value) }
function focusDraft(element: { value: HTMLInputElement | null }, form: { value: HTMLFormElement | null }) { void nextTick(() => { form.value?.scrollIntoView({ behavior: 'smooth', block: 'center' }); element.value?.focus() }) }
function addRule() {
  if (!pendingRule.value) pendingRule.value = { name: `rule-${customLocalRules.value.length + 1}`, exit: 'direct', dns_pool: '', domain_suffix: [] }
  focusDraft(ruleDraftName, ruleDraftForm)
}
function addImport() {
  if (!pendingImport.value) pendingImport.value = { name: `import-${customImports.value.length + 1}`, type: 'sing-box', path: '', url: '', format: '', exit: 'direct', dns_pool: '' }
  focusDraft(importDraftName, importDraftForm)
}
function draftRuleTargetChange(event: Event) { if (pendingRule.value) setTarget(pendingRule.value, (event.target as HTMLSelectElement).value) }
function draftImportTargetChange(event: Event) { if (pendingImport.value) setTarget(pendingImport.value, (event.target as HTMLSelectElement).value) }
function draftRuleDomainChange(event: Event) { if (pendingRule.value) domainChange(pendingRule.value, event) }
function confirmRuleDraft() { if (!pendingRule.value) return; bundle.value.rules.rules!.push(pendingRule.value); pendingRule.value = null }
function cancelRuleDraft() { pendingRule.value = null }
function confirmImportDraft() { if (!pendingImport.value) return; bundle.value.rules.imports!.push(pendingImport.value); pendingImport.value = null }
function cancelImportDraft() { pendingImport.value = null }
function removeRule(rule: Rule) { const index = bundle.value.rules.rules!.indexOf(rule); if (index >= 0) bundle.value.rules.rules!.splice(index, 1) }
function removeImport(item: ImportRule) { const index = bundle.value.rules.imports!.indexOf(item); if (index >= 0) bundle.value.rules.imports!.splice(index, 1) }

onMounted(loadActiveRules)
watch(() => props.activeRevision, (next, previous) => { if (next && next !== previous) loadActiveRules() })
watch(pending, value => emit('pending-change', value), { immediate: true })
onBeforeUnmount(() => emit('pending-change', false))
</script>

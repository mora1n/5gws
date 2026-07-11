<template><div class="panel-section"><h2 class="text-lg font-semibold">设置</h2></div>
  <section class="panel-section"><h3 class="mb-4 font-semibold">面板</h3><div class="grid gap-4 md:grid-cols-2"><label><span class="field-label">监听地址</span><input v-model.trim="bundle.config.panel.listen" class="input w-full mono" /></label><ListField v-model="bundle.config.panel.allowed_cidrs" label="允许的管理 CIDR" /></div></section>
  <section class="panel-section"><h3 class="mb-4 font-semibold">iOS</h3><div class="grid gap-4 md:grid-cols-2 xl:grid-cols-3"><label class="flex items-center gap-3"><input v-model="bundle.config.ios.enabled" type="checkbox" class="toggle toggle-primary" /><span>启用 profile</span></label><label><span class="field-label">公开地址</span><input v-model.trim="bundle.config.ios.base_url" class="input w-full" /></label><label><span class="field-label">组织</span><input v-model.trim="bundle.config.ios.organization" class="input w-full" /></label></div></section>
  <section class="panel-section"><h3 class="mb-4 font-semibold">备份</h3><div class="flex flex-wrap gap-3"><a class="btn btn-outline btn-sm" href="/api/v1/backup"><Download class="size-4" />导出 TOML</a><label class="btn btn-outline btn-sm" :class="{ 'btn-disabled': busy }"><Upload class="size-4" />导入 TOML<input type="file" accept=".toml" class="hidden" :disabled="busy" @change="upload" /></label></div></section>
  <section class="panel-section"><h3 class="mb-4 font-semibold">账号</h3><form class="grid max-w-2xl gap-3 md:grid-cols-[1fr_1fr_auto]" @submit.prevent="changePassword"><input v-model="currentPassword" required type="password" class="input w-full" placeholder="当前密码" /><input v-model="nextPassword" required type="password" minlength="12" class="input w-full" placeholder="新密码" /><button class="btn btn-outline" type="submit" :disabled="busy"><KeyRound class="size-4" />修改密码</button></form></section>
  <section class="panel-section"><div class="flex flex-wrap items-center justify-between gap-3"><div><h3 class="font-semibold">更新</h3><div class="mt-1 text-sm text-base-content/60">{{ updateText }}</div></div><div class="flex gap-2"><button class="btn btn-ghost btn-sm" :disabled="busy" @click="checkUpdate"><RefreshCw class="size-4" />检查</button><button class="btn btn-neutral btn-sm" :disabled="busy || !update?.available" @click="applyUpdate"><Download class="size-4" />更新</button></div></div></section>
</template>
<script setup lang="ts">
import { computed, ref } from 'vue'
import { Download, KeyRound, RefreshCw, Upload } from '@lucide/vue'
import ListField from '@/components/ListField.vue'
import { api } from '@/api'
import type { Bundle } from '@/types'
const bundle = defineModel<Bundle>('bundle', { required: true })
const emit = defineEmits<{ imported: []; message: [value: string]; error: [value: string]; signedOut: [] }>()
const currentPassword = ref(''), nextPassword = ref('')
const busy = ref(false)
const update = ref<{ current: string, latest: string, available: boolean } | null>(null)
const updateText = computed(() => !update.value ? '尚未检查' : update.value.available ? `${update.value.current} → ${update.value.latest}` : `当前已是 ${update.value.current}`)
async function action(fn: () => Promise<string>) { busy.value = true; try { emit('message', await fn()) } catch (cause) { emit('error', cause instanceof Error ? cause.message : String(cause)) } finally { busy.value = false } }
async function upload(event: Event) { const input = event.target as HTMLInputElement; const file = input.files?.[0]; if (!file) return; await action(async () => { const response = await fetch('/api/v1/backup', { method: 'POST', body: await file.text(), headers: { 'Content-Type': 'application/toml' } }); if (!response.ok) { const body = await response.json(); throw new Error(body.error || response.statusText) } emit('imported'); input.value = ''; return '备份已导入为草稿' }) }
async function changePassword() { await action(async () => { await api.changePassword({ current: currentPassword.value, next: nextPassword.value }); currentPassword.value = ''; nextPassword.value = ''; emit('signedOut'); return '密码已修改，请重新登录' }) }
async function checkUpdate() { await action(async () => { update.value = await api.updateCheck(); return update.value.available ? `发现新版本 ${update.value.latest}` : '当前已是最新版本' }) }
async function applyUpdate() { await action(async () => { update.value = await api.updateApply(); return '更新已下载，服务即将重启' }) }
</script>

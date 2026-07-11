<template>
  <div v-if="loading" class="grid min-h-screen place-items-center"><LoaderCircle class="size-7 animate-spin text-primary" /></div>
  <main v-else-if="startupError" class="grid min-h-screen place-items-center bg-base-200 p-4"><div class="alert alert-error max-w-xl"><CircleAlert class="size-5 shrink-0" /><span class="break-all">{{ startupError }}</span><button class="btn btn-sm" @click="initialize">重试</button></div></main>
  <AuthView v-else-if="!authenticated" :setup="needsSetup" @done="start" />
  <div v-else class="flex h-screen overflow-hidden bg-base-200">
    <AppNav :page="page" @select="page = $event" @logout="logout" />
    <main class="min-w-0 flex-1 overflow-y-auto pb-16 lg:pb-0">
      <header class="sticky top-0 z-20 flex min-h-16 flex-wrap items-center justify-between gap-2 border-b border-base-300 bg-base-100/95 px-4 py-2 backdrop-blur sm:px-6">
        <div class="min-w-0"><div class="truncate font-medium">{{ pageTitle }}</div><div class="text-xs text-base-content/55">active {{ dashboard?.active_revision || '-' }} · draft {{ dashboard?.draft_revision || '-' }}</div></div>
        <div class="flex items-center gap-2"><button class="btn btn-ghost btn-square btn-sm" :title="themeTitle" @click="toggleTheme"><Moon v-if="theme === 'light-neutral'" class="size-4" /><Sun v-else class="size-4" /></button><button class="btn btn-ghost btn-sm" :disabled="busy || !draft" @click="save"><Save class="size-4" />保存</button><button class="btn btn-outline btn-sm" :disabled="busy" @click="validate"><ShieldCheck class="size-4" />预检</button><button class="btn btn-primary btn-sm" :disabled="busy" @click="apply"><Play class="size-4" />应用</button></div>
      </header>
      <div v-if="message" class="mx-4 mt-4 flex items-center gap-2 border px-3 py-2 text-sm sm:mx-6" :class="error ? 'border-error/40 bg-error/10 text-error' : 'border-success/40 bg-success/10 text-success'"><CircleAlert v-if="error" class="size-4 shrink-0" /><CircleCheck v-else class="size-4 shrink-0" /><span class="break-all">{{ message }}</span><button class="btn btn-ghost btn-square btn-xs ml-auto" title="关闭" @click="message = ''"><X class="size-4" /></button></div>
      <OverviewPage v-if="page === 'overview'" :dashboard="dashboard" @refresh="refresh" />
      <NetworkPage v-else-if="page === 'network' && draft" v-model:bundle="draft.bundle" />
      <RulesPage v-else-if="page === 'rules' && draft" v-model:bundle="draft.bundle" :active="active" />
      <ExitsPage v-else-if="page === 'exits' && draft" v-model:bundle="draft.bundle" />
      <LogsPage v-else-if="page === 'logs'" @error="show($event, true)" />
      <SettingsPage v-else-if="page === 'settings' && draft" v-model:bundle="draft.bundle" @imported="reload" @message="show($event, false)" @error="show($event, true)" @signed-out="authenticated = false" />
    </main>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { CircleAlert, CircleCheck, LoaderCircle, Moon, Play, Save, ShieldCheck, Sun, X } from '@lucide/vue'
import { APIError, api } from '@/api'; import type { Dashboard, Revision } from '@/types'
import AuthView from '@/components/AuthView.vue'; import AppNav from '@/components/AppNav.vue'
import OverviewPage from '@/pages/OverviewPage.vue'; import NetworkPage from '@/pages/NetworkPage.vue'; import RulesPage from '@/pages/RulesPage.vue'; import ExitsPage from '@/pages/ExitsPage.vue'; import LogsPage from '@/pages/LogsPage.vue'; import SettingsPage from '@/pages/SettingsPage.vue'

const loading=ref(true), authenticated=ref(false), needsSetup=ref(false), busy=ref(false), error=ref(false)
const page=ref('overview'), message=ref(''), startupError=ref(''), dashboard=ref<Dashboard|null>(null), active=ref<Revision|null>(null), draft=ref<Revision|null>(null)
const theme=ref<'light-neutral'|'dark-neutral'>(initialTheme())
const titles:Record<string,string>={overview:'概览',network:'DNS 与网络',rules:'规则',exits:'出口',logs:'日志',settings:'设置'}
const pageTitle=computed(()=>titles[page.value]||'5gws')
const themeTitle=computed(()=>theme.value === 'light-neutral' ? '切换到深色模式' : '切换到浅色模式')
async function start(){ authenticated.value=true; await reload() }
async function reload(){ [dashboard.value,active.value,draft.value]=await Promise.all([api.dashboard(),api.active(),api.draft()]) }
async function refresh(){ try{ await reload() }catch(cause){ show(cause,true) } }
async function save(){ if(!draft.value)return; await action(async()=>{ draft.value=await api.saveDraft(draft.value!.bundle); await reload(); return '草稿已保存' }) }
async function validate(){ await action(async()=>{ const result=await api.validate(); await reload(); return `预检通过，共 ${result.rule_count} 条规则` }) }
async function apply(){ await action(async()=>{ const result=await api.apply(); await reload(); return `已应用 revision ${result.id}` }) }
async function action(fn:()=>Promise<string>){ busy.value=true; try{ show(await fn(),false) }catch(cause){ show(cause,true) }finally{ busy.value=false } }
function show(value:unknown,isError:boolean){ error.value=isError; message.value=value instanceof Error?value.message:String(value) }
function initialTheme(){
  const saved = localStorage.getItem('5gws-theme')
  if (saved === 'light-neutral' || saved === 'dark-neutral') return saved
  return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark-neutral' : 'light-neutral'
}
function applyTheme(){
  document.documentElement.dataset.theme = theme.value
  document.querySelector<HTMLMetaElement>('meta[name="theme-color"]')?.setAttribute('content', theme.value === 'dark-neutral' ? '#1a1a1a' : '#f4f2f1')
}
function toggleTheme(){
  theme.value = theme.value === 'light-neutral' ? 'dark-neutral' : 'light-neutral'
  localStorage.setItem('5gws-theme', theme.value)
  applyTheme()
}
async function logout(){ await api.logout(); authenticated.value=false; needsSetup.value=false }
async function initialize(){
  loading.value=true; startupError.value=''
  try {
    const state=await api.bootstrap(); needsSetup.value=state.needs_setup
    if(!state.needs_setup){
      try { await api.me(); await start() }
      catch(cause) { if(cause instanceof APIError && cause.status===401) authenticated.value=false; else throw cause }
    }
  } catch(cause) { startupError.value=cause instanceof Error?cause.message:String(cause) }
  finally { loading.value=false }
}
onMounted(()=>{ applyTheme(); initialize() })
</script>

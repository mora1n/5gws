<template>
  <div v-if="loading" class="grid min-h-screen place-items-center"><LoaderCircle class="size-7 animate-spin text-primary" /></div>
  <main v-else-if="startupError" class="grid min-h-screen place-items-center bg-base-200 p-4"><div class="alert alert-error max-w-xl"><CircleAlert class="size-5 shrink-0" /><span class="break-all">{{ startupError }}</span><button class="btn btn-sm" @click="initialize">重试</button></div></main>
  <AuthView v-else-if="!authenticated" :setup="needsSetup" @done="start" />
  <div v-else class="flex h-screen overflow-hidden bg-base-200">
    <AppNav :page="page" @select="page = $event" @logout="logout" />
    <main class="min-w-0 flex-1 overflow-y-auto pb-16 lg:pb-0">
      <header class="sticky top-0 z-20 flex min-h-16 flex-wrap items-center justify-between gap-2 border-b border-base-300 bg-base-100/95 px-4 py-2 backdrop-blur sm:px-6">
        <div class="min-w-0 truncate font-medium">{{ pageTitle }}</div>
        <div class="flex items-center gap-2"><button class="btn btn-ghost btn-square btn-sm" :title="themeTitle" @click="toggleTheme"><Moon v-if="theme === 'light-neutral'" class="size-4" /><Sun v-else class="size-4" /></button><template v-if="editablePage"><button class="btn btn-outline btn-sm" :disabled="busy || !bundle" @click="validate"><ShieldCheck class="size-4" />预检</button><button class="btn btn-primary btn-sm" :disabled="busy || !bundle" @click="apply"><Play class="size-4" />应用</button></template></div>
      </header>
      <div v-if="message" class="mx-4 mt-4 flex items-center gap-2 border px-3 py-2 text-sm sm:mx-6" :class="error ? 'border-error/40 bg-error/10 text-error' : 'border-success/40 bg-success/10 text-success'"><CircleAlert v-if="error" class="size-4 shrink-0" /><CircleCheck v-else class="size-4 shrink-0" /><span class="break-all">{{ message }}</span><button class="btn btn-ghost btn-square btn-xs ml-auto" title="关闭" @click="message = ''"><X class="size-4" /></button></div>
      <OverviewPage v-if="page === 'overview'" :dashboard="dashboard" :metrics="metrics" :diagnostics="diagnostics" :runtime-busy="runtimeBusy" @refresh="refresh" @refresh-runtime="refreshRuntime" />
      <NetworkPage v-else-if="page === 'network' && bundle" v-model:bundle="bundle" :diagnostics="diagnostics" :diagnostics-busy="diagnosticsBusy" @refresh-diagnostics="runDiagnostics('network')" />
      <RulesPage v-else-if="page === 'rules' && bundle" v-model:bundle="bundle" :active-revision="dashboard?.active_revision || 0" @error="show($event, true)" />
      <ExitsPage v-else-if="page === 'exits' && bundle" v-model:bundle="bundle" :diagnostics="diagnostics" :diagnostics-busy="diagnosticsBusy" @refresh-diagnostics="runDiagnostics('exits')" />
      <LogsPage v-else-if="page === 'logs'" @error="show($event, true)" />
      <SettingsPage v-else-if="page === 'settings' && bundle" v-model:bundle="bundle" :active-revision="dashboard?.active_revision || 0" @imported="imported" @message="show($event, false)" @error="show($event, true)" @signed-out="authenticated = false" />
    </main>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { CircleAlert, CircleCheck, LoaderCircle, Moon, Play, ShieldCheck, Sun, X } from '@lucide/vue'
import { APIError, api } from '@/api'; import type { Bundle, Dashboard, Diagnostics, Metric } from '@/types'
import AuthView from '@/components/AuthView.vue'; import AppNav from '@/components/AppNav.vue'
import OverviewPage from '@/pages/OverviewPage.vue'; import NetworkPage from '@/pages/NetworkPage.vue'; import RulesPage from '@/pages/RulesPage.vue'; import ExitsPage from '@/pages/ExitsPage.vue'; import LogsPage from '@/pages/LogsPage.vue'; import SettingsPage from '@/pages/SettingsPage.vue'

const loading=ref(true), authenticated=ref(false), needsSetup=ref(false), busy=ref(false), error=ref(false)
const page=ref('overview'), message=ref(''), startupError=ref(''), dashboard=ref<Dashboard|null>(null), bundle=ref<Bundle|null>(null)
const metrics=ref<Metric[]>([]), diagnostics=ref<Diagnostics|null>(null), diagnosticsBusy=ref(false), metricsBusy=ref(false)
let metricsTimer: number | undefined
const theme=ref<'light-neutral'|'dark-neutral'>(initialTheme())
const titles:Record<string,string>={overview:'概览',network:'DNS 与网络',rules:'规则',exits:'出口',logs:'日志',settings:'设置'}
const pageTitle=computed(()=>titles[page.value]||'5gws')
const editablePage=computed(()=>['network','rules','exits','settings'].includes(page.value))
const runtimeBusy=computed(()=>metricsBusy.value || diagnosticsBusy.value)
const themeTitle=computed(()=>theme.value === 'light-neutral' ? '切换到深色模式' : '切换到浅色模式')
async function start(){ authenticated.value=true; await reload(); void refreshRuntime(); startMetricsTimer() }
async function reload(){ [dashboard.value,bundle.value]=await Promise.all([api.dashboard(),api.config()]) }
async function refresh(){ try{ await reload() }catch(cause){ show(cause,true) } }
async function validate(){ if(!bundle.value)return; await action(async()=>{ const result=await api.validateConfig(bundle.value!); return `预检通过，共 ${result.rule_count} 条规则` }) }
async function apply(){ if(!bundle.value)return; await action(async()=>{ const result=await api.applyConfig(bundle.value!); await reload(); return result.changed ? `配置已应用，共 ${result.rule_count} 条规则` : '配置没有变化' }) }
function imported(value: Bundle){ bundle.value=value; show('配置已导入，请预检后应用',false) }
async function action(fn:()=>Promise<string>){ busy.value=true; try{ show(await fn(),false) }catch(cause){ show(cause,true) }finally{ busy.value=false } }
async function loadMetrics(){ if(metricsBusy.value)return; metricsBusy.value=true; try{ metrics.value=(await api.metrics()).metrics }catch(cause){ show(cause,true) }finally{ metricsBusy.value=false } }
async function runDiagnostics(scope='all'){ if(diagnosticsBusy.value)return; diagnosticsBusy.value=true; try{ if(scope==='network'){ const [dns,dot]=await Promise.all([api.runDiagnostics('dns'),api.runDiagnostics('dot')]); diagnostics.value={...diagnostics.value,...dns,...dot,checked_at:dot.checked_at} }else{ const result=await api.runDiagnostics(scope); diagnostics.value={...diagnostics.value,...result} } }catch(cause){ show(cause,true) }finally{ diagnosticsBusy.value=false } }
async function refreshRuntime(){ await Promise.all([loadMetrics(),runDiagnostics()]) }
function startMetricsTimer(){ if(metricsTimer) window.clearInterval(metricsTimer); metricsTimer=window.setInterval(loadMetrics,30000) }
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
async function logout(){ await api.logout(); authenticated.value=false; needsSetup.value=false; if(metricsTimer)window.clearInterval(metricsTimer) }
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
onUnmounted(()=>{ if(metricsTimer)window.clearInterval(metricsTimer) })
</script>

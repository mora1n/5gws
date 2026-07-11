<template>
  <aside class="hidden w-60 shrink-0 border-r border-base-300 bg-base-100 lg:flex lg:flex-col">
    <div class="flex h-16 items-center gap-3 border-b border-base-300 px-5"><div class="grid size-8 place-items-center bg-neutral font-bold text-neutral-content">5G</div><strong>5gws</strong></div>
    <nav class="menu w-full flex-1 gap-1 p-3">
      <li v-for="item in items" :key="item.id"><button :class="page === item.id && 'menu-active'" @click="$emit('select', item.id)"><component :is="item.icon" class="size-4" />{{ item.label }}</button></li>
    </nav>
    <div class="border-t border-base-300 p-3"><button class="btn btn-ghost btn-sm w-full justify-start" @click="$emit('logout')"><LogOut class="size-4" />退出</button></div>
  </aside>
  <nav class="fixed inset-x-0 bottom-0 z-30 grid h-16 grid-cols-7 border-t border-base-300 bg-base-100 lg:hidden">
    <button v-for="item in items" :key="item.id" class="flex min-w-0 flex-col items-center justify-center gap-1 text-xs" :class="page === item.id ? 'text-primary' : 'text-base-content/60'" @click="$emit('select', item.id)"><component :is="item.icon" class="size-5" /><span class="max-w-full truncate">{{ item.short }}</span></button>
  </nav>
</template>

<script setup lang="ts">
import { Activity, History, ListTree, LogOut, Network, Route, Settings } from '@lucide/vue'
defineProps<{ page: string }>()
defineEmits<{ select: [page: string]; logout: [] }>()
const items = [
  { id: 'overview', label: '概览', short: '概览', icon: Activity }, { id: 'network', label: 'DNS 与网络', short: 'DNS', icon: Network },
  { id: 'rules', label: '规则与导入', short: '规则', icon: ListTree }, { id: 'exits', label: '出口', short: '出口', icon: Route },
  { id: 'logs', label: '日志与诊断', short: '日志', icon: Activity }, { id: 'history', label: '版本历史', short: '历史', icon: History },
  { id: 'settings', label: '设置', short: '设置', icon: Settings },
]
</script>

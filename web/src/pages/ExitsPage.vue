<template>
  <div class="panel-section flex items-center justify-between"><h2 class="text-lg font-semibold">出口</h2><button class="btn btn-neutral btn-sm" @click="add"><Plus class="size-4" />SS 出口</button></div>
  <section class="panel-section"><div class="overflow-x-auto border border-base-300"><table class="table"><thead><tr><th>名称</th><th>类型</th><th>服务器</th><th>本地 SOCKS</th><th></th></tr></thead><tbody>
    <tr v-for="(exit, index) in bundle.config.exits" :key="exit.name"><td><input v-model.trim="exit.name" class="input input-sm w-36" :disabled="exit.type === 'direct'" /></td><td><span class="badge badge-ghost">{{ exit.type }}</span></td><td><span v-if="exit.type === 'direct'">-</span><div v-else class="flex gap-2"><input v-model.trim="exit.server" class="input input-sm w-40" /><input v-model.number="exit.server_port" type="number" class="input input-sm w-24" /></div></td><td class="mono text-sm">{{ exit.type === 'direct' ? '-' : `${exit.listen_address}:${exit.listen_port}` }}</td><td><button v-if="exit.type !== 'direct'" class="btn btn-ghost btn-square btn-sm text-error" title="删除" @click="bundle.config.exits.splice(index, 1)"><Trash2 class="size-4" /></button></td></tr>
  </tbody></table></div></section>
  <section v-for="exit in ssExits" :key="exit.name" class="panel-section"><h3 class="mb-4 font-semibold">{{ exit.name }}</h3><div class="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
    <label><span class="field-label">加密方法</span><input v-model.trim="exit.method" class="input w-full" /></label><label><span class="field-label">密码</span><input v-model="exit.password" type="password" class="input w-full" /></label>
    <label><span class="field-label">本地地址</span><input v-model.trim="exit.listen_address" class="input w-full mono" /></label><label><span class="field-label">本地端口</span><input v-model.number="exit.listen_port" type="number" class="input w-full" /></label>
  </div></section>
</template>
<script setup lang="ts">
import { computed } from 'vue'; import { Plus, Trash2 } from '@lucide/vue'; import type { Bundle } from '@/types'
const bundle = defineModel<Bundle>('bundle', { required: true }); const ssExits = computed(() => bundle.value.config.exits.filter(e => e.type === 'shadowsocks-rust'))
function add() { let port = 1080; const used = new Set(bundle.value.config.exits.map(e => e.listen_port)); while (used.has(port)) port++; bundle.value.config.exits.push({ name: `ss${ssExits.value.length + 1}`, type: 'shadowsocks-rust', fwmark: 0, server: '', server_port: 8388, method: '2022-blake3-aes-128-gcm', password: '', username: 'default', listen_address: '127.0.0.1', listen_port: port, tcp: true, udp: true, timeout_seconds: 300 }) }
</script>

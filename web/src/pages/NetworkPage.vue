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
</template>
<script setup lang="ts">
import type { Bundle } from '@/types'
import ListField from '@/components/ListField.vue'
const bundle = defineModel<Bundle>('bundle', { required: true })
</script>

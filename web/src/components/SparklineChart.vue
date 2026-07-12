<template>
  <div class="h-8 w-full overflow-hidden" :aria-label="label" role="img">
    <svg class="h-full w-full" viewBox="0 0 100 28" preserveAspectRatio="none">
      <line x1="0" y1="27" x2="100" y2="27" class="stroke-base-300" vector-effect="non-scaling-stroke" />
      <polyline v-if="points" :points="points" fill="none" class="stroke-current" stroke-width="1.5" vector-effect="non-scaling-stroke" />
    </svg>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{ values: number[]; label: string }>()
const points = computed(() => {
  if (props.values.length < 2) return ''
  const min = Math.min(...props.values)
  const max = Math.max(...props.values)
  const span = max - min || 1
  return props.values.map((value, index) => {
    const x = (index / (props.values.length - 1)) * 100
    const y = 26 - ((value - min) / span) * 24
    return `${x.toFixed(2)},${y.toFixed(2)}`
  }).join(' ')
})
</script>

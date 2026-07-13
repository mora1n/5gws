<template>
  <label>
    <span class="field-label">{{ label }}</span>
    <textarea :value="draft" class="textarea h-28 w-full mono text-xs" @input="change" @blur="normalize" />
  </label>
</template>
<script setup lang="ts">
import { ref, watch } from 'vue'

const props = defineProps<{ modelValue: string[], label: string }>()
const emit = defineEmits<{ 'update:modelValue': [value: string[]] }>()
const draft = ref(format(props.modelValue))

watch(() => props.modelValue, value => {
  if (!sameValues(parse(draft.value), value)) draft.value = format(value)
}, { deep: true })

function change(event: Event) {
  draft.value = (event.target as HTMLTextAreaElement).value
  emit('update:modelValue', parse(draft.value))
}

function normalize() { draft.value = format(parse(draft.value)) }
function parse(value: string) { return value.split(/\r?\n/).map(item => item.trim()).filter(Boolean) }
function format(value: string[]) { return value.join('\n') }
function sameValues(left: string[], right: string[]) { return left.length === right.length && left.every((value, index) => value === right[index]) }
</script>

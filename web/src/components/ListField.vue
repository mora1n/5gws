<template><label><span class="field-label">{{ label }}</span><textarea :value="modelValue.join('\n')" class="textarea h-28 w-full mono text-xs" @input="change" /></label></template>
<script setup lang="ts">
defineProps<{ modelValue: string[], label: string }>()
const emit = defineEmits<{ 'update:modelValue': [value: string[]] }>()
function change(event: Event) { emit('update:modelValue', (event.target as HTMLTextAreaElement).value.split(/\r?\n/).map(v => v.trim()).filter(Boolean)) }
</script>

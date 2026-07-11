<template>
  <main class="flex min-h-screen items-center justify-center bg-base-200 px-4">
    <form class="w-full max-w-sm border border-base-300 bg-base-100 p-6 shadow-sm" @submit.prevent="submit">
      <div class="mb-6 flex items-center gap-3">
        <div class="grid size-10 place-items-center bg-neutral text-lg font-bold text-neutral-content">5G</div>
        <div><h1 class="text-xl font-semibold">5gws</h1><p class="text-sm text-base-content/60">管理面板</p></div>
      </div>
      <div v-if="setup" class="alert alert-error mb-4 text-sm">
        <CircleAlert class="size-4" />
        请在服务器终端运行 sudo 5gws reset-admin 创建管理员密码。
      </div>
      <label class="field-label">用户名</label>
      <input v-model.trim="username" required minlength="3" class="input mb-4 w-full" autocomplete="username" :disabled="setup" />
      <label class="field-label">密码</label>
      <input v-model="password" required minlength="12" type="password" class="input mb-5 w-full" autocomplete="current-password" :disabled="setup" />
      <div v-if="error" class="alert alert-error mb-4 text-sm"><CircleAlert class="size-4" />{{ error }}</div>
      <button class="btn btn-neutral w-full" :disabled="busy || setup"><LoaderCircle v-if="busy" class="size-4 animate-spin" />登录</button>
    </form>
  </main>
</template>

<script setup lang="ts">
import { CircleAlert, LoaderCircle } from '@lucide/vue'
import { ref } from 'vue'
import { api } from '@/api'

defineProps<{ setup: boolean }>()
const emit = defineEmits<{ done: [] }>()
const username = ref('')
const password = ref('')
const busy = ref(false)
const error = ref('')
async function submit() {
  busy.value = true; error.value = ''
  try {
    await api.login({ username: username.value, password: password.value })
    emit('done')
  } catch (cause) { error.value = cause instanceof Error ? cause.message : String(cause) }
  finally { busy.value = false }
}
</script>

<template>
  <main class="flex min-h-screen items-center justify-center bg-base-200 px-4">
    <form class="w-full max-w-sm border border-base-300 bg-base-100 p-6 shadow-sm" @submit.prevent="submit">
      <div class="mb-6 flex items-center gap-3">
        <div class="grid size-10 place-items-center bg-neutral text-lg font-bold text-neutral-content">5G</div>
        <div><h1 class="text-xl font-semibold">5gws</h1><p class="text-sm text-base-content/60">{{ setup ? '初始化管理员' : '管理面板' }}</p></div>
      </div>
      <label v-if="setup" class="field-label">Setup token</label>
      <input v-if="setup" v-model.trim="token" required class="input mb-4 w-full mono" autocomplete="one-time-code" />
      <label class="field-label">用户名</label>
      <input v-model.trim="username" required minlength="3" class="input mb-4 w-full" autocomplete="username" />
      <label class="field-label">密码</label>
      <input v-model="password" required minlength="12" type="password" class="input mb-5 w-full" :autocomplete="setup ? 'new-password' : 'current-password'" />
      <div v-if="error" class="alert alert-error mb-4 text-sm"><CircleAlert class="size-4" />{{ error }}</div>
      <button class="btn btn-neutral w-full" :disabled="busy"><LoaderCircle v-if="busy" class="size-4 animate-spin" />{{ setup ? '创建管理员' : '登录' }}</button>
    </form>
  </main>
</template>

<script setup lang="ts">
import { CircleAlert, LoaderCircle } from '@lucide/vue'
import { ref } from 'vue'
import { api } from '@/api'

const props = defineProps<{ setup: boolean }>()
const emit = defineEmits<{ done: [] }>()
const token = ref('')
const username = ref('')
const password = ref('')
const busy = ref(false)
const error = ref('')
async function submit() {
  busy.value = true; error.value = ''
  try {
    if (props.setup) await api.claim({ token: token.value, username: username.value, password: password.value })
    await api.login({ username: username.value, password: password.value })
    emit('done')
  } catch (cause) { error.value = cause instanceof Error ? cause.message : String(cause) }
  finally { busy.value = false }
}
</script>

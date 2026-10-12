<script setup lang="ts">
import { nextTick, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ApiError } from '@/api/client'
import { useSessionStore } from '@/stores/session'

const session = useSessionStore()
const router = useRouter()

const username = ref('')
const displayName = ref('')
const password = ref('')
const confirmation = ref('')
const error = ref('')
const busy = ref(false)

const usernameInput = ref<HTMLInputElement | null>(null)
onMounted(() => usernameInput.value?.focus())

async function submit(): Promise<void> {
  error.value = ''

  // Checked here as well as on the server, because the cost of getting this
  // wrong is being locked out of a brand-new system with no other account to
  // recover from.
  if (password.value !== confirmation.value) {
    error.value = 'The two passwords do not match.'
    await nextTick()
    return
  }

  busy.value = true
  try {
    await session.createFirstAdmin(username.value, displayName.value, password.value)
    await router.push('/')
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Cannot reach the server.'
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <form class="auth" @submit.prevent="submit">
    <div class="auth__brand">
      <span class="auth__mark" aria-hidden="true">B</span>
      <span class="auth__wordmark">Bokkie</span>
    </div>

    <h1 class="auth__title">Set up Bokkie</h1>
    <p class="auth__station">
      This system has no accounts yet. Create the manager account -- it can add
      everyone else afterwards. This page will not appear again.
    </p>

    <label class="auth__field">
      <span>Username</span>
      <input
        ref="usernameInput"
        v-model="username"
        name="username"
        autocomplete="username"
        autocapitalize="none"
        autocorrect="off"
        spellcheck="false"
        required
      />
      <small>Lowercase letters, numbers, dot, dash or underscore.</small>
    </label>

    <label class="auth__field">
      <span>Full name</span>
      <input v-model="displayName" name="display-name" autocomplete="name" />
      <small>Shown on slips and in the audit log.</small>
    </label>

    <label class="auth__field">
      <span>Password</span>
      <input
        v-model="password"
        name="password"
        type="password"
        autocomplete="new-password"
        required
      />
      <small>At least 8 characters.</small>
    </label>

    <label class="auth__field">
      <span>Confirm password</span>
      <input
        v-model="confirmation"
        name="password-confirmation"
        type="password"
        autocomplete="new-password"
        required
      />
    </label>

    <p v-if="error" class="auth__error" role="alert">{{ error }}</p>

    <button class="auth__submit" type="submit" :disabled="busy">
      {{ busy ? 'Creating...' : 'Create manager account' }}
    </button>
  </form>
</template>

<style scoped src="@/styles/auth.css"></style>

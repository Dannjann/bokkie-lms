<script setup lang="ts">
import { nextTick, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { storeToRefs } from 'pinia'
import { ApiError } from '@/api/client'
import { useSessionStore } from '@/stores/session'

const session = useSessionStore()
const { lockedUser } = storeToRefs(session)
const router = useRouter()

const username = ref('')
const password = ref('')
const error = ref('')
const busy = ref(false)

const usernameInput = ref<HTMLInputElement | null>(null)
const passwordInput = ref<HTMLInputElement | null>(null)

// After Lock the username is already known, so the cursor belongs in the
// password field. That difference is what makes locking a one-second habit
// instead of a chore -- and a long session tolerable without an idle timeout.
onMounted(async () => {
  if (lockedUser.value) {
    username.value = lockedUser.value
    await nextTick()
    passwordInput.value?.focus()
  } else {
    usernameInput.value?.focus()
  }
})

async function submit(): Promise<void> {
  error.value = ''
  busy.value = true
  try {
    await session.login(username.value, password.value)
    await router.push('/')
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Cannot reach the server.'
    password.value = ''
    await nextTick()
    passwordInput.value?.focus()
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <form class="auth" @submit.prevent="submit">
    <h1 class="auth__title">Sign in</h1>
    <p class="auth__station">TLS Palay &amp; Corn Buying Station</p>

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
    </label>

    <label class="auth__field">
      <span>Password</span>
      <input
        ref="passwordInput"
        v-model="password"
        name="password"
        type="password"
        autocomplete="current-password"
        required
      />
    </label>

    <p v-if="error" class="auth__error" role="alert">{{ error }}</p>

    <button class="auth__submit" type="submit" :disabled="busy">
      {{ busy ? 'Signing in...' : 'Sign in' }}
    </button>
  </form>
</template>

<style scoped src="@/styles/auth.css"></style>

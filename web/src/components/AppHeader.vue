<script setup lang="ts">
import { ref } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import { storeToRefs } from 'pinia'
import { useSessionStore } from '@/stores/session'

const session = useSessionStore()
const { user, isLoggedIn } = storeToRefs(session)
const router = useRouter()

const busy = ref(false)

// Lock, not a timeout. The station PC is signed in from opening to closing --
// that is the real usage -- so the way to leave it safely is a one-click action
// that keeps the username, not an idle timer that logs people out mid-payment
// and teaches them to share passwords instead.
async function lock(): Promise<void> {
  busy.value = true
  try {
    await session.lock()
    await router.push({ name: 'login' })
  } finally {
    busy.value = false
  }
}

async function signOut(): Promise<void> {
  busy.value = true
  try {
    await session.logout()
    await router.push({ name: 'login' })
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <header class="app-header">
    <RouterLink to="/" class="app-header__brand">Bokkie</RouterLink>
    <span class="app-header__station">TLS Palay &amp; Corn Buying Station</span>

    <nav v-if="isLoggedIn" class="app-header__nav">
      <RouterLink :to="{ name: 'customers' }">Customers</RouterLink>
    </nav>

    <div v-if="isLoggedIn && user" class="app-header__session">
      <span class="app-header__user">
        {{ user.display_name }}
        <span class="app-header__role">{{ user.role }}</span>
      </span>
      <button type="button" :disabled="busy" @click="lock">Lock</button>
      <button type="button" :disabled="busy" @click="signOut">Sign out</button>
    </div>
  </header>
</template>

<style scoped>
.app-header {
  display: flex;
  align-items: baseline;
  gap: 0.75rem;
  padding: 0.75rem 1rem;
  background: var(--p-primary-color);
  color: var(--p-primary-contrast-color);
}

.app-header__brand {
  font-weight: 700;
  font-size: 1.125rem;
  color: inherit;
  text-decoration: none;
}

.app-header__station {
  font-size: 0.875rem;
  opacity: 0.85;
}

.app-header__nav {
  display: flex;
  gap: 0.75rem;
  font-size: 0.875rem;
}

.app-header__nav a {
  color: inherit;
  text-decoration: none;
  padding: 0.2rem 0.5rem;
  border-radius: 4px;
  opacity: 0.85;
}

.app-header__nav a:hover,
.app-header__nav a.router-link-active {
  opacity: 1;
  background: rgb(255 255 255 / 0.15);
}

.app-header__session {
  margin-left: auto;
  display: flex;
  align-items: center;
  gap: 0.5rem;
  font-size: 0.875rem;
}

.app-header__user {
  display: flex;
  align-items: baseline;
  gap: 0.35rem;
}

.app-header__role {
  font-size: 0.75rem;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  opacity: 0.75;
}

.app-header__session button {
  padding: 0.3rem 0.7rem;
  font-size: 0.8125rem;
  color: inherit;
  background: transparent;
  border: 1px solid currentColor;
  border-radius: 4px;
  cursor: pointer;
  opacity: 0.9;
}

.app-header__session button:hover:not(:disabled) {
  opacity: 1;
  background: rgb(255 255 255 / 0.12);
}

.app-header__session button:disabled {
  opacity: 0.5;
  cursor: progress;
}
</style>

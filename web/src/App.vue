<script setup lang="ts">
import { computed } from 'vue'
import { RouterView, useRoute } from 'vue-router'
import { storeToRefs } from 'pinia'
import AppHeader from '@/components/AppHeader.vue'
import { useSessionStore } from '@/stores/session'

const route = useRoute()
const { isLoggedIn } = storeToRefs(useSessionStore())

// The header carries the Lock and Sign out controls, so it has nothing to show
// on the two screens that exist precisely because nobody is signed in.
const showHeader = computed(() => isLoggedIn.value && !route.meta.public)
</script>

<template>
  <AppHeader v-if="showHeader" />
  <main class="app-main">
    <RouterView />
  </main>
</template>

<style>
:root {
  font-family: system-ui, -apple-system, 'Segoe UI', sans-serif;
  color-scheme: light;
}

body {
  margin: 0;
  background: var(--p-surface-50);
  color: var(--p-surface-900);
}

.app-main {
  max-width: 64rem;
  margin: 0 auto;
  padding: 1.5rem 1rem;
}
</style>

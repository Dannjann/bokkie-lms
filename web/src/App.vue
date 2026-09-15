<script setup lang="ts">
import { RouterView } from 'vue-router'
import AppHeader from '@/components/AppHeader.vue'
</script>

<template>
  <AppHeader />
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

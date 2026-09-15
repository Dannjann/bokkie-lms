<script setup lang="ts">
import { RouterLink } from 'vue-router'
</script>

<template>
  <header class="app-header">
    <RouterLink to="/" class="app-header__brand">Bokkie</RouterLink>
    <span class="app-header__station">TLS Palay &amp; Corn Buying Station</span>
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
</style>

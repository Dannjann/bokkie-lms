<script setup lang="ts">
import { useRouter } from 'vue-router'
import Button from 'primevue/button'

const router = useRouter()
</script>

<template>
  <div class="not-found">
    <i class="pi pi-compass not-found__icon" aria-hidden="true" />
    <h1 class="not-found__title">Page not found</h1>
    <p class="not-found__body">
      That address does not exist in Bokkie. If you reached it from a link inside
      the app rather than by typing it, that is worth reporting.
    </p>
    <Button label="Back to the dashboard" icon="pi pi-home" @click="router.push('/')" />
  </div>
</template>

<style scoped>
.not-found {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 0.5rem;
  max-width: 26rem;
  margin: 3rem auto;
  text-align: center;
}

.not-found__icon {
  font-size: 2rem;
  color: var(--p-surface-400);
}

.not-found__title {
  margin: 0.5rem 0 0;
  font-size: 1.375rem;
  font-weight: 600;
}

.not-found__body {
  margin: 0 0 0.75rem;
  font-size: 0.875rem;
  color: var(--shell-muted);
}
</style>

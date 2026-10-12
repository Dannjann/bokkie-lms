<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { storeToRefs } from 'pinia'
import { useHealthStore } from '@/stores/health'
import { pageTitle } from '@/pageTitle'

defineProps<{ menuOpen: boolean }>()
defineEmits<{ toggleMenu: [] }>()

const route = useRoute()
const { health, error, loading } = storeToRefs(useHealthStore())

const title = computed(() => pageTitle.value || route.meta.title || '')
const parent = computed(() => route.meta.parent)

/**
 * Three states, not two. "Checking" matters on a station LAN: a cable pulled
 * out of a switch looks exactly like a slow first paint for about a second, and
 * telling someone the server is down when it is merely answering slowly is how
 * you get a phone call.
 */
const connection = computed(() => {
  if (loading.value && !health.value) return { tone: 'checking', label: 'Checking…' }
  if (error.value) return { tone: 'down', label: 'No connection' }
  if (health.value?.status === 'ok') return { tone: 'ok', label: 'Connected' }
  return { tone: 'down', label: 'Server unwell' }
})
</script>

<template>
  <header class="topbar">
    <button
      type="button"
      class="topbar__menu"
      :aria-expanded="menuOpen"
      aria-controls="app-sidebar"
      aria-label="Menu"
      @click="$emit('toggleMenu')"
    >
      <i :class="menuOpen ? 'pi pi-times' : 'pi pi-bars'" aria-hidden="true" />
    </button>

    <nav class="topbar__crumbs" aria-label="Breadcrumb">
      <template v-if="parent">
        <RouterLink :to="parent.to" class="topbar__crumb">{{ parent.label }}</RouterLink>
        <i class="pi pi-angle-right topbar__separator" aria-hidden="true" />
      </template>
      <span class="topbar__here" aria-current="page">{{ title }}</span>
    </nav>

    <span class="topbar__connection" :class="`topbar__connection--${connection.tone}`">
      <span class="topbar__dot" aria-hidden="true" />
      {{ connection.label }}
    </span>
  </header>
</template>

<style scoped>
.topbar {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  height: var(--shell-topbar-height);
  padding: 0 1.25rem;
  background: var(--shell-surface);
  border-bottom: 1px solid var(--shell-border);
}

.topbar__menu {
  display: none;
  place-items: center;
  width: 2.25rem;
  height: 2.25rem;
  margin-left: -0.5rem;
  padding: 0;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: var(--p-surface-700);
  cursor: pointer;
}

.topbar__menu:hover {
  background: var(--p-surface-100);
}

.topbar__crumbs {
  display: flex;
  align-items: center;
  gap: 0.35rem;
  min-width: 0;
  font-size: 0.875rem;
}

.topbar__crumb {
  color: var(--shell-muted);
  text-decoration: none;
  white-space: nowrap;
}

.topbar__crumb:hover {
  color: var(--p-primary-color);
  text-decoration: underline;
}

.topbar__separator {
  font-size: 0.75rem;
  color: var(--p-surface-400);
}

.topbar__here {
  font-weight: 600;
  color: var(--p-surface-800);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.topbar__connection {
  margin-left: auto;
  display: flex;
  align-items: center;
  gap: 0.4rem;
  font-size: 0.75rem;
  color: var(--shell-muted);
  white-space: nowrap;
}

.topbar__dot {
  width: 0.5rem;
  height: 0.5rem;
  border-radius: 50%;
  background: currentColor;
}

.topbar__connection--ok .topbar__dot {
  background: #16a34a;
}

.topbar__connection--down {
  color: #b91c1c;
  font-weight: 500;
}

.topbar__connection--down .topbar__dot {
  background: #dc2626;
}

.topbar__connection--checking .topbar__dot {
  background: var(--p-surface-400);
}

@media (max-width: 60rem) {
  .topbar__menu {
    display: grid;
  }
}
</style>

<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { storeToRefs } from 'pinia'
import { useHealthStore } from '@/stores/health'
import { pageParent } from '@/pageTitle'

defineProps<{ menuOpen: boolean }>()
defineEmits<{ toggleMenu: [] }>()

const route = useRoute()
const { health, error, loading } = storeToRefs(useHealthStore())

/**
 * Back sits at the leading edge, named after where it goes — "‹ Customers",
 * not "Back". That is where every platform puts it, and where a hand reaching
 * for it already goes. It used to be a text button at the far right of each
 * screen's header, after the actions, which on a phone wrapped it onto a line
 * of its own beneath them.
 *
 * A page that knows a better answer than its route — a statement belongs to
 * one customer, not to the list — says so through setPageParent.
 */
const parent = computed(() => pageParent.value ?? route.meta.parent ?? null)

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
  <header class="topbar" :class="{ 'topbar--child': parent }">
    <!--
      On a phone the menu and Back compete for the same corner. A top-level
      screen gets the menu; a screen with somewhere to go back to gets Back,
      the way a navigation stack works — the menu is one tap further away, on
      the screen Back leads to.
    -->
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

    <RouterLink
      v-if="parent"
      :to="parent.to"
      class="topbar__back"
      :aria-label="`Back to ${parent.label}`"
    >
      <i class="pi pi-chevron-left" aria-hidden="true" />
      <span class="topbar__back-label">{{ parent.label }}</span>
    </RouterLink>

    <span
      class="topbar__connection"
      :class="`topbar__connection--${connection.tone}`"
      role="status"
    >
      <span class="topbar__dot" aria-hidden="true" />
      {{ connection.label }}
    </span>
  </header>
</template>

<style scoped>
.topbar {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  height: var(--shell-topbar-height);
  padding: 0 1.25rem 0 0.75rem;
  background: rgb(255 255 255 / 0.85);
  backdrop-filter: saturate(180%) blur(12px);
  border-bottom: 1px solid var(--shell-border);
}

.topbar__menu,
.topbar__back {
  display: flex;
  align-items: center;
  min-height: var(--shell-hit);
  border-radius: 8px;
  color: var(--p-primary-color);
}

.topbar__menu {
  display: none;
  justify-content: center;
  min-width: var(--shell-hit);
  padding: 0;
  border: none;
  background: transparent;
  font-size: 1.125rem;
  cursor: pointer;
}

.topbar__back {
  gap: 0.25rem;
  min-width: 0;
  padding: 0 0.625rem 0 0.375rem;
  font-size: 0.9375rem;
  font-weight: 500;
  text-decoration: none;
}

.topbar__back i {
  font-size: 0.875rem;
}

.topbar__back-label {
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.topbar__menu:hover,
.topbar__back:hover {
  background: var(--p-primary-50);
  text-decoration: none;
}

.topbar__menu:focus-visible,
.topbar__back:focus-visible {
  outline: 2px solid var(--p-primary-color);
  outline-offset: 0;
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
  .topbar {
    padding-left: 0.5rem;
  }

  .topbar__menu {
    display: flex;
  }

  .topbar--child .topbar__menu {
    display: none;
  }
}
</style>

<script setup lang="ts">
import { computed } from 'vue'
import { RouterView, useRoute } from 'vue-router'
import { storeToRefs } from 'pinia'
import AppShell from '@/components/AppShell.vue'
import { useSessionStore } from '@/stores/session'

const route = useRoute()
const { isLoggedIn } = storeToRefs(useSessionStore())

// The shell is navigation and a signed-in person's name, so it has nothing to
// show on the two screens that exist precisely because nobody is signed in.
const showShell = computed(() => isLoggedIn.value && !route.meta.public)
</script>

<template>
  <AppShell v-if="showShell">
    <RouterView />
  </AppShell>

  <main v-else class="app-plain">
    <RouterView />
  </main>
</template>

<style scoped>
.app-plain {
  min-height: 100vh;
  min-height: 100dvh;
  padding: 1.5rem 1rem;
}
</style>

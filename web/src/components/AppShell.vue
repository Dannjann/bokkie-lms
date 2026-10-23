<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import AppSidebar from '@/components/AppSidebar.vue'
import AppTopbar from '@/components/AppTopbar.vue'
import { useHealthStore } from '@/stores/health'
import { pageParent } from '@/pageTitle'

/**
 * The frame every signed-in screen sits inside: a fixed rail on the left, a
 * thin bar across the top, and a scrolling well of content.
 *
 * Only the well scrolls. On a counter PC the navigation and the person's name
 * should never move off screen, and on a phone the rail slides over the content
 * instead of sitting beside it.
 */
const menuOpen = ref(false)

/**
 * A screen with a Back link at the top-left reads down from it, so its content
 * starts on the same x as the link and runs the full width — centring it in a
 * capped well leaves the link hanging off to one side of everything below it.
 */
const route = useRoute()
const isChild = computed(() => Boolean(pageParent.value ?? route.meta.parent))

const health = useHealthStore()

// The topbar's connection light is the only always-visible sign that the host
// is still there, so the shell owns the polling rather than any one page. A
// minute is often enough to notice a pulled cable before someone types a
// voucher into a browser that cannot save it.
let timer: ReturnType<typeof setInterval> | undefined

onMounted(() => {
  health.load()
  timer = setInterval(() => health.load(), 60_000)
})

onBeforeUnmount(() => clearInterval(timer))
</script>

<template>
  <div class="shell" :class="{ 'shell--menu-open': menuOpen, 'shell--child': isChild }">
    <!--
      Inert on desktop, where the rail is always in the layout; only the phone
      breakpoint turns it into a dismissible overlay.
    -->
    <div class="shell__scrim" @click="menuOpen = false" />

    <aside id="app-sidebar" class="shell__sidebar">
      <AppSidebar @navigate="menuOpen = false" />
    </aside>

    <div class="shell__body">
      <AppTopbar :menu-open="menuOpen" @toggle-menu="menuOpen = !menuOpen" />
      <main class="shell__content">
        <div class="shell__well">
          <slot />
        </div>
      </main>
    </div>
  </div>
</template>

<style scoped>
.shell {
  display: grid;
  grid-template-columns: var(--shell-nav-width) 1fr;
  height: 100vh;
  height: 100dvh;
}

.shell__sidebar {
  min-height: 0;
  border-right: 1px solid var(--shell-nav-border);
}

.shell__body {
  display: flex;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
}

.shell__content {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 1.5rem 1.25rem 3rem;
}

/*
 * Wide enough for a customer table with an address column, capped so that on a
 * 27" monitor the eye is not tracking a line of text a foot long.
 */
.shell__well {
  max-width: 76rem;
  margin: 0 auto;
}

.shell--child .shell__well {
  max-width: none;
  margin: 0;
}

.shell__scrim {
  display: none;
}

@media (max-width: 60rem) {
  .shell {
    grid-template-columns: 1fr;
  }

  .shell__sidebar {
    position: fixed;
    inset: 0 auto 0 0;
    z-index: 30;
    width: var(--shell-nav-width);
    transform: translateX(-100%);
    transition: transform 150ms ease;
  }

  .shell--menu-open .shell__sidebar {
    transform: none;
  }

  .shell--menu-open .shell__scrim {
    display: block;
    position: fixed;
    inset: 0;
    z-index: 20;
    background: rgb(15 23 42 / 0.45);
  }

  .shell__content {
    padding: 1.25rem 1rem 2.5rem;
  }
}

@media (prefers-reduced-motion: reduce) {
  .shell__sidebar {
    transition: none;
  }
}
</style>

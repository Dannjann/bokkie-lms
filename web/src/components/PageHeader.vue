<script setup lang="ts">
/**
 * The title block at the top of a screen: heading, one line of explanation, and
 * whatever buttons belong to the page as a whole.
 *
 * Shared so that every screen's heading is the same size in the same place.
 * Pages that need something richer than a string — the customer screen puts a
 * code and a status tag above the name — override the `title` slot.
 */
defineProps<{ title?: string; description?: string }>()
</script>

<template>
  <header class="page-header">
    <div class="page-header__text">
      <slot name="title">
        <h1 class="page-header__title">{{ title }}</h1>
      </slot>
      <p v-if="description" class="page-header__description">{{ description }}</p>
    </div>

    <div v-if="$slots.actions" class="page-header__actions">
      <slot name="actions" />
    </div>
  </header>
</template>

<style scoped>
.page-header {
  display: flex;
  align-items: flex-start;
  gap: 1rem;
  margin-bottom: 1.25rem;
}

.page-header__text {
  min-width: 0;
}

.page-header__title {
  margin: 0;
  font-size: 1.375rem;
  font-weight: 600;
  letter-spacing: -0.01em;
  color: var(--p-surface-900);
}

.page-header__description {
  margin: 0.25rem 0 0;
  font-size: 0.875rem;
  color: var(--shell-muted);
}

/*
 * The buttons keep their own line and the heading text gives way instead.
 *
 * Wrapping these was measured, not guessed: with the rail still in the layout
 * there is a band from roughly 964px to 1032px where "Import CSV" and "New
 * customer" no longer fit beside the heading and stack on top of each other.
 * The counter PCs run Edge in --app mode at about 1024px, which is the middle
 * of that band — so the one width the station actually uses was the one width
 * this header came apart at. Below 40rem the rail is an overlay, the content
 * has the full width back, and the actions take a row of their own on purpose.
 */
.page-header__actions {
  margin-left: auto;
  display: flex;
  flex-wrap: nowrap;
  gap: 0.5rem;
}

/*
 * A button is as wide as its label. Without this the labels themselves break --
 * "New customer" becomes two lines inside one double-height button -- which is
 * the same squeeze as the stack above, just moved inside the button. The
 * heading text is the thing with slack in it, so it is the thing that gives.
 */
.page-header__actions :deep(.p-button) {
  white-space: nowrap;
}

@media (max-width: 40rem) {
  .page-header {
    flex-wrap: wrap;
  }

  .page-header__actions {
    margin-left: 0;
    width: 100%;
    flex-wrap: wrap;
  }
}
</style>

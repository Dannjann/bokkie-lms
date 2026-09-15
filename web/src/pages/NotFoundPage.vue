<script setup lang="ts">
import { RouterLink } from 'vue-router'
</script>

<template>
  <h1>Page not found</h1>
  <p>
    That address does not exist in Bokkie.
    <RouterLink to="/">Go back to the start</RouterLink>.
  </p>
</template>

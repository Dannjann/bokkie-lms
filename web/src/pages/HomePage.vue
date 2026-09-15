<script setup lang="ts">
import { onMounted } from 'vue'
import { storeToRefs } from 'pinia'
import { useHealthStore } from '@/stores/health'

const store = useHealthStore()
const { health, error, loading } = storeToRefs(store)

onMounted(() => store.load())
</script>

<template>
  <h1>Bokkie is running</h1>
  <p>
    Lending management for TLS Palay &amp; Corn. The screens arrive from step 4;
    this page exists to prove the path from this device to the server and its
    database.
  </p>

  <p v-if="loading">Checking the server…</p>
  <p v-else-if="error" role="alert">Cannot reach the server: {{ error }}</p>
  <dl v-else-if="health">
    <dt>Status</dt>
    <dd>{{ health.status }}</dd>
    <dt>Build</dt>
    <dd>{{ health.version }} ({{ health.commit }})</dd>
    <dt>Database</dt>
    <dd>{{ health.database }}</dd>
    <dt>Uptime</dt>
    <dd>{{ health.uptime_seconds }}s</dd>
  </dl>
</template>

<style scoped>
dl {
  display: grid;
  grid-template-columns: max-content 1fr;
  gap: 0.25rem 1rem;
}

dt {
  font-weight: 600;
}

dd {
  margin: 0;
}
</style>

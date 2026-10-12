import { defineStore } from 'pinia'
import { ref } from 'vue'
import { fetchHealth, type Health } from '@/api/health'
import { ApiError } from '@/api/client'

/**
 * Health of the server this browser is talking to. It is the one thing worth
 * showing before any feature exists: it proves the whole path — counter PC to
 * host to SQLite — is working.
 */
export const useHealthStore = defineStore('health', () => {
  const health = ref<Health | null>(null)
  const error = ref<string | null>(null)
  const loading = ref(false)

  async function load(): Promise<void> {
    loading.value = true
    error.value = null
    try {
      health.value = await fetchHealth()
    } catch (err) {
      health.value = null
      error.value = err instanceof ApiError ? `${err.message} (${err.status})` : String(err)
    } finally {
      loading.value = false
    }
  }

  return { health, error, loading, load }
})

import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { ApiError } from '@/api/client'
import * as api from '@/api/session'
import type { SessionState, User } from '@/api/session'

/**
 * Who is logged in.
 *
 * The server is the only authority here — this store caches its answer so the
 * router can make a decision synchronously, and re-asks whenever that answer
 * might have changed. Nothing about the session is stored in localStorage: the
 * cookie is HttpOnly and this app runs on shared counter PCs.
 */
export const useSessionStore = defineStore('session', () => {
  const user = ref<User | null>(null)
  const needsSetup = ref(false)
  const lockedUser = ref('')

  /** Whether this browser is the host machine. See SessionState.host. */
  const isHost = ref(false)

  /** Null until the first fetchSession resolves, so the router can wait for it. */
  const loaded = ref(false)

  const isLoggedIn = computed(() => user.value !== null)
  const isManager = computed(() => user.value?.role === 'manager')

  function apply(state: SessionState): void {
    user.value = state.user ?? null
    needsSetup.value = state.needs_setup
    lockedUser.value = state.locked_user ?? ''
    isHost.value = state.host ?? false
    loaded.value = true
  }

  async function load(): Promise<void> {
    try {
      apply(await api.fetchSession())
    } catch {
      // The server is unreachable. Treat it as logged out rather than
      // pretending otherwise — every subsequent call would fail anyway, and
      // the login screen at least says something honest.
      // host: false because we no longer know. Claiming to be the host while
      // the server is unreachable would offer a switch that cannot be pressed.
      apply({ needs_setup: false, host: false })
    }
  }

  async function login(username: string, password: string): Promise<void> {
    apply(await api.login(username, password))
  }

  async function createFirstAdmin(
    username: string,
    displayName: string,
    password: string,
  ): Promise<void> {
    apply(await api.createFirstAdmin(username, displayName, password))
  }

  async function logout(): Promise<void> {
    await api.logout().catch(ignoreUnauthorized)
    await load()
  }

  async function lock(): Promise<void> {
    await api.lock().catch(ignoreUnauthorized)
    await load()
  }

  return {
    user,
    needsSetup,
    lockedUser,
    isHost,
    loaded,
    isLoggedIn,
    isManager,
    load,
    login,
    createFirstAdmin,
    logout,
    lock,
  }
})

/**
 * A 401 while logging out means the session was already gone. That is the
 * outcome the caller wanted, so it is not an error worth showing anyone.
 */
function ignoreUnauthorized(err: unknown): void {
  if (err instanceof ApiError && err.status === 401) return
  throw err
}

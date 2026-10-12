import { deleteJson, getJson, postJson } from '@/api/client'

export type Role = 'encoder' | 'manager'

export interface User {
  id: number
  username: string
  display_name: string
  role: Role
  active: boolean
  created_at: string
  last_login_at?: string
}

export interface SessionState {
  /** True when this install has no accounts at all and belongs on the setup page. */
  needs_setup: boolean
  user?: User
  /** Left behind by Lock so the login form can prefill the username. */
  locked_user?: string
  /**
   * True when this browser is on the machine bokkie runs on. The network
   * switch is host-only, so the dashboard needs this to show whose switch it
   * is rather than letting someone press one that will refuse.
   */
  host: boolean
}

/** Who am I. The app's first call on every load. */
export function fetchSession(): Promise<SessionState> {
  return getJson<SessionState>('/api/session')
}

export function login(username: string, password: string): Promise<SessionState> {
  return postJson<SessionState>('/api/session', { username, password })
}

export function logout(): Promise<SessionState> {
  return deleteJson<SessionState>('/api/session')
}

/** Lock ends the session but leaves the username for the login form. */
export function lock(): Promise<SessionState> {
  return postJson<SessionState>('/api/session/lock')
}

export function createFirstAdmin(
  username: string,
  displayName: string,
  password: string,
): Promise<SessionState> {
  return postJson<SessionState>('/api/setup', {
    username,
    display_name: displayName,
    password,
  })
}

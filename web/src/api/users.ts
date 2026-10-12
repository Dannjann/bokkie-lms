import { getJson, postJson } from '@/api/client'
import type { Role, User } from '@/api/session'

export type { Role, User }

/**
 * The accounts, and what can be done to one.
 *
 * Three of these four are manager-only at the server; changing your own
 * password is not, and is the only part of this file an encoder ever reaches.
 *
 * Deactivating an account and changing its role both take effect on that
 * account's *next request* — the server re-reads the user on every request and
 * ends the session when it finds it inactive. A password change is the one that
 * does not ride on that, so the server ends the sessions itself and says how
 * many it ended.
 */

export function listUsers(): Promise<{ users: User[] }> {
  return getJson<{ users: User[] }>('/api/users')
}

export function createUser(input: {
  username: string
  display_name: string
  password: string
  role: Role
}): Promise<User> {
  return postJson<User>('/api/users', input)
}

export function setUserActive(id: number, active: boolean): Promise<User> {
  return postJson<User>(`/api/users/${id}/active`, { active })
}

export function setUserRole(id: number, role: Role): Promise<User> {
  return postJson<User>(`/api/users/${id}/role`, { role })
}

/** A manager setting somebody else's password. Ends every session they hold. */
export function resetUserPassword(
  id: number,
  password: string,
): Promise<{ user: User; sessions_ended: number }> {
  return postJson(`/api/users/${id}/password`, { password })
}

/**
 * Changing your own. The current password is required — a session is not proof
 * of the password behind it — and the tab this is called from stays signed in
 * while every other one is ended.
 */
export function changeOwnPassword(
  current: string,
  next: string,
): Promise<{ user: User; sessions_ended: number }> {
  return postJson('/api/session/password', { current, new: next })
}

export const roleOptions: { label: string; value: Role }[] = [
  { label: 'Encoder', value: 'encoder' },
  { label: 'Manager', value: 'manager' },
]

/** What each role may do, in the words the person choosing one needs. */
export const roleDescriptions: Record<Role, string> = {
  encoder: 'Counter work: customers, advances, payments.',
  manager: 'Everything, plus voiding, interest, reports and these settings.',
}

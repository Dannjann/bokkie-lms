import { getJson } from '@/api/client'

/**
 * The audit log.
 *
 * Read-only, and there is no write function here because there is no write
 * endpoint: the table is append-only in the schema itself, enforced by triggers
 * that refuse a hand-typed UPDATE in a sqlite3 shell.
 */

export interface AuditEntry {
  id: number
  /** RFC3339 in the station's timezone, already converted by the server. */
  at: string
  user_id?: number
  username: string
  action: string
  entity?: string
  entity_id?: string
  /**
   * The state before and after, as the raw JSON text that was stored. Not
   * parsed by the server on purpose — re-encoding it through today's Go structs
   * would quietly rewrite history — so it is a string here and is pretty-printed
   * for display only.
   */
  before?: string
  after?: string
  request_id?: string
  ip?: string
}

export interface AuditPage {
  entries: AuditEntry[]
  /** The cursor for the next page, or 0 when this was the last one. */
  next: number
}

export interface AuditQuery {
  userId?: number | null
  action?: string | null
  entity?: string | null
  from?: string | null
  to?: string | null
  /** Keyset cursor: only entries older than this id. */
  before?: number
}

export function listAudit(query: AuditQuery = {}): Promise<AuditPage> {
  const params = new URLSearchParams()
  if (query.userId) params.set('user_id', String(query.userId))
  if (query.action) params.set('action', query.action)
  if (query.entity) params.set('entity', query.entity)
  if (query.from) params.set('from', query.from)
  if (query.to) params.set('to', query.to)
  if (query.before) params.set('before', String(query.before))

  const qs = params.toString()
  return getJson<AuditPage>(`/api/audit${qs ? `?${qs}` : ''}`)
}

export interface AuditFacets {
  users: { id: number; username: string }[]
  actions: string[]
  entities: string[]
}

/** What the filter bar offers, read from the table rather than a fixed list. */
export function auditFacets(): Promise<AuditFacets> {
  return getJson<AuditFacets>('/api/audit/facets')
}

/**
 * An action string as a person would say it.
 *
 * Falls back to the raw string rather than to a blank or a guess: a new action
 * shipped by a later step must show up readable-ish and searchable rather than
 * as an empty cell, and the raw form is what the log actually contains.
 */
export function describeAction(action: string): string {
  return actionNames[action] ?? action
}

/**
 * Every action string this system can write, in the words staff use.
 *
 * The list is kept complete on purpose. A half-translated log — "Account
 * deactivated" on one row and `customer.created` on the next — reads as
 * something half-built, and the raw form is the one nobody says out loud.
 * The fallback above still exists for an action a later step adds before this
 * list catches up; it should be a temporary state, not the normal one.
 */
const actionNames: Record<string, string> = {
  // Sessions and access
  'auth.login.succeeded': 'Signed in',
  'auth.login.failed': 'Sign-in failed',
  'auth.login.blocked': 'Sign-in blocked',
  'auth.logout': 'Signed out',
  'auth.lock': 'Locked the screen',
  'auth.access.denied': 'Access denied',
  'setup.completed': 'First administrator created',
  'setup.refused': 'Setup refused',

  // Accounts
  'user.created': 'Account created',
  'user.activated': 'Account reactivated',
  'user.deactivated': 'Account deactivated',
  'user.role.changed': 'Role changed',
  'user.password.reset': 'Password reset by a manager',
  'user.password.changed': 'Password changed',

  // Customers
  'customer.created': 'Customer added',
  'customer.updated': 'Customer edited',
  'customer.active.changed': 'Customer activated or deactivated',
  'customer.imported': 'Customers imported from a file',

  // Advances
  'advance.draft.created': 'Advance slip started',
  'advance.draft.updated': 'Advance slip draft edited',
  'advance.draft.deleted': 'Advance slip draft discarded',
  'advance.posted': 'Advance slip posted',
  'advance.voided': 'Advance slip voided',

  // Payments
  'payment.draft.created': 'Payment started',
  'payment.draft.updated': 'Payment draft edited',
  'payment.draft.deleted': 'Payment draft discarded',
  'payment.posted': 'Payment recorded',
  'payment.voided': 'Payment voided',

  // Settlements
  'settlement.draft.created': 'Settlement computed',
  'settlement.draft.updated': 'Settlement recomputed',
  'settlement.draft.deleted': 'Settlement draft discarded',
  'settlement.posted': 'Farmer settled',
  'settlement.voided': 'Settlement voided',

  // The system itself
  'setting.changed': 'Setting changed',
  'backup.created': 'Backup taken',
  'system.backup.written': 'Backup written',
  'system.update.applied': 'Update applied',
  'system.update.failed': 'Update failed',
  'system.update.rolled_back': 'Update rolled back',
  'ledger.cache.rebuilt': 'Ledger cache rebuilt',
}

/**
 * Whether an action is one somebody should notice while scrolling.
 *
 * Only the ones that mean something went wrong or somebody was refused. A log
 * where half the rows are highlighted is a log with no highlighting.
 */
export function isAlarming(action: string): boolean {
  return alarming.has(action)
}

const alarming = new Set([
  'auth.access.denied',
  'auth.login.failed',
  'auth.login.blocked',
  'setup.refused',
  'system.update.failed',
  'system.update.rolled_back',
  'ledger.cache.rebuilt',
  // A void is not a fault, but it is the reversal of money already recorded
  // and it is what somebody scans this page looking for.
  'advance.voided',
  'payment.voided',
  'settlement.voided',
])

/**
 * Pretty-print stored JSON for display, or hand back the raw text unchanged.
 *
 * A row this version of the code cannot parse is still a row somebody needs to
 * read — the whole point of storing it as text is that it survives the struct
 * definitions that produced it.
 */
export function prettyJSON(raw: string): string {
  try {
    return JSON.stringify(JSON.parse(raw), null, 2)
  } catch {
    return raw
  }
}

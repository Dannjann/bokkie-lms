import { getJson, putJson } from '@/api/client'

/**
 * One setting, with enough description to render an editor for it without a
 * hard-coded form per key: the kind says what it parses as, and a choice
 * setting carries the list it may hold.
 */
export interface Setting {
  key: string
  value: string
  kind: string
  default: string
  doc: string
  options?: string[]
}

export function listSettings(): Promise<{ settings: Setting[] }> {
  return getJson<{ settings: Setting[] }>('/api/settings')
}

/** Managers only. An unknown key is refused rather than stored. */
export function updateSetting(key: string, value: string): Promise<Setting> {
  return putJson<Setting>('/api/settings', { key, value })
}

/** The three that make up the interest rule, in the order a form should ask. */
export const interestKeys = ['interest.method', 'interest.rate_bps', 'interest.basis'] as const

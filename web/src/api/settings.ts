import { getJson, putJson } from '@/api/client'

/**
 * One setting, with enough description to render an editor for it without a
 * hard-coded form per key: the kind says what it parses as, and a choice
 * setting carries the list it may hold.
 */
export interface Setting {
  key: string
  value: string
  /** The sentence shown under the field. Prose — never branch on it. */
  kind: string
  /** The token to branch on: string, int, bool, time_of_day, timezone, choice. */
  kind_slug: SettingKind
  default: string
  doc: string
  options?: string[]
}

export type SettingKind = 'string' | 'int' | 'bool' | 'time_of_day' | 'timezone' | 'choice'

/**
 * The groups the settings screen draws, in order, and which keys go in each.
 *
 * Written out rather than derived from the key prefix so the headings read as
 * English and the order is a decision rather than an alphabetisation. A setting
 * missing from every group still renders — see the screen — so adding one to
 * the Go side never makes it invisible.
 */
export const settingGroups: { label: string; blurb?: string; keys: string[] }[] = [
  { label: 'Station', keys: ['timezone'] },
  {
    label: 'Interest',
    blurb: 'Nothing is charged until a method and a rate are set.',
    keys: ['interest.method', 'interest.rate_bps', 'interest.basis'],
  },
  { label: 'Payments', keys: ['payment.applies'] },
  {
    label: 'Numbering',
    blurb: 'Set these at cutover to continue the legacy sequences. Changing them later leaves a gap.',
    keys: ['refno.start', 'orno.start'],
  },
  { label: 'Network', keys: ['network.lan_access'] },
  { label: 'Backup', keys: ['backup.enabled', 'backup.schedule', 'backup.retention'] },
]

/**
 * Timezones offered in the picker, which is editable — the server accepts any
 * IANA name and this is a shortlist, not a restriction.
 *
 * Asia/Manila first because that is where the station is. The rest are here so
 * a machine set up elsewhere, or a mistyped value being corrected, does not
 * need somebody to remember the exact spelling of an IANA identifier.
 */
export const timezones = [
  'Asia/Manila',
  'Asia/Singapore',
  'Asia/Hong_Kong',
  'Asia/Tokyo',
  'Asia/Jakarta',
  'Asia/Bangkok',
  'UTC',
]

/** A label for a key, so the screen shows "Timezone" rather than "timezone". */
export const settingLabels: Record<string, string> = {
  timezone: 'Timezone',
  'interest.method': 'How interest is computed',
  'interest.rate_bps': 'Rate, in basis points',
  'interest.basis': 'Charged on',
  'payment.applies': 'A payment clears',
  'refno.start': 'First Ref. No.',
  'orno.start': 'First OR No.',
  'network.lan_access': 'Other machines may reach this app',
  'backup.enabled': 'Nightly backup',
  'backup.schedule': 'Backup runs at',
  'backup.retention': 'Backup files to keep',
}

/**
 * A choice value as a person would say it. Falls back to the stored token.
 *
 * Every option of every choice setting must appear here — checked by a Go test
 * that reads this file against settings.All, because a missing one renders as a
 * hyphenated identifier in a dropdown a manager is choosing an interest rule
 * from.
 */
export const settingChoiceLabels: Record<string, string> = {
  // interest.method
  'none': 'Not at all',
  'monthly-on-balance': 'Monthly, on the balance at the end of the period',
  'flat-on-advance': 'Once, on what was advanced during the period',
  // interest.basis
  'principal': 'Principal only',
  'principal-and-interest': 'Principal and unpaid interest',
  // payment.applies
  'interest-first': 'Interest first, then principal',
  'principal-first': 'Principal first, then interest',
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

/**
 * Whether the other machines on the station network may reach the app.
 *
 * Writable only from the host machine itself — the server refuses this one key
 * from anywhere else, because switching it off from a counter PC would take
 * away the app and the switch in the same click.
 */
export const lanAccessKey = 'network.lan_access'

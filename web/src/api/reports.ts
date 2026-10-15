import { getJson } from '@/api/client'

/**
 * Every figure on a report arrives as a decimal string, computed on the server.
 * Nothing in these screens adds anything up: a report is the one place a wrong
 * total would be printed, carried out of the room, and believed.
 */
export interface Who {
  customer_id: number
  code: string
  name: string
}

export interface Debtor extends Who {
  principal: string
  interest: string
  total: string
  last_activity: string
}

export interface CurrentAdvances {
  as_of: string
  rows: Debtor[]
  principal: string
  interest: string
  total: string
}

/** One settlement in the period: the legacy Return of Investment table. */
export interface ROIRow extends Who {
  settlement_id: number
  settlement_no: number
  cutoff: string
  /** What the settlement's payments returned of the money lent. */
  principal: string
  /** What they paid of the interest charged. */
  interest: string
  amount: string
}

export interface ROI {
  from: string
  to: string
  deployed: string
  interest_charged: string
  outstanding: string
  /** The totals of the rows: principal returned, interest paid, and both. */
  repaid: string
  interest_collected: string
  returned: string
  /** Farmers settled in the period. */
  farmers: number
  /** Advance slips posted in the period. */
  vouchers: number
  /** Repaid as a share of deployed, to one decimal place. Empty when nothing was lent. */
  repaid_share: string
  rows: ROIRow[]
}

export interface Buckets {
  current: string
  days_60: string
  days_90: string
  over_90: string
}

export interface AgedRow extends Who, Buckets {
  principal: string
  interest: string
}

export interface Aging extends Buckets {
  as_of: string
  rows: AgedRow[]
  principal: string
  interest: string
}

/** One settlement as the Settlements report lists it. */
export interface SettledRow extends Who {
  settlement_id: number
  settlement_no: number
  cutoff: string
  rate_bps: number
  interest_charged: string
  paid_principal: string
  paid_interest: string
  /** What was still owed afterwards. Negative when the farmer is in credit. */
  balance_after: string
}

export interface Settled {
  from: string
  to: string
  rows: SettledRow[]
}

function asOf(date?: string): string {
  return date ? `?as_of=${encodeURIComponent(date)}` : ''
}

function period(from?: string, to?: string): string {
  const params = new URLSearchParams()
  if (from) params.set('from', from)
  if (to) params.set('to', to)
  const qs = params.toString()
  return qs ? `?${qs}` : ''
}

export function getCurrentAdvances(date?: string): Promise<CurrentAdvances> {
  return getJson<CurrentAdvances>(`/api/reports/current-advances${asOf(date)}`)
}

export function getROI(from?: string, to?: string): Promise<ROI> {
  return getJson<ROI>(`/api/reports/roi${period(from, to)}`)
}

export function getAging(date?: string): Promise<Aging> {
  return getJson<Aging>(`/api/reports/aging${asOf(date)}`)
}

export function getSettled(from?: string, to?: string): Promise<Settled> {
  return getJson<Settled>(`/api/reports/settled${period(from, to)}`)
}

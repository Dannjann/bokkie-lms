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

export interface ROIRow extends Who {
  advanced: string
  repaid: string
  interest_charged: string
  balance: string
}

export interface ROI {
  from: string
  to: string
  deployed: string
  repaid: string
  interest_charged: string
  interest_collected: string
  outstanding: string
  farmers: number
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

export interface Settlement extends Who {
  settled_on: string
  advanced: string
}

export interface Liquidated {
  from: string
  to: string
  rows: Settlement[]
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

export function getLiquidated(from?: string, to?: string): Promise<Liquidated> {
  return getJson<Liquidated>(`/api/reports/liquidated${period(from, to)}`)
}

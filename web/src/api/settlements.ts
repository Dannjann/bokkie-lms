import { deleteJson, getJson, postJson, putJson } from '@/api/client'
import type { LedgerEntryView, PaymentMethod } from '@/api/payments'
import { formatPesos } from '@/format'

export type SettlementStatus = 'draft' | 'posted' | 'voided'

type Actor = { UserID: number; Username: string }

/**
 * What the farmer carried in from his last settlement: the principal and the
 * unpaid interest it left, and the interest that principal has earned since.
 * `since` is empty when there was no earlier settlement.
 */
export interface Carried {
  principal: string
  interest: string
  since: string
  days: number
  charge: string
}

/** One open advance, and the interest it has earned up to the cut-off. */
export interface SettlementAdvance {
  advance_line_id: number
  /** The advance slip the line is on, for linking to it. */
  voucher_id: number
  /** The Ref. No. of the advance slip the line is on. */
  ref_no: number
  date: string
  particular: string
  principal: string
  days: number
  charge: string
}

/** One recorded payment, and how the settlement divided it. */
export interface SettlementPayment {
  payment_id: number
  or_no: number
  date: string
  method: PaymentMethod
  amount: string
  principal: string
  interest: string
}

/** Added up by the server. No peso figure in this system is totalled here. */
export interface SettlementTotals {
  advanced: string
  advance_interest: string
  interest_charged: string
  principal: string
  interest: string
  owed: string
  paid: string
  paid_principal: string
  paid_interest: string
  principal_after: string
  interest_after: string
  /** Negative when the delivery was worth more than the debt. */
  balance_after: string
}

/** One farmer, settled up to a cut-off date — the legacy "Compute Percentage". */
export interface Settlement {
  id: number
  /** Zero until the settlement is posted: the number is allocated then. */
  settlement_no: number
  customer_id: number
  customer_code: string
  customer_name: string
  cutoff: string
  /** Monthly rate in basis points: 500 is 5% a month. */
  rate_bps: number
  notes: string
  status: SettlementStatus
  carried: Carried
  advances: SettlementAdvance[]
  payments: SettlementPayment[]
  totals: SettlementTotals
  created_at: string
  created_by: Actor
  posted_at: string | null
  posted_by: Actor
  voided_at: string | null
  voided_by: Actor
  void_reason: string
}

/** A settlement, and what it wrote — including the reversals a void added. */
export interface SettlementDetail {
  settlement: Settlement
  entries: LedgerEntryView[]
}

export interface SettlementSummary {
  id: number
  settlement_no: number
  customer_id: number
  customer_code: string
  customer_name: string
  cutoff: string
  rate_bps: number
  interest: string
  paid: string
  status: SettlementStatus
  created_by: string
}

export interface SettlementPage {
  settlements: SettlementSummary[]
  total: number
  page: number
  per_page: number
}

/**
 * What a caller is allowed to decide: a farmer, a cut-off, a rate and a note.
 * A null rate takes the station's usual one; 0 is a real rate (waived).
 */
export interface SettlementDraft {
  customer_id: number
  cutoff: string
  rate_bps: number | null
  notes: string
}

export interface SettlementQuery {
  q?: string
  customerId?: number
  status?: SettlementStatus
  from?: string
  to?: string
  page?: number
  perPage?: number
}

export function listSettlements(query: SettlementQuery = {}): Promise<SettlementPage> {
  const params = new URLSearchParams()
  if (query.q) params.set('q', query.q)
  if (query.customerId) params.set('customer_id', String(query.customerId))
  if (query.status) params.set('status', query.status)
  if (query.from) params.set('from', query.from)
  if (query.to) params.set('to', query.to)
  if (query.page) params.set('page', String(query.page))
  if (query.perPage) params.set('per_page', String(query.perPage))

  const qs = params.toString()
  return getJson<SettlementPage>(qs ? `/api/settlements?${qs}` : '/api/settlements')
}

export function getSettlement(id: number): Promise<SettlementDetail> {
  return getJson<SettlementDetail>(`/api/settlements/${id}`)
}

/**
 * Compute a settlement and save it as a draft.
 *
 * This is the preview: the draft is the settlement minus the commit, and
 * posting writes the same figures — or refuses if they have moved since.
 */
export function createSettlement(draft: SettlementDraft): Promise<Settlement> {
  return postJson<Settlement>('/api/settlements', draft)
}

/** Recompute a draft — with a new cut-off or rate, or after the record moved. */
export function recomputeSettlement(id: number, draft: SettlementDraft): Promise<Settlement> {
  return putJson<Settlement>(`/api/settlements/${id}`, draft)
}

/** Drafts only. A posted settlement is voided, never deleted. */
export function deleteSettlement(id: number): Promise<void> {
  return deleteJson<void>(`/api/settlements/${id}`)
}

export function postSettlement(id: number): Promise<Settlement> {
  return postJson<Settlement>(`/api/settlements/${id}/post`)
}

/** Managers only, and the reason is not optional. */
export function voidSettlement(id: number, reason: string): Promise<Settlement> {
  return postJson<Settlement>(`/api/settlements/${id}/void`, { reason })
}

export function settlementStatusLabel(status: SettlementStatus): string {
  switch (status) {
    case 'draft':
      return 'Draft'
    case 'posted':
      return 'Liquidated'
    case 'voided':
      return 'Voided'
    default:
      return status
  }
}

/**
 * A balance after settlement as somebody would say it. Negative means the
 * delivery was worth more than the debt: the farmer is in credit.
 */
export function inCredit(amount: string): boolean {
  return amount.startsWith('-')
}

/** "₱1,000.00", or "₱1,000.00 in credit" for a negative balance. */
export function balanceLabel(amount: string): string {
  return inCredit(amount) ? `${formatPesos(amount.slice(1))} in credit` : formatPesos(amount)
}

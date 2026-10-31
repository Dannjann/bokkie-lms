import { deleteJson, getJson, postJson, putJson } from '@/api/client'

export type AdvanceStatus = 'draft' | 'posted' | 'voided'

/**
 * Money always arrives as a decimal string, never a JSON number, so that
 * JavaScript's float arithmetic can never touch a peso. Amounts typed at the
 * counter go back the same way: the server parses "1,200.50" and decides what
 * it means, which keeps one parser in the system rather than two that can
 * disagree.
 */
export interface AdvanceLine {
  id: number
  customer_id: number
  customer_code: string
  customer_name: string
  date: string
  particular: string
  amount: string
}

export interface Voucher {
  id: number
  /** Zero until the voucher is posted: the number is allocated at posting. */
  ref_no: number
  date: string
  notes: string
  status: AdvanceStatus
  lines: AdvanceLine[]
  total: string
  created_at: string
  created_by: { UserID: number; Username: string }
  posted_at: string | null
  posted_by: { UserID: number; Username: string }
  voided_at: string | null
  voided_by: { UserID: number; Username: string }
  void_reason: string
}

/** One entry the voucher put in the ledger, including the reversals a void wrote. */
export interface LedgerEntryView {
  id: number
  customer_id: number
  date: string
  type: string
  principal: string
  interest: string
  reason: string
  reverses_id: number
  by: string
}

export interface VoucherDetail {
  voucher: Voucher
  entries: LedgerEntryView[]
}

export interface VoucherSummary {
  id: number
  ref_no: number
  date: string
  status: AdvanceStatus
  total: string
  line_count: number
  customers: string[]
  created_by: string
}

export interface VoucherPage {
  vouchers: VoucherSummary[]
  total: number
  page: number
  per_page: number
}

/**
 * What a caller is allowed to decide.
 *
 * No ref_no and no status, on both sides of the wire: the server assigns the
 * number when the voucher is posted and refuses a request carrying one.
 */
export interface VoucherDraft {
  date: string
  notes: string
  lines: VoucherDraftLine[]
}

export interface VoucherDraftLine {
  customer_id: number
  /** Empty means the voucher's own date, which is what the grid fills in. */
  date?: string
  particular: string
  amount: string
}

export interface VoucherQuery {
  q?: string
  status?: AdvanceStatus
  from?: string
  to?: string
  page?: number
  perPage?: number
}

export function listAdvances(query: VoucherQuery = {}): Promise<VoucherPage> {
  const params = new URLSearchParams()
  if (query.q) params.set('q', query.q)
  if (query.status) params.set('status', query.status)
  if (query.from) params.set('from', query.from)
  if (query.to) params.set('to', query.to)
  if (query.page) params.set('page', String(query.page))
  if (query.perPage) params.set('per_page', String(query.perPage))

  const qs = params.toString()
  return getJson<VoucherPage>(qs ? `/api/advances?${qs}` : '/api/advances')
}

export function getAdvance(id: number): Promise<VoucherDetail> {
  return getJson<VoucherDetail>(`/api/advances/${id}`)
}

export function createAdvance(draft: VoucherDraft): Promise<Voucher> {
  return postJson<Voucher>('/api/advances', draft)
}

/** Enters a slip and posts it in one call, so a failed post leaves no draft. */
export function createAndPostAdvance(draft: VoucherDraft): Promise<Voucher> {
  return postJson<Voucher>('/api/advances/posted', draft)
}

export function updateAdvance(id: number, draft: VoucherDraft): Promise<Voucher> {
  return putJson<Voucher>(`/api/advances/${id}`, draft)
}

/** Drafts only. A posted voucher is voided, never deleted. */
export function deleteAdvance(id: number): Promise<void> {
  return deleteJson<void>(`/api/advances/${id}`)
}

export function postAdvance(id: number): Promise<Voucher> {
  return postJson<Voucher>(`/api/advances/${id}/post`)
}

/** Managers only, and the reason is not optional. */
export function voidAdvance(id: number, reason: string): Promise<Voucher> {
  return postJson<Voucher>(`/api/advances/${id}/void`, { reason })
}

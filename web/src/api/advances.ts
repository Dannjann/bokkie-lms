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
  /**
   * 1 as first posted; each edit adds one under the same Ref. No. Above 1,
   * created_by and created_at are who made the latest edit and when.
   */
  revision: number
  /** The slip that replaced this one when it was edited, or 0. */
  superseded_by: number
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

/**
 * Edits a posted slip. The server voids it and posts the edited lines under
 * the same Ref. No., so the answer is a different voucher — the new revision.
 */
export function editAdvance(id: number, draft: VoucherDraft): Promise<Voucher> {
  return postJson<Voucher>(`/api/advances/${id}/edit`, draft)
}

/** What can be changed about one line in place. */
export interface LineChange {
  date: string
  particular: string
  amount: string
}

/**
 * Edits one line of a posted slip, leaving the rest as it is: the slip is read
 * fresh, that line replaced, and the whole of it sent as the edit.
 */
export async function editAdvanceLine(voucherId: number, lineId: number, change: LineChange): Promise<Voucher> {
  const { voucher } = await getAdvance(voucherId)
  if (!voucher.lines.some((l) => l.id === lineId)) {
    throw new Error('That line has changed since this page was opened. Reload and try again.')
  }
  return editAdvance(voucherId, {
    date: voucher.date,
    notes: voucher.notes,
    lines: voucher.lines.map((l) =>
      l.id === lineId
        ? { customer_id: l.customer_id, ...change }
        : { customer_id: l.customer_id, date: l.date, particular: l.particular, amount: l.amount },
    ),
  })
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

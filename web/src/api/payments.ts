import { deleteJson, getJson, postJson, putJson } from '@/api/client'
import type { Balance, Customer } from '@/api/customers'

export type PaymentStatus = 'draft' | 'posted' | 'voided'
export type PaymentMethod = 'cash' | 'palay' | 'other'

/**
 * Money always arrives as a decimal string, never a JSON number, so that
 * JavaScript's float arithmetic can never touch a peso. Amounts typed at the
 * counter go back the same way: the server parses "1,200.50" and decides what
 * it means, which keeps one parser in the system rather than two that can
 * disagree.
 */
export interface Payment {
  id: number
  /** Zero until the payment is posted: the number is allocated at posting. */
  or_no: number
  customer_id: number
  customer_code: string
  customer_name: string
  date: string
  amount: string
  method: PaymentMethod
  /** The lots of palay, for a palay payment; empty otherwise. */
  lines: PaymentLine[]
  particular: string
  status: PaymentStatus
  /** The settlement that applied this payment; 0 while it is still waiting. */
  settlement_id: number
  settlement_no: number
  created_at: string
  created_by: { UserID: number; Username: string }
  posted_at: string | null
  posted_by: { UserID: number; Username: string }
  voided_at: string | null
  voided_by: { UserID: number; Username: string }
  void_reason: string
}

/**
 * One lot of palay on a payment. Kilos travel as a string, as money does —
 * "500.0" — so the browser never holds a weight as a float either.
 */
export interface PaymentLine {
  id: number
  class: string
  sacks: number
  kilos: string
  /** Per kilo. */
  price: string
  /** kilos × price, rounded half-up, worked out by the server. */
  cost: string
}

/** A lot as typed at the counter: the server works out the cost. */
export interface PaymentLineDraft {
  class: string
  sacks: number
  kilos: string
  price: string
}

/** One entry the payment put in the ledger, including the reversal a void wrote. */
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

/**
 * A payment, what it did to the ledger, and what the customer owes now.
 *
 * The entries are where the split between principal and interest is read from.
 * The payment itself does not carry it — the server stores that division on the
 * ledger entry and nowhere else, so there is no second copy to disagree.
 */
export interface PaymentDetail {
  payment: Payment
  entries: LedgerEntryView[]
  balance: Balance
}

export interface PaymentSummary {
  id: number
  or_no: number
  customer_id: number
  customer_code: string
  customer_name: string
  date: string
  amount: string
  method: PaymentMethod
  particular: string
  status: PaymentStatus
  /** Both zero while the payment waits to be settled. */
  settlement_id: number
  settlement_no: number
  created_by: string
}

export interface PaymentPage {
  payments: PaymentSummary[]
  total: number
  page: number
  per_page: number
}

/**
 * What a caller is allowed to decide.
 *
 * No or_no and no status, on both sides of the wire: the server assigns the
 * number when the payment is posted and refuses a request carrying one.
 */
export interface PaymentDraft {
  customer_id: number
  /** Empty means today, which is what a receipt written at the counter is. */
  date: string
  /** Ignored for a palay payment: the server adds up the lines instead. */
  amount: string
  method: PaymentMethod
  /** Palay only, at least one; empty for cash and other. */
  lines: PaymentLineDraft[]
  particular: string
}

export interface PaymentQuery {
  q?: string
  customerId?: number
  status?: PaymentStatus
  from?: string
  to?: string
  page?: number
  perPage?: number
}

export function listPayments(query: PaymentQuery = {}): Promise<PaymentPage> {
  const params = new URLSearchParams()
  if (query.q) params.set('q', query.q)
  if (query.customerId) params.set('customer_id', String(query.customerId))
  if (query.status) params.set('status', query.status)
  if (query.from) params.set('from', query.from)
  if (query.to) params.set('to', query.to)
  if (query.page) params.set('page', String(query.page))
  if (query.perPage) params.set('per_page', String(query.perPage))

  const qs = params.toString()
  return getJson<PaymentPage>(qs ? `/api/payments?${qs}` : '/api/payments')
}

export function getPayment(id: number): Promise<PaymentDetail> {
  return getJson<PaymentDetail>(`/api/payments/${id}`)
}

export function createPayment(draft: PaymentDraft): Promise<Payment> {
  return postJson<Payment>('/api/payments', draft)
}

/** Takes a payment and records it in one call, so a failed one leaves no draft. */
export function createAndPostPayment(draft: PaymentDraft): Promise<Payment> {
  return postJson<Payment>('/api/payments/posted', draft)
}

export function updatePayment(id: number, draft: PaymentDraft): Promise<Payment> {
  return putJson<Payment>(`/api/payments/${id}`, draft)
}

/** Drafts only. A recorded payment is voided, never deleted. */
export function deletePayment(id: number): Promise<void> {
  return deleteJson<void>(`/api/payments/${id}`)
}

/**
 * Record a payment: it gets its OR No. but moves no balance. A settlement is
 * what applies it.
 */
export function postPayment(id: number): Promise<Payment> {
  return postJson<Payment>(`/api/payments/${id}/post`)
}

/** Managers only, and the reason is not optional. */
export function voidPayment(id: number, reason: string): Promise<Payment> {
  return postJson<Payment>(`/api/payments/${id}/void`, { reason })
}

/** How the money arrived, in the order the form should offer them. */
export const paymentMethods: { label: string; value: PaymentMethod }[] = [
  { label: 'Cash', value: 'cash' },
  { label: 'Palay', value: 'palay' },
  { label: 'Other', value: 'other' },
]

export function methodLabel(method: PaymentMethod): string {
  return paymentMethods.find((m) => m.value === method)?.label ?? method
}

/**
 * A payment's status in the station's words. "posted" is "Recorded" until a
 * settlement applies it, and "Settled" after.
 */
export function paymentStatusLabel(status: PaymentStatus, settlementNo: number): string {
  switch (status) {
    case 'draft':
      return 'Draft'
    case 'voided':
      return 'Voided'
    case 'posted':
      return settlementNo > 0 ? 'Liquidated' : 'Recorded'
    default:
      return status
  }
}

/** The palay classes the station grades into. Free text: anything else goes. */
export const palayClasses = ['SLG', 'YCS', 'DOLG', 'YCDG', 'SC10']

/**
 * What a ledger entry is called on screen, in the words the station uses.
 *
 * It lives here rather than on the statement screen because the customer record
 * names the same entries: two copies of this list is two chances for a payment
 * to be called one thing on one screen and another on the next.
 */
export function entryLabel(type: string): string {
  switch (type) {
    case 'advance':
      return 'Advance'
    case 'payment':
      return 'Payment'
    case 'interest':
      return 'Interest'
    case 'reversal':
      return 'Reversal'
    case 'opening':
      return 'Opening balance'
    case 'adjustment':
      return 'Adjustment'
    default:
      return type
  }
}

/**
 * One row of a statement: an entry, the balance it left behind, and the number
 * staff say out loud for the document it came from.
 */
export interface StatementRow {
  entry: LedgerEntryView
  running: Balance
  /** "Ref. No. 1425", "OR No. 7", or absent for an entry with no document. */
  document?: string
}

export interface Statement {
  customer: Customer
  from?: string
  to: string
  opening: Balance
  rows: StatementRow[]
  closing: Balance
  moved: { principal: string; interest: string }
}

/**
 * A customer's statement.
 *
 * Every figure on it — the opening balance, each running balance, the close and
 * the totals — is computed by the server. No peso amount in this system is
 * added up in a browser, and a running balance is the most tempting place in
 * the whole application to break that rule.
 */
export function getStatement(id: number, from?: string, to?: string): Promise<Statement> {
  const params = new URLSearchParams()
  if (from) params.set('from', from)
  if (to) params.set('to', to)

  const qs = params.toString()
  return getJson<Statement>(`/api/customers/${id}/statement${qs ? `?${qs}` : ''}`)
}

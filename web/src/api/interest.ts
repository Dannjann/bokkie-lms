import { deleteJson, getJson, postJson, putJson } from '@/api/client'
import type { LedgerEntryView } from '@/api/payments'

export type RunStatus = 'draft' | 'posted' | 'voided'
export type InterestMethod = 'none' | 'monthly-on-balance' | 'flat-on-advance'
export type InterestBasis = 'principal' | 'principal-and-interest'

/**
 * The rule a run was computed under, copied onto the run itself.
 *
 * It is not a reference to the settings. A run opened in two years has to be
 * able to say what rate produced it, and by then the setting will have moved —
 * which also means changing the rate does not change a draft that already
 * exists. Recomputing it is what picks a new rate up, and the screen says so.
 */
export interface Rule {
  method: InterestMethod
  /** Basis points: 300 is 3%. Never a decimal, on either side of the wire. */
  rate_bps: number
  basis: InterestBasis
}

/** One farmer's charge, and what it was computed from. */
export interface RunLine {
  id: number
  customer_id: number
  customer_code: string
  customer_name: string
  base: string
  charge: string
}

export interface Run {
  id: number
  /** Zero until the run is posted: the number is allocated at posting. */
  run_no: number
  period_from: string
  period_to: string
  rule: Rule
  notes: string
  status: RunStatus
  lines: RunLine[]
  /** Added up by the server. No peso figure in this system is totalled here. */
  total: string
  created_at: string
  created_by: { UserID: number; Username: string }
  posted_at: string | null
  posted_by: { UserID: number; Username: string }
  voided_at: string | null
  voided_by: { UserID: number; Username: string }
  void_reason: string
}

/** A run, and what it actually wrote — including the reversals a void added. */
export interface RunDetail {
  run: Run
  entries: LedgerEntryView[]
}

export interface RunSummary {
  id: number
  run_no: number
  period_from: string
  period_to: string
  rule: Rule
  status: RunStatus
  /** How many farmers the run touched, and how much it charged them. */
  customers: number
  total: string
  created_by: string
}

export interface RunPage {
  runs: RunSummary[]
  total: number
  page: number
  per_page: number
}

/**
 * What a caller is allowed to decide: a period, and a note about why.
 *
 * No rule, no lines, no number and no status. The rule comes from the settings
 * and the lines are computed from the ledger — a caller that could supply
 * either could charge an arbitrary farmer an arbitrary amount, and the server
 * refuses a body carrying any of it.
 */
export interface RunDraft {
  period_from: string
  period_to: string
  notes: string
}

export interface RunQuery {
  q?: string
  status?: RunStatus
  from?: string
  to?: string
  page?: number
  perPage?: number
}

export function listRuns(query: RunQuery = {}): Promise<RunPage> {
  const params = new URLSearchParams()
  if (query.q) params.set('q', query.q)
  if (query.status) params.set('status', query.status)
  if (query.from) params.set('from', query.from)
  if (query.to) params.set('to', query.to)
  if (query.page) params.set('page', String(query.page))
  if (query.perPage) params.set('per_page', String(query.perPage))

  const qs = params.toString()
  return getJson<RunPage>(qs ? `/api/interest-runs?${qs}` : '/api/interest-runs')
}

export function getRun(id: number): Promise<RunDetail> {
  return getJson<RunDetail>(`/api/interest-runs/${id}`)
}

/**
 * Compute a run and save it as a draft.
 *
 * This is the preview, and there is no separate call for one: the draft is the
 * run minus the commit. What the screen shows is the rows the server stored,
 * and posting writes those same rows.
 */
export function createRun(draft: RunDraft): Promise<Run> {
  return postJson<Run>('/api/interest-runs', draft)
}

/** Recompute a draft — against a new period, or after the ledger has moved. */
export function recomputeRun(id: number, draft: RunDraft): Promise<Run> {
  return putJson<Run>(`/api/interest-runs/${id}`, draft)
}

/** Drafts only. A posted run is voided, never deleted. */
export function deleteRun(id: number): Promise<void> {
  return deleteJson<void>(`/api/interest-runs/${id}`)
}

export function postRun(id: number): Promise<Run> {
  return postJson<Run>(`/api/interest-runs/${id}/post`)
}

/** The reason is not optional, and it is copied onto every reversal. */
export function voidRun(id: number, reason: string): Promise<Run> {
  return postJson<Run>(`/api/interest-runs/${id}/void`, { reason })
}

export const interestMethods: { label: string; value: InterestMethod; help: string }[] = [
  { label: 'Not charged', value: 'none', help: 'No interest is computed at all.' },
  {
    label: 'On the balance',
    value: 'monthly-on-balance',
    help: 'The rate against what each farmer owes at the end of the period.',
  },
  {
    label: 'On what was advanced',
    value: 'flat-on-advance',
    help: 'The rate, once, against what each farmer was lent during the period.',
  },
]

export const interestBases: { label: string; value: InterestBasis; help: string }[] = [
  { label: 'Principal only', value: 'principal', help: 'Simple interest.' },
  {
    label: 'Principal and unpaid interest',
    value: 'principal-and-interest',
    help: 'Interest is charged on unpaid interest as well — compounding.',
  },
]

export function methodLabel(method: InterestMethod): string {
  return interestMethods.find((m) => m.value === method)?.label ?? method
}

export function basisLabel(basis: InterestBasis): string {
  return interestBases.find((b) => b.value === basis)?.label ?? basis
}

/**
 * A rate as a percentage, by string surgery rather than by arithmetic.
 *
 * Basis points are an integer, so this is exact: 300 reads "3%", 25 reads
 * "0.25%", 1050 reads "10.5%". The trailing zeroes come off because nobody
 * says "three point zero zero percent".
 */
export function ratePercent(bps: number): string {
  const sign = bps < 0 ? '-' : ''
  const digits = String(Math.abs(Math.trunc(bps))).padStart(3, '0')
  const whole = digits.slice(0, -2)
  const fraction = digits.slice(-2).replace(/0+$/, '')
  return `${sign}${whole}${fraction ? `.${fraction}` : ''}%`
}

/** "1 Sep 2025 – 30 Sep 2025", which is how a period is read aloud. */
export function periodLabel(from: string, to: string): string {
  return from === to ? from : `${from} – ${to}`
}

import { getJson } from '@/api/client'
import type { Customer } from '@/api/customers'

/**
 * The statements index: a way of finding the one farmer's statement somebody
 * wants, with what they owe beside their name so the answer to "how much?" does
 * not need a second click.
 *
 * Readable by anyone signed in, unlike the four reports. The person who has to
 * tell a farmer what they owe is the encoder at the counter, and any single
 * farmer's balance is already one click away on the customer screen — this
 * saves the clicks rather than opening anything that was closed.
 */

export interface StatementBalance {
  principal: string
  interest: string
  total: string
  last_activity?: string
}

export interface StatementIndexRow {
  customer: Customer
  balance: StatementBalance
}

export interface StatementIndex {
  /** The day the balances are as at, echoed back by the server. */
  as_of: string
  rows: StatementIndexRow[]
  total: number
  page: number
  per_page: number
}

export function listStatements(params: {
  asOf?: string
  q?: string
  status?: string
  page?: number
  perPage?: number
}): Promise<StatementIndex> {
  const query = new URLSearchParams()
  if (params.asOf) query.set('as_of', params.asOf)
  if (params.q) query.set('q', params.q)
  if (params.status) query.set('status', params.status)
  if (params.page) query.set('page', String(params.page))
  if (params.perPage) query.set('per_page', String(params.perPage))

  const qs = query.toString()
  return getJson<StatementIndex>(`/api/statements${qs ? `?${qs}` : ''}`)
}

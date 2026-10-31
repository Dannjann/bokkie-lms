import { getJson, postJson, putJson } from '@/api/client'

export interface Customer {
  id: number
  code: string
  name: string
  address: string
  cell_no: string
  notes: string
  /** 0 for a customer with no barangay set — one from before barangays. */
  barangay_id: number
  barangay_name: string
  active: boolean
  created_at: string
  updated_at: string
}

/**
 * What a caller is allowed to decide.
 *
 * There is no code field, and that is deliberate on both sides of the wire: the
 * server assigns codes and refuses a request carrying one, because the code is
 * the number written on the farmer's paper slip.
 */
export interface CustomerDraft {
  name: string
  address: string
  cell_no: string
  notes: string
  /** Decides a new customer's code. Changing it later does not change the code. */
  barangay_id: number
}

/**
 * Money always arrives as a decimal string, never a JSON number, so that
 * JavaScript's float arithmetic can never touch a peso. Format it, do not
 * compute with it.
 */
export interface Balance {
  principal: string
  interest: string
  total: string
  last_activity?: string
}

export interface CustomerDetail {
  customer: Customer
  balance: Balance
}

export interface CustomerPage {
  customers: Customer[]
  total: number
  page: number
  per_page: number
}

export type CustomerStatus = 'active' | 'inactive' | 'all'

export interface CustomerQuery {
  q?: string
  status?: CustomerStatus
  page?: number
  perPage?: number
}

export function listCustomers(query: CustomerQuery = {}): Promise<CustomerPage> {
  const params = new URLSearchParams()
  if (query.q) params.set('q', query.q)
  if (query.status) params.set('status', query.status)
  if (query.page) params.set('page', String(query.page))
  if (query.perPage) params.set('per_page', String(query.perPage))

  const qs = params.toString()
  return getJson<CustomerPage>(qs ? `/api/customers?${qs}` : '/api/customers')
}

export function getCustomer(id: number): Promise<CustomerDetail> {
  return getJson<CustomerDetail>(`/api/customers/${id}`)
}

export function createCustomer(draft: CustomerDraft): Promise<Customer> {
  return postJson<Customer>('/api/customers', draft)
}

export function updateCustomer(id: number, draft: CustomerDraft): Promise<Customer> {
  return putJson<Customer>(`/api/customers/${id}`, draft)
}

/** Deactivate or reactivate. Nothing is ever deleted. */
export function setCustomerActive(id: number, active: boolean): Promise<Customer> {
  return postJson<Customer>(`/api/customers/${id}/active`, { active })
}

export interface ImportRejection {
  line: number
  code?: string
  reason: string
}

export interface ImportReport {
  file_name?: string
  sha256: string
  accepted: number
  rejections: ImportRejection[]
  first_code?: string
  last_code?: string
  applied: boolean
}

/**
 * Send a CSV to the import endpoint.
 *
 * With dryRun the server parses, validates and reports, and writes nothing —
 * the same code path as a real import, minus the commit, which is what makes
 * the preview worth trusting.
 *
 * This is the one endpoint that is not JSON in, so it does not go through the
 * shared client: it needs a multipart body, and it treats a 422 as a result to
 * render rather than an error to throw, because a report of what is wrong with
 * the file is exactly what the person uploading it asked for.
 */
export async function importCustomers(file: File, dryRun: boolean): Promise<ImportReport> {
  const body = new FormData()
  body.append('file', file)

  const response = await fetch(`/api/customers/import?dry_run=${dryRun}`, {
    method: 'POST',
    headers: { Accept: 'application/json' },
    body,
  })

  const payload = await response.json().catch(() => null)

  if (response.ok || response.status === 422) {
    return payload as ImportReport
  }
  throw new Error((payload as { error?: string })?.error ?? response.statusText)
}

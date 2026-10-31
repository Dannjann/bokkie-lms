import { getJson, postJson, putJson } from './client'

/** A place farmers come from, and the prefix their customer codes carry. */
export interface Barangay {
  id: number
  name: string
  /** Two to four capitals: PI → PI-0001. Fixed once a customer holds a code under it. */
  prefix: string
  active: boolean
  /** How many customers point at this barangay. */
  customers: number
  created_at: string
  updated_at: string
}

export interface BarangayDraft {
  name: string
  prefix: string
  active: boolean
}

export function listBarangays(): Promise<Barangay[]> {
  return getJson<Barangay[]>('/api/barangays')
}

export function createBarangay(draft: BarangayDraft): Promise<Barangay> {
  return postJson<Barangay>('/api/barangays', draft)
}

export function updateBarangay(id: number, draft: BarangayDraft): Promise<Barangay> {
  return putJson<Barangay>(`/api/barangays/${id}`, draft)
}

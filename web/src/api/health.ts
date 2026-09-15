import { getJson } from '@/api/client'

export interface Health {
  status: string
  version: string
  commit: string
  uptime_seconds: number
  database: string
}

export function fetchHealth(): Promise<Health> {
  return getJson<Health>('/healthz')
}

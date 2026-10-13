/**
 * The station's own control surface.
 *
 * These endpoints are host-only: the Go server refuses them from any machine
 * but the one serving, whatever the LAN access setting says. A counter PC that
 * loaded this screen would be refused, which is the intended behaviour — the
 * question "may that machine switch the station off" is not the one the network
 * setting answers.
 */
import { getJson, postJson } from '@/api/client'

export interface Status {
  serving: boolean
  /** This machine's own address, which always works. */
  address: string
  /** The name the counter PCs use — the one that can be wrong. */
  lanAddress: string
  version: string
  /** RFC3339, or null when there has never been a backup. */
  lastBackup: string | null
  lastBackupFile: string
  /** The LAN name without ".local". */
  name: string
}

/** CheckState mirrors internal/diagnose.State, which marshals as these words. */
export type CheckState = 'ok' | 'note' | 'problem'

export interface Check {
  name: string
  state: CheckState
  detail?: string
  /** What to do about it. Present whenever state is 'problem'. */
  fix?: string
}

export interface CheckReport {
  checks: Check[]
  /** The address that was proved to work. */
  reach?: string
}

export function fetchStatus(): Promise<Status> {
  return getJson<Status>('/api/control/status')
}

export function runCheck(): Promise<CheckReport> {
  return getJson<CheckReport>('/api/control/check')
}

export interface StopAck {
  stopping: string
}

export function stopStation(): Promise<StopAck> {
  return postJson<StopAck>('/api/control/stop')
}

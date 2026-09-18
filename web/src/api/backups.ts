import { getJson, postJson } from '@/api/client'

/**
 * The backup folder.
 *
 * Nothing here says whether a file is *valid*. Every backup is verified as it
 * is written, but that was then — a USB stick can be pulled mid-copy — and a
 * list claiming "verified" beside a file it has not opened would be asserting
 * something it does not know. Checking one is `bokkie backup -verify`, which
 * reads the file.
 */

export interface BackupFile {
  name: string
  path: string
  bytes?: number
  /**
   * When the backup was written, in station time, read by the server out of
   * the file's own name.
   *
   * Not its modification time: a file copied off a USB stick and back has a
   * modification time recording the copy. And not the raw name either — the
   * name is UTC so it sorts in a directory listing, which is eight hours from
   * the wall clock here and would have the list disagreeing with itself.
   */
  taken_at?: string
}

export function listBackups(): Promise<{ folder: string; backups: BackupFile[] }> {
  return getJson('/api/backups')
}

export interface BackupResult {
  path: string
  bytes: number
  took_ms: number
  row_counts: Record<string, number>
}

/**
 * Run one now. Answers 503 when the destination is unavailable — which is
 * almost always the USB stick not being plugged in, and the message names the
 * folder for that reason.
 */
export function backupNow(): Promise<BackupResult> {
  return postJson<BackupResult>('/api/backups')
}

/** "4.2 MB". Decimal units, because that is what a file manager shows. */
export function formatBytes(bytes: number | undefined): string {
  if (bytes === undefined) return '—'
  if (bytes === 0) return '0 bytes'
  const units = ['bytes', 'kB', 'MB', 'GB']
  const power = Math.min(Math.floor(Math.log10(bytes) / 3), units.length - 1)
  const value = bytes / 1000 ** power
  return `${power === 0 ? value : value.toFixed(1)} ${units[power]}`
}

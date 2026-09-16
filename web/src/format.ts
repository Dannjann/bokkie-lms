/**
 * Display formatting. Nothing here changes what is stored or sent — the server
 * normalises phone numbers to bare digits on the way in, and that is what stays
 * in the database and in the audit log.
 */

/**
 * Space a Philippine mobile number into the groups people read it in.
 *
 * `09171112222` is eleven digits a clerk has to count with a finger before
 * reading it down a phone line; `0917 111 2222` is three glances. Anything that
 * is not an eleven-digit number starting `09` is left exactly as it came —
 * landlines, foreign numbers and half-entered records are none of our business.
 */
export function formatPhone(raw: string): string {
  if (!/^09\d{9}$/.test(raw)) return raw
  return `${raw.slice(0, 4)} ${raw.slice(4, 7)} ${raw.slice(7)}`
}

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

/**
 * Add up what has been typed into a voucher, in centavos.
 *
 * This is the one place the browser does arithmetic on money, and it does it in
 * whole centavos parsed out of the text — never in floats, and never on a value
 * that came from the server. It exists because somebody entering twelve lines
 * off a stack of slips needs to check the total against the paper before
 * saving, and the server's total does not exist until they do.
 *
 * Anything unparseable makes the whole total unknown rather than wrong: a
 * half-typed amount must not quietly count as nothing.
 */
export function sumCentavos(amounts: string[]): number | null {
  let total = 0
  for (const amount of amounts) {
    const centavos = parseCentavos(amount)
    if (centavos === null) return null
    total += centavos
  }
  return total
}

/**
 * Read a typed peso amount as whole centavos.
 *
 * Tolerant in the same way the server's parser is — commas, spaces and a peso
 * sign are all noise — because the two have to agree about what somebody typed.
 * At most two decimal places: a third would be a centavo we cannot store, and
 * rounding it silently is how a total stops matching a slip.
 */
export function parseCentavos(amount: string): number | null {
  const text = amount.replace(/[₱,\s]/g, '')
  if (!/^\d+(\.\d{1,2})?$/.test(text)) return null

  const [pesos, fraction = ''] = text.split('.')
  return Number(pesos) * 100 + Number(fraction.padEnd(2, '0'))
}

/** Centavos back to the decimal string everything else in the system speaks. */
export function formatCentavos(centavos: number): string {
  const pesos = Math.trunc(centavos / 100)
  const fraction = Math.abs(centavos % 100)
  return `${group(String(pesos))}.${String(fraction).padStart(2, '0')}`
}

/**
 * A peso amount from the server, as a screen should show it.
 *
 * Purely textual: the string is split, grouped and re-joined, and no part of it
 * is ever turned into a number. The server sends "1200.50" and this makes it
 * "₱1,200.50" without the value passing through a float on the way.
 *
 * A negative amount — which is what a reversal is — reads "−₱500.00" and not
 * "₱-500.00". The sign belongs to the amount, not to the currency.
 */
export function formatPesos(amount: string): string {
  const negative = amount.startsWith('-')
  const [pesos, fraction = '00'] = (negative ? amount.slice(1) : amount).split('.')
  return `${negative ? '−' : ''}₱${group(pesos)}.${fraction}`
}

/** Thousands separators, by string surgery rather than by arithmetic. */
function group(digits: string): string {
  return digits.replace(/\B(?=(\d{3})+(?!\d))/g, ',')
}

/**
 * A date as the API writes it: YYYY-MM-DD in the station's own calendar.
 *
 * Built from the local getters rather than toISOString, which converts to UTC
 * first and would hand back yesterday for anything typed before 8am in Manila.
 */
export function toISODate(date: Date): string {
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${date.getFullYear()}-${month}-${day}`
}

/** The inverse, for handing a stored date back to a date picker. */
export function fromISODate(text: string): Date | null {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(text)
  if (!match) return null
  return new Date(Number(match[1]), Number(match[2]) - 1, Number(match[3]))
}

/**
 * A rate as a percentage, by string surgery rather than by arithmetic.
 *
 * Basis points are an integer, so this is exact: 500 reads "5%", 25 reads
 * "0.25%", 1050 reads "10.5%". The trailing zeroes come off because nobody
 * says "five point zero zero percent".
 */
export function ratePercent(bps: number): string {
  const sign = bps < 0 ? '-' : ''
  const digits = String(Math.abs(Math.trunc(bps))).padStart(3, '0')
  const whole = digits.slice(0, -2)
  const fraction = digits.slice(-2).replace(/0+$/, '')
  return `${sign}${whole}${fraction ? `.${fraction}` : ''}%`
}

/**
 * A percentage typed by a person, as the basis points the server stores.
 *
 * A percentage with at most two decimal places and an integer of hundredths
 * are the same thing, so this is parseCentavos with a percent sign allowed:
 * "5" is 500, "2.5" is 250, "0" is 0, and nothing passes through a float.
 */
export function percentToBps(text: string): number | null {
  return parseCentavos(text.replace(/%/g, ''))
}

/**
 * A weight typed off a weighing slip, as tenths of a kilo.
 *
 * The same rules as the server's: commas are noise, one decimal place at most.
 * A second decimal is refused rather than rounded — the scale does not weigh
 * to the gram, so it is a typing mistake.
 */
export function parseKiloTenths(kilos: string): number | null {
  const text = kilos.replace(/[,\s]/g, '')
  if (!/^\d+(\.\d?)?$/.test(text)) return null
  const [whole, fraction = ''] = text.split('.')
  return Number(whole) * 10 + Number(fraction.padEnd(1, '0'))
}

/**
 * What a lot of palay is worth, in centavos: kilos × price per kilo, rounded
 * half-up to the centavo — the server's rule, done in integers.
 *
 * tenths × centavos-per-kilo is ten times the cost in centavos, so the
 * division by ten is the only rounding, and it is done on whole numbers.
 * A preview for the person typing; the server's figure is what is stored.
 */
export function palayCostCentavos(kilos: string, price: string): number | null {
  const tenths = parseKiloTenths(kilos)
  const centavos = parseCentavos(price)
  if (tenths === null || centavos === null) return null
  const tenTimes = tenths * centavos
  if (!Number.isSafeInteger(tenTimes)) return null
  return Math.floor((tenTimes + 5) / 10)
}

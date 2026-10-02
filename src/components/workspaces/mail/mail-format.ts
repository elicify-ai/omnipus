/**
 * mail-format.ts — display formatting for the Mail panel (US-3/US-4). Pure
 * functions so the panel's date/bytes rendering stays consistent between the
 * list rows, the reading pane and the sent-copy view.
 */

/**
 * formatMailTime — list-row timestamps: "14:32" for today, "Sep 27" for
 * this year, "2 Jan 2006" for older years. en-GB day-first style. A null or
 * absent date (the scheduled `date` nullability amendment's unknown state)
 * renders "No date" — never blank and never a zero/epoch value (F6); the
 * year-one/zero Go time renders "No date" too (W3 §2.1: "year-one/zero →
 * No date", U13's never-blank pin). An empty or malformed string stays
 * omitted. Surfaces that must OMIT the date entirely (the draft header's
 * F8 pins) gate on hasMailDate, which stays false for year-one.
 */
export function formatMailTime(iso: string | null | undefined): string {
  if (iso === null || iso === undefined) return 'No date'
  if (iso === '') return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  if (d.getUTCFullYear() <= 1) return 'No date'
  const now = new Date()
  const sameDay =
    d.getFullYear() === now.getFullYear() &&
    d.getMonth() === now.getMonth() &&
    d.getDate() === now.getDate()
  if (sameDay) {
    return new Intl.DateTimeFormat('en-GB', { hour: '2-digit', minute: '2-digit' }).format(d)
  }
  if (d.getFullYear() === now.getFullYear()) {
    return new Intl.DateTimeFormat('en-GB', { day: 'numeric', month: 'short' }).format(d)
  }
  return new Intl.DateTimeFormat('en-GB', { day: 'numeric', month: 'short', year: 'numeric' }).format(d)
}

/**
 * formatMailDate — the sent-copy's date line (US-4 AS-4): "2 Jan 2006".
 * Null/absent → "No date" (F6, post-amendment unknown state) and
 * year-one/zero → "No date" (W3 §2.1/U13's never-blank pin); empty and
 * malformed strings stay omitted. The draft header's F8 omission gates on
 * hasMailDate, which stays false for year-one — the two pins reconcile.
 */
export function formatMailDate(iso: string | null | undefined): string {
  if (iso === null || iso === undefined) return 'No date'
  if (iso === '') return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  if (d.getUTCFullYear() <= 1) return 'No date'
  return new Intl.DateTimeFormat('en-GB', { day: 'numeric', month: 'short', year: 'numeric' }).format(d)
}

/**
 * hasMailDate — true when the date carries a displayable value (not
 * null/absent/empty/malformed/year-one). The reading-pane detail header
 * uses it to omit the date line entirely for the F8 pinned cases (the
 * list row instead renders the formatters' "No date" per W3 §2.1).
 */
export function hasMailDate(iso: string | null | undefined): boolean {
  if (iso === null || iso === undefined || iso === '') return false
  const d = new Date(iso)
  return !Number.isNaN(d.getTime()) && d.getUTCFullYear() > 1
}

/**
 * formatMailRelativeAge — the freshness vocabulary's relative slot (U12):
 * "40 seconds ago", "2 minutes ago", "7 minutes ago", "3 hours ago",
 * "2 days ago". A null/unparseable validation time is the word "unknown" —
 * the exact filler of the pinned "Last checked unknown" variants (S-5),
 * never a fabricated zero or "just now".
 */
export function formatMailRelativeAge(iso: string | null | undefined, now: Date = new Date()): string {
  if (iso === null || iso === undefined || iso === '') return 'unknown'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime()) || d.getUTCFullYear() <= 1) return 'unknown'
  const seconds = Math.max(0, Math.round((now.getTime() - d.getTime()) / 1000))
  if (seconds < 60) return `${Math.max(seconds, 1)} second${seconds === 1 ? '' : 's'} ago`
  const minutes = Math.round(seconds / 60)
  if (minutes < 60) return `${minutes} minute${minutes === 1 ? '' : 's'} ago`
  const hours = Math.round(minutes / 60)
  if (hours < 24) return `${hours} hour${hours === 1 ? '' : 's'} ago`
  const days = Math.round(hours / 24)
  return `${days} day${days === 1 ? '' : 's'} ago`
}

/** The freshness input the panel feeds the formatter — the fields of the
 * landed `MailReadMetadata` that a label needs (not-wire-format: a
 * projection; every value still comes from the generated object). */
export interface MailFreshnessInput {
  source: 'live' | 'memory' | 'encrypted_disk' | 'none'
  last_validated_at: string | null
  stale: boolean
  refresh_needed: boolean
}

/**
 * formatMailFreshnessLine — the list/rail freshness line (S-3/S-4/S-5, US-2).
 * `checking` = this event's one live refresh is in flight. A cache hit is
 * never presented as a settled server check: only `source='live'` reads
 * "Checked just now". A null `last_validated_at` is unknown — never
 * "just checked" (US-2 AS-7, scenarios 2.5/2.6 pins).
 */
export function formatMailFreshnessLine(input: MailFreshnessInput, checking: boolean, now: Date = new Date()): string {
  const age = formatMailRelativeAge(input.last_validated_at, now)
  if (checking) {
    return `Last checked ${age} · Checking…`
  }
  if (input.last_validated_at === null || input.last_validated_at === undefined) {
    return 'Last checked unknown.'
  }
  if (input.source === 'live') {
    return 'Checked just now'
  }
  return `Checked ${age}`
}

/**
 * formatMailRefreshFailedLine — the failed-refresh line (S-6, US-2 AS-4).
 * The displayed time is the ORIGINAL last-validated time — a failed refresh
 * never advances it. Unknown original time renders the honest unknown fill.
 */
export function formatMailRefreshFailedLine(lastValidatedAt: string | null | undefined): string {
  if (lastValidatedAt === null || lastValidatedAt === undefined) {
    return "Couldn't refresh — showing messages as of an unknown time."
  }
  return `Couldn't refresh — showing messages as of ${formatMailTime(lastValidatedAt)}.`
}

/**
 * formatMailBytes — attachment sizes (D28): B/KB/MB/GB with one decimal
 * above the unit floor.
 */
export function formatMailBytes(bytes: number | null | undefined): string {
  if (bytes === null || bytes === undefined || !Number.isFinite(bytes) || bytes < 0) return ''
  if (bytes < 1024) return `${bytes} B`
  const units = ['KB', 'MB', 'GB', 'TB'] as const
  let value = bytes
  let unit = 'B'
  for (const next of units) {
    value = value / 1024
    unit = next
    if (value < 1024) break
  }
  const rounded = value >= 100 ? Math.round(value) : Math.round(value * 10) / 10
  return `${rounded} ${unit}`
}

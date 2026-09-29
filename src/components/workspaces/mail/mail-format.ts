/**
 * mail-format.ts — display formatting for the Mail panel (US-3/US-4). Pure
 * functions so the panel's date/bytes rendering stays consistent between the
 * list rows, the reading pane and the sent-copy view.
 */

/**
 * formatMailTime — list-row timestamps: "14:32" for today, "Sep 27" for
 * this year, "2 Jan 2006" for older years. en-GB day-first style.
 */
export function formatMailTime(iso: string | null | undefined): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
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
 */
export function formatMailDate(iso: string | null | undefined): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  return new Intl.DateTimeFormat('en-GB', { day: 'numeric', month: 'short', year: 'numeric' }).format(d)
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

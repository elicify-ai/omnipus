/**
 * Human-readable relative time: "just now", "5m ago", "3h ago", "2d ago".
 * Falls back to a short absolute date (e.g. "Jul 16") once the age reaches
 * a week, and returns '' for an unparseable date string.
 *
 * Shared by session list surfaces (SearchModal, formerly SessionItem) —
 * extracted so there is exactly one relative-time formatter, not a
 * per-component copy that can drift.
 */
export function formatRelative(dateStr: string): string {
  const d = new Date(dateStr)
  if (isNaN(d.getTime())) return ''
  const mins = Math.floor((Date.now() - d.getTime()) / 60_000)
  if (mins < 1) return 'just now'
  if (mins < 60) return `${mins}m ago`
  const hrs = Math.floor(mins / 60)
  if (hrs < 24) return `${hrs}h ago`
  const days = Math.floor(hrs / 24)
  if (days < 7) return `${days}d ago`
  return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
}

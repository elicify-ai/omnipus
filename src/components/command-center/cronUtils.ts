/**
 * Pure utility functions for cron/trigger round-trip logic used by ScheduleFormSheet.
 * Extracted into a co-located module so unit tests can import them directly.
 *
 * No cron parsing library — per spec §9/M-8 (no new library; authoritative validation
 * stays server-side). All checks here are structural only.
 */

// ── Types (re-exported for consumers) ─────────────────────────────────────────

export type EveryUnit = 'minutes' | 'hours' | 'days'
export type FriendlyMode = 'once' | 'repeat' | 'custom'
export type RepeatShape = 'interval' | 'daily-at' | 'weekly-at' | 'monthly-at'

export interface ParsedFriendly {
  friendlyMode: FriendlyMode
  repeatShape: RepeatShape
  everyValue?: string
  everyUnit?: EveryUnit
  repeatTime?: string
  weekday?: string
  monthDay?: string
}

// ── Constants ──────────────────────────────────────────────────────────────────

export const UNIT_MS: Record<EveryUnit, number> = {
  minutes: 60_000,
  hours: 3_600_000,
  days: 86_400_000,
}

// ── Cron validation (5-field structural check, no library) ──────────────────

// Each cron field: digits, *, /, -, , only (handles most valid patterns).
const CRON_FIELD_RE = /^[0-9*,/\-]+$/

/**
 * Returns true if the expression has exactly 5 space-separated fields with
 * basic valid characters. This is a structural gate only (M-8 — no cron library).
 * Semantically invalid crons like "99 99 * * *" return true; the server validates
 * semantics (AC3b).
 */
export function isStructurallyValidCron(expr: string): boolean {
  const fields = expr.trim().split(/\s+/)
  if (fields.length !== 5) return false
  return fields.every((f) => CRON_FIELD_RE.test(f))
}

// ── Cron generation ────────────────────────────────────────────────────────

/** Parse "HH:MM" → { h, m } integers (NaN-safe). */
function parseHHMM(t: string): { h: number; m: number } {
  const [hStr, mStr] = t.split(':')
  return { h: parseInt(hStr ?? '', 10), m: parseInt(mStr ?? '', 10) }
}

/**
 * Generate a cron expression from repeat state.
 * Returns null when inputs are incomplete or invalid.
 * Returns null for 'interval' shape (that shape uses every_ms, not cron).
 */
export function buildRepeatCron(
  shape: RepeatShape,
  time: string,
  weekday: string,
  monthDay: string,
): string | null {
  if (shape === 'interval') return null
  const { h, m } = parseHHMM(time)
  if (isNaN(h) || isNaN(m)) return null
  if (h < 0 || h > 23 || m < 0 || m > 59) return null
  switch (shape) {
    case 'daily-at':
      return `${m} ${h} * * *`
    case 'weekly-at': {
      const d = parseInt(weekday, 10)
      if (isNaN(d) || d < 0 || d > 6) return null
      return `${m} ${h} * * ${d}`
    }
    case 'monthly-at': {
      const day = parseInt(monthDay, 10)
      if (isNaN(day) || day < 1 || day > 31) return null
      return `${m} ${h} ${day} * *`
    }
  }
}

// ── Reverse-parse (existing trigger → friendly controls) ───────────────────

/**
 * Attempt to reverse-parse a fixed-interval ms value into friendly form state.
 * Returns partial ParsedFriendly overrides, or null if the interval isn't a
 * round number of minutes/hours/days (→ falls through to custom view).
 */
export function reverseParseEvery(every_ms: number): Partial<ParsedFriendly> | null {
  if (every_ms % UNIT_MS.days === 0)
    return {
      friendlyMode: 'repeat',
      repeatShape: 'interval',
      everyValue: String(every_ms / UNIT_MS.days),
      everyUnit: 'days',
    }
  if (every_ms % UNIT_MS.hours === 0)
    return {
      friendlyMode: 'repeat',
      repeatShape: 'interval',
      everyValue: String(every_ms / UNIT_MS.hours),
      everyUnit: 'hours',
    }
  if (every_ms % UNIT_MS.minutes === 0)
    return {
      friendlyMode: 'repeat',
      repeatShape: 'interval',
      everyValue: String(every_ms / UNIT_MS.minutes),
      everyUnit: 'minutes',
    }
  // Non-round interval: cannot represent in the friendly UI.
  return null
}

/**
 * Match a cron expression against the three friendly templates.
 * Returns partial ParsedFriendly overrides or null for a complex cron
 * (which opens the Custom view verbatim — never silently rewritten, per M-1).
 *
 * Friendly templates (spec §3 US-A2 M-1):
 * - 'M H * * *'   → daily-at
 * - 'M H * * D'   → weekly-at (D is 0-6 single digit)
 * - 'M H d * *'   → monthly-at (d is 1-31 single digit)
 */
export function reverseParseCron(expr: string): Partial<ParsedFriendly> | null {
  const fields = expr.trim().split(/\s+/)
  if (fields.length !== 5) return null
  const [mStr, hStr, domStr, monStr, dowStr] = fields
  const h = parseInt(hStr, 10)
  const m = parseInt(mStr, 10)
  if (isNaN(h) || isNaN(m) || h < 0 || h > 23 || m < 0 || m > 59) return null
  const timeStr = `${String(h).padStart(2, '0')}:${String(m).padStart(2, '0')}`

  // Daily-at: 'M H * * *'
  if (domStr === '*' && monStr === '*' && dowStr === '*')
    return { friendlyMode: 'repeat', repeatShape: 'daily-at', repeatTime: timeStr }

  // Weekly-at: 'M H * * D' where D is a single weekday digit 0-6
  if (domStr === '*' && monStr === '*' && /^[0-6]$/.test(dowStr))
    return { friendlyMode: 'repeat', repeatShape: 'weekly-at', repeatTime: timeStr, weekday: dowStr }

  // Monthly-at: 'M H d * *' where d is a single day 1-31
  if (/^\d{1,2}$/.test(domStr) && monStr === '*' && dowStr === '*') {
    const day = parseInt(domStr, 10)
    if (day >= 1 && day <= 31)
      return { friendlyMode: 'repeat', repeatShape: 'monthly-at', repeatTime: timeStr, monthDay: domStr }
  }

  // No match → open in Custom view (caller places cronExpr verbatim).
  return null
}

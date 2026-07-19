/**
 * Recurrence editor ⇄ RRULE compile/parse/summarize helpers.
 *
 * Spec: docs/internal/specs/calendar-recurrence-redesign-spec.md
 * (User Story 1; FR-002/003/004/006a/007/019/021; Timezone Semantics §1;
 * "Editor compile" test dataset; presets BDD scenario).
 *
 * not-wire-format: `RecurrenceEditorState`/`RecurrenceValue` are internal UI
 * view models. Only `CompiledRecurrence` (rrule + dtstart_ms + tz) maps onto
 * the wire (`TaskTrigger.config` for `type: recurring`) — and even then this
 * module only produces the plain field VALUES; the actual REST payload
 * shaping/typing is the caller's job against the generated `TaskTrigger` type.
 *
 * Library boundary (Non-Behaviors / Integration Boundaries in the spec):
 * `rrule` (npm) is used for BUILDING the wire RRULE string only — never for
 * occurrence expansion or display math, which is server-authoritative.
 * PARSING an existing RRULE body back into editor state is hand-rolled
 * (regex/split), NOT routed through `RRule.fromString`/`rrulestr` — see the
 * "PARSER GOTCHA" note above `parseRruleString` for why.
 */

import { RRule, Frequency, type Options } from 'rrule'
import type { Weekday } from 'rrule'

// ─── Types ──────────────────────────────────────────────────────────────────

export type WeekdayCode = 'MO' | 'TU' | 'WE' | 'TH' | 'FR' | 'SA' | 'SU'

export type RecurrenceFrequency = 'minutely' | 'hourly' | 'daily' | 'weekly' | 'monthly' | 'yearly'

export type MonthlyMode = 'day-of-month' | 'nth-weekday'

export type RecurrenceEndCondition =
  | { kind: 'never' }
  | { kind: 'on-date'; date: Date }
  | { kind: 'after-count'; count: number }

/**
 * The Custom editor's full state. All monthly-mode fields are always present
 * (anchor-derived defaults) regardless of which `monthlyMode` is active, so
 * toggling the mode never loses/needs-to-recompute the other representation
 * (Outlook/Google convention: both "day N" and "the Nth weekday" are always
 * derived from the same anchor date, the toggle just picks which is stored).
 */
export interface RecurrenceEditorState {
  frequency: RecurrenceFrequency
  /** 1–99, always pre-clamped (see `clampInterval`). */
  interval: number
  /** Only meaningful when `frequency === 'weekly'`. */
  weekdays: WeekdayCode[]
  monthlyMode: MonthlyMode
  /** 1–31; used when `monthlyMode === 'day-of-month'`. */
  monthlyDayOfMonth: number
  /** 1–5 or -1 ("last"); used when `monthlyMode === 'nth-weekday'`. */
  monthlyOrdinal: number
  /** Used when `monthlyMode === 'nth-weekday'`. */
  monthlyWeekday: WeekdayCode
  end: RecurrenceEndCondition
}

/**
 * The value RecurrenceEditor exchanges with its parent. `'none'` means
 * "Does not repeat" — the caller creates a `once`-trigger task, never an
 * RRULE (US-1 Acceptance Scenario 6). There is deliberately no third
 * "legacy" variant here — reading/offering a fresh rule for a legacy
 * (`cron_expr`/`every_ms`) task is the slide-over's concern (US-5), not this
 * leaf's.
 */
export type RecurrenceValue = { kind: 'none' } | { kind: 'rrule'; state: RecurrenceEditorState }

/** The wire-shaped output of a compiled rule (FR-005). */
export interface CompiledRecurrence {
  /** Bare RRULE body, no `RRULE:` prefix, e.g. `FREQ=WEEKLY;INTERVAL=2;BYDAY=MO;COUNT=10`. */
  rrule: string
  dtstart_ms: number
  tz: string
}

export interface RecurrencePreset {
  id: 'none' | 'daily' | 'weekly' | 'monthly' | 'yearly' | 'weekday' | 'custom'
  label: string
  value: RecurrenceValue
}

// ─── Constants ──────────────────────────────────────────────────────────────

/** ISO-ish display/compile order — also the canonical BYDAY join order. */
export const WEEKDAY_ORDER: WeekdayCode[] = ['MO', 'TU', 'WE', 'TH', 'FR', 'SA', 'SU']

export const WEEKDAY_LABEL: Record<WeekdayCode, string> = {
  MO: 'Monday',
  TU: 'Tuesday',
  WE: 'Wednesday',
  TH: 'Thursday',
  FR: 'Friday',
  SA: 'Saturday',
  SU: 'Sunday',
}

export const WEEKDAY_SHORT_LABEL: Record<WeekdayCode, string> = {
  MO: 'Mo',
  TU: 'Tu',
  WE: 'We',
  TH: 'Th',
  FR: 'Fr',
  SA: 'Sa',
  SU: 'Su',
}

const MONTH_LABEL = [
  'January', 'February', 'March', 'April', 'May', 'June',
  'July', 'August', 'September', 'October', 'November', 'December',
]

export const RECURRENCE_FREQUENCY_OPTIONS: RecurrenceFrequency[] = [
  'minutely', 'hourly', 'daily', 'weekly', 'monthly', 'yearly',
]

const FREQUENCY_TO_RRULE: Record<RecurrenceFrequency, Frequency> = {
  minutely: Frequency.MINUTELY,
  hourly: Frequency.HOURLY,
  daily: Frequency.DAILY,
  weekly: Frequency.WEEKLY,
  monthly: Frequency.MONTHLY,
  yearly: Frequency.YEARLY,
}

const RRULE_WEEKDAY: Record<WeekdayCode, Weekday> = {
  MO: RRule.MO,
  TU: RRule.TU,
  WE: RRule.WE,
  TH: RRule.TH,
  FR: RRule.FR,
  SA: RRule.SA,
  SU: RRule.SU,
}

const UNIT_SINGULAR: Record<RecurrenceFrequency, string> = {
  minutely: 'minute',
  hourly: 'hour',
  daily: 'day',
  weekly: 'week',
  monthly: 'month',
  yearly: 'year',
}

const FREQ_WORD_TO_FREQUENCY: Record<string, RecurrenceFrequency> = {
  MINUTELY: 'minutely',
  HOURLY: 'hourly',
  DAILY: 'daily',
  WEEKLY: 'weekly',
  MONTHLY: 'monthly',
  YEARLY: 'yearly',
}

// ─── Small pure helpers ─────────────────────────────────────────────────────

/** Clamp to 1–99 (FR-003/FR-019). Non-finite input clamps to 1 (prevention, not correction). */
export function clampInterval(raw: number): number {
  if (!Number.isFinite(raw)) return 1
  const n = Math.trunc(raw)
  if (n < 1) return 1
  if (n > 99) return 99
  return n
}

/** Clamp to 1–31. Non-finite input clamps to 1. */
export function clampDayOfMonth(raw: number): number {
  if (!Number.isFinite(raw)) return 1
  const n = Math.trunc(raw)
  if (n < 1) return 1
  if (n > 31) return 31
  return n
}

/** D5: a day-of-month clamp form (canonical `BYMONTHDAY=28..N;BYSETPOS=-1`) is needed for N >= 29. */
export function isMonthlyClampDay(day: number): boolean {
  return day >= 29
}

function monthlyClampDays(day: number): number[] {
  const days: number[] = []
  for (let d = 28; d <= day; d++) days.push(d)
  return days
}

const JS_DAY_INDEX_TO_CODE: WeekdayCode[] = ['SU', 'MO', 'TU', 'WE', 'TH', 'FR', 'SA']

export function weekdayCodeOf(date: Date): WeekdayCode {
  return JS_DAY_INDEX_TO_CODE[date.getDay()]
}

/** 1-based ordinal of `date`'s weekday within its month (1st..5th occurrence). */
export function ordinalOfDateInMonth(date: Date): number {
  return Math.ceil(date.getDate() / 7)
}

export function ordinalWord(n: number): string {
  if (n === -1) return 'last'
  switch (n) {
    case 1: return 'first'
    case 2: return 'second'
    case 3: return 'third'
    case 4: return 'fourth'
    case 5: return 'fifth'
    default: return `${n}th`
  }
}

/** Sorts AND de-duplicates into canonical WEEKDAY_ORDER (also the BYDAY join order). */
export function sortWeekdays(codes: WeekdayCode[]): WeekdayCode[] {
  const set = new Set(codes)
  return WEEKDAY_ORDER.filter((c) => set.has(c))
}

function isEveryWeekdaySet(codes: WeekdayCode[]): boolean {
  const set = new Set(codes)
  return set.size === 5 && ['MO', 'TU', 'WE', 'TH', 'FR'].every((d) => set.has(d as WeekdayCode))
}

function isWeekdayCode(s: string): s is WeekdayCode {
  return (WEEKDAY_ORDER as string[]).includes(s)
}

/** Unit label pluralized against the (clamped) interval, e.g. "week" / "weeks". */
export function unitLabel(freq: RecurrenceFrequency, interval: number): string {
  const n = clampInterval(interval)
  const base = UNIT_SINGULAR[freq]
  return n === 1 ? base : `${base}s`
}

export function browserTimeZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
  } catch {
    return 'UTC'
  }
}

/** Local-midnight of `date`'s calendar day (strips time-of-day; used for the end-date picker's min-date matcher). */
export function startOfLocalDay(date: Date): Date {
  return new Date(date.getFullYear(), date.getMonth(), date.getDate())
}

export function formatTimeOfDay(date: Date): string {
  return new Intl.DateTimeFormat(undefined, { hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }).format(date)
}

export function formatDateLabel(date: Date): string {
  return date.toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' })
}

function formatMonthDayYear(date: Date): string {
  return `${MONTH_LABEL[date.getMonth()]} ${date.getDate()}, ${date.getFullYear()}`
}

/**
 * Converts a wall-clock date+time in an arbitrary IANA zone to the equivalent
 * UTC instant, via the `Intl.DateTimeFormat` offset-probe technique (format a
 * UTC guess in the target zone, read back the delta, correct). No date-fns/
 * date-fns-tz dependency (project convention — see "Shadcn Datepicker"
 * decisions) and — unlike a plain local `new Date(y,m,d,h,mi,s)` constructor —
 * independent of the JS runtime's OWN system timezone, so callers (and this
 * module's tests) are deterministic regardless of the machine's `TZ`.
 */
export function zonedWallTimeToUtc(
  year: number,
  month1to12: number,
  day: number,
  hour: number,
  minute: number,
  second: number,
  timeZone: string,
): Date {
  const utcGuess = Date.UTC(year, month1to12 - 1, day, hour, minute, second)
  const dtf = new Intl.DateTimeFormat('en-US', {
    timeZone,
    hourCycle: 'h23',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  })
  const parts = dtf.formatToParts(new Date(utcGuess))
  const get = (type: string): number => {
    const raw = Number(parts.find((p) => p.type === type)?.value ?? '0')
    // Guard a known ICU quirk: some ICU builds render midnight as "24" under
    // hourCycle:'h23' instead of "00". Normalize defensively.
    return type === 'hour' ? raw % 24 : raw
  }
  const asIfUtc = Date.UTC(
    get('year'), get('month') - 1, get('day'), get('hour'), get('minute'), get('second'),
  )
  const offsetMs = asIfUtc - utcGuess
  return new Date(utcGuess - offsetMs)
}

/** FR-006a: "Ends on date D" → D 23:59:59 in the rule's tz, converted to UTC. */
export function endOfDayUtc(date: Date, timeZone: string): Date {
  return zonedWallTimeToUtc(date.getFullYear(), date.getMonth() + 1, date.getDate(), 23, 59, 59, timeZone)
}

// ─── State construction ─────────────────────────────────────────────────────

function baseState(anchor: Date): RecurrenceEditorState {
  return {
    frequency: 'daily',
    interval: 1,
    weekdays: [],
    monthlyMode: 'day-of-month',
    monthlyDayOfMonth: clampDayOfMonth(anchor.getDate()),
    monthlyOrdinal: ordinalOfDateInMonth(anchor),
    monthlyWeekday: weekdayCodeOf(anchor),
    end: { kind: 'never' },
  }
}

/** Seed state for the Custom editor when opened fresh (not derived from an existing rule). */
export function defaultRecurrenceState(anchor: Date): RecurrenceEditorState {
  return { ...baseState(anchor), frequency: 'weekly', weekdays: [weekdayCodeOf(anchor)] }
}

// ─── Compile: state → RRULE string ─────────────────────────────────────────

/**
 * Builds the bare RRULE body (no `RRULE:` prefix, no DTSTART line) via
 * `RRule.optionsToString` — the "compile" half of the ⇄ contract, always
 * routed through the real library per the spec's Integration Boundaries.
 *
 * Safe against the parser gotcha below: `optionsToString` is a pure
 * serializer over exactly the keys present in the object it's given — unlike
 * `RRule.fromString`/`new RRule(...)`, it never constructs an `RRule`
 * instance and never defaults/auto-fills `dtstart`. We never include a
 * `dtstart` key, so no DTSTART line is ever emitted (dtstart_ms/tz travel as
 * separate sibling wire fields per FR-005).
 */
export function buildRruleString(state: RecurrenceEditorState, timeZone: string): string {
  const interval = clampInterval(state.interval)
  const options: Partial<Options> = { freq: FREQUENCY_TO_RRULE[state.frequency] }
  if (interval > 1) options.interval = interval

  if (state.frequency === 'weekly') {
    const days = sortWeekdays(state.weekdays)
    if (days.length > 0) options.byweekday = days.map((d) => RRULE_WEEKDAY[d])
  } else if (state.frequency === 'monthly') {
    if (state.monthlyMode === 'day-of-month') {
      const day = clampDayOfMonth(state.monthlyDayOfMonth)
      if (isMonthlyClampDay(day)) {
        // D5/FR-007: canonical clamp form — never plain BYMONTHDAY=N for N>=29.
        options.bymonthday = monthlyClampDays(day)
        options.bysetpos = -1
      } else {
        options.bymonthday = day
      }
    } else {
      options.byweekday = RRULE_WEEKDAY[state.monthlyWeekday]
      options.bysetpos = state.monthlyOrdinal
    }
  }
  // daily/hourly/minutely/yearly with no BY* rely on DTSTART for anchoring
  // (RFC 5545 default) — no explicit BYMONTH/BYMONTHDAY/BYHOUR is emitted.

  if (state.end.kind === 'on-date') {
    options.until = endOfDayUtc(state.end.date, timeZone)
  } else if (state.end.kind === 'after-count') {
    options.count = Math.max(1, Math.trunc(state.end.count))
  }

  const serialized = RRule.optionsToString(options)
  const rruleLine = serialized.split('\n').find((line) => line.startsWith('RRULE:')) ?? serialized
  return rruleLine.replace(/^RRULE:/, '')
}

/** Compiles a Custom-editor state into the wire-shaped `{rrule, dtstart_ms, tz}` triple. */
export function compileRecurrence(
  state: RecurrenceEditorState,
  anchor: Date,
  timeZone?: string,
): CompiledRecurrence {
  const tz = timeZone ?? browserTimeZone()
  return { rrule: buildRruleString(state, tz), dtstart_ms: anchor.getTime(), tz }
}

// ─── Parse: RRULE string → state ───────────────────────────────────────────
//
// PARSER GOTCHA (documented for the next reader / the slide-over agent):
// `RRule.fromString`/`rrulestr` construct a real `RRule` via `new
// RRule(parseString(str))`, and `parseOptions` (parseoptions.js:32-34) does:
//
//   if (!opts.dtstart) opts.dtstart = new Date(new Date().setMilliseconds(0))
//
// — i.e. defaults dtstart to the CURRENT WALL-CLOCK INSTANT when no DTSTART
// line is present, and then (parseoptions.js:47-68) AUTO-FILLS bymonthday /
// byweekday / bymonth from that now-instant for any YEARLY/MONTHLY/WEEKLY
// rule that has no BY* modifier at all. Because our wire contract stores
// `dtstart_ms`/`tz` as separate sibling fields (never embedded as a DTSTART
// line inside the `rrule` string itself, FR-005), calling
// `RRule.fromString(rruleBody)` with no DTSTART line is non-deterministic
// for exactly the bare `FREQ=WEEKLY`/`FREQ=MONTHLY`/`FREQ=YEARLY` shapes a
// hand-crafted API payload could produce (never emitted by this editor,
// which always writes an explicit BYDAY/BYMONTHDAY/BYSETPOS — see
// buildRruleString above — but still reachable on reopen of foreign data).
//
// To avoid depending on `dtstart` defaulting at all, parsing here is a
// small hand-rolled KEY=VALUE split/regex over the bare RRULE body — fully
// deterministic, and any field NOT explicitly present in the string falls
// back to values derived directly from the caller-supplied `dtstartMs`
// (via plain, local Date getters — correct under the v1 assumption that the
// rule's tz always equals the browser's own tz, Timezone Semantics §1).

function parseFields(body: string): Map<string, string> {
  const out = new Map<string, string>()
  for (const part of body.split(';')) {
    const eq = part.indexOf('=')
    if (eq === -1) continue
    const key = part.slice(0, eq).trim().toUpperCase()
    const val = part.slice(eq + 1).trim()
    if (key) out.set(key, val)
  }
  return out
}

function parseUntilToken(token: string): Date | null {
  const m = /^(\d{4})(\d{2})(\d{2})(?:T(\d{2})(\d{2})(\d{2})Z?)?$/.exec(token.trim())
  if (!m) return null
  const [, y, mo, d, h = '0', mi = '0', s = '0'] = m
  const ms = Date.UTC(Number(y), Number(mo) - 1, Number(d), Number(h), Number(mi), Number(s))
  return Number.isFinite(ms) ? new Date(ms) : null
}

/** Recognizes the canonical D5/FR-007 clamp shape: BYMONTHDAY=28,...,N (consecutive from 28) + BYSETPOS=-1. */
function isConsecutiveFrom28(days: number[]): boolean {
  if (days.length < 2 || days.length > 4) return false
  const sorted = [...days].sort((a, b) => a - b)
  if (sorted[0] !== 28) return false
  for (let i = 1; i < sorted.length; i++) {
    if (sorted[i] !== sorted[i - 1] + 1) return false
  }
  return true
}

/**
 * Parses a bare RRULE body (an `RRULE:` prefix, if present, is stripped)
 * plus its sibling `dtstartMs` into editor state. Returns null when the
 * frequency is unrecognized/unsupported by this editor (e.g. `SECONDLY`,
 * which the server rejects outright, or a syntactically malformed body) —
 * callers should treat null the same as "no rule" for display purposes.
 */
export function parseRruleString(rruleBody: string, dtstartMs: number): RecurrenceEditorState | null {
  const body = rruleBody.replace(/^RRULE:/i, '').trim()
  if (!body) return null

  const fields = parseFields(body)
  const freqWord = fields.get('FREQ')
  const frequency = freqWord ? FREQ_WORD_TO_FREQUENCY[freqWord] : undefined
  if (!frequency) return null

  const anchor = new Date(dtstartMs)
  const defaults = baseState(anchor)

  const interval = fields.has('INTERVAL') ? clampInterval(Number(fields.get('INTERVAL'))) : 1

  let weekdays: WeekdayCode[] = []
  if (frequency === 'weekly' && fields.has('BYDAY')) {
    weekdays = sortWeekdays(
      fields.get('BYDAY')!
        .split(',')
        .map((s) => s.trim().toUpperCase())
        .filter(isWeekdayCode) as WeekdayCode[],
    )
  }

  let monthlyMode: MonthlyMode = defaults.monthlyMode
  let monthlyDayOfMonth = defaults.monthlyDayOfMonth
  let monthlyOrdinal = defaults.monthlyOrdinal
  let monthlyWeekday = defaults.monthlyWeekday

  if (frequency === 'monthly') {
    const bymonthdayRaw = fields.get('BYMONTHDAY')
    const bysetposRaw = fields.get('BYSETPOS')
    const bydayRaw = fields.get('BYDAY')

    if (bymonthdayRaw) {
      const days = bymonthdayRaw
        .split(',')
        .map((s) => parseInt(s.trim(), 10))
        .filter((n) => Number.isFinite(n))
      const setpos = bysetposRaw ? parseInt(bysetposRaw, 10) : null
      if (setpos === -1 && isConsecutiveFrom28(days)) {
        monthlyMode = 'day-of-month'
        monthlyDayOfMonth = Math.max(...days)
      } else if (days.length === 1 && setpos == null) {
        monthlyMode = 'day-of-month'
        monthlyDayOfMonth = clampDayOfMonth(days[0])
      }
      // else: an unrecognized bymonthday+bysetpos shape from a hand-crafted
      // payload — fall back to the anchor-derived defaults set above.
    } else if (bydayRaw && bysetposRaw) {
      const code = bydayRaw.trim().toUpperCase()
      const setpos = parseInt(bysetposRaw, 10)
      if (isWeekdayCode(code) && Number.isFinite(setpos)) {
        monthlyMode = 'nth-weekday'
        monthlyWeekday = code
        monthlyOrdinal = setpos
      }
    }
  }

  let end: RecurrenceEndCondition = { kind: 'never' }
  if (fields.has('UNTIL')) {
    const until = parseUntilToken(fields.get('UNTIL')!)
    if (until) end = { kind: 'on-date', date: until }
  } else if (fields.has('COUNT')) {
    const count = parseInt(fields.get('COUNT')!, 10)
    if (Number.isFinite(count) && count > 0) end = { kind: 'after-count', count }
  }

  return { frequency, interval, weekdays, monthlyMode, monthlyDayOfMonth, monthlyOrdinal, monthlyWeekday, end }
}

// ─── Validation (FR-019) ────────────────────────────────────────────────────

export interface RecurrenceValidity {
  valid: boolean
  reason: string | null
}

export function validateRecurrenceState(state: RecurrenceEditorState): RecurrenceValidity {
  if (state.frequency === 'weekly' && state.weekdays.length === 0) {
    return { valid: false, reason: 'Select at least one day of the week.' }
  }
  if (state.end.kind === 'after-count' && (!Number.isFinite(state.end.count) || state.end.count < 1)) {
    return { valid: false, reason: 'Enter at least 1 occurrence.' }
  }
  return { valid: true, reason: null }
}

// ─── Summary (FR-004/FR-007) ────────────────────────────────────────────────

function weekdayListLabel(codes: WeekdayCode[]): string {
  const labels = sortWeekdays(codes).map((c) => WEEKDAY_LABEL[c])
  if (labels.length === 0) return ''
  if (labels.length === 1) return labels[0]
  if (labels.length === 2) return `${labels[0]} and ${labels[1]}`
  return `${labels.slice(0, -1).join(', ')}, and ${labels[labels.length - 1]}`
}

function frequencyPhrase(state: RecurrenceEditorState): string {
  const n = clampInterval(state.interval)
  switch (state.frequency) {
    case 'minutely':
      return n === 1 ? 'every minute' : `every ${n} minutes`
    case 'hourly':
      return n === 1 ? 'every hour' : `every ${n} hours`
    case 'daily':
      return n === 1 ? 'every day' : `every ${n} days`
    case 'weekly': {
      if (n === 1 && isEveryWeekdaySet(state.weekdays)) return 'every weekday'
      const dayLabel = state.weekdays.length > 0 ? ` on ${weekdayListLabel(state.weekdays)}` : ''
      return `every ${n === 1 ? 'week' : `${n} weeks`}${dayLabel}`
    }
    case 'monthly': {
      const monthWord = n === 1 ? 'month' : `${n} months`
      if (state.monthlyMode === 'nth-weekday') {
        return `every ${monthWord} on the ${ordinalWord(state.monthlyOrdinal)} ${WEEKDAY_LABEL[state.monthlyWeekday]}`
      }
      return `every ${monthWord} on day ${clampDayOfMonth(state.monthlyDayOfMonth)}`
    }
    case 'yearly':
      return n === 1 ? 'every year' : `every ${n} years`
  }
}

function endConditionClause(end: RecurrenceEndCondition): string {
  if (end.kind === 'after-count') {
    const n = Math.max(1, Math.trunc(end.count))
    return `, ${n} time${n === 1 ? '' : 's'}`
  }
  if (end.kind === 'on-date') {
    return `, ends ${formatMonthDayYear(end.date)}`
  }
  return ''
}

function coreClause(state: RecurrenceEditorState, anchor: Date): string {
  // FR-007: the clamp form gets its own MANDATED literal — never a BYMONTHDAY
  // list, never the generic "Repeats every month on day N" phrasing.
  if (
    state.frequency === 'monthly'
    && state.monthlyMode === 'day-of-month'
    && isMonthlyClampDay(clampDayOfMonth(state.monthlyDayOfMonth))
  ) {
    return `Monthly on day ${clampDayOfMonth(state.monthlyDayOfMonth)} (moved to the last day in shorter months)`
  }
  if (state.frequency === 'yearly') {
    return `Repeats ${frequencyPhrase(state)} on ${MONTH_LABEL[anchor.getMonth()]} ${anchor.getDate()}`
  }
  return `Repeats ${frequencyPhrase(state)}`
}

/** Live plain-English summary of the current rule (FR-004), including the FR-007 clamp special case. */
export function summarizeRecurrence(value: RecurrenceValue, anchor: Date): string {
  if (value.kind === 'none') return 'Does not repeat'
  return coreClause(value.state, anchor) + endConditionClause(value.state.end)
}

// ─── Presets (FR-002) ───────────────────────────────────────────────────────

export function computePresets(anchor: Date): RecurrencePreset[] {
  const weekday = weekdayCodeOf(anchor)
  const ordinal = ordinalOfDateInMonth(anchor)
  const monthLabel = MONTH_LABEL[anchor.getMonth()]
  const day = anchor.getDate()

  const dailyState: RecurrenceEditorState = { ...baseState(anchor), frequency: 'daily' }
  const weeklyState: RecurrenceEditorState = { ...baseState(anchor), frequency: 'weekly', weekdays: [weekday] }
  const monthlyNthState: RecurrenceEditorState = {
    ...baseState(anchor),
    frequency: 'monthly',
    monthlyMode: 'nth-weekday',
    monthlyOrdinal: ordinal,
    monthlyWeekday: weekday,
  }
  const yearlyState: RecurrenceEditorState = { ...baseState(anchor), frequency: 'yearly' }
  const weekdayState: RecurrenceEditorState = {
    ...baseState(anchor),
    frequency: 'weekly',
    weekdays: ['MO', 'TU', 'WE', 'TH', 'FR'],
  }

  return [
    { id: 'none', label: 'Does not repeat', value: { kind: 'none' } },
    { id: 'daily', label: 'Daily', value: { kind: 'rrule', state: dailyState } },
    { id: 'weekly', label: `Weekly on ${WEEKDAY_LABEL[weekday]}`, value: { kind: 'rrule', state: weeklyState } },
    {
      id: 'monthly',
      label: `Monthly on the ${ordinalWord(ordinal)} ${WEEKDAY_LABEL[weekday]}`,
      value: { kind: 'rrule', state: monthlyNthState },
    },
    { id: 'yearly', label: `Annually on ${monthLabel} ${day}`, value: { kind: 'rrule', state: yearlyState } },
    { id: 'weekday', label: 'Every weekday', value: { kind: 'rrule', state: weekdayState } },
    // Seed value only — matchPreset() never equality-matches 'custom' itself,
    // it's always the ELSE branch after the concrete presets are checked.
    { id: 'custom', label: 'Custom…', value: { kind: 'rrule', state: weeklyState } },
  ]
}

function endConditionsEqual(a: RecurrenceEndCondition, b: RecurrenceEndCondition): boolean {
  if (a.kind !== b.kind) return false
  if (a.kind === 'after-count' && b.kind === 'after-count') return a.count === b.count
  if (a.kind === 'on-date' && b.kind === 'on-date') return a.date.getTime() === b.date.getTime()
  return true
}

function recurrenceStatesEqual(a: RecurrenceEditorState, b: RecurrenceEditorState): boolean {
  if (a.frequency !== b.frequency) return false
  if (clampInterval(a.interval) !== clampInterval(b.interval)) return false
  if (a.frequency === 'weekly') {
    const aw = sortWeekdays(a.weekdays)
    const bw = sortWeekdays(b.weekdays)
    if (aw.length !== bw.length || aw.some((d, i) => d !== bw[i])) return false
  }
  if (a.frequency === 'monthly') {
    if (a.monthlyMode !== b.monthlyMode) return false
    if (a.monthlyMode === 'day-of-month' && clampDayOfMonth(a.monthlyDayOfMonth) !== clampDayOfMonth(b.monthlyDayOfMonth)) {
      return false
    }
    if (a.monthlyMode === 'nth-weekday' && (a.monthlyOrdinal !== b.monthlyOrdinal || a.monthlyWeekday !== b.monthlyWeekday)) {
      return false
    }
  }
  return endConditionsEqual(a.end, b.end)
}

/** Which preset (if any) the current value matches, for the Select's controlled value. 'custom' is the fallback. */
export function matchPreset(value: RecurrenceValue, anchor: Date): RecurrencePreset['id'] {
  if (value.kind === 'none') return 'none'
  const presets = computePresets(anchor)
  for (const preset of presets) {
    if (preset.id === 'none' || preset.id === 'custom') continue
    if (preset.value.kind === 'rrule' && recurrenceStatesEqual(preset.value.state, value.state)) {
      return preset.id
    }
  }
  return 'custom'
}

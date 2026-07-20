/**
 * Pure event-mapping module for the workspace Calendar (FullCalendar v6).
 * Spec: docs/internal/specs/workspace-calendar-fullcalendar-spec.md (v2)
 * §6 canonical map, FR-002, FR-003, FR-004, FR-005, FR-015.
 *
 * Also implements the occurrence-mapping half of the Calendar Recurrence
 * Redesign (docs/internal/specs/calendar-recurrence-redesign-spec.md, User
 * Story 2, FR-008/009/012, Integration Boundaries → FullCalendar v6): given
 * the server-expanded `TaskOccurrenceSet[]` from `GET /api/v1/tasks/occurrences`
 * (fetched by `useOccurrences.ts`), joins them to the task list by `task_id`
 * and renders `task-occurrence` / `task-occurrence-agg` / `task-occurrence-more`
 * chips. This closes the F-10 v1 scope cut: recurring (`every`/`recurring`
 * trigger) tasks previously produced zero calendar events at all.
 *
 * No React, no Phosphor imports. Emits StatusIconKey strings only.
 * not-wire-format: internal view model; never sent over the network. The
 * `TaskOccurrenceSet`/`DayBucket` input types ARE wire types (contract-first
 * #8) — imported directly from the generated openapi-types, never redeclared.
 */

import type { EventInput } from '@fullcalendar/core'
import type { Task, Milestone } from '@/lib/api'
import type { TaskOccurrenceSet, DayBucket, TaskRun } from '@/lib/api/generated/openapi-types'
import {
  CHIP_TEXT_COLOR,
  STATUS_STYLE,
  STATUS_STYLE_FALLBACK,
  MILESTONE_STYLE,
  SCHEDULED_STYLE,
  NO_RECORD_STYLE,
  type CalendarEventExtProps,
  type ChipStyle,
  type OccurrenceChipStatus,
  type RunDerivedChipStatus,
} from '@/components/calendar/types'

/** One entry of `TaskOccurrenceSet.occurrence_runs[]` — the per-instant run overlay. */
type OccurrenceRunEntry = NonNullable<TaskOccurrenceSet['occurrence_runs']>[number]

/** `DayBucket.run_counts` — the per-day run-status tally overlay. */
type RunCounts = NonNullable<DayBucket['run_counts']>

// ─── TZ-safe date helpers (FR-015, F-06) ────────────────────────────────────

/**
 * Parse a date string TZ-safely.
 *
 * - Date-only `YYYY-MM-DD`: constructed via LOCAL component parts so there is
 *   no UTC-midnight off-by-one in any timezone (DS-1 row 9 / F-06).
 * - Datetime string (contains 'T' or a space): passed to `new Date()` which
 *   gives a wall-clock instant; the local date components are used for
 *   placement by FullCalendar.
 * - Null / undefined / invalid → null (never throws).
 */
export function parseLocalDate(s: string | null | undefined): Date | null {
  if (!s) return null
  // Detect date-only: exactly YYYY-MM-DD (10 chars, digit-dash pattern)
  if (/^\d{4}-\d{2}-\d{2}$/.test(s)) {
    const year = parseInt(s.slice(0, 4), 10)
    const month = parseInt(s.slice(5, 7), 10) - 1 // 0-indexed
    const day = parseInt(s.slice(8, 10), 10)
    if (isNaN(year) || isNaN(month) || isNaN(day)) return null
    const d = new Date(year, month, day)
    return isNaN(d.getTime()) ? null : d
  }
  // Datetime or other ISO string — use native parser
  const d = new Date(s)
  return isNaN(d.getTime()) ? null : d
}

/**
 * Format a Date to `YYYY-MM-DD` using LOCAL date components.
 * Used to write milestone `due_date` on drag and to seed the create-form date
 * prefill (FR-015, F-06). Task `due` is RFC3339 date-time and is written via
 * `toISOString()`, NOT this helper. Never uses UTC methods — no off-by-one.
 */
export function formatLocalDate(d: Date): string {
  const y = d.getFullYear()
  const m = String(d.getMonth() + 1).padStart(2, '0')
  const dd = String(d.getDate()).padStart(2, '0')
  return `${y}-${m}-${dd}`
}

/**
 * Parse a Unix epoch-milliseconds timestamp.
 * Returns null on missing / zero / NaN input (never throws).
 */
export function parseMs(ms: number | null | undefined): Date | null {
  if (ms == null || ms === 0) return null
  const d = new Date(ms)
  return isNaN(d.getTime()) ? null : d
}

// ─── Event builder ────────────────────────────────────────────────────────────

/**
 * Build the common FullCalendar EventInput shape — centralises the per-chip
 * invariant (near-black text, border = background) so it is stated once
 * (FR-005). `extendedProps` is typed against the discriminated
 * CalendarEventExtProps union: the single authoritative construction site, so a
 * mismatched/illegal extendedProps shape is now a compile error.
 *
 * `editable` defaults to `true` (drag enabled) so every pre-existing call site
 * (`:due`, `:fire`, `milestone`) is byte-for-byte unchanged. Recurring
 * occurrence/aggregated/truncation-marker chips pass `false` explicitly
 * (FR-012 — not draggable; the Explicit Non-Behaviors section forbids
 * per-occurrence drag-reschedule).
 */
function makeEvent(
  id: string,
  start: Date,
  title: string,
  bg: string,
  allDay: boolean,
  extendedProps: CalendarEventExtProps,
  editable: boolean = true,
): EventInput {
  return {
    id,
    allDay,
    start,
    title,
    backgroundColor: bg,
    borderColor: bg,
    textColor: CHIP_TEXT_COLOR,
    editable,
    extendedProps,
  }
}

// ─── Occurrence label/tooltip formatting (FR-009, MAJ-008) ────────────────────
//
// Pure display formatting of numbers the SERVER already computed
// (`DayBucket.interval_ms` / `.first_ms`) — NOT recurrence computation. The
// Explicit Non-Behaviors section forbids re-implementing cron/RRULE occurrence
// math client-side; picking a unit word for an already-known millisecond
// count, or formatting an already-known instant as HH:MM, is not that.

/**
 * Format a `DayBucket.interval_ms` as a short human label, e.g. `1_800_000` →
 * `"every 30 min"`. Picks the largest whole unit that evenly divides the
 * interval (day > hour > min > sec); falls back to a rounded second count for
 * a non-whole-unit or sub-minute interval — never throws.
 *
 * The sub-minute "sec" branch is NOT dead code: `DayBucket.interval_ms` is
 * populated for both `rrule` triggers (60s validation floor, so this branch
 * is unreachable for them) AND legacy `every`-type triggers, which validate
 * at a 1000ms floor (`TriggerEvery` in `pkg/task/store.go`) — a legacy
 * `every_ms: 5000` task legitimately hits this branch and renders "every 5
 * sec".
 */
export function formatIntervalLabel(intervalMs: number): string {
  const SEC = 1000
  const MIN = 60 * SEC
  const HOUR = 60 * MIN
  const DAY = 24 * HOUR
  if (intervalMs > 0 && intervalMs % DAY === 0) {
    const n = intervalMs / DAY
    return `every ${n} day${n === 1 ? '' : 's'}`
  }
  if (intervalMs > 0 && intervalMs % HOUR === 0) {
    const n = intervalMs / HOUR
    return `every ${n} hour${n === 1 ? '' : 's'}`
  }
  if (intervalMs > 0 && intervalMs % MIN === 0) {
    const n = intervalMs / MIN
    return `every ${n} min`
  }
  const n = Math.max(1, Math.round(intervalMs / SEC))
  return `every ${n} sec`
}

/**
 * Label for an aggregated `DayBucket` chip, e.g. `"· every 30 min"` or, when
 * the rule is irregular (`interval_ms === null`), `"· 4×/day"` (FR-009).
 */
export function formatBucketLabel(bucket: { count: number; interval_ms: number | null }): string {
  return bucket.interval_ms != null
    ? `· ${formatIntervalLabel(bucket.interval_ms)}`
    : `· ${bucket.count}×/day`
}

/** Format a Unix epoch-ms instant as a local 24h `HH:MM` string (tooltip use only, e.g. "first at 09:00"). */
export function formatTimeOfDay(ms: number): string {
  const d = new Date(ms)
  const hh = String(d.getHours()).padStart(2, '0')
  const mm = String(d.getMinutes()).padStart(2, '0')
  return `${hh}:${mm}`
}

/**
 * The last calendar day covered by a `TaskOccurrenceSet`'s returned data
 * (max of every raw instant and every bucket's `day_start_ms`) — where the
 * truncation marker renders when `truncated: true`. Reads only timestamps the
 * server already returned; not a recurrence computation. `null` when the set
 * carries no data at all (defensive — should not occur when `truncated`).
 */
export function lastCoveredOccurrenceDayMs(set: TaskOccurrenceSet): number | null {
  let max: number | null = null
  for (const ms of set.occurrences_ms) {
    if (max === null || ms > max) max = ms
  }
  for (const bucket of set.day_buckets) {
    if (max === null || bucket.day_start_ms > max) max = bucket.day_start_ms
  }
  return max
}

// ─── Per-occurrence run overlay — the four-state chip rule (ADR-050 RD6) ──────
// docs/internal/specs/task-run-history-spec.md §4.2. Occurrence chips STOP
// reading `task.status` — the render state is resolved from the additive
// `occurrence_runs[]` / `run_counts` overlay fields instead.

/**
 * Resolve a single `task-occurrence` chip's render state under the four-state
 * rule:
 *  - a matching run → the RUN's own status/color/glyph, plus its `runId`/
 *    `sessionId`/`hasResult` for the click-through (Open-in-Chat, spec §4.3).
 *  - no run, instant >= `nowMs` → `'scheduled'` (today's Clock chip).
 *  - no run, instant < `nowMs` → `'no_record'` — a distinct muted glyph,
 *    never rendered as a future "Scheduled" fire, with an explanatory tooltip.
 */
function resolveOccurrenceChipState(
  occurrenceMs: number,
  run: OccurrenceRunEntry | undefined,
  nowMs: number,
): {
  status: RunDerivedChipStatus
  style: ChipStyle
  runId?: string
  sessionId?: string
  hasResult?: boolean
  tooltip?: string
} {
  if (run) {
    return {
      status: run.status,
      style: STATUS_STYLE[run.status] ?? STATUS_STYLE_FALLBACK,
      runId: run.run_id,
      sessionId: run.session_id,
      hasResult: run.has_result,
    }
  }
  if (occurrenceMs >= nowMs) {
    return { status: 'scheduled', style: SCHEDULED_STYLE }
  }
  return {
    status: 'no_record',
    style: NO_RECORD_STYLE,
    tooltip: 'Run history unavailable — retention expired or the schedule changed since this ran.',
  }
}

/**
 * Whether run `a` should be preferred over run `b` when both are candidates
 * for the SAME `occurrence_ms` — byte-for-byte the same tie-break as the
 * server's `runIsNewer` (`pkg/gateway/task_occurrences.go`): the most
 * recently STARTED run wins; an exact `started_at` tie is broken by the
 * lexically-greater `run_id` (ULIDs are themselves time-ordered, so this is
 * also "most recent" on a tie). `started_at` is a fixed-width RFC 3339 UTC
 * string, which sorts lexically identically to chronological order — no
 * `Date` parsing needed (and comparing `Date` objects, as an earlier version
 * of this logic did in `CalendarEventSlideOver.tsx`, silently loses the
 * tie-break: `new Date(a) > new Date(b)` is `false` on an exact timestamp
 * match, so the FIRST-iterated run wins instead of the server's
 * greater-`run_id` rule).
 */
function isRunNewer(a: TaskRun, b: TaskRun): boolean {
  if (a.started_at !== b.started_at) return a.started_at > b.started_at
  return a.run_id > b.run_id
}

/**
 * Resolve the definitive `TaskRun` for a given occurrence instant out of a
 * task's full run list — latest-started-wins, server-tie-break-compatible
 * (`isRunNewer`, mirrors `runIsNewer`). More than one run can legitimately
 * share an `occurrence_ms`: a scheduled fire followed by a later manual
 * re-run of the SAME occurrence (ADR-050 RD7 — `OpenRun`'s idempotency guard
 * only prevents a duplicate CONCURRENTLY-OPEN run for a key, not a later
 * fresh run opened after the first one already closed). Pure, no fetch, no
 * React — the calendar slide-over (`CalendarEventSlideOver.tsx`) imports this
 * instead of re-implementing its own (previously non-server-matching)
 * reduce. `undefined` when no run in `runs` matches `occurrenceMs`.
 */
export function resolveRunForOccurrence(runs: TaskRun[], occurrenceMs: number): TaskRun | undefined {
  let best: TaskRun | undefined
  for (const run of runs) {
    if (run.occurrence_ms !== occurrenceMs) continue
    if (!best || isRunNewer(run, best)) {
      best = run
    }
  }
  return best
}

/**
 * Format a bucket's `run_counts` tally as a breakdown tooltip, e.g.
 * `"12 done · 2 failed · 26 scheduled"` (task-run-history-spec.md §4.2, BDD
 * scenario 9). Zero-count categories are omitted. The fixed presentation
 * order (done, failed, in_progress, scheduled) reproduces the spec's own
 * example wording exactly.
 */
export function buildRunCountsTooltip(counts: RunCounts): string {
  const parts: string[] = []
  if (counts.done > 0) parts.push(`${counts.done} done`)
  if (counts.failed > 0) parts.push(`${counts.failed} failed`)
  if (counts.in_progress > 0) parts.push(`${counts.in_progress} in progress`)
  if (counts.scheduled > 0) parts.push(`${counts.scheduled} scheduled`)
  return parts.join(' · ')
}

/**
 * Worst-wins glyph/status for an aggregated bucket: failed > in_progress >
 * done > scheduled. Never a single clean status — the bucket shows the WORST
 * outcome present that day, not an average or the first one.
 */
function resolveBucketWorstWins(counts: RunCounts): { status: OccurrenceChipStatus; style: ChipStyle } {
  if (counts.failed > 0) return { status: 'failed', style: STATUS_STYLE.failed }
  if (counts.in_progress > 0) return { status: 'in_progress', style: STATUS_STYLE.in_progress }
  if (counts.done > 0) return { status: 'done', style: STATUS_STYLE.done }
  return { status: 'scheduled', style: SCHEDULED_STYLE }
}

/**
 * Resolve a `task-occurrence-agg` bucket chip's render state. When the
 * overlay populated `run_counts`, renders the worst-wins glyph plus a
 * breakdown tooltip (never a single clean status).
 *
 * When `run_counts` is absent — the overlay handler never populated it for
 * this bucket, which happens precisely when NONE of that day's occurrences
 * have a recorded run yet (a bucket that has fired at least once always gets
 * a populated `run_counts`, see `populateBucketRunCounts` server-side) —
 * resolving to `task.status` would repaint every future bucket with the
 * SERIES' stale terminal status the moment occurrence #1 anywhere in the
 * series completes: exactly the bug ADR-050 exists to kill (BLOCKER). There
 * is no "fall back to today's rendering" clause in the spec to lean on here
 * — instead this resolves the SAME day-vs-now split individual instants use
 * (`resolveOccurrenceChipState`): the bucket's day >= `nowMs` → `'scheduled'`
 * (neutral, a future day nobody has run yet); day < `nowMs` → `'no_record'`
 * (a past day whose runs were pruned, or the schedule changed since).
 */
function resolveBucketChip(
  bucket: DayBucket,
  nowMs: number,
): { status: OccurrenceChipStatus; style: ChipStyle; tooltip: string } {
  if (!bucket.run_counts) {
    if (bucket.day_start_ms >= nowMs) {
      return {
        status: 'scheduled',
        style: SCHEDULED_STYLE,
        tooltip: `first at ${formatTimeOfDay(bucket.first_ms)}`,
      }
    }
    return {
      status: 'no_record',
      style: NO_RECORD_STYLE,
      tooltip: 'Run history unavailable — retention expired or the schedule changed since this ran.',
    }
  }
  const worst = resolveBucketWorstWins(bucket.run_counts)
  return { ...worst, tooltip: buildRunCountsTooltip(bucket.run_counts) }
}

// ─── Main mapping function ────────────────────────────────────────────────────

/**
 * Map workspace tasks, milestones, and (optionally) server-expanded recurring
 * occurrences into FullCalendar EventInput objects.
 *
 * Rules (§6, FR-002/003/004/005, F-01/F-06/F-19):
 *  - Tasks with `surface !== 'user'` (e.g. 'heartbeat') → excluded (FR-003, F-01).
 *  - Task with parseable `due` → all-day `:due` event coloured by status.
 *  - Task with `once` trigger and `config.at_ms` → timed `:fire` event, Clock icon.
 *  - A task may yield BOTH a `:due` and a `:fire` event (F-19).
 *  - Milestone with parseable `due_date` → all-day `:milestone` event, gold + Flag.
 *  - Unparseable dates → silently skipped, never throw (FR-003).
 *  - Due/fire/milestone events are `editable: true` (drag enabled) — unchanged
 *    regression baseline (Calendar Recurrence Redesign test 17).
 *
 * Occurrence rules (calendar-recurrence-redesign-spec.md, FR-008/009/012,
 * closes the F-10 v1 cut — `every`/`recurring` tasks no longer render zero
 * events): `occurrenceSets` (default `[]`, so existing 2-arg call sites are
 * unchanged) is joined to `tasks` by `task_id`:
 *  - A set whose `task_id` has no matching task is dropped entirely
 *    (defense-in-depth — the server's own selection predicate should already
 *    exclude it, but the client never trusts a dangling id).
 *  - Each raw instant in `occurrences_ms` → one timed `task-occurrence` chip
 *    (title = task title; time = the instant), colored/glyphed by the
 *    four-state run-overlay rule (ADR-050 RD6, task-run-history-spec.md
 *    §4.2 — `resolveOccurrenceChipState`), NOT by `task.status`.
 *  - Each `DayBucket` → one all-day `task-occurrence-agg` chip on
 *    `day_start_ms`, titled `"{task title} {label}"` where `label` is derived
 *    from `interval_ms` (`formatBucketLabel` — never recomputed client-side);
 *    colored/glyphed by the worst-wins `run_counts` rule when present, else
 *    the same day-vs-now scheduled/no_record split individual instants use
 *    — NEVER `task.status` (`resolveBucketChip`).
 *  - `truncated: true` → one all-day `task-occurrence-more` marker on the
 *    last covered day (`lastCoveredOccurrenceDayMs`) — never a silently
 *    emptier calendar beyond it.
 *  - All occurrence/aggregated/marker chips are `editable: false` (FR-012).
 */
export function mapToCalendarEvents(
  tasks: Task[],
  milestones: Milestone[],
  occurrenceSets: TaskOccurrenceSet[] = [],
  /**
   * F-SFH4 defensive fallback: the instant to place the truncation marker on
   * when `truncated: true` but the set carries NO data at all (empty
   * `occurrences_ms` and `day_buckets`, so `lastCoveredOccurrenceDayMs` has
   * nothing to compute from) — should not occur in practice, but must never
   * silently drop the marker (the whole point of `truncated` is that the
   * task has MORE occurrences than shown). Ideally the caller's own query
   * range start; defaults to "now" so a caller that hasn't threaded its
   * range through this optional param still never regresses to a vanished
   * task.
   */
  fallbackTruncationMarkerMs: number = Date.now(),
  /**
   * The "now" instant the four-state occurrence-chip rule (ADR-050 RD6,
   * task-run-history-spec.md §4.2) compares each run-less occurrence against
   * to pick `'scheduled'` (future) vs `'no_record'` (past). Injected — never
   * read via a bare `Date.now()` deep in this pure mapper — so callers (and
   * tests) can pin it deterministically. Defaults to real "now" so the one
   * production call site (`CalendarScreen`) needs no change.
   */
  nowMs: number = Date.now(),
): EventInput[] {
  const events: EventInput[] = []

  for (const task of tasks) {
    // In practice the only non-user surface is 'heartbeat'; allow-list on 'user'
    // to stay future-proof (FR-003, F-01).
    if (task.surface && task.surface !== 'user') continue

    const style = STATUS_STYLE[task.status] ?? STATUS_STYLE_FALLBACK

    // All-day :due event (FR-002).
    if (task.due) {
      const dueDate = parseLocalDate(task.due)
      if (dueDate !== null) {
        events.push(
          makeEvent(`task:${task.id}:due`, dueDate, task.title, style.bg, true, {
            kind: 'task-due',
            status: task.status,
            icon: style.icon,
            taskId: task.id,
          }),
        )
      }
    }

    // ── Timed :fire event for once triggers (FR-002, US-3) ───────────────────
    // `every` and `recurring` triggers never have a single `at_ms` — their
    // occurrences render via the separate occurrenceSets loop below instead
    // (closes the old F-10 v1 "recurring tasks are invisible" cut).
    if (task.trigger?.type === 'once' && task.trigger.config?.at_ms != null) {
      const fireDate = parseMs(task.trigger.config.at_ms)
      if (fireDate !== null) {
        events.push(
          makeEvent(`task:${task.id}:fire`, fireDate, task.title, style.bg, false, {
            kind: 'task-fire',
            status: task.status,
            icon: 'Clock', // always overrides the status icon on fire chips (§6)
            taskId: task.id,
          }),
        )
      }
    }
  }

  // ── Recurring occurrence chips (FR-008/009/012, closes F-10) ──────────────
  // Joined to `tasks` by `task_id`; a set for a task not in `tasks` is dropped
  // (defense-in-depth, per the endpoint's own task-selection predicate note).
  if (occurrenceSets.length > 0) {
    const taskById = new Map(tasks.map((t) => [t.id, t]))

    for (const set of occurrenceSets) {
      const task = taskById.get(set.task_id)
      if (!task) continue

      const style = STATUS_STYLE[task.status] ?? STATUS_STYLE_FALLBACK

      // Additive per-occurrence run overlay (ADR-050 RD6) — keyed by the
      // instant it matches. Absent/empty overlay → every instant falls
      // through to the scheduled/no_record branch below, which is the
      // correct four-state behaviour, not a special-cased fallback.
      const runsByOccurrenceMs = new Map<number, OccurrenceRunEntry>(
        (set.occurrence_runs ?? []).map((r) => [r.occurrence_ms, r]),
      )

      // Individual raw instants → timed task-occurrence chips, four-state
      // (spec §4.2): run status > scheduled (future, no run) > no_record
      // (past, no run) — never `task.status`.
      for (const ms of set.occurrences_ms) {
        const instant = parseMs(ms)
        if (instant === null) continue
        const chip = resolveOccurrenceChipState(ms, runsByOccurrenceMs.get(ms), nowMs)
        events.push(
          makeEvent(
            `task:${task.id}:occurrence:${ms}`,
            instant,
            task.title,
            chip.style.bg,
            false,
            {
              kind: 'task-occurrence',
              taskId: task.id,
              status: chip.status,
              icon: chip.style.icon,
              occurrenceMs: ms,
              runId: chip.runId,
              sessionId: chip.sessionId,
              hasResult: chip.hasResult,
              tooltip: chip.tooltip,
            },
            false,
          ),
        )
      }

      // Aggregated days → one all-day task-occurrence-agg chip per bucket,
      // worst-wins over `run_counts` when present (spec §4.2), else today's
      // pre-overlay rendering (task.status + Clock + "first at HH:MM").
      for (const bucket of set.day_buckets) {
        const dayStart = parseMs(bucket.day_start_ms)
        if (dayStart === null) continue
        const chip = resolveBucketChip(bucket, nowMs)
        events.push(
          makeEvent(
            `task:${task.id}:occurrence-agg:${bucket.day_start_ms}`,
            dayStart,
            `${task.title} ${formatBucketLabel(bucket)}`,
            chip.style.bg,
            true,
            {
              kind: 'task-occurrence-agg',
              taskId: task.id,
              status: chip.status,
              icon: chip.style.icon,
              tooltip: chip.tooltip,
              dayStartMs: bucket.day_start_ms,
              dayEndMs: bucket.day_end_ms,
            },
            false,
          ),
        )
      }

      // Truncated expansion → one non-interactive marker on the last covered
      // day, or the fallback instant (F-SFH4) when the set has no data at
      // all to compute a "last covered day" from — truncated must never mean
      // a task silently vanishes with no "more not shown" affordance.
      if (set.truncated) {
        const lastMs = lastCoveredOccurrenceDayMs(set) ?? fallbackTruncationMarkerMs
        const markerDate = parseMs(lastMs)
        if (markerDate !== null) {
          events.push(
            makeEvent(
              `task:${task.id}:occurrence-more`,
              markerDate,
              'More not shown',
              style.bg,
              true,
              {
                kind: 'task-occurrence-more',
                taskId: task.id,
                status: task.status,
                icon: 'Clock',
                tooltip: 'More occurrences not shown',
              },
              false,
            ),
          )
        }
      }
    }
  }

  // ── Milestone all-day events (FR-004) ────────────────────────────────────
  for (const m of milestones) {
    if (!m.due_date) continue
    const dueDate = parseLocalDate(m.due_date)
    if (dueDate === null) continue

    events.push(
      makeEvent(`milestone:${m.id}`, dueDate, m.name, MILESTONE_STYLE.bg, true, {
        kind: 'milestone',
        icon: 'Flag',
        milestoneId: m.id,
      }),
    )
  }

  return events
}

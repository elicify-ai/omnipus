/**
 * Unit tests for the per-occurrence run overlay — the four-state calendar
 * chip rule (ADR-050 RD6, docs/internal/specs/task-run-history-spec.md
 * §4.1/§4.2). Complements `eventMapping.occurrences.test.ts` (which covers
 * the underlying occurrence-chip mechanics: id/title/allDay/editable) with
 * dedicated coverage of the run-overlay states themselves:
 *
 *   1. individual instant WITH a matching run → run.status color/glyph +
 *      runId/sessionId/hasResult threaded into extendedProps.
 *   2. individual instant, no run, occurrence_ms >= now → 'scheduled' (Clock).
 *   3. individual instant, no run, occurrence_ms < now → 'no_record' (a
 *      distinct muted glyph) — and NEVER rendered as 'scheduled'.
 *   4. aggregated day bucket → worst-wins glyph (failed > skipped > in_progress
 *      > done > scheduled) + "N done · N failed · N skipped · N in progress ·
 *      N scheduled" tooltip breakdown.
 *   5. aggregated day bucket with no `run_counts` → falls back to today's
 *      pre-overlay rendering (task.status + Clock + "first at HH:MM").
 */

import { describe, it, expect } from 'vitest'
import { mapToCalendarEvents, buildRunCountsTooltip, resolveRunForOccurrence } from './eventMapping'
import type { Task } from '@/lib/api'
import type { TaskOccurrenceSet, TaskRun } from '@/lib/api/generated/openapi-types'

// ─── Factories (mirrors eventMapping.occurrences.test.ts) ─────────────────────

function makeTask(overrides: Partial<Task> = {}): Task {
  return {
    id: 'task-1',
    title: 'Weekly report',
    action: 'llm',
    status: 'next',
    priority: 3,
    workspace_id: 'ws-1',
    surface: 'user',
    ...overrides,
  } as Task
}

function makeOccurrenceSet(overrides: Partial<TaskOccurrenceSet> = {}): TaskOccurrenceSet {
  return {
    task_id: 'task-1',
    occurrences_ms: [],
    day_buckets: [],
    truncated: false,
    ...overrides,
  }
}

const NOW = new Date(2026, 6, 15, 12, 0, 0).getTime() // 2026-07-15 noon, fixed reference "now"

// ─── State 1: individual instant WITH a matching run ──────────────────────────

describe('four-state chip rule — state 1: instant with a matching run', () => {
  it('done run → status/icon from run.status, runId/sessionId/hasResult carried', () => {
    const occurrenceMs = NOW - 60_000 // in the past — matched run takes priority over no_record
    const task = makeTask()
    const set = makeOccurrenceSet({
      occurrences_ms: [occurrenceMs],
      occurrence_runs: [
        {
          occurrence_ms: occurrenceMs,
          status: 'done',
          run_id: 'run-abc',
          session_id: 'session-xyz',
          has_result: true,
        },
      ],
    })

    const events = mapToCalendarEvents([task], [], [set], NOW, NOW)
    expect(events).toHaveLength(1)
    const ext = events[0].extendedProps
    expect(ext?.kind).toBe('task-occurrence')
    expect(ext?.status).toBe('done')
    expect(ext?.icon).toBe('CheckCircle')
    expect(events[0].backgroundColor).toBe('#34D399')
    if (ext?.kind === 'task-occurrence') {
      expect(ext.occurrenceMs).toBe(occurrenceMs)
      expect(ext.runId).toBe('run-abc')
      expect(ext.sessionId).toBe('session-xyz')
      expect(ext.hasResult).toBe(true)
    }
  })

  it('failed run → XCircle glyph, red background', () => {
    const occurrenceMs = NOW - 120_000
    const task = makeTask()
    const set = makeOccurrenceSet({
      occurrences_ms: [occurrenceMs],
      occurrence_runs: [
        { occurrence_ms: occurrenceMs, status: 'failed', run_id: 'run-2', session_id: 's-2', has_result: true },
      ],
    })

    const events = mapToCalendarEvents([task], [], [set], NOW, NOW)
    expect(events[0].extendedProps?.status).toBe('failed')
    expect(events[0].extendedProps?.icon).toBe('XCircle')
    expect(events[0].backgroundColor).toBe('#F87171')
  })

  it('in_progress run → CircleNotch glyph, blue background, hasResult false/no result yet', () => {
    const occurrenceMs = NOW - 5_000
    const task = makeTask()
    const set = makeOccurrenceSet({
      occurrences_ms: [occurrenceMs],
      occurrence_runs: [
        { occurrence_ms: occurrenceMs, status: 'in_progress', run_id: 'run-3', session_id: 's-3', has_result: false },
      ],
    })

    const events = mapToCalendarEvents([task], [], [set], NOW, NOW)
    expect(events[0].extendedProps?.status).toBe('in_progress')
    expect(events[0].extendedProps?.icon).toBe('CircleNotch')
    expect(events[0].backgroundColor).toBe('#60A5FA')
    if (events[0].extendedProps?.kind === 'task-occurrence') {
      expect(events[0].extendedProps.hasResult).toBe(false)
    }
  })

  it('a run match is NOT overridden by task.status — different task.status, run still wins', () => {
    const occurrenceMs = NOW - 60_000
    const task = makeTask({ status: 'blocked' }) // deliberately mismatched vs the run
    const set = makeOccurrenceSet({
      occurrences_ms: [occurrenceMs],
      occurrence_runs: [
        { occurrence_ms: occurrenceMs, status: 'done', run_id: 'run-4', session_id: 's-4', has_result: true },
      ],
    })

    const events = mapToCalendarEvents([task], [], [set], NOW, NOW)
    expect(events[0].extendedProps?.status).toBe('done') // NOT 'blocked'
    expect(events[0].extendedProps?.icon).toBe('CheckCircle') // NOT 'Prohibit'
  })
})

// ─── State 2: no run, occurrence_ms >= now → 'scheduled' ──────────────────────

describe('four-state chip rule — state 2: no run, future instant', () => {
  it('renders "scheduled" (Clock), decoupled from task.status', () => {
    const occurrenceMs = NOW + 3_600_000 // 1h in the future
    const task = makeTask({ status: 'failed' }) // aggregate task.status must NOT leak through
    const set = makeOccurrenceSet({ occurrences_ms: [occurrenceMs] })

    const events = mapToCalendarEvents([task], [], [set], NOW, NOW)
    expect(events[0].extendedProps?.status).toBe('scheduled')
    expect(events[0].extendedProps?.icon).toBe('Clock')
    expect(events[0].backgroundColor).not.toBe('#F87171') // not task.status=failed's red
    if (events[0].extendedProps?.kind === 'task-occurrence') {
      expect(events[0].extendedProps.tooltip).toBeUndefined()
      expect(events[0].extendedProps.runId).toBeUndefined()
    }
  })

  it('an instant exactly equal to now is treated as scheduled (>= boundary)', () => {
    const task = makeTask()
    const set = makeOccurrenceSet({ occurrences_ms: [NOW] })
    const events = mapToCalendarEvents([task], [], [set], NOW, NOW)
    expect(events[0].extendedProps?.status).toBe('scheduled')
  })
})

// ─── State 3: no run, occurrence_ms < now → 'no_record' ───────────────────────

describe('four-state chip rule — state 3: no run, past instant ("no record")', () => {
  it('renders a distinct "no_record" state — not Clock, not a status color, with a tooltip', () => {
    const occurrenceMs = NOW - 3_600_000 // 1h in the past, no matching run
    const task = makeTask({ status: 'done' })
    const set = makeOccurrenceSet({ occurrences_ms: [occurrenceMs] })

    const events = mapToCalendarEvents([task], [], [set], NOW, NOW)
    const ext = events[0].extendedProps
    expect(ext?.status).toBe('no_record')
    expect(ext?.icon).not.toBe('Clock')
    expect(ext?.icon).toBe('Circle')
    expect(events[0].backgroundColor).not.toBe('#34D399') // not task.status=done's green
    if (ext?.kind === 'task-occurrence') {
      expect(ext.tooltip).toBe(
        'Run history unavailable — retention expired or the schedule changed since this ran.',
      )
    }
  })

  it('a past instant with no run is NEVER rendered as "scheduled"', () => {
    const occurrenceMs = NOW - 1
    const task = makeTask()
    const set = makeOccurrenceSet({ occurrences_ms: [occurrenceMs] })

    const events = mapToCalendarEvents([task], [], [set], NOW, NOW)
    expect(events[0].extendedProps?.status).not.toBe('scheduled')
    expect(events[0].extendedProps?.status).toBe('no_record')
  })

  it('the no_record glyph is visually distinct from the scheduled glyph on the same task', () => {
    const past = NOW - 60_000
    const future = NOW + 60_000
    const task = makeTask()
    const set = makeOccurrenceSet({ occurrences_ms: [past, future] })

    const events = mapToCalendarEvents([task], [], [set], NOW, NOW)
    const byMs = new Map(
      events
        .filter((e) => e.extendedProps?.kind === 'task-occurrence')
        .map((e) => [(e.extendedProps as { occurrenceMs: number }).occurrenceMs, e]),
    )
    expect(byMs.get(past)?.extendedProps?.icon).toBe('Circle')
    expect(byMs.get(future)?.extendedProps?.icon).toBe('Clock')
    expect(byMs.get(past)?.extendedProps?.icon).not.toBe(byMs.get(future)?.extendedProps?.icon)
  })
})

// ─── State 4: aggregated bucket — worst-wins ───────────────────────────────────

describe('four-state chip rule — state 4: aggregated bucket worst-wins', () => {
  it('BDD scenario 9: 12 done + 2 failed + 26 scheduled → failed glyph + exact tooltip breakdown', () => {
    const task = makeTask()
    const dayStart = new Date(2026, 6, 20).getTime()
    const set = makeOccurrenceSet({
      day_buckets: [
        {
          day_start_ms: dayStart,
          day_end_ms: dayStart + 86_400_000,
          count: 40,
          first_ms: dayStart,
          interval_ms: null,
          run_counts: { scheduled: 26, in_progress: 0, done: 12, failed: 2, skipped: 0 },
        },
      ],
    })

    const events = mapToCalendarEvents([task], [], [set], NOW, NOW)
    expect(events).toHaveLength(1)
    const ext = events[0].extendedProps
    expect(ext?.kind).toBe('task-occurrence-agg')
    expect(ext?.status).toBe('failed')
    expect(ext?.icon).toBe('XCircle')
    if (ext?.kind === 'task-occurrence-agg') {
      expect(ext.tooltip).toBe('12 done · 2 failed · 26 scheduled')
      expect(ext.dayStartMs).toBe(dayStart)
    }
  })

  it('worst-wins priority: in_progress beats done when no failures', () => {
    const task = makeTask()
    const dayStart = new Date(2026, 6, 21).getTime()
    const set = makeOccurrenceSet({
      day_buckets: [
        {
          day_start_ms: dayStart,
          day_end_ms: dayStart + 86_400_000,
          count: 10,
          first_ms: dayStart,
          interval_ms: null,
          run_counts: { scheduled: 0, in_progress: 3, done: 6, failed: 0, skipped: 0 },
        },
      ],
    })
    const events = mapToCalendarEvents([task], [], [set], NOW, NOW)
    expect(events[0].extendedProps?.status).toBe('in_progress')
    expect(events[0].extendedProps?.icon).toBe('CircleNotch')
  })

  it('worst-wins priority: done beats scheduled when nothing is active/failed', () => {
    const task = makeTask()
    const dayStart = new Date(2026, 6, 22).getTime()
    const set = makeOccurrenceSet({
      day_buckets: [
        {
          day_start_ms: dayStart,
          day_end_ms: dayStart + 86_400_000,
          count: 5,
          first_ms: dayStart,
          interval_ms: null,
          run_counts: { scheduled: 3, in_progress: 0, done: 2, failed: 0, skipped: 0 },
        },
      ],
    })
    const events = mapToCalendarEvents([task], [], [set], NOW, NOW)
    expect(events[0].extendedProps?.status).toBe('done')
    expect(events[0].extendedProps?.icon).toBe('CheckCircle')
  })

  it('worst-wins priority: all-scheduled bucket → scheduled glyph', () => {
    const task = makeTask()
    const dayStart = new Date(2026, 6, 23).getTime()
    const set = makeOccurrenceSet({
      day_buckets: [
        {
          day_start_ms: dayStart,
          day_end_ms: dayStart + 86_400_000,
          count: 4,
          first_ms: dayStart,
          interval_ms: null,
          run_counts: { scheduled: 4, in_progress: 0, done: 0, failed: 0, skipped: 0 },
        },
      ],
    })
    const events = mapToCalendarEvents([task], [], [set], NOW, NOW)
    expect(events[0].extendedProps?.status).toBe('scheduled')
    expect(events[0].extendedProps?.icon).toBe('Clock')
    expect(events[0].extendedProps?.kind === 'task-occurrence-agg' && events[0].extendedProps.tooltip).toBe(
      '4 scheduled',
    )
  })

  it('never renders "a single clean status" when the bucket is mixed — never just Done', () => {
    const task = makeTask({ status: 'done' })
    const dayStart = new Date(2026, 6, 24).getTime()
    const set = makeOccurrenceSet({
      day_buckets: [
        {
          day_start_ms: dayStart,
          day_end_ms: dayStart + 86_400_000,
          count: 10,
          first_ms: dayStart,
          interval_ms: null,
          run_counts: { scheduled: 0, in_progress: 0, done: 9, failed: 1, skipped: 0 },
        },
      ],
    })
    const events = mapToCalendarEvents([task], [], [set], NOW, NOW)
    // Even though 9/10 are done and task.status is 'done', the ONE failure wins.
    expect(events[0].extendedProps?.status).toBe('failed')
  })

  it('buildRunCountsTooltip omits zero-count categories, in fixed done/failed/skipped/in_progress/scheduled order', () => {
    expect(buildRunCountsTooltip({ scheduled: 0, in_progress: 0, done: 5, failed: 0, skipped: 0 })).toBe('5 done')
    expect(buildRunCountsTooltip({ scheduled: 0, in_progress: 2, done: 0, failed: 1, skipped: 0 })).toBe(
      '1 failed · 2 in progress',
    )
    expect(buildRunCountsTooltip({ scheduled: 0, in_progress: 0, done: 0, failed: 0, skipped: 3 })).toBe('3 skipped')
    expect(
      buildRunCountsTooltip({ scheduled: 5, in_progress: 1, done: 2, failed: 1, skipped: 4 }),
    ).toBe('2 done · 1 failed · 4 skipped · 1 in progress · 5 scheduled')
  })
})

// ─── State 5: skipped — a real TaskRun.status, its OWN chip and bucket rank ────
//
// `skipped` (backend overlap guard: a scheduled fire that never ran because
// the previous occurrence was still `in_progress`) is a genuine 5th
// `TaskRun.status` member — NOT a synthetic day-vs-now state like
// `scheduled`/`no_record`. It must resolve to its own distinct orange chip
// (never the grey `STATUS_STYLE_FALLBACK`), and rank directly below `failed`
// in the aggregated-bucket worst-wins order (above in_progress/done/scheduled).

describe('four-state chip rule — state 5: skipped (overlap-guard run outcome)', () => {
  it('individual instant with a matching skipped run → its own status/color/glyph, runId/sessionId carried', () => {
    const occurrenceMs = NOW - 60_000
    const task = makeTask({ status: 'next' }) // task.status must not leak through
    const set = makeOccurrenceSet({
      occurrences_ms: [occurrenceMs],
      occurrence_runs: [
        { occurrence_ms: occurrenceMs, status: 'skipped', run_id: 'run-skip', session_id: 's-skip', has_result: false },
      ],
    })

    const events = mapToCalendarEvents([task], [], [set], NOW, NOW)
    const ext = events[0].extendedProps
    expect(ext?.status).toBe('skipped')
    expect(ext?.icon).toBe('SkipForward')
    expect(events[0].backgroundColor).toBe('#F97316') // SKIPPED_STYLE.bg — the orange, not the grey fallback
    if (ext?.kind === 'task-occurrence') {
      expect(ext.runId).toBe('run-skip')
      expect(ext.sessionId).toBe('s-skip')
    }
  })

  it('a skipped run is NOT overridden by task.status, and never falls back to STATUS_STYLE_FALLBACK grey', () => {
    const occurrenceMs = NOW - 30_000
    const task = makeTask({ status: 'done' })
    const set = makeOccurrenceSet({
      occurrences_ms: [occurrenceMs],
      occurrence_runs: [
        { occurrence_ms: occurrenceMs, status: 'skipped', run_id: 'run-skip-2', session_id: 's-2', has_result: false },
      ],
    })

    const events = mapToCalendarEvents([task], [], [set], NOW, NOW)
    expect(events[0].extendedProps?.status).toBe('skipped') // NOT 'done'
    expect(events[0].backgroundColor).not.toBe('#94A3B8') // not the grey STATUS_STYLE_FALLBACK/NO_RECORD_STYLE
    expect(events[0].backgroundColor).not.toBe('#34D399') // not task.status=done's green
  })

  it('worst-wins priority: skipped beats in_progress/done/scheduled when there is no failure', () => {
    const task = makeTask()
    const dayStart = new Date(2026, 6, 27).getTime()
    const set = makeOccurrenceSet({
      day_buckets: [
        {
          day_start_ms: dayStart,
          day_end_ms: dayStart + 86_400_000,
          count: 10,
          first_ms: dayStart,
          interval_ms: null,
          run_counts: { scheduled: 2, in_progress: 3, done: 4, failed: 0, skipped: 1 },
        },
      ],
    })
    const events = mapToCalendarEvents([task], [], [set], NOW, NOW)
    const ext = events[0].extendedProps
    expect(ext?.status).toBe('skipped')
    expect(ext?.icon).toBe('SkipForward')
    if (ext?.kind === 'task-occurrence-agg') {
      expect(ext.tooltip).toBe('4 done · 1 skipped · 3 in progress · 2 scheduled')
    }
  })

  it('worst-wins priority: failed still beats skipped when both are present', () => {
    const task = makeTask()
    const dayStart = new Date(2026, 6, 28).getTime()
    const set = makeOccurrenceSet({
      day_buckets: [
        {
          day_start_ms: dayStart,
          day_end_ms: dayStart + 86_400_000,
          count: 10,
          first_ms: dayStart,
          interval_ms: null,
          run_counts: { scheduled: 0, in_progress: 0, done: 5, failed: 1, skipped: 4 },
        },
      ],
    })
    const events = mapToCalendarEvents([task], [], [set], NOW, NOW)
    expect(events[0].extendedProps?.status).toBe('failed')
    expect(events[0].extendedProps?.icon).toBe('XCircle')
  })
})

// ─── State 4b/4c: bucket with no run_counts — day-vs-now split, NEVER task.status ──
//
// BLOCKER regression coverage: `run_counts` is absent for EVERY future
// not-yet-fired dense bucket, so a naive `task.status` fallback would paint
// every future bucket with the series' stale terminal status the moment ONE
// occurrence anywhere in the series completes — the exact defect ADR-050
// exists to kill. `resolveBucketChip` must resolve the SAME day-vs-now split
// individual instants use instead.

describe('four-state chip rule — bucket fallback when run_counts is absent', () => {
  it('future bucket day (>= now), no run_counts → "scheduled", decoupled from task.status', () => {
    const task = makeTask({ status: 'failed' }) // deliberately mismatched — must NOT leak through
    const dayStart = new Date(2026, 6, 25).getTime() // after NOW (2026-07-15)
    const firstMs = new Date(2026, 6, 25, 9, 0, 0).getTime()
    const set = makeOccurrenceSet({
      day_buckets: [
        { day_start_ms: dayStart, day_end_ms: dayStart + 86_400_000, count: 48, first_ms: firstMs, interval_ms: 1_800_000 },
      ],
    })

    const events = mapToCalendarEvents([task], [], [set], NOW, NOW)
    const ext = events[0].extendedProps
    expect(ext?.status).toBe('scheduled') // NOT 'failed' (task.status)
    expect(ext?.icon).toBe('Clock')
    expect(events[0].backgroundColor).toBe('#94A3B8') // SCHEDULED_STYLE.bg, NOT task.status=failed's red
    expect(events[0].backgroundColor).not.toBe('#F87171')
    if (ext?.kind === 'task-occurrence-agg') {
      expect(ext.tooltip).toBe('first at 09:00')
    }
  })

  it('a bucket day exactly equal to now is treated as scheduled (>= boundary)', () => {
    const task = makeTask()
    const set = makeOccurrenceSet({
      day_buckets: [{ day_start_ms: NOW, day_end_ms: NOW + 86_400_000, count: 5, first_ms: NOW, interval_ms: null }],
    })
    const events = mapToCalendarEvents([task], [], [set], NOW, NOW)
    expect(events[0].extendedProps?.status).toBe('scheduled')
  })

  it('past bucket day (< now), no run_counts → "no_record", NEVER task.status and NEVER "scheduled"', () => {
    const task = makeTask({ status: 'done' }) // deliberately mismatched — must NOT leak through
    const dayStart = new Date(2026, 6, 10).getTime() // before NOW (2026-07-15)
    const firstMs = new Date(2026, 6, 10, 9, 0, 0).getTime()
    const set = makeOccurrenceSet({
      day_buckets: [
        { day_start_ms: dayStart, day_end_ms: dayStart + 86_400_000, count: 12, first_ms: firstMs, interval_ms: null },
      ],
    })

    const events = mapToCalendarEvents([task], [], [set], NOW, NOW)
    const ext = events[0].extendedProps
    expect(ext?.status).toBe('no_record') // NOT 'done' (task.status), NOT 'scheduled'
    expect(ext?.icon).toBe('Circle')
    expect(ext?.icon).not.toBe('Clock')
    expect(events[0].backgroundColor).not.toBe('#34D399') // not task.status=done's green
    if (ext?.kind === 'task-occurrence-agg') {
      expect(ext.tooltip).toBe(
        'Run history unavailable — retention expired or the schedule changed since this ran.',
      )
    }
  })
})

// ─── resolveRunForOccurrence (dedup extraction) ────────────────────────────────
//
// Server-tie-break-compatible latest-wins-by-started_at resolution, extracted
// so `CalendarEventSlideOver.tsx` can import a single implementation instead
// of re-deriving its own (an earlier version there compared `Date` objects
// and silently lost ties instead of breaking them by `run_id`, unlike the
// server's `runIsNewer`, pkg/gateway/task_occurrences.go).

function makeRun(overrides: Partial<TaskRun> = {}): TaskRun {
  return {
    run_id: 'run-1',
    task_id: 'task-1',
    occurrence_ms: NOW,
    status: 'done',
    session_id: 'session-1',
    kind: 'scheduled',
    started_at: '2026-07-15T09:00:00Z',
    ended_at: '2026-07-15T09:05:00Z',
    ...overrides,
  }
}

describe('resolveRunForOccurrence', () => {
  it('returns undefined when no run matches the occurrence_ms', () => {
    const runs = [makeRun({ occurrence_ms: NOW }), makeRun({ occurrence_ms: NOW + 1000 })]
    expect(resolveRunForOccurrence(runs, NOW + 9999)).toBeUndefined()
  })

  it('returns the single matching run', () => {
    const run = makeRun({ occurrence_ms: NOW, run_id: 'run-only' })
    expect(resolveRunForOccurrence([run], NOW)?.run_id).toBe('run-only')
  })

  it('latest-started-wins when a scheduler fire and a later manual re-run share an occurrence_ms', () => {
    const earlier = makeRun({
      run_id: 'run-early',
      occurrence_ms: NOW,
      started_at: '2026-07-15T09:00:00Z',
      status: 'failed',
    })
    const later = makeRun({
      run_id: 'run-late',
      occurrence_ms: NOW,
      started_at: '2026-07-15T10:00:00Z',
      status: 'done',
    })
    // Order-independent — try both array orderings.
    expect(resolveRunForOccurrence([earlier, later], NOW)?.run_id).toBe('run-late')
    expect(resolveRunForOccurrence([later, earlier], NOW)?.run_id).toBe('run-late')
  })

  it('an exact started_at tie is broken by the lexically-greater run_id (matches server runIsNewer)', () => {
    const a = makeRun({ run_id: 'run-aaa', occurrence_ms: NOW, started_at: '2026-07-15T09:00:00Z' })
    const b = makeRun({ run_id: 'run-bbb', occurrence_ms: NOW, started_at: '2026-07-15T09:00:00Z' })
    // 'run-bbb' > 'run-aaa' lexically — must win regardless of array order.
    expect(resolveRunForOccurrence([a, b], NOW)?.run_id).toBe('run-bbb')
    expect(resolveRunForOccurrence([b, a], NOW)?.run_id).toBe('run-bbb')
  })

  it('ignores runs for other occurrence_ms values', () => {
    const target = makeRun({ run_id: 'run-target', occurrence_ms: NOW })
    const other = makeRun({ run_id: 'run-other', occurrence_ms: NOW + 60_000 })
    expect(resolveRunForOccurrence([other, target], NOW)?.run_id).toBe('run-target')
  })
})

// ─── occurrenceMs / dayStartMs threading (spec §4.1) ───────────────────────────

describe('occurrenceMs / dayStartMs threading', () => {
  it('every task-occurrence chip carries its own occurrenceMs join key', () => {
    const task = makeTask()
    const a = NOW + 1000
    const b = NOW + 2000
    const set = makeOccurrenceSet({ occurrences_ms: [a, b] })
    const events = mapToCalendarEvents([task], [], [set], NOW, NOW)
    const ms = events.map((e) => (e.extendedProps as { occurrenceMs: number }).occurrenceMs).sort()
    expect(ms).toEqual([a, b])
  })

  it('every task-occurrence-agg chip carries its own dayStartMs join key', () => {
    const task = makeTask()
    const dayStart = new Date(2026, 6, 26).getTime()
    const set = makeOccurrenceSet({
      day_buckets: [
        { day_start_ms: dayStart, day_end_ms: dayStart + 86_400_000, count: 2, first_ms: dayStart, interval_ms: null },
      ],
    })
    const events = mapToCalendarEvents([task], [], [set], NOW, NOW)
    expect((events[0].extendedProps as { dayStartMs: number }).dayStartMs).toBe(dayStart)
  })
})

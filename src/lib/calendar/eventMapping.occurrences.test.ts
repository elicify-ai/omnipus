/**
 * Unit tests for the occurrence-mapping half of eventMapping.ts.
 * Spec: docs/internal/specs/calendar-recurrence-redesign-spec.md
 *   User Story 2, FR-008/009/012, Integration Boundaries → FullCalendar v6.
 *
 * Test 17 (`eventMapping.occurrences.test.ts`) per the spec's Test Data
 * Coverage Matrix:
 *   "instants/buckets/marker → chips (labels from interval_ms, "N×/day" when
 *   null); occurrence chips editable:false; due/fire chips unchanged
 *   (regression rows)."
 *
 * Traces:
 *   - "Weekly task renders on every Monday of the month" (BDD)
 *   - "Sub-daily task aggregates to one bucketed chip per day in Month view" (BDD)
 *   - "Truncated expansion is visibly flagged" (BDD)
 *   - "Clicking a due chip keeps today's panel" (BDD, regression)
 */

import { describe, it, expect } from 'vitest'
import {
  mapToCalendarEvents,
  formatIntervalLabel,
  formatBucketLabel,
  formatTimeOfDay,
  lastCoveredOccurrenceDayMs,
} from './eventMapping'
import type { Task, TaskTrigger } from '@/lib/api'
import type { TaskOccurrenceSet } from '@/lib/api/generated/openapi-types'

// ─── Factories ────────────────────────────────────────────────────────────────

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

function makeOnceTrigger(at_ms: number): TaskTrigger {
  return { type: 'once', config: { at_ms } }
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

// ─── Individual instants → task-occurrence chips ──────────────────────────────

describe('mapToCalendarEvents — occurrence instants (test 17)', () => {
  it('weekly task: 4 Monday instants render as 4 timed task-occurrence chips', () => {
    // "Weekly task renders on every Monday of the month" — 4 Mondays, 09:00 each.
    // `nowMs` pinned before all four instants (ADR-050 RD6 four-state rule,
    // task-run-history-spec.md §4.2): with no occurrence_runs overlay and every
    // instant in the future, they resolve to 'scheduled' — this test is about
    // chip MECHANICS (id/title/allDay/editable), not the run-overlay states,
    // which get their own dedicated coverage below.
    const mondays = [
      new Date(2026, 5, 1, 9, 0, 0).getTime(),
      new Date(2026, 5, 8, 9, 0, 0).getTime(),
      new Date(2026, 5, 15, 9, 0, 0).getTime(),
      new Date(2026, 5, 22, 9, 0, 0).getTime(),
    ]
    const nowMs = new Date(2026, 4, 1).getTime() // before every Monday above
    const task = makeTask({ title: 'Weekly report' })
    const set = makeOccurrenceSet({ occurrences_ms: mondays })

    const events = mapToCalendarEvents([task], [], [set], nowMs, nowMs)

    expect(events).toHaveLength(4)
    for (const [i, ev] of events.entries()) {
      expect(ev.id).toBe(`task:task-1:occurrence:${mondays[i]}`)
      expect(ev.title).toBe('Weekly report')
      expect(ev.allDay).toBe(false)
      expect(ev.start).toEqual(new Date(mondays[i]))
      expect(ev.editable).toBe(false)
      expect(ev.extendedProps?.kind).toBe('task-occurrence')
      expect(ev.extendedProps?.taskId).toBe('task-1')
      expect(ev.extendedProps?.status).toBe('scheduled')
      expect(ev.extendedProps?.icon).toBe('Clock')
      if (ev.extendedProps?.kind === 'task-occurrence') {
        expect(ev.extendedProps.occurrenceMs).toBe(mondays[i])
      }
    }
  })

  it('drops an unparseable/zero instant without throwing', () => {
    const task = makeTask()
    const set = makeOccurrenceSet({ occurrences_ms: [0, NaN] })
    expect(() => mapToCalendarEvents([task], [], [set])).not.toThrow()
    expect(mapToCalendarEvents([task], [], [set])).toHaveLength(0)
  })

  it('defense-in-depth: a set whose task_id has no matching task is dropped entirely', () => {
    const set = makeOccurrenceSet({
      task_id: 'ghost-task',
      occurrences_ms: [new Date(2026, 5, 1, 9, 0, 0).getTime()],
    })
    // No task list entry for 'ghost-task' at all.
    expect(mapToCalendarEvents([], [], [set])).toHaveLength(0)
  })

  it('occurrenceSets defaults to [] — existing 2-arg call sites are unaffected', () => {
    const task = makeTask({ due: undefined })
    expect(mapToCalendarEvents([task], [])).toHaveLength(0)
  })
})

// ─── DayBucket → task-occurrence-agg chips (labels) ────────────────────────────

describe('mapToCalendarEvents — aggregated day buckets (test 17)', () => {
  it('sub-daily task aggregates to one bucketed chip per day, labeled from interval_ms', () => {
    // "Sub-daily task aggregates to one bucketed chip per day in Month view" —
    // "Poll inbox" every 30 min: count 48, interval_ms 1_800_000.
    const task = makeTask({ id: 'task-2', title: 'Poll inbox', status: 'in_progress' })
    const dayStart = new Date(2026, 5, 1, 0, 0, 0).getTime()
    const firstMs = new Date(2026, 5, 1, 0, 0, 0).getTime()
    const set = makeOccurrenceSet({
      task_id: 'task-2',
      day_buckets: [{ day_start_ms: dayStart, count: 48, first_ms: firstMs, interval_ms: 1_800_000 }],
    })

    const events = mapToCalendarEvents([task], [], [set])

    expect(events).toHaveLength(1)
    const ev = events[0]
    expect(ev.id).toBe(`task:task-2:occurrence-agg:${dayStart}`)
    expect(ev.title).toBe('Poll inbox · every 30 min')
    expect(ev.allDay).toBe(true)
    expect(ev.start).toEqual(new Date(dayStart))
    expect(ev.editable).toBe(false)
    expect(ev.extendedProps?.kind).toBe('task-occurrence-agg')
    expect(ev.extendedProps?.taskId).toBe('task-2')
    expect(ev.extendedProps?.status).toBe('in_progress')
    if (ev.extendedProps?.kind === 'task-occurrence-agg') {
      expect(ev.extendedProps.tooltip).toBe('first at 00:00')
    }
  })

  it('irregular rule (interval_ms null) labels "{count}×/day"', () => {
    // "irregular rule BYHOUR=9,11,13,15 ... bucket interval_ms: null → client '4×/day'"
    const task = makeTask({ id: 'task-3', title: 'Check dashboards' })
    const dayStart = new Date(2026, 5, 1, 0, 0, 0).getTime()
    const firstMs = new Date(2026, 5, 1, 9, 0, 0).getTime()
    const set = makeOccurrenceSet({
      task_id: 'task-3',
      day_buckets: [{ day_start_ms: dayStart, count: 4, first_ms: firstMs, interval_ms: null }],
    })

    const events = mapToCalendarEvents([task], [], [set])

    expect(events).toHaveLength(1)
    expect(events[0].title).toBe('Check dashboards · 4×/day')
    if (events[0].extendedProps?.kind === 'task-occurrence-agg') {
      expect(events[0].extendedProps.tooltip).toBe('first at 09:00')
    }
  })

  it('formatIntervalLabel: whole units pick the largest evenly-dividing word', () => {
    expect(formatIntervalLabel(1_800_000)).toBe('every 30 min')
    expect(formatIntervalLabel(60_000)).toBe('every 1 min')
    expect(formatIntervalLabel(3_600_000)).toBe('every 1 hour')
    expect(formatIntervalLabel(7_200_000)).toBe('every 2 hours')
    expect(formatIntervalLabel(86_400_000)).toBe('every 1 day')
    expect(formatIntervalLabel(172_800_000)).toBe('every 2 days')
  })

  it('formatIntervalLabel: sub-minute interval (legacy `every_ms` 1000ms floor, F3) falls back to "sec"', () => {
    // Reachable for a legacy every_ms:5000 task (1000ms floor) — never for
    // rrule (60s floor). See the updated doc comment above the function.
    expect(formatIntervalLabel(5_000)).toBe('every 5 sec')
  })

  it('formatBucketLabel: "· " prefix, interval present vs null', () => {
    expect(formatBucketLabel({ count: 48, interval_ms: 1_800_000 })).toBe('· every 30 min')
    expect(formatBucketLabel({ count: 4, interval_ms: null })).toBe('· 4×/day')
  })

  it('formatTimeOfDay: zero-pads HH:MM from a Unix ms instant', () => {
    expect(formatTimeOfDay(new Date(2026, 5, 1, 9, 0, 0).getTime())).toBe('09:00')
    expect(formatTimeOfDay(new Date(2026, 5, 1, 0, 5, 0).getTime())).toBe('00:05')
  })
})

// ─── Truncated expansion → task-occurrence-more marker ────────────────────────

describe('mapToCalendarEvents — truncated expansion (test 17)', () => {
  it('truncated:true renders exactly one non-interactive marker on the last covered day', () => {
    // "Truncated expansion is visibly flagged" — every_ms=60000, 7-day range,
    // 500-instant cap hit; response covers only the first ~8 hours.
    const task = makeTask({ id: 'task-4', title: 'Every-minute poll' })
    const lastInstant = new Date(2026, 5, 1, 7, 59, 0).getTime()
    const set = makeOccurrenceSet({
      task_id: 'task-4',
      occurrences_ms: [new Date(2026, 5, 1, 0, 0, 0).getTime(), lastInstant],
      truncated: true,
    })

    const events = mapToCalendarEvents([task], [], [set])

    const markers = events.filter((e) => e.extendedProps?.kind === 'task-occurrence-more')
    expect(markers).toHaveLength(1)
    const marker = markers[0]
    expect(marker.id).toBe('task:task-4:occurrence-more')
    expect(marker.title).toBe('More not shown')
    expect(marker.allDay).toBe(true)
    expect(marker.editable).toBe(false)
    expect(marker.start).toEqual(new Date(lastInstant))
    expect(marker.extendedProps?.taskId).toBe('task-4')

    // Never a silently-emptier calendar: the raw instant chips still render too.
    const instantChips = events.filter((e) => e.extendedProps?.kind === 'task-occurrence')
    expect(instantChips).toHaveLength(2)
  })

  it('truncated:true with buckets: marker lands on the max of instants AND bucket day_starts', () => {
    const task = makeTask({ id: 'task-5' })
    const earlyInstant = new Date(2026, 5, 1, 9, 0, 0).getTime()
    const laterBucketDay = new Date(2026, 5, 3, 0, 0, 0).getTime()
    const set = makeOccurrenceSet({
      task_id: 'task-5',
      occurrences_ms: [earlyInstant],
      day_buckets: [{ day_start_ms: laterBucketDay, count: 10, first_ms: laterBucketDay, interval_ms: null }],
      truncated: true,
    })

    const events = mapToCalendarEvents([task], [], [set])
    const marker = events.find((e) => e.extendedProps?.kind === 'task-occurrence-more')
    expect(marker).toBeDefined()
    expect(marker!.start).toEqual(new Date(laterBucketDay))
  })

  it('truncated:true with an entirely empty set still renders the marker (F-SFH4) instead of vanishing', () => {
    // Defensive edge case: truncated:true but occurrences_ms and day_buckets
    // are both empty, so lastCoveredOccurrenceDayMs has nothing to compute
    // from. The marker must still render — on the caller-supplied fallback
    // (the query range's own start) — never silently dropped.
    const task = makeTask({ id: 'task-7' })
    const set = makeOccurrenceSet({
      task_id: 'task-7',
      occurrences_ms: [],
      day_buckets: [],
      truncated: true,
    })
    const fallbackMs = new Date(2026, 5, 1, 0, 0, 0).getTime()

    const events = mapToCalendarEvents([task], [], [set], fallbackMs)

    const markers = events.filter((e) => e.extendedProps?.kind === 'task-occurrence-more')
    expect(markers).toHaveLength(1)
    expect(markers[0].start).toEqual(new Date(fallbackMs))
  })

  it('truncated:true with an empty set and no fallback supplied still renders a marker (defaults to now)', () => {
    const task = makeTask({ id: 'task-8' })
    const set = makeOccurrenceSet({ task_id: 'task-8', occurrences_ms: [], day_buckets: [], truncated: true })

    const events = mapToCalendarEvents([task], [], [set])

    const markers = events.filter((e) => e.extendedProps?.kind === 'task-occurrence-more')
    expect(markers).toHaveLength(1)
    expect(markers[0].start).toBeInstanceOf(Date)
  })

  it('truncated:false renders no marker', () => {
    const task = makeTask({ id: 'task-6' })
    const set = makeOccurrenceSet({
      task_id: 'task-6',
      occurrences_ms: [new Date(2026, 5, 1, 9, 0, 0).getTime()],
      truncated: false,
    })
    const events = mapToCalendarEvents([task], [], [set])
    expect(events.some((e) => e.extendedProps?.kind === 'task-occurrence-more')).toBe(false)
  })

  it('lastCoveredOccurrenceDayMs returns null for a set with no data at all', () => {
    expect(lastCoveredOccurrenceDayMs(makeOccurrenceSet())).toBeNull()
  })

  it('lastCoveredOccurrenceDayMs picks the max across instants and buckets', () => {
    const a = 1000
    const b = 5000
    const c = 3000
    const set = makeOccurrenceSet({
      occurrences_ms: [a, c],
      day_buckets: [{ day_start_ms: b, count: 1, first_ms: b, interval_ms: null }],
    })
    expect(lastCoveredOccurrenceDayMs(set)).toBe(b)
  })
})

// ─── Regression: due / once fire chips unchanged (test 17) ────────────────────

describe('mapToCalendarEvents — due/fire chip regression (test 17)', () => {
  it('a due chip is unaffected by occurrenceSets and stays editable:true (drag-reschedule intact)', () => {
    const task = makeTask({ due: '2026-06-20', status: 'blocked' })
    const unrelatedSet = makeOccurrenceSet({ task_id: 'some-other-task', occurrences_ms: [12345] })

    const events = mapToCalendarEvents([task], [], [unrelatedSet])

    const dueChip = events.find((e) => e.extendedProps?.kind === 'task-due')
    expect(dueChip).toBeDefined()
    expect(dueChip!.editable).toBe(true)
    expect(dueChip!.extendedProps?.taskId).toBe('task-1')
  })

  it('a once-trigger fire chip is unaffected by occurrenceSets and stays editable:true', () => {
    const at_ms = new Date(2026, 5, 21, 9, 0, 0).getTime()
    const task = makeTask({ trigger: makeOnceTrigger(at_ms) })

    const events = mapToCalendarEvents([task], [], [])

    const fireChip = events.find((e) => e.extendedProps?.kind === 'task-fire')
    expect(fireChip).toBeDefined()
    expect(fireChip!.editable).toBe(true)
    expect(fireChip!.extendedProps?.icon).toBe('Clock')
  })

  it('a task with due + once-trigger + a recurring occurrence set yields all three kinds, independently editable', () => {
    const at_ms = new Date(2026, 5, 21, 9, 0, 0).getTime()
    const occurrenceMs = new Date(2026, 5, 22, 9, 0, 0).getTime()
    const task = makeTask({ due: '2026-06-20', trigger: makeOnceTrigger(at_ms) })
    const set = makeOccurrenceSet({ occurrences_ms: [occurrenceMs] })

    const events = mapToCalendarEvents([task], [], [set])

    expect(events).toHaveLength(3)
    const byKind = Object.fromEntries(events.map((e) => [e.extendedProps?.kind, e]))
    expect(byKind['task-due'].editable).toBe(true)
    expect(byKind['task-fire'].editable).toBe(true)
    expect(byKind['task-occurrence'].editable).toBe(false)
  })
})

/**
 * Unit tests for eventMapping.ts
 * Spec: docs/internal/specs/workspace-calendar-fullcalendar-spec.md (v2)
 * §9 tests #1–#9, DS-1 rows 1–10, plus TZ-safety helper tests.
 *
 * Covers:
 *  #1  due task → all-day chip {bg, icon, textColor, editable, id 'task:..:due'}
 *  #2  once-trigger → timed ':fire' chip with Clock icon override
 *  #3  every & recurring triggers → 0 events (deferred v1)
 *  #4  surface 'heartbeat' → 0 events
 *  #5  milestone → gold Flag chip
 *  #6  both due + once → 2 events (:due and :fire)
 *  #7  unparseable due → 0 events, no throw
 *  #8  date-only due parsed local (TZ-safe, no UTC off-by-one)
 *  #9  status→{bg,icon} for all 7 statuses (table-driven, DS-1/row 10)
 *  DS-1 TZ-SAFETY  parseLocalDate / formatLocalDate round-trip
 */

import { describe, it, expect } from 'vitest'
import {
  mapToCalendarEvents,
  parseLocalDate,
  formatLocalDate,
  parseMs,
} from './eventMapping'
import {
  CHIP_TEXT_COLOR,
  STATUS_STYLE,
  STATUS_STYLE_FALLBACK,
  MILESTONE_STYLE,
} from '@/components/calendar/types'
import type { Task, Milestone, TaskTrigger } from '@/lib/api'

// ─── Factories ────────────────────────────────────────────────────────────────

function makeTask(overrides: Partial<Task> = {}): Task {
  return {
    id: 'task-1',
    title: 'Test Task',
    action: 'llm',
    status: 'inbox',
    priority: 3,
    workspace_id: 'ws-1',
    surface: 'user',
    ...overrides,
  } as Task
}

function makeOnceTrigger(at_ms: number): TaskTrigger {
  return { type: 'once', config: { at_ms } }
}

function makeMilestone(overrides: Partial<Milestone> = {}): Milestone {
  return {
    id: 'ms-1',
    workspace_id: 'ws-1',
    name: 'Beta',
    created_at: '2026-06-01T00:00:00Z',
    updated_at: '2026-06-01T00:00:00Z',
    ...overrides,
  } as Milestone
}

// ─── Test #1: due task → all-day chip ────────────────────────────────────────

describe('mapToCalendarEvents', () => {
  it('#1 due task produces an all-day chip with correct shape', () => {
    const task = makeTask({ due: '2026-06-20', status: 'next' })
    const events = mapToCalendarEvents([task], [])

    expect(events).toHaveLength(1)
    const ev = events[0]

    expect(ev.id).toBe('task:task-1:due')
    expect(ev.allDay).toBe(true)
    expect(ev.title).toBe('Test Task')
    expect(ev.backgroundColor).toBe(STATUS_STYLE['next'].bg) // slate
    expect(ev.borderColor).toBe(STATUS_STYLE['next'].bg)
    expect(ev.textColor).toBe(CHIP_TEXT_COLOR)
    expect(ev.editable).toBe(true)
    expect(ev.extendedProps?.kind).toBe('task-due')
    expect(ev.extendedProps?.status).toBe('next')
    expect(ev.extendedProps?.icon).toBe('Circle')
    expect(ev.extendedProps?.taskId).toBe('task-1')
  })

  // ─── Test #2: once-trigger → timed :fire chip ──────────────────────────────

  it('#2 once-trigger produces a timed :fire chip with Clock icon', () => {
    const at_ms = new Date(2026, 5, 21, 9, 0, 0).getTime() // 2026-06-21T09:00 local
    const task = makeTask({ trigger: makeOnceTrigger(at_ms), status: 'in_progress' })
    const events = mapToCalendarEvents([task], [])

    expect(events).toHaveLength(1)
    const ev = events[0]

    expect(ev.id).toBe('task:task-1:fire')
    expect(ev.allDay).toBe(false)
    expect(ev.start).toEqual(new Date(at_ms))
    expect(ev.extendedProps?.kind).toBe('task-fire')
    expect(ev.extendedProps?.icon).toBe('Clock') // always Clock, overrides status icon
    expect(ev.extendedProps?.taskId).toBe('task-1')
    // background still uses status style (§6)
    expect(ev.backgroundColor).toBe(STATUS_STYLE['in_progress'].bg)
    expect(ev.textColor).toBe(CHIP_TEXT_COLOR)
    expect(ev.editable).toBe(true)
  })

  // ─── Test #3: every & recurring → 0 events ────────────────────────────────

  it('#3 every trigger produces no events (deferred v1)', () => {
    const task = makeTask({
      trigger: { type: 'every', config: { every_ms: 3_600_000 } },
    })
    expect(mapToCalendarEvents([task], [])).toHaveLength(0)
  })

  it('#3 recurring trigger produces no events (deferred v1)', () => {
    const task = makeTask({
      trigger: { type: 'recurring', config: { cron_expr: '0 9 * * MON' } },
    })
    expect(mapToCalendarEvents([task], [])).toHaveLength(0)
  })

  // ─── Test #4: surface 'heartbeat' → 0 events ──────────────────────────────

  it('#4 surface heartbeat is excluded', () => {
    const task = makeTask({ surface: 'heartbeat', due: '2026-06-20' })
    expect(mapToCalendarEvents([task], [])).toHaveLength(0)
  })

  // ─── Test #5: milestone → gold Flag chip ──────────────────────────────────

  it('#5 milestone with due_date produces a gold Flag all-day chip', () => {
    const m = makeMilestone({ due_date: '2026-06-25' })
    const events = mapToCalendarEvents([], [m])

    expect(events).toHaveLength(1)
    const ev = events[0]

    expect(ev.id).toBe('milestone:ms-1')
    expect(ev.allDay).toBe(true)
    expect(ev.title).toBe('Beta')
    expect(ev.backgroundColor).toBe(MILESTONE_STYLE.bg) // Forge Gold
    expect(ev.borderColor).toBe(MILESTONE_STYLE.bg)
    expect(ev.textColor).toBe(CHIP_TEXT_COLOR)
    expect(ev.editable).toBe(true)
    expect(ev.extendedProps?.kind).toBe('milestone')
    expect(ev.extendedProps?.icon).toBe('Flag')
    expect(ev.extendedProps?.milestoneId).toBe('ms-1')
  })

  // ─── Test #6: both due + once → 2 events ──────────────────────────────────

  it('#6 task with both due and once-trigger yields two events (:due and :fire)', () => {
    const at_ms = new Date(2026, 5, 21, 9, 0, 0).getTime()
    const task = makeTask({
      due: '2026-06-20',
      trigger: makeOnceTrigger(at_ms),
      status: 'next',
    })
    const events = mapToCalendarEvents([task], [])

    expect(events).toHaveLength(2)
    const ids = events.map((e) => e.id)
    expect(ids).toContain('task:task-1:due')
    expect(ids).toContain('task:task-1:fire')
  })

  // ─── Test #7: unparseable due → 0 events, no throw ────────────────────────

  it('#7 unparseable due date skips the event without throwing', () => {
    const task = makeTask({ due: 'not-a-date' })
    expect(() => mapToCalendarEvents([task], [])).not.toThrow()
    expect(mapToCalendarEvents([task], [])).toHaveLength(0)
  })

  it('#7 milestone with null due_date produces no event', () => {
    const m = makeMilestone({ due_date: null })
    expect(mapToCalendarEvents([], [m])).toHaveLength(0)
  })

  it('#7 milestone with undefined due_date produces no event', () => {
    const m = makeMilestone({ due_date: undefined })
    expect(mapToCalendarEvents([], [m])).toHaveLength(0)
  })

  // ─── Test #9: status→{bg,icon} for all 7 statuses (DS-1/row 10) ──────────

  it('#9 status→{bg,icon} table for all 7 task statuses', () => {
    const cases: Array<{
      status: Task['status']
      expectedBg: string
      expectedIcon: string
    }> = [
      { status: 'done', expectedBg: '#34D399', expectedIcon: 'CheckCircle' },
      { status: 'in_progress', expectedBg: '#60A5FA', expectedIcon: 'CircleNotch' },
      { status: 'blocked', expectedBg: '#FBBF24', expectedIcon: 'Prohibit' },
      { status: 'failed', expectedBg: '#F87171', expectedIcon: 'XCircle' },
      { status: 'inbox', expectedBg: '#94A3B8', expectedIcon: 'Circle' },
      { status: 'next', expectedBg: '#94A3B8', expectedIcon: 'Circle' },
      { status: 'planning', expectedBg: '#94A3B8', expectedIcon: 'Circle' },
    ]

    for (const { status, expectedBg, expectedIcon } of cases) {
      const task = makeTask({ status, due: '2026-06-20' })
      const events = mapToCalendarEvents([task], [])
      expect(events).toHaveLength(1)
      const ev = events[0]
      expect(ev.backgroundColor, `bg for ${status}`).toBe(expectedBg)
      expect(ev.extendedProps?.icon, `icon for ${status}`).toBe(expectedIcon)
    }
  })

  // ─── STATUS_STYLE_FALLBACK ─────────────────────────────────────────────────

  it('unknown status falls back to STATUS_STYLE_FALLBACK', () => {
    // Cast to bypass TS — simulating a future/unknown status value at runtime
    const task = makeTask({ status: 'unknown_status' as Task['status'], due: '2026-06-20' })
    const events = mapToCalendarEvents([task], [])
    expect(events).toHaveLength(1)
    expect(events[0].backgroundColor).toBe(STATUS_STYLE_FALLBACK.bg)
    expect(events[0].extendedProps?.icon).toBe(STATUS_STYLE_FALLBACK.icon)
  })
})

// ─── DS-1 TZ-SAFETY: parseLocalDate / formatLocalDate ────────────────────────

describe('parseLocalDate — TZ-safety (DS-1, F-06, #8)', () => {
  it('#8 date-only YYYY-MM-DD is parsed via LOCAL components (no UTC off-by-one)', () => {
    // This assertion is TZ-independent: we test getFullYear/getMonth/getDate
    // (local components), NOT UTC methods. If we had used new Date('2026-06-22')
    // (UTC), this would fail in timezones west of UTC (e.g. America/Los_Angeles
    // at UTC-7: UTC midnight → 2026-06-21 local).
    const d = parseLocalDate('2026-06-22')
    expect(d).not.toBeNull()
    expect(d!.getFullYear()).toBe(2026)
    expect(d!.getMonth()).toBe(5) // 0-indexed June
    expect(d!.getDate()).toBe(22)
  })

  it('returns null for null input', () => {
    expect(parseLocalDate(null)).toBeNull()
  })

  it('returns null for undefined input', () => {
    expect(parseLocalDate(undefined)).toBeNull()
  })

  it('returns null for empty string', () => {
    expect(parseLocalDate('')).toBeNull()
  })

  it('returns null for non-date garbage', () => {
    expect(parseLocalDate('not-a-date')).toBeNull()
  })

  it('parses a full datetime string without throwing', () => {
    const d = parseLocalDate('2026-06-22T10:30:00Z')
    expect(d).not.toBeNull()
    expect(isNaN(d!.getTime())).toBe(false)
  })
})

describe('formatLocalDate (DS-1, F-06)', () => {
  it('formats a local date to YYYY-MM-DD using LOCAL components', () => {
    // new Date(2026, 5, 22) = local June 22 2026 — getFullYear/Month/Date are local
    expect(formatLocalDate(new Date(2026, 5, 22))).toBe('2026-06-22')
  })

  it('zero-pads single-digit month and day', () => {
    expect(formatLocalDate(new Date(2026, 0, 5))).toBe('2026-01-05')
  })
})

describe('parseMs', () => {
  it('returns null for null', () => {
    expect(parseMs(null)).toBeNull()
  })

  it('returns null for undefined', () => {
    expect(parseMs(undefined)).toBeNull()
  })

  it('returns null for 0', () => {
    expect(parseMs(0)).toBeNull()
  })

  it('parses a valid epoch-ms timestamp', () => {
    const ms = new Date(2026, 5, 22, 9, 0, 0).getTime()
    const d = parseMs(ms)
    expect(d).not.toBeNull()
    expect(d!.getTime()).toBe(ms)
  })
})

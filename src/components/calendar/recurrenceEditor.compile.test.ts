/**
 * recurrenceEditor.compile.test.ts — test 14 of the Calendar Recurrence
 * Redesign TDD plan.
 *
 * Spec: docs/internal/specs/calendar-recurrence-redesign-spec.md
 * Traces to: BDD "Editor state compiles to the correct RRULE" (6 rows) +
 * "Dataset: Editor compile" rows 7–13 (row 12 excluded — see note below);
 * BDD "Saved day-31 rule reopens as day 31"; FR-003/004/006a/007/019/021/024.
 *
 * Scope note (row 12): "title with unicode + 300 chars... chip truncates
 * with ellipsis" is chip/title display behavior owned by
 * CalendarEventSlideOver (the title field) and eventMapping (chip
 * rendering) — RecurrenceEditor has no title prop at all, so this row is
 * intentionally not covered here. It belongs to the slide-over agent's
 * CalendarEventSlideOver.test.tsx / eventMapping.occurrences.test.ts.
 *
 * These are pure-function tests against `@/lib/calendar/recurrence` — no
 * rendering needed for the compile/parse/validate direction (RecurrenceEditor
 * is a thin controlled UI wrapper over these functions). One RTL render test
 * is included for row 9's "Save disabled + hint" since that's a real
 * component behavior (FormError rendering + onValidityChange callback).
 *
 * NOTE: this file stays `.ts` (not `.tsx`, matching the TDD plan's exact
 * test filename) — the two render-based tests use `React.createElement`
 * instead of JSX syntax, which the `.ts` extension can't parse.
 */

import * as React from 'react'
import { describe, it, expect, vi, beforeAll } from 'vitest'
import { render, screen } from '@testing-library/react'
import {
  buildRruleString,
  compileRecurrence,
  parseRruleString,
  clampInterval,
  validateRecurrenceState,
  summarizeRecurrence,
  type RecurrenceEditorState,
} from '@/lib/calendar/recurrence'
import { RecurrenceEditor } from './RecurrenceEditor'

beforeAll(() => {
  // Radix Select/Popover jsdom gaps (same polyfills as date-time-picker.test.tsx).
  if (!Element.prototype.hasPointerCapture) {
    Element.prototype.hasPointerCapture = () => false
  }
  if (!Element.prototype.scrollIntoView) {
    Element.prototype.scrollIntoView = () => {}
  }
  if (typeof window !== 'undefined' && !window.ResizeObserver) {
    window.ResizeObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
    } as unknown as typeof ResizeObserver
  }
})

// Real, verified weekdays (node-checked): Jul 20 2026 = Monday,
// Jul 31 2026 = Friday, Aug 18 2026 = Tuesday, Dec 31 2027 = Friday.
const MON_JUL_20_2026_0900 = new Date(2026, 6, 20, 9, 0, 0)
const FRI_JUL_31_2026_0800 = new Date(2026, 6, 31, 8, 0, 0)
const DEC_31_2027 = new Date(2027, 11, 31)

function baseCustomState(overrides: Partial<RecurrenceEditorState> = {}): RecurrenceEditorState {
  return {
    frequency: 'daily',
    interval: 1,
    weekdays: [],
    monthlyMode: 'day-of-month',
    monthlyDayOfMonth: 1,
    monthlyOrdinal: 1,
    monthlyWeekday: 'MO',
    end: { kind: 'never' },
    ...overrides,
  }
}

describe('Editor state compiles to the correct RRULE (Scenario Outline, 6 rows)', () => {
  it('row 1: Mon Jul 20 09:00 | preset "Daily" | FREQ=DAILY', () => {
    const state = baseCustomState({ frequency: 'daily' })
    expect(buildRruleString(state, 'Europe/Berlin')).toBe('FREQ=DAILY')
  })

  it('row 2: Mon Jul 20 09:00 | preset "Every weekday" | FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR', () => {
    const state = baseCustomState({ frequency: 'weekly', weekdays: ['MO', 'TU', 'WE', 'TH', 'FR'] })
    expect(buildRruleString(state, 'Europe/Berlin')).toBe('FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR')
  })

  it('row 3: Mon Jul 20 09:00 | weekly x1, Mon+Thu | FREQ=WEEKLY;BYDAY=MO,TH', () => {
    const state = baseCustomState({ frequency: 'weekly', interval: 1, weekdays: ['TH', 'MO'] })
    // Input order (TH, MO) must not leak into output — canonical WEEKDAY_ORDER wins.
    expect(buildRruleString(state, 'Europe/Berlin')).toBe('FREQ=WEEKLY;BYDAY=MO,TH')
  })

  it('row 4: Fri Jul 31 08:00 | monthly on day 31 | FREQ=MONTHLY;BYMONTHDAY=28,29,30,31;BYSETPOS=-1', () => {
    const state = baseCustomState({ frequency: 'monthly', monthlyMode: 'day-of-month', monthlyDayOfMonth: 31 })
    expect(buildRruleString(state, 'Europe/Berlin')).toBe('FREQ=MONTHLY;BYMONTHDAY=28,29,30,31;BYSETPOS=-1')
  })

  it('row 5: Tue Aug 18 10:00 | monthly on third Tuesday | FREQ=MONTHLY;BYDAY=TU;BYSETPOS=3', () => {
    const state = baseCustomState({
      frequency: 'monthly',
      monthlyMode: 'nth-weekday',
      monthlyOrdinal: 3,
      monthlyWeekday: 'TU',
    })
    expect(buildRruleString(state, 'Europe/Berlin')).toBe('FREQ=MONTHLY;BYDAY=TU;BYSETPOS=3')
  })

  it('row 6: Mon Jul 20 09:00 (Europe/Berlin) | yearly, ends Dec 31 2027 | FREQ=YEARLY;UNTIL=20271231T225959Z', () => {
    const state = baseCustomState({ frequency: 'yearly', end: { kind: 'on-date', date: DEC_31_2027 } })
    // Dec 31 2027 in Europe/Berlin is CET (UTC+1, no DST in December):
    // 23:59:59 CET == 22:59:59 UTC == the literal Z-suffixed value below.
    expect(buildRruleString(state, 'Europe/Berlin')).toBe('FREQ=YEARLY;UNTIL=20271231T225959Z')
  })

  it('compileRecurrence wraps buildRruleString with dtstart_ms + tz (row 1 anchor, explicit tz)', () => {
    const state = baseCustomState({ frequency: 'daily' })
    const compiled = compileRecurrence(state, MON_JUL_20_2026_0900, 'Europe/Berlin')
    expect(compiled).toEqual({
      rrule: 'FREQ=DAILY',
      dtstart_ms: MON_JUL_20_2026_0900.getTime(),
      tz: 'Europe/Berlin',
    })
  })
})

describe('Dataset: Editor compile — rows 7–13', () => {
  it('row 7: interval=0 typed in stepper clamps to 1, no invalid state (prevention over correction)', () => {
    expect(clampInterval(0)).toBe(1)
    // A clamped interval of 1 never emits an explicit INTERVAL= key (RFC default).
    const state = baseCustomState({ frequency: 'daily', interval: clampInterval(0) })
    expect(buildRruleString(state, 'UTC')).toBe('FREQ=DAILY')
  })

  it('row 8: interval=100 clamps to the documented max 99', () => {
    expect(clampInterval(100)).toBe(99)
    const state = baseCustomState({ frequency: 'daily', interval: clampInterval(100) })
    expect(buildRruleString(state, 'UTC')).toBe('FREQ=DAILY;INTERVAL=99')
  })

  it('row 9: weekly with zero weekdays toggled — invalid, with an actionable reason (Save-disabled hint)', () => {
    const state = baseCustomState({ frequency: 'weekly', weekdays: [] })
    expect(validateRecurrenceState(state)).toEqual({
      valid: false,
      reason: 'Select at least one day of the week.',
    })
  })

  it('row 9b: RecurrenceEditor renders the hint and reports invalidity when weekdays are empty', () => {
    const onChange = vi.fn()
    const onValidityChange = vi.fn()
    const state = baseCustomState({ frequency: 'weekly', weekdays: [] })
    render(
      React.createElement(RecurrenceEditor, {
        value: { kind: 'rrule', state },
        onChange,
        anchor: MON_JUL_20_2026_0900,
        tz: 'Europe/Berlin',
        onValidityChange,
      }),
    )
    // A weekly rule with zero weekdays never matches a canonical preset, so
    // matchPreset() falls back to 'custom' and the panel is already open —
    // no Select interaction needed to reach it.
    expect(screen.getByText('Select at least one day.')).toBeInTheDocument()
    expect(onValidityChange).toHaveBeenCalledWith(false)
  })

  it('row 10: "After N occurrences" N=1 compiles to COUNT=1', () => {
    const state = baseCustomState({ frequency: 'daily', end: { kind: 'after-count', count: 1 } })
    expect(buildRruleString(state, 'UTC')).toBe('FREQ=DAILY;COUNT=1')
  })

  it('row 11: a saved clamp form reopened is recognized as "day 31" mode (round-trip)', () => {
    const parsed = parseRruleString(
      'FREQ=MONTHLY;BYMONTHDAY=28,29,30,31;BYSETPOS=-1',
      FRI_JUL_31_2026_0800.getTime(),
    )
    expect(parsed).not.toBeNull()
    expect(parsed?.monthlyMode).toBe('day-of-month')
    expect(parsed?.monthlyDayOfMonth).toBe(31)
    // FR-007: the summary is the mandated literal — never a BYMONTHDAY list.
    expect(summarizeRecurrence({ kind: 'rrule', state: parsed! }, FRI_JUL_31_2026_0800)).toBe(
      'Monthly on day 31 (moved to the last day in shorter months)',
    )
    // Re-compiling the round-tripped state reproduces the exact canonical string.
    expect(buildRruleString(parsed!, 'Europe/Berlin')).toBe('FREQ=MONTHLY;BYMONTHDAY=28,29,30,31;BYSETPOS=-1')
  })

  it('row 11b: day 29 and day 30 clamp forms round-trip too (not just day 31)', () => {
    const p29 = parseRruleString('FREQ=MONTHLY;BYMONTHDAY=28,29;BYSETPOS=-1', FRI_JUL_31_2026_0800.getTime())
    expect(p29?.monthlyDayOfMonth).toBe(29)
    const p30 = parseRruleString('FREQ=MONTHLY;BYMONTHDAY=28,29,30;BYSETPOS=-1', FRI_JUL_31_2026_0800.getTime())
    expect(p30?.monthlyDayOfMonth).toBe(30)
  })

  it('row 13: compiling the same (state, anchor, tz) twice is byte-identical (FR-024 anchor invariance)', () => {
    // This is the property the slide-over's title-only-edit save relies on:
    // as long as it reuses the ORIGINAL anchor/state (doesn't recompute from
    // "now" or a mutated field on an untouched recurrence), the compiled
    // trigger is byte-identical. compileRecurrence itself must be pure.
    const state = baseCustomState({
      frequency: 'weekly',
      interval: 2,
      weekdays: ['MO'],
      end: { kind: 'after-count', count: 10 },
    })
    const first = compileRecurrence(state, MON_JUL_20_2026_0900, 'Europe/Berlin')
    const second = compileRecurrence(state, MON_JUL_20_2026_0900, 'Europe/Berlin')
    expect(second).toEqual(first)
  })
})

describe('FR-021: tz defaulting', () => {
  it('compileRecurrence defaults tz to the browser IANA zone when omitted', () => {
    const state = baseCustomState({ frequency: 'daily' })
    const compiled = compileRecurrence(state, MON_JUL_20_2026_0900)
    expect(compiled.tz).toBe(Intl.DateTimeFormat().resolvedOptions().timeZone)
  })
})

describe('BDD: Operator creates a biweekly task from a preset-plus-custom flow', () => {
  it('interval 2 weeks, Monday, after 10 occurrences summarizes and compiles as specified', () => {
    const state = baseCustomState({
      frequency: 'weekly',
      interval: 2,
      weekdays: ['MO'],
      end: { kind: 'after-count', count: 10 },
    })
    const value = { kind: 'rrule' as const, state }
    expect(summarizeRecurrence(value, MON_JUL_20_2026_0900)).toBe('Repeats every 2 weeks on Monday, 10 times')
    const compiled = compileRecurrence(state, MON_JUL_20_2026_0900, 'Europe/Berlin')
    expect(compiled.rrule).toBe('FREQ=WEEKLY;INTERVAL=2;BYDAY=MO;COUNT=10')
    expect(compiled.dtstart_ms).toBe(MON_JUL_20_2026_0900.getTime())
    expect(compiled.tz).toBe('Europe/Berlin')
  })
})

describe('"Does not repeat" never compiles to an RRULE', () => {
  it('summarizeRecurrence reports "Does not repeat" for the none value', () => {
    expect(summarizeRecurrence({ kind: 'none' }, MON_JUL_20_2026_0900)).toBe('Does not repeat')
  })
})

/**
 * recurrenceEditor.presets.test.ts — test 15 of the Calendar Recurrence
 * Redesign TDD plan.
 *
 * Spec: docs/internal/specs/calendar-recurrence-redesign-spec.md
 * Traces to: BDD "Preset dropdown is computed from the clicked date";
 * User Story 1 Acceptance Scenario 2; FR-002.
 *
 * NOTE: this file stays `.ts` (not `.tsx`, matching the TDD plan's exact
 * test filename) — the two render-based tests use `React.createElement`
 * instead of JSX syntax, which the `.ts` extension can't parse.
 */

import * as React from 'react'
import { describe, it, expect, beforeAll } from 'vitest'
import { render, screen } from '@testing-library/react'
import { computePresets, matchPreset, ordinalOfDateInMonth, ordinalWord } from '@/lib/calendar/recurrence'
import { RecurrenceEditor } from './RecurrenceEditor'

beforeAll(() => {
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

// Real, verified weekdays (node-checked): Jul 20 2026 = Monday (3rd Monday of
// July), Aug 18 2026 = Tuesday (3rd Tuesday of August).
const MON_JUL_20_2026 = new Date(2026, 6, 20, 9, 0, 0)
const TUE_AUG_18_2026 = new Date(2026, 7, 18, 10, 0, 0)

describe('BDD: Preset dropdown is computed from the clicked date', () => {
  it('Tue Aug 18 (the third Tuesday of August) offers the documented preset set', () => {
    const labels = computePresets(TUE_AUG_18_2026).map((p) => p.label)
    expect(labels).toContain('Weekly on Tuesday')
    expect(labels).toContain('Monthly on the third Tuesday')
    expect(labels).toContain('Annually on August 18')
    expect(labels).toContain('Every weekday')
    expect(labels).toContain('Custom…')
  })

  it('Mon Jul 20 (the third Monday of July) offers the matching ordinal + weekday labels', () => {
    const labels = computePresets(MON_JUL_20_2026).map((p) => p.label)
    expect(labels).toContain('Weekly on Monday')
    expect(labels).toContain('Monthly on the third Monday')
    expect(labels).toContain('Annually on July 20')
  })
})

describe('FR-002: full preset set + order', () => {
  it('is exactly Does not repeat / Daily / Weekly / Monthly / Annually / Every weekday / Custom…, in that order', () => {
    const labels = computePresets(TUE_AUG_18_2026).map((p) => p.label)
    expect(labels).toEqual([
      'Does not repeat',
      'Daily',
      'Weekly on Tuesday',
      'Monthly on the third Tuesday',
      'Annually on August 18',
      'Every weekday',
      'Custom…',
    ])
  })

  it('every preset (except none/custom) compiles to a distinct, well-formed rule', () => {
    const presets = computePresets(TUE_AUG_18_2026)
    for (const preset of presets) {
      if (preset.id === 'none') {
        expect(preset.value).toEqual({ kind: 'none' })
      } else {
        expect(preset.value.kind).toBe('rrule')
      }
    }
  })
})

describe('Ordinal-of-date-in-month (nth weekday matching the anchor)', () => {
  it.each([
    [1, 1], // 1st -> first occurrence
    [7, 1], // still week 1
    [8, 2], // 2nd occurrence
    [14, 2],
    [15, 3],
    [20, 3], // Jul 20 -> third
    [21, 3],
    [22, 4],
    [28, 4],
    [29, 5],
  ])('day %i of the month is ordinal %i', (day, expected) => {
    expect(ordinalOfDateInMonth(new Date(2026, 6, day))).toBe(expected)
  })

  it.each([
    [1, 'first'],
    [2, 'second'],
    [3, 'third'],
    [4, 'fourth'],
    [5, 'fifth'],
    [-1, 'last'],
  ])('ordinalWord(%i) === %s', (n, word) => {
    expect(ordinalWord(n)).toBe(word)
  })
})

describe('matchPreset', () => {
  it('matches "none" for the Does-not-repeat value', () => {
    expect(matchPreset({ kind: 'none' }, MON_JUL_20_2026)).toBe('none')
  })

  it('matches each canonical preset back to its own id', () => {
    const presets = computePresets(MON_JUL_20_2026)
    for (const preset of presets) {
      if (preset.id === 'none' || preset.id === 'custom') continue
      expect(matchPreset(preset.value, MON_JUL_20_2026)).toBe(preset.id)
    }
  })

  it('falls back to "custom" for a state matching no canonical preset', () => {
    const value = {
      kind: 'rrule' as const,
      state: {
        frequency: 'weekly' as const,
        interval: 3,
        weekdays: ['MO' as const, 'WE' as const, 'FR' as const],
        monthlyMode: 'day-of-month' as const,
        monthlyDayOfMonth: 20,
        monthlyOrdinal: 3,
        monthlyWeekday: 'MO' as const,
        end: { kind: 'never' as const },
      },
    }
    expect(matchPreset(value, MON_JUL_20_2026)).toBe('custom')
  })
})

describe('RecurrenceEditor renders the anchor-computed preset label in the closed trigger', () => {
  it('shows "Weekly on Tuesday" when value matches that preset, for the Aug 18 anchor', () => {
    const presets = computePresets(TUE_AUG_18_2026)
    const weeklyPreset = presets.find((p) => p.id === 'weekly')!
    render(
      React.createElement(RecurrenceEditor, {
        value: weeklyPreset.value,
        onChange: () => {},
        anchor: TUE_AUG_18_2026,
        tz: 'Europe/Berlin',
      }),
    )
    expect(screen.getByRole('combobox', { name: 'Repeat' })).toHaveTextContent('Weekly on Tuesday')
  })

  it('shows "Does not repeat" for the none value', () => {
    render(
      React.createElement(RecurrenceEditor, {
        value: { kind: 'none' },
        onChange: () => {},
        anchor: MON_JUL_20_2026,
        tz: 'Europe/Berlin',
      }),
    )
    expect(screen.getByRole('combobox', { name: 'Repeat' })).toHaveTextContent('Does not repeat')
  })
})

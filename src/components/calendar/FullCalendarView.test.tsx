/**
 * Unit tests for FullCalendarView.tsx's pure chip-label helpers.
 *
 * jsdom cannot lay out FullCalendar's own DOM (see the comment atop
 * CalendarScreen.occurrencesDegrade.test.tsx — FullCalendarView is mocked at
 * the module boundary in every screen-level test), so `EventChip`'s render
 * output isn't practically unit-testable end to end here. `occurrenceStatusLabel`
 * and `extTooltip` are exported specifically so the two reviewer-found bugs
 * they fix get direct coverage without needing a real FullCalendar render:
 *
 *  - H3 (accessibility): occurrence chips were announcing as "Inbox" to
 *    screen readers because `statusLabel` (src/lib/statusColors.ts) only
 *    knows the canonical 7-member TaskStatus enum and silently falls back to
 *    STATUS_LABELS.inbox for the ADR-050 synthetic states 'scheduled'/
 *    'no_record' — not real TaskStatus members.
 *  - L3 (tooltip): eventMapping.ts populates `ext.tooltip` (no-record
 *    explanation, bucket worst-wins breakdown, "first at HH:MM") but nothing
 *    read it, so it was invisible (BDD #9).
 */

import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { SkipForward, Prohibit } from '@phosphor-icons/react'
import { occurrenceStatusLabel, extTooltip, NowMarkerLine, DayCellRenderer, EventChip } from './FullCalendarView'
import { CHIP_TEXT_COLOR, type CalendarEventExtProps } from './types'
import type { DayCellContentArg, EventContentArg } from '@fullcalendar/core'

function fakeEventContentArg(ext: CalendarEventExtProps, timeText = ''): EventContentArg {
  return {
    event: {
      title: 'Should never be read for now-marker',
      allDay: false,
      backgroundColor: '#D4AF37',
      extendedProps: ext,
    },
    timeText,
  } as unknown as EventContentArg
}

function fakeDayCellArg(overrides: {
  isToday?: boolean
  view: { type: string }
}): DayCellContentArg {
  return {
    date: new Date('2026-07-20T00:00:00Z'),
    dayNumberText: '20',
    isToday: false,
    isPast: false,
    isFuture: false,
    isOther: false,
    isDisabled: false,
    ...overrides,
  } as unknown as DayCellContentArg
}

describe('occurrenceStatusLabel (H3 — accessible name)', () => {
  it('maps the two ADR-050 synthetic states to their own human labels', () => {
    expect(occurrenceStatusLabel('scheduled')).toBe('Scheduled')
    expect(occurrenceStatusLabel('no_record')).toBe('No record')
  })

  it('delegates every real TaskStatus value to statusLabel, unchanged', () => {
    expect(occurrenceStatusLabel('done')).toBe('Done')
    expect(occurrenceStatusLabel('in_progress')).toBe('In Progress')
    expect(occurrenceStatusLabel('failed')).toBe('Failed')
    expect(occurrenceStatusLabel('blocked')).toBe('Blocked')
    expect(occurrenceStatusLabel('inbox')).toBe('Inbox')
    expect(occurrenceStatusLabel('next')).toBe('Next')
    expect(occurrenceStatusLabel('planning')).toBe('Planning')
  })

  it('never silently mislabels "scheduled"/"no_record" as "Inbox" (the H3 regression)', () => {
    expect(occurrenceStatusLabel('scheduled')).not.toBe('Inbox')
    expect(occurrenceStatusLabel('no_record')).not.toBe('Inbox')
  })

  // `skipped` (backend overlap-guard run outcome) is a real `TaskRun.status`
  // value, not a `TaskStatus` member — `statusLabel` doesn't know it and
  // would silently fall back to `STATUS_LABELS.inbox` ("Inbox") exactly like
  // the H3 bug above. `occurrenceStatusLabel` must special-case it the same
  // way it special-cases 'scheduled'/'no_record'.
  it('maps "skipped" to its own human label, not the statusLabel fallback', () => {
    expect(occurrenceStatusLabel('skipped')).toBe('Skipped')
    expect(occurrenceStatusLabel('skipped')).not.toBe('Inbox')
  })
})

describe('extTooltip (L3 — chip tooltip surfacing)', () => {
  it('reads tooltip off a task-occurrence with one (no-record explanation)', () => {
    const ext: CalendarEventExtProps = {
      kind: 'task-occurrence',
      taskId: 't1',
      status: 'no_record',
      icon: 'Circle',
      occurrenceMs: 1000,
      tooltip: 'Run history unavailable — retention expired or the schedule changed since this ran.',
    }
    expect(extTooltip(ext)).toBe(
      'Run history unavailable — retention expired or the schedule changed since this ran.',
    )
  })

  it('returns undefined for a task-occurrence with no tooltip (e.g. the "scheduled" state)', () => {
    const ext: CalendarEventExtProps = {
      kind: 'task-occurrence',
      taskId: 't1',
      status: 'scheduled',
      icon: 'Clock',
      occurrenceMs: 1000,
    }
    expect(extTooltip(ext)).toBeUndefined()
  })

  it('reads the required tooltip off a task-occurrence-agg bucket', () => {
    const ext: CalendarEventExtProps = {
      kind: 'task-occurrence-agg',
      taskId: 't1',
      status: 'failed',
      icon: 'XCircle',
      tooltip: '12 done · 2 failed · 26 scheduled',
      dayStartMs: 2000,
      dayEndMs: 2000 + 24 * 60 * 60 * 1000,
    }
    expect(extTooltip(ext)).toBe('12 done · 2 failed · 26 scheduled')
  })

  it('reads the tooltip off a task-occurrence-more truncation marker', () => {
    const ext: CalendarEventExtProps = {
      kind: 'task-occurrence-more',
      taskId: 't1',
      status: 'next',
      icon: 'Clock',
      tooltip: 'More occurrences not shown',
    }
    expect(extTooltip(ext)).toBe('More occurrences not shown')
  })

  it('returns undefined for kinds with no tooltip field at all (task-due, task-fire, milestone)', () => {
    const due: CalendarEventExtProps = { kind: 'task-due', taskId: 't1', status: 'next', icon: 'Circle' }
    const fire: CalendarEventExtProps = { kind: 'task-fire', taskId: 't1', status: 'next', icon: 'Clock' }
    const milestone: CalendarEventExtProps = { kind: 'milestone', milestoneId: 'm1', icon: 'Flag' }
    expect(extTooltip(due)).toBeUndefined()
    expect(extTooltip(fire)).toBeUndefined()
    expect(extTooltip(milestone)).toBeUndefined()
  })
})

/**
 * NowMarkerLine — the Agenda (listWeek) "now" divider EventChip branches to
 * instead of the normal chip for a `kind: 'now-marker'` synthetic event (see
 * CalendarScreen — appended to `events` only while Agenda is active and "now"
 * falls inside the visible range). Directly rendered here (rather than via a
 * mounted FullCalendar) for the same reason `occurrenceStatusLabel`/
 * `extTooltip` above are exported for direct testing — jsdom cannot lay out
 * FullCalendar's own DOM, but NowMarkerLine itself is a plain, isolated React
 * component with no FullCalendar dependency, so it renders fine standalone.
 */
describe('NowMarkerLine (Agenda "now" divider)', () => {
  it('is aria-hidden — a pure visual divider, not a real list item announced to screen readers', () => {
    const { container } = render(<NowMarkerLine timeText="3:05 PM" />)
    const line = container.querySelector('.fc-sovereign-now-marker-line')
    expect(line).not.toBeNull()
    expect(line).toHaveAttribute('aria-hidden', 'true')
  })

  it('renders the given time text as its label, matching Week/Day\'s time format verbatim', () => {
    render(<NowMarkerLine timeText="3:05 PM" />)
    expect(screen.getByText('3:05 PM')).toBeInTheDocument()
  })

  it('does NOT render the normal chip structure (no aria-label, no fc-sovereign-chip class, no icon)', () => {
    const { container } = render(<NowMarkerLine timeText="3:05 PM" />)
    // The normal EventChip root carries `className="fc-sovereign-chip"` and a
    // real `aria-label` (title + status + time) — this divider has neither.
    expect(container.querySelector('.fc-sovereign-chip')).toBeNull()
    expect(container.querySelector('[aria-label]')).toBeNull()
    // No Phosphor icon <svg> — the normal chip always renders one.
    expect(container.querySelector('svg')).toBeNull()
  })

  it('differentiation: two different time strings render two different labels', () => {
    const { rerender } = render(<NowMarkerLine timeText="9:00 AM" />)
    expect(screen.getByText('9:00 AM')).toBeInTheDocument()
    rerender(<NowMarkerLine timeText="5:45 PM" />)
    expect(screen.getByText('5:45 PM')).toBeInTheDocument()
    expect(screen.queryByText('9:00 AM')).toBeNull()
  })
})

describe('EventChip — dispatches to NowMarkerLine for now-marker, real chips otherwise', () => {
  it('a now-marker event renders NowMarkerLine, not the normal chip, using ext.timeLabel (not arg.timeText)', () => {
    // arg.timeText deliberately left empty here — @fullcalendar/list always
    // passes "" for it in Agenda view (see types.ts's now-marker doc
    // comment), so if EventChip regressed to reading arg.timeText instead of
    // ext.timeLabel, this test would catch the label going missing.
    const arg = fakeEventContentArg({ kind: 'now-marker', timeLabel: '3:05 PM' }, '')
    const { container } = render(<EventChip arg={arg} />)
    expect(container.querySelector('.fc-sovereign-now-marker-line')).not.toBeNull()
    expect(screen.getByText('3:05 PM')).toBeInTheDocument()
    // None of the normal chip's structure/content leaked through.
    expect(container.querySelector('.fc-sovereign-chip')).toBeNull()
    expect(container.querySelector('[aria-label]')).toBeNull()
    expect(screen.queryByText('Should never be read for now-marker')).toBeNull()
  })

  it('a real chip (task-due) renders the normal chip, not NowMarkerLine', () => {
    const arg = fakeEventContentArg(
      { kind: 'task-due', taskId: 't1', status: 'next', icon: 'Circle' },
      '9:00 AM',
    )
    const { container } = render(<EventChip arg={arg} />)
    expect(container.querySelector('.fc-sovereign-chip')).not.toBeNull()
    expect(container.querySelector('.fc-sovereign-now-marker-line')).toBeNull()
    expect(screen.getByText('Should never be read for now-marker')).toBeInTheDocument()
  })
})

/**
 * Render-level coverage for the "skipped" occurrence icon (5-agent review
 * finding — real gap). `eventMapping.runOverlay.test.ts` already proves the
 * MAPPING layer emits `icon: 'SkipForward'` as a string, and
 * `occurrenceStatusLabel('skipped')` above proves the LABEL text — but
 * nothing previously rendered `EventChip` itself with a skipped chip, so a
 * bug like `ICON_MAP = { ..., SkipForward: Prohibit }` (right KEY, wrong
 * Phosphor COMPONENT) would slip through every existing test untouched.
 *
 * Distinguishing "SkipForward rendered" from "some other icon rendered"
 * follows the SAME technique already established in this codebase by
 * `src/components/shared/IconRenderer.test.tsx` (see its case-insensitivity
 * proof): render the actual Phosphor component directly as a reference and
 * compare the rendered `<svg>`'s markup — identical icons produce identical
 * SVG output (same `<path>` data), a different icon component does not. A
 * negative control against `Prohibit` (an existing, differently-shaped
 * ICON_MAP entry) proves the comparison actually discriminates between
 * icons rather than trivially passing.
 */
describe('EventChip — skipped occurrence renders the actual SkipForward icon (not a swapped-in glyph)', () => {
  it('renders SVG markup identical to a direct SkipForward render, and NOT identical to Prohibit', () => {
    const ext: CalendarEventExtProps = {
      kind: 'task-occurrence',
      taskId: 't1',
      status: 'skipped',
      icon: 'SkipForward',
      occurrenceMs: 1000,
    }
    const arg = fakeEventContentArg(ext, '')
    const { container } = render(<EventChip arg={arg} />)
    const renderedIconSvg = container.querySelector('.fc-sovereign-chip svg')
    expect(renderedIconSvg).not.toBeNull()

    // Reference render: the real SkipForward component with the exact same
    // props EventChip passes its icon (size/weight/color) — same call shape
    // as `<Icon size={12} weight="fill" color={CHIP_TEXT_COLOR} aria-hidden="true" />`
    // in FullCalendarView.tsx's EventChip.
    const { container: skipForwardContainer } = render(
      <SkipForward size={12} weight="fill" color={CHIP_TEXT_COLOR} aria-hidden="true" />,
    )
    const skipForwardSvg = skipForwardContainer.querySelector('svg')
    expect(skipForwardSvg).not.toBeNull()
    expect(renderedIconSvg?.innerHTML).toBe(skipForwardSvg?.innerHTML)

    // Negative control — proves the markup comparison actually discriminates
    // between distinct Phosphor icons (i.e. this test WOULD fail if ICON_MAP
    // swapped SkipForward for Prohibit), not just that "some svg" is present.
    const { container: prohibitContainer } = render(
      <Prohibit size={12} weight="fill" color={CHIP_TEXT_COLOR} aria-hidden="true" />,
    )
    const prohibitSvg = prohibitContainer.querySelector('svg')
    expect(prohibitSvg).not.toBeNull()
    expect(renderedIconSvg?.innerHTML).not.toBe(prohibitSvg?.innerHTML)
  })
})

describe('DayCellRenderer — today-pill is Month-view only', () => {
  // Regression coverage: `dayCellContent` is also invoked by FullCalendar for
  // the all-day row's cells in timeGridWeek/timeGridDay, not just
  // dayGridMonth's date cells. An earlier version rendered the gold
  // today-pill unconditionally, putting a big static dot in Week/Day's
  // all-day lane that visually competed with (and was mistaken for) those
  // views' own live nowIndicator line — reported directly by the operator
  // testing the deployed build. This locks the view-gate in place.
  it('renders the today-pill in dayGridMonth (the only view it belongs in)', () => {
    const { container } = render(
      <DayCellRenderer arg={fakeDayCellArg({ isToday: true, view: { type: 'dayGridMonth' } })} />,
    )
    expect(container.querySelector('.fc-sovereign-today-pill')).not.toBeNull()
    expect(screen.getByText('20')).toBeInTheDocument()
  })

  it('renders nothing in timeGridWeek — no dot in the all-day row', () => {
    const { container } = render(
      <DayCellRenderer arg={fakeDayCellArg({ isToday: true, view: { type: 'timeGridWeek' } })} />,
    )
    expect(container.firstChild).toBeNull()
    expect(container.querySelector('.fc-sovereign-today-pill')).toBeNull()
  })

  it('renders nothing in timeGridDay — no dot in the all-day row', () => {
    const { container } = render(
      <DayCellRenderer arg={fakeDayCellArg({ isToday: true, view: { type: 'timeGridDay' } })} />,
    )
    expect(container.firstChild).toBeNull()
    expect(container.querySelector('.fc-sovereign-today-pill')).toBeNull()
  })
})

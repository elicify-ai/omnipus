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
import { occurrenceStatusLabel, extTooltip, DayCellRenderer, EventChip } from './FullCalendarView'
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

  it('returns undefined for kinds with no tooltip field at all (task-due, task-fire)', () => {
    const due: CalendarEventExtProps = { kind: 'task-due', taskId: 't1', status: 'next', icon: 'Circle' }
    const fire: CalendarEventExtProps = { kind: 'task-fire', taskId: 't1', status: 'next', icon: 'Clock' }
    expect(extTooltip(due)).toBeUndefined()
    expect(extTooltip(fire)).toBeUndefined()
  })
})

/**
 * NowMarkerLine — RETIRED (SP-39). The Agenda (listWeek) "now" divider
 * EventChip used to branch to for a `kind: 'now-marker'` synthetic event
 * existed ONLY to serve the Agenda view; SP-39 drops Agenda
 * (side-panel-shell-spec.md §13 SP-39 — "the spec names only Day/Week/Month";
 * wireframe §4), and the join removed the component, the `'now-marker'` event
 * kind and the divider styles with it. The four old tests below pinned the
 * divider's own contract (aria-hidden divider, verbatim time label, no chip
 * structure, label differentiation) — that expected behaviour is expressly
 * superseded, so the pack now pins the RETIREMENT: the export is gone and
 * EventChip renders the normal chip for every surviving kind, never the
 * divider.
 */
describe('NowMarkerLine — RETIRED (SP-39: Agenda dropped, the divider existed only for listWeek)', () => {
  // One separate supersession case for EACH of the four old divider cases;
  // no test is deleted or consolidated. The normal task-chip contract stays.
  it('the retired divider has no exported component or aria-hidden output path', async () => {
    const mod = (await import('./FullCalendarView')) as unknown as Record<string, unknown>
    expect(mod.NowMarkerLine, 'the Agenda-only divider is retired with the Agenda view (SP-39)').toBeUndefined()
    const { container } = render(<EventChip arg={fakeEventContentArg(
      { kind: 'task-due', taskId: 't1', status: 'next', icon: 'Circle' }, '3:05 PM',
    )} />)
    expect(container.querySelector('.fc-sovereign-now-marker-line')).toBeNull()
    expect(container.querySelector('.fc-sovereign-chip')).not.toHaveAttribute('aria-hidden', 'true')
  })

  it('the surviving task chip displays the caller time verbatim, not an Agenda-divider label', () => {
    // Old case: NowMarkerLine(timeText='3:05 PM') displayed the exact label.
    // SP-39 retires that component; the actual Week/Day chip retains time.
    const { container } = render(<EventChip arg={fakeEventContentArg(
      { kind: 'task-due', taskId: 't1', status: 'next', icon: 'Circle' }, '3:05 PM',
    )} />)
    expect(screen.getByText('3:05 PM')).toBeInTheDocument()
    expect(container.querySelector('.fc-sovereign-now-marker-line')).toBeNull()
  })

  it('the surviving task is a real accessible chip with an icon, never the retired icon-less divider', () => {
    // Old case asserted no chip/name/icon for the Agenda-only divider.
    // Supersession keeps the real chip structure, rather than a bare divider.
    const { container } = render(<EventChip arg={fakeEventContentArg(
      { kind: 'task-due', taskId: 't1', status: 'next', icon: 'Circle' }, '3:05 PM',
    )} />)
    const chip = container.querySelector('.fc-sovereign-chip')
    expect(chip).not.toBeNull()
    expect(chip).toHaveAttribute('aria-label', expect.stringContaining('Should never be read for now-marker'))
    expect(chip?.querySelector('svg')).not.toBeNull()
    expect(container.querySelector('.fc-sovereign-now-marker-line')).toBeNull()
  })

  it('different caller times still produce different task-chip labels without resurrecting the divider', () => {
    // Retains the old 9:00 AM → 5:45 PM differentiation state sequence.
    const ext: CalendarEventExtProps = { kind: 'task-due', taskId: 't1', status: 'next', icon: 'Circle' }
    const mounted = render(<EventChip arg={fakeEventContentArg(ext, '9:00 AM')} />)
    expect(screen.getByText('9:00 AM')).toBeInTheDocument()
    mounted.rerender(<EventChip arg={fakeEventContentArg(ext, '5:45 PM')} />)
    expect(screen.getByText('5:45 PM')).toBeInTheDocument()
    expect(screen.queryByText('9:00 AM')).toBeNull()
    expect(mounted.container.querySelector('.fc-sovereign-now-marker-line')).toBeNull()
  })
})

describe('EventChip — renders the normal chip for EVERY surviving kind; the Agenda divider branch is gone (SP-39)', () => {
  const SURVIVING_KINDS: CalendarEventExtProps[] = [
    { kind: 'task-due', taskId: 't1', status: 'next', icon: 'Circle' },
    { kind: 'task-fire', taskId: 't1', status: 'next', icon: 'Clock' },
    { kind: 'task-occurrence', taskId: 't1', status: 'done', icon: 'CheckCircle', occurrenceMs: 0 },
    {
      kind: 'task-occurrence-agg',
      taskId: 't1',
      status: 'done',
      icon: 'CheckCircle',
      tooltip: 'first at 09:00',
      dayStartMs: 0,
      dayEndMs: 0,
    },
    { kind: 'task-occurrence-more', taskId: 't1', status: 'next', icon: 'Clock', tooltip: 'more' },
  ]

  it.each(SURVIVING_KINDS.map((ext) => ({ ext, label: ext.kind })))(
    'a $label event renders the normal chip — never the retired Agenda divider',
    ({ ext }) => {
      const arg = fakeEventContentArg(ext, '9:00 AM')
      const { container } = render(<EventChip arg={arg} />)
      expect(container.querySelector('.fc-sovereign-chip')).not.toBeNull()
      expect(container.querySelector('.fc-sovereign-now-marker-line')).toBeNull()
    },
  )

  it('a real chip (task-due) keeps its full accessible structure (the old anti-regression half, unchanged)', () => {
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

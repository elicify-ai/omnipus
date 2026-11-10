// calendarViews.wave3.test.tsx — wave-3 join pack for side-panel-shell-spec.md
// Wave 3, SP-39 (view SET half):
//
//   "Calendar docks default Week, view selectable; Day/Week/Month all
//    user-selectable."  (§13 SP-39)
//
//   Spec §10 Wave 3: "Day/Week/Month all user-selectable. Month MUST work
//   inside the docked panel AND in the phone full-screen presentation."
//   Wireframe §4: "Agenda is dropped — the spec names only Day/Week/Month";
//   §1 of the founder decisions: "Day, Week and Month are three equal,
//   in-panel choices".
//
// Oracle sources: spec §10 Wave 3 / §13 SP-39, wireframe §4. Expected values
// below are the FullCalendar view-name mapping this repo already uses
// (calendar/types.ts CALENDAR_VIEW_LABELS: dayGridMonth=Month,
// timeGridWeek=Week, timeGridDay=Day) — the names are the existing wire
// vocabulary, not invented by this pack; what the pack pins is the SET
// (exactly three, Agenda gone), which the spec changed.
//
// JOIN STATUS (2026-10-05, work/side-panel-wave3-join-20261005): the calendar
// slice delivered exactly this contract — calendar/types.ts narrows
// CalendarViewName to the three views and drops listWeek, and
// CalendarToolbar renders the SegmentedControl switcher from CALENDAR_VIEWS.
// Assertions are UNCHANGED from the 8773803cf RED pack (they already express
// the SP-39 end state); only this header was updated. The docking/presentation
// half of SP-39 (default Week, Month never routes out) is pinned by
// CalendarPanel.wave3.test.tsx against the joined CalendarScreen.

import { describe, it, expect } from 'vitest'
import { render, screen, within } from '@testing-library/react'

import { CALENDAR_VIEWS, CALENDAR_VIEW_LABELS, type CalendarApiRef } from './types'
import { CalendarToolbar } from './CalendarToolbar'

function makeCalendarRef(): CalendarApiRef {
  // The toolbar only reads the ref through its click handlers; no test here
  // drives one, so an always-null inert ref is the honest stand-in (a plain
  // object satisfies the MutableRefObject shape — no React hook involved).
  return { current: null }
}

describe('Calendar wave 3 — SP-39: exactly three views, Agenda dropped', () => {
  it('CALENDAR_VIEWS is exactly [Month, Week, Day] in that order', () => {
    expect(CALENDAR_VIEWS).toEqual(['dayGridMonth', 'timeGridWeek', 'timeGridDay'])
  })

  it('the Agenda view (listWeek) is gone from the view set', () => {
    expect(CALENDAR_VIEWS).not.toContain('listWeek')
    expect(Object.keys(CALENDAR_VIEW_LABELS)).not.toContain('listWeek')
  })

  it('the label map covers exactly the three surviving views with their names', () => {
    // Runtime deep equality IS the oracle here — the union is now the
    // narrowed CalendarViewName, and deep equality pins both the keys and
    // the human labels the spec names.
    expect(CALENDAR_VIEW_LABELS).toEqual({
      dayGridMonth: 'Month',
      timeGridWeek: 'Week',
      timeGridDay: 'Day',
    })
  })

  it('the rendered view switcher offers exactly Month, Week, Day — no Agenda button', () => {
    const calendarRef = makeCalendarRef()
    const mounted = render(
      <CalendarToolbar
        calendarRef={calendarRef}
        currentView="timeGridWeek"
        title="Sep 22 – 28"
        onViewChange={() => {}}
        onNewTask={() => {}}
      />,
    )
    const switcher = mounted.getByRole('group', { name: 'Calendar view' })
    expect(within(switcher).getByTestId('calendar-view-dayGridMonth')).toBeInTheDocument()
    expect(within(switcher).getByTestId('calendar-view-timeGridWeek')).toBeInTheDocument()
    expect(within(switcher).getByTestId('calendar-view-timeGridDay')).toBeInTheDocument()
    expect(within(switcher).queryByTestId('calendar-view-listWeek')).not.toBeInTheDocument()
    expect(within(switcher).queryByText('Agenda')).not.toBeInTheDocument()
  })

  it('every rendered view button carries one of the three surviving labels', () => {
    const calendarRef = makeCalendarRef()
    render(
      <CalendarToolbar
        calendarRef={calendarRef}
        currentView="timeGridWeek"
        title="Sep 22 – 28"
        onViewChange={() => {}}
        onNewTask={() => {}}
      />,
    )
    const switcher = screen.getByRole('group', { name: 'Calendar view' })
    const labels = Array.from(switcher.querySelectorAll('button')).map((b) => b.textContent)
    expect(labels).toEqual(['Month', 'Week', 'Day'])
  })
})

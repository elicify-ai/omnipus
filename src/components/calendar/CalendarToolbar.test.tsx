/**
 * CalendarToolbar.test.tsx
 *
 * Integration tests for CalendarToolbar (spec §9 #19).
 *
 * Strategy: render CalendarToolbar with a fake `calendarRef` whose
 * `current.getApi()` returns an object of vi.fn() spies. Assert that:
 *   - clicking calendar-view-<viewName> calls getApi().changeView(viewName) AND
 *     onViewChange(viewName)
 *   - clicking calendar-prev calls getApi().prev()
 *   - clicking calendar-next calls getApi().next()
 *   - clicking calendar-today calls getApi().today()
 *   - clicking calendar-new-task calls onNewTask(getApi().getDate()) — the
 *     WCAG 2.1.1 keyboard-equivalent prefill (dateClick is pointer-only)
 *
 * Traces to: workspace-calendar-fullcalendar-spec.md §9 #19 / FR-006 / US-2/AS-1,2
 */

import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { CalendarToolbar } from './CalendarToolbar'
import type { CalendarApiRef } from './types'
import type FullCalendar from '@fullcalendar/react'

// ── Helpers ───────────────────────────────────────────────────────────────────

/**
 * Build a fake FullCalendar ref whose `.current.getApi()` returns spies.
 * The spies are returned alongside the ref so tests can assert on them.
 */
function makeFakeCalendarRef() {
  const changeView = vi.fn()
  const prev = vi.fn()
  const next = vi.fn()
  const today = vi.fn()
  const currentDate = new Date('2026-08-15T00:00:00')
  const getDate = vi.fn(() => currentDate)

  const getApi = vi.fn(() => ({ changeView, prev, next, today, getDate }))

  const calendarRef: CalendarApiRef = {
    current: { getApi } as unknown as FullCalendar,
  }

  return { calendarRef, currentDate, spies: { getApi, changeView, prev, next, today, getDate } }
}

function renderToolbar(
  overrides: Partial<{
    currentView: 'dayGridMonth' | 'timeGridWeek' | 'timeGridDay' | 'listWeek'
    title: string
    onViewChange: (view: string) => void
    onNewTask: () => void
  }> = {},
) {
  const { calendarRef, spies, currentDate } = makeFakeCalendarRef()

  const defaults = {
    currentView: 'dayGridMonth' as const,
    title: 'June 2026',
    onViewChange: vi.fn(),
    onNewTask: vi.fn(),
  }

  const props = { ...defaults, ...overrides }

  const { rerender } = render(
    <CalendarToolbar
      calendarRef={calendarRef}
      currentView={props.currentView}
      title={props.title}
      onViewChange={props.onViewChange}
      onNewTask={props.onNewTask}
    />,
  )

  return { spies, props, calendarRef, currentDate, rerender }
}

// ── Tests ─────────────────────────────────────────────────────────────────────

describe('CalendarToolbar — view switching (spec §9 #19, FR-006, US-2/AS-1)', () => {
  it('clicking calendar-view-timeGridWeek calls changeView("timeGridWeek") AND onViewChange("timeGridWeek")', () => {
    // Traces to: workspace-calendar-fullcalendar-spec.md §9 #19 / FR-006
    // BDD: Given the toolbar renders with Month as current view,
    // When the Week tab button is clicked,
    // Then calendarRef.current.getApi().changeView("timeGridWeek") is called
    // AND onViewChange("timeGridWeek") is called.

    const { spies, props } = renderToolbar({ currentView: 'dayGridMonth' })

    const weekBtn = screen.getByTestId('calendar-view-timeGridWeek')
    fireEvent.click(weekBtn)

    expect(spies.changeView).toHaveBeenCalledOnce()
    expect(spies.changeView).toHaveBeenCalledWith('timeGridWeek')
    expect(props.onViewChange).toHaveBeenCalledOnce()
    expect(props.onViewChange).toHaveBeenCalledWith('timeGridWeek')
  })

  it('clicking calendar-view-timeGridDay calls changeView("timeGridDay") AND onViewChange("timeGridDay")', () => {
    // Traces to: workspace-calendar-fullcalendar-spec.md §9 #19 / FR-006
    const { spies, props } = renderToolbar({ currentView: 'dayGridMonth' })

    const dayBtn = screen.getByTestId('calendar-view-timeGridDay')
    fireEvent.click(dayBtn)

    expect(spies.changeView).toHaveBeenCalledWith('timeGridDay')
    expect(props.onViewChange).toHaveBeenCalledWith('timeGridDay')
  })

  it('clicking calendar-view-listWeek calls changeView("listWeek") AND onViewChange("listWeek")', () => {
    // Traces to: workspace-calendar-fullcalendar-spec.md §9 #19 / FR-006
    const { spies, props } = renderToolbar({ currentView: 'dayGridMonth' })

    const agendaBtn = screen.getByTestId('calendar-view-listWeek')
    fireEvent.click(agendaBtn)

    expect(spies.changeView).toHaveBeenCalledWith('listWeek')
    expect(props.onViewChange).toHaveBeenCalledWith('listWeek')
  })

  it('clicking calendar-view-dayGridMonth calls changeView("dayGridMonth") AND onViewChange("dayGridMonth")', () => {
    // Traces to: workspace-calendar-fullcalendar-spec.md §9 #19 / FR-006
    // Start in a different view to ensure the click is meaningful
    const { spies, props } = renderToolbar({ currentView: 'timeGridWeek' })

    const monthBtn = screen.getByTestId('calendar-view-dayGridMonth')
    fireEvent.click(monthBtn)

    expect(spies.changeView).toHaveBeenCalledWith('dayGridMonth')
    expect(props.onViewChange).toHaveBeenCalledWith('dayGridMonth')
  })

  it('differentiation: clicking different view tabs calls changeView with different arguments', () => {
    // Anti-hardcode: two different view tabs must call changeView with two different view names.
    // Traces to: workspace-calendar-fullcalendar-spec.md §9 #19

    const { spies, props } = renderToolbar({ currentView: 'dayGridMonth' })

    fireEvent.click(screen.getByTestId('calendar-view-timeGridWeek'))
    fireEvent.click(screen.getByTestId('calendar-view-listWeek'))

    expect(spies.changeView).toHaveBeenCalledTimes(2)
    expect(spies.changeView.mock.calls[0][0]).toBe('timeGridWeek')
    expect(spies.changeView.mock.calls[1][0]).toBe('listWeek')
    // The two calls must differ (anti-hardcode)
    expect(spies.changeView.mock.calls[0][0]).not.toBe(spies.changeView.mock.calls[1][0])

    expect(props.onViewChange).toHaveBeenCalledTimes(2)
    expect(vi.mocked(props.onViewChange).mock.calls[0][0]).toBe('timeGridWeek')
    expect(vi.mocked(props.onViewChange).mock.calls[1][0]).toBe('listWeek')
  })
})

describe('CalendarToolbar — navigation: prev / next / today (spec §9 #19, FR-006, US-2/AS-2)', () => {
  it('clicking calendar-prev calls getApi().prev()', () => {
    // Traces to: workspace-calendar-fullcalendar-spec.md §9 #19 / FR-006 / US-2/AS-2
    // BDD: Given any view, When I click prev, Then getApi().prev() is called.

    const { spies } = renderToolbar()

    fireEvent.click(screen.getByTestId('calendar-prev'))

    expect(spies.prev).toHaveBeenCalledOnce()
    // Must NOT have called next or today
    expect(spies.next).not.toHaveBeenCalled()
    expect(spies.today).not.toHaveBeenCalled()
  })

  it('clicking calendar-next calls getApi().next()', () => {
    // Traces to: workspace-calendar-fullcalendar-spec.md §9 #19 / FR-006 / US-2/AS-2
    // BDD: Given any view, When I click next, Then getApi().next() is called.

    const { spies } = renderToolbar()

    fireEvent.click(screen.getByTestId('calendar-next'))

    expect(spies.next).toHaveBeenCalledOnce()
    expect(spies.prev).not.toHaveBeenCalled()
    expect(spies.today).not.toHaveBeenCalled()
  })

  it('clicking calendar-today calls getApi().today()', () => {
    // Traces to: workspace-calendar-fullcalendar-spec.md §9 #19 / FR-006 / US-2/AS-2
    // BDD: Given any view, When I click today, Then getApi().today() is called.

    const { spies } = renderToolbar()

    fireEvent.click(screen.getByTestId('calendar-today'))

    expect(spies.today).toHaveBeenCalledOnce()
    expect(spies.prev).not.toHaveBeenCalled()
    expect(spies.next).not.toHaveBeenCalled()
  })

  it('differentiation: prev and next call different API methods', () => {
    // Anti-hardcode: clicking prev vs next must route to different API calls.
    // Traces to: workspace-calendar-fullcalendar-spec.md §9 #19

    const { spies } = renderToolbar()

    fireEvent.click(screen.getByTestId('calendar-prev'))
    fireEvent.click(screen.getByTestId('calendar-next'))

    // prev was called once; next was called once; they are different functions
    expect(spies.prev).toHaveBeenCalledOnce()
    expect(spies.next).toHaveBeenCalledOnce()
    // The spy functions themselves are different (not the same function)
    expect(spies.prev).not.toBe(spies.next)
  })
})

describe('CalendarToolbar — New task button (spec §9 #19, FR-012)', () => {
  it('clicking calendar-new-task calls onNewTask(currentDate)', () => {
    // Traces to: workspace-calendar-fullcalendar-spec.md §9 #19 / FR-012
    // WCAG 2.1.1: dateClick/select on the grid are pointer-only, so this
    // button is the keyboard-equivalent path — it must pre-fill with the
    // calendar's CURRENTLY VIEWED date (getApi().getDate()), not call
    // onNewTask() with no date.
    const { props, spies, currentDate } = renderToolbar()

    const newTaskBtn = screen.getByTestId('calendar-new-task')
    expect(newTaskBtn).toBeInTheDocument()
    fireEvent.click(newTaskBtn)

    expect(spies.getDate).toHaveBeenCalledOnce()
    expect(props.onNewTask).toHaveBeenCalledOnce()
    expect(props.onNewTask).toHaveBeenCalledWith(currentDate)
  })

  it('clicking New task does NOT call changeView or any nav API method', () => {
    // Rejection test: onNewTask should not route through the calendar API.
    const { spies } = renderToolbar()

    fireEvent.click(screen.getByTestId('calendar-new-task'))

    expect(spies.changeView).not.toHaveBeenCalled()
    expect(spies.prev).not.toHaveBeenCalled()
    expect(spies.next).not.toHaveBeenCalled()
    expect(spies.today).not.toHaveBeenCalled()
  })
})

describe('CalendarToolbar — title and view rendering', () => {
  it('renders the title prop as a heading text', () => {
    // Traces to: workspace-calendar-fullcalendar-spec.md §9 #19
    // The toolbar must surface the period title from the host.

    renderToolbar({ title: 'July 2026' })

    expect(screen.getByText('July 2026')).toBeInTheDocument()
  })

  it('marks the current active view button as aria-pressed=true', () => {
    // Traces to: workspace-calendar-fullcalendar-spec.md §9 #19 / FR-006
    // A11y audit fix (option b): the view switcher is a `role="group"` of
    // independently-tabbable TOGGLE buttons, not an ARIA tablist (no arrow-key
    // roving tabindex / aria-controls is implemented) — so activeness is
    // conveyed via `aria-pressed`, not the tab-pattern's `aria-selected`.
    renderToolbar({ currentView: 'timeGridWeek' })

    const weekBtn = screen.getByTestId('calendar-view-timeGridWeek')
    expect(weekBtn).toHaveAttribute('aria-pressed', 'true')

    // Other view buttons must NOT be marked pressed
    const monthBtn = screen.getByTestId('calendar-view-dayGridMonth')
    expect(monthBtn).toHaveAttribute('aria-pressed', 'false')

    // Neither button carries the ARIA TAB pattern's roles/attributes — that
    // pattern was removed because arrow-key navigation and aria-controls were
    // never implemented for it.
    expect(weekBtn).not.toHaveAttribute('role', 'tab')
    expect(weekBtn).not.toHaveAttribute('aria-selected')
  })

  it('the view switcher container is role="group", not role="tablist"', () => {
    // A11y audit fix (option b): asserts the tab pattern was fully removed,
    // not just the per-button role="tab"/aria-selected attributes.
    renderToolbar()

    const group = screen.getByRole('group', { name: 'Calendar view' })
    expect(group).toBeInTheDocument()
    expect(screen.queryByRole('tablist', { name: 'Calendar view' })).toBeNull()
  })

  it('all four view tab testids are present in the toolbar', () => {
    // Traces to: workspace-calendar-fullcalendar-spec.md §9 #19 / FR-006
    // BDD: All four views (Month/Week/Day/Agenda) must be reachable from the toolbar.

    renderToolbar()

    expect(screen.getByTestId('calendar-view-dayGridMonth')).toBeInTheDocument()
    expect(screen.getByTestId('calendar-view-timeGridWeek')).toBeInTheDocument()
    expect(screen.getByTestId('calendar-view-timeGridDay')).toBeInTheDocument()
    expect(screen.getByTestId('calendar-view-listWeek')).toBeInTheDocument()
  })
})

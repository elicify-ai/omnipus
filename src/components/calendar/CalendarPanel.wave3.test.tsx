// CalendarPanel.wave3.test.tsx — wave-3 join pack for side-panel-shell-spec.md
// Wave 3, SP-39 (docking/presentation half):
//
//   "Calendar docks default Week, view selectable; Month MUST work inside
//    the docked panel AND in the phone full-screen presentation — it never
//    jumps to a different, wider surface (SP-39, corrects the earlier
//    'month view routes to the full page' line)."  (§10 Wave 3 Calendar row)
//
//   Wireframe §4: "docked Calendar opens on Week by default; Day, Week and
//   Month are all reachable in-panel, user-selectable, Month included — it
//   no longer routes to Expand. ... Full screen is panel-content-only,
//   chrome-less, '← Back to chat'." §7 phone frame: "Click Day/Week/Month
//   above — Month renders here, it never routes away."
//
// Oracle sources: spec §10 Wave 3 / §13 SP-39, wireframe §4 + §7.
//
// JOIN STATUS (2026-10-05, work/side-panel-wave3-join-20261005) — the
// 8773803cf pack declared the panel content at
// `src/components/calendar/CalendarPanel.tsx` with a PanelContentProps
// `presentation` prop; the joined slices renegotiated that seam exactly as
// the RED pack's header authorised:
//
//   - The calendar PANEL content is `CalendarScreen` (the real workspace
//     calendar), registered by the shell slice's registry entry
//     (registry.tsx CalendarPanelContent) and rendered unchanged inside the
//     chrome-less full-screen route — one content component for the docked
//     panel and the phone presentation, which is precisely SP-39's
//     "never jumps to a different, wider surface": there is no separate
//     wide-calendar surface to jump TO.
//   - The docked default view is driven through FullCalendarView's
//     `initialView` prop (the host seam this pack always targeted).
//   - Month is delivered as the compact in-panel grid: FullCalendar stays
//     mounted (hidden + inert while Month is active) and CalendarMonthGrid
//     renders from FullCalendar's own reported range — the pack's FullCalendarView
//     stub therefore fires the real onDatesSet contract once on mount (the
//     one thing FullCalendar always does), so the grid's range input is the
//     component's own, not a fabricated one.
//
// The view-SET half of SP-39 (exactly Day/Week/Month, Agenda dropped) is
// pinned by calendarViews.wave3.test.tsx.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, fireEvent, waitFor } from '@testing-library/react'
import { useEffect } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { CalendarScreen } from '@/components/screens/CalendarScreen'

// FullCalendar cannot lay out in jsdom (every CalendarScreen test mocks this
// boundary). The stub keeps the three contract points this pack's oracles
// hang off: the initialView the host passes down; the datesSet callback
// FullCalendar always fires on mount (which feeds the Month grid its visible
// range); and the calendar API the toolbar drives — a real changeView()
// makes FullCalendar fire datesSet for the new view, so the stub's API does
// exactly that through the same calendarRef the host handed over.
vi.mock('@/components/calendar/FullCalendarView', () => ({
  FullCalendarView: (props: {
    calendarRef?: { current: unknown }
    initialView?: string
    onDatesSet?: (title: string, view: string, activeStart: Date, activeEnd: Date) => void
  }) => {
    const fireDatesSet = (view: string) => {
      props.onDatesSet?.(
        'Sep 22 – 28',
        view,
        new Date('2026-09-22T00:00:00'),
        new Date('2026-09-29T00:00:00'),
      )
    }
    useEffect(() => {
      if (props.calendarRef) {
        ;(props.calendarRef as { current: unknown }).current = {
          getApi: () => ({
            changeView: (view: string) => fireDatesSet(view),
            today: () => {},
            prev: () => {},
            next: () => {},
            scrollToTime: () => {},
            getDate: () => new Date('2026-09-22T00:00:00'),
            view: { type: props.initialView ?? 'dayGridMonth' },
          }),
        }
      }
      fireDatesSet(props.initialView ?? 'dayGridMonth')
      return () => {
        if (props.calendarRef) (props.calendarRef as { current: unknown }).current = null
      }
      // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [])
    return <div data-testid="fcv-stub" data-initial-view={props.initialView ?? 'dayGridMonth'} />
  },
}))

// The screen's data layer — kept inert so the panel's own behaviour is the
// variable. (useOccurrences self-fetches and degrades; useWorkspaceTeamIds
// degrades through fetchWorkspaceDelegation below.)
vi.mock('@/lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api')>()),
  fetchTasks: vi.fn(async () => []),
  fetchAgents: vi.fn(async () => []),
  fetchWorkspaceDelegation: vi.fn(async () => null),
  updateTask: vi.fn(),
}))

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

function renderCalendar() {
  return render(
    <QueryClientProvider client={makeClient()}>
      <CalendarScreen workspaceId="ws-1" />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.spyOn(window.history, 'pushState').mockImplementation(() => {})
  vi.spyOn(window.history, 'replaceState').mockImplementation(() => {})
})

describe('CalendarPanel (CalendarScreen) — SP-39: docked Calendar opens on Week by default', () => {
  it('the panel drives the calendar with initialView timeGridWeek (not Month)', async () => {
    const mounted = renderCalendar()
    const stub = await waitFor(() => mounted.getByTestId('fcv-stub'))
    expect(stub.getAttribute('data-initial-view')).toBe('timeGridWeek')
  })
})

describe('CalendarPanel (CalendarScreen) — SP-39: Day/Week/Month are three equal in-panel choices', () => {
  it('the panel renders the calendar toolbar with exactly the three view choices', async () => {
    const mounted = renderCalendar()
    const switcher = await waitFor(() => mounted.getByRole('group', { name: 'Calendar view' }))
    expect(switcher.textContent).toContain('Day')
    expect(switcher.textContent).toContain('Week')
    expect(switcher.textContent).toContain('Month')
    expect(switcher.textContent).not.toContain('Agenda')
  })

  it('selecting Month changes the view IN PANEL — the compact grid renders, FullCalendar stays mounted', async () => {
    const mounted = renderCalendar()
    const monthButton = await waitFor(() => mounted.getByTestId('calendar-view-dayGridMonth'))
    fireEvent.click(monthButton)
    // The compact in-panel Month grid renders from the calendar's own range…
    expect(await waitFor(() => mounted.getByTestId('calendar-month-grid'))).toBeInTheDocument()
    // …while FullCalendar remains mounted underneath as the navigation engine
    // (hidden, not unmounted — switching back to Week must not lose it).
    expect(mounted.getByTestId('fcv-stub')).toBeInTheDocument()
  })
})

describe('CalendarPanel (CalendarScreen) — SP-39: Month never jumps to a different, wider surface', () => {
  it('the panel performs no navigation when Month is selected, and offers no expand affordance of its own', async () => {
    const mounted = renderCalendar()
    const monthButton = await waitFor(() => mounted.getByTestId('calendar-view-dayGridMonth'))
    fireEvent.click(monthButton)
    await waitFor(() => expect(mounted.getByTestId('calendar-month-grid')).toBeInTheDocument())
    // The panel content owns no router exit: selecting a view mutates the
    // in-panel calendar only (SP-39 corrects the old "routes to the full
    // page" behaviour). A docked calendar that opens a route or a wider
    // surface here is exactly the regression this test names.
    expect(mounted.queryByTestId('fullscreen-panel')).not.toBeInTheDocument()
    expect(mounted.getByTestId('fcv-stub')).toBeInTheDocument()
    expect(window.history.pushState).not.toHaveBeenCalled()
    expect(window.history.replaceState).not.toHaveBeenCalled()
    // The content carries no expand affordance of its own — the chrome-less
    // host owns the only way out (its "Back to chat" is pinned in
    // wave3FullScreen.test.tsx); the calendar itself must never offer a
    // "make me bigger" escape hatch (wireframe §4: "it no longer routes to
    // Expand").
    const expandAffordance = Array.from(mounted.container.querySelectorAll('button')).find((b) =>
      /expand/i.test(b.getAttribute('aria-label') ?? b.textContent ?? ''),
    )
    expect(expandAffordance).toBeUndefined()
  })
})

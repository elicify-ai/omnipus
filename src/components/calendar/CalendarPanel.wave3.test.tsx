// CalendarPanel.wave3.test.tsx — RED pack for side-panel-shell-spec.md Wave
// 3, SP-39 (docking/presentation half):
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
// RED↔GREEN contract declared by this pack: the calendar PANEL content
// component lives at `src/components/calendar/CalendarPanel.tsx` and takes
// the shared PanelContentProps (context/presentation/close/expand/
// registerExpandContext/onWidthSettle — the same shape every panel content
// receives, see _fullscreen.panel.$panelId.tsx). Docked default view is
// observed through the FullCalendar host seam: the panel drives
// FullCalendarView with `initialView` (the existing prop,
// FullCalendarView.tsx: "initialView ?? 'dayGridMonth'" is TODAY's
// default — the exact gap SP-39 closes for the docked panel).
//
// RED evidence (2026-10-04): no calendar panel content exists anywhere —
// the registry (src/components/panel-shell/registry.tsx) holds only
// library/browser, and no CalendarPanel module exists. Every test below
// fails today; the BLOCKED error is this role's mandated loud failure for a
// missing implementation (never a skip).
//
// The view-SET half of SP-39 (exactly Day/Week/Month, Agenda dropped) is
// already pinned by calendarViews.wave3.test.tsx against existing modules.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactElement } from 'react'

const PANEL_PATH = '@/components/calendar/CalendarPanel'

async function loadPanel() {
  try {
    // @vite-ignore: the missing module must fail at RUN time (this pack's own
    // BLOCKED error), not fail the whole file at transform time.
    return await import(/* @vite-ignore */ PANEL_PATH)
  } catch {
    throw new Error(
      'BLOCKED: src/components/calendar/CalendarPanel.tsx not implemented — required by SP-6/SP-39 (§10 Wave 3: Calendar becomes a panel docking on Week)',
    )
  }
}

const captured: Array<{ initialView?: string }> = []

vi.mock('@/components/calendar/FullCalendarView', () => ({
  FullCalendarView: (props: { initialView?: string }) => {
    captured.push({ initialView: props.initialView })
    return <div data-testid="fcv-stub" data-initial-view={props.initialView ?? 'dayGridMonth'} />
  },
}))

// The panel host may also read workspaces (leave-gate/deep-link context);
// keep the API layer inert so the panel's own behaviour is the variable.
vi.mock('@/lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api')>()),
  fetchAppState: vi.fn(async () => ({
    onboarding_complete: true,
    identity: { signed_in: true },
  })),
}))

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

type PanelModule = { CalendarPanel: (props: Record<string, unknown>) => ReactElement }

function panelProps(presentation: 'docked' | 'fullscreen' = 'docked') {
  return {
    context: { workspaceId: 'ws-1' },
    presentation,
    close: () => {},
    expand: () => {},
    registerExpandContext: () => {},
    onWidthSettle: () => {},
  }
}

async function renderPanel(presentation: 'docked' | 'fullscreen' = 'docked') {
  const { CalendarPanel } = (await loadPanel()) as PanelModule
  return render(
    <QueryClientProvider client={makeClient()}>
      <CalendarPanel {...panelProps(presentation)} />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  captured.length = 0
})

describe('CalendarPanel — SP-39: docked Calendar opens on Week by default', () => {
  it('the docked panel drives the calendar with initialView timeGridWeek (not Month)', async () => {
    const mounted = await renderPanel('docked')
    await waitFor(() => expect(captured.length).toBeGreaterThan(0))
    const stub = mounted.getByTestId('fcv-stub')
    expect(stub.getAttribute('data-initial-view')).toBe('timeGridWeek')
  })
})

describe('CalendarPanel — SP-39: Day/Week/Month are three equal in-panel choices', () => {
  it('the panel renders the calendar toolbar with exactly the three view choices', async () => {
    const mounted = await renderPanel('docked')
    const switcher = await waitFor(() => mounted.getByRole('group', { name: 'Calendar view' }))
    expect(switcher.textContent).toContain('Day')
    expect(switcher.textContent).toContain('Week')
    expect(switcher.textContent).toContain('Month')
    expect(switcher.textContent).not.toContain('Agenda')
  })

  it('selecting Month changes the view IN PANEL — the grid re-renders, no route change', async () => {
    const mounted = await renderPanel('docked')
    const monthButton = await waitFor(() => mounted.getByTestId('calendar-view-dayGridMonth'))
    fireEvent.click(monthButton)
    await waitFor(() => expect(mounted.getByTestId('fcv-stub').getAttribute('data-initial-view')).toBe('dayGridMonth'))
  })
})

describe('CalendarPanel — SP-39: Month never jumps to a different, wider surface', () => {
  it('the docked panel performs no navigation when Month is selected', async () => {
    const pushSpy = vi.spyOn(window.history, 'pushState')
    const replaceSpy = vi.spyOn(window.history, 'replaceState')
    const mounted = await renderPanel('docked')
    const monthButton = await waitFor(() => mounted.getByTestId('calendar-view-dayGridMonth'))
    fireEvent.click(monthButton)
    // The panel content owns no router exit: selecting a view mutates the
    // in-panel calendar only (SP-39 corrects the old "routes to the full
    // page" behaviour). A docked calendar that opens a route or the
    // fullscreen surface here is exactly the regression this test names.
    expect(mounted.queryByTestId('fullscreen-panel')).not.toBeInTheDocument()
    expect(mounted.getByTestId('fcv-stub')).toBeInTheDocument()
    expect(pushSpy).not.toHaveBeenCalled()
    expect(replaceSpy).not.toHaveBeenCalled()
  })

  it('the phone/full-screen presentation renders Month in place too', async () => {
    const mounted = await renderPanel('fullscreen')
    const monthButton = await waitFor(() => mounted.getByTestId('calendar-view-dayGridMonth'))
    fireEvent.click(monthButton)
    await waitFor(() => expect(mounted.getByTestId('fcv-stub').getAttribute('data-initial-view')).toBe('dayGridMonth'))
    // Still the panel's own grid — never a route-out (wireframe §7: "Month
    // renders here, it never routes away").
    expect(mounted.getByTestId('fcv-stub')).toBeInTheDocument()
  })
})

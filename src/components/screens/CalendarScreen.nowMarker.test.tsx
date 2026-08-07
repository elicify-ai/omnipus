/**
 * CalendarScreen.nowMarker.test.tsx
 *
 * Covers the Agenda-view (`listWeek`) live "now" divider added to
 * CalendarScreen/FullCalendarView/types.ts. `@fullcalendar/list` has no time
 * axis and structurally cannot render FullCalendar's built-in `nowIndicator`
 * (which already works correctly, untouched, in Week/Day via native
 * timeGrid). CalendarScreen instead appends a single synthetic
 * `kind: 'now-marker'` EventInput to the events it passes to
 * FullCalendarView — ONLY while Agenda is the active view AND the
 * live-ticking "now" timestamp falls inside FullCalendar's own reported
 * visible range (`activeRange`, sourced from `onDatesSet`).
 *
 * Strategy mirrors CalendarScreen.occurrencesDegrade.test.tsx (spec §9 —
 * F-03): jsdom cannot lay out FullCalendar's own DOM, so FullCalendarView is
 * mocked at the module boundary and its captured `events`/`onDatesSet`/
 * `onEventClick` props drive the assertions.
 *
 * The inclusion/exclusion/click describes below use the REAL `Date.now()` at
 * component-mount time (the `nowTick` state initializer), computing each
 * test's `activeStart`/`activeEnd` window relative to the real current time
 * — no fake timers needed there. The boundary/interval describe further down
 * DOES use `vi.useFakeTimers()` + `vi.setSystemTime()`, since exact half-open
 * range-edge behaviour and 30s-tick/cleanup coverage both require pinning
 * the clock precisely (same convention as useAutoSave.test.tsx).
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, waitFor, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { EventInput, EventClickArg } from '@fullcalendar/core'
import { CalendarScreen } from './CalendarScreen'
import type { CalendarEventExtProps, CalendarViewName } from '@/components/calendar/types'

// ── 1. Mock FullCalendarView — capture events + onDatesSet + onEventClick ──────

type CapturedProps = {
  events: EventInput[]
  onEventClick: ((arg: EventClickArg) => void) | null
  onDatesSet: ((title: string, view: CalendarViewName, activeStart: Date, activeEnd: Date) => void) | null
}

const capturedProps: CapturedProps = {
  events: [],
  onEventClick: null,
  onDatesSet: null,
}

vi.mock('@/components/calendar/FullCalendarView', () => ({
  FullCalendarView: (props: {
    events: EventInput[]
    onEventClick: (arg: EventClickArg) => void
    onDatesSet?: (title: string, view: CalendarViewName, activeStart: Date, activeEnd: Date) => void
  }) => {
    capturedProps.events = props.events
    capturedProps.onEventClick = props.onEventClick
    capturedProps.onDatesSet = props.onDatesSet ?? null
    return <div data-testid="fullcalendar-stub">FullCalendar stub</div>
  },
}))

vi.mock('@/components/calendar/CalendarToolbar', () => ({
  CalendarToolbar: () => <div data-testid="calendar-toolbar-stub" />,
}))

// ── 2. Mock slide-overs / popover — capture state so a no-op click is provable ─

const capturedEventSlideOver = { open: false, taskId: '' }

vi.mock('@/components/calendar/CalendarEventSlideOver', () => ({
  CalendarEventSlideOver: ({
    open,
    task,
  }: {
    open: boolean
    task: { id: string } | null
  }) => {
    capturedEventSlideOver.open = open
    capturedEventSlideOver.taskId = task?.id ?? ''
    return <div data-testid="calendar-event-slideover" data-open={String(open)} />
  },
}))

const capturedTaskDetailSlideOver = { taskId: '' }

vi.mock('@/components/workspaces/TaskDetailSlideOver', () => ({
  TaskDetailSlideOver: ({ task }: { task: { id: string } | null }) => {
    capturedTaskDetailSlideOver.taskId = task?.id ?? ''
    return <div data-testid="task-detail-slideover" data-task-id={task?.id ?? ''} />
  },
}))

// ── 3. Mock @/lib/api ─────────────────────────────────────────────────────────

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchTasks: vi.fn(),
    fetchAgents: vi.fn().mockResolvedValue([]),
    fetchWorkspaceDelegation: vi.fn().mockRejectedValue(new Error('not mocked')),
    updateTask: vi.fn(),
    tasksQueryKeys: {
      list: (params?: Record<string, unknown>) => ['tasks', params ?? {}],
    },
  }
})

import { fetchTasks } from '@/lib/api'

// ── 4. Mock useOccurrences (module boundary — no real fetch/URL parsing) ────

const mockUseOccurrences = vi.fn()
vi.mock('@/lib/calendar/useOccurrences', () => ({
  useOccurrences: (...args: unknown[]) => mockUseOccurrences(...args),
}))

// ── 5. Mock @/store/ui ────────────────────────────────────────────────────────

const mockAddToast = vi.fn()

vi.mock('@/store/ui', () => ({
  useUiStore: (selector?: (s: { addToast: ReturnType<typeof vi.fn> }) => unknown) => {
    const store = { addToast: mockAddToast }
    return selector ? selector(store) : store
  },
}))

// ── 6. Fixtures ───────────────────────────────────────────────────────────────

const WORKSPACE_ID = 'ws-test-123'

// A "due" task lands the calendar a `task-due` chip via mapToCalendarEvents —
// present so `filteredEvents.length > 0` (the marker's inclusion condition
// requires at least one real item to divide; see CalendarScreen.tsx). Kept
// far from "now" (a fixed 2026-06-20 date) so it never collides with the
// marker's own real-time-derived position in any of these tests.
function makeTask(overrides: Record<string, unknown> = {}) {
  return {
    id: 'task-1',
    title: 'Test Task',
    action: 'llm' as const,
    status: 'next' as const,
    priority: 3,
    workspace_id: WORKSPACE_ID,
    owner: 'alice',
    created_by: 'alice',
    created_at: '2026-06-20T10:00:00Z',
    updated_at: '2026-06-20T10:00:00Z',
    surface: 'user' as const,
    due: '2026-06-20T00:00:00Z',
    ...overrides,
  }
}

function makeClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } },
  })
}

function renderCalendarScreen() {
  return render(
    <QueryClientProvider client={makeClient()}>
      <CalendarScreen workspaceId={WORKSPACE_ID} />
    </QueryClientProvider>,
  )
}

/** True when `events` contains the synthetic now-marker EventInput. */
function hasNowMarker(events: EventInput[]): boolean {
  return events.some(
    (e) => e.id === 'now-marker' && (e.extendedProps as CalendarEventExtProps | undefined)?.kind === 'now-marker',
  )
}

beforeEach(() => {
  capturedProps.events = []
  capturedProps.onEventClick = null
  capturedProps.onDatesSet = null
  capturedEventSlideOver.open = false
  capturedEventSlideOver.taskId = ''
  capturedTaskDetailSlideOver.taskId = ''

  // A real task by default so `filteredEvents.length > 0` — the marker's
  // inclusion condition requires at least one real item to divide (see
  // makeTask's own comment). The dedicated "no real events" test below
  // overrides this back to `[]` to cover that path explicitly.
  vi.mocked(fetchTasks).mockResolvedValue([makeTask()])
  mockAddToast.mockReset()
  mockUseOccurrences.mockReturnValue({ data: [], isError: false, isLoading: false })
})

// ── Tests ─────────────────────────────────────────────────────────────────────

describe('CalendarScreen — Agenda "now" marker inclusion (listWeek only, in-range only)', () => {
  it('includes the now-marker when the active view is listWeek AND "now" falls inside the visible range', async () => {
    renderCalendarScreen()

    await waitFor(() => expect(capturedProps.onDatesSet).not.toBeNull())

    const now = Date.now()
    const activeStart = new Date(now - 24 * 60 * 60 * 1000) // yesterday
    const activeEnd = new Date(now + 24 * 60 * 60 * 1000) // tomorrow

    await act(async () => {
      capturedProps.onDatesSet!('This Week', 'listWeek', activeStart, activeEnd)
    })

    await waitFor(() => expect(hasNowMarker(capturedProps.events)).toBe(true))
  })

  it('excludes the now-marker when the active view is NOT listWeek, even though "now" is inside the range', async () => {
    renderCalendarScreen()

    await waitFor(() => expect(capturedProps.onDatesSet).not.toBeNull())

    const now = Date.now()
    const activeStart = new Date(now - 24 * 60 * 60 * 1000)
    const activeEnd = new Date(now + 24 * 60 * 60 * 1000)

    await act(async () => {
      capturedProps.onDatesSet!('June 2026', 'dayGridMonth', activeStart, activeEnd)
    })

    // Give any pending re-render a chance to land, then assert it never appears.
    await waitFor(() => expect(capturedProps.events).toBeDefined())
    expect(hasNowMarker(capturedProps.events)).toBe(false)
  })

  it('excludes the now-marker when listWeek is active but "now" falls OUTSIDE the visible range', async () => {
    renderCalendarScreen()

    await waitFor(() => expect(capturedProps.onDatesSet).not.toBeNull())

    const now = Date.now()
    // A week comfortably in the future — "now" cannot be inside it.
    const activeStart = new Date(now + 100 * 24 * 60 * 60 * 1000)
    const activeEnd = new Date(now + 107 * 24 * 60 * 60 * 1000)

    await act(async () => {
      capturedProps.onDatesSet!('Far Future Week', 'listWeek', activeStart, activeEnd)
    })

    await waitFor(() => expect(capturedProps.events).toBeDefined())
    expect(hasNowMarker(capturedProps.events)).toBe(false)
  })

  it('differentiation: switching the SAME range from dayGridMonth to listWeek makes the marker appear', async () => {
    renderCalendarScreen()

    await waitFor(() => expect(capturedProps.onDatesSet).not.toBeNull())

    const now = Date.now()
    const activeStart = new Date(now - 24 * 60 * 60 * 1000)
    const activeEnd = new Date(now + 24 * 60 * 60 * 1000)

    await act(async () => {
      capturedProps.onDatesSet!('June 2026', 'dayGridMonth', activeStart, activeEnd)
    })
    expect(hasNowMarker(capturedProps.events)).toBe(false)

    await act(async () => {
      capturedProps.onDatesSet!('This Week', 'listWeek', activeStart, activeEnd)
    })
    await waitFor(() => expect(hasNowMarker(capturedProps.events)).toBe(true))
  })

  it('never shows the marker when there are zero real events — it would coexist with the "No scheduled items" empty-state hint, which reads as contradictory', async () => {
    vi.mocked(fetchTasks).mockResolvedValue([])
    renderCalendarScreen()

    await waitFor(() => expect(capturedProps.onDatesSet).not.toBeNull())

    const now = Date.now()
    const activeStart = new Date(now - 24 * 60 * 60 * 1000)
    const activeEnd = new Date(now + 24 * 60 * 60 * 1000)

    await act(async () => {
      capturedProps.onDatesSet!('This Week', 'listWeek', activeStart, activeEnd)
    })

    await waitFor(() => expect(capturedProps.events).toBeDefined())
    expect(hasNowMarker(capturedProps.events)).toBe(false)
    expect(capturedProps.events.length).toBe(0)
  })
})

describe('CalendarScreen — now-marker click is a no-op (mirrors task-occurrence-more)', () => {
  it('clicking the now-marker does not open the event slide-over or task detail panel, and does not throw', async () => {
    renderCalendarScreen()

    await waitFor(() => expect(capturedProps.onEventClick).not.toBeNull())

    const ext: CalendarEventExtProps = { kind: 'now-marker', timeLabel: '3:05 PM' }

    await expect(
      act(async () => {
        capturedProps.onEventClick!({
          event: { extendedProps: ext },
          jsEvent: { target: document.createElement('div') },
        } as unknown as EventClickArg)
      }),
    ).resolves.not.toThrow()

    expect(capturedEventSlideOver.open).toBe(false)
    expect(capturedTaskDetailSlideOver.taskId).toBe('')
  })
})

describe('CalendarScreen — now-marker range boundary + 30s tick + interval cleanup', () => {
  const FIXED_NOW = new Date('2026-07-20T12:00:00.000Z').getTime()

  beforeEach(() => {
    // Scoped fake — NOT a blanket vi.useFakeTimers(). Faking setTimeout too
    // freezes React's own scheduler and Testing Library's `waitFor` polling
    // (both lean on setTimeout/MessageChannel internally), hanging every
    // `await waitFor(...)` below until the real 15s test timeout. Only the
    // APIs this code under test actually touches (setInterval/clearInterval
    // for the 30s tick, Date for `Date.now()`) need to be fake.
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval', 'Date'] })
    vi.setSystemTime(FIXED_NOW)
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('includes the marker when "now" equals activeRange.start exactly (inclusive lower bound)', async () => {
    renderCalendarScreen()
    await waitFor(() => expect(capturedProps.onDatesSet).not.toBeNull())

    await act(async () => {
      capturedProps.onDatesSet!('Boundary', 'listWeek', new Date(FIXED_NOW), new Date(FIXED_NOW + 60_000))
    })

    expect(hasNowMarker(capturedProps.events)).toBe(true)
  })

  it('excludes the marker when "now" equals activeRange.end exactly (exclusive upper bound)', async () => {
    renderCalendarScreen()
    await waitFor(() => expect(capturedProps.onDatesSet).not.toBeNull())

    await act(async () => {
      capturedProps.onDatesSet!('Boundary', 'listWeek', new Date(FIXED_NOW - 60_000), new Date(FIXED_NOW))
    })

    expect(hasNowMarker(capturedProps.events)).toBe(false)
  })

  it('has an end strictly after start (NOT equal) — never spans past midnight into a second day', async () => {
    // Regression, two layers deep:
    //  1. Without an explicit `end`, FullCalendar assigns its own
    //     defaultTimedEventDuration (1 hour) to a timed event. Late at night
    //     that pushed the marker's span across midnight, and Agenda's list
    //     view renders a segment of any day-spanning event on EVERY day it
    //     touches — reported live (Asia/Jakarta, ~23:20 local): the marker
    //     appeared twice, once under today and once under tomorrow.
    //  2. The first fix attempt (end === start) was ALSO wrong and produced
    //     the SAME live symptom — confirmed by re-testing after deploying
    //     it. @fullcalendar/core's own parseSingle treats `end <= start` as
    //     "no end provided" (`if (startMarker && endMarker <= startMarker)
    //     endMarker = null`) and falls through to the identical 1-hour
    //     default. `end` must be STRICTLY after `start`.
    const LATE_NIGHT = new Date('2026-07-20T23:24:00.000Z').getTime()
    vi.setSystemTime(LATE_NIGHT)
    renderCalendarScreen()
    await waitFor(() => expect(capturedProps.onDatesSet).not.toBeNull())

    await act(async () => {
      capturedProps.onDatesSet!('Late night', 'listWeek', new Date(LATE_NIGHT - 3_600_000), new Date(LATE_NIGHT + 3_600_000))
    })

    const marker = capturedProps.events.find((e) => e.id === 'now-marker')
    expect(marker?.start).toEqual(new Date(LATE_NIGHT))
    expect(marker?.end).toBeDefined()
    expect((marker!.end as Date).getTime()).toBeGreaterThan((marker!.start as Date).getTime())
    // ...and short enough to never itself reach the next day, regardless of
    // exactly how close to midnight "now" is (a multi-hour buffer, like the
    // very default this fix avoids, would just reintroduce the same bug).
    expect((marker!.end as Date).getTime() - (marker!.start as Date).getTime()).toBeLessThan(60_000)
  })

  it('re-ticks every 30s while Agenda stays open, advancing the marker\'s own start time', async () => {
    renderCalendarScreen()
    await waitFor(() => expect(capturedProps.onDatesSet).not.toBeNull())

    // Wide, static range so only the tick — not range validity — changes.
    await act(async () => {
      capturedProps.onDatesSet!('Wide', 'listWeek', new Date(FIXED_NOW - 3_600_000), new Date(FIXED_NOW + 3_600_000))
    })
    const marker1 = capturedProps.events.find((e) => e.id === 'now-marker')
    expect(marker1?.start).toEqual(new Date(FIXED_NOW))

    // advanceTimersByTime alone moves the faked Date forward as it processes
    // the elapsed virtual time — an extra vi.setSystemTime here would jump
    // the clock AND fire any now-due interval immediately, then
    // advanceTimersByTime fires it again, double-counting the tick.
    await act(async () => {
      vi.advanceTimersByTime(30_000)
    })

    const marker2 = capturedProps.events.find((e) => e.id === 'now-marker')
    expect(marker2?.start).toEqual(new Date(FIXED_NOW + 30_000))
  })

  it('resyncs immediately (not stale until the first tick) the instant Agenda becomes active', async () => {
    renderCalendarScreen()
    await waitFor(() => expect(capturedProps.onDatesSet).not.toBeNull())

    // Mount happens at FIXED_NOW (nowTick's initializer). Stay on Month for a
    // while — simulating the operator-reported "switch in after minutes on
    // another view" case — THEN advance the clock before switching to
    // Agenda. Without the immediate resync this fix added, the marker would
    // render at the STALE mount-time tick until the next 30s interval fires.
    await act(async () => {
      capturedProps.onDatesSet!('Month', 'dayGridMonth', new Date(FIXED_NOW - 3_600_000), new Date(FIXED_NOW + 3_600_000))
    })
    vi.setSystemTime(FIXED_NOW + 5 * 60_000) // 5 minutes later, still on Month

    await act(async () => {
      capturedProps.onDatesSet!('Agenda', 'listWeek', new Date(FIXED_NOW - 3_600_000), new Date(FIXED_NOW + 3_600_000))
    })

    const marker = capturedProps.events.find((e) => e.id === 'now-marker')
    expect(marker?.start).toEqual(new Date(FIXED_NOW + 5 * 60_000))
  })

  it('tears the interval down when leaving listWeek — no leaked timer ticking in the background', async () => {
    const clearIntervalSpy = vi.spyOn(window, 'clearInterval')
    renderCalendarScreen()
    await waitFor(() => expect(capturedProps.onDatesSet).not.toBeNull())

    await act(async () => {
      capturedProps.onDatesSet!('Wide', 'listWeek', new Date(FIXED_NOW - 3_600_000), new Date(FIXED_NOW + 3_600_000))
    })
    expect(hasNowMarker(capturedProps.events)).toBe(true)

    await act(async () => {
      capturedProps.onDatesSet!('Wide', 'dayGridMonth', new Date(FIXED_NOW - 3_600_000), new Date(FIXED_NOW + 3_600_000))
    })
    expect(clearIntervalSpy).toHaveBeenCalled()
    expect(hasNowMarker(capturedProps.events)).toBe(false)

    // Advancing time after leaving listWeek must produce no further re-tick —
    // the interval is gone, not just its output momentarily hidden by the
    // view gate.
    const callsBefore = clearIntervalSpy.mock.calls.length
    await act(async () => {
      vi.advanceTimersByTime(120_000)
    })
    expect(clearIntervalSpy.mock.calls.length).toBe(callsBefore) // no additional teardown — nothing to tear down
    expect(hasNowMarker(capturedProps.events)).toBe(false)
  })
})

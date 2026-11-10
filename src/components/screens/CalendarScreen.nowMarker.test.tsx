/**
 * CalendarScreen.nowMarker.test.tsx — SP-39 RETIREMENT pack (wave-3 join,
 * 2026-10-05).
 *
 * SUPERSEDED SCOPE: this pack used to cover the Agenda-view (`listWeek`)
 * live "now" divider — CalendarScreen appended a single synthetic
 * `kind: 'now-marker'` EventInput ONLY while Agenda was active and "now"
 * fell inside FullCalendar's reported visible range. SP-39 drops Agenda
 * (side-panel-shell-spec.md §13 SP-39 — "Calendar docks default Week,
 * view selectable … the spec names only Day/Week/Month"; wireframe §4
 * "Agenda is dropped"), and the join removed the marker machinery from
 * CalendarScreen/FullCalendarView/types.ts with it. The old tests'
 * expected behaviour is therefore EXPRESSLY superseded by the approved
 * spec, and the pack now pins the RETIREMENT instead — with the same
 * strength, because every old inclusion scenario becomes an absence
 * oracle that fails if the marker (or the Agenda view it served) ever
 * comes back:
 *
 *   - no surviving view (Week/Day/Month — the only views SP-39 names)
 *     ever receives a now-marker event, in range or out;
 *   - the old clock boundaries (now == range start / end, 30s re-ticks)
 *     never produce one either;
 *   - every event handed to FullCalendarView carries one of the five
 *     surviving `CalendarEventExtProps` kinds — nothing synthetic exists
 *     to click (supersedes the old click no-op tests).
 *
 * Strategy unchanged (spec §9 F-03): jsdom cannot lay out FullCalendar's
 * DOM, so FullCalendarView is mocked at the module boundary and its
 * captured `events`/`onDatesSet` props drive the assertions. The old
 * 1:1 supersession mapping is cited per test.
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
  isLoading: boolean
}

const capturedProps: CapturedProps = {
  events: [],
  onEventClick: null,
  onDatesSet: null,
  isLoading: true,
}

vi.mock('@/components/calendar/FullCalendarView', () => ({
  FullCalendarView: (props: {
    events: EventInput[]
    onEventClick: (arg: EventClickArg) => void
    onDatesSet?: (title: string, view: CalendarViewName, activeStart: Date, activeEnd: Date) => void
    isLoading?: boolean
  }) => {
    capturedProps.events = props.events
    capturedProps.onEventClick = props.onEventClick
    capturedProps.onDatesSet = props.onDatesSet ?? null
    capturedProps.isLoading = props.isLoading ?? false
    // Loading is observable DOM state, so MutationObserver can wake waitFor
    // even when fake interval polling is paused during the clock cases.
    return <div data-testid="fullcalendar-stub" data-loading={String(props.isLoading ?? false)}>FullCalendar stub</div>
  },
}))

vi.mock('@/components/calendar/CalendarToolbar', () => ({
  CalendarToolbar: () => <div data-testid="calendar-toolbar-stub" />,
}))

// ── 2. Mock slide-overs — the marker's old click-no-op contract is covered
//    by the events-kind invariant (nothing synthetic exists to click), so the
//    slide-over captures stay to prove a real event still opens normally.

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

const capturedTaskDetail = { taskId: '' }
vi.mock('@/components/workspaces/TaskDetailSlideOver', () => ({
  TaskDetailSlideOver: ({ task }: { task: { id: string } | null }) => {
    capturedTaskDetail.taskId = task?.id ?? ''
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

// A real "due" task so `filteredEvents.length > 0` — the old marker's
// inclusion condition required at least one real item; the retirement
// oracles hold with or without it, and the real chip doubles as the
// known-kind control in the invariant test.
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

/** True when `events` contains the synthetic now-marker EventInput — the
 * exact detector the old inclusion tests used; the retirement oracle is its
 * negation everywhere. The kind is compared as a widened string: the
 * generated union no longer contains `'now-marker'`, so a typed comparison
 * would be a compile-time tautology — the whole point of this detector is to
 * catch a REINTRODUCED marker the types do not know about. */
function hasNowMarker(events: EventInput[]): boolean {
  return events.some(
    (e) =>
      e.id === 'now-marker' &&
      (e.extendedProps as { kind?: string } | undefined)?.kind === 'now-marker',
  )
}

/** The five surviving CalendarEventExtProps kinds (SP-39 retired no kind
 * besides adding none; 'now-marker' is NOT among them). */
const SURVIVING_KINDS: CalendarEventExtProps['kind'][] = [
  'task-due',
  'task-fire',
  'task-occurrence',
  'task-occurrence-agg',
  'task-occurrence-more',
]

function kindsOf(events: EventInput[]): string[] {
  return events.map((e) => (e.extendedProps as CalendarEventExtProps | undefined)?.kind ?? '<none>')
}

/** Wait for actual query completion, not an already-defined empty array. */
async function renderReadyCalendar() {
  const mounted = renderCalendarScreen()
  await waitFor(() => {
    expect(capturedProps.onDatesSet).not.toBeNull()
    expect(capturedProps.isLoading).toBe(false)
  })
  return mounted
}

async function reportRange(view: CalendarViewName, activeStart: Date, activeEnd: Date) {
  await act(async () => {
    capturedProps.onDatesSet!('Test Range', view, activeStart, activeEnd)
  })
  return capturedProps.events
}

async function eventsForView(view: CalendarViewName, activeStart: Date, activeEnd: Date) {
  await renderReadyCalendar()
  return reportRange(view, activeStart, activeEnd)
}

function assertNoAgendaMarker(events: EventInput[]) {
  expect(hasNowMarker(events), 'SP-39: no retired Agenda marker is emitted').toBe(false)
  for (const kind of kindsOf(events)) {
    expect(SURVIVING_KINDS, `unexpected emitted event kind ${kind}`).toContain(kind)
  }
}

beforeEach(() => {
  capturedProps.events = []
  capturedProps.onEventClick = null
  capturedProps.onDatesSet = null
  capturedProps.isLoading = true
  capturedEventSlideOver.open = false
  capturedEventSlideOver.taskId = ''
  capturedTaskDetail.taskId = ''

  vi.mocked(fetchTasks).mockResolvedValue([makeTask()])
  mockAddToast.mockReset()
  mockUseOccurrences.mockReturnValue({ data: [], isError: false, isLoading: false })
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.useRealTimers()
})

// ── Tests ─────────────────────────────────────────────────────────────────────

// One separate case for each of the 12 inherited Agenda cases; SP-39 is the
// supersession authority. Numeric range/tick inputs retain their old roles.
describe('CalendarScreen — SP-39 supersessions of all inherited Agenda cases', () => {
  it('in-range inclusion superseded: Week retains real events but emits no Agenda marker', async () => {
    const now = Date.now()
    const events = await eventsForView('timeGridWeek', new Date(now - 86_400_000), new Date(now + 86_400_000))
    expect(kindsOf(events)).toEqual(['task-due'])
    assertNoAgendaMarker(events)
  })

  it('non-Agenda exclusion retained: Day emits no synthetic marker with now inside the range', async () => {
    const now = Date.now()
    const events = await eventsForView('timeGridDay', new Date(now - 86_400_000), new Date(now + 86_400_000))
    expect(kindsOf(events)).toEqual(['task-due'])
    assertNoAgendaMarker(events)
  })

  it('out-of-range exclusion retained: a future Week range cannot emit the retired marker', async () => {
    const now = Date.now()
    // Original case used a range 100..107 days ahead of now.
    const events = await eventsForView('timeGridWeek', new Date(now + 100 * 86_400_000), new Date(now + 107 * 86_400_000))
    expect(kindsOf(events)).toEqual(['task-due'])
    assertNoAgendaMarker(events)
  })

  it('same-range differentiation superseded: switching Month to Week never adds a synthetic item', async () => {
    const now = Date.now()
    const start = new Date(now - 86_400_000)
    const end = new Date(now + 86_400_000)
    const month = await eventsForView('dayGridMonth', start, end)
    expect(kindsOf(month)).toEqual(['task-due'])
    assertNoAgendaMarker(month)
    const week = await reportRange('timeGridWeek', start, end)
    expect(week).toEqual(month)
    assertNoAgendaMarker(week)
  })

  it('zero-real-events exclusion retained: the empty view never grows a synthetic marker', async () => {
    vi.mocked(fetchTasks).mockResolvedValue([])
    const now = Date.now()
    const events = await eventsForView('timeGridWeek', new Date(now - 86_400_000), new Date(now + 86_400_000))
    expect(events).toEqual([])
    assertNoAgendaMarker(events)
  })

  it('marker-click case superseded: no synthetic clickable event exists, while the real due item stays reachable', async () => {
    const events = await eventsForView('timeGridWeek', new Date('2026-06-13T00:00:00'), new Date('2026-06-27T00:00:00'))
    expect(kindsOf(events)).toEqual(['task-due'])
    assertNoAgendaMarker(events)
    expect(capturedEventSlideOver.open).toBe(false)
    const real = events[0]
    // The old test clicked a kind removed with Agenda. The surviving real
    // chip remains actionable; no unsupported synthetic payload is invented.
    await act(async () => {
      capturedProps.onEventClick!({
        event: { ...real, start: new Date('2026-06-20T00:00:00') },
        el: document.createElement('div'),
      } as unknown as EventClickArg)
    })
    // Due/fire items retain their original TaskDetailSlideOver path;
    // occurrence items use CalendarEventSlideOver. Agenda retirement does
    // not change that surviving routing distinction.
    expect(capturedTaskDetail.taskId).toBe('task-1')
    expect(capturedEventSlideOver.open).toBe(false)
  })

  it('inclusive-start boundary superseded: now exactly at range start still emits no Agenda marker', async () => {
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(new Date('2026-08-15T10:00:00'))
    const events = await eventsForView('timeGridWeek', new Date('2026-08-15T10:00:00'), new Date('2026-08-22T10:00:00'))
    expect(kindsOf(events)).toEqual(['task-due'])
    assertNoAgendaMarker(events)
  })

  it('exclusive-end boundary retained: now exactly at range end emits no Agenda marker', async () => {
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(new Date('2026-08-22T10:00:00'))
    const events = await eventsForView('timeGridWeek', new Date('2026-08-15T10:00:00'), new Date('2026-08-22T10:00:00'))
    expect(kindsOf(events)).toEqual(['task-due'])
    assertNoAgendaMarker(events)
  })

  it('midnight-duration case superseded: no synthetic interval can span either side of midnight', async () => {
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(new Date('2026-08-15T23:59:59.999'))
    const before = await eventsForView('timeGridWeek', new Date('2026-08-15T00:00:00'), new Date('2026-08-17T00:00:00'))
    expect(kindsOf(before)).toEqual(['task-due'])
    assertNoAgendaMarker(before)
    vi.setSystemTime(new Date('2026-08-16T00:00:00'))
    const after = await reportRange('timeGridWeek', new Date('2026-08-15T00:00:00'), new Date('2026-08-17T00:00:00'))
    expect(after).toEqual(before)
    assertNoAgendaMarker(after)
  })

  it('30-second-tick case superseded: clock advance never schedules an Agenda-marker interval or changes real events', async () => {
    // Fake the actual interval APIs as well as Date; advancing Date-only
    // timers would not exercise the old 30s timer path.
    vi.useFakeTimers({ toFake: ['Date', 'setInterval', 'clearInterval'] })
    vi.setSystemTime(new Date('2026-08-15T10:00:00'))
    const intervals = vi.spyOn(window, 'setInterval')
    const initial = await eventsForView('timeGridWeek', new Date('2026-08-14T00:00:00'), new Date('2026-08-17T00:00:00'))
    expect(kindsOf(initial)).toEqual(['task-due'])
    await act(async () => { vi.advanceTimersByTime(90_000) })
    assertNoAgendaMarker(capturedProps.events)
    expect(capturedProps.events).toEqual(initial)
    expect(intervals.mock.calls.filter((args) => args[1] === 30_000)).toEqual([])
    intervals.mockRestore()
  })

  it('activation-resync case superseded: entering Week after a clock jump never emits a marker immediately or later', async () => {
    vi.useFakeTimers({ toFake: ['Date', 'setInterval', 'clearInterval'] })
    vi.setSystemTime(new Date('2026-08-15T10:00:00'))
    const start = new Date('2026-08-14T00:00:00')
    const end = new Date('2026-08-17T00:00:00')
    const initial = await eventsForView('dayGridMonth', start, end)
    vi.setSystemTime(new Date('2026-08-15T12:00:00'))
    const week = await reportRange('timeGridWeek', start, end)
    expect(week).toEqual(initial)
    assertNoAgendaMarker(week)
    await act(async () => { vi.advanceTimersByTime(30_000) })
    expect(capturedProps.events).toEqual(initial)
    assertNoAgendaMarker(capturedProps.events)
  })

  it('interval-cleanup case superseded: leaving Week and unmounting retains no retired marker timer', async () => {
    vi.useFakeTimers({ toFake: ['Date', 'setInterval', 'clearInterval'] })
    vi.setSystemTime(new Date('2026-08-15T10:00:00'))
    const intervals = vi.spyOn(window, 'setInterval')
    const mounted = await renderReadyCalendar()
    const start = new Date('2026-08-14T00:00:00')
    const end = new Date('2026-08-17T00:00:00')
    await reportRange('timeGridWeek', start, end)
    const day = await reportRange('timeGridDay', start, end)
    expect(kindsOf(day)).toEqual(['task-due'])
    mounted.unmount()
    await act(async () => { vi.advanceTimersByTime(90_000) })
    expect(capturedProps.events).toEqual(day)
    assertNoAgendaMarker(day)
    expect(intervals.mock.calls.filter((args) => args[1] === 30_000)).toEqual([])
    intervals.mockRestore()
  })
})

describe('retirement detector instrument', () => {
  it('accepts the real kind control but rejects an injected synthetic marker', () => {
    const real: EventInput[] = [{ id: 'due', extendedProps: { kind: 'task-due' } }]
    expect(() => assertNoAgendaMarker(real)).not.toThrow()
    const mutant: EventInput[] = [...real, { id: 'now-marker', extendedProps: { kind: 'now-marker' } }]
    expect(() => assertNoAgendaMarker(mutant)).toThrow(/no retired Agenda marker/)
  })
})

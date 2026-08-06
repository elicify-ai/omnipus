/**
 * CalendarScreen.occurrencesDegrade.test.tsx — test 22 of the Calendar
 * Recurrence Redesign TDD plan.
 *
 * Spec: docs/internal/specs/calendar-recurrence-redesign-spec.md
 * Traces to BDD: "Occurrences query failure degrades without blanking the
 * grid" (US-2 AS-1 degrade path, FR-017); plus wiring coverage for
 * "Weekly task renders on every Monday of the month" (occurrence chips join
 * `events` before the agent filter) and "Clicking a due chip keeps today's
 * panel" / occurrence-chip click routing (FR-001/FR-012).
 *
 * Strategy mirrors CalendarScreen.test.tsx (spec §9 — F-03): FullCalendarView
 * is mocked at the module boundary and its captured `events`/handlers props
 * drive the assertions; jsdom cannot render FullCalendar layout itself.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { EventInput } from '@fullcalendar/core'
import type { EventClickArg } from '@fullcalendar/core'
import { CalendarScreen } from './CalendarScreen'
import type { CalendarEventExtProps } from '@/components/calendar/types'

// ── 1. Mock FullCalendarView — capture events + handlers ───────────────────

type CapturedProps = {
  events: EventInput[]
  onEventClick: ((arg: EventClickArg) => void) | null
  isLoading: boolean | undefined
  isEmpty: boolean | undefined
}

const capturedProps: CapturedProps = {
  events: [],
  onEventClick: null,
  isLoading: undefined,
  isEmpty: undefined,
}

vi.mock('@/components/calendar/FullCalendarView', () => ({
  FullCalendarView: (props: {
    events: EventInput[]
    onEventClick: (arg: EventClickArg) => void
    isLoading?: boolean
    isEmpty?: boolean
  }) => {
    capturedProps.events = props.events
    capturedProps.onEventClick = props.onEventClick
    capturedProps.isLoading = props.isLoading
    capturedProps.isEmpty = props.isEmpty
    return <div data-testid="fullcalendar-stub">FullCalendar stub</div>
  },
}))

vi.mock('@/components/calendar/CalendarToolbar', () => ({
  CalendarToolbar: () => <div data-testid="calendar-toolbar-stub" />,
}))

// ── 2. Mock slide-overs / popover with testid stubs ─────────────────────────

const capturedEventSlideOver: {
  open: boolean
  taskId: string
  selectedOccurrenceMs: number | undefined
  selectedBucketDayRange: { startMs: number; endMs: number } | null | undefined
} = {
  open: false,
  taskId: '',
  selectedOccurrenceMs: undefined,
  selectedBucketDayRange: undefined,
}

vi.mock('@/components/calendar/CalendarEventSlideOver', () => ({
  CalendarEventSlideOver: ({
    open,
    task,
    selectedOccurrenceMs,
    selectedBucketDayRange,
  }: {
    open: boolean
    task: { id: string } | null
    selectedOccurrenceMs?: number
    selectedBucketDayRange?: { startMs: number; endMs: number } | null
  }) => {
    capturedEventSlideOver.open = open
    capturedEventSlideOver.taskId = task?.id ?? ''
    capturedEventSlideOver.selectedOccurrenceMs = selectedOccurrenceMs
    capturedEventSlideOver.selectedBucketDayRange = selectedBucketDayRange
    return <div data-testid="calendar-event-slideover" data-open={String(open)} data-task-id={task?.id ?? ''} />
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
    trigger: { type: 'once' as const, config: { at_ms: new Date(2026, 5, 22, 9, 0, 0).getTime() } },
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

beforeEach(() => {
  capturedProps.events = []
  capturedProps.onEventClick = null
  capturedProps.isLoading = undefined
  capturedProps.isEmpty = undefined
  capturedEventSlideOver.open = false
  capturedEventSlideOver.taskId = ''
  capturedEventSlideOver.selectedOccurrenceMs = undefined
  capturedEventSlideOver.selectedBucketDayRange = undefined
  capturedTaskDetailSlideOver.taskId = ''

  vi.mocked(fetchTasks).mockResolvedValue([makeTask()])
  mockAddToast.mockReset()
  mockUseOccurrences.mockReset()
})

// ── Tests ─────────────────────────────────────────────────────────────────────

describe('CalendarScreen — occurrences query failure degrades without blanking the grid (FR-017)', () => {
  it('still renders due/fire chips and shows a non-blocking "Couldn\'t load recurring occurrences" toast', async () => {
    mockUseOccurrences.mockReturnValue({
      data: undefined,
      isError: true,
      isLoading: false,
      error: new Error('500 Internal Server Error'),
    })

    renderCalendarScreen()

    // Grid still renders.
    expect(await screen.findByTestId('fullcalendar-stub')).toBeInTheDocument()

    // The due/fire chips for the seeded task are still present in `events`.
    await waitFor(() => {
      const kinds = capturedProps.events.map((e) => (e.extendedProps as CalendarEventExtProps).kind)
      expect(kinds).toContain('task-due')
      expect(kinds).toContain('task-fire')
    })

    // Non-blocking toast, distinct from the tasks degrade message.
    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith(
        expect.objectContaining({ message: "Couldn't load recurring occurrences", variant: 'error' }),
      )
    })
  })

  it('renders due/fire chips normally when occurrences load with zero sets (no recurring tasks)', async () => {
    mockUseOccurrences.mockReturnValue({ data: [], isError: false, isLoading: false })

    renderCalendarScreen()

    await waitFor(() => {
      const kinds = capturedProps.events.map((e) => (e.extendedProps as CalendarEventExtProps).kind)
      expect(kinds).toContain('task-due')
      expect(kinds).toContain('task-fire')
    })
    expect(mockAddToast).not.toHaveBeenCalledWith(
      expect.objectContaining({ message: "Couldn't load recurring occurrences" }),
    )
  })
})

describe('CalendarScreen — occurrence chip-click routing (FR-001/FR-012)', () => {
  it('clicking an occurrence chip opens CalendarEventSlideOver in edit mode, not the task detail panel', async () => {
    mockUseOccurrences.mockReturnValue({ data: [], isError: false, isLoading: false })
    renderCalendarScreen()

    // Wait for tasks to load (events non-empty) rather than just
    // "onEventClick is non-null" — a useCallback ref is non-null from the
    // very first render too, and grabbing it too early captures a stale
    // closure over the pre-load empty `tasks` array.
    await waitFor(() => expect(capturedProps.events.length).toBeGreaterThan(0))

    const occurrenceMs = 1_784_620_800_000
    const ext: CalendarEventExtProps = {
      kind: 'task-occurrence',
      taskId: 'task-1',
      status: 'scheduled',
      icon: 'Clock',
      occurrenceMs,
    }
    await act(async () => {
      capturedProps.onEventClick!({
        event: { extendedProps: ext },
        jsEvent: { target: document.createElement('div') },
      } as unknown as EventClickArg)
    })

    await waitFor(() => expect(capturedEventSlideOver.open).toBe(true))
    expect(capturedEventSlideOver.taskId).toBe('task-1')
    expect(capturedTaskDetailSlideOver.taskId).toBe('')
    // ADR-050 RD8 / task-run-history-spec.md §4.1: the individual instant's
    // own occurrenceMs is threaded through as selectedOccurrenceMs.
    expect(capturedEventSlideOver.selectedOccurrenceMs).toBe(occurrenceMs)
    // Mirror image (H2 seam): an individual-instant click leaves the bucket
    // day range unset — it re-points at ONE run, not a day's worth.
    expect(capturedEventSlideOver.selectedBucketDayRange).toBeNull()
  })

  it('clicking an aggregated bucket chip opens the slide-over WITHOUT a selectedOccurrenceMs, WITH a selectedBucketDayRange sourced verbatim from the wire (delta-review HIGH fix)', async () => {
    // A bucket's day_start_ms is not a real occurrence instant and would
    // never exact-match a run's occurrence_ms in the slide-over — passing it
    // as selectedOccurrenceMs would silently hide the Result/Open-in-Chat
    // sections instead of falling back to task-level behaviour. Instead
    // (H2 seam) it threads a `selectedBucketDayRange` so the slide-over can
    // day-scope its run mini-list to just that bucket's day.
    mockUseOccurrences.mockReturnValue({ data: [], isError: false, isLoading: false })
    renderCalendarScreen()

    await waitFor(() => expect(capturedProps.events.length).toBeGreaterThan(0))

    const dayStartMs = 1_784_592_000_000
    // Deliberately a 23h span (NOT dayStartMs + 24h) — simulates the server's
    // DST-aware `day_end_ms` on a spring-forward transition day. If
    // CalendarScreen still recomputed a fixed dayStartMs+24h span instead of
    // reading `ext.dayEndMs` off the wire, this assertion would fail —
    // proving the client no longer recomputes the boundary itself.
    const dayEndMs = dayStartMs + 23 * 60 * 60 * 1000
    const ext: CalendarEventExtProps = {
      kind: 'task-occurrence-agg',
      taskId: 'task-1',
      status: 'done',
      icon: 'CheckCircle',
      tooltip: '3 done',
      dayStartMs,
      dayEndMs,
    }
    await act(async () => {
      capturedProps.onEventClick!({
        event: { extendedProps: ext },
        jsEvent: { target: document.createElement('div') },
      } as unknown as EventClickArg)
    })

    await waitFor(() => expect(capturedEventSlideOver.open).toBe(true))
    expect(capturedEventSlideOver.taskId).toBe('task-1')
    expect(capturedEventSlideOver.selectedOccurrenceMs).toBeUndefined()
    expect(capturedEventSlideOver.selectedBucketDayRange).toEqual({
      startMs: dayStartMs,
      endMs: dayEndMs,
    })
  })

  it('clicking a due chip still opens the existing task detail panel, not the event slide-over', async () => {
    mockUseOccurrences.mockReturnValue({ data: [], isError: false, isLoading: false })
    renderCalendarScreen()

    // Wait for tasks to load (events non-empty) rather than just
    // "onEventClick is non-null" — a useCallback ref is non-null from the
    // very first render too, and grabbing it too early captures a stale
    // closure over the pre-load empty `tasks` array.
    await waitFor(() => expect(capturedProps.events.length).toBeGreaterThan(0))

    const ext: CalendarEventExtProps = {
      kind: 'task-due',
      taskId: 'task-1',
      status: 'next',
      icon: 'Circle',
    }
    await act(async () => {
      capturedProps.onEventClick!({
        event: { extendedProps: ext },
        jsEvent: { target: document.createElement('div') },
      } as unknown as EventClickArg)
    })

    await waitFor(() => expect(capturedTaskDetailSlideOver.taskId).toBe('task-1'))
    expect(capturedEventSlideOver.open).toBe(false)
  })

  it('clicking the truncation marker chip is a no-op (non-interactive)', async () => {
    mockUseOccurrences.mockReturnValue({ data: [], isError: false, isLoading: false })
    renderCalendarScreen()

    // Wait for tasks to load (events non-empty) rather than just
    // "onEventClick is non-null" — a useCallback ref is non-null from the
    // very first render too, and grabbing it too early captures a stale
    // closure over the pre-load empty `tasks` array.
    await waitFor(() => expect(capturedProps.events.length).toBeGreaterThan(0))

    const ext: CalendarEventExtProps = {
      kind: 'task-occurrence-more',
      taskId: 'task-1',
      status: 'next',
      icon: 'Clock',
      tooltip: 'More occurrences not shown',
    }
    await act(async () => {
      capturedProps.onEventClick!({
        event: { extendedProps: ext },
        jsEvent: { target: document.createElement('div') },
      } as unknown as EventClickArg)
    })

    expect(capturedEventSlideOver.open).toBe(false)
    expect(capturedTaskDetailSlideOver.taskId).toBe('')
  })
})

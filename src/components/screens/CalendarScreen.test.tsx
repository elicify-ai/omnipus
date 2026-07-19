/**
 * CalendarScreen.test.tsx
 *
 * Integration tests for CalendarScreen — the integration hub for the workspace
 * calendar (FullCalendar v6).
 *
 * Strategy (spec §9 — F-03): jsdom cannot render FullCalendar layout / compute
 * pointer geometry, so FullCalendarView is mocked at the module boundary. The
 * mock captures the props passed to it (including all event handlers) into a
 * module-level variable. Each test invokes the captured handler with a synthetic
 * `info` object and then asserts on API call arguments and toast calls.
 *
 * Covers spec §9 tests #13–#21:
 *   #13 drop failure → revert() + error toast (variant 'error')           FR-010
 *   #14 drop success → success toast (variant 'success') + Undo action    FR-010
 *   #15 dateClick → opens CreateTaskSlideOver with initialDue             FR-012
 *   #17 eventClick(task) → TaskDetailSlideOver opens                      FR-009
 *   #21 plus: eventDrop task-fire preserves sibling trigger.config keys   FR-008
 *
 * Milestone coverage (drop/click/query-failure) was removed with the milestone
 * feature (ADR-049 — replaced by Plans/tags, not owned by this screen).
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { EventDropArg, EventClickArg, DateSelectArg } from '@fullcalendar/core'
import type { DateClickArg } from '@fullcalendar/interaction'
import { CalendarScreen } from './CalendarScreen'
import type { CalendarEventExtProps } from '@/components/calendar/types'

// ── 1. Mock FullCalendarView — capture props so tests can invoke handlers ──────

/**
 * Module-level capture. Tests read `capturedProps` to fire synthetic events.
 * Reset in beforeEach.
 */

type CapturedProps = {
  onEventDrop: ((arg: EventDropArg) => void) | null
  onEventClick: ((arg: EventClickArg) => void) | null
  onDateClick: ((arg: DateClickArg) => void) | null
  onDateSelect: ((arg: DateSelectArg) => void) | null
  onDatesSet: ((title: string, view: string) => void) | null
  /** isLoading prop passed to FullCalendarView on the most recent render. */
  isLoading: boolean | undefined
  /** isEmpty prop passed to FullCalendarView on the most recent render. */
  isEmpty: boolean | undefined
}

const capturedProps: CapturedProps = {
  onEventDrop: null,
  onEventClick: null,
  onDateClick: null,
  onDateSelect: null,
  onDatesSet: null,
  isLoading: undefined,
  isEmpty: undefined,
}

vi.mock('@/components/calendar/FullCalendarView', () => ({
  FullCalendarView: (props: {
    onEventDrop: (arg: EventDropArg) => void
    onEventClick: (arg: EventClickArg) => void
    onDateClick: (arg: DateClickArg) => void
    onDateSelect: (arg: DateSelectArg) => void
    onDatesSet?: (title: string, view: string) => void
    isLoading?: boolean
    isEmpty?: boolean
  }) => {
    // Capture handlers on every render (they may change via useCallback deps).
    capturedProps.onEventDrop = props.onEventDrop
    capturedProps.onEventClick = props.onEventClick
    capturedProps.onDateClick = props.onDateClick
    capturedProps.onDateSelect = props.onDateSelect
    capturedProps.onDatesSet = props.onDatesSet ?? null
    capturedProps.isLoading = props.isLoading
    capturedProps.isEmpty = props.isEmpty
    // Increment render counter (used by waitForQueriesLoaded)
    _renderCount++
    return <div data-testid="fullcalendar-stub">FullCalendar stub</div>
  },
}))

// ── 2. Mock CalendarToolbar — thin stub ───────────────────────────────────────

vi.mock('@/components/calendar/CalendarToolbar', () => ({
  CalendarToolbar: ({ onNewTask }: { onNewTask: () => void }) => (
    <div data-testid="calendar-toolbar-stub">
      <button data-testid="new-task-btn" onClick={onNewTask}>
        New task
      </button>
    </div>
  ),
}))

// ── 3. Mock slide-overs / popover with testid stubs that expose their state ───

/**
 * CreateTaskSlideOver stub: renders data-testid="create-task-slideover" with
 * open state and the initialDue prop so tests can assert on them.
 */
vi.mock('@/components/workspaces/CreateTaskSlideOver', () => ({
  CreateTaskSlideOver: ({
    open,
    initialDue,
    workspaceId,
    onOpenChange,
  }: {
    open: boolean
    initialDue?: string
    workspaceId: string
    onOpenChange: (open: boolean) => void
  }) => (
    <div
      data-testid="create-task-slideover"
      data-open={String(open)}
      data-initial-due={initialDue ?? ''}
      data-workspace-id={workspaceId}
    >
      {open && (
        <button onClick={() => onOpenChange(false)} data-testid="create-close-btn">
          close
        </button>
      )}
    </div>
  ),
}))

/**
 * TaskDetailSlideOver stub: renders data-testid="task-detail-slideover" with
 * the task id when task is non-null.
 */
vi.mock('@/components/workspaces/TaskDetailSlideOver', () => ({
  TaskDetailSlideOver: ({
    task,
    onClose,
  }: {
    task: { id: string; title: string } | null
    onClose: () => void
  }) => (
    <div
      data-testid="task-detail-slideover"
      data-task-id={task?.id ?? ''}
    >
      {task && (
        <>
          <span data-testid="task-detail-title">{task.title}</span>
          <button onClick={onClose} data-testid="task-detail-close">
            close
          </button>
        </>
      )}
    </div>
  ),
}))

// ── 4. Mock @/lib/api ─────────────────────────────────────────────────────────

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchTasks: vi.fn(),
    updateTask: vi.fn(),
    tasksQueryKeys: {
      list: (params?: Record<string, unknown>) => ['tasks', params ?? {}],
    },
  }
})

import {
  fetchTasks,
  updateTask,
} from '@/lib/api'

// ── 5. Mock @/store/ui ────────────────────────────────────────────────────────

const mockAddToast = vi.fn()

vi.mock('@/store/ui', () => ({
  useUiStore: (selector?: (s: { addToast: ReturnType<typeof vi.fn> }) => unknown) => {
    const store = { addToast: mockAddToast }
    return selector ? selector(store) : store
  },
}))

// ── 6. Test setup file import ────────────────────────────────────────────────

// @testing-library/jest-dom matchers (toBeInTheDocument, toHaveAttribute, etc.)
// are wired via the project's setupFiles (src/test/setup.ts).

// ── 7. Fixtures ───────────────────────────────────────────────────────────────

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
    ...overrides,
  }
}

/**
 * Build a synthetic EventDropArg for testing.
 * The `revert` spy is returned alongside the arg so tests can assert it was called.
 */
function makeDropArg(
  ext: CalendarEventExtProps,
  newStart: Date,
  oldStart: Date,
): { arg: EventDropArg; revert: ReturnType<typeof vi.fn> } {
  const revert = vi.fn()
  const arg = {
    event: {
      start: newStart,
      extendedProps: ext,
    },
    oldEvent: {
      start: oldStart,
    },
    revert,
  } as unknown as EventDropArg
  return { arg, revert }
}

/**
 * Build a synthetic EventClickArg.
 */
function makeClickArg(ext: CalendarEventExtProps): EventClickArg {
  return {
    event: {
      extendedProps: ext,
    },
    jsEvent: { target: document.createElement('div') },
  } as unknown as EventClickArg
}

/**
 * Build a synthetic DateClickArg for an all-day click.
 */
function makeDateClickArg(date: Date, allDay = true): DateClickArg {
  return {
    date,
    allDay,
    jsEvent: { target: document.createElement('div') },
  } as unknown as DateClickArg
}

// ── 7. Render helpers ─────────────────────────────────────────────────────────

function makeClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false },
    },
  })
}

function renderCalendarScreen(client?: QueryClient) {
  const qc = client ?? makeClient()
  return render(
    <QueryClientProvider client={qc}>
      <CalendarScreen workspaceId={WORKSPACE_ID} />
    </QueryClientProvider>,
  )
}

// ── 8. Tests ──────────────────────────────────────────────────────────────────

/**
 * Wait for the CalendarScreen's queries to settle. We track render count via
 * a ref that the mock increments so we can confirm the post-data re-render.
 */
let _renderCount = 0

async function waitForQueriesLoaded() {
  // Wait until both queries have been invoked AND at least 2 renders happened
  // (initial + post-data). The captured handlers are updated on every render.
  await waitFor(
    () => {
      expect(vi.mocked(fetchTasks)).toHaveBeenCalled()
      expect(_renderCount).toBeGreaterThanOrEqual(2)
      expect(capturedProps.onEventClick).not.toBeNull()
    },
    { timeout: 5000 },
  )
}

beforeEach(() => {
  // Reset captured props
  capturedProps.onEventDrop = null
  capturedProps.onEventClick = null
  capturedProps.onDateClick = null
  capturedProps.onDateSelect = null
  capturedProps.onDatesSet = null
  capturedProps.isLoading = undefined
  capturedProps.isEmpty = undefined

  // Reset render counter
  _renderCount = 0

  // Reset mocks
  vi.mocked(fetchTasks).mockResolvedValue([makeTask()])
  vi.mocked(updateTask).mockResolvedValue(makeTask() as never)
  mockAddToast.mockReset()
})

afterEach(() => {
  vi.clearAllMocks()
})

// ── eventDrop: task-due → updateTask({ due: <local-midnight RFC3339 datetime> }) ─

describe('CalendarScreen — eventDrop on a task-due event (spec §9 #13, #14)', () => {
  it('calls updateTask with due as a local-midnight RFC3339 datetime', async () => {
    // Traces to: workspace-calendar-fullcalendar-spec.md §9 #14 / FR-008 / US-3/AS-1
    // BDD: Given a task-due event is dragged to a new day,
    // When CalendarScreen.handleEventDrop fires,
    // Then updateTask(taskId, { due }) is called with the dropped day's local-midnight
    // instant as an RFC3339 datetime (TaskUpdateRequest.due is `format: date-time`;
    // a date-only string is rejected 400). The read side maps it back to the local date.

    vi.mocked(updateTask).mockResolvedValueOnce(makeTask() as never)
    renderCalendarScreen()

    // Wait for FullCalendarView stub to mount and capture props
    await waitFor(() => expect(capturedProps.onEventDrop).not.toBeNull())

    const newStart = new Date(2026, 5, 23, 0, 0, 0) // local Jun 23 2026
    const oldStart = new Date(2026, 5, 20, 0, 0, 0)

    const { arg } = makeDropArg(
      {
        kind: 'task-due',
        taskId: 'task-1',
        icon: 'Circle',
        status: 'next',
      },
      newStart,
      oldStart,
    )

    await act(async () => {
      capturedProps.onEventDrop!(arg)
    })

    await waitFor(() => expect(vi.mocked(updateTask)).toHaveBeenCalledOnce())

    const [calledId, calledData] = vi.mocked(updateTask).mock.calls[0]
    expect(calledId).toBe('task-1')
    // RFC3339 datetime derived from the SAME local Date → timezone-independent (FR-008/FR-015).
    expect(calledData.due).toBe(new Date(2026, 5, 23, 0, 0, 0).toISOString())
  })

  it('shows a success toast with variant "success" and an Undo action', async () => {
    // Traces to: workspace-calendar-fullcalendar-spec.md §9 #14 / FR-010 / US-3/AS-4
    // BDD: Given updateTask resolves successfully,
    // Then addToast is called with variant "success" and an action.label "Undo".

    vi.mocked(updateTask).mockResolvedValueOnce(makeTask() as never)
    renderCalendarScreen()

    await waitFor(() => expect(capturedProps.onEventDrop).not.toBeNull())

    const newStart = new Date(2026, 5, 23, 0, 0, 0)
    const oldStart = new Date(2026, 5, 20, 0, 0, 0)

    const { arg } = makeDropArg(
      { kind: 'task-due', taskId: 'task-1', icon: 'Circle', status: 'next' },
      newStart,
      oldStart,
    )

    await act(async () => {
      capturedProps.onEventDrop!(arg)
    })

    await waitFor(() => expect(mockAddToast).toHaveBeenCalledOnce())

    const toastArg = mockAddToast.mock.calls[0][0]
    expect(toastArg.variant).toBe('success')
    // Must include an Undo action (I-5/I-6)
    expect(toastArg.action).toBeDefined()
    expect(toastArg.action.label).toBe('Undo')
    // Toast message must mention the date
    expect(toastArg.message).toMatch(/rescheduled/i)
  })

  it('calls revert() and shows an error toast when updateTask rejects', async () => {
    // Traces to: workspace-calendar-fullcalendar-spec.md §9 #13 / FR-010 / US-3/AS-5
    // BDD: Given updateTask rejects (e.g. 500),
    // When the drop is processed,
    // Then arg.revert() is called AND addToast with variant "error" fires.

    vi.mocked(updateTask).mockRejectedValueOnce(new Error('500 Internal Server Error'))
    renderCalendarScreen()

    await waitFor(() => expect(capturedProps.onEventDrop).not.toBeNull())

    const newStart = new Date(2026, 5, 24, 0, 0, 0)
    const oldStart = new Date(2026, 5, 20, 0, 0, 0)

    const { arg, revert } = makeDropArg(
      { kind: 'task-due', taskId: 'task-1', icon: 'Circle', status: 'next' },
      newStart,
      oldStart,
    )

    await act(async () => {
      capturedProps.onEventDrop!(arg)
    })

    // Failure path: revert must be called (FR-010)
    await waitFor(() => expect(revert).toHaveBeenCalledOnce())

    // Error toast with variant 'error' (role=alert) (FR-010)
    await waitFor(() => expect(mockAddToast).toHaveBeenCalledOnce())
    const toastArg = mockAddToast.mock.calls[0][0]
    expect(toastArg.variant).toBe('error')
  })
})

// ── eventDrop: task-fire → preserve sibling trigger.config keys ───────────────

describe('CalendarScreen — eventDrop on a task-fire event (spec §9 #11)', () => {
  it('preserves sibling config keys when updating a once-trigger (F-05)', async () => {
    // Traces to: workspace-calendar-fullcalendar-spec.md §9 #11 / FR-008 / US-3/AS-2
    // BDD: Given a once-trigger task with config { at_ms: <original>, foo: "bar" },
    // When its fire chip is dropped to a new slot,
    // Then updateTask sends { trigger: { type:"once", config:{ at_ms:<newMs>, foo:"bar" } } }
    // (sibling keys like "foo" must be preserved — F-05).

    const originalAtMs = new Date(2026, 5, 20, 9, 0, 0).getTime()
    const taskWithTrigger = makeTask({
      id: 'task-fire-1',
      trigger: {
        type: 'once',
        config: { at_ms: originalAtMs, foo: 'bar' }, // sibling key to preserve
      },
      due: undefined,
    })

    vi.mocked(fetchTasks).mockResolvedValue([taskWithTrigger] as never)
    vi.mocked(updateTask).mockResolvedValueOnce(taskWithTrigger as never)

    renderCalendarScreen()

    // Wait for query data to load so `tasks` inside the handler includes the trigger task
    await waitForQueriesLoaded()

    const newStart = new Date(2026, 5, 20, 14, 0, 0) // drag to 14:00 same day
    const oldStart = new Date(2026, 5, 20, 9, 0, 0)

    const { arg } = makeDropArg(
      {
        kind: 'task-fire',
        taskId: 'task-fire-1',
        icon: 'Clock',
        status: 'next',
      },
      newStart,
      oldStart,
    )

    await act(async () => {
      capturedProps.onEventDrop!(arg)
    })

    await waitFor(() => expect(vi.mocked(updateTask)).toHaveBeenCalledOnce())

    const [calledId, calledData] = vi.mocked(updateTask).mock.calls[0]
    expect(calledId).toBe('task-fire-1')
    // Must send the whole trigger (F-05)
    expect(calledData.trigger).toBeDefined()
    expect(calledData.trigger!.type).toBe('once')
    // at_ms must equal the new start time
    expect(calledData.trigger!.config.at_ms).toBe(newStart.getTime())
    // Sibling key "foo" must be preserved (not dropped)
    expect((calledData.trigger!.config as Record<string, unknown>).foo).toBe('bar')
  })

  it('differentiation: two different drops produce two different at_ms values', async () => {
    // Traces to: workspace-calendar-fullcalendar-spec.md §9 #11
    // Anti-hardcode: different drop targets produce different at_ms values.

    const originalAtMs = new Date(2026, 5, 20, 9, 0, 0).getTime()
    const taskWithTrigger = makeTask({
      id: 'task-fire-diff',
      trigger: { type: 'once', config: { at_ms: originalAtMs } },
      due: undefined,
    })

    vi.mocked(fetchTasks).mockResolvedValue([taskWithTrigger] as never)
    vi.mocked(updateTask)
      .mockResolvedValueOnce(taskWithTrigger as never)
      .mockResolvedValueOnce(taskWithTrigger as never)

    const { unmount } = renderCalendarScreen()

    await waitForQueriesLoaded()

    // First drop: 14:00
    const start1 = new Date(2026, 5, 20, 14, 0, 0)
    const { arg: arg1 } = makeDropArg(
      { kind: 'task-fire', taskId: 'task-fire-diff', status: 'next', icon: 'Clock' },
      start1,
      new Date(2026, 5, 20, 9, 0, 0),
    )
    await act(async () => { capturedProps.onEventDrop!(arg1) })
    await waitFor(() => expect(vi.mocked(updateTask)).toHaveBeenCalledTimes(1))

    const atMs1 = vi.mocked(updateTask).mock.calls[0][1].trigger!.config.at_ms as number

    // Second drop: 16:00
    const start2 = new Date(2026, 5, 20, 16, 0, 0)
    const { arg: arg2 } = makeDropArg(
      { kind: 'task-fire', taskId: 'task-fire-diff', status: 'next', icon: 'Clock' },
      start2,
      new Date(2026, 5, 20, 9, 0, 0),
    )
    await act(async () => { capturedProps.onEventDrop!(arg2) })
    await waitFor(() => expect(vi.mocked(updateTask)).toHaveBeenCalledTimes(2))

    const atMs2 = vi.mocked(updateTask).mock.calls[1][1].trigger!.config.at_ms as number

    // Different drop targets must produce different at_ms values
    expect(atMs1).not.toBe(atMs2)
    expect(atMs1).toBe(start1.getTime())
    expect(atMs2).toBe(start2.getTime())

    unmount()
  })
})

// ── dateClick → opens CreateTaskSlideOver ────────────────────────────────────

describe('CalendarScreen — dateClick opens CreateTaskSlideOver (spec §9 #15)', () => {
  it('allDay dateClick opens CreateTaskSlideOver with initialDue="YYYY-MM-DDTHH:mm" (F-02)', async () => {
    // Traces to: workspace-calendar-fullcalendar-spec.md §9 #15 / FR-012 / US-4/AS-1
    // BDD: Given Month view and user clicks day 2026-06-22,
    // When dateClick fires,
    // Then CreateTaskSlideOver opens with initialDue = "2026-06-22T00:00".

    renderCalendarScreen()

    await waitFor(() => expect(capturedProps.onDateClick).not.toBeNull())

    // Confirm create slide-over is closed initially
    expect(screen.getByTestId('create-task-slideover')).toHaveAttribute('data-open', 'false')

    const clickedDate = new Date(2026, 5, 22, 0, 0, 0) // Jun 22, local midnight

    await act(async () => {
      capturedProps.onDateClick!(makeDateClickArg(clickedDate, true))
    })

    // Slide-over must now be open
    expect(screen.getByTestId('create-task-slideover')).toHaveAttribute('data-open', 'true')

    // initialDue must be "2026-06-22T00:00" (local midnight datetime — F-02)
    const initialDue = screen.getByTestId('create-task-slideover').getAttribute('data-initial-due')
    expect(initialDue).toBe('2026-06-22T00:00')
  })

  it('workspaceId is passed to CreateTaskSlideOver', async () => {
    // Traces to: workspace-calendar-fullcalendar-spec.md §9 #15 / FR-012
    // BDD: The workspace ID must be passed to CreateTaskSlideOver.

    renderCalendarScreen()

    await waitFor(() => expect(capturedProps.onDateClick).not.toBeNull())

    const clickedDate = new Date(2026, 5, 22, 0, 0, 0)

    await act(async () => {
      capturedProps.onDateClick!(makeDateClickArg(clickedDate, true))
    })

    expect(screen.getByTestId('create-task-slideover')).toHaveAttribute(
      'data-workspace-id',
      WORKSPACE_ID,
    )
  })
})

// ── eventClick(task) → TaskDetailSlideOver ────────────────────────────────────

describe('CalendarScreen — eventClick(task) opens TaskDetailSlideOver (spec §9 #17)', () => {
  it('opens TaskDetailSlideOver with the matching task', async () => {
    // Traces to: workspace-calendar-fullcalendar-spec.md §9 #17 / FR-009 / US-5/AS-1
    // BDD: Given a task chip is clicked,
    // When eventClick fires,
    // Then TaskDetailSlideOver renders with that task's id/title.

    const task = makeTask({ id: 'task-click-1', title: 'Clicked Task' })
    vi.mocked(fetchTasks).mockResolvedValue([task] as never)

    renderCalendarScreen()

    // Wait for query data to load so `tasks` inside the click handler includes the task
    await waitForQueriesLoaded()

    // Confirm task-detail slide-over is closed initially (no task-id)
    expect(screen.getByTestId('task-detail-slideover')).toHaveAttribute('data-task-id', '')

    await act(async () => {
      capturedProps.onEventClick!(makeClickArg({
        kind: 'task-due',
        taskId: 'task-click-1',
        icon: 'Circle',
        status: 'next',
      }))
    })

    // Task detail must now show the task's id
    await waitFor(() => {
      expect(screen.getByTestId('task-detail-slideover')).toHaveAttribute(
        'data-task-id',
        'task-click-1',
      )
    })
    expect(screen.getByTestId('task-detail-title')).toHaveTextContent('Clicked Task')
  })

  it('differentiation: clicking two different tasks opens the correct one each time', async () => {
    // Traces to: workspace-calendar-fullcalendar-spec.md §9 #17
    // Anti-hardcode: different task chips open different slides.

    const task1 = makeTask({ id: 'task-a', title: 'Task A' })
    const task2 = makeTask({ id: 'task-b', title: 'Task B' })
    vi.mocked(fetchTasks).mockResolvedValue([task1, task2] as never)

    renderCalendarScreen()

    await waitForQueriesLoaded()

    // Click task-a
    await act(async () => {
      capturedProps.onEventClick!(makeClickArg({
        kind: 'task-due', taskId: 'task-a', icon: 'Circle', status: 'next',
      }))
    })
    await waitFor(() => {
      expect(screen.getByTestId('task-detail-slideover')).toHaveAttribute('data-task-id', 'task-a')
    })

    // Close and click task-b
    await act(async () => {
      screen.getByTestId('task-detail-close').click()
    })

    await act(async () => {
      capturedProps.onEventClick!(makeClickArg({
        kind: 'task-due', taskId: 'task-b', icon: 'Circle', status: 'next',
      }))
    })
    await waitFor(() => {
      expect(screen.getByTestId('task-detail-slideover')).toHaveAttribute('data-task-id', 'task-b')
    })
  })
})

// ── Tasks query rejection → error toast (FR-016 tasks-fail branch) ───────────

describe('CalendarScreen — tasks query failure (spec §9 / FR-016 tasks-fail branch)', () => {
  it('shows "Couldn\'t load tasks" error toast when fetchTasks rejects', async () => {
    // Traces to: workspace-calendar-fullcalendar-spec.md §9 / FR-016
    // BDD: Given the tasks request returns 500,
    // When the calendar loads,
    // Then addToast is called with message "Couldn't load tasks" and variant "error",
    //   AND the FullCalendarView stub still renders (degradation, not a crash).

    vi.mocked(fetchTasks).mockRejectedValueOnce(new Error('500 Server Error'))

    renderCalendarScreen()

    await waitFor(() => {
      const calls = mockAddToast.mock.calls
      const tasksFailCall = calls.find(
        (call) => call[0].message === "Couldn't load tasks",
      )
      expect(tasksFailCall).toBeDefined()
    })

    const toastCall = mockAddToast.mock.calls.find(
      (call) => call[0].message === "Couldn't load tasks",
    )
    expect(toastCall![0].variant).toBe('error')

    // The calendar grid stub must still render — tasks failure must not crash the calendar
    expect(screen.getByTestId('fullcalendar-stub')).toBeInTheDocument()
  })
})

// ── Undo restore (#14 / DS-2 row 5) ──────────────────────────────────────────

describe('CalendarScreen — Undo restores the original date (spec §9 #14 / DS-2 row 5 / I-5)', () => {
  it('invoking the success toast Undo action calls updateTask again with the OLD start datetime', async () => {
    // Traces to: workspace-calendar-fullcalendar-spec.md §9 #14 / US-3/AS-4 / DS-2 row 5
    // BDD: Given a successful task-due drop,
    // When the user clicks Undo on the success toast,
    // Then updateTask is called a second time with the OLD start's toISOString() value.

    vi.mocked(updateTask)
      .mockResolvedValueOnce(makeTask() as never) // first drop succeeds
      .mockResolvedValueOnce(makeTask() as never) // undo succeeds

    renderCalendarScreen()
    await waitFor(() => expect(capturedProps.onEventDrop).not.toBeNull())

    const oldStart = new Date(2026, 5, 20, 0, 0, 0) // Jun 20
    const newStart = new Date(2026, 5, 23, 0, 0, 0) // Jun 23

    const { arg } = makeDropArg(
      { kind: 'task-due', taskId: 'task-1', icon: 'Circle', status: 'next' },
      newStart,
      oldStart,
    )

    await act(async () => {
      capturedProps.onEventDrop!(arg)
    })

    // Wait for the first updateTask call + toast
    await waitFor(() => expect(vi.mocked(updateTask)).toHaveBeenCalledTimes(1))
    await waitFor(() => expect(mockAddToast).toHaveBeenCalledOnce())

    // Capture the success toast's Undo action
    const toastArg = mockAddToast.mock.calls[0][0]
    expect(toastArg.variant).toBe('success')
    expect(toastArg.action).toBeDefined()
    expect(toastArg.action.label).toBe('Undo')

    // Invoke the Undo action — this should call updateTask with the OLD start
    await act(async () => {
      toastArg.action.onClick()
    })

    // updateTask must be called a second time with the OLD start's toISOString()
    await waitFor(() => expect(vi.mocked(updateTask)).toHaveBeenCalledTimes(2))

    const [, undoData] = vi.mocked(updateTask).mock.calls[1]
    // The undo must restore the original date — derive the expected ISO from the
    // SAME local Date constructor so this is TZ-robust (no hardcoded UTC string).
    expect(undoData.due).toBe(oldStart.toISOString())
  })

  it('Undo failure shows a "Couldn\'t undo" error toast', async () => {
    // Traces to: workspace-calendar-fullcalendar-spec.md §9 #14 / FR-010 / DS-2 row 5
    // BDD: Given the 2nd updateTask (undo) rejects,
    // When Undo is clicked,
    // Then an error toast with "Couldn't undo" fires.

    vi.mocked(updateTask)
      .mockResolvedValueOnce(makeTask() as never)   // first drop succeeds
      .mockRejectedValueOnce(new Error('500 Undo failed')) // undo fails

    renderCalendarScreen()
    await waitFor(() => expect(capturedProps.onEventDrop).not.toBeNull())

    const oldStart = new Date(2026, 5, 20, 0, 0, 0)
    const newStart = new Date(2026, 5, 23, 0, 0, 0)

    const { arg } = makeDropArg(
      { kind: 'task-due', taskId: 'task-1', icon: 'Circle', status: 'next' },
      newStart,
      oldStart,
    )

    await act(async () => {
      capturedProps.onEventDrop!(arg)
    })

    await waitFor(() => expect(mockAddToast).toHaveBeenCalledOnce())
    const toastArg = mockAddToast.mock.calls[0][0]
    expect(toastArg.variant).toBe('success')
    expect(toastArg.action).toBeDefined()

    // Trigger the undo which will reject
    mockAddToast.mockReset()
    await act(async () => {
      toastArg.action.onClick()
    })

    // An error toast must fire after the undo rejection
    await waitFor(() => expect(mockAddToast).toHaveBeenCalledOnce())
    const errorToast = mockAddToast.mock.calls[0][0]
    expect(errorToast.variant).toBe('error')
    expect(errorToast.message).toMatch(/undo/i)
  })
})

// ── isLoading / isEmpty props (#20 / I-1) ─────────────────────────────────────

describe('CalendarScreen — isLoading and isEmpty props on FullCalendarView (spec §9 #20 / I-1)', () => {
  it('passes isLoading=true and isEmpty=false while queries are pending', async () => {
    // Traces to: workspace-calendar-fullcalendar-spec.md §9 #20 / US-1/AS-6 / I-1
    // BDD: Given the tasks query has not yet resolved,
    // When the calendar mounts,
    // Then FullCalendarView receives isLoading=true and isEmpty=false
    // (grid renders with loading affordance, empty hint must NOT flash).

    // Use a promise we control to keep the query pending
    let resolveTask: (v: never) => void
    const pendingPromise = new Promise<never>((res) => { resolveTask = res })
    vi.mocked(fetchTasks).mockReturnValueOnce(pendingPromise)

    renderCalendarScreen()

    // On the first render, both queries are loading
    await waitFor(() => expect(capturedProps.isLoading).not.toBeUndefined())

    expect(capturedProps.isLoading).toBe(true)
    // isEmpty must not be true while loading (no empty-hint flash)
    expect(capturedProps.isEmpty).toBe(false)

    // Resolve the pending promises to avoid teardown leaks
    resolveTask!([] as never)
  })

  it('passes isEmpty=true and isLoading=false after load with zero tasks', async () => {
    // Traces to: workspace-calendar-fullcalendar-spec.md §9 #20 / US-1/AS-6 / FR-001 / I-1
    // BDD: After the query resolves with an empty array,
    // Then FullCalendarView receives isEmpty=true and isLoading=false.

    vi.mocked(fetchTasks).mockResolvedValue([] as never)

    renderCalendarScreen()

    // Wait for queries to load (render count >= 2)
    await waitFor(() => {
      expect(vi.mocked(fetchTasks)).toHaveBeenCalled()
      expect(_renderCount).toBeGreaterThanOrEqual(2)
    }, { timeout: 5000 })

    // After loading completes with zero events, isEmpty must be true
    await waitFor(() => {
      expect(capturedProps.isEmpty).toBe(true)
      expect(capturedProps.isLoading).toBe(false)
    }, { timeout: 5000 })
  })
})

// ── Slot-select (US-4/AS-2) ───────────────────────────────────────────────────

describe('CalendarScreen — timed slot-select opens CreateTaskSlideOver with initialDue (US-4/AS-2)', () => {
  /**
   * Build a synthetic DateSelectArg for a timed (non-allDay) selection.
   */
  function makeDateSelectArg(start: Date, allDay = false): DateSelectArg {
    return {
      start,
      end: new Date(start.getTime() + 3600_000), // +1h
      allDay,
      jsEvent: { target: document.createElement('div') } as unknown as MouseEvent,
    } as unknown as DateSelectArg
  }

  it('slot-select with allDay=false opens CreateTaskSlideOver prefilled with the slot start time', async () => {
    // Traces to: workspace-calendar-fullcalendar-spec.md §9 #15 / US-4/AS-2 / FR-012
    // BDD: Given Week/Day view,
    // When I drag-select 2026-06-22T10:00,
    // Then CreateTaskSlideOver opens with initialDue = "2026-06-22T10:00".

    renderCalendarScreen()

    await waitFor(() => expect(capturedProps.onDateSelect).not.toBeNull())

    // Confirm create slide-over is closed initially
    expect(screen.getByTestId('create-task-slideover')).toHaveAttribute('data-open', 'false')

    // Simulate a timed slot-select starting at 10:00 local
    const slotStart = new Date(2026, 5, 22, 10, 0, 0) // Jun 22 10:00 local

    await act(async () => {
      capturedProps.onDateSelect!(makeDateSelectArg(slotStart, false))
    })

    // Slide-over must now be open
    expect(screen.getByTestId('create-task-slideover')).toHaveAttribute('data-open', 'true')

    // initialDue must be the local datetime "YYYY-MM-DDTHH:mm" of the slot start
    const initialDue = screen.getByTestId('create-task-slideover').getAttribute('data-initial-due')
    expect(initialDue).toBe('2026-06-22T10:00')
  })

  it('differentiation: slot-select at two different times produces two different initialDue values', async () => {
    // Anti-hardcode: different slot times → different initialDue strings.
    // Traces to: workspace-calendar-fullcalendar-spec.md §9 #15 / US-4/AS-2

    renderCalendarScreen()

    await waitFor(() => expect(capturedProps.onDateSelect).not.toBeNull())

    // First select: 10:00
    const slot1 = new Date(2026, 5, 22, 10, 0, 0)
    await act(async () => {
      capturedProps.onDateSelect!(makeDateSelectArg(slot1, false))
    })
    expect(screen.getByTestId('create-task-slideover')).toHaveAttribute('data-open', 'true')
    const initialDue1 = screen.getByTestId('create-task-slideover').getAttribute('data-initial-due')

    // Close the slide-over
    await act(async () => {
      screen.getByTestId('create-close-btn').click()
    })
    expect(screen.getByTestId('create-task-slideover')).toHaveAttribute('data-open', 'false')

    // Second select: 14:30
    const slot2 = new Date(2026, 5, 22, 14, 30, 0)
    await act(async () => {
      capturedProps.onDateSelect!(makeDateSelectArg(slot2, false))
    })
    expect(screen.getByTestId('create-task-slideover')).toHaveAttribute('data-open', 'true')
    const initialDue2 = screen.getByTestId('create-task-slideover').getAttribute('data-initial-due')

    expect(initialDue1).toBe('2026-06-22T10:00')
    expect(initialDue2).toBe('2026-06-22T14:30')
    expect(initialDue1).not.toBe(initialDue2)
  })
})

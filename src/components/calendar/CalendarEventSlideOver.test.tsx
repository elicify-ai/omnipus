/**
 * CalendarEventSlideOver.test.tsx — test 20 of the Calendar Recurrence
 * Redesign TDD plan.
 *
 * Spec: docs/internal/specs/calendar-recurrence-redesign-spec.md
 * Traces to BDD: "Operator creates a biweekly task…", "Event slide-over opens
 * with asserted defaults", "'Does not repeat' save creates a once-trigger
 * task", "Title-only edit leaves the schedule untouched", "Server rejects a
 * sub-minute rule with an inline message", "End date earlier than start is
 * unreachable"; User Story 1 (AS 1/3/6), User Story 2 (AS 5, FR-024).
 *
 * RTL flows covered: default state (Does not repeat + browser-zone label),
 * preset selection + save body (recurring), once-save defaults, title-only
 * edit → byte-identical trigger payload, recurrence change → re-anchor
 * notice shown before save, 400 → inline error (not a toast), end-date
 * picker disables dates before the anchor.
 *
 * Legacy replace + TaskDetailPanel defensive guard live in
 * CalendarLegacyReplace.test.tsx (test 21) — not duplicated here.
 */

import { describe, it, expect, vi, beforeEach, beforeAll } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { CalendarEventSlideOver } from './CalendarEventSlideOver'
import type { Task, TaskTrigger } from '@/lib/api'

// DateTimePicker / RecurrenceEditor (shadcn Calendar + Select + Popover) need
// these jsdom polyfills to open (same gap noted across the existing suite —
// CreateTaskSlideOver.test.tsx, recurrenceEditor.*.test.ts).
beforeAll(() => {
  if (!Element.prototype.hasPointerCapture) {
    Element.prototype.hasPointerCapture = () => false
  }
  if (!Element.prototype.scrollIntoView) {
    Element.prototype.scrollIntoView = () => {}
  }
  if (typeof window !== 'undefined' && !window.ResizeObserver) {
    window.ResizeObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
    } as unknown as typeof ResizeObserver
  }
})

// ── Mocks ────────────────────────────────────────────────────────────────────

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([]),
    fetchWorkspaceDelegation: vi.fn(),
    createTask: vi.fn(),
    updateTask: vi.fn(),
    tasksQueryKeys: { list: () => ['tasks'] },
  }
})

import {
  createTask,
  updateTask,
  fetchWorkspaceDelegation,
  ApiError,
} from '@/lib/api'

const mockUseOccurrences = vi.fn()
vi.mock('@/lib/calendar/useOccurrences', () => ({
  useOccurrences: (...args: unknown[]) => mockUseOccurrences(...args),
}))

const mockAddToast = vi.fn()

vi.mock('@/store/ui', () => ({
  useUiStore: (selector?: (s: { addToast: ReturnType<typeof vi.fn> }) => unknown) => {
    const store = { addToast: mockAddToast }
    return selector ? selector(store) : store
  },
}))

// ── Fixtures / helpers ───────────────────────────────────────────────────────

function makeClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
}

// Real, verified weekday (node-checked): Jul 20 2026 = Monday.
const ANCHOR = new Date(2026, 6, 20, 9, 0, 0)

function makeTask(overrides: Partial<Task> = {}): Task {
  return {
    id: 'task-1',
    title: 'Weekly report',
    action: 'llm',
    status: 'next',
    priority: 3,
    workspace_id: 'ws-1',
    owner: 'alice',
    created_by: 'alice',
    created_at: '2026-06-01T00:00:00Z',
    updated_at: '2026-06-01T00:00:00Z',
    surface: 'user',
    ...overrides,
  } as Task
}

const RRULE_TRIGGER: TaskTrigger = {
  type: 'recurring',
  config: {
    rrule: 'FREQ=WEEKLY;BYDAY=MO;COUNT=10',
    dtstart_ms: ANCHOR.getTime(),
    tz: 'Europe/Berlin',
  },
}

// A rule shaped so it matches NO canonical preset (INTERVAL=2, two weekdays,
// no end condition) — loads straight into the Custom panel (T3), unlike
// RRULE_TRIGGER above whose COUNT=10 is used for the anchor-invariance tests.
const CUSTOM_RRULE_TRIGGER: TaskTrigger = {
  type: 'recurring',
  config: {
    rrule: 'FREQ=WEEKLY;INTERVAL=2;BYDAY=MO,WE',
    dtstart_ms: ANCHOR.getTime(),
    tz: 'Europe/Berlin',
  },
}

function renderSlideOver(
  props: Partial<{
    open: boolean
    onOpenChange: (open: boolean) => void
    workspaceId: string
    task: Task | null
    initialDate: Date
  }> = {},
) {
  const defaults = {
    open: true,
    onOpenChange: vi.fn(),
    workspaceId: 'ws-1',
    task: null as Task | null,
    initialDate: ANCHOR,
  }
  const merged = { ...defaults, ...props }
  return render(
    <QueryClientProvider client={makeClient()}>
      <CalendarEventSlideOver
        open={merged.open}
        onOpenChange={merged.onOpenChange}
        workspaceId={merged.workspaceId}
        task={merged.task}
        initialDate={merged.initialDate}
      />
    </QueryClientProvider>,
  )
}

function openRepeatDropdown() {
  fireEvent.click(screen.getByRole('combobox', { name: 'Repeat' }))
}

async function pickRepeatOption(label: string) {
  const option = await screen.findByRole('option', { name: label })
  fireEvent.pointerDown(option, { pointerId: 1, button: 0 })
  fireEvent.click(option)
}

beforeEach(() => {
  vi.mocked(createTask).mockReset()
  vi.mocked(updateTask).mockReset()
  vi.mocked(fetchWorkspaceDelegation).mockReset().mockRejectedValue(new Error('not mocked'))
  mockAddToast.mockReset()
  mockUseOccurrences.mockReset().mockReturnValue({ data: [], isError: false, isLoading: false })
})

// ── Tests ─────────────────────────────────────────────────────────────────────

describe('CalendarEventSlideOver — default state (create mode)', () => {
  it('opens defaulting to "Does not repeat", with the browser IANA zone shown beside the time', async () => {
    // BDD: "Event slide-over opens with asserted defaults" — US-1 AS-1.
    renderSlideOver({ task: null })

    expect(screen.getByRole('combobox', { name: 'Repeat' })).toHaveTextContent('Does not repeat')

    const tz = Intl.DateTimeFormat().resolvedOptions().timeZone
    expect(await screen.findByTestId('recurrence-time-label')).toHaveTextContent(tz)

    // No legacy note, no re-anchor notice, no upcoming preview in create mode.
    expect(screen.queryByTestId('legacy-trigger-note')).not.toBeInTheDocument()
    expect(screen.queryByTestId('reanchor-notice')).not.toBeInTheDocument()
  })
})

describe('CalendarEventSlideOver — create: recurring rule (US-1 AS-1/3)', () => {
  it('selecting a Repeat preset and saving compiles a `recurring` trigger with an RRULE', async () => {
    vi.mocked(createTask).mockResolvedValueOnce(makeTask() as never)
    renderSlideOver({ task: null, initialDate: ANCHOR })

    fireEvent.change(await screen.findByLabelText(/title/i), { target: { value: 'Weekly sync' } })

    openRepeatDropdown()
    await pickRepeatOption('Weekly on Monday')

    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))

    await waitFor(() => expect(vi.mocked(createTask)).toHaveBeenCalledOnce())
    const body = vi.mocked(createTask).mock.calls[0][0]

    expect(body.title).toBe('Weekly sync')
    expect(body.surface).toBe('user')
    expect(body.workspace_id).toBe('ws-1')
    expect(body.trigger?.type).toBe('recurring')
    expect(body.trigger?.config?.rrule).toContain('FREQ=WEEKLY')
    expect(body.trigger?.config?.rrule).toContain('BYDAY=MO')
    expect(body.trigger?.config?.dtstart_ms).toBe(ANCHOR.getTime())
    expect(body.trigger?.config?.cron_expr).toBeUndefined()
    expect(typeof body.trigger?.config?.tz).toBe('string')
  })
})

describe('CalendarEventSlideOver — create: "Does not repeat" (US-1 AS-6)', () => {
  it('saves a `once` trigger with generic-form defaults', async () => {
    vi.mocked(createTask).mockResolvedValueOnce(makeTask({ id: 'new' }) as never)
    const initialDate = new Date(2026, 6, 20, 14, 0, 0)
    renderSlideOver({ task: null, initialDate })

    fireEvent.change(await screen.findByLabelText(/title/i), { target: { value: 'One-off check' } })
    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))

    await waitFor(() => expect(vi.mocked(createTask)).toHaveBeenCalledOnce())
    const body = vi.mocked(createTask).mock.calls[0][0]

    expect(body.trigger).toEqual({ type: 'once', config: { at_ms: initialDate.getTime() } })
    expect(body.surface).toBe('user')
    expect(body.priority).toBe(3)
    expect(body.action).toBe('llm')
    expect(body.prompt).toBeUndefined()
  })

  it('requires a title before saving', async () => {
    renderSlideOver({ task: null })
    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))
    expect(await screen.findByText(/title is required/i)).toBeInTheDocument()
    expect(vi.mocked(createTask)).not.toHaveBeenCalled()
  })
})

describe('CalendarEventSlideOver — edit: Timezone Semantics §1 / FR-024 (grill-code FIX 2)', () => {
  it('displays the SERIES stored tz, not the browser/CI zone, when opening an existing RRULE task', async () => {
    const task = makeTask({ trigger: RRULE_TRIGGER }) // tz: 'Europe/Berlin'
    renderSlideOver({ task })

    await screen.findByLabelText(/title/i)
    // Assert the specific stored zone regardless of what TZ the test runner
    // itself is on (a UTC CI machine would silently pass a browser-zone-only
    // assertion here, masking the bug).
    expect(await screen.findByTestId('recurrence-time-label')).toHaveTextContent('Europe/Berlin')
  })
})

describe('CalendarEventSlideOver — edit: FR-024 anchor invariance', () => {
  it('title-only edit sends the ORIGINAL trigger byte-identical (no re-anchor)', async () => {
    vi.mocked(updateTask).mockResolvedValueOnce(makeTask() as never)
    const task = makeTask({ trigger: RRULE_TRIGGER })
    renderSlideOver({ task })

    const titleInput = await screen.findByLabelText(/title/i)
    expect(titleInput).toHaveValue('Weekly report')
    fireEvent.change(titleInput, { target: { value: 'Weekly report v2' } })

    // Never touched the Date & time field or the Repeat section.
    expect(screen.queryByTestId('reanchor-notice')).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /^save$/i }))

    await waitFor(() => expect(vi.mocked(updateTask)).toHaveBeenCalledOnce())
    const [calledId, data] = vi.mocked(updateTask).mock.calls[0]
    expect(calledId).toBe('task-1')
    expect(data.title).toBe('Weekly report v2')
    // Byte-identical: same rrule, dtstart_ms, and tz as the original trigger.
    expect(data.trigger).toEqual(RRULE_TRIGGER)
  })

  it('changing the Repeat rule shows the re-anchor notice before saving and restarts the schedule', async () => {
    vi.mocked(updateTask).mockResolvedValueOnce(makeTask() as never)
    const task = makeTask({ trigger: RRULE_TRIGGER })
    renderSlideOver({ task })

    await screen.findByLabelText(/title/i)
    // The fixture's COUNT=10 end condition doesn't match the plain "weekly"
    // preset (which is "Never"-ending) — it loads as Custom, which is
    // correct: this test only cares that changing it re-anchors.
    expect(screen.getByRole('combobox', { name: 'Repeat' })).toHaveTextContent('Custom')

    openRepeatDropdown()
    await pickRepeatOption('Daily')

    // Visible BEFORE saving.
    expect(await screen.findByTestId('reanchor-notice')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /^save$/i }))

    await waitFor(() => expect(vi.mocked(updateTask)).toHaveBeenCalledOnce())
    const [, data] = vi.mocked(updateTask).mock.calls[0]
    expect(data.trigger?.type).toBe('recurring')
    expect(data.trigger?.config?.rrule).toContain('FREQ=DAILY')
    // Re-anchored — NOT the original dtstart_ms.
    expect(data.trigger?.config?.dtstart_ms).not.toBe(RRULE_TRIGGER.config.dtstart_ms)
    // grill-code FIX 2: re-anchoring dtstart must NOT silently re-zone the
    // series to the (possibly different) browser/CI zone — it keeps its own
    // stored tz.
    expect(data.trigger?.config?.tz).toBe('Europe/Berlin')
  })
})

describe('CalendarEventSlideOver — server validation error (FR-006)', () => {
  it('renders a 400 response inline near the Repeat section, never as a toast', async () => {
    vi.mocked(createTask).mockRejectedValueOnce(new ApiError(400, 'Rule would fire too often'))
    renderSlideOver({ task: null })

    fireEvent.change(await screen.findByLabelText(/title/i), { target: { value: 'Poll inbox' } })
    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))

    await waitFor(() => expect(vi.mocked(createTask)).toHaveBeenCalledOnce())

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('Rule would fire too often')
    // The panel stays open (no onOpenChange(false) call) and no generic toast fires.
    expect(mockAddToast).not.toHaveBeenCalled()
  })
})

describe('CalendarEventSlideOver — end date cannot precede the anchor (US-1 AS-4)', () => {
  it('disables end-date calendar days before the anchor', async () => {
    // Load directly into an already-Custom rule (COUNT=10 doesn't match any
    // canonical preset, so it opens straight into the Custom panel with the
    // End-condition buttons already visible — selecting "Custom…" fresh from
    // a canonical/none starting point round-trips right back to that preset
    // by design, per RecurrenceEditor's matchPreset, so it can't be used to
    // reach the panel here).
    const task = makeTask({ trigger: RRULE_TRIGGER })
    renderSlideOver({ task })

    await screen.findByLabelText(/title/i)
    expect(screen.getByRole('combobox', { name: 'Repeat' })).toHaveTextContent('Custom')

    fireEvent.click(await screen.findByRole('button', { name: 'On date' }))
    fireEvent.click(screen.getByRole('button', { name: 'Ends on date' }))

    const dayBefore = document.querySelector('[data-day="2026-07-19"] button') as HTMLButtonElement | null
    expect(dayBefore).not.toBeNull()
    expect(dayBefore).toBeDisabled()
  })
})

describe('CalendarEventSlideOver — preview query failure (F-SFH2)', () => {
  it('legacy note shows a distinct error notice on preview fetch failure, never "unavailable"/blank', async () => {
    mockUseOccurrences.mockReturnValue({ data: undefined, isError: true, isLoading: false })
    const legacyTask = makeTask({
      id: 'legacy-cron',
      trigger: { type: 'recurring', config: { cron_expr: '0 9 * * MON' } },
    })
    renderSlideOver({ task: legacyTask })

    const note = await screen.findByTestId('legacy-trigger-note')
    expect(note).toHaveTextContent(/couldn't load upcoming run times/i)
    expect(note).not.toHaveTextContent(/next run time unavailable/i)
    expect(note).not.toHaveTextContent(/next run:/i)
  })

  it('upcoming preview shows a distinct error notice on fetch failure instead of silently disappearing', async () => {
    mockUseOccurrences.mockReturnValue({ data: undefined, isError: true, isLoading: false })
    const task = makeTask({ trigger: RRULE_TRIGGER })
    renderSlideOver({ task })

    await screen.findByLabelText(/title/i)
    expect(await screen.findByTestId('upcoming-preview-error')).toHaveTextContent(
      /couldn't load upcoming run times/i,
    )
    expect(screen.queryByTestId('upcoming-occurrences-preview')).not.toBeInTheDocument()
  })
})

describe('CalendarEventSlideOver — real Custom flow via RecurrenceEditor widgets (T3)', () => {
  it('drives interval, weekday toggle, and end-condition through the actual widgets (not a canned preset)', async () => {
    // Unlike every other save-flow test in this file, this one never calls
    // pickRepeatOption — it interacts with the raw Input/Button widgets
    // inside the Custom panel directly.
    vi.mocked(updateTask).mockResolvedValueOnce(makeTask() as never)
    const task = makeTask({ trigger: CUSTOM_RRULE_TRIGGER })
    renderSlideOver({ task })

    await screen.findByLabelText(/title/i)
    expect(screen.getByRole('combobox', { name: 'Repeat' })).toHaveTextContent('Custom')

    // Widget 1: the "Repeat every" interval number input.
    fireEvent.change(screen.getByLabelText('Repeat every'), { target: { value: '3' } })

    // Widget 2: a weekday toggle button — adds Friday to the existing Mon/Wed set.
    fireEvent.click(screen.getByRole('button', { name: 'Friday' }))

    // Widget 3: end-condition buttons — switch from "Never" to "After N occurrences".
    fireEvent.click(screen.getByRole('button', { name: 'After' }))
    fireEvent.change(screen.getByLabelText('Number of occurrences'), { target: { value: '5' } })

    fireEvent.click(screen.getByRole('button', { name: /^save$/i }))

    await waitFor(() => expect(vi.mocked(updateTask)).toHaveBeenCalledOnce())
    const [, data] = vi.mocked(updateTask).mock.calls[0]
    expect(data.trigger?.type).toBe('recurring')
    expect(data.trigger?.config?.rrule).toContain('FREQ=WEEKLY')
    expect(data.trigger?.config?.rrule).toContain('INTERVAL=3')
    expect(data.trigger?.config?.rrule).toContain('BYDAY=MO,WE,FR')
    expect(data.trigger?.config?.rrule).toContain('COUNT=5')
  })

  it('disables Save when the Custom editor reports an invalid state (weekly, zero weekdays)', async () => {
    const task = makeTask({ trigger: CUSTOM_RRULE_TRIGGER })
    renderSlideOver({ task })

    await screen.findByLabelText(/title/i)
    expect(screen.getByRole('combobox', { name: 'Repeat' })).toHaveTextContent('Custom')
    expect(screen.getByRole('button', { name: /^save$/i })).not.toBeDisabled()

    // Untoggle both currently-selected weekdays (Monday, Wednesday), leaving zero.
    fireEvent.click(screen.getByRole('button', { name: 'Monday' }))
    fireEvent.click(screen.getByRole('button', { name: 'Wednesday' }))

    await waitFor(() => expect(screen.getByRole('button', { name: /^save$/i })).toBeDisabled())
    expect(screen.getByText(/select at least one day/i)).toBeInTheDocument()

    // Clicking a disabled button must do nothing — no dead-click mutation.
    fireEvent.click(screen.getByRole('button', { name: /^save$/i }))
    expect(vi.mocked(updateTask)).not.toHaveBeenCalled()
  })

  it('reaches the Custom editor from a fresh "Does not repeat" create state (regression: create-flow reachability)', async () => {
    // Regression for the create-flow reachability bug: picking "Custom…" from a
    // fresh create-mode slide-over seeded weekly-preset-equal state, which
    // matchPreset snapped straight back to "Weekly on Monday" and hid the
    // Custom controls — so a brand-new recurring task could NEVER be hand-built
    // ("every 2 weeks"), defeating US-1's whole purpose. The sticky-custom flag
    // in RecurrenceEditor fixes it.
    vi.mocked(createTask).mockResolvedValueOnce(makeTask() as never)
    renderSlideOver({ task: null, initialDate: ANCHOR })

    fireEvent.change(await screen.findByLabelText(/title/i), { target: { value: 'Sprint sync' } })
    expect(screen.getByRole('combobox', { name: 'Repeat' })).toHaveTextContent('Does not repeat')

    // Pick "Custom…" — the dropdown must STAY on Custom and reveal the widgets.
    openRepeatDropdown()
    await pickRepeatOption('Custom…')

    expect(screen.getByRole('combobox', { name: 'Repeat' })).toHaveTextContent('Custom')
    // The Custom-panel interval widget is now reachable (it was NOT, pre-fix).
    const interval = await screen.findByLabelText('Repeat every')
    fireEvent.change(interval, { target: { value: '2' } })
    fireEvent.click(screen.getByRole('button', { name: 'After' }))
    fireEvent.change(screen.getByLabelText('Number of occurrences'), { target: { value: '10' } })

    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))
    await waitFor(() => expect(vi.mocked(createTask)).toHaveBeenCalledOnce())
    const body = vi.mocked(createTask).mock.calls[0][0]
    expect(body.trigger?.type).toBe('recurring')
    // Anchor Jul 20 2026 is a Monday → BYDAY=MO; interval 2, COUNT 10.
    expect(body.trigger?.config?.rrule).toContain('FREQ=WEEKLY')
    expect(body.trigger?.config?.rrule).toContain('INTERVAL=2')
    expect(body.trigger?.config?.rrule).toContain('BYDAY=MO')
    expect(body.trigger?.config?.rrule).toContain('COUNT=10')
  })
})

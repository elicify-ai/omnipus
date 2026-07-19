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

vi.mock('@/lib/calendar/useOccurrences', () => ({
  useOccurrences: vi.fn(() => ({ data: [], isError: false, isLoading: false })),
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

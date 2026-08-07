/**
 * CalendarLegacyReplace.test.tsx — test 21 of the Calendar Recurrence
 * Redesign TDD plan.
 *
 * Spec: docs/internal/specs/calendar-recurrence-redesign-spec.md
 * Traces to BDD: "Legacy task opens to an old-format note and a fresh
 * picker", "Replacing a legacy rule overwrites to RRULE with an audit
 * entry", "Generic detail panel guards against recurring tasks (defensive)";
 * User Story 5 (D8); User Story 3 Acceptance Scenario 5 (FR-023).
 *
 * Two independent RTL flows, per the task brief:
 *   1. CalendarEventSlideOver opened on a legacy (`cron_expr` / `every_ms`)
 *      task → old-format note with next-run time, NO cron string anywhere in
 *      the rendered output, a FRESH ("Does not repeat") picker → building and
 *      saving a new rule produces `type: recurring` + `rrule` + browser-zone
 *      `tz`, with the old `cron_expr`/`every_ms` keys gone.
 *   2. TaskDetailPanel's defensive guard (already implemented by a Wave-1
 *      leaf, FR-023) when force-fed an `every`/`recurring` task — this test
 *      only ASSERTS that existing behavior, per the task brief.
 */

import { describe, it, expect, vi, beforeEach, beforeAll } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { CalendarEventSlideOver } from './CalendarEventSlideOver'
import { TaskDetailPanel } from '@/components/workspaces/TaskDetailPanel'
import type { Task } from '@/lib/api'
import type { TaskOccurrenceSet } from '@/lib/api/generated/openapi-types'

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

const mockNavigate = vi.fn()

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => mockNavigate,
  Link: ({ children, to, params }: { children: React.ReactNode; to: string; params?: Record<string, string> }) => (
    <a href={to} data-testid="calendar-link" data-params={JSON.stringify(params ?? {})}>
      {children}
    </a>
  ),
}))

// Agent is now a required field on CalendarEventSlideOver (operator decision
// — a scheduled task's only executor). One selectable agent, so Save can be
// enabled in the legacy-replace save flows below.
vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([
      { id: 'mia', name: 'Mia', type: 'core', locked: true, status: 'idle', soul: '' },
    ]),
    fetchSubtasks: vi.fn().mockResolvedValue([]),
    fetchWorkspaces: vi.fn().mockResolvedValue([]),
    fetchTasks: vi.fn().mockResolvedValue([]),
    fetchWorkspaceDelegation: vi.fn(),
    createTask: vi.fn(),
    updateTask: vi.fn().mockResolvedValue({}),
    deleteTask: vi.fn().mockResolvedValue(undefined),
    setTaskTodos: vi.fn().mockResolvedValue({}),
    setTaskDependencies: vi.fn().mockResolvedValue({}),
    tasksQueryKeys: actual.tasksQueryKeys,
    workspacesQueryKeys: actual.workspacesQueryKeys,
  }
})

import {
  createTask,
  updateTask,
  fetchWorkspaceDelegation,
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

function makeTask(overrides: Partial<Task> = {}): Task {
  return {
    id: 'legacy-1',
    title: 'Monday standup reminder',
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

const CRON_STRING = '0 9 * * MON'
const NEXT_RUN_MS = new Date(2026, 6, 27, 9, 0, 0).getTime() // next Monday 09:00

const cronTask = makeTask({
  id: 'legacy-cron',
  title: 'Monday standup reminder',
  trigger: { type: 'recurring', config: { cron_expr: CRON_STRING } },
})

function occurrenceSetFor(taskId: string): TaskOccurrenceSet {
  return {
    task_id: taskId,
    occurrences_ms: [NEXT_RUN_MS, NEXT_RUN_MS + 7 * 24 * 60 * 60 * 1000],
    day_buckets: [],
    truncated: false,
  }
}

function renderSlideOver(task: Task) {
  return render(
    <QueryClientProvider client={makeClient()}>
      <CalendarEventSlideOver
        open
        onOpenChange={vi.fn()}
        workspaceId="ws-1"
        task={task}
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

// Agent required (operator decision) — save flows must pick the one mocked
// agent before Save enables. Waits for the team-scope query (mocked to
// reject → `unscoped` fallback) to settle so the SmartSelect isn't disabled.
async function selectAgent(name = 'Mia') {
  const trigger = await screen.findByRole('combobox', { name: 'Agent' })
  await waitFor(() => expect(trigger).not.toBeDisabled())
  fireEvent.click(trigger)
  const option = await screen.findByRole('option', { name })
  fireEvent.pointerDown(option, { pointerId: 1, button: 0 })
  fireEvent.click(option)
}

// Instruction (prompt) required (operator decision).
function fillPrompt(text = 'Post the reminder to the team channel.') {
  fireEvent.change(screen.getByLabelText(/instruction/i), { target: { value: text } })
}

beforeEach(() => {
  vi.mocked(createTask).mockReset()
  vi.mocked(updateTask).mockReset().mockResolvedValue(makeTask() as never)
  vi.mocked(fetchWorkspaceDelegation).mockReset().mockRejectedValue(new Error('not mocked'))
  mockAddToast.mockReset()
  mockUseOccurrences.mockReset().mockReturnValue({
    data: [occurrenceSetFor('legacy-cron')],
    isError: false,
    isLoading: false,
  })
})

// ── Flow 1: CalendarEventSlideOver on a legacy task ─────────────────────────

describe('CalendarEventSlideOver — legacy trigger (US-5, D8)', () => {
  it('shows an old-format note with the next run time and NO cron string anywhere', async () => {
    renderSlideOver(cronTask)

    const note = await screen.findByTestId('legacy-trigger-note')
    expect(note).toHaveTextContent(/old schedule format/i)
    // Next-run time is server-sourced (from the occurrences endpoint), not
    // parsed/derived from the cron string client-side.
    expect(note.textContent).toMatch(/next run/i)

    // D8/D9: the raw cron expression is NEVER rendered anywhere in the panel.
    expect(document.body.textContent).not.toContain(CRON_STRING)
  })

  it('the Repeat section starts fresh ("Does not repeat"), never a translated rule', async () => {
    renderSlideOver(cronTask)
    await screen.findByTestId('legacy-trigger-note')
    expect(screen.getByRole('combobox', { name: 'Repeat' })).toHaveTextContent('Does not repeat')
  })

  it('saving a new rule replaces the trigger outright: recurring + rrule + browser tz, old keys gone', async () => {
    renderSlideOver(cronTask)
    await screen.findByTestId('legacy-trigger-note')
    await selectAgent()
    fillPrompt()

    // "Daily" is weekday-independent — safe regardless of what real day the
    // suite runs on (the legacy Date & time field defaults to "now", per D8's
    // fresh-picker rule, so a weekday-named preset like "Weekly on Monday"
    // would only be offered on an actual Monday).
    openRepeatDropdown()
    await pickRepeatOption('Daily')

    fireEvent.click(screen.getByRole('button', { name: /^save$/i }))

    await waitFor(() => expect(vi.mocked(updateTask)).toHaveBeenCalledOnce())
    const [calledId, data] = vi.mocked(updateTask).mock.calls[0]
    expect(calledId).toBe('legacy-cron')
    expect(data.trigger?.type).toBe('recurring')
    expect(data.trigger?.config?.rrule).toContain('FREQ=DAILY')
    expect(data.trigger?.config?.cron_expr).toBeUndefined()
    expect(data.trigger?.config?.every_ms).toBeUndefined()
    const tz = Intl.DateTimeFormat().resolvedOptions().timeZone
    expect(data.trigger?.config?.tz).toBe(tz)
  })

  it('grill-code FIX 1 (CRITICAL): a title-only edit on a legacy task PRESERVES the original trigger, never converts to `once`', async () => {
    renderSlideOver(cronTask)
    await screen.findByTestId('legacy-trigger-note')
    await selectAgent()
    fillPrompt()

    // Never touch the Repeat section (stays "Does not repeat") — only edit
    // the title, exactly like the byte-identical RRULE-edit test above.
    const titleInput = await screen.findByLabelText(/title/i)
    fireEvent.change(titleInput, { target: { value: 'Monday standup reminder v2' } })

    fireEvent.click(screen.getByRole('button', { name: /^save$/i }))

    await waitFor(() => expect(vi.mocked(updateTask)).toHaveBeenCalledOnce())
    const [calledId, data] = vi.mocked(updateTask).mock.calls[0]
    expect(calledId).toBe('legacy-cron')
    expect(data.title).toBe('Monday standup reminder v2')
    // The working weekly cron schedule must survive untouched — NOT
    // silently replaced with a one-shot `once` firing at panel-open.
    expect(data.trigger).toEqual(cronTask.trigger)
    expect(data.trigger?.type).toBe('recurring')
    expect(data.trigger?.config?.cron_expr).toBe(CRON_STRING)
  })

  it('an every_ms legacy trigger also gets the fresh-picker + no-raw-value treatment', async () => {
    const everyTask = makeTask({
      id: 'legacy-every',
      trigger: { type: 'every', config: { every_ms: 1_800_000 } },
    })
    mockUseOccurrences.mockReturnValue({
      data: [occurrenceSetFor('legacy-every')],
      isError: false,
      isLoading: false,
    })
    renderSlideOver(everyTask)

    await screen.findByTestId('legacy-trigger-note')
    expect(screen.getByRole('combobox', { name: 'Repeat' })).toHaveTextContent('Does not repeat')
    // No raw millisecond interval value rendered.
    expect(document.body.textContent).not.toContain('1800000')
  })
})

// ── Flow 2: TaskDetailPanel defensive guard (FR-023, already implemented) ──

describe('TaskDetailPanel — defensive guard for a force-fed recurring task (FR-023)', () => {
  it('renders a read-only summary + "Edit in workspace calendar" link, never a raw cron input', async () => {
    render(
      <QueryClientProvider client={makeClient()}>
        <TaskDetailPanel task={cronTask} onClose={vi.fn()} />
      </QueryClientProvider>,
    )

    // Read-only plain-English summary, not a raw cron string.
    expect(await screen.findByText(/repeats/i)).toBeInTheDocument()
    expect(document.body.textContent).not.toContain(CRON_STRING)

    // The escape hatch to the real editor.
    const link = screen.getByTestId('calendar-link')
    expect(link).toHaveTextContent(/edit in workspace calendar/i)
    expect(JSON.parse(link.getAttribute('data-params') ?? '{}')).toMatchObject({ workspaceId: 'ws-1' })

    // No editable Trigger picker is rendered for a recurring task.
    expect(screen.queryByRole('combobox', { name: 'Trigger' })).not.toBeInTheDocument()
  })

  it('renders the same guard for an `every` (fixed-interval) legacy trigger', async () => {
    const everyTask = makeTask({
      id: 'legacy-every-2',
      trigger: { type: 'every', config: { every_ms: 1_800_000 } },
    })
    render(
      <QueryClientProvider client={makeClient()}>
        <TaskDetailPanel task={everyTask} onClose={vi.fn()} />
      </QueryClientProvider>,
    )
    expect(await screen.findByText(/repeats/i)).toBeInTheDocument()
    expect(screen.queryByRole('combobox', { name: 'Trigger' })).not.toBeInTheDocument()
  })

  it('grill-code FIX 3: renders the SPECIFIC rule summary for an RRULE trigger, not the generic placeholder', async () => {
    // FREQ=WEEKLY;BYDAY=MO, no COUNT/UNTIL → summarizeRecurrence produces the
    // exact "Repeats every week on Monday" text (recurrence.ts coreClause +
    // endConditionClause('never') === '').
    const rruleTask = makeTask({
      id: 'rrule-1',
      trigger: {
        type: 'recurring',
        config: {
          rrule: 'FREQ=WEEKLY;BYDAY=MO',
          dtstart_ms: new Date(2026, 6, 20, 9, 0, 0).getTime(), // 2026-07-20 = Monday
          tz: 'UTC',
        },
      },
    })
    render(
      <QueryClientProvider client={makeClient()}>
        <TaskDetailPanel task={rruleTask} onClose={vi.fn()} />
      </QueryClientProvider>,
    )

    expect(await screen.findByText('Repeats every week on Monday')).toBeInTheDocument()
    // Never the generic FR-023 placeholder once a real rule can be parsed.
    expect(screen.queryByText('Repeats on a recurring schedule')).not.toBeInTheDocument()
    // Still no raw rrule string anywhere (D8/D9).
    expect(document.body.textContent).not.toContain('FREQ=WEEKLY')
  })
})

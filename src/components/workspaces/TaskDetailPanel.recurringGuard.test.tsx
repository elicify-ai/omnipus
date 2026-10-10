/**
 * TaskDetailPanel.recurringGuard.test.tsx — TaskDetailPanel's defensive guard
 * for a force-fed recurring task (FR-023): a read-only rule summary plus an
 * "Edit in workspace calendar" link, never a raw cron input or string.
 *
 * Spec: docs/internal/specs/calendar-recurrence-redesign-spec.md
 * (User Story 3 Acceptance Scenario 5, FR-023).
 */

import { describe, it, expect, vi, beforeAll } from 'vitest'
import { render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { TaskDetailPanel } from '@/components/workspaces/TaskDetailPanel'
import type { Task } from '@/lib/api'

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

const mockAddToast = vi.fn()
vi.mock('@/store/ui', () => ({
  useUiStore: (selector?: (s: { addToast: ReturnType<typeof vi.fn> }) => unknown) => {
    const store = { addToast: mockAddToast }
    return selector ? selector(store) : store
  },
}))

// ── Fixtures ──────────────────────────────────────────────────────────────────

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

const cronTask = makeTask({
  id: 'legacy-cron',
  title: 'Monday standup reminder',
  trigger: { type: 'recurring', config: { cron_expr: CRON_STRING } },
})

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

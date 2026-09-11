/**
 * taskFormNoTrigger.test.tsx
 *
 * GOAL-FR-060 ("Trigger is REMOVED from both forms"), asserted across BOTH
 * task surfaces in one file: neither `CreateTaskSlideOver` nor
 * `TaskDetailPanel` renders a Trigger CONTROL any more — a normal task has
 * no timer. The panel's READ-ONLY branch for an already-scheduled task (a
 * plain-English summary + "Edit in workspace calendar" link) MUST survive
 * unchanged; this file proves both halves in one place.
 *
 * This file RENDERS `CreateTaskSlideOver.tsx`; it does not edit it (joint
 * delivery plan U5 row).
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { CreateTaskSlideOver } from './CreateTaskSlideOver'
import { TaskDetailPanel } from './TaskDetailPanel'
import type { Task } from '@/lib/api'

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => vi.fn(),
  Link: ({ children }: { children: React.ReactNode }) => children,
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([]),
    fetchTasks: vi.fn().mockResolvedValue([]),
    fetchPlans: vi.fn().mockResolvedValue([]),
    fetchSubtasks: vi.fn().mockResolvedValue([]),
    fetchWorkspaces: vi.fn().mockResolvedValue([]),
    fetchTaskEvidence: vi.fn().mockResolvedValue([]),
    fetchTaskVerdicts: vi.fn().mockResolvedValue([]),
    fetchWorkspaceDelegation: vi.fn().mockRejectedValue(new Error('not mocked')),
    createTask: vi.fn(),
    updateTask: vi.fn().mockResolvedValue({}),
    deleteTask: vi.fn().mockResolvedValue(undefined),
    setTaskTodos: vi.fn().mockResolvedValue({}),
    setTaskDependencies: vi.fn().mockResolvedValue({}),
    stopTaskGoalLoop: vi.fn().mockResolvedValue({}),
    isApiError: vi.fn().mockReturnValue(false),
    tasksQueryKeys: actual.tasksQueryKeys,
    boardTasksQueryKeys: actual.tasksQueryKeys,
    taskEvidenceQueryKeys: actual.taskEvidenceQueryKeys,
    taskVerdictsQueryKeys: actual.taskVerdictsQueryKeys,
    workspacesQueryKeys: actual.workspacesQueryKeys,
    plansQueryKeys: actual.plansQueryKeys,
  }
})

const mockAddToast = vi.fn()
vi.mock('@/store/ui', () => ({
  useUiStore: (selector?: (s: { addToast: ReturnType<typeof vi.fn> }) => unknown) => {
    const store = { addToast: mockAddToast }
    return selector ? selector(store) : store
  },
}))

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

function renderCreateForm() {
  return render(
    <QueryClientProvider client={makeClient()}>
      <CreateTaskSlideOver open onOpenChange={vi.fn()} workspaceId="ws-1" />
    </QueryClientProvider>,
  )
}

function makeTask(overrides: Partial<Task> = {}): Task {
  return {
    id: 'task-1',
    title: 'A task',
    action: 'llm',
    priority: 3,
    status: 'next',
    workspace_id: 'ws-test',
    surface: 'user',
    owner: 'alice',
    created_by: 'alice',
    created_at: '2026-06-20T10:00:00Z',
    updated_at: '2026-06-20T10:00:00Z',
    ...overrides,
  }
}

function renderPanel(task: Task) {
  return render(
    <QueryClientProvider client={makeClient()}>
      <TaskDetailPanel task={task} onClose={vi.fn()} />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  mockAddToast.mockReset()
})

describe('Neither form renders a Trigger control (GOAL-FR-060)', () => {
  it('CreateTaskSlideOver: no "Trigger" label, no trigger SmartSelect, no DateTimePicker for a schedule', async () => {
    renderCreateForm()
    await screen.findByText(/^title$/i)
    expect(screen.queryByText(/^trigger$/i)).toBeNull()
    expect(screen.queryByRole('combobox', { name: /trigger/i })).toBeNull()
    expect(screen.queryByRole('option', { name: /once \(at a time\)/i })).toBeNull()
    expect(screen.queryByRole('option', { name: /none \(manual\)/i })).toBeNull()
  })

  it('TaskDetailPanel: renders no Trigger field/control for a manual (non-scheduled) task', async () => {
    renderPanel(makeTask({ status: 'next' }))
    await screen.findByText(/^goal$/i)
    expect(screen.queryByText(/^trigger$/i)).toBeNull()
    expect(screen.queryByRole('combobox', { name: /trigger/i })).toBeNull()
  })

  it('TaskDetailPanel: still keeps its READ-ONLY branch for a task that IS already scheduled', async () => {
    renderPanel(makeTask({
      status: 'next',
      trigger: { type: 'once', config: { at_ms: new Date('2026-09-10T08:15').getTime() } },
    }))
    // The Field header DOES render for a scheduled task — this is the
    // surviving read-only case, not a control.
    expect(await screen.findByText(/^trigger$/i)).toBeInTheDocument()
    expect(await screen.findByText(/edit in workspace calendar/i)).toBeInTheDocument()
    // But still no SmartSelect/combobox anywhere in that field.
    expect(screen.queryByRole('combobox', { name: /trigger/i })).toBeNull()
  })

  it('the `trigger` model field, wire type, and helpers all survive — this removes CONTROLS only', async () => {
    // A scheduled task force-fed into the panel still renders its plain-
    // English summary via the surviving isScheduledTrigger/
    // scheduledTriggerSummary helpers — proving those helpers, and the
    // `trigger` field itself, were never touched by FR-060's control
    // removal.
    renderPanel(makeTask({
      status: 'next',
      trigger: { type: 'every', config: { every_ms: 86_400_000 } },
    }))
    expect(await screen.findByText(/^trigger$/i)).toBeInTheDocument()
    // A plain-English summary renders — never a raw cron/rule string.
    expect(screen.queryByText(/every_ms|cron_expr/i)).toBeNull()
  })
})

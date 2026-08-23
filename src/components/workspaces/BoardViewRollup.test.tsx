/**
 * BoardViewRollup.test.tsx
 *
 * Tests for the delegation roll-up feature on the Board view (Detail #6,
 * sprint4-ui-spec). Covers:
 *   1. A parent task with a rollup shows "N sub-agents" badge + avatars.
 *   2. Subtask cards (parent_task_id set) are NOT rendered as top-level cards.
 *   3. Altitude toggle "Show all" expands children inline under the parent;
 *      "Top-level" (default) keeps them collapsed.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BoardView } from './BoardView'
import type { Task, Agent, Plan } from '@/lib/api'

// ── Mocks ────────────────────────────────────────────────────────────────────

vi.mock('framer-motion', () => ({
  motion: {
    span: ({ children, ...props }: React.HTMLAttributes<HTMLSpanElement>) => (
      <span {...props}>{children}</span>
    ),
  },
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchSubtasks: vi.fn().mockResolvedValue([]),
    tasksQueryKeys: {
      list: (p: unknown) => ['tasks', p],
      subtasks: (id: string) => ['tasks', id, 'subtasks'],
    },
  }
})

// ── Fixtures ─────────────────────────────────────────────────────────────────

const baseTask = (overrides: Partial<Task> = {}): Task => ({
  id: 'task-1',
  title: 'Parent task',
  status: 'in_progress',
  action: 'llm',
  priority: 3,
  workspace_id: 'ws-1',
  surface: 'user',
  owner: 'admin',
  created_by: 'admin',
  created_at: '2026-06-20T10:00:00Z',
  updated_at: '2026-06-20T10:00:00Z',
  ...overrides,
})

const subtask = (overrides: Partial<Task> = {}): Task => ({
  id: 'subtask-1',
  title: 'Child subtask',
  status: 'in_progress',
  action: 'llm',
  priority: 3,
  workspace_id: 'ws-1',
  surface: 'user',
  parent_task_id: 'task-1',
  owner: 'admin',
  created_by: 'admin',
  created_at: '2026-06-20T10:00:00Z',
  updated_at: '2026-06-20T10:00:00Z',
  ...overrides,
})

const agentRay: Agent = {
  id: 'ray',
  name: 'Ray',
  type: 'core',
  locked: true,
  needs_model: false,
  status: 'active',
  soul: '',
  color: '#3B82F6',
  icon: 'MagnifyingGlass',
  timeout_seconds: 300,
  max_tool_iterations: 50,
  // ADR-052 FR-039: memory_enabled is required on the wire Agent type.
  memory_enabled: true,
}

const plans: Plan[] = []

// ── Helpers ───────────────────────────────────────────────────────────────────

function makeClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
}

function renderBoard(
  tasks: Task[],
  agents: Agent[] = [],
  altitude: 'top-level' | 'show-all' = 'top-level',
) {
  const client = makeClient()
  return render(
    <QueryClientProvider client={client}>
      <BoardView
        tasks={tasks}
        plans={plans}
        agents={agents}
        altitude={altitude}
        onTaskClick={vi.fn()}
      />
    </QueryClientProvider>,
  )
}

// ── Tests ────────────────────────────────────────────────────────────────────

describe('BoardView delegation roll-up', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('shows "N sub-agents running" badge and avatars when a parent has a rollup', () => {
    const parentWithRollup = baseTask({
      rollup: [
        { agent_id: 'ray', label: 'Research task', status: 'in_progress' },
      ],
    })

    renderBoard([parentWithRollup], [agentRay])

    // The badge should be visible
    const badge = screen.getByLabelText(/1 sub-agent running/i)
    expect(badge).toBeDefined()

    // The avatar for Ray should be rendered with his label
    const avatar = screen.getByLabelText(/Ray.*in_progress/i)
    expect(avatar).toBeDefined()
  })

  it('renders multiple avatars in a rollup', () => {
    const parentWithRollup = baseTask({
      rollup: [
        { agent_id: 'ray', label: 'Research', status: 'in_progress' },
        { agent_id: 'ava', label: 'Build', status: 'next' },
      ],
    })

    const agentAva: Agent = { ...agentRay, id: 'ava', name: 'Ava', color: '#A855F7', icon: 'Gear' }

    renderBoard([parentWithRollup], [agentRay, agentAva])

    // Two avatars should be listed in the avatar row
    const avatarList = screen.getByRole('list', { name: /sub-agent avatars/i })
    const items = within(avatarList).getAllByRole('listitem')
    expect(items.length).toBe(2)
  })

  it('does NOT render subtask cards as top-level cards in any column', () => {
    const parent = baseTask({ id: 'task-parent', title: 'Parent task' })
    const child = subtask({ id: 'task-child', title: 'Child subtask', parent_task_id: 'task-parent' })

    renderBoard([parent, child])

    // The parent card should be present
    expect(screen.getByText('Parent task')).toBeDefined()

    // The child card must NOT appear as a standalone top-level card.
    // It can only appear nested under its parent (only when show-all is active).
    // In 'top-level' mode it should not be visible at all.
    const childElements = screen.queryAllByText('Child subtask')
    expect(childElements.length).toBe(0)
  })

  it("altitude 'show-all' expands children inline under the parent; 'top-level' keeps them collapsed", async () => {
    // The AltitudeToggle control now lives in WorkspaceTasksTab's toolbar (a
    // single toolbar row); BoardView is driven purely by the `altitude` prop.
    // This verifies the prop-driven roll-up behaviour.
    const { fetchSubtasks } = await import('@/lib/api')
    const child = subtask({ id: 'task-child', title: 'Expanded child task', parent_task_id: 'task-parent' })
    vi.mocked(fetchSubtasks).mockResolvedValue([child])

    const parent = baseTask({ id: 'task-parent', title: 'Parent task with children' })

    // Top-level: children stay collapsed — no Subtasks list mounts.
    const { rerender } = renderBoard([parent], [], 'top-level')
    expect(screen.getByText('Parent task with children')).toBeDefined()
    expect(screen.queryByRole('list', { name: /subtasks/i })).toBeNull()

    // Show-all: TaskChildren mounts under the parent and renders the Subtasks list.
    rerender(
      <QueryClientProvider client={makeClient()}>
        <BoardView
          tasks={[parent]}
          plans={plans}
          agents={[]}
          altitude="show-all"
          onTaskClick={vi.fn()}
        />
      </QueryClientProvider>,
    )
    expect(screen.getByText('Parent task with children')).toBeDefined()
    expect(await screen.findByRole('list', { name: /subtasks/i })).toBeInTheDocument()
  })
})

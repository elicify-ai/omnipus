/**
 * TaskCard.assigneeWarning.test.tsx
 *
 * Founder decision 2026-09-15: a task whose assigned agent cannot finish it as
 * configured says so on its card, with the server's own text naming the fix
 * (Task.assignee_warning). Expected strings are the founder's wording, not
 * read back from the implementation.
 *
 * Renders use `showActions={false}`: the ▶/■ button needs a
 * QueryClientProvider and is unrelated to this warning.
 */

import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { TaskCard } from './TaskCard'
import type { Task } from '@/lib/api'

const WARNING =
  "Worker isn't allowed to report tasks as done. Allow 'goal_claim' for it in Agents → Tools, or assign another agent."

function makeTask(overrides: Partial<Task> = {}): Task {
  return {
    id: 'task-1',
    title: 'Reconcile invoices',
    status: 'next',
    action: 'llm',
    priority: 3,
    workspace_id: 'ws-1',
    surface: 'user',
    owner: 'admin',
    created_by: 'admin',
    created_at: '2026-09-15T11:00:00Z',
    updated_at: '2026-09-15T11:00:00Z',
    agent_id: 'worker',
    agent_name: 'Worker',
    ...overrides,
  }
}

describe('TaskCard assignee warning', () => {
  it("shows the server's warning, naming the fix", () => {
    render(
      <TaskCard
        task={makeTask({ assignee_warning: { message: WARNING, field: 'agent_id' } })}
        onClick={() => {}}
        showActions={false}
      />,
    )
    expect(screen.getByTestId('task-assignee-warning')).toHaveTextContent(WARNING)
  })

  it('shows nothing when the agent can finish the task', () => {
    render(<TaskCard task={makeTask()} onClick={() => {}} showActions={false} />)
    expect(screen.queryByTestId('task-assignee-warning')).not.toBeInTheDocument()
  })
})

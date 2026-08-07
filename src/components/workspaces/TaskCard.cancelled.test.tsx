/**
 * TaskCard.cancelled.test.tsx — ADR-052 FR-015/US-8 (orange "Cancelled" vs
 * red "Failed" on the Board card) + §6.8 (the ▶/■ action button embedded on
 * the card, and BoardView's `showActions={false}` opt-out for its drag
 * ghost).
 *
 * The full per-state action button matrix lives in TaskActionButton.test.tsx
 * — this file only proves TaskCard actually wires `TaskActionButton` with
 * the real task and honors `showActions`.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { TaskCard } from './TaskCard'
import type { Task } from '@/lib/api'
import { TASK_CANCELLED_COLOR, STATUS_COLORS, taskDisplayColor } from '@/lib/statusColors'

const runTaskMock = vi.fn()
const stopTaskMock = vi.fn()
const restartTaskMock = vi.fn()

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    runTask: (id: string) => runTaskMock(id),
    stopTask: (id: string) => stopTaskMock(id),
    restartTask: (id: string) => restartTaskMock(id),
  }
})

vi.mock('@/store/ui', () => ({
  useUiStore: (selector?: (s: { addToast: ReturnType<typeof vi.fn> }) => unknown) => {
    const store = { addToast: vi.fn() }
    return selector ? selector(store) : store
  },
}))

beforeEach(() => {
  runTaskMock.mockReset()
  stopTaskMock.mockReset()
  restartTaskMock.mockReset()
})

const baseTask = (overrides: Partial<Task> = {}): Task => ({
  id: 'task-1',
  title: 'Sample task',
  status: 'inbox',
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

function renderCard(task: Task, extraProps: Partial<React.ComponentProps<typeof TaskCard>> = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <TaskCard task={task} onClick={vi.fn()} {...extraProps} />
    </QueryClientProvider>,
  )
}

describe('TaskCard — orange "Cancelled" vs red "Failed" marker (ADR-052 FR-015/US-8)', () => {
  it('a task Stopped by the user (cancel_reason: stopped_by_user) shows "Cancelled", not "Failed"', () => {
    renderCard(baseTask({ status: 'failed', cancel_reason: 'stopped_by_user' }))
    expect(screen.getByText('Cancelled')).toBeInTheDocument()
    expect(screen.queryByText('Failed')).not.toBeInTheDocument()
  })

  it('a genuinely-failed task (no cancel_reason) shows "Failed", not "Cancelled"', () => {
    renderCard(baseTask({ status: 'failed' }))
    expect(screen.getByText('Failed')).toBeInTheDocument()
    expect(screen.queryByText('Cancelled')).not.toBeInTheDocument()
  })

  it('renders no state chip at all for a non-failed task', () => {
    renderCard(baseTask({ status: 'in_progress' }))
    expect(screen.queryByText('Failed')).not.toBeInTheDocument()
    expect(screen.queryByText('Cancelled')).not.toBeInTheDocument()
  })

  it('"Cancelled" and "Failed" render in visually distinct (non-equal) colours', () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const { rerender } = render(
      <QueryClientProvider client={client}>
        <TaskCard task={baseTask({ status: 'failed', cancel_reason: 'stopped_by_user' })} onClick={vi.fn()} />
      </QueryClientProvider>,
    )
    const cancelledColor = screen.getByText('Cancelled').style.color

    rerender(
      <QueryClientProvider client={client}>
        <TaskCard task={baseTask({ status: 'failed' })} onClick={vi.fn()} />
      </QueryClientProvider>,
    )
    const failedColor = screen.getByText('Failed').style.color
    expect(cancelledColor).not.toBe(failedColor)
    expect(cancelledColor).not.toBe('')
    expect(failedColor).not.toBe('')
  })

  // Gate-2 finding #1 regression: TaskCard.tsx:236 builds the pill's tint
  // fill as `` `${taskDisplayColor(task)}1a` `` — a bare string concat, not a
  // CSS function call. If TASK_CANCELLED_COLOR were ever a `var(--x)`
  // reference again, the result (`var(--color-warning)1a`) is not valid CSS
  // (`var()` doesn't accept a trailing token), so the whole `backgroundColor`
  // declaration would be silently dropped and the "Cancelled" pill would
  // render with orange text but NO tint — inconsistent with the red "Failed"
  // pill. jsdom won't reliably validate/reject that invalid CSS for us, so
  // this asserts directly on the STRING the component builds, not on DOM
  // style-parsing behavior.
  it('TASK_CANCELLED_COLOR is a literal hex, not a var(...) reference (Gate-2 finding #1)', () => {
    expect(TASK_CANCELLED_COLOR).not.toMatch(/^var\(/)
    expect(TASK_CANCELLED_COLOR).toMatch(/^#[0-9a-fA-F]{6}$/)
  })

  it('both the cancelled and failed pill tints (`${displayColor}1a`) are valid 8-digit hex strings', () => {
    const cancelledTint = `${taskDisplayColor({ status: 'failed', cancel_reason: 'stopped_by_user' })}1a`
    const failedTint = `${taskDisplayColor({ status: 'failed' })}1a`
    expect(cancelledTint).toMatch(/^#[0-9a-fA-F]{8}$/)
    expect(failedTint).toMatch(/^#[0-9a-fA-F]{8}$/)
    // And they share the exact literal-hex mechanism (STATUS_COLORS.failed is
    // also a literal hex) — one consistent tint mechanism for both pills.
    expect(STATUS_COLORS.failed).toMatch(/^#[0-9a-fA-F]{6}$/)
  })
})

describe('TaskCard — ▶/■ action button wiring (ADR-052 §6.8)', () => {
  it('renders the action button for an idle (inbox) task by default', () => {
    renderCard(baseTask({ status: 'inbox', title: 'Draft the memo' }))
    expect(screen.getByRole('button', { name: 'Run task Draft the memo' })).toBeInTheDocument()
  })

  it('renders no action button when the task offers none (done)', () => {
    renderCard(baseTask({ status: 'done' }))
    expect(screen.queryByRole('button', { name: /task Sample task/ })).not.toBeInTheDocument()
  })

  it('suppresses the action button when showActions=false (BoardView drag-ghost opt-out)', () => {
    renderCard(baseTask({ status: 'inbox' }), { showActions: false })
    expect(screen.queryByRole('button', { name: /Run task/ })).not.toBeInTheDocument()
  })

  it('clicking the action button never opens the task (onClick isolation)', async () => {
    const onClick = vi.fn()
    const userEventModule = await import('@testing-library/user-event')
    const user = userEventModule.default.setup()
    renderCard(baseTask({ status: 'inbox', title: 'Draft the memo' }), { onClick })
    await user.click(screen.getByRole('button', { name: 'Run task Draft the memo' }))
    expect(onClick).not.toHaveBeenCalled()
  })
})

// Founder T24–T27. Generated task timestamps are the duration oracle.
import { afterEach, expect, it, vi } from 'vitest'
import { act, screen, within } from '@testing-library/react'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

// Independent oracle: resolve the registered token source, not TaskCard's
// presentation helper or generated implementation lookup.
const tokenSource = JSON.parse(readFileSync(resolve(process.cwd(), 'design-system/tokens/colors.json'), 'utf8'))
function canonicalPaint(id: string): string {
  const entry = tokenSource.tokens.find((token: { id: string }) => token.id === id)
  if (!entry) throw new Error(`Missing canonical token ${id}`)
  return entry.value ?? canonicalPaint(entry.ref)
}
function normalizedPaint(id: string): string {
  const hex = canonicalPaint(id)
  return `rgb(${[1, 3, 5].map((offset) => Number.parseInt(hex.slice(offset, offset + 2), 16)).join(', ')})`
}
import { TaskCard } from './TaskCard'
import { useToolApprovalStore } from '@/store/toolApproval'
import { layoutTask, renderLayout } from './tasksLayoutFixtures'

afterEach(() => { vi.restoreAllMocks(); vi.useRealTimers(); useToolApprovalStore.setState({ queue: [], resolvedIds: [] }) })

it('T24 row one contains priority and eligible controls only, never the task title', () => {
  renderLayout(<TaskCard task={layoutTask()} onClick={vi.fn()} />)
  const info = screen.getByRole('button', { name: 'Task details: Ray report' })
  const row = info.closest('[data-task-card-row="controls"]')!
  expect(row).not.toBeNull()
  expect(within(row as HTMLElement).getByText('P3')).toBeVisible()
  expect(within(row as HTMLElement).getByRole('button', { name: 'Run task Ray report' })).toBeVisible()
  expect(within(row as HTMLElement).queryByText('Ray report')).not.toBeInTheDocument()
})

it('T25 the two-line title slot owns the full card width beneath controls, with no sibling priority/info/action in its rows', () => {
  renderLayout(<TaskCard task={layoutTask({ title: 'Diagnose gateway interruption and verification' })} onClick={vi.fn()} />)
  const title = screen.getByText('Diagnose gateway interruption and verification')
  expect(title).toHaveClass('w-full', 'max-w-full', 'line-clamp-2', 'break-normal', 'wrap-break-word')
  expect(title.closest('[data-task-card-row="controls"]')).toBeNull()
  expect(title.previousElementSibling).toHaveAttribute('data-task-card-row', 'controls')
  expect(title.parentElement).toHaveAttribute('data-task-card-content')
})

it('T26 agent/activity/elapsed occupy row four, checklist row five is optional, live time ticks and terminal time stays fixed', () => {
  vi.useFakeTimers({ toFake: ['Date'] })
  vi.setSystemTime(new Date('2026-10-08T18:01:02Z'))
  let tick: (() => void) | undefined
  const clear = vi.spyOn(window, 'clearInterval')
  vi.spyOn(window, 'setInterval').mockImplementation((handler) => {
    expect(typeof handler).toBe('function')
    tick = handler as () => void
    return 17 as unknown as ReturnType<typeof setInterval>
  })
  const running = layoutTask({ status: 'in_progress', started_at: '2026-10-08T18:00:00Z', tags: ['hidden-tag'], todos: [{ text: 'Checked', status: 'completed' }, { text: 'Pending', status: 'pending' }] })
  const mounted = renderLayout(<TaskCard task={running} onClick={vi.fn()} showActions={false} />)
  const execution = screen.getByTestId('task-execution-row')
  expect(within(execution).getByText('Ray')).toBeVisible()
  expect(within(execution).getByRole('status', { name: 'Running' })).toBeVisible()
  expect(within(execution).getByText('1m 02s')).toBeVisible()
  expect(screen.getByTestId('task-checklist-row')).toHaveTextContent('1/2')
  expect(screen.queryByText('In Progress')).not.toBeInTheDocument()
  expect(screen.queryByText('hidden-tag')).not.toBeInTheDocument()
  vi.setSystemTime(new Date('2026-10-08T18:01:03Z'))
  act(() => tick!())
  expect(within(execution).getByText('1m 03s')).toBeVisible()
  mounted.unmount()
  expect(clear).toHaveBeenCalledWith(17)

  for (const status of ['done', 'failed'] as const) {
    const terminal = renderLayout(<TaskCard task={layoutTask({ status, started_at: '2026-10-08T18:00:00Z', completed_at: '2026-10-08T18:02:05Z' })} onClick={vi.fn()} />)
    expect(screen.getByTestId('task-execution-row')).toHaveTextContent('2m 05s')
    expect(screen.queryByTestId('task-checklist-row')).not.toBeInTheDocument()
    expect(screen.queryByRole('status', { name: 'Running' })).not.toBeInTheDocument()
    vi.setSystemTime(new Date('2026-10-08T19:00:00Z'))
    expect(screen.getByTestId('task-execution-row')).toHaveTextContent('2m 05s')
    terminal.unmount()
  }
  renderLayout(<TaskCard task={layoutTask({ status: 'failed', started_at: 'invalid', completed_at: '2026-10-08T18:02:05Z' })} onClick={vi.fn()} />)
  expect(screen.getByLabelText('Execution time unavailable')).toHaveTextContent('—')
})

it('the founder card-alert correction keeps approval and assignee warning icons visible in controls, with the exact meaning in info', async () => {
  const warning = 'Allow goal_claim for Worker or assign another agent.'
  useToolApprovalStore.setState({ queue: [{ approvalId: 'approval', toolCallId: 'call', toolName: 'bash', args: {}, agentId: 'ray', sessionId: 'session-task', turnId: 'turn', expiresAt: Date.now() + 60_000, workspaceId: 'ws-layout' }], resolvedIds: [] })
  renderLayout(<TaskCard task={layoutTask({ status: 'in_progress', session_id: 'session-task', assignee_warning: { field: 'agent_id', message: warning } })} onClick={vi.fn()} />)
  const approval = screen.getByRole('img', { name: 'Waiting for your approval to use bash' })
  const assignee = screen.getByRole('img', { name: warning })
  expect(approval.closest('[data-task-card-row="controls"]')).toContainElement(assignee)
  const info = screen.getByRole('button', { name: 'Task details: Ray report' })
  const { fireEvent } = await import('@testing-library/react')
  fireEvent.pointerEnter(info, { pointerType: 'mouse' })
  const details = await screen.findByRole('dialog', { name: 'Task details' })
  expect(within(details).getByText(warning)).toBeVisible()
  expect(within(details).getByText('Waiting for your approval to use bash')).toBeVisible()
})

it('T27 the left border uses the canonical status colour for all six states and cancellation, without showing a status word', () => {
  const cases = [
    ['inbox', 'color.status.inbox'], ['next', 'color.status.next'],
    ['in_progress', 'color.status.in-progress'], ['blocked', 'color.status.blocked'],
    ['done', 'color.status.done'], ['failed', 'color.status.failed'],
  ] as const
  for (const [status, token] of cases) {
    const mounted = renderLayout(<TaskCard task={layoutTask({ status })} onClick={vi.fn()} showActions={false} />)
    const card = screen.getByText('Ray report').closest<HTMLElement>('[role="button"]')!
    expect(card).toHaveClass('border-l-[length:var(--space-1)]')
    expect(card.style.borderLeftColor).toBe(normalizedPaint(token))
    mounted.unmount()
  }
  const cancelled = renderLayout(<TaskCard task={layoutTask({ status: 'failed', cancel_reason: 'stopped_by_user' })} onClick={vi.fn()} />)
  const card = screen.getByText('Ray report').closest<HTMLElement>('[role="button"]')!
  expect(card.style.borderLeftColor).toBe(normalizedPaint('color.status.cancelled'))
  expect(screen.queryByText('Cancelled')).not.toBeInTheDocument()
  cancelled.unmount()
})

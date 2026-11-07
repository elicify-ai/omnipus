// T10 one-shot / schedule-bearing panel scenarios not covered by the RED pack
// (schedules-t10-spec-r6.md US-9 matrix rows 4 and 7, S-H.13, S-H.16 screen
// side): pressing Run now posts the task-level run-now with no occurrence and
// writes no status; a recurring series whose last run is done still offers
// Run now; an ordinary manual task keeps the PATCH-status Start unchanged.
import { beforeAll, beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { TaskDetailPanel } from './TaskDetailPanel'
import * as api from '@/lib/api'
import type { Task } from '@/lib/api'

vi.mock('@tanstack/react-router', () => ({ useNavigate: () => vi.fn(), Link: ({ children }: { children: React.ReactNode }) => <span>{children}</span> }))
vi.mock('@/lib/api', async (original) => {
  const actual = await original<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([{ id: 'mia', name: 'Mia', type: 'core', status: 'idle', soul: '', locked: true }]),
    fetchSubtasks: vi.fn().mockResolvedValue([]), fetchTasks: vi.fn().mockResolvedValue([]),
    fetchPlans: vi.fn().mockResolvedValue([]), fetchWorkspaces: vi.fn().mockResolvedValue([]),
    fetchTaskEvidence: vi.fn().mockResolvedValue([]), fetchTaskVerdicts: vi.fn().mockResolvedValue([]),
    fetchTaskRuns: vi.fn().mockResolvedValue([]),
    fetchWorkspaceDelegation: vi.fn().mockRejectedValue(new Error('unscoped test roster')),
    updateTask: vi.fn().mockResolvedValue({}),
    runTaskNow: vi.fn().mockResolvedValue(undefined),
  }
})
const toast = vi.hoisted(() => vi.fn())
vi.mock('@/store/ui', () => ({ useUiStore: (select?: (s: { addToast: typeof toast }) => unknown) => select ? select({ addToast: toast }) : { addToast: toast } }))

beforeAll(() => {
  Element.prototype.hasPointerCapture ??= () => false
  Element.prototype.scrollIntoView ??= () => {}
  window.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver
})
beforeEach(() => {
  vi.mocked(api.runTaskNow).mockReset().mockResolvedValue(undefined)
  vi.mocked(api.updateTask).mockClear()
  toast.mockClear()
})
afterEach(cleanup)

function open(type: 'manual' | 'once' | 'recurring', status: Task['status'] = 'inbox') {
  const at = Date.now() + 48 * 60 * 60 * 1000
  const task: Task = {
    id: `t10-fe-${type}-${status}`, title: `T10 ${type} task`, prompt: 'Write the report', agent_id: 'mia',
    action: 'llm', status, priority: 3, workspace_id: 'ws-1', owner: 'alice', created_by: 'alice',
    created_at: '2026-10-01T00:00:00Z', updated_at: '2026-10-01T00:00:00Z', surface: 'user',
    trigger: type === 'manual' ? { type, config: {} } : type === 'once' ? { type, config: { at_ms: at } } : { type, config: { rrule: 'FREQ=DAILY;COUNT=3', dtstart_ms: at, tz: 'UTC' } },
  }
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  render(<QueryClientProvider client={client}><TaskDetailPanel task={task} onClose={vi.fn()} /></QueryClientProvider>)
  return task
}

describe('T10 schedule-bearing panel Run now (S-H.13 screen side)', () => {
  it('one-shot Run now calls the task-level run-now with no occurrence and writes no status', async () => {
    const task = open('once')
    fireEvent.click(await screen.findByRole('button', { name: /^Run now$/i }))
    await waitFor(() => expect(vi.mocked(api.runTaskNow)).toHaveBeenCalledTimes(1))
    expect(vi.mocked(api.runTaskNow).mock.calls[0]).toEqual([task.id])
    expect(vi.mocked(api.updateTask)).not.toHaveBeenCalled()
    await waitFor(() => expect(toast).toHaveBeenCalledWith({ message: 'Task started.', variant: 'success' }))
  })

  it('shows the server refusal reason when Run now is refused', async () => {
    // As ApiError.fromResponse builds it: the generic 409 line as userMessage,
    // the server's reason only on body. The toast must name the real blocker.
    const reason = 'The schedule of this task is not synced; save it again to run it.'
    vi.mocked(api.runTaskNow).mockRejectedValueOnce(new api.ApiError(409, undefined, { body: JSON.stringify({ error: reason }) }))
    open('once')
    fireEvent.click(await screen.findByRole('button', { name: /^Run now$/i }))
    await waitFor(() => expect(toast).toHaveBeenCalledWith({ message: 'The schedule of this task is not synced; save it again to run it.', variant: 'error' }))
    expect(vi.mocked(api.updateTask)).not.toHaveBeenCalled()
  })

  it('a recurring series whose last run is done still offers Run now (matrix row 7)', async () => {
    open('recurring', 'done')
    expect(await screen.findByRole('button', { name: /^Run now$/i })).toBeVisible()
    expect(screen.queryByRole('button', { name: 'Start Task' })).not.toBeInTheDocument()
  })

  it('a done one-shot task offers no Run now (existing task-level gate)', async () => {
    open('once', 'done')
    await screen.findByText('T10 once task', { exact: true })
    expect(screen.queryByRole('button', { name: /^Run now$/i })).not.toBeInTheDocument()
  })

  it('an ordinary manual task keeps the PATCH-status Start and has no Run now (control)', async () => {
    const task = open('manual')
    fireEvent.click(await screen.findByRole('button', { name: 'Start Task' }))
    await waitFor(() => expect(vi.mocked(api.updateTask)).toHaveBeenCalledWith(task.id, { status: 'in_progress' }))
    expect(screen.queryByRole('button', { name: /^Run now$/i })).not.toBeInTheDocument()
    expect(vi.mocked(api.runTaskNow)).not.toHaveBeenCalled()
  })
})

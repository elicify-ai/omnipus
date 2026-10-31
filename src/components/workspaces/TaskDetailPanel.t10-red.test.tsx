import { beforeAll, afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { TaskDetailPanel } from './TaskDetailPanel'
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
  }
})
const toast = vi.hoisted(() => vi.fn())
vi.mock('@/store/ui', () => ({ useUiStore: (select?: (s: { addToast: typeof toast }) => unknown) => select ? select({ addToast: toast }) : { addToast: toast } }))

beforeAll(() => {
  Element.prototype.hasPointerCapture ??= () => false
  Element.prototype.scrollIntoView ??= () => {}
  window.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver
})
afterEach(cleanup)
function open(type: 'manual' | 'once' | 'recurring') {
  const at = Date.now() + 48 * 60 * 60 * 1000
  const task: Task = {
    id: `t10-status-${type}`, title: `T10 ${type} task`, prompt: 'Write the report', agent_id: 'mia',
    action: 'llm', status: 'inbox', priority: 3, workspace_id: 'ws-1', owner: 'alice', created_by: 'alice',
    created_at: '2026-10-01T00:00:00Z', updated_at: '2026-10-01T00:00:00Z', surface: 'user',
    trigger: type === 'manual' ? { type, config: {} } : type === 'once' ? { type, config: { at_ms: at } } : { type, config: { rrule: 'FREQ=DAILY;COUNT=3', dtstart_ms: at, tz: 'UTC' } },
  }
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  render(<QueryClientProvider client={client}><TaskDetailPanel task={task} onClose={vi.fn()} /></QueryClientProvider>)
}

describe('T10 schedule-bearing task panel', () => {
  // Traces to: schedules-t10-spec-r6.md lines 985–990, 1304; TDD 21/25, D-11.9.
  it.each(['once', 'recurring'] as const)('%s status selector offers neither activation route', async type => {
    open(type)
    const selector = await screen.findByRole('combobox', { name: 'Status' })
    fireEvent.click(selector)
    expect(screen.queryByRole('option', { name: 'Next', exact: true })).not.toBeInTheDocument()
    expect(screen.queryByRole('option', { name: 'In Progress', exact: true })).not.toBeInTheDocument()
  })

  // Traces to: schedules-t10-spec-r6.md lines 985–990, 1304; D-11.9 manual control.
  it('ordinary manual task retains both existing status routes', async () => {
    open('manual')
    fireEvent.click(await screen.findByRole('combobox', { name: 'Status' }))
    expect(await screen.findByRole('option', { name: 'Next', exact: true })).toBeVisible()
    expect(screen.getByRole('option', { name: 'In Progress', exact: true })).toBeVisible()
  })

  // Traces to: schedules-t10-spec-r6.md lines 959–965, 1302; TDD 21/25, D-11.7.
  it('one-shot detail keeps title and instruction but replaces PATCH Start with Run now', async () => {
    open('once')
    expect(await screen.findByText('T10 once task', { exact: true })).toBeVisible()
    expect(screen.getByText('Write the report', { exact: true })).toBeVisible()
    expect(screen.queryByRole('button', { name: 'Start Task' })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: /^Run now$/i })).toBeVisible()
  })
})

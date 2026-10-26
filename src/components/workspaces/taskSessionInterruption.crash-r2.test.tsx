import { act, cleanup, render, renderHook, screen, waitFor } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Task } from '@/lib/api'
import { fetchSessionDetail, fetchSessionPage } from '@/lib/api/sessions'
import type { components } from '@/lib/api/generated/openapi-types'
import { queryClient } from '@/lib/queryClient'
import { TaskCard } from './TaskCard'
import { ListView } from './ListView'
import { GraphView } from './graph/GraphView'
import { useTaskSessionInterruption } from './taskSessionInterruption'

// CR I2 + PRT T5 / DESIGN 1: the actual HTTP adapter and observer remain real.
// The process-edge fetch server obeys the gateway's roots-only default, exact
// detail and explicitly child-inclusive list contracts. Never put a child into
// the ['sessions'] roots cache to make these tests pass.
const PARENT = 'r2-parent-main'
const CHILD = 'r2-task-child'
const NOW = '2026-10-26T00:00:00Z'
type WireSession = components['schemas']['Session']
type Cause = 'tool_limit' | 'reconciliation_required'
let state: WireSession['lifecycle_state']
let cause: Cause
const requests: string[] = []
function session(child: boolean): WireSession {
  return {
    id: child ? CHILD : PARENT, type: child ? 'delegate' : 'main', agent_id: 'mia',
    title: child ? 'Task child' : 'Parent', status: child && state === 'interrupted' ? 'interrupted' : 'active',
    created_at: NOW, updated_at: NOW, channel: 'webchat', partitions: [],
    stats: { tokens_in: 0, tokens_out: 0, tokens_total: 0, cost: 0, tool_calls: 0, message_count: 0 },
    ...(child ? { parent_session_id: PARENT, lifecycle_state: state,
      ...(state === 'interrupted' ? { interruption_reason: cause } : { execution: 'running' as const }) } : { child_count: 1 }),
  }
}
const task: Task = {
  id: 'r2-task', title: 'Observe interrupted child', status: 'in_progress', action: 'llm', priority: 3,
  workspace_id: 'r2-workspace', surface: 'user', agent_id: 'mia', session_id: CHILD,
  owner: 'owner', created_by: 'owner', created_at: NOW, updated_at: NOW, started_at: NOW,
}
beforeEach(() => {
  state = 'interrupted'; cause = 'tool_limit'; requests.length = 0
  queryClient.clear()
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
    const url = new URL(typeof input === 'string' ? input : input instanceof URL ? input.href : input.url, 'http://localhost')
    requests.push(url.pathname + url.search)
    let body: unknown
    if (url.pathname === `/api/v1/sessions/${CHILD}`) body = { session: session(true), messages: [] }
    else if (url.pathname === '/api/v1/sessions') {
      const inclusive = url.searchParams.get('flat') === 'true' || url.searchParams.get('parent_session_id') === PARENT
      body = { sessions: inclusive ? [session(true)] : [session(false)] }
    } else return new Response(JSON.stringify({ error: `Unexpected test HTTP request ${url}` }), { status: 500 })
    return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })
  }))
})
afterEach(() => { cleanup(); queryClient.clear(); vi.unstubAllGlobals(); vi.restoreAllMocks() })
async function settled() {
  await waitFor(() => expect(requests.length).toBeGreaterThan(0))
  await waitFor(() => expect(queryClient.isFetching()).toBe(0))
  await act(async () => { await Promise.resolve() })
}

it('I2: locates the exact interrupted child without contaminating the roots-only sidebar query', async () => {
  // Positive instrument: the real adapters decode the available child detail
  // and roots response correctly before exercising the real hook.
  const roots = await fetchSessionPage()
  expect(roots.sessions.map(s => s.id)).toEqual([PARENT])
  const detail = await fetchSessionDetail(CHILD)
  expect(detail.session.id).toBe(CHILD)
  expect(detail.session.interruption_reason).toBe('tool_limit')
  requests.length = 0
  const observed = renderHook(() => useTaskSessionInterruption(CHILD))
  await settled()
  expect.soft(observed.result.current).toStrictEqual({ reason: 'tool_limit' })
  const cached = queryClient.getQueryData<Array<{ id: string }>>(['sessions'])
  expect(cached?.some(s => s.id === CHILD) ?? false).toBe(false)
})

describe.each(['Board', 'List', 'Graph'] as const)('T5: %s task presentation', (surface) => {
  function mount() {
    const element = surface === 'Board'
      ? <TaskCard task={task} onClick={vi.fn()} showActions={false} showDetails={false} />
      : surface === 'List'
        ? <ListView tasks={[task]} agents={[]} onTaskClick={vi.fn()} />
        : <GraphView tasks={[task]} agents={[]} onTaskClick={vi.fn()} />
    return render(<QueryClientProvider client={queryClient}>{element}</QueryClientProvider>)
  }
  it.each(['tool_limit', 'reconciliation_required'] as const)('%s is static Interrupted, with its cause and no running clock', async (reason) => {
    cause = reason
    const view = mount()
    await settled()
    const notice = screen.queryByTestId('task-session-interrupted')
    expect.soft(notice?.textContent).toBe('Interrupted')
    expect.soft(notice?.getAttribute('title')).toMatch(reason === 'tool_limit' ? /limit/i : /file.*check/i)
    expect.soft(view.container.querySelector('[aria-label="Running"]')).toBeNull()
    expect.soft(view.container.querySelector('.animate-spin')).toBeNull()
    expect.soft(view.container.querySelector('time[aria-label^="Execution time"]')).toBeNull()
    expect(requests.some(path => path.includes(CHILD) || path.includes('flat=true') || path.includes('parent_session_id='))).toBe(true)
  })
  it('Working positive control and an explicit continuation restore running presentation', async () => {
    state = 'working'
    const view = mount()
    await settled()
    expect(view.container.querySelector('[aria-label="Running"]')).not.toBeNull()
    expect(screen.queryByTestId('task-session-interrupted')).toBeNull()
    state = 'interrupted'
    await act(async () => { await queryClient.invalidateQueries() })
    await settled()
    expect.soft(screen.queryByTestId('task-session-interrupted')?.textContent).toBe('Interrupted')
    state = 'working' // A subsequent server-observed, explicitly continued execution.
    await act(async () => { await queryClient.invalidateQueries() })
    await settled()
    expect.soft(screen.queryByTestId('task-session-interrupted')).toBeNull()
    expect.soft(view.container.querySelector('[aria-label="Running"]')).not.toBeNull()
  })
})

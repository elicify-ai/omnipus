import type { ReactNode } from 'react'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { QueryClientProvider } from '@tanstack/react-query'
import type { Session as WireSession, SessionPage } from '@/lib/api/generated/openapi-types'
import type { SubagentStartFrame, SubagentStateFrame, SubagentEndFrame, CatchUpCompleteFrame } from '@/lib/api/generated/asyncapi-types'
import type { Session, SessionListPage } from '@/lib/api'
import type { WsConnection } from '@/lib/ws'
import { queryClient } from '@/lib/queryClient'
import { getMessages, useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { useSidebarStore } from '@/store/sidebar'
import { Sidebar } from './Sidebar'

// U1-R1/R2 oracle: supplied UAT triage. A done frame precedes lifecycle
// settlement; never infer the REST row's state from it or from its children.
// Real list adapter, queryClient, Sidebar/tree rows, stores and frame routing.
// Only HTTP/socket/router and incidental non-session service calls are fake.
const { PARENT, CHILD, WORKSPACE } = vi.hoisted(() => ({
  PARENT: 'uat-live-parent', CHILD: 'uat-live-child', WORKSPACE: 'uat-live-workspace',
}))
const sender = { send: vi.fn<WsConnection['send']>().mockReturnValue(true) }

vi.mock('@tanstack/react-router', () => ({
  useLocation: () => ({ pathname: '/' }),
  useNavigate: () => vi.fn(),
  Link: ({ children, to, search: _search, ...props }: { children: ReactNode; to: string; search?: unknown }) => <a href={to} {...props}>{children}</a>,
}))
vi.mock('@/lib/api', async (original) => ({
  ...await original<typeof import('@/lib/api')>(),
  fetchWorkspaces: vi.fn().mockResolvedValue([{ id: WORKSPACE, name: 'UAT workspace', status: 'active', pinned: false }]),
  fetchAgents: vi.fn().mockResolvedValue([]),
  fetchAppState: vi.fn().mockResolvedValue({ dev_mode_bypass: false, identity: { mode: 'platform' } }),
  fetchGodMode: vi.fn().mockResolvedValue({ enabled: false }),
}))

function wireRow(id: string, lifecycle: WireSession['lifecycle_state']): WireSession {
  return {
    id, agent_id: 'jim', title: id === PARENT ? 'Parent conversation' : 'Child conversation',
    type: id === PARENT ? 'chat' : 'delegate', status: 'active', lifecycle_state: lifecycle,
    workspace_id: WORKSPACE, ...(id === CHILD ? { parent_session_id: PARENT } : {}),
    child_count: id === PARENT ? 3 : 0, channel: 'webchat', partitions: [],
    created_at: '2026-10-06T00:00:00Z', updated_at: '2026-10-06T00:00:00Z',
    stats: { tokens_in: 0, tokens_out: 0, tokens_total: 0, cost: 0, tool_calls: 0, message_count: 1 },
  }
}

let root: WireSession
let child: WireSession
let rootFetches: number
let childFetches: number
const originalOptions = queryClient.getDefaultOptions()

beforeEach(() => {
  vi.useFakeTimers({ toFake: ['Date', 'setInterval', 'clearInterval'] })
  vi.setSystemTime(new Date('2026-10-06T00:00:00Z'))
  queryClient.clear()
  queryClient.setDefaultOptions({ queries: { retry: false, gcTime: Infinity } })
  root = wireRow(PARENT, 'working')
  child = wireRow(CHILD, 'working')
  rootFetches = 0
  childFetches = 0
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
    const url = new URL(String(input), 'http://localhost')
    if (url.pathname !== '/api/v1/sessions') throw new Error(`Unexpected HTTP request: ${url}`)
    const children = url.searchParams.get('parent_session_id') === PARENT
    if (children) childFetches++
    else rootFetches++
    const body: SessionPage = { sessions: [structuredClone(children ? child : root)] }
    return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })
  }))
  vi.stubGlobal('matchMedia', (query: string) => ({ matches: true, media: query, addEventListener: vi.fn(), removeEventListener: vi.fn() }))
  act(() => {
    useSessionStore.setState(useSessionStore.getInitialState(), true)
    useChatStore.setState(useChatStore.getInitialState(), true)
    useConnectionStore.setState(useConnectionStore.getInitialState(), true)
    useWorkspacesStore.setState(useWorkspacesStore.getInitialState(), true)
    useSidebarStore.setState({ isOpen: true, isPinned: true })
    useWorkspacesStore.getState().setActiveWorkspaceId(WORKSPACE)
    useConnectionStore.getState().setConnection(sender as unknown as WsConnection)
    useConnectionStore.getState().setConnected(true)
    useSessionStore.getState().setActiveSession(PARENT, 'jim')
  })
})

afterEach(() => {
  cleanup()
  queryClient.clear()
  queryClient.setDefaultOptions(originalOptions)
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

async function mountSidebar(expandChildren = false) {
  render(<QueryClientProvider client={queryClient}><Sidebar /></QueryClientProvider>)
  await screen.findByText('Parent conversation')
  if (expandChildren) {
    fireEvent.click(screen.getByLabelText('Expand Parent conversation delegated sessions'))
    await screen.findByText('Child conversation')
  }
}

function rowText(title: string) {
  return screen.getByText(title).closest('button')!.textContent
}

function streamParent() {
  act(() => {
    useChatStore.getState().sendMessage('Report progress', { clientMessageId: 'uat-live-user' })
    useChatStore.getState().handleFrame({ type: 'token', session_id: PARENT, turn_id: 'turn-1', message_id: 'reply-1', content: 'Saved answer' })
  })
}

async function tickRefresh() {
  await act(async () => { await vi.advanceTimersByTimeAsync(15_000) })
}

const start: SubagentStartFrame = { type: 'subagent_start', session_id: PARENT, span_id: 'span-child', parent_call_id: 'delegate-child', child_session_id: CHILD, task_label: 'Child work' }
const state: SubagentStateFrame = { type: 'subagent_state', session_id: PARENT, span_id: start.span_id, child_session_id: CHILD, state: 'completed', created_at: '2026-10-06T00:00:01Z' }
const end: SubagentEndFrame = { type: 'subagent_end', session_id: PARENT, span_id: start.span_id, status: 'success' }
const catchUp: CatchUpCompleteFrame = { type: 'catch_up_complete', session_id: PARENT, seq: 0, boot_id: 'uat-boot', mode: 'snapshot' }

describe('U1 — visible session lists follow real terminal/lifecycle frames and late settlement', () => {
  it.each(['done', 'stopped'] as const)('sees REST %s committed after the final frame, without another frame or remount', async (settled) => {
    await mountSidebar(true)
    streamParent()
    expect(rowText('Parent conversation')).toBe('Parent conversation Working3')
    const before = rootFetches
    act(() => {
      if (settled === 'stopped') {
        useChatStore.getState().cancelStream()
        useChatStore.getState().handleFrame({ type: 'error', session_id: PARENT, message: 'This turn was stopped before it finished.', payload: { llm_error: { code: 'turn_canceled', message: 'This turn was stopped before it finished.', retryable: true } } })
      }
      useChatStore.getState().handleFrame({ type: 'done', session_id: PARENT, turn_id: 'turn-1', stats: { tokens: 1, cost: 0 } })
    })
    // First refetch is deliberately BEFORE settlement. The cache must not
    // invent Done/Stopped from done, an idle composer, or finished helpers.
    await waitFor(() => expect(rootFetches).toBeGreaterThan(before))
    expect(queryClient.getQueryData<Session[]>(['sessions'])?.[0].lifecycle_state).toBe('working')
    expect(rowText('Parent conversation')).toBe('Parent conversation Working3')
    const transcript = structuredClone(getMessages(useChatStore.getState().sessionsById[PARENT]))
    root = { ...root, lifecycle_state: settled, ...(settled === 'stopped' ? { stop_note: { at: '2026-10-06T00:00:02Z', by: 'human:user', seq: 1, cause: 'stop' } } : {}) }
    child = { ...child, lifecycle_state: 'done' }
    const afterImmediateFetch = rootFetches
    const childBefore = childFetches
    await tickRefresh()
    await waitFor(() => expect(rowText('Parent conversation')).toBe(settled === 'done' ? 'Parent conversation Done3' : 'Parent conversation Stopped · stop3'))
    expect(rootFetches).toBeGreaterThan(afterImmediateFetch)
    expect(childFetches).toBeGreaterThan(childBefore)
    expect(rowText('Child conversation')).toBe('Child conversation Done')
    expect(queryClient.getQueryData<Session[]>(['sessions'])?.[0].lifecycle_state).toBe(settled)
    expect(getMessages(useChatStore.getState().sessionsById[PARENT])).toStrictEqual(transcript)
  })

  it.each([start, state, end, catchUp])('$type refreshes root and expanded pages even while another chat is foreground', async (frame) => {
    await mountSidebar(true)
    act(() => {
      useChatStore.getState().handleFrame(start)
      useSessionStore.getState().setActiveSession('unrelated-session', 'jim')
      useChatStore.getState().sendMessage('Keep this chat unchanged', { clientMessageId: 'unrelated-user' })
      useChatStore.getState().handleFrame({ type: 'token', session_id: 'unrelated-session', content: 'Unrelated reply' })
    })
    // Join the actual fetch promises. A clock-driven waitFor cannot observe
    // an unchanged response while its polling clock is deliberately frozen.
    await act(async () => {
      await Promise.all(queryClient.getQueryCache().findAll({ queryKey: ['sessions'] }).map((query) => query.promise))
    })
    expect(queryClient.isFetching({ queryKey: ['sessions'] })).toBe(0)
    const foreground = structuredClone(useChatStore.getState().messages)
    const before = { roots: rootFetches, children: childFetches }
    root = { ...root, lifecycle_state: 'waiting_for_answer' }
    child = { ...child, lifecycle_state: 'done' }
    act(() => { useChatStore.getState().handleFrame(frame) })
    await waitFor(() => expect(rowText('Child conversation')).toBe('Child conversation Done'))
    expect(rowText('Parent conversation')).toBe('Parent conversation Waiting for answer3')
    expect(rootFetches).toBeGreaterThan(before.roots)
    expect(childFetches).toBeGreaterThan(before.children)
    expect(queryClient.getQueryData<SessionListPage>(['sessions', 'children', PARENT, 0])?.sessions[0].lifecycle_state).toBe('done')
    expect(useChatStore.getState().messages).toStrictEqual(foreground)
    expect(useChatStore.getState().isStreaming).toBe(true)
  })

  it('does not infer a parent Done from completed children and does not poll collapsed child pages', async () => {
    await mountSidebar(true)
    child = { ...child, lifecycle_state: 'done' }
    await tickRefresh()
    await waitFor(() => expect(rowText('Child conversation')).toBe('Child conversation Done'))
    expect(rowText('Parent conversation')).toBe('Parent conversation Working3')
    fireEvent.click(screen.getByLabelText('Collapse Parent conversation delegated sessions'))
    const before = childFetches
    await tickRefresh()
    expect(childFetches).toBe(before)
    expect(queryClient.getQueryData<Session[]>(['sessions'])?.[0].lifecycle_state).toBe('working')
  })
})

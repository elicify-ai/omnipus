import type { ReactNode } from 'react'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Session as WireSession, SessionPage } from '@/lib/api/generated/openapi-types'
import type { SubagentStartFrame, SubagentStateFrame } from '@/lib/api/generated/asyncapi-types'
import type { WsConnection } from '@/lib/ws'
import { queryClient } from '@/lib/queryClient'
import { codeToDisplay } from '@/lib/llm-error'
import { getMessages, useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { useSidebarStore } from '@/store/sidebar'
import { ActivityBar } from '@/components/chat/ActivityBar'
import { Sidebar } from './Sidebar'

// Oracle: founder decision 2026-10-07 (S4/S9). One Stop ends only the
// parent turn. The stopped parent must disclose its still-running direct
// helpers, using the same live source as the Agents number, then lose the
// qualifier at zero. Keep the existing stop cause and total-helper badge.
// Real components, query client, REST adapter, stores and frame routing;
// only HTTP/socket/router and incidental service calls are fake.
const { PARENT, CHILD, WORKSPACE } = vi.hoisted(() => ({
  PARENT: 'stopped-label-parent', CHILD: 'stopped-label-child', WORKSPACE: 'stopped-label-workspace',
}))
const sender = { send: vi.fn<WsConnection['send']>().mockReturnValue(true) }
vi.mock('@tanstack/react-router', () => ({
  useLocation: () => ({ pathname: '/' }),
  useNavigate: () => vi.fn(),
  Link: ({ children, to, search, ...props }: { children: ReactNode; to: string; search?: Record<string, string> }) => (
    <a href={search ? `${to}?${new URLSearchParams(search).toString()}` : to} {...props}>{children}</a>
  ),
}))
vi.mock('@/lib/api', async (original) => ({
  ...await original<typeof import('@/lib/api')>(),
  fetchWorkspaces: vi.fn().mockResolvedValue([{ id: WORKSPACE, name: 'Label workspace', status: 'active', pinned: false }]),
  fetchAgents: vi.fn().mockResolvedValue([]),
  fetchAppState: vi.fn().mockResolvedValue({ dev_mode_bypass: false, identity: { mode: 'platform' } }),
  fetchGodMode: vi.fn().mockResolvedValue({ enabled: false }),
}))

function wireRow(id: string): WireSession {
  return {
    id, agent_id: 'jim', title: id === PARENT ? 'Parent conversation' : 'Child conversation',
    type: id === PARENT ? 'chat' : 'delegate', status: 'active', lifecycle_state: 'stopped',
    stop_note: { at: '2026-10-07T00:00:00Z', by: 'human:user', seq: 1, cause: 'stop' },
    workspace_id: WORKSPACE, ...(id === CHILD ? { parent_session_id: PARENT } : {}),
    // Eight total historical helpers deliberately differs from the two live
    // helpers in the founder's example; child_count is not a running count.
    child_count: id === PARENT ? 8 : 0, channel: 'webchat', partitions: [],
    created_at: '2026-10-07T00:00:00Z', updated_at: '2026-10-07T00:00:00Z',
    stats: { tokens_in: 0, tokens_out: 0, tokens_total: 0, cost: 0, tool_calls: 0, message_count: 1 },
  }
}

let parent: WireSession
const originalOptions = queryClient.getDefaultOptions()
beforeEach(() => {
  queryClient.clear()
  queryClient.setDefaultOptions({ queries: { retry: false, gcTime: Infinity } })
  parent = wireRow(PARENT)
  sender.send.mockClear()
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
    const url = new URL(String(input), 'http://localhost')
    if (url.pathname !== '/api/v1/sessions') throw new Error(`Unexpected HTTP request: ${url}`)
    const body: SessionPage = {
      sessions: [structuredClone(url.searchParams.get('parent_session_id') === PARENT ? wireRow(CHILD) : parent)],
    }
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
  vi.unstubAllGlobals()
})

function helperState(child: string, state: SubagentStateFrame['state'], sessionId = PARENT) {
  const frame: SubagentStateFrame = {
    type: 'subagent_state', session_id: sessionId, child_session_id: child,
    span_id: `span-${child}`, state, created_at: new Date().toISOString(),
  }
  useChatStore.getState().handleFrame(frame)
}
function startHelper(child: string, state?: SubagentStateFrame['state'], sessionId = PARENT) {
  const frame: SubagentStartFrame = {
    type: 'subagent_start', session_id: sessionId, span_id: `span-${child}`,
    parent_call_id: `call-${child}`, child_session_id: child, task_label: `Work ${child}`,
  }
  useChatStore.getState().handleFrame(frame)
  if (state) helperState(child, state, sessionId)
}
async function mountSidebar(expandChildren = false) {
  render(<QueryClientProvider client={queryClient}><Sidebar /><ActivityBar /></QueryClientProvider>)
  await screen.findByText('Parent conversation')
  if (expandChildren) {
    fireEvent.click(screen.getByLabelText('Expand Parent conversation delegated sessions'))
    await screen.findByText('Child conversation')
  }
}
function rowText(title = 'Parent conversation') {
  return screen.getByText(title).closest('button')!.textContent
}

// The complete row includes the pre-existing total-helper badge, without a
// separating space. Expectations pin the status phrase as well as that badge.
describe('Stopped parent labels disclose only their own running helpers', () => {
  it('one Stop leaves two helpers running, then updates live to one and no qualifier at zero', async () => {
    parent = { ...parent, lifecycle_state: 'working', stop_note: undefined }
    await mountSidebar()
    act(() => {
      useChatStore.getState().sendMessage('Delegate work', { clientMessageId: 'parent-user' })
      useChatStore.getState().handleFrame({ type: 'token', session_id: PARENT, turn_id: 'parent-turn', message_id: 'parent-reply', content: 'Partial answer' })
      startHelper('helper-a', 'running')
      startHelper('helper-b', 'running')
    })
    expect(rowText()).toBe('Parent conversation Working8')
    expect(screen.getByTestId('activity-bar-label').textContent).toBe('2 running')
    sender.send.mockClear()
    parent = wireRow(PARENT)
    act(() => {
      expect(useChatStore.getState().cancelStream(PARENT, 'session')).toBe(true)
      useChatStore.getState().handleFrame({ type: 'error', session_id: PARENT, message: codeToDisplay.turn_canceled, payload: { llm_error: { code: 'turn_canceled', message: codeToDisplay.turn_canceled, retryable: true } } })
      useChatStore.getState().handleFrame({ type: 'done', session_id: PARENT, turn_id: 'parent-turn', stats: { tokens: 1, cost: 0 } })
    })
    expect(sender.send.mock.calls).toStrictEqual([[{ type: 'cancel', session_id: PARENT, scope: 'session' }]])
    await waitFor(() => expect(rowText()).toBe('Parent conversation Stopped · 2 helpers still running · stop8'))
    expect(screen.getByTestId('activity-bar-label').textContent).toBe('2 running')
    expect(useChatStore.getState().isStreaming).toBe(false)

    // Lifecycle completion arrives before the delegation end bracket.
    act(() => helperState('helper-a', 'completed'))
    expect(rowText()).toBe('Parent conversation Stopped · 1 helper still running · stop8')
    expect(screen.getByTestId('activity-bar-label').textContent).toBe('1 running')
    act(() => helperState('helper-b', 'completed'))
    expect(rowText()).toBe('Parent conversation Stopped · stop8')
    expect(screen.queryByText(/helpers? still running/)).not.toBeInTheDocument()
  })

  it('excludes queued, waiting, stopped, finished, unknown-lifecycle helpers and background commands', async () => {
    act(() => {
      startHelper('executing', 'running')
      startHelper('queued', 'queued')
      startHelper('waiting', 'needs_input')
      startHelper('parked', 'stopped')
      startHelper('completed', 'completed')
      startHelper('failed', 'failed')
      startHelper('unknown')
      useChatStore.getState().startToolCall('shell-call', 'bash', { command: 'sleep 30', run_in_background: true })
      useChatStore.getState().resolveToolCall('shell-call', JSON.stringify({ sessionId: 'background-shell', status: 'running' }), 'success')
    })
    await mountSidebar()
    expect(rowText()).toBe('Parent conversation Stopped · 1 helper still running · stop8')
    expect(screen.getByTestId('activity-bar-label').textContent).toBe('1 running')
    expect(screen.getByTestId('activity-pill-commands-label').textContent).toBe('1 background command')
    act(() => helperState('executing', 'completed'))
    expect(rowText()).toBe('Parent conversation Stopped · stop8')
    expect(screen.getByTestId('activity-pill-commands-label').textContent).toBe('1 background command')
  })

  it('keeps a background parent scoped to its own helpers, excluding another chat and a grandchild', async () => {
    act(() => {
      startHelper('helper-a', 'running')
      startHelper('helper-b', 'running')
      startHelper('grandchild', 'running', 'helper-a')
    })
    await mountSidebar()
    expect(rowText()).toBe('Parent conversation Stopped · 2 helpers still running · stop8')
    act(() => {
      useSessionStore.getState().setActiveSession('unrelated-chat', 'jim')
      useChatStore.getState().sendMessage('Unrelated work', { clientMessageId: 'unrelated-user' })
      useChatStore.getState().handleFrame({ type: 'token', session_id: 'unrelated-chat', content: 'Unrelated reply' })
      startHelper('foreign-helper', 'running', 'unrelated-chat')
    })
    const foreground = structuredClone(useChatStore.getState().messages)
    expect(screen.getByTestId('activity-bar-label').textContent).toBe('1 running')
    expect(rowText()).toBe('Parent conversation Stopped · 2 helpers still running · stop8')
    act(() => helperState('helper-a', 'completed'))
    expect(rowText()).toBe('Parent conversation Stopped · 1 helper still running · stop8')
    act(() => helperState('helper-b', 'completed'))
    expect(rowText()).toBe('Parent conversation Stopped · stop8')
    expect(useChatStore.getState().messages).toStrictEqual(foreground)
  })

  it('does not copy a parent running count onto a stopped child row', async () => {
    act(() => { startHelper('helper-a', 'running'); startHelper('helper-b', 'running') })
    await mountSidebar(true)
    expect(rowText()).toBe('Parent conversation Stopped · 2 helpers still running · stop8')
    expect(rowText('Child conversation')).toBe('Child conversation Stopped · stop')
  })

  it.each([
    ['working', 'Working'], ['waiting_for_answer', 'Waiting for answer'],
    ['done', 'Done'], ['failed', 'Failed'], ['interrupted', 'Interrupted'],
    [undefined, ''],
  ] as const)('keeps the %s parent label unchanged even with two running helpers', async (lifecycle, label) => {
    parent = { ...parent, lifecycle_state: lifecycle, stop_note: undefined }
    act(() => { startHelper('helper-a', 'running'); startHelper('helper-b', 'running') })
    await mountSidebar()
    expect(rowText()).toBe(`Parent conversation${label ? ` ${label}` : ''}8`)
    expect(screen.getByTestId('activity-bar-label').textContent).toBe('2 running')
  })

  it('does not invent a running count from child_count when there is no cached transcript', async () => {
    await mountSidebar()
    expect(useChatStore.getState().sessionsById[PARENT]).toBeUndefined()
    expect(rowText()).toBe('Parent conversation Stopped · stop8')
  })

  it('reads running helpers from earlier parent messages, not just the final stopped notice', async () => {
    act(() => {
      useChatStore.getState().sendMessage('Delegate work', { clientMessageId: 'earlier-user' })
      useChatStore.getState().handleFrame({ type: 'token', session_id: PARENT, content: 'Delegating' })
      startHelper('helper-a', 'running')
      useChatStore.getState().handleFrame({ type: 'done', session_id: PARENT, stats: { tokens: 1, cost: 0 } })
      useChatStore.getState().sendMessage('Follow up', { clientMessageId: 'later-user' })
      useChatStore.getState().handleFrame({ type: 'token', session_id: PARENT, content: 'Later turn' })
      useChatStore.getState().cancelStream(PARENT, 'session')
      useChatStore.getState().handleFrame({ type: 'done', session_id: PARENT, stats: { tokens: 1, cost: 0 } })
    })
    const messages = getMessages(useChatStore.getState().sessionsById[PARENT])
    expect(messages.at(-1)?.spans).toBeUndefined()
    await mountSidebar()
    expect(rowText()).toBe('Parent conversation Stopped · 1 helper still running · stop8')
    expect(screen.getByTestId('activity-bar-label').textContent).toBe('1 running')
  })
})

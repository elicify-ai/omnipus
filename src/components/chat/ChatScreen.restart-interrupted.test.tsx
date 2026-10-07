import type { ReactNode } from 'react'
import { act, cleanup, render, screen, waitFor } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Session as WireSession, SessionPage } from '@/lib/api/generated/openapi-types'
import type { WsConnection } from '@/lib/ws'
import { queryClient } from '@/lib/queryClient'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { ChatScreen } from './ChatScreen'
import { codeToDisplay } from '@/lib/llm-error'
import { KEEP_GENERATE_AGAIN_WHEN_RESTART_INTERRUPTED } from './ConnectionStatus'

// I1 (UAT): after kill -9 + restart a chat whose turn was cut off must read
// "Interrupted" in the chat body (the sidebar already does — see
// Sidebar.lifecycle-labels.test.tsx), never "Working", on a fresh tab, after a
// reload and on a live tab that saw the boot change. Oracle: the real gateway
// projection pinned by pkg/gateway/i1_restart_rest_projection_test.go
// (TestI1RestartRESTProjection, case "restart"): GET /api/v1/sessions returns
// status "active", lifecycle_state "interrupted" and a stop_note with
// cause "restart", by "restart". Real stores, frame routing, list adapter and
// ChatScreen; only HTTP/socket/router and incidental pickers are stand-ins.
vi.mock('@assistant-ui/react', async () => (await import('@/test/assistantUiMock')).createAssistantUiMock())
vi.mock('@tanstack/react-router', () => ({
  useRouter: () => ({ navigate: vi.fn() }), useNavigate: () => vi.fn(),
  useSearch: () => ({}), Link: ({ children }: { children: ReactNode }) => children,
}))
vi.mock('@/lib/api', async (original) => ({
  ...await original<typeof import('@/lib/api')>(),
  fetchAgents: vi.fn().mockResolvedValue([]), fetchSessionMessages: vi.fn().mockResolvedValue([]),
  fetchAboutInfo: vi.fn().mockResolvedValue({ preview_port: 5001 }), fetchProviders: vi.fn().mockResolvedValue([]),
  fetchCommands: vi.fn().mockResolvedValue([]), fetchSkills: vi.fn().mockResolvedValue([]),
}))
vi.mock('./composer/AgentPicker', () => ({ AgentPicker: () => null }))
vi.mock('./composer/ModelPicker', () => ({ ModelPicker: () => null }))
vi.mock('./composer/TokenCounter', () => ({ TokenCounter: () => null }))
vi.mock('@/lib/memory-observer', () => ({
  startMemoryObserver: () => ({ dispose: vi.fn(), getCurrentSnapshot: vi.fn() }),
  addMemoryObserver: () => () => {}, getCurrentSnapshot: () => ({ usedJSHeapSizeBytes: null, level: 'ok', supported: false }),
}))

const SID = 'i1-restart-cut-chat'
const sender = { send: vi.fn<WsConnection['send']>().mockReturnValue(true) }
const originalOptions = queryClient.getDefaultOptions()
let savedLifecycle: WireSession['lifecycle_state']
let stopCause: 'restart' | 'stop' | undefined
let listFails = false

function wireRow(lifecycle: WireSession['lifecycle_state']): WireSession {
  const restartStop = lifecycle === 'interrupted' || stopCause === 'restart'
  const humanStop = stopCause === 'stop'
  return {
    id: SID, agent_id: 'jim', title: 'Restart cut chat', type: 'chat', status: 'active',
    lifecycle_state: lifecycle, channel: 'webchat', partitions: [],
    ...(restartStop ? { stop_note: { at: '2026-10-07T06:00:00Z', by: 'restart', seq: 1, cause: 'restart' as const, boot_seq: 1 } } : {}),
    ...(humanStop ? { stop_note: { at: '2026-10-07T06:00:00Z', by: 'human:owner', seq: 1, cause: 'stop' as const } } : {}),
    created_at: '2026-10-07T05:00:00Z', updated_at: '2026-10-07T06:00:00Z',
    stats: { tokens_in: 0, tokens_out: 0, tokens_total: 0, cost: 0, tool_calls: 0, message_count: 2 },
  }
}

beforeEach(() => {
  vi.stubGlobal('ResizeObserver', undefined)
  queryClient.clear()
  queryClient.setDefaultOptions({ queries: { retry: false, gcTime: Infinity } })
  sender.send.mockClear()
  savedLifecycle = 'interrupted'
  stopCause = undefined
  listFails = false
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
    const url = new URL(String(input), 'http://localhost')
    if (url.pathname !== '/api/v1/sessions') throw new Error(`Unexpected HTTP request: ${url}`)
    if (listFails) return new Response('boom', { status: 500 })
    const body: SessionPage = { sessions: [wireRow(savedLifecycle)] }
    return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })
  }))
  act(() => {
    useSessionStore.setState(useSessionStore.getInitialState(), true)
    useChatStore.setState(useChatStore.getInitialState(), true)
    useConnectionStore.setState(useConnectionStore.getInitialState(), true)
    useConnectionStore.getState().setConnection(sender as unknown as WsConnection)
    useConnectionStore.getState().setConnected(true)
    useSessionStore.getState().setActiveSession(SID, 'jim')
  })
})
afterEach(() => {
  cleanup(); queryClient.clear(); queryClient.setDefaultOptions(originalOptions); vi.unstubAllGlobals()
})

type Frame = Parameters<ReturnType<typeof useChatStore.getState>['handleFrame']>[0]
function feed(frames: Frame[]) {
  act(() => { for (const frame of frames) useChatStore.getState().handleFrame(frame) })
}

// The attach a gateway sends a tab opened after the restart: a snapshot (no
// boot mismatch is observed by a tab that never saw the old boot), an idle
// session_state (the restart Stop cleared the turn), the persisted transcript
// (user prompt + the partial answer that was saved), then catch_up_complete.
function freshTabAttach(reason: 'unknown_position' | 'boot_mismatch' = 'unknown_position'): Frame[] {
  return [
    { type: 'session_snapshot', session_id: SID, seq: 4, boot_id: 'boot-2', reason },
    { type: 'session_state', session_id: SID, user_id: 'u1', pending_approvals: [], emitted_at: '2026-10-07T06:01:00Z' },
    { type: 'replay_message', session_id: SID, id: 'u-1', role: 'user', content: 'Write the long report' },
    { type: 'replay_message', session_id: SID, id: 'a-1', role: 'assistant', content: 'Partial text before the kill', turn_id: 't-1' },
    { type: 'catch_up_complete', session_id: SID, seq: 4, boot_id: 'boot-2', mode: 'snapshot' },
  ]
}

async function mount() {
  await act(async () => { render(<QueryClientProvider client={queryClient}><ChatScreen /></QueryClientProvider>) })
}

describe('I1 — a restart-cut chat reads Interrupted in the chat body, never Working', () => {
  it('fresh tab: saved lifecycle_state "interrupted" shows Interrupted, keeps the partial text, nothing auto-resumes', async () => {
    feed(freshTabAttach())
    await mount()
    const notice = await screen.findByTestId('restart-interrupted-notice')
    expect(notice).toHaveTextContent('Interrupted')
    expect(screen.getByText('Partial text before the kill')).toBeInTheDocument()
    expect(screen.queryByText(/working/i)).not.toBeInTheDocument()
    expect(useChatStore.getState().isStreaming).toBe(false)
    expect(useChatStore.getState().isReplaying).toBe(false)
    expect(sender.send).not.toHaveBeenCalledWith(expect.objectContaining({ type: 'message' }))
  })

  it('reload: tearing everything down and attaching again shows the same Interrupted notice', async () => {
    feed(freshTabAttach())
    await mount()
    await screen.findByTestId('restart-interrupted-notice')
    cleanup(); queryClient.clear()
    act(() => {
      useChatStore.setState(useChatStore.getInitialState(), true)
      useSessionStore.getState().setActiveSession(SID, 'jim')
    })
    feed(freshTabAttach())
    await mount()
    expect(await screen.findByTestId('restart-interrupted-notice')).toHaveTextContent('Interrupted')
    expect(screen.getByText('Partial text before the kill')).toBeInTheDocument()
    expect(screen.queryByText(/working/i)).not.toBeInTheDocument()
  })

  it('live tab that saw the boot change: Interrupted replaces "couldn\'t be finished", no Working', async () => {
    savedLifecycle = 'working'
    await mount()
    act(() => {
      useChatStore.getState().sendMessage('Write the long report', { clientMessageId: 'u-1' })
      useChatStore.getState().handleFrame({ type: 'token', session_id: SID, turn_id: 't-1', message_id: 'a-1', content: 'Partial text before the kill' })
    })
    await waitFor(() => expect(useChatStore.getState().isStreaming).toBe(true))
    // The gateway is killed and restarted: the saved record now lands as interrupted.
    savedLifecycle = 'interrupted'
    feed(freshTabAttach('boot_mismatch'))
    expect(await screen.findByTestId('restart-interrupted-notice')).toHaveTextContent('Interrupted')
    expect(screen.queryByText(/couldn't be finished/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/working/i)).not.toBeInTheDocument()
    expect(useChatStore.getState().isStreaming).toBe(false)
  })

  it('control: a genuinely running session (lifecycle working, active turn) is not shown as Interrupted', async () => {
    savedLifecycle = 'working'
    feed([
      { type: 'session_snapshot', session_id: SID, seq: 4, boot_id: 'boot-2', reason: 'unknown_position' },
      { type: 'session_state', session_id: SID, user_id: 'u1', pending_approvals: [], emitted_at: '2026-10-07T06:01:00Z', active_turn: { turn_id: 't-9', agent_id: 'jim', started_at: '2026-10-07T06:00:30Z' } },
      { type: 'replay_message', session_id: SID, id: 'u-1', role: 'user', content: 'Write the long report' },
      { type: 'catch_up_complete', session_id: SID, seq: 4, boot_id: 'boot-2', mode: 'snapshot' },
    ])
    await mount()
    await waitFor(() => expect(queryClient.getQueryState(['sessions'])?.status).toBe('success'))
    expect(useChatStore.getState().isStreaming).toBe(true)
    expect(screen.queryByTestId('restart-interrupted-notice')).not.toBeInTheDocument()
    expect(screen.queryByText(/interrupted/i)).not.toBeInTheDocument()
  })

  it('the next message continues the same chat: the notice goes away and the message is sent on the same session', async () => {
    feed(freshTabAttach())
    await mount()
    await screen.findByTestId('restart-interrupted-notice')
    savedLifecycle = 'working'
    act(() => { useChatStore.getState().sendMessage('Please continue', { clientMessageId: 'u-2' }) })
    await waitFor(() => expect(screen.queryByTestId('restart-interrupted-notice')).not.toBeInTheDocument())
    expect(useSessionStore.getState().activeSessionId).toBe(SID)
    expect(sender.send).toHaveBeenCalledWith(expect.objectContaining({ type: 'message', session_id: SID, content: 'Please continue' }))
    expect(screen.getByText('Partial text before the kill')).toBeInTheDocument()
  })

  it('stale list: the turn ends while the list still says interrupted — the notice does not come back', async () => {
    feed(freshTabAttach())
    await mount()
    await screen.findByTestId('restart-interrupted-notice')
    // savedLifecycle stays 'interrupted': the server has not committed the new state yet.
    const before = vi.mocked(fetch).mock.calls.length
    act(() => { useChatStore.getState().sendMessage('Please continue', { clientMessageId: 'u-2' }) })
    await waitFor(() => expect(screen.queryByTestId('restart-interrupted-notice')).not.toBeInTheDocument())
    feed([
      { type: 'token', session_id: SID, turn_id: 't-2', message_id: 'a-2', content: 'Continued answer' },
      { type: 'done', session_id: SID, turn_id: 't-2', stats: { tokens: 1, cost: 0 } },
    ])
    // done invalidated the list; wait for that refetch (which still says interrupted) to land.
    await waitFor(() => expect(vi.mocked(fetch).mock.calls.length).toBeGreaterThan(before))
    await waitFor(() => expect(queryClient.isFetching({ queryKey: ['sessions'] })).toBe(0))
    expect(useChatStore.getState().isStreaming).toBe(false)
    expect(screen.getByText('Continued answer')).toBeInTheDocument()
    expect(screen.queryByTestId('restart-interrupted-notice')).not.toBeInTheDocument()
  })

  it('a later, second restart shows Interrupted again once the saved value was seen at something else', async () => {
    feed(freshTabAttach())
    await mount()
    await screen.findByTestId('restart-interrupted-notice')
    act(() => { useChatStore.getState().sendMessage('Please continue', { clientMessageId: 'u-2' }) })
    await waitFor(() => expect(useChatStore.getState().sessionsById[SID]?.restartNoticeDismissed).toBe(true))
    savedLifecycle = 'done'
    feed([
      { type: 'token', session_id: SID, turn_id: 't-2', message_id: 'a-2', content: 'Continued answer' },
      { type: 'done', session_id: SID, turn_id: 't-2', stats: { tokens: 1, cost: 0 } },
    ])
    await waitFor(() => expect(useChatStore.getState().sessionsById[SID]?.restartNoticeDismissed).toBe(false))
    expect(screen.queryByTestId('restart-interrupted-notice')).not.toBeInTheDocument()
    savedLifecycle = 'interrupted'
    await act(async () => { await queryClient.invalidateQueries({ queryKey: ['sessions'] }) })
    expect(await screen.findByTestId('restart-interrupted-notice')).toHaveTextContent('Interrupted')
  })

  it('failed continuation: the provider error shows, and the stale interrupted list does not hide or replace it', async () => {
    feed(freshTabAttach())
    await mount()
    await screen.findByTestId('restart-interrupted-notice')
    act(() => { useChatStore.getState().sendMessage('Please continue', { clientMessageId: 'u-2' }) })
    feed([
      { type: 'error', session_id: SID, message: codeToDisplay.provider_rejected, payload: { llm_error: { code: 'provider_rejected', message: codeToDisplay.provider_rejected, retryable: true } } },
      { type: 'done', session_id: SID, turn_id: 't-2', stats: { tokens: 0, cost: 0 } },
    ])
    await act(async () => { await Promise.resolve() })
    expect(useChatStore.getState().messages.at(-1)).toMatchObject({ role: 'assistant', status: 'error' })
    expect(screen.getAllByText(codeToDisplay.provider_rejected).length).toBeGreaterThan(0)
    expect(screen.queryByTestId('restart-interrupted-notice')).not.toBeInTheDocument()
  })

  it('saved interrupted but a turn is active for the chat: no notice', async () => {
    feed([
      { type: 'session_snapshot', session_id: SID, seq: 4, boot_id: 'boot-2', reason: 'unknown_position' },
      { type: 'session_state', session_id: SID, user_id: 'u1', pending_approvals: [], emitted_at: '2026-10-07T06:01:00Z', active_turn: { turn_id: 't-9', agent_id: 'jim', started_at: '2026-10-07T06:00:30Z' } },
      { type: 'catch_up_complete', session_id: SID, seq: 4, boot_id: 'boot-2', mode: 'snapshot' },
    ])
    await mount()
    await waitFor(() => expect(queryClient.getQueryState(['sessions'])?.status).toBe('success'))
    expect(useChatStore.getState().sessionsById[SID]?.activeTurnId).toBe('t-9')
    expect(screen.queryByTestId('restart-interrupted-notice')).not.toBeInTheDocument()
  })

  it('degraded: GET /sessions 500 — no crash, no notice, no Working, the saved text stays', async () => {
    listFails = true
    feed(freshTabAttach())
    await mount()
    await waitFor(() => expect(queryClient.getQueryState(['sessions'])?.status).toBe('error'))
    expect(screen.getByText('Partial text before the kill')).toBeInTheDocument()
    expect(screen.queryByTestId('restart-interrupted-notice')).not.toBeInTheDocument()
    expect(screen.queryByText(/working/i)).not.toBeInTheDocument()
  })

  it('degraded live tab: list unavailable falls back to the boot-mismatch wording, still never Working', async () => {
    listFails = true
    await mount()
    act(() => {
      useChatStore.getState().sendMessage('Write the long report', { clientMessageId: 'u-1' })
      useChatStore.getState().handleFrame({ type: 'token', session_id: SID, turn_id: 't-1', message_id: 'a-1', content: 'Partial text before the kill' })
    })
    feed(freshTabAttach('boot_mismatch'))
    expect((await screen.findAllByText(/couldn't be finished/i)).length).toBeGreaterThan(0)
    expect(screen.queryByTestId('restart-interrupted-notice')).not.toBeInTheDocument()
    expect(screen.queryByText(/working/i)).not.toBeInTheDocument()
  })

  // Oracle: pkg/gateway/i1_restart_rest_projection_test.go cases human_stop and genuine_failure.
  it.each([
    ['human_stop', 'stopped', 'stop'],
    ['genuine_failure', 'failed', undefined],
  ] as const)('%s lifecycle is not shown as Interrupted', async (_name, lifecycle, cause) => {
    savedLifecycle = lifecycle
    stopCause = cause
    feed(freshTabAttach())
    await mount()
    await waitFor(() => expect(queryClient.getQueryState(['sessions'])?.status).toBe('success'))
    expect(screen.queryByTestId('restart-interrupted-notice')).not.toBeInTheDocument()
    expect(screen.queryByText(/interrupted/i)).not.toBeInTheDocument()
  })

  it('restart-cut question with no answer: Interrupted stands alone — the manual Generate again line is off (pending founder Q1, one-line switch)', async () => {
    feed([
      { type: 'session_snapshot', session_id: SID, seq: 4, boot_id: 'boot-2', reason: 'boot_mismatch' },
      { type: 'session_state', session_id: SID, user_id: 'u1', pending_approvals: [], emitted_at: '2026-10-07T06:01:00Z' },
      { type: 'replay_message', session_id: SID, id: 'u-1', role: 'user', content: 'Write the long report' },
      { type: 'catch_up_complete', session_id: SID, seq: 4, boot_id: 'boot-2', mode: 'snapshot' },
    ])
    expect(useChatStore.getState().sessionsById[SID]?.unansweredLastUserMessageId).toBe('u-1')
    await mount()
    expect(await screen.findByTestId('restart-interrupted-notice')).toHaveTextContent('Interrupted')
    expect(KEEP_GENERATE_AGAIN_WHEN_RESTART_INTERRUPTED).toBe(false)
    expect(screen.queryByText(/Generate again/)).not.toBeInTheDocument()
    expect(sender.send).not.toHaveBeenCalledWith(expect.objectContaining({ type: 'message' }))
  })

  it('without a restart-interrupted saved state the same unanswered question still offers Generate again (suppression is scoped)', async () => {
    savedLifecycle = 'done'
    feed([
      { type: 'session_snapshot', session_id: SID, seq: 4, boot_id: 'boot-2', reason: 'boot_mismatch' },
      { type: 'session_state', session_id: SID, user_id: 'u1', pending_approvals: [], emitted_at: '2026-10-07T06:01:00Z' },
      { type: 'replay_message', session_id: SID, id: 'u-1', role: 'user', content: 'Write the long report' },
      { type: 'catch_up_complete', session_id: SID, seq: 4, boot_id: 'boot-2', mode: 'snapshot' },
    ])
    await mount()
    await waitFor(() => expect(queryClient.getQueryState(['sessions'])?.status).toBe('success'))
    expect(screen.getByText(/Generate again/)).toBeInTheDocument()
    expect(screen.queryByTestId('restart-interrupted-notice')).not.toBeInTheDocument()
  })
})

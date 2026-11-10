import type { ReactNode } from 'react'
import { act, cleanup, render, screen } from '@testing-library/react'
import { QueryClientProvider, QueryObserver } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { parseFrameSafe, type ServerFrame } from '@/lib/ws'
import type { ClientFrame } from '@/lib/api/generated/asyncapi-types'
import type { Session } from '@/lib/api/generated/openapi-types'
import { queryClient } from '@/lib/queryClient'
import { useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { OmnipusRuntimeProvider } from './OmnipusRuntimeProvider'
import { ChatConnectionNotice } from './ConnectionStatus'
import { fixtureHistoryPosition, wireHistoryPage, wireHistorySnapshot } from '../../../tests/chat-history-paging/legacy-wire'

// Founder decision 2026-10-06 (keeps issue #823): connection drops shorter
// than 15 s stay quiet - no status line; the earlier U4 requirement of an
// immediate "Reconnecting…" hint is withdrawn. 4008 stays quiet too. REAL WsConnection, runtime callback wiring, stores,
// frame validation/routing and notice. Fake only WebSocket/clock and the
// external AssistantUI context (not the runtime adapter or callbacks).
vi.mock('@assistant-ui/react', async () => ({
  ...(await import('@/test/assistantUiMock')).createAssistantUiMock(),
  useExternalStoreRuntime: () => ({}),
  AssistantRuntimeProvider: ({ children }: { children: ReactNode }) => children,
}))
const SID = 'uat-reconnect-session'
// Both reconnect snapshots select one saved narration source; controls and the
// pre-drop live token do not become additional historical groups (D3/D4).
function reconnectHistory(id: string, content: string, seq: number, turnId?: string) {
  const position = fixtureHistoryPosition(SID, id)
  const source = { id, type: 'message', role: 'assistant', agent_id: 'jim', content,
    timestamp: '2026-10-06T00:00:00Z', history_position: position, ...(turnId ? { turn_id: turnId } : {}) }
  const replay: ServerFrame = { type: 'replay_message', session_id: SID, id, role: 'assistant', content,
    ...(turnId ? { turn_id: turnId } : {}), history_origin: { position, entry_id: id } }
  return { replay, snapshot: wireHistorySnapshot(wireHistoryPage(SID, [source], 1), {
    seq, boot_id: 'uat-after', reason: 'boot_mismatch',
  }, [replay]) }
}
let sockets: Socket[]
class Socket {
  static OPEN = 1
  static CLOSED = 3
  readyState = Socket.OPEN
  onopen: (() => void) | null = null
  onmessage: ((event: { data: string }) => void) | null = null
  onclose: ((event: { code: number; reason: string }) => void) | null = null
  onerror: (() => void) | null = null
  send = vi.fn()
  close = vi.fn()
  constructor() { sockets.push(this) }
  open() { this.onopen?.() }
  drop(code: number) { this.readyState = Socket.CLOSED; this.onclose?.({ code, reason: '' }) }
  frame(frame: ServerFrame) {
    expect(this.onmessage, 'fixture socket has the real onmessage handler').toBeTypeOf('function')
    expect(parseFrameSafe(JSON.stringify(frame)), 'fixture frame satisfies the generated contract').toMatchObject({ type: frame.type })
    this.onmessage!({ data: JSON.stringify(frame) })
  }
}
function latest() { return sockets[sockets.length - 1] }
async function flushFrames() { await act(async () => { await vi.advanceTimersByTimeAsync(50) }) }

beforeEach(() => {
  vi.useFakeTimers({ toFake: ['Date', 'setTimeout', 'clearTimeout', 'setInterval', 'clearInterval', 'requestAnimationFrame', 'cancelAnimationFrame'] })
  vi.setSystemTime(new Date('2026-10-06T00:00:00Z'))
  sockets = []
  vi.stubGlobal('WebSocket', Socket)
  queryClient.clear()
  act(() => {
    useSessionStore.setState(useSessionStore.getInitialState(), true)
    useChatStore.setState(useChatStore.getInitialState(), true)
    useConnectionStore.setState(useConnectionStore.getInitialState(), true)
    useSessionStore.getState().setActiveSession(SID, 'jim')
  })
})
afterEach(() => { cleanup(); queryClient.clear(); vi.useRealTimers(); vi.unstubAllGlobals() })

async function connect(streaming = false) {
  await act(async () => {
    render(<QueryClientProvider client={queryClient}><OmnipusRuntimeProvider><ChatConnectionNotice /></OmnipusRuntimeProvider></QueryClientProvider>)
    await import('./tools/DraftLink')
  })
  act(() => latest().open())
  act(() => latest().frame({ type: 'catch_up_complete', session_id: SID, seq: 1, boot_id: 'uat-before', mode: 'snapshot' }))
  await flushFrames()
  expect(useConnectionStore.getState().isConnected).toBe(true)
  expect(useChatStore.getState().sessionsById[SID]?.cursor).toStrictEqual({ bootId: 'uat-before', seq: 1 })
  if (streaming) {
    act(() => {
      useChatStore.getState().sendMessage('Work on this', { clientMessageId: 'uat-reconnect-user' })
      latest().frame({ type: 'token', session_id: SID, turn_id: 'uat-active-turn', content: 'Partial reply' })
    })
    await flushFrames()
    expect(useChatStore.getState().isStreaming).toBe(true)
    expect(useChatStore.getState().messages.filter((message) => message.role === 'assistant').map((message) => message.content)).toStrictEqual(['Partial reply'])
  }
  expect(screen.queryByTestId('connection-status-line')).not.toBeInTheDocument()
}

describe('U4 — reconnect behaviour through real runtime/socket callbacks', () => {
  it.each([false, true])('keeps a short ordinary outage quiet (streaming=%s), without a connection-error banner', async (streaming) => {
    await connect(streaming)
    act(() => latest().drop(1006))
    expect(useConnectionStore.getState().isConnected).toBe(false)
    expect(useConnectionStore.getState().reconnectPhase).toBe('reconnecting')
    expect(useConnectionStore.getState().connectionError).toBeNull()
    // Founder decision 2026-10-06: drops under 15 s show no status line (#823).
    expect(screen.queryByTestId('connection-status-line')).not.toBeInTheDocument()
    expect(screen.queryByText(/agents keep working/i)).not.toBeInTheDocument()
  })

  it('stays quiet through a short reconnect, completes real catch-up, preserves an active turn and never auto-resends', async () => {
    await connect(true)
    const sentMessages = () => sockets.flatMap((socket) => socket.send.mock.calls.map(([raw]) => JSON.parse(raw) as ClientFrame)).filter((frame) => frame.type === 'message')
    expect(sentMessages()).toHaveLength(1)
    act(() => latest().drop(1006))
    await act(async () => { await vi.advanceTimersByTimeAsync(1_000) })
    expect(sockets).toHaveLength(2)
    act(() => latest().open())
    expect(screen.queryByTestId('connection-status-line')).not.toBeInTheDocument()
    const history = reconnectHistory('replayed-active-reply', 'Partial reply', 1, 'uat-active-turn')
    act(() => {
      latest().frame(history.snapshot)
      latest().frame({ type: 'session_state', session_id: SID, user_id: 'uat-user', pending_approvals: [], emitted_at: '2026-10-06T00:00:01Z', boot_id: 'uat-after', history_state: { status: 'available' }, active_turn: { turn_id: 'uat-active-turn', agent_id: 'jim', started_at: '2026-10-06T00:00:00Z' } })
      latest().frame(history.replay)
    })
    await flushFrames()
    expect(useConnectionStore.getState().isConnected).toBe(true)
    expect(useChatStore.getState().sessionsById[SID].awaitingCatchUp).toBe(true)
    expect(screen.queryByTestId('connection-status-line')).not.toBeInTheDocument()
    act(() => latest().frame({ type: 'catch_up_complete', session_id: SID, seq: 1, boot_id: 'uat-after', mode: 'snapshot' }))
    await flushFrames()
    const bucket = useChatStore.getState().sessionsById[SID]
    expect(bucket.awaitingCatchUp).toBe(false)
    expect(bucket.cursor).toStrictEqual({ bootId: 'uat-after', seq: 1 })
    expect(bucket.activeTurnId).toBe('uat-active-turn')
    expect(bucket.messagesById['replayed-active-reply'].confirmedUnfinished).not.toBe(true)
    expect(screen.queryByTestId('connection-status-line')).not.toBeInTheDocument()
    expect(useConnectionStore.getState().connectionError).toBeNull()
    expect(sentMessages()).toHaveLength(1)
  })

  it('refreshes lifecycle metadata before a held catch-up boundary without publishing partial history', async () => {
    await connect()
    let serverLifecycle: Session['status'] = 'active'
    const lifecycle = new QueryObserver(queryClient, {
      queryKey: ['sessions'],
      // Network boundary only: the production cache and lifecycle observer stay real.
      queryFn: async (): Promise<Session[]> => [{
        id: SID, type: 'main', agent_id: 'jim', title: 'Jim main', status: serverLifecycle,
        channel: 'web', partitions: [], created_at: '2026-10-06T00:00:00Z', updated_at: '2026-10-06T00:00:01Z',
        stats: { tokens_in: 0, tokens_out: 0, tokens_total: 0, cost: 0, tool_calls: 0, message_count: 0 },
      }],
      staleTime: Infinity,
    })
    const unsubscribe = lifecycle.subscribe(() => {})
    try {
      await flushFrames()
      expect(lifecycle.getCurrentResult().data?.map(session => session.status)).toEqual(['active'])
      serverLifecycle = 'interrupted'
      act(() => latest().drop(1006))
      await act(async () => { await vi.advanceTimersByTimeAsync(1000) })
      act(() => latest().open())
      const history = reconnectHistory('held-history', 'Saved partial answer', 2)
      act(() => {
        latest().frame(history.snapshot)
        latest().frame(history.replay)
        latest().frame({ type: 'done', session_id: SID, stats: { frames_emitted: 1 } })
      })
      await flushFrames()
      // Lifecycle-first ordering is a supported product path: it must not
      // depend on history publication or a boundary that can be held back.
      expect(lifecycle.getCurrentResult().data?.map(session => session.status)).toEqual(['interrupted'])
      expect(useChatStore.getState().messages).toEqual([])
      act(() => latest().frame({ type: 'catch_up_complete', session_id: SID, seq: 2, boot_id: 'uat-after', mode: 'snapshot' }))
      await flushFrames()
      expect(useChatStore.getState().messages.map(message => message.content)).toEqual(['Saved partial answer'])
      expect(useChatStore.getState().sessionsById[SID].awaitingCatchUp).toBe(false)
    } finally {
      unsubscribe()
    }
  })

  it('keeps the special queue catch-up close 4008 quiet, before and after its immediate reconnect', async () => {
    await connect()
    act(() => latest().drop(4008))
    expect(sockets).toHaveLength(2)
    expect(useConnectionStore.getState().reconnectPhase).toBeNull()
    expect(screen.queryByTestId('connection-status-line')).not.toBeInTheDocument()
    act(() => latest().open())
    act(() => latest().frame({ type: 'catch_up_complete', session_id: SID, seq: 1, boot_id: 'uat-before', mode: 'incremental' }))
    await flushFrames()
    expect(screen.queryByTestId('connection-status-line')).not.toBeInTheDocument()
    expect(useConnectionStore.getState().connectionError).toBeNull()
  })
})

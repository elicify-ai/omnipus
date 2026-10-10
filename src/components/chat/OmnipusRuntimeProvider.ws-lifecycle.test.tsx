/**
 * Regression contract: CHECK2 WS race brief / preview RCA (2026-10-08).
 * A retired effect connection cannot mark its replacement disconnected or
 * clear the replacement's streaming state. Current disconnect/reconnect
 * behavior must still work. REAL runtime lifecycle, WsConnection and stores;
 * fake only the browser socket/clock and the external AssistantUI context.
 */
import { StrictMode, type ReactNode } from 'react'
import { act, cleanup, render } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ClientFrame, ServerFrame } from '@/lib/api/generated/asyncapi-types'
import { parseFrameSafe } from '@/lib/ws'
import { queryClient } from '@/lib/queryClient'
import { useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { OmnipusRuntimeProvider } from './OmnipusRuntimeProvider'

vi.mock('@assistant-ui/react', async () => ({
  ...(await import('@/test/assistantUiMock')).createAssistantUiMock(),
  useExternalStoreRuntime: () => ({}),
  AssistantRuntimeProvider: ({ children }: { children: ReactNode }) => children,
}))

const SID = 'lifetime-session'
const BOOT = 'lifetime-boot'
let sockets: DeferredSocket[]

class DeferredSocket {
  static OPEN = 1
  static CLOSED = 3
  readyState = 0
  onopen: ((event: Event) => void) | null = null
  onmessage: ((event: MessageEvent) => void) | null = null
  onclose: ((event: CloseEvent) => void) | null = null
  onerror: ((event: Event) => void) | null = null
  private pendingClose: CloseEvent | null = null
  send = vi.fn<(data: string) => void>()
  // Browser close events are asynchronous. Keep the handler installed by the
  // REAL WsConnection, but deliver its event only when the test requests it.
  close = vi.fn((code = 1000, reason = '') => {
    this.readyState = DeferredSocket.CLOSED
    this.pendingClose = new CloseEvent('close', { code, reason, wasClean: true })
  })

  constructor() { sockets.push(this) }

  open() {
    this.readyState = DeferredSocket.OPEN
    this.onopen?.(new Event('open'))
  }

  deliverClose() {
    expect(this.pendingClose, 'effect cleanup queued an intentional close').not.toBeNull()
    expect(this.onclose, 'real WsConnection installed the close callback').toBeTypeOf('function')
    this.onclose!(this.pendingClose!)
    this.pendingClose = null
  }

  drop() {
    this.readyState = DeferredSocket.CLOSED
    this.onclose?.(new CloseEvent('close', { code: 1006, reason: '', wasClean: false }))
  }

  frame(frame: ServerFrame) {
    const raw = JSON.stringify(frame)
    expect(parseFrameSafe(raw), 'fixture frame satisfies the generated contract').toMatchObject({ type: frame.type })
    expect(this.onmessage, 'real WsConnection installed the frame callback').toBeTypeOf('function')
    this.onmessage!(new MessageEvent('message', { data: raw }))
  }
}

beforeEach(() => {
  vi.useFakeTimers({ toFake: ['Date', 'setTimeout', 'clearTimeout', 'setInterval', 'clearInterval', 'requestAnimationFrame', 'cancelAnimationFrame'] })
  vi.setSystemTime(new Date('2026-10-08T00:00:00Z'))
  sockets = []
  vi.stubGlobal('WebSocket', DeferredSocket)
  queryClient.clear()
  act(() => {
    useSessionStore.setState(useSessionStore.getInitialState(), true)
    useChatStore.setState(useChatStore.getInitialState(), true)
    useConnectionStore.setState(useConnectionStore.getInitialState(), true)
    useSessionStore.getState().setActiveSession(SID, 'jim')
  })
})

afterEach(() => {
  cleanup()
  queryClient.clear()
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

async function flushFrames() {
  await act(async () => { await vi.advanceTimersByTimeAsync(50) })
}

function sentFrames(socket: DeferredSocket): ClientFrame[] {
  return socket.send.mock.calls.map(([raw]) => JSON.parse(raw) as ClientFrame)
}

async function connectStreaming() {
  let view!: ReturnType<typeof render>
  await act(async () => {
    view = render(
      <StrictMode>
        <QueryClientProvider client={queryClient}>
          <OmnipusRuntimeProvider>{null}</OmnipusRuntimeProvider>
        </QueryClientProvider>
      </StrictMode>,
    )
    await import('./tools/DraftLink')
  })
  // StrictMode setup/cleanup/setup: A is retired, B owns the same effect ref.
  expect(sockets).toHaveLength(2)
  const [retired, current] = sockets
  expect(retired.close).toHaveBeenCalledExactlyOnceWith(1000, 'User disconnected')
  expect(retired.readyState).toBe(DeferredSocket.CLOSED)
  act(() => current.open())
  expect(sentFrames(current)).toStrictEqual([{ type: 'attach_session', session_id: SID }])
  act(() => current.frame({ type: 'catch_up_complete', session_id: SID, seq: 1, boot_id: BOOT, mode: 'snapshot' }))
  await flushFrames()
  act(() => {
    useChatStore.getState().sendMessage('Continue the current turn', { clientMessageId: 'lifetime-user' })
    current.frame({ type: 'token', session_id: SID, turn_id: 'lifetime-turn', content: 'Live partial reply' })
  })
  await flushFrames()
  expect(useConnectionStore.getState()).toMatchObject({ isConnected: true, reconnectPhase: null, disconnectedAt: null })
  expect(useChatStore.getState()).toMatchObject({ isStreaming: true, isReplaying: false })
  expect(useChatStore.getState().sessionsById[SID]).toMatchObject({
    isStreaming: true,
    cursor: { bootId: BOOT, seq: 1 },
  })
  expect(useChatStore.getState().messages.filter((message) => message.role === 'assistant').map((message) => message.content)).toStrictEqual(['Live partial reply'])
  return { view, retired, current }
}

describe('WsLifecycle connection ownership', () => {
  it('ignores a retired connection close after its replacement opens, preserving connection and streaming state', async () => {
    const { retired, current } = await connectStreaming()
    const connectionBefore = useConnectionStore.getState()
    const chatBefore = useChatStore.getState()
    const bucketBefore = chatBefore.sessionsById[SID]

    act(() => retired.deliverClose())

    expect(useConnectionStore.getState()).toStrictEqual(connectionBefore)
    expect(useChatStore.getState().sessionsById[SID]).toStrictEqual(bucketBefore)
    expect(useChatStore.getState().isStreaming).toBe(true)
    expect(useChatStore.getState().lastAssistantMessageId).toBe(chatBefore.lastAssistantMessageId)
    expect(current.readyState).toBe(DeferredSocket.OPEN)
    expect(sockets).toHaveLength(2)
  })

  it('still records a current disconnect, clears streaming and reconnects with a real cursor reattach, without resending', async () => {
    const { current } = await connectStreaming()
    const assistantId = useChatStore.getState().lastAssistantMessageId
    expect(assistantId).not.toBeNull()

    act(() => current.drop())

    expect(useConnectionStore.getState()).toMatchObject({
      isConnected: false,
      disconnectedAt: Date.now(),
      disconnectedAssistantMessageId: assistantId,
      reconnectPhase: 'reconnecting',
      reconnectAttempt: 1,
      connectionError: null,
    })
    expect(useChatStore.getState().isStreaming).toBe(false)
    expect(useChatStore.getState().sessionsById[SID]).toMatchObject({
      isStreaming: false,
      isReplaying: false,
      activeTurnId: null,
      activeTurnAgentId: null,
    })
    await act(async () => { await vi.advanceTimersByTimeAsync(1_000) })
    expect(sockets).toHaveLength(3)
    const reconnected = sockets[2]
    act(() => reconnected.open())
    expect(sentFrames(reconnected)).toStrictEqual([{
      type: 'attach_session', session_id: SID, since_seq: 1, boot_id: BOOT,
    }])
    act(() => reconnected.frame({ type: 'catch_up_complete', session_id: SID, seq: 1, boot_id: BOOT, mode: 'incremental' }))
    await flushFrames()
    expect(useConnectionStore.getState()).toMatchObject({
      isConnected: true,
      disconnectedAt: null,
      disconnectedAssistantMessageId: null,
      reconnectPhase: null,
      reconnectAttempt: 0,
      connectionError: null,
    })
    expect(useChatStore.getState().sessionsById[SID].awaitingCatchUp).toBe(false)
    expect(sockets.flatMap(sentFrames).filter((frame) => frame.type === 'message')).toHaveLength(1)
    expect(useChatStore.getState().messages.filter((message) => message.role === 'assistant').map((message) => message.content)).toStrictEqual(['Live partial reply'])
  })

  it('ignores a delayed close after the effect is unmounted with no replacement', async () => {
    const { view, current } = await connectStreaming()
    view.unmount()
    expect(useConnectionStore.getState().connection).toBeNull()
    const connectionBefore = useConnectionStore.getState()
    const bucketBefore = useChatStore.getState().sessionsById[SID]

    act(() => current.deliverClose())

    expect(useConnectionStore.getState()).toStrictEqual(connectionBefore)
    expect(useChatStore.getState().sessionsById[SID]).toStrictEqual(bucketBefore)
    expect(useChatStore.getState().isStreaming).toBe(true)
    expect(sockets).toHaveLength(2)
  })
})

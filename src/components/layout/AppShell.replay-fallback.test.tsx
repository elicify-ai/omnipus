// SF-2 oracle: a size-limit fallback is information, never connection failure.
// REAL AppShell/route tree/runtime/validation/stores/catalogued toast; only the
// HTTP/WebSocket/browser edges use the established stage-one harness.
import { act, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import userEvent from '@testing-library/user-event'
import type { ServerFrame } from '@/lib/api/generated/asyncapi-types'
import { useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { MAIN_ID, TestSocket } from '../../../tests/l1-room-ui/harness'
import {
  emitFromGateway, mountStage1App, resetStage1App, SETTINGS_PATH,
  Stage1Network, stopStage1App,
} from '../../../tests/l1-room-ui/stage1-harness'

const NOTICE = 'Saved history is large. Showing messages as they load.'
let network: Stage1Network
let openSocketsAtCreation: number[]
beforeEach(() => {
  resetStage1App()
  network = new Stage1Network()
  openSocketsAtCreation = []
  vi.stubGlobal('WebSocket', class extends TestSocket {
    constructor(url: string) {
      super(url)
      openSocketsAtCreation.push(TestSocket.instances.filter(socket => socket.readyState === TestSocket.OPEN).length)
    }
  })
})
afterEach(async () => {
  try {
    expect(network.unexpected).toEqual([])
  } finally {
    vi.useRealTimers()
    await stopStage1App()
  }
})

function completion(): ServerFrame {
  return { type: 'catch_up_complete', session_id: MAIN_ID, seq: 10, boot_id: 'fallback-test', mode: 'snapshot' }
}

async function startLargeReplay(socket: TestSocket) {
  act(() => {
    expect(useSessionStore.getState().attachToSession(MAIN_ID, 'main', 'Mia main chat', 'mia')).toBe(true)
  })
  await emitFromGateway(socket, { type: 'session_snapshot', session_id: MAIN_ID, seq: 10, boot_id: 'fallback-test', reason: 'unknown_position' })
  await emitFromGateway(socket, { type: 'replay_message', session_id: MAIN_ID, id: 'saved-question', role: 'user', content: 'Saved question' })
  await emitFromGateway(socket, { type: 'tool_call_start', session_id: MAIN_ID, call_id: 'large-read', tool: 'read_file', params: { path: 'large.txt' } })
  // Product's default 8 MiB UTF-16 retention limit, not a mocked threshold.
  await emitFromGateway(socket, { type: 'tool_call_result', session_id: MAIN_ID, call_id: 'large-read', tool: 'read_file', status: 'success', result: { output: 'x'.repeat(4 * 1024 * 1024) } })
}

describe('SF-2 — informational history fallback through the real shell', () => {
  it('shows a session-scoped status with Dismiss but no connection Retry, preserving the healthy replay', async () => {
    // Settings still hosts the REAL AppShell/runtime. Avoid rendering the large
    // tool payload itself: this case measures notice/recovery wiring, not layout.
    const { socket } = await mountStage1App(SETTINGS_PATH)
    const connection = useConnectionStore.getState().connection!
    const generation = connection.socketGeneration
    await startLargeReplay(socket)
    const text = await screen.findByText(NOTICE, { exact: true })
    expect(text.closest('[role="status"]')).not.toBeNull()
    expect(text.closest('[role="alert"]')).toBeNull()
    expect(within(screen.getByTestId('app-main-content')).queryByRole('button', { name: /^Retry$/ })).toBeNull()
    expect(within(text.closest('[role="status"]') as HTMLElement).queryByRole('button', { name: /^Retry$/ })).toBeNull()
    expect(useConnectionStore.getState().connectionError).toBeNull()
    expect(connection.isConnected).toBe(true)
    expect(useChatStore.getState().sessionsById[MAIN_ID].awaitingCatchUp).toBe(true)
    expect(useChatStore.getState().messages.some(message => message.content === 'Saved question')).toBe(true)
    expect(TestSocket.instances).toHaveLength(1)

    act(() => useSessionStore.getState().setActiveSession('another-chat'))
    expect(screen.queryByText(NOTICE, { exact: true })).toBeNull()
    act(() => useSessionStore.getState().setActiveSession(MAIN_ID))
    const notice = screen.getByText(NOTICE, { exact: true }).closest('[role="status"]')!
    await userEvent.setup().click(within(notice as HTMLElement).getByRole('button', { name: 'Dismiss' }))
    expect(screen.queryByText(NOTICE, { exact: true })).toBeNull()
    expect(connection.socketGeneration).toBe(generation)
    expect(socket.readyState).toBe(TestSocket.OPEN)
    await emitFromGateway(socket, completion())
    await waitFor(() => expect(useChatStore.getState().sessionsById[MAIN_ID].awaitingCatchUp).toBe(false))
    expect(useChatStore.getState().messages.map(message => message.content)).toContain('Saved question')
  })

  it('positive control: the same scoped query finds real connection Retry and clicking it safely replaces the socket', async () => {
    const { socket } = await mountStage1App(SETTINGS_PATH)
    // Actual parser failure -> runtime onError -> real connection banner, not
    // setState or a fake shell. Invalid bytes are a browser transport input.
    await act(async () => {
      for (let i = 0; i < 5; i++) socket.onmessage?.(new MessageEvent('message', { data: 'not valid JSON' }))
    })
    const main = screen.getByTestId('app-main-content')
    const message = await within(main).findByText('Received 5 invalid frames in a row — the connection may be unstable.')
    const retry = within(main).getByRole('button', { name: /^Retry$/ })
    expect(message.closest('[role="alert"]')).toContainElement(retry)
    expect(retry).toBeEnabled()
    expect(socket.readyState).toBe(TestSocket.OPEN)
    await userEvent.setup().click(retry)
    expect(socket.readyState).toBe(TestSocket.CLOSED)
    const replacement = TestSocket.instances.at(-1)!
    await act(async () => replacement.open())
    expect(openSocketsAtCreation).toEqual([0, 0])
    expect(TestSocket.instances.filter(candidate => candidate.readyState === TestSocket.OPEN)).toEqual([replacement])
  })
})

describe('SF-2 — explicit reconnect retires the healthy old socket', () => {
  it.each(['store reconnect', 'direct connect'] as const)('%s closes and detaches before replacement, ignoring stale events', async (path) => {
    const { socket } = await mountStage1App(SETTINGS_PATH)
    const connection = useConnectionStore.getState().connection!
    const stale = { message: socket.onmessage, close: socket.onclose, open: socket.onopen, error: socket.onerror }
    expect(socket.readyState).toBe(TestSocket.OPEN)
    act(() => {
      if (path === 'store reconnect') useConnectionStore.getState().reconnect()
      else connection.connect()
    })
    const replacement = TestSocket.instances.at(-1)!
    expect(replacement).not.toBe(socket)
    expect(socket.readyState).toBe(TestSocket.CLOSED)
    expect(openSocketsAtCreation).toEqual([0, 0]) // Old closed BEFORE new construction.
    expect([socket.onopen, socket.onmessage, socket.onerror, socket.onclose]).toEqual([null, null, null, null])
    await act(async () => replacement.open())
    expect(TestSocket.instances.filter(candidate => candidate.readyState === TestSocket.OPEN)).toEqual([replacement])
    await emitFromGateway(replacement, { type: 'session_snapshot', session_id: MAIN_ID, seq: 10, boot_id: 'fallback-test', reason: 'unknown_position' })
    await emitFromGateway(replacement, { type: 'replay_message', session_id: MAIN_ID, id: 'replacement-row', role: 'assistant', content: 'Fresh socket history' })
    await emitFromGateway(replacement, completion())
    await waitFor(() => expect(useChatStore.getState().messages.map(message => message.content)).toEqual(['Fresh socket history']))

    const late: ServerFrame = { type: 'replay_message', session_id: MAIN_ID, id: 'stale-row', role: 'assistant', content: 'Obsolete socket must not deliver' }
    await act(async () => {
      socket.receive(late)
      // Also cover callbacks captured before retirement, not only null fields.
      stale.message?.(new MessageEvent('message', { data: JSON.stringify(late) }))
      stale.close?.(new CloseEvent('close', { code: 1006, reason: 'late old close' }))
      stale.open?.(new Event('open'))
      stale.error?.(new Event('error'))
    })
    // Positive delivery barrier: this live frame must flush AFTER the stale
    // callbacks, so an absence assertion cannot pass before queued work runs.
    await emitFromGateway(replacement, { type: 'replay_message', session_id: MAIN_ID, id: 'current-barrier', role: 'assistant', content: 'Replacement still live' })
    await waitFor(() => expect(useChatStore.getState().messages.map(message => message.content)).toEqual(['Fresh socket history', 'Replacement still live']))
    expect(useConnectionStore.getState().isConnected).toBe(true)
    expect(useConnectionStore.getState().reconnectPhase).toBeNull()
    expect(useConnectionStore.getState().connectionError).toBeNull()
    expect(connection.isConnected).toBe(true)
    expect(TestSocket.instances).toHaveLength(2)
    expect(TestSocket.instances.filter(candidate => candidate.readyState === TestSocket.OPEN)).toEqual([replacement])
  })
})

describe('SF-2 — intentional effect teardown is not a transport drop', () => {
  it('retires an unmounted shell without clearing its saved live streaming state', async () => {
    const { socket, unmount } = await mountStage1App(SETTINGS_PATH)
    await emitFromGateway(socket, { type: 'token', session_id: MAIN_ID, turn_id: 'live-during-unmount', message_id: 'live-unmount-answer', content: 'Live partial answer' })
    await waitFor(() => expect(useChatStore.getState().isStreaming).toBe(true))
    act(() => unmount())
    expect(socket.readyState).toBe(TestSocket.CLOSED)
    expect(useConnectionStore.getState().connection).toBeNull()
    expect(useChatStore.getState().isStreaming).toBe(true)
    expect(useChatStore.getState().sessionsById[MAIN_ID].isStreaming).toBe(true)
  })
})

describe('SF-2 — failed retirement cannot open another socket', () => {
  it('reports a throwing close and refuses replacement until that same old socket can be retired', async () => {
    const { socket } = await mountStage1App(SETTINGS_PATH)
    const connection = useConnectionStore.getState().connection!
    const close = vi.spyOn(socket, 'close').mockImplementation(() => {
      throw new DOMException('close failed', 'InvalidStateError')
    })
    try {
      act(() => useConnectionStore.getState().reconnect())
      expect(TestSocket.instances).toHaveLength(1)
      expect(connection.isConnected).toBe(false)
      expect([socket.onopen, socket.onmessage, socket.onerror, socket.onclose]).toEqual([null, null, null, null])
      const main = screen.getByTestId('app-main-content')
      expect(await within(main).findByText('Could not close the previous connection. Please reload the page.')).toBeVisible()
      // Retrying the error must retry retirement, not forget the live old socket.
      await userEvent.setup().click(within(main).getByRole('button', { name: /^Retry$/ }))
      expect(TestSocket.instances).toHaveLength(1)
      expect(close).toHaveBeenCalledTimes(2)
    } finally {
      close.mockRestore()
    }
    await userEvent.setup().click(within(screen.getByTestId('app-main-content')).getByRole('button', { name: /^Retry$/ }))
    const replacement = TestSocket.instances.at(-1)!
    expect(replacement).not.toBe(socket)
    expect(socket.readyState).toBe(TestSocket.CLOSED)
    await act(async () => replacement.open())
    expect(openSocketsAtCreation).toEqual([0, 0])
    expect(TestSocket.instances.filter(candidate => candidate.readyState === TestSocket.OPEN)).toEqual([replacement])
  })
})

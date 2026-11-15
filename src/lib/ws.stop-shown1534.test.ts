import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { WsConnection } from './ws'
import type { TokenFrame } from '@/lib/api/generated/asyncapi-types'
import { useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { emptySessionState } from '@/store/chat/session'
import { pendingCancelAckSids } from '@/store/chat/runtime-state'

// Architect §§1,5,6: exercise the REAL onmessage parser -> inbound rAF batch
// -> store reducer -> cancelStream -> socket.send. Only platform socket and
// animation scheduling are controlled, not WsConnection internals or reducers.
const SID = 'queued-stop-1534'
const TURN = 'queued-turn-1534'
const MID = 'queued-message-1534'
let socket: SocketEdge
let connection: WsConnection
let scheduled: FrameRequestCallback[]

class SocketEdge {
  static instances: SocketEdge[] = []
  static OPEN = 1
  static CLOSED = 3
  readyState = SocketEdge.OPEN
  onopen: ((event: Event) => void) | null = null
  onmessage: ((event: MessageEvent) => void) | null = null
  onclose: ((event: CloseEvent) => void) | null = null
  onerror: ((event: Event) => void) | null = null
  sent: unknown[] = []
  constructor() { SocketEdge.instances.push(this) }
  send(raw: string) { this.sent.push(JSON.parse(raw)) }
  close() { this.readyState = SocketEdge.CLOSED }
  receive(content: string, extra: Partial<TokenFrame> = {}) {
    this.onmessage?.(new MessageEvent('message', { data: JSON.stringify({
      type: 'token', session_id: SID, turn_id: TURN, message_id: MID, content, ...extra,
    }) }))
  }
}
function contents() {
  const b = useChatStore.getState().sessionsById[SID]
  return b.messageOrder.map(id => [b.messagesById[id].content, b.messagesById[id].status])
}
function flush() {
  expect(scheduled).toHaveLength(1)
  scheduled.shift()!(0)
}
beforeEach(() => {
  scheduled = []
  SocketEdge.instances = []
  pendingCancelAckSids.clear()
  useChatStore.setState(useChatStore.getInitialState(), true)
  useSessionStore.setState(useSessionStore.getInitialState(), true)
  useConnectionStore.setState(useConnectionStore.getInitialState(), true)
  useSessionStore.setState({ activeSessionId: SID, activeAgentId: 'mia' })
  useChatStore.setState({ sessionsById: { [SID]: emptySessionState() } })
  vi.stubGlobal('Worker', undefined) // actual supported inline parser path
  vi.stubGlobal('WebSocket', SocketEdge)
  vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) => { scheduled.push(callback); return scheduled.length })
  vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible')
  connection = new WsConnection({
    onFrames: frames => frames.forEach(f => useChatStore.getState().handleFrame(f)),
    onConnected: vi.fn(), onDisconnected: vi.fn(), onError: message => { throw new Error(message) },
  })
  connection.connect()
  expect(SocketEdge.instances).toHaveLength(1)
  socket = SocketEdge.instances[0]
  socket.onopen?.(new Event('open'))
  useConnectionStore.getState().setConnection(connection)
})
afterEach(() => {
  connection.disconnect()
  pendingCancelAckSids.clear()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

describe('#1534 queued socket frames do not count as shown', () => {
  it('cancel snapshots only applied code points, excluding a received replace still waiting for rAF', () => {
    socket.receive('VISIBLE 😀')
    flush()
    expect(contents()).toEqual([['VISIBLE 😀', 'streaming']])
    socket.receive('QUEUED LONG REPLACEMENT', { replace: true })
    expect(scheduled).toHaveLength(1)
    expect(contents()).toEqual([['VISIBLE 😀', 'streaming']])
    expect(useChatStore.getState().cancelStream(SID)).toBe(true)
    expect(socket.sent).toEqual([{
      type: 'cancel', session_id: SID, shown: { message_id: MID, turn_id: TURN, code_points: 9 },
    }])
    flush()
    expect(contents()).toEqual([['VISIBLE 😀', 'interrupted']])
  })

  it('new-message-ID token queued at Stop cannot reopen the frozen turn when the batch applies', () => {
    socket.receive('VISIBLE')
    flush()
    socket.receive('UNSEEN NEXT STEP', { message_id: 'later-message' })
    expect(scheduled).toHaveLength(1)
    expect(useChatStore.getState().cancelStream(SID)).toBe(true)
    expect(contents()).toEqual([['VISIBLE', 'interrupted']])
    flush()
    expect(contents()).toEqual([['VISIBLE', 'interrupted']])
  })
})

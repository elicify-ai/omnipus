// Test transport below REAL WsConnection. Deterministic browser-frame barrier;
// deliberately does not wait for each buffered frame to dispatch before complete.
import { act } from '@testing-library/react'
import { vi } from 'vitest'
import type { ServerFrame } from '@/lib/api/generated/asyncapi-types'
import { WsConnection } from '@/lib/ws'
import { useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { TestSocket } from '../l1-room-ui/harness'
import { SID, expectValidFrame } from './fixtures'

export function historyTransport() {
  const frames: ServerFrame[] = []
  const batches: ServerFrame[][] = []
  const browserFrames = new Map<number, FrameRequestCallback>()
  let next = 0
  vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) => { browserFrames.set(++next, callback); return next })
  vi.stubGlobal('cancelAnimationFrame', (id: number) => browserFrames.delete(id))
  vi.stubGlobal('WebSocket', TestSocket)
  TestSocket.instances = []
  const connection = new WsConnection({
    onFrames: (batch) => act(() => {
      batches.push(batch)
      frames.push(...batch)
      batch.forEach((frame) => useChatStore.getState().handleFrame(frame))
    }),
    onConnected: () => useConnectionStore.getState().setConnected(true),
    onDisconnected: () => useConnectionStore.getState().setConnected(false),
    onError: (error) => useConnectionStore.getState().setConnectionError(error),
  })
  act(() => { useConnectionStore.getState().setConnection(connection); connection.connect() })
  const socket = TestSocket.instances[0]
  if (!socket) throw new Error('Transport instrument: no socket was constructed')
  act(() => socket.open())
  function flush() {
    act(() => {
      const pending = [...browserFrames.values()]
      browserFrames.clear()
      pending.forEach((callback) => callback(performance.now()))
    })
  }
  function deliver(...incoming: ServerFrame[]) {
    act(() => incoming.forEach((frame) => { expectValidFrame(frame); socket.receive(frame) }))
    flush()
  }
  function attach(sessionId = SID) {
    if (!connection.send({ type: 'attach_session', session_id: sessionId })) throw new Error('Transport instrument: attach was not sent')
  }
  return { connection, socket, frames, batches, flush, deliver, attach }
}

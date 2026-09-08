import { act } from 'react'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { BrowserLiveView } from './BrowserLiveView'

// US-4.3 / US-5.1: use the real component, socket client and media session.
// Only the browser's network/media APIs are replaced. Failure reports must
// preserve bounded retry delays; an unattached socket needs a fresh attach.
class TestSocket {
  static OPEN = 1
  static instances: TestSocket[] = []
  readyState = 1
  sent: Record<string, unknown>[] = []
  onopen: (() => void) | null = null
  onmessage: ((event: { data: string }) => void) | null = null
  onclose: ((event: { code: number }) => void) | null = null
  onerror: (() => void) | null = null
  constructor() { TestSocket.instances.push(this) }
  send(data: string) { this.sent.push(JSON.parse(data)) }
  close() { this.readyState = 3 }
  receive(frame: Record<string, unknown>) { this.onmessage?.({ data: JSON.stringify(frame) }) }
}

class TestPeer {
  iceGatheringState = 'complete'
  iceConnectionState = 'new'
  localDescription: RTCSessionDescriptionInit | null = null
  addTransceiver() {}
  createDataChannel() { return { close() {} } }
  async createOffer() { return { type: 'offer', sdp: 'test-offer' } }
  async setLocalDescription(description: RTCSessionDescriptionInit) { this.localDescription = description }
  async setRemoteDescription() {}
  close() {}
}

async function receive(socket: TestSocket, frame: Record<string, unknown>) {
  await act(async () => {
    socket.receive(frame)
    await vi.advanceTimersByTimeAsync(0)
  })
}

function offers(socket: TestSocket) {
  return socket.sent.filter(frame => frame.type === 'browser_webrtc_offer')
}

beforeEach(() => {
  vi.useFakeTimers()
  TestSocket.instances = []
  vi.stubGlobal('WebSocket', TestSocket)
  vi.stubGlobal('RTCPeerConnection', TestPeer)
})

afterEach(() => {
  cleanup()
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('browser recovery through the real signaling session', () => {
  it('keeps server offer failures inside the exponential retry budget', async () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    const socket = TestSocket.instances[0]!
    act(() => socket.onopen?.())
    await receive(socket, { type: 'browser_webrtc_state', available: true })
    expect(offers(socket)).toHaveLength(1)
    for (let retry = 0; retry < 5; retry++) {
      await receive(socket, { type: 'browser_webrtc_state', available: true, reason: 'error' })
      expect(offers(socket)).toHaveLength(retry + 1)
      await act(async () => { await vi.advanceTimersByTimeAsync(15_000 * 2 ** retry - 1) })
      expect(offers(socket)).toHaveLength(retry + 1)
      await act(async () => { await vi.advanceTimersByTimeAsync(1) })
      expect(offers(socket)).toHaveLength(retry + 2)
    }
    await receive(socket, { type: 'browser_webrtc_state', available: true, reason: 'error' })
    await act(async () => { await vi.advanceTimersByTimeAsync(480_000) })
    expect(offers(socket)).toHaveLength(6)
  })

  it('retries attachment after an initial attach refusal before offering video', async () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    const first = TestSocket.instances[0]!
    act(() => first.onopen?.())
    await receive(first, { type: 'browser_status', state: 'error', message: 'browser_attach failed: this machine is low on memory' })
    fireEvent.click(screen.getByRole('button', { name: /retry/i }))
    expect(TestSocket.instances).toHaveLength(2)
    expect(first.readyState).toBe(3)
    const replacement = TestSocket.instances[1]!
    expect(offers(first)).toEqual([])
    expect(replacement.sent).toEqual([])
    act(() => replacement.onopen?.())
    expect(replacement.sent).toEqual([{ type: 'browser_attach', session_id: 's1', agent_id: 'a1' }])
    await receive(replacement, { type: 'browser_status', state: 'attached' })
    await receive(replacement, { type: 'browser_webrtc_state', available: true })
    expect(offers(replacement)).toHaveLength(1)
    expect(screen.queryByText(/low on memory/i)).toBeNull()
  })

  it('retries only media when the browser attachment is established', async () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    const socket = TestSocket.instances[0]!
    act(() => socket.onopen?.())
    await receive(socket, { type: 'browser_status', state: 'attached' })
    await receive(socket, { type: 'browser_webrtc_state', available: true })
    await receive(socket, { type: 'browser_webrtc_state', available: true, reason: 'error' })
    await receive(socket, { type: 'error', message: 'The video request could not be completed.' })
    fireEvent.click(screen.getByRole('button', { name: /retry/i }))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(TestSocket.instances).toHaveLength(1)
    expect(offers(socket)).toHaveLength(2)
    expect(screen.queryByText('The video request could not be completed.')).toBeNull()
  })
})

import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { installBrowserFrameCallbacks, emitBrowserFrame } from './browserFrameTestUtils'

vi.mock('@/lib/browserWebRTC', async (original) => ({
  ...await original<typeof import('@/lib/browserWebRTC')>(),
  BrowserWebRTCSession: class {
    state = 'idle'
    onStream() {} onFallback() {} setICEServers() {} applyState() {} start() {} stop() {}
    applyAnswer() { return false }
  },
}))
import { BrowserLiveView } from './BrowserLiveView'

class Socket {
  static OPEN = 1
  static instances: Socket[] = []
  readyState = 1; bufferedAmount = 0
  onopen?: () => void
  onmessage?: (event: { data: string }) => void
  onclose?: (event: { code: number }) => void
  frames: Record<string, unknown>[] = []
  constructor() { Socket.instances.push(this) }
  send(data: string) { this.frames.push(JSON.parse(data)) }
  close() { this.readyState = 3 }
  receive(frame: unknown) { this.onmessage?.({ data: JSON.stringify(frame) }) }
}
class Peer {
  static instances: Peer[] = []
  iceGatheringState = 'complete'; connectionState = 'new'
  localDescription = { sdp: 'offer' }
  channels: Record<string, { readyState: string; bufferedAmount: number; send: ReturnType<typeof vi.fn>; close: () => void }> = {}
  onconnectionstatechange?: () => void
  constructor() { Peer.instances.push(this) }
  createDataChannel(label: string) { return this.channels[label] = { readyState: 'open', bufferedAmount: 0, send: vi.fn(), close() {} } }
  async createOffer() { return { type: 'offer', sdp: 'offer' } }
  async setLocalDescription() {} async setRemoteDescription() {}
  close() { this.connectionState = 'closed'; this.onconnectionstatechange?.() }
}
installBrowserFrameCallbacks()
beforeEach(() => {
  window.history.replaceState({}, '', '/?browserInput=dedicated')
  Socket.instances = []; Peer.instances = []
  vi.stubGlobal('WebSocket', Socket); vi.stubGlobal('RTCPeerConnection', Peer)
})
afterEach(() => { vi.unstubAllGlobals(); window.history.replaceState({}, '', '/') })
const capture = 'a'.repeat(64)
function health(socket: Socket, generation: number, marker?: number) {
  socket.receive({ type: 'browser_video_health', session_id: 's1', state: marker === undefined ? 'transitioning' : 'recovered', capture_id: capture, capture_generation: generation, css_width: 1280, css_height: 720, ...(marker === undefined ? {} : { rtp_timestamp: marker }) })
}
async function connected() {
  render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={{ id: 'unchanged-media' } as MediaStream} />)
  const socket = Socket.instances[0]
  act(() => socket.onopen?.())
  const video = screen.getByTestId('browser-live-video') as HTMLVideoElement
  Object.defineProperty(video, 'videoWidth', { configurable: true, value: 1280 })
  Object.defineProperty(video, 'videoHeight', { configurable: true, value: 720 })
  fireEvent.loadedMetadata(video)
  act(() => {
    health(socket, 1, 100)
    emitBrowserFrame(video, { rtpTimestamp: 100, expectedDisplayTime: performance.now() - 1 })
    socket.receive({ type: 'browser_webrtc_state', available: true, has_audio: true })
  })
  await waitFor(() => expect(socket.frames.filter(f => f.type === 'browser_input_offer')).toHaveLength(1))
  act(() => socket.receive({ type: 'browser_input_answer', session_id: 's1', input_epoch: 1, offer_id: 1, control_epoch: 0, sdp: 'answer' }))
  await waitFor(() => expect(document.querySelector('[data-input-state="ready"]')).not.toBeNull())
  return { socket, video, peer: Peer.instances[0], frame: screen.getByTestId('browser-live-frame') }
}

it.each(['/', '/?browserInput=websocket', '/?browserInput=dedicated'])('requires dedicated input at %s without fallback; Retry preserves media', async (url) => {
  window.history.replaceState({}, '', url)
  const s = await connected()
  const originalMedia = s.video.srcObject
  fireEvent.keyDown(s.frame, { key: 'a' })
  expect(s.peer.channels['input-reliable'].send.mock.calls.map(([data]) => JSON.parse(data))).toEqual([{
    type: 'browser_input', kind: 'text', text: 'a', modifiers: 0, capture_id: capture, capture_generation: 1,
    input_epoch: 1, control_epoch: 0, reliable_seq: 1, gesture_barrier: 0,
  }])
  expect(s.socket.frames.filter(f => f.type === 'browser_input' || f.type === 'browser_control')).toEqual([])
  act(() => s.peer.close())
  expect(screen.getByTestId('browser-input-error')).toHaveTextContent('Input connection lost')
  fireEvent.keyDown(s.frame, { key: 'b' })
  expect(s.peer.channels['input-reliable'].send).toHaveBeenCalledTimes(1)
  expect(s.video.srcObject).toBe(originalMedia)
  expect(s.socket.frames.filter(f => f.type === 'browser_control')).toEqual([{ type: 'browser_control', action: 'release', input_epoch: 1, control_epoch: 1 }])
  act(() => s.socket.receive({ type: 'browser_input_control_ack', session_id: 's1', input_epoch: 1, control_epoch: 1, ok: true, capture_id: capture, capture_generation: 1 }))
  expect(screen.getByTestId('browser-input-error')).toBeVisible()
  expect(s.socket.frames.filter(f => f.type === 'browser_input_offer')).toHaveLength(1)
  fireEvent.click(screen.getByRole('button', { name: 'Retry input' }))
  await waitFor(() => expect(s.socket.frames.filter(f => f.type === 'browser_input_offer')).toHaveLength(2))
  act(() => s.socket.receive({ type: 'browser_input_answer', session_id: 's1', input_epoch: 2, offer_id: 2, control_epoch: 1, sdp: 'answer2' }))
  await waitFor(() => expect(document.querySelector('[data-input-state="ready"]')).not.toBeNull())
  expect(s.video.srcObject).toBe(originalMedia)
  expect(s.socket.frames.filter(f => f.type === 'browser_input')).toEqual([])
})

it('waits for the control acknowledgment and its presented capture generation', async () => {
  const s = await connected()
  fireEvent.click(screen.getByRole('button', { name: 'Go back' }))
  expect(s.socket.frames.at(-1)).toEqual({ type: 'browser_input', kind: 'navigate_back', input_epoch: 1, control_epoch: 1 })
  fireEvent.keyDown(s.frame, { key: 'a' })
  expect(s.peer.channels['input-reliable'].send).not.toHaveBeenCalled()
  act(() => s.socket.receive({ type: 'browser_input_control_ack', session_id: 's1', input_epoch: 1, control_epoch: 1, ok: true, capture_id: capture, capture_generation: 2 }))
  fireEvent.keyDown(s.frame, { key: 'a' })
  expect(s.peer.channels['input-reliable'].send).not.toHaveBeenCalled()
  act(() => { health(s.socket, 2, 200); emitBrowserFrame(s.video, { rtpTimestamp: 200, expectedDisplayTime: performance.now() - 1 }) })
  fireEvent.keyDown(s.frame, { key: 'a' })
  expect(s.peer.channels['input-reliable'].send.mock.calls.map(([data]) => JSON.parse(data).capture_generation)).toEqual([2])
})


it('queues Retry behind failed-input retirement without replacing media or its socket', async () => {
  const s = await connected(), originalMedia = s.video.srcObject
  fireEvent.keyDown(s.frame, { key: 'ArrowLeft', code: 'ArrowLeft' })
  act(() => s.peer.close())
  expect(s.socket.frames.at(-1)).toEqual({ type: 'browser_control', action: 'release', input_epoch: 1, control_epoch: 1 })
  fireEvent.click(screen.getByRole('button', { name: 'Retry input' }))
  expect(Socket.instances).toHaveLength(1)
  expect(s.socket.frames.filter(f => f.type === 'browser_input_offer')).toHaveLength(1)
  expect(screen.getByTestId('browser-input-error')).toBeVisible()
  act(() => s.socket.receive({ type: 'browser_input_control_ack', session_id: 's1', input_epoch: 1, control_epoch: 1, ok: true, capture_id: capture, capture_generation: 1 }))
  await waitFor(() => expect(s.socket.frames.filter(f => f.type === 'browser_input_offer')).toHaveLength(2))
  expect(s.socket.frames.at(-1)).toMatchObject({ type: 'browser_input_offer', input_epoch: 2, control_epoch: 1 })
  expect(s.video.srcObject).toBe(originalMedia)
  expect(s.socket.frames.filter(f => f.type === 'browser_input')).toEqual([])
})

it('uses a fresh attachment when the failed-input retirement socket cannot send', async () => {
  const s = await connected()
  s.socket.readyState = 3
  act(() => s.peer.close())
  expect(screen.getByTestId('browser-input-error')).toBeVisible()
  fireEvent.click(screen.getByRole('button', { name: 'Retry input' }))
  await waitFor(() => expect(Socket.instances).toHaveLength(2))
  expect(s.socket.frames.filter(f => f.type === 'browser_control')).toEqual([])
})

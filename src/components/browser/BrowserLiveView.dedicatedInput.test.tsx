import { decodeBrowserInput } from '@/lib/browserInputCodec'
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
  fireEvent.keyDown(s.frame, { key: 'a', code: 'KeyA', keyCode: 65 })
  fireEvent.keyUp(s.frame, { key: 'a', code: 'KeyA', keyCode: 65 })
  expect(s.peer.channels['input-reliable'].send.mock.calls.map(([data]) => decodeBrowserInput(data))).toEqual([{
    type: 'browser_input', kind: 'key_down', key: 'a', code: 'KeyA', key_code: 65, text: 'a', modifiers: 0, capture_id: capture, capture_generation: 1,
    input_epoch: 1, control_epoch: 0, reliable_seq: 1, gesture_barrier: 1,
  }, {
    type: 'browser_input', kind: 'key_up', key: 'a', code: 'KeyA', key_code: 65, modifiers: 0, capture_id: capture, capture_generation: 1,
    input_epoch: 1, control_epoch: 0, reliable_seq: 2, gesture_barrier: 2,
  }])
  expect(s.socket.frames.filter(f => f.type === 'browser_input' || f.type === 'browser_control')).toEqual([])
  act(() => s.peer.close())
  expect(screen.getByTestId('browser-input-error')).toHaveTextContent('Input connection lost')
  fireEvent.keyDown(s.frame, { key: 'b' })
  expect(s.peer.channels['input-reliable'].send).toHaveBeenCalledTimes(2)
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
  expect(s.socket.frames.at(-1)).toEqual({ type: 'browser_input', kind: 'navigate_back', input_epoch: 1, control_epoch: 2 })
  fireEvent.keyDown(s.frame, { key: 'a', code: 'KeyA', keyCode: 65 })
  fireEvent.keyUp(s.frame, { key: 'a', code: 'KeyA', keyCode: 65 })
  expect(s.peer.channels['input-reliable'].send).not.toHaveBeenCalled()
  // The ownership request is epoch 1; navigation is epoch 2. An
  // intermediate ownership ACK must not reopen input on the previous page.
  act(() => s.socket.receive({ type: 'browser_input_control_ack', session_id: 's1', input_epoch: 1, control_epoch: 1, ok: true, capture_id: capture, capture_generation: 1 }))
  fireEvent.keyDown(s.frame, { key: 'b', code: 'KeyB', keyCode: 66 })
  expect(s.peer.channels['input-reliable'].send).not.toHaveBeenCalled()
  act(() => s.socket.receive({ type: 'browser_input_control_ack', session_id: 's1', input_epoch: 1, control_epoch: 2, ok: true, capture_id: capture, capture_generation: 2 }))
  fireEvent.keyDown(s.frame, { key: 'a', code: 'KeyA', keyCode: 65 })
  fireEvent.keyUp(s.frame, { key: 'a', code: 'KeyA', keyCode: 65 })
  expect(s.peer.channels['input-reliable'].send).not.toHaveBeenCalled()
  act(() => { health(s.socket, 2, 200); emitBrowserFrame(s.video, { rtpTimestamp: 200, expectedDisplayTime: performance.now() - 1 }) })
  fireEvent.keyDown(s.frame, { key: 'a', code: 'KeyA', keyCode: 65 })
  fireEvent.keyUp(s.frame, { key: 'a', code: 'KeyA', keyCode: 65 })
  expect(s.peer.channels['input-reliable'].send.mock.calls.map(([data]) => decodeBrowserInput(data).capture_generation)).toEqual([2, 2])
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

it('shows the real backlog pause and enables explicit resume only after release acknowledgment', async () => {
  const s = await connected()
  const media = s.video.srcObject
  act(() => s.socket.receive({ type: 'browser_input_state', session_id: 's1', input_epoch: 1, offer_id: 1, control_epoch: 0, state: 'failed', reason: 'reliable input queue expired' }))
  expect(screen.getByText('Browser fell behind. Input paused.')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Resume input' })).toBeDisabled()
  expect(s.socket.frames.filter(f => f.type === 'browser_control')).toEqual([{ type: 'browser_control', action: 'release', input_epoch: 1, control_epoch: 1 }])
  expect(s.peer.connectionState).not.toBe('closed')
  fireEvent.keyDown(s.frame, { key: ' ', code: 'Space', keyCode: 32 })
  expect(s.peer.channels['input-reliable'].send).not.toHaveBeenCalled()
  act(() => s.socket.receive({ type: 'browser_input_control_ack', session_id: 's1', input_epoch: 1, control_epoch: 1, ok: true, capture_id: capture, capture_generation: 1 }))
  expect(screen.getByRole('button', { name: 'Resume input' })).toBeEnabled()
  expect(document.querySelector('[data-input-state="paused"]')).not.toBeNull()
  act(() => s.socket.receive({ type: 'browser_webrtc_state', available: true, has_audio: true }))
  expect(document.querySelector('[data-input-state="paused"]')).not.toBeNull()
  fireEvent.keyDown(s.frame, { key: ' ', code: 'Space', keyCode: 32 })
  expect(s.peer.channels['input-reliable'].send).not.toHaveBeenCalled()
  fireEvent.click(screen.getByRole('button', { name: 'Resume input' }))
  expect(document.querySelector('[data-input-state="ready"]')).not.toBeNull()
  expect(Peer.instances).toHaveLength(1)
  expect(s.video.srcObject).toBe(media)
  expect(s.socket.frames.filter(f => f.type === 'browser_input')).toEqual([])
})

it.each(['focused', 'blurred', 'hidden', 'new capture', 'input during pause'])('automatically recovers pressure only with continuous safe remote focus: %s', async scenario => {
  const s = await connected()
  s.peer.connectionState = 'connected'
  vi.spyOn(document, 'hasFocus').mockReturnValue(true)
  const visibility = vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible')
  const hidden = vi.spyOn(document, 'hidden', 'get').mockReturnValue(false)
  const sink = screen.getByRole('textbox', { name: 'Remote browser text input' })
  act(() => sink.focus())
  act(() => s.socket.receive({ type: 'browser_input_state', session_id: 's1', input_epoch: 1, offer_id: 1, control_epoch: 0, state: 'failed', reason: 'reliable input queue expired' }))
  expect(document.querySelector('[data-input-state="paused"]')).not.toBeNull()
  if (scenario === 'blurred') { fireEvent.blur(s.frame); act(() => sink.focus()) }
  if (scenario === 'hidden') { visibility.mockReturnValue('hidden'); hidden.mockReturnValue(true); fireEvent(document, new Event('visibilitychange')); visibility.mockReturnValue('visible'); hidden.mockReturnValue(false) }
  if (scenario === 'input during pause') fireEvent.keyDown(sink, { key: 'a', code: 'KeyA', keyCode: 65 })
  act(() => s.socket.receive({ type: 'browser_input_control_ack', session_id: 's1', input_epoch: 1, control_epoch: 1, ok: true, capture_id: capture, capture_generation: scenario === 'new capture' ? 2 : 1 }))
  expect(document.querySelector(`[data-input-state="${scenario === 'focused' ? 'ready' : 'paused'}"]`)).not.toBeNull()
  expect(s.peer.channels['input-reliable'].send).not.toHaveBeenCalled()
  expect(Peer.instances).toHaveLength(1)
  expect(s.socket.frames.filter(f => f.type === 'browser_control')).toEqual([{ type: 'browser_control', action: 'release', input_epoch: 1, control_epoch: 1 }])
  vi.restoreAllMocks()
})

it('stops loading while picture proof is pending, releases held input, and waits for the new picture', async () => {
  const s = await connected()
  fireEvent.keyDown(s.frame, { key: 'a', code: 'KeyA', keyCode: 65 })
  act(() => health(s.socket, 2))
  const stop = screen.getByRole('button', { name: 'Stop loading' })
  expect(stop).toBeEnabled()
  expect(screen.getByText('Waiting for the current page to appear before enabling input. Use Stop loading to cancel a pending page load.')).toBeInTheDocument()
  fireEvent.click(stop)
  expect(s.socket.frames.filter(frame => frame.type === 'browser_input')).toEqual([
    { type: 'browser_input', kind: 'stop_loading', input_epoch: 1, control_epoch: 2 },
  ])
  expect(s.peer.channels['input-reliable'].send.mock.calls.map(([data]) => decodeBrowserInput(data).kind)).toEqual(['key_down', 'key_up'])
  fireEvent.keyDown(s.frame, { key: 'b', code: 'KeyB' })
  act(() => s.socket.receive({ type: 'browser_input_control_ack', session_id: 's1', input_epoch: 1, control_epoch: 2, ok: true, capture_id: capture, capture_generation: 3 }))
  fireEvent.keyDown(s.frame, { key: 'b', code: 'KeyB' })
  expect(s.peer.channels['input-reliable'].send).toHaveBeenCalledTimes(2)
  act(() => { health(s.socket, 3, 300); emitBrowserFrame(s.video, { rtpTimestamp: 300, expectedDisplayTime: performance.now() - 1 }) })
  fireEvent.keyDown(s.frame, { key: 'c', code: 'KeyC' })
  expect(s.peer.channels['input-reliable'].send.mock.calls.map(([data]) => {
    const frame = decodeBrowserInput(data); return [frame.kind, frame.control_epoch, frame.reliable_seq]
  })).toEqual([['key_down', 0, 1], ['key_up', 0, 2], ['key_down', 2, 1]])
})

it('disables Stop loading before the command connection opens', () => {
  render(<BrowserLiveView sessionId="s1" agentId="a1" />)
  expect(screen.getByRole('button', { name: 'Stop loading' })).toBeDisabled()
})

it('Stop loading releases a held key before retiring the current control', async () => {
  const s = await connected()
  fireEvent.keyDown(s.frame, { key: 'a', code: 'KeyA', keyCode: 65 })
  fireEvent.click(screen.getByRole('button', { name: 'Stop loading' }))
  expect(s.peer.channels['input-reliable'].send.mock.calls.map(([data]) => {
    const frame = decodeBrowserInput(data); return [frame.kind, frame.control_epoch, frame.reliable_seq]
  })).toEqual([['key_down', 0, 1], ['key_up', 0, 2]])
  expect(s.socket.frames.filter(frame => frame.type === 'browser_input')).toEqual([
    { type: 'browser_input', kind: 'stop_loading', input_epoch: 1, control_epoch: 2 },
  ])
})

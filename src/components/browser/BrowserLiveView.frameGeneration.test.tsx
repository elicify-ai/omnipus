import { describe, expect, it, vi, beforeEach } from 'vitest'
import { Profiler } from 'react'
import { act, fireEvent, render, screen } from '@testing-library/react'
import { useUiStore } from '@/store/ui'
import type { BrowserLiveWsCallbacks } from '@/lib/browserLiveWs'
import { installBrowserFrameCallbacks, emitBrowserFrame } from './browserFrameTestUtils'

const { callbacksRef, sendInput } = vi.hoisted(() => ({
  callbacksRef: { current: null as BrowserLiveWsCallbacks | null },
  sendInput: vi.fn(() => true),
}))
vi.mock('@/lib/browserLiveWs', async importOriginal => ({
  ...await importOriginal<typeof import('@/lib/browserLiveWs')>(),
  BrowserLiveWsConnection: vi.fn().mockImplementation(function (_session: string, _agent: string, callbacks: BrowserLiveWsCallbacks) {
    callbacksRef.current = callbacks
    return { connect: vi.fn(), close: vi.fn(), detach: vi.fn(), sendInput, sendControl: vi.fn(() => true), sendTabAction: vi.fn(() => true), sendViewport: vi.fn(() => true), sendWebRTCOffer: vi.fn(() => true) }
  }),
}))
import { BrowserLiveView } from './BrowserLiveView'
installBrowserFrameCallbacks()
beforeEach(() => { sendInput.mockClear(); useUiStore.setState({ toasts: [] }) })

function connect(onRender: () => void = () => {}) {
  render(<Profiler id="browser" onRender={onRender}><BrowserLiveView sessionId="s1" agentId="a1" mediaStream={{ id: 'peer-stream' } as MediaStream} /></Profiler>)
  act(() => callbacksRef.current!.onConnected?.())
  const video = screen.getByTestId('browser-live-video') as HTMLVideoElement
  Object.defineProperty(video, 'videoWidth', { configurable: true, value: 1280 })
  Object.defineProperty(video, 'videoHeight', { configurable: true, value: 720 })
  fireEvent.loadedMetadata(video)
  return video
}
function health(captureId: string, generation: number, marker?: number, cssWidth = 1280, cssHeight = 720) {
  act(() => callbacksRef.current!.onVideoHealth({
    type: 'browser_video_health', session_id: 's1', state: marker === undefined ? 'transitioning' : 'recovered',
    capture_id: captureId, capture_generation: generation, css_width: cssWidth, css_height: cssHeight, ...(marker === undefined ? {} : { rtp_timestamp: marker }),
  }))
}
function key() { fireEvent.keyDown(screen.getByTestId('browser-live-frame'), { key: 'a' }) }

describe('BrowserLiveView displayed capture identity', () => {
  it('requires boundary and actual presented frame and sends their exact identity', () => {
    const video = connect()
    key()
    expect(sendInput.mock.calls).toEqual([])
    health('capture-a', 1, 100)
    key()
    expect(sendInput.mock.calls).toEqual([])
    act(() => emitBrowserFrame(video, { rtpTimestamp: 100, expectedDisplayTime: performance.now() - 1 }))
    key()
    expect(sendInput.mock.calls).toEqual([[{ kind: 'text', text: 'a', modifiers: 0, capture_id: 'capture-a', capture_generation: 1 }]])
  })

  it('locks synchronously on a same-size target transition and ignores older frames', () => {
    const video = connect()
    health('capture-a', 1, 100)
    act(() => emitBrowserFrame(video, { rtpTimestamp: 100, expectedDisplayTime: performance.now() - 1 }))
    health('capture-a', 2)
    key()
    expect(sendInput.mock.calls).toEqual([])
    health('capture-a', 2, 200)
    act(() => emitBrowserFrame(video, { rtpTimestamp: 199, expectedDisplayTime: performance.now() - 1 }))
    key()
    expect(sendInput.mock.calls).toEqual([])
    act(() => emitBrowserFrame(video, { rtpTimestamp: 200, expectedDisplayTime: performance.now() - 1 }))
    key()
    expect(sendInput.mock.calls).toEqual([[{ kind: 'text', text: 'a', modifiers: 0, capture_id: 'capture-a', capture_generation: 2 }]])
  })

  it('does not authorize a replacement capture using the previous capture frame', () => {
    const video = connect()
    health('capture-a', 1, 100)
    act(() => emitBrowserFrame(video, { rtpTimestamp: 100, expectedDisplayTime: performance.now() - 1 }))
    health('capture-b', 1, 100)
    key()
    expect(sendInput.mock.calls).toEqual([])
  })

  it('keeps input locked until the compositor display time even after a matching callback', () => {
    vi.useFakeTimers()
    try {
      vi.setSystemTime(1000)
      const video = connect()
      health('capture-a', 1, 0)
      const displayAt = performance.now() + 50
      act(() => emitBrowserFrame(video, { rtpTimestamp: 0, expectedDisplayTime: displayAt }))
      key()
      expect(sendInput.mock.calls).toEqual([])
      act(() => vi.advanceTimersByTime(49))
      key()
      expect(sendInput.mock.calls).toEqual([])
      act(() => vi.advanceTimersByTime(1))
      key()
      expect(sendInput.mock.calls).toEqual([[{ kind: 'text', text: 'a', modifiers: 0, capture_id: 'capture-a', capture_generation: 1 }]])
    } finally { vi.useRealTimers() }
  })
  it('cancels queued CSS positions before accepting a new recovered generation', () => {
    vi.useFakeTimers()
    try {
      const video = connect()
      health('capture-a', 1, 100)
      act(() => vi.advanceTimersByTime(1))
      act(() => emitBrowserFrame(video, { rtpTimestamp: 100, expectedDisplayTime: performance.now() }))
      const frame = screen.getByTestId('browser-live-frame')
      vi.spyOn(frame, 'getBoundingClientRect').mockReturnValue({ left: 0, top: 0, width: 1280, height: 720, right: 1280, bottom: 720, x: 0, y: 0, toJSON: () => ({}) })
      fireEvent.pointerMove(frame, { clientX: 100, clientY: 100 })
      expect(sendInput.mock.calls).toEqual([])
      // The recovered event alone changes the target/geometry generation.
      health('capture-a', 2, 200)
      act(() => emitBrowserFrame(video, { rtpTimestamp: 200, expectedDisplayTime: performance.now() }))
      act(() => vi.advanceTimersByTime(60))
      expect(sendInput.mock.calls).toEqual([])
      fireEvent.pointerMove(frame, { clientX: 200, clientY: 200 })
      act(() => vi.advanceTimersByTime(60))
      expect(sendInput.mock.calls).toEqual([[{ kind: 'mouse_move', x: 200, y: 200, modifiers: 0, capture_id: 'capture-a', capture_generation: 2 }]])
    } finally { vi.useRealTimers() }
  })

  it('does not rerender the React view for already authorized frames', () => {
    const onRender = vi.fn()
    const video = connect(onRender)
    health('capture-a', 1, 100)
    act(() => emitBrowserFrame(video, { rtpTimestamp: 100, expectedDisplayTime: performance.now() - 1 }))
    onRender.mockClear()
    act(() => emitBrowserFrame(video, { rtpTimestamp: 101, expectedDisplayTime: performance.now() - 1 }))
    expect(onRender.mock.calls).toEqual([])
  })

  it('locks input on a loss event even when the server omits optional capture identity', () => {
    const video = connect()
    health('capture-a', 1, 100)
    act(() => emitBrowserFrame(video, { rtpTimestamp: 100, expectedDisplayTime: performance.now() - 1 }))
    act(() => callbacksRef.current!.onVideoHealth({ type: 'browser_video_health', session_id: 's1', state: 'lost' }))
    key()
    expect(sendInput.mock.calls).toEqual([])
    health('capture-a', 1, 101)
    key()
    expect(sendInput.mock.calls).toEqual([])
    act(() => emitBrowserFrame(video, { rtpTimestamp: 101, expectedDisplayTime: performance.now() - 1 }))
    key()
    expect(sendInput.mock.calls).toEqual([[{ kind: 'text', text: 'a', modifiers: 0, capture_id: 'capture-a', capture_generation: 1 }]])
  })

  it('locks a lost feed until a recovered boundary and a new frame are presented', () => {
    const video = connect()
    health('capture-a', 1, 100)
    act(() => emitBrowserFrame(video, { rtpTimestamp: 100, expectedDisplayTime: performance.now() - 1 }))
    act(() => callbacksRef.current!.onVideoHealth({ type: 'browser_video_health', session_id: 's1', state: 'lost', capture_id: 'capture-a', capture_generation: 1 }))
    key()
    expect(sendInput.mock.calls).toEqual([])
    act(() => emitBrowserFrame(video, { rtpTimestamp: 199, expectedDisplayTime: performance.now() - 1 }))
    key()
    expect(sendInput.mock.calls).toEqual([])
    health('capture-a', 1, 200)
    key()
    expect(sendInput.mock.calls).toEqual([])
    act(() => emitBrowserFrame(video, { rtpTimestamp: 200, expectedDisplayTime: performance.now() - 1 }))
    key()
    expect(sendInput.mock.calls).toEqual([[{ kind: 'text', text: 'a', modifiers: 0, capture_id: 'capture-a', capture_generation: 1 }]])
  })

  it('states that input is unavailable when the browser has no frame callbacks', () => {
    Reflect.deleteProperty(HTMLVideoElement.prototype, 'requestVideoFrameCallback')
    connect()
    health('capture-a', 1, 100)
    key()
    expect(sendInput.mock.calls).toEqual([])
    expect(screen.getByText('Browser input is unavailable because this browser cannot confirm displayed video frames.')).toBeInTheDocument()
  })

  it('never substitutes a timeout for a missing compositor presentation time', () => {
    const video = connect()
    health('capture-a', 1, 100)
    act(() => emitBrowserFrame(video, { rtpTimestamp: 100 }))
    key()
    expect(sendInput.mock.calls).toEqual([])
    expect(screen.getByText('Browser input is unavailable because this browser cannot confirm displayed video frames.')).toBeInTheDocument()
  })

  it('does not carry displayed authorization into a different mounted session', () => {
    const stream = { id: 'supplied-stream' } as MediaStream
    const view = render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={stream} />)
    act(() => callbacksRef.current!.onConnected?.())
    const video = screen.getByTestId('browser-live-video') as HTMLVideoElement
    health('capture-a', 1, 100)
    act(() => emitBrowserFrame(video, { rtpTimestamp: 100, expectedDisplayTime: performance.now() - 1 }))
    view.rerender(<BrowserLiveView sessionId="s2" agentId="a1" mediaStream={stream} />)
    act(() => callbacksRef.current!.onConnected?.())
    key()
    expect(sendInput.mock.calls).toEqual([])
  })

  it('ignores both padding layers, sends CSS pixels, and releases a held pointer at its actual outside point', () => {
    const video = connect()
    Object.defineProperty(video, 'videoWidth', { configurable: true, value: 800 })
    Object.defineProperty(video, 'videoHeight', { configurable: true, value: 400 })
    const container = screen.getByTestId('browser-live-frame')
    vi.spyOn(container, 'getBoundingClientRect').mockReturnValue({ left: 100, top: 50, width: 800, height: 600, right: 900, bottom: 650, x: 100, y: 50, toJSON: () => ({}) })
    health('capture-a', 1, 100, 600, 400)
    act(() => emitBrowserFrame(video, { rtpTimestamp: 100, expectedDisplayTime: performance.now() - 1 }))
    fireEvent.pointerDown(container, { clientX: 150, clientY: 350, button: 0 })
    fireEvent.pointerUp(container, { clientX: 150, clientY: 350, button: 0 })
    expect(sendInput.mock.calls).toEqual([])
    fireEvent.pointerDown(container, { clientX: 500, clientY: 350, button: 0 })
    fireEvent.pointerUp(container, { clientX: 150, clientY: 350, button: 0 })
    expect(sendInput.mock.calls).toEqual([
      [{ kind: 'mouse_down', x: 300, y: 200, button: 'left', modifiers: 0, capture_id: 'capture-a', capture_generation: 1 }],
      [{ kind: 'mouse_up', x: -50, y: 200, button: 'left', modifiers: 0, capture_id: 'capture-a', capture_generation: 1 }],
    ])
  })

  it('requires confirmed CSS geometry for pointer input while allowing proven keyboard input', () => {
    const video = connect()
    const container = screen.getByTestId('browser-live-frame')
    vi.spyOn(container, 'getBoundingClientRect').mockReturnValue({ left: 0, top: 0, width: 1280, height: 720, right: 1280, bottom: 720, x: 0, y: 0, toJSON: () => ({}) })
    act(() => callbacksRef.current!.onVideoHealth({ type: 'browser_video_health', session_id: 's1', state: 'recovered', capture_id: 'capture-a', capture_generation: 1, rtp_timestamp: 100 }))
    act(() => emitBrowserFrame(video, { rtpTimestamp: 100, expectedDisplayTime: performance.now() - 1 }))
    fireEvent.pointerDown(container, { clientX: 100, clientY: 100, button: 0 })
    expect(sendInput.mock.calls).toEqual([])
    key()
    expect(sendInput.mock.calls).toEqual([[{ kind: 'text', text: 'a', modifiers: 0, capture_id: 'capture-a', capture_generation: 1 }]])
  })

  it('surfaces an operation-only failure without altering the healthy picture or input authorization', () => {
    const video = connect()
    health('capture-a', 1, 100)
    act(() => emitBrowserFrame(video, { rtpTimestamp: 100, expectedDisplayTime: performance.now() - 1 }))
    act(() => callbacksRef.current!.onStatus({ type: 'browser_status', state: 'error', operation_only: true, message: 'The page changed before this input arrived.' }))
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(useUiStore.getState().toasts.map(toast => toast.message)).toEqual(['The page changed before this input arrived.'])
    key()
    expect(sendInput.mock.calls).toEqual([[{ kind: 'text', text: 'a', modifiers: 0, capture_id: 'capture-a', capture_generation: 1 }]])
  })

})

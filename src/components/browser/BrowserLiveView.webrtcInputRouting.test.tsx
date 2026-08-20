// BrowserLiveView.webrtcInputRouting.test.tsx — WebRTC build (wave-plan.md
// W2-B) signaling wiring + the DC-vs-WS input routing rule.
//
// Mocks BOTH `@/lib/browserLiveWs` (same technique as
// BrowserLiveView.webrtcSink.test.tsx / .takeTheWheel.test.tsx) AND
// `@/lib/browserWebRTC` (the state machine is unit-tested on its own in
// browserWebRTC.test.ts — here we only need a thin double that records calls
// and lets the test fire its registered callbacks). This isolates exactly
// what THIS component is responsible for: wiring browser_webrtc_state/answer
// frames into the machine, and choosing DC vs WS per input event.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { act } from 'react'
import type { BrowserLiveWsCallbacks } from '@/lib/browserLiveWs'
import { useUiStore } from '@/store/ui'

const {
  mockSendInput,
  mockSendControl,
  mockSendTabAction,
  mockSendWebRTCOffer,
  wsCallbacksRef,
  mockMachineSendInput,
  machineRetryAttemptsRef,
  mockMachineStart,
  mockMachineApplyAnswer,
  mockMachineApplyState,
  mockMachineStop,
  machineCallbacksRef,
  machineHasConnectedOnceRef,
  machineStateRef,
} = vi.hoisted(() => ({
  mockSendInput: vi.fn(() => true),
  mockSendControl: vi.fn(() => true),
  mockSendTabAction: vi.fn(() => true),
  mockSendWebRTCOffer: vi.fn(() => true),
  wsCallbacksRef: { current: null as BrowserLiveWsCallbacks | null },
  mockMachineSendInput: vi.fn((_json: string) => {
    void _json // present only to give the mock the real call-argument type it's asserted against below
    return true
  }),
  mockMachineStart: vi.fn(),
  mockMachineApplyAnswer: vi.fn(),
  mockMachineApplyState: vi.fn(),
  mockMachineStop: vi.fn(),
  machineCallbacksRef: {
    current: {
      onStream: null as ((s: MediaStream) => void) | null,
      onInputChannelOpen: null as (() => void) | null,
      onInputChannelClose: null as (() => void) | null,
      onFallback: null as ((r: string) => void) | null,
    },
  },
  machineRetryAttemptsRef: { current: 0 },
  // fix-wave (MED): backs the mocked machine's `hasConnectedOnce` getter —
  // BrowserLiveView.tsx reads this to decide whether an 'answer-timeout'
  // fallback is a cold-start false positive (never connected, stay quiet)
  // or a genuine degradation (connected before, warn). Defaults false
  // (cold start) — individual tests flip it true to exercise the
  // already-connected path. Reset in beforeEach below.
  machineHasConnectedOnceRef: { current: false },
  // F1 fix coverage (external review, 2026-08-13): backs the mocked
  // machine's `state` getter — BrowserLiveView.tsx's `onWebRTCState` handler
  // now reads this directly to cover the gap `applyState` deliberately
  // leaves uncovered (a capability-gate `available:false` arriving while the
  // real machine is still `idle`, before `start()` has ever run). Defaults
  // to 'idle', matching a freshly-constructed real BrowserWebRTCSession.
  // Individual tests override it to exercise the offering/connected/fallback
  // branches. Reset in beforeEach below.
  machineStateRef: { current: 'idle' as 'idle' | 'offering' | 'connected' | 'fallback' },
}))

// D5: importOriginal so the real translateBrowserErrorMessage (now imported
// by BrowserLiveView for the D5 fix) stays live under this mock — only
// BrowserLiveWsConnection itself is replaced.
vi.mock('@/lib/browserLiveWs', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/browserLiveWs')>()
  return {
    ...actual,
    BrowserLiveWsConnection: vi.fn().mockImplementation(
      function (_sessionId: string, _agentId: string, callbacks: BrowserLiveWsCallbacks) {
        wsCallbacksRef.current = callbacks
        return {
          connect: vi.fn(),
          detach: vi.fn(),
          close: vi.fn(),
          sendInput: mockSendInput,
          sendControl: mockSendControl,
          sendTabAction: mockSendTabAction,
          // Adaptive viewport (2026-07-31): BrowserLiveView's ResizeObserver
          // calls this on mount, so every connection double needs it.
          sendViewport: vi.fn(() => true),
          sendWebRTCOffer: mockSendWebRTCOffer,
          isConnected: true,
        }
      },
    ),
  }
})

// importOriginal so the real translateWebRTCFallbackReason (used by
// BrowserLiveView to turn a fallback reason into the honest, actionable
// message it displays) stays live under this mock — only BrowserWebRTCSession
// itself is replaced.
vi.mock('@/lib/browserWebRTC', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/browserWebRTC')>()
  return {
  ...actual,
  BrowserWebRTCSession: vi.fn().mockImplementation(function () {
    return {
      start: mockMachineStart,
      applyAnswer: mockMachineApplyAnswer,
      applyState: mockMachineApplyState,
      stop: mockMachineStop,
      sendInput: mockMachineSendInput,
      get hasConnectedOnce() {
        return machineHasConnectedOnceRef.current
      },
      get state() {
        return machineStateRef.current
      },
      // Cold-start toast suppression now also requires this to be 0 (i.e.
      // the FIRST attempt). Suppressing every retry meant a total WebRTC
      // failure produced no user-facing explanation at all. 0 keeps this
      // double on the first-attempt path these tests exercise.
      get retryAttempts() {
        return machineRetryAttemptsRef.current
      },
      onStream: (cb: (s: MediaStream) => void) => {
        machineCallbacksRef.current.onStream = cb
      },
      onInputChannelOpen: (cb: () => void) => {
        machineCallbacksRef.current.onInputChannelOpen = cb
      },
      onInputChannelClose: (cb: () => void) => {
        machineCallbacksRef.current.onInputChannelClose = cb
      },
      onFallback: (cb: (r: string) => void) => {
        machineCallbacksRef.current.onFallback = cb
      },
    }
  }),
  }
})

import { BrowserLiveView } from './BrowserLiveView'

/** Stand-in MediaStream — jsdom has no real WebRTC/MediaStream (see
 * BrowserLiveView.webrtcSink.test.tsx's own note). */
function fakeMediaStream(id = 'stream-1'): MediaStream {
  return { id } as unknown as MediaStream
}

function connectAndFrame() {
  act(() => {
    wsCallbacksRef.current?.onConnected?.()
  })
}

function stubFrameRect() {
  const container = screen.getByTestId('browser-live-frame')
  vi.spyOn(container, 'getBoundingClientRect').mockReturnValue({
    left: 0, top: 0, width: 1280, height: 720, right: 1280, bottom: 720, x: 0, y: 0,
    toJSON() { return {} },
  } as DOMRect)
  return container
}

function stubVideoDims() {
  const video = screen.getByTestId('browser-live-video') as HTMLVideoElement
  Object.defineProperty(video, 'videoWidth', { value: 1280, configurable: true })
  Object.defineProperty(video, 'videoHeight', { value: 720, configurable: true })
  return video
}

beforeEach(() => {
  vi.clearAllMocks()
  wsCallbacksRef.current = null
  machineCallbacksRef.current = { onStream: null, onInputChannelOpen: null, onInputChannelClose: null, onFallback: null }
  machineHasConnectedOnceRef.current = false
  machineStateRef.current = 'idle'
  useUiStore.setState({ toasts: [] })
})

/** Take the wheel via an implicit-drive click AND land the server's
 * 'controlling' ack, then clear the send mocks — so the assertions that
 * follow exercise an ORDINARY acked-driving gesture. Needed because the
 * implicit-take gesture itself deliberately rides the WS regardless of DC
 * state (UAT 2026-07-18: the DC has no ordering guarantee against the WS
 * `browser_control{take}` frame — see dispatchInput's forceWs doc comment),
 * so DC-routing behavior is only observable on a LATER gesture. */
function ackDriving(container: HTMLElement) {
  fireEvent.pointerDown(container, { clientX: 10, clientY: 10 })
  fireEvent.pointerUp(container, { clientX: 10, clientY: 10 })
  act(() => wsCallbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'controlling' }))
  mockMachineSendInput.mockClear()
  mockSendInput.mockClear()
  mockSendControl.mockClear()
}

describe('BrowserLiveView — input routing: data channel vs WS (WebRTC build W2-B)', () => {
  it('DC not open: pointer input falls back to WS', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    // Never fire onInputChannelOpen — the DC never reports open.
    const container = stubFrameRect()
    stubVideoDims()
    ackDriving(container)

    fireEvent.pointerDown(container, { clientX: 10, clientY: 10 })

    expect(mockMachineSendInput).not.toHaveBeenCalled()
    expect(mockSendInput).toHaveBeenCalledWith(expect.objectContaining({ kind: 'mouse_down' }))
  })

  it('video mode, DC open, acked driving: mouse_down goes over the data channel as a JSON-serialized BrowserInputFrame, not WS', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    act(() => machineCallbacksRef.current.onInputChannelOpen?.())
    const container = stubFrameRect()
    stubVideoDims()
    ackDriving(container)

    fireEvent.pointerDown(container, { clientX: 10, clientY: 10 })

    expect(mockSendInput).not.toHaveBeenCalled()
    expect(mockMachineSendInput).toHaveBeenCalledTimes(1)
    const payload = JSON.parse(mockMachineSendInput.mock.calls[0][0] as string)
    expect(payload).toEqual(expect.objectContaining({ type: 'browser_input', kind: 'mouse_down', x: 10, y: 10 }))
  })

  it('video mode, DC open: key/text input also goes over the data channel', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    act(() => machineCallbacksRef.current.onInputChannelOpen?.())
    const container = stubFrameRect()
    stubVideoDims()
    // Take the wheel first (idle → you-driving) via an implicit-drive click.
    ackDriving(container)

    fireEvent.keyDown(container, { key: 'a' })

    expect(mockSendInput).not.toHaveBeenCalled()
    expect(mockMachineSendInput).toHaveBeenCalledTimes(1)
    const payload = JSON.parse(mockMachineSendInput.mock.calls[0][0] as string)
    expect(payload).toEqual(expect.objectContaining({ type: 'browser_input', kind: 'text', text: 'a' }))
  })

  it('video mode, DC open, but the DC send itself fails: falls through to WS rather than dropping the event', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    act(() => machineCallbacksRef.current.onInputChannelOpen?.())
    const container = stubFrameRect()
    stubVideoDims()
    ackDriving(container)
    mockMachineSendInput.mockReturnValueOnce(false)

    fireEvent.pointerDown(container, { clientX: 10, clientY: 10 })

    expect(mockMachineSendInput).toHaveBeenCalledTimes(1)
    expect(mockSendInput).toHaveBeenCalledWith(expect.objectContaining({ kind: 'mouse_down' }))
  })

  it('video mode, DC open then closed mid-session: input reverts to WS the moment onInputChannelClose fires', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    act(() => machineCallbacksRef.current.onInputChannelOpen?.())
    act(() => machineCallbacksRef.current.onInputChannelClose?.())
    const container = stubFrameRect()
    stubVideoDims()
    ackDriving(container)

    fireEvent.pointerDown(container, { clientX: 10, clientY: 10 })

    expect(mockMachineSendInput).not.toHaveBeenCalled()
    expect(mockSendInput).toHaveBeenCalledWith(expect.objectContaining({ kind: 'mouse_down' }))
  })

  it('implicit-take gesture (UAT 2026-07-18): with the DC open, the WHOLE acquiring gesture — mouse_down, coalesced moves, mouse_up — rides the WS so it can never race ahead of the browser_control{take} frame; the NEXT acked gesture uses the DC', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    act(() => machineCallbacksRef.current.onInputChannelOpen?.())
    const container = stubFrameRect()
    stubVideoDims()

    // Gesture 1 — implicit take while idle. Everything rides WS, DC untouched.
    fireEvent.pointerDown(container, { clientX: 10, clientY: 10 })
    fireEvent.pointerUp(container, { clientX: 12, clientY: 12 })
    expect(mockSendControl).toHaveBeenCalledWith('take')
    expect(mockMachineSendInput).not.toHaveBeenCalled()
    expect(mockSendInput).toHaveBeenCalledWith(expect.objectContaining({ kind: 'mouse_down' }))
    expect(mockSendInput).toHaveBeenCalledWith(expect.objectContaining({ kind: 'mouse_up' }))

    // Ack lands; gesture 2 is ordinary acked driving — DC-first again.
    act(() => wsCallbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'controlling' }))
    mockMachineSendInput.mockClear()
    mockSendInput.mockClear()
    fireEvent.pointerDown(container, { clientX: 20, clientY: 20 })
    expect(mockSendInput).not.toHaveBeenCalled()
    expect(mockMachineSendInput).toHaveBeenCalledTimes(1)
    expect(JSON.parse(mockMachineSendInput.mock.calls[0][0] as string)).toEqual(
      expect.objectContaining({ kind: 'mouse_down' }),
    )
  })
})

describe('BrowserLiveView — control/navigate/tab-action always ride WS, even in video mode with the DC open (WebRTC build W2-B)', () => {
  it('the omnibox submit (navigate) never touches the data channel', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    act(() => machineCallbacksRef.current.onInputChannelOpen?.())

    const addressInput = screen.getByRole('textbox', { name: /address bar/i })
    fireEvent.change(addressInput, { target: { value: 'example.com' } })
    const form = addressInput.closest('form')
    expect(form).not.toBeNull()
    fireEvent.submit(form!)

    expect(mockMachineSendInput).not.toHaveBeenCalled()
    expect(mockSendInput).toHaveBeenCalledWith(expect.objectContaining({ kind: 'navigate', url: 'https://example.com' }))
  })

  it('Back/Refresh toolbar actions never touch the data channel', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    act(() => machineCallbacksRef.current.onInputChannelOpen?.())

    fireEvent.click(screen.getByRole('button', { name: /refresh page/i }))

    expect(mockMachineSendInput).not.toHaveBeenCalled()
    expect(mockSendInput).toHaveBeenCalledWith(expect.objectContaining({ kind: 'reload' }))
  })

  it('take/release control frames never touch the data channel — and the pointer input riding alongside the implicit take rides the SAME WS (ordering guarantee, UAT 2026-07-18)', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    act(() => machineCallbacksRef.current.onInputChannelOpen?.())
    const container = stubFrameRect()
    stubVideoDims()

    // The first pointerdown while idle implicitly takes the wheel — this
    // sends `browser_control{take}` on WS (sendControl is a wholly separate
    // method from dispatchInput; there is no code path by which it could
    // ever reach the DC) AND dispatches the mouse_down itself over the SAME
    // WS (dispatchInput forceWs): the DC has no ordering guarantee relative
    // to the WS, so a DC-routed first click could reach the server before
    // the take and be dropped as not-controlling.
    fireEvent.pointerDown(container, { clientX: 10, clientY: 10 })

    expect(mockSendControl).toHaveBeenCalledWith('take')
    expect(mockMachineSendInput).not.toHaveBeenCalled()
    expect(mockSendInput).toHaveBeenCalledWith(expect.objectContaining({ kind: 'mouse_down' }))
  })
})

// Fault 3 fix (docs/internal/browser-viewport-input-rootcause-2026-07-31.md):
// the video sink's intrinsic size can silently drift from the page's real CSS
// pixel space once the encoder downscales under load (measured 319x158 vs
// ~1280 page) — coordinate-carrying input frames must report the capture
// geometry they were mapped into so the server can rescale instead of
// assuming videoWidth == page pixels.
describe('BrowserLiveView — capture_width/capture_height on coordinate-carrying input (Fault 3 fix)', () => {
  it('mouse_down carries capture_width/capture_height equal to the mocked video sink intrinsic size', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    act(() => machineCallbacksRef.current.onInputChannelOpen?.())
    const container = stubFrameRect()
    stubVideoDims() // videoWidth/videoHeight 1280/720
    ackDriving(container)

    fireEvent.pointerDown(container, { clientX: 10, clientY: 10 })

    expect(mockSendInput).not.toHaveBeenCalled()
    expect(mockMachineSendInput).toHaveBeenCalledTimes(1)
    const payload = JSON.parse(mockMachineSendInput.mock.calls[0][0] as string)
    expect(payload).toEqual(
      expect.objectContaining({ kind: 'mouse_down', capture_width: 1280, capture_height: 720 }),
    )
  })

  it('mouse_down never dispatches at all before the video reports real dimensions (no fallback sink to carry the click)', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    const container = stubFrameRect()
    // Deliberately do not call stubVideoDims() — videoWidth/videoHeight stay 0.

    fireEvent.pointerDown(container, { clientX: 10, clientY: 10 })

    expect(mockSendInput).not.toHaveBeenCalled()
    expect(mockMachineSendInput).not.toHaveBeenCalled()
  })

  // Wheel is COALESCED onto the shared input pacer (deltas accumulated,
  // position = latest), so it no longer dispatches synchronously. The dims
  // must survive that deferral: they are captured at the wheel event that
  // computed x/y, not re-read from a possibly-since-drifted video element when
  // the flush fires.
  it('video mode: wheel frames also carry capture_width/capture_height', async () => {
    vi.useFakeTimers()
    try {
      render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
      connectAndFrame()
      act(() => machineCallbacksRef.current.onInputChannelOpen?.())
      const container = stubFrameRect()
      stubVideoDims()
      ackDriving(container)
      mockMachineSendInput.mockClear()

      fireEvent.wheel(container, { deltaX: 0, deltaY: 120, clientX: 10, clientY: 10 })
      await vi.advanceTimersByTimeAsync(60)

      const wheels = mockMachineSendInput.mock.calls
        .map((c) => JSON.parse(c[0] as string) as Record<string, unknown>)
        .filter((p) => p.kind === 'wheel')
      expect(wheels).toHaveLength(1)
      expect(wheels[0]).toEqual(
        expect.objectContaining({ kind: 'wheel', capture_width: 1280, capture_height: 720 }),
      )
    } finally {
      vi.useRealTimers()
    }
  })

  // The point of coalescing: a burst becomes ONE send whose deltas sum, so
  // pacing costs resolution in time but never scroll distance. An un-paced
  // wheel stream (a trackpad emits at display refresh rate) was overrunning the
  // server's per-second input budget on its own, and the click that followed
  // the scroll was the event that got dropped.
  it('video mode: a wheel burst coalesces into one frame with summed deltas', async () => {
    vi.useFakeTimers()
    try {
      render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
      connectAndFrame()
      act(() => machineCallbacksRef.current.onInputChannelOpen?.())
      const container = stubFrameRect()
      stubVideoDims()
      ackDriving(container)
      mockMachineSendInput.mockClear()

      for (let i = 0; i < 10; i++) {
        fireEvent.wheel(container, { deltaX: 2, deltaY: 12, clientX: 10, clientY: 10 })
      }
      await vi.advanceTimersByTimeAsync(60)

      const wheels = mockMachineSendInput.mock.calls
        .map((c) => JSON.parse(c[0] as string) as Record<string, unknown>)
        .filter((p) => p.kind === 'wheel')
      expect(wheels).toHaveLength(1)
      expect(wheels[0].delta_y).toBe(120)
      expect(wheels[0].delta_x).toBe(20)
    } finally {
      vi.useRealTimers()
    }
  })

  it('video mode: key_down frames never carry capture_width/capture_height (no x/y to correct)', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    act(() => machineCallbacksRef.current.onInputChannelOpen?.())
    const container = stubFrameRect()
    stubVideoDims()
    ackDriving(container)

    // A non-printable key (length > 1) takes the `key_down` branch, not the
    // one-shot `text` insert isPrintableKey routes single characters to.
    fireEvent.keyDown(container, { key: 'Tab' })

    expect(mockMachineSendInput).toHaveBeenCalledTimes(1)
    const payload = JSON.parse(mockMachineSendInput.mock.calls[0][0] as string) as Record<string, unknown>
    expect(payload.kind).toBe('key_down')
    expect(payload).not.toHaveProperty('capture_width')
    expect(payload).not.toHaveProperty('capture_height')
  })

  // Coalesced mouse_move is the one dispatch site that doesn't send
  // synchronously — the capture dims must be captured at the pointermove
  // event that computed x/y, not re-read from the (possibly-since-drifted)
  // live video element when the deferred flush actually fires. Forces the
  // setTimeout(0) fallback (document hidden) the same way
  // BrowserLiveView.mouseMoveThrottle.test.tsx does, since jsdom's RAF isn't
  // deterministically triggerable via fake timers.
  it('video mode: a coalesced mouse_move flush still carries the capture dims read at the ORIGINAL pointermove, even if the video sink has since resized', () => {
    // Local, not restoreAllMocks — this file's `vi.fn(() => true)` doubles
    // (mockSendInput et al.) are shared module-level state across every test
    // in this file; restoreAllMocks would strip their default implementation
    // and break every test that runs after this one.
    const visibilitySpy = vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden')
    vi.useFakeTimers()
    try {
      render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
      connectAndFrame()
      act(() => machineCallbacksRef.current.onInputChannelOpen?.())
      const container = stubFrameRect()
      const video = stubVideoDims()
      ackDriving(container)

      act(() => {
        fireEvent.pointerMove(container, { clientX: 10, clientY: 10 })
      })
      // The encoder rebuilds the stream mid-gesture (Fault 2/3) — the live
      // element now reports a different intrinsic size before the coalesced
      // flush fires.
      Object.defineProperty(video, 'videoWidth', { value: 320, configurable: true })
      Object.defineProperty(video, 'videoHeight', { value: 160, configurable: true })

      act(() => {
        vi.runAllTimers()
      })

      expect(mockMachineSendInput).toHaveBeenCalledTimes(1)
      const payload = JSON.parse(mockMachineSendInput.mock.calls[0][0] as string)
      expect(payload).toEqual(
        expect.objectContaining({ kind: 'mouse_move', capture_width: 1280, capture_height: 720 }),
      )
    } finally {
      vi.useRealTimers()
      visibilitySpy.mockRestore()
    }
  })
})

describe('BrowserLiveView — WebRTC signaling wiring (WebRTC build W2-B)', () => {
  it('calls machine.start on browser_webrtc_state{available:true}, and the sendOffer callback it receives sends the SDP over WS as browser_webrtc_offer', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()

    act(() => {
      wsCallbacksRef.current?.onWebRTCState?.({ type: 'browser_webrtc_state', available: true, has_audio: true })
    })

    expect(mockMachineApplyState).toHaveBeenCalledWith({ type: 'browser_webrtc_state', available: true, has_audio: true })
    expect(mockMachineStart).toHaveBeenCalledTimes(1)
    const sendOfferFn = mockMachineStart.mock.calls[0][0] as (sdp: string) => void
    sendOfferFn('fake-sdp')
    expect(mockSendWebRTCOffer).toHaveBeenCalledWith('fake-sdp')
  })

  it('fix-wave B (MED): the sendOffer callback propagates sendWebRTCOffer\'s boolean return — true on success, false on failure — so browserWebRTC.ts can fall back immediately instead of waiting out the answer timeout', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()

    act(() => {
      wsCallbacksRef.current?.onWebRTCState?.({ type: 'browser_webrtc_state', available: true, has_audio: true })
    })

    const sendOfferFn = mockMachineStart.mock.calls[0][0] as (sdp: string) => boolean

    expect(sendOfferFn('sdp-ok')).toBe(true) // mockSendWebRTCOffer defaults to () => true

    mockSendWebRTCOffer.mockReturnValueOnce(false)
    expect(sendOfferFn('sdp-fails')).toBe(false)
  })

  it('does not call machine.start on browser_webrtc_state{available:false} — applyState alone handles that', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()

    act(() => {
      wsCallbacksRef.current?.onWebRTCState?.({ type: 'browser_webrtc_state', available: false, reason: 'lite_build' })
    })

    expect(mockMachineApplyState).toHaveBeenCalledWith({ type: 'browser_webrtc_state', available: false, reason: 'lite_build' })
    expect(mockMachineStart).not.toHaveBeenCalled()
  })

  // Bugfix (HIGH, external review F1, 2026-08-13): `applyState` (mocked
  // above) is documented to react ONLY while the real machine is
  // offering/connected — a capability-gate `available:false` arriving at
  // ATTACH time, before `start()` has ever run, left the machine `idle` and
  // the whole handler a no-op: no error ever surfaced, and the panel
  // silently sat on "Connecting…" until an unrelated timeout eventually fired
  // a wrong "stale tab" message. See BrowserLiveView.tsx's `onWebRTCState`
  // doc comment for the full trace.
  it('surfaces the real reason immediately when available:false arrives while the machine is still idle (never started)', () => {
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    machineStateRef.current = 'idle' // never started — the exact gap applyState leaves uncovered

    act(() => {
      wsCallbacksRef.current?.onWebRTCState?.({ type: 'browser_webrtc_state', available: false, reason: 'disabled' })
    })

    expect(warnSpy).toHaveBeenCalledWith('[browser-live] WebRTC failed:', 'disabled')
    expect(screen.getByText(/turned off for this installation/i)).toBeInTheDocument()
    warnSpy.mockRestore()
  })

  it("does NOT double-report when the machine is already offering/connected — applyState's own fallback already covers that case", () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    machineStateRef.current = 'connected'

    act(() => {
      wsCallbacksRef.current?.onWebRTCState?.({ type: 'browser_webrtc_state', available: false, reason: 'error' })
    })

    // applyState (mocked here, so it does not itself call onFallback) is the
    // sole responsible party while offering/connected; this proves the new
    // idle-covering branch does not ALSO fire and duplicate/race whatever
    // applyState's real onFallback would independently report.
    expect(screen.queryByText(/reported an error starting video/i)).not.toBeInTheDocument()
  })

  it('forwards browser_webrtc_answer.sdp to machine.applyAnswer', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()

    act(() => {
      wsCallbacksRef.current?.onWebRTCAnswer?.({ type: 'browser_webrtc_answer', sdp: 'answer-sdp' })
    })

    expect(mockMachineApplyAnswer).toHaveBeenCalledWith('answer-sdp')
  })

  it('renders the <video> sink once the machine reports a stream via onStream (no mediaStream prop override)', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    // Not attached yet — no second sink to fall back to while waiting.
    expect(screen.queryByTestId('browser-live-video')).not.toBeInTheDocument()
    expect(screen.queryByTestId('browser-live-frame')).not.toBeInTheDocument()

    act(() => machineCallbacksRef.current.onStream?.(fakeMediaStream()))

    expect(screen.getByTestId('browser-live-video')).toBeInTheDocument()
  })

  // Operator directive (JPEG-fallback removal) — WebRTC is the ONLY live-video
  // path now. A fallback no longer swaps to a second sink; it tears the
  // interactive surface down ENTIRELY (no silent degrade, no blank panel) and
  // the panel's empty state shows the honest error instead.
  it('on fallback, unmounts the interactive surface entirely and stops routing input over the DC (nothing left to click)', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    act(() => machineCallbacksRef.current.onStream?.(fakeMediaStream()))
    act(() => machineCallbacksRef.current.onInputChannelOpen?.())
    expect(screen.getByTestId('browser-live-video')).toBeInTheDocument()

    act(() => machineCallbacksRef.current.onFallback?.('ice-failed'))

    expect(screen.queryByTestId('browser-live-video')).not.toBeInTheDocument()
    expect(screen.queryByTestId('browser-live-frame')).not.toBeInTheDocument()
    // The honest failure reason is what's shown instead.
    expect(screen.getByText(/live video connection failed \(ice-failed\)/i)).toBeInTheDocument()

    expect(mockMachineSendInput).not.toHaveBeenCalled()
    expect(mockSendInput).not.toHaveBeenCalledWith(expect.objectContaining({ kind: 'mouse_down' }))
  })

  it('on WS disconnect, stops the machine and unmounts the interactive surface', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    act(() => machineCallbacksRef.current.onStream?.(fakeMediaStream()))
    expect(screen.getByTestId('browser-live-video')).toBeInTheDocument()

    act(() => wsCallbacksRef.current?.onDisconnected?.())

    expect(mockMachineStop).toHaveBeenCalledTimes(1)
    expect(screen.queryByTestId('browser-live-video')).not.toBeInTheDocument()
  })
})

// Operator directive (JPEG-fallback removal) — every `onFallback` reason,
// including the three that used to be silently suppressed as "capability
// gates" (JPEG carried on underneath, so there was nothing to tell the user),
// now surfaces as a persistent, honest, actionable error in the panel body —
// never a toast (which can auto-dismiss unnoticed), and never silence.
describe('BrowserLiveView — surfacing WebRTC fallback reasons (honest failure, no silent degrade)', () => {
  it.each(['ice-failed', 'ice-disconnected-timeout', 'offer-send-failed', 'stream-stopped', 'error', 'unavailable'])(
    'logs console.warn and shows a persistent, actionable error for the reason "%s"',
    (reason) => {
      const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
      render(<BrowserLiveView sessionId="s1" agentId="a1" />)
      connectAndFrame()

      act(() => machineCallbacksRef.current.onFallback?.(reason))

      expect(warnSpy).toHaveBeenCalledWith('[browser-live] WebRTC failed:', reason)
      // No toast — the failure is the panel's PRIMARY content, not an
      // ephemeral notification that could go unnoticed.
      expect(useUiStore.getState().toasts).toHaveLength(0)
      expect(screen.getByRole('button', { name: /retry/i })).toBeInTheDocument()
      warnSpy.mockRestore()
    },
  )

  it.each(['disabled', 'not_capable', 'lite_build'])(
    'surfaces a specific, non-retry-inviting explanation for the CAPABILITY-GATE reason "%s" (this mode genuinely is not available here)',
    (reason) => {
      const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
      render(<BrowserLiveView sessionId="s1" agentId="a1" />)
      connectAndFrame()

      act(() => machineCallbacksRef.current.onFallback?.(reason))

      // Still logged and still visible — the old "stay silent, JPEG carries
      // on" behavior is gone; there is no second sink left to carry on with.
      expect(warnSpy).toHaveBeenCalledWith('[browser-live] WebRTC failed:', reason)
      expect(screen.queryByTestId('browser-live-video')).not.toBeInTheDocument()
      warnSpy.mockRestore()
    },
  )

  it('unmounts the video sink for a capability-gate reason exactly like any other fallback reason', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    act(() => machineCallbacksRef.current.onStream?.(fakeMediaStream()))
    expect(screen.getByTestId('browser-live-video')).toBeInTheDocument()

    act(() => machineCallbacksRef.current.onFallback?.('lite_build'))

    expect(screen.queryByTestId('browser-live-video')).not.toBeInTheDocument()
    expect(screen.getByText(/lite build/i)).toBeInTheDocument()
  })

  it('maps each known reason to its own distinct, honest message', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()

    act(() => machineCallbacksRef.current.onFallback?.('disabled'))
    expect(screen.getByText(/turned off for this installation/i)).toBeInTheDocument()
  })

  it('clears the error and re-attempts signaling when the Retry button is clicked', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    act(() => machineCallbacksRef.current.onFallback?.('ice-failed'))
    expect(screen.getByText(/live video connection failed/i)).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /retry/i }))

    // F7 fix (external review, 2026-08-13): a genuine retry must tear the
    // stale session down FIRST — `start()` alone silently no-ops once the
    // machine is already past 'idle' (see the dedicated firstFrameTimedOut
    // coverage below for the case this actually mattered for in practice).
    expect(mockMachineStop).toHaveBeenCalled()
    expect(mockMachineStart).toHaveBeenCalled()
    expect(screen.queryByText(/live video connection failed/i)).not.toBeInTheDocument()
  })
})

// F7 fix (external review, 2026-08-13): the Retry button rendered for a
// `firstFrameTimedOut` failure (connected, stream attached, no frame ever
// decoded) used to be inert. `machine.start()` — the old retry body — is a
// documented no-op once the machine is already 'offering'/'connected', which
// is EXACTLY the state that failure leaves it in, so the click did nothing
// observable: no fresh negotiation attempt, and neither `firstFrameTimedOut`
// nor `webrtcError` was ever cleared.
describe('BrowserLiveView — Retry must actually retry for a firstFrameTimedOut failure, not just no-op (external review F7)', () => {
  it('stops the stale session before restarting, and clears the "No video received" message', () => {
    vi.useFakeTimers()
    try {
      render(<BrowserLiveView sessionId="s1" agentId="a1" />)
      connectAndFrame()
      act(() => machineCallbacksRef.current.onStream?.(fakeMediaStream()))
      // Deliberately never fire `loadedmetadata` — this is the
      // firstFrameTimedOut path (the machine reports a live stream via
      // onStream, i.e. NOT an onFallback reason), not a machine-level
      // failure.
      act(() => {
        vi.advanceTimersByTime(120_000)
      })
      expect(screen.getByText(/No video received/i)).toBeInTheDocument()

      fireEvent.click(screen.getByRole('button', { name: /retry/i }))

      // A genuine retry tears down the stale (already-connected-but-dead)
      // session before asking for a fresh one.
      expect(mockMachineStop).toHaveBeenCalled()
      expect(mockMachineStart).toHaveBeenCalled()
      const stopOrder = mockMachineStop.mock.invocationCallOrder[0]
      const startOrder = mockMachineStart.mock.invocationCallOrder[mockMachineStart.mock.invocationCallOrder.length - 1]
      expect(stopOrder).toBeLessThan(startOrder)
      expect(screen.queryByText(/No video received/i)).not.toBeInTheDocument()
      expect(screen.getByText('Waiting for the first frame…')).toBeInTheDocument()
    } finally {
      vi.useRealTimers()
    }
  })
})

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
  mockMachineStart,
  mockMachineApplyAnswer,
  mockMachineApplyState,
  mockMachineStop,
  machineCallbacksRef,
} = vi.hoisted(() => ({
  mockSendInput: vi.fn(() => true),
  mockSendControl: vi.fn(() => true),
  mockSendTabAction: vi.fn(() => true),
  mockSendWebRTCOffer: vi.fn(() => true),
  wsCallbacksRef: { current: null as BrowserLiveWsCallbacks | null },
  mockMachineSendInput: vi.fn((_json: string) => true),
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
}))

vi.mock('@/lib/browserLiveWs', () => ({
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
        sendWebRTCOffer: mockSendWebRTCOffer,
        isConnected: true,
      }
    },
  ),
}))

vi.mock('@/lib/browserWebRTC', () => ({
  BrowserWebRTCSession: vi.fn().mockImplementation(function () {
    return {
      start: mockMachineStart,
      applyAnswer: mockMachineApplyAnswer,
      applyState: mockMachineApplyState,
      stop: mockMachineStop,
      sendInput: mockMachineSendInput,
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
}))

import { BrowserLiveView } from './BrowserLiveView'

/** Stand-in MediaStream — jsdom has no real WebRTC/MediaStream (see
 * BrowserLiveView.webrtcSink.test.tsx's own note). */
function fakeMediaStream(id = 'stream-1'): MediaStream {
  return { id } as unknown as MediaStream
}

function connectAndFrame() {
  act(() => {
    wsCallbacksRef.current?.onConnected?.()
    wsCallbacksRef.current?.onScreencast?.({
      type: 'browser_screencast',
      session_id: 's1',
      seq: 1,
      data: 'AAAA',
      width: 1280,
      height: 720,
    })
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
  it('JPEG mode (no mediaStream): pointer input always goes over WS, even if the DC happens to report open', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    act(() => machineCallbacksRef.current.onInputChannelOpen?.())
    const container = stubFrameRect()

    fireEvent.pointerDown(container, { clientX: 10, clientY: 10 })

    expect(mockMachineSendInput).not.toHaveBeenCalled()
    expect(mockSendInput).toHaveBeenCalledWith(expect.objectContaining({ kind: 'mouse_down' }))
  })

  it('video mode, DC not open: pointer input falls back to WS', () => {
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
    expect(screen.getByTestId('browser-live-img')).toBeInTheDocument()

    act(() => machineCallbacksRef.current.onStream?.(fakeMediaStream()))

    expect(screen.getByTestId('browser-live-video')).toBeInTheDocument()
    expect(screen.queryByTestId('browser-live-img')).not.toBeInTheDocument()
  })

  it('on fallback, drops back to the JPEG sink and stops routing input over the DC', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    act(() => machineCallbacksRef.current.onStream?.(fakeMediaStream()))
    act(() => machineCallbacksRef.current.onInputChannelOpen?.())
    expect(screen.getByTestId('browser-live-video')).toBeInTheDocument()

    act(() => machineCallbacksRef.current.onFallback?.('ice-failed'))

    expect(screen.getByTestId('browser-live-img')).toBeInTheDocument()
    expect(screen.queryByTestId('browser-live-video')).not.toBeInTheDocument()

    const container = stubFrameRect()
    fireEvent.pointerDown(container, { clientX: 10, clientY: 10 })
    expect(mockMachineSendInput).not.toHaveBeenCalled()
    expect(mockSendInput).toHaveBeenCalledWith(expect.objectContaining({ kind: 'mouse_down' }))
  })

  it('on WS disconnect, stops the machine and drops back to JPEG', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    act(() => machineCallbacksRef.current.onStream?.(fakeMediaStream()))
    expect(screen.getByTestId('browser-live-video')).toBeInTheDocument()

    act(() => wsCallbacksRef.current?.onDisconnected?.())

    expect(mockMachineStop).toHaveBeenCalledTimes(1)
    expect(screen.getByTestId('browser-live-img')).toBeInTheDocument()
  })
})

describe('BrowserLiveView — surfacing WebRTC fallback reasons (fix-wave B, HIGH)', () => {
  it.each(['ice-failed', 'answer-timeout', 'ice-disconnected-timeout', 'offer-send-failed', 'stream-stopped', 'error', 'unavailable'])(
    'logs console.warn and shows a transient warning toast for the RUNTIME reason "%s"',
    (reason) => {
      const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
      render(<BrowserLiveView sessionId="s1" agentId="a1" />)
      connectAndFrame()

      act(() => machineCallbacksRef.current.onFallback?.(reason))

      expect(warnSpy).toHaveBeenCalledWith('[browser-live] WebRTC fell back to JPEG:', reason)
      const toasts = useUiStore.getState().toasts
      expect(toasts).toHaveLength(1)
      expect(toasts[0]).toEqual(
        expect.objectContaining({ variant: 'warning', message: expect.stringContaining('Live video degraded') }),
      )
      warnSpy.mockRestore()
    },
  )

  it.each(['disabled', 'not_capable', 'lite_build'])(
    'stays silent — no console.warn, no toast — for the CAPABILITY-GATE reason "%s" (this mode simply is not available; not a degradation)',
    (reason) => {
      const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
      render(<BrowserLiveView sessionId="s1" agentId="a1" />)
      connectAndFrame()

      act(() => machineCallbacksRef.current.onFallback?.(reason))

      expect(warnSpy).not.toHaveBeenCalled()
      expect(useUiStore.getState().toasts).toHaveLength(0)
      warnSpy.mockRestore()
    },
  )

  it('still drops back to the JPEG sink for a capability-gate reason, even though it stays silent', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    act(() => machineCallbacksRef.current.onStream?.(fakeMediaStream()))
    expect(screen.getByTestId('browser-live-video')).toBeInTheDocument()

    act(() => machineCallbacksRef.current.onFallback?.('lite_build'))

    expect(screen.getByTestId('browser-live-img')).toBeInTheDocument()
    expect(useUiStore.getState().toasts).toHaveLength(0)
  })
})

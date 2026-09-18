// Signaling callbacks and the independent dedicated input path.
//
// Mocks BOTH `@/lib/browserLiveWs` (same technique as
// BrowserLiveView.webrtcSink.test.tsx / .takeTheWheel.test.tsx) AND
// `@/lib/browserWebRTC` (the state machine is unit-tested on its own in
// browserWebRTC.test.ts — here we only need a thin double that records calls
// and lets the test fire its registered callbacks). This isolates exactly
// what THIS component is responsible for: wiring browser_webrtc_state/answer
// frames into the media machine, and routing gestures to the input peer.
// BrowserLiveView.recovery.test.tsx composes the real media session as well.

import { installBrowserFrameCallbacks, confirmBrowserFrame, emitBrowserFrame } from './browserFrameTestUtils'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { act } from 'react'
import type { BrowserPeerIdentity } from '@/lib/browserWebRTC'
import type { BrowserLiveWsCallbacks } from '@/lib/browserLiveWs'
import { useUiStore } from '@/store/ui'

const {
  mockSendInput,
  mockSocketSendInput,
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
  mockSocketSendInput: vi.fn<(input: Record<string, unknown>) => boolean>(() => true),
  mockSendInput: vi.fn<(input: Record<string, unknown>) => boolean>(() => true),
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
      onStream: null as ((s: MediaStream, identity: BrowserPeerIdentity) => void) | null,
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
vi.mock('@/lib/browserInputWebRTC', async () => {
  const { dedicatedInputSessionStub } = await import('./dedicatedInputTestUtils')
  return { BrowserInputWebRTCSession: dedicatedInputSessionStub(mockSendInput) }
})

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
          sendInput: mockSocketSendInput,
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
      onStream: (cb: (s: MediaStream, identity: BrowserPeerIdentity) => void) => {
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
installBrowserFrameCallbacks()

/** Stand-in MediaStream — jsdom has no real WebRTC/MediaStream (see
 * BrowserLiveView.webrtcSink.test.tsx's own note). */
function fakeMediaStream(id = 'stream-1'): MediaStream {
  return { id } as unknown as MediaStream
}

function connectAndFrame() {
  act(() => {
    wsCallbacksRef.current?.onConnected?.()
  })
  const video = screen.queryByTestId('browser-live-video') as HTMLVideoElement | null
  if (video) confirmBrowserFrame(wsCallbacksRef.current, video)
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

afterEach(() => {
  expect(mockSocketSendInput.mock.calls.filter(([input]) => !['navigate', 'navigate_back', 'reload'].includes(String(input.kind)))).toEqual([])
})

/** Complete an initial gesture and control-status update, then record only
 * the subsequent gesture. Control status does not gate shared input. */
function ackDriving(container: HTMLElement) {
  fireEvent.pointerDown(container, { clientX: 10, clientY: 10 })
  fireEvent.pointerUp(container, { clientX: 10, clientY: 10 })
  act(() => wsCallbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'controlling' }))
  mockMachineSendInput.mockClear()
  mockSendInput.mockClear()
  mockSendControl.mockClear()
}

describe('BrowserLiveView — input routing: dedicated input during media connection changes', () => {
  it('native composition commits Unicode once and keeps candidate keys local', async () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame(); const container = stubFrameRect(); stubVideoDims(); ackDriving(container)
    const sink = screen.getByRole('textbox', { name: 'Remote browser text input' })
    act(() => sink.focus())
    fireEvent.compositionStart(sink)
    fireEvent.input(sink, { target: { value: 'に' }, data: 'に', isComposing: true, inputType: 'insertCompositionText' })
    fireEvent.keyDown(sink, { key: 'ArrowDown', code: 'ArrowDown', keyCode: 40, isComposing: true })
    fireEvent.keyDown(sink, { key: 'Enter', code: 'Enter', keyCode: 13, isComposing: true })
    expect(mockSendInput).not.toHaveBeenCalled()
    fireEvent.compositionEnd(sink, { data: '日本é🙂' })
    fireEvent.input(sink, { target: { value: '日本é🙂' }, data: '日本é🙂', inputType: 'insertFromComposition' })
    await act(async () => { await Promise.resolve() })
    expect(mockSendInput.mock.calls.map(([input]) => input)).toEqual([{ kind: 'text', text: '日本é🙂', modifiers: 0, capture_id: 'capture-test', capture_generation: 1 }])
    fireEvent.compositionStart(sink)
    fireEvent.compositionEnd(sink, { data: '日本é🙂' })
    await act(async () => { await Promise.resolve() })
    expect(mockSendInput).toHaveBeenCalledTimes(2)
  })

  it('native text insertion and paste preserve combining marks and newlines without physical paste duplication', async () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame(); const container = stubFrameRect(); stubVideoDims(); ackDriving(container)
    const sink = screen.getByRole('textbox', { name: 'Remote browser text input' })
    act(() => sink.focus())
    fireEvent.input(sink, { target: { value: '🙂e\u0301' }, data: '🙂e\u0301', inputType: 'insertText' })
    await act(async () => { await Promise.resolve() })
    fireEvent.keyDown(sink, { key: 'v', code: 'KeyV', keyCode: 86, ctrlKey: true })
    fireEvent.paste(sink, { clipboardData: { getData: () => '日本\nمرحبا🙂' } })
    fireEvent.keyUp(sink, { key: 'v', code: 'KeyV', keyCode: 86, ctrlKey: true })
    await act(async () => { await Promise.resolve() })
    fireEvent.input(sink, { target: { value: '後🙂' }, data: '後🙂', inputType: 'insertText' })
    await act(async () => { await Promise.resolve() })
    expect(mockSendInput.mock.calls.map(([input]) => input)).toEqual([
      { kind: 'text', text: '🙂e\u0301', modifiers: 0, capture_id: 'capture-test', capture_generation: 1 },
      { kind: 'text', text: '日本\nمرحبا🙂', modifiers: 0, capture_id: 'capture-test', capture_generation: 1 },
      { kind: 'text', text: '後🙂', modifiers: 0, capture_id: 'capture-test', capture_generation: 1 },
    ])
  })

  it('dead-key native text and idle insertion after pointer capture loss stay usable', async () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame(); const container = stubFrameRect(); stubVideoDims(); ackDriving(container)
    const sink = screen.getByRole('textbox', { name: 'Remote browser text input' })
    act(() => sink.focus())
    fireEvent.lostPointerCapture(container)
    fireEvent.input(sink, { target: { value: '🙂' }, inputType: 'insertText' })
    await act(async () => { await Promise.resolve() })
    fireEvent.keyDown(sink, { key: 'Dead', code: 'Quote', keyCode: 222 })
    fireEvent.keyDown(sink, { key: 'é', code: 'KeyE', keyCode: 69 })
    fireEvent.input(sink, { target: { value: 'é' }, data: 'é', inputType: 'insertText' })
    fireEvent.keyUp(sink, { key: 'é', code: 'KeyE', keyCode: 69 })
    await act(async () => { await Promise.resolve() })
    expect(mockSendInput.mock.calls.map(([input]) => input)).toEqual([
      { kind: 'text', text: '🙂', modifiers: 0, capture_id: 'capture-test', capture_generation: 1 },
      { kind: 'text', text: 'é', modifiers: 0, capture_id: 'capture-test', capture_generation: 1 },
    ])
  })

  it('paste cancels an active candidate and permits the next native insertion after its terminal event', async () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame(); const container = stubFrameRect(); stubVideoDims(); ackDriving(container)
    const sink = screen.getByRole('textbox', { name: 'Remote browser text input' })
    act(() => sink.focus())
    fireEvent.compositionStart(sink)
    fireEvent.input(sink, { target: { value: '候補' }, isComposing: true, inputType: 'insertCompositionText' })
    fireEvent.paste(sink, { clipboardData: { getData: () => 'pasted' } })
    fireEvent.compositionEnd(sink, { data: '候補' })
    fireEvent.input(sink, { target: { value: '候補' }, inputType: 'insertFromComposition' })
    await act(async () => { await Promise.resolve() })
    fireEvent.input(sink, { target: { value: '🙂' }, inputType: 'insertText' })
    await act(async () => { await Promise.resolve() })
    expect(mockSendInput.mock.calls.map(([input]) => input.text)).toEqual(['pasted', '🙂'])
    expect(mockSendInput.mock.calls.every(([input]) => input.kind === 'text')).toBe(true)
  })

  it('navigation cancels candidate text and refuses its late terminal events', async () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame(); const container = stubFrameRect(); stubVideoDims(); ackDriving(container)
    const sink = screen.getByRole('textbox', { name: 'Remote browser text input' })
    act(() => sink.focus())
    fireEvent.compositionStart(sink)
    fireEvent.input(sink, { target: { value: 'discard' }, isComposing: true, inputType: 'insertCompositionText' })
    fireEvent.click(screen.getByRole('button', { name: 'Refresh page' }))
    fireEvent.compositionEnd(sink, { data: 'discard' })
    fireEvent.input(sink, { target: { value: 'discard' }, inputType: 'insertFromComposition' })
    await act(async () => { await Promise.resolve() })
    expect(mockSendInput).not.toHaveBeenCalled()
    expect(mockSocketSendInput).toHaveBeenCalledExactlyOnceWith({ kind: 'reload' })
  })

  it('oversized native paste refuses the complete action without partial insertion', async () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame(); const container = stubFrameRect(); stubVideoDims(); ackDriving(container)
    const sink = screen.getByRole('textbox', { name: 'Remote browser text input' })
    act(() => sink.focus())
    fireEvent.paste(sink, { clipboardData: { getData: () => '🙂'.repeat(8193) } })
    await act(async () => { await Promise.resolve() })
    expect(mockSendInput).not.toHaveBeenCalled()
    expect(useUiStore.getState().toasts.at(-1)?.message).toContain('8,192')
  })

  it.each(['disconnect', 'frame change'])('native commit cannot survive %s', async (change) => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame(); const container = stubFrameRect(); stubVideoDims(); ackDriving(container)
    const sink = screen.getByRole('textbox', { name: 'Remote browser text input' })
    act(() => sink.focus())
    fireEvent.compositionStart(sink)
    fireEvent.compositionEnd(sink, { data: 'discard' })
    act(() => {
      if (change === 'disconnect') wsCallbacksRef.current?.onDisconnected?.()
      else wsCallbacksRef.current?.onVideoHealth({ type: 'browser_video_health', session_id: 's1', state: 'transitioning', capture_id: 'capture-test', capture_generation: 2 })
    })
    await act(async () => { await Promise.resolve() })
    expect(mockSendInput).not.toHaveBeenCalled()
  })

  it('composition cancellation on true blur cannot commit late text or release driving on IME Escape', async () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame(); const container = stubFrameRect(); stubVideoDims(); ackDriving(container)
    const sink = screen.getByRole('textbox', { name: 'Remote browser text input' })
    act(() => sink.focus())
    fireEvent.compositionStart(sink)
    fireEvent.keyDown(sink, { key: 'Escape', code: 'Escape', keyCode: 27, isComposing: true })
    expect(mockSendControl).not.toHaveBeenCalled()
    fireEvent.compositionEnd(sink, { data: 'discard' })
    act(() => screen.getByRole('textbox', { name: 'Address bar' }).focus())
    await act(async () => { await Promise.resolve() })
    expect(mockSendInput).not.toHaveBeenCalled()
  })
  it.each([
    { key: 'a', code: 'KeyA', keyCode: 65, platform: 'Linux x86_64', altKey: false, modifiers: 0 },
    { key: ' ', code: 'Space', keyCode: 32, platform: 'Linux x86_64', altKey: false, modifiers: 0 },
    { key: '@', code: 'KeyL', keyCode: 76, platform: 'MacIntel', altKey: true, modifiers: 1 },
  ])('physical $code carries printable text and both key transitions on $platform', ({ key, code, keyCode, platform, altKey, modifiers }) => {
    const platformSpy = vi.spyOn(navigator, 'platform', 'get').mockReturnValue(platform)
    try {
      render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
      connectAndFrame()
      const container = stubFrameRect()
      stubVideoDims()
      ackDriving(container)
      fireEvent.keyDown(container, { key, code, keyCode, altKey })
      fireEvent.keyUp(container, { key, code, keyCode, altKey })
      const identity = { capture_id: 'capture-test', capture_generation: 1 }
      expect(mockSendInput.mock.calls.map(([input]) => input)).toEqual([
        { kind: 'key_down', key, code, key_code: keyCode, text: key, modifiers, ...identity },
        { kind: 'key_up', key, code, key_code: keyCode, modifiers, ...identity },
      ])
      expect(mockMachineSendInput).not.toHaveBeenCalled()
      fireEvent.blur(window)
      expect(mockSendInput).toHaveBeenCalledTimes(2)
    } finally { platformSpy.mockRestore() }
  })
  // US-1.4 and US-4.1: cleanup must survive media loss and navigation must
  // remain available without a presented frame. The component and gate are
  // real; only the socket and media boundary are controlled by this fixture.
  it('allows Back while the current video frame is unavailable', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    fireEvent.click(screen.getByRole('button', { name: 'Go back' }))
    expect(mockSocketSendInput.mock.calls).toEqual([[{ kind: 'navigate_back' }]])
  })

  it.each([
    { key: 'Control', code: 'ControlLeft', keyCode: 17, flag: 'ctrlKey', modifiers: 2 },
    { key: 'Meta', code: 'MetaLeft', keyCode: 91, flag: 'metaKey', modifiers: 4 },
  ])('releases shortcut A when $key is released first', ({ key, code, keyCode, flag, modifiers }) => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    const container = stubFrameRect()
    stubVideoDims()
    ackDriving(container)
    fireEvent.keyDown(container, { key, code, keyCode, [flag]: true })
    fireEvent.keyDown(container, { key: 'a', code: 'KeyA', keyCode: 65, [flag]: true })
    fireEvent.keyUp(container, { key, code, keyCode })
    fireEvent.keyUp(container, { key: 'a', code: 'KeyA', keyCode: 65 })
    const identity = { capture_id: 'capture-test', capture_generation: 1 }
    expect(mockSendInput.mock.calls.map(([input]) => input)).toEqual([
      { kind: 'key_down', key, code, key_code: keyCode, modifiers, ...identity },
      { kind: 'key_down', key: 'a', code: 'KeyA', key_code: 65, modifiers, ...identity },
      { kind: 'key_up', key, code, key_code: keyCode, modifiers: 0, ...identity },
      { kind: 'key_up', key: 'a', code: 'KeyA', key_code: 65, modifiers: 0, ...identity },
    ])
    fireEvent.blur(window)
    expect(mockSendInput).toHaveBeenCalledTimes(4)
  })

  it('releases held keys and buttons over the surviving input peer when video fails', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    const container = stubFrameRect()
    stubVideoDims()
    ackDriving(container)
    fireEvent.keyDown(container, { key: 'Shift', code: 'ShiftLeft', keyCode: 16, shiftKey: true })
    fireEvent.pointerDown(container, { clientX: 10, clientY: 10 })
    mockSendInput.mockClear()
    act(() => machineCallbacksRef.current.onFallback?.('ice-failed'))
    const identity = { capture_id: 'capture-test', capture_generation: 1 }
    expect(mockSendInput.mock.calls.map(([input]) => input)).toEqual([
      { kind: 'key_up', key: 'Shift', code: 'ShiftLeft', key_code: 16, modifiers: 0, ...identity },
      { kind: 'mouse_up', x: 10, y: 10, button: 'left', modifiers: 0, capture_width: 1280, capture_height: 720, ...identity },
    ])
    fireEvent.blur(window)
    expect(mockSendInput).toHaveBeenCalledTimes(2)
  })

  it('pointer input uses the dedicated peer before the media data channel opens', () => {
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

  it('mouse input stays on the dedicated peer after the media data channel opens', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    act(() => machineCallbacksRef.current.onInputChannelOpen?.())
    const container = stubFrameRect()
    stubVideoDims()
    ackDriving(container)

    fireEvent.pointerDown(container, { clientX: 10, clientY: 10 })

    expect(mockMachineSendInput).not.toHaveBeenCalled()
    expect(mockSendInput).toHaveBeenCalledTimes(1)
    const payload = mockSendInput.mock.calls[0][0]
    expect(payload).toEqual(expect.objectContaining({ kind: 'mouse_down', x: 10, y: 10 }))
  })

  it('keyboard input stays on the dedicated peer after the media data channel opens', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    act(() => machineCallbacksRef.current.onInputChannelOpen?.())
    const container = stubFrameRect()
    stubVideoDims()
    // Take the wheel first (idle → you-driving) via an implicit-drive click.
    ackDriving(container)

    fireEvent.keyDown(container, { key: 'a' })

    expect(mockMachineSendInput).not.toHaveBeenCalled()
    expect(mockSendInput).toHaveBeenCalledTimes(1)
    const payload = mockSendInput.mock.calls[0][0]
    expect(payload).toEqual(expect.objectContaining({ kind: 'key_down', key: 'a', code: '', key_code: 0, text: 'a' }))
  })

  it('a failing unused media data channel does not duplicate dedicated input', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    act(() => machineCallbacksRef.current.onInputChannelOpen?.())
    const container = stubFrameRect()
    stubVideoDims()
    ackDriving(container)
    mockMachineSendInput.mockReturnValueOnce(false)

    fireEvent.pointerDown(container, { clientX: 10, clientY: 10 })

    expect(mockSendInput).toHaveBeenCalledTimes(1)
    expect(mockSendInput).toHaveBeenCalledWith(expect.objectContaining({ kind: 'mouse_down' }))
  })

  it('closing the media data channel does not change dedicated input transport', () => {
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

  it('initial and later gestures stay on dedicated input without an implicit control change', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    act(() => machineCallbacksRef.current.onInputChannelOpen?.())
    const container = stubFrameRect()
    stubVideoDims()

    // Gesture 1 uses the dedicated peer without taking control.
    fireEvent.pointerDown(container, { clientX: 10, clientY: 10 })
    fireEvent.pointerUp(container, { clientX: 12, clientY: 12 })
    expect(mockSendControl).not.toHaveBeenCalledWith('take')
    expect(mockMachineSendInput).not.toHaveBeenCalled()
    expect(mockSendInput).toHaveBeenCalledWith(expect.objectContaining({ kind: 'mouse_down' }))
    expect(mockSendInput).toHaveBeenCalledWith(expect.objectContaining({ kind: 'mouse_up' }))

    // A presentation status update does not change the gesture route.
    act(() => wsCallbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'controlling' }))
    mockMachineSendInput.mockClear()
    mockSendInput.mockClear()
    fireEvent.pointerDown(container, { clientX: 20, clientY: 20 })
    expect(mockMachineSendInput).not.toHaveBeenCalled()
    expect(mockSendInput).toHaveBeenCalledTimes(1)
    expect(mockSendInput.mock.calls[0][0]).toEqual(
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
    expect(mockSocketSendInput).toHaveBeenCalledWith(expect.objectContaining({ kind: 'navigate', url: 'https://example.com' }))
    expect(mockSendInput).not.toHaveBeenCalled()
  })

  it('Refresh uses the ordered socket', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    act(() => machineCallbacksRef.current.onInputChannelOpen?.())

    fireEvent.click(screen.getByRole('button', { name: /refresh page/i }))

    expect(mockMachineSendInput).not.toHaveBeenCalled()
    expect(mockSocketSendInput).toHaveBeenCalledWith(expect.objectContaining({ kind: 'reload' }))
    expect(mockSendInput).not.toHaveBeenCalled()
  })

  it('the initial pointer press uses dedicated input without an implicit control hint', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    act(() => machineCallbacksRef.current.onInputChannelOpen?.())
    const container = stubFrameRect()
    stubVideoDims()

    // The first press does not introduce a control barrier.
    fireEvent.pointerDown(container, { clientX: 10, clientY: 10 })

    expect(mockSendControl).not.toHaveBeenCalledWith('take')
    expect(mockMachineSendInput).not.toHaveBeenCalled()
    expect(mockSendInput).toHaveBeenCalledWith(expect.objectContaining({ kind: 'mouse_down' }))
  })
})

// Confirmed page geometry lets the client send CSS pixels directly, including
// when the encoder changes its resolution between input events.
describe('BrowserLiveView — confirmed CSS coordinates on input', () => {
  it('mouse_down sends confirmed CSS pixels without server rescaling', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    act(() => machineCallbacksRef.current.onInputChannelOpen?.())
    const container = stubFrameRect()
    stubVideoDims() // videoWidth/videoHeight 1280/720
    ackDriving(container)

    fireEvent.pointerDown(container, { clientX: 10, clientY: 10 })

    expect(mockMachineSendInput).not.toHaveBeenCalled()
    expect(mockSendInput).toHaveBeenCalledTimes(1)
    const payload = mockSendInput.mock.calls[0][0]
    // capture_width/height name the CSS space x/y were mapped into — without
    // them the gateway dispatches raw capture coordinates (2026-09-07 regression).
    expect(payload).toEqual(
      { kind: 'mouse_down', x: 10, y: 10, button: 'left', modifiers: 0, capture_width: 1280, capture_height: 720, capture_id: 'capture-test', capture_generation: 1 },
    )
  })

  it('mouse_down never dispatches at all before the video reports real dimensions (no fallback sink to carry the click)', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    const container = stubFrameRect()
    // Deliberately do not call stubVideoDims() — videoWidth/videoHeight stay 0.

    fireEvent.pointerDown(container, { clientX: 10, clientY: 10 })

    expect(mockMachineSendInput).not.toHaveBeenCalled()
    expect(mockSendInput).not.toHaveBeenCalled()
  })

  // Wheel deltas accumulate, while its CSS position is fixed at event time.
  it('video mode: wheel frames send confirmed CSS pixels', async () => {
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

      const wheels = mockSendInput.mock.calls
        .map((c) => c[0] as Record<string, unknown>)
        .filter((p) => p.kind === 'wheel')
      expect(wheels).toHaveLength(1)
      expect(wheels[0]).toEqual(
        { kind: 'wheel', x: 10, y: 10, delta_x: 0, delta_y: 120, modifiers: 0, capture_width: 1280, capture_height: 720, capture_id: 'capture-test', capture_generation: 1 },
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

      const wheels = mockSendInput.mock.calls
        .map((c) => c[0] as Record<string, unknown>)
        .filter((p) => p.kind === 'wheel')
      expect(wheels).toHaveLength(1)
      expect(wheels[0].delta_y).toBe(120)
      expect(wheels[0].delta_x).toBe(20)
    } finally {
      vi.useRealTimers()
    }
  })

  it.each([
    ['modifier', { clientX: 10, clientY: 10, ctrlKey: true }, { x: 10, y: 10, modifiers: 2 }],
    ['x position', { clientX: 30, clientY: 10 }, { x: 30, y: 10, modifiers: 0 }],
    ['y position', { clientX: 10, clientY: 20 }, { x: 10, y: 20, modifiers: 0 }],
  ])('video mode: wheel preserves each %s boundary', async (_name, secondEvent, secondPoint) => {
    vi.useFakeTimers()
    try {
      render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
      connectAndFrame()
      const container = stubFrameRect()
      stubVideoDims()
      ackDriving(container)
      mockSendInput.mockClear()
      fireEvent.wheel(container, { deltaX: 0, deltaY: 100, clientX: 10, clientY: 10 })
      fireEvent.wheel(container, { deltaX: 0, deltaY: 1, ...secondEvent })
      await vi.advanceTimersByTimeAsync(60)
      const wheels = mockSendInput.mock.calls.map(c => c[0]).filter(p => p.kind === 'wheel')
      expect(wheels).toEqual([
        { kind: 'wheel', x: 10, y: 10, modifiers: 0, delta_x: 0, delta_y: 100, capture_width: 1280, capture_height: 720, capture_id: 'capture-test', capture_generation: 1 },
        { kind: 'wheel', ...secondPoint, delta_x: 0, delta_y: 1, capture_width: 1280, capture_height: 720, capture_id: 'capture-test', capture_generation: 1 },
      ])
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

    expect(mockSendInput).toHaveBeenCalledTimes(1)
    const payload = mockSendInput.mock.calls[0][0] as Record<string, unknown>
    expect(payload.kind).toBe('key_down')
    expect(payload).not.toHaveProperty('capture_width')
    expect(payload).not.toHaveProperty('capture_height')
  })

  // A later encoder resize must not reinterpret an already mapped CSS point.
  it('video mode: a coalesced mouse_move preserves CSS coordinates computed before the video sink resized', () => {
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

      expect(mockSendInput).toHaveBeenCalledTimes(1)
      const payload = mockSendInput.mock.calls[0][0]
      // Dims still describe the event-time css space (1280x720), not the
      // rebuilt stream's 320x160 — carried, never re-derived at flush.
      expect(payload).toEqual(
        { kind: 'mouse_move', x: 10, y: 10, modifiers: 0, capture_width: 1280, capture_height: 720, capture_id: 'capture-test', capture_generation: 1 },
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

    expect(mockMachineApplyAnswer).toHaveBeenCalledWith({ type: 'browser_webrtc_answer', sdp: 'answer-sdp' })
  })

  it('renders the <video> sink once the machine reports a stream via onStream (no mediaStream prop override)', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    // Not attached yet — no second sink to fall back to while waiting.
    expect(screen.queryByTestId('browser-live-video')).not.toBeInTheDocument()
    expect(screen.queryByTestId('browser-live-frame')).not.toBeInTheDocument()

    act(() => machineCallbacksRef.current.onStream?.(fakeMediaStream(), { captureId: 'capture-test', generation: 1, offerId: 1 }))

    expect(screen.getByTestId('browser-live-video')).toBeInTheDocument()
  })

  // Operator directive (JPEG-fallback removal) — WebRTC is the ONLY live-video
  // path now. A fallback no longer swaps to a second sink; it tears the
  // interactive surface down ENTIRELY (no silent degrade, no blank panel) and
  // the panel's empty state shows the honest error instead.
  it('on fallback, unmounts the interactive surface entirely and stops routing input (nothing left to click)', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    act(() => machineCallbacksRef.current.onStream?.(fakeMediaStream(), { captureId: 'capture-test', generation: 1, offerId: 1 }))
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
    act(() => machineCallbacksRef.current.onStream?.(fakeMediaStream(), { captureId: 'capture-test', generation: 1, offerId: 1 }))
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
    act(() => machineCallbacksRef.current.onStream?.(fakeMediaStream(), { captureId: 'capture-test', generation: 1, offerId: 1 }))
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
    act(() => wsCallbacksRef.current?.onStatus({ type: 'browser_status', state: 'attached' }))
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
      act(() => machineCallbacksRef.current.onStream?.(fakeMediaStream(), { captureId: 'capture-test', generation: 1, offerId: 1 }))
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


describe('BrowserLiveView fresh viewer fallback for missing received timestamps', () => {
  function boundary(generation: number, rtpTimestamp: number) {
    act(() => wsCallbacksRef.current!.onVideoHealth({ type: 'browser_video_health', session_id: 's1', state: 'recovered', capture_id: 'capture-test', capture_generation: generation, rtp_timestamp: rtpTimestamp }))
  }
  function incoming(offerId: number, generation = 1) {
    const stream = fakeMediaStream(`peer-${offerId}`)
    act(() => machineCallbacksRef.current.onStream?.(stream, { captureId: 'capture-test', generation, offerId }))
    return screen.getByTestId('browser-live-video') as HTMLVideoElement
  }
  function presentWithoutRtp(video: HTMLVideoElement) {
    act(() => emitBrowserFrame(video, { expectedDisplayTime: performance.now() - 1 }))
  }
  function typeA() { fireEvent.keyDown(screen.getByTestId('browser-live-frame'), { key: 'a' }) }

  it('restarts for a replacement capture even before the old peer publishes a stream', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    boundary(1, 100)
    mockMachineStart.mockClear()
    act(() => wsCallbacksRef.current!.onVideoHealth({ type: 'browser_video_health', session_id: 's1', state: 'recovered', capture_id: 'capture-replacement', capture_generation: 1, rtp_timestamp: 200 }))
    expect(mockMachineStart.mock.calls).toEqual([[expect.any(Function), { captureId: 'capture-replacement', generation: 1 }]])
    expect(mockMachineStop).toHaveBeenCalledTimes(1)
  })

  it('cannot replace current health identity with an unknown old answer capture', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    boundary(1, 100)
    mockMachineStart.mockClear()
    mockMachineApplyAnswer.mockClear()
    act(() => wsCallbacksRef.current!.onWebRTCAnswer({ type: 'browser_webrtc_answer', sdp: 'old-answer', offer_id: 1, capture_id: 'capture-never-observed', capture_generation: 1 }))
    expect(mockMachineApplyAnswer.mock.calls).toEqual([])
    expect(mockMachineStart.mock.calls).toEqual([[expect.any(Function), { captureId: 'capture-test', generation: 1 }]])
  })

  it('waits for a committed boundary before replacing a mismatched pending viewer', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    act(() => wsCallbacksRef.current!.onVideoHealth({ type: 'browser_video_health', session_id: 's1', state: 'transitioning', capture_id: 'capture-test', capture_generation: 1 }))
    mockMachineStart.mockClear()
    act(() => wsCallbacksRef.current!.onWebRTCAnswer({ type: 'browser_webrtc_answer', sdp: 'old-answer', offer_id: 1, capture_id: 'capture-never-observed', capture_generation: 1 }))
    expect(mockMachineApplyAnswer.mock.calls).toEqual([])
    expect(mockMachineStart.mock.calls).toEqual([])
    boundary(1, 100)
    expect(mockMachineStart.mock.calls).toEqual([[expect.any(Function), { captureId: 'capture-test', generation: 1 }]])
  })

  it('negotiates a fresh peer for the committed generation before allowing timestamp-free input', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    const original = incoming(1)
    boundary(1, 100)
    presentWithoutRtp(original)
    typeA()
    expect(mockSendInput.mock.calls).toEqual([])
    expect(mockMachineStop).toHaveBeenCalledTimes(1)
    expect(mockMachineStart).toHaveBeenCalledWith(expect.any(Function), { captureId: 'capture-test', generation: 1 })
    const fresh = incoming(2)
    typeA()
    expect(mockSendInput.mock.calls).toEqual([])
    presentWithoutRtp(fresh)
    typeA()
    expect(mockSendInput.mock.calls).toEqual([[{ kind: 'key_down', key: 'a', code: '', key_code: 0, text: 'a', modifiers: 0, capture_id: 'capture-test', capture_generation: 1 }]])
    expect(mockMachineStart).toHaveBeenCalledTimes(1)
  })

  it('requires a new fresh peer when a recovery changes the marker within the same generation', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    const original = incoming(1)
    boundary(1, 100)
    presentWithoutRtp(original)
    const firstFresh = incoming(2)
    presentWithoutRtp(firstFresh)
    mockMachineStart.mockClear()
    boundary(1, 200)
    typeA()
    expect(mockSendInput.mock.calls).toEqual([])
    expect(mockMachineStart).toHaveBeenCalledWith(expect.any(Function), { captureId: 'capture-test', generation: 1 })
    const secondFresh = incoming(3)
    presentWithoutRtp(secondFresh)
    typeA()
    expect(mockSendInput.mock.calls).toEqual([[{ kind: 'key_down', key: 'a', code: '', key_code: 0, text: 'a', modifiers: 0, capture_id: 'capture-test', capture_generation: 1 }]])
  })

  // The satisfied fresh-viewer requirement must not linger. requiresFreshViewerRef
  // used to stay true forever after one needs-fresh-viewer, so every recovered
  // boundary with a new rtp_timestamp rebuilt the media peer via
  // requestFreshViewerRef — one stop()/start() cycle per recovery event, each
  // re-locking input and leaving a dead stream bound mid-renegotiation. Once
  // the demanded fresh viewer has been authorized, a later boundary that the
  // CURRENT stream can prove by RTP timestamp must keep that stream: no
  // rebuild, input stays dispatchable. (Timestamp-free streams re-arm the
  // requirement on their own — the previous test pins that path.)
  it('keeps the satisfied fresh viewer for a later boundary its own RTP timestamps prove', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    const original = incoming(1)
    boundary(1, 100)
    presentWithoutRtp(original)
    expect(mockMachineStop).toHaveBeenCalledTimes(1)
    expect(mockMachineStart).toHaveBeenCalledTimes(1)
    const fresh = incoming(2)
    presentWithoutRtp(fresh)
    typeA()
    expect(mockSendInput.mock.calls).toEqual([[{ kind: 'key_down', key: 'a', code: '', key_code: 0, text: 'a', modifiers: 0, capture_id: 'capture-test', capture_generation: 1 }]])
    mockSendInput.mockClear()
    // The fresh viewer now carries real RTP timestamps: a newer boundary on
    // the same generation is provable in place, without another peer.
    act(() => emitBrowserFrame(fresh, { rtpTimestamp: 150, expectedDisplayTime: performance.now() - 1 }))
    boundary(1, 200)
    act(() => emitBrowserFrame(fresh, { rtpTimestamp: 200, expectedDisplayTime: performance.now() - 1 }))
    typeA()
    expect(mockSendInput.mock.calls).toEqual([[{ kind: 'key_down', key: 'a', code: '', key_code: 0, text: 'a', modifiers: 0, capture_id: 'capture-test', capture_generation: 1 }]])
    expect(mockMachineStop).toHaveBeenCalledTimes(1)
    expect(mockMachineStart).toHaveBeenCalledTimes(1)
  })

  it('cannot authorize an old fallback peer when another generation commits during negotiation', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    const original = incoming(1)
    boundary(1, 100)
    presentWithoutRtp(original)
    act(() => wsCallbacksRef.current!.onVideoHealth({ type: 'browser_video_health', session_id: 's1', state: 'transitioning', capture_id: 'capture-test', capture_generation: 2 }))
    incoming(2, 1)
    mockMachineStart.mockClear()
    boundary(2, 200)
    typeA()
    expect(mockSendInput.mock.calls).toEqual([])
    expect(mockMachineStart).toHaveBeenCalledWith(expect.any(Function), { captureId: 'capture-test', generation: 2 })
    const current = incoming(3, 2)
    presentWithoutRtp(current)
    typeA()
    expect(mockSendInput.mock.calls).toEqual([[{ kind: 'key_down', key: 'a', code: '', key_code: 0, text: 'a', modifiers: 0, capture_id: 'capture-test', capture_generation: 2 }]])
  })
})

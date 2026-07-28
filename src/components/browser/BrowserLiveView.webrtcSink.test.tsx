// BrowserLiveView.webrtcSink.test.tsx — WebRTC build (webrtc-build/wave-plan.md
// W1-F) SPA playback groundwork: the <video> sink swap, mute/unmute control,
// video-mode coordinate mapping routing, and the annotate-crop video-mode
// branch. No signaling exists yet (Wave 2 owns browserLiveWs.ts's offer/
// answer exchange) — every test here either exercises the `mediaStream`
// prop directly (a fake object standing in for a real MediaStream, since
// jsdom doesn't implement WebRTC) or asserts the JPEG-only default is
// byte-identical when the prop is omitted/null. Mocks BrowserLiveWsConnection
// the same way BrowserLiveView.takeTheWheel.test.tsx does.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { act } from 'react'
import type { BrowserLiveWsCallbacks } from '@/lib/browserLiveWs'
import { useUiStore } from '@/store/ui'

const { mockSendControl, mockSendInput, callbacksRef } = vi.hoisted(() => ({
  mockSendControl: vi.fn(() => true),
  mockSendInput: vi.fn(),
  callbacksRef: { current: null as BrowserLiveWsCallbacks | null },
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
        callbacksRef.current = callbacks
        return {
          connect: vi.fn(),
          detach: vi.fn(),
          close: vi.fn(),
          sendInput: mockSendInput,
          sendControl: mockSendControl,
          sendTabAction: vi.fn(() => true),
          isConnected: true,
        }
      },
    ),
  }
})

import { BrowserLiveView } from './BrowserLiveView'

/** A fake MediaStream stand-in — jsdom has no real WebRTC/MediaStream
 * implementation, and this wave only needs `video.srcObject = mediaStream`
 * assignment/readback to work (a plain property set/get, verified against
 * jsdom directly), never actual playback. Cast at the call site. */
function fakeMediaStream(id = 'stream-1'): MediaStream {
  return { id } as unknown as MediaStream
}

function connectAndFrame(pageScale?: number) {
  act(() => {
    callbacksRef.current?.onConnected?.()
    callbacksRef.current?.onScreencast?.({
      type: 'browser_screencast',
      session_id: 's1',
      seq: 1,
      data: 'AAAA',
      width: 1280,
      height: 720,
      page_scale: pageScale,
    })
  })
}

/** Mirrors the sibling suites' technique: jsdom reports all-zero rects by
 * default, so getBoundingClientRect is stubbed to a clean 1:1 box. */
function stubFrameRect() {
  const container = screen.getByTestId('browser-live-frame')
  vi.spyOn(container, 'getBoundingClientRect').mockReturnValue({
    left: 0, top: 0, width: 1280, height: 720, right: 1280, bottom: 720, x: 0, y: 0,
    toJSON() { return {} },
  } as DOMRect)
  return container
}

beforeEach(() => {
  vi.clearAllMocks()
  callbacksRef.current = null
  useUiStore.setState({ toasts: [] })
})

describe('BrowserLiveView — sink swap (WebRTC build W1-F)', () => {
  it('renders the JPEG <img> sink and no <video> when mediaStream is not provided (default)', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()

    expect(screen.getByTestId('browser-live-img')).toBeInTheDocument()
    expect(screen.queryByTestId('browser-live-video')).not.toBeInTheDocument()
  })

  it('renders the JPEG <img> sink when mediaStream is explicitly null (same as omitted — byte-identical default)', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={null} />)
    connectAndFrame()

    expect(screen.getByTestId('browser-live-img')).toBeInTheDocument()
    expect(screen.queryByTestId('browser-live-video')).not.toBeInTheDocument()
  })

  it('renders a <video> sink IN PLACE of the <img> when mediaStream is set', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()

    expect(screen.getByTestId('browser-live-video')).toBeInTheDocument()
    expect(screen.queryByTestId('browser-live-img')).not.toBeInTheDocument()
  })

  it('binds the mediaStream to the <video> element\'s srcObject', () => {
    const stream = fakeMediaStream('bound-stream')
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={stream} />)
    connectAndFrame()

    const video = screen.getByTestId('browser-live-video') as HTMLVideoElement
    expect(video.srcObject).toBe(stream)
  })

  it('rebinds srcObject when the mediaStream prop changes to a different stream instance (same element, no remount)', () => {
    const streamA = fakeMediaStream('a')
    const { rerender } = render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={streamA} />)
    connectAndFrame()
    expect((screen.getByTestId('browser-live-video') as HTMLVideoElement).srcObject).toBe(streamA)

    const streamB = fakeMediaStream('b')
    rerender(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={streamB} />)
    expect((screen.getByTestId('browser-live-video') as HTMLVideoElement).srcObject).toBe(streamB)
  })

  it('falls back to the <img> sink again when mediaStream transitions from set to null', () => {
    const { rerender } = render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    expect(screen.getByTestId('browser-live-video')).toBeInTheDocument()

    rerender(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={null} />)
    expect(screen.getByTestId('browser-live-img')).toBeInTheDocument()
    expect(screen.queryByTestId('browser-live-video')).not.toBeInTheDocument()
  })

  it('the <video> sink is sized to mirror the <img> (object-contain within the panel)', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()

    expect(screen.getByTestId('browser-live-video').className).toContain('object-contain')
  })
})

describe('BrowserLiveView — video mute/unmute control (WebRTC build W1-F)', () => {
  it('the <video> sink starts muted (autoplay-safe)', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} hasAudio />)
    connectAndFrame()

    expect((screen.getByTestId('browser-live-video') as HTMLVideoElement).muted).toBe(true)
  })

  it('does not render the mute toggle when mediaStream is null, even with hasAudio true', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={null} hasAudio />)
    connectAndFrame()

    expect(screen.queryByTestId('browser-live-mute-toggle')).not.toBeInTheDocument()
  })

  it('does not render the mute toggle in video mode when hasAudio is false (default)', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()

    expect(screen.queryByTestId('browser-live-mute-toggle')).not.toBeInTheDocument()
  })

  it('renders the mute toggle only when BOTH mediaStream is set and hasAudio is true', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} hasAudio />)
    connectAndFrame()

    expect(screen.getByTestId('browser-live-mute-toggle')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /unmute audio/i })).toBeInTheDocument()
  })

  it('unmutes the video element on click (the click IS the user gesture) and flips the button label/aria-pressed', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} hasAudio />)
    connectAndFrame()
    const video = screen.getByTestId('browser-live-video') as HTMLVideoElement
    expect(video.muted).toBe(true)

    fireEvent.click(screen.getByTestId('browser-live-mute-toggle'))

    expect(video.muted).toBe(false)
    expect(screen.getByRole('button', { name: /mute audio/i })).toHaveAttribute('aria-pressed', 'true')
  })

  it('re-mutes on a second click', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} hasAudio />)
    connectAndFrame()
    const toggle = screen.getByTestId('browser-live-mute-toggle')

    fireEvent.click(toggle)
    fireEvent.click(toggle)

    expect((screen.getByTestId('browser-live-video') as HTMLVideoElement).muted).toBe(true)
    expect(screen.getByRole('button', { name: /unmute audio/i })).toHaveAttribute('aria-pressed', 'false')
  })

  it('remembers the mute choice locally across re-renders driven by unrelated prop/state changes (component state, not persisted)', () => {
    const { rerender } = render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} hasAudio />)
    connectAndFrame()
    fireEvent.click(screen.getByTestId('browser-live-mute-toggle'))
    expect((screen.getByTestId('browser-live-video') as HTMLVideoElement).muted).toBe(false)

    // Re-render with the SAME stream instance (e.g. an unrelated parent
    // re-render) — the local unmute choice must survive.
    rerender(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} hasAudio />)
    expect((screen.getByTestId('browser-live-video') as HTMLVideoElement).muted).toBe(false)
  })
})

describe('BrowserLiveView — coordinate mapping routes to the video-mode variant when the video sink is active (WebRTC build W1-F)', () => {
  it('dispatches input using video-mode mapping (page_scale fixed at 1) instead of the JPEG frame\'s page_scale, once the video reports its dimensions', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    // JPEG frame reports page_scale: 2 — if coordinate mapping wrongly kept
    // reading this while in video mode, a 1:1 rect→video click would land at
    // HALF the expected device coordinate (mapClientToDevice divides by
    // page_scale). Video mode must ignore it entirely.
    connectAndFrame(2)
    const container = stubFrameRect()
    const video = screen.getByTestId('browser-live-video') as HTMLVideoElement
    Object.defineProperty(video, 'videoWidth', { value: 1280, configurable: true })
    Object.defineProperty(video, 'videoHeight', { value: 720, configurable: true })

    fireEvent.pointerDown(container, { clientX: 640, clientY: 360 })

    // Video-mode math (scale fixed at 1): device coords equal the raw
    // rect-relative client coords for this 1:1 rect/video size. The
    // JPEG-mode math (page_scale 2) would have produced {x:320, y:180}.
    expect(mockSendInput).toHaveBeenCalledWith(expect.objectContaining({ kind: 'mouse_down', x: 640, y: 360 }))
  })

  it('falls back to the JPEG-frame mapping (with its page_scale) when mediaStream is set but the video has not yet reported dimensions (videoWidth/videoHeight still 0)', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame(2)
    const container = stubFrameRect()
    // Deliberately do NOT set videoWidth/videoHeight — jsdom defaults them to
    // 0, exactly the pre-`loadedmetadata` state a real <video> starts in.

    fireEvent.pointerDown(container, { clientX: 640, clientY: 360 })

    // Falls back to the JPEG frame's mapping — page_scale 2 halves the
    // device coordinate, proving activeFrameDims's video branch correctly
    // declined (videoWidth/Height not yet reported) rather than dispatching
    // garbage (0-sized) coordinates.
    expect(mockSendInput).toHaveBeenCalledWith(expect.objectContaining({ kind: 'mouse_down', x: 320, y: 180 }))
  })
})

describe('BrowserLiveView — annotate crop video-mode branch (WebRTC build W1-F — math/wiring only, draw covered in e2e)', () => {
  it('is wired (does not throw/crash) and surfaces the same graceful failure toast as the img path when the video has no decoded frame yet (readyState 0 in jsdom)', async () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} canAnnotate />)
    connectAndFrame()
    fireEvent.click(screen.getByRole('button', { name: /annotate a region/i }))
    const container = screen.getByTestId('browser-live-frame')

    fireEvent.pointerDown(container, { clientX: 10, clientY: 10 })
    fireEvent.pointerMove(container, { clientX: 200, clientY: 150 })
    fireEvent.pointerUp(container, { clientX: 200, clientY: 150 })

    // cropFrameToFile's video-mode branch checks `video.readyState < 2`
    // BEFORE ever touching canvas (jsdom has no `canvas` package either way)
    // — this deterministically exercises that guard without needing to mock
    // video playback or canvas drawing.
    await vi.waitFor(() => {
      expect(useUiStore.getState().toasts.some((t) => /could not capture/i.test(t.message))).toBe(true)
    })
    expect(screen.queryByTestId('annotate-popover')).not.toBeInTheDocument()
  })

  it('does not forward any driving input while annotating in video mode (annotate/driving mutual exclusion holds regardless of sink)', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} canAnnotate />)
    connectAndFrame()
    fireEvent.click(screen.getByRole('button', { name: /annotate a region/i }))
    const container = screen.getByTestId('browser-live-frame')

    fireEvent.pointerDown(container, { clientX: 10, clientY: 10 })
    fireEvent.pointerMove(container, { clientX: 60, clientY: 80 })
    fireEvent.pointerUp(container, { clientX: 60, clientY: 80 })

    expect(mockSendInput).not.toHaveBeenCalled()
  })
})

// BrowserLiveView.webrtcSink.test.tsx — the <video> sink: mount/attach gating,
// srcObject binding, mute/unmute control, coordinate-mapping routing, and the
// annotate-crop video branch.
//
// ADR-047 (operator directive — JPEG-fallback removal): the JPEG screencast
// `<img>` sink is DELETED, not flagged off. WebRTC video is the only live
// sink; `attached` (mediaStream !== null) is what gates the interactive
// container/video mounting at all, and `videoReady` (set on the video's
// `onLoadedMetadata`) is what gates real pixel dimensions being available to
// coordinate mapping / annotate crop. Every test here drives the `mediaStream`
// test/override seam directly (a fake object standing in for a real
// MediaStream, since jsdom doesn't implement WebRTC) rather than the real
// internal signaling path (covered by BrowserLiveView.webrtcInputRouting.test.tsx
// and browserWebRTC.test.ts). Mocks BrowserLiveWsConnection the same way
// BrowserLiveView.takeTheWheel.test.tsx does.

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
          // Adaptive viewport (2026-07-31): BrowserLiveView's ResizeObserver
          // calls this on mount, so every connection double needs it.
          sendViewport: vi.fn(() => true),
          isConnected: true,
        }
      },
    ),
  }
})

import { BrowserLiveView } from './BrowserLiveView'

/** A fake MediaStream stand-in — jsdom has no real WebRTC/MediaStream
 * implementation, and this only needs `video.srcObject = mediaStream`
 * assignment/readback to work (a plain property set/get, verified against
 * jsdom directly), never actual playback. Cast at the call site. */
function fakeMediaStream(id = 'stream-1'): MediaStream {
  return { id } as unknown as MediaStream
}

/** Connects the WS. Requires the component to already be rendered with a
 * `mediaStream` prop (attached) for the <video> element to exist at all. */
function connectAndFrame() {
  act(() => {
    callbacksRef.current?.onConnected?.()
  })
}

/** Stubs the video's intrinsic dimensions and fires `loadedmetadata` — the
 * signal `videoReady`/`activeFrameDims` wait for. */
function decodeFrame(width = 1280, height = 720) {
  const video = screen.getByTestId('browser-live-video') as HTMLVideoElement
  Object.defineProperty(video, 'videoWidth', { value: width, configurable: true })
  Object.defineProperty(video, 'videoHeight', { value: height, configurable: true })
  fireEvent.loadedMetadata(video)
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

describe('BrowserLiveView — video sink mount gating (attached vs. not)', () => {
  it('renders neither the interactive container nor the video sink when mediaStream is not provided (default)', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()

    expect(screen.queryByTestId('browser-live-frame')).not.toBeInTheDocument()
    expect(screen.queryByTestId('browser-live-video')).not.toBeInTheDocument()
  })

  it('renders neither when mediaStream is explicitly null (same as omitted)', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={null} />)
    connectAndFrame()

    expect(screen.queryByTestId('browser-live-frame')).not.toBeInTheDocument()
    expect(screen.queryByTestId('browser-live-video')).not.toBeInTheDocument()
  })

  it('mounts the video sink as soon as mediaStream is set, even before the video decodes a frame', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()

    expect(screen.getByTestId('browser-live-frame')).toBeInTheDocument()
    expect(screen.getByTestId('browser-live-video')).toBeInTheDocument()
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

  it('unmounts the container/video entirely when mediaStream transitions from set to null (no silent fallback — the whole panel visibly reverts to its empty state)', () => {
    const { rerender } = render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    expect(screen.getByTestId('browser-live-video')).toBeInTheDocument()

    rerender(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={null} />)
    expect(screen.queryByTestId('browser-live-video')).not.toBeInTheDocument()
    expect(screen.queryByTestId('browser-live-frame')).not.toBeInTheDocument()
  })

  it('the <video> sink sizing defaults to intrinsic-capped, object-contain', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()

    expect(screen.getByTestId('browser-live-video').className).toContain('object-contain')
  })

  it('shows the "waiting for first frame" overlay until the video decodes real dimensions, then clears it', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()

    expect(screen.getByTestId('browser-live-waiting-overlay')).toBeInTheDocument()
    expect(screen.getByText('Waiting for the first frame…')).toBeInTheDocument()

    act(() => {
      decodeFrame()
    })

    expect(screen.queryByTestId('browser-live-waiting-overlay')).not.toBeInTheDocument()
  })
})

describe('BrowserLiveView — video mute/unmute control', () => {
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

  it('does not render the mute toggle when hasAudio is false (default)', () => {
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

describe('BrowserLiveView — coordinate mapping (video-mode only)', () => {
  it('dispatches input using the video\'s real decoded dimensions', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    const container = stubFrameRect()
    act(() => {
      decodeFrame(1280, 720)
    })

    fireEvent.pointerDown(container, { clientX: 640, clientY: 360 })

    // 1:1 rect/video size — device coords equal the raw rect-relative client
    // coords (no page_scale-style division; the video path never had one).
    expect(mockSendInput).toHaveBeenCalledWith(expect.objectContaining({ kind: 'mouse_down', x: 640, y: 360 }))
  })

  it('does not dispatch input while the video has not yet reported real dimensions (videoWidth/videoHeight still 0)', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    const container = stubFrameRect()
    // Deliberately do NOT decode a frame — jsdom defaults videoWidth/Height
    // to 0, exactly the pre-`loadedmetadata` state a real <video> starts in.
    // There is no fallback sink to map against instead any more — the click
    // is silently declined (mapPointerToDeviceCoords/activeFrameDims return
    // null), not garbage-dispatched.

    fireEvent.pointerDown(container, { clientX: 640, clientY: 360 })

    expect(mockSendInput).not.toHaveBeenCalledWith(expect.objectContaining({ kind: 'mouse_down' }))
  })
})

describe('BrowserLiveView — annotate crop (video sink only)', () => {
  it('is wired (does not throw/crash) and surfaces a graceful failure toast when the video has no decoded frame yet (readyState 0 in jsdom)', async () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} canAnnotate />)
    connectAndFrame()
    fireEvent.click(screen.getByRole('button', { name: /annotate a region/i }))
    const container = screen.getByTestId('browser-live-frame')

    fireEvent.pointerDown(container, { clientX: 10, clientY: 10 })
    fireEvent.pointerMove(container, { clientX: 200, clientY: 150 })
    fireEvent.pointerUp(container, { clientX: 200, clientY: 150 })

    // cropFrameToFile checks `video.readyState < 2` BEFORE ever touching
    // canvas (jsdom has no `canvas` package either way) — this
    // deterministically exercises that guard without needing to mock video
    // playback or canvas drawing.
    await vi.waitFor(() => {
      expect(useUiStore.getState().toasts.some((t) => /could not capture/i.test(t.message))).toBe(true)
    })
    expect(screen.queryByTestId('annotate-popover')).not.toBeInTheDocument()
  })

  it('does not forward any driving input while annotating (annotate/driving mutual exclusion)', () => {
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

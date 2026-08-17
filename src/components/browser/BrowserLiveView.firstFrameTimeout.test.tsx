// BrowserLiveView.firstFrameTimeout.test.tsx — regression coverage for the
// silent "waiting forever" failure measured live on UAT (2026-08-03).
//
// The panel can be fully connected — WS open, WebRTC stream attached, ZERO
// console errors — while no real frame ever decodes, because the capture
// bound to a tab that is no longer the one being shown. Before this fix the
// panel showed "Waiting for the first frame…" indefinitely and then fell to
// indistinguishable black, with nothing anywhere telling the user it had
// failed. The silence WAS the bug: that state was visually identical to
// "still loading".
//
// Mocks BrowserLiveWsConnection entirely, matching the sibling suites. Video
// attachment is driven via the `mediaStream` test/override seam (see
// BrowserLiveView.webrtcSink.test.tsx) — the video "decoding a real frame"
// signal is `onLoadedMetadata`, fired manually here after stubbing
// videoWidth/videoHeight, mirroring how a real browser reports intrinsic
// size once metadata loads.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { act } from 'react'
import type { BrowserLiveWsCallbacks } from '@/lib/browserLiveWs'
import { DEFAULT_FIRST_ANSWER_TIMEOUT_MS } from '@/lib/browserWebRTC'

const { callbacksRef } = vi.hoisted(() => ({
  callbacksRef: { current: null as BrowserLiveWsCallbacks | null },
}))

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
          sendInput: vi.fn(),
          sendControl: vi.fn(() => true),
          sendViewport: vi.fn(() => true),
          isConnected: true,
        }
      },
    ),
  }
})

import { BrowserLiveView } from './BrowserLiveView'

/** Matches FIRST_FRAME_TIMEOUT_MS in BrowserLiveView.tsx.
 *
 * Bugfix (MED, external review F6, 2026-08-13): this used to be a bare
 * hardcoded 15_000 with no relationship to the real constant, which is
 * exactly how the real one was allowed to drift shorter than
 * browserWebRTC.ts's own cold-start answer budget without any test catching
 * it. Deriving this mirror from the SAME imported constant BrowserLiveView.tsx
 * now uses keeps this file honest about what it's actually asserting against. */
const FIRST_FRAME_TIMEOUT_MS = DEFAULT_FIRST_ANSWER_TIMEOUT_MS + 15_000

/** Stand-in MediaStream — jsdom has no real WebRTC/MediaStream. */
function fakeMediaStream(id = 'stream-1'): MediaStream {
  return { id } as unknown as MediaStream
}

/** Simulates the <video> sink decoding its first real frame — the direct
 * replacement for the old JPEG-era `emitFrame` (which delivered a
 * `browser_screencast` frame over WS). Requires the component to already be
 * `attached` (rendered with a non-null `mediaStream`), since the <video>
 * element only mounts then. */
function decodeFirstFrame() {
  const video = screen.getByTestId('browser-live-video') as HTMLVideoElement
  act(() => {
    Object.defineProperty(video, 'videoWidth', { value: 1280, configurable: true })
    Object.defineProperty(video, 'videoHeight', { value: 720, configurable: true })
    fireEvent.loadedMetadata(video)
  })
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.useFakeTimers()
  callbacksRef.current = null
})

afterEach(() => {
  vi.useRealTimers()
})

describe('BrowserLiveView — first-frame timeout', () => {
  // Bugfix (MED, external review F6, 2026-08-13): FIRST_FRAME_TIMEOUT_MS
  // used to be a fixed 15_000, shorter than browserWebRTC.ts's own
  // DEFAULT_FIRST_ANSWER_TIMEOUT_MS (30_000) cold-start budget — which this
  // timer sits strictly DOWNSTREAM of (a decoded frame can only happen after
  // the answer round trip completes). A healthy cold start could still be
  // legitimately negotiating its first answer at the 30s mark; the panel
  // must not have already declared failure by then.
  it('does not fire before the WebRTC cold-start answer budget elapses (must never be shorter than DEFAULT_FIRST_ANSWER_TIMEOUT_MS)', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    act(() => {
      callbacksRef.current?.onConnected?.()
    })

    act(() => {
      vi.advanceTimersByTime(DEFAULT_FIRST_ANSWER_TIMEOUT_MS - 1)
    })

    // A cold start can legitimately still be waiting on its FIRST answer at
    // this point — the panel must still be honestly "waiting", never already
    // claiming failure.
    expect(screen.getByText('Waiting for the first frame…')).toBeInTheDocument()
    expect(screen.queryByText(/No video received/i)).not.toBeInTheDocument()
  })

  it('surfaces an actionable error when connected and attached but no frame ever decodes', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    act(() => {
      callbacksRef.current?.onConnected?.()
    })

    // Before the deadline the spinner is the honest state.
    expect(screen.getByText('Waiting for the first frame…')).toBeInTheDocument()

    act(() => {
      vi.advanceTimersByTime(FIRST_FRAME_TIMEOUT_MS + 100)
    })

    expect(screen.queryByText('Waiting for the first frame…')).not.toBeInTheDocument()
    expect(screen.getByText(/No video received from the live browser/i)).toBeInTheDocument()
    // The message must point somewhere useful, not just say "error".
    expect(screen.getByText(/tab that is no longer active/i)).toBeInTheDocument()
    // And it must offer a way forward, not just describe the failure.
    expect(screen.getByRole('button', { name: /retry/i })).toBeInTheDocument()
  })

  it('does not fire while still connecting — a slow connect is not a first-frame failure', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    // Never call onConnected, never attach a stream: the panel is still dialing.
    act(() => {
      vi.advanceTimersByTime(FIRST_FRAME_TIMEOUT_MS * 3)
    })

    expect(screen.getByText('Connecting to the live browser…')).toBeInTheDocument()
    expect(screen.queryByText(/No video received/i)).not.toBeInTheDocument()
  })

  it('never fires when a frame decodes before the deadline', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    act(() => {
      callbacksRef.current?.onConnected?.()
    })
    act(() => {
      vi.advanceTimersByTime(FIRST_FRAME_TIMEOUT_MS - 1_000)
    })
    decodeFirstFrame()

    act(() => {
      vi.advanceTimersByTime(FIRST_FRAME_TIMEOUT_MS * 3)
    })

    expect(screen.queryByText(/No video received/i)).not.toBeInTheDocument()
  })

  it('clears the timeout error when a later frame recovers the stream', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    act(() => {
      callbacksRef.current?.onConnected?.()
    })
    act(() => {
      vi.advanceTimersByTime(FIRST_FRAME_TIMEOUT_MS + 100)
    })
    expect(screen.getByText(/No video received/i)).toBeInTheDocument()

    // A recapture (e.g. the tab-activation fix landing a rebind) delivers a
    // frame after all — the panel must recover rather than stay stuck on a
    // stale error.
    decodeFirstFrame()

    expect(screen.queryByText(/No video received/i)).not.toBeInTheDocument()
  })

  it('lets a real transport error win over the generic timeout message', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    act(() => {
      callbacksRef.current?.onConnected?.()
      callbacksRef.current?.onError?.('session not found')
    })
    act(() => {
      vi.advanceTimersByTime(FIRST_FRAME_TIMEOUT_MS + 100)
    })

    // The specific cause is strictly more useful than "no video received".
    expect(screen.getByText('session not found')).toBeInTheDocument()
    expect(screen.queryByText(/No video received/i)).not.toBeInTheDocument()
  })

  it('lets a browser_status error win over the generic timeout message', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    act(() => {
      callbacksRef.current?.onConnected?.()
      callbacksRef.current?.onStatus?.({
        type: 'browser_status',
        state: 'error',
        message: 'This session already has a controller.',
      })
    })
    act(() => {
      vi.advanceTimersByTime(FIRST_FRAME_TIMEOUT_MS + 100)
    })

    expect(screen.getByText('This session already has a controller.')).toBeInTheDocument()
    expect(screen.queryByText(/No video received/i)).not.toBeInTheDocument()
  })
})

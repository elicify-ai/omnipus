// BrowserLiveView.videoHealth.test.tsx — issue #674.
//
// The gap this closes: the gateway knows the instant the capture's ingest
// connection dies (Pion hands it a terminal PeerConnection state), but the
// panel used to learn only by exhausting FIRST_FRAME_TIMEOUT_MS — 45 seconds
// of "Connecting…" over a picture that had already stopped, ending in a
// message about a stale tab that was usually not the cause.
//
// FIRST_FRAME_TIMEOUT_MS is NOT the bug and is deliberately not touched by
// these tests: its value was derived after a live incident where a
// healthy-but-slow cold start showed a red error and then connected fine.
// What these assert is that the panel no longer NEEDS it to find out — the
// reason is on screen long before the deadline could fire.
//
// Harness mirrors the sibling suites (BrowserLiveView.reasonDetail.test.tsx):
// BrowserLiveWsConnection is mocked so a browser_video_health frame can be
// delivered straight into the component's own onVideoHealth callback.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, act } from '@testing-library/react'
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
          sendWebRTCOffer: vi.fn(() => true),
          sendTabAction: vi.fn(() => true),
          isConnected: true,
        }
      },
    ),
  }
})

import { BrowserLiveView } from './BrowserLiveView'

/** Mirrors FIRST_FRAME_TIMEOUT_MS in BrowserLiveView.tsx, derived from the
 * same imported constant so this file cannot drift away from it. */
const FIRST_FRAME_TIMEOUT_MS = DEFAULT_FIRST_ANSWER_TIMEOUT_MS + 15_000

function connect() {
  act(() => {
    callbacksRef.current?.onConnected?.()
  })
}

beforeEach(() => {
  vi.clearAllMocks()
  callbacksRef.current = null
})

afterEach(() => {
  vi.useRealTimers()
})

describe('BrowserLiveView — the panel states a dead video feed promptly (#674)', () => {
  it('shows the loss the moment the gateway reports it, long before the first-frame deadline', () => {
    vi.useFakeTimers()
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connect()

    act(() => {
      callbacksRef.current?.onVideoHealth({
        type: 'browser_video_health',
        session_id: 's1',
        state: 'lost',
        attempt: 1,
        max_attempts: 3,
        detail: 'the live browser video feed stopped — reconnecting automatically',
      })
    })

    expect(screen.getByText(/video stopped\. Reconnecting/i)).toBeInTheDocument()
    expect(screen.getByText(/Reported cause:/i)).toBeInTheDocument()

    // And it was there WITHOUT the deadline having had any part in it: advance
    // to just under FIRST_FRAME_TIMEOUT_MS and the message is unchanged, so it
    // cannot have come from the timeout path.
    act(() => {
      vi.advanceTimersByTime(FIRST_FRAME_TIMEOUT_MS - 1)
    })
    expect(screen.getByText(/video stopped\. Reconnecting/i)).toBeInTheDocument()
    expect(screen.queryByText(/bound to a tab that is no longer active/i)).not.toBeInTheDocument()
  })

  it('names which bounded attempt is running, so the retry does not read as endless', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connect()

    act(() => {
      callbacksRef.current?.onVideoHealth({
        type: 'browser_video_health',
        session_id: 's1',
        state: 'recovering',
        attempt: 2,
        max_attempts: 3,
      })
    })

    expect(screen.getByText(/attempt 2 of 3/i)).toBeInTheDocument()
  })

  it('reports an exhausted recovery as a terminal, named failure with a Retry', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connect()

    act(() => {
      callbacksRef.current?.onVideoHealth({
        type: 'browser_video_health',
        session_id: 's1',
        state: 'unrecoverable',
        attempt: 3,
        max_attempts: 3,
        detail: 'the capture encoder is not producing frames',
      })
    })

    expect(screen.getByText(/could not restart its video after 3 attempts/i)).toBeInTheDocument()
    expect(screen.getByText(/capture encoder is not producing frames/i)).toBeInTheDocument()
    expect(screen.getByTestId('browser-live-retry')).toBeInTheDocument()
  })

  it('clears the message once video comes back', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connect()

    act(() => {
      callbacksRef.current?.onVideoHealth({
        type: 'browser_video_health',
        session_id: 's1',
        state: 'lost',
        attempt: 1,
        max_attempts: 3,
      })
    })
    expect(screen.getByText(/video stopped\. Reconnecting/i)).toBeInTheDocument()

    act(() => {
      callbacksRef.current?.onVideoHealth({
        type: 'browser_video_health',
        session_id: 's1',
        state: 'recovered',
      })
    })

    expect(screen.queryByText(/video stopped\. Reconnecting/i)).not.toBeInTheDocument()
  })
})

// BrowserLiveView.firstFrameTimeout.test.tsx — regression coverage for the
// silent "waiting forever" failure measured live on UAT (2026-08-03).
//
// The panel can be fully connected — WS open, video track readyState:"live",
// muted:false, ZERO console errors — while no frame ever arrives, because the
// capture bound to a tab that is no longer the one being shown. Before this
// fix the panel showed "Waiting for the first frame…" indefinitely and then
// fell to indistinguishable black, with nothing anywhere telling the user it
// had failed. The silence WAS the bug: that state was visually identical to
// "still loading".
//
// Mocks BrowserLiveWsConnection entirely, matching the sibling suites.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { act } from 'react'
import type { BrowserLiveWsCallbacks } from '@/lib/browserLiveWs'

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

/** Matches FIRST_FRAME_TIMEOUT_MS in BrowserLiveView.tsx. */
const FIRST_FRAME_TIMEOUT_MS = 15_000

function emitFrame(seq = 1) {
  act(() => {
    callbacksRef.current?.onScreencast?.({
      type: 'browser_screencast',
      session_id: 's1',
      seq,
      data: 'AAAA',
      width: 1280,
      height: 720,
    })
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
  it('surfaces an actionable error when connected but no frame ever arrives', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    act(() => {
      callbacksRef.current?.onConnected?.()
    })

    // Before the deadline the spinner is the honest state.
    expect(screen.getByText('Waiting for the first frame…')).toBeInTheDocument()

    act(() => {
      vi.advanceTimersByTime(FIRST_FRAME_TIMEOUT_MS + 100)
    })

    expect(screen.queryByText('Waiting for the first frame…')).not.toBeInTheDocument()
    expect(screen.getByText(/No video received from the browser/i)).toBeInTheDocument()
    // The message must point somewhere useful, not just say "error".
    expect(screen.getByText(/tab that is no longer active/i)).toBeInTheDocument()
  })

  it('does not fire while still connecting — a slow connect is not a first-frame failure', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    // Never call onConnected: the panel is still dialing.
    act(() => {
      vi.advanceTimersByTime(FIRST_FRAME_TIMEOUT_MS * 3)
    })

    expect(screen.getByText('Connecting to the live browser…')).toBeInTheDocument()
    expect(screen.queryByText(/No video received/i)).not.toBeInTheDocument()
  })

  it('never fires when a frame arrives before the deadline', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    act(() => {
      callbacksRef.current?.onConnected?.()
    })
    act(() => {
      vi.advanceTimersByTime(FIRST_FRAME_TIMEOUT_MS - 1_000)
    })
    emitFrame()

    act(() => {
      vi.advanceTimersByTime(FIRST_FRAME_TIMEOUT_MS * 3)
    })

    expect(screen.queryByText(/No video received/i)).not.toBeInTheDocument()
  })

  it('clears the timeout error when a later frame recovers the stream', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
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
    emitFrame(2)

    expect(screen.queryByText(/No video received/i)).not.toBeInTheDocument()
  })

  it('lets a real transport error win over the generic timeout message', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
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
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
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

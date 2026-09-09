// BrowserLiveView.reasonDetail.test.tsx — UAT case 16.
//
// The panel used to answer every start-video failure with one sentence:
// "The live browser reported an error starting video. Retry, or reload the
// page if it keeps failing." At that same second the gateway log held the
// actual cause — "capture session: create encoder target: browser: timed out
// after 20s waiting for the browser to attach the tab (target may be
// unresponsive)" — and the operator never saw it.
//
// ADR-061 deleted the JPEG fallback precisely because a failure nobody can
// see hides the real defect indefinitely. A failure everybody can see but
// nobody can diagnose gives that back one level up. The browser_attach path
// already surfaces its full error chain (browser_ws.go sends
// "browser_attach failed: %s" as free text on browser_status); these tests
// hold the start-video path to the same standard.
//
// Harness mirrors the sibling suites: BrowserLiveWsConnection is mocked so a
// browser_webrtc_state frame can be delivered straight into the component's
// own onWebRTCState callback.

import { describe, it, expect, vi, beforeEach } from 'vitest'
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
          sendWebRTCOffer: vi.fn(() => true),
          sendTabAction: vi.fn(() => true),
          isConnected: true,
        }
      },
    ),
  }
})

import { BrowserLiveView } from './BrowserLiveView'

/** The verbatim cause an operator hit on UAT. */
const REAL_CAUSE =
  'capture session: create encoder target: browser: timed out after 20s waiting for the browser to attach the tab (target may be unresponsive)'

/** Delivers a browser_webrtc_state failure frame exactly as the gateway
 * sends one from handleWebRTCOffer's Start-failure branch. */
function deliverFailure(detail?: string) {
  act(() => {
    callbacksRef.current?.onConnected?.()
  })
  act(() => {
    callbacksRef.current?.onWebRTCState({
      type: 'browser_webrtc_state',
      session_id: 's1',
      available: false,
      reason: 'error',
      ...(detail === undefined ? {} : { reason_detail: detail }),
    })
  })
}

beforeEach(() => {
  vi.clearAllMocks()
  callbacksRef.current = null
})

describe('BrowserLiveView — a video failure names its cause (UAT case 16)', () => {
  it('shows the gateway-reported cause, not only the generic sentence', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    deliverFailure(REAL_CAUSE)

    expect(
      screen.getByText(/timed out after 20s waiting for the browser to attach the tab/i),
    ).toBeInTheDocument()
  })

  it('keeps the plain-language category and the Retry affordance alongside it', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    deliverFailure(REAL_CAUSE)

    expect(screen.getByText(/reported an error starting video/i)).toBeInTheDocument()
    expect(screen.getByTestId('browser-live-retry')).toBeInTheDocument()
  })

  // "Never leave a stale last frame on screen." The failure handler clears
  // the stream (setWebrtcStream(null)) and the decoded-frame flag before the
  // error is rendered, so the panel drops out of its attached state entirely
  // — the error IS the panel, not a strip over a frozen picture.
  //
  // Driven WITHOUT the `mediaStream` prop on purpose: that prop is a test
  // override that wins outright over the internal stream
  // (`mediaStreamProp ?? webrtcStream`), so a test that passed one would be
  // asserting against a stream the component is not allowed to clear, and
  // would pass or fail for reasons unrelated to the handler.
  it('renders the failure as the panel itself, with no video sink left mounted', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    deliverFailure(REAL_CAUSE)

    expect(screen.queryByTestId('browser-live-video')).not.toBeInTheDocument()
    expect(
      screen.getByText(/timed out after 20s waiting for the browser to attach the tab/i),
    ).toBeInTheDocument()
    expect(screen.queryByText(/Waiting for the first frame/i)).not.toBeInTheDocument()
  })

  it('falls back to the category sentence alone when the gateway sent no cause', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    deliverFailure()

    expect(screen.getByText(/reported an error starting video/i)).toBeInTheDocument()
    expect(screen.queryByText(/Reported cause:/i)).not.toBeInTheDocument()
  })
})

// BrowserLiveView.controlToggle.test.tsx — ADR-040 D2 implicit control model
// (formerly the ADR-038 explicit Take/Release control toggle, removed
// entirely per D1). Mocks BrowserLiveWsConnection entirely (unit-tests the
// component's reaction to callbacks, not the real WS transport — that's
// covered separately in src/lib/browserLiveWs.test.ts).
//
// "Driving" is simulated the same way BrowserLiveView.mouseMoveThrottle.test.tsx
// does — by emitting a browser_status{state:'controlling'} frame directly —
// since there is no longer an explicit button to click to acquire the lock.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { act } from 'react'
import type { BrowserLiveWsCallbacks } from '@/lib/browserLiveWs'

const { mockSendControl, mockSendInput, mockConnect, mockDetach, mockClose, callbacksRef } = vi.hoisted(() => ({
  // Returns `true` by default (mirrors a successful send on an OPEN socket) —
  // see BrowserLiveView.takeTheWheel.test.tsx's identical hoisted mock for
  // why this matters (the auto-release effect now reacts to a falsy return).
  mockSendControl: vi.fn(() => true),
  mockSendInput: vi.fn(),
  mockConnect: vi.fn(),
  mockDetach: vi.fn(),
  mockClose: vi.fn(),
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
          connect: mockConnect,
          detach: mockDetach,
          close: mockClose,
          sendInput: mockSendInput,
          sendControl: mockSendControl,
          isConnected: true,
        }
      },
    ),
  }
})

import { BrowserLiveView } from './BrowserLiveView'

/** Stubs the frame container's layout rect to a clean 1:1 box so
 * mapClientToDevice never short-circuits to null (jsdom reports all-zero
 * rects by default) — same technique as the mouseMoveThrottle test suite. */
function stubFrameRect() {
  const container = screen.getByTestId('browser-live-frame')
  vi.spyOn(container, 'getBoundingClientRect').mockReturnValue({
    left: 0, top: 0, width: 1280, height: 720, right: 1280, bottom: 720, x: 0, y: 0,
    toJSON() { return {} },
  } as DOMRect)
  return container
}

function connectAndFrame() {
  act(() => {
    callbacksRef.current?.onConnected?.()
    callbacksRef.current?.onScreencast?.({
      type: 'browser_screencast',
      session_id: 's1',
      seq: 1,
      data: 'AAAA',
      width: 1280,
      height: 720,
    })
  })
}

beforeEach(() => {
  vi.clearAllMocks()
  callbacksRef.current = null
})

describe('BrowserLiveView — connection lifecycle chip (ADR-040 D6)', () => {
  it('starts in the "Connecting…" chip state', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    expect(screen.getByTestId('browser-live-status-chip')).toHaveTextContent('Connecting…')
  })

  it('moves to "Click to drive" once connected with no frame-driving state yet', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    act(() => {
      callbacksRef.current?.onConnected?.()
    })
    expect(screen.getByTestId('browser-live-status-chip')).toHaveTextContent('Click to drive')
  })

  it('reflects a browser_status "controlling" frame as "You\'re driving"', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    act(() => {
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'controlling' })
    })
    expect(screen.getByTestId('browser-live-status-chip')).toHaveTextContent("You're driving")
  })

  it('reverts to "Click to drive" once status moves back to released', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    act(() => {
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'controlling' })
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'released' })
    })
    expect(screen.getByTestId('browser-live-status-chip')).toHaveTextContent('Click to drive')
  })

  it('shows an error chip + message when a server error frame arrives', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    act(() => {
      callbacksRef.current?.onConnected?.()
      callbacksRef.current?.onError?.('session not found')
    })

    expect(screen.getByTestId('browser-live-status-chip')).toHaveTextContent('Error')
    expect(screen.getByText('session not found')).toBeInTheDocument()
  })

  // Reviewer finding: browser_status.message was discarded (only f.state was kept),
  // so a terminal browser_status{state:'error', message:...} — already-controlled,
  // take-control-disabled, no-manager-for-agent, live-view-disabled, malformed
  // control — left the user stuck on the "Connecting…" spinner forever, with no
  // frame ever going to arrive to break out of the `!frame` branch.
  it('surfaces a browser_status error message before any frame arrives, stopping the connecting spinner', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    act(() => {
      callbacksRef.current?.onConnected?.()
      callbacksRef.current?.onStatus?.({
        type: 'browser_status',
        state: 'error',
        message: 'This session already has a controller.',
      })
    })

    expect(screen.getByTestId('browser-live-status-chip')).toHaveTextContent('Error')
    expect(screen.getByText('This session already has a controller.')).toBeInTheDocument()
    expect(screen.queryByText('Connecting to the live browser…')).not.toBeInTheDocument()
    expect(screen.queryByText('Waiting for the first frame…')).not.toBeInTheDocument()
  })

  it('surfaces a browser_status error message in the persistent strip once a frame has already arrived', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    act(() => {
      callbacksRef.current?.onStatus?.({
        type: 'browser_status',
        state: 'error',
        message: 'Live view is disabled for this agent.',
      })
    })

    expect(screen.getByTestId('browser-live-status-chip')).toHaveTextContent('Error')
    expect(screen.getByRole('alert')).toHaveTextContent('Live view is disabled for this agent.')
  })

  // Reviewer finding: onDisconnected only cleared `connected`, leaving statusState
  // (and therefore isControlling/controllingRef) at 'controlling' — the chip kept
  // claiming "You're driving" and pointer/keyboard handlers kept attempting
  // sendInput (silently dropped by the transport) for the whole reconnect window.
  it('reverts control state and stops accepting input when the socket disconnects mid-control', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    act(() => {
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'controlling' })
    })
    expect(screen.getByTestId('browser-live-status-chip')).toHaveTextContent("You're driving")

    act(() => {
      callbacksRef.current?.onDisconnected?.()
    })

    // Chip drops out of "controlling" — no human is actually driving anything
    // once the transport is gone.
    expect(screen.getByTestId('browser-live-status-chip')).toHaveTextContent('Reconnecting…')
    // The synthetic cursor overlay is control-gated too — it must clear.
    expect(screen.queryByTestId('synthetic-cursor')).not.toBeInTheDocument()

    // Pointer/keyboard handlers must no-op while disconnected, not silently
    // attempt (and drop) a send — click-to-drive must not fire against a
    // dead transport either (ADR-040 D2 addition: connectedRef guard).
    mockSendInput.mockClear()
    mockSendControl.mockClear()
    const container = stubFrameRect()
    fireEvent.pointerMove(container, { clientX: 20, clientY: 20 })
    fireEvent.pointerDown(container, { clientX: 20, clientY: 20 })
    fireEvent.pointerUp(container, { clientX: 20, clientY: 20 })
    fireEvent.keyDown(container, { key: 'a' })
    fireEvent.keyUp(container, { key: 'a' })
    expect(mockSendInput).not.toHaveBeenCalled()
    expect(mockSendControl).not.toHaveBeenCalled()
  })

  // Reviewer finding: onStatus overwrote statusState with EVERY frame,
  // including the routine `error` state a blocked `navigate` produces —
  // that flipped isControlling to false (URL bar/cursor vanish) even though
  // the server never actually released control. A per-request error must
  // surface without dropping the "You're driving" state.
  it('keeps "You\'re driving" and shows the error when a browser_status error frame arrives while controlling', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    act(() => {
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'controlling' })
    })
    expect(screen.getByTestId('browser-live-status-chip')).toHaveTextContent("You're driving")

    act(() => {
      callbacksRef.current?.onStatus?.({
        type: 'browser_status',
        state: 'error',
        message: 'Navigation blocked: target resolves to a private address.',
      })
    })

    // Still driving — the chip must NOT flip to "Error".
    expect(screen.getByTestId('browser-live-status-chip')).toHaveTextContent("You're driving")
    // But the error itself is still surfaced.
    expect(screen.getByRole('alert')).toHaveTextContent('Navigation blocked: target resolves to a private address.')

    // And input keeps flowing — the human is still actually in control.
    mockSendInput.mockClear()
    const container = stubFrameRect()
    fireEvent.pointerDown(container, { clientX: 20, clientY: 20 })
    expect(mockSendInput).toHaveBeenCalledWith(expect.objectContaining({ kind: 'mouse_down' }))
  })

  // Reviewer finding F1(a): a `control_only` frame's SOLE purpose is to
  // broadcast a control-ownership change to OTHER viewers of the session —
  // it arrives as state:'idle' with no message. Treating it like any other
  // status frame used to WIPE a real, still-valid error banner shown on
  // this viewer.
  it('does not clear a pre-set error banner when a control_only frame arrives', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    act(() => {
      callbacksRef.current?.onStatus?.({
        type: 'browser_status',
        state: 'error',
        message: 'Live view is disabled for this agent.',
      })
    })
    expect(screen.getByRole('alert')).toHaveTextContent('Live view is disabled for this agent.')

    act(() => {
      callbacksRef.current?.onStatus?.({
        type: 'browser_status',
        state: 'idle',
        control_only: true,
      })
    })

    // The error banner — and the Error chip — must survive: a control-only
    // broadcast is not a real lifecycle/error change.
    expect(screen.getByRole('alert')).toHaveTextContent('Live view is disabled for this agent.')
    expect(screen.getByTestId('browser-live-status-chip')).toHaveTextContent('Error')
  })

  // Reviewer finding F1(b): a tab-death/error frame omits
  // `controlled_by_other` entirely (it's not a control-ownership frame) —
  // resetting it to false via `?? false` wrongly re-enabled click-to-drive
  // while another viewer was still actually driving.
  it('does not reset a pre-set controlledByOther when an error frame omits the field', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    act(() => {
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'attached', controlled_by_other: true })
    })
    expect(screen.getByTestId('browser-live-status-chip')).toHaveTextContent('Someone else is driving')

    act(() => {
      callbacksRef.current?.onStatus?.({
        type: 'browser_status',
        state: 'error',
        message: 'browser session terminated unexpectedly',
      })
    })

    // Still gated at the STATE level — the error frame never reported
    // controlled_by_other, so the prior true value must be left alone (the
    // chip itself now shows "Error", since an active error takes display
    // priority — but the underlying controlledByOther guard must survive,
    // proven below by the click NOT sending `take`).
    expect(screen.getByTestId('browser-live-status-chip')).toHaveTextContent('Error')
    expect(screen.getByRole('alert')).toHaveTextContent('browser session terminated unexpectedly')
    mockSendControl.mockClear()
    const container = stubFrameRect()
    fireEvent.pointerDown(container, { clientX: 20, clientY: 20 })
    expect(mockSendControl).not.toHaveBeenCalled()
  })

  it('calls detach() then close() on unmount so the backend engine can ref-count down', () => {
    const { unmount } = render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    unmount()
    expect(mockDetach).toHaveBeenCalledTimes(1)
    expect(mockClose).toHaveBeenCalledTimes(1)
  })

  it('renders the Pop out / Close buttons only when the corresponding callback prop is provided', () => {
    const { rerender } = render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    expect(screen.queryByRole('button', { name: /pop out/i })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /close live browser panel/i })).not.toBeInTheDocument()

    rerender(<BrowserLiveView sessionId="s1" agentId="a1" onPopOut={vi.fn()} onClose={vi.fn()} />)
    expect(screen.getByRole('button', { name: /pop out/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /close live browser panel/i })).toBeInTheDocument()
  })

  // ADR-040 D1: the old explicit Take control / Release control toggle and
  // the Hand-to-agent button are both gone entirely.
  it('never renders a Take control / Release control / Hand to agent button', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    act(() => {
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'controlling' })
    })
    expect(screen.queryByRole('button', { name: /take control/i })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /release control/i })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /hand to agent/i })).not.toBeInTheDocument()
  })
})

// The ADR-040 D1/D4 Pin toggle was retired 2026-07-16 (operator direction):
// the panel is ALWAYS docked when open, so there is no overlay layout to
// pin/unpin between and the view renders no pin control at all. These
// negative guards keep it retired.
describe('BrowserLiveView — Pin toggle retired (always-docked)', () => {
  it('never renders a pin/unpin button', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" onPopOut={vi.fn()} onClose={vi.fn()} />)
    expect(screen.queryByRole('button', { name: /pin live browser panel/i })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /unpin live browser panel/i })).not.toBeInTheDocument()
  })

  it('renders Pop out purely on onPopOut presence (the fullscreen escape from the docked panel)', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" onPopOut={vi.fn()} />)
    expect(screen.getByRole('button', { name: /pop out/i })).toBeInTheDocument()
  })
})

// BrowserLiveView.controlToggle.test.tsx — ADR-038 D5/D6 take/release control
// toggle behaviour. Mocks BrowserLiveWsConnection entirely (unit-tests the
// component's reaction to callbacks, not the real WS transport — that's
// covered separately in src/lib/browserLiveWs.test.ts).

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { act } from 'react'
import type { BrowserLiveWsCallbacks } from '@/lib/browserLiveWs'

const { mockSendControl, mockSendInput, mockConnect, mockDetach, mockClose, callbacksRef } = vi.hoisted(() => ({
  mockSendControl: vi.fn(),
  mockSendInput: vi.fn(),
  mockConnect: vi.fn(),
  mockDetach: vi.fn(),
  mockClose: vi.fn(),
  callbacksRef: { current: null as BrowserLiveWsCallbacks | null },
}))

vi.mock('@/lib/browserLiveWs', () => ({
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
}))

import { BrowserLiveView } from './BrowserLiveView'

beforeEach(() => {
  vi.clearAllMocks()
  callbacksRef.current = null
})

describe('BrowserLiveView — control toggle', () => {
  it('starts in the "Connecting…" pill state with the take-control button disabled', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    expect(screen.getByTestId('browser-live-status-pill')).toHaveTextContent('Connecting…')
    expect(screen.getByRole('button', { name: /take control/i })).toBeDisabled()
  })

  it('enables Take control once onConnected fires, and clicking it sends control:take', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    act(() => {
      callbacksRef.current?.onConnected?.()
    })

    const button = screen.getByRole('button', { name: /take control/i })
    expect(button).not.toBeDisabled()

    fireEvent.click(button)
    expect(mockSendControl).toHaveBeenCalledWith('take')
  })

  it('reflects a browser_status "controlling" frame as "You\'re driving" / Release control', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    act(() => {
      callbacksRef.current?.onConnected?.()
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'controlling' })
    })

    expect(screen.getByTestId('browser-live-status-pill')).toHaveTextContent("You're driving")

    const button = screen.getByRole('button', { name: /release control/i })
    fireEvent.click(button)
    expect(mockSendControl).toHaveBeenCalledWith('release')
  })

  it('reverts to "Agent driving" once status moves back to released', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    act(() => {
      callbacksRef.current?.onConnected?.()
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'controlling' })
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'released' })
    })

    expect(screen.getByTestId('browser-live-status-pill')).toHaveTextContent('Agent driving')
    expect(screen.getByRole('button', { name: /take control/i })).toBeInTheDocument()
  })

  it('shows an error pill + message when a server error frame arrives', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    act(() => {
      callbacksRef.current?.onConnected?.()
      callbacksRef.current?.onError?.('session not found')
    })

    expect(screen.getByTestId('browser-live-status-pill')).toHaveTextContent('Error')
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

    expect(screen.getByTestId('browser-live-status-pill')).toHaveTextContent('Error')
    expect(screen.getByText('This session already has a controller.')).toBeInTheDocument()
    expect(screen.queryByText('Connecting to the live browser…')).not.toBeInTheDocument()
    expect(screen.queryByText('Waiting for the first frame…')).not.toBeInTheDocument()
  })

  it('surfaces a browser_status error message in the persistent strip once a frame has already arrived', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
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
      callbacksRef.current?.onStatus?.({
        type: 'browser_status',
        state: 'error',
        message: 'Live view is disabled for this agent.',
      })
    })

    expect(screen.getByTestId('browser-live-status-pill')).toHaveTextContent('Error')
    expect(screen.getByRole('alert')).toHaveTextContent('Live view is disabled for this agent.')
  })

  // Reviewer finding: onDisconnected only cleared `connected`, leaving statusState
  // (and therefore isControlling/controllingRef) at 'controlling' — the pill kept
  // claiming "You're driving" and pointer/keyboard handlers kept attempting
  // sendInput (silently dropped by the transport) for the whole reconnect window.
  it('reverts control state and stops accepting input when the socket disconnects mid-control', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
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
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'controlling' })
    })
    expect(screen.getByTestId('browser-live-status-pill')).toHaveTextContent("You're driving")
    expect(screen.getByRole('button', { name: /release control/i })).not.toBeDisabled()

    act(() => {
      callbacksRef.current?.onDisconnected?.()
    })

    // Pill drops out of "controlling" — no human is actually driving anything
    // once the transport is gone.
    expect(screen.getByTestId('browser-live-status-pill')).toHaveTextContent('Reconnecting…')
    // The toggle button re-derives its disabled state from `connected`, which
    // onDisconnected also clears.
    expect(screen.getByRole('button', { name: /take control/i })).toBeDisabled()
    // The synthetic cursor overlay is control-gated too — it must clear.
    expect(screen.queryByTestId('synthetic-cursor')).not.toBeInTheDocument()

    // Pointer/keyboard handlers must no-op while disconnected, not silently
    // attempt (and drop) a send.
    mockSendInput.mockClear()
    const container = screen.getByTestId('browser-live-frame')
    fireEvent.pointerMove(container, { clientX: 20, clientY: 20 })
    fireEvent.pointerDown(container, { clientX: 20, clientY: 20 })
    fireEvent.pointerUp(container, { clientX: 20, clientY: 20 })
    fireEvent.keyDown(container, { key: 'a' })
    fireEvent.keyUp(container, { key: 'a' })
    expect(mockSendInput).not.toHaveBeenCalled()
  })

  // Reviewer finding: onStatus overwrote statusState with EVERY frame,
  // including the routine `error` state a blocked `navigate` produces —
  // that flipped isControlling to false (URL bar/cursor vanish, Take
  // control reappears) even though the server never released control. A
  // per-request error must surface without dropping the "You're driving"
  // state.
  it('keeps "You\'re driving" and shows the error when a browser_status error frame arrives while controlling', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
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
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'controlling' })
    })
    expect(screen.getByTestId('browser-live-status-pill')).toHaveTextContent("You're driving")

    act(() => {
      callbacksRef.current?.onStatus?.({
        type: 'browser_status',
        state: 'error',
        message: 'Navigation blocked: target resolves to a private address.',
      })
    })

    // Still driving — the pill must NOT flip to "Error" / the toggle must
    // NOT revert to "Take control".
    expect(screen.getByTestId('browser-live-status-pill')).toHaveTextContent("You're driving")
    expect(screen.getByRole('button', { name: /release control/i })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^take control$/i })).not.toBeInTheDocument()
    // But the error itself is still surfaced.
    expect(screen.getByRole('alert')).toHaveTextContent('Navigation blocked: target resolves to a private address.')

    // And input keeps flowing — the human is still actually in control.
    // jsdom performs no real layout (getBoundingClientRect() returns a 0×0
    // rect by default), which mapClientToDevice treats as "unmeasurable" and
    // no-ops on — stub it so a real device coordinate resolves, same pattern
    // as BrowserLiveView.mouseMoveThrottle.test.tsx's mountControllingWithFrame.
    mockSendInput.mockClear()
    const container = screen.getByTestId('browser-live-frame')
    vi.spyOn(container, 'getBoundingClientRect').mockReturnValue({
      left: 0, top: 0, width: 1280, height: 720, right: 1280, bottom: 720, x: 0, y: 0,
      toJSON() { return {} },
    } as DOMRect)
    fireEvent.pointerDown(container, { clientX: 20, clientY: 20 })
    expect(mockSendInput).toHaveBeenCalledWith(expect.objectContaining({ kind: 'mouse_down' }))
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
})

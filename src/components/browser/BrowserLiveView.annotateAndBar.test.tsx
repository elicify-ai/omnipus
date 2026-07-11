// BrowserLiveView.annotateAndBar.test.tsx — ADR-039 D-A2 (URL bar),
// D-A3 (hand to agent), and D-B1/B2 (annotate mode ⟷ take-control mutual
// exclusion) behaviour. Mocks BrowserLiveWsConnection entirely, same pattern
// as BrowserLiveView.controlToggle.test.tsx.
//
// The full drag→crop→popover→send success path needs a real canvas 2D
// context, which jsdom does not provide (no `canvas` package installed —
// see media-actions.test.ts's precedent and browserAnnotate.test.ts, which
// covers the post-crop upload/inspect/send orchestration directly). What IS
// exercised here is the real jsdom behaviour: a drag attempts the crop and
// gracefully surfaces a "could not capture" toast when canvas is
// unavailable, without crashing or leaving the UI in a stuck state.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { act } from 'react'
import type { BrowserLiveWsCallbacks } from '@/lib/browserLiveWs'
import { useUiStore } from '@/store/ui'

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

function takeControl() {
  act(() => {
    callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'controlling' })
  })
}

beforeEach(() => {
  vi.clearAllMocks()
  callbacksRef.current = null
  useUiStore.setState({ composerPrefill: null, toasts: [] })
})

describe('BrowserLiveView — URL bar (ADR-039 D-A2)', () => {
  it('does not render the address bar while not controlling', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    expect(screen.queryByRole('textbox', { name: /navigate to url/i })).not.toBeInTheDocument()
  })

  it('renders the address bar once the viewer takes control', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    takeControl()
    expect(screen.getByRole('textbox', { name: /navigate to url/i })).toBeInTheDocument()
  })

  it('normalizes a bare hostname and sends a navigate input frame on submit', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    takeControl()

    const input = screen.getByRole('textbox', { name: /navigate to url/i })
    fireEvent.change(input, { target: { value: 'example.com' } })
    fireEvent.click(screen.getByRole('button', { name: /^go$/i }))

    expect(mockSendInput).toHaveBeenCalledWith({ kind: 'navigate', url: 'https://example.com' })
  })

  it('does not send when the address bar is empty', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    takeControl()

    expect(screen.getByRole('button', { name: /^go$/i })).toBeDisabled()
    expect(mockSendInput).not.toHaveBeenCalledWith(expect.objectContaining({ kind: 'navigate' }))
  })

  it('leaves an explicit https:// URL untouched', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    takeControl()

    const input = screen.getByRole('textbox', { name: /navigate to url/i })
    fireEvent.change(input, { target: { value: 'https://example.com/path' } })
    fireEvent.click(screen.getByRole('button', { name: /^go$/i }))

    expect(mockSendInput).toHaveBeenCalledWith({ kind: 'navigate', url: 'https://example.com/path' })
  })
})

describe('BrowserLiveView — Hand to agent (ADR-039 D-A3)', () => {
  it('is not rendered while not controlling', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    expect(screen.queryByRole('button', { name: /hand to agent/i })).not.toBeInTheDocument()
  })

  it('releases control and drops a hint into the composer-prefill bridge', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    takeControl()

    fireEvent.click(screen.getByRole('button', { name: /hand to agent/i }))

    expect(mockSendControl).toHaveBeenCalledWith('release')
    expect(useUiStore.getState().composerPrefill).toBe('Continue from the current page: ')
    expect(useUiStore.getState().toasts.some((t) => /control released/i.test(t.message))).toBe(true)
  })
})

describe('BrowserLiveView — Annotate mode ⟷ take-control mutual exclusion (ADR-039 D-B1/B2)', () => {
  it('disables Take control while annotate mode is active', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()

    fireEvent.click(screen.getByRole('button', { name: /annotate a region/i }))
    expect(screen.getByRole('button', { name: /take control/i })).toBeDisabled()
  })

  it('releases control automatically when entering annotate mode while driving', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    takeControl()

    fireEvent.click(screen.getByRole('button', { name: /annotate a region/i }))
    expect(mockSendControl).toHaveBeenCalledWith('release')
  })

  it('exiting annotate mode re-enables Take control', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()

    fireEvent.click(screen.getByRole('button', { name: /annotate a region/i }))
    fireEvent.click(screen.getByRole('button', { name: /exit annotate mode/i }))
    expect(screen.getByRole('button', { name: /take control/i })).not.toBeDisabled()
  })

  it('sets a crosshair cursor over the frame while annotating', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    fireEvent.click(screen.getByRole('button', { name: /annotate a region/i }))

    expect(screen.getByTestId('browser-live-frame')).toHaveStyle({ cursor: 'crosshair' })
  })

  it('a drag inside the frame does NOT forward any control input while annotating', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    fireEvent.click(screen.getByRole('button', { name: /annotate a region/i }))

    const container = screen.getByTestId('browser-live-frame')
    fireEvent.pointerDown(container, { clientX: 10, clientY: 10 })
    fireEvent.pointerMove(container, { clientX: 60, clientY: 80 })
    fireEvent.pointerUp(container, { clientX: 60, clientY: 80 })

    expect(mockSendInput).not.toHaveBeenCalled()
  })

  it('renders a live selection-box overlay while dragging in annotate mode', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    fireEvent.click(screen.getByRole('button', { name: /annotate a region/i }))

    const container = screen.getByTestId('browser-live-frame')
    fireEvent.pointerDown(container, { clientX: 10, clientY: 10 })
    fireEvent.pointerMove(container, { clientX: 60, clientY: 80 })

    expect(screen.getByTestId('annotate-selection-box')).toBeInTheDocument()
  })

  // jsdom performs no real layout (getBoundingClientRect() returns a 0×0
  // rect) and has no canvas 2D context (no `canvas` package installed), so
  // the real crop step always fails somewhere along the way here — this
  // asserts that ANY failure in that chain is surfaced as a toast and the
  // UI recovers cleanly (selection cleared, no crash, no stuck state)
  // rather than testing the success path (covered by
  // browserLiveCoords.test.ts's computeCropRect/mapClientToFramePixels and
  // browserAnnotate.test.ts's post-crop orchestration).
  it('a completed drag attempts the crop and surfaces a graceful failure toast when capture is unavailable', async () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    fireEvent.click(screen.getByRole('button', { name: /annotate a region/i }))

    const container = screen.getByTestId('browser-live-frame')
    fireEvent.pointerDown(container, { clientX: 10, clientY: 10 })
    fireEvent.pointerMove(container, { clientX: 200, clientY: 150 })
    fireEvent.pointerUp(container, { clientX: 200, clientY: 150 })

    await vi.waitFor(() => {
      expect(useUiStore.getState().toasts.some((t) => /could not capture/i.test(t.message))).toBe(true)
    })
    // No popover — the crop never succeeded.
    expect(screen.queryByTestId('annotate-popover')).not.toBeInTheDocument()
  })
})

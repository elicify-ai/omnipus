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
    render(<BrowserLiveView sessionId="s1" agentId="a1" onHandToAgent={vi.fn()} />)
    connectAndFrame()
    expect(screen.queryByRole('button', { name: /hand to agent/i })).not.toBeInTheDocument()
  })

  it('releases control and invokes the onHandToAgent callback', () => {
    const onHandToAgent = vi.fn()
    render(<BrowserLiveView sessionId="s1" agentId="a1" onHandToAgent={onHandToAgent} />)
    connectAndFrame()
    takeControl()

    fireEvent.click(screen.getByRole('button', { name: /hand to agent/i }))

    expect(mockSendControl).toHaveBeenCalledWith('release')
    expect(onHandToAgent).toHaveBeenCalledTimes(1)
  })

  // Reviewer finding (CRITICAL): the pop-out window (routes/_app/browser-live.tsx)
  // is a separate `window.open` JS realm with its own useUiStore instance —
  // ChatScreen (the only composerPrefill consumer) isn't mounted there, so
  // writing composerPrefill from that realm is a silent no-op with a false
  // success toast. The fix hides the button entirely when the host doesn't
  // provide onHandToAgent, rather than rendering a control that does nothing.
  it('is hidden even while controlling when onHandToAgent is not provided (e.g. the pop-out window)', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    takeControl()

    expect(screen.queryByRole('button', { name: /hand to agent/i })).not.toBeInTheDocument()
    // Release control is still available from the pop-out — only the
    // agent-hand-off affordance is gated on onHandToAgent.
    expect(screen.getByRole('button', { name: /release control/i })).toBeInTheDocument()
  })
})

describe('BrowserLiveView — Annotate mode ⟷ take-control mutual exclusion (ADR-039 D-B1/B2)', () => {
  it('disables Take control while annotate mode is active', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" canAnnotate />)
    connectAndFrame()

    fireEvent.click(screen.getByRole('button', { name: /annotate a region/i }))
    expect(screen.getByRole('button', { name: /take control/i })).toBeDisabled()
  })

  it('releases control automatically when entering annotate mode while driving', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" canAnnotate />)
    connectAndFrame()
    takeControl()

    fireEvent.click(screen.getByRole('button', { name: /annotate a region/i }))
    expect(mockSendControl).toHaveBeenCalledWith('release')
  })

  // Reviewer finding (CRITICAL race): sendControl('release') is async — the
  // server's browser_status frame (which is what actually flips
  // isControlling) has NOT arrived yet by the time handleToggleAnnotate
  // returns, so isControlling is still stale-true on the very next render.
  // A reactive `if (isControlling && annotateMode) setAnnotateMode(false)`
  // effect used to see that stale-true value and immediately revert the
  // toggle the user just clicked — "Annotate" silently no-op'd on the first
  // click while driving. Mutual exclusion is enforced procedurally instead
  // (release-before-entering + Take-control disabled + pointer handlers
  // branching on annotateMode first), so annotate mode must actually engage
  // here, well before any browser_status('released') round-trip.
  it('actually enters annotate mode on the first click while driving, despite the async release gap', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" canAnnotate />)
    connectAndFrame()
    takeControl()

    fireEvent.click(screen.getByRole('button', { name: /annotate a region/i }))

    // No browser_status('released') frame has arrived — the mock ws never
    // emits one on its own — so this exercises exactly the stale-isControlling
    // window the race lived in.
    expect(screen.getByRole('button', { name: /exit annotate mode/i })).toBeInTheDocument()
    expect(screen.queryByRole('textbox', { name: /navigate to url/i })).not.toBeInTheDocument()
    expect(screen.getByTestId('browser-live-frame')).toHaveStyle({ cursor: 'crosshair' })
  })

  it('exiting annotate mode re-enables Take control', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" canAnnotate />)
    connectAndFrame()

    fireEvent.click(screen.getByRole('button', { name: /annotate a region/i }))
    fireEvent.click(screen.getByRole('button', { name: /exit annotate mode/i }))
    expect(screen.getByRole('button', { name: /take control/i })).not.toBeDisabled()
  })

  it('sets a crosshair cursor over the frame while annotating', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" canAnnotate />)
    connectAndFrame()
    fireEvent.click(screen.getByRole('button', { name: /annotate a region/i }))

    expect(screen.getByTestId('browser-live-frame')).toHaveStyle({ cursor: 'crosshair' })
  })

  it('a drag inside the frame does NOT forward any control input while annotating', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" canAnnotate />)
    connectAndFrame()
    fireEvent.click(screen.getByRole('button', { name: /annotate a region/i }))

    const container = screen.getByTestId('browser-live-frame')
    fireEvent.pointerDown(container, { clientX: 10, clientY: 10 })
    fireEvent.pointerMove(container, { clientX: 60, clientY: 80 })
    fireEvent.pointerUp(container, { clientX: 60, clientY: 80 })

    expect(mockSendInput).not.toHaveBeenCalled()
  })

  it('renders a live selection-box overlay while dragging in annotate mode', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" canAnnotate />)
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
    render(<BrowserLiveView sessionId="s1" agentId="a1" canAnnotate />)
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

  // UAT finding FE-5: entering annotate mode releases control, which used to
  // flip the header pill to the control-derived "Agent driving" label even
  // though the user is actively mid-annotation and Take-control is disabled.
  it('shows an "annotating" status pill instead of the control-derived label', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" canAnnotate />)
    connectAndFrame()

    fireEvent.click(screen.getByRole('button', { name: /annotate a region/i }))

    expect(screen.getByTestId('browser-live-status-pill')).toHaveTextContent(/annotating/i)
    expect(screen.getByTestId('browser-live-status-pill')).not.toHaveTextContent(/agent driving/i)
  })
})

// UAT finding FE-4: annotate needs the chat (submitAnnotation sends through
// useChatStore directly), which only a host sharing the SAME JS realm as
// ChatScreen can deliver on. The fullscreen pop-out (routes/_app/browser-live.tsx)
// is a separate `window.open` document with no chat store — starting an
// annotation there could never succeed and only "Cancel" escaped it, losing
// the drafted comment. `canAnnotate` defaults to false (mirrors onHandToAgent's
// opt-in-only pattern) so a host must explicitly declare it can deliver
// annotate to chat; the pop-out route deliberately omits the prop.
describe('BrowserLiveView — Annotate visibility gate (ADR-039 D-B1/B2, UAT FE-4)', () => {
  it('does not render the Annotate button when canAnnotate is not provided (e.g. the pop-out window)', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    expect(screen.queryByRole('button', { name: /annotate a region/i })).not.toBeInTheDocument()
  })

  it('does not render the Annotate button when canAnnotate is explicitly false', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" canAnnotate={false} />)
    connectAndFrame()
    expect(screen.queryByRole('button', { name: /annotate a region/i })).not.toBeInTheDocument()
  })

  it('renders the Annotate button when canAnnotate is true (e.g. the docked panel)', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" canAnnotate />)
    connectAndFrame()
    expect(screen.getByRole('button', { name: /annotate a region/i })).toBeInTheDocument()
  })
})

// UAT finding FE-6: `controlled_by_other` on BrowserStatusFrame is set true
// by the backend on a viewer whenever a DIFFERENT connection of the same
// browser session holds control (e.g. the docked panel and a pop-out both
// watching the same agent).
describe('BrowserLiveView — controlled_by_other (ADR-038, UAT FE-6)', () => {
  it('disables Take control and shows "someone else is driving" when controlled_by_other is true', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    act(() => {
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'attached', controlled_by_other: true })
    })

    expect(screen.getByRole('button', { name: /someone else is currently driving/i })).toBeDisabled()
    expect(screen.getByTestId('browser-live-status-pill')).toHaveTextContent(/someone else is driving/i)
  })

  it('leaves Take control enabled and shows "agent driving" when controlled_by_other is false/absent', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    act(() => {
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'attached' })
    })

    expect(screen.getByRole('button', { name: /^take control$/i })).not.toBeDisabled()
    expect(screen.getByTestId('browser-live-status-pill')).toHaveTextContent(/agent driving/i)
  })
})

// UAT finding FE-7: raw Go error strings (SSRF-blocked navigate, url.Parse
// failures) were shown to users verbatim. Known cases are mapped to plain
// language; anything unrecognized passes through unchanged.
describe('BrowserLiveView — friendly error messages (UAT FE-7)', () => {
  it('maps an SSRF-blocked navigate error to plain language', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    act(() => {
      callbacksRef.current?.onStatus?.({
        type: 'browser_status',
        state: 'error',
        message:
          'browser input failed: browser live: navigate blocked: SSRF: blocked cloud metadata endpoint 169.254.169.254',
      })
    })

    expect(screen.getByRole('alert')).toHaveTextContent('That address is blocked for security reasons.')
  })

  it('maps a URL-parse failure to plain language', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    act(() => {
      callbacksRef.current?.onStatus?.({
        type: 'browser_status',
        state: 'error',
        message: 'parse "http://exa mple.com": invalid character " " in host name',
      })
    })

    expect(screen.getByRole('alert')).toHaveTextContent("That doesn't look like a valid web address.")
  })

  it('passes an unrecognized error message through unchanged', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    act(() => {
      callbacksRef.current?.onStatus?.({
        type: 'browser_status',
        state: 'error',
        message: 'no browser manager for agent "a1" (browser tools may not be registered for this agent)',
      })
    })

    expect(screen.getByRole('alert')).toHaveTextContent(
      'no browser manager for agent "a1" (browser tools may not be registered for this agent)',
    )
  })
})

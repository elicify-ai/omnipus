// BrowserLiveView.annotateAndBar.test.tsx — ADR-039/ADR-040 D5 (always-visible
// omnibox) and D-B1/B2 (annotate mode ⟷ driving mutual exclusion) behaviour.
// Mocks BrowserLiveWsConnection entirely, same pattern as
// BrowserLiveView.controlToggle.test.tsx. The old "Hand to agent" button
// (ADR-039 D-A3) is removed entirely by ADR-040 D1/D2 — handing back to the
// agent is now just sending a chat message — so that coverage is dropped.
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
          sendViewport: vi.fn(() => true),
          isConnected: true,
        }
      },
    ),
  }
})

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
  useUiStore.setState({ toasts: [] })
})

describe('BrowserLiveView — omnibox (ADR-039 D-A2, ADR-040 D5 — always visible)', () => {
  it('renders the address bar even while not controlling (D5: always visible)', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    expect(screen.getByRole('textbox', { name: /address bar/i })).toBeInTheDocument()
  })

  it('renders the address bar once the viewer takes control too', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    takeControl()
    expect(screen.getByRole('textbox', { name: /address bar/i })).toBeInTheDocument()
  })

  it('normalizes a bare hostname and sends a navigate input frame on submit', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    takeControl()

    const input = screen.getByRole('textbox', { name: /address bar/i })
    fireEvent.change(input, { target: { value: 'example.com' } })
    fireEvent.submit(screen.getByRole('textbox', { name: /address bar/i }).closest('form')!)

    expect(mockSendInput).toHaveBeenCalledWith({ kind: 'navigate', url: 'https://example.com' })
  })

  it('does not send when the address bar is empty (no Go button — Enter submits)', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    takeControl()

    // No Go button anymore; submitting an empty bar is a no-op (resolveOmniboxInput returns falsy).
    fireEvent.submit(screen.getByRole('textbox', { name: /address bar/i }).closest('form')!)
    expect(mockSendInput).not.toHaveBeenCalledWith(expect.objectContaining({ kind: 'navigate' }))
  })

  it('leaves an explicit https:// URL untouched', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    takeControl()

    const input = screen.getByRole('textbox', { name: /address bar/i })
    fireEvent.change(input, { target: { value: 'https://example.com/path' } })
    fireEvent.submit(screen.getByRole('textbox', { name: /address bar/i }).closest('form')!)

    expect(mockSendInput).toHaveBeenCalledWith({ kind: 'navigate', url: 'https://example.com/path' })
  })

  // ADR-040 D5: multi-word / no-dot input routes to a Google search instead
  // of a raw navigate — resolveOmniboxInput's own decision logic is unit
  // tested directly in browserLiveUrl.test.ts; this proves the component
  // actually wires it in (not `normalizeNavigateUrl`).
  it('routes a search-like phrase to a Google search navigate', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    takeControl()

    const input = screen.getByRole('textbox', { name: /address bar/i })
    fireEvent.change(input, { target: { value: 'cheap flights to tokyo' } })
    fireEvent.submit(screen.getByRole('textbox', { name: /address bar/i }).closest('form')!)

    expect(mockSendInput).toHaveBeenCalledWith({
      kind: 'navigate',
      url: 'https://www.google.com/search?q=cheap%20flights%20to%20tokyo',
    })
  })

  // F6 coverage gap: a prior blocked-navigate error banner used to persist
  // through a subsequent SUCCESSFUL navigate (a successful navigate emits
  // only screencast frames, never a browser_status, so nothing cleared it) —
  // handleOmniboxSubmit clears it optimistically on every new submit.
  it('clears a stale error banner when a new navigate is submitted', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    takeControl()

    act(() => {
      callbacksRef.current?.onStatus?.({
        type: 'browser_status',
        state: 'error',
        message: 'That address is blocked for security reasons.',
      })
    })
    expect(screen.getByRole('alert')).toHaveTextContent('That address is blocked for security reasons.')

    const input = screen.getByRole('textbox', { name: /address bar/i })
    fireEvent.change(input, { target: { value: 'example.com' } })
    fireEvent.submit(screen.getByRole('textbox', { name: /address bar/i }).closest('form')!)

    expect(mockSendInput).toHaveBeenCalledWith({ kind: 'navigate', url: 'https://example.com' })
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  // ADR-040 D5 "must-handle": submitting while NOT currently driving takes
  // the wheel first (sendControl('take')) before dispatching the navigate.
  it('takes the wheel before navigating when submitted while not driving', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()

    const input = screen.getByRole('textbox', { name: /address bar/i })
    fireEvent.change(input, { target: { value: 'example.com' } })
    fireEvent.submit(screen.getByRole('textbox', { name: /address bar/i }).closest('form')!)

    expect(mockSendControl).toHaveBeenCalledWith('take')
    expect(mockSendInput).toHaveBeenCalledWith({ kind: 'navigate', url: 'https://example.com' })
  })

  it('does not send control:take again when submitted while already driving', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    takeControl()
    mockSendControl.mockClear()

    const input = screen.getByRole('textbox', { name: /address bar/i })
    fireEvent.change(input, { target: { value: 'example.com' } })
    fireEvent.submit(screen.getByRole('textbox', { name: /address bar/i }).closest('form')!)

    expect(mockSendControl).not.toHaveBeenCalled()
    expect(mockSendInput).toHaveBeenCalledWith({ kind: 'navigate', url: 'https://example.com' })
  })

  // CRITICAL reviewer finding: the omnibox is now gated through the SAME
  // driveMode as the pointer/keyboard handlers, not a hand-rolled duplicate
  // guard — this proves controlled_by_other blocks the submit even when
  // dispatched directly (bypassing the Go button's OWN `disabled` attribute,
  // which already covers the click path — this is defense-in-depth for the
  // handler itself, exercised via a direct form submit).
  it('does not take the wheel nor navigate when controlled_by_other is true (direct submit, bypassing the disabled Go button)', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    act(() => {
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'attached', controlled_by_other: true })
    })

    const input = screen.getByRole('textbox', { name: /address bar/i })
    fireEvent.change(input, { target: { value: 'example.com' } })
    const form = input.closest('form')
    expect(form).not.toBeNull()
    fireEvent.submit(form!)

    expect(mockSendControl).not.toHaveBeenCalled()
    expect(mockSendInput).not.toHaveBeenCalledWith(expect.objectContaining({ kind: 'navigate' }))
  })

  // Reviewer finding: the previous hand-rolled omnibox guard never accounted
  // for annotate mode at all — closing that gap is a direct consequence of
  // routing the omnibox through the same driveMode as everything else
  // ("annotate excludes driving" applies here too now).
  it('does not take the wheel nor navigate while annotate mode is active', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" canAnnotate />)
    connectAndFrame()
    fireEvent.click(screen.getByRole('button', { name: /annotate a region/i }))

    const input = screen.getByRole('textbox', { name: /address bar/i })
    fireEvent.change(input, { target: { value: 'example.com' } })
    const form = input.closest('form')
    expect(form).not.toBeNull()
    fireEvent.submit(form!)

    expect(mockSendControl).not.toHaveBeenCalled()
    expect(mockSendInput).not.toHaveBeenCalledWith(expect.objectContaining({ kind: 'navigate' }))
  })
})

describe('BrowserLiveView — Annotate mode ⟷ driving mutual exclusion (ADR-039 D-B1/B2, ADR-040 D2/D3)', () => {
  // ADR-040 D2: there's no more explicit "Take control" button to disable —
  // mutual exclusion is now enforced by the pointer handlers branching on
  // `annotateMode` BEFORE ever consulting the driving state, so a click on
  // the frame while annotating must never implicitly take the wheel.
  it('a click on the frame does not implicitly take the wheel while annotate mode is active', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" canAnnotate />)
    connectAndFrame()

    fireEvent.click(screen.getByRole('button', { name: /annotate a region/i }))
    mockSendControl.mockClear()
    const container = screen.getByTestId('browser-live-frame')
    fireEvent.pointerDown(container, { clientX: 10, clientY: 10 })
    expect(mockSendControl).not.toHaveBeenCalledWith('take')
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
    expect(screen.getByTestId('browser-live-frame')).toHaveStyle({ cursor: 'crosshair' })
  })

  it('exiting annotate mode re-allows a pointer click to implicitly take the wheel', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" canAnnotate />)
    connectAndFrame()

    fireEvent.click(screen.getByRole('button', { name: /annotate a region/i }))
    fireEvent.click(screen.getByRole('button', { name: /exit annotate mode/i }))

    mockSendControl.mockClear()
    const container = screen.getByTestId('browser-live-frame')
    vi.spyOn(container, 'getBoundingClientRect').mockReturnValue({
      left: 0, top: 0, width: 1280, height: 720, right: 1280, bottom: 720, x: 0, y: 0,
      toJSON() { return {} },
    } as DOMRect)
    fireEvent.pointerDown(container, { clientX: 20, clientY: 20 })
    expect(mockSendControl).toHaveBeenCalledWith('take')
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
  // flip the header chip to the driving-derived "Click to drive" label even
  // though the user is actively mid-annotation. `visualState` gives
  // annotating display priority over every other derived state.
  it('shows a "You\'re annotating" status chip instead of the driving-derived label', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" canAnnotate />)
    connectAndFrame()

    fireEvent.click(screen.getByRole('button', { name: /annotate a region/i }))

    expect(screen.getByTestId('browser-live-status-chip')).toHaveTextContent(/you're annotating/i)
    expect(screen.getByTestId('browser-live-status-chip')).not.toHaveTextContent(/click to drive/i)
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
describe('BrowserLiveView — controlled_by_other (ADR-038, UAT FE-6, carried into ADR-040 D2)', () => {
  // Rewritten 2026-08-03. This previously asserted that controlled_by_other
  // BLOCKED the local user's click ("blocks click-to-drive"). That was the
  // operator-reported dead-input bug encoded as expected behaviour: any second
  // viewer — another panel, a pop-out, a stale automation session — silently
  // disabled the real human's mouse and keyboard. Control is shared now: the
  // chip is informational only, and the click must still act.
  it('still lets the local user act when another viewer is also present', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    act(() => {
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'attached', controlled_by_other: true })
    })

    // Informational, not a lock-out.
    expect(screen.getByTestId('browser-live-status-chip')).toHaveTextContent(/also viewing/i)

    mockSendControl.mockClear()
    mockSendInput.mockClear()
    const container = screen.getByTestId('browser-live-frame')
    vi.spyOn(container, 'getBoundingClientRect').mockReturnValue({
      left: 0, top: 0, width: 1280, height: 720, right: 1280, bottom: 720, x: 0, y: 0,
      toJSON() { return {} },
    } as DOMRect)
    fireEvent.pointerDown(container, { clientX: 20, clientY: 20 })
    expect(mockSendControl).toHaveBeenCalled()
  })

  it('shows "Click to drive" and allows click-to-drive when controlled_by_other is false/absent', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    act(() => {
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'attached' })
    })

    expect(screen.getByTestId('browser-live-status-chip')).toHaveTextContent(/click to drive/i)

    const container = screen.getByTestId('browser-live-frame')
    vi.spyOn(container, 'getBoundingClientRect').mockReturnValue({
      left: 0, top: 0, width: 1280, height: 720, right: 1280, bottom: 720, x: 0, y: 0,
      toJSON() { return {} },
    } as DOMRect)
    fireEvent.pointerDown(container, { clientX: 20, clientY: 20 })
    expect(mockSendControl).toHaveBeenCalledWith('take')
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

  // Reviewer finding F2: the old catch-all `/navigate blocked|SSRF/i` also
  // matched the ordinary "domain doesn't resolve / typo / site down" path —
  // pkg/security/ssrf.go's resolver returns e.g. "SSRF: DNS resolution
  // failed for <host>" for that path too (it's prefixed "SSRF:" because the
  // resolver lives in the SSRF-guarded dial path, not because the address
  // was actually policy-blocked) — so a mistyped/unreachable URL was wrongly
  // told it was "blocked for security reasons".
  it.each([
    ['browser input failed: browser live: navigate blocked: SSRF: DNS resolution failed for nosuchhost.example: lookup nosuchhost.example: no such host'],
    ['browser input failed: browser live: navigate blocked: SSRF: no addresses found for nosuchhost.example'],
    ['browser input failed: browser live: navigate blocked: SSRF: too many redirects'],
    ['browser input failed: browser live: navigate blocked: SSRF: cannot extract host from URL "not-a-url"'],
  ])('maps an unreachable/DNS-failure navigate error to plain "couldn\'t be reached" language: %s', (message) => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    act(() => {
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'error', message })
    })

    expect(screen.getByRole('alert')).toHaveTextContent("That address couldn't be reached — check the URL and try again.")
  })

  // Reviewer finding F2 — the "order matters" regression the old tests
  // didn't actually exercise: a message containing BOTH "navigate blocked"
  // AND the invalid-character url.Parse signature (the real backend shape
  // for a malformed host) must map to the invalid-URL message, not the
  // security-block one — the invalid-URL branch must run FIRST.
  it('maps a message containing both "navigate blocked" and an invalid-character host to the invalid-URL message (order matters)', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    act(() => {
      callbacksRef.current?.onStatus?.({
        type: 'browser_status',
        state: 'error',
        message: 'browser input failed: browser live: navigate blocked: parse "http://exa mple.com": invalid character " " in host name',
      })
    })

    expect(screen.getByRole('alert')).toHaveTextContent("That doesn't look like a valid web address.")
  })

  it.each([
    ['browser input failed: browser live: navigate blocked: SSRF: blocked cloud metadata endpoint 169.254.169.254'],
    ['browser input failed: browser live: navigate blocked: SSRF: blocked private IP range 10.0.0.5 (10.0.0.0/8)'],
    ['browser input failed: browser live: navigate blocked: SSRF: blocked private IPv6 range fd00:: (fd00::/8)'],
  ])('maps an actual SSRF policy block to the "blocked for security" message: %s', (message) => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    act(() => {
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'error', message })
    })

    expect(screen.getByRole('alert')).toHaveTextContent('That address is blocked for security reasons.')
  })
})

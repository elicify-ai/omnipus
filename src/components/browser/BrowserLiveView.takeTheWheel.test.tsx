// BrowserLiveView.takeTheWheel.test.tsx — ADR-040 D2 (implicit control model)
// and D6 (visual "who's driving" indicator) coverage: click-to-drive, the
// watch-only state while the agent is streaming, the "Take over" button
// (cancel + take), and the driving-state chip/glow-border reflecting the
// chat store's per-session isStreaming. Mocks BrowserLiveWsConnection the
// same way the sibling BrowserLiveView test files do; drives the REAL
// useChatStore (not mocked) via setState/spyOn, matching how
// BrowserLiveView.annotateAndBar.test.tsx already drives the real useUiStore.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { act } from 'react'
import type { BrowserLiveWsCallbacks } from '@/lib/browserLiveWs'
import { useChatStore, type SessionChatState } from '@/store/chat'
import { useUiStore } from '@/store/ui'

const { mockSendControl, mockSendInput, callbacksRef } = vi.hoisted(() => ({
  // Returns `true` by default — mirrors the real BrowserLiveWsConnection's
  // "sent on an OPEN socket" success case. The auto-release effect (and any
  // future caller) reacts to a falsy return as a failed send; tests that
  // want to exercise that failure path override it per-call via
  // `mockSendControl.mockReturnValueOnce(false)`.
  mockSendControl: vi.fn(() => true),
  mockSendInput: vi.fn(),
  callbacksRef: { current: null as BrowserLiveWsCallbacks | null },
}))

vi.mock('@/lib/browserLiveWs', () => ({
  BrowserLiveWsConnection: vi.fn().mockImplementation(
    function (_sessionId: string, _agentId: string, callbacks: BrowserLiveWsCallbacks) {
      callbacksRef.current = callbacks
      return {
        connect: vi.fn(),
        detach: vi.fn(),
        close: vi.fn(),
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

/** Stubs the frame container's layout rect to a clean 1:1 box so
 * mapClientToDevice never short-circuits to null (jsdom reports all-zero
 * rects by default) — same technique used across the BrowserLiveView suite. */
function stubFrameRect() {
  const container = screen.getByTestId('browser-live-frame')
  vi.spyOn(container, 'getBoundingClientRect').mockReturnValue({
    left: 0, top: 0, width: 1280, height: 720, right: 1280, bottom: 720, x: 0, y: 0,
    toJSON() { return {} },
  } as DOMRect)
  return container
}

/** Marks session `s1` as mid-turn (agent working) in the REAL chat store —
 * this is the exact signal BrowserLiveView reads (`sessionsById[sessionId].isStreaming`).
 * Wrapped in `act()` since this mutates a store BrowserLiveView is already
 * subscribed to post-render — without it, React Testing Library's assertions
 * can run before the resulting re-render flushes. */
function setAgentWorking(sessionId: string, isStreaming: boolean) {
  act(() => {
    useChatStore.setState((state) => {
      const existing = state.sessionsById[sessionId]
      const bucket: SessionChatState = existing
        ? { ...existing, isStreaming }
        : {
            messagesById: {},
            messageOrder: [],
            trimmedCount: 0,
            toolCalls: {},
            toolCallOrder: [],
            textAtToolCallStart: {},
            isStreaming,
            isReplaying: false,
            replayCompletedForSession: null,
            sessionTokens: 0,
            sessionCost: 0,
            rateLimitEvent: null,
            lastUserMessageAt: null,
            cancelStage: null,
            lastReceivedEventTime: null,
            spanByParentCallId: {},
          }
      return { sessionsById: { ...state.sessionsById, [sessionId]: bucket } }
    })
  })
}

const initialChatState = useChatStore.getState()

beforeEach(() => {
  vi.clearAllMocks()
  callbacksRef.current = null
  // Reset the real chat store's per-session buckets between tests so a
  // prior test's isStreaming:true doesn't leak into the next one.
  useChatStore.setState({ sessionsById: {} }, false)
  // Reset toasts between tests (the auto-release-resilience tests below
  // assert on useUiStore.getState().toasts).
  useUiStore.setState({ toasts: [] })
})

afterEach(() => {
  vi.restoreAllMocks()
  useChatStore.setState(initialChatState, true)
})

describe('BrowserLiveView — click-to-drive (ADR-040 D2, agent idle)', () => {
  it('acquires the lock then dispatches the same pointerdown as input', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    const container = stubFrameRect()

    fireEvent.pointerDown(container, { clientX: 20, clientY: 20 })

    expect(mockSendControl).toHaveBeenCalledWith('take')
    expect(mockSendInput).toHaveBeenCalledWith(expect.objectContaining({ kind: 'mouse_down', x: 20, y: 20 }))
    // control:take must have been sent before the input dispatch (same
    // connection ordering is what makes this safe without waiting for ack).
    const takeOrder = mockSendControl.mock.invocationCallOrder[0]
    const inputOrder = mockSendInput.mock.invocationCallOrder[0]
    expect(takeOrder).toBeLessThan(inputOrder)
  })

  it('does not send a second control:take for pointermove/pointerup in the same gesture', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    const container = stubFrameRect()

    fireEvent.pointerDown(container, { clientX: 20, clientY: 20 })
    mockSendControl.mockClear()
    fireEvent.pointerUp(container, { clientX: 20, clientY: 20 })

    expect(mockSendControl).not.toHaveBeenCalled()
    expect(mockSendInput).toHaveBeenCalledWith(expect.objectContaining({ kind: 'mouse_up' }))
  })

  it('does not double-fire control:take on a rapid second pointerdown before the ack lands', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    const container = stubFrameRect()

    fireEvent.pointerDown(container, { clientX: 20, clientY: 20 })
    fireEvent.pointerUp(container, { clientX: 20, clientY: 20 })
    mockSendControl.mockClear()
    mockSendInput.mockClear()
    // No browser_status('controlling') ack has arrived yet — the mock ws
    // never emits one on its own — so this is exactly the in-flight window
    // pendingTakeRef guards against.
    fireEvent.pointerDown(container, { clientX: 25, clientY: 25 })

    expect(mockSendControl).not.toHaveBeenCalled()
    // Reviewer finding (pending-take residual): a SECOND gesture starting
    // while the first take is still unacked must not dispatch ITS input
    // either — we don't yet know whether the pending take will actually
    // land (e.g. it could be rejected by another viewer beating us to it).
    expect(mockSendInput).not.toHaveBeenCalledWith(expect.objectContaining({ kind: 'mouse_down' }))
  })

  it('sends control:take again for a NEW click after the previous take was acknowledged and released', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    const container = stubFrameRect()

    fireEvent.pointerDown(container, { clientX: 20, clientY: 20 })
    act(() => {
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'controlling' })
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'released' })
    })
    mockSendControl.mockClear()

    fireEvent.pointerDown(container, { clientX: 30, clientY: 30 })
    expect(mockSendControl).toHaveBeenCalledWith('take')
  })

  it('shows a "pointer" cursor over the frame while idle (click-to-drive affordance)', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    expect(screen.getByTestId('browser-live-frame')).toHaveStyle({ cursor: 'pointer' })
  })
})

describe('BrowserLiveView — watch-only while the agent is working (ADR-040 D2)', () => {
  it('blocks pointerdown/move/up from dispatching input or taking the wheel', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    setAgentWorking('s1', true)
    const container = stubFrameRect()

    fireEvent.pointerDown(container, { clientX: 20, clientY: 20 })
    fireEvent.pointerMove(container, { clientX: 25, clientY: 25 })
    fireEvent.pointerUp(container, { clientX: 25, clientY: 25 })

    expect(mockSendControl).not.toHaveBeenCalled()
    expect(mockSendInput).not.toHaveBeenCalled()
  })

  it('blocks keyboard input while watch-only', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    setAgentWorking('s1', true)
    const container = screen.getByTestId('browser-live-frame')

    fireEvent.keyDown(container, { key: 'a' })
    fireEvent.keyUp(container, { key: 'a' })

    expect(mockSendInput).not.toHaveBeenCalled()
  })

  // Reviewer finding coverage: the wheel listener now consults the same
  // driveMode gate as pointer/keyboard — this proves it's actually wired,
  // AND that agent-working wins even with a stale "controlling" status still
  // set (the exact ordering bug the driveMode refactor fixed: the cursor
  // ternary used to check isControlling before agentWorking).
  it('blocks wheel input while watch-only, even with a stale "controlling" status mid-release', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    const container = stubFrameRect()
    act(() => {
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'controlling' })
    })
    setAgentWorking('s1', true)
    mockSendInput.mockClear()

    fireEvent.wheel(container, { deltaX: 0, deltaY: 120 })

    expect(mockSendInput).not.toHaveBeenCalled()
  })

  it('shows the "Take over" button and a not-allowed cursor', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    setAgentWorking('s1', true)

    expect(screen.getByRole('button', { name: /take over/i })).toBeInTheDocument()
    expect(screen.getByTestId('browser-live-frame')).toHaveStyle({ cursor: 'not-allowed' })
  })

  it('releases the lock if the user was already driving when the agent starts working', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    act(() => {
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'controlling' })
    })
    mockSendControl.mockClear()

    setAgentWorking('s1', true)

    expect(mockSendControl).toHaveBeenCalledWith('release')
  })

  it('does NOT show the Take over button, nor block input, for a DIFFERENT session\'s isStreaming', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    // A different session (e.g. the globally-active chat session) streaming
    // must not affect THIS panel's pinned (s1, a1) — see the component's own
    // doc comment on why this reads sessionsById[sessionId] directly.
    setAgentWorking('some-other-session', true)

    expect(screen.queryByRole('button', { name: /take over/i })).not.toBeInTheDocument()
    const container = stubFrameRect()
    fireEvent.pointerDown(container, { clientX: 20, clientY: 20 })
    expect(mockSendControl).toHaveBeenCalledWith('take')
  })
})

describe('BrowserLiveView — "Take over" (ADR-040 D2)', () => {
  // CRITICAL reviewer finding: cancelStream now takes an explicit session id
  // (defaulting to whichever session is ACTIVE in chat when omitted) — an
  // unscoped call would pause the wrong turn whenever this panel's pinned
  // session isn't the globally-active one. Take-over must always pass
  // THIS panel's own pinned sessionId ("s1" here), never rely on the default.
  it('calls the chat store\'s cancelStream WITH this panel\'s pinned sessionId, then acquires the lock', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    setAgentWorking('s1', true)
    const cancelSpy = vi.spyOn(useChatStore.getState(), 'cancelStream').mockImplementation(() => {})

    fireEvent.click(screen.getByRole('button', { name: /take over/i }))

    expect(cancelSpy).toHaveBeenCalledTimes(1)
    expect(cancelSpy).toHaveBeenCalledWith('s1')
    expect(mockSendControl).toHaveBeenCalledWith('take')
    // cancel before take — the agent must be paused before this connection
    // claims the lock.
    const cancelOrder = cancelSpy.mock.invocationCallOrder[0]
    const takeOrder = mockSendControl.mock.invocationCallOrder[0]
    expect(cancelOrder).toBeLessThan(takeOrder)
  })

  // A DIFFERENT panel instance (different sessionId prop) must pass ITS OWN
  // pinned session, not "s1" — proves the argument is wired from the prop,
  // not hardcoded/defaulted.
  it('scopes cancelStream to whichever session THIS instance is pinned to', () => {
    render(<BrowserLiveView sessionId="s2" agentId="a1" />)
    act(() => {
      callbacksRef.current?.onConnected?.()
    })
    setAgentWorking('s2', true)
    const cancelSpy = vi.spyOn(useChatStore.getState(), 'cancelStream').mockImplementation(() => {})

    fireEvent.click(screen.getByRole('button', { name: /take over/i }))

    expect(cancelSpy).toHaveBeenCalledWith('s2')
  })

  it('is disabled while disconnected', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    setAgentWorking('s1', true)
    // Never connected in this test — the button still renders (agent-working
    // is independent of the live-view transport) but must be disabled.
    expect(screen.getByRole('button', { name: /take over/i })).toBeDisabled()
  })

  it('does not render while the agent is idle', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    expect(screen.queryByRole('button', { name: /take over/i })).not.toBeInTheDocument()
  })

  // Reviewer finding: this test's title used to claim the OPPOSITE of what it
  // asserts ("does not render... " while actually asserting
  // `toBeInTheDocument()`). What it really proves: agent-working AND
  // isControlling-still-stale-true is a transient state (the auto-release
  // effect fires immediately, but its ack hasn't landed yet) — driveMode
  // gives `agent-working` top priority over `you-driving`, so Take-over
  // still renders (and input still can't be dispatched) rather than
  // incorrectly showing the driving chip/controls for that one tick.
  it('still shows Take over even if the user already held the lock, transiently, before the auto-release ack lands', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    act(() => {
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'controlling' })
    })
    setAgentWorking('s1', true)
    expect(screen.getByRole('button', { name: /take over/i })).toBeInTheDocument()
  })
})

describe('BrowserLiveView — auto-release resilience (ADR-040 D2, reviewer finding)', () => {
  it('surfaces a toast and clears the local lock if the auto-release send fails while agent-working', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    act(() => {
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'controlling' })
    })
    // Simulate a send that fails despite the socket being "connected" (e.g.
    // ws.send() throwing synchronously) — the auto-release effect's own
    // sendControl('release') call.
    mockSendControl.mockReturnValueOnce(false)

    setAgentWorking('s1', true)

    expect(mockSendControl).toHaveBeenCalledWith('release')
    // Local lock state must not stay stuck at 'controlling' just because the
    // release frame never actually reached the server — driveMode already
    // gives agent-working priority for input-gating, but the CHIP itself
    // must also stop claiming "You're driving".
    expect(screen.getByTestId('browser-live-status-chip')).not.toHaveTextContent("You're driving")
    expect(screen.getByTestId('browser-live-status-chip')).toHaveTextContent('is browsing')
    expect(useUiStore.getState().toasts.some((t) => /could not confirm pausing/i.test(t.message))).toBe(true)
  })

  it('does not surface a failure toast when the release send succeeds', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    act(() => {
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'controlling' })
    })

    setAgentWorking('s1', true)

    expect(mockSendControl).toHaveBeenCalledWith('release')
    expect(useUiStore.getState().toasts.some((t) => /could not confirm pausing/i.test(t.message))).toBe(false)
  })
})

describe('BrowserLiveView — annotate mid-gesture resets take/implicit refs (ADR-040 D2/D3)', () => {
  // Reviewer finding: entering annotate mode WHILE a click-to-drive gesture's
  // take is still in flight (unacked) must fully abandon it — a take ack
  // that lands LATE, after annotate mode is already active, must not resume
  // driving out from under it.
  it('a delayed take-ack after switching to annotate mode does not resume driving', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" canAnnotate />)
    connectAndFrame()
    const container = stubFrameRect()

    // Gesture starts, implicitly takes the wheel — ack not landed yet.
    fireEvent.pointerDown(container, { clientX: 20, clientY: 20 })
    expect(mockSendControl).toHaveBeenCalledWith('take')

    // Mid-gesture (before the ack, before pointerup), the user switches to
    // annotate mode.
    fireEvent.click(screen.getByRole('button', { name: /annotate a region/i }))
    mockSendInput.mockClear()

    // The take ack arrives late, AFTER annotate mode is already active.
    act(() => {
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'controlling' })
    })

    // Cursor and glow must still reflect annotating, not driving.
    expect(container).toHaveStyle({ cursor: 'crosshair' })
    expect(screen.getByTestId('browser-live-glow')).toHaveAttribute('data-visual-state', 'annotating')

    // A subsequent pointer move must never dispatch remote drive input (it
    // draws a local annotate selection box instead, gated on
    // annotateDraggingRef which a stale gesture never set).
    fireEvent.pointerMove(container, { clientX: 40, clientY: 40 })
    expect(mockSendInput).not.toHaveBeenCalled()
  })
})

describe('BrowserLiveView — D6 driving-state chip + glow border', () => {
  it('shows "{agent} is browsing…" using the resolved agent display name when the agent is working', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    setAgentWorking('s1', true)
    // No agents query cache populated — falls back to the generic 'Agent' name.
    expect(screen.getByTestId('browser-live-status-chip')).toHaveTextContent('Agent is browsing…')
  })

  it('reflects agent-working in the glow border data-visual-state', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    setAgentWorking('s1', true)
    expect(screen.getByTestId('browser-live-glow')).toHaveAttribute('data-visual-state', 'agent-working')
  })

  it('reflects you-driving in the glow border data-visual-state', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    act(() => {
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'controlling' })
    })
    expect(screen.getByTestId('browser-live-glow')).toHaveAttribute('data-visual-state', 'you-driving')
  })

  it('reflects idle in the glow border data-visual-state by default', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    expect(screen.getByTestId('browser-live-glow')).toHaveAttribute('data-visual-state', 'idle')
  })

  it('is aria-hidden (decorative) and never intercepts pointer events', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    const glow = screen.getByTestId('browser-live-glow')
    expect(glow).toHaveAttribute('aria-hidden', 'true')
    expect(glow.className).toContain('pointer-events-none')
  })

  // `motion-safe:` is the sole reduced-motion mechanism (GLOW_BORDER_CLASSES'
  // doc comment) — assert the class is actually present for the pulsing
  // states and absent for the non-pulsing ones, rather than just checking
  // `data-visual-state` (which says nothing about the pulse itself).
  it('applies motion-safe:animate-pulse to the glow border while agent-working', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    setAgentWorking('s1', true)
    expect(screen.getByTestId('browser-live-glow').className).toContain('motion-safe:animate-pulse')
  })

  it('applies motion-safe:animate-pulse to the glow border while you-driving', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    act(() => {
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'controlling' })
    })
    expect(screen.getByTestId('browser-live-glow').className).toContain('motion-safe:animate-pulse')
  })

  it('does NOT apply motion-safe:animate-pulse to the glow border while idle', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    expect(screen.getByTestId('browser-live-glow').className).not.toContain('motion-safe:animate-pulse')
  })

  it('applies motion-safe:animate-pulse to the header chip\'s "live" dot while you-driving', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    act(() => {
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'controlling' })
    })
    const chip = screen.getByTestId('browser-live-status-chip')
    const dot = chip.querySelector('[aria-hidden="true"]')
    expect(dot).not.toBeNull()
    expect(dot?.className).toContain('motion-safe:animate-pulse')
  })

  it('does NOT apply motion-safe:animate-pulse to the header chip\'s dot while idle', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    const chip = screen.getByTestId('browser-live-status-chip')
    const dot = chip.querySelector('[aria-hidden="true"]')
    expect(dot).not.toBeNull()
    expect(dot?.className).not.toContain('motion-safe:animate-pulse')
  })
})

describe('BrowserLiveView — A8 optimistic driving chip (UAT polish)', () => {
  // The bug: cancelStream (called first, inside takeWheelIfNeeded) often
  // flips `agentWorking` to false well before the server's 'controlling'
  // ack for the take lands — computeDriveMode used to have nothing to fall
  // back on for that gap and dropped straight to 'idle' ("Click to drive"),
  // even though the user just explicitly took the wheel.
  it('shows the driving-state chip immediately after Take-over is clicked, once the agent stops working, before the take ack lands', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    setAgentWorking('s1', true)
    vi.spyOn(useChatStore.getState(), 'cancelStream').mockImplementation(() => {})

    fireEvent.click(screen.getByRole('button', { name: /take over/i }))
    // No browser_status('controlling') ack has arrived yet — simulate only
    // the real-world side effect of cancelStream succeeding fast.
    setAgentWorking('s1', false)

    const chip = screen.getByTestId('browser-live-status-chip')
    expect(chip).toHaveTextContent("You're driving")
    expect(chip).not.toHaveTextContent('Click to drive')
  })

  it('also shows the driving-state chip immediately for click-to-drive (idle + first pointerdown), before the take ack lands', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    const container = stubFrameRect()

    fireEvent.pointerDown(container, { clientX: 20, clientY: 20 })

    expect(screen.getByTestId('browser-live-status-chip')).toHaveTextContent("You're driving")
  })

  it('falls back off the optimistic driving chip if the take is rejected/abandoned (any status frame clears it, isControlling never lands)', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    setAgentWorking('s1', true)
    vi.spyOn(useChatStore.getState(), 'cancelStream').mockImplementation(() => {})

    fireEvent.click(screen.getByRole('button', { name: /take over/i }))
    setAgentWorking('s1', false)
    expect(screen.getByTestId('browser-live-status-chip')).toHaveTextContent("You're driving")

    // The server rejects/abandons the take (e.g. another viewer grabbed the
    // lock first) — any status frame clears the in-flight guard per the
    // onStatus handler; isControlling never becomes true.
    act(() => {
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'idle' })
    })

    const chip = screen.getByTestId('browser-live-status-chip')
    expect(chip).not.toHaveTextContent("You're driving")
    expect(chip).toHaveTextContent('Click to drive')
  })
})

describe('BrowserLiveView — hand-back discoverability hint (UAT polish)', () => {
  it('shows a hand-back hint using the resolved agent name only while you-driving', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    expect(screen.queryByTestId('browser-live-handback-hint')).not.toBeInTheDocument()

    act(() => {
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'controlling' })
    })

    // No agents query cache populated in this suite — falls back to "the agent".
    expect(screen.getByTestId('browser-live-handback-hint')).toHaveTextContent(
      'Send a message to hand back to the agent',
    )
  })

  it('does not render the hand-back hint while the agent is working', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    setAgentWorking('s1', true)

    expect(screen.queryByTestId('browser-live-handback-hint')).not.toBeInTheDocument()
  })

  it('hides the hand-back hint again once control is released', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" />)
    connectAndFrame()
    act(() => {
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'controlling' })
    })
    expect(screen.getByTestId('browser-live-handback-hint')).toBeInTheDocument()

    act(() => {
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'released' })
    })

    expect(screen.queryByTestId('browser-live-handback-hint')).not.toBeInTheDocument()
  })
})

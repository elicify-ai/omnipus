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

const { mockSendControl, mockSendInput, mockSendTabAction, mockSendViewport, callbacksRef } = vi.hoisted(() => ({
  // Returns `true` by default — mirrors the real BrowserLiveWsConnection's
  // "sent on an OPEN socket" success case. The auto-release effect (and any
  // future caller) reacts to a falsy return as a failed send; tests that
  // want to exercise that failure path override it per-call via
  // `mockSendControl.mockReturnValueOnce(false)`.
  mockSendControl: vi.fn(() => true),
  mockSendInput: vi.fn(),
  mockSendTabAction: vi.fn(() => true),
  // Hoisted so the layout-stability suite can assert that a drive-state
  // change pushes NO viewport (the resize-flap regression).
  mockSendViewport: vi.fn(() => true),
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
          connect: vi.fn(),
          detach: vi.fn(),
          close: vi.fn(),
          sendInput: mockSendInput,
          sendControl: mockSendControl,
          sendTabAction: mockSendTabAction,
          // Adaptive viewport (2026-07-31): BrowserLiveView's ResizeObserver
          // calls this on mount, so every connection double needs it.
          sendViewport: mockSendViewport,
          isConnected: true,
        }
      },
    ),
  }
})

import { BrowserLiveView } from './BrowserLiveView'

/** Stand-in MediaStream — jsdom has no real WebRTC/MediaStream. Every render
 * call below supplies it via the `mediaStream` test/override seam (see
 * BrowserLiveView.webrtcSink.test.tsx) — the JPEG screencast sink is gone
 * (ADR-047), WebRTC video is the only path. */
function fakeMediaStream(id = 'stream-1'): MediaStream {
  return { id } as unknown as MediaStream
}

/** Connects the WS and simulates the <video> sink decoding its first real
 * frame — the direct replacement for the old JPEG-era `onScreencast`
 * emission. Requires the component to be rendered with `mediaStream` so the
 * <video> element exists to fire `loadedmetadata` on. */
function connectAndFrame() {
  act(() => {
    callbacksRef.current?.onConnected?.()
  })
  const video = screen.getByTestId('browser-live-video') as HTMLVideoElement
  act(() => {
    Object.defineProperty(video, 'videoWidth', { value: 1280, configurable: true })
    Object.defineProperty(video, 'videoHeight', { value: 720, configurable: true })
    fireEvent.loadedMetadata(video)
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
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
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
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    const container = stubFrameRect()

    fireEvent.pointerDown(container, { clientX: 20, clientY: 20 })
    mockSendControl.mockClear()
    fireEvent.pointerUp(container, { clientX: 20, clientY: 20 })

    expect(mockSendControl).not.toHaveBeenCalled()
    expect(mockSendInput).toHaveBeenCalledWith(expect.objectContaining({ kind: 'mouse_up' }))
  })

  it('does not double-fire control:take on a rapid second pointerdown before the ack lands', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
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
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
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
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    expect(screen.getByTestId('browser-live-frame')).toHaveStyle({ cursor: 'pointer' })
  })
})

describe('BrowserLiveView — watch-only while the agent is working (ADR-040 D2, amended BROWSER-FR-001/FR-054)', () => {
  // A click on the frame while the agent is working takes the wheel in ONE
  // action, exactly like the omnibox/Take-over/tab-chip paths — and (FR-001)
  // does NOT touch the agent's own turn. See BrowserLiveView.handover.test.tsx
  // for the dedicated "does not call cancelStream" assertion.
  it('a frame click while the agent is working acquires the lock and dispatches the SAME pointerdown as input (ONE click)', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    setAgentWorking('s1', true)
    const container = stubFrameRect()

    fireEvent.pointerDown(container, { clientX: 20, clientY: 20 })

    expect(mockSendControl).toHaveBeenCalledWith('take')
    expect(mockSendInput).toHaveBeenCalledWith(expect.objectContaining({ kind: 'mouse_down', x: 20, y: 20 }))
    // control:take must still be sent before the input dispatch, exactly
    // like the idle click-to-drive path.
    const takeOrder = mockSendControl.mock.invocationCallOrder[0]
    const inputOrder = mockSendInput.mock.invocationCallOrder[0]
    expect(takeOrder).toBeLessThan(inputOrder)
  })

  it('continues dispatching pointerup for the SAME gesture that took the wheel from agent-working', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    setAgentWorking('s1', true)
    const container = stubFrameRect()

    fireEvent.pointerDown(container, { clientX: 20, clientY: 20 })
    mockSendControl.mockClear()
    mockSendInput.mockClear()
    fireEvent.pointerUp(container, { clientX: 25, clientY: 25 })

    // No second control:take for the tail of the SAME gesture (mirrors the
    // idle click-to-drive coverage above).
    expect(mockSendControl).not.toHaveBeenCalled()
    expect(mockSendInput).toHaveBeenCalledWith(expect.objectContaining({ kind: 'mouse_up' }))
  })

  it('a plain keyboard press (no prior click/take) is still blocked while watch-only — keyboard alone never implicitly acquires the wheel', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    setAgentWorking('s1', true)
    const container = screen.getByTestId('browser-live-frame')

    fireEvent.keyDown(container, { key: 'a' })
    fireEvent.keyUp(container, { key: 'a' })

    expect(mockSendInput).not.toHaveBeenCalled()
  })

  it('shows the "Take over" button and a not-allowed cursor', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    setAgentWorking('s1', true)

    expect(screen.getByRole('button', { name: /take over/i })).toBeInTheDocument()
    expect(screen.getByTestId('browser-live-frame')).toHaveStyle({ cursor: 'not-allowed' })
  })

  // BROWSER-FR-054/FR-057: the two tests this describe block used to carry
  // here — "blocks wheel input while watch-only, even with a stale
  // 'controlling' status" and "releases the lock if the user was already
  // driving when the agent starts working" — asserted the OLD priority
  // (agent-working outranks a held wheel, and the wheel auto-releases the
  // instant a new agent turn starts). BROWSER-FR-054/FR-055 deliberately
  // INVERT both: see the "operator keeps driving..." and "still holds the
  // wheel when a SECOND agent turn starts..." regression tests in
  // BrowserLiveView.handover.test.tsx for the current, correct behaviour.

  it('does NOT show the Take over button, nor block input, for a DIFFERENT session\'s isStreaming', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
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

// Bugfix (MED, external review F5, 2026-08-13): the interactive container
// (and its pointerdown handler) mounts the instant a WebRTC stream ATTACHES,
// well before its first real frame ever decodes — the black "Waiting for the
// first frame…" overlay covering it is pointer-events-none specifically so a
// click still reaches the handler once real pixels ARE showing. Before this
// fix, clicking that still-black box while the agent was mid-turn called
// takeWheelIfNeeded unconditionally, which grabs the control lock for a
// click that could never have landed on the page at all.
describe('BrowserLiveView — waiting-overlay click before the first frame decodes (external review F5)', () => {
  it('does NOT take the lock for a click before the video has decoded a real frame (agent working)', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    // Deliberately NOT calling connectAndFrame()'s loadedmetadata step:
    // `attached` is true (mediaStream set, so the container/overlay mount)
    // but `videoReady` stays false — exactly the "Waiting for the first
    // frame…" window this finding is about.
    act(() => {
      callbacksRef.current?.onConnected?.()
    })
    setAgentWorking('s1', true)
    const container = screen.getByTestId('browser-live-frame')
    expect(screen.getByTestId('browser-live-waiting-overlay')).toBeInTheDocument()

    fireEvent.pointerDown(container, { clientX: 20, clientY: 20 })

    expect(mockSendControl).not.toHaveBeenCalledWith('take')
    expect(mockSendInput).not.toHaveBeenCalled()
  })

  it('does NOT implicitly acquire the lock for a click before the video has decoded a real frame (agent idle)', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    act(() => {
      callbacksRef.current?.onConnected?.()
    })
    const container = screen.getByTestId('browser-live-frame')

    fireEvent.pointerDown(container, { clientX: 20, clientY: 20 })

    expect(mockSendControl).not.toHaveBeenCalled()
    expect(mockSendInput).not.toHaveBeenCalled()
  })

  it('takes the lock and dispatches input in ONE click once the frame HAS decoded (no regression)', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    setAgentWorking('s1', true)
    const container = stubFrameRect()

    fireEvent.pointerDown(container, { clientX: 20, clientY: 20 })

    expect(mockSendControl).toHaveBeenCalledWith('take')
    expect(mockSendInput).toHaveBeenCalledWith(expect.objectContaining({ kind: 'mouse_down', x: 20, y: 20 }))
  })
})

describe('BrowserLiveView — "Take over" (ADR-040 D2, amended BROWSER-FR-001)', () => {
  // BROWSER-FR-001: the two tests this block used to carry here — asserting
  // that clicking "Take over" calls the chat store's cancelStream, scoped to
  // this panel's pinned sessionId — are gone. Taking the wheel must NOT
  // invoke the chat cancel action at all any more; see the paired
  // negative+positive assertion in BrowserLiveView.handover.test.tsx
  // ("does not call cancelStream when taking the wheel, and DOES send one
  // browser_control{take} frame in the same gesture").
  it('acquires the lock on click', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    setAgentWorking('s1', true)

    fireEvent.click(screen.getByRole('button', { name: /take over/i }))

    expect(mockSendControl).toHaveBeenCalledTimes(1)
    expect(mockSendControl).toHaveBeenCalledWith('take')
  })

  it('is disabled while disconnected', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    setAgentWorking('s1', true)
    // Never connected in this test — the button still renders (agent-working
    // is independent of the live-view transport) but must be disabled.
    expect(screen.getByRole('button', { name: /take over/i })).toBeDisabled()
  })

  it('does not render while the agent is idle', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    expect(screen.queryByRole('button', { name: /take over/i })).not.toBeInTheDocument()
  })

  // BROWSER-FR-054: the test this block used to carry here — "still shows
  // Take over even if the user already held the lock, transiently, before
  // the auto-release ack lands" — asserted that a CONFIRMED `isControlling`
  // still lost to `agent-working` for one tick. That priority is inverted
  // now (operator-holds-wheel outranks agent-working, unconditionally) and
  // the auto-release effect it depended on is deleted (FR-055): once
  // `isControlling` is true, Take-over must NOT show. See "keeps
  // you-driving priority over agent-working for the chip and the cursor" in
  // BrowserLiveView.handover.test.tsx for the current, correct behaviour.
})

// BROWSER-FR-001/FR-053/FR-055: the describe block this comment used to
// introduce ("UAT fix: one-click take-over while isStreaming is still
// stale-true") tested the OLD `cancelStream` + `agentPausedByUser` /
// `effectiveAgentWorking` mechanism, including a test that asserted a
// LATER agent turn auto-releases a held wheel. That whole mechanism no
// longer exists: `takeWheelIfNeeded` no longer touches the chat store at
// all (FR-001), so there is no `isStreaming`-catches-up race left to prove
// anything about, and the auto-release effect it partly protected against
// is deleted outright (FR-055). The still-relevant coverage — the
// optimistic "You're driving" chip appearing immediately on click, and one
// click both acquiring the wheel and dispatching from the frame/omnibox/
// tab-strip/Take-over — lives in BrowserLiveView.handover.test.tsx (FR-056)
// and in the pre-existing "A8 optimistic driving chip" suite below.

describe('BrowserLiveView — auto-release resilience (ADR-040 D2, reviewer finding)', () => {
  // BROWSER-FR-055: the two tests this block used to carry here —
  // "surfaces a toast and clears the local lock if the auto-release send
  // fails while agent-working" and "does not surface a failure toast when
  // the release send succeeds" — exercised the auto-release effect itself
  // (`if (effectiveAgentWorking && isControlling) sendControl('release')`),
  // which is deleted in full, not gated. See "does not auto-release the
  // wheel after the take ack while the agent is still working" and "still
  // holds the wheel when a SECOND agent turn starts..." in
  // BrowserLiveView.handover.test.tsx for the current, correct behaviour.

  // Reviewer finding F3: takeWheelIfNeeded used to discard sendControl('take')'s
  // boolean return — a failed send left pendingTakeRef stuck true forever,
  // wedging the "take control" affordance permanently at "you're driving"
  // while real control never transferred, and blocking every later take via
  // the pendingTakeRef.current in-flight guard.
  it('click-to-drive: clears the optimistic pendingTake and toasts if sendControl("take") fails', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    const container = stubFrameRect()
    mockSendControl.mockReturnValueOnce(false)

    fireEvent.pointerDown(container, { clientX: 20, clientY: 20 })

    // The optimistic "you're driving" chip must NOT stay stuck once the
    // send is known to have failed.
    expect(screen.getByTestId('browser-live-status-chip')).not.toHaveTextContent("You're driving")
    expect(useUiStore.getState().toasts.some((t) => /could not confirm taking control/i.test(t.message))).toBe(true)
  })

  it('click-to-drive: a NEW gesture can retry control:take after a failed take was cleared', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    const container = stubFrameRect()
    mockSendControl.mockReturnValueOnce(false)

    fireEvent.pointerDown(container, { clientX: 20, clientY: 20 })
    fireEvent.pointerUp(container, { clientX: 20, clientY: 20 })
    mockSendControl.mockClear()

    // pendingTakeRef must have been cleared by the failed-send recovery —
    // otherwise this second gesture would be silently swallowed forever.
    fireEvent.pointerDown(container, { clientX: 30, clientY: 30 })
    expect(mockSendControl).toHaveBeenCalledWith('take')
  })

  it('"Take over": clears the optimistic pendingTake and toasts if sendControl("take") fails', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    setAgentWorking('s1', true)
    mockSendControl.mockReturnValueOnce(false)

    fireEvent.click(screen.getByRole('button', { name: /take over/i }))

    expect(screen.getByTestId('browser-live-status-chip')).not.toHaveTextContent("You're driving")
    expect(useUiStore.getState().toasts.some((t) => /could not confirm taking control/i.test(t.message))).toBe(true)
  })

  it('does not surface a take-failure toast when the take send succeeds', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    const container = stubFrameRect()

    fireEvent.pointerDown(container, { clientX: 20, clientY: 20 })

    expect(useUiStore.getState().toasts.some((t) => /could not confirm taking control/i.test(t.message))).toBe(false)
  })
})

describe('BrowserLiveView — annotate mid-gesture resets take/implicit refs (ADR-040 D2/D3)', () => {
  // Reviewer finding: entering annotate mode WHILE a click-to-drive gesture's
  // take is still in flight (unacked) must fully abandon it — a take ack
  // that lands LATE, after annotate mode is already active, must not resume
  // driving out from under it.
  it('a delayed take-ack after switching to annotate mode does not resume driving', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} canAnnotate />)
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
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    setAgentWorking('s1', true)
    // No agents query cache populated — falls back to the generic 'Agent' name.
    expect(screen.getByTestId('browser-live-status-chip')).toHaveTextContent('Agent is browsing…')
  })

  it('reflects agent-working in the glow border data-visual-state', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    setAgentWorking('s1', true)
    expect(screen.getByTestId('browser-live-glow')).toHaveAttribute('data-visual-state', 'agent-working')
  })

  it('reflects you-driving in the glow border data-visual-state', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    act(() => {
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'controlling' })
    })
    expect(screen.getByTestId('browser-live-glow')).toHaveAttribute('data-visual-state', 'you-driving')
  })

  it('reflects idle in the glow border data-visual-state by default', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    expect(screen.getByTestId('browser-live-glow')).toHaveAttribute('data-visual-state', 'idle')
  })

  it('is aria-hidden (decorative) and never intercepts pointer events', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    const glow = screen.getByTestId('browser-live-glow')
    expect(glow).toHaveAttribute('aria-hidden', 'true')
    expect(glow.className).toContain('pointer-events-none')
  })

  // `motion-safe:` is the sole reduced-motion mechanism (GLOW_BORDER_CLASSES'
  // doc comment) — assert the class is actually present for the pulsing
  // states and absent for the non-pulsing ones, rather than just checking
  // `data-visual-state` (which says nothing about the pulse itself).
  // ADR-040 D6 (revised): the border is a calm 1px solid line — gold for
  // you-driving, neutral for ambient states. The pulse is reserved for ERROR
  // only (Von Restorff + cry-wolf avoidance: pulsing every non-idle state
  // trains the user to ignore the signal). agent-working/you-driving must NOT
  // pulse.
  it('does NOT pulse the border while agent-working (pulse is error-only)', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    setAgentWorking('s1', true)
    expect(screen.getByTestId('browser-live-glow').className).not.toContain('motion-safe:animate-pulse')
  })

  it('does NOT pulse the border while you-driving (pulse is error-only)', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    act(() => {
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'controlling' })
    })
    expect(screen.getByTestId('browser-live-glow').className).not.toContain('motion-safe:animate-pulse')
  })

  it('the glow overlay is borderless (frame removed) but tracks visual state', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    act(() => {
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'error', message: 'boom' })
    })
    const glow = screen.getByTestId('browser-live-glow')
    expect(glow).toHaveAttribute('data-visual-state', 'error')
    // No border/pulse classes — the visible frame is removed per operator direction.
    expect(glow.className).not.toContain('border')
    expect(glow.className).not.toContain('animate-pulse')
  })

  it('does NOT apply motion-safe:animate-pulse to the glow border while idle', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    expect(screen.getByTestId('browser-live-glow').className).not.toContain('motion-safe:animate-pulse')
  })

  it('applies motion-safe:animate-pulse to the header chip\'s "live" dot while you-driving', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
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
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    const chip = screen.getByTestId('browser-live-status-chip')
    const dot = chip.querySelector('[aria-hidden="true"]')
    expect(dot).not.toBeNull()
    expect(dot?.className).not.toContain('motion-safe:animate-pulse')
  })
})

describe('BrowserLiveView — A8 optimistic driving chip (UAT polish)', () => {
  // computeDriveMode/visualDriveMode check isControlling (folded with
  // pendingTake for the optimistic window — see visualDriveMode's own doc
  // comment) BEFORE agentWorking (BROWSER-FR-054), so the chip shows
  // "You're driving" the instant a take is SENT, before the server's
  // 'controlling' ack round-trips back — regardless of whether the agent is
  // still genuinely working, since FR-001 means taking the wheel never
  // touches the agent's turn at all.
  it('shows the driving-state chip immediately after Take-over is clicked, before the take ack lands, while the agent keeps working', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    setAgentWorking('s1', true)

    fireEvent.click(screen.getByRole('button', { name: /take over/i }))
    // No browser_status('controlling') ack has arrived yet, and the agent is
    // still working (isStreaming stays true throughout — there is no
    // chat-store interaction to wait on any more).

    const chip = screen.getByTestId('browser-live-status-chip')
    expect(chip).toHaveTextContent("You're driving")
    expect(chip).not.toHaveTextContent('Click to drive')
    expect(chip).not.toHaveTextContent('is browsing')
  })

  it('also shows the driving-state chip immediately for click-to-drive (idle + first pointerdown), before the take ack lands', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    const container = stubFrameRect()

    fireEvent.pointerDown(container, { clientX: 20, clientY: 20 })

    expect(screen.getByTestId('browser-live-status-chip')).toHaveTextContent("You're driving")
  })

  it('falls back off the optimistic driving chip if the take is rejected/abandoned, reverting to the agent-working chip while the agent is still working', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    setAgentWorking('s1', true)

    fireEvent.click(screen.getByRole('button', { name: /take over/i }))
    expect(screen.getByTestId('browser-live-status-chip')).toHaveTextContent("You're driving")

    // The server rejects/abandons the take (e.g. another viewer grabbed the
    // lock first) — any status frame clears the in-flight guard per the
    // onStatus handler; isControlling never becomes true. The agent is still
    // genuinely working throughout, so the chip must fall back to
    // "is browsing", not all the way to idle's "Click to drive".
    act(() => {
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'idle' })
    })

    const chip = screen.getByTestId('browser-live-status-chip')
    expect(chip).not.toHaveTextContent("You're driving")
    expect(chip).toHaveTextContent('is browsing')
  })
})

// CRITICAL (WCAG 2.1.2 "No Keyboard Trap") — while you-driving, every key
// except Escape is forwarded to the remote page. Pre-fix, Escape was a local
// no-op (justified by a stale comment about a Radix Sheet capture listener
// that no longer exists — the panel is now always a plain docked <aside>),
// so a keyboard-only user who tabbed into the frame had NO way out at all.
// These tests fail against the pre-fix code (Escape returning early with no
// sendControl('release') call and no focus move) and pass now that Escape
// actively releases the wheel.
describe('BrowserLiveView — Escape releases the wheel (WCAG 2.1.2 No Keyboard Trap, CRITICAL)', () => {
  function driveIt(container: HTMLElement) {
    act(() => {
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'controlling' })
    })
    return container
  }

  it('pressing Escape while you-driving releases control via the same sendControl("release") path other exits use, instead of forwarding Escape to the remote page', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    const container = driveIt(stubFrameRect())
    mockSendInput.mockClear()
    mockSendControl.mockClear()

    fireEvent.keyDown(container, { key: 'Escape' })

    // Escape must NOT be forwarded as remote keyboard input (no key_down/text)...
    expect(mockSendInput).not.toHaveBeenCalled()
    // ...and MUST release the wheel — a genuine exit, not a local no-op.
    expect(mockSendControl).toHaveBeenCalledWith('release')
  })

  it('moves focus to the address bar after Escape releases the wheel', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    const container = driveIt(stubFrameRect())

    fireEvent.keyDown(container, { key: 'Escape' })

    expect(screen.getByLabelText('Address bar')).toHaveFocus()
  })

  it('surfaces a toast and forces the local status back to released if the Escape release send fails', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    const container = driveIt(stubFrameRect())
    mockSendControl.mockReturnValueOnce(false)

    fireEvent.keyDown(container, { key: 'Escape' })

    expect(
      useUiStore.getState().toasts.some((t) => /could not confirm releasing control/i.test(t.message)),
    ).toBe(true)
    expect(screen.getByTestId('browser-live-status-chip')).not.toHaveTextContent("You're driving")
  })

  it('still forwards every OTHER key while you-driving — Escape is the one exception, not a reason to stop forwarding input', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    const container = driveIt(stubFrameRect())
    mockSendInput.mockClear()

    fireEvent.keyDown(container, { key: 'Tab' })

    expect(mockSendInput).toHaveBeenCalledWith(expect.objectContaining({ kind: 'key_down', key: 'Tab' }))
    expect(mockSendControl).not.toHaveBeenCalledWith('release')
  })

  it('Escape keyup does not forward a key_up either', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    const container = driveIt(stubFrameRect())
    mockSendInput.mockClear()

    fireEvent.keyUp(container, { key: 'Escape' })

    expect(mockSendInput).not.toHaveBeenCalled()
  })
})

// The hint is ALWAYS MOUNTED and visibility-toggled (2026-08-03). It used to be
// conditionally rendered, which resized the live frame by its own height on
// every drive-state change (measured 564 <-> 587px) — the SPA pushes the frame
// box as the viewport, so each toggle forced a window resize + full capture
// rebuild and invalidated the cached CSS viewport. These assertions therefore
// check VISIBILITY, not presence: asserting presence again would re-introduce
// the resize loop the moment someone "fixed" the test.
describe('BrowserLiveView — hand-back discoverability hint (UAT polish)', () => {
  it('shows a hand-back hint using the resolved agent name only while you-driving', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    // Present (so the layout never shifts) but hidden while idle.
    const idleHint = screen.getByTestId('browser-live-handback-hint')
    expect(idleHint).toHaveClass('invisible')
    expect(idleHint).toHaveAttribute('aria-hidden', 'true')

    act(() => {
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'controlling' })
    })

    expect(screen.getByTestId('browser-live-handback-hint')).not.toHaveClass('invisible')

    // Header consolidation (2026-08-04): the hint no longer owns a chrome row —
    // it overlays the FRAME (absolutely positioned, pointer-events-none), so it
    // costs zero layout while keeping the whole sentence on screen. No agents
    // query cache is populated in this suite, so it falls back to "the agent".
    expect(screen.getByTestId('browser-live-handback-hint')).toHaveTextContent(
      'Send a message to hand back to the agent',
    )
  })

  // WCAG 2.1.2: the Escape exit must be ADVERTISED somewhere on screen, not
  // just functional — this is the advertisement half of the CRITICAL fix
  // covered above.
  it('advertises the Escape exit alongside the hand-back hint', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()

    act(() => {
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'controlling' })
    })

    // Still advertised ON SCREEN after the header consolidation, not demoted to
    // a tooltip: a keyboard escape only discoverable by hovering does not
    // satisfy "No Keyboard Trap".
    expect(screen.getByTestId('browser-live-handback-hint')).toHaveTextContent(/press Esc to stop driving/i)
  })

  it('does not render the hand-back hint while the agent is working', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    setAgentWorking('s1', true)

    const hint = screen.getByTestId('browser-live-handback-hint')
    expect(hint).toHaveClass('invisible')
    expect(hint).toHaveAttribute('aria-hidden', 'true')
  })

  it('hides the hand-back hint again once control is released', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    act(() => {
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'controlling' })
    })
    expect(screen.getByTestId('browser-live-handback-hint')).not.toHaveClass('invisible')

    act(() => {
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'released' })
    })

    // Hidden again, but STILL MOUNTED — the frame box must not change height.
    const released = screen.getByTestId('browser-live-handback-hint')
    expect(released).toHaveClass('invisible')
    expect(released).toHaveAttribute('aria-hidden', 'true')
  })
})

// Regression coverage for the resize flap (measured live on UAT, 2026-08-03).
//
// The hand-back hint used to be conditionally rendered, so taking or releasing
// the wheel changed the live frame's height by exactly the hint's own height
// (reproduced deterministically: 564 <-> 587px). The SPA pushes the frame box
// as the viewport, so every click-to-drive produced:
//   browser_viewport push -> OS window resize -> full capture rebuild
//        -> cached CSS viewport invalidated -> the next click could take
//           dispatchInput's unmappable path and land off-target.
//
// This is the operator-reported "mouse clicks work only sometimes". The test
// asserts the INVARIANT that prevents it: drive-state changes must never add or
// remove nodes above the frame.
describe('BrowserLiveView — layout stability across drive-state changes', () => {
  it('keeps the hand-back hint mounted so the frame never resizes', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()

    const hintCount = () => screen.queryAllByTestId('browser-live-handback-hint').length
    const idle = hintCount()

    act(() => {
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'controlling' })
    })
    const driving = hintCount()

    act(() => {
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'released' })
    })
    const releasedAgain = hintCount()

    expect(idle).toBe(1)
    expect(driving).toBe(1)
    expect(releasedAgain).toBe(1)
  })

  it('does not push a new viewport when only the drive state changes', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()

    mockSendViewport.mockClear()
    act(() => {
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'controlling' })
    })
    act(() => {
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'released' })
    })

    expect(mockSendViewport).not.toHaveBeenCalled()
  })
})

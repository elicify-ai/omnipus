// BrowserLiveView.mouseMoveThrottle.test.tsx — pointer input coalescing.
//
// Original finding: handlePointerMove sent one browser_input{kind:mouse_move}
// per native pointermove (60-120Hz+, higher on gaming mice/some trackpads),
// exceeding the backend's input rate limiter — silent server-side drops + a
// janky remote cursor. Fix: coalesce to "only the latest position", flushed
// once per pacing interval.
//
// The pacer is a FIXED setTimeout(MOVE_FLUSH_MS), no longer
// requestAnimationFrame with a visibility-based fallback (operator report
// 2026-08-04, "inputs feel laggy/slowish", worst while a video plays: rAF ties
// the network send to the local compositor, so pointer positions queued behind
// video painting). There is now exactly ONE code path, so nothing here needs to
// steer which branch runs — the tests drive it purely with fake timers.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { act } from 'react'
import type { BrowserLiveWsCallbacks } from '@/lib/browserLiveWs'
import { useChatStore, type SessionChatState } from '@/store/chat'

const { mockSendInput, callbacksRef } = vi.hoisted(() => ({
  mockSendInput: vi.fn(),
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
          sendControl: vi.fn(() => true),
          // Adaptive viewport (2026-07-31): BrowserLiveView's ResizeObserver
          // calls this on mount, so this double needs it too.
          sendViewport: vi.fn(() => true),
          isConnected: true,
        }
      },
    ),
  }
})

import { BrowserLiveView } from './BrowserLiveView'

const initialChatState = useChatStore.getState()

beforeEach(() => {
  vi.clearAllMocks()
  callbacksRef.current = null
  vi.useFakeTimers()
  // Reset the real chat store's per-session buckets between tests so a prior
  // test's isStreaming:true doesn't leak into the next one (mirrors
  // BrowserLiveView.takeTheWheel.test.tsx).
  useChatStore.setState({ sessionsById: {} }, false)
})

afterEach(() => {
  vi.clearAllTimers()
  vi.useRealTimers()
  vi.restoreAllMocks()
  useChatStore.setState(initialChatState, true)
})

/** Marks session `s1` as mid-turn (agent working) in the REAL chat store —
 * mirrors BrowserLiveView.takeTheWheel.test.tsx's identical helper. */
function setAgentWorking(sessionId: string, isStreaming: boolean) {
  const bucket: SessionChatState = {
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
  useChatStore.setState((state) => ({ sessionsById: { ...state.sessionsById, [sessionId]: bucket } }))
}

/** Mounts, connects, delivers a screencast frame, takes control, and stubs the
 * frame container's layout rect to a clean 1:1 box so mapClientToDevice never
 * short-circuits to null (jsdom reports all-zero rects by default). */
function mountControllingWithFrame() {
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
  const container = screen.getByTestId('browser-live-frame')
  vi.spyOn(container, 'getBoundingClientRect').mockReturnValue({
    left: 0,
    top: 0,
    width: 1280,
    height: 720,
    right: 1280,
    bottom: 720,
    x: 0,
    y: 0,
    toJSON() {
      return {}
    },
  } as DOMRect)
  return container
}

/** Mounts, connects, and delivers a screencast frame WITHOUT taking control —
 * for exercising the click-to-drive implicit-take path, mirrors
 * mountControllingWithFrame minus the 'controlling' status frame. */
function mountIdleWithFrame() {
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
  })
  const container = screen.getByTestId('browser-live-frame')
  vi.spyOn(container, 'getBoundingClientRect').mockReturnValue({
    left: 0,
    top: 0,
    width: 1280,
    height: 720,
    right: 1280,
    bottom: 720,
    x: 0,
    y: 0,
    toJSON() {
      return {}
    },
  } as DOMRect)
  return container
}

function moveCalls() {
  return mockSendInput.mock.calls.filter(([arg]) => (arg as { kind?: string }).kind === 'mouse_move')
}

describe('BrowserLiveView — mouse_move RAF coalescing', () => {
  it('coalesces a burst of pointermove events into a single sendInput carrying the latest position', () => {
    const container = mountControllingWithFrame()

    act(() => {
      fireEvent.pointerMove(container, { clientX: 10, clientY: 10 })
      fireEvent.pointerMove(container, { clientX: 20, clientY: 20 })
      fireEvent.pointerMove(container, { clientX: 30, clientY: 30 })
    })

    // Nothing sent synchronously — still coalescing, awaiting the flush.
    expect(moveCalls()).toHaveLength(0)

    act(() => {
      vi.runAllTimers()
    })

    expect(moveCalls()).toHaveLength(1)
    expect(moveCalls()[0][0]).toMatchObject({ kind: 'mouse_move', x: 30, y: 30 })
  })

  it('schedules a fresh flush for the next burst after the previous one drains', () => {
    const container = mountControllingWithFrame()

    act(() => {
      fireEvent.pointerMove(container, { clientX: 1, clientY: 1 })
    })
    act(() => {
      vi.runAllTimers()
    })
    expect(moveCalls()).toHaveLength(1)

    act(() => {
      fireEvent.pointerMove(container, { clientX: 2, clientY: 2 })
      fireEvent.pointerMove(container, { clientX: 3, clientY: 3 })
    })
    act(() => {
      vi.runAllTimers()
    })
    expect(moveCalls()).toHaveLength(2)
    expect(moveCalls()[1][0]).toMatchObject({ x: 3, y: 3 })
  })

  it('does not throttle pointer down/up — sent immediately, never coalesced', () => {
    const container = mountControllingWithFrame()

    act(() => {
      fireEvent.pointerDown(container, { clientX: 5, clientY: 5 })
      fireEvent.pointerUp(container, { clientX: 5, clientY: 5 })
    })

    const downCalls = mockSendInput.mock.calls.filter(([arg]) => (arg as { kind?: string }).kind === 'mouse_down')
    const upCalls = mockSendInput.mock.calls.filter(([arg]) => (arg as { kind?: string }).kind === 'mouse_up')
    expect(downCalls).toHaveLength(1)
    expect(upCalls).toHaveLength(1)
  })

  it('does not throttle key events — sent immediately, never coalesced', () => {
    const container = mountControllingWithFrame()

    act(() => {
      fireEvent.keyDown(container, { key: 'a' })
      fireEvent.keyUp(container, { key: 'a' })
    })

    const textCalls = mockSendInput.mock.calls.filter(([arg]) => (arg as { kind?: string }).kind === 'text')
    expect(textCalls).toHaveLength(1)
  })
})

describe('BrowserLiveView — ADR-040 D2 driveMode refactor regression coverage', () => {
  // Reviewer finding coverage: a pointermove mid-gesture must keep dispatching
  // (and flushing) even though the server's 'controlling' ack for the
  // implicit take hasn't landed yet — this is what implicitDriveRef exists
  // for, and canDispatchInput's `implicitDriveActive` parameter must
  // preserve it exactly.
  it('flushes a mouse_move mid-gesture even before the implicit take is acknowledged', () => {
    const container = mountIdleWithFrame()

    act(() => {
      fireEvent.pointerDown(container, { clientX: 20, clientY: 20 })
    })
    // No browser_status('controlling') ack has arrived yet.
    mockSendInput.mockClear()

    act(() => {
      fireEvent.pointerMove(container, { clientX: 40, clientY: 40 })
      vi.runAllTimers()
    })

    expect(moveCalls()).toHaveLength(1)
    expect(moveCalls()[0][0]).toMatchObject({ kind: 'mouse_move', x: 40, y: 40 })
  })

  // Reviewer finding (queued-move leak): flushPendingMove now re-validates
  // the drive gate at FLUSH time, not just at schedule time — a position
  // queued WHILE driving must not leak into the tab if the agent starts
  // working in the gap before the animation-frame/timer flush actually
  // fires.
  it('drops a queued mouse_move if the agent starts working before the flush fires', () => {
    const container = mountControllingWithFrame()

    act(() => {
      fireEvent.pointerMove(container, { clientX: 10, clientY: 10 })
    })
    // Still coalescing — nothing sent synchronously.
    expect(moveCalls()).toHaveLength(0)

    // The agent starts working in the gap before the scheduled flush fires.
    act(() => {
      setAgentWorking('s1', true)
    })

    act(() => {
      vi.runAllTimers()
    })

    // The queued position must NOT have leaked into the tab.
    expect(moveCalls()).toHaveLength(0)
  })
})

// Every test above drains with runAllTimers, which proves coalescing happens
// but says nothing about the INTERVAL. That mattered: the pacing constant is
// what keeps the client under the server's per-second input budget, and a
// regression to 0 (flood) or to something large (visible lag) would have gone
// unnoticed by the whole suite. These pin the actual cadence.
describe('BrowserLiveView — the pacing interval itself', () => {
  it('holds a move until the pacing interval elapses, then sends exactly one', async () => {
    const container = mountControllingWithFrame()
    mockSendInput.mockClear()

    act(() => {
      fireEvent.pointerMove(container, { clientX: 10, clientY: 10 })
    })

    // Just short of the interval: still buffered, nothing on the wire.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(20)
    })
    expect(moveCalls()).toHaveLength(0)

    // Crossing it releases exactly one coalesced send.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(15)
    })
    expect(moveCalls()).toHaveLength(1)
  })

  it('sustains at most ~40 moves/sec, staying inside the server budget', async () => {
    const container = mountControllingWithFrame()
    mockSendInput.mockClear()

    // A 120Hz pointer stream for one second — what a real trackpad produces.
    for (let i = 0; i < 120; i++) {
      await act(async () => {
        fireEvent.pointerMove(container, { clientX: i, clientY: i })
        await vi.advanceTimersByTimeAsync(1000 / 120)
      })
    }

    const moves = moveCalls().length
    expect(moves).toBeLessThanOrEqual(41)
    expect(moves).toBeGreaterThan(30)
  })
})

// Both ends of a gesture must supersede a queued move, not just the press.
// mouse_down already did this; mouse_up did not, which is asymmetric for
// exactly the interactions that read the last move relative to the release.
describe('BrowserLiveView — a queued move never lands after a button transition', () => {
  it('discards a queued move on pointerup so the release is the last word', () => {
    const container = mountControllingWithFrame()
    act(() => {
      fireEvent.pointerDown(container, { clientX: 5, clientY: 5 })
    })
    mockSendInput.mockClear()

    // A drag: the move is still buffered when the button comes up. mouse_up
    // maps its own fresh coordinates, so the buffered position is not merely
    // redundant — it is stale, and arriving after the release it would tell the
    // remote page the pointer moved back to where it no longer is. For a drag,
    // slider, or text selection that is a wrong drop point.
    act(() => {
      fireEvent.pointerMove(container, { clientX: 50, clientY: 50 })
      fireEvent.pointerUp(container, { clientX: 60, clientY: 60 })
    })
    act(() => {
      vi.runAllTimers()
    })

    const kinds = mockSendInput.mock.calls.map(([arg]) => (arg as { kind?: string }).kind)
    expect(kinds).toEqual(['mouse_up'])
  })
})

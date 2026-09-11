// BrowserLiveView.handover.test.tsx — ADR-085 (BROWSER-FR-001, FR-053–FR-059)
// regression coverage for the "take the wheel never ends the agent's turn"
// rebuild. Wave B5 (joint delivery plan §3). Mocks BrowserLiveWsConnection
// the same way the sibling BrowserLiveView test files do; drives the REAL
// useChatStore (not mocked) via setState/spyOn, matching
// BrowserLiveView.takeTheWheel.test.tsx.
//
// What this file is NOT: a re-test of everything takeTheWheel.test.tsx
// already covers (click-to-drive at idle, the chip/glow-border states, the
// Escape/hand-back-hint/layout-stability suites). It carries specifically
// the FOUR regression guards the joint delivery plan's test matrix names for
// this rebuild (FR-053/FR-054 post-gesture continuation, FR-054 priority,
// FR-055 no auto-release, FR-057 no clearing across a turn boundary), the
// FR-001 paired negative+positive assertion, the FR-031b unsolicited-release
// handling, the FR-056 coverage case, and the FR-053 structural assertion.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { act } from 'react'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import type { BrowserLiveWsCallbacks } from '@/lib/browserLiveWs'
import { useChatStore, type SessionChatState } from '@/store/chat'
import { useUiStore } from '@/store/ui'

const { mockSendControl, mockSendInput, mockSendTabAction, mockSendViewport, callbacksRef } = vi.hoisted(() => ({
  mockSendControl: vi.fn(() => true),
  mockSendInput: vi.fn(),
  mockSendTabAction: vi.fn(() => true),
  mockSendViewport: vi.fn(() => true),
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
          sendInput: mockSendInput,
          sendControl: mockSendControl,
          sendTabAction: mockSendTabAction,
          sendViewport: mockSendViewport,
          isConnected: true,
        }
      },
    ),
  }
})

import { BrowserLiveView } from './BrowserLiveView'

function fakeMediaStream(id = 'stream-1'): MediaStream {
  return { id } as unknown as MediaStream
}

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

function emitTabs(activeIndex: number, tabs: Array<{ index: number; title?: string; url?: string; active?: boolean }>) {
  act(() => {
    callbacksRef.current?.onTabs?.({
      type: 'browser_tabs',
      session_id: 's1',
      active_index: activeIndex,
      tabs,
    })
  })
}

function stubFrameRect() {
  const container = screen.getByTestId('browser-live-frame')
  vi.spyOn(container, 'getBoundingClientRect').mockReturnValue({
    left: 0, top: 0, width: 1280, height: 720, right: 1280, bottom: 720, x: 0, y: 0,
    toJSON() { return {} },
  } as DOMRect)
  return container
}

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

/** Confirms the take: drives the mock ws's 'controlling' ack, the same way
 * a real server round-trip would. */
function ackControlling() {
  act(() => {
    callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'controlling' })
  })
}

const initialChatState = useChatStore.getState()

beforeEach(() => {
  vi.clearAllMocks()
  callbacksRef.current = null
  useChatStore.setState({ sessionsById: {} }, false)
  useUiStore.setState({ toasts: [] })
})

afterEach(() => {
  vi.restoreAllMocks()
  // Harmless when a test never switched to fake timers; required for the
  // one test below that does (the wheel/pointermove flush is paced by a
  // real setTimeout — see MOVE_FLUSH_MS — so leaving fake timers on would
  // hang every later test's own real timers, e.g. FIRST_FRAME_TIMEOUT_MS).
  vi.useRealTimers()
  useChatStore.setState(initialChatState, true)
})

// ── BROWSER-FR-001 ──────────────────────────────────────────────────────────
describe('BrowserLiveView — taking the wheel never touches the agent turn (BROWSER-FR-001)', () => {
  it('does not call cancelStream when taking the wheel, and DOES send one browser_control{take} frame in the same gesture', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    setAgentWorking('s1', true)
    const cancelSpy = vi.spyOn(useChatStore.getState(), 'cancelStream')

    fireEvent.click(screen.getByRole('button', { name: /take over/i }))

    expect(cancelSpy).not.toHaveBeenCalled()
    expect(mockSendControl).toHaveBeenCalledTimes(1)
    expect(mockSendControl).toHaveBeenCalledWith('take')
  })
})

// ── BROWSER-FR-053 structural assertion ─────────────────────────────────────
describe('BrowserLiveView — BROWSER-FR-053 structural assertion', () => {
  it('does not contain agentPausedByUser anywhere', () => {
    const here = fileURLToPath(import.meta.url)
    const componentPath = here.replace(/BrowserLiveView\.handover\.test\.tsx$/, 'BrowserLiveView.tsx')
    const source = readFileSync(componentPath, 'utf8')

    expect(source).not.toMatch(/agentPausedByUser/)
    expect(source).not.toMatch(/agentPausedByUserRef/)
    expect(source).not.toMatch(/setAgentPausedByUser/)
    expect(source).not.toMatch(/effectiveAgentWorking/)
  })
})

// ── BROWSER-FR-053/FR-054 — the headline regression ─────────────────────────
describe('BrowserLiveView — the operator keeps driving after the click that took the wheel (BROWSER-FR-053/FR-054)', () => {
  it('keeps dispatching keyboard, wheel and pointermove AFTER the pointerup that took the wheel, while the agent is still working', async () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    const container = stubFrameRect()

    // Click-to-drive at idle, confirmed by the server.
    fireEvent.pointerDown(container, { clientX: 20, clientY: 20 })
    fireEvent.pointerUp(container, { clientX: 20, clientY: 20 })
    ackControlling()

    // A NEW agent turn starts while the operator still holds the wheel —
    // the exact scenario the deleted auto-release effect used to fight.
    setAgentWorking('s1', true)
    mockSendInput.mockClear()

    // Keyboard dispatches synchronously (handleKeyDown calls dispatchInput
    // directly — no pacer involved).
    fireEvent.keyDown(container, { key: 'a' })
    expect(mockSendInput).toHaveBeenCalledWith(expect.objectContaining({ kind: 'text', text: 'a' }))

    // Wheel and pointermove are coalesced onto a single MOVE_FLUSH_MS timer
    // (scheduleInputFlush) — switch to fake timers to observe the flush.
    vi.useFakeTimers()
    mockSendInput.mockClear()
    fireEvent.wheel(container, { deltaX: 0, deltaY: 120 })
    await vi.advanceTimersByTimeAsync(30)
    expect(mockSendInput).toHaveBeenCalledWith(expect.objectContaining({ kind: 'wheel' }))

    mockSendInput.mockClear()
    fireEvent.pointerMove(container, { clientX: 30, clientY: 30 })
    await vi.advanceTimersByTimeAsync(30)
    expect(mockSendInput).toHaveBeenCalledWith(expect.objectContaining({ kind: 'mouse_move' }))
  })
})

// ── BROWSER-FR-054 — priority for the chip and the cursor ──────────────────
describe('BrowserLiveView — operator-holds-wheel outranks agent-working for the chip and the cursor (BROWSER-FR-054)', () => {
  it('keeps you-driving priority over agent-working for the chip and the cursor', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    const container = stubFrameRect()

    fireEvent.pointerDown(container, { clientX: 20, clientY: 20 })
    ackControlling()

    setAgentWorking('s1', true)

    expect(screen.getByTestId('browser-live-status-chip')).toHaveTextContent("You're driving")
    expect(screen.getByTestId('browser-live-glow')).toHaveAttribute('data-visual-state', 'you-driving')
    expect(container).toHaveStyle({ cursor: 'default' })
    // The watch-only "Take over" affordance must not reappear while the
    // operator already holds the wheel, no matter what the agent is doing.
    expect(screen.queryByRole('button', { name: /take over/i })).not.toBeInTheDocument()
  })
})

// ── BROWSER-FR-055 — no auto-release ────────────────────────────────────────
describe('BrowserLiveView — the wheel is not handed back the instant the take lands (BROWSER-FR-055)', () => {
  it('does not auto-release the wheel after the take ack while the agent is still working', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    setAgentWorking('s1', true)

    fireEvent.click(screen.getByRole('button', { name: /take over/i }))
    mockSendControl.mockClear()
    ackControlling()

    expect(mockSendControl).not.toHaveBeenCalledWith('release')
    expect(screen.getByTestId('browser-live-status-chip')).toHaveTextContent("You're driving")
  })
})

// ── BROWSER-FR-057 — cleared only on a named release, never on agentWorking ─
describe('BrowserLiveView — a wheel held across the end of one turn survives the start of the next (BROWSER-FR-057)', () => {
  it('still holds the wheel when a SECOND agent turn starts after the first ended', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    const container = stubFrameRect()
    fireEvent.pointerDown(container, { clientX: 20, clientY: 20 })
    ackControlling()
    mockSendControl.mockClear()

    setAgentWorking('s1', true)
    setAgentWorking('s1', false)
    setAgentWorking('s1', true)

    // Neither transition may have sent a release frame — a release, once
    // this bug is reintroduced, fires on the SECOND turn's first render
    // (see the deleted effect's own former doc comment for the exact
    // mechanism this guards against).
    expect(mockSendControl).not.toHaveBeenCalledWith('release')
    expect(screen.getByTestId('browser-live-status-chip')).toHaveTextContent("You're driving")
  })

  it('clears operator-holds-wheel on a server released status and on annotate mode', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" canAnnotate mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    const container = stubFrameRect()
    fireEvent.pointerDown(container, { clientX: 20, clientY: 20 })
    ackControlling()
    expect(screen.getByTestId('browser-live-status-chip')).toHaveTextContent("You're driving")

    act(() => {
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'released' })
    })
    expect(screen.getByTestId('browser-live-status-chip')).not.toHaveTextContent("You're driving")

    // Re-take, then release by entering annotate mode instead.
    fireEvent.pointerDown(container, { clientX: 20, clientY: 20 })
    ackControlling()
    mockSendControl.mockClear()
    fireEvent.click(screen.getByRole('button', { name: /annotate a region/i }))

    expect(mockSendControl).toHaveBeenCalledWith('release')
  })

  // BROWSER-FR-031b: an UNSOLICITED released frame (the server took the
  // wheel back — e.g. the idle-release sweeper — without this connection
  // ever asking) must clear operator-holds-wheel exactly the same as a
  // solicited one, and a plain `control_only` broadcast frame (telling this
  // viewer about SOMEONE ELSE's control change) must NOT.
  it('clears operator-holds-wheel on an UNSOLICITED server released frame, and re-takes on the next click', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    const container = stubFrameRect()
    fireEvent.pointerDown(container, { clientX: 20, clientY: 20 })
    ackControlling()
    expect(screen.getByTestId('browser-live-status-chip')).toHaveTextContent("You're driving")

    // A control_only frame (about a DIFFERENT viewer) must be a no-op here.
    act(() => {
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'idle', control_only: true, controlled_by_other: false })
    })
    expect(screen.getByTestId('browser-live-status-chip')).toHaveTextContent("You're driving")

    // The server released this connection's own hold with no local
    // sendControl('release') call preceding it.
    act(() => {
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'released' })
    })
    expect(screen.getByTestId('browser-live-status-chip')).not.toHaveTextContent("You're driving")

    // The `controllingRef` guard that used to block a re-take
    // (`if (controllingRef.current) return`) must have cleared too.
    mockSendControl.mockClear()
    fireEvent.pointerDown(container, { clientX: 25, clientY: 25 })
    expect(mockSendControl).toHaveBeenCalledWith('take')
  })
})

// ── BROWSER-FR-056 — coverage, not a regression guard ───────────────────────
describe('BrowserLiveView — one click both acquires the wheel and dispatches, from every entry point (BROWSER-FR-056, coverage)', () => {
  it('acquires the wheel and dispatches in one click from the frame, the omnibox, the tab strip and Take over', () => {
    render(<BrowserLiveView sessionId="s1" agentId="a1" mediaStream={fakeMediaStream()} />)
    connectAndFrame()
    emitTabs(0, [
      { index: 0, title: 'Tab A', url: 'https://a.example.com' },
      { index: 1, title: 'Tab B', url: 'https://b.example.com' },
    ])
    const container = stubFrameRect()

    // 1) The frame.
    fireEvent.pointerDown(container, { clientX: 20, clientY: 20 })
    expect(mockSendControl).toHaveBeenCalledWith('take')
    expect(mockSendInput).toHaveBeenCalledWith(expect.objectContaining({ kind: 'mouse_down' }))
    ackControlling()
    act(() => {
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'released' })
    })
    mockSendControl.mockClear()
    mockSendInput.mockClear()

    // 2) The omnibox.
    const addressBar = screen.getByLabelText('Address bar')
    fireEvent.change(addressBar, { target: { value: 'example.com' } })
    fireEvent.submit(addressBar.closest('form') as HTMLFormElement)
    expect(mockSendControl).toHaveBeenCalledWith('take')
    expect(mockSendInput).toHaveBeenCalledWith(expect.objectContaining({ kind: 'navigate' }))
    ackControlling()
    act(() => {
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'released' })
    })
    mockSendControl.mockClear()
    mockSendTabAction.mockClear()

    // 3) The tab strip.
    fireEvent.click(screen.getByTestId('browser-tab-1'))
    expect(mockSendControl).toHaveBeenCalledWith('take')
    expect(mockSendTabAction).toHaveBeenCalledWith('switch', 1)
    ackControlling()
    act(() => {
      callbacksRef.current?.onStatus?.({ type: 'browser_status', state: 'released' })
    })
    mockSendControl.mockClear()

    // 4) The explicit "Take over" button, shown once the agent is working.
    setAgentWorking('s1', true)
    fireEvent.click(screen.getByRole('button', { name: /take over/i }))
    expect(mockSendControl).toHaveBeenCalledWith('take')
  })
})

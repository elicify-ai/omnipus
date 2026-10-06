// ADR-20260928 D9 + MAJ-002 — web Stop/Esc surface scoping (RED pack).
//
// The user-visible contract under test, asserted at the WIRE level (a spied
// connection.send — the process edge): which cancel frame each surface emits.
//
//   - Single Stop press / single Esc (composer focused) → exactly one
//     {type:'cancel', session_id} frame whose scope is absent or "session"
//     (MAJ-002 default session) — NEVER "tree".
//   - Second activation on the SAME session within the 3-second confirmation
//     window — confirming the visible Stop-all offer, or a second Esc —
//     sends a frame with scope:"tree". The window expires after 3 s, on
//     focus change, and on session switch. (The Stop button itself unmounts
//     once the cancel lands locally — markLastMessageInterrupted flips
//     isStreaming — so the spec-named second-activation surface is the offer
//     the first press must show, plus the Esc key.)
//   - The FIRST press must VISIBLY offer the Stop-all confirmation (D9).
//   - The Stop-all control and the /cancel command are themselves the
//     confirmation: one action → one scope:"tree" frame.
//   - Escape that closes an open slash menu/editor sends NO stop frame
//     (D9: Esc first closes the active overlay).
//
// RED status (2026-10-02): the tests named RED below fail because no
// double-press window, no visible offer, no Stop-all control, and no
// tree-scoped frame exist anywhere in the funnel yet. Tests marked PIN
// assert the decided single-surface behaviour that already works and must
// survive GREEN unchanged.
//
// Oracle: ADR-20260928 D9 table + MAJ-002 wire enum; no expected value was
// derived from running the implementation.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import * as React from 'react'
import { act } from 'react'
import { useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
if (typeof globalThis.ResizeObserver === 'undefined') {
  vi.stubGlobal('ResizeObserver', ResizeObserverStub)
}
if (typeof Element !== 'undefined' && !Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = function () {}
}

import { OmnipusComposer } from './ChatScreen'

// Presentation/runtime adapter only: stop/cancel interception and frame
// construction stay real through OmnipusComposer, both hooks and the stores.
const composerRuntime = vi.hoisted(() => {
  let text = ''
  return {
    getState: () => ({ text }),
    setText: vi.fn((value: string) => { text = value }),
    send: vi.fn(),
    addAttachment: vi.fn(),
    subscribe: vi.fn(() => vi.fn()),
  }
})

import { MockButton } from '@/test/assistantUiMock'
vi.mock('@assistant-ui/react', async () => (await import('@/test/assistantUiMock')).createAssistantUiMock({
  ThreadPrimitive: {
    Viewport: ({ children, className }: { children: React.ReactNode; className?: string }) =>
              React.createElement('div', { className }, children),
  },
  ComposerPrimitive: {
    Root: ({ children, className, onSubmit }: { children: React.ReactNode; className?: string; onSubmit?: (e: React.FormEvent) => void }) =>
              React.createElement('form', { className, onSubmit, 'data-testid': 'composer-form' }, children),
    Send: ({ disabled, children, className, 'data-testid': testId, 'aria-label': ariaLabel }: {
              disabled?: boolean; children?: React.ReactNode; className?: string;
              'data-testid'?: string; 'aria-label'?: string;
            }) =>
              React.createElement(MockButton, {
                type: 'button', disabled, className,
                'data-testid': testId ?? 'chat-send',
                'aria-label': ariaLabel, children: children}),
    AddAttachment: ({ disabled, children, className, 'aria-label': ariaLabel }: {
              disabled?: boolean; children?: React.ReactNode; className?: string; 'aria-label'?: string
            }) =>
              React.createElement(MockButton, { type: 'button', disabled, className, 'aria-label': ariaLabel, 'data-testid': 'add-attachment', children: children}),
  },
  AttachmentPrimitive: {
    Remove: ({ children, className, 'aria-label': ariaLabel }: { children?: React.ReactNode; className?: string; 'aria-label'?: string }) =>
              React.createElement(MockButton, { type: 'button', className, 'aria-label': ariaLabel, children: children}),
  },
  ActionBarPrimitive: {
    Root: ({ children, className }: { children: React.ReactNode; className?: string }) =>
              React.createElement('div', { className }, children),
  },
  useComposerRuntime: vi.fn(() => composerRuntime),
  useMessage: () => ({
          id: 'msg_1',
          role: 'assistant',
          status: { type: 'complete' },
          content: [],
        }),
}))

vi.mock('@tanstack/react-query', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-query')>()
  const mockCommands = [
    { name: 'clear',   label: '/clear',   description: 'Start a new conversation',   delivery: 'client', available_while_streaming: false },
    { name: 'help',    label: '/help',    description: 'Show available commands',     delivery: 'client', available_while_streaming: false },
    { name: 'model',   label: '/model',   description: 'Change the chat model',       delivery: 'client', available_while_streaming: false },
    { name: 'agents',  label: '/agents',  description: 'Open agent selector',         delivery: 'client', available_while_streaming: false },
    { name: 'cancel',  label: '/cancel',  description: 'Cancel the current turn',     delivery: 'client', available_while_streaming: true  },
    { name: 'stop',    label: '/stop',    description: 'Stop only this conversation', delivery: 'client', available_while_streaming: true  },
  ]
  const mockSkills = [
    { id: 'web-research',  name: 'Web Research',  version: '1.0', description: 'Web search and extraction', verified: true,  status: 'active' },
    { id: 'code-review',   name: 'Code Review',   version: '1.0', description: 'Reviews code quality',      verified: true,  status: 'active' },
    { id: 'data-analysis', name: 'Data Analysis', version: '1.0', description: 'Analyses datasets',         verified: false, status: 'active' },
  ]
  return {
    ...actual,
    useQuery: (opts: { queryKey: unknown[] }) => {
      const key = opts?.queryKey
      if (Array.isArray(key) && key[0] === 'commands' && key[1] === 'web') {
        return { data: mockCommands, isError: false, refetch: vi.fn() }
      }
      if (Array.isArray(key) && key[0] === 'skills') {
        return { data: mockSkills, isError: false, refetch: vi.fn() }
      }
      return { data: [], isError: false, refetch: vi.fn() }
    },
    useMutation: () => ({ mutate: vi.fn(), isPending: false }),
    useQueryClient: () => ({ invalidateQueries: vi.fn(), removeQueries: vi.fn() }),
  }
})

vi.mock('@tanstack/react-router', () => ({
  useRouter: () => ({ navigate: vi.fn() }),
  useSearch: () => ({}),
  Link: ({ children }: { children: React.ReactNode }) => children,
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([]),
    fetchSessionMessages: vi.fn().mockResolvedValue([]),
    fetchCommands: vi.fn().mockResolvedValue([]),
    fetchSkills: vi.fn().mockResolvedValue([]),
    fetchProviders: vi.fn().mockResolvedValue([]),
    uploadFiles: vi.fn(),
  }
})

vi.mock('@/assets/logo/omnipus-avatar.svg?url', () => ({ default: 'omnipus-avatar.svg' }))
vi.mock('./RateLimitIndicator', () => ({ RateLimitIndicator: () => null }))
vi.mock('./markdown-text', () => ({ MarkdownText: () => null }))
vi.mock('./tools/GenericToolCall', () => ({ GenericToolCall: () => null }))
vi.mock('@/components/shared/IconRenderer', () => ({ IconRenderer: () => null }))
vi.mock('./composer/AgentPicker', () => ({ AgentPicker: () => null }))
vi.mock('./composer/ModelPicker', () => ({ ModelPicker: () => null }))
vi.mock('./composer/TokenCounter', () => ({ TokenCounter: () => null }))

// A cancel frame as it crosses the WebSocket edge — the GENERATED wire type
// (src/lib/api/generated/asyncapi-types.ts::CancelFrame): type, session_id,
// optional scope enum "session"|"tree". No hand-written wire shape here.
import type { CancelFrame } from '@/lib/api/generated/asyncapi-types'
type CancelFrameOnWire = CancelFrame

function sentCancelFrames(send: ReturnType<typeof vi.fn>): CancelFrameOnWire[] {
  return send.mock.calls
    .map((call) => call[0] as CancelFrameOnWire)
    .filter((frame) => frame && frame.type === 'cancel')
}

function assertNoTreeScope(frames: CancelFrameOnWire[], context: string) {
  for (const frame of frames) {
    expect(frame.scope, `${context}: a frame carried scope ${JSON.stringify(frame.scope)} — single-session surfaces must never send tree (MAJ-002)`).not.toBe('tree')
  }
}

function pressStop() {
  const stop = screen.getByTestId('stop-btn')
  act(() => { fireEvent.click(stop) })
}

function pressEscape() {
  const input = screen.getByTestId('composer-input')
  act(() => { fireEvent.keyDown(input, { key: 'Escape' }) })
}

function submitCommand(text: string) {
  const input = screen.getByTestId('composer-input')
  act(() => {
    composerRuntime.setText(text)
    fireEvent.change(input, { target: { value: text } })
    fireEvent.submit(screen.getByTestId('composer-form'))
  })
}

describe('Stop/Esc surface scoping (ADR-20260928 D9)', () => {
  let send: ReturnType<typeof vi.fn>

  beforeEach(() => {
    send = vi.fn().mockReturnValue(true)
    composerRuntime.setText('')
    composerRuntime.send.mockClear()
    act(() => {
      useConnectionStore.setState({
        connection: { send } as unknown as ReturnType<typeof useConnectionStore.getState>['connection'],
        isConnected: true,
        connectionError: null,
      })
      useSessionStore.setState({
        activeSessionId: 'sess_test',
        activeAgentId: 'general-assistant',
        activeAgentType: null,
        attachedSessionType: null,
      })
      useChatStore.setState({
        messages: [],
        isStreaming: true,
        isReplaying: false,
        toolCalls: {},
        sessionTokens: 0,
        sessionCost: 0,
      })
    })
  })

  afterEach(() => {
    vi.useRealTimers()
    act(() => {
      useChatStore.setState({
        messages: [],
        isStreaming: false,
        isReplaying: false,
        toolCalls: {},
        sessionTokens: 0,
        sessionCost: 0,
      })
      useConnectionStore.setState({ connection: null, isConnected: true, connectionError: null })
      useSessionStore.setState({ activeSessionId: null })
    })
  })

  it('PIN single stop press sends exactly one cancel frame without tree scope', () => {
    render(<OmnipusComposer />)
    pressStop()

    const frames = sentCancelFrames(send)
    expect(frames).toHaveLength(1)
    expect(frames[0].session_id).toBe('sess_test')
    assertNoTreeScope(frames, 'single stop press')
  })

  // Founder-reconfirmed D9 row 1: /stop is always self-only; the Stop
  // button's first-press state/offer must not widen that command's scope.
  it.each([
    { chat: 'root', sessionType: null },
    { chat: 'helper', sessionType: 'delegate' },
  ] as const)('submitted /stop in a $chat chat sends only a session cancel and offers Stop all', ({ sessionType }) => {
    act(() => { useSessionStore.setState({ attachedSessionType: sessionType }) })
    render(<OmnipusComposer />)

    submitCommand('/stop')

    expect(send.mock.calls).toEqual([[{ type: 'cancel', session_id: 'sess_test' }]])
    assertNoTreeScope(sentCancelFrames(send), `submitted /stop in ${sessionType ?? 'root'}`)
    expect(screen.getByRole('button', { name: 'Stopping...' })).toBeInTheDocument()
    expect(screen.getByText('Stop all')).toBeInTheDocument()
    expect(composerRuntime.send).not.toHaveBeenCalled()
  })

  it('palette /stop sends only a session cancel and uses the first-press state and offer', () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('composer-input')
    act(() => { fireEvent.change(input, { target: { value: '/stop' } }) })
    const stopItem = screen.getByText('/stop').closest('button')
    expect(stopItem).not.toBeNull()
    act(() => { fireEvent.mouseDown(stopItem!) })

    expect(send.mock.calls).toEqual([[{ type: 'cancel', session_id: 'sess_test' }]])
    assertNoTreeScope(sentCancelFrames(send), 'palette /stop')
    expect(screen.getByRole('button', { name: 'Stopping...' })).toBeInTheDocument()
    expect(screen.getByText('Stop all')).toBeInTheDocument()
    expect(composerRuntime.send).not.toHaveBeenCalled()
  })

  it('/stop with an already armed offer never confirms a tree stop', () => {
    render(<OmnipusComposer />)
    pressStop()
    expect(screen.getByText('Stop all')).toBeInTheDocument()

    submitCommand('/stop')

    // The first Stop ended streaming locally; there is no new turn for the
    // second self-only command to cancel, and it must not escalate to tree.
    expect(send.mock.calls).toEqual([[{ type: 'cancel', session_id: 'sess_test' }]])
    assertNoTreeScope(sentCancelFrames(send), '/stop with an armed offer')
    expect(screen.getByRole('button', { name: 'Stopping...' })).toBeInTheDocument()
    expect(screen.getByText('Stop all')).toBeInTheDocument()
  })

  it('submitted /cancel still sends exactly one tree cancel frame', () => {
    render(<OmnipusComposer />)

    submitCommand('/cancel')

    expect(send.mock.calls).toEqual([[{ type: 'cancel', session_id: 'sess_test', scope: 'tree' }]])
    expect(composerRuntime.send).not.toHaveBeenCalled()
    expect(screen.queryByText('Stop all')).not.toBeInTheDocument()
  })

  it('/cancel explicitly confirms Stop all after /stop without an extra session cancel', () => {
    render(<OmnipusComposer />)
    submitCommand('/stop')
    expect(screen.getByText('Stop all')).toBeInTheDocument()

    submitCommand('/cancel')

    expect(send.mock.calls).toEqual([
      [{ type: 'cancel', session_id: 'sess_test' }],
      [{ type: 'cancel', session_id: 'sess_test', scope: 'tree' }],
    ])
    expect(screen.queryByText('Stop all')).not.toBeInTheDocument()
    expect(composerRuntime.send).not.toHaveBeenCalled()
  })

  it('RED confirming the stop-all offer within 3s sends scope tree', () => {
    vi.useFakeTimers()
    render(<OmnipusComposer />)

    pressStop()
    // The offer is the second activation's surface (D9: the first press
    // visibly offers the Stop-all confirmation; confirming it is the second
    // press). Still inside the window at ~1s.
    act(() => { vi.advanceTimersByTime(1000) })
    const offer = screen.getByText(/stop all/i).closest('button')
    expect(offer).not.toBeNull()
    act(() => { fireEvent.click(offer!) })

    const frames = sentCancelFrames(send)
    expect(frames).toHaveLength(2)
    expect(frames[0].scope).not.toBe('tree')
    expect(frames[1].scope, `confirming the offer within the 3s window must carry scope "tree" (D9), got ${JSON.stringify(frames[1].scope)}`).toBe('tree')
  })

  it('PIN stop-all offer is gone once the 3-second window expires', () => {
    vi.useFakeTimers()
    render(<OmnipusComposer />)

    pressStop()
    expect(screen.getByText(/stop all/i)).toBeInTheDocument()
    // Past the window (3100ms > 3s) the confirmation is no longer offered —
    // a fresh activation is a new FIRST press (single-session), so no tree
    // frame may exist.
    act(() => { vi.advanceTimersByTime(3100) })
    expect(screen.queryByText(/stop all/i)).not.toBeInTheDocument()
    assertNoTreeScope(sentCancelFrames(send), 'after window expiry')
  })

  it('PIN focus change closes the stop-all offer window', () => {
    vi.useFakeTimers()
    render(<OmnipusComposer />)

    pressStop()
    expect(screen.getByText(/stop all/i)).toBeInTheDocument()
    act(() => { fireEvent(window, new Event('blur')) })
    expect(screen.queryByText(/stop all/i)).not.toBeInTheDocument()
    assertNoTreeScope(sentCancelFrames(send), 'after focus change')
  })

  it('PIN session switch closes the stop-all offer window', () => {
    vi.useFakeTimers()
    render(<OmnipusComposer />)

    pressStop()
    expect(screen.getByText(/stop all/i)).toBeInTheDocument()
    act(() => { useSessionStore.setState({ activeSessionId: 'sess_other' }) })
    expect(screen.queryByText(/stop all/i)).not.toBeInTheDocument()
    const frames = sentCancelFrames(send)
    expect(frames.map((f) => f.session_id)).not.toContain('sess_other')
    assertNoTreeScope(frames, 'after session switch')
  })

  it('RED first stop press visibly offers the stop-all confirmation', () => {
    render(<OmnipusComposer />)
    pressStop()

    // D9: the first press VISIBLY offers the Stop-all confirmation — an
    // accessible "Stop all" affordance must be present after one press.
    expect(screen.getByText(/stop all/i)).toBeInTheDocument()
  })

  it('RED stop-all control sends tree scope in one action', () => {
    render(<OmnipusComposer />)

    // The dedicated Stop-all control (chat session controls) is itself the
    // confirmation: ONE activation → one scope:"tree" frame, no double press.
    const stopAll = screen.getByRole('button', { name: /stop all/i })
    act(() => { fireEvent.click(stopAll) })

    const frames = sentCancelFrames(send)
    expect(frames).toHaveLength(1)
    expect(frames[0].scope, `stop-all control must send scope "tree" in one action (D9), got ${JSON.stringify(frames[0].scope)}`).toBe('tree')
    expect(frames[0].session_id).toBe('sess_test')
  })

  it('RED slash /cancel sends tree scope in one action', () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('composer-input')

    // /cancel is available_while_streaming, so it renders in the menu while
    // streaming; selecting it runs the command (mouse-down selects, same as
    // the partitioned-menu suite).
    act(() => { fireEvent.change(input, { target: { value: '/' } }) })
    const cancelItem = screen.getByText('/cancel').closest('button')
    expect(cancelItem).not.toBeNull()
    act(() => { fireEvent.mouseDown(cancelItem!) })

    const frames = sentCancelFrames(send)
    expect(frames).toHaveLength(1)
    expect(frames[0].scope, `/cancel is a Stop-all surface — its frame must carry scope "tree" (D9), got ${JSON.stringify(frames[0].scope)}`).toBe('tree')
    expect(frames[0].session_id).toBe('sess_test')
  })

  it('PIN single escape with composer focused sends one cancel frame without tree scope', () => {
    render(<OmnipusComposer />)
    pressEscape()

    const frames = sentCancelFrames(send)
    expect(frames).toHaveLength(1)
    expect(frames[0].session_id).toBe('sess_test')
    assertNoTreeScope(frames, 'single escape')
  })

  it('RED second escape within 3s sends scope tree', () => {
    vi.useFakeTimers()
    render(<OmnipusComposer />)

    pressEscape()
    act(() => { vi.advanceTimersByTime(1000) })
    pressEscape()

    const frames = sentCancelFrames(send)
    expect(frames).toHaveLength(2)
    expect(frames[1].scope, `second escape within the 3s window must carry scope "tree" (D9), got ${JSON.stringify(frames[1].scope)}`).toBe('tree')
  })

  it('PIN escape that closes an open slash menu sends no stop frame', () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('composer-input')

    // Open the slash menu mid-stream, then Escape: the menu must close and
    // NO cancel frame may reach the wire (D9 — Esc first closes the active
    // overlay; only the NEXT Esc is a stop).
    act(() => { fireEvent.change(input, { target: { value: '/' } }) })
    expect(screen.getByText('/cancel')).toBeInTheDocument()
    act(() => { fireEvent.keyDown(input, { key: 'Escape' }) })
    expect(screen.queryByText('/cancel')).not.toBeInTheDocument()

    expect(sentCancelFrames(send)).toHaveLength(0)
  })
})

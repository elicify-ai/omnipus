// Web Stop/Esc and current-chat redirect, asserted at connection.send (the
// process edge), with the composer, both hooks and the stores left real.
//
// Founder 2026-10-06 (permission to replace removed-button assertions):
// "why would we need a stop all button in addition if two times stop does
// the job already? the button needs to go, /stop in chat needs to work like
// one escape or one stop click, /stop-redirect in the chat does not redirect
// a helper it redirects the chat itself not their helper".
//
// First Stop/Esc or /stop sends session scope and arms the existing 3 s
// window. The next activation confirms tree scope; /cancel is immediate
// tree. No separate Stop-all buttons remain. Redirect targets the current
// chat, root or helper. Empty/missing targets still refuse without a frame.
// Oracle: the founder's ruling + the generated wire contract; expected
// values are not derived from observing the implementation.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import * as React from 'react'
import { act } from 'react'
import { useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'

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
    { name: 'stop-redirect', label: '/stop-redirect', description: 'Replace this chat\'s turn', delivery: 'client', available_while_streaming: true, argument_hint: '<instruction>' },
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

describe('Single Stop surface scoping (ADR-20260928 D9)', () => {
  it('PIN single stop press sends exactly one cancel frame without tree scope', () => {
    render(<OmnipusComposer />)
    pressStop()

    const frames = sentCancelFrames(send)
    expect(frames).toHaveLength(1)
    expect(frames[0].session_id).toBe('sess_test')
    assertNoTreeScope(frames, 'single stop press')
  })

})

describe('Slash Stop/cancel and current-chat redirect (founder 2026-10-06)', () => {
  it.each([
    { chat: 'root', sessionType: null },
    { chat: 'helper', sessionType: 'delegate' },
  ] as const)('submitted /stop in a $chat chat sends only a session cancel and shows Stopping...', ({ sessionType }) => {
    act(() => { useSessionStore.setState({ attachedSessionType: sessionType }) })
    render(<OmnipusComposer />)

    submitCommand('/stop')

    expect(send.mock.calls).toEqual([[{ type: 'cancel', session_id: 'sess_test' }]])
    assertNoTreeScope(sentCancelFrames(send), `submitted /stop in ${sessionType ?? 'root'}`)
    expect(screen.getByRole('button', { name: 'Stopping...' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /stop all/i })).not.toBeInTheDocument()
    expect(composerRuntime.send).not.toHaveBeenCalled()
  })

  it('palette /stop sends only a session cancel and uses the first-press state', () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('composer-input')
    act(() => { fireEvent.change(input, { target: { value: '/stop' } }) })
    const stopItem = screen.getByText('/stop').closest('button')
    expect(stopItem).not.toBeNull()
    act(() => { fireEvent.mouseDown(stopItem!) })

    expect(send.mock.calls).toEqual([[{ type: 'cancel', session_id: 'sess_test' }]])
    assertNoTreeScope(sentCancelFrames(send), 'palette /stop')
    expect(screen.getByRole('button', { name: 'Stopping...' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /stop all/i })).not.toBeInTheDocument()
    expect(composerRuntime.send).not.toHaveBeenCalled()
  })

  it('/stop inside the armed window is exactly a second Stop activation', () => {
    render(<OmnipusComposer />)
    pressStop()
    expect(screen.queryByRole('button', { name: /stop all/i })).not.toBeInTheDocument()

    submitCommand('/stop')

    // Founder: "/stop in chat needs to work like one escape or one stop
    // click" — it reuses the same second-activation tree path when armed.
    expect(send.mock.calls).toEqual([
      [{ type: 'cancel', session_id: 'sess_test' }],
      [{ type: 'cancel', session_id: 'sess_test', scope: 'tree' }],
    ])
    expect(screen.getByRole('button', { name: 'Stopping...' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /stop all/i })).not.toBeInTheDocument()
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
    expect(screen.queryByRole('button', { name: /stop all/i })).not.toBeInTheDocument()

    submitCommand('/cancel')

    expect(send.mock.calls).toEqual([
      [{ type: 'cancel', session_id: 'sess_test' }],
      [{ type: 'cancel', session_id: 'sess_test', scope: 'tree' }],
    ])
    expect(screen.queryByText('Stop all')).not.toBeInTheDocument()
    expect(composerRuntime.send).not.toHaveBeenCalled()
  })

  it.each(['Stop', 'Escape'] as const)('/stop arms the same window for a second %s after the stopping label resets', (confirmation) => {
    vi.useFakeTimers()
    render(<OmnipusComposer />)
    submitCommand('/stop')
    expect(send.mock.calls).toEqual([[{ type: 'cancel', session_id: 'sess_test' }]])

    // Past the 1 s label minimum but still inside the 3 s confirmation
    // window: the same Stop button, not a separate offer, must be usable.
    act(() => { vi.advanceTimersByTime(1001) })
    if (confirmation === 'Stop') pressStop()
    else pressEscape()

    expect(send.mock.calls).toEqual([
      [{ type: 'cancel', session_id: 'sess_test' }],
      [{ type: 'cancel', session_id: 'sess_test', scope: 'tree' }],
    ])
  })

  it.each([
    { chat: 'root', sessionType: null },
    { chat: 'helper', sessionType: 'delegate' },
  ] as const)('/stop-redirect in a $chat chat sends the existing frame for that current chat', ({ sessionType }) => {
    act(() => { useSessionStore.setState({ attachedSessionType: sessionType }) })
    render(<OmnipusComposer />)

    submitCommand('/stop-redirect focus on the failing tests')

    // The socket edge is spied, not the backend's redirect implementation:
    // this proves the SPA request, not server acceptance or execution.
    expect(send.mock.calls).toEqual([[{
      type: 'redirect',
      session_id: 'sess_test',
      instruction: 'focus on the failing tests',
    }]])
    expect(sentCancelFrames(send)).toEqual([])
    expect(composerRuntime.send).not.toHaveBeenCalled()
  })
})

describe('Double Stop/Esc confirmation and dismissal (founder 2026-10-06)', () => {
  it('second Stop press within 3s sends scope tree', () => {
    vi.useFakeTimers()
    render(<OmnipusComposer />)

    pressStop()
    // Founder: "the button needs to go". Confirm through the SAME Stop
    // button; retain all frame assertions from the removed-offer test.
    act(() => { vi.advanceTimersByTime(1000) })
    pressStop()

    const frames = sentCancelFrames(send)
    expect(frames).toHaveLength(2)
    expect(frames[0].scope).not.toBe('tree')
    expect(frames[1].scope, `second Stop within the 3s window must carry scope "tree" (D9), got ${JSON.stringify(frames[1].scope)}`).toBe('tree')
  })

  it('PIN confirmation closes once the 3-second window expires', () => {
    vi.useFakeTimers()
    render(<OmnipusComposer />)

    pressStop()
    expect(screen.getByTestId('stop-btn')).toBeInTheDocument()
    // Founder: "the button needs to go" — the remaining Stop button is
    // the confirmation surface, not the removed Stop-all offer.
    act(() => { vi.advanceTimersByTime(3100) })
    expect(screen.queryByTestId('stop-btn')).not.toBeInTheDocument()
    assertNoTreeScope(sentCancelFrames(send), 'after window expiry')
  })

  it('PIN focus change closes the confirmation window', () => {
    vi.useFakeTimers()
    render(<OmnipusComposer />)

    pressStop()
    expect(screen.getByTestId('stop-btn')).toBeInTheDocument()
    act(() => { fireEvent(window, new Event('blur')) })
    act(() => { vi.advanceTimersByTime(1001) })
    expect(screen.queryByTestId('stop-btn')).not.toBeInTheDocument()
    assertNoTreeScope(sentCancelFrames(send), 'after focus change')
  })

  it('PIN session switch closes the confirmation window', () => {
    vi.useFakeTimers()
    render(<OmnipusComposer />)

    pressStop()
    expect(screen.getByTestId('stop-btn')).toBeInTheDocument()
    act(() => { useSessionStore.setState({ activeSessionId: 'sess_other' }) })
    act(() => { vi.advanceTimersByTime(1001) })
    expect(screen.queryByTestId('stop-btn')).not.toBeInTheDocument()
    const frames = sentCancelFrames(send)
    expect(frames.map((f) => f.session_id)).not.toContain('sess_other')
    assertNoTreeScope(frames, 'after session switch')
  })

  it('first Stop press leaves no separate Stop-all offer button', () => {
    render(<OmnipusComposer />)
    pressStop()

    // Founder: "the button needs to go". The armed-offer assertion is
    // intentionally replaced with absence; the main Stop remains usable.
    expect(screen.queryByRole('button', { name: /stop all/i })).not.toBeInTheDocument()
    expect(screen.getByTestId('stop-btn')).toBeInTheDocument()
  })

  it('streaming chat has no separate Stop-all control; /cancel remains one-action tree', () => {
    render(<OmnipusComposer />)

    // Founder: "why would we need a stop all button in addition if two
    // times stop does the job already?" Keep the prior one-action frame
    // assertions through /cancel, which is the remaining explicit command.
    expect(screen.queryByRole('button', { name: /stop all/i })).not.toBeInTheDocument()
    submitCommand('/cancel')

    const frames = sentCancelFrames(send)
    expect(frames).toHaveLength(1)
    expect(frames[0].scope, `/cancel must send scope "tree" in one action (D9), got ${JSON.stringify(frames[0].scope)}`).toBe('tree')
    expect(frames[0].session_id).toBe('sess_test')
  })

  it('the same Stop button remains reachable at 2999ms and expires at 3000ms', () => {
    vi.useFakeTimers()
    render(<OmnipusComposer />)
    pressStop()

    // 3 s is the existing confirmation boundary, not a widened timeout.
    act(() => { vi.advanceTimersByTime(2999) })
    expect(screen.getByTestId('stop-btn')).toBeInTheDocument()
    act(() => { vi.advanceTimersByTime(1) })
    expect(screen.queryByTestId('stop-btn')).not.toBeInTheDocument()
    expect(send.mock.calls).toEqual([[{ type: 'cancel', session_id: 'sess_test' }]])
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

// ── Reviewer round 2 (founder stop rules 2026-10-06) ─────────────────────
// W1 stuck label · W2 undelivered Stop · W3 a closed window really is a
// fresh first press · W4 /stop-redirect is neither arming nor a second Stop.
// Oracle: the founder table (click 1 / Esc 1 / /stop = this chat only +
// 3 s window; second activation in the window = tree; /cancel = tree;
// /stop-redirect = this chat's own redirect frame) and the generated wire
// types — not read off the implementation.

const SESSION_FRAME = { type: 'cancel', session_id: 'sess_test' }
const TREE_FRAME = { type: 'cancel', session_id: 'sess_test', scope: 'tree' }

describe('W1 no stuck "Stopping..." after the stream ended', () => {
  it('click 1, stream ended, click 2 inside the window sends one tree frame and Send returns after the window', () => {
    vi.useFakeTimers()
    render(<OmnipusComposer />)

    pressStop()
    // Premise: the first activation already ended the turn locally.
    expect(useChatStore.getState().isStreaming).toBe(false)
    act(() => { vi.advanceTimersByTime(1500) })
    pressStop()

    expect(send.mock.calls).toEqual([[SESSION_FRAME], [TREE_FRAME]])
    expect(screen.getByRole('button', { name: 'Stopping...' })).toBeInTheDocument()

    act(() => { vi.advanceTimersByTime(3100) })
    expect(screen.queryByTestId('stop-btn')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Stopping...' })).not.toBeInTheDocument()
    expect(screen.getByTestId('chat-send')).toBeInTheDocument()
  })

  it('/stop on an idle chat leaves no stuck label and the composer returns to Send', () => {
    vi.useFakeTimers()
    act(() => { useChatStore.setState({ isStreaming: false }) })
    render(<OmnipusComposer />)

    submitCommand('/stop')
    act(() => { vi.advanceTimersByTime(3100) })

    expect(screen.queryByTestId('stop-btn')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Stopping...' })).not.toBeInTheDocument()
    expect(screen.getByTestId('chat-send')).toBeInTheDocument()
  })

  it('/stop twice on an idle chat inside the window sends one tree frame and then returns to Send', () => {
    vi.useFakeTimers()
    act(() => { useChatStore.setState({ isStreaming: false }) })
    render(<OmnipusComposer />)

    submitCommand('/stop')
    act(() => { vi.advanceTimersByTime(1500) })
    submitCommand('/stop')
    expect(sentCancelFrames(send)).toEqual([TREE_FRAME])

    act(() => { vi.advanceTimersByTime(3100) })
    expect(screen.queryByTestId('stop-btn')).not.toBeInTheDocument()
    expect(screen.getByTestId('chat-send')).toBeInTheDocument()
  })
})

describe('Q16 the Stop button holds the Send position for the whole window (founder 2026-10-06)', () => {
  it('stream ended: Stop is still present at 2999ms and Send only returns at 3000ms (Enter: see ChatScreen.uat-enter-after-stop)', () => {
    vi.useFakeTimers()
    render(<OmnipusComposer />)

    pressStop()
    expect(useChatStore.getState().isStreaming).toBe(false) // the turn ended
    act(() => { vi.advanceTimersByTime(2999) })
    expect(screen.getByTestId('stop-btn')).toBeInTheDocument()

    expect(sentCancelFrames(send)).toEqual([SESSION_FRAME])

    act(() => { vi.advanceTimersByTime(1) })
    expect(screen.queryByTestId('stop-btn')).not.toBeInTheDocument()
    expect(screen.getByTestId('chat-send')).toBeInTheDocument()
  })
})

describe('W2 a Stop that sent nothing does not arm the window', () => {
  it('no connection: visible error toast, no Stopping label, next press is a first press', () => {
    act(() => {
      useUiStore.setState({ toasts: [] })
      useConnectionStore.setState({ connection: null, isConnected: false, connectionError: null })
    })
    render(<OmnipusComposer />)

    pressStop()

    const toasts = useUiStore.getState().toasts
    expect(toasts).toHaveLength(1)
    expect(toasts[0].variant).toBe('error')
    expect(toasts[0].message).toMatch(/could not send/i)
    expect(screen.queryByRole('button', { name: 'Stopping...' })).not.toBeInTheDocument()
    expect(screen.queryByTestId('stop-btn')).not.toBeInTheDocument()

    // The gateway comes back while the turn is still running: the next press
    // is again a FIRST press (session scope), never a tree stop.
    act(() => {
      useConnectionStore.setState({
        connection: { send } as unknown as ReturnType<typeof useConnectionStore.getState>['connection'],
        isConnected: true,
      })
      useChatStore.setState({ isStreaming: true })
    })
    pressStop()
    expect(send.mock.calls).toEqual([[SESSION_FRAME]])
  })

  it('no connection on Escape: same visible error and no armed window', () => {
    act(() => {
      useUiStore.setState({ toasts: [] })
      useConnectionStore.setState({ connection: null, isConnected: false, connectionError: null })
    })
    render(<OmnipusComposer />)

    pressEscape()

    expect(useUiStore.getState().toasts).toHaveLength(1)
    act(() => {
      useConnectionStore.setState({
        connection: { send } as unknown as ReturnType<typeof useConnectionStore.getState>['connection'],
        isConnected: true,
      })
      useChatStore.setState({ isStreaming: true })
    })
    pressEscape()
    expect(send.mock.calls).toEqual([[SESSION_FRAME]])
  })
})

type Activation = 'Stop' | 'Escape' | '/stop'
function activate(kind: Activation) {
  if (kind === 'Stop') pressStop()
  else if (kind === 'Escape') pressEscape()
  else submitCommand('/stop')
}
// Closes the 3 s window by the three founder routes.
type Closer = 'expiry' | 'blur' | 'session switch'
function closeWindow(how: Closer) {
  if (how === 'expiry') act(() => { vi.advanceTimersByTime(3100) })
  else if (how === 'blur') act(() => { fireEvent(window, new Event('blur')) })
  else act(() => { useSessionStore.setState({ activeSessionId: 'sess_other' }) })
}

describe('W3 after the window closes, the next activation is a true fresh first press', () => {
  const kinds: Activation[] = ['Stop', 'Escape', '/stop']
  const closers: Closer[] = ['expiry', 'blur', 'session switch']
  const cases = closers.flatMap((closer) => kinds.map((kind) => ({ closer, kind })))

  it.each(cases)('$kind after $closer sends a session-scoped frame, not tree', ({ closer, kind }) => {
    vi.useFakeTimers()
    render(<OmnipusComposer />)

    pressStop() // arms the window
    closeWindow(closer)
    // A turn is running again so the same surfaces are available.
    act(() => { useChatStore.setState({ isStreaming: true }) })
    activate(kind)

    const frames = sentCancelFrames(send)
    expect(frames).toHaveLength(2)
    expect(frames[0]).toEqual(SESSION_FRAME)
    expect(frames[1].scope, `${kind} after ${closer} must be a fresh first press, got ${JSON.stringify(frames[1])}`).toBeUndefined()
    expect(frames[1].session_id).toBe(closer === 'session switch' ? 'sess_other' : 'sess_test')
  })
})

describe('W4 /stop-redirect neither arms the window nor counts as the second Stop', () => {
  it('outside a window: exactly the redirect frame, and the next Stop is still a first press', () => {
    render(<OmnipusComposer />)

    submitCommand('/stop-redirect do the other thing')
    expect(send.mock.calls).toEqual([[{ type: 'redirect', session_id: 'sess_test', instruction: 'do the other thing' }]])

    pressStop()
    expect(sentCancelFrames(send)).toEqual([SESSION_FRAME])
  })

  it('inside an armed window: exactly the redirect frame and no tree frame', () => {
    vi.useFakeTimers()
    render(<OmnipusComposer />)

    pressStop()
    act(() => { vi.advanceTimersByTime(500) })
    submitCommand('/stop-redirect do the other thing')

    expect(send.mock.calls).toEqual([
      [SESSION_FRAME],
      [{ type: 'redirect', session_id: 'sess_test', instruction: 'do the other thing' }],
    ])
    expect(sentCancelFrames(send).some((f) => f.scope === 'tree')).toBe(false)
  })

  // Window state after a redirect, read from useSlashMenu (the redirect branch
  // only calls sendRedirectFrame) and useCancelState (arming happens only in
  // activateFirstPress): the redirect neither disarms nor re-arms. The window
  // armed at t=0 keeps its ORIGINAL 3 s timer: Stop is still there at t=2999
  // and a press then is the confirmed tree stop; with no press, Send returns
  // exactly at t=3000 (a re-arm would have pushed that to t=3500).
  it('inside an armed window: the redirect leaves the window open, un-extended, and a following Stop is the tree stop', () => {
    vi.useFakeTimers()
    render(<OmnipusComposer />)

    pressStop()
    act(() => { vi.advanceTimersByTime(500) })
    submitCommand('/stop-redirect do the other thing')

    // Not disarmed: Stop still holds the Send position right after the redirect.
    expect(screen.getByTestId('stop-btn')).toBeInTheDocument()
    expect(screen.queryByTestId('chat-send')).not.toBeInTheDocument()
    act(() => { vi.advanceTimersByTime(2499) }) // t = 2999
    expect(screen.getByTestId('stop-btn')).toBeInTheDocument()

    // Not re-armed: the original window closes at t = 3000.
    act(() => { vi.advanceTimersByTime(1) })
    expect(screen.queryByTestId('stop-btn')).not.toBeInTheDocument()
    expect(screen.getByTestId('chat-send')).toBeInTheDocument()
  })

  it('inside an armed window: a Stop after the redirect is the second activation (tree)', () => {
    vi.useFakeTimers()
    render(<OmnipusComposer />)

    pressStop()
    act(() => { vi.advanceTimersByTime(500) })
    submitCommand('/stop-redirect do the other thing')
    pressStop()

    expect(send.mock.calls).toEqual([
      [SESSION_FRAME],
      [{ type: 'redirect', session_id: 'sess_test', instruction: 'do the other thing' }],
      [TREE_FRAME],
    ])
  })
})

// G1 (UAT): the idle gap between a goal's turns has isStreaming false, but the
// goal keeper resumes the helper on its own, so Stop must stay reachable.
describe('Stop during the idle gap of an active goal (G1)', () => {
  // The shared setup above resets only flat fields; drop any goal frame a previous test filed.
  beforeEach(() => { act(() => { useChatStore.setState(useChatStore.getInitialState(), true) }) })
  afterEach(() => { act(() => { useChatStore.setState(useChatStore.getInitialState(), true) }) })

  const goal = (state: 'active' | 'done') => ({
    type: 'goal_status' as const, session_id: 'sess_test', goal_id: 'g1', condition: 'c',
    round: 1, max_rounds: 5, latest_reason: '', active_loops: 1, cap: 3, state,
  })

  it('shows Stop while a goal is active and nothing streams, and Stop sends one cancel frame', () => {
    act(() => { useChatStore.setState({ isStreaming: false }); useChatStore.getState().handleFrame(goal('active')) })
    render(<OmnipusComposer />)
    pressStop()
    expect(sentCancelFrames(send)).toEqual([{ type: 'cancel', session_id: 'sess_test' }])
  })

  it('Escape in the focused composer sends exactly one cancel frame while a goal is active and nothing streams', () => {
    act(() => { useChatStore.setState({ isStreaming: false }); useChatStore.getState().handleFrame(goal('active')) })
    render(<OmnipusComposer />)
    pressEscape()
    expect(sentCancelFrames(send)).toEqual([{ type: 'cancel', session_id: 'sess_test' }])
  })

  it('Escape on an idle chat without a goal (or a finished goal) sends nothing', () => {
    act(() => { useChatStore.setState({ isStreaming: false }) })
    render(<OmnipusComposer />)
    pressEscape()
    act(() => { useChatStore.getState().handleFrame(goal('done')) })
    pressEscape()
    expect(sentCancelFrames(send)).toEqual([])
  })

  // CI run 37723139967: the gateway pauses the goal keeper on a person's Stop and reports it as
  // goal_status waiting_on_user (GoalStatusFrame.yaml), then active again on the next message.
  // The client follows that server signal only; it keeps no parallel pause state.
  it('Stop follows the server goal state: shown while active, hidden on waiting_on_user, shown again on active', () => {
    act(() => { useChatStore.setState({ isStreaming: false }); useChatStore.getState().handleFrame(goal('active')) })
    render(<OmnipusComposer />)
    expect(screen.getByTestId('stop-btn')).toBeInTheDocument()
    act(() => { useChatStore.getState().handleFrame({ ...goal('active'), state: 'waiting_on_user' }) })
    expect(screen.queryByTestId('stop-btn')).not.toBeInTheDocument()
    act(() => { useChatStore.getState().handleFrame(goal('active')) })
    expect(screen.getByTestId('stop-btn')).toBeInTheDocument()
  })

  it('Stop of a running goal sends one cancel frame, and the button goes once the server reports waiting_on_user', () => {
    vi.useFakeTimers()
    act(() => { useChatStore.setState({ isStreaming: false }); useChatStore.getState().handleFrame(goal('active')) })
    render(<OmnipusComposer />)
    pressStop()
    expect(sentCancelFrames(send)).toEqual([{ type: 'cancel', session_id: 'sess_test' }])
    act(() => { useChatStore.getState().handleFrame({ ...goal('active'), state: 'waiting_on_user' }) })
    act(() => { vi.advanceTimersByTime(3100) })
    expect(screen.queryByTestId('stop-btn')).not.toBeInTheDocument()
  })

  it('shows no Stop for an idle chat without a goal, or with a finished goal', () => {
    act(() => { useChatStore.setState({ isStreaming: false }) })
    const view = render(<OmnipusComposer />)
    expect(screen.queryByTestId('stop-btn')).not.toBeInTheDocument()
    act(() => { useChatStore.getState().handleFrame(goal('done')) })
    expect(screen.queryByTestId('stop-btn')).not.toBeInTheDocument()
    view.unmount()
  })
})

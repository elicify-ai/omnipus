// ChatScreen.mid-turn-send.test.tsx — mid-turn steering sends (bugfixes3:
// enable mid-turn queued sends in the web chat composer).
//
// Verified context (task brief): the gateway already supports messages
// arriving mid-turn — they're queued per-scope and INJECTED into the running
// turn between tool calls (steering). Channels get this today; the SPA
// composer was the one surface that hard-blocked it via four gates:
//   1. handleKeyDown swallowing Enter while streaming.
//   2. ComposerPrimitive.Root's onSubmit preventDefault while streaming.
//   3. The Send button unmounting (Stop rendered alone) while streaming.
//   4. store sendMessage's isStreaming hard-return (covered separately in
//      src/store/chat.mid-turn-send.test.ts).
//
// This file covers gates 1-3 at the component level. The mechanism under
// test: mid-stream Enter (and a new plain Send button rendered next to Stop)
// call `composerRuntime.send()` DIRECTLY — the `ComposerRuntimeImpl`
// instance method from `useComposerRuntime()` — bypassing assistant-ui's
// `useComposerSend()` React hook, which is hard-disabled whenever
// `thread.isRunning && !thread.capabilities.queue` (verified against the
// installed @assistant-ui packages — see submitMidStreamMessage's doc
// comment in ChatScreen.tsx; our ExternalStoreAdapter runtime always reports
// `capabilities.queue === false`, so that hook can never re-enable mid-run).
// `composerRuntime.send()` reaches `BaseComposerRuntimeCore.send()`, which is
// gated only by `canSend`, not `isRunning` — a genuine, intentional bypass
// of the presentational-only gate, not a workaround.
//
// Mock shape follows ChatScreen.send-button-intercept.test.tsx's precedent
// (real <form>/<textarea> DOM so onSubmit/onKeyDown/onChange wiring is
// actually exercised), extended with a `send` spy on the mocked
// `useComposerRuntime()` return value standing in for the real, internal
// `ComposerRuntimeImpl.send()` — asserting on it proves ChatScreen.tsx
// reaches the bypass method, independent of assistant-ui's own internals
// (which are verified separately, by reading the installed source, not by
// this mock).

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import * as React from 'react'
import { act } from 'react'
import { useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { useComposerRuntime } from '@assistant-ui/react'

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

// Stands in for the REAL, internal `ComposerRuntimeImpl.send()` (NOT the
// gated `useComposerSend()` hook) — see file header.
const mockComposerSend = vi.fn()
const mockSetText = vi.fn()

vi.mock('@assistant-ui/react', () => {
  return {
    useThreadViewportStore: () => ({ getState: () => ({ isAtBottom: true }) }),
    ThreadPrimitive: {
      Root: ({ children, className }: { children: React.ReactNode; className?: string }) =>
        React.createElement('div', { className }, children),
      Viewport: ({ children, className }: { children: React.ReactNode; className?: string }) =>
        React.createElement('div', { className }, children),
      Messages: () => null,
    },
    MessagePrimitive: {
      Root: ({ children, className }: { children: React.ReactNode; className?: string }) =>
        React.createElement('div', { className }, children),
      Parts: () => null,
    },
    ComposerPrimitive: {
      Root: ({ children, className, onSubmit }: { children: React.ReactNode; className?: string; onSubmit?: (e: React.FormEvent) => void }) =>
        React.createElement('form', { className, onSubmit, 'data-testid': 'composer-form' }, children),
      Input: ({ disabled, placeholder, className, onChange, onKeyDown, onBlur }: {
        disabled?: boolean; placeholder?: string; className?: string;
        onChange?: (e: React.ChangeEvent<HTMLTextAreaElement>) => void;
        onKeyDown?: (e: React.KeyboardEvent<HTMLTextAreaElement>) => void;
        onBlur?: () => void;
      }) =>
        React.createElement('textarea', {
          disabled, placeholder, className,
          onChange, onKeyDown, onBlur,
          'data-testid': 'composer-input',
        }),
      // Reproduces the REAL createActionButton + composeEventHandlers
      // contract for completeness (this file's tests don't click the idle
      // Send button, but other suites sharing this component do rely on the
      // same shape — kept consistent with send-button-intercept.test.tsx).
      Send: ({ disabled, children, className, 'data-testid': testId, 'aria-label': ariaLabel, onClick }: {
        disabled?: boolean; children?: React.ReactNode; className?: string;
        'data-testid'?: string; 'aria-label'?: string; onClick?: (e: React.MouseEvent) => void
      }) =>
        React.createElement('button', {
          type: 'button', disabled, className,
          'data-testid': testId ?? 'chat-send',
          'aria-label': ariaLabel,
          onClick: (e: React.MouseEvent) => {
            onClick?.(e)
            if (!e.defaultPrevented) {
              mockComposerSend()
            }
          },
        }, children),
      AddAttachment: ({ disabled, children, className, 'aria-label': ariaLabel }: {
        disabled?: boolean; children?: React.ReactNode; className?: string; 'aria-label'?: string
      }) =>
        React.createElement('button', { type: 'button', disabled, className, 'aria-label': ariaLabel, 'data-testid': 'add-attachment' }, children),
      Attachments: () => null,
    },
    AttachmentPrimitive: {
      Root: ({ children, className }: { children?: React.ReactNode; className?: string }) =>
        React.createElement('div', { className }, children),
      Name: () => null,
      Remove: ({ children, className, 'aria-label': ariaLabel }: { children?: React.ReactNode; className?: string; 'aria-label'?: string }) =>
        React.createElement('button', { type: 'button', className, 'aria-label': ariaLabel }, children),
      Thumb: () => null,
    },
    MessagePartPrimitive: { InProgress: () => null },
    ActionBarPrimitive: {
      Root: ({ children, className }: { children: React.ReactNode; className?: string }) =>
        React.createElement('div', { className }, children),
      Copy: ({ children }: { children: React.ReactNode }) => React.createElement('span', {}, children),
    },
    AuiIf: () => null,
    // `send` is the mechanism under test in this file — see file header.
    useComposerRuntime: vi.fn(() => ({
      getState: () => ({ text: '' }),
      setText: mockSetText,
      addAttachment: vi.fn(),
      subscribe: vi.fn(() => vi.fn()),
      send: mockComposerSend,
    })),
    useMessage: () => ({
      id: 'msg_1',
      role: 'assistant',
      status: { type: 'complete' },
      content: [],
    }),
    makeAssistantToolUI: () => () => null,
  }
})

vi.mock('@tanstack/react-query', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-query')>()
  const mockCommands = [
    { name: 'clear',  label: '/clear',  description: 'Start a new conversation', delivery: 'client', available_while_streaming: false },
    { name: 'cancel', label: '/cancel', description: 'Cancel the current turn',  delivery: 'client', available_while_streaming: true },
  ]
  return {
    ...actual,
    useQuery: (opts: { queryKey: unknown[] }) => {
      const key = opts?.queryKey
      if (Array.isArray(key) && key[0] === 'commands') {
        return { data: mockCommands, isError: false, refetch: vi.fn() }
      }
      if (Array.isArray(key) && key[0] === 'skills') {
        return { data: [], isError: false, refetch: vi.fn() }
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
vi.mock('./SubagentBlock', () => ({ SubagentBlock: () => null }))
vi.mock('./markdown-text', () => ({ MarkdownText: () => null }))
vi.mock('./tools/GenericToolCall', () => ({ GenericToolCall: () => null }))
vi.mock('@/components/shared/IconRenderer', () => ({ IconRenderer: () => null }))
vi.mock('./composer/AgentPicker', () => ({ AgentPicker: () => null }))
vi.mock('./composer/ModelPicker', () => ({ ModelPicker: () => null }))
vi.mock('./composer/TokenCounter', () => ({ TokenCounter: () => null }))

function resetStores() {
  act(() => {
    useChatStore.setState({
      messages: [],
      isStreaming: true,
      isReplaying: false,
      toolCalls: {},
      sessionTokens: 0,
      sessionCost: 0,
    })
    useConnectionStore.setState({
      connection: { send: vi.fn().mockReturnValue(true) } as any,
      isConnected: true,
      connectionError: null,
      reconnectPhase: null,
    })
    useSessionStore.setState({
      activeSessionId: 'sess_mid_turn_ui_test',
      activeAgentId: 'general-assistant',
      activeAgentType: null,
    })
  })
}

beforeEach(() => {
  resetStores()
  mockComposerSend.mockClear()
  mockSetText.mockClear()
  // One test below overrides useComposerRuntime's return value (to control
  // composerRuntime.getState().text for the client-command-interception
  // case) via `.mockReturnValue(...)`, which — unlike `mockReturnValueOnce`
  // — persists across subsequent calls/tests until reset. Restore the
  // default shape before every test so that override can never leak into
  // an unrelated later test (regardless of file run order).
  ;(useComposerRuntime as ReturnType<typeof vi.fn>).mockImplementation(() => ({
    getState: () => ({ text: '' }),
    setText: mockSetText,
    addAttachment: vi.fn(),
    subscribe: vi.fn(() => vi.fn()),
    send: mockComposerSend,
  }))
})

describe('OmnipusComposer — mid-turn steering send (Enter)', () => {
  it('Enter mid-stream with plain text calls composerRuntime.send() (the isRunning bypass) instead of being blocked', () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('composer-input')

    act(() => { fireEvent.change(input, { target: { value: 'steer this turn' } }) })
    act(() => { fireEvent.keyDown(input, { key: 'Enter' }) })

    expect(mockComposerSend).toHaveBeenCalledTimes(1)
  })

  it('Shift+Enter mid-stream does NOT send — still treated as a newline', () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('composer-input')

    act(() => { fireEvent.change(input, { target: { value: 'multi\nline' } }) })
    act(() => { fireEvent.keyDown(input, { key: 'Enter', shiftKey: true }) })

    expect(mockComposerSend).not.toHaveBeenCalled()
  })

  it('resyncs the slash-menu text mirror after a mid-stream send (Fix-4 parity with the idle-state path)', () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('composer-input')

    act(() => { fireEvent.change(input, { target: { value: 'steer this turn' } }) })
    act(() => { fireEvent.keyDown(input, { key: 'Enter' } ) })

    // The mid-stream Send button (rendered only while hasComposerText) must
    // disappear once the mirror is cleared — proves onInputChange('') ran.
    expect(screen.queryByTestId('chat-send-mid-stream')).not.toBeInTheDocument()
  })

  it('a typed client command ("/clear") mid-stream still intercepts locally instead of steering literal text into the turn', async () => {
    // "/clear" is NOT available_while_streaming (see the mocked commands
    // list above), so it never matches the mid-stream-filtered menu —
    // shouldShowSlash is false, routing this Enter through
    // submitMidStreamMessage()'s OWN interceptClientCommand() check rather
    // than the menu's Enter-selects-item branch (covered by the next
    // describe block below).
    const realStartNewSession = useSessionStore.getState().startNewSession
    const startNewSession = vi.fn()
    act(() => { useSessionStore.setState({ startNewSession }) })

    try {
      const { useComposerRuntime } = await import('@assistant-ui/react')
      ;(useComposerRuntime as ReturnType<typeof vi.fn>).mockReturnValue({
        getState: () => ({ text: '/clear' }),
        setText: mockSetText,
        addAttachment: vi.fn(),
        subscribe: vi.fn(() => vi.fn()),
        send: mockComposerSend,
      })

      render(<OmnipusComposer />)
      const input = screen.getByTestId('composer-input')

      act(() => { fireEvent.change(input, { target: { value: '/clear' } }) })
      act(() => { fireEvent.keyDown(input, { key: 'Enter' }) })

      // Intercepted locally — the client command ran...
      expect(startNewSession).toHaveBeenCalledTimes(1)
      // ...and the literal text "/clear" was never steered into the turn.
      expect(mockComposerSend).not.toHaveBeenCalled()
    } finally {
      act(() => { useSessionStore.setState({ startNewSession: realStartNewSession }) })
    }
  })
})

describe('OmnipusComposer — slash-menu Enter precedence mid-stream (menu-open Enter still selects, not "send")', () => {
  it('Enter with the streaming-safe "/cancel" menu open selects the command instead of steering it as text', () => {
    const mockCancelStream = vi.fn()
    act(() => { useChatStore.setState({ cancelStream: mockCancelStream }) })

    render(<OmnipusComposer />)
    const input = screen.getByTestId('composer-input')

    // "/cancel" IS available_while_streaming, so it matches the mid-stream
    // filtered menu — shouldShowSlash is true, and handleKeyDown must defer
    // Enter to the menu's own selection branch instead of treating it as a
    // mid-turn send.
    act(() => { fireEvent.change(input, { target: { value: '/cancel' } }) })
    act(() => { fireEvent.keyDown(input, { key: 'ArrowDown' } ) })
    expect(screen.getByText('/cancel')).toBeInTheDocument()

    act(() => { fireEvent.keyDown(input, { key: 'Enter' }) })

    // Selected via the menu (cancelIfStreaming -> cancelStream), not sent as
    // a steering chat message.
    expect(mockCancelStream).toHaveBeenCalledTimes(1)
    expect(mockComposerSend).not.toHaveBeenCalled()
  })
})

describe('OmnipusComposer — Stop + mid-turn Send button coexist while streaming', () => {
  it('Stop renders and still cancels the turn while streaming', () => {
    const mockCancelStream = vi.fn()
    act(() => { useChatStore.setState({ cancelStream: mockCancelStream } as any) })

    render(<OmnipusComposer />)
    const stopBtn = screen.getByTestId('stop-btn')
    expect(stopBtn).toBeInTheDocument()

    act(() => { fireEvent.click(stopBtn) })
    expect(mockCancelStream).toHaveBeenCalledTimes(1)
  })

  it('the mid-turn Send button is absent when the composer is empty, and appears once text is typed', () => {
    render(<OmnipusComposer />)
    expect(screen.queryByTestId('chat-send-mid-stream')).not.toBeInTheDocument()

    const input = screen.getByTestId('composer-input')
    act(() => { fireEvent.change(input, { target: { value: 'hello' } }) })
    expect(screen.getByTestId('chat-send-mid-stream')).toBeInTheDocument()
    // Stop is still present alongside it — cancel stays one click away.
    expect(screen.getByTestId('stop-btn')).toBeInTheDocument()
  })

  it('clicking the mid-turn Send button calls composerRuntime.send() (the bypass)', () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('composer-input')
    act(() => { fireEvent.change(input, { target: { value: 'steer via click' } }) })

    const sendBtn = screen.getByTestId('chat-send-mid-stream')
    act(() => { fireEvent.click(sendBtn) })

    expect(mockComposerSend).toHaveBeenCalledTimes(1)
  })

  it('the idle Send button (ComposerPrimitive.Send) is not mounted while streaming — Stop occupies its slot', () => {
    render(<OmnipusComposer />)
    // 'chat-send' is the idle-state ComposerPrimitive.Send testid; it must
    // not appear while isStreaming — only 'stop-btn' (and, once there's
    // text, 'chat-send-mid-stream').
    expect(screen.queryByTestId('chat-send')).not.toBeInTheDocument()
  })

  it('the "Sends into the running response" hint appears only once there is text mid-stream', () => {
    render(<OmnipusComposer />)
    expect(screen.queryByTestId('mid-stream-send-hint')).not.toBeInTheDocument()

    const input = screen.getByTestId('composer-input')
    act(() => { fireEvent.change(input, { target: { value: 'hi' } }) })
    expect(screen.getByTestId('mid-stream-send-hint')).toBeInTheDocument()
    expect(screen.getByTestId('mid-stream-send-hint').textContent).toMatch(/running response/i)
  })
})

describe('OmnipusComposer — idle (not streaming) composer is unaffected by the mid-turn changes', () => {
  it('idle Send button is present, Stop and the mid-turn Send button are absent', () => {
    act(() => { useChatStore.setState({ isStreaming: false }) })
    render(<OmnipusComposer />)

    expect(screen.getByTestId('chat-send')).toBeInTheDocument()
    expect(screen.queryByTestId('stop-btn')).not.toBeInTheDocument()
    expect(screen.queryByTestId('chat-send-mid-stream')).not.toBeInTheDocument()
    expect(screen.queryByTestId('mid-stream-send-hint')).not.toBeInTheDocument()
  })

  it('Enter while idle still submits through the normal form path (unchanged)', () => {
    act(() => { useChatStore.setState({ isStreaming: false }) })
    render(<OmnipusComposer />)
    const input = screen.getByTestId('composer-input')
    const form = screen.getByTestId('composer-form') as HTMLFormElement

    const submitSpy = vi.fn((e: Event) => e.preventDefault())
    form.addEventListener('submit', submitSpy)

    act(() => { fireEvent.change(input, { target: { value: 'hello idle' } }) })
    act(() => { fireEvent.keyDown(input, { key: 'Enter' }) })

    // Idle-state Enter is NOT handled by submitMidStreamMessage — it's a
    // native textarea Enter with no form-submit wiring in THIS mock (the
    // real ComposerPrimitive.Input owns that natively); the only contract
    // this test pins is that the mid-stream bypass path was NOT taken.
    expect(mockComposerSend).not.toHaveBeenCalled()
  })
})

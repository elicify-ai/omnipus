// ChatScreen.clear-command.test.tsx — FR-030/031 (U10b): the real composer →
// wire chain for /clear and the retired /new.
//
// Oracles: docs/internal/specs/session-core-spec.md FR-030/FR-031 + the WC-1
// amendment; core's pkg/commands/cmd_clear.go (git 67345b1d7, read-only):
// /clear is DeliveryAgent — the SERVER executes it, so a typed /clear must
// leave the SPA as an ordinary message frame ({type:'message',
// content:'/clear'}) and nothing may run locally; /new is retired — the SPA
// must never send it, must not start a session for it, and must refuse it
// visibly instead of swallowing it.
//
// What is real here: the full ChatScreen (composer interception, the slash
// hook, the chat store's sendMessage) and the real outgoing-frame builder.
// What is mocked: only the edges — the WS connection (a send spy) and
// AssistantUI's runtime internals (the shared test stand-in, with the
// composer runtime's send() forwarding to the REAL store sendMessage, which
// is exactly what the real BaseComposerRuntimeCore.send → adapter.onNew →
// sendMessage chain does).
//
// The tests drive the MID-TURN path (Enter while a turn streams): it is the
// one path that reaches the real send through this mock (ComposerPrimitive
// form submit has no runtime behind it in jsdom), and BDD-09.2 explicitly
// covers "/clear requested while a step is in flight".

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'
import * as React from 'react'
import { useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import type { WsConnection } from '@/lib/ws'
import { MockButton, MockTextarea } from '@/test/assistantUiMock'

if (typeof Element !== 'undefined' && !Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = function () {}
}

// The typed composer text, mirrored by the Input override below — the mock
// runtime reads it exactly where the real runtime reads the composer core.
let composerText = ''
// Captured outgoing WS payloads — the wire edge under assertion.
let sentFrames: unknown[] = []
// The server's command list for the palette (mutable per test).
let serverCommands: Array<Record<string, unknown>> = []

vi.mock('@assistant-ui/react', async () => (await import('@/test/assistantUiMock')).createAssistantUiMock({
  ThreadPrimitive: {
    Viewport: ({ children, className }: { children: React.ReactNode; className?: string }) =>
      React.createElement('div', { className }, children),
  },
  ComposerPrimitive: {
    Root: ({ children, className, onSubmit }: { children: React.ReactNode; className?: string; onSubmit?: (e: React.FormEvent) => void }) =>
      React.createElement('form', { className, onSubmit, 'data-testid': 'composer-form' }, children),
    Input: ({ onChange, onKeyDown, onBlur, disabled, placeholder, className }: {
      onChange?: (e: React.ChangeEvent<HTMLTextAreaElement>) => void
      onKeyDown?: (e: React.KeyboardEvent<HTMLTextAreaElement>) => void
      onBlur?: () => void
      disabled?: boolean
      placeholder?: string
      className?: string
    }) =>
      React.createElement(MockTextarea, {
        disabled,
        placeholder,
        className,
        onChange: (e: React.ChangeEvent<HTMLTextAreaElement>) => {
          composerText = e.target.value
          onChange?.(e)
        },
        onKeyDown,
        onBlur,
        'data-testid': 'composer-input',
      }),
    Send: ({ disabled, children, className, 'data-testid': testId }: {
      disabled?: boolean; children?: React.ReactNode; className?: string; 'data-testid'?: string
    }) =>
      React.createElement(MockButton, {
        type: 'button', disabled, className, 'data-testid': testId ?? 'chat-send', children,
      }),
  },
  useComposerRuntime: () => ({
    getState: () => ({ text: composerText }),
    setText: vi.fn((value: string) => { composerText = value }),
    addAttachment: vi.fn(),
    subscribe: vi.fn(() => vi.fn()),
    // The real chain this stands in for: BaseComposerRuntimeCore.send() →
    // adapter.onNew() → useChatStore.sendMessage(text), then the runtime
    // clears its own text.
    send: () => {
      const text = composerText
      useChatStore.getState().sendMessage(text)
      composerText = ''
    },
  }),
  useMessage: () => ({
    id: 'msg_1',
    role: 'assistant',
    status: { type: 'complete' },
    content: [],
  }),
}))

vi.mock('@tanstack/react-query', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-query')>()
  return {
    ...actual,
    useQuery: (opts: { queryKey: unknown[] }) => {
      const key = opts?.queryKey
      if (Array.isArray(key) && key[0] === 'commands' && key[1] === 'web') {
        return { data: serverCommands, isError: false, isLoading: false, refetch: vi.fn() }
      }
      return { data: [], isError: false, isLoading: false, refetch: vi.fn() }
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
vi.mock('./composer/ModelPicker', () => ({ ModelPicker: () => null }))
vi.mock('./composer/TokenCounter', () => ({ TokenCounter: () => null }))

import { ChatScreen } from './ChatScreen'

const SID = 'sess-clear-command-test'

function resetStores() {
  act(() => {
    sentFrames = []
    composerText = ''
    // A mid-stream turn: the last message is a streaming assistant bubble,
    // which is what routes Enter through the mid-turn send branch.
    useChatStore.setState((s) => ({
      ...s,
      sessionsById: {
        ...s.sessionsById,
        [SID]: {
          ...(s.sessionsById ?? {})[SID],
          messagesById: { 'assistant-live': { id: 'assistant-live', role: 'assistant', content: '', timestamp: new Date().toISOString(), status: 'streaming', isStreaming: true } },
          messageOrder: ['assistant-live'],
          isStreaming: true,
          isReplaying: false,
          replayCompletedForSession: SID,
          toolCalls: {},
          toolCallOrder: [],
          textAtToolCallStart: {},
          sessionTokens: 0,
          sessionCost: 0,
        },
      },
      messages: [],
      messagesById: {},
      isStreaming: true,
      isReplaying: false,
      replayCompletedForSession: SID,
    }))
    useConnectionStore.setState({
      connection: {
        send: (payload: unknown) => { sentFrames.push(payload); return true },
        close: () => {},
      } as unknown as WsConnection,
      isConnected: true,
      connectionError: null,
      reconnectPhase: null,
    })
    useSessionStore.setState({
      activeSessionId: SID,
      activeAgentId: 'general-assistant',
      activeAgentType: null,
    })
  })
}

beforeEach(() => {
  // This base's server: /new retired (absent), /clear not yet listed.
  serverCommands = [
    { name: 'help', label: '/help', description: 'Show available commands', delivery: 'client', available_while_streaming: false },
    { name: 'cancel', label: '/cancel', description: 'Cancel the current turn', delivery: 'client', available_while_streaming: true },
  ]
  resetStores()
  // Force the PlainMessageList fallback (no ResizeObserver in jsdom) so the
  // full ChatScreen's thread renders every row — the same harness every other
  // full-screen ChatScreen.*.test.tsx in this directory uses.
  vi.unstubAllGlobals()
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  ;(globalThis as any).ResizeObserver = undefined
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('ChatScreen — typed /clear and the retired /new (FR-030/031)', () => {
  it('typed "/clear" + Enter leaves the SPA as an ordinary message frame for the server', () => {
    render(<ChatScreen />)
    const input = screen.getByTestId('composer-input')

    act(() => { fireEvent.change(input, { target: { value: '/clear' } }) })
    act(() => { fireEvent.keyDown(input, { key: 'Enter' }) })

    expect(sentFrames).toHaveLength(1)
    expect(sentFrames[0]).toMatchObject({ type: 'message', content: '/clear', session_id: SID })
    // The same session, unchanged — /clear starts no session.
    expect(useSessionStore.getState().activeSessionId).toBe(SID)
  })

  it('typed "/new" + Enter is refused visibly, sends NOTHING, and starts no session', () => {
    render(<ChatScreen />)
    const input = screen.getByTestId('composer-input')

    act(() => { fireEvent.change(input, { target: { value: '/new' } }) })
    act(() => { fireEvent.keyDown(input, { key: 'Enter' }) })

    // Nothing reached the wire.
    expect(sentFrames).toHaveLength(0)
    // No new session was minted or attached.
    expect(useSessionStore.getState().activeSessionId).toBe(SID)
    // The refusal is VISIBLE in the thread: it names the retirement and both
    // replacements (the agent row's New chat action, and /clear).
    const thread = document.body.textContent ?? ''
    expect(thread).toContain('/new')
    expect(thread).toContain('New chat')
    expect(thread).toContain('/clear')
    expect(thread).toContain('no longer exists')
  })

  it('the palette lists /clear only when the server command list contains it', () => {
    // IDLE state: mid-turn, the palette deliberately narrows to
    // available_while_streaming commands only, and /clear (like most server
    // commands) is not one — the streaming-filter behavior is pinned
    // elsewhere. This test is about which commands are listed at all.
    act(() => {
      useChatStore.setState((s) => ({
        ...s,
        isStreaming: false,
        sessionsById: {
          ...s.sessionsById,
          [SID]: { ...(s.sessionsById ?? {})[SID], isStreaming: false },
        },
      }))
    })
    const { rerender } = render(<ChatScreen />)
    const input = screen.getByTestId('composer-input')

    // This base's server (no /clear in the list): no /clear entry.
    act(() => { fireEvent.change(input, { target: { value: '/cl' } }) })
    expect(screen.queryByText('/clear')).not.toBeInTheDocument()

    // A U10b server listing /clear: the palette shows exactly that row.
    serverCommands = [
      ...serverCommands,
      { name: 'clear', label: '/clear', description: "Clear this chat's context; the transcript is kept", usage: '/clear', delivery: 'agent', available_while_streaming: false },
    ]
    act(() => { fireEvent.change(input, { target: { value: '/clear' } }) })
    rerender(<ChatScreen />)
    expect(screen.getByText('/clear')).toBeInTheDocument()
  })
})

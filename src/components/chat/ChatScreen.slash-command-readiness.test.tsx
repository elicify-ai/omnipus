// Slash-command readiness — a command typed before GET /api/v1/commands has
// resolved must never be dispatched to the backend as a chat message.
//
// Observed for real (CI trace): the fetch was issued at t=3424ms and "/new"
// was submitted at t=3660ms, 236ms later. `useSlashMenu`'s `allCommands` still
// held only the two synthetic client-only entries, so `interceptClientCommand`
// missed, returned false, and the literal "/new" was dispatched — the gateway
// then answered it as a SERVER-side command ("Chat history cleared!", from
// pkg/commands/cmd_clear.go) and persisted "/new" into the transcript as a
// user message. Typing a command a fraction of a second too early silently
// took a different code path than the same keystrokes a moment later.
//
// These tests drive the REAL submit path (ComposerPrimitive.Root's onSubmit,
// same as pressing Enter) and assert the two user-visible outcomes:
//   1. nothing is dispatched while the list is in flight (the submit event is
//      defaultPrevented, so assistant-ui never calls composer.send()), and
//      once the list lands "/new" runs as the CLIENT command it always was;
//   2. input that turns out NOT to be a command is still delivered — the gate
//      holds submissions, it never eats them.

import { describe, it, expect, vi, beforeEach } from 'vitest'
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
      Send: ({ disabled, children, className, 'data-testid': testId, 'aria-label': ariaLabel }: {
        disabled?: boolean; children?: React.ReactNode; className?: string;
        'data-testid'?: string; 'aria-label'?: string;
      }) =>
        React.createElement('button', {
          type: 'button', disabled, className,
          'data-testid': testId ?? 'chat-send',
          'aria-label': ariaLabel,
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
    useComposerRuntime: vi.fn(() => ({
      getState: () => ({ text: '' }),
      setText: vi.fn(),
      addAttachment: vi.fn(),
      subscribe: vi.fn(() => vi.fn()),
      send: vi.fn(),
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

// Flipped by each test to simulate `fetchCommands('web')` still being in
// flight (React Query `isLoading`: first fetch, no data yet) and then landing.
let commandsStillLoading = true

vi.mock('@tanstack/react-query', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-query')>()
  const mockCommands = [
    { name: 'new',    label: '/new',    description: 'Start a new conversation', delivery: 'client', available_while_streaming: false, aliases: ['clear'] },
    { name: 'help',   label: '/help',   description: 'Show available commands',  delivery: 'client', available_while_streaming: false },
    { name: 'cancel', label: '/cancel', description: 'Cancel the current turn',  delivery: 'client', available_while_streaming: true  },
  ]
  return {
    ...actual,
    useQuery: (opts: { queryKey: unknown[] }) => {
      const key = opts?.queryKey
      if (Array.isArray(key) && key[0] === 'commands' && key[1] === 'web') {
        return commandsStillLoading
          ? { data: undefined, isError: false, isLoading: true, refetch: vi.fn() }
          : { data: mockCommands, isError: false, isLoading: false, refetch: vi.fn() }
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
vi.mock('./SubagentBlock', () => ({ SubagentBlock: () => null }))
vi.mock('./markdown-text', () => ({ MarkdownText: () => null }))
vi.mock('./tools/GenericToolCall', () => ({ GenericToolCall: () => null }))
vi.mock('@/components/shared/IconRenderer', () => ({ IconRenderer: () => null }))
vi.mock('./composer/AgentPicker', () => ({ AgentPicker: () => null }))
vi.mock('./composer/ModelPicker', () => ({ ModelPicker: () => null }))
vi.mock('./composer/TokenCounter', () => ({ TokenCounter: () => null }))

beforeEach(() => {
  commandsStillLoading = true
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
    useSessionStore.setState({
      activeSessionId: 'sess_readiness_test',
      activeAgentId: 'mia',
      activeAgentType: null,
      agentSelectionSource: 'auto',
      agentSelectionWorkspaceId: null,
    })
  })
})

/** Installs a composer runtime whose text is `text`, and returns its spies. */
async function mockRuntimeWithText(text: string) {
  const setText = vi.fn()
  const send = vi.fn()
  const { useComposerRuntime } = await import('@assistant-ui/react')
  ;(useComposerRuntime as ReturnType<typeof vi.fn>).mockReturnValue({
    getState: () => ({ text }),
    setText,
    addAttachment: vi.fn(),
    subscribe: vi.fn(() => vi.fn()),
    send,
  })
  return { setText, send }
}

describe('slash command typed before the command list resolves', () => {
  it('"/new" is never dispatched as a chat message, and runs as the client command once the list lands', async () => {
    const { setText, send } = await mockRuntimeWithText('/new')

    const { rerender } = render(<OmnipusComposer />)
    const form = screen.getByTestId('composer-form')
    const input = screen.getByTestId('composer-input')
    act(() => { fireEvent.change(input, { target: { value: '/new' } }) })

    // Enter, while GET /api/v1/commands is still in flight.
    let dispatched = true
    act(() => { dispatched = fireEvent.submit(form) })

    // fireEvent returns false when the handler called preventDefault(), i.e.
    // assistant-ui never got to call composer.send() — "/new" did NOT go out
    // as a chat message. This is the exact assertion that fails without the
    // readiness gate.
    expect(dispatched).toBe(false)
    expect(send).not.toHaveBeenCalled()
    // The session is still the one we started on — nothing has run yet.
    expect(useSessionStore.getState().activeSessionId).toBe('sess_readiness_test')

    // The command list arrives.
    commandsStillLoading = false
    act(() => { rerender(<OmnipusComposer />) })

    // "/new" now does what the user asked: starts a new conversation
    // (startNewSession clears activeSessionId) and clears the composer...
    expect(useSessionStore.getState().activeSessionId).toBeNull()
    expect(setText).toHaveBeenCalledWith('')
    // ...and it was never sent to the backend at any point.
    expect(send).not.toHaveBeenCalled()
  })

  it('a non-command "/zzz hi" is held and then delivered verbatim — the gate never eats input', async () => {
    const { setText, send } = await mockRuntimeWithText('/zzz hi')

    const { rerender } = render(<OmnipusComposer />)
    const form = screen.getByTestId('composer-form')
    const input = screen.getByTestId('composer-input')
    act(() => { fireEvent.change(input, { target: { value: '/zzz hi' } }) })

    act(() => { fireEvent.submit(form) })
    expect(send).not.toHaveBeenCalled()

    commandsStillLoading = false
    act(() => { rerender(<OmnipusComposer />) })

    // Delivered exactly once, unmodified (never cleared before the send), and
    // no client command ran.
    expect(send).toHaveBeenCalledTimes(1)
    expect(setText).not.toHaveBeenCalledWith('')
    expect(useSessionStore.getState().activeSessionId).toBe('sess_readiness_test')
  })

  it('plain text submitted in the same window is dispatched immediately — only "/" input is held', async () => {
    const { send } = await mockRuntimeWithText('hello there')

    render(<OmnipusComposer />)
    const form = screen.getByTestId('composer-form')
    const input = screen.getByTestId('composer-input')
    act(() => { fireEvent.change(input, { target: { value: 'hello there' } }) })

    let dispatched = false
    act(() => { dispatched = fireEvent.submit(form) })

    // Not intercepted — the form submit proceeds to assistant-ui as normal.
    expect(dispatched).toBe(true)
    expect(send).not.toHaveBeenCalled()
  })

  it('once the list has loaded, "/new" is handled synchronously (no hold at all)', async () => {
    commandsStillLoading = false
    const { setText, send } = await mockRuntimeWithText('/new')

    render(<OmnipusComposer />)
    const form = screen.getByTestId('composer-form')
    const input = screen.getByTestId('composer-input')
    act(() => { fireEvent.change(input, { target: { value: '/new' } }) })

    let dispatched = true
    act(() => { dispatched = fireEvent.submit(form) })

    expect(dispatched).toBe(false)
    expect(useSessionStore.getState().activeSessionId).toBeNull()
    expect(setText).toHaveBeenCalledWith('')
    expect(send).not.toHaveBeenCalled()
  })
})

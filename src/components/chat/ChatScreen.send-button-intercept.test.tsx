// bugfixes3 deferred item 1 — Send-BUTTON click must run client-command
// interception. Pre-existing FR-009 bug: ComposerPrimitive.Send (assistant-ui's
// createActionButton) renders `type="button"` with
// `onClick: composeEventHandlers(primitiveProps.onClick, callback)`, where
// `callback` invokes `useComposerSend`'s send() DIRECTLY — a mouse click on
// Send never fires ComposerPrimitive.Root's onSubmit, so typing "/new" and
// clicking Send sent the literal text "/new" to the backend instead of
// running the client command locally. Fixed by passing an `onClick` to
// ComposerPrimitive.Send in ChatScreen.tsx that calls
// `slashMenu.interceptClientCommand()` and, if it handled the text,
// `e.preventDefault()` — verified against
// node_modules/@assistant-ui/react/dist/utils/createActionButton.js +
// node_modules/@radix-ui/primitive/dist/index.js that composeEventHandlers
// runs the caller's onClick FIRST and only invokes the internal send
// callback if that onClick did NOT call preventDefault
// (checkForDefaultPrevented defaults to true).
//
// This file's mock `ComposerPrimitive.Send` deliberately reproduces that
// exact composeEventHandlers contract (see `mockComposerSend` below) instead
// of just forwarding onClick — a mock that merely forwarded onClick without
// gating on `defaultPrevented` would not actually prove the interception
// suppresses the send; it would just prove ChatScreen.tsx called SOME onClick.

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

// Module-level spy standing in for assistant-ui's real, internal
// `composer.send()` callback (the `callback` argument to composeEventHandlers
// inside createActionButton) — reset in beforeEach. Asserting on this proves
// whether the REAL send path was reached, independent of whatever
// ChatScreen.tsx's own onClick prop did.
const mockComposerSend = vi.fn()

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
      // Reproduces assistant-ui's real createActionButton +
      // composeEventHandlers(primitiveProps.onClick, callback) contract
      // (checkForDefaultPrevented=true): the caller's `onClick` (ChatScreen.
      // tsx's interception) runs FIRST; `mockComposerSend` (standing in for
      // the real internal `composer.send()`) only runs afterward if that
      // onClick did NOT call `e.preventDefault()`.
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
    useComposerRuntime: vi.fn(() => ({
      getState: () => ({ text: '' }),
      setText: vi.fn(),
      addAttachment: vi.fn(),
      subscribe: vi.fn(() => vi.fn()),
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
    { name: 'clear',   label: '/clear',   description: 'Start a new conversation',   delivery: 'client', available_while_streaming: false },
    { name: 'help',    label: '/help',    description: 'Show available commands',     delivery: 'client', available_while_streaming: false },
    { name: 'model',   label: '/model',   description: 'Change the chat model',       delivery: 'client', available_while_streaming: false },
    { name: 'agents',  label: '/agents',  description: 'Open agent selector',         delivery: 'client', available_while_streaming: false },
    { name: 'cancel',  label: '/cancel',  description: 'Cancel the current turn',     delivery: 'client', available_while_streaming: true  },
  ]
  const mockSkills = [
    { id: 'web-research',  name: 'Web Research',  version: '1.0', description: 'Web search and extraction', verified: true,  status: 'active' },
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

// importOriginal: useSlashMenu calls useChatAgents unconditionally (the "@"
// mention menu), which needs real `fetchWorkspaces`/`workspacesQueryKeys`/
// `isWorker` even though this file doesn't exercise mentions.
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
      isStreaming: false,
      isReplaying: false,
      toolCalls: {},
      sessionTokens: 0,
      sessionCost: 0,
    })
    useConnectionStore.setState({
      connection: null,
      isConnected: true,
      connectionError: null,
    })
    useSessionStore.setState({
      activeSessionId: 'sess_send_intercept_test',
      activeAgentId: 'general-assistant',
      activeAgentType: null,
    })
  })
}

beforeEach(() => {
  resetStores()
  mockComposerSend.mockClear()
})

describe('Send-button click — client-command interception (bugfixes3 deferred item 1)', () => {
  it('typing "/clear" (a client command) and clicking Send runs the command locally — nothing is sent', async () => {
    const realStartNewSession = useSessionStore.getState().startNewSession
    const startNewSession = vi.fn()
    act(() => { useSessionStore.setState({ startNewSession }) })

    // interceptClientCommand reads `composerRuntime.getState().text` FIRST
    // (falling back to the hook's own inputValue mirror only when that's
    // nullish) — mirrors ChatScreen.agents-command.test.tsx's own
    // send-path-interception test, which does the same for the Enter path.
    const { useComposerRuntime } = await import('@assistant-ui/react')
    ;(useComposerRuntime as ReturnType<typeof vi.fn>).mockReturnValue({
      getState: () => ({ text: '/clear' }),
      setText: vi.fn(),
      addAttachment: vi.fn(),
      subscribe: vi.fn(() => vi.fn()),
    })

    try {
      render(<OmnipusComposer />)
      const input = screen.getByTestId('composer-input')
      const sendButton = screen.getByTestId('chat-send')

      act(() => { fireEvent.change(input, { target: { value: '/clear' } }) })
      act(() => { fireEvent.click(sendButton) })

      // The client command ran locally (runClientCommand's 'clear' branch
      // calls startNewSession — see useSlashMenu.ts).
      expect(startNewSession).toHaveBeenCalledTimes(1)
      // The internal composer.send() callback must NOT have run — the
      // literal text "/clear" was never dispatched as a chat message.
      expect(mockComposerSend).not.toHaveBeenCalled()
    } finally {
      act(() => { useSessionStore.setState({ startNewSession: realStartNewSession }) })
    }
  })

  it('a normal (non-command) message + clicking Send still sends — the interception does not swallow real messages', async () => {
    const { useComposerRuntime } = await import('@assistant-ui/react')
    ;(useComposerRuntime as ReturnType<typeof vi.fn>).mockReturnValue({
      getState: () => ({ text: 'hello, how are you?' }),
      setText: vi.fn(),
      addAttachment: vi.fn(),
      subscribe: vi.fn(() => vi.fn()),
    })

    render(<OmnipusComposer />)
    const input = screen.getByTestId('composer-input')
    const sendButton = screen.getByTestId('chat-send')

    act(() => { fireEvent.change(input, { target: { value: 'hello, how are you?' } }) })
    act(() => { fireEvent.click(sendButton) })

    // Not a client command — interceptClientCommand returns false, no
    // preventDefault, so the internal send callback runs normally.
    expect(mockComposerSend).toHaveBeenCalledTimes(1)
  })

  it('typing "/help" (also a client command) and clicking Send appends a local system message instead of sending', async () => {
    // /help calls appendMessage (useChatStore's own action — ChatScreen.tsx
    // wires `useChatStore((s) => s.appendMessage)` straight into
    // useSlashMenu) — verify via the resulting store message list rather
    // than a spy, since it's a real store action, not an injected mock.
    const { useComposerRuntime } = await import('@assistant-ui/react')
    ;(useComposerRuntime as ReturnType<typeof vi.fn>).mockReturnValue({
      getState: () => ({ text: '/help' }),
      setText: vi.fn(),
      addAttachment: vi.fn(),
      subscribe: vi.fn(() => vi.fn()),
    })

    render(<OmnipusComposer />)
    const input = screen.getByTestId('composer-input')
    const sendButton = screen.getByTestId('chat-send')

    act(() => { fireEvent.change(input, { target: { value: '/help' } }) })
    act(() => { fireEvent.click(sendButton) })

    expect(mockComposerSend).not.toHaveBeenCalled()
    const messages = useChatStore.getState().messages
    expect(messages).toHaveLength(1)
    expect(messages[0].role).toBe('system')
    expect(messages[0].content).toContain('/clear')
  })
})

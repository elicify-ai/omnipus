// "@" agent-mention menu — component-level coverage (real DOM, real
// OmnipusComposer render tree). Restored (ported from commit 103c5fd0 /
// release/v0.1.1) by founder decision 2026-10-11 Q1 B, which reverses the
// FR-007 cutover for addressing only. Complements the hook-level suite in
// src/hooks/useSlashMenu.test.ts by proving the same behavior reaches the
// rendered DOM: the "Agents" section header, `agent-mention-item` rows, the
// highlight marker, and the selection side effects wired through
// OmnipusComposer.
//
// THE ONE BEHAVIOUR CHANGE vs the old menu (FR-029 — no in-chat handover):
// selecting an agent inserts "@<Agent name> " into the composer and marks
// that agent as the message's recipient. It must NOT switch the active agent
// or session. The recipient itself is derived from the leading "@<Name>"
// prefix at SEND time (src/lib/recipientAddress.ts); the store/wire side is
// covered by src/store/chat.recipient-send.test.ts.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import * as React from 'react'
import { act } from 'react'
import { useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { queryClient } from '@/lib/queryClient'
import { makeAgent } from '@/test/factories'

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

import { MockButton, MockTextarea } from '@/test/assistantUiMock'
vi.mock('@assistant-ui/react', async () => (await import('@/test/assistantUiMock')).createAssistantUiMock({
  ThreadPrimitive: {
    Viewport: ({ children, className }: { children: React.ReactNode; className?: string }) =>
              React.createElement('div', { className }, children),
  },
  ComposerPrimitive: {
    Root: ({ children, className, onSubmit }: { children: React.ReactNode; className?: string; onSubmit?: (e: React.FormEvent) => void }) =>
              React.createElement('form', { className, onSubmit, 'data-testid': 'composer-form' }, children),
    Input: ({ disabled, placeholder, className, onChange, onKeyDown, onBlur, 'data-testid': testId }: {
              disabled?: boolean; placeholder?: string; className?: string;
              onChange?: (e: React.ChangeEvent<HTMLTextAreaElement>) => void;
              onKeyDown?: (e: React.KeyboardEvent<HTMLTextAreaElement>) => void;
              onBlur?: () => void; 'data-testid'?: string;
            }) =>
              React.createElement(MockTextarea, {
                disabled, placeholder, className,
                onChange, onKeyDown, onBlur,
                'data-testid': testId ?? 'chat-input',}),
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
}))

// Command/skill fixtures — needed for the "/" regression guard proving the
// two menu modes never mix. Mirrors ChatScreen.partitioned-menu.test.tsx's
// fixtures.
const mockCommands = [
  { name: 'clear',  label: '/clear',  description: 'Start a new conversation', delivery: 'client', available_while_streaming: false },
  { name: 'help',   label: '/help',   description: 'Show available commands',  delivery: 'client', available_while_streaming: false },
  { name: 'cancel', label: '/cancel', description: 'Cancel the current turn',  delivery: 'client', available_while_streaming: true },
]
const mockSkills = [
  { id: 'web-research', name: 'Web Research', version: '1.0', description: 'Web search and extraction', verified: true, status: 'active' },
  { id: 'code-review',  name: 'Code Review',  version: '1.0', description: 'Reviews code quality',      verified: true, status: 'active' },
]

// "@" mention menu source data — three distinct-prefix scoped agents
// (Mia/Jim/Ava) plus two agents that must NEVER render as mention rows:
// a worker (Subagent — isWorker() exclusion) and a draft-status agent (not
// active/idle). "Max Worker" deliberately also starts with "m" so the "@m"
// filter test doubles as proof that worker exclusion applies BEFORE the
// prefix filter.
const mockAgents = [
  makeAgent({ id: 'mia', name: 'Mia', type: 'core', status: 'active', color: '#6E8BA8', description: 'Assistant' }),
  makeAgent({ id: 'jim', name: 'Jim', type: 'core', status: 'idle', description: 'Orchestrator' }),
  makeAgent({ id: 'ava', name: 'Ava', type: 'Main', status: 'active', description: 'Builder' }),
  makeAgent({ id: 'max', name: 'Max Worker', type: 'Subagent', status: 'active', description: 'Labour agent' }),
  makeAgent({ id: 'ray', name: 'Ray', type: 'Main', status: 'draft', description: 'Scout (draft)' }),
]

vi.mock('@tanstack/react-query', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-query')>()
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
      if (Array.isArray(key) && key[0] === 'agents') {
        return { data: mockAgents, isError: false, refetch: vi.fn() }
      }
      // 'workspaces' (and anything else) — no active workspace is set in
      // these tests, so core_team scoping never engages; that scoping path
      // is covered separately (useChatAgents.test.ts).
      return { data: queryDataWithEmptyHistory(key), isError: false, refetch: vi.fn() }
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

// importOriginal: the mention menu calls useChatAgents, which needs the REAL
// `isWorker` (this file's worker-exclusion assertions depend on it) plus
// `fetchWorkspaces`/`workspacesQueryKeys` to exist so the module loads —
// useQuery is fully mocked above and never actually invokes these queryFns.
import { emptyHistoryPage, queryDataWithEmptyHistory, type HistoryReadPort } from '../../../tests/chat-history-paging/legacy-fixtures'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([]),
    fetchSessionMessages: vi.fn<HistoryReadPort>(async (sessionId) => emptyHistoryPage(sessionId)),
    fetchCommands: vi.fn().mockResolvedValue([]),
    fetchSkills: vi.fn().mockResolvedValue([]),
    fetchProviders: vi.fn().mockResolvedValue([]),
    fetchWorkspaces: vi.fn().mockResolvedValue([]),
    uploadFiles: vi.fn(),
  }
})

vi.mock('@/assets/logo/omnipus-avatar.svg?url', () => ({ default: 'omnipus-avatar.svg' }))
vi.mock('./RateLimitIndicator', () => ({ RateLimitIndicator: () => null }))
vi.mock('./markdown-text', () => ({ MarkdownText: () => null }))
vi.mock('./tools/GenericToolCall', () => ({ GenericToolCall: () => null }))
// The mention menu is independent of the model/token sub-components (same
// rationale as the sibling "/" menu test files) — stub them to null so their
// own workspaces/providers query plumbing doesn't need mocking here.
vi.mock('./composer/ModelPicker', () => ({ ModelPicker: () => null }))
vi.mock('./composer/TokenCounter', () => ({ TokenCounter: () => null }))
vi.mock('./composer/AutoApprovePicker', () => ({ AutoApprovePicker: () => null }))

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
      activeSessionId: 'sess_mention_test',
      // Deliberately not one of mockAgents' ids: no row is the chat's own
      // agent, so the self-exclusion never hides a fixture row in these
      // tests (the self-exclusion itself is covered in useSlashMenu.test.ts).
      activeAgentId: 'general-assistant',
      activeAgentType: null,
      attachedSessionType: null,
      attachedTaskTitle: null,
    })
    useUiStore.setState({ toasts: [] })
  })
}

beforeEach(resetStores)

// K.4 (bugfixes3 sign-off): tests that override `useComposerRuntime`'s
// return value via `.mockReturnValue(...)` leak that override for the rest
// of THIS FILE's run (vite.config.ts sets no resetMocks). Restore the base
// factory's shape after EVERY test.
afterEach(async () => {
  const { useComposerRuntime } = await import('@assistant-ui/react')
  ;(useComposerRuntime as ReturnType<typeof vi.fn>).mockReturnValue({
    getState: () => ({ text: '' }),
    setText: vi.fn(),
    addAttachment: vi.fn(),
    subscribe: vi.fn(() => vi.fn()),
  })
})

describe('"@" agent-mention menu — opens and lists scoped agents', () => {
  it('typing "@" renders the slash-menu container with an "Agents" section header and one row per scoped agent', () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('chat-input')

    act(() => { fireEvent.change(input, { target: { value: '@' } }) })

    expect(screen.getByTestId('slash-menu')).toBeInTheDocument()
    expect(screen.getByText('Agents')).toBeInTheDocument()
    // Exactly the 3 scoped chat agents (mia/jim/ava) — worker (max) and
    // draft-status (ray) are excluded, proven separately below.
    expect(screen.getAllByTestId('agent-mention-item')).toHaveLength(3)
    expect(screen.getByText('@Mia')).toBeInTheDocument()
    expect(screen.getByText('@Jim')).toBeInTheDocument()
    expect(screen.getByText('@Ava')).toBeInTheDocument()
  })

  it('each agent row renders the agent\'s AgentMark (no bare initial dot)', () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('chat-input')

    act(() => { fireEvent.change(input, { target: { value: '@' } }) })

    const miaRow = screen.getByText('@Mia').closest('button')
    expect(miaRow).not.toBeNull()
    // AgentMark renders the catalogued AgentIcon; the row carries it in front
    // of the label. The old menu's initial-dot markup is gone.
    expect(miaRow!.querySelector('img, svg')).not.toBeNull()
  })
})

describe('"@" agent-mention menu — filtering', () => {
  it('typing "@m" filters to agents whose NAME prefix- or substring-matches "m" (case-insensitive)', () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('chat-input')

    act(() => { fireEvent.change(input, { target: { value: '@m' } }) })

    // Prefix-then-substring ranking: "Mia" prefix-matches "m"; "Jim" also
    // matches via the substring rank, ranked after the prefix match.
    const rows = screen.getAllByTestId('agent-mention-item')
    expect(rows).toHaveLength(2)
    expect(screen.getByText('@Mia')).toBeInTheDocument()
    expect(screen.getByText('@Jim')).toBeInTheDocument()
    expect(screen.queryByText('@Ava')).not.toBeInTheDocument()
    // Max Worker also starts with "m" but must stay excluded — worker
    // exclusion applies before the prefix/substring filter runs.
    expect(screen.queryByText('Max Worker')).not.toBeInTheDocument()
  })

  it('typing "@zzz" (no match) hides the menu entirely', () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('chat-input')

    act(() => { fireEvent.change(input, { target: { value: '@zzz' } }) })

    expect(screen.queryByTestId('slash-menu')).not.toBeInTheDocument()
    expect(screen.queryAllByTestId('agent-mention-item')).toHaveLength(0)
  })
})

describe('"@" agent-mention menu — selection (FR-029: addresses the message, never the chat)', () => {
  it('Enter on the highlighted row inserts "@<Name> " into the composer and does NOT switch the active agent or session', async () => {
    const mockSetText = vi.fn()
    const { useComposerRuntime } = await import('@assistant-ui/react')
    ;(useComposerRuntime as ReturnType<typeof vi.fn>).mockReturnValue({
      getState: () => ({ text: '@m' }),
      setText: mockSetText,
      addAttachment: vi.fn(),
      subscribe: vi.fn(() => vi.fn()),
    })

    render(<OmnipusComposer />)
    const input = screen.getByTestId('chat-input')

    act(() => { fireEvent.change(input, { target: { value: '@m' } }) })
    // Only "Mia" matches — highlight defaults to index 0.
    act(() => { fireEvent.keyDown(input, { key: 'Enter' }) })

    expect(mockSetText).toHaveBeenCalledWith('@Mia ')
    // FR-029: the active agent, its type and the session are all untouched.
    expect(useSessionStore.getState().activeAgentId).toBe('general-assistant')
    expect(useSessionStore.getState().activeAgentType).toBeNull()
    expect(useSessionStore.getState().activeSessionId).toBe('sess_mention_test')
    expect(screen.queryByTestId('slash-menu')).not.toBeInTheDocument()
    // The composer was NOT cleared by the selection (the runtime now carries
    // the address prefix; the mock textarea does not re-render runtime text,
    // so the assertion is on the runtime calls, not the DOM value).
    expect(mockSetText).toHaveBeenLastCalledWith('@Mia ')
    expect(mockSetText).not.toHaveBeenCalledWith('')
  })

  it('mousedown on an agent row inserts the same "@<Name> " prefix', async () => {
    const mockSetText = vi.fn()
    const { useComposerRuntime } = await import('@assistant-ui/react')
    ;(useComposerRuntime as ReturnType<typeof vi.fn>).mockReturnValue({
      getState: () => ({ text: '@' }),
      setText: mockSetText,
      addAttachment: vi.fn(),
      subscribe: vi.fn(() => vi.fn()),
    })

    render(<OmnipusComposer />)
    const input = screen.getByTestId('chat-input')

    act(() => { fireEvent.change(input, { target: { value: '@' } }) })
    const jimRow = screen.getByText('@Jim').closest('button')
    expect(jimRow).not.toBeNull()

    act(() => { fireEvent.mouseDown(jimRow!) })

    expect(mockSetText).toHaveBeenCalledWith('@Jim ')
    expect(useSessionStore.getState().activeAgentId).toBe('general-assistant')
    expect(useSessionStore.getState().activeSessionId).toBe('sess_mention_test')
    expect(screen.queryByTestId('slash-menu')).not.toBeInTheDocument()
  })

  it('selection announces the addressing in the sr-only live region', () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('chat-input')

    expect(screen.getByTestId('agent-mention-announcement').textContent).toBe('')

    act(() => { fireEvent.change(input, { target: { value: '@m' } }) })
    act(() => { fireEvent.keyDown(input, { key: 'Enter' }) })

    expect(screen.getByTestId('agent-mention-announcement').textContent).toBe('Message addressed to Mia')
  })
})

describe('"@" agent-mention menu — keyboard highlight and Escape', () => {
  it('ArrowDown moves the highlight from the first row to the second', () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('chat-input')

    act(() => { fireEvent.change(input, { target: { value: '@' } }) })
    const rowsBefore = screen.getAllByTestId('agent-mention-item')
    expect(rowsBefore).toHaveLength(3)
    expect(rowsBefore[0]).toHaveAttribute('data-highlighted', 'true')
    expect(rowsBefore[1]).not.toHaveAttribute('data-highlighted')

    act(() => { fireEvent.keyDown(input, { key: 'ArrowDown' }) })

    const rowsAfter = screen.getAllByTestId('agent-mention-item')
    expect(rowsAfter[0]).not.toHaveAttribute('data-highlighted')
    expect(rowsAfter[1]).toHaveAttribute('data-highlighted', 'true')
  })

  it('menu open + streaming: first Escape closes the menu only (no cancel); second Escape cancels', () => {
    const cancelStream = vi.fn()
    const realCancelStream = useChatStore.getState().cancelStream
    act(() => { useChatStore.setState({ isStreaming: true, cancelStream }) })

    try {
      render(<OmnipusComposer />)
      const input = screen.getByTestId('chat-input')

      act(() => { fireEvent.change(input, { target: { value: '@' } }) })
      expect(screen.getByTestId('slash-menu')).toBeInTheDocument()

      act(() => { fireEvent.keyDown(input, { key: 'Escape' }) })
      expect(screen.queryByTestId('slash-menu')).not.toBeInTheDocument()
      expect(cancelStream).not.toHaveBeenCalled()

      act(() => { fireEvent.keyDown(input, { key: 'Escape' }) })
      expect(cancelStream).toHaveBeenCalledTimes(1)
    } finally {
      act(() => { useChatStore.setState({ cancelStream: realCancelStream }) })
    }
  })
})

describe('"@" agent-mention menu — leading-position gate', () => {
  it('"hello @x" (non-leading "@") never opens the mention menu', () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('chat-input')

    act(() => { fireEvent.change(input, { target: { value: 'hello @x' } }) })

    expect(screen.queryByTestId('slash-menu')).not.toBeInTheDocument()
    expect(screen.queryByText('Agents')).not.toBeInTheDocument()
    expect(screen.queryAllByTestId('agent-mention-item')).toHaveLength(0)
  })
})

describe('"@" agent-mention menu — scoping exclusions', () => {
  it('excludes worker agents (type Subagent) and non-active/idle agents from the menu', () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('chat-input')

    act(() => { fireEvent.change(input, { target: { value: '@' } }) })

    expect(screen.getAllByTestId('agent-mention-item')).toHaveLength(3)
    expect(screen.queryByText('Max Worker')).not.toBeInTheDocument()
    expect(screen.queryByText('@Max Worker')).not.toBeInTheDocument()
    expect(screen.queryByText('Ray')).not.toBeInTheDocument()
    expect(screen.queryByText('@Ray')).not.toBeInTheDocument()
  })
})

describe('"@" agent-mention menu — regression guard: "/" and "@" never mix', () => {
  it('input "/" still shows Commands/Skills sections and zero agent items', () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('chat-input')

    act(() => { fireEvent.change(input, { target: { value: '/' } }) })

    expect(screen.getByText('Commands')).toBeInTheDocument()
    expect(screen.getByText('Skills')).toBeInTheDocument()
    expect(screen.getByText('/clear')).toBeInTheDocument()
    expect(screen.getByText('/web-research')).toBeInTheDocument()
    expect(screen.queryByText('Agents')).not.toBeInTheDocument()
    expect(screen.queryAllByTestId('agent-mention-item')).toHaveLength(0)
    expect(screen.queryByText('@Mia')).not.toBeInTheDocument()
  })
})

describe('"@" addressing — blocked send without an existing chat (server rule)', () => {
  // The server refuses a recipient that arrives without session_id
  // (pkg/gateway/ws_recipient.go::refuseRecipient — "a request to another
  // agent needs an existing chat"). The composer therefore blocks the send
  // VISIBLY and keeps the text, the same way it blocks other sends
  // (the intercept pattern), instead of letting the server refuse a frame
  // that would drop both the message and the addressing.
  it('submitting an @-addressed message in a brand-new chat (no session) preventDefaults, keeps the text, and warns', async () => {
    // The recipient derivation reads the REAL react-query cache and the REAL
    // workspaces store (not this file's useQuery mock) — seed both so the
    // block has a candidate to resolve against.
    queryClient.setQueryData(['agents'], mockAgents)
    act(() => {
      useWorkspacesStore.setState({ activeWorkspaceId: 'ws_mention_test' })
      useSessionStore.setState({ activeSessionId: null })
    })
    const sendSpy = vi.fn().mockReturnValue(true)
    act(() => {
      useConnectionStore.setState({
        connection: { send: sendSpy, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as never,
        isConnected: true,
      })
    })
    const { useComposerRuntime } = await import('@assistant-ui/react')
    ;(useComposerRuntime as ReturnType<typeof vi.fn>).mockReturnValue({
      getState: () => ({ text: '@Mia review this' }),
      setText: vi.fn(),
      addAttachment: vi.fn(),
      subscribe: vi.fn(() => vi.fn()),
    })

    render(<OmnipusComposer />)
    const input = screen.getByTestId('chat-input')
    act(() => { fireEvent.change(input, { target: { value: '@Mia review this' } }) })

    const form = screen.getByTestId('composer-form')
    act(() => { fireEvent.submit(form) })

    // Visible block: the composer's warning toast names the situation.
    const toasts = useUiStore.getState().toasts
    expect(toasts.some((t) => t.message.includes('@Mia') && t.message.includes('existing'))).toBe(true)
    // The message was NOT sent and NOT dropped from the composer.
    expect(sendSpy).not.toHaveBeenCalled()
    expect(input).toHaveValue('@Mia review this')
  })

  it('submitting the same text in an EXISTING chat sends normally (no block)', async () => {
    const sendSpy = vi.fn().mockReturnValue(true)
    act(() => {
      useConnectionStore.setState({
        connection: { send: sendSpy, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as never,
        isConnected: true,
      })
      useSessionStore.setState({ activeSessionId: 'sess_mention_test' })
    })

    render(<OmnipusComposer />)
    const input = screen.getByTestId('chat-input')
    act(() => { fireEvent.change(input, { target: { value: '@Mia review this' } }) })

    const form = screen.getByTestId('composer-form')
    act(() => { fireEvent.submit(form) })

    expect(useUiStore.getState().toasts).toHaveLength(0)
  })
})

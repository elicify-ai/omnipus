// #1428 step 2: both submit paths (Enter and the Send button) and the mid-stream path record the chat the
// draft was written in, so a chat switch between typing and sending cannot redirect the draft.
// Real: OmnipusComposer, its submit handlers, the draft-target module, the session store.
// Replaced edge: the assistant-ui composer runtime (send is a spy; the runtime side is covered by
// src/lib/omnipus-runtime.draft-target.test.tsx with the real composer).
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import * as React from 'react'
import { act } from 'react'
import { useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'
import { useWorkspacesStore } from '@/store/workspacesStore'
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
    Send: ({ disabled, children, className, onClick, 'data-testid': testId, 'aria-label': ariaLabel }: {
              disabled?: boolean; children?: React.ReactNode; className?: string; onClick?: (e: React.MouseEvent) => void;
              'data-testid'?: string; 'aria-label'?: string;
            }) =>
              React.createElement(MockButton, {
                type: 'button', disabled, className, onClick,
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
  { has_local_changes: false, id: 'web-research', name: 'Web Research', version: '1.0', description: 'Web search and extraction', verified: true, status: 'active' },
  { has_local_changes: false, id: 'code-review',  name: 'Code Review',  version: '1.0', description: 'Reviews code quality',      verified: true, status: 'active' },
]

// "@" mention menu source data — three distinct-prefix scoped agents
// (Mia/Jim/Ava) plus two agents that must NEVER render as mention rows:
// a worker (Subagent — isWorker() exclusion) and a draft-status agent (not
// active/idle). "Max Worker" deliberately also starts with "m" so the "@m"
// filter test doubles as proof that worker exclusion applies BEFORE the
// prefix filter.
const mockAgents = [
  makeAgent({ id: 'mia', name: 'Mia', type: 'core', status: 'active', description: 'Assistant' }),
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
      activeSessionId: 'sess_refused_test',
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

import { noteDraftText, takeDraftTarget, resetDraftTarget } from '@/lib/draft-target'

describe('submit paths record the chat the draft was written in (#1428 step 2)', () => {
  async function runtimeWith(text: string) {
    const send = vi.fn()
    const { useComposerRuntime } = await import('@assistant-ui/react')
    ;(useComposerRuntime as ReturnType<typeof vi.fn>).mockReturnValue({
      getState: () => ({ text }), setText: vi.fn(), addAttachment: vi.fn(), subscribe: vi.fn(() => vi.fn()), send,
    })
    return send
  }

  // The draft begins while chat A is attached; then another chat (a helper opened from the Activity row) is attached.
  function draftInAThenSwitchToB(text: string) {
    resetDraftTarget()
    act(() => { useSessionStore.setState({ activeSessionId: 'chat-a' }) })
    noteDraftText(text)
    act(() => { useSessionStore.setState({ activeSessionId: 'chat-b' }) })
  }

  it('Enter (form submit) marks the draft with the chat it was typed in, not the attached one', async () => {
    await runtimeWith('typed in A')
    draftInAThenSwitchToB('typed in A')
    render(<OmnipusComposer />)
    act(() => { fireEvent.change(screen.getByTestId('chat-input'), { target: { value: 'typed in A' } }) })
    act(() => { fireEvent.submit(screen.getByTestId('composer-form')) })
    expect(takeDraftTarget('typed in A')).toBe('chat-a')
  })

  it('the Send button marks the draft with the chat it was typed in', async () => {
    await runtimeWith('typed in A')
    draftInAThenSwitchToB('typed in A')
    render(<OmnipusComposer />)
    act(() => { fireEvent.change(screen.getByTestId('chat-input'), { target: { value: 'typed in A' } }) })
    act(() => { fireEvent.click(screen.getByTestId('chat-send')) })
    expect(takeDraftTarget('typed in A')).toBe('chat-a')
  })

  it('a refused Send (blocked recipient) does not leave a mark behind', async () => {
    await runtimeWith('go')
    resetDraftTarget()
    act(() => { useWorkspacesStore.setState({ activeWorkspaceId: 'ws_refused' }) })
    render(<OmnipusComposer />)
    act(() => { fireEvent.change(screen.getByTestId('chat-input'), { target: { value: 'go' } }) })
    act(() => { useSessionStore.setState({ resolvingSessionForWorkspace: { ws_refused: true } }) })
    act(() => { fireEvent.submit(screen.getByTestId('composer-form')) })
    expect(takeDraftTarget('go')).toBeUndefined()
  })
})

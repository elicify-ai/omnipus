// #1428 item 5: an idle `/cancel` says so. The tree cancel frame is ALWAYS still
// sent (helpers may run while this chat is idle); the notice appears only when
// nothing at all is running here.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import * as React from 'react'
import { act } from 'react'
import { emptySessionState } from '@/store/chat/session'
import { useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'
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

describe('/cancel feedback when nothing is running (#1428 item 5)', () => {
  const NOTHING = 'Nothing to cancel'
  const sendSpy = vi.fn().mockReturnValue(true)

  async function submitCancel() {
    const { useComposerRuntime } = await import('@assistant-ui/react')
    ;(useComposerRuntime as ReturnType<typeof vi.fn>).mockReturnValue({
      getState: () => ({ text: '/cancel' }), setText: vi.fn(), addAttachment: vi.fn(), subscribe: vi.fn(() => vi.fn()), send: vi.fn(),
    })
    sendSpy.mockClear()
    act(() => {
      useConnectionStore.setState({
        connection: { send: sendSpy, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as never,
        isConnected: true,
      })
    })
    render(<OmnipusComposer />)
    act(() => { fireEvent.change(screen.getByTestId('chat-input'), { target: { value: '/cancel' } }) })
    act(() => { fireEvent.submit(screen.getByTestId('composer-form')) })
  }
  const toasted = () => useUiStore.getState().toasts.filter((t) => t.message.startsWith(NOTHING))
  const cancelFrames = () => sendSpy.mock.calls.map((c) => c[0]).filter((f) => f?.type === 'cancel')

  it('idle: shows "Nothing to cancel" AND still sends the tree cancel frame', async () => {
    await submitCancel()
    expect(toasted()).toHaveLength(1)
    expect(cancelFrames()).toEqual([expect.objectContaining({ session_id: 'sess_refused_test', scope: 'tree' })])
  })

  it('a running helper: no notice, frame sent', async () => {
    act(() => {
      useChatStore.setState({
        messages: [{ id: 'm1', role: 'assistant', content: '', timestamp: '', status: 'done',
          spans: [{ spanId: 's1', parentCallId: 'c1', taskLabel: 'helper', status: 'running' }] }] as never,
      })
    })
    await submitCancel()
    expect(toasted()).toHaveLength(0)
    expect(cancelFrames()).toHaveLength(1)
  })

  it('a finished helper does not count as running', async () => {
    act(() => {
      useChatStore.setState({
        messages: [{ id: 'm1', role: 'assistant', content: '', timestamp: '', status: 'done',
          spans: [{ spanId: 's1', parentCallId: 'c1', taskLabel: 'helper', status: 'success', durationMs: 5 }] }] as never,
      })
    })
    await submitCancel()
    expect(toasted()).toHaveLength(1)
    expect(cancelFrames()).toHaveLength(1)
  })

  const bashCall = (status: string) => ({
    id: 'call_bash_1', call_id: 'call_bash_1', tool: 'bash',
    params: { command: 'sleep 15 && echo done', run_in_background: true },
    status: 'success', result: JSON.stringify({ sessionId: 'abc123', status }),
  })

  it('a running background command: no notice, frame sent', async () => {
    act(() => { useChatStore.setState({ toolCalls: { call_bash_1: bashCall('running') } as never }) })
    await submitCancel()
    expect(toasted()).toHaveLength(0)
    expect(cancelFrames()).toHaveLength(1)
  })

  it('a finished background command does not count as running', async () => {
    act(() => { useChatStore.setState({ toolCalls: { call_bash_1: bashCall('exited') } as never }) })
    await submitCancel()
    expect(toasted()).toHaveLength(1)
  })

  it('a streaming turn: no notice, frame sent', async () => {
    act(() => { useChatStore.setState({ isStreaming: true }) })
    await submitCancel()
    expect(toasted()).toHaveLength(0)
    expect(cancelFrames()).toHaveLength(1)
  })

  it('a running goal between turns: no notice, frame sent', async () => {
    act(() => { useChatStore.setState({ goalStatus: { state: 'active' } as never }) })
    await submitCancel()
    expect(toasted()).toHaveLength(0)
    expect(cancelFrames()).toHaveLength(1)
  })

  // CHECK r1 F1/F2: the notice must follow the same scope as the tree cancel.
  const asst = (id: string, extra: Record<string, unknown>) => ({ id, role: 'assistant', content: '', timestamp: '', status: 'done', ...extra })
  const bucketOf = (messages: Record<string, unknown>[], extra: Record<string, unknown> = {}) => ({
    ...emptySessionState(),
    messagesById: Object.fromEntries(messages.map((m) => [m.id as string, m])),
    messageOrder: messages.map((m) => m.id as string),
    ...extra,
  })
  const endedSpan = (over: Record<string, unknown>) => ({
    spanId: 's1', parentCallId: 'c1', taskLabel: 'helper', status: 'interrupted', durationMs: 5, ...over,
  })

  it('F1: a resumed helper (ended bracket, lifecycle running) counts as running', async () => {
    act(() => {
      useChatStore.setState({ messages: [asst('m1', { spans: [endedSpan({ lifecycleState: 'running' })] })] as never })
    })
    await submitCancel()
    expect(toasted()).toHaveLength(0)
    expect(cancelFrames()).toHaveLength(1)
  })

  it('F1: an ended helper whose lifecycle is completed does not count', async () => {
    act(() => {
      useChatStore.setState({ messages: [asst('m1', { spans: [endedSpan({ status: 'success', lifecycleState: 'completed' })] })] as never })
    })
    await submitCancel()
    expect(toasted()).toHaveLength(1)
  })

  it('F2: a running grandchild under a stopped child counts as running', async () => {
    act(() => {
      useChatStore.setState({
        messages: [asst('m1', { spans: [endedSpan({ status: 'interrupted', lifecycleState: 'stopped', childSessionId: 'child_1' })] })] as never,
        sessionsById: {
          sess_refused_test: bucketOf([]),
          child_1: bucketOf([asst('c1m', { spans: [{ spanId: 'g1', parentCallId: 'x', taskLabel: 'grand', status: 'running' }] })]),
        } as never,
      })
    })
    await submitCancel()
    expect(toasted()).toHaveLength(0)
    expect(cancelFrames()).toHaveLength(1)
  })

  it('F2: a background command left running by a completed child counts as running', async () => {
    act(() => {
      useChatStore.setState({
        messages: [asst('m1', { spans: [endedSpan({ status: 'success', lifecycleState: 'completed', childSessionId: 'child_1' })] })] as never,
        sessionsById: {
          sess_refused_test: bucketOf([]),
          child_1: bucketOf([asst('c1m', { tool_calls: [bashCall('running')] })]),
        } as never,
      })
    })
    await submitCancel()
    expect(toasted()).toHaveLength(0)
  })

  it('F2: a streaming child session counts as running', async () => {
    act(() => {
      useChatStore.setState({
        messages: [asst('m1', { spans: [endedSpan({ status: 'success', lifecycleState: 'completed', childSessionId: 'child_1' })] })] as never,
        sessionsById: { sess_refused_test: bucketOf([]), child_1: bucketOf([], { isStreaming: true }) } as never,
      })
    })
    await submitCancel()
    expect(toasted()).toHaveLength(0)
  })

  it('F2: a streaming session two levels down (child then grandchild session) counts as running', async () => {
    act(() => {
      useChatStore.setState({
        messages: [asst('m1', { spans: [endedSpan({ status: 'success', lifecycleState: 'completed', childSessionId: 'child_1' })] })] as never,
        sessionsById: {
          sess_refused_test: bucketOf([]),
          child_1: bucketOf([asst('c1m', { spans: [endedSpan({ spanId: 'g1', status: 'success', lifecycleState: 'completed', childSessionId: 'grand_1' })] })]),
          grand_1: bucketOf([], { isStreaming: true }),
        } as never,
      })
    })
    await submitCancel()
    expect(toasted()).toHaveLength(0)
  })

  it('R2-F1: after message-ring eviction (trimmedCount > 0) a loaded live session cannot be ruled out', async () => {
    act(() => {
      useChatStore.setState({
        sessionsById: {
          sess_refused_test: bucketOf([], { trimmedCount: 3 }),
          child_1: bucketOf([], { isStreaming: true }),
        } as never,
      })
    })
    await submitCancel()
    expect(toasted()).toHaveLength(0)
    expect(cancelFrames()).toHaveLength(1)
  })

  it('R2-F1: trimmed messages in a reached helper session also rule nothing out', async () => {
    act(() => {
      useChatStore.setState({
        messages: [asst('m1', { spans: [endedSpan({ status: 'success', lifecycleState: 'completed', childSessionId: 'child_1' })] })] as never,
        sessionsById: {
          sess_refused_test: bucketOf([]),
          child_1: bucketOf([], { trimmedCount: 2 }),
          grand_1: bucketOf([], { isStreaming: true }),
        } as never,
      })
    })
    await submitCancel()
    expect(toasted()).toHaveLength(0)
  })

  it('safe over-suppression: running work in an unrelated open chat also keeps the notice away', async () => {
    act(() => {
      useChatStore.setState({
        sessionsById: {
          sess_refused_test: bucketOf([]),
          other_chat: bucketOf([asst('om', { spans: [{ spanId: 'o1', parentCallId: 'x', taskLabel: 'o', status: 'running' }] })], { isStreaming: true }),
        } as never,
      })
    })
    await submitCancel()
    expect(toasted()).toHaveLength(0)
    expect(cancelFrames()).toHaveLength(1)
  })

  it('R3-F2: a helper in a message hidden while reading older history counts as running', async () => {
    act(() => {
      useChatStore.setState({
        sessionsById: {
          sess_refused_test: { ...bucketOf([asst('hid', { spans: [{ spanId: 'h1', parentCallId: 'x', taskLabel: 'h', status: 'running' }] })]), messageOrder: [] },
        } as never,
      })
    })
    await submitCancel()
    expect(toasted()).toHaveLength(0)
  })

  it('R3-F2: a running background command in a hidden message counts as running', async () => {
    act(() => {
      useChatStore.setState({
        sessionsById: {
          sess_refused_test: { ...bucketOf([asst('hid', { tool_calls: [bashCall('running')] })]), messageOrder: [] },
        } as never,
      })
    })
    await submitCancel()
    expect(toasted()).toHaveLength(0)
  })

  it('R3-F1: a streaming session with no link left to it still keeps the notice away', async () => {
    act(() => {
      useChatStore.setState({
        sessionsById: { sess_refused_test: bucketOf([]), grand_1: bucketOf([], { isStreaming: true }) } as never,
      })
    })
    await submitCancel()
    expect(toasted()).toHaveLength(0)
  })

  // CHECK r4 F1/F2.
  it.each(['active', 're-planning', 'judging', 'judge_unavailable'])('R4-F2: a loaded other chat with a %s goal keeps the notice away', async (state) => {
    act(() => {
      useChatStore.setState({
        sessionsById: { sess_refused_test: bucketOf([]), child_1: bucketOf([], { goalStatus: { state } }) } as never,
      })
    })
    await submitCancel()
    expect(toasted()).toHaveLength(0)
    expect(cancelFrames()).toHaveLength(1)
  })

  it('R4-F2 control: a loaded other chat with a finished goal does not keep the notice away', async () => {
    act(() => {
      useChatStore.setState({
        sessionsById: { sess_refused_test: bucketOf([]), child_1: bucketOf([], { goalStatus: { state: 'done' } }) } as never,
      })
    })
    await submitCancel()
    expect(toasted()).toHaveLength(1)
  })

  // The chat's own evidence is incomplete when older messages are not loaded: a running
  // background command may have been dispatched in a message that is gone.
  it.each([
    ['trimmed messages', { trimmedCount: 1 }],
    ['earlier history not loaded', { hasEarlierMessages: true }],
    ['a history snapshot still pending', { historyNeedsSnapshot: true }],
  ])('R4-F1: %s in this chat means idle cannot be proven, so no notice', async (_name, extra) => {
    act(() => {
      useChatStore.setState({ sessionsById: { sess_refused_test: bucketOf([], extra) } as never })
    })
    await submitCancel()
    expect(toasted()).toHaveLength(0)
    expect(cancelFrames()).toHaveLength(1)
  })

  it('R4-F1 control: a fully loaded idle chat still gets the notice', async () => {
    act(() => {
      useChatStore.setState({ sessionsById: { sess_refused_test: bucketOf([asst('m1', {})]) } as never })
    })
    await submitCancel()
    expect(toasted()).toHaveLength(1)
  })
})

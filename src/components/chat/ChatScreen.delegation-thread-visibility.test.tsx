/**
 * ChatScreen.delegation-thread-visibility.test.tsx — Fix 2 (user-approved
 * 2026-07-16, revised same day): delegation is hidden from the chat THREAD
 * by default, with NO failed-state exception — a subagent/background-shell
 * error is returned to the delegating agent's own turn as the tool result,
 * and that agent explains it in its own response text; the raw result stays
 * transparent in the ActivityPanel slide-out (see ActivityPanel.test.tsx's
 * "panel-only step visibility policy" block for that side). Only the
 * Verbose-chat setting (useChatPreferencesStore) reveals these rows/cards in
 * the thread.
 *
 * Style mirrors ChatScreen.tool-order.test.tsx: full ChatScreen render,
 * PlainMessageList fallback (ResizeObserver forced undefined) so a finished
 * message renders through VirtualAssistantMessageRow without needing to fake
 * virtualizer geometry. Unlike that file, `./tools/GenericToolCall` is left
 * UNMOCKED here — the assertions in this file are specifically about
 * GenericToolCall's OWN internal shouldRenderToolCall gate (a delegate 'run'
 * call), so stubbing it away would defeat the point. `./SubagentBlock` is
 * mocked down to a detectable stub (not `() => null`, unlike sibling
 * ChatScreen test files) exposing the span's status/id, so this file can
 * assert PRESENCE/ABSENCE of the card without depending on SubagentBlock's
 * own internal markup (that component's own rendering is covered by
 * SubagentBlock.test.tsx).
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, act } from '@testing-library/react'
import * as React from 'react'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { useChatPreferencesStore } from '@/store/chatPreferences'

vi.mock('@assistant-ui/react', () => {
  return {
    useThreadViewportStore: () => ({ getState: () => ({ isAtBottom: true }) }),
    ThreadPrimitive: {
      Root: ({ children, className }: { children: React.ReactNode; className?: string }) =>
        React.createElement('div', { className }, children),
      Viewport: React.forwardRef(
        (
          { children, className, style, 'data-testid': testId }: {
            children?: React.ReactNode; className?: string; style?: React.CSSProperties; 'data-testid'?: string
          },
          ref: React.Ref<HTMLDivElement>,
        ) => React.createElement('div', { ref, className, style, 'data-testid': testId }, children),
      ),
      Messages: () => null,
    },
    MessagePrimitive: {
      Root: ({ children, className }: { children: React.ReactNode; className?: string }) =>
        React.createElement('div', { className }, children),
      Parts: () => null,
    },
    ComposerPrimitive: {
      Root: ({ children, className }: { children: React.ReactNode; className?: string }) =>
        React.createElement('div', { className }, children),
      Input: ({ disabled, placeholder, className, onChange, onKeyDown, onBlur }: {
        disabled?: boolean; placeholder?: string; className?: string;
        onChange?: (e: React.ChangeEvent<HTMLTextAreaElement>) => void;
        onKeyDown?: (e: React.KeyboardEvent<HTMLTextAreaElement>) => void;
        onBlur?: () => void;
      }) =>
        React.createElement('textarea', {
          disabled, placeholder, className, onChange, onKeyDown, onBlur,
          'data-testid': 'composer-input',
        }),
      Send: ({ disabled, children, className, 'data-testid': testId }: {
        disabled?: boolean; children?: React.ReactNode; className?: string; 'data-testid'?: string
      }) =>
        React.createElement('button', { type: 'button', disabled, className, 'data-testid': testId ?? 'chat-send' }, children),
      AddAttachment: ({ disabled, children, className }: { disabled?: boolean; children?: React.ReactNode; className?: string }) =>
        React.createElement('button', { type: 'button', disabled, className, 'data-testid': 'add-attachment' }, children),
      Attachments: () => null,
    },
    AttachmentPrimitive: {
      Root: ({ children, className }: { children?: React.ReactNode; className?: string }) =>
        React.createElement('div', { className }, children),
      Name: () => null,
      Remove: ({ children, className }: { children?: React.ReactNode; className?: string }) =>
        React.createElement('button', { type: 'button', className }, children),
      Thumb: () => null,
    },
    MessagePartPrimitive: { InProgress: () => null },
    ActionBarPrimitive: {
      Root: ({ children }: { children: React.ReactNode }) => React.createElement('div', {}, children),
      Copy: ({ children }: { children: React.ReactNode }) => React.createElement('span', {}, children),
    },
    AuiIf: () => null,
    useComposerRuntime: () => ({
      getState: () => ({ text: '' }),
      setText: vi.fn(),
      addAttachment: vi.fn(),
      subscribe: vi.fn(() => vi.fn()),
    }),
    useMessage: () => ({
      id: 'msg_streaming',
      role: 'assistant',
      status: { type: 'running' },
      content: [],
    }),
    useAttachment: vi.fn(() => ({
      id: 'att-default',
      name: 'file.txt',
      contentType: 'text/plain',
      file: undefined,
      status: { type: 'complete' },
      content: [],
    })),
    makeAssistantToolUI: () => () => null,
  }
})

vi.mock('@tanstack/react-query', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-query')>()
  return {
    ...actual,
    useQuery: () => ({ data: [], isError: false, refetch: vi.fn() }),
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
    fetchAboutInfo: vi.fn().mockResolvedValue({ preview_port: 5001 }),
    createSession: vi.fn(),
    uploadFiles: vi.fn(),
    fetchProviders: vi.fn().mockResolvedValue([]),
    isApiError: vi.fn().mockReturnValue(false),
    fetchCommands: vi.fn().mockResolvedValue([]),
    fetchSkills: vi.fn().mockResolvedValue([]),
  }
})

vi.mock('./historical-markdown', () => ({
  HistoricalMessageMarkdown: ({ content }: { content: string }) =>
    React.createElement('div', { 'data-testid': 'historical-markdown' }, content),
}))

vi.mock('@/assets/logo/omnipus-avatar.svg?url', () => ({ default: 'omnipus-avatar.svg' }))
vi.mock('./RateLimitIndicator', () => ({ RateLimitIndicator: () => null }))

// Detectable stub (unlike sibling ChatScreen test files' `() => null`) — this
// file's whole point is asserting whether ChatScreen chooses to mount a
// SubagentBlock at all for a given span, so the stub must expose enough to
// query for it and read the span it was given.
vi.mock('./SubagentBlock', () => ({
  SubagentBlock: ({ span }: { span: { spanId: string; status: string; taskLabel: string } }) =>
    React.createElement(
      'div',
      { 'data-testid': 'subagent-block-stub', 'data-span-id': span.spanId, 'data-status': span.status },
      span.taskLabel,
    ),
}))

// `./tools/GenericToolCall` deliberately LEFT UNMOCKED — see file header.
vi.mock('./tools/BrowserTool', () => ({
  isReplayBrowserToolName: () => false,
  BrowserToolReplayBlock: () => null,
}))
vi.mock('./tools/WebServeUI', () => ({
  WebServeBlock: () => null,
}))
vi.mock('./markdown-text', () => ({
  MarkdownText: () => React.createElement('div', {}),
}))
vi.mock('@/components/shared/IconRenderer', () => ({ IconRenderer: () => null }))
vi.mock('./composer/AgentPicker', () => ({ AgentPicker: () => null }))
vi.mock('./composer/ModelPicker', () => ({ ModelPicker: () => null }))
vi.mock('./composer/TokenCounter', () => ({ TokenCounter: () => null }))
vi.mock('@/lib/memory-observer', () => ({
  startMemoryObserver: () => ({ dispose: vi.fn(), getCurrentSnapshot: vi.fn() }),
  addMemoryObserver: () => () => {},
  getCurrentSnapshot: () => ({ usedJSHeapSizeBytes: null, level: 'ok', supported: false }),
}))

import { ChatScreen } from './ChatScreen'

const SID = 'test-session-delegation-visibility'

beforeEach(() => {
  useConnectionStore.setState({
    isConnected: true,
    liteMode: false,
    reconnectPhase: null,
    reconnectAttempt: 0,
    connectionError: null,
    connection: null,
  })
  // Force the PlainMessageList fallback (as tool-order/plain-list-parity do)
  // so a finished message renders through VirtualAssistantMessageRow without
  // needing to fake virtualizer geometry.
  vi.unstubAllGlobals()
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  ;(globalThis as any).ResizeObserver = undefined

  useSessionStore.setState({ activeSessionId: SID, activeAgentId: 'agent-1' })
  act(() => {
    useChatStore.getState().resetSession()
  })
  act(() => {
    useChatPreferencesStore.setState({ verboseChatEnabled: false })
  })
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('ChatScreen — delegation card thread visibility (Fix 2, revised: verbose-only, no failed-state exception)', () => {
  it('a running delegation span renders NO SubagentBlock card by default', async () => {
    act(() => { useChatStore.getState().handleFrame({ type: 'token', content: 'Working on it. ', session_id: SID }) })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'subagent_start',
        span_id: 'span_running',
        parent_call_id: 'delegate_call_running',
        task_label: 'audit files',
        agent_id: 'ray',
        session_id: SID,
      })
    })
    act(() => { useChatStore.getState().handleFrame({ type: 'done', session_id: SID }) })

    let container!: HTMLElement
    await act(async () => {
      const result = render(<ChatScreen />)
      container = result.container
    })

    expect(container.querySelector('[data-testid="subagent-block-stub"]')).toBeNull()
  })

  it('a FAILED (error) delegation span ALSO renders NO SubagentBlock card by default — no failed-state exception', async () => {
    act(() => { useChatStore.getState().handleFrame({ type: 'token', content: 'Working on it. ', session_id: SID }) })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'subagent_start',
        span_id: 'span_failed',
        parent_call_id: 'delegate_call_failed',
        task_label: 'audit files',
        agent_id: 'ray',
        session_id: SID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'subagent_end',
        span_id: 'span_failed',
        status: 'error',
        duration_ms: 500,
        session_id: SID,
      })
    })
    act(() => { useChatStore.getState().handleFrame({ type: 'done', session_id: SID }) })

    let container!: HTMLElement
    await act(async () => {
      const result = render(<ChatScreen />)
      container = result.container
    })

    // The failure is left for the delegating agent's own response text and
    // the ActivityPanel to surface — NOT a thread card, per the revised
    // no-failed-state-exception policy.
    expect(container.querySelector('[data-testid="subagent-block-stub"]')).toBeNull()
  })

  it('the same FAILED delegation span becomes visible once verbose chat is enabled', async () => {
    act(() => {
      useChatPreferencesStore.setState({ verboseChatEnabled: true })
    })
    act(() => { useChatStore.getState().handleFrame({ type: 'token', content: 'Working on it. ', session_id: SID }) })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'subagent_start',
        span_id: 'span_failed_verbose',
        parent_call_id: 'delegate_call_failed_verbose',
        task_label: 'audit files',
        agent_id: 'ray',
        session_id: SID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'subagent_end',
        span_id: 'span_failed_verbose',
        status: 'error',
        duration_ms: 500,
        session_id: SID,
      })
    })
    act(() => { useChatStore.getState().handleFrame({ type: 'done', session_id: SID }) })

    let container!: HTMLElement
    await act(async () => {
      const result = render(<ChatScreen />)
      container = result.container
    })

    const stub = container.querySelector('[data-testid="subagent-block-stub"]')
    expect(stub).not.toBeNull()
    expect(stub).toHaveAttribute('data-status', 'error')
  })
})

describe('ChatScreen — synchronous delegate GenericToolCall row thread visibility (Fix 2, revised)', () => {
  /** GenericToolCall row for a flat (non-spanned) tool call — no parent_call_id. */
  function seedSyncDelegateCall(opts: { status: 'success' | 'error'; error?: string }) {
    act(() => { useChatStore.getState().handleFrame({ type: 'token', content: 'Delegating now. ', session_id: SID }) })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_start',
        call_id: 'tc_delegate_sync',
        tool: 'delegate',
        params: { action: 'run', async: false, target_agent_id: 'ray', task: 'do X' },
        session_id: SID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_result',
        call_id: 'tc_delegate_sync',
        tool: 'delegate',
        result: opts.status === 'error' ? { error: 'delegation_denied' } : { ok: true },
        status: opts.status,
        error: opts.error,
        session_id: SID,
      })
    })
    act(() => { useChatStore.getState().handleFrame({ type: 'done', session_id: SID }) })
  }

  it('an explicit synchronous (async:false) delegate run renders NO tool-call badge by default — the SubagentBlock card, not this row, was the pre-Fix-2 duplicate', async () => {
    seedSyncDelegateCall({ status: 'success' })

    let container!: HTMLElement
    await act(async () => {
      const result = render(<ChatScreen />)
      container = result.container
    })

    expect(container.querySelector('[data-testid="tool-call-badge"][data-tool="delegate"]')).toBeNull()
  })

  it('a FAILED synchronous delegate run is ALSO hidden by default — no isError exception (LLM-mediated failure presentation)', async () => {
    seedSyncDelegateCall({ status: 'error', error: 'delegation_denied' })

    let container!: HTMLElement
    await act(async () => {
      const result = render(<ChatScreen />)
      container = result.container
    })

    expect(container.querySelector('[data-testid="tool-call-badge"][data-tool="delegate"]')).toBeNull()
  })

  it('a FAILED synchronous delegate run becomes visible once verbose chat is enabled', async () => {
    act(() => {
      useChatPreferencesStore.setState({ verboseChatEnabled: true })
    })
    seedSyncDelegateCall({ status: 'error', error: 'delegation_denied' })

    let container!: HTMLElement
    await act(async () => {
      const result = render(<ChatScreen />)
      container = result.container
    })

    const badge = container.querySelector('[data-testid="tool-call-badge"][data-tool="delegate"]')
    expect(badge).not.toBeNull()
  })
})

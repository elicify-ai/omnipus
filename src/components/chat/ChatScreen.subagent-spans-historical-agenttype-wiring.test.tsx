/**
 * ChatScreen.subagent-spans-historical-agenttype-wiring.test.tsx — W3
 * resolveSpanAgentType wiring, historical/virtualized render path
 * (test-analyzer MED-HIGH finding, delegation-visibility review).
 *
 * ChatScreen.tsx's `resolveSpanAgentType` (~L461-471) resolves a subagent
 * span's delegate KIND (native vs `subagent_3p`) against the `['agents']`
 * query so SubagentBlock can show its "no live progress" notice for a
 * running external-CLI delegate. `SubagentBlock.test.tsx` covers what
 * SubagentBlock does with a given `agentType` prop; `ChatScreen.subagent-
 * spans-live-visibility.test.tsx` covers the LIVE render path
 * (SubagentSpansRenderer) computing and passing the right value. Neither
 * covers the HISTORICAL/virtualized render path (VirtualAssistantMessageRow,
 * ~L967) — and neither exercises the REAL, unmocked SubagentBlock end to
 * end, so the wiring between resolveSpanAgentType and the actual rendered
 * notice DOM was never verified together on any path.
 *
 * This file closes both gaps for the historical path: `./SubagentBlock` is
 * deliberately left UNMOCKED (unlike every sibling ChatScreen.*.test.tsx,
 * which stub it down to a detectable div) so the real "no live progress"
 * notice (`data-testid="subagent-3p-running-notice"`) must actually reach
 * the DOM for these tests to pass — proving the full chain: WS
 * `subagent_start` frame -> store span with `agentId` -> ChatScreen's
 * `['agents']` query (real fetchAgents mock, includes a genuine
 * `type: 'subagent_3p'` agent) -> resolveSpanAgentType -> SubagentBlock's
 * own `show3pRunningNotice` gate.
 *
 * Harness: full manual mock of `@assistant-ui/react` (ThreadPrimitive.
 * Messages -> null) forces every message through the historical/virtualized
 * render tree, exactly as ChatScreen.delegation-thread-visibility.test.tsx
 * does — plus ResizeObserver forced undefined so PlainMessageList (which
 * routes every message, including in-flight ones, through
 * VirtualAssistantMessageRow) is used instead of the real virtualizer,
 * avoiding the need to fake virtualizer geometry. Unlike that file,
 * `@tanstack/react-query` is left REAL (with a real QueryClientProvider) so
 * ChatScreen's own `useQuery(['agents'], fetchAgents)` actually resolves the
 * mocked agents list instead of the blanket `{ data: [] }} that file's
 * generic mock returns — resolveSpanAgentType has nothing to resolve
 * against otherwise.
 */

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, act, waitFor } from '@testing-library/react'
import * as React from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
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

// `@tanstack/react-query` is deliberately left REAL (see file header) — a
// real QueryClientProvider is supplied by the Providers wrapper below.

vi.mock('@tanstack/react-router', () => ({
  useRouter: () => ({ navigate: vi.fn() }),
  useSearch: () => ({}),
  Link: ({ children }: { children: React.ReactNode }) => children,
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([
      { id: 'agent-1', name: 'Mia', type: 'Main', locked: true, status: 'active', color: '#123456', icon: null },
      { id: 'ray', name: 'Ray', type: 'Subagent', locked: false, status: 'active', color: '#4488ff', icon: 'compass' },
      // The one agent this file's assertions actually depend on: a REAL
      // subagent_3p agent, so resolveSpanAgentType has something genuine to
      // resolve '3p' against on the historical render path.
      { id: 'codex-1', name: 'Codex Runner', type: 'subagent_3p', locked: false, status: 'active', color: '#ff8800', icon: 'terminal-window' },
    ]),
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

// `./SubagentBlock` is deliberately LEFT UNMOCKED — see file header. This is
// the one thing that differs from every sibling ChatScreen.*.test.tsx file.

// `./tools/GenericToolCall` also LEFT UNMOCKED — SubagentBlock's real
// ToolCallBadge imports named exports from it (isDelegationFailure,
// policyAxisLabel); mocking it down to `() => null` would leave those
// imports undefined. Not actually exercised by these tests (the spans here
// carry no steps), but importing the real module keeps it safe either way.
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

const SID = 'test-session-historical-agenttype-wiring'

function Providers({ children }: { children: React.ReactNode }) {
  const queryClientRef = React.useRef<QueryClient | null>(null)
  if (!queryClientRef.current) {
    queryClientRef.current = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    // Prime the ['agents'] cache synchronously (same list the mocked
    // fetchAgents above resolves to) so the very first render already has
    // resolveSpanAgentType's data available — without this, the notice
    // assertions below race the real fetchAgents() promise's microtask
    // against React Testing Library's single `act(async () => render(...))`
    // flush, and the '3p' case can observe the pre-resolution empty agents
    // list. This makes all three tests deterministic instead of relying on
    // how many microtask ticks happen to elapse.
    queryClientRef.current.setQueryData(['agents'], [
      { id: 'agent-1', name: 'Mia', type: 'Main', locked: true, status: 'active', color: '#123456', icon: null },
      { id: 'ray', name: 'Ray', type: 'Subagent', locked: false, status: 'active', color: '#4488ff', icon: 'compass' },
      { id: 'codex-1', name: 'Codex Runner', type: 'subagent_3p', locked: false, status: 'active', color: '#ff8800', icon: 'terminal-window' },
    ])
  }
  return React.createElement(QueryClientProvider, { client: queryClientRef.current }, children)
}

beforeEach(() => {
  useConnectionStore.setState({
    isConnected: true,
    liteMode: false,
    reconnectPhase: null,
    reconnectAttempt: 0,
    connectionError: null,
    connection: null,
  })
  // Force the PlainMessageList fallback so a finished message renders
  // through VirtualAssistantMessageRow without needing to fake virtualizer
  // geometry (mirrors ChatScreen.delegation-thread-visibility.test.tsx).
  vi.unstubAllGlobals()
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  ;(globalThis as any).ResizeObserver = undefined

  useSessionStore.setState({ activeSessionId: SID, activeAgentId: 'agent-1' })
  act(() => {
    useChatStore.getState().resetSession()
  })
  act(() => {
    // Spans are hidden from the thread by default (Fix 2) — every test in
    // this file needs verbose chat on to see the span at all; that gating
    // is already covered elsewhere, it's incidental here.
    useChatPreferencesStore.setState({ verboseChatEnabled: true })
  })
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('ChatScreen — W3 resolveSpanAgentType wiring, historical/virtualized render path (real SubagentBlock)', () => {
  it('a running span whose agentId resolves to a real subagent_3p agent shows the "no live progress" notice', async () => {
    act(() => { useChatStore.getState().handleFrame({ type: 'token', content: 'Delegating now. ', session_id: SID }) })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'subagent_start',
        span_id: 'span_hist_3p_running',
        parent_call_id: 'delegate_call_hist_3p',
        task_label: 'run external task',
        agent_id: 'codex-1',
        session_id: SID,
      })
    })
    // No subagent_end — the span stays 'running' (async fire-and-forget
    // delegation can legitimately outlive the parent turn's own 'done').
    act(() => { useChatStore.getState().handleFrame({ type: 'done', session_id: SID }) })

    let container!: HTMLElement
    await act(async () => {
      const result = render(
        React.createElement(Providers, null, React.createElement(ChatScreen)),
      )
      container = result.container
    })

    await waitFor(() => {
      expect(container.querySelector('[data-testid="subagent-3p-running-notice"]')).not.toBeNull()
    })
    const notice = container.querySelector('[data-testid="subagent-3p-running-notice"]')
    expect(notice?.textContent).toMatch(/no live progress/i)
  })

  it('a running span whose agentId resolves to a NATIVE agent does NOT show the notice', async () => {
    act(() => { useChatStore.getState().handleFrame({ type: 'token', content: 'Delegating now. ', session_id: SID }) })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'subagent_start',
        span_id: 'span_hist_native_running',
        parent_call_id: 'delegate_call_hist_native',
        task_label: 'audit files',
        agent_id: 'ray',
        session_id: SID,
      })
    })
    act(() => { useChatStore.getState().handleFrame({ type: 'done', session_id: SID }) })

    let container!: HTMLElement
    await act(async () => {
      const result = render(
        React.createElement(Providers, null, React.createElement(ChatScreen)),
      )
      container = result.container
    })

    // The delegation card itself is visible (verbose chat is on), but no
    // "no live progress" notice — that notice is '3p'-only.
    expect(container.querySelector('[data-testid="subagent-collapsed"]')).not.toBeNull()
    expect(container.querySelector('[data-testid="subagent-3p-running-notice"]')).toBeNull()
  })

  it('an UNRESOLVABLE agentId (unknown agent) does NOT show the notice — never guessed as 3p', async () => {
    act(() => { useChatStore.getState().handleFrame({ type: 'token', content: 'Delegating now. ', session_id: SID }) })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'subagent_start',
        span_id: 'span_hist_unknown_running',
        parent_call_id: 'delegate_call_hist_unknown',
        task_label: 'audit files',
        agent_id: 'agent-does-not-exist',
        session_id: SID,
      })
    })
    act(() => { useChatStore.getState().handleFrame({ type: 'done', session_id: SID }) })

    let container!: HTMLElement
    await act(async () => {
      const result = render(
        React.createElement(Providers, null, React.createElement(ChatScreen)),
      )
      container = result.container
    })

    expect(container.querySelector('[data-testid="subagent-collapsed"]')).not.toBeNull()
    expect(container.querySelector('[data-testid="subagent-3p-running-notice"]')).toBeNull()
  })
})

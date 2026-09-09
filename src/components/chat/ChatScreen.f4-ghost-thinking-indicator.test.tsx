/**
 * ChatScreen.f4-ghost-thinking-indicator.test.tsx
 *
 * F4 (second review wave on branch fix/615-617-618-hardening): VirtualAssistant
 * MessageRow's `hasVisibleToolCalls` computation (used only to decide
 * `showEmptyPlaceholder`) called `wouldToolCallBeVisible(tc.tool, tc.params,
 * tc.result, !!tc.error, verboseChatEnabled)` — the `!!tc.error` disjunct is
 * exactly the signal #617 established is EMPTY for an ordinary failure
 * (pkg/gateway/websocket.go deliberately leaves `Result.Error` unset when
 * `Result` still holds the text). Its two siblings in this same file already
 * moved off that proxy: the live-path equivalent passes `!!part.isError`, and
 * the row's own actual render for the SAME `tc` uses `tc.status === 'error'`
 * directly (VirtualAssistantMessageRow's GenericToolCall/BrowserToolReplayBlock/
 * WebServeBlock call sites, all `isError={tc.status === 'error'}`).
 *
 * Concretely: a streaming message whose only content is a failed `ToolSearch`
 * call (status:'error', no `error` string — the exact producible shape #617
 * is about) computed `hasVisibleToolCalls: false` under the OLD code, while
 * the row itself — whose own visibility gate reads `tc.status === 'error'`
 * — still rendered the tool row (ToolSearch's shouldRenderToolCall case
 * forces visibility on error). `showEmptyPlaceholder` unconditionally gates
 * only the ThinkingIndicator (`{showEmptyPlaceholder && <ThinkingIndicator
 * />}`), NOT the tool-call render loop below it (`messageParts.map` has no
 * matching `{!showEmptyPlaceholder && ...}` guard) — so BOTH rendered at
 * once: a ghost "Thinking…" indicator sitting above a legitimately-visible
 * "Failed" tool row.
 *
 * This message is reachable via PlainMessageList (the ResizeObserver-
 * unavailable fallback) — unlike VirtualizedMessageListInner, it renders
 * every message including an in-flight one (isStreaming:true) through
 * VirtualAssistantMessageRow directly, without splitting the streaming
 * placeholder out into ThreadPrimitive.Messages. Same technique as
 * ChatScreen.issue-617-replay-outcome.test.tsx (ResizeObserver forced
 * undefined, GenericToolCall left unmocked so the real "Failed" label
 * renders).
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, act, screen } from '@testing-library/react'
import * as React from 'react'
import { useChatStore, makeBucketMessages } from '@/store/chat'
import type { ChatMessage, PositionedToolCall } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'

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
vi.mock('./SubagentBlock', () => ({ SubagentBlock: () => null }))
// `./tools/GenericToolCall` deliberately LEFT UNMOCKED — the whole point of
// this test is that the real row (whose own visibility gate reads
// `tc.status === 'error'`) renders "Failed" for the failed ToolSearch call
// while `hasVisibleToolCalls` must ALSO see it as visible (or the ghost
// ThinkingIndicator bug reproduces).
vi.mock('./IframePreview', () => ({ IframePreview: () => null }))
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

const SID = 'test-session-f4-ghost-thinking-indicator'

const THINKING_TEXT_RE = /Thinking…|Composing response…|Processing your request…|Analyzing…|Generating…/

function seedBucket(messages: ChatMessage[]): void {
  const bucket = makeBucketMessages(messages)
  useChatStore.setState((s) => ({
    ...s,
    sessionsById: {
      [SID]: {
        ...((s.sessionsById ?? {})[SID] ?? {}),
        ...bucket,
        isStreaming: true,
        isReplaying: false,
        replayCompletedForSession: SID,
        toolCalls: {},
        toolCallOrder: [],
        textAtToolCallStart: {},
        sessionTokens: 0,
        sessionCost: 0,
        rateLimitEvent: null,
        lastUserMessageAt: null,
        cancelStage: null,
        lastReceivedEventTime: null,
        spanByParentCallId: {},
        trimmedCount: 0,
      },
    },
    messages,
    isStreaming: true,
    isReplaying: false,
    replayCompletedForSession: SID,
  }))
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
  vi.unstubAllGlobals()
  // Forces PlainMessageList (ResizeObserver-unavailable fallback), which
  // renders every message — including an in-flight one — through
  // VirtualAssistantMessageRow. See file header.
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  ;(globalThis as any).ResizeObserver = undefined

  useSessionStore.setState({ activeSessionId: SID, activeAgentId: 'agent-1' })
  act(() => {
    useChatStore.getState().resetSession()
  })
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('F4 — ghost ThinkingIndicator over a visible failed ToolSearch row', () => {
  it('a streaming message whose only content is a failed ToolSearch (status:error, no error string) shows the "Failed" row and does NOT also show the ghost thinking indicator', async () => {
    const now = new Date().toISOString()
    const assistantMsg: ChatMessage = {
      id: 'msg_ghost',
      role: 'assistant',
      content: '',
      timestamp: now,
      status: 'streaming',
      isStreaming: true,
      tool_calls: [
        { id: 'tc_ghost', tool: 'ToolSearch', params: { name: 'foo' }, status: 'error' } as PositionedToolCall,
      ],
    }
    seedBucket([assistantMsg])

    await act(async () => {
      render(<ChatScreen />)
    })

    // The real failed-row render — ToolSearch's shouldRenderToolCall case
    // forces visibility on error, so this must be visible either way.
    expect(screen.getByText('Failed')).toBeInTheDocument()

    // The bug: hasVisibleToolCalls computed false (via the stale `!!tc.error`
    // proxy) even though the row above is visible, so showEmptyPlaceholder
    // was also true and rendered a ghost "Thinking…" alongside it.
    expect(screen.queryByText(THINKING_TEXT_RE)).toBeNull()
  })

  it('control: a streaming message whose only content is a SUCCESSFUL ToolSearch call correctly shows the ghost thinking indicator (ToolSearch stays hidden by default, so the message legitimately has no visible content yet)', async () => {
    const now = new Date().toISOString()
    const assistantMsg: ChatMessage = {
      id: 'msg_ghost_ok',
      role: 'assistant',
      content: '',
      timestamp: now,
      status: 'streaming',
      isStreaming: true,
      tool_calls: [
        { id: 'tc_ghost_ok', tool: 'ToolSearch', params: { name: 'foo' }, status: 'success', result: { ok: true } } as PositionedToolCall,
      ],
    }
    seedBucket([assistantMsg])

    await act(async () => {
      render(<ChatScreen />)
    })

    // ToolSearch with no error is hidden by default (toolVisibility.ts) —
    // there is genuinely no visible content yet, so the thinking indicator
    // is the CORRECT render here, not a ghost. This pins the boundary: the
    // F4 fix must not force ToolSearch visible unconditionally.
    expect(screen.queryByText('Failed')).toBeNull()
    expect(screen.queryByText('Done')).toBeNull()
    expect(screen.getByText(THINKING_TEXT_RE)).toBeInTheDocument()
  })
})

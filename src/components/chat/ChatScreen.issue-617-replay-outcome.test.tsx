/**
 * ChatScreen.issue-617-replay-outcome.test.tsx — H1 hardening (second review
 * wave on branch fix/615-617-618-hardening).
 *
 * The #617 fix touches THREE call sites in ChatScreen.tsx's
 * VirtualAssistantMessageRow (~line 954-1010):
 *   - GenericToolCall's new `isError={tc.status === 'error'}` prop
 *   - BrowserToolReplayBlock's `isError={tc.status === 'error'}` prop
 *   - WebServeBlock's `isError={tc.status === 'error'}` / `isCancelled={tc.status
 *     === 'cancelled'}` props
 * plus the `replayPartStatus(tc.status)` helper that replaced the old
 * hardcoded `status={{type:'complete'}}` so BrowserToolReplayBlock/
 * GenericToolCall's own `isCancelledStatus(status)` checks can see a
 * cancelled replay too.
 *
 * Every OTHER ChatScreen.*.test.tsx file that reaches this code mocks all
 * three tool components down to a stable `data-testid="tool-call-badge"`
 * stub (see ChatScreen.tool-order.test.tsx, ChatScreen.delegation-thread-
 * visibility.test.tsx, ChatScreen.subagent-spans-historical-agenttype-
 * wiring.test.tsx) — none of them render the real component, so none of
 * them would go red if the `isError`/`isCancelled`/`replayPartStatus` wiring
 * were reverted. This file renders the REAL GenericToolCall,
 * BrowserToolReplayBlock, and WebServeBlock components (only `IframePreview`
 * — WebServeBlock's own preview-link renderer, an unrelated concern — is
 * mocked away) so the assertions fail if ChatScreen stops passing the real
 * outcome through.
 *
 * Style follows ChatScreen.delegation-thread-visibility.test.tsx (which
 * already leaves GenericToolCall unmocked for the same reason): full
 * ChatScreen render, PlainMessageList fallback (ResizeObserver forced
 * undefined).
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
// `./tools/GenericToolCall`, `./tools/BrowserTool`, `./tools/WebServeUI`
// deliberately LEFT UNMOCKED — see file header. Only WebServeBlock's own
// preview-link renderer (an unrelated concern — resolving/copying a
// clickable URL) is stubbed away, so this file's assertions never depend on
// window.location/fetch behavior.
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

const SID = 'test-session-issue-617-replay-outcome'

function seedBucket(messages: ChatMessage[]): void {
  const bucket = makeBucketMessages(messages)
  useChatStore.setState((s) => ({
    ...s,
    sessionsById: {
      [SID]: {
        ...((s.sessionsById ?? {})[SID] ?? {}),
        ...bucket,
        isStreaming: false,
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
    isStreaming: false,
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

describe('ChatScreen replay wiring — #617 catch-all (GenericToolCall)', () => {
  it('a failed tool call whose store frame carries status:"error" with NO `error` string renders "Failed", not "Done"', async () => {
    // This is the exact producible shape #617 was about: pkg/gateway/
    // websocket.go only sets resultF.Error when the result was offloaded or
    // replaced — an ordinary small failed call arrives with status:'error'
    // and no `error` field at all.
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_start',
        call_id: 'tc_fail',
        tool: 'run_diagnostic',
        params: {},
        session_id: SID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_result',
        call_id: 'tc_fail',
        tool: 'run_diagnostic',
        result: 'exit code 1: diagnostic failed',
        status: 'error',
        session_id: SID,
      })
    })
    act(() => { useChatStore.getState().handleFrame({ type: 'done', session_id: SID }) })

    await act(async () => {
      render(<ChatScreen />)
    })

    expect(screen.getByText('Failed')).toBeInTheDocument()
    expect(screen.queryByText('Done')).toBeNull()
  })

  it('a successful tool call with the same shape (no `error` string) still renders "Done"', async () => {
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_start',
        call_id: 'tc_ok',
        tool: 'run_diagnostic',
        params: {},
        session_id: SID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_result',
        call_id: 'tc_ok',
        tool: 'run_diagnostic',
        result: 'all checks passed',
        status: 'success',
        session_id: SID,
      })
    })
    act(() => { useChatStore.getState().handleFrame({ type: 'done', session_id: SID }) })

    await act(async () => {
      render(<ChatScreen />)
    })

    expect(screen.getByText('Done')).toBeInTheDocument()
    expect(screen.queryByText('Failed')).toBeNull()
  })
})

describe('ChatScreen replay wiring — #617 browser.* branch (BrowserToolReplayBlock)', () => {
  it('a failed browser.click replay renders the error dot + "Failed" label', async () => {
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_start',
        call_id: 'tc_browser_fail',
        tool: 'browser.click',
        params: { selector: '#submit' },
        session_id: SID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_result',
        call_id: 'tc_browser_fail',
        tool: 'browser.click',
        result: { ok: false },
        status: 'error',
        session_id: SID,
      })
    })
    act(() => { useChatStore.getState().handleFrame({ type: 'done', session_id: SID }) })

    let container!: HTMLElement
    await act(async () => {
      const result = render(<ChatScreen />)
      container = result.container
    })

    expect(screen.getByText('Failed')).toBeInTheDocument()
    expect(container.querySelector('.bg-\\[var\\(--color-error\\)\\]')).not.toBeNull()
  })
})

describe('ChatScreen replay wiring — #617 web_serve branch (WebServeBlock)', () => {
  it('a failed web_serve replay (null result, status:"error") renders "Failed", not "Done"', async () => {
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_start',
        call_id: 'tc_serve_fail',
        tool: 'web_serve',
        params: { path: '/app' },
        session_id: SID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_result',
        call_id: 'tc_serve_fail',
        tool: 'web_serve',
        result: null,
        status: 'error',
        session_id: SID,
      })
    })
    act(() => { useChatStore.getState().handleFrame({ type: 'done', session_id: SID }) })

    await act(async () => {
      render(<ChatScreen />)
    })

    expect(screen.getByText('Failed')).toBeInTheDocument()
    expect(screen.queryByText('Done')).toBeNull()
  })
})

describe('ChatScreen replay wiring — F2 cancelled replay (replayPartStatus)', () => {
  // Cancellation is not producible via a plain tool_call_result WS frame (the
  // wire ToolCallResultFrame.status enum is only "success"|"error" — a
  // running call flips to 'cancelled' in the store only via the
  // interrupt/session-close cleanup path, per store/chat.ts). A message
  // already carrying a 'cancelled' PositionedToolCall — exactly what a REST
  // history load / session replay hands ChatScreen — exercises the SAME
  // VirtualAssistantMessageRow rendering code this fix touches, without
  // needing to replicate that whole interrupt pipeline.
  it('a replayed CANCELLED browser.click call renders "Cancelled", not "Done" (BrowserToolReplayBlock)', async () => {
    const now = new Date().toISOString()
    const assistantMsg: ChatMessage = {
      id: 'msg_cancelled_browser',
      role: 'assistant',
      content: '',
      timestamp: now,
      status: 'done',
      tool_calls: [
        { id: 'tc_cancelled_browser', tool: 'browser.click', params: { selector: '#x' }, status: 'cancelled' } as PositionedToolCall,
      ],
    }
    seedBucket([assistantMsg])

    await act(async () => {
      render(<ChatScreen />)
    })

    expect(screen.getByText('Cancelled')).toBeInTheDocument()
    expect(screen.queryByText('Done')).toBeNull()
    expect(screen.queryByText('Failed')).toBeNull()
  })

  it('a replayed CANCELLED web_serve call renders "Cancelled", not "Done" (WebServeBlock)', async () => {
    const now = new Date().toISOString()
    const assistantMsg: ChatMessage = {
      id: 'msg_cancelled_serve',
      role: 'assistant',
      content: '',
      timestamp: now,
      status: 'done',
      tool_calls: [
        { id: 'tc_cancelled_serve', tool: 'web_serve', params: { path: '/app' }, status: 'cancelled' } as PositionedToolCall,
      ],
    }
    seedBucket([assistantMsg])

    await act(async () => {
      render(<ChatScreen />)
    })

    expect(screen.getByText('Cancelled')).toBeInTheDocument()
    expect(screen.queryByText('Done')).toBeNull()
  })

  it('a replayed CANCELLED generic tool call renders "Cancelled" via GenericToolCall (isCancelledStatus sees the derived status)', async () => {
    const now = new Date().toISOString()
    const assistantMsg: ChatMessage = {
      id: 'msg_cancelled_generic',
      role: 'assistant',
      content: '',
      timestamp: now,
      status: 'done',
      tool_calls: [
        { id: 'tc_cancelled_generic', tool: 'run_diagnostic', params: {}, status: 'cancelled' } as PositionedToolCall,
      ],
    }
    seedBucket([assistantMsg])

    await act(async () => {
      render(<ChatScreen />)
    })

    expect(screen.getByText('Cancelled')).toBeInTheDocument()
    expect(screen.queryByText('Done')).toBeNull()
  })
})

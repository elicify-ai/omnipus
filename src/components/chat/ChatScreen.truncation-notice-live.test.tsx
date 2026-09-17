/**
 * ChatScreen.truncation-notice-live.test.tsx — ADR-087 D2, finding #10.
 *
 * `ChatScreen.truncation-notice.test.tsx` (sibling file) proves the render
 * sites correctly consume a ChatMessage that already carries
 * `truncated`/`truncationReason` — but every message it seeds is
 * hand-authored with those fields already set, the same shape a REPLAY
 * (`ReplayMessageFrame`) or cold-load (`Message`) produces. It never drives
 * the actual live `done` frame reducer, so it could not have caught finding
 * #10: a turn cut off at the output limit WHILE THE USER IS WATCHING
 * rendered no notice until a reload or reconnect replayed it back in.
 *
 * This file closes that gap: it seeds a genuinely *live* streaming bubble
 * (`isStreaming: true`, no truncated fields at all — nothing pre-baked),
 * renders `ChatScreen`, then dispatches a REAL `done` WS frame through
 * `useChatStore.getState().handleFrame(...)` exactly as the WebSocket
 * manager (`src/lib/ws.ts`) would on a genuine truncation. No reload, no
 * `replay_message` frame, no remount — the assertion is that the DOM
 * updates in place from the live frame alone.
 *
 * `InterruptedMessageMarkers` (src/components/chat/ChatScreen.tsx) is the
 * target: ADR-087 D1's own table names it "the reliable E2E-detectable
 * fallback" specifically because the true in-bubble `AssistantMessage`
 * render path runs through AssistantUI's `ThreadPrimitive.Messages`, which
 * this suite's (and the sibling suite's) mock scaffold stubs to `() => null`
 * — the same scaffold `ChatScreen.truncation-notice.test.tsx` already uses
 * for the identical reason. `InterruptedMessageMarkers` is not gated behind
 * that mock: it reads `useChatStore((s) => s.messages)` directly, so it is
 * the correct, ADR-designated target for asserting the live no-reload path
 * in this harness. The mock scaffold below is copied verbatim from the
 * sibling file to keep both suites exercising the same render tree.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, act, screen } from '@testing-library/react'
import * as React from 'react'
import { useChatStore, makeBucketMessages } from '@/store/chat'
import type { ChatMessage } from '@/store/chat'
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
vi.mock('./tools/GenericToolCall', () => ({
  GenericToolCall: ({ toolName }: { toolName: string }) =>
    React.createElement('div', { 'data-testid': 'tool-call-badge' }, toolName),
}))
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

const SID = 'test-session-truncation-notice-live'

/** Seed a genuinely LIVE bucket: a streaming placeholder with NO
 * truncated/truncationReason pre-baked — those must arrive via the `done`
 * frame reducer, not the fixture. */
function seedLiveStreamingBucket(content: string): void {
  const placeholder: ChatMessage = {
    id: 'live-bubble-1',
    role: 'assistant',
    content,
    timestamp: new Date().toISOString(),
    status: 'streaming',
    isStreaming: true,
    agentId: 'agent-1',
  }
  const bucket = makeBucketMessages([placeholder])
  useChatStore.setState((s) => ({
    ...s,
    sessionsById: {
      [SID]: {
        ...((s.sessionsById ?? {})[SID] ?? {}),
        ...bucket,
        isStreaming: true,
        isReplaying: false,
        replayCompletedForSession: null,
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
    messages: [placeholder],
    isStreaming: true,
    isReplaying: false,
    replayCompletedForSession: null,
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
  // Forces PlainMessageList (ResizeObserver-unavailable fallback) — same
  // technique as the sibling replay-path suite.
  ;(globalThis as { ResizeObserver?: unknown }).ResizeObserver = undefined

  useSessionStore.setState({ activeSessionId: SID, activeAgentId: 'agent-1' })
  act(() => {
    useChatStore.getState().resetSession()
  })
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('ADR-087 D2 (finding #10) — live `done` frame renders the truncation notice with no reload', () => {
  it('a live done frame with stats.truncated + max_output_tokens shows "(cut off at the output limit)" without reload', async () => {
    seedLiveStreamingBucket('Here is a long answer that is about to be cut off')

    await act(async () => {
      render(<ChatScreen />)
    })

    // Before the done frame: no cutoff notice yet — the turn is still live.
    expect(screen.queryByText('(cut off at the output limit)')).toBeNull()

    // The real reducer path — same shape src/lib/ws.ts hands to handleFrame
    // on a genuine live WebSocket `done` frame.
    await act(async () => {
      useChatStore.getState().handleFrame({
        type: 'done',
        session_id: SID,
        stats: { tokens: 128, cost: 0.0009, duration_ms: 640, truncated: true, truncation_reason: 'max_output_tokens' },
      })
    })

    expect(screen.getByTestId('truncated-marker')).toBeInTheDocument()
    expect(screen.getAllByText('(cut off at the output limit)').length).toBeGreaterThan(0)
    expect(screen.queryByText('(interrupted)')).toBeNull()
  })

  it('a live done frame with stats.truncated + cancelled shows "(interrupted)" only, live, no reload', async () => {
    seedLiveStreamingBucket('Working on it and then')

    await act(async () => {
      render(<ChatScreen />)
    })

    await act(async () => {
      useChatStore.getState().handleFrame({
        type: 'done',
        session_id: SID,
        stats: { tokens: 12, cost: 0.0001, truncated: true, truncation_reason: 'cancelled' },
      })
    })

    expect(screen.getByTestId('interrupted-marker')).toBeInTheDocument()
    expect(screen.queryByTestId('truncated-marker')).toBeNull()
    expect(screen.queryByText('(cut off at the output limit)')).toBeNull()
  })

  it('a plain live done frame (no truncation) renders no suffix at all', async () => {
    seedLiveStreamingBucket('A perfectly ordinary complete answer.')

    await act(async () => {
      render(<ChatScreen />)
    })

    await act(async () => {
      useChatStore.getState().handleFrame({
        type: 'done',
        session_id: SID,
        stats: { tokens: 20, cost: 0.0001 },
      })
    })

    expect(screen.queryByTestId('truncated-marker')).toBeNull()
    expect(screen.queryByTestId('interrupted-marker')).toBeNull()
    expect(screen.queryByText('(cut off at the output limit)')).toBeNull()
    expect(screen.queryByText('(interrupted)')).toBeNull()
  })
})

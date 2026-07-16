// ChatScreen.subagent-spans-live-visibility.test.tsx — item 8b/8c (2026-07-16
// fix wave): SubagentSpansRenderer (ChatScreen.tsx, mounted inside the LIVE
// AssistantMessage() render via real ThreadPrimitive.Messages) carries its
// own shouldRenderSubagentSpan gate (Fix 2, 2026-07-16) — but every OTHER
// ChatScreen.*.test.tsx suite mocks ThreadPrimitive.Messages to `() => null`
// to isolate the historical (VirtualAssistantMessageRow) render path, so
// this gate has never actually been exercised on the STREAMING/live path by
// a test, despite SubagentBlock.test.tsx and ChatScreen.delegation-thread-
// visibility.test.tsx both covering the historical side.
//
// Harness mirrors ChatScreen.assistant-empty-bubble.test.tsx: '@assistant-ui/
// react' is left UNMOCKED and ChatScreen mounts inside a real
// AssistantRuntimeProvider (via the real useOmnipusRuntime hook, skipping
// OmnipusRuntimeProvider's WsLifecycle so no real WebSocket is attempted).
// Unlike that file, `./SubagentBlock` is mocked down to a DETECTABLE stub
// (exposing the span's id/status), not `() => null` — this file's whole
// point is asserting whether SubagentSpansRenderer chooses to mount a
// SubagentBlock at all for a given span.

import { describe, it, expect, vi } from 'vitest'
import { render, screen, act } from '@testing-library/react'
import * as React from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { AssistantRuntimeProvider } from '@assistant-ui/react'
import { useChatStore, makeBucketMessages } from '@/store/chat'
import type { ChatMessage, SubagentSpan } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import { useOmnipusRuntime } from '@/lib/omnipus-runtime'

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
vi.stubGlobal('ResizeObserver', ResizeObserverStub)
if (typeof Element !== 'undefined' && !Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = function () {}
}
if (typeof Element !== 'undefined' && !Element.prototype.scrollTo) {
  Element.prototype.scrollTo = function () {}
}

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([
      { id: 'agent-1', name: 'Mia', color: '#123456', icon: null },
      { id: 'ray', name: 'Ray', type: 'Subagent', locked: false, status: 'active', color: '#4488ff', icon: 'compass' },
    ]),
    fetchSessionMessages: vi.fn().mockResolvedValue([]),
    fetchCommands: vi.fn().mockResolvedValue([]),
    fetchSkills: vi.fn().mockResolvedValue([]),
    uploadFiles: vi.fn(),
    fetchProviders: vi.fn().mockResolvedValue([]),
  }
})

vi.mock('@tanstack/react-router', () => ({
  useRouter: () => ({ navigate: vi.fn() }),
  useSearch: () => ({}),
  Link: ({ children }: { children: React.ReactNode }) => children,
}))

vi.mock('@/assets/logo/omnipus-avatar.svg?url', () => ({ default: 'omnipus-avatar.svg' }))
vi.mock('./RateLimitIndicator', () => ({ RateLimitIndicator: () => null }))
vi.mock('./ActivityBar', () => ({ ActivityBar: () => null }))
vi.mock('./tools/GenericToolCall', () => ({ GenericToolCall: () => null }))
vi.mock('@/components/shared/IconRenderer', () => ({ IconRenderer: () => null }))
vi.mock('./composer/AgentPicker', () => ({ AgentPicker: () => null }))
vi.mock('./composer/ModelPicker', () => ({ ModelPicker: () => null }))
vi.mock('./composer/TokenCounter', () => ({ TokenCounter: () => null }))
vi.mock('./markdown-text', () => ({
  MarkdownText: () => React.createElement('span', { 'data-testid': 'assistant-text' }),
}))

// Detectable stub (unlike the historical-path sibling's own tests) — exposes
// the span's id/status so this file can assert PRESENCE/ABSENCE of the card
// without depending on SubagentBlock's own internal markup.
vi.mock('./SubagentBlock', () => ({
  SubagentBlock: ({ span }: { span: SubagentSpan }) =>
    React.createElement('div', {
      'data-testid': 'subagent-block-stub',
      'data-span-id': span.spanId,
      'data-status': span.status,
    }),
}))

import { ChatScreen } from './ChatScreen'

const SID = 'sess_subagent_spans_live_visibility'

function Providers({ children }: { children: React.ReactNode }) {
  const queryClientRef = React.useRef<QueryClient | null>(null)
  if (!queryClientRef.current) {
    queryClientRef.current = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  }
  const runtime = useOmnipusRuntime()
  return (
    <QueryClientProvider client={queryClientRef.current}>
      <AssistantRuntimeProvider runtime={runtime}>{children}</AssistantRuntimeProvider>
    </QueryClientProvider>
  )
}

function runningSpan(overrides: Partial<SubagentSpan> = {}): SubagentSpan {
  return {
    spanId: 'span_live_1',
    parentCallId: 'call_live_1',
    taskLabel: 'digging into logs',
    steps: [],
    status: 'running',
    ...overrides,
  } as SubagentSpan
}

/** Seeds a user message followed by a streaming assistant placeholder carrying a live span. */
function seedStreamingAssistantWithSpan(span: SubagentSpan): void {
  const userMsg: ChatMessage = {
    id: 'msg_user',
    role: 'user',
    content: 'hi',
    timestamp: new Date().toISOString(),
    status: 'done',
  }
  const streamingMsg: ChatMessage = {
    id: 'msg_assistant',
    role: 'assistant',
    content: 'Delegating now.',
    timestamp: new Date().toISOString(),
    status: 'streaming',
    isStreaming: true,
    spans: [span],
  }
  const allMessages = [userMsg, streamingMsg]
  const bucket = makeBucketMessages(allMessages)
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
    messages: allMessages,
    isStreaming: true,
    isReplaying: false,
    replayCompletedForSession: SID,
  }))
  useSessionStore.setState({ activeSessionId: SID, activeAgentId: 'agent-1' })
  useConnectionStore.setState({ connection: null, isConnected: true, connectionError: null })
  act(() => {
    useChatPreferencesStore.setState({ verboseChatEnabled: false })
  })
}

describe('SubagentSpansRenderer — live/streaming span-site gating (item 8b, Fix 2)', () => {
  it('hides a running span by default on the LIVE render path (verboseChatEnabled false)', async () => {
    seedStreamingAssistantWithSpan(runningSpan())

    await act(async () => {
      render(
        <Providers>
          <ChatScreen />
        </Providers>,
      )
    })

    expect(screen.queryByTestId('subagent-block-stub')).not.toBeInTheDocument()
  })

  it('shows the span once verbose chat is enabled, on the LIVE render path', async () => {
    seedStreamingAssistantWithSpan(runningSpan())
    act(() => {
      useChatPreferencesStore.setState({ verboseChatEnabled: true })
    })

    await act(async () => {
      render(
        <Providers>
          <ChatScreen />
        </Providers>,
      )
    })

    const stub = screen.getByTestId('subagent-block-stub')
    expect(stub).toHaveAttribute('data-span-id', 'span_live_1')
    expect(stub).toHaveAttribute('data-status', 'running')
  })
})

describe('SubagentSpansRenderer — item 8c: live verbose toggle within a single render', () => {
  it('the card appears when verbose is toggled on, and disappears when toggled back off, without remounting', async () => {
    seedStreamingAssistantWithSpan(runningSpan())

    await act(async () => {
      render(
        <Providers>
          <ChatScreen />
        </Providers>,
      )
    })

    expect(screen.queryByTestId('subagent-block-stub')).not.toBeInTheDocument()

    act(() => {
      useChatPreferencesStore.setState({ verboseChatEnabled: true })
    })
    expect(screen.getByTestId('subagent-block-stub')).toBeInTheDocument()

    act(() => {
      useChatPreferencesStore.setState({ verboseChatEnabled: false })
    })
    expect(screen.queryByTestId('subagent-block-stub')).not.toBeInTheDocument()
  })
})

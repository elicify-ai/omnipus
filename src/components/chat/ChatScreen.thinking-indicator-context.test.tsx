// Founder 2026-10-21: one random fallback per status appearance, held fixed.
// Real AssistantUI/live runtime. Hidden tool commands never become status copy.
import { describe, it, expect, vi } from 'vitest'
import { render, within } from '@testing-library/react'
import * as React from 'react'
import { act } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { AssistantRuntimeProvider, useMessagePartText } from '@assistant-ui/react'
import { useChatStore, makeBucketMessages } from '@/store/chat'
import type { ChatMessage } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
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
// jsdom has no layout engine and doesn't implement scrollTo — the real
// (unmocked) ThreadPrimitive.Viewport's autoscroll effect calls it on a rAF
// callback that can fire after the test body returns, otherwise surfacing
// as an unhandled rejection.
if (typeof Element !== 'undefined' && !Element.prototype.scrollTo) {
  Element.prototype.scrollTo = function () {}
}

// '@assistant-ui/react' is intentionally NOT mocked in this file — see header.

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([
      { figure: 'Omnipus', role: 'general', id: 'agent-1', name: 'Mia', color: '#8E9196', icon: null },
      { figure: 'Omnipus', role: 'general', id: 'agent-ray', name: 'Ray', color: '#8E9196', icon: null },
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
vi.mock('./composer/ModelPicker', () => ({ ModelPicker: () => null }))
vi.mock('./composer/TokenCounter', () => ({ TokenCounter: () => null }))
vi.mock('./markdown-text', () => ({
  MarkdownText: () => {
    const { text } = useMessagePartText()
    return React.createElement('span', { 'data-testid': 'assistant-text' }, text)
  },
}))

import { ChatScreen } from './ChatScreen'

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

const GENERIC_THINKING_RE =
  /^(Thinking…|Working on it…|Composing a response…|Processing your request…|Analyzing…|Considering the details…|Piecing it together…|Reasoning it through…|Working through this…|Gathering my thoughts…|Figuring out the approach…|Reviewing the context…|Drafting a response…|Making sense of it…|Weighing the options…)$/

/**
 * Seeds a user message followed by a streaming assistant placeholder whose
 * only content is a single live tool call — mirrors
 * ChatScreen.assistant-empty-bubble.test.tsx's
 * seedStreamingAssistantWithHiddenToolCall, generalized to any tool/params.
 */
function seedStreamingAssistantWithToolCall(
  sid: string,
  toolCallId: string,
  tool: string,
  params: Record<string, unknown>,
  status: 'running' | 'success' | 'error' | 'cancelled',
): void {
  const userMsg: ChatMessage = {
    id: `${sid}_user`,
    role: 'user',
    content: 'hi',
    timestamp: new Date().toISOString(),
    status: 'done',
  }
  const streamingMsg: ChatMessage = {
    id: `${sid}_assistant`,
    role: 'assistant',
    content: '',
    timestamp: new Date().toISOString(),
    status: 'streaming',
    isStreaming: true,
  }
  const allMessages = [userMsg, streamingMsg]
  const bucket = makeBucketMessages(allMessages)
  const liveToolCalls = {
    [toolCallId]: {
      id: toolCallId,
      call_id: toolCallId,
      tool,
      params,
      status,
      ...(status === 'success' ? { result: { ok: true } } : {}),
    },
  }
  useChatStore.setState((s) => ({
    ...s,
    sessionsById: {
      [sid]: {
        ...((s.sessionsById ?? {})[sid] ?? {}),
        ...bucket,
        isStreaming: true,
        isReplaying: false,
        replayCompletedForSession: sid,
        toolCalls: liveToolCalls,
        toolCallOrder: [toolCallId],
        textAtToolCallStart: {},
        sessionTokens: 0,
        sessionCost: 0,
        rateLimitEvent: null,
        lastUserMessageAt: null,
        cancelStage: null,
        lastReceivedEventTime: null,
        trimmedCount: 0,
      },
    },
    messages: allMessages,
    isStreaming: true,
    isReplaying: false,
    replayCompletedForSession: sid,
    // useOmnipusRuntime's convertMessage interleaves LIVE tool calls into
    // message.content from these ROOT-level ("foreground") store fields —
    // NOT from sessionsById[sid].toolCalls (that copy above only feeds
    // components that read the active session's bucket directly). Both
    // must be seeded for the live AssistantMessage()/InlineThinkingIndicator
    // path (real useOmnipusRuntime, unmocked in this file) to see the call
    // as a real 'tool-call' part of message.content.
    toolCalls: liveToolCalls,
    toolCallOrder: [toolCallId],
    textAtToolCallStart: {},
  }))
  useSessionStore.setState({ activeSessionId: sid, activeAgentId: 'agent-1' })
  useConnectionStore.setState({ connection: null, isConnected: true, connectionError: null })
}

/** Seeds a user message followed by a plain streaming assistant placeholder — no tool calls. */
function seedPlainStreamingAssistant(sid: string): void {
  const userMsg: ChatMessage = {
    id: `${sid}_user`,
    role: 'user',
    content: 'hi',
    timestamp: new Date().toISOString(),
    status: 'done',
  }
  const streamingMsg: ChatMessage = {
    id: `${sid}_assistant`,
    role: 'assistant',
    content: '',
    timestamp: new Date().toISOString(),
    status: 'streaming',
    isStreaming: true,
  }
  const allMessages = [userMsg, streamingMsg]
  const bucket = makeBucketMessages(allMessages)
  useChatStore.setState((s) => ({
    ...s,
    sessionsById: {
      [sid]: {
        ...((s.sessionsById ?? {})[sid] ?? {}),
        ...bucket,
        isStreaming: true,
        isReplaying: false,
        replayCompletedForSession: sid,
        toolCalls: {},
        toolCallOrder: [],
        textAtToolCallStart: {},
        sessionTokens: 0,
        sessionCost: 0,
        rateLimitEvent: null,
        lastUserMessageAt: null,
        cancelStage: null,
        lastReceivedEventTime: null,
        trimmedCount: 0,
      },
    },
    messages: allMessages,
    isStreaming: true,
    isReplaying: false,
    replayCompletedForSession: sid,
  }))
  useSessionStore.setState({ activeSessionId: sid, activeAgentId: 'agent-1' })
  useConnectionStore.setState({ connection: null, isConnected: true, connectionError: null })
}

describe('ChatScreen thinking indicator — fixed random fallback', () => {
  it('selects its initial phrase from the pool when the status appears', async () => {
    const sid = 'sess_thinking_first_beat'
    seedPlainStreamingAssistant(sid)

    let container!: HTMLElement
    await act(async () => {
      const result = render(
        <Providers>
          <ChatScreen />
        </Providers>,
      )
      container = result.container
    })

    const bubble = container.querySelector('[data-testid="assistant-message"]') as HTMLElement
    expect(bubble).toBeTruthy()
    expect(within(bubble).getByTestId('reply-status-fallback')).toHaveTextContent(GENERIC_THINKING_RE)
  })

  it('holds its chosen phrase across former two-second ticks', async () => {
    seedPlainStreamingAssistant('sess_thinking_fixed')
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    const random = vi.spyOn(Math, 'random').mockReturnValue(5.5 / 15)
    try {
      let container!: HTMLElement
      await act(async () => { container = render(<Providers><ChatScreen /></Providers>).container })
      const bubble = container.querySelector('[data-testid="assistant-message"]') as HTMLElement
      expect(within(bubble).getByTestId('reply-status-fallback')).toHaveTextContent('Considering the details…')
      random.mockClear()
      let alternate = false
      random.mockImplementation(() => { alternate = !alternate; return alternate ? 0.2 : 0.6 })
      await act(async () => { await vi.advanceTimersByTimeAsync(8000) })
      expect(within(bubble).getByTestId('reply-status-fallback')).toHaveTextContent('Considering the details…')
      expect(random).not.toHaveBeenCalled()
    } finally { random.mockRestore(); vi.useRealTimers() }
  })
})

describe('ChatScreen thinking indicator — hidden background bash', () => {
  it('uses the fixed fallback, not the hidden call description or raw command', async () => {
    const sid = 'sess_thinking_bash_description'
    // 68 characters — long enough to force truncation.
    const description = 'Running the complete cross-platform regression suite before shipping'
    const rawCommand = './scripts/run-full-regression-suite.sh --all-platforms --verbose'
    seedStreamingAssistantWithToolCall(
      sid,
      'tc_bash_description',
      'bash',
      { action: 'run', run_in_background: true, description, command: rawCommand },
      'running',
    )

    let container!: HTMLElement
    await act(async () => {
      const result = render(
        <Providers>
          <ChatScreen />
        </Providers>,
      )
      container = result.container
    })

    const bubble = container.querySelector('[data-testid="assistant-message"]') as HTMLElement
    expect(within(bubble).getByTestId('reply-status-fallback')).toHaveTextContent(GENERIC_THINKING_RE)
    expect(within(bubble).queryByText(description)).not.toBeInTheDocument()
    // The raw command string must NEVER be rendered anywhere in the bubble.
    expect(bubble.textContent).not.toContain(rawCommand)
    expect(bubble.textContent).not.toContain('run-full-regression-suite')
  })

  it('uses the fallback without exposing an unlabelled command', async () => {
    const sid = 'sess_thinking_bash_verb'
    const rawCommand = 'git status --short'
    seedStreamingAssistantWithToolCall(
      sid,
      'tc_bash_verb',
      'bash',
      { action: 'run', run_in_background: true, command: rawCommand },
      'running',
    )

    let container!: HTMLElement
    await act(async () => {
      const result = render(
        <Providers>
          <ChatScreen />
        </Providers>,
      )
      container = result.container
    })

    const bubble = container.querySelector('[data-testid="assistant-message"]') as HTMLElement
    expect(within(bubble).getByTestId('reply-status-fallback')).toHaveTextContent(GENERIC_THINKING_RE)
    expect(bubble.textContent).not.toContain(rawCommand)
    expect(bubble.textContent).not.toContain('--short')
  })

  it('uses the same fallback when neither description nor command is present', async () => {
    const sid = 'sess_thinking_bash_fallback'
    seedStreamingAssistantWithToolCall(
      sid,
      'tc_bash_fallback',
      'bash',
      { action: 'poll', session_id: 'bg-session-1' },
      'running',
    )

    let container!: HTMLElement
    await act(async () => {
      const result = render(
        <Providers>
          <ChatScreen />
        </Providers>,
      )
      container = result.container
    })

    const bubble = container.querySelector('[data-testid="assistant-message"]') as HTMLElement
    expect(within(bubble).getByTestId('reply-status-fallback')).toHaveTextContent(GENERIC_THINKING_RE)
  })
})

// ADR-091 D7/AC-7: a `delegate` 'run' call (the default) is visible
// unconditionally now — it never reaches deriveHiddenRunningToolLabel's
// tool-name branches (the `shouldRenderToolCall` check inside it returns
// early), so the old "Delegating to <name>…" / "Delegating…" specific-label
// tests for that case are deleted along with `deriveDelegateThinkingLabel`
// itself (ChatScreen.tsx). Only `status` (polling) still hides by default,
// and it has no specific-label rule — same generic-pool fallback as
// ToolSearch below.
describe('ChatScreen thinking indicator — hidden delegate status poll falls through to the generic pool', () => {
  it('shows a generic rotating phrase, not a delegate-specific label', async () => {
    const sid = 'sess_thinking_delegate_status'
    seedStreamingAssistantWithToolCall(
      sid,
      'tc_delegate_status',
      'delegate',
      { action: 'status', call_id: 'tc_prior_dispatch' },
      'running',
    )

    let container!: HTMLElement
    await act(async () => {
      const result = render(
        <Providers>
          <ChatScreen />
        </Providers>,
      )
      container = result.container
    })

    const bubble = container.querySelector('[data-testid="assistant-message"]') as HTMLElement
    expect(within(bubble).getByText(GENERIC_THINKING_RE)).toBeInTheDocument()
  })
})

describe('ChatScreen thinking indicator — ToolSearch falls through to the generic pool', () => {
  it('shows a generic rotating phrase, not a ToolSearch-specific label', async () => {
    const sid = 'sess_thinking_toolsearch'
    seedStreamingAssistantWithToolCall(
      sid,
      'tc_toolsearch',
      'ToolSearch',
      { query: 'select:Read' },
      'running',
    )

    let container!: HTMLElement
    await act(async () => {
      const result = render(
        <Providers>
          <ChatScreen />
        </Providers>,
      )
      container = result.container
    })

    const bubble = container.querySelector('[data-testid="assistant-message"]') as HTMLElement
    expect(within(bubble).getByText(GENERIC_THINKING_RE)).toBeInTheDocument()
  })
})

// Context-aware, randomized thinking indicator — coverage for
// InlineThinkingIndicator / ThinkingIndicator / deriveHiddenRunningToolLabel
// (ChatScreen.tsx). Mirrors ChatScreen.assistant-empty-bubble.test.tsx's
// harness: '@assistant-ui/react' is deliberately left UNMOCKED and ChatScreen
// is mounted inside a real AssistantRuntimeProvider (via the real
// useOmnipusRuntime hook, skipping OmnipusRuntimeProvider's WsLifecycle) so
// these tests exercise the actual live AssistantMessage()/
// InlineThinkingIndicator() render path — every OTHER ChatScreen test file
// mocks ThreadPrimitive.Messages to null, which would make this feature
// unreachable.
//
// BDD:
//   Given: a fresh streaming assistant message with no tool calls
//   Then:  the thinking indicator's first shown phrase is always 'Thinking…'
//   Given: the generic rotating pool is showing
//   Then:  each 2s tick picks a random phrase, never immediately repeating
//   Given: the only in-progress step is a hidden background `bash` call
//   Then:  the indicator shows a stable, context-specific label derived from
//          the call's `description` (capped) or a command-verb mapping —
//          and NEVER the raw command string
//   Given: the only in-progress step is a hidden `delegate` 'run' call
//   Then:  the indicator shows "Delegating to <name>…" when the target
//          agent's name resolves, else a bare "Delegating…"
//   Given: the only in-progress step is a hidden `ToolSearch` call
//   Then:  the indicator falls through to the generic rotating pool (no
//          special-cased label)

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
      { id: 'agent-1', name: 'Mia', color: '#123456', icon: null },
      { id: 'agent-ray', name: 'Ray', color: '#654321', icon: null },
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
vi.mock('./SubagentBlock', () => ({ SubagentBlock: () => null }))
vi.mock('./ActivityBar', () => ({ ActivityBar: () => null }))
vi.mock('./tools/GenericToolCall', () => ({ GenericToolCall: () => null }))
vi.mock('@/components/shared/IconRenderer', () => ({ IconRenderer: () => null }))
vi.mock('./composer/AgentPicker', () => ({ AgentPicker: () => null }))
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
        spanByParentCallId: {},
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
        spanByParentCallId: {},
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

describe('ChatScreen thinking indicator — first beat and rotation', () => {
  it('first shown phrase is always "Thinking…"', async () => {
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
    // Deterministic opening beat — no timer has ticked yet.
    expect(within(bubble).getByText('Thinking…')).toBeInTheDocument()
  })

  it('rotates to a random phrase on each 2s tick and never immediately repeats', async () => {
    const sid = 'sess_thinking_rotation'
    seedPlainStreamingAssistant(sid)

    vi.useFakeTimers()
    const randomSpy = vi.spyOn(Math, 'random')
    try {
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
      expect(within(bubble).getByText('Thinking…')).toBeInTheDocument()

      // First tick (t=2000ms): force index 5 ('Considering the details…') —
      // differs from the current 'Thinking…' (index 0), so the picker
      // accepts it on the first Math.random() call.
      randomSpy.mockReturnValueOnce(5.5 / 15)
      await act(async () => {
        await vi.advanceTimersByTimeAsync(2000)
      })
      expect(within(bubble).getByText('Considering the details…')).toBeInTheDocument()

      // Second tick (t=4000ms): force Math.random() to first propose the
      // SAME index as the currently-shown phrase (5 — an immediate repeat),
      // then a different one (index 2, 'Composing a response…'). Proves the
      // no-immediate-repeat retry actually engages rather than the first
      // pick being accepted unconditionally.
      randomSpy.mockReturnValueOnce(5.5 / 15).mockReturnValueOnce(2.5 / 15)
      await act(async () => {
        await vi.advanceTimersByTimeAsync(2000)
      })
      expect(within(bubble).getByText('Composing a response…')).toBeInTheDocument()
      expect(randomSpy).toHaveBeenCalled()
    } finally {
      randomSpy.mockRestore()
      vi.useRealTimers()
    }
  })
})

describe('ChatScreen thinking indicator — hidden background bash', () => {
  it('shows the call\'s own description (capped to one line / ~48 chars), not the raw command', async () => {
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
    // Spec rule: first line, capped at 48 chars, ellipsis appended when cut.
    const expectedLabel = `${description.slice(0, 48).trimEnd()}…`
    expect(within(bubble).getByText(expectedLabel)).toBeInTheDocument()
    // The raw command string must NEVER be rendered anywhere in the bubble.
    expect(bubble.textContent).not.toContain(rawCommand)
    expect(bubble.textContent).not.toContain('run-full-regression-suite')
  })

  it('derives a verb from the command\'s first token when no description is given', async () => {
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
    expect(within(bubble).getByText('Running git…')).toBeInTheDocument()
    expect(bubble.textContent).not.toContain(rawCommand)
    expect(bubble.textContent).not.toContain('--short')
  })

  it('falls back to "Working in the background…" when neither description nor command is present', async () => {
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
    expect(within(bubble).getByText('Working in the background…')).toBeInTheDocument()
  })
})

describe('ChatScreen thinking indicator — hidden delegate', () => {
  it('shows "Delegating to <name>…" when the target agent resolves', async () => {
    const sid = 'sess_thinking_delegate_named'
    seedStreamingAssistantWithToolCall(
      sid,
      'tc_delegate_named',
      'delegate',
      { action: 'run', async: true, agent_id: 'agent-ray', task: 'investigate the flaky test' },
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
    // The mocked fetchAgents() resolves asynchronously — findByText waits
    // for the agents query to settle and the label to update from the bare
    // "Delegating…" fallback to the name-resolved text.
    expect(await within(bubble).findByText('Delegating to Ray…')).toBeInTheDocument()
  })

  it('falls back to a bare "Delegating…" when the target agent id is absent — never invents a name', async () => {
    const sid = 'sess_thinking_delegate_unnamed'
    seedStreamingAssistantWithToolCall(
      sid,
      'tc_delegate_unnamed',
      'delegate',
      { action: 'run', async: true, task: 'do the thing' },
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
    expect(within(bubble).getByText('Delegating…')).toBeInTheDocument()
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

// Goal-aware thinking indicator — operator-reported UX fix, 2026-09-08
// (GX-C). Mirrors ChatScreen.thinking-indicator-context.test.tsx's harness
// exactly ('@assistant-ui/react' left UNMOCKED, ChatScreen mounted inside a
// real AssistantRuntimeProvider via the real useOmnipusRuntime hook) — see
// that file's header comment for why every other ChatScreen test file
// (which mocks ThreadPrimitive.Messages to null) cannot reach this surface.
//
// BDD:
//   Given: a goal is active and its record is still empty, no tool call running
//   Then:  the indicator reads "Framing your goal" (never the generic pool)
//   Given: a goal is active, record empty, and `set_goal` is the running step
//   Then:  the indicator reads "Setting acceptance criteria"
//   Given: the goal's record has already been populated (criteria present)
//   Then:  the override does not apply — falls back to the generic pool

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
import type { GoalStatusFrame } from '@/lib/api/generated/asyncapi-types'

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

// '@assistant-ui/react' is intentionally NOT mocked in this file — see header.

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([{ id: 'agent-1', name: 'Mia', color: '#123456', icon: null }]),
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

function makeGoalFrame(overrides: Partial<GoalStatusFrame> = {}): GoalStatusFrame {
  return {
    type: 'goal_status',
    session_id: 'placeholder',
    goal_id: 'goal_indicator_test',
    condition: 'ship the release notes',
    round: 0,
    max_rounds: 20,
    latest_reason: '',
    active_loops: 1,
    cap: 16,
    state: 'active',
    ...overrides,
  }
}

/**
 * Seeds a streaming assistant placeholder, optionally with one live tool
 * call, AND a goalStatus frame on both the session bucket and the
 * foreground selector (InlineThinkingIndicator reads the foreground field —
 * see chat.ts's bucketToForeground). goalFrame: null means no active goal
 * (control case).
 */
function seedGoalAwareStreamingAssistant(
  sid: string,
  goalFrame: GoalStatusFrame | null,
  liveCall?: { toolCallId: string; tool: string; params: Record<string, unknown> },
): void {
  const userMsg: ChatMessage = {
    id: `${sid}_user`,
    role: 'user',
    content: '/goal ship the release notes',
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
  const liveToolCalls = liveCall
    ? {
        [liveCall.toolCallId]: {
          id: liveCall.toolCallId,
          call_id: liveCall.toolCallId,
          tool: liveCall.tool,
          params: liveCall.params,
          status: 'running' as const,
        },
      }
    : {}
  const toolCallOrder = liveCall ? [liveCall.toolCallId] : []
  const frame = goalFrame ? { ...goalFrame, session_id: sid } : null

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
        toolCallOrder,
        textAtToolCallStart: {},
        sessionTokens: 0,
        sessionCost: 0,
        rateLimitEvent: null,
        lastUserMessageAt: null,
        cancelStage: null,
        lastReceivedEventTime: null,
        spanByParentCallId: {},
        trimmedCount: 0,
        goalStatus: frame,
      },
    },
    messages: allMessages,
    isStreaming: true,
    isReplaying: false,
    replayCompletedForSession: sid,
    toolCalls: liveToolCalls,
    toolCallOrder,
    textAtToolCallStart: {},
    goalStatus: frame,
  }))
  useSessionStore.setState({ activeSessionId: sid, activeAgentId: 'agent-1' })
  useConnectionStore.setState({ connection: null, isConnected: true, connectionError: null })
}

describe('ChatScreen thinking indicator — goal-aware override (operator UX fix, 2026-09-08)', () => {
  it('shows "Framing your goal" while a goal is active with an empty record and nothing is running yet', async () => {
    const sid = 'sess_goal_framing'
    seedGoalAwareStreamingAssistant(sid, makeGoalFrame())

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
    expect(within(bubble).getByText('Framing your goal')).toBeInTheDocument()
  })

  it('shows "Setting acceptance criteria" while set_goal is the running step and the record is still empty', async () => {
    const sid = 'sess_goal_setting_criteria'
    seedGoalAwareStreamingAssistant(sid, makeGoalFrame(), {
      toolCallId: 'tc_set_goal_1',
      tool: 'set_goal',
      params: { mode: 'register', definition: 'Ship the release notes', criteria: [] },
    })

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
    expect(within(bubble).getByText('Setting acceptance criteria')).toBeInTheDocument()
  })

  it('falls back to the generic rotating pool once the goal record is populated (criteria present)', async () => {
    const sid = 'sess_goal_record_populated'
    seedGoalAwareStreamingAssistant(
      sid,
      makeGoalFrame({
        criteria: [{ id: 'c1', kind: 'prose', text: 'release notes are published', judgment: 'boolean', status: 'pending', author: { kind: 'agent', id: 'tester' } }],
      }),
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

  it('falls back to the generic rotating pool when there is no active goal at all', async () => {
    const sid = 'sess_goal_none'
    seedGoalAwareStreamingAssistant(sid, null)

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

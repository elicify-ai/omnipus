// RED pack — WC-RESUME RED·U13/U14, unit U14 (FR-039; DEL-F39–40).
//
// Spec source: docs/internal/specs/session-core-spec.md
//   FR-039 — "Thinking/error indicators MUST join their own producing
//            run/turn/message goal_id to exact keyed merged criteria; unknown
//            neutral. Delete only latest-goal-wins indicator selection."
//   DEL-F39–40 — old branch: the InlineThinkingIndicator / FallbackToolUI /
//            VirtualAssistantMessageRow indicator use of the shared scalar
//            `goalStatus` (latest frame across all goals). Replacement:
//            "producer run/turn/message goal_id → goalPills/mergeGoalPillFrame
//            with exact merged criteria, neutral if unknown."
//   C-GOAL UI — "Matching empty active goal may show setup; different/
//            nonempty/unknown uses ordinary/neutral state."; "FR-039 forbids
//            scalar/latest/_default indicator choice."
//   BDD-12.5   — "a matching G1 setup only; b G2/ordinary never relabelled G1;
//            c neutral."
//
// Oracle provenance: the spec's replacement column.
//   * UNKNOWN (message carries no goal association) MUST be neutral — the
//     generic rotating pool — even while some OTHER goal is live and
//     record-empty. The latest-goal-wins scalar reading treats the live frame
//     as if it described this message, which is exactly the deleted branch.
//   * KEYED (message belongs to G1, whose record is empty) MUST show G1's own
//     setup label, never G2's state — even when G2 is the latest frame.
//
// FIELD-NAME ASSUMPTION (stated, not hidden): the producing message's goal
// association is read here as `message.goalId` (the camelCase twin of the
// wire `goal_id`, matching the existing `turnId`/`agentId` convention on
// ChatMessage). The spec names the association by meaning ("run/turn/message
// goal_id"), not by TS field name — if the frontend-lead's GREEN lands a
// different field name, update ONLY this harness, not the assertions.
//
// NOTE for GREEN: this pack's UNKNOWN-neutral case contradicts the existing
// `ChatScreen.goal-aware-thinking-indicator.test.tsx` first case (which seeds
// an active-empty goal as the latest scalar and expects "Framing your goal"
// for a message with no goal association). DEL-F39–40 removes that scalar
// reading, so that case is part of the change and must be updated, not kept.
//
// Harness mirrors ChatScreen.goal-aware-thinking-indicator.test.tsx exactly
// ('@assistant-ui/react' left UNMOCKED, ChatScreen mounted inside a real
// AssistantRuntimeProvider via the real useOmnipusRuntime hook).

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

// '@assistant-ui/react' is intentionally NOT mocked — see header.
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
vi.mock('./ActivityBar', () => ({ ActivityBar: () => null }))
vi.mock('./tools/GenericToolCall', () => ({ GenericToolCall: () => null }))
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

const ONE_CRITERION: NonNullable<GoalStatusFrame['criteria']> = [
  { kind: 'prose', judgment: 'boolean', text: 'release notes published', author: { kind: 'agent', id: 'mia' }, status: 'pending' },
]

function makeGoalFrame(goalId: string, state: GoalStatusFrame['state'] = 'active', withCriteria = false): GoalStatusFrame {
  return {
    type: 'goal_status',
    session_id: 'placeholder',
    goal_id: goalId,
    condition: 'ship the release notes',
    round: 0,
    max_rounds: 20,
    latest_reason: '',
    active_loops: 1,
    cap: 16,
    state,
    ...(withCriteria ? { criteria: ONE_CRITERION } : {}),
  }
}

/**
 * Seeds a streaming assistant placeholder (optionally carrying the producing
 * message's own `goalId`), a `goalStatus` scalar (the latest frame across all
 * goals — the field FR-039 forbids the indicator from reading), and a
 * `goalPills` map (the exact-keyed store the indicator MUST read).
 */
function seedStreamingAssistant(opts: {
  sid: string
  latestGoalStatus: GoalStatusFrame | null
  goalPills: Record<string, GoalStatusFrame>
  messageGoalId?: string
}): void {
  const { sid, latestGoalStatus, goalPills, messageGoalId } = opts
  const userMsg: ChatMessage = {
    id: `${sid}_user`,
    role: 'user',
    content: '/goal ship the release notes',
    timestamp: new Date().toISOString(),
    status: 'done',
  }
  // `goalId` is not yet on ChatMessage in the current tree — see the
  // FIELD-NAME ASSUMPTION note in the header. The cast keeps the assertion
  // about RENDERED OUTPUT (the label), not about the type.
  const streamingMsg = {
    id: `${sid}_assistant`,
    role: 'assistant',
    content: '',
    timestamp: new Date().toISOString(),
    status: 'streaming',
    isStreaming: true,
    ...(messageGoalId ? { goalId: messageGoalId } : {}),
  } as ChatMessage
  const allMessages = [userMsg, streamingMsg]
  const bucket = makeBucketMessages(allMessages)
  const latest = latestGoalStatus ? { ...latestGoalStatus, session_id: sid } : null

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
        goalStatus: latest,
        goalPills,
      },
    },
    messages: allMessages,
    isStreaming: true,
    isReplaying: false,
    replayCompletedForSession: sid,
    toolCalls: {},
    toolCallOrder: [],
    textAtToolCallStart: {},
    goalStatus: latest,
    goalPills,
  }))
  useSessionStore.setState({ activeSessionId: sid, activeAgentId: 'agent-1' })
  useConnectionStore.setState({ connection: null, isConnected: true, connectionError: null })
}

async function renderChatScreen(): Promise<HTMLElement> {
  let container!: HTMLElement
  await act(async () => {
    const result = render(
      <Providers>
        <ChatScreen />
      </Providers>,
    )
    container = result.container
  })
  return container
}

describe('U14/DEL-F39–40 — thinking indicator joins the producing message goal_id (FR-039)', () => {
  it('is NEUTRAL (generic pool) when the producing message has no goal association, even with a live record-empty goal', async () => {
    // BDD-12.5 "c unknown → neutral". A live, record-empty goal exists as the
    // latest scalar `goalStatus`, but THIS message is not associated with it —
    // the latest-goal-wins reading (the deleted branch) would wrongly show
    // the goal-setup label.
    const sid = 'u14_indicator_unknown'
    seedStreamingAssistant({
      sid,
      latestGoalStatus: makeGoalFrame('goal-G2'),
      goalPills: { 'goal-G2': makeGoalFrame('goal-G2') },
      // no messageGoalId — unknown
    })

    const container = await renderChatScreen()
    const bubble = container.querySelector('[data-testid="assistant-message"]') as HTMLElement
    expect(within(bubble).getByText(GENERIC_THINKING_RE)).toBeInTheDocument()
  })

  it('shows the producing message\'s OWN goal G1 setup label, never the latest goal G2 state', async () => {
    // BDD-12.5 "a matching G1 setup only; b G2/ordinary never relabelled G1".
    // The message belongs to G1 (record still empty); G2 is the latest scalar
    // frame and its record is populated. The indicator must follow G1.
    const sid = 'u14_indicator_keyed'
    seedStreamingAssistant({
      sid,
      latestGoalStatus: makeGoalFrame('goal-G2', 'active', true),
      goalPills: {
        'goal-G1': makeGoalFrame('goal-G1'), // empty record
        'goal-G2': makeGoalFrame('goal-G2', 'active', true), // populated record
      },
      messageGoalId: 'goal-G1',
    })

    const container = await renderChatScreen()
    const bubble = container.querySelector('[data-testid="assistant-message"]') as HTMLElement
    expect(within(bubble).getByText('Framing your goal')).toBeInTheDocument()
  })

  it('falls back to the generic pool when the producing message\'s OWN goal record is populated (positive control)', async () => {
    const sid = 'u14_indicator_populated'
    seedStreamingAssistant({
      sid,
      latestGoalStatus: makeGoalFrame('goal-G2', 'active', true),
      goalPills: {
        'goal-G2': makeGoalFrame('goal-G2', 'active', true),
      },
      messageGoalId: 'goal-G2',
    })

    const container = await renderChatScreen()
    const bubble = container.querySelector('[data-testid="assistant-message"]') as HTMLElement
    expect(within(bubble).getByText(GENERIC_THINKING_RE)).toBeInTheDocument()
  })
})

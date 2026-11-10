// Founder UI feedback 2026-10-11, point 1 (reply labels) — RED-first pack.
//
//   1a — the reply label is the agent NAME only: no agent status word ("Idle",
//        "Unavailable/reconnecting", …) is rendered next to the name on any
//        settled reply render path (virtualized history, plain fallback).
//        Evidence that motivated this: qa e2e receipt nav-e2e-ff-pw-catchup-a-2
//        read a reply row's text as "AvaIdle…" live and
//        "AvaUnavailable/reconnecting…" after reload.
//   1b — the inline indicator never disappears while the reply is running: a
//        streaming reply that already has visible content (streamed text or a
//        visible tool card) still shows the indicator, pinned at the bottom of
//        the reply (plain fallback row; a virtualized in-flight row — the
//        case where the streaming message is not the last message, e.g. a
//        queued user message after it).
//   1c — the label carries the agent's small AgentMark beside the name on
//        every reply render path (live, virtualized history, plain fallback),
//        the U8 reply header ("Ava → Alice · Telegram") included.
//
// Harness mirrors ChatScreen.wave1-indicator.test.tsx (same mocks, same
// seeding) so both packs fail and pass on the same reality.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, within, waitFor } from '@testing-library/react'
import * as React from 'react'
import { act } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { AssistantRuntimeProvider, useMessagePartText } from '@assistant-ui/react'
import { useChatStore, makeBucketMessages } from '@/store/chat'
import type { ChatMessage } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import { useOmnipusRuntime } from '@/lib/omnipus-runtime'
import { useToolApprovalStore } from '@/store/toolApproval'
import type { ChatParticipant } from '@/lib/api/generated/asyncapi-types'

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

import { emptyHistoryPage, type HistoryReadPort } from '../../../tests/chat-history-paging/legacy-fixtures'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([
      { id: 'agent-1', name: 'Mia', color: '#6E8BA8', icon: 'lightbulb', figure: 'Omnipus', role: 'general' },
      { id: 'agent-jim', name: 'Jim', color: '#4F8A80', icon: 'graph', figure: 'Man', role: 'general' },
    ]),
    fetchSessionMessages: vi.fn<HistoryReadPort>(async (sessionId) => emptyHistoryPage(sessionId)),
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

function seedMessages(sid: string, messages: ChatMessage[], flags: { streaming: boolean; replaying: boolean }) {
  const bucket = makeBucketMessages(messages)
  useChatStore.setState((s) => ({
    ...s,
    sessionsById: {
      [sid]: {
        ...((s.sessionsById ?? {})[sid] ?? {}),
        ...bucket,
        isStreaming: flags.streaming,
        isReplaying: flags.replaying,
        replayCompletedForSession: sid,
        toolCalls: {},
        toolCallOrder: [],
        textAtToolCallStart: {},
        toolCallOwnerMessageId: {},
        goalStatus: null,
        goalPills: {},
        sessionTokens: 0,
        sessionCost: 0,
        rateLimitEvent: null,
        lastUserMessageAt: null,
        cancelStage: null,
        lastReceivedEventTime: null,
        trimmedCount: 0,
      },
    },
    messages,
    isStreaming: flags.streaming,
    isReplaying: flags.replaying,
    replayCompletedForSession: sid,
    toolCalls: {},
    toolCallOrder: [],
    textAtToolCallStart: {},
    goalStatus: null,
    goalPills: {},
    pendingAsk: null,
  }))
  useSessionStore.setState({ activeSessionId: sid, activeAgentId: 'agent-1' })
  useConnectionStore.setState({
    connection: null,
    isConnected: true,
    connectionError: null,
    reconnectPhase: null,
    reconnectAttempt: 0,
  })
}

type Pair = [Extract<ChatMessage, { role: 'user' }>, Extract<ChatMessage, { role: 'assistant' }>]

function settledPair(sid: string, agentId: string, content: string): Pair {
  return [
    { id: `${sid}_user`, role: 'user', content: 'hi', timestamp: new Date().toISOString(), status: 'done' },
    { id: `${sid}_assistant`, role: 'assistant', content, timestamp: new Date().toISOString(), status: 'done', agentId },
  ]
}

function streamingPair(sid: string, agentId: string, content = ''): Pair {
  return [
    { id: `${sid}_user`, role: 'user', content: 'hi', timestamp: new Date().toISOString(), status: 'done' },
    {
      id: `${sid}_assistant`, role: 'assistant', content, timestamp: new Date().toISOString(),
      status: 'streaming', isStreaming: true, agentId, turnId: `${sid}_turn`,
    },
  ]
}

async function mount(): Promise<HTMLElement> {
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

function assistantBubble(container: HTMLElement): HTMLElement {
  const bubble = container.querySelector('[data-testid="assistant-message"]')
  expect(bubble).toBeTruthy()
  return bubble as HTMLElement
}

/** 1c — the reply label: small AgentMark beside the plain name, no status word. */
function expectReplyLabel(bubble: HTMLElement, name: string) {
  const avatarWrap = within(bubble).getByTestId('agent-label-avatar')
  const mark = avatarWrap.querySelector('[data-testid="agent-icon"]')
  expect(mark, 'small agent mark beside the name').not.toBeNull()
  expect(mark).toHaveAttribute('data-size', '18')
  const label = within(bubble).getByTestId('agent-label')
  expect(label.textContent).toBe(name)
  // The mark is a SIBLING of the name span (the name stays plain text, exactly
  // the name — the wave1 name-only contract on the label span itself).
  expect(avatarWrap.nextElementSibling).toBe(label)
  expect(label.childElementCount).toBe(0)
  // 1a — no status word anywhere in the reply row.
  expect(within(bubble).queryByText('Idle')).toBeNull()
  expect(within(bubble).queryByText('Unavailable/reconnecting')).toBeNull()
}

/** 1b — the indicator is present and is the LAST thing in its content column. */
function expectTrailingIndicator(bubble: HTMLElement) {
  const indicator = within(bubble).getByTestId('reply-status-indicator')
  expect(indicator.parentElement?.lastElementChild).toBe(indicator)
  return indicator
}

/** Virtualizer rows only mount with real element metrics — stub them for the
 * duration of fn (same pattern as wave1's virtual-names test). */
async function withVirtualizerMetrics(fn: () => Promise<void>) {
  const height = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetHeight')
  const width = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetWidth')
  Object.defineProperty(HTMLElement.prototype, 'offsetHeight', { configurable: true, get: () => 800 })
  Object.defineProperty(HTMLElement.prototype, 'offsetWidth', { configurable: true, get: () => 800 })
  try {
    await fn()
  } finally {
    if (height) Object.defineProperty(HTMLElement.prototype, 'offsetHeight', height)
    if (width) Object.defineProperty(HTMLElement.prototype, 'offsetWidth', width)
  }
}

beforeEach(() => {
  useToolApprovalStore.setState({ queue: [], resolvedIds: [] })
  useChatPreferencesStore.getState().setVerboseChatEnabled(false)
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  vi.stubGlobal('ResizeObserver', ResizeObserverStub)
})

describe('reply labels — settled rows show the name only (1a)', () => {
  it('virtualized history: no Idle status word next to the agent name', async () => {
    const sid = 'idle-virtual'
    seedMessages(sid, settledPair(sid, 'agent-1', 'The answer'), { streaming: false, replaying: false })
    await withVirtualizerMetrics(async () => {
      const bubble = assistantBubble(await mount())
      expect(await within(bubble).findByText('Mia')).toBeInTheDocument()
      expect(within(bubble).queryByText('Idle')).toBeNull()
      expect(within(bubble).getByTestId('agent-label').textContent).toBe('Mia')
    })
  })

  it('plain fallback: no Idle status word next to the agent name', async () => {
    vi.unstubAllGlobals()
    ;(globalThis as { ResizeObserver?: unknown }).ResizeObserver = undefined
    const sid = 'idle-plain'
    seedMessages(sid, settledPair(sid, 'agent-jim', 'The answer'), { streaming: false, replaying: false })
    const bubble = assistantBubble(await mount())
    expect(await within(bubble).findByText('Jim')).toBeInTheDocument()
    expect(within(bubble).queryByText('Idle')).toBeNull()
  })

  it('plain fallback: a settled reply paints no Unavailable/reconnecting word when the link is down (the reload case)', async () => {
    vi.unstubAllGlobals()
    ;(globalThis as { ResizeObserver?: unknown }).ResizeObserver = undefined
    const sid = 'settled-linkdown'
    seedMessages(sid, settledPair(sid, 'agent-1', 'The tide is the sea'), { streaming: false, replaying: false })
    act(() => useConnectionStore.setState({ isConnected: false, reconnectPhase: 'reconnecting' }))
    const bubble = assistantBubble(await mount())
    expect(await within(bubble).findByText('Mia')).toBeInTheDocument()
    expect(bubble.textContent).toContain('The tide is the sea')
    expect(within(bubble).queryByText('Unavailable/reconnecting')).toBeNull()
  })
})

describe('reply labels — the indicator stays while the reply runs (1b)', () => {
  it('plain fallback: a streaming reply that already streamed text keeps the indicator, pinned at the bottom', async () => {
    vi.unstubAllGlobals()
    ;(globalThis as { ResizeObserver?: unknown }).ResizeObserver = undefined
    const sid = 'plain-text-stream'
    seedMessages(sid, streamingPair(sid, 'agent-jim', 'Partial answer already on screen'), { streaming: true, replaying: false })
    const bubble = assistantBubble(await mount())
    expect(await within(bubble).findByText('Jim')).toBeInTheDocument()
    expect(bubble.textContent).toContain('Partial answer already on screen')
    expectTrailingIndicator(bubble)
  })

  it('plain fallback: a streaming reply whose visible content is a tool card keeps the indicator', async () => {
    vi.unstubAllGlobals()
    ;(globalThis as { ResizeObserver?: unknown }).ResizeObserver = undefined
    const sid = 'plain-card-stream'
    const messages = streamingPair(sid, 'agent-1')
    messages[1].tool_calls = [
      { id: 'tc-run', call_id: 'tc-run', tool: 'bash', params: { action: 'run', command: 'git status' }, status: 'running' },
    ] as unknown as ChatMessage['tool_calls']
    seedMessages(sid, messages, { streaming: true, replaying: false })
    const bubble = assistantBubble(await mount())
    expect(await within(bubble).findByText('Mia')).toBeInTheDocument()
    expectTrailingIndicator(bubble)
  })

  it('virtualized in-flight row (streaming message is not the last message): the indicator still shows', async () => {
    const sid = 'virtual-inflight'
    const inflight = streamingPair(sid, 'agent-jim', 'Working answer')
    const messages: ChatMessage[] = [
      ...inflight,
      { id: `${sid}_queued`, role: 'user', content: 'and one more thing', timestamp: new Date().toISOString(), status: 'done' },
    ]
    seedMessages(sid, messages, { streaming: true, replaying: false })
    await withVirtualizerMetrics(async () => {
      const container = await mount()
      await waitFor(() => expect(container.querySelector(`[data-message-id="${sid}_assistant"]`)).not.toBeNull())
      const bubble = container.querySelector(`[data-message-id="${sid}_assistant"]`) as HTMLElement
      expect(bubble.textContent).toContain('Working answer')
      expectTrailingIndicator(bubble)
    })
  })

  it('live path: a streaming reply that already streamed text keeps the indicator (regression guard)', async () => {
    const sid = 'live-text-stream'
    seedMessages(sid, streamingPair(sid, 'agent-1', 'Streamed words'), { streaming: true, replaying: false })
    const bubble = assistantBubble(await mount())
    expect(await within(bubble).findByText('Mia')).toBeInTheDocument()
    expect(bubble).toHaveAttribute('data-status', 'running')
    expectTrailingIndicator(bubble)
  })
})

describe('reply labels — the small agent mark beside the name (1c)', () => {
  it('live path: label is mark + name, no status word', async () => {
    const sid = 'label-live'
    seedMessages(sid, streamingPair(sid, 'agent-1'), { streaming: true, replaying: false })
    const bubble = assistantBubble(await mount())
    expect(await within(bubble).findByText('Mia')).toBeInTheDocument()
    expectReplyLabel(bubble, 'Mia')
  })

  it('virtualized history: label is mark + name, no status word', async () => {
    const sid = 'label-virtual'
    seedMessages(sid, settledPair(sid, 'agent-jim', 'Older answer'), { streaming: false, replaying: false })
    await withVirtualizerMetrics(async () => {
      const bubble = assistantBubble(await mount())
      expect(await within(bubble).findByText('Jim')).toBeInTheDocument()
      expectReplyLabel(bubble, 'Jim')
    })
  })

  it('plain fallback: label is mark + name, no status word', async () => {
    vi.unstubAllGlobals()
    ;(globalThis as { ResizeObserver?: unknown }).ResizeObserver = undefined
    const sid = 'label-plain'
    seedMessages(sid, settledPair(sid, 'agent-1', 'Fallback answer'), { streaming: false, replaying: false })
    const bubble = assistantBubble(await mount())
    expect(await within(bubble).findByText('Mia')).toBeInTheDocument()
    expectReplyLabel(bubble, 'Mia')
  })

  it('U8 reply header: the mark sits beside "Jim → Alice · Telegram"', async () => {
    vi.unstubAllGlobals()
    ;(globalThis as { ResizeObserver?: unknown }).ResizeObserver = undefined
    const alice: ChatParticipant = { kind: 'human', display_name: 'Alice', source: 'telegram' }
    const sid = 'label-reply-header'
    const pair = settledPair(sid, 'agent-jim', 'Sent it back to her')
    const reply = { ...pair[1], replyToMessageId: 'req-1', replyToParticipant: alice } as typeof pair[1]
    seedMessages(sid, [pair[0], reply], { streaming: false, replaying: false })
    const bubble = assistantBubble(await mount())
    await waitFor(() => expect(bubble.textContent).toContain('Alice · Telegram'))
    // The header renders IN PLACE OF the plain author label (D4 rows 5-6).
    expect(within(bubble).queryByTestId('agent-label')).toBeNull()
    const avatarWrap = within(bubble).getByTestId('agent-label-avatar')
    const header = within(bubble).getByTestId('reply-header')
    expect(header.textContent).toBe('Jim → Alice · Telegram')
    expect(avatarWrap.nextElementSibling).toBe(header)
    const mark = avatarWrap.querySelector('[data-testid="agent-icon"]')
    expect(mark).toHaveAttribute('data-size', '18')
    expect(within(bubble).queryByText('Idle')).toBeNull()
  })
})

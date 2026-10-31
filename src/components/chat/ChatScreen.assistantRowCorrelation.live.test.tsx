/**
 * Assistant row correlation attributes — the LIVE render path.
 *
 * WHY: an end-to-end check driving the real browser has to tell WHICH
 * persisted transcript entry a rendered reply came from. The row's
 * `data-message-id` is the DISPLAY id of the bubble, and one bubble can
 * stand for SEVERAL persisted entries — the follow-up segments of one turn
 * are merged into the bubble that opened it (`ChatMessage.mergedReplayIds`)
 * — while the turn-correlation id was not readable from the DOM at all.
 * The two read-only attributes close that gap:
 *
 *   data-turn-id            — the row's turn id; OMITTED when it has none.
 *   data-merged-message-ids — the merged entry ids, space separated, in the
 *                             store's order, never the row's own id;
 *                             OMITTED when the row merged nothing.
 *
 * This file covers the LIVE row — `ChatScreen.tsx::AssistantMessage`, the
 * AssistantUI streaming bubble. The history/virtualised row
 * (`ChatScreen.tsx::VirtualAssistantMessageRow`) is covered by
 * ChatScreen.assistantRowCorrelation.history.test.tsx.
 *
 * Harness mirrors ChatScreen.reply-label.test.tsx (same mocks, same
 * seeding) so both packs read the same reality.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, waitFor } from '@testing-library/react'
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

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}

// ResizeObserver PRESENT -> the virtualized list renders the HISTORY rows
// while the streaming tail is rendered by the live path (AssistantMessage).
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

type Pair = [Extract<ChatMessage, { role: 'user' }>, Extract<ChatMessage, { role: 'assistant' }>]

/** A user turn plus the assistant reply that is STILL streaming — the shape
 * the live path renders (the streaming tail stays in the AssistantUI
 * thread; see ChatMessageList.tsx::VirtualizedMessageListInner). */
function streamingPair(sid: string, turnId: string): Pair {
  return [
    { id: `${sid}_user`, role: 'user', content: 'hi', timestamp: new Date().toISOString(), status: 'done' },
    {
      id: `${sid}_assistant`, role: 'assistant', content: 'The answer', timestamp: new Date().toISOString(),
      status: 'streaming', isStreaming: true, agentId: 'agent-1', turnId,
    },
  ]
}

function seedMessages(sid: string, messages: ChatMessage[]) {
  const bucket = makeBucketMessages(messages)
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
    isStreaming: true,
    isReplaying: false,
    replayCompletedForSession: sid,
    toolCalls: {},
    toolCallOrder: [],
    textAtToolCallStart: {},
    toolCallOwnerMessageId: {},
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

/** The ONE assistant row on screen, asserted to be the LIVE one: the live
 * row's status comes from the AssistantUI message ('running' while the turn
 * streams), while the history row hard-codes 'complete'. */
async function liveAssistantRow(container: HTMLElement): Promise<HTMLElement> {
  let row: HTMLElement | null = null
  await waitFor(() => {
    const rows = container.querySelectorAll<HTMLElement>('[data-testid="assistant-message"]')
    expect(rows.length, 'exactly one assistant row is rendered').toBe(1)
    row = rows[0]
  })
  const found = row as unknown as HTMLElement
  expect(found, 'the live row is the streaming one, not a history row').toHaveAttribute('data-status', 'running')
  return found
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

describe('assistant row correlation attributes — live row', () => {
  it('a single-round reply carries its turn id and no merged ids attribute', async () => {
    const sid = 'live-single-round'
    const turnId = 'turn-live-single'
    seedMessages(sid, streamingPair(sid, turnId))

    const row = await liveAssistantRow(await mount())

    expect(row, 'the live row carries the turn id of its producing turn').toHaveAttribute('data-turn-id', turnId)
    expect(
      row.hasAttribute('data-merged-message-ids'),
      'a row that merged no transcript entry carries NO merged ids attribute',
    ).toBe(false)
  })

  it('a multi-round reply carries its turn id and its two merged entry ids in order', async () => {
    const sid = 'live-multi-round'
    const turnId = 'turn-live-multi'
    const pair = streamingPair(sid, turnId)
    pair[1].mergedReplayIds = ['entry-b', 'entry-c']
    seedMessages(sid, pair)

    const row = await liveAssistantRow(await mount())

    expect(row, 'the live row carries the turn id of its producing turn').toHaveAttribute('data-turn-id', turnId)
    expect(
      row.getAttribute('data-merged-message-ids'),
      'the merged entry ids are space separated and keep the store order',
    ).toBe('entry-b entry-c')
    expect(
      row.getAttribute('data-merged-message-ids')?.split(' '),
      'the merged ids never repeat the row own id',
    ).not.toContain(`${sid}_assistant`)
  })
})

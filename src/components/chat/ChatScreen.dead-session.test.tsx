// ChatScreen.dead-session.test.tsx — D5 (audit joint-merge 2026-10-09): a
// deleted agent's solely-owned main chat is deleted with it, and the
// workspace Chat tab attaches that dead session with no route-level guard
// (the deep-link route 404s into SessionNotFound instead). Before this fix
// the tab showed "Could not load messages." with the composer ENABLED — a
// send failed as a turn error against a nonexistent session.
//
// The fix under test: a CONFIRMED 404 from the history read is the one
// truthful "this session is gone" signal, and it puts the surface into the
// existing read-only state. Paging D15 now confines a history-read failure
// to the history slot: the composer stays mounted, with a confirmed missing
// session disabled/read-only. D6 uses disconnected bounded REST fallback
// for these HTTP-error cases; connected opening is WS-authoritative.
// A transport failure (network, 5xx) is NOT deletion and must NOT produce
// the read-only banner.
//
// Boundary note: the REAL react-query (QueryClientProvider, retry off) runs
// ChatScreen's history query and the REAL isApiError classifies its error; the
// only stub on that path is the network boundary, fetchSessionMessages, which
// rejects with a real ApiError. That a 404 HTTP response becomes an ApiError
// with status 404 is http.ts's existing, separately covered behavior
// (ApiError.fromResponse), not code introduced here.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, act, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import * as React from 'react'
import { useChatStore, makeBucketMessages } from '@/store/chat'
import type { ChatMessage } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { ApiError } from '@/lib/api-error'
import { emptyHistoryPage, type HistoryReadPort } from '../../../tests/chat-history-paging/legacy-fixtures'
import { emptySessionState } from '@/store/chat/session'

const { historyRead } = vi.hoisted(() => ({
  historyRead: vi.fn<HistoryReadPort>(async (sessionId) => emptyHistoryPage(sessionId)),
}))

vi.mock('@assistant-ui/react', async () => (await import('@/test/assistantUiMock')).createAssistantUiMock())

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
    fetchSessionMessages: historyRead,
    fetchAboutInfo: vi.fn().mockResolvedValue({}),
    createSession: vi.fn(),
    uploadFiles: vi.fn(),
    fetchProviders: vi.fn().mockResolvedValue([]),
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
vi.mock('./tools/GenericToolCall', () => ({ GenericToolCall: () => null }))
vi.mock('./markdown-text', () => ({
  MarkdownText: () => React.createElement('div', {}),
}))
vi.mock('./composer/ModelPicker', () => ({ ModelPicker: () => null }))
vi.mock('./composer/TokenCounter', () => ({ TokenCounter: () => null }))
vi.mock('@/lib/memory-observer', () => ({
  startMemoryObserver: () => ({ dispose: vi.fn(), getCurrentSnapshot: vi.fn() }),
  addMemoryObserver: () => () => {},
  getCurrentSnapshot: () => ({ usedJSHeapSizeBytes: null, level: 'ok', supported: false }),
}))

import { ChatScreen } from './ChatScreen'

const SID = 'test-session-dead'

function seedBucket(messages: ChatMessage[]): void {
  const bucket = makeBucketMessages(messages)
  useChatStore.setState((s) => ({
    ...s,
    sessionsById: {
      [SID]: {
        ...emptySessionState(),
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
        trimmedCount: 0,
      },
    },
    messages,
    isStreaming: false,
    isReplaying: false,
    replayCompletedForSession: SID,
  }))
  useSessionStore.setState({ activeSessionId: SID, activeAgentId: 'agent-1' })
}

async function renderScreen(): Promise<HTMLElement> {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  let container!: HTMLElement
  await act(async () => {
    const result = render(<QueryClientProvider client={client}>{<ChatScreen />}</QueryClientProvider>)
    container = result.container
  })
  // The history error screen appears once the real query has rejected.
  await waitFor(() => expect(container.textContent).toContain('Could not load messages.'))
  return container
}

describe('ChatScreen — a deleted session (confirmed 404) renders read-only (D5)', () => {
  beforeEach(() => {
    useConnectionStore.setState({
      // D6: exercise the real bounded HTTP fallback, not connected startup.
      isConnected: false,
      liteMode: false,
      reconnectPhase: null,
      reconnectAttempt: 0,
      connectionError: null,
      connection: null,
    })
    vi.unstubAllGlobals()
    vi.stubGlobal('ResizeObserver', undefined)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('a 404 history read shows the read-only banner; the composer is unreachable (no turn-error sends)', async () => {
    seedBucket([{ id: 'u1', role: 'user', status: 'done', content: 'hello', timestamp: new Date().toISOString() }])
    historyRead.mockRejectedValue(new ApiError(404, 'session not found'))

    const container = await renderScreen()

    expect(container.querySelector('[data-testid="agent-removed-banner"]'), 'the read-only banner is visible').toBeTruthy()
    // D15 keeps the control shell mounted, but a confirmed missing chat
    // remains read-only: no doomed send becomes possible.
    expect(container.textContent).toContain('Could not load messages.')
    expect(container.querySelector('[data-testid="composer-input"]')).toBeDisabled()
  })

  it('a transport failure (status 0) is NOT deletion — no read-only banner', async () => {
    seedBucket([{ id: 'u1', role: 'user', status: 'done', content: 'hello', timestamp: new Date().toISOString() }])
    historyRead.mockRejectedValue(new ApiError(0, 'Network unavailable. Check your connection.'))

    const container = await renderScreen()

    expect(container.querySelector('[data-testid="agent-removed-banner"]')).toBeNull()
    // D15 confines transport/history failure to the history slot. The
    // composer stays mounted; offline connection disabling is independent.
    expect(container.textContent).toContain('Could not load messages.')
    expect(container.querySelector('[data-testid="composer-input"]')).toBeVisible()
  })

  // C10 (R3 delta re-review): the banner said "Agent removed" for ANY
  // confirmed 404 — but a 404 only proves the CHAT is gone; the server says
  // "agent removed" only via the agent_removed signal (agentRemoved prop).
  // And the Retry offered next to a 404 can never succeed — a deleted chat
  // 404s again — so a missing chat gets no Retry.
  it('a missing chat (404, no agent_removed signal) says so plainly and offers no doomed Retry', async () => {
    seedBucket([{ id: 'u1', role: 'user', status: 'done', content: 'hello', timestamp: new Date().toISOString() }])
    historyRead.mockRejectedValue(new ApiError(404, 'session not found'))

    const container = await renderScreen()

    expect(container.textContent).toContain('This chat no longer exists')
    expect(container.textContent).not.toContain('Agent removed')
    // No Retry that cannot succeed: the 404 refetch 404s again.
    expect(container.textContent).not.toContain('Retry')
  })

  it('a server agent_removed signal keeps the agent-removed wording', async () => {
    seedBucket([{ id: 'u1', role: 'user', status: 'done', content: 'hello', timestamp: new Date().toISOString() }])
    // The history read succeeds; the signal arrives the production way — the
    // agentRemoved PROP, which the deep-link route feeds from
    // fetchSessionDetail().agent_removed (sessions.$sessionId.tsx). That prop
    // is the only path ChatScreen reads the signal through.
    historyRead.mockResolvedValue(emptyHistoryPage(SID))
    let container!: HTMLElement
    await act(async () => {
      const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
      container = render(
        <QueryClientProvider client={client}>
          <ChatScreen agentRemoved />
        </QueryClientProvider>,
      ).container
    })

    expect(container.textContent).toContain('Agent removed — this session is read-only')
    expect(container.textContent).not.toContain('This chat no longer exists')
  })

  it('a transport failure keeps Retry (only a confirmed 404 loses it)', async () => {
    seedBucket([{ id: 'u1', role: 'user', status: 'done', content: 'hello', timestamp: new Date().toISOString() }])
    historyRead.mockRejectedValue(new ApiError(0, 'Network unavailable. Check your connection.'))

    const container = await renderScreen()

    expect(container.textContent).toContain('Retry')
  })
})

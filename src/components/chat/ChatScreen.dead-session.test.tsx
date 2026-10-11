// ChatScreen.dead-session.test.tsx — D5 (audit joint-merge 2026-10-09): a
// deleted agent's solely-owned main chat is deleted with it, and the
// workspace Chat tab attaches that dead session with no route-level guard
// (the deep-link route 404s into SessionNotFound instead). Before this fix
// the tab showed "Could not load messages." with the composer ENABLED — a
// send failed as a turn error against a nonexistent session.
//
// The fix under test: a CONFIRMED 404 from the history read is the one
// truthful "this session is gone" signal, and it puts the surface into the
// existing read-only state — the agent-removed banner visible above the
// history-error screen, whose branch already replaces the whole
// thread+composer (so no send can fail as a turn error against a
// nonexistent session). A transport failure (network, 5xx) is NOT deletion
// and must NOT produce the banner.
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
import { fetchSessionMessages } from '@/lib/api'

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
    fetchSessionMessages: vi.fn().mockResolvedValue([]),
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
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('a 404 history read shows the read-only banner; the composer is unreachable (no turn-error sends)', async () => {
    seedBucket([{ id: 'u1', role: 'user', status: 'done', content: 'hello', timestamp: new Date().toISOString() }])
    vi.mocked(fetchSessionMessages).mockRejectedValue(new ApiError(404, 'session not found'))

    const container = await renderScreen()

    expect(container.querySelector('[data-testid="agent-removed-banner"]'), 'the read-only banner is visible').toBeTruthy()
    // The history-error branch replaces the whole thread+composer with the
    // Retry screen — there is no input to type a doomed send into.
    expect(container.textContent).toContain('Could not load messages.')
    expect(container.querySelector('[data-testid="composer-input"]')).toBeNull()
  })

  it('a transport failure (status 0) is NOT deletion — no read-only banner', async () => {
    seedBucket([{ id: 'u1', role: 'user', status: 'done', content: 'hello', timestamp: new Date().toISOString() }])
    vi.mocked(fetchSessionMessages).mockRejectedValue(new ApiError(0, 'Network unavailable. Check your connection.'))

    const container = await renderScreen()

    expect(container.querySelector('[data-testid="agent-removed-banner"]')).toBeNull()
    // The error screen itself is shown (renderScreen waited for it), so the
    // absent composer is the history-error branch, not a branch that stopped rendering.
    expect(container.textContent).toContain('Could not load messages.')
    expect(container.querySelector('[data-testid="composer-input"]')).toBeNull()
  })
})

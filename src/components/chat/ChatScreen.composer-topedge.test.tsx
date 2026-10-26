// ChatScreen composer top-edge test (founder UI feedback 2026-10-11,
// point 5 — follow-up dispatch): the full-width composer container must show
// NO top edge in the rendered app. The rendered-app inspection (temp gateway,
// Playwright, receipts nav-composer-pw-gradient2.log) proved the visible line
// was the decorative "Gradient fade above composer" strip: a full-width div
// with `bg-gradient-to-t from-[var(--color-primary)] to-transparent` whose
// opaque bottom edge (rgb(10,10,11)) sits exactly on the container's top
// border, painted over the chat column's `--color-surface-1` backdrop — a
// shadow-like dark band hugging the edge. This test pins its removal: the
// composer container carries no bg-gradient element at all, the container
// itself stays full-width, and the composer card keeps its border and the
// focus-within accent cue (design-system rule 7).
//
// Scaffolding (mocks + store seeding) is copied from
// ChatScreen.clear-marker.test.tsx's real-ChatScreen render path, the
// precedent every ChatScreen.*.test.tsx here relies on.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, act } from '@testing-library/react'
import * as React from 'react'
import { useChatStore, makeBucketMessages } from '@/store/chat'
import type { ChatMessage } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'

vi.mock('@assistant-ui/react', async () => (await import('@/test/assistantUiMock')).createAssistantUiMock())

vi.mock('@tanstack/react-query', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-query')>()
  return {
    ...actual,
    useQuery: (opts: { queryKey?: readonly unknown[] }) => ({ data: queryDataWithEmptyHistory(opts?.queryKey), isError: false, refetch: vi.fn() }),
    useMutation: () => ({ mutate: vi.fn(), isPending: false }),
    useQueryClient: () => ({ invalidateQueries: vi.fn(), removeQueries: vi.fn() }),
  }
})

vi.mock('@tanstack/react-router', () => ({
  useRouter: () => ({ navigate: vi.fn() }),
  useSearch: () => ({}),
  Link: ({ children }: { children: React.ReactNode }) => children,
}))

import { emptyHistoryPage, queryDataWithEmptyHistory, type HistoryReadPort } from '../../../tests/chat-history-paging/legacy-fixtures'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([]),
    fetchSessionMessages: vi.fn<HistoryReadPort>(async (sessionId) => emptyHistoryPage(sessionId)),
    fetchAboutInfo: vi.fn().mockResolvedValue({}),
    createSession: vi.fn(),
    uploadFiles: vi.fn(),
    fetchProviders: vi.fn().mockResolvedValue([]),
    isApiError: vi.fn().mockReturnValue(false),
    fetchCommands: vi.fn().mockResolvedValue([]),
    fetchSkills: vi.fn().mockResolvedValue([]),
  }
})

vi.mock('@/assets/logo/omnipus-avatar.svg?url', () => ({ default: 'omnipus-avatar.svg' }))
vi.mock('./RateLimitIndicator', () => ({ RateLimitIndicator: () => null }))
vi.mock('./tools/GenericToolCall', () => ({ GenericToolCall: () => null }))
vi.mock('./markdown-text', () => ({
  MarkdownText: () => React.createElement('div', {}),
}))
vi.mock('./composer/ModelPicker', () => ({ ModelPicker: () => null }))
vi.mock('./composer/TokenCounter', () => ({ TokenCounter: () => null }))
vi.mock('./ActivityBar', () => ({ ActivityBar: () => <div data-testid="activity-bar-stub" /> }))
vi.mock('@/lib/memory-observer', () => ({
  startMemoryObserver: () => ({ dispose: vi.fn(), getCurrentSnapshot: vi.fn() }),
  addMemoryObserver: () => () => {},
  getCurrentSnapshot: () => ({ usedJSHeapSizeBytes: null, level: 'ok', supported: false }),
}))

import { ChatScreen } from './ChatScreen'

const SID = 'test-session-topedge'

// Same seeding shape as ChatScreen.clear-marker.test.tsx: a real session
// bucket for the active session, so the mount reaches the composer without
// store-driven render loops.
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

describe('ChatScreen — composer container shows no top edge (founder feedback point 5)', () => {
  beforeEach(() => {
    useConnectionStore.setState({
      isConnected: true,
      liteMode: false,
      reconnectPhase: null,
      reconnectAttempt: 0,
      connectionError: null,
      connection: null,
    })
    seedBucket([
      { id: 'u1', role: 'user', status: 'done', content: 'hello', timestamp: new Date().toISOString() },
    ])
    vi.unstubAllGlobals()
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    ;(globalThis as any).ResizeObserver = undefined
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('renders the full-width composer container with NO bg-gradient element inside it', async () => {
    let container!: HTMLElement
    await act(async () => {
      const result = render(<ChatScreen />)
      container = result.container
    })

    const card = container.querySelector('[data-testid="composer-card"]')
    expect(card, 'the composer card renders').toBeTruthy()

    // The full-width container is the card's third ancestor
    // (`relative w-full` — verified in the rendered app: x=0, width=1440).
    const full = card!.parentElement?.parentElement?.parentElement
    expect(full, 'the composer container chain resolves').toBeTruthy()
    expect(full!.className).toContain('w-full')

    // The removed decoration must not come back in ANY wrapper of the
    // container: a bg-gradient strip here paints an opaque near-black edge
    // over the lighter chat backdrop and reads as a shadow on the container.
    const gradient = full!.querySelector('[class*="bg-gradient"]')
    expect(gradient, 'no bg-gradient element inside the composer container').toBeNull()

    // The card keeps its own border and the sanctioned focus cue.
    expect(card!.className).toContain('border')
    expect(card!.className).toContain('focus-within:border-[var(--color-accent)]')
    expect(card!.className).not.toMatch(/shadow/i)
  })
})

// ChatScreen.clear-marker.test.tsx — FR-030/031 (U10b) render half.
//
// Oracles: docs/internal/specs/session-core-spec.md FR-030 (the /clear marker
// entry: type system, role system, view_membership chat, content
// "Conversation context cleared") + core's pkg/agent/clear_session.go (git
// 67345b1d7, read-only) + FR-031/BDD-09.3 (a refusal is the command's plain
// Reason, shown to the person, nothing changed).
//
// What must hold, through the real ChatScreen render path:
//   - the /clear marker row renders as a QUIET DIVIDER (its own testid), not
//     as an ordinary chat bubble;
//   - any OTHER system row (an unknown marker, a help line, a refusal
//     reason) never crashes the renderer and is never silently dropped — it
//     renders plainly, without the divider discriminator;
//   - a refusal reply's exact reason text is visible.
//
// jsdom has no ResizeObserver, so ChatScreen renders PlainMessageList and
// every message goes through VirtualSystemMessageRow — the same routing every
// other ChatScreen.*.test.tsx in this directory relies on.

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
    useQuery: () => ({ data: [], isError: false, refetch: vi.fn() }),
    useMutation: () => ({ mutate: vi.fn(), isPending: false }),
    useQueryClient: () => ({ invalidateQueries: vi.fn(), removeQueries: vi.fn() }),
  }
})

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
    isApiError: vi.fn().mockReturnValue(false),
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

const SID = 'test-session-clear-marker'
// Core's clearMarkerText (pkg/agent/clear_session.go @ 67345b1d7).
const CLEAR_MARKER_TEXT = 'Conversation context cleared'
// Core's clearHandler success reply, and the clearRefusal helper's reason —
// both quoted verbatim from the oracle so the assertions cannot be satisfied
// by a paraphrase.
const SUCCESS_REPLY = 'Context cleared. The conversation and its history are kept; the assistant continues from here with a fresh context.'
const REFUSAL_REASON = '/clear works only in a main or extra chat. This is a helper or task session, so nothing was cleared.'

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

describe('ChatScreen — the /clear marker row and other system rows (FR-030/031)', () => {
  beforeEach(() => {
    useConnectionStore.setState({
      isConnected: true,
      liteMode: false,
      reconnectPhase: null,
      reconnectAttempt: 0,
      connectionError: null,
      connection: null,
    })
    // Force the PlainMessageList fallback (no ResizeObserver in jsdom) so
    // every message renders through VirtualSystemMessageRow.
    vi.unstubAllGlobals()
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    ;(globalThis as any).ResizeObserver = undefined
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('renders the clear marker as a quiet divider with the marker text, not a chat bubble', async () => {
    seedBucket([
      { id: 'u1', role: 'user', status: 'done', content: 'what changed?', timestamp: new Date().toISOString() },
      { id: 'clear-1', role: 'system', status: 'done', content: CLEAR_MARKER_TEXT, timestamp: new Date().toISOString() },
    ])

    let container!: HTMLElement
    await act(async () => {
      const result = render(<ChatScreen />)
      container = result.container
    })

    const divider = container.querySelector('[data-testid="clear-context-divider"]')
    expect(divider, 'the marker row renders as the quiet divider').toBeTruthy()
    expect(divider!.textContent).toContain(CLEAR_MARKER_TEXT)
    // Presentation, not just identity: the row IS a divider — the catalogued
    // Separator primitive flanks the marker text (two hairline rules), and
    // none of the ordinary system-pill treatment (the rounded bubble) is
    // present.
    expect(divider!.querySelectorAll('[data-orientation="horizontal"]')).toHaveLength(2)
    expect(divider!.querySelector('.rounded-full')).toBeNull()
  })

  it('renders an UNKNOWN system row without throwing, present in the DOM, without the divider discriminator', async () => {
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {})
    try {
      seedBucket([
        { id: 'sys-unknown-1', role: 'system', status: 'done', content: 'Some future system marker nobody told the SPA about.', timestamp: new Date().toISOString() },
      ])

      let container!: HTMLElement
      await act(async () => {
        const result = render(<ChatScreen />)
        container = result.container
      })

      // Not dropped: the text is in the DOM. Not a divider either.
      expect(container.textContent).toContain('Some future system marker nobody told the SPA about.')
      expect(container.querySelector('[data-testid="clear-context-divider"]')).toBeNull()
      expect(container.querySelector('[data-message-role="system"]')).toBeTruthy()
      expect(consoleError).not.toHaveBeenCalled()
    } finally {
      consoleError.mockRestore()
    }
  })

  it('keeps the marker divider and an unknown system row distinct in the same thread', async () => {
    seedBucket([
      { id: 'clear-1', role: 'system', status: 'done', content: CLEAR_MARKER_TEXT, timestamp: new Date().toISOString() },
      { id: 'sys-unknown-2', role: 'system', status: 'done', content: 'Another unknown marker.', timestamp: new Date().toISOString() },
    ])

    let container!: HTMLElement
    await act(async () => {
      const result = render(<ChatScreen />)
      container = result.container
    })

    const dividers = container.querySelectorAll('[data-testid="clear-context-divider"]')
    expect(dividers).toHaveLength(1)
    expect(dividers[0].textContent).toContain(CLEAR_MARKER_TEXT)
    expect(container.textContent).toContain('Another unknown marker.')
  })

  it('shows the server success reply verbatim after a clear', async () => {
    seedBucket([
      { id: 'u1', role: 'user', status: 'done', content: '/clear', timestamp: new Date().toISOString() },
      { id: 'a1', role: 'assistant', status: 'done', content: SUCCESS_REPLY, timestamp: new Date().toISOString() },
      { id: 'clear-1', role: 'system', status: 'done', content: CLEAR_MARKER_TEXT, timestamp: new Date().toISOString() },
    ])

    let container!: HTMLElement
    await act(async () => {
      const result = render(<ChatScreen />)
      container = result.container
    })

    expect(container.textContent).toContain(SUCCESS_REPLY)
  })

  it('shows a refusal reason verbatim, visibly, as the command reply (FR-031/BDD-09.3)', async () => {
    seedBucket([
      { id: 'u1', role: 'user', status: 'done', content: '/clear', timestamp: new Date().toISOString() },
      { id: 'a2', role: 'assistant', status: 'done', content: REFUSAL_REASON, timestamp: new Date().toISOString() },
    ])

    let container!: HTMLElement
    await act(async () => {
      const result = render(<ChatScreen />)
      container = result.container
    })

    expect(container.textContent).toContain(REFUSAL_REASON)
    // And nothing pretended the clear happened.
    expect(container.querySelector('[data-testid="clear-context-divider"]')).toBeNull()
  })
})

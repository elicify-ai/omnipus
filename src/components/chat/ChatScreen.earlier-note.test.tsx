// ChatScreen.earlier-note.test.tsx — the quiet "Earlier messages are saved."
// line above the clear divider (founder ruling 2026-10-11, interim until
// #1266; wording confirmed by the founder — keep it exact, one component:
// components/chat/ClearContextDivider.tsx).
//
// What must hold, through the real ChatScreen render path:
//   - when the session's chat state carries hasEarlierMessages (read from
//     the server's has_earlier_messages carriers), the line renders DIRECTLY
//     above the clear divider with the EXACT agreed wording;
//   - it is a statement, never a control: no button, no link, no pager;
//   - without the flag (no cleared context) the line is absent and the
//     divider still renders.
//
// jsdom has no ResizeObserver, so ChatScreen renders PlainMessageList and
// every message goes through VirtualSystemMessageRow — the same routing
// every other ChatScreen.*.test.tsx in this directory relies on.

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
import { sessionMessagesHasEarlier } from '@/lib/api'

const SID = 'test-session-earlier-note'
// Core's ClearMarkerText (pkg/session/transcript_view.go) — the divider's row.
const CLEAR_MARKER_TEXT = 'Conversation context cleared'
// The founder-confirmed wording (nav-clearview brief T3, 2026-10-11). Exact
// match asserted — a paraphrase must fail.
const EARLIER_NOTE_TEXT = 'Earlier messages are saved. The agent can still recall them.'

function seedBucket(messages: ChatMessage[], hasEarlierMessages: boolean): void {
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
        hasEarlierMessages,
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

const seedRows = (marker: boolean): ChatMessage[] => [
  ...(marker
    ? []
    : [{ id: 'u1', role: 'user' as const, status: 'done' as const, content: 'before the clear', timestamp: new Date().toISOString() }]),
  { id: 'clear-1', role: 'system', status: 'done', content: CLEAR_MARKER_TEXT, timestamp: new Date().toISOString() },
]

describe('ChatScreen — the quiet "Earlier messages are saved." line above the clear divider', () => {
  beforeEach(() => {
    // The header carrier record is module-scoped — clear it so one test's
    // value cannot leak into the next.
    delete sessionMessagesHasEarlier[SID]
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

  it('shows the exact line directly above the divider when hasEarlierMessages is true', async () => {
    // The messages read's header carrier (X-Has-Earlier-Messages, carried in
    // sessionMessagesHasEarlier) is what ChatScreen's history effect writes
    // into the bucket — seed it too so the mocked useQuery's effect run
    // matches the bucket seed instead of overwriting it (in production both
    // come from the same fetchSessionMessages response).
    sessionMessagesHasEarlier[SID] = true
    seedBucket(seedRows(true), true)

    let container!: HTMLElement
    await act(async () => {
      const result = render(<ChatScreen />)
      container = result.container
    })

    const note = container.querySelector('[data-testid="earlier-messages-note"]')
    expect(note, 'the note renders when the flag is true').toBeTruthy()
    expect(note!.textContent).toBe(EARLIER_NOTE_TEXT)

    // Directly above the divider: the note's next sibling IS the divider row.
    const divider = container.querySelector('[data-testid="clear-context-divider"]')
    expect(divider, 'the divider still renders').toBeTruthy()
    expect(note!.nextElementSibling).toBe(divider)

    // A statement, never a control: no button, no link, no pager inside.
    expect(note!.querySelectorAll('button, a, [role="button"], [role="link"]')).toHaveLength(0)
  })

  it('shows nothing when the flag is false — the divider alone', async () => {
    seedBucket(seedRows(false), false)

    let container!: HTMLElement
    await act(async () => {
      const result = render(<ChatScreen />)
      container = result.container
    })

    expect(container.querySelector('[data-testid="earlier-messages-note"]')).toBeNull()
    expect(container.querySelector('[data-testid="clear-context-divider"]')).toBeTruthy()
  })

  it('shows nothing when the flag is unset (a session never cleared)', async () => {
    seedBucket(
      [{ id: 'u1', role: 'user', status: 'done', content: 'plain chat', timestamp: new Date().toISOString() }],
      false,
    )
    // Simulate the unset shape explicitly: remove the field from the bucket.
    useChatStore.setState((s) => {
      const b = s.sessionsById[SID]
      if (!b) return s
      const { hasEarlierMessages: _omitted, ...rest } = b
      void _omitted
      return { ...s, sessionsById: { ...s.sessionsById, [SID]: { ...rest } as typeof b } }
    })

    let container!: HTMLElement
    await act(async () => {
      const result = render(<ChatScreen />)
      container = result.container
    })

    expect(container.querySelector('[data-testid="earlier-messages-note"]')).toBeNull()
  })
})

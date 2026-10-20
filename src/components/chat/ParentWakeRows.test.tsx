/**
 * Parent-wake visibility — rendered rows (spec §10, §18, §19, Tests 24 and 33).
 *
 * Oracle: docs/internal/specs/parent-wake-visibility-spec.md.
 * The restarted sentence is "Interrupted by a restart."
 * The pending-save sentence is the §18 nonfatal line.
 * One new row is announced once. Replay does not announce it again.
 * The disclosure is a native details/summary. Focus stays in the composer.
 *
 * This jsdom harness has no ResizeObserver, so ChatScreen uses PlainMessageList,
 * which renders system rows through VirtualSystemMessageRow. The assistant-ui
 * SystemMessage branch is the other required renderer; a stored system row in
 * this harness does not mount it (same split as ChatScreen.goal-outcome.test.tsx).
 *
 * Step 4 items 2–3 are deferred to CHECK.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, act } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import * as React from 'react'
import { useChatStore, makeBucketMessages, type ChatMessage } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import type { ServerFrame } from '@/lib/ws'

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
  useNavigate: () => vi.fn(),
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

const SID = 'parent-wake-render'
const RESTART_SENTENCE = 'Interrupted by a restart.'
const PENDING_SENTENCE = "A helper event could not be saved in this chat. Omnipus will retry. This is not a failure of the helper's work."

function frame(): ServerFrame {
  return {
    type: 'helper_event',
    session_id: SID,
    message_id: 'stopped-notice:parent:helper:1:1',
    helper_event: {
      helper_session_id: 'helper-1',
      kind: 'restarted',
      text: RESTART_SENTENCE,
      created_at: '2026-10-15T12:00:00Z',
    },
  } as unknown as ServerFrame
}

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

beforeEach(() => {
  useConnectionStore.setState({
    isConnected: true,
    liteMode: false,
    reconnectPhase: null,
    reconnectAttempt: 0,
    connectionError: null,
    connection: null,
  })
  useChatPreferencesStore.setState({ verboseChatEnabled: false })
  vi.unstubAllGlobals()
  ;(globalThis as { ResizeObserver?: unknown }).ResizeObserver = undefined
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('ParentWakeRows', () => {
  it('shows one restarted disclosure, announces it once, and leaves focus in the composer', async () => {
    const user = userEvent.setup()
    seedBucket([
      { id: 'u1', role: 'user', content: 'delegate a short task', timestamp: '2026-10-15T11:00:00Z', status: 'done' },
    ])
    await act(async () => {
      render(<ChatScreen />)
    })
    expect(screen.queryByText(PENDING_SENTENCE)).toBeNull()
    expect(screen.queryByText(RESTART_SENTENCE)).toBeNull()

    const composer = screen.getByRole('textbox')
    composer.focus()

    await act(async () => {
      useChatStore.getState().handleFrame(frame())
    })

    const summary = screen.getByText(RESTART_SENTENCE)
    expect(summary.closest('details')).not.toBeNull()
    expect(summary.closest('summary')?.textContent).toContain(RESTART_SENTENCE)
    expect(document.activeElement).toBe(composer)

    const announced = Array.from(document.querySelectorAll('[aria-live]'))
      .map((node) => node.textContent ?? '')
      .filter((text) => text.includes(RESTART_SENTENCE))
    expect(announced, 'one live announcement names the new row (§19)').toEqual([RESTART_SENTENCE])

    await user.click(summary)
    expect(summary.closest('details')?.hasAttribute('open')).toBe(true)
    await user.keyboard('{Enter}')
    expect(summary.closest('details')?.hasAttribute('open')).toBe(false)

    await act(async () => {
      useChatStore.getState().handleFrame(frame())
    })
    const afterReplay = Array.from(document.querySelectorAll('[aria-live]'))
      .map((node) => node.textContent ?? '')
      .filter((text) => text.includes(RESTART_SENTENCE))
    expect(afterReplay, 'replay does not announce the row again (§19)').toEqual([RESTART_SENTENCE])
    expect(screen.getAllByText(RESTART_SENTENCE)).toHaveLength(1)
  })
})

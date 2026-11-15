/**
 * ChatScreen.provider-waiting-attempt1664.test.tsx — issue #1664 B3 through
 * the REAL screen (CHECK F2, round 3): the round-2 pack tested the store
 * slot and the component separately, so deleting the frame→store attempt
 * transfer (CHECK mutant m05) passed everywhere. Here a generated-type
 * waiting frame carrying attempt/max_attempts goes through the real
 * store's handleFrame, the real ChatScreen renders, and the rendered
 * waiting notice must show the count — the store→screen handoff, not a
 * direct-props component test. Harness: ChatScreen.truncation-notice
 * (seeds nothing but the frame; ResizeObserver removed so the
 * PlainMessageList fallback renders deterministically).
 *
 * Oracle: verify-report §B3 + founder Q1 A ("attempt N of 3 visible from
 * the first notice") — never the implementation.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, act } from '@testing-library/react'
import * as React from 'react'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import type { ProviderRetryFrame, ProviderStatusFrame, SessionStateFrame } from '@/lib/api/generated/asyncapi-types'

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
    fetchAboutInfo: vi.fn().mockResolvedValue({ preview_port: 5001 }),
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
vi.mock('./IframePreview', () => ({ IframePreview: () => null }))
vi.mock('./markdown-text', () => ({ MarkdownText: () => React.createElement('div', {}) }))
vi.mock('./composer/ModelPicker', () => ({ ModelPicker: () => null }))
vi.mock('./composer/TokenCounter', () => ({ TokenCounter: () => null }))
vi.mock('@/lib/memory-observer', () => ({
  startMemoryObserver: () => ({ dispose: vi.fn(), getCurrentSnapshot: vi.fn() }),
  addMemoryObserver: () => () => {},
  getCurrentSnapshot: () => ({ usedJSHeapSizeBytes: null, level: 'ok', supported: false }),
}))

import { ChatScreen } from './ChatScreen'

const SID = 'test-session-waiting-attempt-1664'

// The frame's server-clock facts (B1's 7 s guard shape): silent 10 s before
// the frame, the attempt ends 7 s after silence began.
const SENT_AT = '2026-11-08T14:00:40.000Z'
const SILENT_SINCE = '2026-11-08T14:00:30.000Z'
const STALL_AT = '2026-11-08T14:00:37.000Z'

function waitingFrame(attempt = 1): ProviderStatusFrame {
  return {
    type: 'provider_status',
    session_id: SID,
    turn_id: 'turn-1664-screen',
    state: 'waiting',
    sent_at: SENT_AT,
    silent_since: SILENT_SINCE,
    stall_at: STALL_AT,
    attempt,
    max_attempts: 3,
    seq: attempt,
  }
}

const expectedStallTime = () =>
  new Intl.DateTimeFormat(undefined, { timeStyle: 'short' }).format(new Date(STALL_AT))

beforeEach(() => {
  useConnectionStore.setState({
    isConnected: true,
    liteMode: false,
    reconnectPhase: null,
    reconnectAttempt: 0,
    connectionError: null,
    connection: null,
  })
  // PlainMessageList fallback — same technique as truncation-notice.
  vi.stubGlobal('ResizeObserver', undefined)
  useSessionStore.setState({ activeSessionId: SID, activeAgentId: 'agent-1' })
  act(() => {
    useChatStore.getState().resetSession()
  })
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.useRealTimers()
})

describe('ChatScreen — the waiting notice shows the frame attempt through the real store (issue #1664 B3, CHECK F2)', () => {
  it('hands the stored attempt and max_attempts to the rendered waiting notice', () => {
    act(() => {
      useChatStore.getState().handleFrame(waitingFrame())
    })
    const stored = useChatStore.getState().sessionsById[SID]?.providerStatusEvent
    expect(stored?.attempt).toBe(1)
    expect(stored?.maxAttempts).toBe(3)
    // The notice slot renders only while the turn is streaming.
    act(() => {
      useChatStore.setState({ isStreaming: true })
    })

    const { container } = render(<ChatScreen />)
    const indicator = container.querySelector('[data-testid="provider-waiting-indicator"]')
    expect(indicator, 'the waiting notice must be on screen').not.toBeNull()
    // The VISIBLE sentence carries the count (F1: never judge by
    // container.textContent, which merges the hidden live region).
    const visible = indicator!.querySelector('span:not([aria-hidden])')
    expect(visible?.textContent).toContain(`If nothing arrives by ${expectedStallTime()}`)
    expect(visible?.textContent).toContain('(attempt 1 of 3)')
  })

  it('updates the real waiting line for attempts 2 and 3 of 3 from later frames', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date(SENT_AT))
    // Seed the live turn through its real frame. A foreground-only busy flag
    // is reset by the next bucket update and would hide later waiting frames.
    const liveTurn: SessionStateFrame = {
      type: 'session_state',
      session_id: SID,
      user_id: 'test-user-1664',
      pending_approvals: [],
      emitted_at: SENT_AT,
      active_turn: { turn_id: 'turn-1664-screen', agent_id: 'agent-1', started_at: SILENT_SINCE },
    }
    act(() => {
      useChatStore.getState().handleFrame(liveTurn)
      useChatStore.getState().handleFrame(waitingFrame())
      useChatStore.setState({ isStreaming: true })
    })
    expect(useChatStore.getState().sessionsById[SID]?.isStreaming).toBe(true)
    const { container } = render(<ChatScreen />)

    for (const [attempt, ending] of [[2, 'this attempt ends'], [3, 'the call ends']] as const) {
      act(() => {
        useChatStore.getState().handleFrame(waitingFrame(attempt))
      })
      const stored = useChatStore.getState().sessionsById[SID]?.providerStatusEvent
      expect(stored?.attempt).toBe(attempt)
      expect(stored?.maxAttempts).toBe(3)
      const indicator = container.querySelector('[data-testid="provider-waiting-indicator"]')
      expect(indicator, `attempt ${attempt} waiting notice must render`).not.toBeNull()
      const visible = indicator!.querySelector('span:not([aria-hidden])')
      const live = indicator!.querySelector('[role="status"]')
      const full = `The model hasn't responded for 10s — still waiting. If nothing arrives by ${expectedStallTime()}, ${ending} (attempt ${attempt} of 3).`
      expect(visible?.textContent).toBe(full)
      expect(live?.textContent).toBe(full)
      expect(visible).toBeVisible()
    }
  })

  it.each([
    ['no_response', 'did not start answering in time'],
    ['empty_reply', 'sent an empty reply'],
  ] as const)('hands the model rather than the provider to the real %s retry line', (errorCode, clause) => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date(SENT_AT))
    const frame: ProviderRetryFrame = {
      type: 'provider_retry',
      session_id: SID,
      turn_id: 'turn-1664-retry-screen',
      provider: 'OpenRouter',
      model: 'model-a',
      sent_at: SENT_AT,
      retry_at: new Date(Date.parse(SENT_AT) + 2_000).toISOString(),
      retry_after_seconds: 2,
      attempt: 2,
      max_attempts: 3,
      error_code: errorCode,
      seq: 1,
    }
    expect(frame.provider).not.toBe(frame.model)
    act(() => {
      useChatStore.getState().handleFrame(frame)
      useChatStore.setState({ isStreaming: true })
    })
    const stored = useChatStore.getState().sessionsById[SID]?.providerRetryEvent
    expect(stored?.provider).toBe('OpenRouter')
    expect(stored?.model).toBe('model-a')
    const { container } = render(<ChatScreen />)
    const indicator = container.querySelector('[data-testid="provider-retry-indicator"]')
    expect(indicator, 'the actual retry indicator must render in ChatScreen').not.toBeNull()
    const visible = indicator!.querySelector('span:not([aria-hidden])')
    const live = indicator!.querySelector('[role="status"]')
    const full = `model-a ${clause}. Retrying automatically in 0:02 (attempt 2 of 3).`
    expect(visible?.textContent).toBe(full)
    expect(live?.textContent).toBe(full)
    expect(visible).toBeVisible()
  })
})

// ChatScreen.queued-target.test.tsx - #1428 step 1, screen level (CHECK round 2, T1).
//
// Oracle: the issue's "Expected" line and the chief's decision Q-a: a message
// queued for another chat is not shown in the open chat. Real: the full
// ChatScreen and the chat/session/connection stores. Stubbed: the network edge
// and AssistantUI internals (same harness as ChatScreen.clear-retry.test.tsx).
// Boundary: no server effect is observable here.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, act } from '@testing-library/react'
import * as React from 'react'
import { QueryClientProvider } from '@tanstack/react-query'
import { useChatStore } from '@/store/chat'
import { emptySessionState } from '@/store/chat/session'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { queryClient } from '@/lib/queryClient'
import { ApiSchemaError } from '@/lib/api/http'
import type { WsConnection } from '@/lib/ws'
import type { ChatMessage } from '@/store/chat'
import { MockButton, MockTextarea } from '@/test/assistantUiMock'
import type { FixtureHistoryPage } from '../../../tests/chat-history-paging/legacy-wire'
import type { ServerFrame } from '@/lib/api/generated/asyncapi-types'
import { entry, expectValidFrame, expectValidPage, page, pos, replay, snapshot, STAMP } from '../../../tests/chat-history-paging/fixtures'

if (typeof Element !== 'undefined' && !Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = function () {}
}

// The network edge. Default: empty arrays for the screen's auxiliary queries;
// the per-test message-path behavior is set inside each test.
const requestMock = vi.hoisted(() => vi.fn())

vi.mock('@/lib/api/http', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api/http')>()
  return {
    ...actual,
    request: requestMock,
  }
})

let sentFrames: unknown[] = []
let messageResponses: Array<() => FixtureHistoryPage> = []

function nextMessageResponse(): unknown {
  const next = messageResponses.shift()
  // A non-deletion, NON-RETRIED failure on purpose. This harness stands in
  // for "the read failed" on an EXISTING chat, and a confirmed 404 now
  // truthfully means "this chat no longer exists" (C10) — the dead-chat
  // screen offers no Retry at all. shouldRetryQuery (lib/queryClient) skips
  // 401/403/404 and ApiSchemaError; a 4xx/5xx body would be retried 3×,
  // inflating messageRequests and racing the queue. The schema-break error
  // is the one failure class that surfaces immediately, is NOT deletion,
  // and keeps the test's intent (a failed read the screen must show).
  if (!next) throw new ApiSchemaError('GET /api/v1/sessions/x/messages', [{ path: [], message: 'messages read failed' }], [])
  return next()
}

vi.mock('@assistant-ui/react', async () => (await import('@/test/assistantUiMock')).createAssistantUiMock({
  ThreadPrimitive: {
    Viewport: ({ children, className }: { children: React.ReactNode; className?: string }) =>
      React.createElement('div', { className }, children),
  },
  ComposerPrimitive: {
    Root: ({ children, className }: { children?: React.ReactNode; className?: string }) =>
      React.createElement('div', { className }, children),
    Input: ({ onChange, onKeyDown, onBlur, disabled, placeholder, className }: {
      onChange?: (e: React.ChangeEvent<HTMLTextAreaElement>) => void
      onKeyDown?: (e: React.KeyboardEvent<HTMLTextAreaElement>) => void
      onBlur?: () => void
      disabled?: boolean
      placeholder?: string
      className?: string
    }) =>
      React.createElement(MockTextarea, {
        disabled, placeholder, className, onChange, onKeyDown, onBlur,
        'data-testid': 'composer-input',
      }),
    Send: ({ disabled, children, className, 'data-testid': testId }: {
      disabled?: boolean; children?: React.ReactNode; className?: string; 'data-testid'?: string
    }) =>
      React.createElement(MockButton, {
        type: 'button', disabled, className, 'data-testid': testId ?? 'chat-send', children,
      }),
  },
  useComposerRuntime: () => ({
    getState: () => ({ text: '' }),
    setText: vi.fn(),
    addAttachment: vi.fn(),
    subscribe: vi.fn(() => vi.fn()),
  }),
  useMessage: () => ({
    id: 'msg_1',
    role: 'assistant',
    status: { type: 'complete' },
    content: [],
  }),
}))

vi.mock('@tanstack/react-router', () => ({
  useRouter: () => ({ navigate: vi.fn() }),
  useSearch: () => ({}),
  Link: ({ children }: { children: React.ReactNode }) => children,
}))

vi.mock('@/assets/logo/omnipus-avatar.svg?url', () => ({ default: 'omnipus-avatar.svg' }))
vi.mock('./RateLimitIndicator', () => ({ RateLimitIndicator: () => null }))
vi.mock('./markdown-text', () => ({ MarkdownText: () => null }))
vi.mock('./tools/GenericToolCall', () => ({ GenericToolCall: () => null }))
vi.mock('./composer/ModelPicker', () => ({ ModelPicker: () => null }))
vi.mock('./composer/TokenCounter', () => ({ TokenCounter: () => null }))
vi.mock('./historical-markdown', () => ({
  HistoricalMessageMarkdown: ({ content }: { content: string }) =>
    React.createElement('div', { 'data-testid': 'historical-markdown' }, content),
}))

import { ChatScreen } from './ChatScreen'

const SID = 'sess-queued-target-screen'

function publishConnectedOpening(): void {
  const history = page([entry(1, {
    id: 'srv-u-seed', role: 'user', agent_id: 'jim', content: 'earlier question', timestamp: '2026-10-10T00:00:00Z',
  })], { sessionId: SID })
  expectValidPage(history, [pos(1)], 1)
  const frames: ServerFrame[] = [
    { ...snapshot(history), seq: 1 },
    { type: 'session_state', session_id: SID, user_id: 'clear-retry-reader', history_state: { status: 'available' }, pending_approvals: [], emitted_at: STAMP },
    ...history.entries.map(source => replay(source, SID)),
    { type: 'done', session_id: SID },
    { type: 'catch_up_complete', session_id: SID, seq: 1, boot_id: 'history-boot', mode: 'snapshot' },
  ]
  frames.forEach(expectValidFrame)
  frames.forEach(frame => useChatStore.getState().handleFrame(frame))
  expect(useChatStore.getState().history.historyLoaded, 'positive opening instrument: the real WS snapshot committed').toBe(true)
  expect(useChatStore.getState().sessionsById[SID].cursor).toStrictEqual({ bootId: 'history-boot', seq: 1 })
}

function seedIdleBucket(): void {
  const seeded: ChatMessage[] = [
    { id: 'u-seed', role: 'user', content: 'earlier question', timestamp: new Date().toISOString(), status: 'done' },
  ]
  const byId = Object.fromEntries(seeded.map((m) => [m.id, m]))
  const defaults = emptySessionState()
  useChatStore.setState((s) => ({
    ...s,
    sessionsById: {
      ...s.sessionsById,
      [SID]: {
        ...defaults,
        messagesById: byId,
        messageOrder: seeded.map((m) => m.id),
        isStreaming: false,
        isReplaying: false,
        replayCompletedForSession: SID,
        activeTurnId: null,
        // Reset the sequence cursor too: a previous test's frames advance it,
        // and a stale cursor would drop this test's frames as already-seen.
        cursor: null,
        toolCalls: {},
        toolCallOrder: [],
        textAtToolCallStart: {},
        sessionTokens: 0,
        sessionCost: 0,
      },
    },
    // The screen renders the FOREGROUND projection — mirror the bucket (the
    // store syncs it on every withBucket write; the seed must do the same).
    messages: seeded,
    messagesById: byId,
    history: defaults.history,
    isStreaming: false,
    isReplaying: false,
    replayCompletedForSession: SID,
  }))
  useSessionStore.setState({ activeSessionId: SID, activeAgentId: 'jim', activeAgentType: null })
}

beforeEach(() => {
  sentFrames = []
  messageResponses = []
  queryClient.removeQueries()
  requestMock.mockReset()
  requestMock.mockImplementation(async (path: string) => {
    if (typeof path === 'string' && path.includes('/messages')) return nextMessageResponse()
    if (typeof path === 'string' && path.includes('/about')) return {}
    return []
  })
  seedIdleBucket()
  useConnectionStore.setState({
    connection: { send: (p: unknown) => { sentFrames.push(p); return true }, close: () => {} } as unknown as WsConnection,
    isConnected: true,
    connectionError: null,
  })
  // jsdom: force the PlainMessageList path so the thread renders every row.
  vi.unstubAllGlobals()
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  ;(globalThis as any).ResizeObserver = undefined
})

afterEach(() => {
  vi.unstubAllGlobals()
})


describe('ChatScreen - queued rows belong to the open chat (#1428 step 1)', () => {
  it('shows a queued message of this chat once, and hides one queued for another chat', async () => {
    act(() => publishConnectedOpening())
    let container!: HTMLElement
    await act(async () => {
      const result = render(
        <QueryClientProvider client={queryClient}>
          <ChatScreen />
        </QueryClientProvider>,
      )
      container = result.container
    })
    await vi.waitFor(() => { expect(container.textContent).toContain('earlier question') })
    act(() => {
      useChatStore.setState({
        outboundQueue: [
          { id: 'q-other', content: 'queued for another chat', timestamp: '2026-11-14T10:00:01.000Z', targetSessionId: 'sess-other' },
          { id: 'q-here', content: 'queued for this chat', timestamp: '2026-11-14T10:00:02.000Z', targetSessionId: SID },
        ],
      })
    })
    await vi.waitFor(() => { expect(container.textContent).toContain('queued for this chat') })
    expect(container.textContent, 'the other chat\'s message is not shown here').not.toContain('queued for another chat')
    expect(container.textContent?.split('queued for this chat').length, 'shown exactly once').toBe(2)
  })
})

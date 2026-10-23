// ChatScreen.clear-retry.test.tsx — FR-030/031 (U10b) V3, screen level.
//
// The history Retry is the recovery control for a /clear whose transcript
// re-read failed: when an operation is pending for the session, the click
// must run THAT operation's own read+apply (which publishes into the
// history cache on success) — not a competing ordinary refetch — and the
// failure must be VISIBLE as this screen's existing history-error state.
//
// What is real: the full ChatScreen (its own React Query useQuery, the
// history-error block, the actual Retry button, the thread render through
// the real adapter), the real QueryClient singleton shared with the chat
// store, the store's send/frame paths. What is stubbed: the network edge
// (the HTTP request function) and AssistantUI's runtime internals.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'
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
import { clearHistoryPage, wireHistoryPage, type FixtureHistoryPage } from '../../../tests/chat-history-paging/legacy-wire'

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
let messageRequests = 0

function rawUser(id: string, clientMessageId: string | undefined, content: string, ts: string): Record<string, unknown> {
  return { id, role: 'user', content, timestamp: ts, agent_id: 'jim', status: 'ok', ...(clientMessageId ? { client_message_id: clientMessageId } : {}) }
}
function rawAssistant(id: string, content: string, ts: string): Record<string, unknown> {
  return { id, role: 'assistant', content, timestamp: ts, agent_id: 'jim', status: 'ok' }
}
function rawSystem(id: string, content: string, ts: string): Record<string, unknown> {
  return { id, role: 'system', content, timestamp: ts, agent_id: 'jim', status: 'ok' }
}
const MARKER_TEXT = 'Conversation context cleared'
const SUCCESS_REPLY = 'Context cleared. The conversation and its history are kept; the assistant continues from here with a fresh context.'

function rawPostClear(_clearCmid: string): FixtureHistoryPage {
  // D3/D6: latest starts at the reached clear, never with the cleared command
  // or content before it. The original response/marker identity is retained.
  return clearHistoryPage(SID, [
    rawSystem('srv-clear-marker', MARKER_TEXT, '2026-10-10T00:00:00Z'),
    rawAssistant('srv-a-reply', SUCCESS_REPLY, '2026-10-10T00:00:01Z'),
  ], 2)
}

function nextMessageResponse(): unknown {
  messageRequests += 1
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

const SID = 'sess-clear-retry-screen'

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
  messageRequests = 0
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

describe('ChatScreen — the history Retry completes a failed /clear refresh (V3)', () => {
  it('T4: a failed operation read is VISIBLE as the history-error state; Retry recovers with one /clear frame', async () => {
    // The initial history load succeeds.
    messageResponses.push(() => wireHistoryPage(SID, [rawUser('srv-u-seed', undefined, 'earlier question', '2026-10-10T00:00:00Z')], 1))
    let container!: HTMLElement
    await act(async () => {
      const result = render(
        <QueryClientProvider client={queryClient}>
          <ChatScreen />
        </QueryClientProvider>,
      )
      container = result.container
    })
    await vi.waitFor(() => {
      expect(container.textContent).toContain('earlier question')
    })
    expect(screen.queryByText('Could not load messages.')).toBeNull()

    // The /clear is sent; its transcript read FAILS.
    const readsBefore = messageRequests
    act(() => { useChatStore.getState().sendMessage('/clear') })
    expect(sentFrames).toHaveLength(1)
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'done', session_id: SID, message_id: 'a-reply', turn_id: 'turn-clear', seq: 1, stats: { tokens: 1, cost: 0 },
      } as never)
    })

    // The failure is VISIBLE: the screen's own history-error state, with
    // Retry. (The read's rejection settles the query back into the error
    // state after the refetch resets it — wait for THAT, not the transient.)
    await vi.waitFor(() => {
      const state = queryClient.getQueryState(['messages', SID])
      expect(state?.status).toBe('error')
      expect(screen.getByText('Could not load messages.')).toBeInTheDocument()
    })
    expect(screen.getByRole('button', { name: /Retry/i })).toBeInTheDocument()

    // The recovery: the messages endpoint now answers with the post-clear view.
    const clearCmid = (sentFrames[0] as { client_message_id: string }).client_message_id
    messageResponses.push(() => rawPostClear(clearCmid))
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /Retry/i }))
    })

    // The divider appears — the operation's own read+apply ran.
    await vi.waitFor(() => {
      expect(container.querySelector('[data-testid="clear-context-divider"]')).toBeTruthy()
    })
    // No lingering error, exactly one /clear frame, and the click caused
    // exactly ONE network read (the operation's own — no competing refetch).
    expect(screen.queryByText('Could not load messages.')).toBeNull()
    expect(sentFrames).toHaveLength(1)
    expect(messageRequests).toBe(readsBefore + 2)
  })

  it('T3: from the history-error state, the Retry click runs ONLY the operation read (no competing request)', async () => {
    // The history endpoint fails from the start: the screen mounts into the
    // error state.
    let container!: HTMLElement
    await act(async () => {
      const result = render(
        <QueryClientProvider client={queryClient}>
          <ChatScreen />
        </QueryClientProvider>,
      )
      container = result.container
    })
    await vi.waitFor(() => {
      expect(screen.getByText('Could not load messages.')).toBeInTheDocument()
    })

    // A /clear goes out and its read fails too (still no good response).
    act(() => { useChatStore.getState().sendMessage('/clear') })
    expect(sentFrames).toHaveLength(1)
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'done', session_id: SID, message_id: 'a-reply', turn_id: 'turn-clear', seq: 1, stats: { tokens: 1, cost: 0 },
      } as never)
    })
    await vi.waitFor(() => {
      expect(messageRequests).toBe(2)
    })
    // The op read's rejection settles the query back into the error state.
    await vi.waitFor(() => {
      const state = queryClient.getQueryState(['messages', SID])
      expect(state?.status).toBe('error')
      expect(screen.getByText('Could not load messages.')).toBeInTheDocument()
    })

    // The endpoint now answers with the post-clear view.
    const clearCmid = (sentFrames[0] as { client_message_id: string }).client_message_id
    messageResponses.push(() => rawPostClear(clearCmid))
    const readsBefore = messageRequests
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /Retry/i }))
    })

    await vi.waitFor(() => {
      expect(container.querySelector('[data-testid="clear-context-divider"]')).toBeTruthy()
    })
    expect(screen.queryByText('Could not load messages.')).toBeNull()
    // Exactly ONE read from the click, one /clear frame total, and the error
    // stays gone (no competing request flips it back).
    expect(messageRequests).toBe(readsBefore + 1)
    expect(sentFrames).toHaveLength(1)
    await new Promise((r) => setTimeout(r, 25))
    expect(screen.queryByText('Could not load messages.')).toBeNull()
    expect(container.querySelector('[data-testid="clear-context-divider"]')).toBeTruthy()
  })
})

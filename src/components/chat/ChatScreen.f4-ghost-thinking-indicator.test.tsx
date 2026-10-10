/**
 * ChatScreen.f4-ghost-thinking-indicator.test.tsx
 *
 * F4 (second review wave on branch fix/615-617-618-hardening): VirtualAssistant
 * MessageRow's `hasVisibleToolCalls` computation (used only to decide
 * `showEmptyPlaceholder`) called `wouldToolCallBeVisible(tc.tool, tc.params,
 * tc.result, !!tc.error, verboseChatEnabled)` — the `!!tc.error` disjunct is
 * exactly the signal #617 established is EMPTY for an ordinary failure
 * (pkg/gateway/websocket.go deliberately leaves `Result.Error` unset when
 * `Result` still holds the text). Its two siblings in this same file already
 * moved off that proxy: the live-path equivalent passes `!!part.isError`, and
 * the row's own actual render for the SAME `tc` uses `tc.status === 'error'`
 * directly (VirtualAssistantMessageRow's GenericToolCall/BrowserToolReplayBlock/
 * WebServeBlock call sites, all `isError={tc.status === 'error'}`).
 *
 * Concretely: a streaming message whose only content is a failed `ToolSearch`
 * call (status:'error', no `error` string — the exact producible shape #617
 * is about) computed `hasVisibleToolCalls: false` under the OLD code, while
 * the row itself — whose own visibility gate reads `tc.status === 'error'`
 * — still rendered the tool row (ToolSearch's shouldRenderToolCall case
 * forces visibility on error). `showEmptyPlaceholder` unconditionally gates
 * only the ThinkingIndicator (`{showEmptyPlaceholder && <ThinkingIndicator
 * />}`), NOT the tool-call render loop below it (`messageParts.map` has no
 * matching `{!showEmptyPlaceholder && ...}` guard) — so BOTH rendered at
 * once: a ghost "Thinking…" indicator sitting above a legitimately-visible
 * "Failed" tool row.
 *
 * This message is reachable via PlainMessageList (the ResizeObserver-
 * unavailable fallback) — unlike VirtualizedMessageListInner, it renders
 * every message including an in-flight one (isStreaming:true) through
 * VirtualAssistantMessageRow directly, without splitting the streaming
 * placeholder out into ThreadPrimitive.Messages. Same technique as
 * ChatScreen.issue-617-replay-outcome.test.tsx (ResizeObserver forced
 * undefined, GenericToolCall left unmocked so the real "Failed" label
 * renders).
 *
 * Regression-proof dispatch (2026-10-09): seed ONLY an empty streaming reply.
 * ToolSearch state must arrive through useChatStore.handleFrame, not through a
 * fixture's tool_calls field. The real store, foreground projection, fallback
 * row, visibility gate and phase indicator are all left unmocked. A missing
 * live-call projection must therefore fail these tests instead of being hidden
 * by a pre-populated message. The fallback phase case also drives start/result
 * after mounting and checks Thinking -> Working -> Thinking.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, act, screen, within } from '@testing-library/react'
import * as React from 'react'
import { useChatStore, makeBucketMessages } from '@/store/chat'
import type { ChatMessage } from '@/store/chat'
import type { ToolCallStartFrame, ToolCallResultFrame } from '@/lib/api/generated/asyncapi-types'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import { useToolApprovalStore } from '@/store/toolApproval'

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
// `./tools/GenericToolCall` deliberately LEFT UNMOCKED — the whole point of
// this test is that the real row (whose own visibility gate reads
// `tc.status === 'error'`) renders "Failed" for the failed ToolSearch call
// while `hasVisibleToolCalls` must ALSO see it as visible (or the ghost
// ThinkingIndicator bug reproduces).
vi.mock('./IframePreview', () => ({ IframePreview: () => null }))
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

const SID = 'test-session-f4-ghost-thinking-indicator'

const THINKING_TEXT_RE =
  /Thinking…|Working on it…|Composing a response…|Processing your request…|Analyzing…|Considering the details…|Piecing it together…|Reasoning it through…|Working through this…|Gathering my thoughts…|Figuring out the approach…|Reviewing the context…|Drafting a response…|Making sense of it…|Weighing the options…/

function seedBucket(messages: ChatMessage[]): void {
  const bucket = makeBucketMessages(messages)
  useChatStore.setState((s) => ({
    ...s,
    sessionsById: {
      [SID]: {
        ...((s.sessionsById ?? {})[SID] ?? {}),
        ...bucket,
        isStreaming: true,
        isReplaying: false,
        replayCompletedForSession: SID,
        toolCalls: {},
        toolCallOrder: [],
        textAtToolCallStart: {},
        toolCallOwnerMessageId: {},
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
    replayCompletedForSession: SID,
  }))
}

function seedEmptyStreamingReply(messageId: string): void {
  seedBucket([{
    id: messageId,
    role: 'assistant',
    content: '',
    timestamp: new Date().toISOString(),
    status: 'streaming',
    isStreaming: true,
    agentId: 'agent-1',
  }])
}

function startToolSearch(callId: string, messageId: string): void {
  // These are the generated wire fields. Tool frames carry no message_id:
  // the production reducer correlates call_id to the existing assistant reply.
  const frame: ToolCallStartFrame = {
    type: 'tool_call_start',
    session_id: SID,
    call_id: callId,
    tool: 'ToolSearch',
    params: { name: 'foo' },
    agent_id: 'agent-1',
  }
  act(() => useChatStore.getState().handleFrame(frame))

  const bucket = useChatStore.getState().sessionsById[SID]
  expect(bucket.toolCalls[callId], 'real start frame must register a running ToolSearch').toEqual({
    id: callId,
    call_id: callId,
    tool: 'ToolSearch',
    params: { name: 'foo' },
    status: 'running',
  })
  expect(bucket.toolCallOrder, 'start frame must register this call exactly once').toEqual([callId])
  expect(bucket.toolCallOwnerMessageId, 'the owner map must exist after a start frame').toBeDefined()
  expect(bucket.toolCallOwnerMessageId?.[callId], 'running call must belong to the seeded reply').toBe(messageId)
  expect(bucket.messageOrder, 'start frame must not create a different reply').toEqual([messageId])
  expect(bucket.messagesById[messageId].content).toBe('')
  expect(bucket.messagesById[messageId].isStreaming).toBe(true)
}

function resolveToolSearch(callId: string, status: ToolCallResultFrame['status'], result: unknown): void {
  const frame: ToolCallResultFrame = {
    type: 'tool_call_result',
    session_id: SID,
    call_id: callId,
    tool: 'ToolSearch',
    status,
    result,
    // Deliberately no error string: status:error alone must make F4 visible.
  }
  act(() => useChatStore.getState().handleFrame(frame))

  const call = useChatStore.getState().sessionsById[SID].toolCalls[callId]
  expect(call.status, 'real result frame must settle the same call').toBe(status)
  expect(call.result, 'real result frame must retain its payload').toEqual(result)
  expect(call.error, 'F4 must not depend on an error string').toBeUndefined()
}

function plainReply(messageId: string): HTMLElement {
  expect(globalThis.ResizeObserver, 'force the real PlainMessageList fallback').toBeUndefined()
  const reply = screen.getByTestId('assistant-message')
  expect(reply).toHaveAttribute('data-message-id', messageId)
  expect(reply.closest('[data-index]'), 'plain reply must not be a virtualized row').toBeNull()
  return reply
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
  vi.unstubAllGlobals()
  // Forces PlainMessageList (ResizeObserver-unavailable fallback), which
  // renders every message — including an in-flight one — through
  // VirtualAssistantMessageRow. See file header.
  vi.stubGlobal('ResizeObserver', undefined)
  useChatPreferencesStore.getState().setVerboseChatEnabled(false)
  useToolApprovalStore.setState({ queue: [], resolvedIds: [] })

  useSessionStore.setState({ activeSessionId: SID, activeAgentId: 'agent-1' })
  act(() => {
    useChatStore.getState().resetSession()
  })
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('F4 — ghost ThinkingIndicator over a visible failed ToolSearch row', () => {
  it('a streaming message whose only content is a failed ToolSearch (status:error, no error string) shows the "Failed" row and does NOT also show the ghost thinking indicator', async () => {
    const messageId = 'msg_ghost'
    seedEmptyStreamingReply(messageId)

    await act(async () => {
      render(<ChatScreen />)
    })
    plainReply(messageId)
    startToolSearch('tc_ghost', messageId)
    // Error results may be null per ToolCallResultFrame; the status alone is
    // the F4 error oracle. No populated tool_calls fixture or explicit bake.
    resolveToolSearch('tc_ghost', 'error', null)

    // Keep the original F4 assertions: the real failed row must render, and
    // that visible row must not share its empty reply with a ghost indicator.
    expect(screen.getByText('Failed')).toBeInTheDocument()
    expect(screen.queryByText(THINKING_TEXT_RE)).toBeNull()
    expect(screen.queryByText('Thinking', { exact: true })).toBeNull()
  })

  it('control: a streaming message whose only content is a SUCCESSFUL ToolSearch call correctly shows the ghost thinking indicator (ToolSearch stays hidden by default, so the message legitimately has no visible content yet)', async () => {
    const messageId = 'msg_ghost_ok'
    seedEmptyStreamingReply(messageId)

    await act(async () => {
      render(<ChatScreen />)
    })
    plainReply(messageId)
    startToolSearch('tc_ghost_ok', messageId)
    resolveToolSearch('tc_ghost_ok', 'success', { ok: true })

    // Successful ToolSearch remains hidden. Keep the original boundary
    // assertions and also check the stable accessible Thinking phase label.
    expect(screen.queryByText('Failed')).toBeNull()
    expect(screen.queryByText('Done')).toBeNull()
    expect(screen.getByText(THINKING_TEXT_RE)).toBeInTheDocument()
    expect(screen.getByText('Thinking', { exact: true })).toBeInTheDocument()
  })
})

describe('plain-list fallback — real tool frame phase transition', () => {
  it('shows Working while the correlated tool executes, then Thinking after tool_call_result', async () => {
    const messageId = 'msg_fallback_phase'
    const callId = 'tc_fallback_phase'
    seedEmptyStreamingReply(messageId)

    await act(async () => {
      render(<ChatScreen />)
    })
    const reply = within(plainReply(messageId))
    expect(reply.getByText('Thinking', { exact: true })).toBeInTheDocument()
    expect(reply.getByText(THINKING_TEXT_RE)).toBeInTheDocument()

    startToolSearch(callId, messageId)
    // Soft assertions remain CI failures, but allow the result frame and the
    // return-to-Thinking checks to run even if this Working transition is red.
    expect.soft(
      reply.queryByText(THINKING_TEXT_RE)?.textContent ?? null,
      'tool_call_start must change the plain reply to Working (not Thinking)',
    ).toBe('Working on it…')
    // The thinking phase can rotate to the *same* Working on it… phrase.
    // Its stable accessible label must therefore disappear too; text alone
    // would let a random thinking beat manufacture a false green.
    expect.soft(
      reply.queryByText('Thinking', { exact: true }),
      'Working must replace the stable Thinking phase label',
    ).toBeNull()

    resolveToolSearch(callId, 'success', { ok: true })
    expect(reply.getByText('Thinking', { exact: true })).toBeInTheDocument()
    expect(reply.getByText(THINKING_TEXT_RE)).toBeInTheDocument()
    expect(useChatStore.getState().sessionsById[SID].messagesById[messageId].isStreaming).toBe(true)
  })
})

import { describe, it, expect, beforeEach, vi } from 'vitest'
import { act } from 'react'
import { useChatStore, getMessages, makeBucketMessages, MAX_MESSAGES_PER_SESSION, clampToolResult, isClientTruncatedResult, evictMessageFromBucket, findLastAssistantMessageId, findAssistantMessageIdByTurnId } from './chat'
import type { SessionChatState, ChatMessage } from './chat'
import { useConnectionStore } from './connection'
import { useSessionStore } from './session'
import { useWhatsAppPairingStore } from './whatsappPairing'
import { useWorkspacesStore } from './workspacesStore'
import type { WhatsAppPairingFrame } from '@/lib/api/generated/asyncapi-types'

// test_chat_store (test #22)
// Traces to: wave5a-wire-ui-spec.md — Scenario: User sends message and receives streaming response
//             wave5a-wire-ui-spec.md — Scenario: Cancel during streaming preserves partial response

const TEST_SESSION_ID = 'test-session-1'

function resetStore() {
  act(() => {
    // F-S3: clearStreamingState() also drains the module-scoped
    // pendingCancelAckSids tracker (see chat.ts) — call it before rebuilding
    // state so a cancelStream(sessionId) from a PRIOR test can never bleed
    // into this test's missing-session_id frame-routing fallback.
    useChatStore.getState().clearStreamingState()
    useChatStore.setState({
      sessionsById: {},
      messages: [],
      isStreaming: false,
      toolCalls: {},
      toolCallOrder: [],
      textAtToolCallStart: {},
      sessionTokens: 0,
      sessionCost: 0,
      isReplaying: false,
      replayCompletedForSession: null,
      rateLimitEvent: null,
      lastUserMessageAt: null,
      cancelStage: null,
      lastReceivedEventTime: null,
    })
    useConnectionStore.setState({
      connection: null,
      isConnected: false,
      connectionError: null,
    })
    useSessionStore.setState({
      activeSessionId: TEST_SESSION_ID,
      activeAgentId: null,
      activeAgentType: null,
    })
    // M4: default to "not in a workspace" so the global-chat assertions hold
    // unless a test explicitly sets an active workspace.
    useWorkspacesStore.setState({ activeWorkspaceId: null })
  })
}

beforeEach(resetStore)

describe('chat store — initial state', () => {
  it('initializes with empty messages, not streaming; activeSessionId set by beforeEach', () => {
    const chatState = useChatStore.getState()
    const sessionState = useSessionStore.getState()
    expect(chatState.messages).toEqual([])
    expect(chatState.isStreaming).toBe(false)
    // beforeEach sets activeSessionId to TEST_SESSION_ID so per-session actions have a target.
    expect(sessionState.activeSessionId).toBe(TEST_SESSION_ID)
    expect(sessionState.activeAgentId).toBeNull()
  })
})

describe('chat store — session management', () => {
  it('setActiveSession updates activeSessionId and activeAgentId without wiping buckets', () => {
    // Seed a message in the first session bucket.
    act(() => {
      useChatStore.getState().appendMessage({
        id: 'msg_original',
        session_id: TEST_SESSION_ID,
        role: 'user',
        content: 'Original session message',
        timestamp: '2026-03-29T10:00:00Z',
        status: 'done',
      })
    })
    // Switch to a different session — the original bucket must survive.
    act(() => {
      useSessionStore.getState().setActiveSession('sess_abc', 'general-assistant')
    })
    const sessionState = useSessionStore.getState()
    expect(sessionState.activeSessionId).toBe('sess_abc')
    expect(sessionState.activeAgentId).toBe('general-assistant')
    // Original bucket is intact (not wiped by session switch).
    const bucket = useChatStore.getState().sessionsById[TEST_SESSION_ID]
    const msgs = bucket ? getMessages(bucket) : []
    expect(msgs).toHaveLength(1)
    expect(msgs[0].content).toBe('Original session message')
  })
})

describe('chat store — message handling', () => {
  it('appendMessage adds a user message to the thread', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: User message appears optimistically
    act(() => {
      useChatStore.getState().appendMessage({
        id: 'user_1',
        session_id: 'sess_1',
        role: 'user',
        content: 'Hello, world!',
        timestamp: '2026-03-29T10:00:00Z',
        status: 'done',
      })
    })
    const { messages } = useChatStore.getState()
    expect(messages).toHaveLength(1)
    expect(messages[0].role).toBe('user')
    expect(messages[0].content).toBe('Hello, world!')
  })

  it('setMessages replaces all messages and resets tool calls', () => {
    act(() => {
      useChatStore.getState().appendMessage({
        id: 'old_1',
        session_id: 'sess_1',
        role: 'user',
        content: 'Old message',
        timestamp: '2026-03-29T09:00:00Z',
        status: 'done',
      })
      useChatStore.getState().setMessages([
        {
          id: 'new_1',
          session_id: 'sess_2',
          role: 'user',
          content: 'New message',
          timestamp: '2026-03-29T10:00:00Z',
          status: 'done',
        },
      ])
    })
    const { messages, toolCalls, sessionTokens } = useChatStore.getState()
    expect(messages).toHaveLength(1)
    expect(messages[0].content).toBe('New message')
    expect(Object.keys(toolCalls)).toHaveLength(0)
    expect(sessionTokens).toBe(0)
  })
})

describe('chat store — streaming via handleFrame', () => {
  it('handleFrame(token) appends content to the last assistant message', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Streaming response — tokens append
    act(() => {
      useChatStore.getState().appendMessage({
        id: 'asst_1',
        session_id: 'sess_1',
        role: 'assistant',
        content: '',
        timestamp: '2026-03-29T10:00:01Z',
        status: 'streaming',
        isStreaming: true,
      })
      useChatStore.setState({ isStreaming: true })
      useChatStore.getState().handleFrame({ type: 'token', content: 'Hello', session_id: TEST_SESSION_ID })
      useChatStore.getState().handleFrame({ type: 'token', content: ' world', session_id: TEST_SESSION_ID })
    })
    const { messages } = useChatStore.getState()
    const asst = messages.find((m) => m.id === 'asst_1')
    expect(asst?.content).toBe('Hello world')
    expect(asst?.isStreaming).toBe(true)
  })

  it('handleFrame(done) marks last assistant message as done and sets isStreaming false', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Streaming completes with markdown rendering
    act(() => {
      useChatStore.getState().appendMessage({
        id: 'asst_2',
        session_id: 'sess_1',
        role: 'assistant',
        content: '# Heading\nParagraph',
        timestamp: '2026-03-29T10:00:01Z',
        status: 'streaming',
        isStreaming: true,
      })
      useChatStore.setState({ isStreaming: true })
      useChatStore.getState().handleFrame({ type: 'done', stats: { tokens: 150, cost: 0.02, duration_ms: 0 }, session_id: TEST_SESSION_ID })
    })
    const state = useChatStore.getState()
    expect(state.isStreaming).toBe(false)
    const asst = state.messages.find((m) => m.id === 'asst_2')
    expect(asst?.status).toBe('done')
    expect(asst?.isStreaming).toBe(false)
    expect(state.sessionTokens).toBe(150)
    expect(state.sessionCost).toBeCloseTo(0.02)
  })

  // Live UAT (2026-08-26): the provider returned a response the engine judged
  // empty. pkg/agent/loop.go substitutes its `defaultResponse` fallback text
  // ("The model returned an empty response...") for the SUCCESS-path
  // finalContent, but pkg/gateway/websocket.go's wsStreamer.Finalize only
  // marks the chat "streamed" (suppressing webchatChannel.Send's own
  // token+done fallback) when `accumulated.Len() > 0` — i.e. when at least
  // one real token was actually streamed live. An empty-response turn never
  // streams anything, so Finalize's own `done` frame lands FIRST (finalizing
  // the optimistic placeholder with content:'' still empty), and THEN
  // webchatChannel.Send fires its own token+done pair carrying the real
  // fallback text — landing on an assistant bubble that is already closed
  // (isStreaming:false). Per the 'closed bubble = new segment' boundary
  // rule a few lines above the guard this test exercises, that would abandon
  // the empty placeholder and open a brand-new bubble for the fallback text
  // — leaving TWO assistant bubbles on screen: an empty one with a Copy
  // button that copies nothing, and a second one holding the real text.
  // The `lastMsgIsEmptyTerminal` guard reuses a closed bubble instead of
  // abandoning it when — and only when — that bubble finalized holding
  // nothing at all, collapsing this exact two-frame delivery into ONE
  // bubble with the real content.
  it('handleFrame: a trailing token+done pair after an EMPTY terminal placeholder merges into the same bubble, not a second one', () => {
    const FALLBACK_TEXT = 'The model returned an empty response. This may indicate a provider error or token limit.'
    act(() => {
      // The optimistic placeholder sendMessage() creates.
      useChatStore.getState().appendMessage({
        id: 'asst_empty_final',
        session_id: 'sess_1',
        role: 'assistant',
        content: '',
        timestamp: '2026-03-29T10:00:01Z',
        status: 'streaming',
        isStreaming: true,
      })
      useChatStore.setState({ isStreaming: true })
      // wsStreamer.Finalize's `done` — no token frame preceded it, since the
      // LLM call itself produced nothing.
      useChatStore.getState().handleFrame({ type: 'done', stats: { tokens: 0, cost: 0, duration_ms: 5 }, session_id: TEST_SESSION_ID })
    })

    // Placeholder is now terminal and still empty — exactly the state a
    // user would see rendered as a bare Copy-button bubble without the
    // ChatScreen.tsx render-layer fix.
    const midState = useChatStore.getState()
    const midAsst = midState.messages.find((m) => m.id === 'asst_empty_final')
    expect(midAsst?.content).toBe('')
    expect(midAsst?.isStreaming).toBe(false)
    expect(midAsst?.status).toBe('done')

    act(() => {
      // webchatChannel.Send's own fallback delivery — a second, independent
      // token+done pair for the SAME chat.
      useChatStore.getState().handleFrame({ type: 'token', content: FALLBACK_TEXT, session_id: TEST_SESSION_ID })
      useChatStore.getState().handleFrame({ type: 'done', stats: { tokens: 0, cost: 0, duration_ms: 0 }, session_id: TEST_SESSION_ID })
    })

    const state = useChatStore.getState()
    const assistantMsgs = state.messages.filter((m) => m.role === 'assistant')
    // Exactly one assistant bubble — the fallback text landed on the SAME
    // message as the empty placeholder, not a second, newly-minted one.
    expect(assistantMsgs).toHaveLength(1)
    expect(assistantMsgs[0].id).toBe('asst_empty_final')
    expect(assistantMsgs[0].content).toBe(FALLBACK_TEXT)
    expect(assistantMsgs[0].isStreaming).toBe(false)
    expect(assistantMsgs[0].status).toBe('done')
  })

  it('handleFrame(error) sets message to error status — message-level error does NOT set connectionError', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: WebSocket connection error during streaming
    // When an assistant message already exists, the error is message-level (e.g. LLM rejected
    // the request). The inline bubble is updated; connectionError is NOT set to avoid falsely
    // showing a connection-down banner when the connection is fine.
    act(() => {
      useChatStore.getState().appendMessage({
        id: 'asst_3',
        session_id: 'sess_1',
        role: 'assistant',
        content: '',
        timestamp: '2026-03-29T10:00:01Z',
        status: 'streaming',
        isStreaming: true,
      })
      useChatStore.setState({ isStreaming: true })
      useConnectionStore.setState({ connectionError: null })
      useChatStore.getState().handleFrame({ type: 'error', message: 'LLM quota exceeded' })
    })
    const chatState = useChatStore.getState()
    expect(chatState.isStreaming).toBe(false)
    const asst = chatState.messages.find((m) => m.id === 'asst_3')
    expect(asst?.status).toBe('error')
    // Message-level error must NOT propagate to the connection error banner
    expect(useConnectionStore.getState().connectionError).toBeNull()
  })

  it('handleFrame(error) with no assistant message sets connectionError banner', () => {
    // When no assistant message exists, the error is connection-level (e.g. the WS frame
    // arrived before the server could even start a reply). Both the error message AND
    // connectionError are set so the banner shows.
    act(() => {
      useChatStore.setState({ isStreaming: true })
      useChatStore.getState().handleFrame({ type: 'error', message: 'Connection lost' })
    })
    const chatState = useChatStore.getState()
    expect(chatState.isStreaming).toBe(false)
    expect(useConnectionStore.getState().connectionError).toBe('Connection lost')
    const errMsg = chatState.messages.find((m) => m.status === 'error')
    expect(errMsg).toBeDefined()
  })
})

describe('chat store — tool calls', () => {
  it('startToolCall registers a running tool call', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Running tool call shows spinner
    act(() => {
      useChatStore.getState().startToolCall('tc_1', 'web_search', { query: 'AWS pricing' })
    })
    const { toolCalls } = useChatStore.getState()
    expect(toolCalls['tc_1']).toMatchObject({
      call_id: 'tc_1',
      tool: 'web_search',
      status: 'running',
    })
  })

  it('resolveToolCall updates status to success with result', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Successful tool call collapses by default
    act(() => {
      useChatStore.getState().startToolCall('tc_2', 'exec', { command: 'ls' })
      useChatStore.getState().resolveToolCall('tc_2', { exit_code: 0 }, 'success', 250)
    })
    const { toolCalls } = useChatStore.getState()
    expect(toolCalls['tc_2'].status).toBe('success')
    expect(toolCalls['tc_2'].duration_ms).toBe(250)
  })

  it('resolveToolCall updates status to error with error message', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Failed tool call shows error with retry
    act(() => {
      useChatStore.getState().startToolCall('tc_3', 'exec', { command: 'ls' })
      useChatStore.getState().resolveToolCall('tc_3', null, 'error', 30000, 'Timeout after 30s')
    })
    const { toolCalls } = useChatStore.getState()
    expect(toolCalls['tc_3'].status).toBe('error')
    expect(toolCalls['tc_3'].error).toBe('Timeout after 30s')
  })

  it('cancelToolCall sets tool status to cancelled', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Cancel during tool execution
    act(() => {
      useChatStore.getState().startToolCall('tc_4', 'web_search', {})
      useChatStore.getState().cancelToolCall('tc_4')
    })
    expect(useChatStore.getState().toolCalls['tc_4'].status).toBe('cancelled')
  })
})

describe('chat store — cancel/interrupt (test_cancel_preserves_partial)', () => {
  it('markLastMessageInterrupted sets status to interrupted and clears streaming', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Cancel during streaming preserves partial response (AC1)
    act(() => {
      useChatStore.getState().appendMessage({
        id: 'asst_cancel',
        session_id: 'sess_1',
        role: 'assistant',
        content: 'Here is the analysis of...',
        timestamp: '2026-03-29T10:00:01Z',
        status: 'streaming',
        isStreaming: true,
      })
      useChatStore.setState({ isStreaming: true })
      useChatStore.getState().markLastMessageInterrupted()
    })
    const state = useChatStore.getState()
    expect(state.isStreaming).toBe(false)
    const msg = state.messages.find((m) => m.id === 'asst_cancel')
    expect(msg?.status).toBe('interrupted')
    expect(msg?.content).toBe('Here is the analysis of...')
  })

  // T24b regression (cancel-cross-channel.spec.ts:569 — "cancel cascades to
  // awaited subagent"): the Stop button's render condition (ChatScreen.tsx:
  // `isStreaming || cancelState.stopLabel === 'stopping'`) and
  // useCancelState's minimum-display reset effect both key off the STORE's
  // bucket-level `isStreaming`, which must clear the instant a cancel is
  // issued — the SPA is documented to deliberately not wait for the
  // server's `done` frame (see markLastMessageInterrupted's doc comment).
  // The test above doesn't actually prove this: it forces the flat
  // `isStreaming` field true via a raw setState call that bypasses the
  // session bucket entirely, so the bucket's own isStreaming was false the
  // whole time and the assertion passed for the wrong reason. This test
  // drives isStreaming true through the REAL code path (sendMessage, which
  // is what a live streaming turn actually uses) so the bucket genuinely
  // starts at isStreaming:true with an assistant message already present —
  // the "already exists" branch inside markLastMessageInterrupted, which is
  // the branch every real Stop-button click hits (T21/T24a/T24b all already
  // have an assistant message by the time Stop is clickable). Before the
  // fix, that branch cleared only the message's own isStreaming and left
  // the bucket's isStreaming (and therefore the foreground field the Stop
  // button reads) stuck at true until a 'done' frame arrived — invisible to
  // the flawed test above, but exactly what made the awaited-delegate e2e
  // intermittently exceed its 5s window while waiting on the server to
  // unwind a running subagent turn.
  it('cancelStream clears bucket-level isStreaming synchronously even when an assistant message already exists (T24b: must not wait for the done frame)', () => {
    const mockSend = vi.fn().mockReturnValue(true)
    act(() => {
      useChatStore.setState({ isStreaming: false })
      useConnectionStore.setState({
        connection: { send: mockSend, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as any,
        isConnected: true,
      })
      useSessionStore.setState({ activeSessionId: TEST_SESSION_ID, activeAgentId: 'general-assistant' })
      // Starts a real turn: mints the assistant placeholder message
      // (isStreaming:true) and sets isStreaming:true on BOTH the session
      // bucket and the foreground field — the same state a live streaming
      // turn (or T24b's awaited-delegate turn) is in when Stop is clicked.
      useChatStore.getState().sendMessage('Hello, world!')
    })
    // Sanity: confirm the bucket itself (not just the foreground mirror) is
    // really streaming, and an assistant message already exists — otherwise
    // this test would silently hit the OTHER (already-correct) branch.
    const preCancel = useChatStore.getState()
    expect(preCancel.sessionsById[TEST_SESSION_ID]?.isStreaming).toBe(true)
    expect(preCancel.messages.some((m) => m.role === 'assistant')).toBe(true)

    act(() => {
      useChatStore.getState().cancelStream()
    })

    const state = useChatStore.getState()
    // No 'done' frame was ever delivered — if this is true, the clear was
    // synchronous and local, not dependent on server round-trip latency.
    expect(state.isStreaming).toBe(false)
    expect(state.sessionsById[TEST_SESSION_ID]?.isStreaming).toBe(false)
    const assistantMsg = state.messages.find((m) => m.role === 'assistant')
    expect(assistantMsg?.status).toBe('interrupted')
  })

  it('cancelStream calls connection.send with cancel frame', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Cancel during streaming (AC1 — WebSocket frame sent)
    const mockSend = vi.fn()
    act(() => {
      useChatStore.getState().appendMessage({
        id: 'asst_5',
        session_id: TEST_SESSION_ID,
        role: 'assistant',
        content: 'Partial...',
        timestamp: '2026-03-29T10:00:01Z',
        status: 'streaming',
        isStreaming: true,
      })
      useChatStore.setState({ isStreaming: true })
      useConnectionStore.setState({
        connection: { send: mockSend, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as any,
        isConnected: true,
      })
      // activeSessionId is already TEST_SESSION_ID from resetStore
      useChatStore.getState().cancelStream()
    })
    expect(mockSend).toHaveBeenCalledWith({ type: 'cancel', session_id: TEST_SESSION_ID })
    expect(useChatStore.getState().isStreaming).toBe(false)
  })

  it('cancelStream is a no-op when not streaming', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Cancel when idle is a no-op (AC3)
    const mockSend = vi.fn()
    act(() => {
      useChatStore.setState({ isStreaming: false })
      useConnectionStore.setState({
        connection: { send: mockSend, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as any,
        isConnected: true,
      })
      useChatStore.getState().cancelStream()
    })
    expect(mockSend).not.toHaveBeenCalled()
  })

  // ADR-040: cancelStream() gained an optional sessionId so the browser
  // panel's "Take over" can pause its own PINNED session even when a
  // different chat is currently foreground, instead of unconditionally
  // acting on activeSessionId.
  function bucketFor(msgs: ChatMessage[]): SessionChatState {
    return {
      ...makeBucketMessages(msgs),
      toolCalls: {},
      toolCallOrder: [],
      textAtToolCallStart: {},
      isStreaming: true,
      isReplaying: false,
      replayCompletedForSession: null,
      sessionTokens: 0,
      sessionCost: 0,
      rateLimitEvent: null,
      lastUserMessageAt: null,
      cancelStage: null,
      lastReceivedEventTime: null,
      spanByParentCallId: {},
    }
  }

  it('cancelStream(sessionId) cancels/marks the specified (background) session, not the active one', () => {
    const OTHER_SID = 'other-sid'
    const mockSend = vi.fn()

    const activeMsgs: ChatMessage[] = [
      { id: 'asst_active', session_id: TEST_SESSION_ID, role: 'assistant', content: 'active turn...', timestamp: '2026-03-29T10:00:01Z', status: 'streaming', isStreaming: true },
    ]
    const otherMsgs: ChatMessage[] = [
      { id: 'asst_other', session_id: OTHER_SID, role: 'assistant', content: 'other turn...', timestamp: '2026-03-29T10:00:01Z', status: 'streaming', isStreaming: true },
    ]

    act(() => {
      useConnectionStore.setState({
        connection: { send: mockSend, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as any,
        isConnected: true,
      })
      // Seed BOTH buckets directly (appendMessage always targets the active
      // session, so the background OTHER_SID bucket can only be built via
      // direct state manipulation — mirrors the H1-FE seeding pattern above).
      useChatStore.setState({
        sessionsById: {
          [TEST_SESSION_ID]: bucketFor(activeMsgs),
          [OTHER_SID]: bucketFor(otherMsgs),
        },
        // Sync foreground fields to the ACTIVE session, as the real store
        // does after any bucket mutation.
        isStreaming: true,
        messages: activeMsgs,
      })
    })

    act(() => {
      useChatStore.getState().cancelStream(OTHER_SID)
    })

    // The cancel frame targets OTHER_SID only — never the active session.
    expect(mockSend).toHaveBeenCalledTimes(1)
    expect(mockSend).toHaveBeenCalledWith({ type: 'cancel', session_id: OTHER_SID })

    // OTHER_SID's message is marked interrupted.
    const otherBucket = useChatStore.getState().sessionsById[OTHER_SID]
    const otherMsg = getMessages(otherBucket).find((m) => m.id === 'asst_other')
    expect(otherMsg?.status).toBe('interrupted')
    expect(otherMsg?.isStreaming).toBe(false)

    // The ACTIVE session's message — and the foreground isStreaming
    // projection derived from it — are completely untouched.
    const activeBucket = useChatStore.getState().sessionsById[TEST_SESSION_ID]
    const activeMsg = getMessages(activeBucket).find((m) => m.id === 'asst_active')
    expect(activeMsg?.status).toBe('streaming')
    expect(activeMsg?.isStreaming).toBe(true)
    expect(useChatStore.getState().isStreaming).toBe(true)
  })

  it('cancelStream() with no argument still targets the active session (default behaviour unchanged)', () => {
    const mockSend = vi.fn()
    const activeMsgs: ChatMessage[] = [
      { id: 'asst_default', session_id: TEST_SESSION_ID, role: 'assistant', content: 'active turn...', timestamp: '2026-03-29T10:00:01Z', status: 'streaming', isStreaming: true },
    ]
    act(() => {
      useChatStore.setState({
        sessionsById: { [TEST_SESSION_ID]: bucketFor(activeMsgs) },
        isStreaming: true,
        messages: activeMsgs,
      })
      useConnectionStore.setState({
        connection: { send: mockSend, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as any,
        isConnected: true,
      })
      useChatStore.getState().cancelStream()
    })
    expect(mockSend).toHaveBeenCalledWith({ type: 'cancel', session_id: TEST_SESSION_ID })
    const msg = useChatStore.getState().messages.find((m) => m.id === 'asst_default')
    expect(msg?.status).toBe('interrupted')
  })

  // ADR-040 UAT (Bug 1, F-S3): "Take over" on the browser panel calls
  // cancelStream(bgSid) while a DIFFERENT session is foreground. Both UAT
  // testers saw the server's cancellation acknowledgment leak a spurious
  // "Error processing message: turn canceled" bubble/status into the ACTIVE
  // (foreground) session instead of the cancelled background one.
  function setUpActiveAndBackground(mockSend: ReturnType<typeof vi.fn>) {
    const OTHER_SID = 'bg-ray-sid'
    const activeMsgs: ChatMessage[] = [
      { id: 'asst_active', session_id: TEST_SESSION_ID, role: 'assistant', content: 'mia is mid-turn...', timestamp: '2026-03-29T10:00:01Z', status: 'streaming', isStreaming: true },
    ]
    const otherMsgs: ChatMessage[] = [
      { id: 'asst_other', session_id: OTHER_SID, role: 'assistant', content: 'ray is browsing...', timestamp: '2026-03-29T10:00:01Z', status: 'streaming', isStreaming: true },
    ]
    useConnectionStore.setState({
      connection: { send: mockSend, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as any,
      isConnected: true,
    })
    useChatStore.setState({
      sessionsById: {
        [TEST_SESSION_ID]: bucketFor(activeMsgs),
        [OTHER_SID]: bucketFor(otherMsgs),
      },
      isStreaming: true,
      messages: activeMsgs,
    })
    return { OTHER_SID, activeMsgs, otherMsgs }
  }

  it('post-cancel token+done frames TAGGED with the background session id land only in that bucket (no regression)', () => {
    const mockSend = vi.fn(() => true)
    const { OTHER_SID } = setUpActiveAndBackground(mockSend)

    act(() => {
      useChatStore.getState().cancelStream(OTHER_SID)
    })
    act(() => {
      useChatStore.getState().handleFrame({ type: 'token', session_id: OTHER_SID, content: 'Error processing message: turn canceled' })
      useChatStore.getState().handleFrame({ type: 'done', session_id: OTHER_SID, stats: { tokens: 1, cost: 0, duration_ms: 5 } })
    })

    const state = useChatStore.getState()
    const otherMsgs = getMessages(state.sessionsById[OTHER_SID])
    expect(otherMsgs).toHaveLength(1)
    expect(otherMsgs[0].status).toBe('interrupted')
    expect(otherMsgs[0].content).toBe('ray is browsing...') // trailing token swallowed, not appended

    // The active (foreground) session must show no trace of the background cancellation.
    const activeMsgs = getMessages(state.sessionsById[TEST_SESSION_ID])
    expect(activeMsgs).toHaveLength(1)
    expect(activeMsgs[0].status).toBe('streaming')
    expect(activeMsgs[0].isStreaming).toBe(true)
    expect(activeMsgs[0].content).toBe('mia is mid-turn...')
    expect(state.isStreaming).toBe(true)
    expect(useConnectionStore.getState().connectionError).toBeNull()
  })

  it('a post-cancel error frame MISSING session_id is attributed to the cancelled background session, not the active one', () => {
    const mockSend = vi.fn(() => true)
    const { OTHER_SID } = setUpActiveAndBackground(mockSend)

    act(() => {
      useChatStore.getState().cancelStream(OTHER_SID)
    })
    // Simulate the server's cancellation ack arriving with NO session_id at all —
    // the untagged case (a plausible backend gap for a generic error-wrap path).
    act(() => {
      useChatStore.getState().handleFrame({ type: 'error', message: 'Error processing message: turn canceled' })
    })

    const state = useChatStore.getState()

    // The background (cancelled) session absorbs the ack.
    const otherMsgs = getMessages(state.sessionsById[OTHER_SID])
    expect(otherMsgs[0].status).toBe('interrupted')

    // CRITICAL: the active session's own in-flight message and isStreaming
    // projection must be completely untouched — this is the exact leak UAT saw.
    const activeMsgs = getMessages(state.sessionsById[TEST_SESSION_ID])
    expect(activeMsgs).toHaveLength(1)
    expect(activeMsgs[0].status).toBe('streaming')
    expect(activeMsgs[0].isStreaming).toBe(true)
    expect(activeMsgs[0].content).toBe('mia is mid-turn...')
    expect(state.isStreaming).toBe(true)
    expect(useConnectionStore.getState().connectionError).toBeNull()
  })

  it('a post-cancel token+done pair MISSING session_id both land on the cancelled background session', () => {
    const mockSend = vi.fn(() => true)
    const { OTHER_SID } = setUpActiveAndBackground(mockSend)

    act(() => {
      useChatStore.getState().cancelStream(OTHER_SID)
    })
    act(() => {
      // Untagged token+done, mirroring the UAT report's literal claim.
      useChatStore.getState().handleFrame({ type: 'token', content: 'Error processing message: turn canceled' } as any)
      useChatStore.getState().handleFrame({ type: 'done', stats: { tokens: 1, cost: 0, duration_ms: 5 } } as any)
    })

    const state = useChatStore.getState()
    const otherBucket = state.sessionsById[OTHER_SID]
    expect(otherBucket.isStreaming).toBe(false)
    const otherMsgs = getMessages(otherBucket)
    expect(otherMsgs[0].status).toBe('interrupted')
    expect(otherMsgs[0].content).toBe('ray is browsing...')

    const activeMsgs = getMessages(state.sessionsById[TEST_SESSION_ID])
    expect(activeMsgs[0].status).toBe('streaming')
    expect(activeMsgs[0].isStreaming).toBe(true)
    expect(state.isStreaming).toBe(true)
  })

  it('an untagged cancellation-ack frame falls back to the active session when it IS the only pending cancel (ordinary Stop-button flow, unchanged)', () => {
    const mockSend = vi.fn(() => true)
    const activeMsgs: ChatMessage[] = [
      { id: 'asst_active', session_id: TEST_SESSION_ID, role: 'assistant', content: 'active turn...', timestamp: '2026-03-29T10:00:01Z', status: 'streaming', isStreaming: true },
    ]
    act(() => {
      useConnectionStore.setState({
        connection: { send: mockSend, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as any,
        isConnected: true,
      })
      useChatStore.setState({
        sessionsById: { [TEST_SESSION_ID]: bucketFor(activeMsgs) },
        isStreaming: true,
        messages: activeMsgs,
      })
      // Stop button — no explicit sessionId, defaults to the active session.
      useChatStore.getState().cancelStream()
    })
    act(() => {
      useChatStore.getState().handleFrame({ type: 'error', message: 'Error processing message: turn canceled' })
    })
    const msg = useChatStore.getState().messages.find((m) => m.id === 'asst_active')
    expect(msg?.status).toBe('interrupted')
  })

  it('an untagged cancellation-ack frame with TWO ambiguous pending cancels falls back to the active session rather than guessing wrong', () => {
    const mockSend = vi.fn(() => true)
    const { OTHER_SID } = setUpActiveAndBackground(mockSend)
    const SECOND_BG_SID = 'bg-ava-sid'
    const secondMsgs: ChatMessage[] = [
      { id: 'asst_second', session_id: SECOND_BG_SID, role: 'assistant', content: 'ava is drafting...', timestamp: '2026-03-29T10:00:01Z', status: 'streaming', isStreaming: true },
    ]
    act(() => {
      useChatStore.setState({
        sessionsById: {
          ...useChatStore.getState().sessionsById,
          [SECOND_BG_SID]: bucketFor(secondMsgs),
        },
      })
    })

    act(() => {
      useChatStore.getState().cancelStream(OTHER_SID)
      useChatStore.getState().cancelStream(SECOND_BG_SID)
    })
    act(() => {
      // Ambiguous: two sessions are awaiting a cancel ack. Must not guess —
      // falls back to the pre-existing activeSid behaviour.
      useChatStore.getState().handleFrame({ type: 'error', message: 'Error processing message: turn canceled' })
    })

    const state = useChatStore.getState()
    // Both backgrounds are independently marked interrupted by their own explicit cancelStream() calls.
    expect(getMessages(state.sessionsById[OTHER_SID])[0].status).toBe('interrupted')
    expect(getMessages(state.sessionsById[SECOND_BG_SID])[0].status).toBe('interrupted')
    // The untagged frame itself, being ambiguous, falls back to the active session (unchanged
    // pre-existing behaviour) rather than silently guessing one of the two backgrounds.
    const activeMsgs = getMessages(state.sessionsById[TEST_SESSION_ID])
    expect(activeMsgs[0].status).toBe('interrupted')
  })
})

describe('chat store — sendMessage optimistic render', () => {
  it('sendMessage appends user message immediately and sets isStreaming', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: User message appears optimistically
    // mockSend must return true — sendMessage reverts the optimistic update if send() returns falsy
    const mockSend = vi.fn().mockReturnValue(true)
    act(() => {
      useChatStore.setState({ isStreaming: false })
      useConnectionStore.setState({
        connection: { send: mockSend, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as any,
        isConnected: true,
      })
      useSessionStore.setState({
        activeSessionId: TEST_SESSION_ID,
        activeAgentId: 'general-assistant',
      })
      useChatStore.getState().sendMessage('Hello, world!')
    })
    const state = useChatStore.getState()
    // User message appended immediately
    const userMsg = state.messages.find((m) => m.role === 'user')
    expect(userMsg?.content).toBe('Hello, world!')
    // isStreaming set to true
    expect(state.isStreaming).toBe(true)
    // WS frame sent
    expect(mockSend).toHaveBeenCalledWith(
      expect.objectContaining({ type: 'message', content: 'Hello, world!' })
    )
  })

  // #254 regression: media:// refs must be threaded into the outbound frame so
  // the agent sees the attachment as a multimodal content block.
  it('sendMessage threads mediaRefs into the WS message frame', () => {
    const mockSend = vi.fn().mockReturnValue(true)
    act(() => {
      useChatStore.setState({ isStreaming: false })
      useConnectionStore.setState({
        connection: { send: mockSend, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as any,
        isConnected: true,
      })
      useSessionStore.setState({ activeSessionId: TEST_SESSION_ID, activeAgentId: 'general-assistant' })
      useChatStore.getState().sendMessage('here is a pic', {
        mediaRefs: ['media://pic1'],
        attachments: [{ type: 'image', url: '/api/v1/uploads/s/pic.png', filename: 'pic.png', contentType: 'image/png' }],
      })
    })
    expect(mockSend).toHaveBeenCalledWith(
      expect.objectContaining({ type: 'message', content: 'here is a pic', media: ['media://pic1'] })
    )
    // Optimistic user bubble carries the attachment for inline display.
    const userMsg = useChatStore.getState().messages.find((m) => m.role === 'user')
    expect(userMsg?.media?.[0]?.url).toBe('/api/v1/uploads/s/pic.png')
  })

  // #253 (P0 DATA LOSS) regression: when the WS send fails, the user's message
  // must NOT be silently dropped. The user bubble is kept (flagged 'error') and
  // only the empty assistant placeholder is removed.
  it('sendMessage keeps the user message when the WS send fails', () => {
    const mockSend = vi.fn().mockReturnValue(false) // simulate send failure
    act(() => {
      useChatStore.setState({ isStreaming: false })
      useConnectionStore.setState({
        connection: { send: mockSend, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as any,
        isConnected: true,
      })
      useSessionStore.setState({ activeSessionId: TEST_SESSION_ID, activeAgentId: 'general-assistant' })
      useChatStore.getState().sendMessage('do not lose me')
    })
    const state = useChatStore.getState()
    const userMsg = state.messages.find((m) => m.role === 'user')
    // The user turn survives the failed send.
    expect(userMsg).toBeDefined()
    expect(userMsg?.content).toBe('do not lose me')
    expect(userMsg?.status).toBe('error')
    // The empty streaming assistant placeholder is removed.
    expect(state.messages.some((m) => m.role === 'assistant')).toBe(false)
    // Streaming flag cleared so the composer is usable again.
    expect(state.isStreaming).toBe(false)
    // Error surfaced to the user.
    expect(useConnectionStore.getState().connectionError).toContain('kept')
  })
})

// ── Wave 3 Fix 3 (HIGH, pr-test-analyzer): sendMessage rebake-path coverage ──
// Traces to: this review pass's Fix 3. sendMessage's "rebake" step (chat.ts,
// inside the activeSessionId branch, just above the outbound WS payload build)
// looks up the previous assistant message via findLastAssistantMessageId and,
// if that message still has live/unbaked tool calls sitting in the bucket's
// flat toolCalls/toolCallOrder maps, merges them into that message's
// tool_calls array BEFORE the new user+assistant messages are appended. No
// test — before or after the Wave 3 refactor that extracted
// findLastAssistantMessageId — exercised this integration path; the existing
// 'findLastAssistantMessageId — direct unit coverage' block below only tests
// the extracted helper in isolation. This is the same tool-call-vanishing
// failure mode already fixed once at the `done`-frame and
// `clearStreamingState` baking sites (see those functions' comments above).
describe('chat store — sendMessage rebakes pending tool calls into prior assistant message (Wave 3 Fix 3)', () => {
  it('bakes a live, resolved tool call into the prior assistant message before appending the new turn', () => {
    const mockSend = vi.fn().mockReturnValue(true)
    act(() => {
      // Seed a prior assistant message that finished streaming but whose tool
      // call was never baked into tool_calls — e.g. the `done` frame's own
      // bake step raced a slow tool_call_result, or a WS hiccup left it live.
      // This is exactly the scenario the rebake step exists to recover.
      useChatStore.getState().appendMessage({
        id: 'asst_prior',
        session_id: TEST_SESSION_ID,
        role: 'assistant',
        content: 'Here is the first answer.',
        timestamp: '2026-03-29T10:00:00Z',
        status: 'done',
        isStreaming: false,
      })
      // A tool call from that turn is still "live" in the bucket's flat
      // toolCalls/toolCallOrder maps — resolved (terminal), but never baked
      // into asst_prior.tool_calls.
      useChatStore.getState().startToolCall('tc_live_1', 'web_search', { query: 'weather in NYC' })
      useChatStore.getState().resolveToolCall('tc_live_1', { temp_f: 61 }, 'success', 420)

      useChatStore.setState({ isStreaming: false })
      useConnectionStore.setState({
        connection: { send: mockSend, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as any,
        isConnected: true,
      })
      useSessionStore.setState({ activeSessionId: TEST_SESSION_ID, activeAgentId: 'general-assistant' })

      useChatStore.getState().sendMessage('Follow-up question')
    })

    const state = useChatStore.getState()

    // The prior assistant message now carries the baked tool call — not dropped.
    const priorMsg = state.messages.find((m) => m.id === 'asst_prior')
    expect(priorMsg?.role).toBe('assistant')
    expect(priorMsg?.tool_calls).toHaveLength(1)
    expect(priorMsg?.tool_calls?.[0]).toMatchObject({
      id: 'tc_live_1',
      tool: 'web_search',
      params: { query: 'weather in NYC' },
      result: { temp_f: 61 },
      status: 'success',
      duration_ms: 420,
    })

    // The live tool-call bucket is cleared of the now-baked call.
    expect(state.toolCalls['tc_live_1']).toBeUndefined()
    expect(state.toolCallOrder).not.toContain('tc_live_1')

    // The new turn was appended AFTER the (now-baked) prior assistant message,
    // not before it and not in place of it.
    expect(state.messages.map((m) => m.id)).toEqual([
      'asst_prior',
      expect.any(String), // new user message
      expect.any(String), // new streaming assistant placeholder
    ])
    const newUserMsg = state.messages[1]
    expect(newUserMsg.role).toBe('user')
    expect(newUserMsg.content).toBe('Follow-up question')
    const newAssistantMsg = state.messages[2]
    expect(newAssistantMsg.role).toBe('assistant')
    expect(newAssistantMsg.isStreaming).toBe(true)

    // WS frame sent for the new turn.
    expect(mockSend).toHaveBeenCalledWith(
      expect.objectContaining({ type: 'message', content: 'Follow-up question' })
    )
  })

  it('does not duplicate a tool call that was already baked into the prior assistant message', () => {
    const mockSend = vi.fn().mockReturnValue(true)
    act(() => {
      // Prior assistant message already has the tool call baked (the normal
      // path — a `done` frame already ran its own bake step for this call id).
      useChatStore.getState().appendMessage({
        id: 'asst_prior2',
        session_id: TEST_SESSION_ID,
        role: 'assistant',
        content: 'Already baked.',
        timestamp: '2026-03-29T10:00:00Z',
        status: 'done',
        isStreaming: false,
        tool_calls: [
          { id: 'tc_baked_1', tool: 'web_search', params: { query: 'x' }, result: { ok: true }, status: 'success' },
        ],
      })
      useChatStore.setState({ isStreaming: false })
      useConnectionStore.setState({
        connection: { send: mockSend, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as any,
        isConnected: true,
      })
      useSessionStore.setState({ activeSessionId: TEST_SESSION_ID, activeAgentId: 'general-assistant' })

      useChatStore.getState().sendMessage('Another follow-up')
    })

    const state = useChatStore.getState()
    const priorMsg = state.messages.find((m) => m.id === 'asst_prior2')
    // Still exactly one tool call — no duplicate entry from a spurious re-bake.
    expect(priorMsg?.tool_calls).toHaveLength(1)
    expect(priorMsg?.tool_calls?.[0].id).toBe('tc_baked_1')
  })
})

// ── M4 (BLOCKER 1): workspace→turn binding ───────────────────────────────────
// Traces to: wave5a-wire-ui-spec.md M4 — a chat sent inside a workspace must
// stamp metadata.workspace_id so created/delegated tasks land on THIS workspace.
describe('chat store — M4 workspace→turn binding (metadata.workspace_id)', () => {
  it('attaches metadata.workspace_id when chatting inside a workspace', () => {
    const mockSend = vi.fn().mockReturnValue(true)
    act(() => {
      useChatStore.setState({ isStreaming: false })
      useConnectionStore.setState({
        connection: { send: mockSend, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as any,
        isConnected: true,
      })
      useSessionStore.setState({ activeSessionId: TEST_SESSION_ID, activeAgentId: 'general-assistant' })
      useWorkspacesStore.setState({ activeWorkspaceId: '01JXWORKSPACE0000000000001' })
      useChatStore.getState().sendMessage('do this in the workspace')
    })
    expect(mockSend).toHaveBeenCalledWith(
      expect.objectContaining({
        type: 'message',
        content: 'do this in the workspace',
        metadata: expect.objectContaining({ workspace_id: '01JXWORKSPACE0000000000001' }),
      }),
    )
  })

  it('omits metadata entirely on the global (non-workspace) chat', () => {
    const mockSend = vi.fn().mockReturnValue(true)
    act(() => {
      useChatStore.setState({ isStreaming: false })
      useConnectionStore.setState({
        connection: { send: mockSend, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as any,
        isConnected: true,
      })
      useSessionStore.setState({ activeSessionId: TEST_SESSION_ID, activeAgentId: 'general-assistant' })
      useWorkspacesStore.setState({ activeWorkspaceId: null })
      useChatStore.getState().sendMessage('global chat, no workspace')
    })
    const payload = mockSend.mock.calls[0]?.[0] as Record<string, unknown>
    expect(payload).toMatchObject({ type: 'message', content: 'global chat, no workspace' })
    // No workspace and no per-turn model → metadata is omitted, matching the
    // backend's default-workspace fallback.
    expect(payload).not.toHaveProperty('metadata')
  })

  it('merges workspace_id with a per-turn model_name override', () => {
    const mockSend = vi.fn().mockReturnValue(true)
    act(() => {
      useChatStore.setState({ isStreaming: false })
      useConnectionStore.setState({
        connection: { send: mockSend, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as any,
        isConnected: true,
      })
      useSessionStore.setState({ activeSessionId: TEST_SESSION_ID, activeAgentId: 'general-assistant' })
      useWorkspacesStore.setState({ activeWorkspaceId: '01JXWORKSPACE0000000000002' })
      useChatStore.getState().sendMessage('use a specific model here', { model_name: 'z-ai/glm-5-turbo' })
    })
    expect(mockSend).toHaveBeenCalledWith(
      expect.objectContaining({
        type: 'message',
        metadata: expect.objectContaining({
          workspace_id: '01JXWORKSPACE0000000000002',
          model_name: 'z-ai/glm-5-turbo',
        }),
      }),
    )
  })
})

// ── #253: recovery error-bubble tests ────────────────────────────────────────
// Traces to: sprint-258 review finding #253 — failed send must preserve typed
// content as a retriable error bubble, not silently drop the message.

describe('chat store — #253 no-session send failure creates retriable error bubble', () => {
  it('when activeSessionId is null and send() fails, user message gets status:error (not dropped)', () => {
    // BDD:
    //   Given no active session (activeSessionId = null)
    //   And the WS send() returns false (connection dropped mid-send)
    //   When sendMessage('Hello') is called
    //   Then a user message with content 'Hello' and status 'error' is present in the store
    //   And isStreaming is false (no phantom spinner)
    //   And a Retry affordance is reachable (the message is in the store with status:'error')
    const mockSend = vi.fn().mockReturnValue(false) // send fails
    act(() => {
      useChatStore.setState({ isStreaming: false })
      useConnectionStore.setState({
        connection: { send: mockSend, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as any,
        isConnected: true,
        connectionError: null,
      })
      // No active session — null triggers the no-session path
      useSessionStore.setState({
        activeSessionId: null,
        activeAgentId: 'general-assistant',
      })
      useChatStore.getState().sendMessage('Hello no-session')
    })

    // After the failed send, the user message must be preserved with status:'error'
    const state = useChatStore.getState()

    // The message must be in SOME bucket (either the pending one or the active one)
    // Check the global messages selector which reflects the active session.
    // The pending bucket is activated via setActiveSession('__pending', ...)
    const userMsg = state.messages.find((m) => m.role === 'user' && m.content === 'Hello no-session')
    expect(userMsg).toBeDefined()
    expect(userMsg?.status).toBe('error')

    // isStreaming must be false — no phantom spinner
    expect(state.isStreaming).toBe(false)

    // The Retry affordance: VirtualUserMessageRow renders UserMessageRetryButton
    // when message.status === 'error'. Assert the store state that drives it:
    // the user message with status:'error' IS reachable in the messages array.
    const errorMessages = state.messages.filter((m) => m.role === 'user' && m.status === 'error')
    expect(errorMessages.length).toBeGreaterThan(0)
  })

  it('when activeSessionId is null and send() succeeds, user message is rendered optimistically', () => {
    // BDD:
    //   Given no active session
    //   And the WS send() returns true (succeeds)
    //   When sendMessage('Hello') is called
    //   Then a user message with content 'Hello' is visible (not status:'error')
    //   And isStreaming is true (awaiting session_started ack)
    const mockSend = vi.fn().mockReturnValue(true)
    act(() => {
      useChatStore.setState({ isStreaming: false })
      useConnectionStore.setState({
        connection: { send: mockSend, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as any,
        isConnected: true,
      })
      useSessionStore.setState({
        activeSessionId: null,
        activeAgentId: 'general-assistant',
      })
      useChatStore.getState().sendMessage('Hello pending')
    })

    const state = useChatStore.getState()
    const userMsg = state.messages.find((m) => m.role === 'user' && m.content === 'Hello pending')
    expect(userMsg).toBeDefined()
    // status should be 'done' (successfully queued, awaiting session_started)
    expect(userMsg?.status).toBe('done')
    // isStreaming should be true — the agent is about to respond
    expect(state.isStreaming).toBe(true)
  })
})

// ── Sprint H: subagent span tests ─────────────────────────────────────────────
// TDD row 11: ChatStore_GroupsFramesBySpan
// Traces to: sprint-h-subagent-block-spec.md Scenarios 2, 4, 5, 8

describe('ChatStore_GroupsFramesBySpan', () => {
  /** Seed an assistant placeholder so spans have a message to attach to. */
  function seedAssistant() {
    act(() => {
      useChatStore.getState().updateLastAssistantMessage('', false)
    })
  }

  it('in-order: subagent_start → tool_call_start → tool_call_result → subagent_end populates span', () => {
    seedAssistant()

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'subagent_start',
        span_id: 'span_c1',
        parent_call_id: 'c1',
        task_label: 'audit go files',
        agent_id: 'max',
        session_id: TEST_SESSION_ID,
      })
    })

    let msgs = useChatStore.getState().messages
    let span = msgs[msgs.length - 1].spans?.[0]
    expect(span).toBeDefined()
    expect(span?.spanId).toBe('span_c1')
    expect(span?.taskLabel).toBe('audit go files')
    expect(span?.status).toBe('running')
    expect(span?.steps).toHaveLength(0)
    // subagent_start's own agent_id must be carried onto the running span.
    expect(span?.agentId).toBe('max')

    // tool_call_start with matching parent_call_id
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_start',
        call_id: 't1',
        tool: 'fs.list',
        params: { path: '/tmp' },
        parent_call_id: 'c1',
        session_id: TEST_SESSION_ID,
      })
    })

    msgs = useChatStore.getState().messages
    span = msgs[msgs.length - 1].spans?.[0]
    expect(span?.steps).toHaveLength(1)
    const s0 = span?.steps[0]
    expect(s0?.kind === 'tool' ? s0.tool.tool : undefined).toBe('fs.list')
    expect(s0?.kind === 'tool' ? s0.tool.status : undefined).toBe('running')

    // tool_call_result
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_result',
        call_id: 't1',
        tool: 'fs.list',
        result: 'file.go',
        status: 'success',
        duration_ms: 100,
        parent_call_id: 'c1',
        session_id: TEST_SESSION_ID,
      })
    })

    msgs = useChatStore.getState().messages
    span = msgs[msgs.length - 1].spans?.[0]
    const s0after = span?.steps[0]
    expect(s0after?.kind === 'tool' ? s0after.tool.status : undefined).toBe('success')
    expect(s0after?.kind === 'tool' ? s0after.tool.result : undefined).toBe('file.go')

    // subagent_end
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'subagent_end',
        span_id: 'span_c1',
        status: 'success',
        duration_ms: 4210,
        final_result: 'Found 1 Go file',
        session_id: TEST_SESSION_ID,
      })
    })

    msgs = useChatStore.getState().messages
    span = msgs[msgs.length - 1].spans?.[0]
    expect(span?.status).toBe('success')
    // Narrow to terminal span to access durationMs and finalResult.
    const terminalSpan = span?.status !== 'running' ? span : undefined
    expect((terminalSpan as import('@/store/chat').SubagentSpanTerminal | undefined)?.durationMs).toBe(4210)
    expect((terminalSpan as import('@/store/chat').SubagentSpanTerminal | undefined)?.finalResult).toBe('Found 1 Go file')
    // agentId must survive the running → terminal transition (the subagent_end
    // frame here carries no agent_id of its own, so this also exercises the
    // `ef.agent_id ?? existingSpan.agentId` fallback taking the existing-span branch).
    expect(span?.agentId).toBe('max')
  })

  it('out-of-order: tool_call_start arrives before subagent_start — buffered then drained', () => {
    seedAssistant()

    // tool_call_start arrives BEFORE subagent_start
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_start',
        call_id: 't2',
        tool: 'shell',
        params: { cmd: 'ls' },
        parent_call_id: 'c2',
        session_id: TEST_SESSION_ID,
      })
    })

    // No span yet — should not appear in flat toolCalls either yet
    let msgs = useChatStore.getState().messages
    expect(msgs[msgs.length - 1].spans ?? []).toHaveLength(0)

    // Now subagent_start arrives — should drain the buffer
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'subagent_start',
        span_id: 'span_c2',
        parent_call_id: 'c2',
        task_label: 'list files',
        session_id: TEST_SESSION_ID,
      })
    })

    msgs = useChatStore.getState().messages
    const span = msgs[msgs.length - 1].spans?.[0]
    expect(span).toBeDefined()
    expect(span?.spanId).toBe('span_c2')
    expect(span?.steps).toHaveLength(1)
    const step0 = span?.steps[0]
    expect(step0?.kind).toBe('tool')
    expect(step0?.kind === 'tool' ? step0.tool.tool : undefined).toBe('shell')
  })

  it('step count increments +1 per tool_call_start, not per result (FR-H-010)', () => {
    seedAssistant()

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'subagent_start',
        span_id: 'span_c3',
        parent_call_id: 'c3',
        task_label: 'multi-step task',
        session_id: TEST_SESSION_ID,
      })
    })

    for (let i = 1; i <= 3; i++) {
      act(() => {
        useChatStore.getState().handleFrame({
          type: 'tool_call_start',
          call_id: `t_${i}`,
          tool: 'fs.list',
          params: {},
          parent_call_id: 'c3',
          session_id: TEST_SESSION_ID,
        })
      })
      const msgs = useChatStore.getState().messages
      const span = msgs[msgs.length - 1].spans?.[0]
      expect(span?.steps).toHaveLength(i)
    }
  })

  it('two sibling spans accumulate steps independently', () => {
    seedAssistant()

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'subagent_start',
        span_id: 'span_s1',
        parent_call_id: 's1',
        task_label: 'first',
        session_id: TEST_SESSION_ID,
      })
      useChatStore.getState().handleFrame({
        type: 'subagent_start',
        span_id: 'span_s2',
        parent_call_id: 's2',
        task_label: 'second',
        session_id: TEST_SESSION_ID,
      })
    })

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_start',
        call_id: 'ts1',
        tool: 'exec',
        params: {},
        parent_call_id: 's1',
        session_id: TEST_SESSION_ID,
      })
      useChatStore.getState().handleFrame({
        type: 'tool_call_start',
        call_id: 'ts2a',
        tool: 'web_search',
        params: {},
        parent_call_id: 's2',
        session_id: TEST_SESSION_ID,
      })
      useChatStore.getState().handleFrame({
        type: 'tool_call_start',
        call_id: 'ts2b',
        tool: 'file.read',
        params: {},
        parent_call_id: 's2',
        session_id: TEST_SESSION_ID,
      })
    })

    const msgs = useChatStore.getState().messages
    const spans = msgs[msgs.length - 1].spans ?? []
    expect(spans).toHaveLength(2)
    expect(spans[0].steps).toHaveLength(1)
    expect(spans[1].steps).toHaveLength(2)
  })

  // Bug fix regression (root-caused at chat.ts's tool_call_result span-merge
  // sites): the result-step object used to hardcode `params: {}`, and
  // `{ ...existingStep.tool, ...step }` let that empty object clobber the
  // REAL params recorded at tool_call_start — so a step's params silently
  // reverted to {} the moment its result arrived. Downstream, ToolCallBadge's
  // shouldRenderToolCall(tool, params, ...) misclassified e.g. a
  // `bash {action:'poll'}` step as visible (params={} doesn't match the
  // poll/read hide rule), leaking noisy background infra into
  // SubagentBlock/ActivityPanel. Pins that the step's params survive the
  // result merge unchanged.
  it('a span step keeps its tool_call_start params after tool_call_result arrives (params must not be clobbered)', () => {
    seedAssistant()

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'subagent_start',
        span_id: 'span_params',
        parent_call_id: 'c_params',
        task_label: 'poll a background session',
        session_id: TEST_SESSION_ID,
      })
      useChatStore.getState().handleFrame({
        type: 'tool_call_start',
        call_id: 't_params',
        tool: 'bash',
        params: { action: 'poll' },
        parent_call_id: 'c_params',
        session_id: TEST_SESSION_ID,
      })
    })

    let msgs = useChatStore.getState().messages
    let span = msgs[msgs.length - 1].spans?.[0]
    const stepBefore = span?.steps[0]
    expect(stepBefore?.kind === 'tool' ? stepBefore.tool.params : undefined).toEqual({ action: 'poll' })

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_result',
        call_id: 't_params',
        tool: 'bash',
        result: 'still running',
        status: 'success',
        duration_ms: 50,
        parent_call_id: 'c_params',
        session_id: TEST_SESSION_ID,
      })
    })

    msgs = useChatStore.getState().messages
    span = msgs[msgs.length - 1].spans?.[0]
    const stepAfter = span?.steps[0]
    // The bug: this used to become {} after the result merge.
    expect(stepAfter?.kind === 'tool' ? stepAfter.tool.params : undefined).toEqual({ action: 'poll' })
    expect(stepAfter?.kind === 'tool' ? stepAfter.tool.status : undefined).toBe('success')
    expect(stepAfter?.kind === 'tool' ? stepAfter.tool.result : undefined).toBe('still running')
  })

  // (item 8d, 2026-07-16 fix wave): a `tool_call_result` can arrive for a
  // call_id this span's index has NO existing step for — a genuine race
  // where the result beat its own `tool_call_start` (as opposed to
  // ChatStore_OrphanFrame_FallsBackFlat below, which covers the SPAN itself
  // never having started at all). The span IS already open here
  // (subagent_start already ran), so this hits the "no existingIdx" push
  // branch (chat.ts ~2802-2807) rather than the orphan-buffer path. Pins:
  // no crash, and the pushed step defaults to params:{} (there is no
  // start-time params to inherit).
  it('a tool_call_result with no prior tool_call_start, on an already-open span, pushes a step with params:{} and does not crash', () => {
    seedAssistant()

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'subagent_start',
        span_id: 'span_orphan_step',
        parent_call_id: 'c_orphan_step',
        task_label: 'race condition repro',
        session_id: TEST_SESSION_ID,
      })
    })

    expect(() => {
      act(() => {
        useChatStore.getState().handleFrame({
          type: 'tool_call_result',
          call_id: 't_orphan_step',
          tool: 'fs.list',
          result: '["a.txt"]',
          status: 'success',
          duration_ms: 12,
          parent_call_id: 'c_orphan_step',
          session_id: TEST_SESSION_ID,
        })
      })
    }).not.toThrow()

    const msgs = useChatStore.getState().messages
    const span = msgs[msgs.length - 1].spans?.[0]
    expect(span?.steps).toHaveLength(1)
    const step = span?.steps[0]
    expect(step?.kind).toBe('tool')
    if (step?.kind === 'tool') {
      expect(step.tool.call_id).toBe('t_orphan_step')
      expect(step.tool.tool).toBe('fs.list')
      expect(step.tool.params).toEqual({})
      expect(step.tool.status).toBe('success')
      expect(step.tool.result).toBe('["a.txt"]')
    }
  })
})

// TDD row 12: ChatStore_OrphanFrame_FallsBackFlat
// Traces to: sprint-h-subagent-block-spec.md Edge (out-of-order), FR-H-009

describe('ChatStore_OrphanFrame_FallsBackFlat', () => {
  it('frame with unknown parent_call_id + no subagent_start within 10s → flat + dev warning', async () => {
    // Use fake timers to simulate the 10s TTL without waiting
    vi.useFakeTimers()
    const warnSpy = vi.spyOn(console, 'warn')

    act(() => {
      useChatStore.getState().updateLastAssistantMessage('', false)
    })

    // tool_call_start with a parent_call_id that has no matching subagent_start
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_start',
        call_id: 'orphan_t1',
        tool: 'fs.list',
        params: {},
        parent_call_id: 'orphan_parent',
        session_id: TEST_SESSION_ID,
      })
    })

    // No span yet, not in toolCalls yet (buffered)
    expect(useChatStore.getState().toolCalls['orphan_t1']).toBeUndefined()

    // Advance time past 10s TTL
    await act(async () => {
      vi.advanceTimersByTime(10_001)
    })

    // Now the buffered frame should be released as a flat tool call
    const state = useChatStore.getState()
    expect(state.toolCalls['orphan_t1']).toBeDefined()
    expect(state.toolCalls['orphan_t1'].tool).toBe('fs.list')

    // A dev console warning must have been emitted with the stable prefix.
    expect(warnSpy).toHaveBeenCalledWith(
      expect.stringContaining('[chat] orphan frame'),
    )

    vi.useRealTimers()
    warnSpy.mockRestore()
  })
})

// Regression: TestChatRouter_NonSpawnCall_NoSpan
// flat tool_call_start (no parent_call_id) is NOT grouped into any span

describe('ChatStore regression: flat tool call without parent_call_id', () => {
  it('renders as a flat ToolCallBadge (not attached to any span)', () => {
    act(() => {
      useChatStore.getState().updateLastAssistantMessage('', false)
    })

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_start',
        call_id: 'flat_1',
        tool: 'exec',
        params: { cmd: 'pwd' },
        session_id: TEST_SESSION_ID,
        // no parent_call_id
      })
    })

    const state = useChatStore.getState()
    // Tool call appears in the flat toolCalls map
    expect(state.toolCalls['flat_1']).toBeDefined()
    expect(state.toolCalls['flat_1'].tool).toBe('exec')

    // No span was created
    const lastMsg = state.messages[state.messages.length - 1]
    expect(lastMsg.spans ?? []).toHaveLength(0)
  })
})

// ── Replay parity tests ──────────────────────────────────────────────────────

// TDD row 18: ChatStore_ReplaySequence_MatchesLiveSequence
// Traces to: sprint-i-historical-replay-fidelity-spec.md FR-I-010
// Hard Constraint: "one reducer path" — live and replay sequences must produce
// identical ChatMessage shapes (excluding cursor/isStreaming flags).
describe('ChatStore_ReplaySequence_MatchesLiveSequence', () => {
  it('live token sequence and replay_message produce equivalent content, tool-call count, and ordering', () => {
    // Full reset including toolCallOrder (beforeEach only resets a subset of state)
    act(() => { useChatStore.getState().resetSession() })

    // ── Live sequence ──────────────────────────────────────────────────────────
    // Emit token frames producing text "A", then a tool call, then text "B", then done.
    act(() => {
      // Seed an assistant placeholder (sendMessage path does this; replicate here)
      useChatStore.getState().handleFrame({ type: 'token', content: 'A', session_id: TEST_SESSION_ID })
    })
    act(() => {
      // tool_call_start (no parent_call_id — flat call)
      useChatStore.getState().handleFrame({
        type: 'tool_call_start',
        call_id: 'tc_live_1',
        tool: 'shell',
        params: { cmd: 'echo hi' },
        session_id: TEST_SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_result',
        call_id: 'tc_live_1',
        tool: 'shell',
        result: { stdout: 'hi\n' },
        status: 'success',
        duration_ms: 42,
        session_id: TEST_SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({ type: 'token', content: 'B', session_id: TEST_SESSION_ID })
    })
    act(() => {
      useChatStore.getState().handleFrame({ type: 'done', session_id: TEST_SESSION_ID })
    })

    const liveState = useChatStore.getState()
    // Extract the single assistant message
    const liveAssistant = liveState.messages.find((m) => m.role === 'assistant')
    expect(liveAssistant).toBeDefined()
    const liveContent = liveAssistant!.content
    // After done, tool calls are baked into message.tool_calls and live map is cleared.
    const liveBakedToolCalls = liveAssistant!.tool_calls ?? []
    // Live-UAT regression fix: a tool call starting while the
    // bubble still holds text is a new-logical-unit boundary — the next token
    // gets a paragraph break instead of gluing directly onto the trailing
    // text (previously 'AB' with zero separator, matching the exact bug
    // repro shape "...now.Now delegating..."). See `pendingTextBoundary` on
    // ChatMessage. This diverges from the byte-for-byte live/replay parity
    // asserted below ONLY in this whitespace-formatting respect: the real
    // backend persists a tool-call-interrupted turn's pre/post narration as
    // SEPARATE transcript entries (pkg/agent/turn.go
    // appendIntermediateAssistantTranscript, Bug #416 fix), which replay
    // reconstructs as separate bubbles — not the single reused-bubble shape
    // this synthetic fixture exercises — so real replay never hits the glue
    // bug this fixes. The content-parity assertion below is normalized to
    // ignore this whitespace-only difference accordingly.
    expect(liveContent).toBe('A\n\nB')
    expect(liveBakedToolCalls).toHaveLength(1)
    expect(liveBakedToolCalls[0].tool).toBe('shell')
    expect(liveBakedToolCalls[0].status).toBe('success')
    expect(liveState.toolCallOrder).toHaveLength(0)
    // Live sequence: streaming flags settled
    expect(liveAssistant!.isStreaming).toBe(false)

    // ── Reset ─────────────────────────────────────────────────────────────────
    act(() => {
      useChatStore.getState().resetSession()
    })

    // ── Replay sequence ────────────────────────────────────────────────────────
    // replay_message for the completed assistant text, then tool_call_start/result, then done.
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message',
        role: 'assistant',
        content: 'AB',
        session_id: TEST_SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_start',
        call_id: 'tc_replay_1',
        tool: 'shell',
        params: { cmd: 'echo hi' },
        session_id: TEST_SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_result',
        call_id: 'tc_replay_1',
        tool: 'shell',
        result: { stdout: 'hi\n' },
        status: 'success',
        duration_ms: 42,
        session_id: TEST_SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({ type: 'done', session_id: TEST_SESSION_ID })
    })

    const replayState = useChatStore.getState()
    const replayAssistant = replayState.messages.find((m) => m.role === 'assistant')
    expect(replayAssistant).toBeDefined()

    // ── Assert shape parity ────────────────────────────────────────────────────
    // Content must match modulo whitespace: the live path now inserts a
    // paragraph break at the tool-call boundary (see comment above
    // `liveContent`'s assertion) — replay's single `replay_message` fixture
    // here has no equivalent boundary to break at, so it stays 'AB'. The
    // WORDS must still be identical; only the deliberate live-only
    // formatting differs.
    expect(replayAssistant!.content.replace(/\s+/g, '')).toBe(liveContent.replace(/\s+/g, ''))

    // Tool-call count must match (both baked into message.tool_calls after done)
    const replayBakedToolCalls = replayAssistant!.tool_calls ?? []
    expect(replayBakedToolCalls).toHaveLength(liveBakedToolCalls.length)

    // Tool-call properties must match
    expect(replayBakedToolCalls[0].tool).toBe(liveBakedToolCalls[0].tool)
    expect(replayBakedToolCalls[0].status).toBe(liveBakedToolCalls[0].status)

    // Cursor/streaming flags: replay_message arrives as a completed message (no cursor)
    // Live message: also settled after done. Both must be false.
    expect(replayAssistant!.isStreaming).toBe(false)
    // Live and replay both settle identically after done
    expect(replayAssistant!.isStreaming).toBe(liveAssistant!.isStreaming)
  })
})

// TDD row 18b: ChatStore_ReplaySequence_InterleavedTurn_TwoFrames
// Traces to: sprint-i-historical-replay-fidelity-spec.md FR-I-010
//
// Bug 1 fix (live/reload rendering parity regression, UAT A-I4): a real
// interleaved (narration -> tool call -> narration) turn persists as TWO
// SEPARATE transcript entries — pkg/agent/turn.go's
// appendIntermediateAssistantTranscript writes the pre-tool-call segment as
// its own entry, separate from the post-tool-call segment (the pre-existing
// "Bug #416" fix) — both stamped with the SAME ts.turnID. pkg/gateway/replay.go
// emits ONE replay_message frame per non-empty-content entry with no signal
// distinguishing "a new turn" from "the next segment of a still-in-progress
// turn", so a real interleaved turn replays as TWO SEPARATE replay_message
// frames ("A" then "B") — not the single merged frame the synthetic fixture
// in ChatStore_ReplaySequence_MatchesLiveSequence above uses.
//
// This test previously PINNED (documented, did not fix) a structural
// divergence: two replay_message frames used to produce two separate
// bubbles, not the single merged "A\n\nB" bubble live rendering produces for
// the same underlying turn (live's `token`-frame agent_id-boundary rule, Fix
// 5a, keeps one bubble open across a producer's whole turn). The
// `replay_message` reducer now merges consecutive same-turn_id/same-agent_id
// segments into one bubble (with a pendingTextBoundary-driven paragraph
// break, mirroring the live path exactly), so this test now asserts the
// FIXED behavior instead of merely documenting the gap.
describe('ChatStore_ReplaySequence_InterleavedTurn_TwoFrames', () => {
  it('two same-turn_id replay_message frames (matching real interleaved-turn backend output) merge into ONE bubble, matching live rendering', () => {
    act(() => { useChatStore.getState().resetSession() })

    // Entry 1: pre-tool-call narration, persisted as its own transcript entry
    // with the tool call attached to it (mirrors appendIntermediateAssistantTranscript).
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message',
        role: 'assistant',
        content: 'A',
        id: 'entry-1',
        agent_id: 'agent-ray',
        turn_id: 'turn-interleaved-1',
        session_id: TEST_SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_start',
        call_id: 'tc_interleaved_1',
        tool: 'shell',
        params: { cmd: 'echo hi' },
        agent_id: 'agent-ray',
        session_id: TEST_SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_result',
        call_id: 'tc_interleaved_1',
        tool: 'shell',
        result: { stdout: 'hi\n' },
        status: 'success',
        duration_ms: 42,
        session_id: TEST_SESSION_ID,
      })
    })
    // Entry 2: post-tool-call narration — its OWN separate transcript entry,
    // same turn_id AND same agent_id as entry 1 (both stamped from the same
    // ts.turnID / ts.resolveActiveAgentID() during the same turnState).
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message',
        role: 'assistant',
        content: 'B',
        id: 'entry-2',
        agent_id: 'agent-ray',
        turn_id: 'turn-interleaved-1',
        session_id: TEST_SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({ type: 'done', session_id: TEST_SESSION_ID })
    })

    const state = useChatStore.getState()
    const assistantMsgs = state.messages.filter((m) => m.role === 'assistant')

    // THE FIX: exactly ONE bubble — matching the single "A\n\nB" bubble the
    // live path produces for the same underlying turn — not two.
    expect(assistantMsgs).toHaveLength(1)
    expect(assistantMsgs[0].content).toBe('A\n\nB')
    expect(assistantMsgs[0].agentId).toBe('agent-ray')

    // The tool call lands on the single merged bubble.
    const toolCalls = assistantMsgs[0].tool_calls ?? []
    expect(toolCalls).toHaveLength(1)
    expect(toolCalls[0].tool).toBe('shell')

    // A reconnect that re-replays the same window (attach_session `since`
    // cursor) must not re-merge entry-2 a second time and duplicate content.
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message',
        role: 'assistant',
        content: 'B',
        id: 'entry-2',
        agent_id: 'agent-ray',
        turn_id: 'turn-interleaved-1',
        session_id: TEST_SESSION_ID,
      })
    })
    const afterReplay = useChatStore.getState()
    const afterAssistantMsgs = afterReplay.messages.filter((m) => m.role === 'assistant')
    expect(afterAssistantMsgs).toHaveLength(1)
    expect(afterAssistantMsgs[0].content).toBe('A\n\nB')
  })

  it('two replay_message frames with DIFFERENT turn_id (two genuinely separate turns by the same agent) stay as two separate bubbles', () => {
    act(() => { useChatStore.getState().resetSession() })

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message',
        role: 'assistant',
        content: 'First turn reply',
        id: 'entry-turn-1',
        agent_id: 'agent-ray',
        turn_id: 'turn-1',
        session_id: TEST_SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message',
        role: 'user',
        content: 'Follow-up question',
        id: 'entry-user-2',
        session_id: TEST_SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message',
        role: 'assistant',
        content: 'Second turn reply',
        id: 'entry-turn-2',
        agent_id: 'agent-ray',
        turn_id: 'turn-2',
        session_id: TEST_SESSION_ID,
      })
    })

    const state = useChatStore.getState()
    const assistantMsgs = state.messages.filter((m) => m.role === 'assistant')
    expect(assistantMsgs).toHaveLength(2)
    expect(assistantMsgs[0].content).toBe('First turn reply')
    expect(assistantMsgs[1].content).toBe('Second turn reply')
  })

  // 7-reviewer-gate finding (2 independent reviewers): the dedup check used
  // to consult ONLY the current tail bubble's `mergedReplayIds`. That is
  // correct at MERGE time (a merge always targets the then-current tail) but
  // wrong at DEDUP-CHECK time, which can run LATER on a WS reconnect. Trace:
  // entry B merges into bubble A (tail at merge time, B's id recorded in
  // A.mergedReplayIds). A new turn later produces bubble D (now the tail). A
  // WS reconnect re-replays the window including B again:
  // findLastAssistantMessageId now resolves to D, not A; D.mergedReplayIds
  // doesn't contain B's id, so the old tail-only check said "not merged";
  // the merge branch's own `sameTurn` check also fails (B belongs to an
  // earlier turn than D); execution fell through to pushing B as a
  // brand-new standalone message — a visible duplicate bubble of content
  // already rendered. Fixed via a session-scoped `mergedReplayMessageIds`
  // set (checked instead of the single-tail-bubble field) — see that
  // field's doc comment on SessionChatState.
  it('a WS reconnect re-replaying an already-merged entry after a LATER turn has become the tail does not duplicate it as a new bubble', () => {
    act(() => { useChatStore.getState().resetSession() })

    // Entry A: opens turn-1.
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message',
        role: 'assistant',
        content: 'A',
        id: 'entry-a',
        agent_id: 'agent-ray',
        turn_id: 'turn-1',
        session_id: TEST_SESSION_ID,
      })
    })
    // Entry B: same turn_id + agent_id as A — merges into A's bubble (A is
    // the tail at merge time). A.mergedReplayIds now contains 'entry-b'.
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message',
        role: 'assistant',
        content: 'B',
        id: 'entry-b',
        agent_id: 'agent-ray',
        turn_id: 'turn-1',
        session_id: TEST_SESSION_ID,
      })
    })

    let state = useChatStore.getState()
    let assistantMsgs = state.messages.filter((m) => m.role === 'assistant')
    expect(assistantMsgs).toHaveLength(1)
    expect(assistantMsgs[0].content).toBe('A\n\nB')

    // Entry D: a genuinely LATER, separate turn (turn-2) — becomes the new
    // tail bubble. A (with B merged in) is no longer the tail.
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message',
        role: 'assistant',
        content: 'D',
        id: 'entry-d',
        agent_id: 'agent-ray',
        turn_id: 'turn-2',
        session_id: TEST_SESSION_ID,
      })
    })

    state = useChatStore.getState()
    assistantMsgs = state.messages.filter((m) => m.role === 'assistant')
    expect(assistantMsgs).toHaveLength(2)
    expect(assistantMsgs[1].content).toBe('D')

    // WS reconnect re-replays the window, including entry B again — same id,
    // same content, same turn_id — even though D is now the tail bubble.
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message',
        role: 'assistant',
        content: 'B',
        id: 'entry-b',
        agent_id: 'agent-ray',
        turn_id: 'turn-1',
        session_id: TEST_SESSION_ID,
      })
    })

    state = useChatStore.getState()
    assistantMsgs = state.messages.filter((m) => m.role === 'assistant')
    // THE FIX: still exactly 2 assistant bubbles — B must NOT reappear as a
    // third, standalone duplicate bubble.
    expect(assistantMsgs).toHaveLength(2)
    expect(assistantMsgs[0].content).toBe('A\n\nB')
    expect(assistantMsgs[1].content).toBe('D')
  })

  it('two same-turn_id replay_message frames from DIFFERENT agents (delegate hands back to delegator) stay as two separate bubbles', () => {
    act(() => { useChatStore.getState().resetSession() })

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message',
        role: 'assistant',
        content: "Ray's research notes",
        id: 'entry-ray-1',
        agent_id: 'agent-ray',
        turn_id: 'shared-turn',
        session_id: TEST_SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message',
        role: 'assistant',
        content: "Delegator's synthesis",
        id: 'entry-delegator-1',
        agent_id: 'agent-delegator',
        turn_id: 'shared-turn',
        session_id: TEST_SESSION_ID,
      })
    })

    const state = useChatStore.getState()
    const assistantMsgs = state.messages.filter((m) => m.role === 'assistant')
    expect(assistantMsgs).toHaveLength(2)
    expect(assistantMsgs[0].agentId).toBe('agent-ray')
    expect(assistantMsgs[1].agentId).toBe('agent-delegator')
  })
})

// TDD row 19: ChatStore_ReplayMessageThenToolCall_InterleavesCorrectly
// Traces to: sprint-i-historical-replay-fidelity-spec.md FR-I-010
// Verifies that textAtToolCallStart is captured correctly when a tool_call_start
// follows a replay_message (completed, non-streaming assistant message).
describe('ChatStore_ReplayMessageThenToolCall_InterleavesCorrectly', () => {
  it('textAtToolCallStart snapshot equals the replay_message content when tool_call_start follows', () => {
    act(() => { useChatStore.getState().resetSession() })
    // Simulate: replay_message with content "Hello from replay" arrives, then tool_call_start
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message',
        role: 'assistant',
        content: 'Hello from replay',
        session_id: TEST_SESSION_ID,
      })
    })

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_start',
        call_id: 'tc_interleave_1',
        tool: 'fs.read',
        params: { path: '/etc/hosts' },
        session_id: TEST_SESSION_ID,
      })
    })

    const state = useChatStore.getState()

    // The tool call must be registered
    expect(state.toolCalls['tc_interleave_1']).toBeDefined()
    expect(state.toolCalls['tc_interleave_1'].tool).toBe('fs.read')

    // textAtToolCallStart must capture the replay_message content at the
    // point the tool call started — this is the visual text position for interleaving.
    const snapshot = state.textAtToolCallStart['tc_interleave_1']
    expect(snapshot).toBe('Hello from replay')
  })

  it('textAtToolCallStart is empty string when tool_call_start arrives before any assistant message', () => {
    act(() => { useChatStore.getState().resetSession() })
    // During replay, tool_call_start may arrive after an entry with no text content.
    // The snapshot should be '' not undefined.
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_start',
        call_id: 'tc_no_text',
        tool: 'web_search',
        params: { query: 'test' },
        session_id: TEST_SESSION_ID,
      })
    })

    const state = useChatStore.getState()
    const snapshot = state.textAtToolCallStart['tc_no_text']
    expect(snapshot).toBe('')
  })
})

// TDD row 18 supplement: isReplaying flag transitions
describe('ChatStore_isReplaying_flag', () => {
  it('starts false, can be set true via setReplaying, cleared to false on done (with 750ms minimum display window)', async () => {
    // W2-6(a): MIN_REPLAY_DISPLAY_MS raised from 250ms → 750ms (FR-I-014 timing fix).
    // The minimum window is long enough for Playwright's page.click() to return and
    // the test assertion to start polling — without this, the timer fires before the
    // first poll and the textarea is already enabled when Playwright checks.
    // Traces to: temporal-puzzling-melody.md W2-6(a)
    // Initial state
    expect(useChatStore.getState().isReplaying).toBe(false)

    // Simulate attach_session triggering setReplaying(true)
    act(() => {
      useChatStore.getState().setReplaying(true)
    })
    expect(useChatStore.getState().isReplaying).toBe(true)

    // done frame schedules clear — but minimum 750ms display window is enforced
    // so the placeholder doesn't flicker on sub-frame replays and Playwright can
    // observe the disabled state before the window expires.
    act(() => {
      useChatStore.getState().handleFrame({ type: 'done', session_id: TEST_SESSION_ID })
    })
    // Still true immediately after done (inside the window).
    expect(useChatStore.getState().isReplaying).toBe(true)

    // After >= 750ms the setTimeout fires and flips it.
    await new Promise((r) => setTimeout(r, 800))
    expect(useChatStore.getState().isReplaying).toBe(false)
  })

  it('done frame while not replaying is harmless — isReplaying stays false', () => {
    expect(useChatStore.getState().isReplaying).toBe(false)
    act(() => {
      useChatStore.getState().handleFrame({ type: 'done', session_id: TEST_SESSION_ID })
    })
    expect(useChatStore.getState().isReplaying).toBe(false)
  })

  it('resetSession clears isReplaying', () => {
    act(() => {
      useChatStore.getState().setReplaying(true)
    })
    expect(useChatStore.getState().isReplaying).toBe(true)
    act(() => {
      useChatStore.getState().resetSession()
    })
    expect(useChatStore.getState().isReplaying).toBe(false)
  })

  // W2-6(b): setReplaying(false) when already false is a no-op.
  it('setReplaying(false) when already false is a no-op — isReplaying stays false', () => {
    // BDD: Given isReplaying is already false
    // BDD: When setReplaying(false) is called
    // BDD: Then isReplaying stays false (no state change)
    // Traces to: temporal-puzzling-melody.md W2-6(b)
    expect(useChatStore.getState().isReplaying).toBe(false)
    act(() => {
      useChatStore.getState().setReplaying(false)
    })
    expect(useChatStore.getState().isReplaying).toBe(false)
  })

  // W2-6(c): setReplaying(true) resets the minimum window AND cancels any pending
  // clear-timer from the previous attach.
  //
  // Updated 2026-05-11: the previous behaviour (no-reset on re-true) made the
  // window observable for as little as ~0ms if a stale `replayingStartedAt`
  // from minutes earlier was still in the map (e.g. a session reopened in the
  // same SPA session). FR-I-014 requires a guaranteed visible window on every
  // attach; the only correct policy is to reset the window each time replay
  // is re-armed and cancel any in-flight false-flip timer that would otherwise
  // fire mid-window and prematurely re-enable the composer.
  it('setReplaying(true) when already true resets the minimum window and cancels stale timers', async () => {
    // BDD: Given setReplaying(true) was called at T=0, starting the 750ms window
    // BDD: When setReplaying(true) is called again at T=700ms
    // BDD: Then the window restarts — isReplaying stays true until at least T=700+750=1450ms

    act(() => {
      useChatStore.getState().setReplaying(true)
    })
    expect(useChatStore.getState().isReplaying).toBe(true)

    // Wait 700ms (still inside the 750ms window from the first call).
    await new Promise((r) => setTimeout(r, 700))

    // Second setReplaying(true) — must reset the window.
    act(() => {
      useChatStore.getState().setReplaying(true)
    })
    expect(useChatStore.getState().isReplaying).toBe(true)

    // Done frame schedules a clear. With the new behaviour, replayingStartedAt
    // was just reset to ~T=700, so elapsed=0, and the clear is scheduled
    // 750ms from now (~T=1450ms from the original T=0).
    act(() => {
      useChatStore.getState().handleFrame({ type: 'done', session_id: TEST_SESSION_ID })
    })

    // 100ms after the second setReplaying(true) — well inside the fresh 750ms window.
    // With the old (broken) behaviour, isReplaying would already be false because the
    // stale start time put us past 750ms. With the new behaviour, it stays true.
    await new Promise((r) => setTimeout(r, 100))
    expect(useChatStore.getState().isReplaying).toBe(true)

    // After the fresh window expires (~750ms after the second setReplaying), it clears.
    await new Promise((r) => setTimeout(r, 700))
    expect(useChatStore.getState().isReplaying).toBe(false)
  })
})

// ── B1.3(d) — unknown-targetSid done frame handling ───────────────────────────

describe('chat store — done frame for unknown targetSid (B1.3d)', () => {
  // Traces to: B1.3(d) security hardening
  // When a done frame arrives for a targetSid that is not in sessionsById, the
  // store must log a warning and force-clear isStreaming on the active bucket so
  // the spinner does not render indefinitely.

  it('logs console.warn with chat.done_unknown_sid when targetSid is not in sessionsById', () => {
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})

    act(() => {
      // Active session IS in the store (set by beforeEach → resetStore)
      useChatStore.getState().appendMessage({
        id: 'asst_streaming',
        session_id: TEST_SESSION_ID,
        role: 'assistant',
        content: 'streaming…',
        timestamp: new Date().toISOString(),
        status: 'streaming',
        isStreaming: true,
      })
      useChatStore.setState({ isStreaming: true })

      // done arrives for a session that is NOT in sessionsById
      useChatStore.getState().handleFrame({
        type: 'done',
        session_id: 'unknown-session-xyz',
      })
    })

    expect(warnSpy).toHaveBeenCalledWith(
      'chat.done_unknown_sid',
      expect.objectContaining({ targetSid: 'unknown-session-xyz' })
    )

    warnSpy.mockRestore()
  })

  it('force-clears isStreaming on the active bucket when done arrives for unknown targetSid', () => {
    act(() => {
      useChatStore.getState().appendMessage({
        id: 'asst_stuck',
        session_id: TEST_SESSION_ID,
        role: 'assistant',
        content: 'partial…',
        timestamp: new Date().toISOString(),
        status: 'streaming',
        isStreaming: true,
      })
      useChatStore.setState({ isStreaming: true })
    })

    // Verify we start streaming
    expect(useChatStore.getState().isStreaming).toBe(true)

    act(() => {
      // done for an unknown session — the active bucket must recover
      useChatStore.getState().handleFrame({
        type: 'done',
        session_id: 'unknown-session-xyz',
      })
    })

    // isStreaming must be cleared on the active bucket
    expect(useChatStore.getState().isStreaming).toBe(false)
    // The active bucket in sessionsById must also reflect the cleared state
    const activeBucket = useChatStore.getState().sessionsById[TEST_SESSION_ID]
    expect(activeBucket?.isStreaming).toBe(false)
  })

  it('processes done normally when targetSid is known', () => {
    act(() => {
      useChatStore.getState().appendMessage({
        id: 'asst_known',
        session_id: TEST_SESSION_ID,
        role: 'assistant',
        content: 'some text',
        timestamp: new Date().toISOString(),
        status: 'streaming',
        isStreaming: true,
      })
      useChatStore.setState({ isStreaming: true })

      // done for the known TEST_SESSION_ID — normal path
      useChatStore.getState().handleFrame({
        type: 'done',
        session_id: TEST_SESSION_ID,
        stats: { tokens: 42, cost: 0.001, duration_ms: 100 },
      })
    })

    expect(useChatStore.getState().isStreaming).toBe(false)
    const msg = useChatStore.getState().messages.find((m) => m.id === 'asst_known')
    expect(msg?.status).toBe('done')
    expect(useChatStore.getState().sessionTokens).toBe(42)
  })
})

// W2-10: Sibling-spans cross-wire test.
// Two spans A (parentCallId "cA") and B (parentCallId "cB") open.
// Emit 2 tool_call_start frames both with parent_call_id "cA".
// Assert A.steps.length === 2 AND B.steps.length === 0.
// Guards against a routing bug that could increment both spans' counters.
//
// Traces to: temporal-puzzling-melody.md W2-10
describe('ChatStore_sibling_spans_crosswire (W2-10)', () => {
  it('tool_call_start with parent_call_id "cA" routes to span A only, not span B', () => {
    // BDD: Given two open spans A (parentCallId "cA") and B (parentCallId "cB")
    // BDD: When 2 tool_call_start frames arrive with parent_call_id "cA"
    // BDD: Then span A has 2 steps and span B has 0 steps
    // Traces to: temporal-puzzling-melody.md W2-10

    act(() => {
      // Create an assistant message to host the spans
      useChatStore.getState().appendMessage({
        id: 'asst-sibling-1',
        role: 'assistant',
        content: 'Working...',
        timestamp: new Date().toISOString(),
        status: 'streaming',
        isStreaming: true,
      })

      // Start span A (parentCallId = "cA")
      useChatStore.getState().handleFrame({
        type: 'subagent_start',
        span_id: 'spanA',
        parent_call_id: 'cA',
        task_label: 'Span A task',
        agent_id: 'agent-a',
        session_id: TEST_SESSION_ID,
      })

      // Start span B (parentCallId = "cB")
      useChatStore.getState().handleFrame({
        type: 'subagent_start',
        span_id: 'spanB',
        parent_call_id: 'cB',
        task_label: 'Span B task',
        agent_id: 'agent-b',
        session_id: TEST_SESSION_ID,
      })
    })

    // Emit 2 tool_call_start frames, both targeting span A (parent_call_id: "cA")
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_start',
        call_id: 'step_a_1',
        tool: 'web_search',
        params: { query: 'query 1' },
        parent_call_id: 'cA',
        session_id: TEST_SESSION_ID,
      })
      useChatStore.getState().handleFrame({
        type: 'tool_call_start',
        call_id: 'step_a_2',
        tool: 'fs.read',
        params: { path: '/tmp/test' },
        parent_call_id: 'cA',
        session_id: TEST_SESSION_ID,
      })
    })

    const state = useChatStore.getState()
    const asstMsg = state.messages.find((m) => m.id === 'asst-sibling-1')
    expect(asstMsg).toBeDefined()
    expect(asstMsg?.spans).toHaveLength(2)

    const spanA = asstMsg!.spans!.find((s) => s.spanId === 'spanA')
    const spanB = asstMsg!.spans!.find((s) => s.spanId === 'spanB')
    expect(spanA).toBeDefined()
    expect(spanB).toBeDefined()

    // Span A must have exactly 2 steps (both tool_call_start frames targeted "cA")
    expect(spanA!.steps).toHaveLength(2)
    const stepA0 = spanA!.steps[0]
    const stepA1 = spanA!.steps[1]
    expect(stepA0.kind === 'tool' ? stepA0.tool.call_id : undefined).toBe('step_a_1')
    expect(stepA1.kind === 'tool' ? stepA1.tool.call_id : undefined).toBe('step_a_2')

    // Span B must have exactly 0 steps (no frames targeted "cB")
    expect(spanB!.steps).toHaveLength(0)
  })
})

// H1-FE: Regression — unknown-sid done must not corrupt an active mid-stream session.
// When a `done` frame arrives for a session_id not in sessionsById (e.g. a deleted
// or replayed session), the handler should NOT force-clear isStreaming on the active
// bucket if the active session sent a user message recently and is still streaming.
describe('chat store — H1-FE: unknown-sid done does not corrupt active stream', () => {
  const ACTIVE_SID = 'active-session'
  const GHOST_SID = 'ghost-session-wiped'

  function seedActiveMidStream(lastUserMessageAt: number) {
    act(() => {
      useSessionStore.setState({ activeSessionId: ACTIVE_SID, activeAgentId: null, activeAgentType: null })
      const seedMsgs = [
        { id: 'u1', session_id: ACTIVE_SID, role: 'user' as const, content: 'hi', timestamp: new Date().toISOString(), status: 'done' as const },
        { id: 'a1', session_id: ACTIVE_SID, role: 'assistant' as const, content: 'thinking…', timestamp: new Date().toISOString(), status: 'streaming' as const, isStreaming: true },
      ]
      useChatStore.setState((state) => ({
        sessionsById: {
          ...state.sessionsById,
          [ACTIVE_SID]: {
            ...makeBucketMessages(seedMsgs),
            toolCalls: {},
            toolCallOrder: [],
            textAtToolCallStart: {},
            isStreaming: true,
            isReplaying: false,
            replayCompletedForSession: null,
            sessionTokens: 0,
            sessionCost: 0,
            rateLimitEvent: null,
            cancelStage: null,
            lastUserMessageAt,
            lastReceivedEventTime: null,
            spanByParentCallId: {},
          },
        },
        // Sync foreground fields
        isStreaming: true,
        messages: seedMsgs,
        toolCalls: {},
        toolCallOrder: [],
        textAtToolCallStart: {},
        sessionTokens: 0,
        sessionCost: 0,
        isReplaying: false,
        replayCompletedForSession: null,
        rateLimitEvent: null,
        lastUserMessageAt,
        lastReceivedEventTime: null,
      }))
    })
  }

  it('leaves active bucket isStreaming=true when unknown-sid done arrives mid-stream (within 10s)', () => {
    // Seed active session as streaming, user sent message 2 seconds ago
    seedActiveMidStream(Date.now() - 2_000)

    // Dispatch a done frame for the ghost session (not in sessionsById)
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'done',
        session_id: GHOST_SID,
        stats: { tokens: 0, cost: 0, duration_ms: 0 },
      })
    })

    // Active bucket must still be streaming — the unknown-sid done must not touch it
    const activeBucket = useChatStore.getState().sessionsById[ACTIVE_SID]
    expect(activeBucket?.isStreaming).toBe(true)
  })

  it('clears active bucket isStreaming when unknown-sid done arrives after grace period (>10s)', () => {
    // Seed active session as streaming, but user sent message 15 seconds ago
    // (stale spinner from a wiped session — safe to clear)
    seedActiveMidStream(Date.now() - 15_000)

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'done',
        session_id: GHOST_SID,
        stats: { tokens: 0, cost: 0, duration_ms: 0 },
      })
    })

    // Active bucket spinner is stale — should be cleared
    const activeBucket = useChatStore.getState().sessionsById[ACTIVE_SID]
    expect(activeBucket?.isStreaming).toBe(false)
  })
})

// B3: cancel_stage frame handling
// Refs: docs/internal/specs/cancel-cross-channel-spec-review.md B3, L-1
describe('chat store — cancel_stage frame (B3)', () => {
  it('sets cancelStage to "graceful" when a cancel_stage graceful frame arrives', () => {
    // Seed session as streaming so the frame has a valid target bucket.
    act(() => {
      useChatStore.setState({
        sessionsById: {
          [TEST_SESSION_ID]: {
            ...useChatStore.getState().sessionsById[TEST_SESSION_ID] ?? {
              ...makeBucketMessages([]),
              toolCalls: {},
              toolCallOrder: [],
              textAtToolCallStart: {},
              isReplaying: false,
              replayCompletedForSession: null,
              sessionTokens: 0,
              sessionCost: 0,
              rateLimitEvent: null,
              lastUserMessageAt: null,
              cancelStage: null,
              lastReceivedEventTime: null,
              spanByParentCallId: {},
            },
            isStreaming: true,
          },
        },
      })
    })

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'cancel_stage',
        session_id: TEST_SESSION_ID,
        stage: 'graceful',
      })
    })

    const bucket = useChatStore.getState().sessionsById[TEST_SESSION_ID]
    expect(bucket?.cancelStage).toBe('graceful')
    // Foreground field is synced too.
    expect(useChatStore.getState().cancelStage).toBe('graceful')
  })

  it('sets cancelStage to "hard" when a cancel_stage hard frame arrives', () => {
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'cancel_stage',
        session_id: TEST_SESSION_ID,
        stage: 'hard',
      })
    })

    const bucket = useChatStore.getState().sessionsById[TEST_SESSION_ID]
    expect(bucket?.cancelStage).toBe('hard')
  })

  it('sets cancelStage to "detached" when a cancel_stage detached frame arrives', () => {
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'cancel_stage',
        session_id: TEST_SESSION_ID,
        stage: 'detached',
      })
    })

    const bucket = useChatStore.getState().sessionsById[TEST_SESSION_ID]
    expect(bucket?.cancelStage).toBe('detached')
  })

  it('clears cancelStage to null when a done frame arrives after cancel', () => {
    // First set a cancel stage.
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'cancel_stage',
        session_id: TEST_SESSION_ID,
        stage: 'hard',
      })
    })
    expect(useChatStore.getState().sessionsById[TEST_SESSION_ID]?.cancelStage).toBe('hard')

    // Then receive the done frame.
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'done',
        session_id: TEST_SESSION_ID,
        stats: { tokens: 10, cost: 0.001, duration_ms: 500 },
      })
    })

    expect(useChatStore.getState().sessionsById[TEST_SESSION_ID]?.cancelStage).toBeNull()
    expect(useChatStore.getState().cancelStage).toBeNull()
  })
})

// ── G1: MAX_MESSAGES_PER_SESSION ring-buffer enforcement ──────────────────────

describe('G1: ring buffer — MAX_MESSAGES_PER_SESSION enforcement', () => {
  it('appending 600 messages leaves exactly MAX_MESSAGES_PER_SESSION messages in the bucket', () => {
    // Seed 600 replay_message frames via resetSessionForReplay + handleFrame.
    act(() => {
      useChatStore.getState().resetSessionForReplay(TEST_SESSION_ID)
    })

    for (let i = 0; i < 600; i++) {
      act(() => {
        useChatStore.getState().handleFrame({
          type: 'replay_message',
          session_id: TEST_SESSION_ID,
          role: i % 2 === 0 ? 'user' : 'assistant',
          content: `Message ${i}`,
          timestamp: new Date(Date.now() + i * 1000).toISOString(),
        })
      })
    }

    const bucket = useChatStore.getState().sessionsById[TEST_SESSION_ID]
    const msgs = bucket ? getMessages(bucket) : []
    expect(
      msgs.length,
      `Expected exactly ${MAX_MESSAGES_PER_SESSION} messages after ring-buffer trim, got ${msgs.length}`,
    ).toBe(MAX_MESSAGES_PER_SESSION)

    // Verify the oldest messages were evicted (we should see messages 100–599)
    expect(msgs[0].content).toBe('Message 100')
    expect(msgs[msgs.length - 1].content).toBe('Message 599')

    // trimmedCount must reflect the eviction
    expect(bucket?.trimmedCount).toBeGreaterThan(0)
  })
})

// ── G4: clampToolResult — client-side truncation ──────────────────────────────

describe('G4: clampToolResult — large result is clamped to ClientTruncatedResult', () => {
  it('a 100 KiB string result is replaced with a _truncated_client sentinel', () => {
    // Build a 100 KiB string (100 * 1024 = 102400 chars).
    const largeString = 'x'.repeat(100 * 1024)
    const clamped = clampToolResult(largeString)

    expect(
      isClientTruncatedResult(clamped),
      'clampToolResult must return a ClientTruncatedResult for a 100 KiB input',
    ).toBe(true)

    if (isClientTruncatedResult(clamped)) {
      expect(clamped._truncated_client).toBe(true)
      expect(clamped.original_size_bytes).toBeGreaterThan(0)
      // Preview must be exactly 4 KiB (4096 chars from the original)
      expect(clamped.preview.length).toBe(4096)
      expect(clamped.preview).toBe(largeString.slice(0, 4096))
    }
  })

  it('a small result (under 50 KiB) passes through unchanged', () => {
    const small = { status: 'ok', data: 'hello' }
    const result = clampToolResult(small)
    expect(result).toBe(small)
    expect(isClientTruncatedResult(result)).toBe(false)
  })

  it('an existing _truncated sentinel passes through unchanged (no double-wrapping)', () => {
    const serverTruncated = { _truncated: true as const, original_size_bytes: 500_000, preview: 'first bytes...' }
    const result = clampToolResult(serverTruncated)
    expect(result).toBe(serverTruncated)
    expect(isClientTruncatedResult(result)).toBe(false)
  })
})

// ── Wave 3 Fix 3: findLastAssistantMessageId direct unit coverage ────────────
//
// findLastAssistantMessageId (chat.ts ~L265) is exported and used at 13 call
// sites (WS-frame handlers, cancel, sendMessage's tool-call rebake), but
// previously only had indirect coverage via reducer tests that happen to
// exercise it. These are direct tests of the pure function itself.

function makeMessage(id: string, role: ChatMessage['role']): ChatMessage {
  return {
    id,
    role,
    content: `content-${id}`,
    timestamp: '2026-01-01T00:00:00.000Z',
  } as ChatMessage
}

describe('findLastAssistantMessageId — direct unit coverage (Wave 3 Fix 3)', () => {
  it('returns the id of the most recent assistant message in a mixed order', () => {
    const msgs = [
      makeMessage('u1', 'user'),
      makeMessage('a1', 'assistant'),
      makeMessage('u2', 'user'),
      makeMessage('a2', 'assistant'),
      makeMessage('u3', 'user'),
    ]
    const { messagesById, messageOrder } = makeBucketMessages(msgs)

    expect(findLastAssistantMessageId(messageOrder, messagesById)).toBe('a2')
  })

  it('returns null when no assistant messages are present', () => {
    const msgs = [makeMessage('u1', 'user'), makeMessage('u2', 'user'), makeMessage('s1', 'system')]
    const { messagesById, messageOrder } = makeBucketMessages(msgs)

    expect(findLastAssistantMessageId(messageOrder, messagesById)).toBeNull()
  })

  it('returns null for an empty order', () => {
    expect(findLastAssistantMessageId([], {})).toBeNull()
  })
})

// ── G2: span-index O(1) hit before scan-fallback ──────────────────────────────

describe('G2: spanByParentCallId O(1) index is written on subagent_start and cleared on subagent_end', () => {
  it('spanByParentCallId has the parentCallId → {messageId, spanIdx} entry after subagent_start', () => {
    // Arrange: an active streaming assistant message (so the span attaches to it).
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'session_started',
        session_id: TEST_SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'token',
        session_id: TEST_SESSION_ID,
        content: 'Planning...',
      })
    })

    // Inject subagent_start with a known parentCallId.
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'subagent_start',
        session_id: TEST_SESSION_ID,
        span_id: 'span-g2-test',
        parent_call_id: 'tc-g2-parent',
        task_label: 'Run subtask',
      })
    })

    const bucket = useChatStore.getState().sessionsById[TEST_SESSION_ID]
    expect(bucket).toBeDefined()

    const index = bucket!.spanByParentCallId
    expect(
      'tc-g2-parent' in index,
      'spanByParentCallId must contain the parentCallId after subagent_start',
    ).toBe(true)
    expect(index['tc-g2-parent'].messageId).toBeTruthy()
    expect(typeof index['tc-g2-parent'].spanIdx).toBe('number')
  })

  it('spanByParentCallId entry is removed after subagent_end', () => {
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'session_started',
        session_id: TEST_SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'token',
        session_id: TEST_SESSION_ID,
        content: 'Planning...',
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'subagent_start',
        session_id: TEST_SESSION_ID,
        span_id: 'span-g2-end-test',
        parent_call_id: 'tc-g2-end',
        task_label: 'Run subtask',
      })
    })

    // Verify the index was written.
    const before = useChatStore.getState().sessionsById[TEST_SESSION_ID]
    expect('tc-g2-end' in (before?.spanByParentCallId ?? {})).toBe(true)

    // subagent_end must clear the index entry.
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'subagent_end',
        session_id: TEST_SESSION_ID,
        span_id: 'span-g2-end-test',
        status: 'success',
        duration_ms: 500,
        final_result: 'done',
      })
    })

    const after = useChatStore.getState().sessionsById[TEST_SESSION_ID]
    expect(
      'tc-g2-end' in (after?.spanByParentCallId ?? {}),
      'spanByParentCallId must NOT contain the parentCallId after subagent_end',
    ).toBe(false)
  })
})

// ── G5: lastReceivedEventTime advances monotonically ─────────────────────────

describe('G5: lastReceivedEventTime advances on each replay_message with a newer timestamp', () => {
  it('lastReceivedEventTime is null initially, then advances with each timestamped replay frame', () => {
    act(() => {
      useChatStore.getState().resetSessionForReplay(TEST_SESSION_ID)
    })

    const t0 = '2026-01-01T00:00:00.000Z'
    const t1 = '2026-01-01T00:00:01.000Z'
    const t2 = '2026-01-01T00:00:02.000Z'

    // Initial state: no lastReceivedEventTime.
    const init = useChatStore.getState().sessionsById[TEST_SESSION_ID]
    expect(init?.lastReceivedEventTime).toBeNull()

    // First frame at t0.
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message',
        session_id: TEST_SESSION_ID,
        role: 'user',
        content: 'hello',
        timestamp: t0,
      })
    })
    const after0 = useChatStore.getState().sessionsById[TEST_SESSION_ID]
    expect(after0?.lastReceivedEventTime).toBe(t0)

    // Second frame at t1 (newer) — must advance.
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message',
        session_id: TEST_SESSION_ID,
        role: 'assistant',
        content: 'world',
        timestamp: t1,
      })
    })
    const after1 = useChatStore.getState().sessionsById[TEST_SESSION_ID]
    expect(after1?.lastReceivedEventTime).toBe(t1)

    // Third frame at t2 (newer) — must advance.
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message',
        session_id: TEST_SESSION_ID,
        role: 'user',
        content: 'again',
        timestamp: t2,
      })
    })
    const after2 = useChatStore.getState().sessionsById[TEST_SESSION_ID]
    expect(after2?.lastReceivedEventTime).toBe(t2)

    // An older frame must NOT rewind the cursor.
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message',
        session_id: TEST_SESSION_ID,
        role: 'user',
        content: 'old',
        timestamp: t0, // older than t2
      })
    })
    const afterOld = useChatStore.getState().sessionsById[TEST_SESSION_ID]
    expect(
      afterOld?.lastReceivedEventTime,
      'lastReceivedEventTime must not rewind when an older-timestamped frame arrives',
    ).toBe(t2)
  })
})

// ── evictMessageFromBucket unit tests ─────────────────────────────────────────

describe('evictMessageFromBucket — full sweep of dependent maps', () => {
  it('removes the message from messagesById and messageOrder', () => {
    const bucket: SessionChatState = {
      messagesById: { 'm1': { id: 'm1', role: 'user', content: 'hi', timestamp: '', status: 'done' } },
      messageOrder: ['m1'],
      trimmedCount: 0,
      toolCalls: {},
      toolCallOrder: [],
      textAtToolCallStart: {},
      spanByParentCallId: {},
      isStreaming: false,
      isReplaying: false,
      replayCompletedForSession: null,
      sessionTokens: 0,
      sessionCost: 0,
      rateLimitEvent: null,
      lastUserMessageAt: null,
      cancelStage: null,
      lastReceivedEventTime: null,
    }
    evictMessageFromBucket(bucket, 'm1')
    expect(bucket.messageOrder).toHaveLength(0)
    expect(bucket.messagesById['m1']).toBeUndefined()
  })

  it('removes tool calls owned by the evicted message (via tool_calls array)', () => {
    const bucket: SessionChatState = {
      messagesById: {
        'm1': {
          id: 'm1', role: 'assistant', content: '', timestamp: '', status: 'done',
          tool_calls: [{ id: 'tc1', tool: 'exec', params: {}, status: 'success' }],
        },
      },
      messageOrder: ['m1'],
      trimmedCount: 0,
      toolCalls: { tc1: { id: 'tc1', call_id: 'tc1', tool: 'exec', params: {}, status: 'success' } },
      toolCallOrder: ['tc1'],
      textAtToolCallStart: { tc1: 'some text' },
      spanByParentCallId: {},
      isStreaming: false,
      isReplaying: false,
      replayCompletedForSession: null,
      sessionTokens: 0,
      sessionCost: 0,
      rateLimitEvent: null,
      lastUserMessageAt: null,
      cancelStage: null,
      lastReceivedEventTime: null,
    }
    evictMessageFromBucket(bucket, 'm1')
    expect(bucket.toolCalls['tc1']).toBeUndefined()
    expect(bucket.toolCallOrder).toHaveLength(0)
    expect(bucket.textAtToolCallStart['tc1']).toBeUndefined()
  })

  it('removes spanByParentCallId entries pointing at the evicted message', () => {
    const bucket: SessionChatState = {
      messagesById: {
        'm1': { id: 'm1', role: 'assistant', content: '', timestamp: '', status: 'done' },
      },
      messageOrder: ['m1'],
      trimmedCount: 0,
      toolCalls: {},
      toolCallOrder: [],
      textAtToolCallStart: {},
      spanByParentCallId: {
        'pc1': { messageId: 'm1', spanIdx: 0 },
        'pc2': { messageId: 'm2', spanIdx: 0 }, // belongs to a different message — must survive
      },
      isStreaming: false,
      isReplaying: false,
      replayCompletedForSession: null,
      sessionTokens: 0,
      sessionCost: 0,
      rateLimitEvent: null,
      lastUserMessageAt: null,
      cancelStage: null,
      lastReceivedEventTime: null,
    }
    evictMessageFromBucket(bucket, 'm1')
    expect(bucket.spanByParentCallId['pc1']).toBeUndefined()
    // pc2 belongs to a different message and must not be touched.
    expect(bucket.spanByParentCallId['pc2']).toBeDefined()
  })
})

// ── Eviction-leak regression: applyMessageArray path ─────────────────────────
// Seeds 600 messages where every assistant message has 3 tool calls and 1 span.
// Trims via appendMessage (applyMessageArray path) and asserts dependent maps
// evict the correct entries.

describe('eviction-leak regression — applyMessageArray evicts spanByParentCallId, spanBySpanId, and textAtToolCallStart', () => {
  it('after trimming 100 messages, spanByParentCallId, spanBySpanId, and textAtToolCallStart track the surviving set', () => {
    act(() => {
      useChatStore.getState().resetSession()
    })

    // Seed 600 assistant messages, each with 3 tool calls (in tool_calls array).
    // Also manually populate spanByParentCallId, spanBySpanId, and textAtToolCallStart
    // to simulate in-flight spans and tool call snapshots.
    const TOTAL = 600

    // Build the bucket directly to avoid the test going through 600 handleFrame cycles.
    const msgs: import('./chat').ChatMessage[] = []
    const toolCalls: SessionChatState['toolCalls'] = {}
    const toolCallOrder: string[] = []
    const textAtToolCallStart: SessionChatState['textAtToolCallStart'] = {}
    const spanByParentCallId: SessionChatState['spanByParentCallId'] = {}
    const spanBySpanId: NonNullable<SessionChatState['spanBySpanId']> = {}

    for (let i = 0; i < TOTAL; i++) {
      const msgId = `msg_${i}`
      const tcIds = [`tc_${i}_0`, `tc_${i}_1`, `tc_${i}_2`]
      const parentCallId = `pc_${i}`
      const spanId = `span_${i}`

      msgs.push({
        id: msgId,
        role: 'assistant' as const,
        content: `Message ${i}`,
        timestamp: new Date(Date.now() + i * 1000).toISOString(),
        status: 'done' as const,
        tool_calls: tcIds.map((tcId) => ({ id: tcId, tool: 'exec', params: {}, status: 'success' as const })),
      })

      for (const tcId of tcIds) {
        toolCalls[tcId] = { id: tcId, call_id: tcId, tool: 'exec', params: {}, status: 'success' }
        toolCallOrder.push(tcId)
        textAtToolCallStart[tcId] = `snapshot for ${tcId}`
      }

      // Each message also has a span entry in BOTH span indexes (lockstep).
      spanByParentCallId[parentCallId] = { messageId: msgId, spanIdx: 0 }
      spanBySpanId[spanId] = { messageId: msgId, spanIdx: 0 }
    }

    act(() => {
      useChatStore.setState((s) => ({
        sessionsById: {
          ...s.sessionsById,
          [TEST_SESSION_ID]: {
            ...makeBucketMessages(msgs),
            toolCalls,
            toolCallOrder,
            textAtToolCallStart,
            spanByParentCallId,
            spanBySpanId,
            isStreaming: false,
            isReplaying: false,
            replayCompletedForSession: null,
            sessionTokens: 0,
            sessionCost: 0,
            rateLimitEvent: null,
            lastUserMessageAt: null,
            cancelStage: null,
            lastReceivedEventTime: null,
          },
        },
      }))
    })

    // Now trigger appendMessage which will go through applyMessageArray and trim.
    act(() => {
      useChatStore.getState().appendMessage({
        id: 'trigger_msg',
        role: 'user',
        content: 'trigger',
        timestamp: new Date().toISOString(),
        status: 'done',
      })
    })

    const bucket = useChatStore.getState().sessionsById[TEST_SESSION_ID]!
    const survivingMsgIds = new Set(bucket.messageOrder)

    // There should be exactly MAX_MESSAGES_PER_SESSION messages after trim.
    expect(bucket.messageOrder.length).toBe(MAX_MESSAGES_PER_SESSION)

    // All spanByParentCallId entries must point to surviving messages.
    for (const [parentCallId, entry] of Object.entries(bucket.spanByParentCallId)) {
      expect(
        survivingMsgIds.has(entry.messageId),
        `spanByParentCallId["${parentCallId}"] points to evicted message "${entry.messageId}"`,
      ).toBe(true)
    }

    // All spanBySpanId entries must point to surviving messages (mirrors above —
    // lockstep invariant: the helper-based eviction must filter both maps together).
    for (const [spanId, entry] of Object.entries(bucket.spanBySpanId ?? {})) {
      expect(
        survivingMsgIds.has(entry.messageId),
        `spanBySpanId["${spanId}"] points to evicted message "${entry.messageId}"`,
      ).toBe(true)
    }

    // Count how many span entries we expect: one per surviving assistant message.
    const survivingAssistantMsgIds = new Set(
      bucket.messageOrder.filter((id) => bucket.messagesById[id]?.role === 'assistant')
    )
    expect(Object.keys(bucket.spanByParentCallId).length).toBe(survivingAssistantMsgIds.size)
    expect(Object.keys(bucket.spanBySpanId ?? {}).length).toBe(survivingAssistantMsgIds.size)

    // All textAtToolCallStart entries must have call_ids present in toolCallOrder.
    const liveCallIdSet = new Set(bucket.toolCallOrder)
    for (const callId of Object.keys(bucket.textAtToolCallStart)) {
      expect(
        liveCallIdSet.has(callId),
        `textAtToolCallStart has entry for evicted call_id "${callId}"`,
      ).toBe(true)
    }
  })
})

// ── Eviction-leak regression: replay_message path ────────────────────────────
// Sends 600+ replay_message frames so the inline replay-path eviction fires.
// Asserts spanByParentCallId and textAtToolCallStart are clean after replay.

describe('eviction-leak regression — replay_message path evicts dependent maps', () => {
  it('after replaying 600 messages with spans, spanByParentCallId has no dangling entries', () => {
    act(() => {
      useChatStore.getState().resetSessionForReplay(TEST_SESSION_ID)
    })

    const TOTAL = 600

    // Pre-populate spanByParentCallId with entries for messages 0..599,
    // then send replay_message frames for all 600 messages so the ring buffer
    // evicts the oldest 100 via the inline replay-path.
    const spanByParentCallId: SessionChatState['spanByParentCallId'] = {}
    for (let i = 0; i < TOTAL; i++) {
      spanByParentCallId[`pc_${i}`] = { messageId: `replay_msg_${i}`, spanIdx: 0 }
    }

    act(() => {
      useChatStore.setState((s) => ({
        sessionsById: {
          ...s.sessionsById,
          [TEST_SESSION_ID]: {
            ...s.sessionsById[TEST_SESSION_ID]!,
            spanByParentCallId,
          },
        },
      }))
    })

    // Replay 600 messages — the ring buffer evicts old ones via the replay path.
    for (let i = 0; i < TOTAL; i++) {
      act(() => {
        useChatStore.getState().handleFrame({
          type: 'replay_message',
          session_id: TEST_SESSION_ID,
          id: `replay_msg_${i}`,
          role: i % 2 === 0 ? 'user' : 'assistant',
          content: `Replay ${i}`,
          timestamp: new Date(Date.now() + i * 1000).toISOString(),
        })
      })
    }

    const bucket = useChatStore.getState().sessionsById[TEST_SESSION_ID]!
    const survivingMsgIds = new Set(bucket.messageOrder)

    expect(bucket.messageOrder.length).toBe(MAX_MESSAGES_PER_SESSION)

    // After replay, all spanByParentCallId entries must point to surviving messages only.
    for (const [parentCallId, entry] of Object.entries(bucket.spanByParentCallId)) {
      expect(
        survivingMsgIds.has(entry.messageId),
        `spanByParentCallId["${parentCallId}"] points to evicted message "${entry.messageId}" after replay eviction`,
      ).toBe(true)
    }
  })
})

// ── Eviction-leak regression: tool_call_result burst path ────────────────────
// Verifies the ring buffer stays clean when a burst of tool_call_result frames
// (not just replay_message) causes eviction.

describe('eviction-leak regression — tool_call_result burst triggers eviction with clean maps', () => {
  it('after 501 appendMessage calls with tool calls, textAtToolCallStart contains no evicted entries', () => {
    act(() => {
      useChatStore.getState().resetSession()
    })

    // Append MAX+1 messages where each is an assistant message with a tool call
    // snapshot recorded via startToolCall (which writes textAtToolCallStart).
    // We drive this through handleFrame to exercise the full pipeline.
    for (let i = 0; i < MAX_MESSAGES_PER_SESSION + 1; i++) {
      act(() => {
        // A tool_call_start frame adds a textAtToolCallStart entry.
        useChatStore.getState().handleFrame({
          type: 'tool_call_start',
          call_id: `burst_tc_${i}`,
          tool: 'exec',
          params: { cmd: `cmd_${i}` },
          session_id: TEST_SESSION_ID,
        })
      })
      act(() => {
        // A done frame bakes the tool calls into the message and closes the turn.
        // Each done frame appends a completed assistant message + resets toolCalls.
        useChatStore.getState().handleFrame({
          type: 'tool_call_result',
          call_id: `burst_tc_${i}`,
          tool: 'exec',
          result: { exit_code: 0 },
          status: 'success',
          duration_ms: 10,
          session_id: TEST_SESSION_ID,
        })
      })
    }

    const bucket = useChatStore.getState().sessionsById[TEST_SESSION_ID]!

    // All textAtToolCallStart entries must have a corresponding live toolCallOrder entry.
    const liveCallIdSet = new Set(bucket.toolCallOrder)
    for (const callId of Object.keys(bucket.textAtToolCallStart)) {
      expect(
        liveCallIdSet.has(callId),
        `textAtToolCallStart has a dangling entry for "${callId}" not in toolCallOrder`,
      ).toBe(true)
    }
  })
})

describe('chat store — whatsapp_pairing routing (#283)', () => {
  it('handleFrame(whatsapp_pairing) routes the QR to the pairing store', () => {
    const pairingFrame: WhatsAppPairingFrame = {
      type: 'whatsapp_pairing',
      channel_id: 'whatsapp.sales', // pairing frames carry the INSTANCE id (post ADR-029)
      status: 'code',
      qr: 'QR-ROUTED',
    }
    act(() => {
      useWhatsAppPairingStore.setState({ byChannel: {} })
      useChatStore.getState().handleFrame(pairingFrame)
    })
    expect(useWhatsAppPairingStore.getState().byChannel['whatsapp.sales']).toEqual({
      status: 'code',
      qr: 'QR-ROUTED',
      message: '',
    })
  })
})

// B1 regression: placeholder agentId must be stamped at creation time, not read
// at render time. Switching the active agent after a message is created must NOT
// re-attribute that message to the new agent.
describe('chat store — B1: placeholder agentId stamped at creation, not at render', () => {
  it('token placeholder created with agent A retains agentId===A after switching to agent B', () => {
    // BDD:
    //   Given activeAgentId is 'agent-mia' and a token frame arrives (no prior assistant bubble)
    //   When the active agent is switched to 'agent-jim'
    //   Then the placeholder message still has agentId === 'agent-mia'
    act(() => {
      useSessionStore.setState({ activeSessionId: TEST_SESSION_ID, activeAgentId: 'agent-mia', activeAgentType: null })
      // Token frame with no prior assistant bubble — store must create a new placeholder.
      useChatStore.getState().handleFrame({ type: 'token', content: 'Hello', session_id: TEST_SESSION_ID })
    })

    // Placeholder created — agentId must be 'agent-mia'
    const afterToken = useChatStore.getState()
    const placeholder = afterToken.messages.find((m) => m.role === 'assistant')
    expect(placeholder).toBeDefined()
    expect(placeholder?.agentId).toBe('agent-mia')

    // Switch to a different agent and mark the message done.
    act(() => {
      useSessionStore.setState({ activeAgentId: 'agent-jim' })
      useChatStore.getState().handleFrame({ type: 'done', stats: { tokens: 10, cost: 0, duration_ms: 0 }, session_id: TEST_SESSION_ID })
    })

    // The completed message must still carry the original authoring agent.
    const afterSwitch = useChatStore.getState()
    const completedMsg = afterSwitch.messages.find((m) => m.role === 'assistant')
    expect(completedMsg).toBeDefined()
    expect(completedMsg?.agentId).toBe('agent-mia')
    expect(completedMsg?.status).toBe('done')
  })

  it('tool_call_start placeholder created with agent A retains agentId===A after switching to agent B', () => {
    // BDD:
    //   Given activeAgentId is 'agent-mia' and a tool_call_start frame arrives with no prior assistant bubble
    //   When the active agent is switched to 'agent-jim'
    //   Then the placeholder message still has agentId === 'agent-mia'
    act(() => {
      useSessionStore.setState({ activeSessionId: TEST_SESSION_ID, activeAgentId: 'agent-mia', activeAgentType: null })
      // tool_call_start with no prior assistant bubble — store must create a new placeholder.
      useChatStore.getState().handleFrame({
        type: 'tool_call_start',
        session_id: TEST_SESSION_ID,
        tool: 'web_search',
        call_id: 'call-001',
        params: { query: 'test' },
      })
    })

    // Placeholder created — agentId must be 'agent-mia'
    const afterToolCallStart = useChatStore.getState()
    const placeholder = afterToolCallStart.messages.find((m) => m.role === 'assistant')
    expect(placeholder).toBeDefined()
    expect(placeholder?.agentId).toBe('agent-mia')

    // Switch to a different agent.
    act(() => {
      useSessionStore.setState({ activeAgentId: 'agent-jim' })
    })

    // The placeholder must still carry the original authoring agent.
    const afterSwitch = useChatStore.getState()
    const msg = afterSwitch.messages.find((m) => m.role === 'assistant')
    expect(msg).toBeDefined()
    expect(msg?.agentId).toBe('agent-mia')
  })
})

// Wave 3 Fix 5a: TokenFrame.agent_id is now populated by the backend with the
// real producer's agent id at the point tokens are emitted. Live attribution
// must consume it instead of the client-side activeAgentId guess, which is
// wrong for background/delegated sub-turns where the true producer isn't
// "whoever the user happens to be chatting with".
describe('chat store — Fix 5a: live token attribution consumes TokenFrame.agent_id', () => {
  it('token frame carrying agent_id attributes a new placeholder to that agent, not activeAgentId', () => {
    act(() => {
      useSessionStore.setState({ activeSessionId: TEST_SESSION_ID, activeAgentId: 'agent-mia', activeAgentType: null })
      useChatStore.getState().handleFrame({
        type: 'token',
        content: 'Hello',
        session_id: TEST_SESSION_ID,
        agent_id: 'agent-jim', // real producer per the backend, differs from the client's activeAgentId guess
      })
    })
    const placeholder = useChatStore.getState().messages.find((m) => m.role === 'assistant')
    expect(placeholder).toBeDefined()
    expect(placeholder?.agentId).toBe('agent-jim')
  })

  it('token frame carrying agent_id backfills attribution onto the optimistic sendMessage() placeholder (no agentId yet)', () => {
    // This is the actual real-world path: sendMessage() creates an assistant
    // placeholder synchronously with no agentId (the true producer isn't known
    // at send time), then the first 'token' frame for the reply arrives and
    // reuses that SAME bubble (still isStreaming) rather than creating a new
    // one. Fix 5a must stamp agentId on this reuse path too, not only at
    // placeholder-creation time.
    const mockSend = vi.fn().mockReturnValue(true)
    act(() => {
      useConnectionStore.setState({
        connection: { send: mockSend, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as any,
        isConnected: true,
      })
      useSessionStore.setState({ activeSessionId: TEST_SESSION_ID, activeAgentId: 'agent-mia', activeAgentType: null })
    })
    act(() => {
      useChatStore.getState().sendMessage('hi there')
    })
    const optimisticPlaceholder = useChatStore.getState().messages.find((m) => m.role === 'assistant')
    expect(optimisticPlaceholder).toBeDefined()
    expect(optimisticPlaceholder?.agentId).toBeUndefined()

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'token',
        content: 'Hello',
        session_id: TEST_SESSION_ID,
        agent_id: 'agent-jim',
      })
    })
    const afterToken = useChatStore.getState().messages.find((m) => m.role === 'assistant')
    expect(afterToken?.agentId).toBe('agent-jim')
    expect(afterToken?.content).toBe('Hello')
  })

  it('token frame WITHOUT agent_id still falls back to the activeAgentId guess (no regression)', () => {
    act(() => {
      useSessionStore.setState({ activeSessionId: TEST_SESSION_ID, activeAgentId: 'agent-mia', activeAgentType: null })
      useChatStore.getState().handleFrame({
        type: 'token',
        content: 'Hello',
        session_id: TEST_SESSION_ID,
        // agent_id omitted — legacy/older-format frame.
      })
    })
    const placeholder = useChatStore.getState().messages.find((m) => m.role === 'assistant')
    expect(placeholder).toBeDefined()
    expect(placeholder?.agentId).toBe('agent-mia')
  })
})

// Wave 3 Fix 5c: pkg/gateway/replay.go now emits a role:"turn_canceled"
// ReplayMessageFrame carrying a turn_id that correlates to the specific
// assistant replay_message entry it interrupted (both stamped from
// TranscriptEntry.TurnID). The frontend must consume this to mark that exact
// message interrupted, matching the live-cancel rendering
// (markLastMessageInterrupted), instead of silently discarding the frame.
describe('chat store — Fix 5c: turn_canceled replay correlation via turn_id', () => {
  it('turn_canceled replay frame with a matching turn_id marks the correct message interrupted', () => {
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message',
        role: 'user',
        content: 'do the thing',
        session_id: TEST_SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message',
        role: 'assistant',
        content: 'partial respo',
        session_id: TEST_SESSION_ID,
        turn_id: 'turn-abc',
      } as Parameters<ReturnType<typeof useChatStore.getState>['handleFrame']>[0])
    })
    const beforeCancel = useChatStore.getState().messages.find((m) => m.role === 'assistant')
    expect(beforeCancel?.status).toBe('done')

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message',
        role: 'turn_canceled',
        content: '',
        session_id: TEST_SESSION_ID,
        turn_id: 'turn-abc',
      } as Parameters<ReturnType<typeof useChatStore.getState>['handleFrame']>[0])
    })

    const state = useChatStore.getState()
    const assistantMsgs = state.messages.filter((m) => m.role === 'assistant')
    // The turn_canceled entry itself must never render as its own bubble.
    expect(assistantMsgs).toHaveLength(1)
    expect(assistantMsgs[0].content).toBe('partial respo')
    expect(assistantMsgs[0].status).toBe('interrupted')
    expect(assistantMsgs[0].isStreaming).toBe(false)
  })

  it('turn_canceled correctly targets the matching turn among multiple assistant turns (not just the last one)', () => {
    // Simulates async delegation interleaving: turn-1 finishes, turn-2 starts
    // and is still the "last" assistant message, but the cancellation belongs
    // to turn-1. Correlation must be by turn_id, never by stream adjacency.
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message', role: 'user', content: 'q1', session_id: TEST_SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message', role: 'assistant', content: 'turn 1 reply', session_id: TEST_SESSION_ID, turn_id: 'turn-1',
      } as Parameters<ReturnType<typeof useChatStore.getState>['handleFrame']>[0])
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message', role: 'user', content: 'q2', session_id: TEST_SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message', role: 'assistant', content: 'turn 2 reply', session_id: TEST_SESSION_ID, turn_id: 'turn-2',
      } as Parameters<ReturnType<typeof useChatStore.getState>['handleFrame']>[0])
    })

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message', role: 'turn_canceled', content: '', session_id: TEST_SESSION_ID, turn_id: 'turn-1',
      } as Parameters<ReturnType<typeof useChatStore.getState>['handleFrame']>[0])
    })

    const state = useChatStore.getState()
    const [turn1Msg, turn2Msg] = state.messages.filter((m) => m.role === 'assistant')
    expect(turn1Msg.content).toBe('turn 1 reply')
    expect(turn1Msg.status).toBe('interrupted')
    // turn 2 must be completely unaffected — no false-positive interruption.
    expect(turn2Msg.content).toBe('turn 2 reply')
    expect(turn2Msg.status).toBe('done')
  })

  it('turn_canceled frame with no matching message is handled gracefully — no crash, no false-positive interruption', () => {
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message', role: 'user', content: 'q1', session_id: TEST_SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message', role: 'assistant', content: 'unrelated reply', session_id: TEST_SESSION_ID, turn_id: 'turn-real',
      } as Parameters<ReturnType<typeof useChatStore.getState>['handleFrame']>[0])
    })

    expect(() => {
      act(() => {
        useChatStore.getState().handleFrame({
          type: 'replay_message', role: 'turn_canceled', content: '', session_id: TEST_SESSION_ID, turn_id: 'turn-nonexistent',
        } as Parameters<ReturnType<typeof useChatStore.getState>['handleFrame']>[0])
      })
    }).not.toThrow()

    const state = useChatStore.getState()
    const assistantMsgs = state.messages.filter((m) => m.role === 'assistant')
    expect(assistantMsgs).toHaveLength(1)
    // The unrelated message must NOT be mis-marked as interrupted.
    expect(assistantMsgs[0].status).toBe('done')
  })

  it('turn_canceled frame with no turn_id at all is dropped gracefully (legacy frame, nothing to correlate)', () => {
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message', role: 'assistant', content: 'some reply', session_id: TEST_SESSION_ID, turn_id: 'turn-x',
      } as Parameters<ReturnType<typeof useChatStore.getState>['handleFrame']>[0])
    })

    expect(() => {
      act(() => {
        useChatStore.getState().handleFrame({
          type: 'replay_message', role: 'turn_canceled', content: '', session_id: TEST_SESSION_ID,
          // turn_id omitted entirely.
        } as Parameters<ReturnType<typeof useChatStore.getState>['handleFrame']>[0])
      })
    }).not.toThrow()

    const msg = useChatStore.getState().messages.find((m) => m.role === 'assistant')
    expect(msg?.status).toBe('done')
  })

  it('findAssistantMessageIdByTurnId finds the assistant message by turnId, ignoring role and scanning backward', () => {
    const messagesById: Record<string, ChatMessage> = {
      u1: { id: 'u1', role: 'user', content: 'hi', timestamp: 't', status: 'done' },
      a1: { id: 'a1', role: 'assistant', content: 'r1', timestamp: 't', status: 'done', turnId: 'turn-1' },
      a2: { id: 'a2', role: 'assistant', content: 'r2', timestamp: 't', status: 'done', turnId: 'turn-2' },
    }
    const order = ['u1', 'a1', 'a2']
    expect(findAssistantMessageIdByTurnId(order, messagesById, 'turn-1')).toBe('a1')
    expect(findAssistantMessageIdByTurnId(order, messagesById, 'turn-2')).toBe('a2')
    expect(findAssistantMessageIdByTurnId(order, messagesById, 'turn-missing')).toBeNull()
  })
})

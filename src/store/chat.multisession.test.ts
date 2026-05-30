import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import { act } from 'react'
import { useChatStore, getMessages } from './chat'
import { useSessionStore } from './session'
import { queryClient } from '@/lib/queryClient'

// chat.multisession.test.ts — per-session sharding unit tests.
//
// Contract being pinned:
//   - Two session_started frames create two distinct buckets in sessionsById.
//   - Each bucket holds independent messages, toolCalls, isStreaming, tokens, cost.
//   - Token frames for session A land in A's bucket only; B's bucket is unchanged.
//   - setActiveSession(B) preserves A's bucket; foreground selectors show B.
//   - startNewSession() clears activeSessionId only; existing buckets are intact.
//   - done frame on session A while activeSessionId=B: A's isStreaming flips,
//     B's bucket and the foreground selectors are unaffected.
//   - session_started triggers queryClient.invalidateQueries for ['sessions'].
//   - Concurrent token streams for A and B end up in the right buckets.
//   - Frame missing session_id falls back to activeSessionId with console.warn.
//
// Traces to: quizzical-marinating-frog.md Step 9 — src/store/chat.multisession.test.ts

const SID_A = 'multisession-test-sid-a'
const SID_B = 'multisession-test-sid-b'

function resetStores() {
  act(() => {
    useChatStore.setState({
      sessionsById: {},
      messages: [],
      isStreaming: false,
      toolCalls: {},
      toolCallOrder: [],
      textAtToolCallStart: {},
      pendingApprovals: [],
      sessionTokens: 0,
      sessionCost: 0,
      isReplaying: false,
      replayCompletedForSession: null,
      rateLimitEvent: null,
      lastReceivedEventTime: null,
    })
    useSessionStore.setState({
      activeSessionId: null,
      activeAgentId: null,
      activeAgentType: null,
    })
  })
}

beforeEach(resetStores)
afterEach(() => {
  vi.restoreAllMocks()
})

// ---------------------------------------------------------------------------
// (a) Two session_started frames create two distinct buckets
// ---------------------------------------------------------------------------

describe('chat.multisession — (a) two session_started frames create two distinct buckets', () => {
  // Traces to: quizzical-marinating-frog.md Step 9 — scenario (a)
  it('each session_started frame registers an independent SessionChatState bucket', () => {
    // BDD: Given no active session and empty sessionsById
    // BDD: When two session_started frames arrive with different ids
    act(() => {
      useChatStore.getState().handleFrame({ type: 'session_started', session_id: SID_A })
    })
    act(() => {
      useChatStore.getState().handleFrame({ type: 'session_started', session_id: SID_B })
    })

    // BDD: Then sessionsById has two independent entries
    const state = useChatStore.getState()
    expect(Object.keys(state.sessionsById)).toContain(SID_A)
    expect(Object.keys(state.sessionsById)).toContain(SID_B)

    // Differentiation: the two buckets must be separate objects
    const bucketA = state.sessionsById[SID_A]
    const bucketB = state.sessionsById[SID_B]
    expect(bucketA).toBeDefined()
    expect(bucketB).toBeDefined()
    expect(bucketA).not.toBe(bucketB)

    // Each bucket has independent empty defaults.
    // FR-21 / T21-T25: session_started pre-sets isStreaming=true so the Stop
    // button appears immediately without waiting for the first token frame.
    expect(getMessages(bucketA)).toEqual([])
    expect(bucketA.isStreaming).toBe(true)
    expect(bucketA.sessionTokens).toBe(0)
    expect(bucketA.sessionCost).toBe(0)

    expect(getMessages(bucketB)).toEqual([])
    expect(bucketB.isStreaming).toBe(true)
    expect(bucketB.sessionTokens).toBe(0)
    expect(bucketB.sessionCost).toBe(0)
  })
})

// ---------------------------------------------------------------------------
// (b) Token frame for session A is appended to A's bucket only
// ---------------------------------------------------------------------------

describe('chat.multisession — (b) token frame routes to correct bucket only', () => {
  // Traces to: quizzical-marinating-frog.md Step 9 — scenario (b)
  it('token frame for session A does not appear in session B bucket', () => {
    // BDD: Given both sessions are registered
    act(() => {
      useChatStore.getState().handleFrame({ type: 'session_started', session_id: SID_A })
      useChatStore.getState().handleFrame({ type: 'session_started', session_id: SID_B })
      useSessionStore.setState({ activeSessionId: SID_A })
    })

    // BDD: When a token frame for session A arrives
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'token',
        session_id: SID_A,
        content: 'hello from A',
      })
    })

    // BDD: Then A's bucket has the token content
    const state = useChatStore.getState()
    const bucketA = state.sessionsById[SID_A]
    const msgsA = getMessages(bucketA)
    expect(msgsA.length).toBeGreaterThan(0)
    const lastMsgA = msgsA[msgsA.length - 1]
    expect(lastMsgA.content).toBe('hello from A')

    // BDD: And B's bucket is content-isolated — no token leaked across.
    // (B's isStreaming is true because session_started pre-set it for B; this
    // test asserts content isolation, not streaming-flag isolation.)
    const bucketB = state.sessionsById[SID_B]
    expect(getMessages(bucketB)).toHaveLength(0)
  })
})

// ---------------------------------------------------------------------------
// (c) setActiveSession(B) preserves A's bucket
// ---------------------------------------------------------------------------

describe('chat.multisession — (c) setActiveSession(B) leaves A bucket intact', () => {
  // Traces to: quizzical-marinating-frog.md Step 9 — scenario (c)
  it("switching to session B does not wipe session A's messages", () => {
    // BDD: Given session A is active and has a message
    act(() => {
      useChatStore.getState().handleFrame({ type: 'session_started', session_id: SID_A })
      useSessionStore.setState({ activeSessionId: SID_A })
      useChatStore.getState().handleFrame({
        type: 'token',
        session_id: SID_A,
        content: 'A-content-must-survive',
      })
    })

    // BDD: When setActiveSession switches to B
    act(() => {
      useSessionStore.getState().setActiveSession(SID_B)
    })

    // BDD: Then A's bucket is still intact
    const state = useChatStore.getState()
    const bucketA = state.sessionsById[SID_A]
    expect(bucketA).toBeDefined()
    const msgsA = getMessages(bucketA)
    expect(msgsA.length).toBeGreaterThan(0)
    const msgA = msgsA.find((m) => m.content === 'A-content-must-survive')
    expect(msgA).toBeDefined()

    // Foreground selectors now point to B (empty)
    expect(useSessionStore.getState().activeSessionId).toBe(SID_B)
    expect(state.messages).toHaveLength(0)
  })
})

// ---------------------------------------------------------------------------
// (d) startNewSession() clears activeSessionId only; existing buckets intact
// ---------------------------------------------------------------------------

describe('chat.multisession — (d) startNewSession clears activeSessionId only', () => {
  // Traces to: quizzical-marinating-frog.md Step 9 — scenario (d)
  it('startNewSession sets activeSessionId to null; preserves existing buckets', () => {
    // BDD: Given session A is active and has a message
    act(() => {
      useChatStore.getState().handleFrame({ type: 'session_started', session_id: SID_A })
      useSessionStore.setState({ activeSessionId: SID_A })
      useChatStore.getState().handleFrame({
        type: 'token',
        session_id: SID_A,
        content: 'A-preserved-on-new-session',
      })
    })

    // BDD: When startNewSession is called
    act(() => {
      useSessionStore.getState().startNewSession()
    })

    // BDD: Then activeSessionId is null
    expect(useSessionStore.getState().activeSessionId).toBeNull()

    // BDD: And A's bucket is untouched (not wiped)
    const state = useChatStore.getState()
    const bucketA = state.sessionsById[SID_A]
    expect(bucketA).toBeDefined()
    const msgsA2 = getMessages(bucketA)
    expect(msgsA2.length).toBeGreaterThan(0)
    const msgA = msgsA2.find((m) => m.content === 'A-preserved-on-new-session')
    expect(msgA).toBeDefined()

    // Foreground selectors show empty defaults (no active session)
    expect(state.messages).toHaveLength(0)
    expect(state.isStreaming).toBe(false)
  })
})

// ---------------------------------------------------------------------------
// (e) done frame on background session flips its isStreaming; foreground unchanged
// ---------------------------------------------------------------------------

describe('chat.multisession — (e) done frame on background session A while active=B', () => {
  // Traces to: quizzical-marinating-frog.md Step 9 — scenario (e)
  it("done frame for A flips A's isStreaming; B's bucket and foreground are unaffected", () => {
    // BDD: Given both sessions are registered and A is streaming
    act(() => {
      useChatStore.getState().handleFrame({ type: 'session_started', session_id: SID_A })
      useChatStore.getState().handleFrame({ type: 'session_started', session_id: SID_B })
      useSessionStore.setState({ activeSessionId: SID_A })
      // Make A appear streaming by adding a streaming message
      useChatStore.getState().handleFrame({
        type: 'token',
        session_id: SID_A,
        content: 'background streaming content',
      })
    })

    // Verify A is streaming
    expect(useChatStore.getState().sessionsById[SID_A].isStreaming).toBe(true)

    // BDD: When the user switches to B and A receives a done frame
    act(() => {
      useSessionStore.getState().setActiveSession(SID_B)
      useChatStore.getState().handleFrame({
        type: 'token',
        session_id: SID_B,
        content: 'B foreground token',
      })
    })

    // A completes in the background
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'done',
        session_id: SID_A,
        stats: { tokens: 50, cost: 0.001, duration_ms: 100 },
      })
    })

    const state = useChatStore.getState()

    // BDD: Then A's bucket isStreaming is now false
    const bucketA = state.sessionsById[SID_A]
    expect(bucketA.isStreaming).toBe(false)
    expect(bucketA.sessionTokens).toBe(50)

    // BDD: And B's bucket is unchanged (still streaming, different content)
    const bucketB = state.sessionsById[SID_B]
    expect(bucketB.isStreaming).toBe(true)
    const msgsB = getMessages(bucketB)
    const lastBMsg = msgsB[msgsB.length - 1]
    expect(lastBMsg.content).toBe('B foreground token')

    // BDD: And foreground selectors still show B (activeSessionId=B)
    expect(useSessionStore.getState().activeSessionId).toBe(SID_B)
    expect(state.isStreaming).toBe(true)
  })
})

// ---------------------------------------------------------------------------
// (f) session_started triggers queryClient.invalidateQueries(['sessions'])
// ---------------------------------------------------------------------------

describe('chat.multisession — (f) session_started invalidates sessions cache', () => {
  // Traces to: quizzical-marinating-frog.md Step 9 — scenario (f)
  it('handleFrame(session_started) calls queryClient.invalidateQueries for sessions key', () => {
    // BDD: Given a spy on queryClient.invalidateQueries
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    // BDD: When a session_started frame arrives
    act(() => {
      useChatStore.getState().handleFrame({ type: 'session_started', session_id: SID_A })
    })

    // BDD: Then invalidateQueries was called with the ['sessions'] query key
    expect(invalidateSpy).toHaveBeenCalledWith(
      expect.objectContaining({ queryKey: ['sessions'] })
    )
  })

  it('second session_started for a different id also invalidates the sessions cache', () => {
    // Differentiation: must fire for every new session, not just the first
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    act(() => {
      useChatStore.getState().handleFrame({ type: 'session_started', session_id: SID_A })
      useChatStore.getState().handleFrame({ type: 'session_started', session_id: SID_B })
    })

    // Must have been called at least twice (once per session_started)
    expect(invalidateSpy.mock.calls.length).toBeGreaterThanOrEqual(2)
  })
})

// ---------------------------------------------------------------------------
// (g) Concurrent token streams populate both buckets correctly
// ---------------------------------------------------------------------------

describe('chat.multisession — (g) concurrent token streams for A and B', () => {
  // Traces to: quizzical-marinating-frog.md Step 9 — scenario (g)
  it('interleaved token frames for A and B end up in the correct buckets', () => {
    // BDD: Given both sessions exist
    act(() => {
      useChatStore.getState().handleFrame({ type: 'session_started', session_id: SID_A })
      useChatStore.getState().handleFrame({ type: 'session_started', session_id: SID_B })
      useSessionStore.setState({ activeSessionId: SID_A })
    })

    // BDD: When token frames arrive interleaved
    act(() => {
      useChatStore.getState().handleFrame({ type: 'token', session_id: SID_A, content: 'A1' })
      useChatStore.getState().handleFrame({ type: 'token', session_id: SID_B, content: 'B1' })
      useChatStore.getState().handleFrame({ type: 'token', session_id: SID_A, content: 'A2' })
      useChatStore.getState().handleFrame({ type: 'token', session_id: SID_B, content: 'B2' })
    })

    // BDD: Then each bucket holds only its own content
    const state = useChatStore.getState()
    const bucketA = state.sessionsById[SID_A]
    const bucketB = state.sessionsById[SID_B]
    const msgsAi = getMessages(bucketA)
    const msgsBi = getMessages(bucketB)

    const lastA = msgsAi[msgsAi.length - 1]
    const lastB = msgsBi[msgsBi.length - 1]

    // A's message content is A1+A2 concatenated, not B's tokens
    expect(lastA.content).toContain('A1')
    expect(lastA.content).toContain('A2')
    expect(lastA.content).not.toContain('B1')
    expect(lastA.content).not.toContain('B2')

    // B's message content is B1+B2 concatenated, not A's tokens
    expect(lastB.content).toContain('B1')
    expect(lastB.content).toContain('B2')
    expect(lastB.content).not.toContain('A1')
    expect(lastB.content).not.toContain('A2')
  })
})

// ---------------------------------------------------------------------------
// (h) Frame missing session_id falls back to activeSessionId with console.warn
// ---------------------------------------------------------------------------

describe('chat.multisession — (h) frame missing session_id falls back with console.warn', () => {
  // Traces to: quizzical-marinating-frog.md Step 9 — scenario (h)
  it('token frame without session_id routes to activeSessionId and emits console.warn', () => {
    // BDD: Given session A is active
    act(() => {
      useChatStore.getState().handleFrame({ type: 'session_started', session_id: SID_A })
      useSessionStore.setState({ activeSessionId: SID_A })
    })

    const warnSpy = vi.spyOn(console, 'warn')

    // BDD: When a token frame arrives with no session_id
    act(() => {
      useChatStore.getState().handleFrame({ type: 'token', content: 'fallback-token' } as any)
    })

    // BDD: Then a console.warn was emitted
    const warnCalls = warnSpy.mock.calls
    const hasWarn = warnCalls.some(
      (args) =>
        typeof args[0] === 'string' &&
        (args[0].includes('session_id') || args[0].includes('missing'))
    )
    expect(hasWarn).toBe(true)

    // BDD: And the token was routed to the active session's bucket (not lost)
    const state = useChatStore.getState()
    const bucketA = state.sessionsById[SID_A]
    // The bucket should have received the token (routed to fallback SID_A)
    const hasToken = bucketA ? getMessages(bucketA).some((m) => m.content.includes('fallback-token')) : false
    expect(hasToken).toBe(true)
  })
})

// ---------------------------------------------------------------------------
// (i) session_started binds when activeSessionId is null AND optimistic msg exists
// ---------------------------------------------------------------------------

describe('chat.multisession — (i) session_started binds when activeSessionId is null', () => {
  // F-S10 test (j): session_started correctly binds when the SPA had no active session
  // and an optimistic message was written to the __default bucket before the ack arrived.
  it('session_started creates a bucket and sets activeSessionId when prev was null', () => {
    // BDD: Given no active session and an optimistic message in the default bucket
    act(() => {
      // Simulate an optimistic append before server ack (message sent without session_id)
      useSessionStore.setState({ activeSessionId: null })
      useChatStore.getState().appendMessage({
        id: 'opt_1',
        role: 'user',
        content: 'optimistic message',
        timestamp: new Date().toISOString(),
        status: 'done',
      })
    })

    // BDD: When the server acks with session_started
    act(() => {
      useChatStore.getState().handleFrame({ type: 'session_started', session_id: SID_A })
    })

    // BDD: Then activeSessionId is set to the minted id
    expect(useSessionStore.getState().activeSessionId).toBe(SID_A)

    // BDD: And sessionsById has a bucket for SID_A
    const state = useChatStore.getState()
    expect(state.sessionsById[SID_A]).toBeDefined()
  })
})

// ---------------------------------------------------------------------------
// (k) untagged session-scoped frame in production drops and sets connection error
// ---------------------------------------------------------------------------

describe('chat.multisession — (k) untagged session-scoped frame in production mode', () => {
  // F-S10 test (k): in production, a session-scoped frame missing session_id is dropped
  // and a connection error toast is surfaced.
  it('drops the frame and calls setConnectionError in production mode', async () => {
    const { useConnectionStore } = await import('@/store/connection')

    // Stub MODE to 'production' for this test
    const origMode = import.meta.env.MODE
    vi.stubEnv('MODE', 'production')

    act(() => {
      useSessionStore.setState({ activeSessionId: SID_A })
      useChatStore.getState().handleFrame({ type: 'session_started', session_id: SID_A })
    })

    const errorSpy = vi.spyOn(console, 'error')

    act(() => {
      // This frame is session-scoped but missing session_id — should be dropped in production
      useChatStore.getState().handleFrame({ type: 'token', content: 'should be dropped' } as any)
    })

    // BDD: Then console.error was called
    const errorCalls = errorSpy.mock.calls
    const hasError = errorCalls.some(
      (args) => typeof args[0] === 'string' && args[0].includes('session_id')
    )
    expect(hasError).toBe(true)

    // BDD: And a connection error was set
    const connError = useConnectionStore.getState().connectionError
    expect(connError).toBeTruthy()
    expect(connError).toContain('session_id')

    // BDD: And the token was NOT routed to SID_A's bucket (frame dropped)
    const state = useChatStore.getState()
    const bucketA = state.sessionsById[SID_A]
    const hasToken = bucketA ? getMessages(bucketA).some((m) => m.content.includes('should be dropped')) : false
    expect(hasToken).toBeFalsy()

    // Restore MODE
    vi.stubEnv('MODE', origMode)
  })
})

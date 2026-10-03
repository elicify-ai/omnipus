/**
 * chat.error-terminal.test.ts — an error bubble is TERMINAL.
 *
 * Nothing in this codebase asserted what happens when a live `error` frame is
 * followed by a `done` frame for the same session, even though that is the
 * ordinary — in fact the only — ordering a failed turn produces. The frame
 * sequence below is not hypothetical: it was captured off a real WebSocket in
 * pkg/gateway/websocket_provider_refusal_test.go, driving a real agent turn
 * whose provider refused with HTTP 429.
 *
 *   1. error  {session_id, message, payload:{llm_error:{code, message, retryable, detail}}}
 *   2. done   {session_id, stats:{turn_failed:true, ...}}     ← streamer finalize
 *   3. token  {session_id, content:<the same sentence>}       ← outbound fallback
 *   4. done   {session_id}                                    ← outbound fallback
 *
 * Frames 2 and 3–4 come from two independent server paths that both fire on a
 * failed turn, so the error frame is always followed by at least one `done` and
 * usually by a duplicate `token`.
 *
 * Two defects lived in that sequence, both fixed in the reducer:
 *
 *  - Frame 2 demoted `status:'error'` to `'done'`. The error bubble IS the last
 *    assistant message, so the finalize sweep's `continue` did not protect it.
 *    `errorCode` survived while `status` did not, leaving a state no renderer
 *    can act on (every error affordance gates on `status === 'error'`), so the
 *    "Error" label, the Retry button, and the verbose technical-details
 *    disclosure all vanished the instant the turn ended.
 *  - Frame 3 crossed the closed-bubble segment boundary and minted a SECOND
 *    bubble containing a duplicate of the sentence already on screen — the
 *    duplicate carrying none of the error treatment.
 *
 * Both are now handled the same way the pre-existing `'interrupted'` rules
 * handle a cancelled turn: a terminal status is not overwritten, and trailing
 * tokens after a terminal status are discarded.
 */

import { describe, it, expect, beforeEach } from 'vitest'
import { act } from 'react'
import { useChatStore, getMessages, makeBucketMessages, type ChatMessage } from './chat'
import { useSessionStore } from './session'
import { useConnectionStore } from './connection'
import { useChatPreferencesStore } from './chatPreferences'
import { codeToDisplay } from '@/lib/llm-error'

const SID = 'error-terminal-test-session'

/** The user-facing copy a rate-limit refusal renders, from the generated catalogue. */
const RATE_LIMIT_COPY = codeToDisplay.rate_limited

function resetStores(): void {
  act(() => {
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
    })
    useSessionStore.setState({ activeSessionId: SID, activeAgentId: 'mia', activeAgentType: null })
    useConnectionStore.setState({ connection: null, isConnected: false, connectionError: null })
    useChatPreferencesStore.setState({ verboseChatEnabled: false })
  })
}

beforeEach(resetStores)

/**
 * Seed a bucket at SID, optionally containing the optimistic streaming
 * assistant placeholder that sendMessage creates before the server replies.
 * Production always has one by the time an error frame lands, so the
 * "streaming placeholder" variant is the faithful case; the empty variant
 * covers a bubble minted by the error frame itself.
 */
function seedBucket(seedMessages: ChatMessage[] = []): void {
  act(() => {
    useChatStore.setState(() => ({
      sessionsById: {
        [SID]: {
          ...makeBucketMessages(seedMessages),
          toolCalls: {},
          toolCallOrder: [],
          textAtToolCallStart: {},
          isStreaming: seedMessages.some((m) => m.isStreaming),
          isReplaying: false,
          replayCompletedForSession: null,
          sessionTokens: 0,
          sessionCost: 0,
          rateLimitEvent: null,
          cancelStage: null,
          lastUserMessageAt: null,
          lastReceivedEventTime: null,
        },
      },
      messages: seedMessages,
      toolCalls: {},
      toolCallOrder: [],
      isStreaming: seedMessages.some((m) => m.isStreaming),
      isReplaying: false,
    }))
  })
}

function streamingPlaceholder(): ChatMessage {
  return {
    id: 'optimistic-assistant-placeholder',
    role: 'assistant',
    content: '',
    timestamp: new Date().toISOString(),
    status: 'streaming',
    isStreaming: true,
    agentId: 'mia',
  } as ChatMessage
}

function bucketMessages(): ChatMessage[] {
  const b = useChatStore.getState().sessionsById[SID]
  return b ? getMessages(b) : []
}

/** Feed the exact captured server sequence for a provider-refused turn. */
function playRefusedTurnFrames(opts: { includeOutboundDuplicate: boolean }): void {
  const store = useChatStore.getState()
  act(() => {
    store.handleFrame({
      type: 'error',
      session_id: SID,
      message: RATE_LIMIT_COPY,
      payload: {
        llm_error: {
          code: 'rate_limited',
          message: RATE_LIMIT_COPY,
          retryable: true,
          detail: 'status=429 body={"error":{"type":"rate_limit_error"}}',
        },
      },
    })
  })
  act(() => {
    store.handleFrame({
      type: 'done',
      session_id: SID,
      stats: { tokens: 0, cost: 0, duration_ms: 442, turn_failed: true },
    })
  })
  if (!opts.includeOutboundDuplicate) return
  act(() => {
    store.handleFrame({ type: 'token', session_id: SID, content: RATE_LIMIT_COPY })
  })
  act(() => {
    store.handleFrame({ type: 'done', session_id: SID })
  })
}

describe('an error bubble is terminal across the frames a failed turn really sends', () => {
  it('keeps status "error" when the streamer\'s done frame follows (production ordering)', () => {
    seedBucket([streamingPlaceholder()])
    playRefusedTurnFrames({ includeOutboundDuplicate: false })

    const msgs = bucketMessages()
    expect(msgs).toHaveLength(1)
    const bubble = msgs[0]

    expect(bubble.status).toBe('error')
    expect(bubble.errorCode).toBe('rate_limited')
    expect(bubble.content).toBe(RATE_LIMIT_COPY)
    // isStreaming must still clear — the turn IS over. Only the *status*
    // must survive; leaving isStreaming true would shimmer forever.
    expect(bubble.isStreaming).toBe(false)
    // The bucket-level spinner must clear too, or the composer stays disabled.
    expect(useChatStore.getState().sessionsById[SID]?.isStreaming).toBe(false)
  })

  it('never leaves the contradictory "status done + errorCode set" state', () => {
    seedBucket([streamingPlaceholder()])
    playRefusedTurnFrames({ includeOutboundDuplicate: false })

    const bubble = bucketMessages()[0]
    // Every error affordance in the renderers gates on status === 'error'
    // (label, Retry, verbose technical-details). A bubble carrying errorCode
    // while claiming status 'done' is unreachable by all of them — the error
    // is recorded and simultaneously invisible.
    expect(bubble.errorCode).toBeDefined()
    expect(bubble.status).not.toBe('done')
  })

  it('renders the failure exactly ONCE — the outbound duplicate is discarded', () => {
    seedBucket([streamingPlaceholder()])
    playRefusedTurnFrames({ includeOutboundDuplicate: true })

    const msgs = bucketMessages()
    expect(msgs).toHaveLength(1)
    expect(msgs[0].status).toBe('error')
    expect(msgs[0].content).toBe(RATE_LIMIT_COPY)
    // Specifically: the sentence must not appear twice in the thread.
    const occurrences = msgs.filter((m) => m.content === RATE_LIMIT_COPY).length
    expect(occurrences).toBe(1)
  })

  it('mints a single error bubble when no placeholder exists yet', () => {
    seedBucket([])
    playRefusedTurnFrames({ includeOutboundDuplicate: true })

    const msgs = bucketMessages()
    expect(msgs).toHaveLength(1)
    expect(msgs[0].role).toBe('assistant')
    expect(msgs[0].status).toBe('error')
    expect(msgs[0].content).toBe(RATE_LIMIT_COPY)
  })

  it('keeps errorDetail reachable when verbose is on, through the done frame', () => {
    useChatPreferencesStore.setState({ verboseChatEnabled: true })
    seedBucket([streamingPlaceholder()])
    playRefusedTurnFrames({ includeOutboundDuplicate: true })

    const bubble = bucketMessages()[0]
    expect(bubble.status).toBe('error')
    // The disclosure renders only when status==='error' AND errorCode AND
    // errorDetail are all present; a demoted status made detail unreachable
    // even though it was still stored.
    expect(bubble.errorCode).toBe('rate_limited')
    expect(bubble.errorDetail).toContain('status=429')
  })
})

describe('the terminal-status rules do not disturb the paths they sit beside', () => {
  it('a successful turn still finalizes as "done"', () => {
    seedBucket([streamingPlaceholder()])
    const store = useChatStore.getState()
    act(() => {
      store.handleFrame({ type: 'token', session_id: SID, content: 'Here is the answer.' })
    })
    act(() => {
      store.handleFrame({
        type: 'done',
        session_id: SID,
        stats: { tokens: 12, cost: 0.0001, duration_ms: 300 },
      })
    })

    const msgs = bucketMessages()
    expect(msgs).toHaveLength(1)
    expect(msgs[0].status).toBe('done')
    expect(msgs[0].content).toBe('Here is the answer.')
    expect(msgs[0].errorCode).toBeUndefined()
  })

  it('a cancelled turn still finalizes as "interrupted" (pre-existing FR-21 rule)', () => {
    seedBucket([
      {
        ...streamingPlaceholder(),
        content: 'partial answer',
        status: 'interrupted',
        isStreaming: false,
      } as ChatMessage,
    ])
    const store = useChatStore.getState()
    act(() => {
      store.handleFrame({ type: 'token', session_id: SID, content: 'trailing token after cancel' })
    })
    act(() => {
      store.handleFrame({ type: 'done', session_id: SID, stats: { tokens: 0, cost: 0 } })
    })

    const msgs = bucketMessages()
    expect(msgs).toHaveLength(1)
    expect(msgs[0].status).toBe('interrupted')
    expect(msgs[0].content).toBe('partial answer')
  })

  it('a NEW turn after an errored one still opens its own bubble', () => {
    seedBucket([streamingPlaceholder()])
    playRefusedTurnFrames({ includeOutboundDuplicate: true })
    expect(bucketMessages()).toHaveLength(1)

    // The user retries. sendMessage's optimistic placeholder is what re-opens
    // the thread — the token guard must not wedge the session permanently in
    // "error" so that a later, successful turn can never render.
    act(() => {
      useChatStore.setState((s) => {
        const b = s.sessionsById[SID]!
        const next = [...getMessages(b), streamingPlaceholder2()]
        return {
          sessionsById: { ...s.sessionsById, [SID]: { ...b, ...makeBucketMessages(next) } },
        }
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'token',
        session_id: SID,
        content: 'The retry worked.',
      })
    })

    const msgs = bucketMessages()
    expect(msgs).toHaveLength(2)
    expect(msgs[0].status).toBe('error')
    expect(msgs[1].content).toBe('The retry worked.')
  })
})

function streamingPlaceholder2(): ChatMessage {
  return { ...streamingPlaceholder(), id: 'optimistic-assistant-placeholder-2' }
}

describe('an internal rate_limit frame is a visible denial, not a successful close', () => {
  it('keeps rateLimitEvent set when done follows — removing case rate_limit cannot stay green', () => {
    seedBucket([streamingPlaceholder()])
    const store = useChatStore.getState()
    act(() => {
      store.handleFrame({
        type: 'rate_limit',
        session_id: SID,
        scope: 'agent',
        resource: 'turns',
        policy_rule: 'sec26',
        retry_after_seconds: 60,
        agent_id: 'mia',
      })
    })
    act(() => {
      store.handleFrame({
        type: 'done',
        session_id: SID,
        stats: { tokens: 0, cost: 0, duration_ms: 12 },
      })
    })

    const event = useChatStore.getState().sessionsById[SID]?.rateLimitEvent
    expect(event).not.toBeNull()
    expect(event?.scope).toBe('agent')
    expect(event?.resource).toBe('turns')
    expect(event?.retryAfterSeconds).toBe(60)
    // Banner-only path: no error bubble. Provider 429 is a separate error frame.
    const msgs = bucketMessages()
    expect(msgs.every((m) => m.status !== 'error')).toBe(true)
  })
})

describe('a live error on a second tab does not rewrite the last healthy reply', () => {
  function healthyReply(): ChatMessage {
    return {
      id: 'prior-healthy-reply',
      role: 'assistant',
      content: 'Yesterday this worked.',
      timestamp: new Date().toISOString(),
      status: 'done',
      isStreaming: false,
      agentId: 'mia',
    } as ChatMessage
  }

  it('pushes a new error bubble instead of flipping the last successful reply', () => {
    seedBucket([healthyReply()])
    playRefusedTurnFrames({ includeOutboundDuplicate: false })

    const msgs = bucketMessages()
    expect(msgs).toHaveLength(2)
    expect(msgs[0].id).toBe('prior-healthy-reply')
    expect(msgs[0].status).toBe('done')
    expect(msgs[0].content).toBe('Yesterday this worked.')
    expect(msgs[0].errorCode).toBeUndefined()
    expect(msgs[1].status).toBe('error')
    expect(msgs[1].errorCode).toBe('rate_limited')
    expect(msgs[1].content).toBe(RATE_LIMIT_COPY)
  })

  it('does not mint a second bubble when replay already drew the same error', () => {
    seedBucket([
      {
        id: 'replayed-error',
        role: 'assistant',
        content: RATE_LIMIT_COPY,
        timestamp: new Date().toISOString(),
        status: 'error',
        isStreaming: false,
        errorCode: 'rate_limited',
        agentId: 'mia',
      } as ChatMessage,
    ])
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'error',
        session_id: SID,
        message: RATE_LIMIT_COPY,
        payload: {
          llm_error: {
            code: 'rate_limited',
            message: RATE_LIMIT_COPY,
            retryable: true,
          },
        },
      })
    })

    const msgs = bucketMessages()
    expect(msgs).toHaveLength(1)
    expect(msgs[0].id).toBe('replayed-error')
    expect(msgs[0].status).toBe('error')
    expect(msgs[0].errorCode).toBe('rate_limited')
  })
})

// #1081 RC2: the terminal sentence must replace already-streamed narration.
// Oracle: contracts/asyncapi.yaml, LLMError.x-user-messages.turn_timed_out,
// read from the generated catalogue (codeToDisplay) instead of pasted by hand
// — same pattern as RATE_LIMIT_COPY above. This test's load-bearing
// assertions are about reducer behavior (bubble identity, narration
// replacement, terminal status, the verbose-detail gate), not about whether
// codeToMessage('turn_timed_out') itself returns the right sentence.
// This is a real-store reproduction, NOT a live-browser/delivery capture.
// GREEN and implementation mutation checks are deferred to independent CHECK.
describe('#1081 RC2 — narration cannot hide a terminal error', () => {
  const CONTEXT_ERROR_COPY = codeToDisplay.turn_timed_out
  const NARRATION = 'Let me check the task list before continuing.'
  const DETAIL = 'un-emptiable tool-result residue exceeds the absolute tool-result-share bound'

  it.each([
    { verbose: false, afterDone: false },
    { verbose: true, afterDone: false },
    { verbose: false, afterDone: true },
    { verbose: true, afterDone: true },
  ])('replaces same-bubble narration with the catalogued error (verbose=$verbose, afterDone=$afterDone)', ({ verbose, afterDone }) => {
    const placeholder = streamingPlaceholder()
    const turnId = `rc2-context-turn-${verbose}-${afterDone}`
    seedBucket([placeholder])
    useChatPreferencesStore.setState({ verboseChatEnabled: verbose })

    // Narration must come through the production token reducer, not a seed.
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'token',
        session_id: SID,
        message_id: placeholder.id,
        turn_id: turnId,
        agent_id: 'mia',
        content: NARRATION,
      })
    })
    const narrated = bucketMessages()
    expect(narrated, 'the narration token must populate exactly one bubble').toHaveLength(1)
    expect(narrated[0].id).toBe(placeholder.id)
    expect(narrated[0].turnId).toBe(turnId)
    expect(narrated[0].content).toBe(NARRATION)
    expect(narrated[0].status).toBe('streaming')
    expect(narrated[0].isStreaming).toBe(true)
    expect(useChatStore.getState().messages[0].content).toBe(NARRATION)

    act(() => {
      // ErrorFrame is session-scoped: its contract has no message_id/turn_id.
      // The same-session terminal error must resolve this existing bubble.
      useChatStore.getState().handleFrame({
        type: 'error',
        session_id: SID,
        message: CONTEXT_ERROR_COPY,
        payload: {
          llm_error: {
            code: 'turn_timed_out',
            message: CONTEXT_ERROR_COPY,
            retryable: false,
            detail: DETAIL,
          },
        },
      })
    })
    if (afterDone) {
      act(() => {
        useChatStore.getState().handleFrame({
          type: 'done',
          session_id: SID,
          message_id: placeholder.id,
          turn_id: turnId,
          stats: { turn_failed: true },
        })
      })
    }

    const terminal = bucketMessages()
    expect(terminal, 'the error must resolve the same bubble, not duplicate it').toHaveLength(1)
    const bubble = terminal[0]
    expect(bubble.id).toBe(placeholder.id)
    expect(bubble.turnId).toBe(turnId)
    expect(bubble.role).toBe('assistant')
    expect(bubble.status).toBe('error')
    expect(bubble.errorCode).toBe('turn_timed_out')
    expect(bubble.isStreaming).toBe(false)
    expect(useChatStore.getState().sessionsById[SID]?.isStreaming).toBe(false)
    expect(useChatStore.getState().isStreaming).toBe(false)
    expect(bubble.errorDetail).toBe(verbose ? DETAIL : undefined)
    // Stamping status/errorCode alone is not enough: the default-visible text
    // must say that the turn failed, even when Verbose chat is disabled.
    expect(bubble.content, '#1081 RC2: terminal copy must replace stale narration').toBe(CONTEXT_ERROR_COPY)
    expect(useChatStore.getState().messages).toHaveLength(1)
    expect(useChatStore.getState().messages[0].content, 'the foreground must surface the terminal copy').toBe(CONTEXT_ERROR_COPY)
  })
})

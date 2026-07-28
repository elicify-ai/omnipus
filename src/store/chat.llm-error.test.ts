/**
 * chat.llm-error.test.ts — ADR-051 store-reducer tests for the typed
 * LLM-error translation path on the live `case 'error'` and the replay
 * `case 'replay_error'` branches.
 *
 * Coverage:
 *  - live 'error' with typed payload renders the translated code→display copy
 *    and stamps errorCode/errorDetail/errorEntryId on the bubble.
 *  - verbose-off frames still render the translated message but omit detail.
 *  - entry-id dedup: a live error then a replay_error with the same id
 *    produces ONE bubble (no duplication on reload).
 *  - legacy 'error' frames (no payload.llm_error) still render frame.message.
 *  - kickoff-reject and cancel-ack sub-paths are untouched.
 */

import { describe, it, expect, beforeEach, vi } from 'vitest'
import { act } from 'react'
import { useChatStore, getMessages, makeBucketMessages, type ChatMessage } from './chat'
import { useSessionStore } from './session'
import { useConnectionStore } from './connection'
import { useChatPreferencesStore } from './chatPreferences'
import { useUiStore } from './ui'
import { codeToDisplay } from '@/lib/llm-error'
import type { WsReceiveFrame } from '@/lib/ws'

/**
 * Live ErrorFrame with an optional `entry_id` (read defensively by
 * readEntryIdFromFrame for live→replay dedup). The canonical AsyncAPI
 * ErrorFrame schema does not include entry_id today, so this helper
 * constructs the frame and casts it to WsReceiveFrame for handleFrame.
 */
function liveErrorFrame(fields: {
  sessionId?: string
  message: string
  entryId?: string
  llmError?: { code: string; message: string; retryable: boolean; detail?: string }
}): WsReceiveFrame {
  return {
    type: 'error',
    ...(fields.sessionId === undefined ? {} : { session_id: fields.sessionId }),
    message: fields.message,
    ...(fields.entryId === undefined ? {} : { entry_id: fields.entryId }),
    ...(fields.llmError === undefined
      ? {}
      : { payload: { llm_error: fields.llmError } }),
  } as unknown as WsReceiveFrame
}

const SID = 'llm-error-test-session'

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
    useSessionStore.setState({
      activeSessionId: SID,
      activeAgentId: null,
      activeAgentType: null,
    })
    useConnectionStore.setState({
      connection: null,
      isConnected: false,
      connectionError: null,
    })
    // Verbose preference is per-device persisted; ensure deterministic start.
    useChatPreferencesStore.setState({ verboseChatEnabled: false })
  })
}

beforeEach(resetStores)

/** Seed an empty bucket at SID so handleFrame has somewhere to route to. */
function seedEmptyBucket(opts: { isReplaying?: boolean; isStreaming?: boolean } = {}): void {
  act(() => {
    useChatStore.setState((_s) => ({
      sessionsById: {
        [SID]: {
          ...makeBucketMessages([]),
          toolCalls: {},
          toolCallOrder: [],
          textAtToolCallStart: {},
          isStreaming: opts.isStreaming ?? false,
          isReplaying: opts.isReplaying ?? false,
          replayCompletedForSession: null,
          sessionTokens: 0,
          sessionCost: 0,
          rateLimitEvent: null,
          cancelStage: null,
          lastUserMessageAt: null,
          lastReceivedEventTime: null,
          spanByParentCallId: {},
        },
      },
      messages: [],
      toolCalls: {},
      toolCallOrder: [],
      isStreaming: opts.isStreaming ?? false,
      isReplaying: opts.isReplaying ?? false,
    }))
  })
}

function bucketMessages(): ChatMessage[] {
  const b = useChatStore.getState().sessionsById[SID]
  return b ? getMessages(b) : []
}

// ---------------------------------------------------------------------------
// LIVE case 'error' — typed payload translation
// ---------------------------------------------------------------------------

describe("ADR-051 live 'error' — typed payload translation", () => {
  it('renders the translated code→display copy (NOT the raw wire message) on a fresh error bubble', () => {
    seedEmptyBucket()
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'error',
        session_id: SID,
        message: 'raw provider internals: TCP RST on 10.0.0.1',
        payload: {
          llm_error: {
            code: 'network',
            message: 'raw provider internals: TCP RST on 10.0.0.1',
            retryable: true,
          },
        },
      })
    })

    const msgs = bucketMessages()
    expect(msgs).toHaveLength(1)
    const errBubble = msgs[0]
    expect(errBubble.role).toBe('assistant')
    expect(errBubble.status).toBe('error')
    // Visible content is the translated copy — NOT the raw wire message.
    expect(errBubble.content).toBe(codeToDisplay.network)
    // Typed fields are stamped for the disclosure.
    expect(errBubble.errorCode).toBe('network')
    // No entry_id on this frame.
    expect(errBubble.errorEntryId).toBeUndefined()
    // No detail on the wire payload → no detail on the bubble.
    expect(errBubble.errorDetail).toBeUndefined()
  })

  it('stamps errorDetail when verbose is on AND payload carries detail', () => {
    useChatPreferencesStore.setState({ verboseChatEnabled: true })
    seedEmptyBucket()
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'error',
        session_id: SID,
        message: 'm',
        payload: {
          llm_error: {
            code: 'provider_rejected',
            message: 'm',
            retryable: false,
            detail: 'provider returned 400: bad_request',
          },
        },
      })
    })

    const m = bucketMessages()[0]
    expect(m.errorCode).toBe('provider_rejected')
    expect(m.errorDetail).toBe('provider returned 400: bad_request')
  })

  it('omits errorDetail when verbose is off, even if payload carries detail', () => {
    useChatPreferencesStore.setState({ verboseChatEnabled: false })
    seedEmptyBucket()
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'error',
        session_id: SID,
        message: 'm',
        payload: {
          llm_error: {
            code: 'provider_rejected',
            message: 'm',
            retryable: false,
            detail: 'should not be surfaced',
          },
        },
      })
    })

    const m = bucketMessages()[0]
    expect(m.errorCode).toBe('provider_rejected')
    expect(m.errorDetail).toBeUndefined()
  })

  it('stamps errorEntryId when the frame carries entry_id', () => {
    seedEmptyBucket()
    act(() => {
      // entry_id is read via safe cast — not in the live ErrorFrame contract
      // today, but the readEntryIdFromFrame accessor tolerates its presence
      // defensively (forward-compat for when the backend stamps it on the
      // live path too). The liveErrorFrame helper casts the untyped field.
      useChatStore.getState().handleFrame(
        liveErrorFrame({
          sessionId: SID,
          message: 'm',
          entryId: 'entry-42',
          llmError: { code: 'unknown', message: 'm', retryable: false },
        }),
      )
    })
    expect(bucketMessages()[0].errorEntryId).toBe('entry-42')
  })
})

// ---------------------------------------------------------------------------
// LIVE case 'error' — legacy frame fallback
// ---------------------------------------------------------------------------

describe("ADR-051 live 'error' — legacy frame (no typed payload) fallback", () => {
  it('renders frame.message verbatim when payload.llm_error is absent', () => {
    seedEmptyBucket()
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'error',
        session_id: SID,
        message: 'legacy error string',
      })
    })

    const m = bucketMessages()[0]
    expect(m.content).toBe('legacy error string')
    expect(m.errorCode).toBeUndefined()
    expect(m.errorDetail).toBeUndefined()
    expect(m.errorEntryId).toBeUndefined()
  })

  // D5 fix (UAT "Site 3"): a legacy ErrorFrame whose message LOOKS like
  // Go-internal/protocol jargon (the `<component>: <verb>` convention) must
  // NOT be shown verbatim — both the fresh error bubble's content AND the
  // global connection-error banner (AppShell, "visible on every screen" per
  // its own doc comment) are the two sinks this exact frame shape reaches
  // when there's no prior assistant message in the bucket (seedEmptyBucket
  // — streamingIds ends up empty, so the "push a fresh bubble" branch runs,
  // which is also the ONLY branch that calls setConnectionError for a
  // tagged frame).
  it('D5: sanitizes a Go-internal-jargon-shaped frame.message on both the fresh bubble content and the connection-error banner', () => {
    seedEmptyBucket()
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'error',
        session_id: SID,
        message: 'browser_control: attach before requesting control',
      })
    })

    const m = bucketMessages()[0]
    expect(m.content).toBe('Something went wrong — please try again.')
    expect(m.content).not.toContain('browser_control')
    expect(useConnectionStore.getState().connectionError).toBe('Something went wrong — please try again.')
  })

  // Negative control: a legacy message that is NOT jargon-shaped (no
  // "<component>: " prefix) must still pass through unchanged on both sinks
  // — this is what proves the D5 fix is a targeted filter, not a blanket
  // "always show a generic message" regression.
  it('D5: still passes a non-jargon legacy frame.message through unchanged on both sinks', () => {
    seedEmptyBucket()
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'error',
        session_id: SID,
        message: 'session not found',
      })
    })

    const m = bucketMessages()[0]
    expect(m.content).toBe('session not found')
    expect(useConnectionStore.getState().connectionError).toBe('session not found')
  })
})

// ---------------------------------------------------------------------------
// REPLAY case 'replay_error' — mirrors the live path
// ---------------------------------------------------------------------------

describe("ADR-051 'replay_error' — typed payload translation + coalesce", () => {
  it('pushes a fresh error bubble with the translated copy when no trailing assistant bubble exists', () => {
    seedEmptyBucket({ isReplaying: true })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_error',
        session_id: SID,
        entry_id: 'entry-r1',
        timestamp: '2026-01-01T00:00:00Z',
        kind: 'error',
        message: 'legacy wire msg',
        payload: { llm_error: { code: 'rate_limited', message: 'm', retryable: true } },
      })
    })

    const msgs = bucketMessages()
    expect(msgs).toHaveLength(1)
    expect(msgs[0].status).toBe('error')
    expect(msgs[0].content).toBe(codeToDisplay.rate_limited)
    expect(msgs[0].errorCode).toBe('rate_limited')
    expect(msgs[0].errorEntryId).toBe('entry-r1')
    // Replay path strips detail — never present.
    expect(msgs[0].errorDetail).toBeUndefined()
  })

  it('coalesces into a trailing streaming assistant bubble instead of pushing a fresh one', () => {
    // Seed an EMPTY streaming assistant bubble (a placeholder that never got
    // tokens before the error fired — same-turn unit).
    const placeholder: ChatMessage = {
      id: 'ph1',
      role: 'assistant',
      content: '',
      timestamp: '2026-01-01T00:00:00Z',
      status: 'streaming',
      isStreaming: true,
    }
    act(() => {
      useChatStore.setState((_s) => {
        const bucket = {
          ...makeBucketMessages([placeholder]),
          toolCalls: {},
          toolCallOrder: [],
          textAtToolCallStart: {},
          isStreaming: true,
          isReplaying: true,
          replayCompletedForSession: null,
          sessionTokens: 0,
          sessionCost: 0,
          rateLimitEvent: null,
          cancelStage: null,
          lastUserMessageAt: null,
          lastReceivedEventTime: null,
          spanByParentCallId: {},
        }
        return {
          sessionsById: { [SID]: bucket },
          messages: getMessages(bucket),
          isStreaming: true,
          isReplaying: true,
        }
      })
    })

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_error',
        session_id: SID,
        entry_id: 'entry-r2',
        timestamp: '2026-01-01T00:00:01Z',
        kind: 'error',
        message: 'm',
        payload: { llm_error: { code: 'context_too_long', message: 'm', retryable: false } },
      })
    })

    // Coalesced — same placeholder bubble, NOT a new one.
    const msgs = bucketMessages()
    expect(msgs).toHaveLength(1)
    expect(msgs[0].id).toBe('ph1')
    expect(msgs[0].status).toBe('error')
    expect(msgs[0].isStreaming).toBe(false)
    // The placeholder had empty content → translated copy is used.
    expect(msgs[0].content).toBe(codeToDisplay.context_too_long)
    expect(msgs[0].errorCode).toBe('context_too_long')
    expect(msgs[0].errorEntryId).toBe('entry-r2')
  })

  it('falls back to frame.message for legacy replay_error frames without typed payload', () => {
    seedEmptyBucket({ isReplaying: true })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_error',
        session_id: SID,
        entry_id: 'entry-r3',
        timestamp: '2026-01-01T00:00:00Z',
        kind: 'error',
        message: 'legacy replay text',
      })
    })

    const m = bucketMessages()[0]
    expect(m.content).toBe('legacy replay text')
    expect(m.errorCode).toBeUndefined()
  })
})

// ---------------------------------------------------------------------------
// live → replay dedup by entry_id
// ---------------------------------------------------------------------------

describe('ADR-051 — live→replay entry_id dedup', () => {
  it('a live error then a replay_error with the SAME entry_id yields ONE bubble', () => {
    seedEmptyBucket()
    // 1. Live error arrives, stamps entry id 'dup-1'.
    act(() => {
      useChatStore.getState().handleFrame(
        liveErrorFrame({
          sessionId: SID,
          message: 'live m',
          entryId: 'dup-1',
          llmError: { code: 'network', message: 'live m', retryable: true },
        }),
      )
    })
    expect(bucketMessages()).toHaveLength(1)

    // 2. User reloads; the same error replays with the same entry_id.
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_error',
        session_id: SID,
        entry_id: 'dup-1',
        timestamp: '2026-01-01T00:00:00Z',
        kind: 'error',
        message: 'replay m',
        payload: { llm_error: { code: 'network', message: 'replay m', retryable: true } },
      })
    })

    // Still exactly one bubble — the replay was deduped.
    const msgs = bucketMessages()
    expect(msgs).toHaveLength(1)
    // Original live content is preserved (dedup = no-op, no overwrite).
    expect(msgs[0].content).toBe(codeToDisplay.network)
    expect(msgs[0].errorEntryId).toBe('dup-1')
  })

  it('two live errors with DIFFERENT entry_ids both render (dedup is per-entry-id, not a global cap)', () => {
    seedEmptyBucket()
    for (const id of ['e-A', 'e-B']) {
      act(() => {
        useChatStore.getState().handleFrame(
          liveErrorFrame({
            sessionId: SID,
            message: 'm',
            entryId: id,
            llmError: { code: 'unknown', message: 'm', retryable: false },
          }),
        )
      })
    }
    expect(bucketMessages()).toHaveLength(2)
  })
})

// ---------------------------------------------------------------------------
// Regression guards — kickoff reject & cancel ack remain untouched
// ---------------------------------------------------------------------------

describe("ADR-051 — kickoff-reject and cancel-ack sub-paths unchanged", () => {
  it('a kickoff-reject (no session_id) still routes to abandonPendingKickoffInternal + toast, not the typed-payload branch', () => {
    // Seed a pendingKickoff so the reject branch fires.
    act(() => {
      useChatStore.setState({ pendingKickoff: { workspaceId: 'ws-1', workspaceName: 'WS', kickoffContent: 'go' } as never })
      useSessionStore.setState({ activeSessionId: '__pending' })
    })
    const toastSpy = vi.spyOn(useUiStore.getState(), 'addToast')

    act(() => {
      useChatStore.getState().handleFrame({
        // NO session_id — this is what gates the kickoff-reject branch.
        type: 'error',
        message: 'kickoff blew up',
        // Even if a (malformed) typed payload is present, the kickoff branch
        // fires FIRST and never reaches the typed-payload render code.
        payload: { llm_error: { code: 'unknown', message: 'kickoff blew up', retryable: false } },
      })
    })

    // No bucket was created (no session to tag it to).
    expect(useChatStore.getState().sessionsById['__pending']).toBeUndefined()
    // The pendingKickoff was cleared by abandonPendingKickoffInternal.
    expect(useChatStore.getState().pendingKickoff).toBeNull()
    // A toast fired (kickoff-reject path).
    expect(toastSpy).toHaveBeenCalled()
    toastSpy.mockRestore()
  })

  it('a cancel-ack error (message matches /turn.cancel/i) still resolves to interrupted status, not error', () => {
    seedEmptyBucket()
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'error',
        session_id: SID,
        message: 'turn.cancel: user requested stop',
      })
    })

    const m = bucketMessages()[0]
    // Cancel-ack → interrupted (NOT error), empty content.
    expect(m.status).toBe('interrupted')
    expect(m.content).toBe('')
    expect(m.errorCode).toBeUndefined()
  })
})

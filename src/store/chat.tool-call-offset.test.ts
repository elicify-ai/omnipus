// Regression tests for tool-call interleaving POSITION STAMPING (`textOffset`).
//
// Bug (root-caused): during streaming, tool-call interleaving was rendered
// from per-call snapshots `textAtToolCallStart[callId]` — the assistant
// bubble's `content` at the moment the call started. On the `done` frame
// (and every WS-replay merge branch), the bake logic flattens live calls
// into `message.tool_calls` and then WIPES `textAtToolCallStart` —
// destroying the only position info before any renderer could use it for a
// non-last (historical) bubble.
//
// Fix: every bake site (`bakeToolCallsByOwner`, the sendMessage-time bake,
// and every inline WS-replay bake block) now stamps each baked call with
// `textOffset` via `stampToolCallOffset` (chat.ts) BEFORE the snapshot
// tables are cleared. `textOffset` is the character offset into the owning
// message's FINAL `content` where the call started — since content is
// append-only, the snapshot's `.length` at bake time is already correct for
// the eventually-finalized content, no matter what appends after. A call
// re-baked more than once (abandoned-bubble copy-bake + turn-end bake;
// replay same-turn merges) preserves its already-stamped offset if the
// snapshot has since been cleared, rather than defaulting to 0.
//
// See chat.ts's `PositionedToolCall` / `stampToolCallOffset` doc comments
// for the exact contract these tests pin.

import { describe, it, expect, beforeEach, vi } from 'vitest'
import { act } from 'react'
import { useChatStore, makeBucketMessages, evictMessageFromBucket, getMessages } from './chat'
import type { SessionChatState, PositionedToolCall } from './chat'
import { useConnectionStore } from './connection'
import { useSessionStore } from './session'

const SID = 'sess-offset-test'

/** Stub a connected WS + active session, matching chat.multi-turn-order.test.ts's pattern. */
function stubConnection(): ReturnType<typeof vi.fn> {
  const send = vi.fn().mockReturnValue(true)
  useConnectionStore.setState({
    connection: { send } as unknown as ReturnType<typeof useConnectionStore.getState>['connection'],
    isConnected: true,
    connectionError: null,
    setConnectionError: useConnectionStore.getState().setConnectionError,
    setConnection: useConnectionStore.getState().setConnection,
    setConnected: useConnectionStore.getState().setConnected,
  })
  useSessionStore.setState({
    ...useSessionStore.getState(),
    activeSessionId: SID,
  })
  return send
}

/** Default field values shared by every hand-built SessionChatState fixture in this file. */
const BUCKET_DEFAULTS = {
  trimmedCount: 0,
  isReplaying: false,
  replayCompletedForSession: null,
  sessionTokens: 0,
  sessionCost: 0,
  rateLimitEvent: null,
  lastUserMessageAt: null,
  cancelStage: null,
  lastReceivedEventTime: null,
  spanByParentCallId: {},
} satisfies Partial<SessionChatState>

function toolCallOf(msg: { tool_calls?: unknown } | undefined, idx = 0): PositionedToolCall | undefined {
  const list = (msg?.tool_calls ?? []) as PositionedToolCall[]
  return list[idx]
}

describe('chat store — tool-call offset stamping (position-in-model fix)', () => {
  beforeEach(() => {
    useSessionStore.setState({ ...useSessionStore.getState(), activeSessionId: SID })
    act(() => { useChatStore.getState().resetSession() })
  })

  it('single tool call: textOffset equals the character length of the text preceding it, and the snapshot table is empty after done', () => {
    act(() => { useChatStore.getState().handleFrame({ type: 'token', content: 'Let me check. ', session_id: SID }) })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_start',
        call_id: 'tc1',
        tool: 'web_search',
        params: { query: 'the answer' },
        session_id: SID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_result',
        call_id: 'tc1',
        tool: 'web_search',
        result: { hits: [] },
        status: 'success',
        session_id: SID,
      })
    })
    act(() => { useChatStore.getState().handleFrame({ type: 'token', content: 'The answer is 42.', session_id: SID }) })
    act(() => { useChatStore.getState().handleFrame({ type: 'done', session_id: SID }) })

    const state = useChatStore.getState()
    const assistant = state.messages.find((m) => m.role === 'assistant')!
    // The tool_call_start sets pendingTextBoundary, so the trailing token
    // gets a paragraph break inserted before it — content is still
    // append-only, so the offset recorded at call-start time (14, the
    // length of the pre-call snapshot) remains the correct split point.
    expect(assistant.content).toBe('Let me check. \n\nThe answer is 42.')
    expect(assistant.content.slice(0, 14)).toBe('Let me check. ')

    const toolCalls = (assistant.tool_calls ?? []) as PositionedToolCall[]
    expect(toolCalls).toHaveLength(1)
    expect('Let me check. '.length).toBe(14) // pin the literal the assertion below relies on
    expect(toolCalls[0].textOffset).toBe(14)

    const bucket = state.sessionsById[SID]!
    expect(bucket.textAtToolCallStart).toEqual({})
    expect(bucket.toolCallOrder).toEqual([])
  })

  describe('multiple tool calls in one turn', () => {
    it('two calls separated by streamed text get distinct offsets, in call order', () => {
      act(() => { useChatStore.getState().handleFrame({ type: 'token', content: 'A', session_id: SID }) })
      act(() => {
        useChatStore.getState().handleFrame({ type: 'tool_call_start', call_id: 'tc1', tool: 'a', params: {}, session_id: SID })
      })
      act(() => {
        useChatStore.getState().handleFrame({ type: 'tool_call_result', call_id: 'tc1', tool: 'a', result: 1, status: 'success', session_id: SID })
      })
      act(() => { useChatStore.getState().handleFrame({ type: 'token', content: 'B', session_id: SID }) })
      act(() => {
        useChatStore.getState().handleFrame({ type: 'tool_call_start', call_id: 'tc2', tool: 'b', params: {}, session_id: SID })
      })
      act(() => {
        useChatStore.getState().handleFrame({ type: 'tool_call_result', call_id: 'tc2', tool: 'b', result: 2, status: 'success', session_id: SID })
      })
      act(() => { useChatStore.getState().handleFrame({ type: 'token', content: 'C', session_id: SID }) })
      act(() => { useChatStore.getState().handleFrame({ type: 'done', session_id: SID }) })

      const assistant = useChatStore.getState().messages.find((m) => m.role === 'assistant')!
      // Each tool_call_start opens a pendingTextBoundary paragraph break before
      // the next token — content is append-only, so the offsets recorded at
      // call-start time remain valid split points into this final string.
      expect(assistant.content).toBe('A\n\nB\n\nC')

      const toolCalls = (assistant.tool_calls ?? []) as PositionedToolCall[]
      expect(toolCalls.map((tc) => tc.id)).toEqual(['tc1', 'tc2'])
      expect(toolCalls[0].textOffset).toBe(1) // 'A'.length
      expect(toolCalls[1].textOffset).toBe(4) // 'A\n\nB'.length
      expect(toolCalls[0].textOffset).not.toBe(toolCalls[1].textOffset)
    })

    it('two calls back-to-back with no text between them get EQUAL offsets, call order preserved', () => {
      act(() => { useChatStore.getState().handleFrame({ type: 'token', content: 'A', session_id: SID }) })
      act(() => {
        useChatStore.getState().handleFrame({ type: 'tool_call_start', call_id: 'tc1', tool: 'a', params: {}, session_id: SID })
      })
      act(() => {
        useChatStore.getState().handleFrame({ type: 'tool_call_result', call_id: 'tc1', tool: 'a', result: 1, status: 'success', session_id: SID })
      })
      // No token frame between the two calls.
      act(() => {
        useChatStore.getState().handleFrame({ type: 'tool_call_start', call_id: 'tc2', tool: 'b', params: {}, session_id: SID })
      })
      act(() => {
        useChatStore.getState().handleFrame({ type: 'tool_call_result', call_id: 'tc2', tool: 'b', result: 2, status: 'success', session_id: SID })
      })
      act(() => { useChatStore.getState().handleFrame({ type: 'token', content: 'B', session_id: SID }) })
      act(() => { useChatStore.getState().handleFrame({ type: 'done', session_id: SID }) })

      const assistant = useChatStore.getState().messages.find((m) => m.role === 'assistant')!
      expect(assistant.content).toBe('A\n\nB')

      const toolCalls = (assistant.tool_calls ?? []) as PositionedToolCall[]
      expect(toolCalls.map((tc) => tc.id)).toEqual(['tc1', 'tc2'])
      expect(toolCalls[0].textOffset).toBe(1)
      expect(toolCalls[1].textOffset).toBe(1)
      expect(toolCalls[0].textOffset).toBe(toolCalls[1].textOffset)
    })
  })

  describe('sendMessage-time bake preservation', () => {
    it('stamps the offset once for a still-dangling prior-turn call; an unrelated later done for the next turn does not disturb it', () => {
      // Seed turn 1 as if it streamed "foo " then started+resolved tc1, but
      // the bucket's isStreaming was already force-cleared by some other path
      // (e.g. the F-S3 unknown-sid done guard) while tc1 is still genuinely
      // unbaked in the live toolCallOrder/toolCalls maps — the exact
      // precondition sendMessage's own dangling-call bake exists to rescue.
      const assistantId = 'asst-turn1'
      act(() => {
        useChatStore.setState({
          sessionsById: {
            [SID]: {
              ...makeBucketMessages([
                { id: assistantId, role: 'assistant', content: 'foo ', timestamp: '2026-07-16T00:00:00Z', status: 'streaming', isStreaming: true },
              ]),
              toolCalls: { tc1: { id: 'tc1', call_id: 'tc1', tool: 'shell', params: {}, status: 'success', result: { ok: true } } },
              toolCallOrder: ['tc1'],
              textAtToolCallStart: { tc1: 'foo ' },
              toolCallOwnerMessageId: { tc1: assistantId },
              isStreaming: false,
              ...BUCKET_DEFAULTS,
            },
          },
          isStreaming: false,
        })
      })

      stubConnection()
      act(() => { useChatStore.getState().sendMessage('second user message') })

      const afterSend = useChatStore.getState()
      const turn1MsgAfterSend = afterSend.sessionsById[SID]!.messagesById[assistantId]
      const tcAfterSend = toolCallOf(turn1MsgAfterSend)
      expect(tcAfterSend?.id).toBe('tc1')
      expect(tcAfterSend?.textOffset).toBe(4) // 'foo '.length
      // Baked and removed from the live map — nothing left to re-bake later.
      expect(afterSend.sessionsById[SID]!.toolCalls['tc1']).toBeUndefined()

      // Turn 2 streams and finishes normally; its own done-time bake only
      // touches turn 2's toolCallOrder, never turn 1's already-baked call.
      act(() => { useChatStore.getState().handleFrame({ type: 'token', content: 'bar', session_id: SID }) })
      act(() => { useChatStore.getState().handleFrame({ type: 'done', session_id: SID }) })

      const final = useChatStore.getState()
      const turn1MsgFinal = final.sessionsById[SID]!.messagesById[assistantId]
      expect(toolCallOf(turn1MsgFinal)?.textOffset).toBe(4)
    })

    it('re-baking a call after its snapshot has been cleared preserves the previously-stamped offset (never resets to 0)', () => {
      // Simulates a call that was already baked once (offset 4 stamped) but
      // is somehow still present in the live toolCallOrder/toolCalls maps
      // when a SECOND bake fires (e.g. the abandoned-bubble copy-bake
      // deliberately leaves the live bucket untouched) — by the time this
      // second bake runs, textAtToolCallStart no longer has an entry for it.
      const assistantId = 'asst1'
      act(() => {
        useChatStore.setState({
          sessionsById: {
            [SID]: {
              ...makeBucketMessages([
                {
                  id: assistantId,
                  role: 'assistant',
                  content: 'foo bar',
                  timestamp: '',
                  status: 'streaming',
                  isStreaming: true,
                  tool_calls: [{ id: 'tc1', tool: 'shell', params: {}, status: 'success', result: { ok: true }, textOffset: 4 } as PositionedToolCall],
                },
              ]),
              toolCalls: { tc1: { id: 'tc1', call_id: 'tc1', tool: 'shell', params: {}, status: 'success', result: { ok: true } } },
              toolCallOrder: ['tc1'],
              textAtToolCallStart: {}, // already cleared by the earlier bake
              toolCallOwnerMessageId: { tc1: assistantId },
              isStreaming: true,
              ...BUCKET_DEFAULTS,
            },
          },
          isStreaming: true,
        })
      })

      act(() => { useChatStore.getState().handleFrame({ type: 'done', session_id: SID }) })

      const state = useChatStore.getState()
      const msg = state.sessionsById[SID]!.messagesById[assistantId]
      expect(toolCallOf(msg)?.textOffset).toBe(4)
      expect(state.sessionsById[SID]!.textAtToolCallStart).toEqual({})
    })
  })

  describe('WS-replay same-turn merge', () => {
    it('a tool call started between two same-turn_id replay segments gets textOffset === length of the FIRST segment', () => {
      // Mirrors ChatStore_ReplaySequence_InterleavedTurn_TwoFrames in
      // chat.test.ts: segment 'A' -> shell call -> segment 'B', merged into
      // one 'A\n\nB' bubble. The bake happens BEFORE the second segment is
      // appended (candidate.content += '\n\n' + text), so the offset
      // computed from the tool call's snapshot ('A', captured when it
      // started) is already the correct split point in the final content.
      act(() => {
        useChatStore.getState().handleFrame({
          type: 'replay_message',
          role: 'assistant',
          content: 'A',
          id: 'entry-1',
          agent_id: 'agent-ray',
          turn_id: 'turn-interleaved-1',
          session_id: SID,
        })
      })
      act(() => {
        useChatStore.getState().handleFrame({
          type: 'tool_call_start',
          call_id: 'tc_shell',
          tool: 'shell',
          params: { cmd: 'echo hi' },
          agent_id: 'agent-ray',
          session_id: SID,
        })
      })
      act(() => {
        useChatStore.getState().handleFrame({
          type: 'tool_call_result',
          call_id: 'tc_shell',
          tool: 'shell',
          result: { stdout: 'hi\n' },
          status: 'success',
          duration_ms: 42,
          session_id: SID,
        })
      })
      act(() => {
        useChatStore.getState().handleFrame({
          type: 'replay_message',
          role: 'assistant',
          content: 'B',
          id: 'entry-2',
          agent_id: 'agent-ray',
          turn_id: 'turn-interleaved-1',
          session_id: SID,
        })
      })

      const state = useChatStore.getState()
      const assistantMsgs = state.messages.filter((m) => m.role === 'assistant')
      expect(assistantMsgs).toHaveLength(1)
      expect(assistantMsgs[0].content).toBe('A\n\nB')

      const tc = toolCallOf(assistantMsgs[0])
      expect(tc?.id).toBe('tc_shell')
      expect('A'.length).toBe(1) // pin the literal the assertion below relies on
      expect(tc?.textOffset).toBe('A'.length)
    })

    /**
     * ADR-070 §2.2: the merge condition above (`sameTurn && compatibleProducer`)
     * is deliberately blind to whether the two segments are ADJACENT in
     * messageOrder — it never checked for that before this fix. A mid-turn
     * steer's persisted user entry, replayed between two same-turn_id
     * assistant entries, must break the merge; without the raw-tail guard
     * this test pins, the two entries would wrongly coalesce into one bubble
     * positioned BEFORE the steer's user message on reload — reproducing the
     * live ordering bug (ADR-070's whole reason for existing) after a
     * refresh, even once the live-path fix (§2.1) is applied.
     */
    it('a mid-turn-steer user entry replayed BETWEEN two same-turn_id assistant entries prevents them from merging (ADR-070 §2.2)', () => {
      act(() => {
        useChatStore.getState().handleFrame({
          type: 'replay_message',
          role: 'assistant',
          content: 'pre-steer reply',
          id: 'entry-pre',
          agent_id: 'agent-ray',
          turn_id: 'turn-steered-1',
          session_id: SID,
        })
      })
      act(() => {
        useChatStore.getState().handleFrame({
          type: 'replay_message',
          role: 'user',
          content: 'the follow-up sent mid-turn',
          id: 'entry-steer',
          session_id: SID,
        })
      })
      act(() => {
        useChatStore.getState().handleFrame({
          type: 'replay_message',
          role: 'assistant',
          content: 'post-steer reply',
          id: 'entry-post',
          agent_id: 'agent-ray',
          turn_id: 'turn-steered-1',
          session_id: SID,
        })
      })

      const state = useChatStore.getState()
      const messages = state.messages
      // TWO separate assistant bubbles, correctly ordered around the steer —
      // not one merged bubble sitting before it.
      expect(messages.map((m) => m.role)).toEqual(['assistant', 'user', 'assistant'])
      expect(messages[0].content).toBe('pre-steer reply')
      expect(messages[1].content).toBe('the follow-up sent mid-turn')
      expect(messages[2].content).toBe('post-steer reply')
      expect(messages.filter((m) => m.role === 'assistant')).toHaveLength(2)
    })

    /**
     * Regression guard, same fix: when NOTHING intervenes between two
     * same-turn_id assistant entries (the ordinary interleaved-narration
     * case this describe block's other tests already cover for the
     * tool-call variant), the raw-tail guard must be a no-op — the candidate
     * IS still the raw tail, so the merge proceeds exactly as before.
     */
    it('two same-turn_id assistant entries with NO intervening entry still merge (regression guard, non-steer case unchanged)', () => {
      act(() => {
        useChatStore.getState().handleFrame({
          type: 'replay_message',
          role: 'assistant',
          content: 'first half',
          id: 'entry-1b',
          agent_id: 'agent-ray',
          turn_id: 'turn-unsteered-1',
          session_id: SID,
        })
      })
      act(() => {
        useChatStore.getState().handleFrame({
          type: 'replay_message',
          role: 'assistant',
          content: 'second half',
          id: 'entry-2b',
          agent_id: 'agent-ray',
          turn_id: 'turn-unsteered-1',
          session_id: SID,
        })
      })

      const state = useChatStore.getState()
      const assistantMsgs = state.messages.filter((m) => m.role === 'assistant')
      expect(assistantMsgs).toHaveLength(1)
      expect(assistantMsgs[0].content).toBe('first half\n\nsecond half')
    })

    /**
     * ADR-070 §2.2 covers TWO replay merge branches: the general same-turn
     * merge (tested above) and this one — coalescing replayed text into a
     * trailing EMPTY assistant placeholder that a replayed tool_call_start
     * already created. Self-verification (test-plan-and-write mutation
     * gate): confirmed by deliberately removing the raw-tail guard from this
     * specific branch that no existing test in the suite caught it — this
     * test closes that real gap. Without the guard, a steer's user entry
     * replayed between the empty placeholder and its text would be skipped
     * over, coalescing the text into a bubble positioned BEFORE the steer.
     */
    it('a mid-turn-steer user entry replayed BETWEEN an empty tool-call placeholder and its text prevents coalescing (ADR-070 §2.2, empty-placeholder branch)', () => {
      act(() => {
        useChatStore.getState().handleFrame({
          type: 'tool_call_start',
          call_id: 'tc_pre_steer',
          tool: 'bash',
          params: {},
          agent_id: 'agent-ray',
          session_id: SID,
        })
      })
      act(() => {
        useChatStore.getState().handleFrame({
          type: 'replay_message',
          role: 'user',
          content: 'the follow-up sent mid-turn',
          id: 'entry-steer-2',
          session_id: SID,
        })
      })
      act(() => {
        useChatStore.getState().handleFrame({
          type: 'replay_message',
          role: 'assistant',
          content: 'post-steer narration',
          id: 'entry-post-2',
          agent_id: 'agent-ray',
          turn_id: 'turn-steered-2',
          session_id: SID,
        })
      })

      const state = useChatStore.getState()
      const messages = state.messages
      // The empty placeholder stays empty and separate — the post-steer
      // text must NOT coalesce into it across the steer boundary.
      expect(messages.map((m) => m.role)).toEqual(['assistant', 'user', 'assistant'])
      expect(messages[0].content).toBe('')
      expect(messages[1].content).toBe('the follow-up sent mid-turn')
      expect(messages[2].content).toBe('post-steer narration')
    })
  })

  describe('reconnect-guard: missing snapshot', () => {
    it('a call whose start-of-call snapshot was never recorded bakes with textOffset undefined — never a 0-default, never a crash', () => {
      const assistantId = 'asst-recon'
      act(() => {
        useChatStore.setState({
          sessionsById: {
            [SID]: {
              ...makeBucketMessages([
                { id: assistantId, role: 'assistant', content: 'reconnected text', timestamp: '', status: 'streaming', isStreaming: true },
              ]),
              toolCalls: { tc_orphan: { id: 'tc_orphan', call_id: 'tc_orphan', tool: 'shell', params: {}, status: 'success', result: {} } },
              toolCallOrder: ['tc_orphan'],
              textAtToolCallStart: {}, // never recorded — reconnect edge, no owner recorded either
              toolCallOwnerMessageId: {},
              isStreaming: true,
              ...BUCKET_DEFAULTS,
            },
          },
          isStreaming: true,
        })
      })

      expect(() => {
        act(() => { useChatStore.getState().handleFrame({ type: 'done', session_id: SID }) })
      }).not.toThrow()

      const state = useChatStore.getState()
      const msg = state.sessionsById[SID]!.messagesById[assistantId]
      const tc = toolCallOf(msg)
      expect(tc?.id).toBe('tc_orphan')
      expect(tc?.textOffset).toBeUndefined()
      // Must be genuinely ABSENT, not a `textOffset: undefined` key — and
      // definitely never silently defaulted to 0.
      expect(tc && 'textOffset' in tc).toBe(false)
    })
  })

  describe('evictMessageFromBucket — positioned tool calls', () => {
    it('evicting a message removes its positioned tool call from every dependent map, with no dangling entries', () => {
      const bucket: SessionChatState = {
        messagesById: {
          m1: {
            id: 'm1',
            role: 'assistant',
            content: 'foo bar',
            timestamp: '',
            status: 'done',
            tool_calls: [{ id: 'tc1', tool: 'exec', params: {}, status: 'success', textOffset: 3 } as PositionedToolCall],
          },
        },
        messageOrder: ['m1'],
        toolCalls: { tc1: { id: 'tc1', call_id: 'tc1', tool: 'exec', params: {}, status: 'success' } },
        toolCallOrder: ['tc1'],
        textAtToolCallStart: { tc1: 'foo' },
        toolCallOwnerMessageId: { tc1: 'm1' },
        isStreaming: false,
        ...BUCKET_DEFAULTS,
      }
      evictMessageFromBucket(bucket, 'm1')
      expect(bucket.messagesById['m1']).toBeUndefined()
      expect(bucket.toolCalls['tc1']).toBeUndefined()
      expect(bucket.toolCallOrder).toHaveLength(0)
      expect(bucket.textAtToolCallStart['tc1']).toBeUndefined()
      expect(bucket.toolCallOwnerMessageId?.['tc1']).toBeUndefined()
    })
  })

  // Permanent pin for the merge-blocking gate finding: a WS reconnect that
  // replays an already-baked call's tool_call_start/tool_call_result frames
  // must not corrupt its already-stamped textOffset. Root cause: the
  // done-bake wipes toolCalls/toolCallOrder/textAtToolCallStart/
  // toolCallOwnerMessageId (see the `done` case in chat.ts); on reconnect the
  // transcript replays into the now-warm bucket, so the `tool_call_start`
  // reconnect guard finds no snapshot for the already-baked call and records
  // a FRESH one reflecting the CURRENT (already-finalized) content instead of
  // the original call-start position; the next bake's `stampToolCallOffset`
  // used to prioritize that fresh snapshot over the already-correct
  // `prevOffset`, silently overwriting a correct offset with a bogus
  // end-of-text one. Fixed by (a) `stampToolCallOffset` preferring
  // `prevOffset` over a fresh snapshot (see its doc comment's INVARIANT), and
  // (b) `isToolCallBakedInBucket` skipping re-recording the snapshot and
  // re-queuing into `toolCallOrder` for a call that's already baked.
  describe('reconnect replay does not corrupt a stamped offset (gate finding pin)', () => {
    it('a live turn stamps textOffset 5; a WS reconnect replaying tc1\'s start/result (+ a deduped replayed segment) + a fresh done must NOT shift it to 11', () => {
      // --- Phase 1: ordinary live turn --------------------------------
      act(() => { useChatStore.getState().handleFrame({ type: 'token', content: 'Alpha', session_id: SID }) })
      act(() => {
        useChatStore.getState().handleFrame({
          type: 'tool_call_start', call_id: 'tc1', tool: 'shell', params: { cmd: 'echo hi' }, session_id: SID,
        })
      })
      act(() => {
        useChatStore.getState().handleFrame({
          type: 'tool_call_result', call_id: 'tc1', tool: 'shell', result: { stdout: 'hi\n' }, status: 'success', session_id: SID,
        })
      })
      act(() => { useChatStore.getState().handleFrame({ type: 'token', content: 'Beta', session_id: SID }) })
      act(() => { useChatStore.getState().handleFrame({ type: 'done', session_id: SID }) })

      const afterLiveTurn = useChatStore.getState()
      const liveAssistant = afterLiveTurn.messages.find((m) => m.role === 'assistant')!
      expect('Alpha'.length).toBe(5) // pin the literal the offset assertion relies on
      expect('Alpha\n\nBeta'.length).toBe(11) // pin the literal the pre-fix corruption value relies on
      expect(liveAssistant.content).toBe('Alpha\n\nBeta')
      const originalOffset = toolCallOf(liveAssistant)?.textOffset
      expect(originalOffset).toBe(5)
      const assistantId = liveAssistant.id

      // --- Phase 2: WS reconnect replays the already-completed turn ----
      act(() => { useChatStore.getState().setReplaying(true) })
      act(() => {
        useChatStore.getState().handleFrame({
          type: 'tool_call_start', call_id: 'tc1', tool: 'shell', params: { cmd: 'echo hi' }, session_id: SID,
        })
      })
      act(() => {
        useChatStore.getState().handleFrame({
          type: 'tool_call_result', call_id: 'tc1', tool: 'shell', result: { stdout: 'hi\n' }, status: 'success', session_id: SID,
        })
      })
      // Replayed segment: the backend re-sends the transcript entry for this
      // same assistant bubble (same server-assigned id) — the SPA's own
      // id-based replay dedup (see the `replay_message` reducer's
      // `draft.messageOrder.includes(messageId)` check) recognizes the id is
      // already present and no-ops, exactly as a warm-bucket reconnect
      // replaying content the client already rendered live would behave.
      act(() => {
        useChatStore.getState().handleFrame({
          type: 'replay_message', role: 'assistant', content: 'Alpha\n\nBeta', id: assistantId, session_id: SID,
        })
      })
      act(() => { useChatStore.getState().handleFrame({ type: 'done', session_id: SID }) })

      const final = useChatStore.getState()
      const finalAssistant = final.messages.find((m) => m.id === assistantId)!
      // Content is unchanged — the replayed segment was a dedup no-op, so
      // the pre-fix corrupted value (content.length === 11) is well-defined.
      expect(finalAssistant.content).toBe('Alpha\n\nBeta')
      // THE PIN.
      expect(toolCallOf(finalAssistant)?.textOffset).toBe(originalOffset)
      expect(toolCallOf(finalAssistant)?.textOffset).toBe(5)
      expect(toolCallOf(finalAssistant)?.textOffset).not.toBe(11)
    })
  })

  // gate-6 F5: the offset-stamping bake must work identically for a
  // background (non-active) session bucket — a session the user isn't
  // currently viewing, but whose WS frames still arrive and must still be
  // baked correctly — and must never leak into or read from the foreground
  // (active session) projection.
  describe('background-bucket bake (gate-6 F5)', () => {
    it('bakes textOffset correctly into sessionsById[SID] while a DIFFERENT session is active, and leaves the active bucket untouched', () => {
      const OTHER_SID = 'other-session-active'
      useSessionStore.setState({ ...useSessionStore.getState(), activeSessionId: OTHER_SID })

      act(() => { useChatStore.getState().handleFrame({ type: 'token', content: 'Let me check. ', session_id: SID }) })
      act(() => {
        useChatStore.getState().handleFrame({
          type: 'tool_call_start', call_id: 'tc1', tool: 'web_search', params: { query: 'the answer' }, session_id: SID,
        })
      })
      act(() => {
        useChatStore.getState().handleFrame({
          type: 'tool_call_result', call_id: 'tc1', tool: 'web_search', result: { hits: [] }, status: 'success', session_id: SID,
        })
      })
      act(() => { useChatStore.getState().handleFrame({ type: 'token', content: 'The answer is 42.', session_id: SID }) })
      act(() => { useChatStore.getState().handleFrame({ type: 'done', session_id: SID }) })

      const state = useChatStore.getState()
      // The active session (OTHER_SID) was never targeted by any of these
      // SID-tagged frames — its bucket must never have been created, and the
      // foreground `messages` projection (which mirrors the active bucket)
      // must stay empty.
      expect(state.sessionsById[OTHER_SID]).toBeUndefined()
      expect(state.messages).toEqual([])

      // The background bucket (SID) must have baked exactly as the
      // single-call offset case (first test in this file) does when SID is
      // active — routing to a background bucket must not change the bake.
      const bucket = state.sessionsById[SID]!
      const assistant = getMessages(bucket).find((m) => m.role === 'assistant')!
      expect(assistant.content).toBe('Let me check. \n\nThe answer is 42.')
      const toolCalls = (assistant.tool_calls ?? []) as PositionedToolCall[]
      expect(toolCalls).toHaveLength(1)
      expect(toolCalls[0].textOffset).toBe(14)
      expect(bucket.textAtToolCallStart).toEqual({})
      expect(bucket.toolCallOrder).toEqual([])
    })
  })
})

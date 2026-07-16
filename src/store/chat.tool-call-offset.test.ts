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
import { useChatStore, makeBucketMessages, evictMessageFromBucket } from './chat'
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
})

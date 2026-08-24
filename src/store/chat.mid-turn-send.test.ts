/**
 * chat.mid-turn-send.test.ts — mid-turn steering sends (bugfixes3: enable
 * mid-turn queued sends in the web chat composer).
 *
 * Verified context (task brief): the gateway already supports messages
 * arriving mid-turn — they're queued per-scope and INJECTED into the running
 * turn between tool calls (steering; any remaining tool calls for that turn
 * are skipped server-side with "Skipped due to queued user message").
 * Channels get this today; the SPA composer was the one surface that
 * hard-blocked it via `sendMessage`'s `isStreaming` guard (gate 4 of 4 — see
 * ChatScreen.tsx for the other three, UI-level gates).
 *
 * This file covers the STORE side of the fix: `sendMessage` called while
 * `isStreaming` is already true must NOT start a second turn. It must:
 *   - append ONLY a user message (no second assistant placeholder — the
 *     backend keeps streaming into the FIRST/existing assistant bubble);
 *   - position that user message AFTER the still-streaming assistant bubble
 *     (correct chronology — it was sent while that turn was still running);
 *   - leave `isStreaming` untouched (already true);
 *   - forward the same `{type:'message', ...}` WS frame shape a new turn
 *     uses (the gateway does the steering, not a different wire frame);
 *   - on a failed send, mark only the just-appended steering message
 *     'error' — the in-flight turn and its assistant bubble are untouched;
 *   - still respect offline buffering FIRST — a mid-turn send made while the
 *     socket is down enqueues into outboundQueue exactly like an idle-state
 *     send, it does not bypass that mechanism.
 *
 * Setup deliberately reuses the store's OWN sendMessage (not hand-seeded
 * bucket state) to establish "a turn is already in flight": SessionChatState
 * is bucket-keyed (sessionsById), and withBucket() re-derives the flat
 * foreground fields (including `isStreaming`) from the ACTIVE bucket on
 * every call — hand-setting only the top-level `useChatStore.setState({
 * isStreaming: true })` (as some older tests in chat.test.ts do, for
 * assertions that don't depend on it surviving a subsequent withBucket call)
 * would NOT be reflected on `sessionsById[sid].isStreaming`, and the very
 * first withBucket() call inside the code under test would silently
 * overwrite the foreground flag back to the bucket's stale `false`. Driving
 * a real first `sendMessage()` call goes through the actual code path and
 * sets both levels correctly, matching how a real turn starts.
 */

import { describe, it, expect, beforeEach, vi } from 'vitest'
import { act } from 'react'
import { useChatStore } from './chat'
import { useConnectionStore } from './connection'
import { useSessionStore } from './session'
import { useWorkspacesStore } from './workspacesStore'
import type { WsConnection } from '@/lib/ws'
import type { ToolCall } from '@/lib/api'

const TEST_SESSION_ID = 'sess_mid_turn_test'

function resetStores() {
  act(() => {
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
      outboundQueue: [],
      pendingDrainQueue: [],
    })
    useConnectionStore.setState({
      connection: null,
      isConnected: false,
      connectionError: null,
      reconnectPhase: null,
      reconnectAttempt: 0,
    })
    useSessionStore.setState({
      activeSessionId: TEST_SESSION_ID,
      activeAgentId: 'general-assistant',
      activeAgentType: null,
    })
    useWorkspacesStore.setState({ activeWorkspaceId: null })
  })
}

beforeEach(resetStores)

/** Connects the store with a `send` spy that always succeeds. */
function connectWithSendSpy() {
  const send = vi.fn().mockReturnValue(true)
  act(() => {
    useConnectionStore.setState({
      connection: { send, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as unknown as WsConnection,
      isConnected: true,
      connectionError: null,
    })
  })
  return send
}

/** Starts a real turn (mirrors an idle-state send) so isStreaming is true at
 * BOTH the bucket level and the derived foreground level — see file header. */
function startTurn(send: ReturnType<typeof vi.fn>, content = 'first message') {
  act(() => {
    useChatStore.getState().sendMessage(content)
  })
  expect(useChatStore.getState().isStreaming).toBe(true)
  send.mockClear()
}

describe('sendMessage — mid-turn steering send (bucket-level)', () => {
  beforeEach(resetStores)

  it('appends only a user message after the streaming assistant bubble, closing it in place (ADR-070)', () => {
    const send = connectWithSendSpy()
    startTurn(send)

    act(() => {
      useChatStore.getState().sendMessage('steer this turn')
    })

    const { messages, isStreaming } = useChatStore.getState()
    expect(messages).toHaveLength(3)
    expect(messages[0].role).toBe('user')
    expect(messages[0].content).toBe('first message')
    expect(messages[1].role).toBe('assistant')
    // ADR-070 §2.1: the pre-steer bubble is closed IN PLACE the moment the
    // steer is sent, so the next token/tool_call_start opens a fresh bubble
    // positioned after the steer's user message instead of continuing to
    // write into this one.
    expect(messages[1].isStreaming).toBe(false)
    expect(messages[1].status).toBe('done')
    expect(messages[1].closedBySteer).toBe(true)
    expect(messages[2].role).toBe('user')
    expect(messages[2].content).toBe('steer this turn')
    expect(messages[2].status).toBe('done')

    // Exactly ONE assistant message exists — the mid-turn send did not mint
    // a second (permanently-empty) streaming placeholder. A second bubble
    // opens only once the NEXT frame actually arrives.
    expect(messages.filter((m) => m.role === 'assistant')).toHaveLength(1)

    // isStreaming is left alone at the bucket/session level — the overall
    // turn is still in flight even though this specific segment closed.
    expect(isStreaming).toBe(true)
  })

  it('forwards the steering message over the WS with the same frame shape a new turn uses', () => {
    const send = connectWithSendSpy()
    startTurn(send)

    act(() => {
      useChatStore.getState().sendMessage('steer this turn')
    })

    expect(send).toHaveBeenCalledTimes(1)
    expect(send).toHaveBeenCalledWith(
      expect.objectContaining({
        type: 'message',
        content: 'steer this turn',
        session_id: TEST_SESSION_ID,
        agent_id: 'general-assistant',
      })
    )
  })

  it('updates lastUserMessageAt on a mid-turn steering send', () => {
    const send = connectWithSendSpy()
    startTurn(send)

    // Reset to null so this assertion is unambiguous — startTurn's own
    // sendMessage call already set a (possibly millisecond-identical)
    // timestamp, which would make a "changed" comparison flaky under fast
    // test execution. Confirms the STEERING send is the one setting it, not
    // just that some earlier value happens to survive.
    act(() => {
      useChatStore.setState({ lastUserMessageAt: null })
    })
    act(() => {
      useChatStore.getState().sendMessage('steer this turn')
    })
    expect(useChatStore.getState().lastUserMessageAt).not.toBeNull()
    expect(typeof useChatStore.getState().lastUserMessageAt).toBe('number')
  })

  it('3 rapid mid-turn sends append 3 distinct user messages in order — no dedupe/drop weirdness', () => {
    const send = connectWithSendSpy()
    startTurn(send)

    act(() => {
      useChatStore.getState().sendMessage('steer 1')
      useChatStore.getState().sendMessage('steer 2')
      useChatStore.getState().sendMessage('steer 3')
    })

    const { messages, isStreaming } = useChatStore.getState()
    // first + assistant placeholder + 3 steering messages = 5.
    expect(messages).toHaveLength(5)
    expect(messages.filter((m) => m.role === 'assistant')).toHaveLength(1)
    const userContents = messages.filter((m) => m.role === 'user').map((m) => m.content)
    expect(userContents).toEqual(['first message', 'steer 1', 'steer 2', 'steer 3'])
    expect(isStreaming).toBe(true)

    expect(send).toHaveBeenCalledTimes(3)
    expect(send.mock.calls.map((c) => (c[0] as { content: string }).content)).toEqual([
      'steer 1',
      'steer 2',
      'steer 3',
    ])
  })

  it('a failed mid-turn send marks only the steering message as error — the in-flight turn is untouched', () => {
    const send = vi.fn().mockReturnValueOnce(true).mockReturnValueOnce(false)
    act(() => {
      useConnectionStore.setState({
        connection: { send, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as unknown as WsConnection,
        isConnected: true,
        connectionError: null,
      })
    })
    startTurn(send, 'first message')
    // startTurn cleared the mock's call history but NOT its queued return
    // values — mockReturnValueOnce(false) is still the next resolved value.

    act(() => {
      useChatStore.getState().sendMessage('this one fails')
    })

    const state = useChatStore.getState()
    // Turn is still in flight — untouched by the steering-send failure.
    expect(state.isStreaming).toBe(true)
    const assistantMsgs = state.messages.filter((m) => m.role === 'assistant')
    expect(assistantMsgs).toHaveLength(1)
    expect(assistantMsgs[0].isStreaming).toBe(true)

    // Only the failed steering message is flagged.
    const failedMsg = state.messages.find((m) => m.content === 'this one fails')
    expect(failedMsg?.status).toBe('error')
    expect(useConnectionStore.getState().connectionError).toContain('kept')
  })

  it('does NOT bake live tool calls onto the previous assistant message (that logic is for turn-END, not mid-turn)', () => {
    const send = connectWithSendSpy()
    startTurn(send)

    // Simulate a live (unbaked) tool call on the CURRENT streaming turn —
    // the same shape sendMessage's own new-turn rebake step reads from. Must
    // be set on BOTH the bucket (sessionsById[sid]) AND the derived
    // foreground mirror: withBucket() re-derives the foreground fields from
    // the bucket on every call (see file header comment), so a top-level-only
    // `setState({ toolCalls, toolCallOrder })` would be silently discarded by
    // the very first withBucket() call inside sendMessage.
    const liveToolCall: ToolCall & { call_id: string } = {
      id: 'tc_1',
      call_id: 'tc_1',
      tool: 'bash',
      status: 'running',
      params: {},
    }
    act(() => {
      useChatStore.setState((s) => ({
        sessionsById: {
          ...s.sessionsById,
          [TEST_SESSION_ID]: {
            ...s.sessionsById[TEST_SESSION_ID],
            toolCalls: { tc_1: liveToolCall },
            toolCallOrder: ['tc_1'],
          },
        },
        toolCalls: { tc_1: liveToolCall },
        toolCallOrder: ['tc_1'],
      }))
    })

    act(() => {
      useChatStore.getState().sendMessage('steer while a tool runs')
    })

    const { messages, toolCalls, toolCallOrder } = useChatStore.getState()
    const assistantMsg = messages.find((m) => m.role === 'assistant')
    // The live tool call must NOT have been baked onto the assistant message
    // — the turn hasn't ended, so there's nothing to finalize yet.
    expect(assistantMsg?.tool_calls ?? []).toHaveLength(0)
    // The live toolCalls/toolCallOrder maps are untouched.
    expect(toolCalls['tc_1']).toBeDefined()
    expect(toolCallOrder).toEqual(['tc_1'])
  })
})

/**
 * bugfixes3 follow-up: the Attach button (ComposerPrimitive.AddAttachment)
 * and drag-drop were gated off mid-stream via `attachDisabled` in
 * ChatScreen.tsx (`isStreaming` was one of its conditions) even though
 * pasted images already rode a mid-turn steer correctly — an inconsistent
 * affordance, since paste, the Attach button, and drag-drop all funnel
 * through the exact same `composerRuntime.addAttachment()` call
 * (src/hooks/useFileUpload.ts's `commitFiles`) and are therefore
 * indistinguishable by the time they reach `sendMessage`. This describe
 * block proves the STORE side already carries attachments correctly on the
 * mid-turn steer branch — `opts.mediaRefs`/`opts.attachments` reach the WS
 * `media` field and the locally-rendered user bubble's `media` field
 * unconditionally on `isStreaming`, exactly like the already-covered
 * plain-text steer above (no separate/divergent code path for attachments
 * exists to fix).
 */
describe('sendMessage — mid-turn steering carries media attachments (bugfixes3: Attach button works mid-stream)', () => {
  beforeEach(resetStores)

  it('a mid-turn steering send with mediaRefs includes `media` in the WS steer payload', () => {
    const send = connectWithSendSpy()
    startTurn(send)

    act(() => {
      useChatStore.getState().sendMessage('see attached', {
        mediaRefs: ['media://abc123'],
        attachments: [
          { type: 'image', url: '/api/v1/uploads/sess_mid_turn_test/abc.png', filename: 'abc.png', contentType: 'image/png' },
        ],
      })
    })

    expect(send).toHaveBeenCalledTimes(1)
    expect(send).toHaveBeenCalledWith(
      expect.objectContaining({
        type: 'message',
        content: 'see attached',
        session_id: TEST_SESSION_ID,
        media: ['media://abc123'],
      })
    )
  })

  it('a mid-turn steering send with mediaRefs renders the attachment on the appended user bubble', () => {
    const send = connectWithSendSpy()
    startTurn(send)

    act(() => {
      useChatStore.getState().sendMessage('see attached', {
        mediaRefs: ['media://abc123'],
        attachments: [
          { type: 'image', url: '/api/v1/uploads/sess_mid_turn_test/abc.png', filename: 'abc.png', contentType: 'image/png' },
        ],
      })
    })

    const { messages, isStreaming } = useChatStore.getState()
    const steerMsg = messages.find((m) => m.content === 'see attached')
    expect(steerMsg).toBeDefined()
    expect(steerMsg?.media).toEqual([
      expect.objectContaining({ type: 'image', filename: 'abc.png', url: '/api/v1/uploads/sess_mid_turn_test/abc.png' }),
    ])
    // The in-flight turn is unaffected — same contract as the plain-text
    // steer case above.
    expect(isStreaming).toBe(true)
    expect(messages.filter((m) => m.role === 'assistant')).toHaveLength(1)
  })

  it('a mid-turn steering send with an empty mediaRefs array omits `media` from the WS frame (matches the no-attachment steer)', () => {
    const send = connectWithSendSpy()
    startTurn(send)

    act(() => {
      useChatStore.getState().sendMessage('no attachments here')
    })

    const frame = send.mock.calls[0]![0] as Record<string, unknown>
    expect(frame).not.toHaveProperty('media')
  })
})

describe('sendMessage — mid-turn steering respects offline buffering (does not bypass it)', () => {
  beforeEach(resetStores)

  it('a mid-turn send made while the socket is down enqueues into outboundQueue instead of sending', () => {
    const send = connectWithSendSpy()
    startTurn(send)

    // Simulate the socket going down mid-turn WITHOUT going through the
    // normal onDisconnected/clearStreamingState cleanup — isolates the
    // offline-check-runs-first ordering in sendMessage itself, independent
    // of whatever else a real disconnect handler does.
    act(() => {
      useConnectionStore.setState({ isConnected: false })
    })

    act(() => {
      useChatStore.getState().sendMessage('steer while offline')
    })

    expect(send).not.toHaveBeenCalled()
    expect(useChatStore.getState().outboundQueue).toContain('steer while offline')
    // No new user message was appended to the thread — it's buffered, not
    // rendered optimistically (matches the pre-existing idle-state offline
    // behavior in sendMessage's disconnected-WS branch).
    expect(
      useChatStore.getState().messages.some((m) => m.content === 'steer while offline')
    ).toBe(false)
    // The in-flight turn's own state is untouched by the buffered attempt.
    expect(useChatStore.getState().isStreaming).toBe(true)
  })
})

/**
 * bugfixes3 consolidated fix round — item 1 (HIGH, reproduced): a steer
 * appended a USER message after the still-streaming assistant bubble,
 * making the bucket's raw message-order TAIL a user message. Two bugs
 * stemmed from that:
 *   (a) `tool_call_start` anchored on the raw tail instead of the last
 *       STREAMING assistant message, so it minted a brand-new assistant
 *       placeholder instead of reusing the original — stranding the
 *       original bubble at isStreaming:true forever (permanent shimmer,
 *       Copy bar suppressed; only a reload repaired it).
 *   (b) `done`/`error` only finalized the LAST assistant message, which
 *       (post-steer) is no longer the one still streaming, so even without
 *       bug (a) the original bubble would never get closed out.
 * Both are fixed in src/store/chat.ts: `tool_call_start` now resolves its
 * anchor via `findLastAssistantMessageId` + a still-streaming check
 * (mirroring the `token` handler), and `done`/`error` now sweep EVERY
 * still-streaming assistant message in the bucket (mirroring
 * clearStreamingState's own sweep) instead of only the last one.
 */
describe('sendMessage — mid-turn steering + tool_call_start/done sweep (bugfixes3 fix round, item 1)', () => {
  beforeEach(resetStores)

  it('steer → tool_call_start → token → done: two assistant bubbles split at the steer, correct order, none left status "streaming" (ADR-070)', () => {
    const send = connectWithSendSpy()
    startTurn(send)

    act(() => {
      useChatStore.getState().handleFrame({ type: 'token', content: 'AAAA', session_id: TEST_SESSION_ID })
    })

    // Steer mid-turn — appends a USER message AFTER the still-streaming
    // assistant bubble, making the raw message-order tail a user message.
    // This is the exact precondition that broke tool_call_start's old
    // raw-tail anchor.
    act(() => {
      useChatStore.getState().sendMessage('steer this turn')
    })

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_start',
        call_id: 'tc_post_steer',
        tool: 'bash',
        params: {},
        session_id: TEST_SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_result',
        call_id: 'tc_post_steer',
        tool: 'bash',
        result: 'ok',
        status: 'success',
        session_id: TEST_SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({ type: 'token', content: 'BBBB', session_id: TEST_SESSION_ID })
    })
    act(() => {
      useChatStore.getState().handleFrame({ type: 'done', session_id: TEST_SESSION_ID })
    })

    const { messages, isStreaming } = useChatStore.getState()
    const assistantMsgs = messages.filter((m) => m.role === 'assistant')

    // ADR-070 §2.1: the steer closes the pre-steer bubble in place, so
    // tool_call_start (finding no still-open bubble — the raw tail is the
    // steer's user message and the pre-steer bubble is no longer streaming)
    // opens a SECOND bubble positioned after the steer, not a stray/extra
    // one — this is the fix, not a regression.
    expect(assistantMsgs).toHaveLength(2)
    expect(assistantMsgs[0].content).toBe('AAAA')
    expect(assistantMsgs[0].closedBySteer).toBe(true)
    expect(assistantMsgs[0].tool_calls ?? []).toHaveLength(0)
    expect(assistantMsgs[1].content).toBe('BBBB')
    expect((assistantMsgs[1].tool_calls ?? []).map((tc) => tc.id)).toEqual(['tc_post_steer'])
    expect(assistantMsgs[1].closedBySteer).toBeFalsy()

    // NONE left mid-stream — the done-sweep fix must fully finalize every
    // assistant message it touches, not just the (now-wrong) "last" one.
    for (const m of assistantMsgs) {
      expect(m.isStreaming).toBe(false)
      expect(m.status).not.toBe('streaming')
    }
    expect(isStreaming).toBe(false)

    // Chronology: first user msg, the finished pre-steer bubble, the steer,
    // then the new post-steer bubble — this ordering (not the old merged
    // single bubble) is the entire point of ADR-070.
    expect(messages.map((m) => m.role)).toEqual(['user', 'assistant', 'user', 'assistant'])
  })

  it('steer → done (no tool calls): unchanged — single assistant bubble, fully finalized', () => {
    const send = connectWithSendSpy()
    startTurn(send)

    act(() => {
      useChatStore.getState().handleFrame({ type: 'token', content: 'Working on it.', session_id: TEST_SESSION_ID })
    })
    act(() => {
      useChatStore.getState().sendMessage('steer this turn')
    })
    act(() => {
      useChatStore.getState().handleFrame({ type: 'done', session_id: TEST_SESSION_ID })
    })

    const { messages, isStreaming } = useChatStore.getState()
    const assistantMsgs = messages.filter((m) => m.role === 'assistant')
    expect(assistantMsgs).toHaveLength(1)
    expect(assistantMsgs[0].content).toBe('Working on it.')
    expect(assistantMsgs[0].isStreaming).toBe(false)
    expect(assistantMsgs[0].status).toBe('done')
    // ADR-070 §2.1: closed at steer-time already, before `done` ever
    // arrives — this bubble was never merely "left alone by done," it was
    // actively closed the moment the steer was sent.
    expect(assistantMsgs[0].closedBySteer).toBe(true)
    expect(isStreaming).toBe(false)
    expect(messages.map((m) => m.role)).toEqual(['user', 'assistant', 'user'])
  })

  it('a tool call BEFORE the steer and one AFTER each attach to their own respective bubble (ADR-070)', () => {
    const send = connectWithSendSpy()
    startTurn(send)

    // Pre-steer tool call — establishes ownership/offset bookkeeping on the
    // pre-steer bubble, before the raw tail ever becomes a user message.
    act(() => {
      useChatStore.getState().handleFrame({ type: 'token', content: 'AAAA', session_id: TEST_SESSION_ID })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_start',
        call_id: 'tc_pre',
        tool: 'bash',
        params: {},
        session_id: TEST_SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_result',
        call_id: 'tc_pre',
        tool: 'bash',
        result: 'ok',
        status: 'success',
        session_id: TEST_SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({ type: 'token', content: 'BBBB', session_id: TEST_SESSION_ID })
    })

    // Steer — raw tail is now the user steering message.
    act(() => {
      useChatStore.getState().sendMessage('steer this turn')
    })

    // Post-steer tool call — must NOT anchor on the pre-steer bubble (now
    // closed) and must NOT misattribute ownership; it opens its own fresh
    // bubble, positioned after the steer.
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_start',
        call_id: 'tc_post',
        tool: 'bash',
        params: {},
        session_id: TEST_SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_result',
        call_id: 'tc_post',
        tool: 'bash',
        result: 'ok',
        status: 'success',
        session_id: TEST_SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({ type: 'token', content: 'CCCC', session_id: TEST_SESSION_ID })
    })
    act(() => {
      useChatStore.getState().handleFrame({ type: 'done', session_id: TEST_SESSION_ID })
    })

    const { messages } = useChatStore.getState()
    const assistantMsgs = messages.filter((m) => m.role === 'assistant')

    // ADR-070 §2.1: each tool call attaches to whichever bubble was open
    // when IT started — tc_pre on the pre-steer bubble, tc_post on the
    // fresh post-steer bubble. Neither is dropped, misattributed, or
    // shares the other's snapshot/offset bookkeeping.
    expect(assistantMsgs).toHaveLength(2)

    const preSteerBubble = assistantMsgs[0]
    const postSteerBubble = assistantMsgs[1]

    const preToolCalls = preSteerBubble.tool_calls ?? []
    expect(preToolCalls.map((tc) => tc.id)).toEqual(['tc_pre'])
    // Snapshot taken before the post-tool-call "BBBB" segment landed
    // (offset 4 = length of "AAAA") — unaffected by the steer, since tc_pre
    // started and resolved entirely before it.
    expect((preToolCalls[0] as { textOffset?: number }).textOffset).toBe(4)
    expect(preSteerBubble.content).toBe('AAAA\n\nBBBB')
    expect(preSteerBubble.isStreaming).toBe(false)
    expect(preSteerBubble.status).toBe('done')
    expect(preSteerBubble.closedBySteer).toBe(true)

    const postToolCalls = postSteerBubble.tool_calls ?? []
    expect(postToolCalls.map((tc) => tc.id)).toEqual(['tc_post'])
    // tc_post started on a BRAND NEW, empty bubble (not a continuation of
    // the pre-steer one) — its snapshot is taken at offset 0, not 10
    // (which is what the old, pre-fix single-bubble merge would have
    // produced). Confirms tc_post did NOT anchor on the closed pre-steer
    // bubble.
    expect((postToolCalls[0] as { textOffset?: number }).textOffset).toBe(0)
    expect(postSteerBubble.content).toBe('CCCC')
    expect(postSteerBubble.isStreaming).toBe(false)
    expect(postSteerBubble.status).toBe('done')
    expect(postSteerBubble.closedBySteer).toBeFalsy()

    expect(messages.map((m) => m.role)).toEqual(['user', 'assistant', 'user', 'assistant'])
  })
})

/**
 * bugfixes3 consolidated fix round — item 2 (MEDIUM): a steer sent while
 * the FIRST message of a brand new chat is still under the '__pending'
 * placeholder session id (before session_started resolves the real
 * session_id) used to send session_id:'__pending' on the wire — a protocol
 * violation the gateway answers with a terminal error, which then
 * (incorrectly) errored out the legitimate first turn and lost the steer
 * text. Fixed by degrading to the existing offline-style buffering
 * (enqueueOutboundMessage) instead of sending.
 */
describe('sendMessage — steer during the "__pending" session window (bugfixes3 fix round, item 2)', () => {
  beforeEach(resetStores)

  it('nothing is sent on the socket, the message is buffered, and the first turn is untouched', () => {
    const send = connectWithSendSpy()

    // No active session yet — sendMessage's no-session branch renders
    // optimistically into the '__pending' bucket and sets it active.
    act(() => {
      useSessionStore.setState({ activeSessionId: null })
    })
    act(() => {
      useChatStore.getState().sendMessage('first message')
    })
    expect(useSessionStore.getState().activeSessionId).toBe('__pending')
    expect(useChatStore.getState().isStreaming).toBe(true)
    send.mockClear()

    act(() => {
      useChatStore.getState().sendMessage('steer before session_started')
    })

    // Nothing sent on the socket for the steer attempt.
    expect(send).not.toHaveBeenCalled()
    // Buffered into outboundQueue instead of lost.
    expect(useChatStore.getState().outboundQueue).toContain('steer before session_started')
    // The first turn's own bucket/messages are untouched — no stray user
    // bubble was appended for the (buffered, not sent) steer, and the
    // original placeholder assistant message is still streaming normally.
    const { messages, isStreaming } = useChatStore.getState()
    expect(messages.map((m) => m.role)).toEqual(['user', 'assistant'])
    expect(messages[0].content).toBe('first message')
    expect(
      messages.some((m) => m.content === 'steer before session_started')
    ).toBe(false)
    expect(isStreaming).toBe(true)
    expect(useConnectionStore.getState().connectionError).toBeNull()
  })

  it('drains the buffered steer as a follow-up message once session_started resolves and the first turn completes', () => {
    const send = connectWithSendSpy()

    act(() => {
      useSessionStore.setState({ activeSessionId: null })
    })
    act(() => {
      useChatStore.getState().sendMessage('first message')
    })
    send.mockClear()

    act(() => {
      useChatStore.getState().sendMessage('steer before session_started')
    })
    expect(useChatStore.getState().outboundQueue).toContain('steer before session_started')

    // The server resolves the real session_id — session_started now also
    // calls drainOutboundQueue(), which moves the buffered steer into
    // pendingDrainQueue. maybeDrainNext() (called inside) reads isStreaming
    // fresh — still true for the just-started turn — so it must NOT send yet.
    act(() => {
      useChatStore.getState().handleFrame({ type: 'session_started', session_id: 'real-session-1', agent_id: 'general-assistant' })
    })
    expect(useChatStore.getState().outboundQueue).toEqual([])
    expect(useChatStore.getState().pendingDrainQueue).toContain('steer before session_started')
    expect(send).not.toHaveBeenCalled()

    // The first turn completes — its own `done` handler's maybeDrainNext()
    // call now finds isStreaming:false and sends the buffered message as an
    // ordinary next-turn message.
    act(() => {
      useChatStore.getState().handleFrame({ type: 'done', session_id: 'real-session-1' })
    })
    expect(send).toHaveBeenCalledWith(
      expect.objectContaining({ type: 'message', content: 'steer before session_started', session_id: 'real-session-1' })
    )
    expect(useChatStore.getState().pendingDrainQueue).toEqual([])
  })
})

/**
 * ADR-070 §2.1/F2 (grill-spec round 1 on the implementation spec, F2): two
 * more live-frame handlers — subagent_start and all three media branches —
 * share the same unguarded-scan defect the original steer fix addressed in
 * token/tool_call_start. Left unguarded, a delegation span or a media
 * attachment arriving as the FIRST live frame after a steer (before any
 * token/tool_call_start has opened the post-steer bubble) would reattach to
 * the closed, closedBySteer bubble — reproducing the ordering bug for
 * delegation/media content instead of narration text. Code-review mutation
 * testing (2026-08-24) proved these five call sites were completely
 * unverified before this describe block existed — reverting each guard left
 * the full suite green.
 */
describe('mid-turn steer + subagent_start/media frames, no intervening token (ADR-070 §2.1/F2)', () => {
  beforeEach(resetStores)

  it('subagent_start immediately after a steer opens a NEW bubble, not the closed pre-steer one', () => {
    const send = connectWithSendSpy()
    startTurn(send)
    act(() => { useChatStore.getState().handleFrame({ type: 'token', content: 'AAAA', session_id: TEST_SESSION_ID }) })
    act(() => { useChatStore.getState().sendMessage('steer this turn') })

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'subagent_start',
        span_id: 'span_1',
        parent_call_id: 'delegate_1',
        task_label: 'research',
        agent_id: 'ray',
        session_id: TEST_SESSION_ID,
      })
    })

    const { messages } = useChatStore.getState()
    const assistantMsgs = messages.filter((m) => m.role === 'assistant')
    expect(assistantMsgs).toHaveLength(2)
    expect(assistantMsgs[0].closedBySteer).toBe(true)
    expect(assistantMsgs[0].spans ?? []).toHaveLength(0)
    expect(assistantMsgs[1].spans ?? []).toHaveLength(1)
    expect(assistantMsgs[1].spans?.[0]?.spanId).toBe('span_1')
    expect(messages.map((m) => m.role)).toEqual(['user', 'assistant', 'user', 'assistant'])
  })

  it('a real media attachment immediately after a steer opens a NEW bubble, not the closed pre-steer one', () => {
    const send = connectWithSendSpy()
    startTurn(send)
    act(() => { useChatStore.getState().handleFrame({ type: 'token', content: 'AAAA', session_id: TEST_SESSION_ID }) })
    act(() => { useChatStore.getState().sendMessage('steer this turn') })

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'media',
        session_id: TEST_SESSION_ID,
        parts: [{ type: 'image', url: '/media/x.png', filename: 'x.png', content_type: 'image/png' }],
      })
    })

    const { messages } = useChatStore.getState()
    const assistantMsgs = messages.filter((m) => m.role === 'assistant')
    expect(assistantMsgs).toHaveLength(2)
    expect(assistantMsgs[0].media ?? []).toHaveLength(0)
    expect(assistantMsgs[1].media?.[0]?.url).toBe('/media/x.png')
    expect(messages.map((m) => m.role)).toEqual(['user', 'assistant', 'user', 'assistant'])
  })

  /**
   * Self-verification (test-plan-and-write mutation gate): the test above
   * (non-empty pre-steer content) is NOT actually lethal against the
   * findOpenAssistantMessageId guard on this branch — reverting the guard to
   * a bare findLastAssistantMessageId scan still passes it, because the
   * downstream `canAttach = msg.isStreaming || (msg.content ?? '') === ''`
   * check independently rejects a non-empty, non-streaming bubble regardless
   * of which scan found it. Confirmed by deliberately reverting the guard
   * and re-running the suite. An EMPTY pre-steer bubble is the case that
   * actually exercises the guard: `canAttach`'s own OR-empty-content clause
   * would otherwise treat the closed, empty, closedBySteer bubble as a valid
   * attach target — this is the scenario the guard alone must prevent.
   */
  it('a real media attachment immediately after a steer with an EMPTY pre-steer bubble opens a NEW bubble, not the closed empty one', () => {
    const send = connectWithSendSpy()
    startTurn(send)
    // No token before the steer — the pre-steer bubble is empty when closed.
    act(() => { useChatStore.getState().sendMessage('steer this turn') })

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'media',
        session_id: TEST_SESSION_ID,
        parts: [{ type: 'image', url: '/media/y.png', filename: 'y.png', content_type: 'image/png' }],
      })
    })

    const { messages } = useChatStore.getState()
    const assistantMsgs = messages.filter((m) => m.role === 'assistant')
    expect(assistantMsgs).toHaveLength(2)
    expect(assistantMsgs[0].closedBySteer).toBe(true)
    expect(assistantMsgs[0].media ?? []).toHaveLength(0)
    expect(assistantMsgs[1].media?.[0]?.url).toBe('/media/y.png')
    expect(assistantMsgs[1].closedBySteer).toBeFalsy()
    expect(messages.map((m) => m.role)).toEqual(['user', 'assistant', 'user', 'assistant'])
  })

  it('an invalid-parts media notice immediately after a steer opens a NEW bubble instead of appending to the closed one', () => {
    const send = connectWithSendSpy()
    startTurn(send)
    act(() => { useChatStore.getState().handleFrame({ type: 'token', content: 'AAAA', session_id: TEST_SESSION_ID }) })
    act(() => { useChatStore.getState().sendMessage('steer this turn') })

    act(() => {
      useChatStore.getState().handleFrame({ type: 'media', session_id: TEST_SESSION_ID, parts: [] })
    })

    const { messages } = useChatStore.getState()
    const assistantMsgs = messages.filter((m) => m.role === 'assistant')
    expect(assistantMsgs).toHaveLength(2)
    expect(assistantMsgs[0].content).toBe('AAAA')
    expect(assistantMsgs[1].content).toContain('could not be displayed')
    expect(messages.map((m) => m.role)).toEqual(['user', 'assistant', 'user', 'assistant'])
  })

  it('a zero-attachments media notice immediately after a steer opens a NEW bubble instead of appending to the closed one', () => {
    const send = connectWithSendSpy()
    startTurn(send)
    act(() => { useChatStore.getState().handleFrame({ type: 'token', content: 'AAAA', session_id: TEST_SESSION_ID }) })
    act(() => { useChatStore.getState().sendMessage('steer this turn') })

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'media',
        session_id: TEST_SESSION_ID,
        parts: [{ type: 'image', url: '', filename: '', content_type: '' }],
      })
    })

    const { messages } = useChatStore.getState()
    const assistantMsgs = messages.filter((m) => m.role === 'assistant')
    expect(assistantMsgs).toHaveLength(2)
    expect(assistantMsgs[0].content).toBe('AAAA')
    expect(assistantMsgs[1].content).toContain('could not be displayed')
    expect(messages.map((m) => m.role)).toEqual(['user', 'assistant', 'user', 'assistant'])
  })
})

/**
 * ADR-070 §2.6 (grill-spec F5): a steer sent before any text has streamed
 * leaves an empty, closedBySteer bubble. The C8 error-sweep's
 * `lastContentEmpty` fallback would otherwise pull that closed bubble back
 * into scope purely because its content is empty, re-stamping it 'error' and
 * silently overwriting the correct closed state.
 */
describe('mid-turn steer + error frame C8 sweep (ADR-070 §2.6, grill-spec F5)', () => {
  beforeEach(resetStores)

  it('a steer sent before any text streamed leaves an empty closedBySteer bubble that a later error does NOT re-stamp', () => {
    const send = connectWithSendSpy()
    startTurn(send)
    // No token arrives before the steer — the pre-steer bubble is still empty.
    act(() => { useChatStore.getState().sendMessage('steer this turn') })

    act(() => {
      useChatStore.getState().handleFrame({ type: 'error', session_id: TEST_SESSION_ID, message: 'boom' })
    })

    const { messages } = useChatStore.getState()
    const assistantMsgs = messages.filter((m) => m.role === 'assistant')
    // The closed, empty pre-steer bubble is untouched — NOT re-stamped 'error'.
    expect(assistantMsgs[0].status).toBe('done')
    expect(assistantMsgs[0].closedBySteer).toBe(true)
    // A NEW bubble carries the error instead of overwriting the closed one.
    expect(assistantMsgs).toHaveLength(2)
    expect(assistantMsgs[1].status).toBe('error')
    expect(messages.map((m) => m.role)).toEqual(['user', 'assistant', 'user', 'assistant'])
  })
})

/**
 * ADR-070 §2.7 (grill-spec round 2, NEW-001): Stop/Escape/`/cancel` resolves
 * its target via the SAME unguarded scan pattern §2.1/F2 already fixed
 * elsewhere. In the gap between a steer closing the pre-steer bubble and the
 * next frame opening a new one, cancelling used to mislabel the already-
 * finished, correct reply segment as 'interrupted'.
 */
describe('mid-turn steer + cancel with no intervening frame (ADR-070 §2.7, grill-spec round 2 NEW-001)', () => {
  beforeEach(resetStores)

  it('cancelling immediately after a steer does not mislabel the closed pre-steer bubble; a NEW interrupted placeholder is created', () => {
    const send = connectWithSendSpy()
    startTurn(send)
    act(() => { useChatStore.getState().handleFrame({ type: 'token', content: 'AAAA', session_id: TEST_SESSION_ID }) })
    act(() => { useChatStore.getState().sendMessage('steer this turn') })

    act(() => { useChatStore.getState().markLastMessageInterrupted() })

    const { messages } = useChatStore.getState()
    const assistantMsgs = messages.filter((m) => m.role === 'assistant')
    expect(assistantMsgs).toHaveLength(2)
    expect(assistantMsgs[0].content).toBe('AAAA')
    expect(assistantMsgs[0].status).toBe('done')
    expect(assistantMsgs[0].closedBySteer).toBe(true)
    expect(assistantMsgs[1].status).toBe('interrupted')
    expect(assistantMsgs[1].content).toBe('')
    expect(messages.map((m) => m.role)).toEqual(['user', 'assistant', 'user', 'assistant'])
  })
})

/**
 * ADR-070 §2.7 (grill-spec round 2, NEW-002): `lastAssistantMessageId` is a
 * fifth unguarded scan site — its only consumer anywhere in the codebase is
 * ChatScreen's ARIA "New response from {agent}" live-region announcement.
 * Left unguarded, it resolved to the closed pre-steer bubble in the steer
 * gap, firing the announcement prematurely (before the agent had said
 * anything in response to the follow-up) and then again at true completion.
 */
describe('mid-turn steer + lastAssistantMessageId (ARIA announcement field) (ADR-070 §2.7, grill-spec round 2 NEW-002)', () => {
  beforeEach(resetStores)

  it('lastAssistantMessageId is null in the gap between a steer closing a bubble and the next frame opening one', () => {
    const send = connectWithSendSpy()
    startTurn(send)
    act(() => { useChatStore.getState().handleFrame({ type: 'token', content: 'AAAA', session_id: TEST_SESSION_ID }) })
    act(() => { useChatStore.getState().sendMessage('steer this turn') })

    // Not resolving to the closed bubble is exactly what suppresses the
    // premature ARIA announcement (ChatScreen.tsx's shouldAnnounce/render
    // condition both require a non-null, non-undefined message).
    expect(useChatStore.getState().lastAssistantMessageId).toBeNull()
  })

  it('lastAssistantMessageId resolves to the new post-steer bubble once it exists, not the closed pre-steer one', () => {
    const send = connectWithSendSpy()
    startTurn(send)
    act(() => { useChatStore.getState().handleFrame({ type: 'token', content: 'AAAA', session_id: TEST_SESSION_ID }) })
    act(() => { useChatStore.getState().sendMessage('steer this turn') })
    act(() => { useChatStore.getState().handleFrame({ type: 'token', content: 'BBBB', session_id: TEST_SESSION_ID }) })

    const state = useChatStore.getState()
    const newBubbleId = state.messages.find((m) => m.role === 'assistant' && m.content === 'BBBB')?.id
    expect(newBubbleId).toBeDefined()
    expect(state.lastAssistantMessageId).toBe(newBubbleId)
  })
})

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
      connection: { send, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as any,
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

  it('appends only a user message after the streaming assistant bubble — no second placeholder', () => {
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
    expect(messages[1].isStreaming).toBe(true)
    expect(messages[1].status).toBe('streaming')
    expect(messages[2].role).toBe('user')
    expect(messages[2].content).toBe('steer this turn')
    expect(messages[2].status).toBe('done')

    // Exactly ONE assistant message exists — the mid-turn send did not mint
    // a second (permanently-empty) streaming placeholder.
    expect(messages.filter((m) => m.role === 'assistant')).toHaveLength(1)

    // isStreaming is left alone — the original turn is still in flight.
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
        connection: { send, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as any,
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
    const liveToolCall = { id: 'tc_1', name: 'bash', status: 'running', params: {} } as any
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

  it('steer → tool_call_start → token → done: exactly one assistant bubble, none left status "streaming" (regression — stranded streaming bubble)', () => {
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

    // Exactly ONE assistant bubble for the whole turn — no stray second
    // placeholder minted by the mis-anchored tool_call_start.
    expect(assistantMsgs).toHaveLength(1)
    // pendingTextBoundary inserts a paragraph break between the tool call
    // and the resumed narration.
    expect(assistantMsgs[0].content).toBe('AAAA\n\nBBBB')
    expect((assistantMsgs[0].tool_calls ?? []).map((tc) => tc.id)).toEqual(['tc_post_steer'])

    // NONE left mid-stream — the done-sweep fix must fully finalize every
    // assistant message it touches, not just the (now-wrong) "last" one.
    for (const m of assistantMsgs) {
      expect(m.isStreaming).toBe(false)
      expect(m.status).not.toBe('streaming')
    }
    expect(isStreaming).toBe(false)

    // Chronology preserved: first user msg, the single assistant bubble,
    // then the steer.
    expect(messages.map((m) => m.role)).toEqual(['user', 'assistant', 'user'])
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
    expect(isStreaming).toBe(false)
    expect(messages.map((m) => m.role)).toEqual(['user', 'assistant', 'user'])
  })

  it('a tool call BEFORE the steer and one AFTER both keep correct owner/offset on the same bubble', () => {
    const send = connectWithSendSpy()
    startTurn(send)

    // Pre-steer tool call — establishes ownership/offset bookkeeping the
    // normal (pre-fix) way, before the raw tail ever becomes a user message.
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

    // Post-steer tool call — must anchor on the still-streaming assistant
    // bubble (the fix), not mint a second one or misattribute ownership.
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

    // Both tool calls landed on the SAME single bubble, in call order.
    expect(assistantMsgs).toHaveLength(1)
    const toolCalls = assistantMsgs[0].tool_calls ?? []
    expect(toolCalls.map((tc) => tc.id)).toEqual(['tc_pre', 'tc_post'])

    // Pre-steer call snapshot the text BEFORE the post-tool-call "BBBB"
    // segment (offset 4 = length of "AAAA"); post-steer call snapshot the
    // text after the paragraph-break-joined "AAAA\n\nBBBB" (offset 10).
    // Confirms the post-steer tool_call_start did NOT anchor on the user
    // steering bubble (which would have produced a fresh, wrong snapshot on
    // a brand-new placeholder instead).
    expect((toolCalls[0] as { textOffset?: number }).textOffset).toBe(4)
    expect((toolCalls[1] as { textOffset?: number }).textOffset).toBe(10)

    expect(assistantMsgs[0].content).toBe('AAAA\n\nBBBB\n\nCCCC')
    expect(assistantMsgs[0].isStreaming).toBe(false)
    expect(assistantMsgs[0].status).toBe('done')
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

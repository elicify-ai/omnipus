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

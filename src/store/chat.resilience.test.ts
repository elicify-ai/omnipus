/**
 * chat.resilience.test.ts — regression tests for stream-hang, reconnect cursor,
 * and ack-frame reducer cases.
 *
 * C8 — chat-stream-hang: a turn that never receives a terminal (done/error)
 *   frame must not wedge the UI in a permanent streaming state. clearStreamingState
 *   (called on WS close/disconnect and on an unroutable error frame) sweeps every
 *   bucket so isStreaming flips false and any still-streaming assistant message is
 *   closed.
 *
 * I1 — reconnect cursor: the `since` cursor (lastReceivedEventTime) must advance
 *   for ANY frame carrying a timestamp, not only replay_message, so reconnect does
 *   not re-replay already-seen frames.
 *
 * I2 — ack frames: device_pairing_request is a real ServerFrame type; its
 *   reducer case must NOT fall through to the unknown-frame dev toast.
 *   (exec_approval_response_ack's no-op case was removed with the rest of the
 *   exec-only approval flow — ADR-036 §3.4 retires it in favor of the generic
 *   tool_approval_required/ToolApprovalModal flow, which has no ack frame.)
 */

import { describe, it, expect, beforeEach, vi } from 'vitest'
import { act } from 'react'
import { useChatStore, type SessionChatState } from './chat'
import { useSessionStore } from './session'
import { useUiStore } from './ui'
import { queryClient } from '@/lib/queryClient'

const SID = 'resilience-test-session'

function resetStores() {
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
      lastReceivedEventTime: null,
    })
    useSessionStore.setState({
      activeSessionId: SID,
      activeAgentId: null,
      activeAgentType: null,
    })
  })
}

/** Seed a bucket that is mid-stream: isStreaming=true, a streaming assistant bubble, a running tool. */
function seedStreamingBucket(sid: string, overrides: Partial<SessionChatState> = {}) {
  const bucket: SessionChatState = {
    messagesById: {
      'a1': {
        id: 'a1',
        role: 'assistant',
        content: 'thinking…',
        timestamp: new Date().toISOString(),
        status: 'streaming',
        isStreaming: true,
      },
    },
    messageOrder: ['a1'],
    trimmedCount: 0,
    toolCalls: { 'tc1': { id: 'tc1', call_id: 'tc1', tool: 'exec', params: {}, status: 'running' } },
    toolCallOrder: ['tc1'],
    textAtToolCallStart: {},
    isStreaming: true,
    isReplaying: false,
    replayCompletedForSession: null,
    sessionTokens: 0,
    sessionCost: 0,
    rateLimitEvent: null,
    lastUserMessageAt: Date.now(),
    cancelStage: 'graceful',
    lastReceivedEventTime: null,
    spanByParentCallId: {},
    ...overrides,
  }
  act(() => {
    useChatStore.setState((s) => ({ sessionsById: { ...s.sessionsById, [sid]: bucket } }))
  })
}

beforeEach(() => {
  resetStores()
  vi.restoreAllMocks()
})

describe('C8 — clearStreamingState resolves a stuck stream', () => {
  it('flips isStreaming false and closes the streaming assistant message', () => {
    seedStreamingBucket(SID)
    act(() => {
      useChatStore.getState().clearStreamingState()
    })
    const bucket = useChatStore.getState().sessionsById[SID]
    expect(bucket.isStreaming).toBe(false)
    expect(bucket.cancelStage).toBeNull()
    const msg = bucket.messagesById['a1']
    expect(msg.isStreaming).toBe(false)
    expect(msg.status).toBe('done')
    // Running tool calls are flipped to cancelled and baked into the
    // finalized message's tool_calls (see the dedicated baking test below);
    // the live bucket map is cleared so nothing renders as in-flight via the
    // stale live-bucket path.
    expect(bucket.toolCalls).toEqual({})
    expect(msg.tool_calls?.find((tc) => tc.id === 'tc1')?.status).toBe('cancelled')
    // Foreground projection is synced.
    expect(useChatStore.getState().isStreaming).toBe(false)
  })

  it('bakes in-flight tool calls into the finalized message, not just cancels them in place', () => {
    // Regression test: once isStreaming flips false, the historical renderer
    // (VirtualAssistantMessageRow) reads message.tool_calls instead of the
    // live bucket.toolCalls map. Before the fix, the WS-disconnect fallback
    // only flipped bucket.toolCalls['tc1'].status to 'cancelled' in place and
    // never baked it into messagesById['a1'].tool_calls — so the in-flight
    // tool call vanished from the UI the instant the stream was force-closed.
    seedStreamingBucket(SID)
    act(() => {
      useChatStore.getState().clearStreamingState()
    })
    const bucket = useChatStore.getState().sessionsById[SID]
    const msg = bucket.messagesById['a1']
    expect(msg.tool_calls).toEqual([
      { id: 'tc1', tool: 'exec', params: {}, result: undefined, status: 'cancelled', duration_ms: undefined, error: undefined },
    ])
    // The live bucket state must be cleared too, so it doesn't leak into the
    // next turn (mirrors the `done` frame handler's baking contract).
    expect(bucket.toolCalls).toEqual({})
    expect(bucket.toolCallOrder).toEqual([])
  })

  it('preserves an already-baked tool_calls entry when baking a second in-flight call on disconnect', () => {
    // If the message already has a baked tool call (e.g. from an earlier
    // partial bake) and a different call is still live when disconnect
    // fires, both must survive in the merged tool_calls array.
    seedStreamingBucket(SID, {
      messagesById: {
        'a1': {
          id: 'a1',
          role: 'assistant',
          content: 'thinking…',
          timestamp: new Date().toISOString(),
          status: 'streaming',
          isStreaming: true,
          tool_calls: [{ id: 'tc0', tool: 'read_file', params: {}, status: 'success' }],
        },
      },
    })
    act(() => {
      useChatStore.getState().clearStreamingState()
    })
    const msg = useChatStore.getState().sessionsById[SID].messagesById['a1']
    expect(msg.tool_calls?.map((tc) => tc.id)).toEqual(['tc0', 'tc1'])
    expect(msg.tool_calls?.find((tc) => tc.id === 'tc1')?.status).toBe('cancelled')
  })

  it('bakes an already-resolved (non-running) tool call on disconnect, not just still-running ones', () => {
    // Regression test: a tool_call_result frame commonly arrives before the
    // trailing assistant text finishes streaming, flipping the tool call to a
    // terminal status ('success'/'error') while the message is still
    // isStreaming=true. If the WS drops in that window, the old
    // `hasRunningTools` gate (status === 'running' only) skipped the entire
    // bake-and-clear block, and the already-resolved tool call silently
    // vanished once isStreaming flipped false and the historical renderer
    // took over (it reads message.tool_calls, not the live bucket).
    seedStreamingBucket(SID, {
      toolCalls: { 'tc1': { id: 'tc1', call_id: 'tc1', tool: 'exec', params: {}, status: 'success', result: 'ok' } },
    })
    act(() => {
      useChatStore.getState().clearStreamingState()
    })
    const bucket = useChatStore.getState().sessionsById[SID]
    const msg = bucket.messagesById['a1']
    // The already-terminal status is preserved as-is (only 'running' entries
    // are flipped to 'cancelled' — a resolved call should not be relabeled).
    expect(msg.tool_calls).toEqual([
      { id: 'tc1', tool: 'exec', params: {}, result: 'ok', status: 'success', duration_ms: undefined, error: undefined },
    ])
    expect(bucket.toolCalls).toEqual({})
    expect(bucket.toolCallOrder).toEqual([])
  })

  it('sweeps background buckets, not only the active one', () => {
    const OTHER = 'other-session'
    seedStreamingBucket(SID)
    seedStreamingBucket(OTHER)
    act(() => {
      useChatStore.getState().clearStreamingState()
    })
    expect(useChatStore.getState().sessionsById[SID].isStreaming).toBe(false)
    expect(useChatStore.getState().sessionsById[OTHER].isStreaming).toBe(false)
  })

  it('preserves an already-interrupted status (does not overwrite with done)', () => {
    seedStreamingBucket(SID, {
      messagesById: {
        'a1': {
          id: 'a1',
          role: 'assistant',
          content: 'partial',
          timestamp: new Date().toISOString(),
          status: 'interrupted',
          isStreaming: true,
        },
      },
    })
    act(() => {
      useChatStore.getState().clearStreamingState()
    })
    expect(useChatStore.getState().sessionsById[SID].messagesById['a1'].status).toBe('interrupted')
  })

  it('a terminal error frame for the active turn clears its streaming state', () => {
    seedStreamingBucket(SID)
    act(() => {
      useChatStore.getState().handleFrame({ type: 'error', session_id: SID, message: 'gateway exploded' })
    })
    const bucket = useChatStore.getState().sessionsById[SID]
    expect(bucket.isStreaming).toBe(false)
    expect(bucket.messagesById['a1'].status).toBe('error')
    expect(bucket.messagesById['a1'].isStreaming).toBe(false)
  })

  // D5 fix (UAT "Site 3"): the coalesce-into-existing-streaming-bubble path
  // (a1 is already streaming, unlike the fresh-bubble case covered in
  // chat.llm-error.test.ts) must ALSO sanitize a Go-internal-jargon-shaped
  // legacy frame.message instead of stamping it verbatim as the closed
  // bubble's content. The bubble must have EMPTY content (an unstarted
  // placeholder) — `msg.content || fallbackContent` only reaches
  // fallbackContent when there is no partial content yet (see the
  // reducer's own comment); seedStreamingBucket's default 'a1' already has
  // content:'thinking…', so this overrides it to '' to actually exercise
  // the fallback path.
  it('D5: sanitizes a Go-internal-jargon-shaped frame.message when coalescing into an existing (empty-content) streaming bubble', () => {
    seedStreamingBucket(SID, {
      messagesById: {
        'a1': {
          id: 'a1',
          role: 'assistant',
          content: '',
          timestamp: new Date().toISOString(),
          status: 'streaming',
          isStreaming: true,
        },
      },
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'error',
        session_id: SID,
        message: 'browser_attach: agent_id and session_id are required',
      })
    })
    const bucket = useChatStore.getState().sessionsById[SID]
    expect(bucket.messagesById['a1'].content).toBe('Something went wrong — please try again.')
    expect(bucket.messagesById['a1'].content).not.toContain('browser_attach')
  })
})

describe('I1 — reconnect cursor advances for any timestamped frame', () => {
  it('advances lastReceivedEventTime on a replay_message carrying a timestamp', () => {
    const ts = '2026-06-14T12:00:00.000Z'
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message',
        session_id: SID,
        role: 'assistant',
        content: 'hi',
        timestamp: ts,
      })
    })
    expect(useChatStore.getState().sessionsById[SID].lastReceivedEventTime).toBe(ts)
  })

  it('is monotonic — an older timestamp does not move the cursor backwards', () => {
    const newer = '2026-06-14T12:00:00.000Z'
    const older = '2026-06-14T11:00:00.000Z'
    act(() => {
      useChatStore.getState().handleFrame({ type: 'replay_message', session_id: SID, role: 'assistant', content: 'a', timestamp: newer })
      useChatStore.getState().handleFrame({ type: 'replay_message', session_id: SID, role: 'user', content: 'b', timestamp: older })
    })
    expect(useChatStore.getState().sessionsById[SID].lastReceivedEventTime).toBe(newer)
  })
})

describe('I2 — ack frames do not trip the unknown-frame toast', () => {
  it('device_pairing_request invalidates the devices query and does not toast', () => {
    const addToast = vi.fn()
    vi.spyOn(useUiStore, 'getState').mockReturnValue({
      addToast,
    } as unknown as ReturnType<typeof useUiStore.getState>)
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries').mockImplementation(() => Promise.resolve())
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'device_pairing_request',
        device_id: 'dev-1',
        fingerprint: 'fp',
        pairing_code: '123456',
      })
    })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['devices'] })
    expect(addToast).not.toHaveBeenCalled()
  })
})

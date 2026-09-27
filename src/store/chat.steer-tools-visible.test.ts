/**
 * chat.steer-tools-visible.test.ts — founder-reported hotfix (2026-09-26):
 * "tool rows vanish when a message is sent mid-turn".
 *
 * Evidence pack (real-browser repro + WS frame logs this scenario mirrors):
 *   /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-evidence/steer-tools-2026-09-26/
 *   (ist-repro.mjs; new-a1-log.txt: rows go 3 -> 0 the instant a mid-turn
 *   steer is sent; zz-investigate-steer-tools.test.ts — the investigation
 *   harness this file promotes off its zz-/INVESTIGATION ONLY marker).
 *
 * Root cause (two halves, both required):
 *   1. outbound-lifecycle.ts::sendMessage's mid-turn branch closed the
 *      open assistant bubble (isStreaming=false, closedBySteer=true) WITHOUT
 *      baking the tool calls it owned from toolCallOrder into its
 *      tool_calls — so the bubble, now rendered by the HISTORICAL renderer
 *      (VirtualAssistantMessageRow reads ONLY message.tool_calls), showed
 *      0 rows until turn end. Fix: bake-in-place the closing bubble's OWNED
 *      calls at close time, KEEPING the live entries in toolCallOrder (a
 *      late tool_call_result still lands in the live map, which the
 *      renderer resolves through; turn-end's bake re-merges cleanly).
 *   2. omnipus-runtime.ts::buildContentParts attached ALL live calls in
 *      toolCallOrder to the NEWEST bubble with no ownership check — so the
 *      orphaned pre-steer rows reappeared stuck under the wrong (new)
 *      message. Fix: attach a live call only if toolCallOwnerMessageId has
 *      no entry for it (legacy fallback) or names THIS message.
 *
 * The projection helper below models ChatScreen's render split (see
 * VirtualizedMessageListInner): historical rows read ONLY
 * message.tool_calls; the LIVE row (only while the LAST message is
 * streaming) renders the message's baked calls plus the live toolCallOrder
 * entries owned by it (the ownership rule buildContentParts applies).
 */

import { beforeEach, describe, expect, it, vi } from 'vitest'
import { act } from 'react'
import { useChatStore } from './chat'
import { useConnectionStore } from './connection'
import { useSessionStore } from './session'
import { useWorkspacesStore } from './workspacesStore'
import type { WsConnection } from '@/lib/ws'

const SID = 'sess_steer_tools_test'
const TURN = 'turn-1'

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
      activeSessionId: SID,
      activeAgentId: 'mia',
      activeAgentType: null,
    })
    useWorkspacesStore.setState({ activeWorkspaceId: null })
  })
}

beforeEach(resetStores)

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

function f(frame: Record<string, unknown>) {
  act(() => {
    useChatStore.getState().handleFrame({ session_id: SID, agent_id: 'mia', ...frame } as never)
  })
}

/** Per-message tool ids as ChatScreen renders them (renderer model — see
 * file header). Returns one entry per message, ordered as the thread. */
function rendered(): { id: string; role: string; toolIds: string[] }[] {
  const s = useChatStore.getState()
  const b = s.sessionsById[SID]!
  const msgs = b.messageOrder.map((id) => b.messagesById[id])
  const last = msgs[msgs.length - 1]
  const hasStreamingMessage = s.isStreaming && !!last?.isStreaming
  const owners = b.toolCallOwnerMessageId ?? {}
  return msgs.map((m, i) => {
    const isLive = hasStreamingMessage && i === msgs.length - 1
    const baked = (m.tool_calls ?? []).map((tc) => tc.id)
    const live = isLive
      ? b.toolCallOrder.filter((id) => !baked.includes(id) && (owners[id] === undefined || owners[id] === m.id))
      : []
    return { id: m.id, role: m.role, toolIds: [...baked, ...live] }
  })
}

function assistantBubbles(): { id: string; role: string; toolIds: string[] }[] {
  return rendered().filter((r) => r.role === 'assistant')
}

describe('mid-turn steer keeps the closing bubble’s tool rows visible (founder-reported 2026-09-26)', () => {
  it('first reply keeps all 3 rows after a mid-turn send; the next tool_call_start lands ONLY on the second reply', () => {
    connectWithSendSpy()

    // Turn 1 opens: the first token registers the server's message_id onto
    // the optimistic placeholder bubble (the bubble keeps its client id).
    act(() => { useChatStore.getState().sendMessage('list the dir, read two files, summarise') })
    f({ type: 'token', content: 'Let me look.', message_id: 'm1', turn_id: TURN, seq: 1 })
    f({ type: 'tool_call_start', call_id: 'tc1', tool: 'list_directory', params: { path: '.' }, seq: 2 })
    f({ type: 'tool_call_result', call_id: 'tc1', tool: 'list_directory', result: 'a\\nb', status: 'success', seq: 3 })
    f({ type: 'tool_call_start', call_id: 'tc2', tool: 'read_file', params: { path: 'a' }, seq: 4 })
    f({ type: 'tool_call_result', call_id: 'tc2', tool: 'read_file', result: 'A', status: 'success', seq: 5 })
    f({ type: 'tool_call_start', call_id: 'tc3', tool: 'read_file', params: { path: 'b' }, seq: 6 })

    // Baseline: the live first-reply bubble shows all three rows (tc3 running).
    const m1Id = assistantBubbles()[0]!.id
    expect(assistantBubbles().map((b) => b.toolIds)).toEqual([['tc1', 'tc2', 'tc3']])
    // Live bookkeeping: all three owned by the first bubble, none baked yet.
    const preSteer = useChatStore.getState().sessionsById[SID]!
    expect(preSteer.toolCallOrder).toEqual(['tc1', 'tc2', 'tc3'])
    expect(preSteer.toolCallOwnerMessageId).toEqual({ tc1: m1Id, tc2: m1Id, tc3: m1Id })
    expect((preSteer.messagesById[m1Id]!.tool_calls ?? [])).toHaveLength(0)

    // -- The steer (mid-turn send) ----------------------------------------
    act(() => { useChatStore.getState().sendMessage('also mention the file sizes') })

    // FIX 1: the closing bubble keeps its OWNED rows - baked in place at
    // close time, while the live entries stay queued for a late result.
    const postSteer = useChatStore.getState().sessionsById[SID]!
    expect((postSteer.messagesById[m1Id]!.tool_calls ?? []).map((tc) => tc.id)).toEqual(['tc1', 'tc2', 'tc3'])
    expect(postSteer.messagesById[m1Id]!.closedBySteer).toBe(true)
    expect(postSteer.toolCallOrder).toEqual(['tc1', 'tc2', 'tc3'])
    expect(postSteer.toolCalls['tc3']!.status).toBe('running')
    // The steer's user bubble is appended after the first reply - so it now
    // renders via the HISTORICAL renderer, which reads ONLY
    // message.tool_calls. The measured symptom was exactly here: rows 3 -> 0.
    expect(assistantBubbles().map((b) => b.toolIds)).toEqual([['tc1', 'tc2', 'tc3']])

    // A late result for the still-running pre-steer call updates the live
    // record (turn-end's bake and the runtime renderer resolve through it).
    f({ type: 'tool_call_result', call_id: 'tc3', tool: 'read_file', result: 'B', status: 'success', seq: 7 })
    const afterLateResult = useChatStore.getState().sessionsById[SID]!
    expect(afterLateResult.toolCalls['tc3']!.status).toBe('success')
    // Rows stay put after the late result too.
    expect(assistantBubbles().map((b) => b.toolIds)).toEqual([['tc1', 'tc2', 'tc3']])

    // -- The next tool_call_start opens the SECOND reply -------------------
    f({ type: 'tool_call_start', call_id: 'tc9', tool: 'bash', params: {}, seq: 8 })

    const afterNewCall = useChatStore.getState().sessionsById[SID]!
    const m2Id = assistantBubbles()[1]!.id
    expect(m2Id).not.toBe(m1Id)
    // FIX 2's store contract: ownership is per-bubble and PRESERVED across
    // the steer - the old calls still belong to the first bubble, the new
    // one to the second.
    expect(afterNewCall.toolCallOwnerMessageId).toEqual({
      tc1: m1Id, tc2: m1Id, tc3: m1Id, tc9: m2Id,
    })
    // First reply still shows its three rows (including the resolved tc3).
    // Second reply shows ONLY the new call - not the old three again.
    expect(assistantBubbles().map((b) => b.toolIds)).toEqual([
      ['tc1', 'tc2', 'tc3'],
      ['tc9'],
    ])

    // -- Turn end: done re-bakes by owner and wipes the live maps ----------
    f({ type: 'tool_call_result', call_id: 'tc9', tool: 'bash', result: 'ok', status: 'success', seq: 9 })
    f({ type: 'token', content: 'Sure - sizes:', message_id: 'm2', turn_id: TURN, seq: 10 })
    f({ type: 'done', message_id: 'm2', turn_id: TURN, seq: 11, stats: { tokens: 1, cost: 0 } })

    const afterDone = useChatStore.getState().sessionsById[SID]!
    expect((afterDone.messagesById[m1Id]!.tool_calls ?? []).map((tc) => tc.id)).toEqual(['tc1', 'tc2', 'tc3'])
    expect((afterDone.messagesById[m2Id]!.tool_calls ?? []).map((tc) => tc.id)).toEqual(['tc9'])
    expect(afterDone.toolCallOrder).toEqual([])
    expect(useChatStore.getState().isStreaming).toBe(false)
    // No duplicate ids across bubbles after the turn-end re-bake.
    expect(assistantBubbles().map((b) => b.toolIds)).toEqual([
      ['tc1', 'tc2', 'tc3'],
      ['tc9'],
    ])
  })

  it('a steer with NO live tool calls closes the bubble without touching tool_calls (pre-existing ADR-070 path unchanged)', () => {
    connectWithSendSpy()
    act(() => { useChatStore.getState().sendMessage('go') })
    f({ type: 'token', content: 'Looking.', message_id: 'm1', turn_id: TURN, seq: 1 })
    act(() => { useChatStore.getState().sendMessage('steer') })

    const post = useChatStore.getState().sessionsById[SID]!
    const aId = post.messageOrder.filter((id) => post.messagesById[id]!.role === 'assistant').at(-1)!
    expect((post.messagesById[aId]!.tool_calls ?? [])).toHaveLength(0)
    expect(post.messagesById[aId]!.closedBySteer).toBe(true)
    expect(post.toolCallOrder).toEqual([])
  })
})

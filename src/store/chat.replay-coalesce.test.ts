// Regression test for the multi-turn replay tool-call coalesce bug.
//
// Symptom: In a two-turn session replay, tool calls from turn 1 were baked onto
// turn 2's assistant message instead of turn 1's. Turn 1's assistant message
// rendered with no tool calls; turn 2's assistant message rendered with both turns'
// tool calls.
//
// Root cause: The `replay_message(assistant)` coalesce path in chat.ts returned
// early when it coalesced text into an empty assistant placeholder, WITHOUT first
// baking the pending toolCallOrder into that placeholder's `tool_calls` field.
// toolCallOrder accumulated across turns and was only flushed at the `done` frame,
// by which point all tool calls were attributed to the LAST assistant message.
//
// Fix: the coalesce path now bakes pending toolCallOrder into the empty placeholder
// before taking the early return, exactly mirroring the non-coalesce bake path
// (chat.ts lines 2119–2134).

import { describe, it, expect, beforeEach } from 'vitest'
import { act } from 'react'
import { useChatStore } from './chat'
import { useSessionStore } from './session'

const SESSION_ID = 'replay-coalesce-test'

function resetStore() {
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
      lastUserMessageAt: null,
      cancelStage: null,
      lastReceivedEventTime: null,
    })
    useSessionStore.setState({
      activeSessionId: SESSION_ID,
      activeAgentId: null,
      activeAgentType: null,
    })
  })
}

beforeEach(resetStore)

describe('chat store — multi-turn replay coalesce (regression)', () => {
  it('turn-1 tool calls are baked into turn-1 assistant, not turn-2 (coalesce path)', () => {
    // ── Turn 1 ───────────────────────────────────────────────────────────────
    // Replay sequence: user msg → tool_call_start → tool_call_result → assistant msg
    // tool_call_start creates an empty assistant placeholder;
    // replay_message(assistant) coalesces into it (content was '').
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message',
        role: 'user',
        content: 'first question',
        session_id: SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_start',
        call_id: 'tc_r1',
        tool: 'read_file',
        params: { path: '/a.txt' },
        session_id: SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_result',
        call_id: 'tc_r1',
        tool: 'read_file',
        result: { content: 'hello' },
        status: 'success',
        session_id: SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message',
        role: 'assistant',
        content: 'turn 1 response',
        session_id: SESSION_ID,
      })
    })

    // BUG REGRESSION: After turn 1's replay_message, tc_r1 must already be baked
    // into the turn-1 assistant message, and toolCallOrder must be cleared.
    const afterTurn1 = useChatStore.getState()
    const assistantsT1 = afterTurn1.messages.filter((m) => m.role === 'assistant')
    expect(assistantsT1).toHaveLength(1)
    expect(assistantsT1[0].content).toBe('turn 1 response')
    expect(assistantsT1[0].tool_calls?.map((tc) => tc.id)).toEqual(['tc_r1'])
    // toolCallOrder must be flushed — not leaked into turn 2
    expect(afterTurn1.toolCallOrder).toEqual([])

    // ── Turn 2 ───────────────────────────────────────────────────────────────
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message',
        role: 'user',
        content: 'second question',
        session_id: SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_start',
        call_id: 'tc_r2',
        tool: 'write_file',
        params: { path: '/b.txt', content: 'world' },
        session_id: SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_result',
        call_id: 'tc_r2',
        tool: 'write_file',
        result: { ok: true },
        status: 'success',
        session_id: SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message',
        role: 'assistant',
        content: 'turn 2 response',
        session_id: SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({ type: 'done', session_id: SESSION_ID })
    })

    // ── Final assertions ─────────────────────────────────────────────────────
    const finalState = useChatStore.getState()
    const assistants = finalState.messages.filter((m) => m.role === 'assistant')
    expect(assistants).toHaveLength(2)

    const [turn1Asst, turn2Asst] = assistants

    // Turn 1: owns exactly tc_r1, nothing from turn 2
    expect(turn1Asst.content).toBe('turn 1 response')
    expect(turn1Asst.tool_calls?.map((tc) => tc.id)).toEqual(['tc_r1'])
    expect(turn1Asst.tool_calls?.map((tc) => tc.tool)).toEqual(['read_file'])

    // Turn 2: owns exactly tc_r2, nothing from turn 1
    expect(turn2Asst.content).toBe('turn 2 response')
    expect(turn2Asst.tool_calls?.map((tc) => tc.id)).toEqual(['tc_r2'])
    expect(turn2Asst.tool_calls?.map((tc) => tc.tool)).toEqual(['write_file'])

    // Live maps completely drained after done
    expect(finalState.toolCallOrder).toEqual([])
    expect(finalState.toolCalls).toEqual({})
  })

  it('three-turn replay: each assistant message owns only its own tool calls', () => {
    const turns = [
      { user: 'q1', tool: 'tc_a', toolName: 'shell', response: 'r1' },
      { user: 'q2', tool: 'tc_b', toolName: 'list_dir', response: 'r2' },
      { user: 'q3', tool: 'tc_c', toolName: 'read_file', response: 'r3' },
    ]

    for (const t of turns) {
      act(() => {
        useChatStore.getState().handleFrame({
          type: 'replay_message', role: 'user', content: t.user, session_id: SESSION_ID,
        })
      })
      act(() => {
        useChatStore.getState().handleFrame({
          type: 'tool_call_start', call_id: t.tool, tool: t.toolName, params: {}, session_id: SESSION_ID,
        })
      })
      act(() => {
        useChatStore.getState().handleFrame({
          type: 'tool_call_result', call_id: t.tool, tool: t.toolName, result: {}, status: 'success', session_id: SESSION_ID,
        })
      })
      act(() => {
        useChatStore.getState().handleFrame({
          type: 'replay_message', role: 'assistant', content: t.response, session_id: SESSION_ID,
        })
      })
    }

    act(() => {
      useChatStore.getState().handleFrame({ type: 'done', session_id: SESSION_ID })
    })

    const state = useChatStore.getState()
    const assistants = state.messages.filter((m) => m.role === 'assistant')
    expect(assistants).toHaveLength(3)

    for (let i = 0; i < 3; i++) {
      const asst = assistants[i]
      const t = turns[i]
      expect(asst.content).toBe(t.response)
      expect(asst.tool_calls).toHaveLength(1)
      expect(asst.tool_calls![0].id).toBe(t.tool)
      expect(asst.tool_calls![0].tool).toBe(t.toolName)
    }
  })
})

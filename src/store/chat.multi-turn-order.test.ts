// Regression test for the multi-turn tool-call ordering bug.
//
// Symptom (reported 2026-04-29): turn 1's tool calls visually relocate to the
// bottom of turn 2's assistant message after the user sends a second message.
//
// Root cause: `toolCallOrder` and `toolCalls` are session-scoped and never
// associated with a specific assistant message. `buildContentParts` in
// omnipus-runtime treats the LAST assistant as owner of all live tool calls.
// When turn 2 begins, the new assistant placeholder becomes "last" and pulls
// turn 1's still-live tool calls under it.
//
// Fix: when `sendMessage` appends the new turn (user + fresh assistant
// placeholder), bake the previous assistant's live tool calls into its own
// `tool_calls` field and clear the live state.

import { describe, it, expect, beforeEach, vi } from 'vitest'
import { act } from 'react'
import { useChatStore } from './chat'
import { useConnectionStore } from './connection'
import { useSessionStore } from './session'

describe('chat store — multi-turn tool-call ordering (regression)', () => {
  beforeEach(() => {
    act(() => { useChatStore.getState().resetSession() })
  })

  it('turn-1 tool calls stay attached to turn-1 assistant after turn-2 starts', () => {
    // Stub a connected WS and an active session — sendMessage requires both.
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
      activeSessionId: 'sess-test',
    })

    // ── Turn 1 ───────────────────────────────────────────────────────────────
    act(() => { useChatStore.getState().sendMessage('first user message') })

    // Simulate the assistant streaming two tool calls.
    act(() => {
      useChatStore.getState().handleFrame({ type: 'token', content: 'doing things ', session_id: 'sess-test' })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_start',
        call_id: 'tc_t1_a',
        tool: 'write_file',
        params: { path: '/x' },
        session_id: 'sess-test',
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_result',
        call_id: 'tc_t1_a',
        tool: 'write_file',
        result: { ok: true },
        status: 'success',
        session_id: 'sess-test',
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_start',
        call_id: 'tc_t1_b',
        tool: 'serve_workspace',
        params: { path: '/x' },
        session_id: 'sess-test',
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_result',
        call_id: 'tc_t1_b',
        tool: 'serve_workspace',
        result: { url: 'http://example' },
        status: 'success',
        session_id: 'sess-test',
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({ type: 'token', content: 'done.', session_id: 'sess-test' })
    })
    act(() => { useChatStore.getState().handleFrame({ type: 'done', session_id: 'sess-test' }) })

    // After done, tool calls are baked onto the assistant message immediately
    // so VirtualAssistantMessageRow can render them. toolCallOrder is cleared.
    const afterTurn1 = useChatStore.getState()
    const turn1Assistant = afterTurn1.messages.find((m) => m.role === 'assistant')
    expect(turn1Assistant?.tool_calls?.map((tc) => tc.id)).toEqual(['tc_t1_a', 'tc_t1_b'])
    expect(afterTurn1.toolCallOrder).toEqual([])

    // ── Turn 2 starts ────────────────────────────────────────────────────────
    act(() => { useChatStore.getState().sendMessage('second user message') })

    const afterTurn2Start = useChatStore.getState()

    // The previous assistant message MUST now own its tool calls via the
    // baked `tool_calls` field — not via the global live map.
    const assistants = afterTurn2Start.messages.filter((m) => m.role === 'assistant')
    expect(assistants).toHaveLength(2)

    const prevAssistant = assistants[0]
    expect(prevAssistant.tool_calls?.map((tc) => tc.id)).toEqual(['tc_t1_a', 'tc_t1_b'])
    expect(prevAssistant.tool_calls?.[0].tool).toBe('write_file')
    expect(prevAssistant.tool_calls?.[1].tool).toBe('serve_workspace')

    // The new turn-2 assistant placeholder must NOT have inherited turn-1's
    // tool calls — its `tool_calls` should be undefined or empty.
    const newAssistant = assistants[1]
    expect(newAssistant.tool_calls ?? []).toEqual([])

    // Live maps were cleared so the next turn's `liveIds` starts empty.
    expect(afterTurn2Start.toolCallOrder).toEqual([])
    expect(afterTurn2Start.toolCalls).toEqual({})
  })

  it('a tool call started in turn 2 does not appear under turn 1', () => {
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
      activeSessionId: 'sess-test',
    })

    // Turn 1 with one tool call.
    act(() => { useChatStore.getState().sendMessage('m1') })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_start',
        call_id: 'tc_t1',
        tool: 'a',
        params: {},
        session_id: 'sess-test',
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_result',
        call_id: 'tc_t1',
        tool: 'a',
        result: 1,
        status: 'success',
        session_id: 'sess-test',
      })
    })
    act(() => { useChatStore.getState().handleFrame({ type: 'done', session_id: 'sess-test' }) })

    // Turn 2 with one tool call.
    act(() => { useChatStore.getState().sendMessage('m2') })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_start',
        call_id: 'tc_t2',
        tool: 'b',
        params: {},
        session_id: 'sess-test',
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_result',
        call_id: 'tc_t2',
        tool: 'b',
        result: 2,
        status: 'success',
        session_id: 'sess-test',
      })
    })

    const state = useChatStore.getState()
    const assistants = state.messages.filter((m) => m.role === 'assistant')
    expect(assistants).toHaveLength(2)

    // Turn 1 owns tc_t1; turn 2's live map owns tc_t2.
    expect(assistants[0].tool_calls?.map((tc) => tc.id)).toEqual(['tc_t1'])
    expect(state.toolCallOrder).toEqual(['tc_t2'])
    expect(state.toolCalls['tc_t2']).toBeDefined()
    expect(state.toolCalls['tc_t1']).toBeUndefined()
  })
})

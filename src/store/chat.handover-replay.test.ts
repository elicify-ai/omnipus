// Regression test for the handover-reload identity bug.
//
// Symptom (UAT, 2026-06-10): after reloading a session where Mia handed over to
// Jim, the transcript replay re-attributed every assistant turn to the session's
// active/default agent instead of each message's true author. Jim's post-handover
// turns rendered as Mia.
//
// Root cause (store side): replay frames must stamp each assistant message with
// its own `agent_id` so the renderer (VirtualAssistantMessageRow) resolves the
// name/avatar from message.agentId — independent of the session's activeAgentId.
//
// This test asserts the store preserves per-message authorship on WS replay:
//   - Mia's pre-handover turn  → message.agentId === 'mia'
//   - Jim's post-handover turn → message.agentId === 'jim'
// regardless of the session's activeAgentId.

import { describe, it, expect, beforeEach } from 'vitest'
import { act } from 'react'
import { useChatStore } from './chat'
import { useSessionStore } from './session'

const SID = 'sess-handover'

describe('chat store — handover replay preserves per-message author', () => {
  beforeEach(() => {
    act(() => {
      useChatStore.setState({
        sessionsById: {},
        messages: [],
        isStreaming: false,
        isReplaying: false,
        toolCalls: {},
        toolCallOrder: [],
        textAtToolCallStart: {},
        sessionTokens: 0,
        sessionCost: 0,
      })
      // The session's active agent is Jim (the last-active after handover), but
      // each replayed message must keep ITS OWN author — not borrow this value.
      useSessionStore.setState({ activeSessionId: SID, activeAgentId: 'jim' })
    })
  })

  it('stamps each replayed assistant message with its own agent_id', () => {
    act(() => {
      // Mia's turn before the handover.
      useChatStore.getState().handleFrame({
        type: 'replay_message',
        id: 'm-mia',
        role: 'assistant',
        content: 'Handing this over to Jim.',
        agent_id: 'mia',
        timestamp: '2026-06-10T10:00:00Z',
        session_id: SID,
      })
      // Jim's turn after the handover.
      useChatStore.getState().handleFrame({
        type: 'replay_message',
        id: 'm-jim',
        role: 'assistant',
        content: 'On it.',
        agent_id: 'jim',
        timestamp: '2026-06-10T10:01:00Z',
        session_id: SID,
      })
    })

    const state = useChatStore.getState()
    const mia = state.messages.find((m) => m.id === 'm-mia')
    const jim = state.messages.find((m) => m.id === 'm-jim')

    expect(mia).toBeDefined()
    expect(jim).toBeDefined()
    // Mutation check: Mia's turn must NOT be re-attributed to the active agent.
    expect(mia!.agentId).toBe('mia')
    expect(mia!.agentId).not.toBe('jim')
    expect(jim!.agentId).toBe('jim')
  })
})

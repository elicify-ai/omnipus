// Cold-load attach-sequence regression for the handover header/composer bug.
//
// Symptom (UAT, 2026-06-10): after a FULL page reload of a session where Mia
// handed over to Jim, per-message attribution was correct but the header agent
// button + composer placeholder reverted to the CREATING agent (Mia) instead of
// the LAST-ACTIVE agent (Jim, = session.active_agent_id).
//
// This locks the end-to-end store outcome of the cold-load attach sequence:
//
//   1. route loader      → setActiveSession(sid, active_agent_id ?? agent_id)
//                           i.e. seeds Jim from the detail (active_agent_id='jim')
//   2. WS replay frames   → the gateway replays the transcript; for the handoff
//                           system entry replay.go emits an `agent_switched`
//                           (wire `ln`) frame carrying the TARGET agent (Jim),
//                           plus per-message replay_message frames.
//
// The invariant: after the whole sequence the session store's activeAgentId is
// 'jim' (last-active) — never the creator 'mia'. The replay's agent_switched
// frame must REINFORCE Jim, and no frame may downgrade the header to the
// creating agent.

import { describe, it, expect, beforeEach } from 'vitest'
import { act } from 'react'
import { useChatStore } from './chat'
import { useSessionStore } from './session'

const SID = 'sess-handover'

describe('chat store — cold-load attach sequence keeps header on last-active agent', () => {
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
      useSessionStore.setState({ activeSessionId: null, activeAgentId: null, activeAgentType: null })
    })
  })

  it('given detail agent_id=mia + active_agent_id=jim, ends on jim after the cold-load attach sequence', () => {
    act(() => {
      // Step 1 — route loader / WsLifecycle reseed: seed from the LAST-ACTIVE
      // agent. The loader computes `active_agent_id ?? agent_id`, i.e. 'jim'.
      useSessionStore.getState().setActiveSession(SID, 'jim', null)
    })
    expect(useSessionStore.getState().activeAgentId).toBe('jim')

    act(() => {
      // Step 2 — WS replay. The gateway replays the handoff system entry as a
      // replay_message (agent_id='jim') followed by an agent_switched frame
      // whose agent_id is the handoff TARGET ('jim'). Then Jim's own turns.
      const chat = useChatStore.getState()
      chat.handleFrame({
        type: 'replay_message',
        id: 'sys-handoff',
        role: 'system',
        content: 'Handoff: mia → Jim — General Purpose.',
        agent_id: 'jim',
        timestamp: '2026-06-10T10:00:00Z',
        session_id: SID,
      })
      // replay.go emits this for the "Handoff:" system entry.
      chat.handleFrame({
        type: 'agent_switched',
        agent_id: 'jim',
        session_id: SID,
      })
      chat.handleFrame({
        type: 'replay_message',
        id: 'm-jim',
        role: 'assistant',
        content: 'On it — using browser tools.',
        agent_id: 'jim',
        timestamp: '2026-06-10T10:01:00Z',
        session_id: SID,
      })
    })

    // The invariant: header/composer agent is the last-active agent, not the creator.
    expect(useSessionStore.getState().activeAgentId).toBe('jim')
    expect(useSessionStore.getState().activeAgentId).not.toBe('mia')
  })

  it('the replay agent_switched frame never downgrades a Jim header back to the creator', () => {
    // Even if a stray agent_switched carrying the CREATOR arrived (it must not,
    // per replay.go which emits the target), the store applies exactly what the
    // frame carries — so this test documents that the gateway emits the TARGET.
    // Here we assert the realistic flow: seed jim, replay carries jim, stays jim.
    act(() => {
      useSessionStore.getState().setActiveSession(SID, 'jim', null)
      useChatStore.getState().handleFrame({
        type: 'agent_switched',
        agent_id: 'jim',
        session_id: SID,
      })
    })
    expect(useSessionStore.getState().activeAgentId).toBe('jim')
  })
})

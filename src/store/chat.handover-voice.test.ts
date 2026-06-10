// Fix 2 regression: the handover "connecting" narration must render under the
// SOURCE agent (Mia), and the target's first greeting under the TARGET (Jim).
//
// The backend (pkg/agent/turn.go + loop.go, #416) attributes:
//   - the intermediate narration text ("Let me connect you with Jim…") to the
//     active agent BEFORE the handoff tool runs  → Mia (source)
//   - the `handoff` tool call to the pre-exec agent → Mia (source)
//   - Jim's first greeting to the now-active agent → Jim (target)
// and replay (pkg/gateway/replay.go) emits these as replay_message + tool_call_*
// + agent_switched frames, each stamped with that agent_id.
//
// This test locks the STORE side of that contract: across the full handover
// replay ordering the per-message authorship and the tool-call placement must
// land on the right bubbles. Specifically it guards against the coalesce/bake
// interaction silently attributing the handoff tool chip or Jim's greeting to
// the wrong agent.
//
// Replay ordering modelled (as streamReplay emits it):
//   1. user message
//   2. replay_message(assistant, "Let me connect you with Jim.", agent_id=mia)  ← narration
//   3. tool_call_start(handoff, agent_id=mia) / tool_call_result(handoff, agent_id=mia)
//   4. agent_switched(agent_id=jim)            ← session active agent flips to Jim
//   5. replay_message(assistant, "Hi, I'm Jim — how can I help?", agent_id=jim) ← greeting
//   6. done

import { describe, it, expect, beforeEach } from 'vitest'
import { act } from 'react'
import { useChatStore } from './chat'
import { useSessionStore } from './session'

const SID = 'sess-handover-voice'

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
    // The session was created by Mia; after the handover the active agent is Jim.
    useSessionStore.setState({
      activeSessionId: SID,
      activeAgentId: 'mia',
      activeAgentType: 'core',
    })
  })
}

beforeEach(resetStore)

describe('chat store — handover voice attribution (Fix 2)', () => {
  it('narration + handoff tool render under SOURCE (mia); greeting under TARGET (jim)', () => {
    act(() => {
      const s = useChatStore.getState()
      // 1. user
      s.handleFrame({
        type: 'replay_message',
        id: 'u-1',
        role: 'user',
        content: 'I need help deploying.',
        timestamp: '2026-06-10T10:00:00Z',
        session_id: SID,
      })
      // 2. Mia's connecting narration.
      s.handleFrame({
        type: 'replay_message',
        id: 'm-mia-narr',
        role: 'assistant',
        content: 'Let me connect you with Jim.',
        agent_id: 'mia',
        timestamp: '2026-06-10T10:00:01Z',
        session_id: SID,
      })
      // 3. handoff tool call — attributed to Mia (source) by the backend.
      s.handleFrame({
        type: 'tool_call_start',
        call_id: 'tc-handoff',
        tool: 'handoff',
        params: { agent_id: 'jim', context: 'deploy help' },
        agent_id: 'mia',
        session_id: SID,
      })
      s.handleFrame({
        type: 'tool_call_result',
        call_id: 'tc-handoff',
        tool: 'handoff',
        result: 'Connecting you with Jim...',
        status: 'success',
        agent_id: 'mia',
        session_id: SID,
      })
      // 4. session active agent flips to Jim.
      s.handleFrame({
        type: 'agent_switched',
        agent_id: 'jim',
        session_id: SID,
      })
      // 5. Jim's first-person greeting.
      s.handleFrame({
        type: 'replay_message',
        id: 'm-jim-greet',
        role: 'assistant',
        content: "Hi, I'm Jim — how can I help?",
        agent_id: 'jim',
        timestamp: '2026-06-10T10:00:02Z',
        session_id: SID,
      })
      // 6. done — bakes any remaining tool calls.
      s.handleFrame({ type: 'done', session_id: SID })
    })

    const state = useChatStore.getState()
    const miaNarr = state.messages.find((m) => m.id === 'm-mia-narr')
    const jimGreet = state.messages.find((m) => m.id === 'm-jim-greet')

    // The connecting line is voiced by the SOURCE agent, not the target.
    expect(miaNarr).toBeDefined()
    expect(miaNarr!.agentId).toBe('mia')
    expect(miaNarr!.content).toContain('connect you with Jim')

    // The handoff tool chip is baked onto Mia's narration bubble (the source
    // turn that decided to hand off), NOT onto Jim's greeting.
    const handoffOnMia = miaNarr!.tool_calls?.some((tc) => tc.tool === 'handoff')
    expect(handoffOnMia).toBe(true)

    // Jim greets in the first person under his own identity.
    expect(jimGreet).toBeDefined()
    expect(jimGreet!.agentId).toBe('jim')
    expect(jimGreet!.tool_calls?.some((tc) => tc.tool === 'handoff')).toBeFalsy()

    // The active (header/composer) agent ends on Jim — the last-active agent.
    expect(useSessionStore.getState().activeAgentId).toBe('jim')
  })
})

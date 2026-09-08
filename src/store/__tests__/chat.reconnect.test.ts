// chat.reconnect.test.ts — ADR-082 D4/D5 (FR-009): session_state.active_turn
// opens the streaming bubble + Stop state on a mid-turn reconnect; catch-up
// and live tokens append into it; the turn's own `done` finalizes it exactly
// once. Spec: docs/internal/specs/ui-independent-turns-spec.md T-19.
//
// Frame sequence under test mirrors the real gateway contract (ADR-082 §3
// D3/D4): on a connection binding to a session with an in-flight turn —
//   session_state (active_turn present)
//   → 0+ replay_message frames (transcript history, arrival order)
//   → done (the REPLAY's own terminator — carries stats.frames_emitted, not
//           turn stats; existing gateway behaviour per D3)
//   → token (catch-up: everything generated so far, in ONE frame)
//   → token* (live tail)
//   → done (the TURN's own completion)
//
// The two `done` frames must not be confused: only the second one finalizes
// the bubble. This file pins that this store correctly tells them apart via
// isReplaying + the new activeTurnId/activeTurnAgentId bucket fields (see
// chat.ts's 'session_state' and 'done' cases).

import { describe, it, expect, beforeEach } from 'vitest'
import { act } from 'react'
import { useChatStore, getMessages } from '../chat'
import { useSessionStore } from '../session'
import type { WsSessionStateFrame, WsReplayMessageFrame, TokenFrame, DoneFrame } from '@/lib/ws'

const SID = 'reconnect-active-turn-session'
const AGENT_ID = 'agent-jim'
const TURN_ID = 'turn-abc-123'

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
    })
    useSessionStore.setState({
      activeSessionId: SID,
      activeAgentId: null,
      activeAgentType: null,
    })
  })
}

beforeEach(resetStores)

function sessionStateWithActiveTurn(): WsSessionStateFrame {
  return {
    type: 'session_state',
    user_id: 'user-1',
    pending_approvals: [],
    active_turn: {
      turn_id: TURN_ID,
      agent_id: AGENT_ID,
      started_at: '2026-09-08T10:00:00.000Z',
    },
    emitted_at: '2026-09-08T10:00:01.000Z',
  }
}

function sessionStateWithoutActiveTurn(): WsSessionStateFrame {
  return {
    type: 'session_state',
    user_id: 'user-1',
    pending_approvals: [],
    emitted_at: '2026-09-08T10:00:01.000Z',
  }
}

function replayMessage(index: number): WsReplayMessageFrame {
  return {
    type: 'replay_message',
    session_id: SID,
    role: index % 2 === 0 ? 'user' : 'assistant',
    content: `history entry ${index}`,
    timestamp: new Date(Date.parse('2026-09-08T09:00:00.000Z') + index * 1000).toISOString(),
  }
}

// The replay's OWN terminator — pre-existing gateway behaviour (D3): every
// attach's transcript replay ends with a `done` carrying `stats.frames_emitted`,
// never token/cost stats. Not to be confused with the turn's own `done`.
function replayTerminatorDone(framesEmitted: number): DoneFrame {
  return {
    type: 'done',
    session_id: SID,
    stats: { frames_emitted: framesEmitted },
  }
}

function catchUpOrLiveToken(content: string): TokenFrame {
  return { type: 'token', session_id: SID, content, agent_id: AGENT_ID }
}

// The TURN's own completion `done` — carries real token/cost stats.
function turnDone(): DoneFrame {
  return { type: 'done', session_id: SID, stats: { tokens: 42, cost: 0.005 } }
}

function bucket() {
  return useChatStore.getState().sessionsById[SID]
}

function assistantMessages() {
  const b = bucket()
  return b ? getMessages(b).filter((m) => m.role === 'assistant') : []
}

// Mirrors reattachActiveSession's own optimistic `isReplaying: true` set,
// made BEFORE attach_session is even sent (see OmnipusRuntimeProvider.tsx).
// Every scenario below starts from a connection that just bound to the
// session, so isReplaying is true until the replay-terminating `done`.
function beginAttach() {
  act(() => {
    useChatStore.getState().setReplaying(true)
  })
}

describe('chat.reconnect — session_state.active_turn (ADR-082 D4/FR-009)', () => {
  it('session_state.active_turn sets isStreaming + Stop-visible state, but does NOT open a bubble yet (no replay has landed)', () => {
    beginAttach()
    act(() => {
      useChatStore.getState().handleFrame(sessionStateWithActiveTurn())
    })

    // Stop-button visibility (ChatScreen.tsx reads the flat/foreground field).
    expect(useChatStore.getState().isStreaming).toBe(true)
    expect(bucket()?.isStreaming).toBe(true)
    // No bubble yet: opening one here (before replay_message frames for this
    // attach have arrived) would insert it ahead of the history that is
    // chronologically earlier — see chat.ts's 'session_state' case comment.
    expect(assistantMessages()).toHaveLength(0)
  })

  it('session_state WITHOUT active_turn leaves current behaviour unchanged', () => {
    act(() => {
      useChatStore.getState().handleFrame(sessionStateWithoutActiveTurn())
    })

    expect(useChatStore.getState().isStreaming).toBe(false)
    expect(bucket()).toBeUndefined()
  })

  it('the replay-terminating done (stats.frames_emitted) opens the empty streaming bubble AFTER history, and does NOT finalize anything', () => {
    act(() => {
      useChatStore.getState().handleFrame(sessionStateWithActiveTurn())
    })
    // Two history entries replay in arrival order. Both role 'user' (indices
    // 0 and 2 are even) so `assistantMessages()` below counts ONLY the new
    // placeholder, not an unrelated historical assistant bubble.
    act(() => {
      useChatStore.getState().handleFrame(replayMessage(0))
      useChatStore.getState().handleFrame(replayMessage(2))
    })
    // Replay's own terminator.
    act(() => {
      useChatStore.getState().handleFrame(replayTerminatorDone(2))
    })

    const msgs = assistantMessages()
    // Exactly one streaming placeholder opened — no double bubble.
    expect(msgs).toHaveLength(1)
    const placeholder = msgs[0]
    expect(placeholder.content).toBe('')
    expect(placeholder.isStreaming).toBe(true)
    expect(placeholder.status).toBe('streaming')
    expect(placeholder.agentId).toBe(AGENT_ID)
    // Placeholder is positioned AFTER the replayed history (ordering intact).
    const b = bucket()!
    expect(b.messageOrder[b.messageOrder.length - 1]).toBe(placeholder.id)
    expect(b.messageOrder.length).toBe(3) // 2 history entries + placeholder
    // Streaming/Stop state must still read "live" — this done did not finalize.
    expect(useChatStore.getState().isStreaming).toBe(true)
    expect(b.isStreaming).toBe(true)
  })

  it('catch-up token appends into the placeholder (no new bubble); subsequent live tokens keep appending', () => {
    act(() => {
      useChatStore.getState().handleFrame(sessionStateWithActiveTurn())
      useChatStore.getState().handleFrame(replayTerminatorDone(0))
    })
    expect(assistantMessages()).toHaveLength(1)

    // Catch-up: everything generated so far, in one frame.
    act(() => {
      useChatStore.getState().handleFrame(catchUpOrLiveToken('Hello, this is the catch-up so far.'))
    })
    let msgs = assistantMessages()
    expect(msgs).toHaveLength(1) // still exactly one bubble
    expect(msgs[0].content).toBe('Hello, this is the catch-up so far.')
    expect(msgs[0].isStreaming).toBe(true)

    // Live tail continues appending to the SAME bubble.
    act(() => {
      useChatStore.getState().handleFrame(catchUpOrLiveToken(' And now more live text.'))
    })
    msgs = assistantMessages()
    expect(msgs).toHaveLength(1)
    expect(msgs[0].content).toBe('Hello, this is the catch-up so far. And now more live text.')
  })

  it('the turn\'s own done finalizes the bubble exactly once (status done, isStreaming false, Stop hidden)', () => {
    act(() => {
      useChatStore.getState().handleFrame(sessionStateWithActiveTurn())
      useChatStore.getState().handleFrame(replayTerminatorDone(0))
      useChatStore.getState().handleFrame(catchUpOrLiveToken('final answer'))
    })
    // Sanity: still streaming before the turn's own done arrives.
    expect(useChatStore.getState().isStreaming).toBe(true)

    act(() => {
      useChatStore.getState().handleFrame(turnDone())
    })

    const msgs = assistantMessages()
    expect(msgs).toHaveLength(1)
    expect(msgs[0].content).toBe('final answer')
    expect(msgs[0].isStreaming).toBe(false)
    expect(msgs[0].status).toBe('done')
    expect(useChatStore.getState().isStreaming).toBe(false)
    expect(bucket()?.isStreaming).toBe(false)
    // Token/cost stats from the REAL done are applied (proves this — not the
    // replay terminator — is the frame that finalized).
    expect(bucket()?.sessionTokens).toBe(42)
    expect(bucket()?.sessionCost).toBe(0.005)
  })

  it('a full sequence with real history interleaved produces exactly one final assistant bubble, correctly ordered after history', () => {
    act(() => {
      useChatStore.getState().handleFrame(sessionStateWithActiveTurn())
      useChatStore.getState().handleFrame(replayMessage(0))
      useChatStore.getState().handleFrame(replayMessage(1))
      useChatStore.getState().handleFrame(replayMessage(2))
      useChatStore.getState().handleFrame(replayTerminatorDone(3))
      useChatStore.getState().handleFrame(catchUpOrLiveToken('catch up '))
      useChatStore.getState().handleFrame(catchUpOrLiveToken('and live'))
      useChatStore.getState().handleFrame(turnDone())
    })

    const b = bucket()!
    // 3 history entries (indices 0,1,2 → user/assistant/user) + exactly one
    // NEW assistant bubble for the announced turn — never a duplicate.
    const allMsgs = getMessages(b)
    expect(allMsgs).toHaveLength(4)
    const finalAssistant = allMsgs[allMsgs.length - 1]
    expect(finalAssistant.role).toBe('assistant')
    expect(finalAssistant.content).toBe('catch up and live')
    expect(finalAssistant.status).toBe('done')
    expect(finalAssistant.isStreaming).toBe(false)
    expect(useChatStore.getState().isStreaming).toBe(false)
  })
})

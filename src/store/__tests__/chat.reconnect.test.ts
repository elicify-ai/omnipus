// chat.reconnect.test.ts — ADR-082 D4/D5 (FR-009): session_state.active_turn
// opens the streaming bubble + Stop state on a mid-turn reconnect; catch-up
// and live tokens append into it; the turn's own `done` finalizes it exactly
// once. Spec: docs/internal/specs/ui-independent-turns-spec.md T-19.
//
// Review round (S1/CR1): the store must be ORDER-AGNOSTIC. The fixed gateway
// contract emits, on attach_session —
//   session_state (active_turn present)
//   → 0+ replay_message frames (transcript history, arrival order)
//   → done (the REPLAY's own terminator — carries stats.frames_emitted, not
//           turn stats; existing gateway behaviour per D3)
//   → token (catch-up: everything generated so far, in ONE frame)
//   → token* (live tail)
//   → done (the TURN's own completion — carries stats.tokens/stats.cost)
// — but an OLDER gateway sent session_state LAST (after the replay
// terminator and even after some/all tokens), and even under the fixed
// contract a fast concurrent turn can race its own done ahead of the replay
// terminator ("turn finished during replay"). The two `done` frames must
// never be confused, in ANY of these orders: chat.ts classifies a `done`
// purely by its stats shape (frames_emitted-only vs tokens/cost present),
// never by activeTurnId/activeTurnBubbleOpened/isReplaying state — see the
// 'done' case's own review-S1 comment. This file pins that behaviour across
// all three orders. The 'chat.reconnect — fixed gateway contract order'
// describe block below is the primary (in-order) contract; the two describe
// blocks after it pin the out-of-order cases found in review.

import { describe, it, expect, beforeEach } from 'vitest'
import { act } from 'react'
import { useChatStore, getMessages, __resetFinishedTurnIdsForTests } from '../chat'
import { useSessionStore } from '../session'
import type { WsSessionStateFrame, WsReplayMessageFrame, TokenFrame, DoneFrame } from '@/lib/ws'

const SID = 'reconnect-active-turn-session'
const AGENT_ID = 'agent-jim'
const TURN_ID = 'turn-abc-123'

function resetStores() {
  // S2's finished-turn tracker is deliberately module-scoped (must survive a
  // sessionsById wipe in production) — reset it explicitly per test so a
  // turn finalized (turnDone()) in one `it()` block isn't wrongly treated as
  // already-finished when a LATER, unrelated `it()` announces the same
  // TURN_ID constant for the same SID via sessionStateWithActiveTurn().
  __resetFinishedTurnIdsForTests()
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

describe('chat.reconnect — fixed gateway contract order (session_state FIRST, ADR-082 D4/FR-009)', () => {
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

// Review finding S1/CR1 (HIGH): an OLDER gateway sends session_state LAST —
// after the replay terminator done, sometimes after tokens have already
// started flowing. The pre-review code classified a `done` as "still
// awaiting catch-up" purely from `activeTurnId && !activeTurnBubbleOpened`,
// and NOTHING ever set `activeTurnBubbleOpened` except the 'done' case's own
// placeholder-open branch. Under this order the turn's REAL done (carrying
// stats.tokens/cost) would find activeTurnId set (session_state ran) and
// activeTurnBubbleOpened still false (no frame had ever flipped it, because
// tokens arrived and appended into a bubble the 'done' case itself opened,
// not via this path) — misclassifying itself as the replay terminator: it
// pushed a SECOND, empty placeholder and `break`ed without finalizing the
// real, content-bearing bubble. Composer stayed locked forever. The fix
// (chat.ts's 'token' case) sets activeTurnBubbleOpened=true the instant any
// token lands for an announced turn, and the 'done' case now classifies
// itself purely by its own stats shape — never by activeTurn* state.
describe('chat.reconnect — OLD gateway contract order (session_state LAST, review S1/CR1)', () => {
  it('the announced turn\'s real done finalizes correctly when session_state arrives AFTER the replay terminator (old order)', () => {
    // Old order: replay history + its own terminator done BEFORE session_state.
    act(() => {
      useChatStore.getState().handleFrame(replayMessage(0))
      useChatStore.getState().handleFrame(replayMessage(2))
      useChatStore.getState().handleFrame(replayTerminatorDone(2))
    })
    expect(assistantMessages()).toHaveLength(0)

    // session_state announces the turn only now.
    act(() => {
      useChatStore.getState().handleFrame(sessionStateWithActiveTurn())
    })
    expect(useChatStore.getState().isStreaming).toBe(true)

    // Catch-up + live tokens open and fill a bubble (no placeholder was
    // opened by any 'done' — the replay terminator already ran).
    act(() => {
      useChatStore.getState().handleFrame(catchUpOrLiveToken('catch-up content '))
      useChatStore.getState().handleFrame(catchUpOrLiveToken('and more'))
    })
    expect(assistantMessages()).toHaveLength(1)

    // The turn's own real done arrives last, as always.
    act(() => {
      useChatStore.getState().handleFrame(turnDone())
    })

    const msgs = assistantMessages()
    // Exactly one finalized bubble — no stray empty placeholder from a
    // misclassified "still awaiting catch-up" done, and the real content
    // bubble is the one that got finalized.
    expect(msgs).toHaveLength(1)
    expect(msgs[0].content).toBe('catch-up content and more')
    expect(msgs[0].status).toBe('done')
    expect(msgs[0].isStreaming).toBe(false)
    expect(useChatStore.getState().isStreaming).toBe(false)
    expect(bucket()?.isStreaming).toBe(false)
    expect(bucket()?.activeTurnId).toBeNull()
    expect(bucket()?.sessionTokens).toBe(42)
  })

  it('a late-arriving replay-terminator done (out-of-order beyond just session_state) does not duplicate the already-open bubble', () => {
    act(() => {
      useChatStore.getState().handleFrame(sessionStateWithActiveTurn())
      // Catch-up token arrives BEFORE the replay terminator done — the
      // 'token' case's fix must mark the bubble opened right here.
      useChatStore.getState().handleFrame(catchUpOrLiveToken('first content'))
    })
    expect(assistantMessages()).toHaveLength(1)
    expect(bucket()?.activeTurnBubbleOpened).toBe(true)

    // Late replay terminator — must be a no-op for bubble purposes now that
    // the bubble is already open.
    act(() => {
      useChatStore.getState().handleFrame(replayTerminatorDone(0))
    })
    expect(assistantMessages()).toHaveLength(1)
    expect(assistantMessages()[0].content).toBe('first content')

    act(() => {
      useChatStore.getState().handleFrame(catchUpOrLiveToken(' and more'))
      useChatStore.getState().handleFrame(turnDone())
    })

    const msgs = assistantMessages()
    expect(msgs).toHaveLength(1)
    expect(msgs[0].content).toBe('first content and more')
    expect(msgs[0].status).toBe('done')
    expect(useChatStore.getState().isStreaming).toBe(false)
  })
})

// Review finding S1/CR1: "turn finished during replay" order — a fast
// concurrent turn's own done can race AHEAD of the replay terminator done
// even under the fixed gateway contract (they are emitted from independent
// code paths server-side). The done classifier must still tell them apart
// correctly regardless of which arrives first.
describe('chat.reconnect — turn finished during replay (real done races ahead of the replay terminator, review S1/CR1)', () => {
  it('the real done finalizes normally when it arrives BEFORE the replay terminator; the late terminator is then a no-op', () => {
    act(() => {
      useChatStore.getState().handleFrame(sessionStateWithActiveTurn())
      useChatStore.getState().handleFrame(replayMessage(0))
      // The turn's own content and completion arrive before the replay's
      // own terminator done.
      useChatStore.getState().handleFrame(catchUpOrLiveToken('final answer'))
      useChatStore.getState().handleFrame(turnDone())
    })

    let msgs = assistantMessages()
    expect(msgs).toHaveLength(1)
    expect(msgs[0].content).toBe('final answer')
    expect(msgs[0].status).toBe('done')
    expect(msgs[0].isStreaming).toBe(false)
    expect(useChatStore.getState().isStreaming).toBe(false)
    expect(bucket()?.activeTurnId).toBeNull()
    const messageCountAfterRealDone = bucket()!.messageOrder.length

    // The replay's own terminator arrives late — activeTurnId is already
    // null (the real done cleared it), so this must be a pure no-op: no new
    // placeholder, no re-opened streaming state.
    act(() => {
      useChatStore.getState().handleFrame(replayTerminatorDone(1))
    })

    msgs = assistantMessages()
    expect(msgs).toHaveLength(1)
    expect(bucket()!.messageOrder.length).toBe(messageCountAfterRealDone)
    expect(useChatStore.getState().isStreaming).toBe(false)
    expect(bucket()?.activeTurnId).toBeNull()
  })
})

// Review finding S6 (MED): a hard WS disconnect mid-replay (before either
// done ever arrives) must not leave isReplaying wedged true forever —
// nothing else clears it once the connection is gone.
describe('chat.reconnect — hard disconnect mid-replay clears isReplaying (review S6)', () => {
  it('clearStreamingState() clears isReplaying (and any ADR-082 active-turn state) for a bucket that never got past session_state', () => {
    act(() => {
      useChatStore.getState().setReplaying(true)
      useChatStore.getState().handleFrame(sessionStateWithActiveTurn())
    })
    expect(bucket()?.isReplaying).toBe(true)
    expect(bucket()?.isStreaming).toBe(true)
    expect(bucket()?.activeTurnId).toBe(TURN_ID)

    // Simulate the WS onDisconnected handler firing before any replay_message,
    // done, or token ever arrived.
    act(() => {
      useChatStore.getState().clearStreamingState()
    })

    expect(bucket()?.isReplaying).toBe(false)
    expect(bucket()?.isStreaming).toBe(false)
    expect(bucket()?.activeTurnId).toBeNull()
    expect(bucket()?.activeTurnAgentId).toBeNull()
    expect(bucket()?.activeTurnBubbleOpened).toBe(false)
  })

  it('sweeps a BACKGROUNDED bucket too, not just the currently-active one — a session the user switched away from mid-replay must not stay wedged forever', () => {
    // SID starts replaying (mirrors a real attach) but the user switches to
    // a different session before SID's replay ever completes — SID's bucket
    // is now a background bucket with isReplaying:true and no way for any
    // further frame to reach it specifically.
    act(() => {
      useChatStore.getState().setReplaying(true)
      useChatStore.getState().handleFrame(sessionStateWithActiveTurn())
    })
    expect(bucket()?.isReplaying).toBe(true)
    expect(bucket()?.activeTurnId).toBe(TURN_ID)

    act(() => {
      useSessionStore.setState({ activeSessionId: 'some-other-session' })
    })

    // The socket drops (onDisconnected) while SID is backgrounded.
    act(() => {
      useChatStore.getState().clearStreamingState()
    })

    expect(bucket()?.isReplaying).toBe(false)
    expect(bucket()?.isStreaming).toBe(false)
    expect(bucket()?.activeTurnId).toBeNull()
  })
})

// Review finding S7 (LOW): an explicit user cancel (Stop button/Escape)
// must clear the ADR-082 active-turn fields alongside isStreaming — the
// invariant documented on `activeTurnId` says they are always written
// together, and cancelStream/markLastMessageInterrupted are among the
// paths that end streaming.
describe('chat.reconnect — explicit cancel clears active-turn state (review S7)', () => {
  it('cancelStream() clears activeTurnId/activeTurnAgentId/activeTurnBubbleOpened alongside isStreaming', () => {
    act(() => {
      useChatStore.getState().setReplaying(true)
      useChatStore.getState().handleFrame(sessionStateWithActiveTurn())
      useChatStore.getState().handleFrame(replayTerminatorDone(0))
      useChatStore.getState().handleFrame(catchUpOrLiveToken('partial answer'))
    })
    expect(bucket()?.activeTurnId).toBe(TURN_ID)
    expect(bucket()?.activeTurnBubbleOpened).toBe(true)

    act(() => {
      useChatStore.getState().cancelStream()
    })

    expect(bucket()?.activeTurnId).toBeNull()
    expect(bucket()?.activeTurnAgentId).toBeNull()
    expect(bucket()?.activeTurnBubbleOpened).toBe(false)
    expect(useChatStore.getState().isStreaming).toBe(false)
  })
})

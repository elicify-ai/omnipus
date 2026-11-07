// turn-liveness.test.ts — vitest coverage for the server-side active-turn-at-
// attach proof used by replay-fidelity (c) (CHECK 3 F1, CHECK 4 F1/F2).
//
// Faked edge: the frames themselves (the Playwright socket wiring in
// observeAttachTurn is a thin adapter over AttachTurnTracker and is covered by
// the Playwright suite, not here). The tracker's pairing of request, the
// attach's own session_state and its catch_up_complete runs for REAL.
// Expectations are the contract: SessionStateFrame.active_turn is present only
// when the attached session has a foreground turn in flight at emit time, and
// the attach response is session_state ... catch_up_complete for the session.
//
// Deliberately NOT covered here: that replay-fidelity.spec.ts calls
// requireActiveTurnAtAttach at all (call-site wiring is only exercised by the
// live Playwright run) and the real gateway's frames.

// @vitest-environment node
import { describe, expect, it } from 'vitest'
import {
  AttachTurnTracker,
  NO_ACTIVE_TURN_AT_ATTACH,
  NO_COMPLETED_ATTACH,
  NO_SESSION_STATE_AT_ATTACH,
  requireActiveTurnAtAttach,
  type ObservedFrame,
} from './turn-liveness'

const SID = 'sess-target'
const TURN = { turn_id: 't1', agent_id: 'mia', started_at: '2026-11-07T10:00:00Z' }
const attach = (sid = SID): ObservedFrame => ({ type: 'attach_session', session_id: sid })
const state = (sid = SID, withTurn = true): ObservedFrame =>
  withTurn ? { type: 'session_state', session_id: sid, active_turn: TURN } : { type: 'session_state', session_id: sid }
const complete = (sid = SID): ObservedFrame => ({ type: 'catch_up_complete', session_id: sid })

describe('requireActiveTurnAtAttach', () => {
  it('returns the turn when the attach response session_state carries active_turn', () => {
    const t = new AttachTurnTracker()
    const mark = t.mark
    t.sent(attach())
    t.received(state())
    t.received(complete())
    expect(requireActiveTurnAtAttach(t, mark, SID)).toEqual(TURN)
  })

  it('F1 counterexample: backend already finished at attach (no active_turn) fails even though the first viewer may still show Stop', () => {
    const t = new AttachTurnTracker()
    const mark = t.mark
    t.sent(attach())
    t.received(state(SID, false))
    t.received(complete())
    expect(() => requireActiveTurnAtAttach(t, mark, SID)).toThrow(NO_ACTIVE_TURN_AT_ATTACH)
  })

  it('F2: a turn that ends immediately AFTER the attach still passes (no post-sample)', () => {
    const t = new AttachTurnTracker()
    const mark = t.mark
    t.sent(attach())
    t.received(state())
    t.received(complete())
    t.received({ type: 'done', session_id: SID }) // terminal frame after the attach
    t.received(state(SID, false)) // later state without a turn
    expect(requireActiveTurnAtAttach(t, mark, SID)).toEqual(TURN)
  })

  it('uses the FIRST session_state of the attach window, not a later one', () => {
    const t = new AttachTurnTracker()
    const mark = t.mark
    t.sent(attach())
    t.received(state(SID, false))
    t.received(state(SID, true)) // a later frame must not rescue an inactive attach
    t.received(complete())
    expect(() => requireActiveTurnAtAttach(t, mark, SID)).toThrow(NO_ACTIVE_TURN_AT_ATTACH)
  })

  it('fails when the attach completed without any session_state', () => {
    const t = new AttachTurnTracker()
    const mark = t.mark
    t.sent(attach())
    t.received(complete())
    expect(() => requireActiveTurnAtAttach(t, mark, SID)).toThrow(NO_SESSION_STATE_AT_ATTACH)
  })

  it('fails when no attach for the session completed (request only)', () => {
    const t = new AttachTurnTracker()
    const mark = t.mark
    t.sent(attach())
    t.received(state())
    expect(() => requireActiveTurnAtAttach(t, mark, SID)).toThrow(NO_COMPLETED_ATTACH)
  })

  it('ignores an active session_state for a DIFFERENT session', () => {
    const t = new AttachTurnTracker()
    const mark = t.mark
    t.sent(attach())
    t.received(state('other', true))
    t.received(state(SID, false))
    t.received(complete())
    expect(() => requireActiveTurnAtAttach(t, mark, SID)).toThrow(NO_ACTIVE_TURN_AT_ATTACH)
  })

  it('ignores a session_state seen before the attach request (unscoped or earlier emit)', () => {
    const t = new AttachTurnTracker()
    t.received(state()) // before any request: must not count
    const mark = t.mark
    t.sent(attach())
    t.received(state(SID, false))
    t.received(complete())
    expect(() => requireActiveTurnAtAttach(t, mark, SID)).toThrow(NO_ACTIVE_TURN_AT_ATTACH)
  })

  it('does not count a state + completion that answers no observed attach request', () => {
    const t = new AttachTurnTracker()
    const mark = t.mark
    t.received(state())
    t.received(complete())
    expect(() => requireActiveTurnAtAttach(t, mark, SID)).toThrow(NO_COMPLETED_ATTACH)
  })

  it('ignores an earlier completed attach (before the mark), e.g. the root attach', () => {
    const t = new AttachTurnTracker()
    t.sent(attach())
    t.received(state())
    t.received(complete()) // earlier, active
    const mark = t.mark
    expect(() => requireActiveTurnAtAttach(t, mark, SID)).toThrow(NO_COMPLETED_ATTACH)
  })

  it('judges the NEWEST completed attach after the mark', () => {
    const t = new AttachTurnTracker()
    const mark = t.mark
    t.sent(attach())
    t.received(state())
    t.received(complete())
    t.sent(attach()) // a reconnect re-attach, answered after the turn ended
    t.received(state(SID, false))
    t.received(complete())
    expect(() => requireActiveTurnAtAttach(t, mark, SID)).toThrow(NO_ACTIVE_TURN_AT_ATTACH)
  })

  it('names the three failure causes distinctly', () => {
    const msgs = [NO_COMPLETED_ATTACH, NO_SESSION_STATE_AT_ATTACH, NO_ACTIVE_TURN_AT_ATTACH]
    expect(new Set(msgs).size).toBe(3)
  })
})

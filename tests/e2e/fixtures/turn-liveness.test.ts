// turn-liveness.test.ts — vitest coverage for the server-side active-turn-at-
// attach proof used by replay-fidelity (c) (CHECK 3 F1, CHECK 4 F1/F2).
//
// Faked edge: Playwright's Page/WebSocket event delivery (an EventEmitter
// stand-in) and the tracker-level frame sequences. The tracker, the socket
// adapter (observeAttachTurn), the attachWhileObservingTurn wrapper and the
// generated runtime WsFrame validation all run for REAL.
// Expectations are the contract: SessionStateFrame.active_turn is present only
// when the attached session has a foreground turn in flight at emit time, and
// the attach response carries session_state then catch_up_complete for the
// session (snapshot attaches send a session_snapshot prefix first); a refused
// attach sends an error and no completion.
//
// Deliberately NOT covered here: that replay-fidelity.spec.ts uses the wrapper
// at its call site (a change that bypasses it is not caught offline; only the
// live Playwright run exercises that path), the real browser socket, and the
// real gateway's frames.

// @vitest-environment node
import { EventEmitter } from 'node:events'
import type { Page } from '@playwright/test'
import { describe, expect, it } from 'vitest'
import {
  ATTACH_REJECTED,
  AttachTurnTracker,
  attachWhileObservingTurn,
  observeAttachTurn,
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

  it('judges the FIRST request sent after the mark (the marked attach), not a later re-attach (CHECK 5 decision)', () => {
    const t = new AttachTurnTracker()
    const mark = t.mark
    t.sent(attach())
    t.received(state())
    t.received(complete())
    t.sent(attach()) // a later re-attach, answered after the turn ended
    t.received(state(SID, false))
    t.received(complete())
    expect(() => requireActiveTurnAtAttach(t, mark, SID), 'the marked attach was active').not.toThrow()
    expect(requireActiveTurnAtAttach(t, mark, SID)).toEqual(TURN)
  })

  it('F1 (CHECK 5): a turn that ends between bind and state construction is REJECTED - documented best-effort limit', () => {
    // Real-gateway order from CHECK 5's probe: the hub binds while the turn is
    // alive, the turn finishes, then session_state is built without active_turn.
    const t = new AttachTurnTracker()
    const mark = t.mark
    t.sent(attach())
    t.received({ type: 'session_snapshot', session_id: SID })
    t.received(state(SID, false))
    t.received({ type: 'done', session_id: SID })
    t.received(complete())
    expect(() => requireActiveTurnAtAttach(t, mark, SID)).toThrow(NO_ACTIVE_TURN_AT_ATTACH)
    expect(NO_ACTIVE_TURN_AT_ATTACH).toContain('Best-effort')
    expect(NO_ACTIVE_TURN_AT_ATTACH).toContain('between the hub bind and the state construction')
  })

  it('names the three failure causes distinctly', () => {
    const msgs = [NO_COMPLETED_ATTACH, NO_SESSION_STATE_AT_ATTACH, NO_ACTIVE_TURN_AT_ATTACH]
    expect(new Set(msgs).size).toBe(3)
  })
})

// ── CHECK 5 F3: overlapping same-session requests are paired FIFO ───────────

describe('AttachTurnTracker overlapping same-session requests (CHECK 5 F3)', () => {
  it('a pre-mark response cannot complete the later marked request', () => {
    const t = new AttachTurnTracker()
    t.sent(attach()) // A, before the mark
    const mark = t.mark
    t.sent(attach()) // B, the marked request, while A is still outstanding
    t.received(state()) // A answer (active)
    t.received(complete()) // A done
    expect(() => requireActiveTurnAtAttach(t, mark, SID), 'B has not been answered yet').toThrow(NO_COMPLETED_ATTACH)
  })

  it('the marked request is judged on ITS OWN response, not the older active one', () => {
    const t = new AttachTurnTracker()
    t.sent(attach()) // A
    const mark = t.mark
    t.sent(attach()) // B
    t.received(state(SID, true))
    t.received(complete()) // A: active
    t.received(state(SID, false))
    t.received(complete()) // B: inactive
    expect(() => requireActiveTurnAtAttach(t, mark, SID)).toThrow(NO_ACTIVE_TURN_AT_ATTACH)
  })

  it('the marked request wins when it is the active one and A was inactive', () => {
    const t = new AttachTurnTracker()
    t.sent(attach()) // A
    const mark = t.mark
    t.sent(attach()) // B
    t.received(state(SID, false))
    t.received(complete())
    t.received(state(SID, true))
    t.received(complete())
    expect(requireActiveTurnAtAttach(t, mark, SID)).toEqual(TURN)
  })

  it('does not count a completed attach for ANOTHER session after the mark', () => {
    const t = new AttachTurnTracker()
    const mark = t.mark
    t.sent(attach('other'))
    t.received(state('other', true))
    t.received(complete('other'))
    expect(() => requireActiveTurnAtAttach(t, mark, SID)).toThrow(NO_COMPLETED_ATTACH)
  })
})

// ── CHECK 5 F2: bind to the CURRENT socket generation only ──────────────────

class FakeSocket extends EventEmitter {
  constructor(private readonly path = '/api/v1/chat/ws') {
    super()
  }
  url() {
    return `ws://127.0.0.1:1${this.path}`
  }
  sendFrame(frame: object) {
    this.emit('framesent', { payload: JSON.stringify(frame) })
  }
  recvRaw(payload: string) {
    this.emit('framereceived', { payload })
  }
  recvFrame(frame: object) {
    this.recvRaw(JSON.stringify(frame))
  }
  closeSocket() {
    this.emit('close')
  }
}

function fakePage() {
  const emitter = new EventEmitter()
  return { page: emitter as unknown as Page, open: (s: FakeSocket) => emitter.emit('websocket', s) }
}

const wireAttach = (sid = SID) => ({ type: 'attach_session', session_id: sid })
const wireState = (sid = SID, withTurn = true) => ({
  type: 'session_state',
  user_id: 'u',
  pending_approvals: [],
  emitted_at: '2026-11-07T10:00:00Z',
  session_id: sid,
  ...(withTurn ? { active_turn: TURN } : {}),
})
const wireComplete = (sid = SID) => ({ type: 'catch_up_complete', session_id: sid, seq: 1, mode: 'incremental' })

describe('observeAttachTurn socket adapter', () => {
  it('proves an active attach from real native frames on the chat socket', () => {
    const { page, open } = fakePage()
    const obs = observeAttachTurn(page)
    const s = new FakeSocket()
    open(s)
    const mark = obs.mark()
    s.sendFrame(wireAttach())
    s.recvFrame(wireState())
    s.recvFrame(wireComplete())
    expect(obs.require(mark, SID)).toEqual(TURN)
    obs.dispose()
  })

  it('ignores sockets that are not the chat socket', () => {
    const { page, open } = fakePage()
    const obs = observeAttachTurn(page)
    const s = new FakeSocket('/api/v1/other')
    open(s)
    const mark = obs.mark()
    s.sendFrame(wireAttach())
    s.recvFrame(wireState())
    s.recvFrame(wireComplete())
    expect(() => obs.require(mark, SID)).toThrow(NO_COMPLETED_ATTACH)
    obs.dispose()
  })

  it('latches an invalid native frame and fails loudly instead of swallowing it', () => {
    const { page, open } = fakePage()
    const obs = observeAttachTurn(page)
    const s = new FakeSocket()
    open(s)
    const mark = obs.mark()
    s.sendFrame(wireAttach())
    s.recvRaw('{"type":"session_state"}') // schema-invalid: required fields missing
    s.recvFrame(wireComplete())
    expect(() => obs.require(mark, SID)).toThrow(/invalid native frame/)
    obs.dispose()
  })

  it('stale ACTIVE reply from an older socket cannot prove the current attach live', () => {
    const { page, open } = fakePage()
    const obs = observeAttachTurn(page)
    const old = new FakeSocket()
    open(old)
    const mark = obs.mark()
    old.sendFrame(wireAttach()) // outstanding on the old socket
    const cur = new FakeSocket()
    open(cur) // newer generation
    cur.sendFrame(wireAttach())
    old.recvFrame(wireState(SID, true)) // late, stale
    old.recvFrame(wireComplete())
    cur.recvFrame(wireState(SID, false))
    cur.recvFrame(wireComplete())
    expect(() => obs.require(mark, SID)).toThrow(NO_ACTIVE_TURN_AT_ATTACH)
    obs.dispose()
  })

  it('stale INACTIVE reply from an older socket cannot reject a current active attach', () => {
    const { page, open } = fakePage()
    const obs = observeAttachTurn(page)
    const old = new FakeSocket()
    open(old)
    const mark = obs.mark()
    old.sendFrame(wireAttach())
    const cur = new FakeSocket()
    open(cur)
    cur.sendFrame(wireAttach())
    old.recvFrame(wireState(SID, false))
    old.recvFrame(wireComplete())
    cur.recvFrame(wireState(SID, true))
    cur.recvFrame(wireComplete())
    expect(obs.require(mark, SID)).toEqual(TURN)
    obs.dispose()
  })

  it('a closed socket proves nothing after it closes', () => {
    const { page, open } = fakePage()
    const obs = observeAttachTurn(page)
    const s = new FakeSocket()
    open(s)
    const mark = obs.mark()
    s.sendFrame(wireAttach())
    s.recvFrame(wireState())
    s.closeSocket()
    s.recvFrame(wireComplete()) // late frame after close
    expect(() => obs.require(mark, SID)).toThrow(NO_COMPLETED_ATTACH)
    obs.dispose()
  })

  it('a completed attach from a superseded socket does not count once a newer socket opens', () => {
    const { page, open } = fakePage()
    const obs = observeAttachTurn(page)
    const old = new FakeSocket()
    open(old)
    const mark = obs.mark()
    old.sendFrame(wireAttach())
    old.recvFrame(wireState())
    old.recvFrame(wireComplete()) // complete on the old socket
    open(new FakeSocket()) // reconnect: the proof must be re-earned on the new socket
    expect(() => obs.require(mark, SID)).toThrow(NO_COMPLETED_ATTACH)
    obs.dispose()
  })
})

// ── Call-site wiring: the check travels with the attach ─────────────────────

describe('attachWhileObservingTurn', () => {
  it('returns the attach result and requires the marked attach to have been active', async () => {
    const { page, open } = fakePage()
    const obs = observeAttachTurn(page)
    const s = new FakeSocket()
    open(s)
    const out = await attachWhileObservingTurn(obs, SID, async () => {
      s.sendFrame(wireAttach())
      s.recvFrame(wireState())
      s.recvFrame(wireComplete())
      return 'proof'
    })
    expect(out).toBe('proof')
    obs.dispose()
  })

  it('rejects when the marked attach was answered without an active turn', async () => {
    const { page, open } = fakePage()
    const obs = observeAttachTurn(page)
    const s = new FakeSocket()
    open(s)
    await expect(
      attachWhileObservingTurn(obs, SID, async () => {
        s.sendFrame(wireAttach())
        s.recvFrame(wireState(SID, false))
        s.recvFrame(wireComplete())
        return 'proof'
      }),
    ).rejects.toThrow(NO_ACTIVE_TURN_AT_ATTACH)
    obs.dispose()
  })

  it('takes the mark BEFORE the attach runs (an earlier active attach does not count)', async () => {
    const { page, open } = fakePage()
    const obs = observeAttachTurn(page)
    const s = new FakeSocket()
    open(s)
    s.sendFrame(wireAttach())
    s.recvFrame(wireState())
    s.recvFrame(wireComplete()) // root-style earlier attach, active
    await expect(attachWhileObservingTurn(obs, SID, async () => 'no attach sent')).rejects.toThrow(NO_COMPLETED_ATTACH)
    obs.dispose()
  })

  it('propagates an attach failure unchanged', async () => {
    const { page } = fakePage()
    const obs = observeAttachTurn(page)
    await expect(
      attachWhileObservingTurn(obs, SID, async () => {
        throw new Error('attach exploded')
      }),
    ).rejects.toThrow('attach exploded')
    obs.dispose()
  })
})

// ── CHECK 6 R1: a rejected attach fails its own queue slot ──────────────────

const errorFrame = (sid: string | undefined = SID, message = 'history_min_rows exceeds history_limit'): ObservedFrame => ({
  type: 'error',
  ...(sid ? { session_id: sid } : {}),
  message,
})

describe('AttachTurnTracker rejected attach (CHECK 6 R1)', () => {
  it('a rejected marked request fails the prerequisite even if a later request succeeds', () => {
    const t = new AttachTurnTracker()
    const mark = t.mark
    t.sent(attach()) // A, rejected before bind: no state, no completion
    t.received(errorFrame())
    t.sent(attach()) // B, valid
    t.received(state())
    t.received(complete())
    expect(() => requireActiveTurnAtAttach(t, mark, SID)).toThrow(ATTACH_REJECTED)
  })

  it('names the server message in the failure', () => {
    const t = new AttachTurnTracker()
    const mark = t.mark
    t.sent(attach())
    t.received(errorFrame(SID, 'history_min_rows exceeds history_limit'))
    expect(() => requireActiveTurnAtAttach(t, mark, SID)).toThrow('history_min_rows exceeds history_limit')
  })

  it('a rejected PRE-mark request does not lend its slot: the marked request keeps its own response', () => {
    const t = new AttachTurnTracker()
    t.sent(attach()) // A, before the mark, rejected
    const mark = t.mark
    t.sent(attach()) // B, the marked request
    t.received(errorFrame()) // answers A, not B
    t.received(state())
    t.received(complete())
    expect(() => requireActiveTurnAtAttach(t, mark, SID), 'B was active').not.toThrow()
    expect(requireActiveTurnAtAttach(t, mark, SID)).toEqual(TURN)
  })

  it('an error AFTER the attach state is not an attach rejection', () => {
    const t = new AttachTurnTracker()
    const mark = t.mark
    t.sent(attach())
    t.received(state())
    t.received(errorFrame(SID, 'some tool failed')) // unrelated, in-turn
    t.received(complete())
    expect(() => requireActiveTurnAtAttach(t, mark, SID), 'the attach itself succeeded').not.toThrow()
    expect(requireActiveTurnAtAttach(t, mark, SID)).toEqual(TURN)
  })

  it('an error for another session or without a session id is not an attach rejection', () => {
    const t = new AttachTurnTracker()
    const mark = t.mark
    t.sent(attach())
    t.received(errorFrame('other'))
    t.received({ type: 'error', message: 'connection-level error without a session id' })
    t.received(state())
    t.received(complete())
    expect(() => requireActiveTurnAtAttach(t, mark, SID), 'neither error addressed this attach').not.toThrow()
    expect(requireActiveTurnAtAttach(t, mark, SID)).toEqual(TURN)
  })

  it('a rejected marked request with no later request fails with the rejection, not "no completed attach"', () => {
    const t = new AttachTurnTracker()
    const mark = t.mark
    t.sent(attach())
    t.received(errorFrame())
    expect(() => requireActiveTurnAtAttach(t, mark, SID)).toThrow(ATTACH_REJECTED)
  })
})

describe('observeAttachTurn real-frame edge cases (CHECK 6 R1/R2)', () => {
  it('R1 through the adapter: a rejected attach then a valid one still fails', () => {
    const { page, open } = fakePage()
    const obs = observeAttachTurn(page)
    const s = new FakeSocket()
    open(s)
    const mark = obs.mark()
    s.sendFrame(wireAttach())
    s.recvFrame({ type: 'error', session_id: SID, message: 'history_min_rows exceeds history_limit' })
    s.sendFrame(wireAttach())
    s.recvFrame(wireState())
    s.recvFrame(wireComplete())
    expect(() => obs.require(mark, SID)).toThrow(ATTACH_REJECTED)
    obs.dispose()
  })

  it('R2: a nonzero mark earned on an earlier socket stays valid after a reconnect', () => {
    const { page, open } = fakePage()
    const obs = observeAttachTurn(page)
    const first = new FakeSocket()
    open(first)
    first.sendFrame(wireAttach('root'))
    first.recvFrame(wireState('root', false))
    first.recvFrame(wireComplete('root'))
    const mark = obs.mark()
    expect(mark, 'the root attach earned ordinal 1').toBe(1)
    const second = new FakeSocket()
    open(second) // reconnect
    second.sendFrame(wireAttach())
    second.recvFrame(wireState())
    second.recvFrame(wireComplete())
    expect(() => obs.require(mark, SID), 'ordinals must keep counting across generations').not.toThrow()
    expect(obs.require(mark, SID)).toEqual(TURN)
    obs.dispose()
  })
})

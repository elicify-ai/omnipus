// turn-liveness.ts — proves, from the SERVER, that a turn was in flight when a
// second viewer attached.
//
// CHECK 3 F1 and CHECK 4 F1/F2 (ciflake-check3/4-report.md): replay-fidelity
// (c) must prove active live production at the second viewer's attach
// (sprint-i-historical-replay-fidelity-spec.md Scenario 9 / TDD row 25).
// Sampling the FIRST viewer's composer Stop button before and after the attach
// cannot: Stop is a client-side render of the first viewer's own store, so the
// backend can finish after the first sample, the second viewer can attach to
// completed history, and a lagging first viewer still shows Stop at the second
// sample (CHECK 4's offline counterexample). Conversely the post-sample can
// reject a legitimate attach when the turn ends right after it.
//
// The signal used instead is the server's own answer to THIS attach. Per the
// contract, the attach_session response starts with a session_state frame for
// the bound session whose `active_turn` is "present only when the attached
// session has a foreground turn in flight at emit time" (SessionStateFrame /
// SessionStateActiveTurn, ADR-082 D4) and ends with catch_up_complete for that
// session. The tracker below pairs the second viewer's own attach_session
// request with the first session_state for the same session before that
// attach's catch_up_complete, and requires `active_turn` on it. That is a
// server observation at bind time, so it needs no post-sample, and a turn that
// ends immediately after the attach still passes.
//
// Observation only: it never sends frames or mocks transport.

import type { Page, WebSocket } from '@playwright/test'
import { WsFrame } from '../../../src/lib/api/generated/ws-schemas'

// not-wire-format: minimal structural view of the generated WsFrame fields this
// observer reads (attach_session, session_state, catch_up_complete).
export interface ObservedFrame {
  type: string
  session_id?: string
  active_turn?: { turn_id: string; agent_id: string; started_at: string }
}

// not-wire-format: local observation result, one per completed attach.
export interface AttachObservation {
  ordinal: number
  sessionId: string
  /** The first session_state for the session inside this attach's window, if any. */
  sessionState?: ObservedFrame
}

/** Socket-independent core: feed it the frames of one connection in order. */
export class AttachTurnTracker {
  private ordinal = 0
  private readonly pending = new Map<string, { ordinal: number; sessionState?: ObservedFrame }>()
  private readonly done: AttachObservation[] = []

  /** Highest attach ordinal seen so far; use as the `after` mark before navigating. */
  get mark(): number {
    return this.ordinal
  }

  sent(frame: ObservedFrame): void {
    if (frame.type !== 'attach_session' || !frame.session_id) return
    this.pending.set(frame.session_id, { ordinal: ++this.ordinal })
  }

  received(frame: ObservedFrame): void {
    if (!frame.session_id) return
    const open = this.pending.get(frame.session_id)
    if (!open) return
    if (frame.type === 'session_state') {
      open.sessionState ??= frame
    } else if (frame.type === 'catch_up_complete') {
      this.pending.delete(frame.session_id)
      this.done.push({ ordinal: open.ordinal, sessionId: frame.session_id, sessionState: open.sessionState })
    }
  }

  /** The newest completed attach for `sessionId` requested after `mark`. */
  completedAfter(mark: number, sessionId: string): AttachObservation | undefined {
    return [...this.done].reverse().find((o) => o.ordinal > mark && o.sessionId === sessionId)
  }
}

export const NO_COMPLETED_ATTACH =
  'PREREQUISITE: no completed attach_session -> catch_up_complete was observed for the target session ' +
  'after the mark, so the server state at the second viewer\'s attach is unknown.'

export const NO_SESSION_STATE_AT_ATTACH =
  'PREREQUISITE: the target attach completed without a session_state frame for the session, so the ' +
  'server state at the second viewer\'s attach is unknown.'

export const NO_ACTIVE_TURN_AT_ATTACH =
  'PREREQUISITE: the server reported NO foreground turn in flight (session_state without active_turn) when ' +
  'the second viewer attached, so the attach was a completed-history replay, not an attach during live production.'

/**
 * Throws unless the newest completed attach for `sessionId` after `mark` was
 * answered with a session_state carrying `active_turn`. Returns that turn.
 */
export function requireActiveTurnAtAttach(
  tracker: AttachTurnTracker,
  mark: number,
  sessionId: string,
): NonNullable<ObservedFrame['active_turn']> {
  const attach = tracker.completedAfter(mark, sessionId)
  if (!attach) throw new Error(NO_COMPLETED_ATTACH)
  if (!attach.sessionState) throw new Error(NO_SESSION_STATE_AT_ATTACH)
  if (!attach.sessionState.active_turn) throw new Error(NO_ACTIVE_TURN_AT_ATTACH)
  return attach.sessionState.active_turn
}

/** Wires a tracker to a page's native chat sockets. Observation only. */
export function observeAttachTurn(page: Page) {
  const tracker = new AttachTurnTracker()
  const cleanup: Array<() => void> = []
  let failure: Error | undefined
  const feed = (handler: (frame: ObservedFrame) => void) => ({ payload }: { payload: string | Buffer }) => {
    try {
      const parsed = WsFrame.safeParse(JSON.parse(payload.toString()))
      if (!parsed.success) throw new Error(`Attach turn observer: invalid native frame: ${parsed.error.message}`)
      handler(parsed.data as ObservedFrame)
    } catch (error) {
      failure ??= error instanceof Error ? error : new Error(String(error))
    }
  }
  const onSocket = (socket: WebSocket) => {
    if (new URL(socket.url()).pathname !== '/api/v1/chat/ws') return
    const sent = feed((f) => tracker.sent(f))
    const received = feed((f) => tracker.received(f))
    socket.on('framesent', sent)
    socket.on('framereceived', received)
    cleanup.push(() => {
      socket.off('framesent', sent)
      socket.off('framereceived', received)
    })
  }
  page.on('websocket', onSocket)
  return {
    /** Mark before navigating; pass the result to `require`. */
    mark: () => tracker.mark,
    /** Fails (loudly) on an observer parse failure, else on a non-active attach. */
    require(mark: number, sessionId: string) {
      if (failure) throw failure
      return requireActiveTurnAtAttach(tracker, mark, sessionId)
    },
    dispose() {
      page.off('websocket', onSocket)
      for (const remove of cleanup) remove()
      cleanup.length = 0
    },
  }
}

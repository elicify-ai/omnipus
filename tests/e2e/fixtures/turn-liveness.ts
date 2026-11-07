// turn-liveness.ts — a best-effort, server-reported check that a turn was in
// flight when a second viewer attached.
//
// History: CHECK 3 F1 and CHECK 4 F1/F2 (ciflake-check3/4-report.md) showed
// that sampling the FIRST viewer's composer Stop button before and after the
// attach cannot prove the backend was live at the second viewer's attach (Stop
// is a client render of the first viewer's own store and can lag the backend).
// The signal used instead is the server's own answer to THIS attach: the
// attach_session response starts with a session_state frame for the bound
// session, and its `active_turn` is "present only when the attached session
// has a foreground turn in flight at emit time" (SessionStateFrame /
// SessionStateActiveTurn, ADR-082 D4), and the response ends with
// catch_up_complete for that session.
//
// WHAT THIS DOES AND DOES NOT PROVE (CHECK 5 F1 — be honest about it):
//   - It proves the server reported an in-flight foreground turn when it BUILT
//     that session_state for the marked attach. It does NOT prove the turn was
//     live at the instant the hub bound the connection: the gateway binds the
//     hub first and constructs session_state afterwards (after preparing the
//     history snapshot), so a turn that ends between bind and state
//     construction is reported without active_turn. CHECK 5 reproduced that
//     interval against the real gateway.
//   - Consequence, accepted and documented: a fast turn that ended before the
//     state was built is REJECTED here as "no turn observably active at the
//     attach", even though the attach did bind while it was alive. That is a
//     prerequisite failure (the experiment did not observe live production), not
//     a delivery verdict. It is therefore a best-effort prerequisite, never a
//     proof of "active at bind".
//
// Correlation rules (CHECK 5 F2/F3):
//   - Bound to the CURRENT socket generation only. A new chat socket resets the
//     tracker and detaches the old one; frames from an older or closed socket
//     are ignored, so a stale reply can neither prove nor reject the current
//     attach. (Request ordinals keep counting across generations so a mark
//     taken before a reconnect stays meaningful.)
//   - Requests are queued per session and answered FIFO (the gateway dispatches
//     frames synchronously per connection). A response never overwrites or
//     completes another request, so a pre-mark response cannot complete a later
//     marked request.
//   - The proof is the FIRST request sent after the mark for the session (the
//     marked request), judged on its own first session_state.
// Remaining limit: a live (non-attach) session_state for the same session that
// arrives while a request is outstanding and before its own session_state would
// be taken as that request's state; the contract puts the attach's state first.
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

interface OpenRequest {
  ordinal: number
  sessionState?: ObservedFrame
}

/** Socket-independent core: feed it the frames of ONE connection in order. */
export class AttachTurnTracker {
  private ordinal = 0
  private pending = new Map<string, OpenRequest[]>()
  private done: AttachObservation[] = []

  /** Highest attach ordinal sent so far; take it as the mark before navigating. */
  get mark(): number {
    return this.ordinal
  }

  sent(frame: ObservedFrame): void {
    if (frame.type !== 'attach_session' || !frame.session_id) return
    const queue = this.pending.get(frame.session_id) ?? []
    queue.push({ ordinal: ++this.ordinal })
    this.pending.set(frame.session_id, queue)
  }

  received(frame: ObservedFrame): void {
    if (!frame.session_id) return
    const head = this.pending.get(frame.session_id)?.[0]
    if (!head) return
    if (frame.type === 'session_state') {
      head.sessionState ??= frame
    } else if (frame.type === 'catch_up_complete') {
      this.pending.get(frame.session_id)?.shift()
      this.done.push({ ordinal: head.ordinal, sessionId: frame.session_id, sessionState: head.sessionState })
    }
  }

  /** Forget everything observed on the previous connection; ordinals keep counting. */
  reset(): void {
    this.pending = new Map()
    this.done = []
  }

  /** The completed attach for the FIRST request for `sessionId` sent after `mark`. */
  completedAfter(mark: number, sessionId: string): AttachObservation | undefined {
    return this.done.find((o) => o.ordinal > mark && o.sessionId === sessionId)
  }
}

export const NO_COMPLETED_ATTACH =
  'PREREQUISITE: no completed attach_session -> catch_up_complete was observed on the current chat socket for the ' +
  "target session after the mark, so the server state at the second viewer's attach is unknown."

export const NO_SESSION_STATE_AT_ATTACH =
  'PREREQUISITE: the target attach completed without a session_state frame for the session, so the ' +
  "server state at the second viewer's attach is unknown."

export const NO_ACTIVE_TURN_AT_ATTACH =
  'PREREQUISITE: the server reported NO foreground turn in flight when it built the attach session_state (no ' +
  'active_turn), so the attach was not observed during live production. Best-effort: a turn that ended between ' +
  'the hub bind and the state construction is also rejected here.'

/**
 * Throws unless the marked attach for `sessionId` was answered with a
 * session_state carrying `active_turn`. Returns that turn.
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

/** The surface the spec needs from an observer (injectable). */
export interface AttachTurnObserver {
  mark(): number
  require(mark: number, sessionId: string): NonNullable<ObservedFrame['active_turn']>
}

/**
 * Marks, runs the attach, then requires the active-turn observation for the
 * marked attach. Returns the attach's own result, so a caller that uses the
 * result cannot drop the check without dropping the attach.
 */
export async function attachWhileObservingTurn<T>(
  observer: AttachTurnObserver,
  sessionId: string,
  attach: () => Promise<T>,
): Promise<T> {
  const mark = observer.mark()
  const result = await attach()
  observer.require(mark, sessionId)
  return result
}

/** Wires a tracker to a page's native chat sockets (current generation only). */
export function observeAttachTurn(page: Page) {
  const tracker = new AttachTurnTracker()
  let failure: Error | undefined
  let current: { socket: WebSocket; detach: () => void } | undefined
  const feed =
    (handler: (frame: ObservedFrame) => void) =>
    ({ payload }: { payload: string | Buffer }) => {
      try {
        const parsed = WsFrame.safeParse(JSON.parse(payload.toString()))
        if (!parsed.success) throw new Error(`Attach turn observer: invalid native frame: ${parsed.error.message}`)
        handler(parsed.data as ObservedFrame)
      } catch (error) {
        failure ??= error instanceof Error ? error : new Error(String(error))
      }
    }
  const drop = () => {
    current?.detach()
    current = undefined
    tracker.reset()
  }
  const onSocket = (socket: WebSocket) => {
    if (new URL(socket.url()).pathname !== '/api/v1/chat/ws') return
    drop() // a newer generation supersedes the older one; its frames are ignored from here on
    const sent = feed((f) => tracker.sent(f))
    const received = feed((f) => tracker.received(f))
    const closed = () => {
      if (current?.socket === socket) drop()
    }
    socket.on('framesent', sent)
    socket.on('framereceived', received)
    socket.on('close', closed)
    current = {
      socket,
      detach: () => {
        socket.off('framesent', sent)
        socket.off('framereceived', received)
        socket.off('close', closed)
      },
    }
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
      drop()
    },
  }
}

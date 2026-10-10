import { expect, type Page } from '@playwright/test'
import type { components } from '@/lib/api/generated/openapi-types'

/**
 * Upper bound for the server's answer to a Stop, shared by every spec that
 * ends a live turn with this helper. The gateway either answers a cancel on
 * the session's websocket or keeps the session's lifecycle state at
 * "working" while its turn is genuinely still running (see below), so 30s
 * of neither is a lost/ignored cancel, never a slow answer.
 */
export const END_TURN_BOUND_MS = 30_000

export interface ServerFrameLog {
  /**
   * Accepted cancel answers, scoped to the root session (see rootSessionId):
   * done / cancel_stage scoped by session_id; error:turn_canceled scoped when
   * it carries a session_id and accepted unscoped when it does not (error is
   * a GLOBAL frame type — src/store/chat/runtime-state.ts::
   * CANCEL_ACK_FRAME_TYPES); goal_status:waiting_on_user (the keeper-pause
   * announcement the server emits when a Stop pauses a goal's keeper).
   */
  readonly answers: string[]
  /** Every server frame's type since tracking began — diagnostics only. */
  readonly allTypes: string[]
  /**
   * The ROOT session id, taken from `session_started` frames ONLY (review
   * finding F on c6b3bed29/afb08e377: lastSessionId-from-any-frame let a
   * helper/sub-agent frame redirect the REST check to another session's
   * terminal record). Frames from other sessions never count as answers.
   */
  rootSessionId: string | null
}

const logsByPage = new WeakMap<Page, ServerFrameLog>()

/**
 * Start recording the page's server frames. MUST run before the page's
 * websocket opens — call it in beforeEach BEFORE page.goto('/'):
 * Playwright's `page.on('websocket')` only fires for sockets opened after
 * the listener attaches, and the SPA connects during goto, so a listener
 * installed later (e.g. inside endTurnDeterministically) never sees the
 * already-open socket. endTurnDeterministically reads the recording this
 * sets up; call sites of that helper stay unchanged.
 */
export function trackServerFrames(page: Page): void {
  const log: ServerFrameLog = { answers: [], allTypes: [], rootSessionId: null }
  page.on('websocket', (socket) => {
    socket.on('framereceived', ({ payload }) => {
      let frame: { type?: string; code?: string; stage?: string; session_id?: string; state?: string }
      try {
        frame = JSON.parse(payload.toString()) as typeof frame
      } catch {
        return
      }
      if (!frame || typeof frame !== 'object' || typeof frame.type !== 'string') return
      log.allTypes.push(frame.type)
      if (frame.type === 'session_started' && typeof frame.session_id === 'string' && frame.session_id) {
        log.rootSessionId = frame.session_id
      }
      const sid = typeof frame.session_id === 'string' ? frame.session_id : undefined
      const scopedToRoot = log.rootSessionId !== null && sid === log.rootSessionId
      // error is a global frame type: accept it without a session_id (the
      // store does the same — CANCEL_ACK_FRAME_TYPES), require a match when
      // it carries one.
      const globalError = frame.type === 'error' && sid === undefined
      if (!scopedToRoot && !globalError) return
      if (frame.type === 'done') log.answers.push('done')
      if (frame.type === 'error' && frame.code === 'turn_canceled') log.answers.push('error:turn_canceled')
      if (frame.type === 'cancel_stage') log.answers.push(`cancel_stage:${frame.stage ?? '?'}`)
      if (frame.type === 'goal_status' && frame.state === 'waiting_on_user') {
        log.answers.push('goal_status:waiting_on_user')
      }
    })
  })
  logsByPage.set(page, log)
}

/**
 * Shared endTurnDeterministically — click Stop if it is showing, then wait
 * for the SERVER to say THIS turn is over. Used by goal-work-first.spec.ts,
 * goal-card-position.spec.ts and browser-control-handover.spec.ts (this
 * replaces their three byte-similar local copies; the goal-work-first one
 * failed CI 37943454247 and passed only on retry). Requires a prior
 * trackServerFrames(page) in the spec's beforeEach.
 *
 * Why NOT "Stop becomes hidden": while the session carries an ACTIVE goal,
 * Stop stays mounted BY DESIGN even with no turn in flight
 * (src/lib/goalActivity.ts::isGoalRunning — the goal keeper resumes the goal
 * between turns, so Stop must remain reachable to pause it; ChatScreen.tsx's
 * composer render condition includes goalRunning). Waiting for toBeHidden on
 * a /goal session waits on the GOAL's lifecycle, which is unbounded by
 * construction.
 *
 * Why NOT the client's message state either: cancelStream
 * (src/store/chat/slices/outbound-lifecycle.ts) marks the last message
 * interrupted synchronously BEFORE the cancel frame is sent, so
 * data-status leaves "running" whether or not the server honours anything —
 * a client-state wait is vacuous exactly when the cancel is lost. Review
 * finding F1 on c6b3bed29.
 *
 * The server-derived answer (review round 3 on afb08e377 — findings D/F/H):
 * the wait settles only on evidence about THIS turn, never on a stale
 * record, and never on another session's state:
 *
 *   1. A scoped cancel-answer frame since the click — `done` or
 *      `cancel_stage` carrying THIS session's id, `error` with code
 *      "turn_canceled" (scoped when it carries a session_id; accepted
 *      unscoped because error is a global frame type), or
 *      `goal_status:waiting_on_user` for this session (the server's own
 *      keeper-pause announcement, pkg/agent/goal_triggers.go::
 *      pauseGoalKeeperForStop → EmitGoalStatusRehydrate).
 *   2. The session's lifecycle over REST leaving "working" AFTER "working"
 *      was observed post-click. GET /api/v1/sessions/{id} exposes
 *      `lifecycle_state` from the session's authoritative LifecycleRecord
 *      (pkg/gateway/rest_sessions.go::computeSessionLifecycle; the record
 *      is written at turn start and turn end). A pre-click non-working
 *      state is the PREVIOUS turn's record and is deliberately NOT
 *      accepted (finding D: it satisfied the wait in 53ms against an
 *      ignored cancel). Consequence, accepted deliberately: a Stop that
 *      lands after the turn already finished settles only via a scoped
 *      frame answer — the four call sites all click mid-turn (each waits
 *      for streaming to start first), so the raced case is rare and a
 *      false FAIL is preferred over a vacuous PASS. An ABSENT record also
 *      never settles the wait (fail closed): without a lifecycle record
 *      the REST arm cannot confirm anything about this turn, and only a
 *      scoped frame answer can.
 *      The wire exposes no generation/run id on Session (only
 *      lifecycle_state + stop_note), so the "generation changed" variant
 *      of this arm is not implementable from the wire; "working observed
 *      post-click, then non-working" is the implemented form.
 *
 * `waiting_for_answer` counts as ended only under arm 2's working-then-
 * terminal rule: a turn the server observed running and that then parked
 * on the user IS over, so proceeding to the next phase is safe. A
 * pre-click `waiting_for_answer` is a parked record like any other stale
 * terminal state and never settles the wait on its own (finding 6).
 *
 * W3 bound: a Stop pauses the goal keeper for that session until a user
 * message arrives after it (pkg/agent/goal_triggers.go::
 * pauseGoalKeeperForStop — "Stop paused the goal keeper … (the goal stays
 * active)"), so no freshly re-delivered goal turn can legitimately stretch
 * the wait inside this bound; 30s without a scoped frame and with no
 * working→terminal transition is a lost/ignored cancel. The specs that
 * share this helper send their next user message AFTER the helper returns,
 * which is what lifts the pause again.
 */
export async function endTurnDeterministically(page: Page): Promise<void> {
  const stop = page.locator('[data-testid="stop-btn"]')
  // No catch: isVisible() returns false for a missing element (nothing to
  // end — the ordinary already-settled path) and throws only on real page
  // errors, which must surface (review R2-4).
  if (!(await stop.isVisible())) return
  const log = logsByPage.get(page)
  if (!log) {
    throw new Error(
      'endTurnDeterministically: trackServerFrames(page) was never called for this page. ' +
        'Call it in the spec’s beforeEach BEFORE page.goto(\'/\') — the SPA’s websocket opens ' +
        'during goto, and Playwright only reports sockets opened after the listener attaches.',
    )
  }

  // Snapshots BEFORE the click (review finding H: a cancel_stage-only answer
  // landing during the click used to be swallowed into the "before" count
  // and the wait timed out in 3/3 scripted runs).
  const answersBeforeClick = log.answers.length
  const allTypesBeforeClick = log.allTypes.length
  const readLifecycle = async (): Promise<{ state?: string; note: string }> => {
    const sid = log.rootSessionId
    if (!sid) return { note: 'no root session id pinned yet (no session_started frame)' }
    let res: import('@playwright/test').APIResponse
    try {
      res = await page.request.get(`/api/v1/sessions/${sid}`, { failOnStatusCode: false })
    } catch (error) {
      return { note: `sessions REST error: ${String(error)}` }
    }
    if (res.status() !== 200) return { note: `sessions REST status ${res.status()}` }
    const body = (await res.json().catch(() => null)) as components['schemas']['SessionDetail'] | null
    if (!body?.session) return { note: 'sessions REST body had no session object' }
    const lifecycle = body.session.lifecycle_state
    return { state: lifecycle, note: `lifecycle_state=${lifecycle ?? 'absent (no lifecycle record)'}` }
  }
  const before = await readLifecycle()

  try {
    await stop.click({ timeout: 5_000 })
  } catch (err) {
    // The only tolerated failure is the turn settling between the
    // visibility check and the click — the button genuinely left the DOM.
    // Everything else (overlay intercepts, detached-while-clicking with the
    // element re-mounted, page closure) surfaces (review R2-4).
    if ((await stop.count()) === 0) {
      // already settled — nothing to stop
    } else {
      throw new Error(
        `endTurnDeterministically: the Stop click failed while the button was still mounted ` +
          `(pre-click server state: ${before.note}).`,
        { cause: err },
      )
    }
  }

  // "working at least once" includes the pre-click read: a pre-click
  // 'working' record is THIS turn, admitted — its later departure is this
  // turn's end. Only a pre-click NON-working record is a stale previous
  // turn and is disregarded (review finding D).
  let workingSeen = before.state === 'working'
  let lastObserved = before.note
  const serverSettled = async (): Promise<boolean> => {
    if (log.answers.length > answersBeforeClick) {
      lastObserved = `cancel-answer frame: ${log.answers[log.answers.length - 1]}`
      return true
    }
    const read = await readLifecycle()
    lastObserved = read.note
    if (read.state === 'working') {
      // THIS turn observed running server-side; its later departure is the
      // end signal (review finding D).
      workingSeen = true
      return false
    }
    if (read.state === undefined) return false // fail closed: no record, no confirmation
    return workingSeen
  }

  try {
    await expect
      .poll(serverSettled, { timeout: END_TURN_BOUND_MS, intervals: [250, 500, 1_000] })
      .toBe(true)
  } catch (err) {
    const answersSince = log.answers.slice(answersBeforeClick)
    const trafficSince = log.allTypes.slice(allTypesBeforeClick)
    throw new Error(
      `endTurnDeterministically: the server never settled THIS turn within ${END_TURN_BOUND_MS}ms of Stop — ` +
        `${lastObserved}; working observed post-click: ${workingSeen ? 'yes' : 'no'}. Cancel-answer frames ` +
        `since the click: ${answersSince.length === 0 ? 'none' : answersSince.join(', ')}; all server frame ` +
        `types since the click: ${trafficSince.length === 0 ? 'none' : trafficSince.join(', ')}. ` +
        'The cancel was lost or ignored (or the click landed outside any server-observed turn); ' +
        'the turn may still be running.',
      { cause: err },
    )
  }
}

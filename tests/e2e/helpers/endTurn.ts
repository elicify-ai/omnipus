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
  /** Accepted cancel answers: done / error:turn_canceled / cancel_stage:… */
  readonly answers: string[]
  /** Every server frame's type since tracking began — diagnostics only. */
  readonly allTypes: string[]
  /** Session id of the most recent frame that carried one (the Stop target). */
  lastSessionId: string | null
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
  const log: ServerFrameLog = { answers: [], allTypes: [], lastSessionId: null }
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
      if (typeof frame.session_id === 'string' && frame.session_id) log.lastSessionId = frame.session_id
      if (frame.type === 'done') log.answers.push('done')
      if (frame.type === 'error' && frame.code === 'turn_canceled') log.answers.push('error:turn_canceled')
      if (frame.type === 'cancel_stage') log.answers.push(`cancel_stage:${frame.stage ?? '?'}`)
    })
  })
  logsByPage.set(page, log)
}

/**
 * Shared endTurnDeterministically — click Stop if it is showing, then wait
 * for the SERVER to say the turn is over. Used by goal-work-first.spec.ts,
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
 * The server-derived answer — resolved by EITHER of two independent
 * signals, because no single wire frame covers every cancel branch:
 *
 *   1. A cancel-answer frame on the session's websocket: `done`, `error`
 *      with code "turn_canceled" (contracts ErrorFrame code enum; the same
 *      frame type the store treats as its cancel ack,
 *      src/store/chat/runtime-state.ts::CANCEL_ACK_FRAME_TYPES), or
 *      `cancel_stage` (sent on the armed / cascade / background-kill
 *      branches — pkg/gateway/websocket_cancel.go::handleCancelWithScope).
 *   2. The session's OWN lifecycle state over REST: GET /api/v1/sessions/
 *      {id} exposes `lifecycle_state`, populated from the session's
 *      authoritative LifecycleRecord (pkg/gateway/rest_sessions.go::
 *      computeSessionLifecycle; the record is written at turn start and
 *      turn end). "working" means the server still has the turn
 *      queued/running; any other value (or the record's absence on a
 *      session that never ran) means the server is not working the turn.
 *      This arm covers the plain idle-gap branch, where the gateway
 *      legitimately sends NO frame at all (verified: the no-active-turn
 *      branch only answers on the armed/cascade/background-kill paths).
 *
 * W3 bound: a Stop pauses the goal keeper for that session until a user
 * message arrives after it (pkg/agent/goal_triggers.go::
 * pauseGoalKeeperForStop — "Stop paused the goal keeper … (the goal stays
 * active)"), so no freshly re-delivered goal turn can legitimately stretch
 * the wait inside this bound; 30s without a frame AND with the server still
 * reporting "working" is a lost/ignored cancel. The specs that share this
 * helper send their next user message AFTER the helper returns, which is
 * what lifts the pause again.
 *
 * A genuinely lost cancel (the frame never reaches the gateway) keeps the
 * lifecycle record at "working" with no answer frame — the poll starves and
 * throws below with the observed frames and the last observed server state.
 */
export async function endTurnDeterministically(page: Page): Promise<void> {
  const stop = page.locator('[data-testid="stop-btn"]')
  if (!(await stop.isVisible().catch(() => false))) return
  const log = logsByPage.get(page)
  if (!log) {
    throw new Error(
      'endTurnDeterministically: trackServerFrames(page) was never called for this page. ' +
        'Call it in the spec’s beforeEach BEFORE page.goto(\'/\') — the SPA’s websocket opens ' +
        'during goto, and Playwright only reports sockets opened after the listener attaches.',
    )
  }

  await stop.click().catch(() => {
    /* already settled between the check and the click — nothing to stop */
  })
  const answersBeforeClick = log.answers.length
  const allTypesBeforeClick = log.allTypes.length

  let lastObserved = 'poll never ran'
  const serverSettled = async (): Promise<boolean> => {
    if (log.answers.length > answersBeforeClick) {
      lastObserved = `cancel-answer frame: ${log.answers[log.answers.length - 1]}`
      return true
    }
    const sid = log.lastSessionId
    if (!sid) {
      lastObserved = 'no session id captured from any frame yet'
      return false
    }
    const res = await page.request
      .get(`/api/v1/sessions/${sid}`, { failOnStatusCode: false })
      .catch((error: unknown) => {
        lastObserved = `sessions REST error: ${String(error)}`
        return null
      })
    if (!res || res.status() !== 200) {
      lastObserved = `sessions REST status ${res ? res.status() : 'error'}`
      return false
    }
    const body = (await res.json().catch(() => null)) as components['schemas']['SessionDetail'] | null
    if (!body?.session) {
      lastObserved = 'sessions REST body had no session object'
      return false
    }
    const lifecycle = body.session.lifecycle_state
    lastObserved = `server lifecycle_state=${lifecycle ?? 'absent (no lifecycle record)'}`
    return lifecycle !== undefined && lifecycle !== 'working'
  }

  try {
    await expect
      .poll(serverSettled, { timeout: END_TURN_BOUND_MS, intervals: [250, 500, 1_000] })
      .toBe(true)
  } catch (err) {
    const answersSince = log.answers.slice(answersBeforeClick)
    const trafficSince = log.allTypes.slice(allTypesBeforeClick)
    throw new Error(
      `endTurnDeterministically: the server never settled the turn within ${END_TURN_BOUND_MS}ms of Stop — ` +
        `${lastObserved}. Cancel-answer frames since the click: ` +
        `${answersSince.length === 0 ? 'none' : answersSince.join(', ')}; all server frame types since the click: ` +
        `${trafficSince.length === 0 ? 'none' : trafficSince.join(', ')}. The cancel was lost or ignored; ` +
        'the turn may still be running.',
      { cause: err },
    )
  }
}

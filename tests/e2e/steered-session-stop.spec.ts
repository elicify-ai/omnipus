/**
 * steered-session-stop.spec.ts — ADR-091 fix-lane 7, Gap 6: no end-to-end
 * test presses Stop from the UI on a steered (delegated) session. Cancel
 * cascade behaviour is well covered in Go (pkg/agent/steer_cancel*_test.go)
 * and the WS cascade frame is covered at the gateway layer, but nothing
 * before this file drove the actual Stop BUTTON and verified the side-panel
 * row reaches a stopped state.
 *
 * Deterministic by construction (per this task's explicit instruction): the
 * steered child is seeded through REST + direct on-disk fixtures — the same
 * seam session-setup.ts's seedTranscript already uses for replay-fidelity —
 * NOT by asking a model to delegate. No LLM, no OPENROUTER key, no timing
 * flakiness from a real provider round-trip.
 *
 * Seeding mechanism:
 *   1. Create the ROOT session via REST (POST /api/v1/sessions).
 *   2. Create the CHILD session via REST, the same way.
 *   3. Persist a `steered`, `running` LifecycleRecord for the child directly
 *      under $OMNIPUS_HOME/session_lifecycle/<child-id>.jsonl — the exact
 *      on-disk shape pkg/session/lifecycle.go's LifecycleRecord/SteeredBy
 *      marshal to (pkg/agent/steer_launcher.go::launchSteered writes this
 *      same shape in production).
 *   4. Append a `subagent_start` + `subagent_state(state:"running")` pair
 *      into the ROOT's own transcript.jsonl — the exact persisted-frame
 *      mechanism pkg/agent/steer_frames.go's deliverSubagentStart writes in
 *      production (ADR-091 D7/I-4: "persisted as events in the PARENT's
 *      own transcript ... so the existing since-cursor replay returns them
 *      after a reload with no new store"). This is what
 *      tests/adr091/steered_sessions_test.go::TestE2E_TaskChildInSidePanel
 *      (Go) proves replay needs to restore the side-panel row.
 *   5. Open the ROOT via deep link — the real WS attach + replay path
 *      (openSessionByDeepLink) reconstructs the ActivityPanel row from
 *      those persisted frames.
 *
 * ADR-091 fix lane RX-FRONTEND, Defect 2 — two bugs fixed here, both found
 * by re-deriving what the system actually emits/renders instead of trusting
 * this file's own prior assertions:
 *
 * 1. WRONG STATUS VALUE. `ActivityPanel.tsx::ActivityRow` stamps
 *    `data-status` from `item.status` — the SPAN axis (span.status, cleared
 *    only by `subagent_end`) — never from `lifecycleState` (the
 *    `subagent_state` axis). On a Stop, the Go side emits DIFFERENT values on
 *    each axis (verified against source, not assumed):
 *      - `pkg/agent/steer_frames.go::deliverSubagentEnd` maps
 *        `steer.OutcomeInterrupted` -> `SubTurnStatusInterrupted`, i.e.
 *        `subagent_end.status = "interrupted"` — this is `item.status`, i.e.
 *        `data-status`.
 *      - `pkg/agent/steer_audience.go` (~line 182-183) separately maps that
 *        SAME `steer.OutcomeInterrupted` -> `session.LifecycleCancelled`
 *        ("cancelled") for the `subagent_state` frame — this is
 *        `lifecycleState`, rendered as the row's visible status TEXT via
 *        `getLifecycleStatusDot` (`src/lib/subagentStatus.tsx`), never as
 *        `data-status`.
 *    The old assertion (`data-status: 'cancelled'`) read the second value off
 *    the first attribute — a value the system never emits there. Fixed below
 *    to assert `data-status: 'interrupted'` and the visible label "cancelled".
 *
 * 2. UNREACHABLE STOP BUTTON. `ChatScreen.tsx`'s `stop-btn` only renders
 *    while `isStreaming` is true for the currently attached session, and
 *    `cancelStream()` (src/store/chat/slices/outbound-lifecycle.ts) ADDITIONALLY
 *    gates its own network send on that same flag — both are populated only
 *    by a real client-initiated turn or by a `session_state.active_turn`
 *    snapshot the gateway derives from a REAL in-memory turn object. This
 *    spec's child is seeded purely on disk (a `LifecycleRecord` row plus
 *    `subagent_start`/`subagent_state` transcript entries, both written
 *    directly by Node, never through a real Launch/Dispatch) — no in-memory
 *    turn is ever created for it, so `isStreaming` can never become true and
 *    `stop-btn` can never render. Waiting on it was a permanent hang, not a
 *    slow pass.
 *
 *    Fix: `sendCancelFrame` below sends the byte-identical wire frame a real
 *    Stop click sends — `{type:"cancel",session_id}`, exactly
 *    `contracts/components/schemas/CancelFrame.yaml` /
 *    `outbound-lifecycle.ts`'s own `connection.send({ type: 'cancel',
 *    session_id: targetSid })` — over a SECOND, same-origin WebSocket opened
 *    from inside the page. No separate auth is needed: `pkg/gateway/
 *    websocket.go`'s upgrade handler authenticates purely off the session
 *    cookie the browser already carries (verified: "the SPA always attaches
 *    the same-origin session cookie" / no client `{"type":"auth",...}` frame
 *    needed on that path), and `chat_id` is server-assigned per connection
 *    (`pkg/gateway/websocket.go:592`), not client-supplied — so a fresh
 *    connection needs no setup before sending. This exercises the REAL
 *    `pkg/gateway/websocket_cancel.go::handleCancel` ->
 *    `cancelSteeredSubtree` -> `pkg/agent/steer_cancel.go` cascade
 *    end-to-end; only the UI click itself is synthesized, because this
 *    fixture's seeding strategy makes that click structurally unreachable no
 *    matter what the test does downstream of it.
 *
 * IMPORTANT — still not verified by an observed pass. This lane (RX-FRONTEND,
 * frontend-only: src/** and tests/e2e/** ownership, no Go edits, no live
 * gateway) confirmed `tests/e2e/global-setup.ts::preflightCheck` hard-fails
 * the ENTIRE Playwright suite — including this deterministic, no-LLM spec —
 * unless `OPENROUTER_API_KEY_CI` is set, and no built Omnipus binary /
 * bootstrapped `$OMNIPUS_HOME` was available in this environment either. Both
 * fixes above are grounded in direct reads of the cited Go source and
 * contract files, not in an observed pass — `bash scripts/e2e-shards.sh check`
 * (shard registration, needs no live gateway) was run and passes; the spec
 * itself was not executed. Whoever next runs this against a built binary
 * should treat any further mismatch as a fresh finding, not a defect in this
 * fix.
 */

import * as fs from 'fs'
import * as path from 'path'
import { expect, type Page } from '@playwright/test'
import { test } from './fixtures/console-errors'
import { chatInput, waitForConnected } from './fixtures/selectors'
import { openSessionByDeepLink } from './fixtures/session-setup'

const BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6060'
const OMNIPUS_HOME =
  process.env.OMNIPUS_HOME ||
  (process.env.HOME ? path.join(process.env.HOME, '.omnipus') : '/tmp/omnipus-e2e-test')

function getStoredAuthToken(): string | null {
  const authFile = process.env.OMNIPUS_AUTH_FILE
    ? path.resolve(process.env.OMNIPUS_AUTH_FILE)
    : path.join(path.dirname(new URL(import.meta.url).pathname), 'fixtures/.auth/admin.json')
  if (!fs.existsSync(authFile)) return null
  try {
    const state = JSON.parse(fs.readFileSync(authFile, 'utf-8')) as {
      origins?: Array<{ origin: string; localStorage?: Array<{ name: string; value: string }> }>
    }
    for (const origin of state.origins ?? []) {
      for (const item of origin.localStorage ?? []) {
        if (item.name === 'omnipus_auth_token') return item.value
      }
    }
  } catch {
    // no auth file yet
  }
  return null
}

async function getCsrfToken(page: Page): Promise<string | null> {
  const cookies = await page.context().cookies()
  return cookies.find(c => c.name === '__Host-csrf' || c.name === 'csrf')?.value ?? null
}

async function apiHeaders(page: Page): Promise<Record<string, string>> {
  const authToken = getStoredAuthToken()
  const csrfToken = await getCsrfToken(page)
  return {
    'Content-Type': 'application/json',
    ...(authToken ? { Authorization: `Bearer ${authToken}` } : {}),
    ...(csrfToken ? { 'X-CSRF-Token': csrfToken } : {}),
  }
}

async function createSession(page: Page, type: 'chat' | 'delegate' = 'chat'): Promise<string> {
  const resp = await page.request.post(`${BASE_URL}/api/v1/sessions`, {
    headers: await apiHeaders(page),
    data: { agent_id: 'mia', type },
  })
  if (!resp.ok()) throw new Error(`POST /api/v1/sessions failed: ${resp.status()} — ${await resp.text()}`)
  const meta = (await resp.json()) as { id: string }
  if (!meta.id) throw new Error('POST /api/v1/sessions returned no id')
  return meta.id
}

/**
 * Seed a `steered`, `running` LifecycleRecord for childId directly on disk —
 * the shape pkg/agent/steer_launcher.go::launchSteered persists (see
 * pkg/session/lifecycle.go's LifecycleRecord and SteeredBy field tags).
 */
function seedSteeredLifecycleRecord(childId: string, rootId: string, callId: string): void {
  const dir = path.join(OMNIPUS_HOME, 'session_lifecycle')
  fs.mkdirSync(dir, { recursive: true })
  const record = {
    session_id: childId,
    generation: 1,
    state: 'running',
    origin: { kind: 'delegate', call_id: callId },
    steered_by: {
      steering_session_id: rootId,
      root_session_id: rootId,
      reporting_target: { session_id: rootId, channel: 'webchat', chat_id: rootId },
      authorization: { mode: 'direct', remaining_depth: 3 },
      limits: { timeout_seconds: 0 },
    },
    owner_scope_kind: 'parent_session',
    owner_scope_id: rootId,
    workspace_id: '',
    agent_id: 'mia',
    is_3p: false,
  }
  fs.writeFileSync(path.join(dir, `${childId}.jsonl`), JSON.stringify(record) + '\n', { encoding: 'utf-8' })
}

/**
 * Append the subagent_start + subagent_state(running) pair into the ROOT's
 * own transcript — see pkg/agent/steer_frames.go's deliverSubagentStart and
 * pkg/session/daypartition.go's TranscriptEntry.Subagent* fields.
 */
function seedSubagentFramesInParentTranscript(rootId: string, childId: string, callId: string, taskLabel: string): void {
  const sessionDir = path.join(OMNIPUS_HOME, 'sessions', rootId)
  if (!fs.existsSync(sessionDir)) {
    throw new Error(`Root session directory does not exist: ${sessionDir}. Create it via REST first.`)
  }
  const now = new Date().toISOString()
  const spanId = `span_${callId}`
  const startEntry = {
    id: `${callId}:start`,
    type: 'system',
    system_subtype: 'subagent_start',
    timestamp: now,
    agent_id: 'mia',
    subagent_start: {
      type: 'subagent_start',
      session_id: rootId,
      child_session_id: childId,
      span_id: spanId,
      parent_call_id: callId,
      task_label: taskLabel,
      agent_id: 'mia',
    },
  }
  const stateEntry = {
    id: `${callId}:state`,
    type: 'system',
    system_subtype: 'subagent_state',
    timestamp: now,
    agent_id: 'mia',
    subagent_state: {
      type: 'subagent_state',
      session_id: rootId,
      child_session_id: childId,
      span_id: spanId,
      state: 'running',
      created_at: now,
    },
  }
  const transcriptPath = path.join(sessionDir, 'transcript.jsonl')
  const lines = [startEntry, stateEntry].map(e => JSON.stringify(e)).join('\n') + '\n'
  fs.appendFileSync(transcriptPath, lines, { encoding: 'utf-8' })
}

/**
 * Sends `{type:"cancel", session_id: sessionId}` — the exact `CancelFrame`
 * (contracts/components/schemas/CancelFrame.yaml) a real Stop click sends
 * (outbound-lifecycle.ts:757's `connection.send({ type: 'cancel',
 * session_id: targetSid })`) — over a FRESH, same-origin WebSocket opened
 * from inside the page.
 *
 * Why not press the real `stop-btn`: it (and `cancelStream()`'s own network
 * send) is gated on `isStreaming`, which only ever becomes true for a
 * REAL in-memory turn — never for this spec's purely-disk-seeded child (see
 * this file's header comment, Defect 2 fix #2). This sends the same wire
 * frame without needing that gate. No extra auth setup is required: the
 * gateway's WS upgrade authenticates off the session cookie the browser
 * already carries, and `chat_id` is server-assigned per connection
 * (pkg/gateway/websocket.go:592), so a brand-new connection can send this
 * frame immediately after opening.
 */
async function sendCancelFrame(page: Page, sessionId: string): Promise<void> {
  await page.evaluate((sid) => {
    return new Promise<void>((resolve, reject) => {
      const wsBase = window.location.origin.replace(/^http/, 'ws')
      const socket = new WebSocket(`${wsBase}/api/v1/chat/ws`)
      const timer = setTimeout(() => {
        socket.close()
        reject(new Error('sendCancelFrame: socket did not open within 10s'))
      }, 10_000)
      socket.onopen = () => {
        socket.send(JSON.stringify({ type: 'cancel', session_id: sid }))
        // WebSocket.send() returns synchronously but the frame is flushed
        // asynchronously — give it a beat before tearing the socket down.
        setTimeout(() => {
          clearTimeout(timer)
          socket.close()
          resolve()
        }, 500)
      }
      socket.onerror = () => {
        clearTimeout(timer)
        reject(new Error('sendCancelFrame: socket errored before opening'))
      }
    })
  }, sessionId)
}

test(
  'stopping a steered session row from the activity panel reaches a stopped state and survives reload',
  async ({ page }) => {
    await page.goto('/')
    await expect(page.getByRole('banner')).toBeVisible({ timeout: 15_000 })
    await expect(chatInput(page)).toBeEnabled({ timeout: 15_000 })
    await waitForConnected(page, { timeout: 15_000 })

    const rootId = await createSession(page, 'chat')
    const childId = await createSession(page, 'delegate')
    const callId = `adr091-stop-spec-${Date.now()}`
    const taskLabel = `steered-session-stop-${Date.now()}`

    seedSteeredLifecycleRecord(childId, rootId, callId)
    // Root's session directory must exist (created by the REST call above)
    // before its transcript can be appended to.
    seedSubagentFramesInParentTranscript(rootId, childId, callId, taskLabel)

    await openSessionByDeepLink(page, rootId)

    // Open the activity panel and find the seeded worker's row.
    const activityBar = page.locator('[data-testid="activity-bar"]')
    await expect(activityBar).toBeVisible({ timeout: 15_000 })
    await activityBar.click()

    const row = page.locator('[data-testid="activity-row"]', { hasText: taskLabel })
    await expect(row).toBeVisible({ timeout: 15_000 })
    await expect(row).toHaveAttribute('data-status', 'running')

    // Open the row (drives into the worker's own session) — this still
    // exercises the real "Open" navigation control. The real Stop click
    // that would normally follow is unreachable here (`stop-btn` only
    // renders while `isStreaming`, which this disk-seeded child never sets
    // — see this file's header comment, Defect 2 fix #2) — send the
    // equivalent `cancel` wire frame directly instead. Stop on a session
    // stops everything below it (pkg/agent/CLAUDE.md); this targets the
    // worker's OWN session id, matching what a real Stop click sends while
    // attached to it (`cancelStream()` with no explicit sessionId defaults
    // to the active session).
    await row.locator('[data-testid="activity-row-open"]').click()
    await sendCancelFrame(page, childId)

    // Back to the root and re-open the panel to read the row's settled status.
    await openSessionByDeepLink(page, rootId)
    await activityBar.click()
    const rowAfterStop = page.locator('[data-testid="activity-row"]', { hasText: taskLabel })

    // `data-status` is `ActivityRow`'s SPAN axis (`item.status`, from
    // `subagent_end.status`) — a Stop maps to `steer.OutcomeInterrupted`,
    // which `deliverSubagentEnd` (pkg/agent/steer_frames.go) stamps as
    // "interrupted", never "cancelled". "cancelled" is the SEPARATE
    // `lifecycleState` axis (`subagent_state.state`, pkg/agent/
    // steer_audience.go ~182-183) that never reaches `data-status` — it
    // only drives the row's visible label (getLifecycleStatusDot,
    // src/lib/subagentStatus.tsx). Assert both, on their correct axes, and
    // explicitly rule out the states an interrupted-but-mishandled Stop
    // most often gets confused with.
    await expect(rowAfterStop).toHaveAttribute('data-status', 'interrupted', { timeout: 15_000 })
    await expect(rowAfterStop).not.toHaveAttribute('data-status', 'failed')
    await expect(rowAfterStop).not.toHaveAttribute('data-status', 'running')
    await expect(rowAfterStop.getByText('cancelled', { exact: true })).toBeVisible()

    // Reload and confirm the stopped state was durably persisted, not just
    // held in the client's in-memory store.
    await page.reload()
    await expect(chatInput(page)).toBeEnabled({ timeout: 15_000 })
    await waitForConnected(page, { timeout: 15_000 })
    await activityBar.click()
    const rowAfterReload = page.locator('[data-testid="activity-row"]', { hasText: taskLabel })
    await expect(rowAfterReload).toHaveAttribute('data-status', 'interrupted', { timeout: 15_000 })
    await expect(rowAfterReload.getByText('cancelled', { exact: true })).toBeVisible()
  },
)

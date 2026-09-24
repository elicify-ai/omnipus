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
 * IMPORTANT — unverified by execution: per this lane's explicit instruction
 * ("Do not run the browser suite — it needs a built binary. Just add the
 * spec and confirm `bash scripts/e2e-shards.sh check` passes"), this spec
 * has NOT been run against a live gateway. The on-disk shapes above are
 * grounded in direct reads of pkg/session/lifecycle.go, pkg/session/
 * daypartition.go and pkg/agent/steer_frames.go (cited inline), not in an
 * observed pass. If the ActivityPanel's replay reconstruction turns out to
 * need additional persisted state (e.g. a `subagent_message` frame, or a
 * live WS push rather than pure replay), that is a finding for whoever
 * first runs this spec against a built binary, not a defect in the seeding
 * approach itself.
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

    // Open the row (drives into the worker's own session) and press the
    // real Stop control there — Stop on a session stops everything below
    // it (pkg/agent/CLAUDE.md), including this worker's own turn.
    await row.locator('[data-testid="activity-row-open"]').click()
    const stopBtn = page.locator('[data-testid="stop-btn"]')
    await expect(stopBtn).toBeVisible({ timeout: 15_000 })
    await stopBtn.click()

    // Back to the root and re-open the panel to read the row's settled status.
    await openSessionByDeepLink(page, rootId)
    await activityBar.click()
    const rowAfterStop = page.locator('[data-testid="activity-row"]', { hasText: taskLabel })

    // The product's own status vocabulary is "cancelled" — never "stopped"
    // (contracts/components/schemas/SessionLifecycleRecord.yaml,
    // pkg/session/lifecycle.go's LifecycleCancelled = "cancelled") — a Stop
    // is recorded as a cancellation, not a bespoke "stopped" state. Assert
    // the real wire value, and explicitly rule out the two states an
    // interrupted-but-mishandled Stop most often gets confused with.
    await expect(rowAfterStop).toHaveAttribute('data-status', 'cancelled', { timeout: 15_000 })
    await expect(rowAfterStop).not.toHaveAttribute('data-status', 'failed')
    await expect(rowAfterStop).not.toHaveAttribute('data-status', 'running')

    // Reload and confirm the stopped state was durably persisted, not just
    // held in the client's in-memory store.
    await page.reload()
    await expect(chatInput(page)).toBeEnabled({ timeout: 15_000 })
    await waitForConnected(page, { timeout: 15_000 })
    await activityBar.click()
    const rowAfterReload = page.locator('[data-testid="activity-row"]', { hasText: taskLabel })
    await expect(rowAfterReload).toHaveAttribute('data-status', 'cancelled', { timeout: 15_000 })
  },
)

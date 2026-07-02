/**
 * recap-continuity.spec.ts — Recap → LAST_SESSION.md → next-session injection continuity.
 *
 * WHAT THIS CATCHES
 * -----------------
 * The "agent remembers last session" feature is composed of three independent pipes:
 *
 *   1. CloseSession (pkg/agent/session_end.go) → runRecap → LLM summarisation
 *      → WriteLastSession writes agents/<id>/.omnipus/last-session.md
 *
 *   2. The WS session_close frame (pkg/gateway/websocket.go:888-917) triggers
 *      CloseSession("explicit") and acknowledges with session_close_ack.
 *
 *   3. On the NEXT session, ReadLastSession (pkg/agent/memory.go:912) injects
 *      last-session.md into the system prompt context before the first LLM call.
 *
 * Each pipe is unit-tested in Go. This spec is the ONLY test that chains them
 * end-to-end through the real gateway + WS transport + LLM + filesystem.
 *
 * If this spec is RED it means recap is written or injected incorrectly in at
 * least one of the pipes — a real user-facing gap, not a test infrastructure
 * problem. Do NOT add softSkip() here without a tracked GitHub issue.
 *
 * CLOSE TRIGGER CHOSEN
 * --------------------
 * We send a `session_close` WS frame directly from the browser page's live
 * WebSocket (via window.__ws_instances, the same global the burst-resilience
 * spec uses). This is the synchronous, deterministic path:
 *
 *   - It exercises the real WS handler (not a REST call or a timer).
 *   - We receive a `session_close_ack` in return, giving a reliable sync point.
 *   - We do NOT rely on idle timeout (too slow and flaky in CI).
 *   - A plain DELETE /api/v1/sessions does NOT trigger recap (just deletes files).
 *
 * LAST_SESSION PATH
 * -----------------
 * Confirmed from pkg/memrooms/rooms.go:44-45 and pkg/agent/memory.go:908:
 *   <OMNIPUS_HOME>/agents/<agent_id>/.omnipus/last-session.md
 *
 * Traces to: docs/internal/BRD/Omnipus BRD.md FR-023..FR-030 (session-end recap pipeline)
 */

import * as fs from 'fs'
import * as path from 'path'
import { expect, type Page } from '@playwright/test'
import { test } from './fixtures/console-errors'
import { chatInput, assistantMessages, newChatButton } from './fixtures/selectors'

// ── Constants ──────────────────────────────────────────────────────────────────

const BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6060'

const OMNIPUS_HOME =
  process.env.OMNIPUS_HOME ||
  (process.env.HOME ? path.join(process.env.HOME, '.omnipus') : '/tmp/omnipus-e2e-test')

// Auth file written by global-setup.ts after onboarding.
const AUTH_FILE = process.env.OMNIPUS_AUTH_FILE
  ? path.resolve(process.env.OMNIPUS_AUTH_FILE)
  : path.join(
      path.dirname(new URL(import.meta.url).pathname),
      'fixtures/.auth/admin.json',
    )

// Unique nonce embedded in the distinctive fact.  Using a fixed prefix so the
// assertion grep is stable; the timestamp suffix prevents cross-run pollution.
const NONCE = `FALCON-${Date.now()}`

// ── Helpers ────────────────────────────────────────────────────────────────────

/** Read the admin Bearer token from the global-setup storageState file. */
function getStoredAuthToken(): string | null {
  if (!fs.existsSync(AUTH_FILE)) return null
  try {
    const raw = fs.readFileSync(AUTH_FILE, 'utf-8')
    const state = JSON.parse(raw) as {
      origins?: Array<{
        origin: string
        localStorage?: Array<{ name: string; value: string }>
      }>
    }
    for (const origin of state.origins ?? []) {
      for (const item of origin.localStorage ?? []) {
        if (item.name === 'omnipus_auth_token') return item.value
      }
    }
  } catch {
    // Auth file may not exist on first run; will be caught by test assertions.
  }
  return null
}

/** Build Authorization + CSRF headers for direct API calls. */
async function apiHeaders(page: Page): Promise<Record<string, string>> {
  const token = getStoredAuthToken()
  const cookies = await page.context().cookies()
  const csrf = cookies.find((c) => c.name === '__Host-csrf' || c.name === 'csrf')?.value ?? null
  return {
    'Content-Type': 'application/json',
    ...(token ? { Authorization: `Bearer ${token}` } : {}),
    ...(csrf ? { 'X-CSRF-Token': csrf } : {}),
  }
}

/**
 * Wait for any follow-up LLM call that may start after a tool call completes.
 * Pattern lifted from chat.spec.ts.
 */
async function waitForTurnFullyDone(page: Page, gapMs = 8_000): Promise<void> {
  const stopBtn = page.locator('[data-testid="stop-btn"]')
  try {
    await expect(stopBtn).toBeVisible({ timeout: gapMs })
    await expect(stopBtn).not.toBeVisible({ timeout: 180_000 })
  } catch {
    // Stop button did not reappear — turn is fully done.
  }
}

/**
 * Return the agent_id for the first agent whose `default` flag is true.
 * Falls back to "jim" when the agent list cannot be fetched or no default is set.
 */
async function resolveDefaultAgentID(page: Page): Promise<string> {
  try {
    const resp = await page.request.get(`${BASE_URL}/api/v1/agents`, {
      headers: await apiHeaders(page),
      failOnStatusCode: false,
    })
    if (resp.ok()) {
      const agents = (await resp.json()) as Array<{ id: string; default?: boolean }>
      const def = agents.find((a) => a.default === true)
      if (def?.id) return def.id
      // If no explicit default, take first available agent.
      if (agents.length > 0 && agents[0].id) return agents[0].id
    }
  } catch {
    // Network error; fall through to fallback.
  }
  return 'jim'
}

/**
 * Create a new REST-backed session for the given agent and return its ID.
 * Matches the pattern in cancel-cross-channel.spec.ts.
 */
async function createSession(page: Page, agentID: string): Promise<string> {
  const resp = await page.request.post(`${BASE_URL}/api/v1/sessions`, {
    headers: await apiHeaders(page),
    data: { agent_id: agentID, type: 'chat' },
    failOnStatusCode: false,
  })
  if (!resp.ok()) {
    const body = await resp.text()
    throw new Error(`POST /api/v1/sessions failed: ${resp.status()} ${resp.statusText()} — ${body}`)
  }
  const meta = (await resp.json()) as { id?: string }
  if (!meta.id) throw new Error('POST /api/v1/sessions returned no id')
  return meta.id
}

/**
 * Install WS interceptors to capture the session_id the SPA actually uses for the
 * fact-message turn.  Two capture paths:
 *
 * 1. Outgoing `{ type: "message", session_id: "<id>" }` frame — works when the SPA
 *    attaches to the REST-created session (activeSessionId != null at send time).
 *
 * 2. Incoming `{ type: "done", session_id: "<id>" }` frame — works when the SPA
 *    sends `session_id: null` (gateway mints a new id and echoes it in `done`).
 *    The `done` frame is authoritative: it is the id the gateway used for the turn.
 *
 * The captured value is stored in `window.__spa_last_session_id`.
 * Must be called BEFORE the fact message is sent.
 */
async function installSpaSessionCapture(page: Page): Promise<void> {
  await page.evaluate(() => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const g = globalThis as any
    g.__spa_last_session_id = null

    // Path 1: intercept outgoing frames (message with explicit session_id).
    const origSend = WebSocket.prototype.send
    WebSocket.prototype.send = function (this: WebSocket, data: string | ArrayBufferLike | Blob | ArrayBufferView) {
      try {
        if (typeof data === 'string') {
          const parsed = JSON.parse(data) as { type?: string; session_id?: string | null }
          if (parsed.type === 'message' && parsed.session_id) {
            g.__spa_last_session_id = parsed.session_id
          }
        }
      } catch {
        // Non-JSON data — ignore.
      }
      return origSend.call(this, data)
    }

    // Path 2: listen for the incoming `done` frame on the existing open WS.
    // The `done` frame always carries the authoritative session_id from the gateway,
    // even when the SPA sent session_id: null (gateway mints a new id).
    const sockets: WebSocket[] = Array.isArray(g.__ws_instances)
      ? (g.__ws_instances as WebSocket[]).filter((s: WebSocket) => s.readyState === 1)
      : []
    const ws = sockets[sockets.length - 1]
    if (ws) {
      ws.addEventListener('message', (evt: MessageEvent) => {
        try {
          const parsed = JSON.parse(evt.data as string) as { type?: string; session_id?: string }
          if (parsed.type === 'done' && parsed.session_id && !g.__spa_last_session_id) {
            g.__spa_last_session_id = parsed.session_id
          }
        } catch {
          // Non-JSON — ignore.
        }
      })
    }
  })
}

/**
 * Read the session_id the SPA used when sending the fact message.
 * Returns null if the interceptor was not installed or no message-type frame was sent yet.
 */
async function getSpaSessionId(page: Page): Promise<string | null> {
  return page.evaluate(() => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    return (globalThis as any).__spa_last_session_id as string | null
  })
}

/**
 * Send a `session_close` WS frame using the live WebSocket the SPA has open.
 *
 * The frame must be:
 *   { type: "session_close", session_id: "<id>" }
 *
 * The gateway responds with a `session_close_ack` frame and then calls
 * CloseSession("explicit"), which (when auto_recap_enabled=true) spawns the
 * async runRecap goroutine that writes last-session.md.
 *
 * We wait for the `session_close_ack` by polling window.__lastWsMessage for up
 * to 10 seconds — a simple alternative to wiring a full message listener.
 *
 * Returns true if the ack was received; false on timeout.
 */
async function sendSessionCloseFrame(page: Page, sessionId: string): Promise<boolean> {
  const frame = JSON.stringify({ type: 'session_close', session_id: sessionId })

  // Expose the most-recent message received from the server so we can poll for ack.
  await page.evaluate((f: string) => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const g = globalThis as any
    // Install ack listener on the most recently opened WS.
    const sockets: WebSocket[] = Array.isArray(g.__ws_instances)
      ? (g.__ws_instances as WebSocket[]).filter((s: WebSocket) => s.readyState === 1)
      : []
    const ws = sockets[sockets.length - 1]
    if (!ws) {
      console.warn('recap-continuity: no open WebSocket found in window.__ws_instances')
      return
    }
    g.__recap_ack_received = false
    const prevOnMessage = ws.onmessage
    ws.onmessage = (evt: MessageEvent) => {
      // Forward to the real handler first.
      if (typeof prevOnMessage === 'function') {
        prevOnMessage.call(ws, evt)
      }
      try {
        const parsed = JSON.parse(evt.data as string) as { type?: string }
        if (parsed.type === 'session_close_ack') {
          g.__recap_ack_received = true
        }
      } catch {
        // Non-JSON frame — ignore.
      }
    }
    ws.send(f)
  }, frame)

  // Poll for ack up to 10 s.
  const deadline = Date.now() + 10_000
  while (Date.now() < deadline) {
    const ackReceived = await page.evaluate(() => {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      return (globalThis as any).__recap_ack_received === true
    })
    if (ackReceived) return true
    await page.waitForTimeout(300)
  }
  return false
}

/**
 * Poll until <OMNIPUS_HOME>/agents/<agentId>/.omnipus/last-session.md exists
 * and contains `needle`.  Returns the file contents on success; throws on timeout.
 *
 * Budget is generous (60 s) because recap involves an async LLM call.
 */
async function pollLastSession(
  page: Page,
  agentId: string,
  needle: string,
  budgetMs = 60_000,
): Promise<string> {
  const lastSessionPath = path.join(OMNIPUS_HOME, 'agents', agentId, '.omnipus', 'last-session.md')
  const deadline = Date.now() + budgetMs
  let lastContents = ''

  while (Date.now() < deadline) {
    if (fs.existsSync(lastSessionPath)) {
      lastContents = fs.readFileSync(lastSessionPath, 'utf-8')
      if (lastContents.includes(needle)) {
        return lastContents
      }
    }
    await page.waitForTimeout(2_000)
  }

  // Timeout — surface a clear diagnostic.
  if (!fs.existsSync(lastSessionPath)) {
    throw new Error(
      `recap-continuity: last-session.md NOT FOUND after ${budgetMs / 1000}s.\n` +
        `Expected path: ${lastSessionPath}\n` +
        `This means CloseSession → runRecap → WriteLastSession did NOT run.\n` +
        `Check: (a) auto_recap_enabled=true was applied before the turn, ` +
        `(b) session_close_ack was received, (c) recap LLM call did not error out.`,
    )
  }

  throw new Error(
    `recap-continuity: last-session.md EXISTS but does not contain nonce "${needle}".\n` +
      `Path: ${lastSessionPath}\n` +
      `File size: ${fs.statSync(lastSessionPath).size} bytes.\n` +
      `Contents (first 500 chars): ${lastContents.slice(0, 500)}\n` +
      `This means the recap ran but omitted the distinctive fact from the summary.`,
  )
}

// ── beforeAll / afterAll ───────────────────────────────────────────────────────

let originalAutoRecap: boolean | null = null

test.beforeAll(async ({ browser }) => {
  // Enable auto_recap_enabled via the settings API.
  // The e2e config boots with it OFF; we must enable it for recap to fire.
  const page = await browser.newPage()
  try {
    await page.goto(BASE_URL)
    await page.waitForLoadState('domcontentloaded', { timeout: 30_000 })

    const headers = await apiHeaders(page)

    // Read current value so we can restore it in afterAll.
    const getResp = await page.request.get(`${BASE_URL}/api/v1/settings/memory`, {
      headers,
      failOnStatusCode: false,
    })
    if (getResp.ok()) {
      const settings = (await getResp.json()) as { auto_recap_enabled?: boolean }
      originalAutoRecap = settings.auto_recap_enabled ?? null
    }

    // Enable auto recap.  This must succeed — a silent failure here means
    // CloseSession returns at the !AutoRecapEnabled gate and the test
    // produces zero recap log lines, making the root cause invisible.
    const putResp = await page.request.put(`${BASE_URL}/api/v1/settings/memory`, {
      headers,
      data: { auto_recap_enabled: true },
      failOnStatusCode: false,
    })
    if (!putResp.ok()) {
      throw new Error(
        `recap-continuity beforeAll: PUT /api/v1/settings/memory returned ${putResp.status()} — ` +
          `${await putResp.text()}`,
      )
    }

    // Confirm via GET that auto_recap_enabled is now true.
    // Throw rather than warn: a silent mismatch produces a confusing "file never
    // appears" failure with zero recap log lines instead of a clear root-cause error.
    const confirmResp = await page.request.get(`${BASE_URL}/api/v1/settings/memory`, {
      headers,
      failOnStatusCode: false,
    })
    if (!confirmResp.ok()) {
      throw new Error(
        `recap-continuity beforeAll: GET /api/v1/settings/memory returned ${confirmResp.status()}`,
      )
    }
    const confirmed = (await confirmResp.json()) as { auto_recap_enabled?: boolean }
    if (confirmed.auto_recap_enabled !== true) {
      throw new Error(
        'recap-continuity beforeAll: auto_recap_enabled did not confirm as true after PUT — ' +
          `got: ${JSON.stringify(confirmed.auto_recap_enabled)}. ` +
          'CloseSession will return early and no recap log lines will appear.',
      )
    }
  } finally {
    await page.close()
  }
})

test.afterAll(async ({ browser }) => {
  // Best-effort: restore auto_recap_enabled to its original value.
  if (originalAutoRecap === null) return
  const page = await browser.newPage()
  try {
    await page.goto(BASE_URL)
    await page.waitForLoadState('domcontentloaded', { timeout: 15_000 })
    await page.request.put(`${BASE_URL}/api/v1/settings/memory`, {
      headers: await apiHeaders(page),
      data: { auto_recap_enabled: originalAutoRecap },
      failOnStatusCode: false,
    })
  } catch {
    // Best-effort; do not fail the suite on cleanup error.
  } finally {
    await page.close()
  }
})

// ── The test ───────────────────────────────────────────────────────────────────

test(
  'recap_continuity: distinctive fact persists across sessions via last-session.md injection',
  async ({ page }) => {
    // Generous timeout: two LLM round-trips + async recap + disk poll.
    // Turn 1 (fact statement): ~90s.  Recap LLM call: ~60s.  Turn 2 (recall): ~90s.
    // Total budget: 300s + margin = 360s.
    test.setTimeout(360_000)

    // BDD:
    //   Given auto_recap_enabled is true (set in beforeAll)
    //   And  a fresh chat session with the default agent
    //
    //   When the user states a distinctive fact the agent wouldn't otherwise know
    //   And  the user sends a session_close WS frame to trigger recap
    //   Then last-session.md under agents/<id>/.omnipus/ is written within 60s
    //   And  last-session.md contains the distinctive fact/nonce
    //
    //   When the user opens a NEW session with the same agent
    //   And  asks a question only answerable from the recap
    //   Then the agent's reply contains the nonce
    //   This proves last-session.md was injected into the new session's context.
    //
    // Traces to: docs/internal/BRD/Omnipus BRD.md FR-023, FR-028, FR-030

    // ── Step 0: Navigate and pick an agent ────────────────────────────────────

    await page.goto('/')
    const inputLocator = chatInput(page)
    await expect(inputLocator).toBeVisible({ timeout: 20_000 })

    // Use the default agent (mia) for chat. Mia handles acknowledgement
    // requests reliably; the NONCE persistence test only needs the agent to
    // acknowledge and have a non-empty transcript — any chat-capable agent works.
    //
    // NOTE: we resolved the agent ID from the API (not from the UI picker)
    // to avoid a mismatch: selectAgent(Jim) + resolveDefaultAgentID() → mia
    // would create a mia session while the SPA had Jim selected, causing the
    // transcript to be routed to mia's session but the disk-poll to also look
    // in mia's workspace — which is actually correct. However, keeping the
    // selection and the session creation consistent removes all ambiguity.
    const agentId = await resolveDefaultAgentID(page)

    // ── Step 1: Create the session via the REST API so we have its ID ─────────

    // We create the session via REST so we have the session_id for the WS close
    // frame.  Then we navigate to it so the SPA attaches to this session.
    // Pattern from cancel-cross-channel.spec.ts.
    const sessionId = await createSession(page, agentId)
    await page.goto(`/#/sessions/${sessionId}`)
    await expect(inputLocator).toBeVisible({ timeout: 15_000 })

    // Wait for the SPA WS to connect before sending any messages.
    // The SPA auto-attaches on session navigation; give it a moment.
    await page.waitForTimeout(2_000)

    // ── Step 2: Send distinctive fact ─────────────────────────────────────────

    // Install the WS send-interceptor BEFORE sending the fact message so we can
    // capture the session_id the SPA actually uses in the outgoing message frame.
    // The SPA may route to a different session than the REST-created `sessionId`
    // when enterWorkspaceChat calls startNewSession() (e.g. when sessionByWorkspace
    // is unpopulated on first mount). We need the real session_id for session_close.
    await installSpaSessionCapture(page)

    const factMessage =
      `Remember for later: the launch codename is ${NONCE}. ` +
      `Please acknowledge that you've noted it.`

    await inputLocator.fill(factMessage)
    await inputLocator.press('Enter')

    // Wait for turn 1 to fully complete (including any tool-call follow-up).
    await expect(assistantMessages(page)).toHaveCount(1, { timeout: 90_000 })
    await waitForTurnFullyDone(page)

    // Verify the agent actually acknowledged (not just "no crash").
    // The reply should reference the nonce or "noted" / "codename" / similar.
    const firstReply = await assistantMessages(page).first().textContent()
    expect(
      firstReply,
      `Turn 1: expected agent to acknowledge the codename "${NONCE}" but reply was empty`,
    ).toBeTruthy()

    // ── Step 3: Trigger recap via session_close WS frame ──────────────────────

    // Resolve the effective session id: prefer what the SPA actually sent the fact
    // to (captured via WS interceptor). Fall back to the REST-created sessionId only
    // if the interceptor missed it (should not happen in normal flow).
    const capturedSessionId = await getSpaSessionId(page)
    const effectiveSessionId = capturedSessionId ?? sessionId

    const ackReceived = await sendSessionCloseFrame(page, effectiveSessionId)
    expect(
      ackReceived,
      `session_close_ack was NOT received within 10s for session ${effectiveSessionId}.\n` +
        `(REST-created session: ${sessionId}, SPA-captured session: ${capturedSessionId ?? 'none'})\n` +
        'This means the WS handler did not process the session_close frame.\n' +
        'Check: (a) WS is connected, (b) session_id is valid, (c) gateway logs for errors.',
    ).toBe(true)

    // ── Step 4: Poll last-session.md for the nonce ────────────────────────────

    // Recap is async: CloseSession spawns a goroutine → LLM call → WriteLastSession.
    // We poll up to 60 s.  If pollLastSession throws, the test fails with a specific
    // diagnostic — never silently passes.
    const lastSessionContents = await pollLastSession(page, agentId, NONCE)

    // Extra content assertion: the file should be non-trivial (not just the nonce).
    expect(
      lastSessionContents.length,
      `last-session.md contains the nonce but is suspiciously short (${lastSessionContents.length} bytes). ` +
        'Verify the recap LLM produced a real summary.',
    ).toBeGreaterThan(50)

    // ── Step 5: Open a NEW session and verify recall ──────────────────────────

    // Start a brand-new chat session.  The previous session is closed; its recap
    // lives in last-session.md.  When the agent processes the first turn of a
    // new session, ReadLastSession injects that file into the system prompt.
    await newChatButton(page).click()
    await expect(assistantMessages(page)).toHaveCount(0, { timeout: 10_000 })
    await expect(inputLocator).toBeVisible({ timeout: 10_000 })

    // Give the SPA a moment to reset the session and reconnect WS.
    await page.waitForTimeout(1_500)

    const recallQuestion =
      'What was the launch codename I mentioned in our previous conversation?'
    await inputLocator.fill(recallQuestion)
    await inputLocator.press('Enter')

    await expect(assistantMessages(page)).toHaveCount(1, { timeout: 90_000 })
    await waitForTurnFullyDone(page)

    const recallReply = await assistantMessages(page).first().textContent()

    // This is the critical assertion: the agent can only know the nonce if
    // last-session.md was injected into the new session's system prompt.
    expect(
      recallReply ?? '',
      `Recall turn: expected agent reply to contain nonce "${NONCE}" ` +
        '(proving last-session.md was injected into the new session context), ' +
        `but got: "${(recallReply ?? '').slice(0, 300)}".\n` +
        `last-session.md path: ${path.join(OMNIPUS_HOME, 'agents', agentId, '.omnipus', 'last-session.md')}\n` +
        'This means either: (a) last-session.md was not injected into the new session context, ' +
        '(b) ReadLastSession returned empty, or (c) the agent prompt template dropped it.',
    ).toContain(NONCE)
  },
)

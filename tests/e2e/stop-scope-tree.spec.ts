/**
 * stop-scope-tree.spec.ts — ADR-20260928 D9 + MAJ-002: web Stop/Esc//cancel
 * surface scoping, asserted on the REAL WebSocket wire in a real browser.
 *
 * STATUS (2026-10-03, fixture repair after first CI run): the first CI run
 * (run 37109574321, ui-render shard) failed all 4 tests × 4 attempts at
 * startStreamingTurn's message-visibility wait — "Could not load messages."
 * error panel instead of a thread. ROOT CAUSE (fixture, not product): the
 * mock's `session_started` announced a FABRICATED session id the real
 * gateway has never minted. The SPA correctly adopts the acked id
 * (frames.ts session_started case → setActiveSession + isStreaming=true,
 * which is why the stop-btn assertion passed) and then enables its REST
 * history query fetchSessionMessages(activeSessionId) (ChatScreen.tsx,
 * enabled: !!activeSessionId && activeSessionId !== '__pending') — against
 * the REAL gateway that 404s for the phantom id, historyError flips true,
 * and ChatScreen renders the "Could not load messages." + Retry panel IN
 * PLACE OF the thread, so no [data-message-id] can ever mount. FIX: mint a
 * REAL session over REST first (POST /api/v1/sessions — the exact proven
 * pattern of fixtures/session-setup.ts::createSession, agent 'mia') and let
 * the mock announce THAT server-real id, so the history fetch succeeds with
 * an empty transcript and the thread renders. No product code was touched.
 * Second fixture repair (same day, pre-CI, dispatcher review): the mint
 * validates the response against the GENERATED Session schema (no `as {id}`
 * cast) and rides the browser context's HttpOnly `omnipus-session` cookie +
 * a fresh X-CSRF-Token echo — the storageState bearer reader is gone.
 *
 * The RED-author header below claimed the fully-mocked chat WS was an
 * "established pattern (whatsapp-qr.spec.ts, reconnect-mid-turn.spec.ts)".
 * That was wrong: reconnect-mid-turn drives the REAL gateway against a LIVE
 * model, and whatsapp-qr mocks the WS only for the /connectors pairing
 * surface — neither ever mocked a fresh-chat streaming turn. Corrected.
 * The vitest packs carry the frontend RED evidence
 * (ChatScreen.stop-scope.test.tsx, Sidebar.lifecycle-labels.test.tsx);
 * this spec has never passed in CI yet — the repaired instrument was
 * written 2026-10-03 and CI has not re-run it (no local gateway build
 * permitted; main promotes the branch to draft CI).
 *
 * Approach — fully deterministic, no live model:
 *   - A REAL, empty session is minted over REST before each test (server
 *     owns the id; the WS mock then announces it, mirroring the real
 *     gateway handshake where the id in session_started always exists
 *     server-side).
 *   - page.routeWebSocket() fully mocks the chat WS transport.
 *   - The mock answers the client's first `message` frame with
 *     `session_started` + a `token` stream and never sends `done`, so the
 *     turn is mid-stream for the whole test (stop button visible).
 *   - Mid-stream instrument (startStreamingTurn): the Stop button AND the
 *     exact sent user text rendered in the user bubble AND the exact mock
 *     token stream rendered in the assistant row — proof that a user
 *     message was sent and the assistant stream is actually visible, not
 *     merely that a streaming flag flipped.
 *   - Every client→server frame is captured in Node; the assertions read the
 *     captured CANCEL FRAMES — the process edge, not DOM internals. Scope
 *     values come from the generated CancelFrame schema (scope enum
 *     "session"|"tree", optional).
 *
 * The contract under test (D9 surface table):
 *   - single Stop press            → cancel frame WITHOUT scope "tree"
 *   - first press visibly offers the Stop-all confirmation
 *   - second Esc within 3 s        → cancel frame with scope "tree"
 *   - offer gone after the window expires
 *   - /cancel command              → cancel frame with scope "tree"
 *
 * Traces to: ADR-20260928 D9, MAJ-002, T21/T26; common.md decided behaviour.
 *
 * CLAUDE.md — "E2E tests always target the embedded SPA (Go binary)"
 */

import type { WebSocketRoute } from '@playwright/test'
import { expect, test } from '@playwright/test'
// Generated wire schema (contract-first, hard-constraint #8): the POST
// /api/v1/sessions response is validated against it, never a hand-written
// cast. Value-import precedent in e2e specs: mail-panel.spec.ts.
import { Session as SessionSchema } from '../../src/lib/api/generated/schemas'
import { chatInput, userMessages, waitForConnected } from './fixtures/selectors'

interface CapturedFrame {
  type?: string
  session_id?: string
  scope?: 'session' | 'tree'
}

const BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6060'

/** The exact text startStreamingTurn sends — the instrument's user-bubble oracle. */
const USER_TURN_TEXT = 'Trigger a turn for the stop-scope test'

/** The mock's token stream — the instrument's assistant-row oracle. The
 * assistant bubble must show EXACTLY the concatenation of these chunks
 * (derived from this spec's own mock definition, never from observed DOM). */
const TURN_CHUNKS = ['Stop', '-scope ', 'e2e ', 'turn ', 'running.']
const ASSISTANT_STREAM_TEXT = TURN_CHUNKS.join('')

/**
 * Mint a REAL, empty chat session over REST so the WS mock can announce a
 * session id that actually exists server-side. Provenance: the exact POST
 * pattern of fixtures/session-setup.ts::createSession (agent 'mia' — the
 * seeded default of the roster; the historical 'main' id no longer resolves
 * and would lock the composer read-only).
 *
 * Auth: the browser context's real `omnipus-session` HttpOnly cookie rides
 * along automatically — page.request shares the BrowserContext cookie jar
 * (Playwright APIRequestContext contract), the same credentials:'include'
 * channel src/lib/api.ts documents. No bearer token is read or sent: the SPA
 * keeps no JS-visible token (ADR-044), and cloning session-setup's
 * storageState file reader would silently degrade to unauthenticated on a
 * malformed file. CSRF mirrors src/lib/api.ts::readCSRFCookie: X-CSRF-Token
 * echoes the __Host-csrf (plain-HTTP `csrf`) cookie, read FRESH from the
 * context jar on every call.
 *
 * The response is validated against the GENERATED Session schema
 * (src/lib/api/generated/schemas.ts::Session — the contract type OpenAPI
 * gives POST /api/v1/sessions), never a hand-written cast. Failure errors
 * carry the HTTP status + a fixed sentence only — the raw body is never
 * echoed into a thrown error, it can carry server-side data.
 */
async function mintRealSession(page: import('@playwright/test').Page): Promise<string> {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' }
  const csrf = (await page.context().cookies()).find(
    (c) => c.name === '__Host-csrf' || c.name === 'csrf',
  )
  if (csrf) headers['X-CSRF-Token'] = csrf.value
  const resp = await page.request.post(`${BASE_URL}/api/v1/sessions`, {
    headers,
    data: { agent_id: 'mia', type: 'chat' },
  })
  if (!resp.ok()) {
    throw new Error(`POST /api/v1/sessions failed with HTTP ${resp.status()}`)
  }
  let body: unknown
  try {
    body = await resp.json()
  } catch {
    throw new Error(`POST /api/v1/sessions returned HTTP ${resp.status()} with a non-JSON body`)
  }
  const parsed = SessionSchema.safeParse(body)
  if (!parsed.success) {
    throw new Error(
      `POST /api/v1/sessions returned HTTP ${resp.status()} but its body failed the generated Session schema (src/lib/api/generated/schemas.ts::Session)`,
    )
  }
  // The generated schema types id as a plain string — an empty id passes it,
  // and a phantom id is exactly what broke the first CI run.
  if (!parsed.data.id) {
    throw new Error('POST /api/v1/sessions returned an empty session id')
  }
  return parsed.data.id
}

/**
 * Any assistant row, running or finished. Ground truth: fixtures/selectors.ts
 * documents data-message-id on all message roots and flex-row-reverse on user
 * rows; assistantMessages() additionally excludes data-status="running", which
 * a permanently mid-stream turn always is — so this spec needs the unfiltered
 * complement (same locator reconnect-mid-turn.spec.ts calls anyAssistantRow).
 */
const anyStreamingAssistantRow = (page: import('@playwright/test').Page) =>
  page.locator('[data-message-id]:not(.flex-row-reverse)')

/**
 * Installs the WS mock and starts collecting client→server frames.
 *
 * `sessionId` is a REAL gateway-minted session (mintRealSession) — the ack
 * below must announce a session the server actually has, because the SPA
 * enables its REST history fetch for the adopted id (ChatScreen.tsx) and a
 * phantom id 404s into the "Could not load messages." error panel.
 *
 * Server-side script: on the first `message` frame, ack with session_started
 * and stream a few tokens; never send done. auth/ping are ignored (the SPA
 * does not need acks to function — whatsapp-qr.spec.ts precedent).
 */
async function mockChatWebSocket(
  page: import('@playwright/test').Page,
  sentToServer: CapturedFrame[],
  sessionId: string,
): Promise<void> {
  await page.routeWebSocket(/api\/v1\/chat\/ws/, (ws: WebSocketRoute) => {
    let turnStarted = false
    ws.onMessage((raw: string | Buffer) => {
      let frame: CapturedFrame & { content?: string }
      try {
        frame = JSON.parse(typeof raw === 'string' ? raw : raw.toString())
      } catch {
        return
      }
      sentToServer.push(frame)

      if (frame.type === 'message' && !turnStarted) {
        turnStarted = true
        ws.send(JSON.stringify({ type: 'session_started', session_id: sessionId }))
        for (const chunk of TURN_CHUNKS) {
          ws.send(JSON.stringify({ type: 'token', session_id: sessionId, content: chunk }))
        }
        // Deliberately NO done frame — the turn stays mid-stream.
      }
    })
  })
}

function cancelFrames(sentToServer: CapturedFrame[]): CapturedFrame[] {
  return sentToServer.filter((f) => f.type === 'cancel')
}

async function startStreamingTurn(page: import('@playwright/test').Page): Promise<void> {
  const input = chatInput(page)
  await expect(input).toBeVisible({ timeout: 15_000 })
  await waitForConnected(page)
  await input.fill(USER_TURN_TEXT)
  await input.press('Enter')
  // Mid-stream confirmation: the Stop button replaced Send.
  await expect(page.getByTestId('stop-btn')).toBeVisible({ timeout: 15_000 })
  // The streaming turn must be RENDERED, not merely flagged: the user bubble
  // shows the exact text that was sent, and the assistant row shows the mock's
  // exact token stream (both oracles derive from this spec's own constants).
  // This replaced the RED author's bare [data-message-id] visibility wait,
  // which passed-for-failed through the "Could not load messages." teardown.
  await expect(userMessages(page).first()).toContainText(USER_TURN_TEXT, { timeout: 15_000 })
  await expect(anyStreamingAssistantRow(page).first()).toContainText(ASSISTANT_STREAM_TEXT, {
    timeout: 15_000,
  })
}

test.describe('Stop/Esc//cancel surface scoping (ADR-20260928 D9)', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/')
  })

  test('single Stop press sends one cancel frame without tree scope and shows the Stop-all offer', async ({ page }) => {
    test.setTimeout(90_000)
    const sentToServer: CapturedFrame[] = []
    const sessionId = await mintRealSession(page)
    await mockChatWebSocket(page, sentToServer, sessionId)
    await page.goto('/')
    await startStreamingTurn(page)

    await page.getByTestId('stop-btn').click()

    // D9: the first press VISIBLY offers the Stop-all confirmation.
    await expect(page.getByText(/stop all/i)).toBeVisible({ timeout: 3_000 })

    // And the wire saw exactly one cancel frame, session-scoped (no tree).
    const cancels = cancelFrames(sentToServer)
    expect(cancels).toHaveLength(1)
    expect(cancels[0].session_id).toBeTruthy()
    expect(cancels[0].scope).not.toBe('tree')
  })

  test('second Esc within 3s confirms stop-all — cancel frame carries scope tree', async ({ page }) => {
    test.setTimeout(90_000)
    const sentToServer: CapturedFrame[] = []
    const sessionId = await mintRealSession(page)
    await mockChatWebSocket(page, sentToServer, sessionId)
    await page.goto('/')
    await startStreamingTurn(page)

    const input = chatInput(page)
    await input.press('Escape')
    // Second Esc inside the confirmation window (well under 3 s).
    await input.press('Escape')

    await expect
      .poll(() => cancelFrames(sentToServer).some((f) => f.scope === 'tree'), { timeout: 5_000 })
      .toBe(true)
  })

  test('Stop-all offer disappears once the confirmation window expires', async ({ page }) => {
    test.setTimeout(90_000)
    const sentToServer: CapturedFrame[] = []
    const sessionId = await mintRealSession(page)
    await mockChatWebSocket(page, sentToServer, sessionId)
    await page.goto('/')
    await startStreamingTurn(page)

    await page.getByTestId('stop-btn').click()
    await expect(page.getByText(/stop all/i)).toBeVisible({ timeout: 3_000 })

    // Past the 3-second window the offer is gone; a fresh activation would be
    // a new FIRST press (session-scoped), never a silent stop-all.
    await expect(page.getByText(/stop all/i)).toBeHidden({ timeout: 6_000 })
    expect(cancelFrames(sentToServer).some((f) => f.scope === 'tree')).toBe(false)
  })

  test('/cancel sends one cancel frame with scope tree', async ({ page }) => {
    test.setTimeout(90_000)
    const sentToServer: CapturedFrame[] = []
    const sessionId = await mintRealSession(page)
    await mockChatWebSocket(page, sentToServer, sessionId)
    await page.goto('/')
    await startStreamingTurn(page)

    // /cancel is available while streaming; select it from the slash menu.
    const input = chatInput(page)
    await input.fill('/')
    await page.getByText('/cancel').click()

    await expect
      .poll(() => cancelFrames(sentToServer), { timeout: 5_000 })
      .toHaveLength(1)
    expect(cancelFrames(sentToServer)[0].scope).toBe('tree')
    expect(cancelFrames(sentToServer)[0].session_id).toBe(sessionId)
  })
})

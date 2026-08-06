/**
 * session-setup.ts — Shared helper for replay-fidelity E2E setup.
 *
 * Factors out the repeated 7-step setup block used by replay-fidelity.spec.ts
 * tests (a), (b), (d), and (e):
 *   goto('/') → waitForWsConnected → createSession → renameSession →
 *   seedTranscript → openSessionByDeepLink (folds in the old separate
 *   waitForReplayDone step — see that helper's doc comment)
 *
 * Traces to: temporal-puzzling-melody.md W5-16.
 */

import * as fs from 'fs'
import * as path from 'path'
import { expect, type Page } from '@playwright/test'
import { chatInput, waitForConnected } from './selectors'

const BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6060'

const OMNIPUS_HOME =
  process.env.OMNIPUS_HOME ||
  (process.env.HOME ? path.join(process.env.HOME, '.omnipus') : '/tmp/omnipus-e2e-test')

// ── Transcript entry types (mirrors replay-fidelity.spec.ts) ──────────────────

export interface TranscriptToolCall {
  id: string
  tool: string
  status: string
  duration_ms?: number
  parameters?: Record<string, unknown>
  result?: Record<string, unknown>
  parent_tool_call_id?: string
}

export interface TranscriptEntry {
  id: string
  type?: string
  role: string
  content?: string
  summary?: string
  timestamp: string
  agent_id?: string
  tool_calls?: TranscriptToolCall[]
}

// ── Internal helpers ──────────────────────────────────────────────────────────

function getStoredAuthToken(): string | null {
  const authFile = process.env.OMNIPUS_AUTH_FILE
    ? path.resolve(process.env.OMNIPUS_AUTH_FILE)
    : path.join(
        path.dirname(new URL(import.meta.url).pathname),
        '.auth/admin.json',
      )
  if (!fs.existsSync(authFile)) return null
  try {
    const raw = fs.readFileSync(authFile, 'utf-8')
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
    // Auth file may not exist on first run
  }
  return null
}

async function getCsrfToken(page: Page): Promise<string | null> {
  const cookies = await page.context().cookies()
  const csrfCookie = cookies.find((c) => c.name === '__Host-csrf' || c.name === 'csrf')
  return csrfCookie?.value ?? null
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

async function createSession(page: Page): Promise<string> {
  const resp = await page.request.post(`${BASE_URL}/api/v1/sessions`, {
    headers: await apiHeaders(page),
    // 'mia' — the seeded default of the 4-base roster. The historical
    // 'main' id no longer resolves to any agent, which makes the session
    // detail report agent_removed and locks the composer read-only.
    data: { agent_id: 'mia', type: 'chat' },
  })
  if (!resp.ok()) {
    const body = await resp.text()
    throw new Error(`POST /api/v1/sessions failed: ${resp.status()} — ${body}`)
  }
  const meta = (await resp.json()) as { id: string }
  if (!meta.id) throw new Error('POST /api/v1/sessions returned no id')
  return meta.id
}

async function renameSession(page: Page, sessionId: string, title: string): Promise<void> {
  const resp = await page.request.put(`${BASE_URL}/api/v1/sessions/${sessionId}`, {
    headers: await apiHeaders(page),
    data: { title },
  })
  if (!resp.ok()) {
    const body = await resp.text()
    throw new Error(`PUT /api/v1/sessions/${sessionId} failed: ${resp.status()} — ${body}`)
  }
}

function seedTranscript(sessionId: string, entries: TranscriptEntry[]): void {
  const sessionDir = path.join(OMNIPUS_HOME, 'sessions', sessionId)
  if (!fs.existsSync(sessionDir)) {
    throw new Error(
      `Session directory does not exist: ${sessionDir}. ` +
        'Create the session via REST API before seeding the transcript.',
    )
  }
  const transcriptPath = path.join(sessionDir, 'transcript.jsonl')
  const lines = entries.map((e) => JSON.stringify(e)).join('\n') + '\n'
  fs.writeFileSync(transcriptPath, lines, { encoding: 'utf-8' })
}

async function waitForWsConnected(page: Page): Promise<void> {
  await expect(chatInput(page)).toBeEnabled({ timeout: 15_000 })
  // toBeEnabled() alone no longer implies "connected" (2fa26e6a, #105 fix —
  // see waitForConnected's doc comment in ./selectors). Confirm the socket
  // is genuinely open, not merely queueing, before callers create a session
  // and send messages against it.
  await waitForConnected(page, { timeout: 15_000 })
}

// ── Public API ────────────────────────────────────────────────────────────────

/**
 * Open (or reopen) a seeded session via the app's own current deep-link
 * route, `/#/sessions/{id}`.
 *
 * Replaces the old click-through flow (click a button named "Open sessions
 * panel", then one named "Open session: <title>") — neither accessible name
 * exists anywhere in current `src/` since the session panel was redesigned
 * into the two-mode SearchModal (`src/components/search/SearchModal.tsx`):
 * the sidebar's session-search trigger is now
 * `aria-label="Search sessions"` (`src/components/layout/Sidebar.tsx:316`),
 * and session rows render `{session.title || 'Untitled session'}` inside
 * plain buttons with no "Open session: <title>" naming convention.
 *
 * The deep link is not a workaround standing in for that broken flow — it is
 * the app's own real navigation seam: `src/routes/_app/sessions.$sessionId.tsx`'s
 * loader no longer pre-sets `activeSessionId`, so navigating straight to this
 * route makes `SessionRoute`'s attach effect see a genuine mismatch and send
 * `attach_session` over the WebSocket, exactly as clicking the (now-gone)
 * session-list button used to — a real WS attach + replay, not a REST-only
 * render.
 *
 * Waits for the chat composer to mount (route loaded) AND re-enable (the
 * replay `done` frame landed) — this subsumes the old separate
 * `waitForReplayDone` wait, so callers should NOT add another enabled-wait
 * immediately after this.
 */
export async function openSessionByDeepLink(page: Page, sessionId: string): Promise<void> {
  await page.goto(`/#/sessions/${sessionId}`)
  // Route-swap guard FIRST: wait until the chat surface is bound to THIS
  // session (ChatScreen stamps data-active-session-id once attach lands).
  // A bare visible/enabled wait on the composer is satisfied by the
  // PREVIOUS route's composer during the swap — typing at that instant
  // sends with a stale/null session binding and the message lands in a
  // brand-new workspace session instead of sessionId (root cause of the
  // replay-fidelity (c) mid-turn attach flake).
  await expect(page.locator(`[data-active-session-id="${sessionId}"]`)).toBeVisible({
    timeout: 15_000,
  })
  await expect(chatInput(page)).toBeVisible({ timeout: 15_000 })
  // "Replay finished" signal — the composer re-enables once the replay's
  // done frame lands. No arbitrary timeout.
  await expect(chatInput(page)).toBeEnabled({ timeout: 30_000 })
  // toBeEnabled() alone conflates "replay done" with "connected" — it no
  // longer implies the latter (2fa26e6a, #105 fix; see waitForConnected's
  // doc comment in ./selectors). A caller that fills+sends immediately
  // after this returns needs BOTH true, or the message lands in the
  // outbound queue instead of the wire.
  await waitForConnected(page, { timeout: 30_000 })
}

export interface SeedAndOpenResult {
  sessionId: string
  sessionTitle: string
}

/**
 * seedAndOpenSession — one-call replacement for the repeated 7-step setup block
 * in replay-fidelity.spec.ts tests (a), (b), (d), and (e).
 *
 * Steps:
 *   1. Navigate to '/'
 *   2. Wait for the app banner to be visible
 *   3. Wait for WebSocket connection
 *   4. Create a session via REST API
 *   5. Rename it to `${namePrefix}-${Date.now()}`
 *   6. Seed the transcript.jsonl with `entries`
 *   7. Open the session via its deep-link route, `/#/sessions/{id}`
 *      (openSessionByDeepLink) — NOT the sessions panel. The panel affordance
 *      this step used to click ("Open sessions panel" / "Open session:
 *      <title>") was redesigned away (see openSessionByDeepLink's doc
 *      comment above); the deep link is the current deterministic seam and
 *      drives the same real WS attach + replay the old button click did.
 *   8. Wait for replay to complete (done frame) — folded into step 7 via
 *      openSessionByDeepLink, which itself waits for the composer to
 *      re-enable, so there is no separate step 8 call here.
 *
 * Returns the session ID and title so callers can reference them in assertions.
 */
export async function seedAndOpenSession(
  page: Page,
  namePrefix: string,
  entries: TranscriptEntry[],
): Promise<SeedAndOpenResult> {
  await page.goto('/')
  await expect(page.getByRole('banner')).toBeVisible({ timeout: 15_000 })
  await waitForWsConnected(page)

  const sessionId = await createSession(page)
  const sessionTitle = `${namePrefix}-${Date.now()}`
  await renameSession(page, sessionId, sessionTitle)

  seedTranscript(sessionId, entries)

  await openSessionByDeepLink(page, sessionId)

  return { sessionId, sessionTitle }
}

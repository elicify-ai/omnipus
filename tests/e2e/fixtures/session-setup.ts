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
import { expect, type Locator, type Page } from '@playwright/test'
import { canonicalHistoryContext } from './canonical-history-context'
import { seedCanonicalLegacyTranscript } from './chat-history'
import {
  agentRowGroup,
  chatInput,
  DEFAULT_WORKSPACE_NAME,
  openSidebar,
  selectedAgentRow,
  waitForConnected,
  closeSidebar,
} from './selectors'

const BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6060'

function sessionSetupBaseURL() { return canonicalHistoryContext()?.gateway.baseURL ?? BASE_URL }

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
  /**
   * ADR-087 D2 — set on the last assistant entry of an incomplete turn.
   * Optional, additive fields (mirror the wire `Message`/`ReplayMessageFrame`
   * schema) so existing seeded fixtures are unaffected.
   */
  truncated?: boolean
  truncation_reason?: 'cancelled' | 'max_output_tokens'
}

// ── Internal helpers ──────────────────────────────────────────────────────────

function getStoredAuthToken(): string | null {
  const authFile = canonicalHistoryContext()?.authFile ?? (process.env.OMNIPUS_AUTH_FILE
    ? path.resolve(process.env.OMNIPUS_AUTH_FILE)
    : path.join(
        path.dirname(new URL(import.meta.url).pathname),
        '.auth/admin.json',
      ))
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
  const resp = await page.request.post(`${sessionSetupBaseURL()}/api/v1/sessions`, {
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
  const resp = await page.request.put(`${sessionSetupBaseURL()}/api/v1/sessions/${sessionId}`, {
    headers: await apiHeaders(page),
    data: { title },
  })
  if (!resp.ok()) {
    const body = await resp.text()
    throw new Error(`PUT /api/v1/sessions/${sessionId} failed: ${resp.status()} — ${body}`)
  }
}

async function seedTranscript(sessionId: string, entries: TranscriptEntry[]): Promise<void> {
  await seedCanonicalLegacyTranscript(sessionId, entries)
}

export async function waitForWsConnected(page: Page): Promise<void> {
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
export async function openSessionByDeepLink(
  page: Page,
  sessionId: string,
  origin?: string,
): Promise<void> {
  // `origin` must be passed when the page is NOT on the shared server:
  // Playwright resolves a relative goto against the configured baseURL
  // (playwright.config.ts — the SHARED origin), never against the page's
  // current origin, so the relative form would yank an isolated-GatewayProcess
  // page to the wrong server. Omitted = the historical relative form, for the
  // shared-gateway callers that predate freshChat.
  await page.goto(origin !== undefined ? `${origin}/#/sessions/${sessionId}` : `/#/sessions/${sessionId}`)
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

// ── freshChat — the fresh-chat isolation seam (founder decision D7) ───────────

/**
 * The app origin the PAGE is currently on — the base for every REST call and
 * the deep link freshChat makes. Deriving it from the page (not a module
 * constant) is what makes freshChat work unchanged on isolated
 * GatewayProcess gateways (provider-retry, catchup-scenarios, gateway-restart
 * scenarios run on their OWN origin, not BASE_URL). Throws when the page is
 * not on an app origin (about:blank) — the caller must goto() first, exactly
 * like every current call site does; failing loudly beats guessing a server.
 */
export function originOf(page: Page): string {
  const url = page.url()
  try {
    const parsed = new URL(url)
    if (parsed.protocol === 'http:' || parsed.protocol === 'https:') return parsed.origin
  } catch {
    // falls through to the throw below
  }
  // The message names originOf, not freshChat: originOf is called from
  // several places (freshChat's own body, but also a spec's beforeEach reading
  // the gateway's origin directly), and a message that named only freshChat
  // sent a PR-#1260 triage to the wrong line. Say what actually failed.
  throw new Error(
    `originOf: the page is not on an app origin (url: ${url || '(empty)'}). ` +
      'goto() the app first, or pass an explicit origin — refusing to guess a server.'
  )
}

/**
 * Cookie + CSRF auth headers ONLY — the same shape agents.spec.ts::csrfHeaders.
 * freshChat deliberately does NOT reuse this file's apiHeaders(): that helper
 * attaches the SHARED server's bearer token from .auth/admin.json, and an
 * isolated GatewayProcess gateway must never receive it (the page there
 * authenticates through its OWN UI login — provider-retry.spec.ts::login —
 * so its cookie jar already carries the right omnipus-session cookie for
 * whichever gateway the page is on).
 */
async function pageCsrfHeaders(page: Page): Promise<Record<string, string>> {
  const cookies = await page.context().cookies()
  const csrfCookie = cookies.find((c) => c.name === '__Host-csrf' || c.name === 'csrf')
  return csrfCookie ? { 'X-CSRF-Token': csrfCookie.value } : {}
}

export interface FreshChat {
  sessionId: string
  agentId: string
  agentName: string
}

/**
 * Start a genuinely NEW chat session for an agent — the replacement for the
 * deleted selectors.ts::startNewChat, which clicked the agent row's
 * "New chat with <Agent>" icon button. That button (and the whole new-chat UI
 * access point) was REMOVED by founder decision D7 (DECISIONS.md, squad
 * nav-agent-row-menu-20261011): the capability stays in code, but there is no
 * UI path to a fresh chat anymore. Specs obtain one the way the SPA itself
 * can, through public surfaces only — no test-only hook exists or is added:
 *
 *   1. `POST /api/v1/sessions` — contracts/openapi.yaml `createSession`:
 *      `{agent_id, type: 'chat', workspace_id}` → 201 + `Session{id}`. The
 *      same call the SPA's own "Open browser" launcher makes
 *      (SessionCreateRequest.yaml description) and the same seam this file's
 *      createSession/seedAndOpenSession already drive.
 *   2. openSessionByDeepLink — the SPA's own /sessions/<id> route
 *      (src/routes/_app/sessions.$sessionId.tsx), which adopts the session
 *      into its workspace chat: binds the workspace, sends the real
 *      `attach_session` WS frame, and redirects to
 *      /#/workspaces/<id>/chat. Its data-active-session-id guard is the
 *      binding proof.
 *
 * WHY THIS IS SOUND WHEN AN EARLIER ATTEMPT WAS NOT: cancel-cross-channel
 * T26's 2026-07-28 header records that "createSession() REST + goto('/#/sessions/<id>')
 * does not bind — the SPA lazily mints its own session on first send". That
 * finding predates the route's deep-link adoption; today the route attaches
 * for real, and open-in-chat.spec.ts test (a) proves END-TO-END against a
 * live gateway that a POST-created session deep-linked via /sessions/<id>
 * attaches (attach_session frame) and streams a subsequent send in THAT
 * session. The earlier attempt also lacked any binding check before sending;
 * openSessionByDeepLink's data-active-session-id wait is exactly that check,
 * and the row assertion below adds the agent half (the selected row must be
 * THIS agent's after the redirect). If the bind ever regresses, freshChat
 * fails loudly here instead of letting the caller send into the wrong
 * session.
 *
 * Compared with the removed button's flow the OUTCOME is the same shape (a
 * new, empty, selected chat for the agent) with one honest difference: the
 * session id is KNOWN here (the REST response carries it), where the button's
 * lazy flow minted it on first send. Specs that identify a turn's session by
 * diffing OMNIPUS_HOME/sessions can now bind audit/transcript rows to the
 * exact id instead of inferring it from a directory diff.
 *
 * Which agent: `name`/`workspace` pick one explicitly (same contract as the
 * helper this replaces); with no name, the CURRENTLY SELECTED row's agent is
 * used when one exists (the removed button kept the active agent — this
 * preserves that contract), falling back to the first row of the workspace
 * roster (deterministic core_team order).
 *
 * REQUIRES an online page: the REST call and the deep-link's WS-driven
 * attach both need the connection. Every current caller satisfies this
 * (they run after goto('/') + readiness waits, before any setOffline()).
 *
 * Returns the created session's id, the agent's id, and the agent's concrete
 * display name. A freshly created session has no transcript, so callers'
 * existing `assistantMessages → toHaveCount(0)` assertions hold immediately.
 */
export async function freshChat(
  page: Page,
  name?: string | RegExp,
  workspace: string = DEFAULT_WORKSPACE_NAME,
): Promise<FreshChat> {
  // Resolve the CONCRETE agent display name from the row — the same first
  // step the removed helper performed (its click label was derived from the
  // row's aria-label). The row, not the caller's pattern, is the source of
  // the concrete name, so an explicit /Jim/i and a no-arg call behave
  // identically from here on.
  let row: Locator
  if (name !== undefined) {
    row = await agentRowGroup(page, name, workspace)
  } else {
    await openSidebar(page)
    row = selectedAgentRow(page)
    if ((await row.count()) === 0) {
      row = page
        .getByRole('group', { name: workspace, exact: true })
        .getByRole('group')
        .first()
    }
  }
  const agentName = await row.getAttribute('aria-label', { timeout: 15_000 })
  if (!agentName) {
    throw new Error(
      'freshChat: could not resolve an agent row — no row label could be read ' +
        `(name=${String(name)}). Pass an explicit agent name if the sidebar has ` +
        'no selected/first row to read.'
    )
  }

  // Reading the row opened the sidebar. It is an unpinned overlay whose
  // full-page click-catcher swallows every later pointer event on the chat
  // ("<div aria-hidden> intercepts pointer events" on the Stop button), and the
  // hash-only deep link below is a same-document navigation that does not close
  // it - so close it here, where freshChat opened it.
  await closeSidebar(page)

  // Every REST call and the deep link ride the PAGE's origin — freshChat
  // must hit whichever gateway the page is on (shared OR isolated
  // GatewayProcess), and openSessionByDeepLink needs the absolute form
  // because Playwright resolves relative gotos against the configured
  // (shared) baseURL, not the page origin. The page is on the app by here:
  // the row resolution above already drove sidebar UI.
  const origin = originOf(page)

  const workspaceId = await resolveWorkspaceIdByName(page, origin, workspace)
  const agentId = await resolveAgentIdByName(page, origin, agentName)

  // Create the session — contracts/openapi.yaml createSession: 201 + Session.
  // Both failure modes below are honest reds (unknown agent → 400; contract
  // drift in the response shape) and must fail the caller's test, never retry.
  const createResp = await page.request.post(`${origin}/api/v1/sessions`, {
    headers: await pageCsrfHeaders(page),
    data: { agent_id: agentId, type: 'chat', workspace_id: workspaceId },
  })
  if (!createResp.ok() || createResp.status() !== 201) {
    throw new Error(
      `freshChat: POST /api/v1/sessions failed: ${createResp.status()} ` +
        `${await createResp.text()} (oracle: contracts/openapi.yaml createSession — ` +
        '201 on success, 400 when the agent does not exist)'
    )
  }
  const created = (await createResp.json()) as { id?: string }
  if (!created.id) {
    throw new Error(
      `freshChat: POST /api/v1/sessions returned no session id (response: ` +
        `${JSON.stringify(created)}; contract: 201 → Session{id})`
    )
  }

  // Open it through the SPA's own deep-link route — the real attach +
  // replay path, with the data-active-session-id binding guard
  // (see openSessionByDeepLink's doc comment). Absolute origin: see above.
  await openSessionByDeepLink(page, created.id, origin)

  // The adopted session is the SELECTED chat of THIS agent's row — the
  // user-visible half of the binding (data-active-session-id above proves
  // the session; this proves the agent): data-selected="true" is exactly
  // "this workspace is active AND this agent owns the selected chat"
  // (WorkspaceAgentList.tsx). Catches a wrong-agent resolution before any
  // caller sends a message.
  const boundRow = await agentRowGroup(page, agentName, workspace)
  await expect(
    boundRow,
    `freshChat: deep-linked session ${created.id} did not become the selected ` +
      `chat of ${agentName}'s row in "${workspace}" — the composer would send ` +
      'into a different session or as a different agent than this helper created'
  ).toHaveAttribute('data-selected', 'true', { timeout: 15_000 })

  // The bound-row check above opened the sidebar again (agentRowGroup); leave it
  // closed so its click-catcher does not swallow the caller's next click.
  await closeSidebar(page)

  return { sessionId: created.id, agentId, agentName }
}

/** Resolve a workspace's id by its display name via GET /api/v1/workspaces —
 * fails with the full roster when the name matches nothing (contract:
 * Workspace{id,name}, contracts/components/schemas/Workspace.yaml). */
async function resolveWorkspaceIdByName(
  page: Page,
  origin: string,
  workspaceName: string
): Promise<string> {
  const resp = await page.request.get(`${origin}/api/v1/workspaces`, {
    headers: await pageCsrfHeaders(page),
    failOnStatusCode: false,
  })
  if (!resp.ok()) {
    throw new Error(
      `freshChat: GET /api/v1/workspaces failed: ${resp.status()} ${await resp.text()}`
    )
  }
  const workspaces = (await resp.json()) as Array<{ id: string; name?: string }>
  const found = workspaces.find((w) => w.name === workspaceName)
  if (!found?.id) {
    throw new Error(
      `freshChat: no workspace named "${workspaceName}" — names present: ` +
        `${JSON.stringify(workspaces.map((w) => w.name))}. The seeded default is ` +
        `"${DEFAULT_WORKSPACE_NAME}" (ensureDefaultWorkspace, pkg/gateway/rest_workspaces.go:584)`
    )
  }
  return found.id
}

/** Resolve an agent's id by matching its display name via GET /api/v1/agents —
 * RegExp matched with test(), strings compared case-insensitively (the same
 * matching rules getByRole applies to the row's aria-label). Fails with the
 * full roster when nothing matches. */
async function resolveAgentIdByName(
  page: Page,
  origin: string,
  name: string | RegExp
): Promise<string> {
  const resp = await page.request.get(`${origin}/api/v1/agents`, {
    headers: await pageCsrfHeaders(page),
    failOnStatusCode: false,
  })
  if (!resp.ok()) {
    throw new Error(
      `freshChat: GET /api/v1/agents failed: ${resp.status()} ${await resp.text()}`
    )
  }
  const agents = (await resp.json()) as Array<{ id: string; name?: string }>
  const match = agents.find((a) =>
    typeof name === 'string'
      ? a.name?.toLowerCase() === name.toLowerCase()
      : name.test(a.name ?? '')
  )
  if (!match?.id) {
    throw new Error(
      `freshChat: no agent matches ${String(name)} — roster present: ` +
        `${JSON.stringify(agents.map((a) => a.name))}`
    )
  }
  return match.id
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

  await seedTranscript(sessionId, entries)

  await openSessionByDeepLink(page, sessionId)

  return { sessionId, sessionTitle }
}

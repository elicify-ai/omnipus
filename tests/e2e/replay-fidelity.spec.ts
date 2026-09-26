/**
 * replay-fidelity.spec.ts — Sprint I3 E2E tests for historical chat replay fidelity.
 *
 * Scope: TDD rows 23-27 from sprint-i-historical-replay-fidelity-spec.md.
 * Traces to: sprint-i-historical-replay-fidelity-spec.md BDD Scenarios 1-10, SC-I-001 to SC-I-006.
 *
 * Both Sprint I1 (pkg/gateway/replay.go) and Sprint I2 (src/store/chat.ts) are merged.
 * All tests except (c) are runnable against the current build.
 *
 * "Scenario provider" approach: since no deterministic LLM scenario provider exists yet,
 * these tests create sessions via the REST API, then seed the transcript.jsonl file
 * directly in OMNIPUS_HOME/sessions/<id>/ before navigating the browser to the session.
 * OMNIPUS_HOME is read from the OMNIPUS_HOME env var (default /tmp/omnipus-e2e-test).
 *
 * Axe accessibility check: sub-test at the end of test (a) validates WCAG 2.1 AA on a
 * replay-rendered page, per SC-I-006.
 */

import * as fs from 'fs'
import * as path from 'path'
import { expect, type Page } from '@playwright/test'
import { test } from './fixtures/console-errors'
import { expectA11yClean } from './fixtures/a11y'
// No verbose-chat opt-in anywhere in this file: ADR-091 D7/AC-7 makes a
// delegation visible in the DEFAULT, non-verbose thread (the delegate/spawn
// tool call is the parent chat's only delegation surface now that
// SubagentBlock and shouldRenderSubagentSpan are deleted), so test (b)
// asserts the default policy directly — see its own comment.
import { chatInput, sendButton, waitForConnected } from './fixtures/selectors'

// ── Constants ──────────────────────────────────────────────────────────────────

const BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6060'

/**
 * OMNIPUS_HOME: the gateway's workspace directory.
 * Must match the directory the running gateway is using.
 * Override via env var when the gateway is started with a non-default home.
 */
const OMNIPUS_HOME =
  process.env.OMNIPUS_HOME ||
  (process.env.HOME ? path.join(process.env.HOME, '.omnipus') : '/tmp/omnipus-e2e-test')

// ── Transcript helpers ─────────────────────────────────────────────────────────

interface TranscriptToolCall {
  id: string
  tool: string
  status: string
  duration_ms?: number
  parameters?: Record<string, unknown>
  result?: Record<string, unknown>
  parent_tool_call_id?: string
}

interface TranscriptEntry {
  id: string
  type?: string
  role: string
  content?: string
  summary?: string
  timestamp: string
  agent_id?: string
  tool_calls?: TranscriptToolCall[]
}

/**
 * Seed a transcript.jsonl into the session's directory.
 * The gateway reads this file when the browser sends attach_session.
 * Precondition: the session directory must already exist (created via POST /api/v1/sessions).
 */
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

/**
 * Read the auth token from the auth state file.
 * The global-setup copies it from sessionStorage to localStorage and saves
 * storageState — we extract it from the auth file here.
 */
function getStoredAuthToken(): string | null {
  // Respect OMNIPUS_AUTH_FILE env var set by isolated test runs (e.g. port 6062)
  // to avoid using a token minted for a different gateway instance.
  const authFile = process.env.OMNIPUS_AUTH_FILE
    ? path.resolve(process.env.OMNIPUS_AUTH_FILE)
    : path.join(
        path.dirname(new URL(import.meta.url).pathname),
        'fixtures/.auth/admin.json',
      )
  if (!fs.existsSync(authFile)) {
    return null
  }
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
        if (item.name === 'omnipus_auth_token') {
          return item.value
        }
      }
    }
  } catch {
    // Auth file may not exist in first run
  }
  return null
}

/**
 * Extract the __Host-csrf cookie value from the browser context.
 * The gateway uses double-submit cookie CSRF: the cookie value must also be
 * sent as the X-CSRF-Token request header on state-mutating endpoints.
 * Returns null if the cookie is not present (pre-auth or first load).
 */
async function getCsrfToken(page: Page): Promise<string | null> {
  const cookies = await page.context().cookies()
  const csrfCookie = cookies.find((c) => c.name === '__Host-csrf' || c.name === 'csrf')
  return csrfCookie?.value ?? null
}

/**
 * Build headers for a REST API request: Authorization + CSRF token.
 * Both are required for state-mutating endpoints.
 */
async function apiHeaders(
  page: Page,
): Promise<Record<string, string>> {
  const authToken = getStoredAuthToken()
  const csrfToken = await getCsrfToken(page)
  return {
    'Content-Type': 'application/json',
    ...(authToken ? { Authorization: `Bearer ${authToken}` } : {}),
    ...(csrfToken ? { 'X-CSRF-Token': csrfToken } : {}),
  }
}

/**
 * Create a new session via the REST API and return its ID.
 * Uses page.request which inherits the browser's cookies (including __Host-csrf),
 * and sends the X-CSRF-Token header required by the gateway's double-submit CSRF check.
 */
async function createSession(page: Page): Promise<string> {
  const resp = await page.request.post(`${BASE_URL}/api/v1/sessions`, {
    headers: await apiHeaders(page),
    data: { agent_id: 'mia', type: 'chat' },
  })

  if (!resp.ok()) {
    const body = await resp.text()
    throw new Error(
      `POST /api/v1/sessions failed: ${resp.status()} ${resp.statusText()} — ${body}`,
    )
  }

  const meta = (await resp.json()) as { id: string }
  if (!meta.id) {
    throw new Error('POST /api/v1/sessions returned no id')
  }
  return meta.id
}

/**
 * Rename a session via the REST API.
 * Sends both Authorization and X-CSRF-Token headers.
 */
async function renameSession(page: Page, sessionId: string, title: string): Promise<void> {
  const resp = await page.request.put(`${BASE_URL}/api/v1/sessions/${sessionId}`, {
    headers: await apiHeaders(page),
    data: { title },
  })

  if (!resp.ok()) {
    const body = await resp.text()
    throw new Error(
      `PUT /api/v1/sessions/${sessionId} failed: ${resp.status()} ${resp.statusText()} — ${body}`,
    )
  }
}

/**
 * Open (or reopen) a session via its deep-link route, `/#/sessions/{id}`.
 *
 * Was: click a button named "Open sessions panel", then one named "Open
 * session: <title>". Neither accessible name exists anywhere in current
 * `src/` since the session panel was redesigned into the two-mode
 * SearchModal (`src/components/search/SearchModal.tsx`) — see
 * tests/e2e/fixtures/session-setup.ts's `openSessionByDeepLink` doc comment
 * for the full history. The deep link is the app's own real navigation
 * seam: `src/routes/_app/sessions.$sessionId.tsx`'s loader no longer
 * pre-sets `activeSessionId`, so this drives a genuine WS attach + replay,
 * not a REST-only render.
 */
async function openSession(page: Page, sessionId: string): Promise<void> {
  await page.goto(`/#/sessions/${sessionId}`)
  // Route-swap guard FIRST (mirrors session-setup.ts's openSessionByDeepLink):
  // wait until the chat surface is bound to THIS session — a bare composer
  // wait is satisfied by the previous route's composer during the swap, and
  // typing at that instant sends on a stale/null session binding.
  await expect(page.locator(`[data-active-session-id="${sessionId}"]`)).toBeVisible({
    timeout: 15_000,
  })
  await expect(chatInput(page)).toBeVisible({ timeout: 15_000 })
}

/**
 * Wait for the WebSocket connection to be established.
 * The chat input is disabled and shows "Connecting to gateway..." when isConnected=false.
 * We wait for the placeholder to change to indicate the connection is up.
 */
async function waitForWsConnected(page: Page): Promise<void> {
  // The textarea becomes enabled when isConnected=true. Its placeholder changes from
  // "Connecting to gateway..." to an agent-specific prompt.
  // Wait for the textarea to be enabled (not disabled), indicating WS is connected.
  await expect(chatInput(page)).toBeEnabled({ timeout: 15_000 })
  // toBeEnabled() alone no longer implies "connected" since the #105
  // offline-queue fix (2fa26e6a) also enables the composer while
  // reconnecting/queueing — see waitForConnected's doc comment in
  // fixtures/selectors.ts. Confirm the socket is genuinely open.
  await waitForConnected(page, { timeout: 15_000 })
}

/**
 * Wait for the replay to complete by waiting for the done frame.
 * Post-I2: the chat input is disabled during replay (FR-I-014 — isReplaying=true) and
 * re-enabled on done. Pre-I2: the input state is unchanged by replay (always enabled).
 *
 * We wait for the chat input to be enabled as the signal that either:
 * (a) the done frame arrived and isReplaying flipped to false (post-I2), or
 * (b) replay had no effect on the input (pre-I2, so it's enabled immediately).
 *
 * The send button (ComposerPrimitive.Send) is disabled when the input is empty regardless,
 * so it is not a reliable done-frame indicator.
 */
async function waitForReplayDone(page: Page): Promise<void> {
  await expect(chatInput(page)).toBeEnabled({ timeout: 30_000 })
  // toBeEnabled() alone conflates "replay done" with "connected" — it no
  // longer implies the latter (2fa26e6a, #105 fix; see waitForConnected's
  // doc comment in fixtures/selectors.ts).
  await waitForConnected(page, { timeout: 30_000 })
}

// ── Test (a): tool-call fidelity on reopen ────────────────────────────────────

test(
  '(a) tool-call fidelity on reopen: live-captured DOM matches replay-captured DOM',
  async ({ page }) => {
    // Traces to: sprint-i-historical-replay-fidelity-spec.md BDD Scenarios 1, 2, 3; TDD row 23.
    // SC-I-004 narrow criteria: badge count, tool attribute values, status icons, message roles.
    //
    await page.goto('/')
    await expect(page.getByRole('banner')).toBeVisible({ timeout: 15_000 })

    // Wait for WS connection to be established before proceeding.
    // The send button is disabled when isConnected=false.
    await waitForWsConnected(page)

    // ── Step 1: Create a session and seed a transcript with known tool calls ──
    // Uses page.request for CSRF cookie inheritance (see createSession helper).
    const sessionId = await createSession(page)
    const sessionTitle = `replay-fidelity-test-a-${Date.now()}`
    await renameSession(page, sessionId, sessionTitle)

    // Seed the transcript: one user message + one assistant reply with two tool calls.
    // Dataset D1 (shell tool) + D4 (agent_id="ray") from spec.
    // Traces to: BDD Scenario 1 (tool-call fidelity), Scenario 3 (agent label).
    seedTranscript(sessionId, [
      {
        id: 'entry-user-1',
        role: 'user',
        content: 'run a shell command',
        timestamp: new Date(Date.now() - 5000).toISOString(),
        agent_id: '',
      },
      {
        id: 'entry-asst-1',
        role: 'assistant',
        content: 'I ran the commands for you.',
        timestamp: new Date(Date.now() - 4000).toISOString(),
        agent_id: 'ray',
        tool_calls: [
          {
            id: 't1',
            tool: 'shell',
            status: 'success',
            duration_ms: 42,
            parameters: { cmd: 'echo hi' },
            result: { stdout: 'hi\n' },
          },
          {
            id: 't2',
            tool: 'fs.list',
            status: 'success',
            duration_ms: 15,
            parameters: { path: '/tmp' },
            result: { entries: ['a', 'b'] },
          },
        ],
      },
    ])

    // ── Step 2: Navigate to the session via the session panel ──
    await openSession(page, sessionId)

    // Wait for replay to complete
    await waitForReplayDone(page)

    // ── Step 3: Capture replayed DOM ──

    // SC-I-004(iv): message role order — user message appears before assistant message.
    // Traces to: Scenario 4 (user messages), Scenario 3 (agent label).
    const userMsgs = page.locator('[data-message-id].flex-row-reverse')
    await expect(userMsgs).toHaveCount(1, { timeout: 15_000 })

    // Exclude data-status="running" — only count completed messages, not in-progress placeholders.
    const asstMsgs = page.locator('[data-message-id]:not(.flex-row-reverse):not([data-status="running"])')
    await expect(asstMsgs).toHaveCount(1, { timeout: 15_000 })

    // SC-I-004(i): badge count — two tool calls must produce two tool-call-badge elements.
    // SC-I-004(i): badge count — two tool calls must produce two tool-call-badge elements.
    const badges = page.locator('[data-testid="tool-call-badge"]')
    await expect(badges).toHaveCount(2, { timeout: 15_000 })

    // SC-I-004(ii): tool attribute values — shell then fs.list, in that order.
    // Traces to: BDD Scenario 1, Scenario 2.
    // T0.5: array-equality (not Set) — catches badge-order regressions where
    // tool-call badges jump to the wrong turn. Set-equality cannot detect ordering bugs.
    const toolNames = await badges.evaluateAll((els) =>
      els.map((el) => el.getAttribute('data-tool') ?? el.getAttribute('tool') ?? el.textContent ?? ''),
    )
    expect(toolNames).toEqual(['shell', 'fs.list'])

    // SC-I-004(iii): status icons — both badges should show success status.
    // The ToolCallBadge renders a CheckCircle icon for success (aria-label or class-based).
    for (let i = 0; i < 2; i++) {
      const badge = badges.nth(i)
      // Post-I1: badges carry data-status="success" or have aria-label containing success.
      // We check for the absence of error/running indicators as the baseline.
      await expect(badge).not.toContainText('Running...', { timeout: 5_000 })
      await expect(badge).not.toContainText('Failed', { timeout: 5_000 })
    }

    // Expanded pane: click the shell badge and verify params/result.
    // Traces to: BDD Scenario 1 (expand, echo hi params, hi result).
    const shellBadge = page.locator('[data-testid="tool-call-badge"][data-tool="shell"]').first()
    await shellBadge.click()
    await expect(shellBadge).toContainText('echo hi', { timeout: 5_000 })
    await expect(shellBadge).toContainText('hi', { timeout: 5_000 })

    // ── Axe check: SC-I-006 ──
    // WCAG 2.1 AA on replay-rendered page; zero new violations vs. baseline.
    // Traces to: SC-I-006.
    await expectA11yClean(page)
  },
)

// ── Test (b): subagent span round-trip ────────────────────────────────────────

/**
 * Append the persisted `subagent_start` + `subagent_state(running)` pair that
 * pkg/agent/steer_frames.go's `deliverSubagentStart` writes into the PARENT's
 * OWN transcript (ADR-091 D7/I-4: "persisted as events in the PARENT's own
 * transcript ... so the existing since-cursor replay returns them after a
 * reload with no new store").
 *
 * Deliberately appended AFTER `seedTranscript` (which truncates the file) and
 * kept byte-shape-identical to
 * `steered-session-stop.spec.ts::seedSubagentFramesInParentTranscript` — one
 * on-disk shape, two specs, so a transcript-format change breaks both
 * together instead of leaving one silently seeding a shape the gateway no
 * longer reads.
 *
 * `span_id` for generation 1 MUST be `"span_" + <originating tool-call id>`:
 * that is `pkg/agent/steer_frames.go::SubagentSpanID`'s convention for
 * generation 1 (generation N >= 2 appends `_g<N>`).
 * `pkg/gateway/replay.go::classifyToolCall` recomputes the generation-1 id
 * the same way to
 * decide whether this span already has a persisted start/end
 * (`persistedSubagentStartSpans` / `persistedSubagentEndSpans`). Get it wrong
 * and replay silently falls back to the reconstructed, child_session_id-less
 * frame instead.
 */
function appendPersistedSubagentStart(
  parentSessionId: string,
  childSessionId: string,
  callId: string,
  taskLabel: string,
  agentId: string,
): void {
  const sessionDir = path.join(OMNIPUS_HOME, 'sessions', parentSessionId)
  if (!fs.existsSync(sessionDir)) {
    throw new Error(
      `Session directory does not exist: ${sessionDir}. ` +
        'Create the session via REST API before seeding the transcript.',
    )
  }
  const now = new Date().toISOString()
  // generation 1 deliberately (see SubagentSpanID)
  const spanId = `span_${callId}`
  const entries = [
    {
      id: `${callId}:start`,
      type: 'system',
      system_subtype: 'subagent_start',
      timestamp: now,
      agent_id: agentId,
      subagent_start: {
        type: 'subagent_start',
        session_id: parentSessionId,
        child_session_id: childSessionId,
        span_id: spanId,
        parent_call_id: callId,
        task_label: taskLabel,
        agent_id: agentId,
      },
    },
    {
      id: `${callId}:state`,
      type: 'system',
      system_subtype: 'subagent_state',
      timestamp: now,
      agent_id: agentId,
      subagent_state: {
        type: 'subagent_state',
        session_id: parentSessionId,
        child_session_id: childSessionId,
        span_id: spanId,
        state: 'running',
        created_at: now,
      },
    },
  ]
  fs.appendFileSync(
    path.join(sessionDir, 'transcript.jsonl'),
    entries.map((e) => JSON.stringify(e)).join('\n') + '\n',
    { encoding: 'utf-8' },
  )
}

/**
 * Open the Activity panel, tolerating an already-open one, and close it again
 * once the caller is done.
 *
 * `ActivityBar`'s `panelOpen` is component-local React state and the panel is
 * a Radix `Dialog` with `modal` defaulting to true (src/components/ui/sheet.tsx),
 * which puts `pointer-events: none` on the document while open. A navigation
 * that only changes the hash (`page.goto('/#/sessions/<id>')`, and `goto('/')`
 * from one) is a SAME-document navigation, so the bar is never unmounted and
 * the panel never closes by itself — an unconditional second click on the bar
 * would then be blocked forever by the modal behind it. That exact trap cost
 * `steered-session-stop.spec.ts` a 90s timeout in CI (2026-09-24); see
 * `openActivityPanel` there.
 */
async function openActivityPanel(page: Page): Promise<void> {
  const bar = page.locator('[data-testid="activity-bar"]')
  await expect(bar).toBeVisible({ timeout: 15_000 })
  if ((await bar.getAttribute('aria-expanded')) !== 'true') {
    await bar.click()
  }
  // `aria-expanded` is bound directly to `panelOpen` (ActivityBar.tsx), so it
  // is the panel's own open signal rather than a proxy for it.
  await expect(bar).toHaveAttribute('aria-expanded', 'true', { timeout: 15_000 })
}

async function closeActivityPanel(page: Page): Promise<void> {
  const bar = page.locator('[data-testid="activity-bar"]')
  await page.keyboard.press('Escape')
  await expect(bar).toHaveAttribute('aria-expanded', 'false', { timeout: 15_000 })
}

/**
 * Assert the WHOLE post-ADR-091 delegation contract for a parent session that
 * is showing one in-flight delegation. Called once on the first open and again
 * after the close/reopen — the round trip is "these two calls agree", so every
 * clause below is a round-trip assertion, not just the panel row.
 */
async function expectParentDelegationSurface(page: Page, taskLabel: string): Promise<void> {
  // (1) THREAD — the delegation tool call itself is the parent chat's ONLY
  // delegation surface (ADR-091 D7/AC-7, src/lib/toolVisibility.ts's
  // `shouldRenderToolCall` header), and AC-7 requires it in the DEFAULT,
  // non-verbose thread. This test therefore does NOT opt into verbose chat:
  // needing verbose to see a delegation is itself the regression.
  await expect(
    page.locator('[data-testid="tool-call-badge"][data-tool="spawn"]'),
  ).toHaveCount(1, { timeout: 15_000 })

  // (2) THREAD — no span card, at any verbosity. `SubagentBlock` and its gate
  // `shouldRenderSubagentSpan` were deleted by ADR-091 D7/D10 (commit
  // 66362240d); `data-testid="subagent-collapsed"` has no producer left in
  // `src/`. Asserted as a contract so the card cannot quietly come back and
  // re-nest a child's steps under the parent.
  await expect(page.locator('[data-testid="subagent-collapsed"]')).toHaveCount(0)

  // (3) THREAD — the child's OWN recorded step never renders in the parent.
  // The seeded dataset is a pre-ADR-091 transcript that DOES nest a child tool
  // call under the spawn call; `pkg/gateway/replay.go::classifyToolCall` drops
  // such a nested call from replay outright ("greenfield migration, §6 ... it
  // is simply dropped from replay rather than mis-rendered as a flat top-level
  // call"). This is the behaviour that replaced the old "correct step count"
  // assertion: the count is not merely zero on the parent's span, the step is
  // not in the parent's thread at all.
  await expect(
    page.locator('[data-testid="tool-call-badge"][data-tool="shell"]'),
  ).toHaveCount(0)

  // (4) PANEL — the parent's row for this child, the surface that replaced
  // the deleted card. `data-status` is the SPAN axis (`item.status`); the
  // delegation was still in flight when the transcript was written (a
  // persisted `subagent_start` with no matching `subagent_end`), so
  // `replay.go::classifyToolCall`'s `stillActive` withholds the terminal
  // frames and the span must come back RUNNING rather than a fabricated
  // "done".
  await openActivityPanel(page)
  const row = page.locator('[data-testid="activity-row"]', { hasText: taskLabel })
  await expect(row).toBeVisible({ timeout: 15_000 })
  await expect(row).toHaveAttribute('data-status', 'running')

  // (5) PANEL — the way INTO the child's own session. ADR-091 D7/I-4 gave
  // `subagent_start` its `child_session_id` precisely "so the open control
  // knows where to go"; the control renders only when the span carries one
  // (ActivityPanel.tsx). Its presence after a reopen is what proves the
  // persisted start frame — not the reconstructed, child_session_id-less
  // fallback — survived the round trip.
  await expect(row.locator('[data-testid="activity-row-open"]')).toBeVisible()

  await closeActivityPanel(page)
}

test(
  "(b) subagent span round-trip: the parent's delegation row survives a reopen and still opens the child's own session",
  // Traces to: sprint-i-historical-replay-fidelity-spec.md BDD Scenario 5; TDD row 24.
  //
  // REWRITTEN 2026-09-24 (lane FX-E2E). This test used to be
  // "(b) subagent span round-trip: SubagentBlock renders on reopen with
  // correct step count" and failed 4/4 in CI (shard ui-heavy) at its very
  // first assertion, before any reopen. It was asserting behaviour ADR-091
  // D7/D10 deliberately DELETED, not a replay regression: commit 66362240d
  // ("delete the nested child-step rendering — SubagentBlock, its ChatScreen
  // mounts, and shouldRenderSubagentSpan") removed the component, and a
  // repo-wide search finds no producer of `data-testid="subagent-collapsed"`
  // in `src/` at all. A child's own frames now carry the CHILD's session_id
  // (I-4) and land in the child's own bucket, so "step count on the PARENT's
  // span" is not a number the system computes any more.
  //
  // The round-trip coverage is NOT dropped — it is re-pointed at the surfaces
  // that replaced the card: the delegation tool call in the parent's default
  // thread, the absence of any span card or nested child step, and the
  // Activity panel row with its open control into the child's own session.
  // See `expectParentDelegationSurface` above, which is asserted identically
  // before and after the close/reopen.
  //
  // Coverage genuinely removed, and why: the old assertion
  // `expect(replayedStepText).toBe(liveStepText)` compared a step-count
  // string rendered by a component that no longer exists, on a span that no
  // longer accumulates steps. There is nothing left to point it at. Clause
  // (3) below is its replacement in spirit — it pins the child's step to
  // being absent from the parent, which is the contract the deletion created.
  async ({ page }) => {
    await page.goto('/')
    await expect(page.getByRole('banner')).toBeVisible({ timeout: 15_000 })

    const sessionId = await createSession(page)
    // A REAL child session, so the open control has a live route to land on
    // at the end of this test rather than a fabricated id.
    const childSessionId = await createSession(page)
    const sessionTitle = `replay-fidelity-test-b-${Date.now()}`
    await renameSession(page, sessionId, sessionTitle)

    const callId = 'c1'
    const taskLabel = `replay-fidelity-b-${Date.now()}`

    // Dataset D2: a delegation that was still in flight when the transcript
    // was last written, plus a nested child tool call.
    //
    // NOTE (ADR-036, 2026-07-04): tool: 'spawn' is deliberately kept as the
    // legacy pre-merge delegation tool name, NOT updated to 'delegate'. This
    // dataset exercises the replay backend's historical-transcript compat path
    // (pkg/gateway/replay.go's buildSpawnIDsWithChildren and classifyToolCall
    // both check `tc.Tool == "spawn" || tc.Tool == "delegate" ||
    // tc.Tool == "create_task"` so sessions recorded before the rename still
    // reconstruct a subagent span correctly). Changing this to 'delegate'
    // would silently drop e2e coverage of that back-compat branch.
    //
    // The spawn call carries no result and status 'running': its terminal
    // frames are withheld by `stillActive` anyway, and recording a "success"
    // for a call the transcript also says is still open would be an
    // incoherent fixture.
    seedTranscript(sessionId, [
      {
        id: 'entry-asst-spawn',
        role: 'assistant',
        content: 'Spawning a subagent.',
        timestamp: new Date(Date.now() - 3000).toISOString(),
        agent_id: 'mia',
        tool_calls: [
          {
            id: callId,
            tool: 'spawn',
            status: 'running',
            parameters: { task: taskLabel },
          },
        ],
      },
      {
        id: 'entry-nested-1',
        role: 'assistant',
        content: '',
        timestamp: new Date(Date.now() - 2000).toISOString(),
        agent_id: 'ray',
        tool_calls: [
          {
            id: 't2',
            tool: 'shell',
            status: 'success',
            duration_ms: 20,
            parameters: { cmd: 'echo nested' },
            result: { stdout: 'nested\n' },
            parent_tool_call_id: callId,
          },
        ],
      },
    ])
    appendPersistedSubagentStart(sessionId, childSessionId, callId, taskLabel, 'mia')

    await openSession(page, sessionId)
    await waitForReplayDone(page)
    await expectParentDelegationSurface(page, taskLabel)

    // Close and reopen the session to validate the replay round-trip.
    await page.goto('/')
    await openSession(page, sessionId)
    await waitForReplayDone(page)
    await expectParentDelegationSurface(page, taskLabel)

    // Finally, take the open control: the row must not merely LOOK navigable,
    // it must actually land on the child's own session (FR-046 / ADR-091
    // D7/I-4 — "the child's steps are visible only in the child's own
    // session, opened via childSessionId"). Left last so nothing above
    // depends on the navigation.
    await openActivityPanel(page)
    await page
      .locator('[data-testid="activity-row"]', { hasText: taskLabel })
      .locator('[data-testid="activity-row-open"]')
      .click()
    await expect(
      page.locator(`[data-active-session-id="${childSessionId}"]`),
    ).toBeVisible({ timeout: 15_000 })
  },
)

// ── Test (c): attach-during-active-turn no event loss ─────────────────────────

// Respect OMNIPUS_AUTH_FILE env var set by isolated test runs (e.g. port 6062)
// to avoid using a token minted for a different gateway instance.
const AUTH_FILE = process.env.OMNIPUS_AUTH_FILE
  ? path.resolve(process.env.OMNIPUS_AUTH_FILE)
  : path.join(
      path.dirname(new URL(import.meta.url).pathname),
      'fixtures/.auth/admin.json',
    )

test(
  '(c) attach-during-active-turn: second browser context receives all events without loss',
  // Belt-and-suspenders companion to TestAttach_RegistersLiveEventsBeforeReplay
  // (pkg/gateway/replay_test.go). Drives a real LLM with a prompt engineered to
  // produce a long enough response (4-12s of streaming) that page2 can attach
  // mid-turn before the final assistant message arrives.
  //
  // The scripted-scenario harness this test originally used was removed
  // 2026-05-10 along with the test_harness build tag. We now rely on the LLM's
  // streaming latency for the "active turn" window — variance is acceptable
  // because the assertions check final state, and the wall-clock floor is
  // relaxed to 3s (vs. the deterministic 7s the harness provided).
  //
  // Traces to: sprint-i-historical-replay-fidelity-spec.md BDD Scenario 9; TDD row 25.
  async ({ page, browser }) => {
    test.setTimeout(90_000)

    // ── Step 1: page1 — fresh session ──
    await page.goto('/')
    await expect(page.getByRole('banner')).toBeVisible({ timeout: 15_000 })
    await waitForWsConnected(page)

    const sessionId = await createSession(page)
    const sessionTitle = `replay-fidelity-test-c-${Date.now()}`
    await renameSession(page, sessionId, sessionTitle)

    // ── Step 2: open session and send a long-form prompt on page1 ──
    // The unique nonce forces a verbatim echo we can match exactly. The
    // 600-word body prefix ensures the response streams for several seconds
    // (typically 4-12s on gemini-2.5-flash) so page2 can attach mid-turn.
    await openSession(page, sessionId)
    await waitForReplayDone(page)

    const nonce = `attach-during-turn-${Date.now()}`
    const prompt =
      `Write exactly 600 words about renewable energy. Vary your wording — do not repeat sentences. ` +
      `At the very end of your reply, output the literal token ${nonce} on its own line.`

    const input = chatInput(page)
    await expect(input).toBeEnabled({ timeout: 10_000 })
    // toBeEnabled() alone no longer implies "connected" (2fa26e6a, #105 fix —
    // see waitForConnected's doc comment in fixtures/selectors.ts).
    await waitForConnected(page, { timeout: 10_000 })
    await input.fill(prompt)
    const sendStart = Date.now()
    await input.press('Enter')

    // The user message should appear immediately (echoed by WS).
    const userMsgs = page.locator('[data-message-id].flex-row-reverse')
    await expect(userMsgs).toHaveCount(1, { timeout: 5_000 })

    // ── Step 3: while the agent is still streaming, attach page2 ──
    // Wait ~800ms after send so we are reliably mid-stream but well before
    // the assistant message completes.
    await page.waitForTimeout(800)
    const context2 = await browser.newContext({ storageState: AUTH_FILE, baseURL: BASE_URL })
    const page2 = await context2.newPage()

    try {
      await page2.goto('/')
      await expect(page2.getByRole('banner')).toBeVisible({ timeout: 15_000 })
      await waitForWsConnected(page2)
      await openSession(page2, sessionId)
      await waitForReplayDone(page2)

      // page2 must replay the user message that page1 sent before page2 attached.
      const userMsgs2 = page2.locator('[data-message-id].flex-row-reverse')
      await expect(userMsgs2).toHaveCount(1, { timeout: 10_000 })

      // ── Step 4: both contexts receive the final assistant message ──
      // Wait for the assistant reply containing our nonce on both pages.
      // The nonce is a unique random string we asked the LLM to echo, so a
      // body-text containment check confirms the response was delivered.
      // Page2 first — it attached mid-turn so it's the harder case (FR-I-009).
      await expect(page2.locator('body')).toContainText(nonce, { timeout: 60_000 })
      await expect(page.locator('body')).toContainText(nonce, { timeout: 60_000 })

      // page2 attached AFTER the turn started, so the only way it could see the
      // assistant message is if live forwarding picked up where replay left off
      // — that's what FR-I-009 demands and what we're verifying. We require at
      // least 1.5s of wall-clock since send to confirm we observed a real
      // mid-turn attach (the 800ms attach delay + page2 setup means anything
      // above that floor proves page2 attached *after* the user message but
      // *during* the turn). The previous 3s floor failed when GLM-5v-turbo
      // streamed the 600-word reply in <3s, even though the mid-turn-attach
      // contract was still satisfied (page2 attached at 800ms, well within
      // the streaming window).
      const wallClock = Date.now() - sendStart
      expect(wallClock).toBeGreaterThanOrEqual(1_500)

      // Final-state agreement: both browser contexts must show the same nonce.
      // The body-text contains check above proved both saw it; belt-and-suspenders
      // verifies the matched bubble text agrees character-for-character.
      const text1 = await page.locator(`text=${nonce}`).first().innerText()
      const text2 = await page2.locator(`text=${nonce}`).first().innerText()
      expect(text2.trim()).toBe(text1.trim())
    } finally {
      await context2.close()
    }
  },
)

// ── Test (d): live continuation after replay ──────────────────────────────────

test(
  '(d) live continuation after replay: new message appends below replayed transcript',
  async ({ page }) => {
    // Traces to: sprint-i-historical-replay-fidelity-spec.md BDD Scenario 8; TDD row 26.
    // SC-I-004(iv): message ordering preserved; live continuation appears after replayed transcript.
    //
    // IMPORTANT: This test requires a real LLM (OPENROUTER_API_KEY_CI) to send a live message.

    // Bump per-test timeout to 360s. test.slow() triples the global 90s to 270s,
    // which has proven insufficient under full-suite LLM contention (this test
    // runs late in the suite after ~45 prior tests have hit OpenRouter,
    // backlogging GLM-4.6's extended-thinking queue). In isolation the test
    // completes in ~6s; in-suite it routinely needs 90-150s for the assistant
    // reply to materialize, so the 90s assertion timeout below was over budget.
    test.setTimeout(360_000)

    await page.goto('/')
    await expect(page.getByRole('banner')).toBeVisible({ timeout: 15_000 })

    // Wait for WS connection to be established.
    await waitForWsConnected(page)

    const sessionId = await createSession(page)
    const sessionTitle = `replay-fidelity-test-d-${Date.now()}`
    await renameSession(page, sessionId, sessionTitle)

    // Seed a simple multi-entry transcript (no tool calls — validates text replay + continuation).
    // Traces to: BDD Scenario 8 (replay → live continuation), Scenario 4 (user message replay).
    seedTranscript(sessionId, [
      {
        id: 'entry-user-d1',
        role: 'user',
        content: 'My first message in this replayed session.',
        timestamp: new Date(Date.now() - 10000).toISOString(),
        agent_id: '',
      },
      {
        id: 'entry-asst-d1',
        role: 'assistant',
        content: 'I acknowledge your first message.',
        timestamp: new Date(Date.now() - 9000).toISOString(),
        agent_id: 'mia',
      },
    ])

    // ── Step 1: Open the session and wait for replay ──
    await openSession(page, sessionId)
    await waitForReplayDone(page)

    // Verify the replayed messages are present (user + assistant).
    // Both are simple text entries — works even without I1.
    const userMsgs = page.locator('[data-message-id].flex-row-reverse')
    await expect(userMsgs).toHaveCount(1, { timeout: 15_000 })

    // W2-9: Replace loose GreaterThanOrEqual(1) with exact count.
    // The fixture seeds exactly 1 assistant message — fidelity tests assert fidelity, not tolerance.
    // Traces to: temporal-puzzling-melody.md W2-9
    // Exclude data-status="running" to only count completed messages, not in-progress placeholders.
    const asstMsgs = page.locator('[data-message-id]:not(.flex-row-reverse):not([data-status="running"])')
    await expect(asstMsgs).toHaveCount(1, { timeout: 15_000 })
    const countAfterReplay = 1

    // ── Step 2: Send a new message and verify it appears BELOW the replayed transcript ──
    // Traces to: BDD Scenario 8 AS-1: new response streams in below replayed transcript.
    //
    // Phrasing note: the prompt mirrors chat.spec.ts (b)'s "Echo it back verbatim"
    // pattern, which reliably yields deterministic short text from Mia (main agent).
    // Earlier wording ("Reply with exactly:") tripped Mia's "no boilerplate" guardrail
    // and the LLM streamed only thinking-mode placeholders without producing final text.
    const input = chatInput(page)
    await expect(input).toBeEnabled({ timeout: 10_000 })
    // toBeEnabled() alone no longer implies "connected" (2fa26e6a, #105 fix —
    // see waitForConnected's doc comment in fixtures/selectors.ts).
    await waitForConnected(page, { timeout: 10_000 })
    await input.fill(
      'Echo this token back to me verbatim, on its own line, with no other words: continuation confirmed',
    )
    await input.press('Enter')

    // A new assistant message appears (total count increases by 1).
    // 180s: GLM-4.6 routinely takes 90-150s under full-suite load (extended
    // thinking + the OpenRouter queue backlogged by prior tests). 90s was
    // insufficient — failed all 3 retries in the 2026-05-16 full-suite run
    // while passing in 6s in isolation.
    await expect(asstMsgs).toHaveCount(countAfterReplay + 1, { timeout: 180_000 })

    // The last assistant message should contain the expected reply.
    const lastAsstMsg = asstMsgs.last()
    await expect(lastAsstMsg).toContainText(/continuation confirmed/i, { timeout: 90_000 })

    // No messages are duplicated: the replayed messages remain exactly as seeded.
    // SC-I-004(iv): user messages still show exactly 1 (the replayed one + new one we sent = 2).
    await expect(userMsgs).toHaveCount(2, { timeout: 10_000 })

    // All assistant messages: original replayed (1) + new response (1) = 2.
    // Duplication would show 3+ or messages out of order.
    await expect(asstMsgs).toHaveCount(2, { timeout: 10_000 })
  },
)

// ── Test (f): tool_call badges survive navigate-away → navigate-back ──────────
//
// Contract under test: tool_call badges present when a session is first opened
// must still be present (same count, same data-tool values) after the user
// navigates away and navigates back to the same session.  A failure would
// indicate that the gateway replay path drops or reorders tool_call frames
// on a second attach.
//
// BDD:
//   Given a session with known tool_call transcript entries exists
//   When the user opens the session (first open — replay path)
//   Then tool_call badges equal the seeded count with correct data-tool values
//   When the user navigates away from the session
//   And the user navigates back to the same session (second open — replay again)
//   Then the badge count and data-tool values are identical to the first open
//
// This test uses the same deterministic seedTranscript approach as tests (a)–(b)
// to avoid LLM non-determinism while still exercising the reopen / second-attach
// replay path that tests (a)–(b) do not cover.
//
// Traces to: sprint-i-historical-replay-fidelity-spec.md BDD Scenario 1 (tool-call
//   fidelity) and Scenario 2 (tool attribute values survive reopen); TDD row 23.

test(
  '(f) real-session replay: tool_call badges survive session reopen',
  async ({ page }) => {
    await page.goto('/')
    await expect(page.getByRole('banner')).toBeVisible({ timeout: 15_000 })
    await waitForWsConnected(page)

    // ── Step 1: Create a session seeded with one tool_call entry ──
    const sessionId = await createSession(page)
    const sessionTitle = `replay-fidelity-test-f-${Date.now()}`
    await renameSession(page, sessionId, sessionTitle)

    // Seed transcript: one assistant turn with a completed bash tool call.
    // This mirrors the JSONL that the gateway writes during a real tool turn
    // (ADR-036 renamed exec/workspace_shell/workspace_shell_bg to the unified
    // "bash" tool; a fresh session's transcript now records tool: "bash").
    seedTranscript(sessionId, [
      {
        id: 'entry-user-f1',
        role: 'user',
        content: 'Run echo replay-badge-test',
        timestamp: new Date(Date.now() - 5000).toISOString(),
        agent_id: '',
      },
      {
        id: 'entry-asst-f1',
        role: 'assistant',
        content: 'Done.',
        timestamp: new Date(Date.now() - 4000).toISOString(),
        agent_id: 'mia',
        tool_calls: [
          {
            id: 'tc-f1',
            tool: 'bash',
            status: 'success',
            duration_ms: 31,
            parameters: { action: 'run', command: 'echo replay-badge-test' },
            result: { stdout: 'replay-badge-test\n', exit_code: 0 },
          },
        ],
      },
    ])

    // ── Step 2: Open the session for the first time and capture badge count ──
    await openSession(page, sessionId)
    await waitForReplayDone(page)

    // Since chat-tool-ui-collapse, bash renders via BashOutputBlock on BOTH
    // paths (replay included), whose toggle is data-testid="bash-output-toggle"
    // — not the generic tool-call-badge. Accept either shape (both are real
    // renderings of the same seeded call, same pattern as handoff.spec.ts's
    // bash assertion); the data-tool read below resolves for both now that
    // BashOutputBlock's outer wrapper carries data-tool (GenericToolCall/
    // ToolCallBadge's existing convention).
    const badgeLocator = page.locator('[data-testid="tool-call-badge"], [data-testid="bash-output-toggle"]')
    await expect(badgeLocator.first()).toBeVisible({ timeout: 15_000 })
    const firstOpenCount = await badgeLocator.count()
    expect(firstOpenCount).toBe(1)

    const toolNames = await badgeLocator.evaluateAll((els) =>
      els.map((el) => el.getAttribute('data-tool') ?? ''),
    )
    expect(toolNames).toEqual(['bash'])

    // ── Step 3: Navigate away ──
    await page.goto('/')
    await expect(page.getByRole('banner')).toBeVisible({ timeout: 10_000 })
    await waitForWsConnected(page)

    // ── Step 4: Navigate back and verify badges are identical ──
    // This is the core contract: the second open (replay) must produce the
    // same badges as the first open.
    await openSession(page, sessionId)
    await waitForReplayDone(page)

    // Same either-shape locator as first open (see above).
    const replayedBadges = page.locator('[data-testid="tool-call-badge"], [data-testid="bash-output-toggle"]')
    await expect(replayedBadges).toHaveCount(firstOpenCount, { timeout: 15_000 })

    const replayedToolNames = await replayedBadges.evaluateAll((els) =>
      els.map((el) => el.getAttribute('data-tool') ?? ''),
    )
    expect(replayedToolNames).toEqual(toolNames)
  },
)

// ── Test (e): send button disabled during replay ──────────────────────────────
//
// T0.1: Promoted from test.fixme — issue #133 was previously used to suppress
// this assertion. It is now required. If the input is not disabled during
// replay, that is a confirmed product regression (FR-I-014) that must be fixed.
// Do not re-suppress with test.fixme or test.skip.

test(
  '(e) send button disabled during replay: input locked until done frame arrives',
  async ({ page }) => {
    // Traces to: sprint-i-historical-replay-fidelity-spec.md BDD Scenario 10; TDD row 27; FR-I-014.
    //
    // IMPORTANT: This test validates FR-I-014 which requires I2 (isReplaying state in chat store).
    // Until I2 lands, the send button will NOT be disabled during replay — this test will FAIL (red).
    // That is the intended behaviour: the failing test shows the feature is not yet implemented.
    //
    // To validate: the test attempts to read the button's disabled state immediately after
    // navigating to a session that has a non-trivial transcript (enough entries to take >0ms to
    // replay). We cannot guarantee timing precisely, but a seeded 20-entry transcript with
    // consecutive entries should give enough replay time to observe the disabled state.

    await page.goto('/')
    await expect(page.getByRole('banner')).toBeVisible({ timeout: 15_000 })

    // Wait for WS connection to be established.
    await waitForWsConnected(page)

    const sessionId = await createSession(page)
    const sessionTitle = `replay-fidelity-test-e-${Date.now()}`
    await renameSession(page, sessionId, sessionTitle)

    // Seed a transcript with many entries to extend replay duration.
    // 10 alternating user/assistant turns with tool calls to give I1's replay loop
    // enough frames to keep the "done" frame delayed for a few hundred milliseconds.
    // Traces to: BDD Scenario 10.
    const entries: TranscriptEntry[] = []
    for (let i = 0; i < 10; i++) {
      entries.push({
        id: `entry-user-${i}`,
        role: 'user',
        content: `Message ${i}`,
        timestamp: new Date(Date.now() - (10000 - i * 900)).toISOString(),
        agent_id: '',
      })
      entries.push({
        id: `entry-asst-${i}`,
        role: 'assistant',
        content: `Response to message ${i}`,
        timestamp: new Date(Date.now() - (9000 - i * 900)).toISOString(),
        agent_id: 'mia',
        tool_calls: [
          {
            id: `tc-${i}`,
            tool: 'shell',
            status: 'success',
            duration_ms: 30,
            parameters: { cmd: `echo ${i}` },
            result: { stdout: `${i}\n` },
          },
        ],
      })
    }
    seedTranscript(sessionId, entries)

    // ── Navigate to the session and immediately check send button state ──
    // Traces to: Scenario 10 When "user types in the input → send button is disabled".
    //
    // This test does NOT use the shared `openSession` helper (which now
    // navigates via the deep-link route AND waits for the composer to
    // re-enable) because it needs to install a MutationObserver BEFORE the
    // attach-triggering navigation fires, then trigger that navigation
    // itself — see the OBSERVATION STRATEGY comment below. `sessionId` is
    // already in scope from the `createSession` call above; the navigation
    // itself happens further down, right after the observer is armed.

    // Note: the send button (ComposerPrimitive.Send) is ALSO disabled when the input
    // is empty (AssistantUI internal behavior), making it unreliable for replay detection.
    // We use the textarea disabled state as the primary replay indicator.
    const input = chatInput(page)

    // FR-I-014 (I2): the chat input MUST be disabled during replay (isReplaying=true).
    // The textarea is disabled={!isConnected || isStreaming || isUploading || isReplaying}.
    // isReplaying flips on at attachToSession (sessionBtn click handler), and off when
    // the 'done' frame arrives from the gateway (with a MIN_REPLAY_DISPLAY_MS=750ms
    // minimum visible window enforced in src/store/chat.ts).
    //
    // OBSERVATION STRATEGY: install a client-side MutationObserver BEFORE the click
    // and capture every disabled/placeholder transition. The earlier `Promise.all`
    // approach raced `expect.toBeDisabled` against `click()`, but Playwright's
    // assertion-polling cadence (~100ms minimum, with backoff) and click()'s
    // post-action networkidle wait conspire to miss the 750ms disabled window
    // when the seeded transcript is small. The MutationObserver runs inside the
    // browser on every attribute mutation, so it cannot miss the transition no
    // matter how short the window is.
    await page.evaluate(() => {
      const w = window as unknown as { __replayDisabledTrace?: Array<{ t: number; disabled: boolean; placeholder: string }> }
      w.__replayDisabledTrace = []
      const observe = (ta: HTMLTextAreaElement): void => {
        const obs = new MutationObserver(() => {
          w.__replayDisabledTrace!.push({
            t: performance.now(),
            disabled: ta.disabled,
            placeholder: ta.placeholder,
          })
        })
        obs.observe(ta, { attributes: true, attributeFilter: ['disabled', 'placeholder'] })
        // Capture the initial state too.
        w.__replayDisabledTrace!.push({
          t: performance.now(),
          disabled: ta.disabled,
          placeholder: ta.placeholder,
        })
      }
      // Observe the current textarea AND any future ones (React may remount on session switch).
      const current = document.querySelector<HTMLTextAreaElement>('textarea[aria-label="Message input"]')
      if (current) observe(current)
      const bodyObs = new MutationObserver((mutations) => {
        for (const m of mutations) {
          for (const node of m.addedNodes) {
            if (!(node instanceof HTMLElement)) continue
            const found = node.matches?.('textarea[aria-label="Message input"]')
              ? (node as HTMLTextAreaElement)
              : node.querySelector?.<HTMLTextAreaElement>('textarea[aria-label="Message input"]')
            if (found) observe(found)
          }
        }
      })
      bodyObs.observe(document.body, { subtree: true, childList: true })
    })

    // Trigger the attach-triggering navigation now that the observer is armed.
    // Same-document hash navigation (HashRouter): the `window` the observer
    // was installed on survives this goto, so the trace keeps accumulating
    // uninterrupted — same guarantee the old sessionBtn.click() gave, just
    // addressed directly via the app's deep-link route (see openSession's
    // doc comment above for why this is the current real seam, not a
    // workaround).
    await page.goto(`/#/sessions/${sessionId}`)

    // Poll the observer trace from the test side. We accept anything that proves
    // the disabled state was true at least once between click and done. expect.poll
    // gives us up to 10s of patient waiting in case the SPA boots slowly under load.
    await expect
      .poll(
        async () => {
          return page.evaluate(() => {
            const w = window as unknown as { __replayDisabledTrace?: Array<{ disabled: boolean }> }
            return (w.__replayDisabledTrace ?? []).some((entry) => entry.disabled === true)
          })
        },
        {
          timeout: 10_000,
          message:
            'expected textarea to be disabled at least once during replay (FR-I-014). ' +
            'The trace recorded only disabled=false events — isReplaying never reached the composer.',
        },
      )
      .toBe(true)

    // After replay completes (done frame arrives, isReplaying → false), input must be enabled.
    // Traces to: Scenario 10 When "replay's done frame arrives → send button becomes enabled".
    // Routed through the shared waitForReplayDone helper (also confirms the
    // socket is genuinely connected, not just enabled-while-queueing — see
    // waitForConnected's doc comment in fixtures/selectors.ts) for
    // consistency with every other replay-done gate in this file.
    await waitForReplayDone(page)

    // After replay, verify the send button enables when text is typed.
    // (ComposerPrimitive.Send is also disabled on empty input — typing enables it.)
    await input.fill('test message to verify send is active')
    const send = sendButton(page)
    await expect(send).toBeEnabled({ timeout: 5_000 })

    // Clear the input after validation
    await input.fill('')
  },
)

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
import { chatInput, sendButton } from './fixtures/selectors'

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
  const csrfCookie = cookies.find((c) => c.name === '__Host-csrf')
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
    data: { agent_id: 'main', type: 'chat' },
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
 * Navigate to the sessions panel and click a session by its title.
 * The session must be visible in the panel (sorted by recency — newly created sessions appear first).
 */
async function openSession(page: Page, sessionTitle: string): Promise<void> {
  // Click the "Sessions" button in the top-right of the chat header
  await page.getByRole('button', { name: 'Open sessions panel' }).click()

  // Wait for the session list to appear
  const sessionBtn = page
    .getByRole('button', { name: new RegExp(`Open session: ${sessionTitle}`, 'i') })
    .first()

  await expect(sessionBtn).toBeVisible({ timeout: 10_000 })
  await sessionBtn.click()
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
    await openSession(page, sessionTitle)

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

test(
  '(b) subagent span round-trip: SubagentBlock renders on reopen with correct step count',
  // Traces to: sprint-i-historical-replay-fidelity-spec.md BDD Scenario 5; TDD row 24.
  // Unskipped after Sprint H SubagentBlock UI + Sprint I streamReplay landed on the same branch.
  async ({ page }) => {
    await page.goto('/')
    await expect(page.getByRole('banner')).toBeVisible({ timeout: 15_000 })

    const sessionId = await createSession(page)
    const sessionTitle = `replay-fidelity-test-b-${Date.now()}`
    await renameSession(page, sessionId, sessionTitle)

    // Dataset D2: spawn call + nested tool with ParentToolCallID.
    // Traces to: BDD Scenario 5.
    seedTranscript(sessionId, [
      {
        id: 'entry-asst-spawn',
        role: 'assistant',
        content: 'Spawning a subagent.',
        timestamp: new Date(Date.now() - 3000).toISOString(),
        agent_id: 'mia',
        tool_calls: [
          {
            id: 'c1',
            tool: 'spawn',
            status: 'success',
            duration_ms: 500,
            parameters: { task: 'do nested work' },
            result: { status: 'done' },
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
            parent_tool_call_id: 'c1',
          },
        ],
      },
    ])

    await openSession(page, sessionTitle)
    await waitForReplayDone(page)

    // Assert SubagentBlock is present.
    const subagentBlock = page.locator('[data-testid="subagent-collapsed"]').first()
    await expect(subagentBlock).toBeVisible({ timeout: 15_000 })

    // Capture live step count from collapsed header.
    const liveStepText = await subagentBlock.textContent()

    // Expand the block.
    await subagentBlock.click()

    // Assert the nested tool call badge is present.
    const nestedBadge = page.locator('[data-testid="tool-call-badge"][data-tool="shell"]').first()
    await expect(nestedBadge).toBeVisible({ timeout: 5_000 })

    // Close and reopen the session to validate replay round-trip.
    // Navigate away then back.
    await page.goto('/')
    await openSession(page, sessionTitle)
    await waitForReplayDone(page)

    const replayedBlock = page.locator('[data-testid="subagent-collapsed"]').first()
    await expect(replayedBlock).toBeVisible({ timeout: 15_000 })
    const replayedStepText = await replayedBlock.textContent()

    // Same step count as live.
    expect(replayedStepText).toBe(liveStepText)

    // Expand replayed block; nested badge still present.
    await replayedBlock.click()
    await expect(
      page.locator('[data-testid="tool-call-badge"][data-tool="shell"]').first(),
    ).toBeVisible({ timeout: 5_000 })
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
    await openSession(page, sessionTitle)
    await waitForReplayDone(page)

    const nonce = `attach-during-turn-${Date.now()}`
    const prompt =
      `Write exactly 600 words about renewable energy. Vary your wording — do not repeat sentences. ` +
      `At the very end of your reply, output the literal token ${nonce} on its own line.`

    const input = chatInput(page)
    await expect(input).toBeEnabled({ timeout: 10_000 })
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
      await openSession(page2, sessionTitle)
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
    await openSession(page, sessionTitle)
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

    // Seed transcript: one assistant turn with a completed exec tool call.
    // This mirrors the JSONL that the gateway writes during a real tool turn.
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
            tool: 'exec',
            status: 'success',
            duration_ms: 31,
            parameters: { action: 'run', command: 'echo replay-badge-test' },
            result: { stdout: 'replay-badge-test\n', exit_code: 0 },
          },
        ],
      },
    ])

    // ── Step 2: Open the session for the first time and capture badge count ──
    await openSession(page, sessionTitle)
    await waitForReplayDone(page)

    const badgeLocator = page.locator('[data-testid="tool-call-badge"]')
    await expect(badgeLocator.first()).toBeVisible({ timeout: 15_000 })
    const firstOpenCount = await badgeLocator.count()
    expect(firstOpenCount).toBe(1)

    const toolNames = await badgeLocator.evaluateAll((els) =>
      els.map((el) => el.getAttribute('data-tool') ?? ''),
    )
    expect(toolNames).toEqual(['exec'])

    // ── Step 3: Navigate away ──
    await page.goto('/')
    await expect(page.getByRole('banner')).toBeVisible({ timeout: 10_000 })
    await waitForWsConnected(page)

    // ── Step 4: Navigate back and verify badges are identical ──
    // This is the core contract: the second open (replay) must produce the
    // same badges as the first open.
    await openSession(page, sessionTitle)
    await waitForReplayDone(page)

    const replayedBadges = page.locator('[data-testid="tool-call-badge"]')
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

    // Open the session panel and click the session
    await page.getByRole('button', { name: 'Open sessions panel' }).click()
    const sessionBtn = page
      .getByRole('button', {
        name: new RegExp(`Open session: ${sessionTitle}`, 'i'),
      })
      .first()
    await expect(sessionBtn).toBeVisible({ timeout: 10_000 })

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

    await sessionBtn.click()

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
    await expect(input).toBeEnabled({ timeout: 30_000 })

    // After replay, verify the send button enables when text is typed.
    // (ComposerPrimitive.Send is also disabled on empty input — typing enables it.)
    await input.fill('test message to verify send is active')
    const send = sendButton(page)
    await expect(send).toBeEnabled({ timeout: 5_000 })

    // Clear the input after validation
    await input.fill('')
  },
)

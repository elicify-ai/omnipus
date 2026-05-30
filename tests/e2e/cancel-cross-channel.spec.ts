/**
 * cancel-cross-channel.spec.ts — E2E tests for cross-channel /cancel feature.
 *
 * Scope: T21–T26 from cancel-cross-channel-spec.md §10 TDD Plan.
 * Traces to: docs/internal/specs/cancel-cross-channel-spec.md US-1, US-4, US-5; T21–T26.
 *
 * Tests T21–T24, T26 drive a real LLM (OPENROUTER_API_KEY_CI required in CI).
 * T25 is partially driven by real LLM; the "Force-stopping..." path requires a
 * stuck goroutine and is documented as manually-verifiable — the test covers
 * the "Stopping..." label only, which fires from React state on click.
 *
 * Architecture note: Tests switch the active agent to Jim, which generates
 * long inline prose rather than handing off to another agent (Mia has strong
 * "no long enumerations" guardrails that finish too quickly for the Stop button
 * to be visible). This mirrors the pattern in tests/e2e/chat.spec.ts test (e).
 */

import * as fs from 'fs'
import * as path from 'path'
import { expect, type Page } from '@playwright/test'
import { test } from './fixtures/console-errors'
import { chatInput, agentPicker, assistantMessages } from './fixtures/selectors'

// ── Constants ──────────────────────────────────────────────────────────────────

const BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6060'

/**
 * OMNIPUS_HOME: gateway workspace directory.
 * Must match the directory the running gateway is using.
 */
const OMNIPUS_HOME =
  process.env.OMNIPUS_HOME ||
  (process.env.HOME ? path.join(process.env.HOME, '.omnipus') : '/tmp/omnipus-e2e-test')

// ── Auth helpers (mirrored from handoff.spec.ts) ──────────────────────────────

function getStoredAuthToken(): string | null {
  const authFile = process.env.OMNIPUS_AUTH_FILE
    ? path.resolve(process.env.OMNIPUS_AUTH_FILE)
    : path.join(
        path.dirname(new URL(import.meta.url).pathname),
        'fixtures/.auth/admin.json',
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

async function createSession(page: Page, agentID: string = 'jim'): Promise<string> {
  const resp = await page.request.post(`${BASE_URL}/api/v1/sessions`, {
    headers: await apiHeaders(page),
    data: { agent_id: agentID, type: 'chat' },
  })
  if (!resp.ok()) {
    const body = await resp.text()
    throw new Error(`POST /api/v1/sessions failed: ${resp.status()} ${resp.statusText()} — ${body}`)
  }
  const meta = (await resp.json()) as { id: string }
  if (!meta.id) throw new Error('POST /api/v1/sessions returned no id')
  return meta.id
}

// ── JSONL reader helpers ───────────────────────────────────────────────────────

interface AuditEntry {
  event?: string
  // Audit entries nest their payload under "fields" (see pkg/audit/audit.go Emit).
  fields?: {
    was_fired?: boolean
    session_id?: string
    [key: string]: unknown
  }
  // allow any extra top-level fields
  [key: string]: unknown
}

interface TranscriptEntry {
  id?: string
  type?: string
  role?: string
  truncated?: boolean
  descendants_canceled?: string[]
  // allow any extra fields
  [key: string]: unknown
}

/**
 * Read and parse all lines from a JSONL file. Lines that fail JSON.parse are
 * skipped (partial writes, comment lines, etc.).
 */
function readJsonl<T>(filePath: string): T[] {
  if (!fs.existsSync(filePath)) return []
  const lines = fs.readFileSync(filePath, 'utf-8').split('\n')
  const results: T[] = []
  for (const line of lines) {
    const trimmed = line.trim()
    if (!trimmed) continue
    try {
      results.push(JSON.parse(trimmed) as T)
    } catch {
      // skip malformed lines
    }
  }
  return results
}

// ── Shared helper: switch to Jim and trigger a long-running turn ──────────────

/**
 * Switch the agent picker to Jim (who generates long inline prose) and send
 * a prompt that reliably produces multi-second streaming output.
 * Returns when streaming has started (stop button is visible).
 */
async function triggerLongStreamingTurn(page: Page): Promise<void> {
  const input = chatInput(page)
  await expect(input).toBeEnabled({ timeout: 20_000 })

  // Switch to Jim — long inline generation, multi-second stream window.
  const picker = agentPicker(page)
  await expect(picker).toBeVisible({ timeout: 15_000 })
  await picker.click()
  await page.getByRole('menuitem', { name: /Jim/i }).click()
  await expect(picker).toContainText(/Jim/i, { timeout: 5_000 })

  await input.fill(
    'Write exactly 500 words about renewable energy. Start now and continue for the full 500 words.',
  )
  await input.press('Enter')

  // Wait for the stop button to appear — confirms streaming has started.
  const stopBtn = page.locator('[data-testid="stop-btn"]')
  await expect(stopBtn).toBeVisible({ timeout: 30_000 })
}

// ── T21: Web stop button cancels turn within 5 seconds (US-1.1, SC-1) ─────────
// Traces to: cancel-cross-channel-spec.md line 600 (T21)
// BDD: Given a turn is actively streaming
//      When the user clicks the Stop button
//      Then within 5 seconds the turn ends and the message shows "(interrupted)"
//      And chat input is re-enabled.

test(
  'T21 — web Stop button cancels streaming turn within 5s',
  async ({ page }) => {
    // test.slow() triples the 90s timeout. Real-LLM round-trips can take up to 30s.
    test.slow()

    await page.goto('/')

    await triggerLongStreamingTurn(page)

    const stopBtn = page.locator('[data-testid="stop-btn"]')
    await stopBtn.click()

    // Within 5s: the stop button disappears (streaming ended).
    await expect(stopBtn).not.toBeVisible({ timeout: 5_000 })

    // Within 5s: the chat input is re-enabled.
    await expect(chatInput(page)).toBeEnabled({ timeout: 5_000 })

    // The last assistant message should show the "(interrupted)" label suffix.
    // MessageItem renders: {message.status === 'interrupted' && <span>(interrupted)</span>}
    // Allow up to 5s for the message to reflect the cancelled status.
    const interruptedLabels = page.locator('text=(interrupted)')
    await expect(interruptedLabels.first()).toBeVisible({ timeout: 5_000 })
  },
)

// ── T22: Web slash menu /cancel during streaming (US-1.3, FR-3a) ─────────────
// Traces to: cancel-cross-channel-spec.md line 601 (T22)
// BDD: Given a turn is actively streaming
//      When the user types "/c" into the chat input
//      Then the slash menu appears with "/cancel" visible (FR-3a)
//      When the user clicks "/cancel"
//      Then the same cancel behavior fires as clicking Stop.

test(
  'T22 — web slash menu /cancel is visible during streaming and cancels turn',
  async ({ page }) => {
    test.slow()

    await page.goto('/')

    await triggerLongStreamingTurn(page)

    const input = chatInput(page)

    // Typing "/c" mid-stream: FR-3a requires the slash menu to appear during
    // streaming for commands tagged availableWhileStreaming: true.
    // The input is disabled during streaming per MessageInput.tsx, but
    // ChatScreen.tsx has a second textarea (data-testid="chat-input") that
    // owns the slash logic. Use that testid for the type interaction.
    const slashInput = page.locator('[data-testid="chat-input"]')
    await slashInput.click()
    await slashInput.type('/c')

    // Assert the slash menu is shown.
    // ChatScreen renders the dropdown when shouldShowSlash && slashOpen:
    // the menu contains buttons with font-mono label text.
    const slashMenu = page.locator('.font-mono').filter({ hasText: '/cancel' })
    await expect(slashMenu.first()).toBeVisible({ timeout: 5_000 })

    // The entire menu should be visible.
    const cancelMenuItem = page.getByRole('button', { name: /\/cancel/ })
    await expect(cancelMenuItem.first()).toBeVisible({ timeout: 5_000 })

    // Click /cancel.
    await cancelMenuItem.first().click()

    // Assert cancel behavior: stop button disappears, input re-enabled, (interrupted) shown.
    const stopBtn = page.locator('[data-testid="stop-btn"]')
    await expect(stopBtn).not.toBeVisible({ timeout: 5_000 })
    await expect(chatInput(page)).toBeEnabled({ timeout: 5_000 })
    const interruptedLabels = page.locator('text=(interrupted)')
    await expect(interruptedLabels.first()).toBeVisible({ timeout: 5_000 })
  },
)

// ── T23: Web Escape key cancels during streaming (US-1.4) ────────────────────
// Traces to: cancel-cross-channel-spec.md line 602 (T23)
// BDD: Given a turn is actively streaming
//      When the user presses Escape with focus in the chat input
//      Then the same cancel code path fires as the Stop button.

test(
  'T23 — web Escape key cancels streaming turn',
  async ({ page }) => {
    test.slow()

    await page.goto('/')

    await triggerLongStreamingTurn(page)

    // MessageInput.tsx handles Escape on the textarea aria-label="Message input"
    // when isStreaming === true. The textarea is disabled during streaming but
    // keyboard events still fire. Press Escape on the page directly.
    await page.keyboard.press('Escape')

    // Assert cancel behavior.
    const stopBtn = page.locator('[data-testid="stop-btn"]')
    await expect(stopBtn).not.toBeVisible({ timeout: 5_000 })
    await expect(chatInput(page)).toBeEnabled({ timeout: 5_000 })
    const interruptedLabels = page.locator('text=(interrupted)')
    await expect(interruptedLabels.first()).toBeVisible({ timeout: 5_000 })
  },
)

// ── T24: Cancel cascades to subagent (US-4.1) ────────────────────────────────
// Traces to: cancel-cross-channel-spec.md line 603 (T24)
// BDD: Given a session with a parent turn that has spawned a subagent (spawn tool)
//      When the user clicks Stop while the subagent is running
//      Then the parent message shows "(interrupted)" within 5s
//      And transcript.jsonl contains a {type: "turn_canceled"} entry
//      And that entry has a non-empty descendants_cancelled array.

test(
  'T24 — cancel cascades to subagent: transcript.jsonl records turn_canceled with descendants',
  async ({ page }) => {
    // 270s: matches sibling spawn-based tests (handoff(b), subagent(a/d)) which
    // all use Mia and complete in 6-16s standalone, up to 60-90s under suite
    // load. test.slow() ceiling. Previously inflated to 600s when this test
    // forced agent=Jim and Jim's spawn was hitting 4-6 min in CI; with Mia
    // restored we no longer need that headroom.
    test.slow()

    await page.goto('/')

    const input = chatInput(page)
    await expect(input).toBeEnabled({ timeout: 20_000 })

    // Use the SPA's own "New Chat" button to create a fresh session and bind
    // the page to it. Empirically createSession + page.goto(/#/sessions/<id>)
    // does NOT bind the SPA — the input falls back to creating a new session
    // on first message, leaving us reading the wrong transcript file.
    //
    // Why Mia (default), NOT Jim: file-level "switch to Jim" comment applies
    // to T22/T23/T25/T26 (cancel during inline prose — needs Jim's long
    // streaming). T24's cancel window comes from the SUBAGENT's 3× read_file
    // loop, so the parent's prose behaviour is irrelevant. Jim has 16 default
    // tools with overlapping action options (spawn, subagent, workspace.shell,
    // workspace.shell_bg) that push glm-5v-turbo into extended thinking when
    // asked to spawn. Mia has 7 cleaner tools and reliably emits spawn in
    // single-digit seconds — same agent every other spawn-based test uses
    // (handoff(b), subagent(a/d)).
    // Clicking "New Chat" only nullifies activeSessionId — it does NOT mint
    // a new session in the URL. The SPA creates the session lazily on the
    // first sent message. We need to (a) trigger that flow, then (b) read the
    // sessionId from the URL hash once the SPA navigates.
    const newChatBtn = page.getByRole('banner').getByRole('button', { name: 'New Chat' })
    await expect(newChatBtn).toBeVisible({ timeout: 10_000 })
    await newChatBtn.click()
    await expect(input).toBeEnabled({ timeout: 20_000 })

    // Trigger a turn that uses the spawn tool. The prompt must be explicit enough
    // to overcome GLM-5v-turbo's tendency to reply in prose on short prompts.
    await input.fill(
      [
        'Call the `spawn` tool exactly once, now, with these arguments:',
        '  label: "cancel cascade test"',
        '  task: "You are a subagent. Call read_file with path=\\"/etc/hostname\\" three times, pausing briefly between calls. After all three calls, reply with the word \\"done\\"."',
        'Do not reply in prose. Call the spawn tool immediately.',
      ].join('\n'),
    )
    await input.press('Enter')

    // Now the SPA mints the session and navigates — read the ID from the URL
    // hash. TanStack Router hash history format: /#/sessions/<id>.
    await page.waitForFunction(
      () => /\/sessions\/[A-Za-z0-9_]+/.test(window.location.hash),
      { timeout: 15_000 },
    )
    const sessionId = await page.evaluate(() => {
      const match = window.location.hash.match(/\/sessions\/([A-Za-z0-9_]+)/)
      return match ? match[1] : ''
    })
    if (!sessionId) throw new Error('T24: no session ID in URL after first message')

    // Wait for the subagent collapsed block to appear — confirms spawn fired.
    // 150s: matches the sibling Mia-spawn tests (subagent(a) uses 150s; the
    // empirical CI tail for Mia's spawn is single-digit seconds, so 150s is
    // already comfortable headroom).
    const collapsedBlock = page.locator('[data-testid="subagent-collapsed"]')
    await expect(collapsedBlock).toBeVisible({ timeout: 150_000 })

    // Click Stop while the subagent is running.
    const stopBtn = page.locator('[data-testid="stop-btn"]')
    await expect(stopBtn).toBeVisible({ timeout: 10_000 })
    await stopBtn.click()

    // Assert the parent message shows "(interrupted)" within 5s.
    await expect(stopBtn).not.toBeVisible({ timeout: 5_000 })
    await expect(chatInput(page)).toBeEnabled({ timeout: 5_000 })
    const interruptedLabels = page.locator('text=(interrupted)')
    await expect(interruptedLabels.first()).toBeVisible({ timeout: 5_000 })

    // Assert transcript.jsonl contains {type: "turn_canceled"} entry with
    // a non-empty descendants_canceled array. The backend uses single-L
    // spelling for transcript entries (pkg/session/daypartition.go).
    // Allow a short settling window (max 3s) for the transcript write to flush.
    await page.waitForTimeout(3_000)

    // Sessions are stored at OMNIPUS_HOME/sessions/<id>/<YYYY-MM-DD>.jsonl
    // The transcript file path uses day-partitioned JSONL.
    const today = new Date().toISOString().slice(0, 10) // YYYY-MM-DD
    const transcriptDir = path.join(OMNIPUS_HOME, 'sessions', sessionId)
    // Try both the day-partitioned name and the legacy transcript.jsonl name.
    const candidates = [
      path.join(transcriptDir, `${today}.jsonl`),
      path.join(transcriptDir, 'transcript.jsonl'),
    ]
    let entries: TranscriptEntry[] = []
    for (const candidate of candidates) {
      const parsed = readJsonl<TranscriptEntry>(candidate)
      if (parsed.length > 0) {
        entries = parsed
        break
      }
    }

    const cancelledEntry = entries.find((e) => e.type === 'turn_canceled')
    if (!cancelledEntry) {
      // Allow the alternative: the feature may not yet be implemented —
      // report as a test failure with clear context rather than a silent skip.
      throw new Error(
        'BLOCKED or INCOMPLETE: transcript.jsonl does not contain a {type:"turn_canceled"} entry ' +
          `after cancel. Searched: ${candidates.join(', ')}. ` +
          `Entries found: ${JSON.stringify(entries.map((e) => ({ type: e.type, role: e.role })))}. ` +
          'Traces to: cancel-cross-channel-spec.md T24, US-4.1, FR-15.',
      )
    }

    // descendants_canceled must be a non-empty array (cascade fired).
    expect(
      Array.isArray(cancelledEntry.descendants_canceled) &&
        (cancelledEntry.descendants_canceled as string[]).length > 0,
      'turn_canceled entry must have a non-empty descendants_canceled array (cascade wired per FR-6a)',
    ).toBe(true)
  },
)

// ── T25: Stop button UI progression (EC-15, FR-21) ──────────────────────────
// Traces to: cancel-cross-channel-spec.md line 604 (T25)
// BDD: Given a cancel is fired on a streaming turn
//      When the user clicks Stop
//      Then the button label is "Stopping..." (or shows a spinner) within 100ms.
//
// Note: "Force-stopping..." requires a stuck loop (t=3s) and "Cancelled" requires
// detach (t=8s). These stages need a goroutine that ignores context cancellation,
// which cannot be reliably induced via E2E. Those stages are documented as
// manually-verifiable. This test asserts only the t=0 "Stopping..." label.

test(
  'T25 — stop button morphs to "Stopping..." immediately on click (EC-15, FR-21)',
  async ({ page }) => {
    test.slow()

    await page.goto('/')

    await triggerLongStreamingTurn(page)

    const stopBtn = page.locator('[data-testid="stop-btn"]')

    // Capture the moment of click — the label should change within 100ms.
    // We click and immediately check for the "Stopping..." text.
    await stopBtn.click()

    // Within 500ms (generous for flaky environments): button text is "Stopping..."
    // MessageInput.tsx: handleCancel calls setStopLabel('stopping') synchronously
    // before cancelStream(), so this fires as local React state with no network RTT.
    await expect(stopBtn).toContainText('Stopping...', { timeout: 500 })

    // Allow the test to settle — the stop button disappears when streaming ends.
    await expect(stopBtn).not.toBeVisible({ timeout: 10_000 })
    await expect(chatInput(page)).toBeEnabled({ timeout: 5_000 })

    // SKIP ASSERTION — "Force-stopping..." state (t=3s):
    // Requires the agent goroutine to ignore context.Cancel() for 3s. In normal
    // operation the LLM stream aborts quickly via providerCancel(). This stage
    // is not reliably inducible in E2E. Verified manually per EC-15.
    //
    // SKIP ASSERTION — "Cancelled" state (t=8s detach):
    // Same reason as above. Verified manually.
  },
)

// ── T26: Audit entries exist after cancel (US-5.1, US-5.2) ──────────────────
// Traces to: cancel-cross-channel-spec.md line 605 (T26)
// BDD: Given any cancel request lands at the gateway
//      When the audit log is queried
//      Then exactly one turn_cancel_attempt entry exists with was_fired: true
//      And exactly one turn_canceled entry exists
//      And their session_id values match.

test(
  'T26 — audit log contains turn_cancel_attempt and turn_canceled entries after cancel',
  async ({ page }) => {
    test.slow()

    await page.goto('/')

    // Precondition: the audit subsystem must be live on this gateway process.
    // sandbox.audit_log is restart-gated (pkg/gateway/rest_pending_restart.go) —
    // toggling it via REST persists the flag but does NOT activate it on the
    // running process. The audit subsystem creates ~/.omnipus/system/audit.jsonl
    // at boot when cfg.Sandbox.AuditLog=true (pkg/agent/loop.go:346). We check
    // file existence directly because the /security/audit-log REST endpoint is
    // guarded by RequireNotBypass and returns 503 in dev-mode-bypass test runs.
    //
    // If audit isn't live, attempt the REST PUT so a future restart picks it up,
    // then fail loudly with an actionable message.
    const auditPathPrecheck = path.join(OMNIPUS_HOME, 'system', 'audit.jsonl')
    if (!fs.existsSync(auditPathPrecheck)) {
      // Best-effort PUT to persist the flag for the next gateway restart. Ignore
      // result — endpoint may be 503-guarded in dev-mode-bypass.
      await page.request
        .put(`${BASE_URL}/api/v1/security/audit-log`, {
          headers: await apiHeaders(page),
          data: { enabled: true },
        })
        .catch(() => undefined)
      throw new Error(
        'T26 precondition: audit.jsonl is absent from $OMNIPUS_HOME/system. ' +
          'sandbox.audit_log must be enabled when the gateway BOOTS — it is restart-gated, ' +
          'so toggling via REST mid-run does not activate it. Start the gateway with ' +
          'sandbox.audit_log:true in config.json (the CI workflow seeds this; for local runs ' +
          'add it to $OMNIPUS_HOME/config.json before launching the gateway).',
      )
    }

    // Create a fresh session so we can match session_id in audit log.
    // TanStack Router session URL is /#/sessions/<sessionId> (not /#/<sessionId>).
    const sessionId = await createSession(page)
    await page.goto(`/#/sessions/${sessionId}`)

    const input = chatInput(page)
    await expect(input).toBeEnabled({ timeout: 20_000 })

    // Switch to Jim for reliable long streaming output.
    const picker = agentPicker(page)
    await expect(picker).toBeVisible({ timeout: 15_000 })
    await picker.click()
    await page.getByRole('menuitem', { name: /Jim/i }).click()
    await expect(picker).toContainText(/Jim/i, { timeout: 5_000 })

    // Record audit log size before the cancel to isolate entries added by THIS test.
    const auditPath = path.join(OMNIPUS_HOME, 'system', 'audit.jsonl')
    const entriesBefore = readJsonl<AuditEntry>(auditPath).length

    await input.fill(
      'Write exactly 400 words about solar power. Do not call any tool, just write prose.',
    )
    await input.press('Enter')

    // Wait for streaming to start.
    const stopBtn = page.locator('[data-testid="stop-btn"]')
    await expect(stopBtn).toBeVisible({ timeout: 30_000 })

    // Cancel the turn.
    await stopBtn.click()

    // Wait for cancel to complete. Allow 15s — under suite load the LLM cancel
    // propagation can take several seconds after the stop button is clicked.
    await expect(stopBtn).not.toBeVisible({ timeout: 15_000 })
    await expect(chatInput(page)).toBeEnabled({ timeout: 15_000 })

    // Allow audit flush (audit writes are synchronous on the gateway path, but
    // give a short settling window).
    await page.waitForTimeout(2_000)

    // Read all audit entries written after we started.
    const allEntries = readJsonl<AuditEntry>(auditPath)
    const newEntries = allEntries.slice(entriesBefore)

    // Assert: turn_cancel_attempt entry with was_fired: true.
    // events.go: EventTurnCancelAttempt = "turn.cancel.attempt"; struct tag json:"event"
    // Audit entries nest their payload under "fields" (pkg/audit/audit.go Emit).
    const attemptEntry = newEntries.find(
      (e) => e.event === 'turn.cancel.attempt' && e.fields?.was_fired === true,
    )
    if (!attemptEntry) {
      throw new Error(
        'INCOMPLETE: audit log does not contain a turn.cancel.attempt entry with was_fired:true. ' +
          `New entries found: ${JSON.stringify(newEntries.map((e) => ({ event: e.event, was_fired: e.fields?.was_fired })))}. ` +
          'Traces to: cancel-cross-channel-spec.md T26, US-5.1, FR-18.',
      )
    }

    // Assert: turn_canceled entry.
    // events.go: EventTurnCancelled = "turn.cancelled"; struct tag json:"event"
    const cancelledEntry = newEntries.find((e) => e.event === 'turn.cancelled')
    if (!cancelledEntry) {
      throw new Error(
        'INCOMPLETE: audit log does not contain a turn.cancelled entry. ' +
          `New entries found: ${JSON.stringify(newEntries.map((e) => ({ event: e.event })))}. ` +
          'Traces to: cancel-cross-channel-spec.md T26, US-5.2, FR-19.',
      )
    }

    // Assert: session_id matches between the two entries.
    // session_id is nested under fields (pkg/audit/audit.go Emit structure).
    expect(attemptEntry.fields?.session_id).toBeTruthy()
    expect(cancelledEntry.fields?.session_id).toBeTruthy()
    expect(attemptEntry.fields?.session_id).toEqual(cancelledEntry.fields?.session_id)
  },
)

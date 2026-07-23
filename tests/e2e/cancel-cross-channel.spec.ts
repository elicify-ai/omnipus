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
import { chatInput, agentPicker, assistantMessages, selectAgent } from './fixtures/selectors'

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

/**
 * List the session directory names under OMNIPUS_HOME/sessions. Used to discover
 * the session a turn created WITHOUT relying on the URL — the workspace-scoped
 * chat IA keeps the page at /#/workspaces/<id>/chat and never puts the session id
 * in the hash, so the old `/sessions/<id>` URL extraction no longer matches.
 */
function listSessionDirs(home: string): string[] {
  const dir = path.join(home, 'sessions')
  try {
    return fs.readdirSync(dir).filter((name: string) => {
      try {
        return fs.statSync(path.join(dir, name)).isDirectory()
      } catch {
        return false
      }
    })
  } catch {
    return []
  }
}

/** Best-effort mtime (ms) of a path; 0 if it cannot be stat'd. */
function safeMtimeMs(p: string): number {
  try {
    return fs.statSync(p).mtimeMs
  } catch {
    return 0
  }
}

// ── Shared helper: switch to Jim and trigger a long-running turn ──────────────

/**
 * Switch the agent picker to Jim (who generates long inline prose) and send
 * a prompt that reliably produces multi-second streaming output.
 * Returns once a message is ACTIVELY streaming (stop button visible AND the live
 * streaming-message anchor is present) — so a subsequent cancel always has an
 * incomplete message to mark interrupted.
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

  // Forbid tools and demand inline prose: on a bare "write 500 words" prompt,
  // gemini-2.5-flash intermittently shortcuts to the write_file TOOL (Jim has an
  // explicit "allow" policy entry for it), which ends the turn instantly with
  // zero inline stream and no cancellable window. Forcing long inline prose
  // keeps stop-btn live for several seconds so Stop/Escape/cancel land mid-stream.
  await input.fill(
    'Do not use any tools. Reply only with inline prose, no files. Write a detailed ' +
      '700-word essay about renewable energy, beginning immediately and continuing ' +
      'without stopping until you reach 700 words.',
  )
  await input.press('Enter')

  // Wait for the stop button (request in-flight)…
  const stopBtn = page.locator('[data-testid="stop-btn"]')
  await expect(stopBtn).toBeVisible({ timeout: 30_000 })

  // …AND for the assistant to have actually STREAMED real text. The stop button
  // (and even the streaming-message placeholder) appear BEFORE the first token; a
  // cancel fired in that gap aborts the turn with zero tokens and removes the empty
  // message, so nothing is ever marked "(interrupted)" (observed T23 flake: token
  // count 0, empty thread). Waiting until the live streaming message has accumulated
  // a substantial chunk of text (well beyond the short agent label) guarantees there
  // is a real, incomplete message to interrupt. The 700-word prompt ensures the
  // stream keeps going for seconds afterwards, so the cancel lands comfortably
  // mid-stream.
  await page.waitForFunction(
    () => {
      const el = document.querySelector('[data-testid="streaming-message-anchor"]')
      const text = (el?.textContent ?? '').replace(/\s+/g, ' ').trim()
      return text.length > 80
    },
    { timeout: 30_000 },
  )
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
    // Rows are `role="option"` (not the native implicit "button" role),
    // per the W3C APG combobox-with-listbox-popup pattern (ChatScreen.tsx —
    // the listbox container carries `role="listbox"`, rows carry
    // `role="option"` + `aria-selected`, and the textarea points at the
    // highlighted row via `aria-activedescendant`). This is a deliberate,
    // documented a11y redesign, not a regression — `getByRole('button', …)`
    // predates it and no longer matches anything. Matches the same
    // `getByRole('option', …)` convention already used for listbox rows
    // elsewhere in this suite (e.g. channel-routing.spec.ts,
    // channel-instance-crud.spec.ts).
    const cancelMenuItem = page.getByRole('option', { name: /\/cancel/ })
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

    // triggerLongStreamingTurn only returns once stop-btn is visible, i.e.
    // isStreaming is already true and ChatScreen's document-level Escape handler
    // is armed. Press Escape IMMEDIATELY — do NOT waitForTimeout first: with the
    // real model a turn can finish within a few hundred ms, and the prior
    // waitForTimeout(500)+2s re-assert raced that close and failed. Mirror the
    // immediate stop-action used by the passing T21/T22.
    const stopBtn = page.locator('[data-testid="stop-btn"]')
    await expect(stopBtn).toBeVisible({ timeout: 5_000 })

    // ChatScreen registers a document-level 'keydown' listener for Escape.
    await page.keyboard.press('Escape')

    // Assert cancel behavior.
    await expect(stopBtn).not.toBeVisible({ timeout: 5_000 })
    await expect(chatInput(page)).toBeEnabled({ timeout: 5_000 })

    // The interrupted marker (data-testid="interrupted-marker") is rendered per
    // message INSIDE the virtualized list, so for an off-screen/just-cancelled row
    // it is present in the DOM but visibility:hidden until the virtualizer scrolls
    // to it — asserting toBeVisible races that and flakes ("unexpected value
    // 'hidden'"). The functional cancel is already proven above (stop-btn gone +
    // input re-enabled); here we assert the message reached the interrupted STATE
    // by checking the marker is attached to the DOM (it only renders for messages
    // with status === 'interrupted').
    const interruptedMarker = page.locator('[data-testid="interrupted-marker"]')
    await expect(interruptedMarker.first()).toBeAttached({ timeout: 5_000 })
  },
)

// ── T24: Cancel cascades to subagent (US-4.1) ────────────────────────────────
// Traces to: cancel-cross-channel-spec.md line 603 (T24)
// BDD: Given a session with a parent turn that has delegated to a subagent
//      When the user clicks Stop while the subagent is running
//      Then the parent message shows "(interrupted)" within 5s
//      And transcript.jsonl contains a {type: "turn_canceled"} entry
//      And that entry has a non-empty descendants_canceled array.
//
// Both delegation modes are covered — both go through the single, unified
// `delegate` tool (ADR-036), differentiated only by its `async` argument:
//   T24a — `delegate` async=true  (background): the descendant streams in the
//                                   background while the parent turn stays live.
//   T24b — `delegate` async=false (await):      the parent turn BLOCKS on the
//                                   descendant's run until it returns (or is
//                                   cancelled).
// Both route through spawnSubTurn (pkg/agent/subturn.go:618), which registers the
// child in activeTurnStates — so RequestCancel → InterruptSession cascades to the
// descendant in BOTH cases (the Go-level proof is TestCancel_SubAgentCascade).
// This pair is the e2e proof that the cascade holds for background AND await.

/**
 * Drive the cancel-cascade scenario for one delegation mode and assert the
 * transcript records turn_canceled with a non-empty descendants_canceled.
 *
 * `mode.asyncArg` selects the delegation mode via the `delegate` tool's
 * `async` argument (`"true"` background / `"false"` await); `mode.closer` is
 * the final instruction line that nudges glm-5.2 to emit exactly that tool
 * call and nothing else.
 */
async function assertCancelCascadesToSubagent(
  page: Page,
  mode: { asyncArg: 'true' | 'false'; closer: string },
) {
  await page.goto('/')

  const input = chatInput(page)
  await expect(input).toBeEnabled({ timeout: 20_000 })

  // Use the `/new` client-delivery slash command to create a fresh session
  // and bind the page to it. Empirically createSession + page.goto(/#/sessions/<id>)
  // does NOT bind the SPA — the input falls back to creating a new session
  // on first message, leaving us reading the wrong transcript file.
  //
  // The header "New Chat" button this test used to click was REMOVED from
  // the workspace top-bar banner (ChatControls.tsx: "New Chat was removed
  // from the header — three paths for one action was redundant (Hick's
  // Law). It lives where the user already is: the sidebar's per-workspace
  // 'New chat' row and the /new slash command.") — a deliberate, documented
  // product redesign, not a regression. `/new` drives the exact same
  // `startNewSession()` store action the old button called (see
  // useSlashMenu.ts's runClientCommand, name === 'new'), including the same
  // "only nullifies activeSessionId, does not mint a new session" contract
  // the comment below still describes — so this swap is behavior-preserving.
  //
  // Route to Jim (Planner & Orchestrator), NOT the default agent Mia. CI
  // investigation (run 27296266639) found Mia's "guide" persona makes the model
  // REFUSE to delegate ("My role is to explain… not to delegate to subagents"), so the
  // parent never emits a delegation frame and the subagent-collapsed block never
  // appears. Jim delegates on the explicit prompt below. The cancel window comes
  // from the SUBAGENT running long enough (it streams a multi-hundred-word inline
  // essay), so the parent agent's prose behaviour is irrelevant — only that it
  // delegates.
  // `/new` only nullifies activeSessionId — it does NOT mint a new
  // session. The SPA creates the session lazily on the first sent message. The
  // workspace-scoped chat IA keeps the page at /#/workspaces/<id>/chat (no
  // session id in the URL), so we discover the new session by diffing the
  // OMNIPUS_HOME/sessions directory before vs. after the turn (below).
  await input.fill('/new')
  await input.press('Enter')
  await expect(input).toBeEnabled({ timeout: 20_000 })

  // Switch to Jim so the parent turn will actually delegate.
  await selectAgent(page, /Jim/i)

  // Snapshot existing session dirs so we can identify the one THIS turn creates
  // (earlier tests in the same gateway leave their own session dirs behind).
  const sessionsBefore = new Set(listSessionDirs(OMNIPUS_HOME))

  // The subagent task must keep the descendant RUNNING for several seconds so a
  // Stop click lands while it's live. A long inline essay streams for several
  // seconds; an instant-rejected task (e.g. a sandbox-escaping read) finishes in
  // ~0s before Stop can fire. Explicit single-tool instruction with a hard "no
  // prose" guardrail so glm-5.2 reliably emits the delegation call.
  await input.fill(
    [
      'Call the `delegate` tool exactly once, now, with these arguments:',
      '  label: "cancel cascade test"',
      '  task: "You are a subagent. Do not use any tools. Write a detailed 800-word essay about renewable energy as continuous inline prose, writing without stopping until you reach 800 words."',
      `  async: ${mode.asyncArg}`,
      mode.closer,
    ].join('\n'),
  )
  await input.press('Enter')

  // Wait for the Activity Bar pill to appear — confirms delegation fired
  // (and that the SPA has minted the session on disk).
  //
  // Switched from [data-testid="subagent-collapsed"] (2026-07-16): that
  // testid is the chat-thread SubagentBlock card, which commit 8e1bf1b9 made
  // verbose-only (shouldRenderSubagentSpan gates on verboseChatEnabled,
  // default false — src/store/chatPreferences.ts). This test's "delegation
  // started" gate has nothing to do with thread display policy — it only
  // needs to know a subagent span exists and is running — so
  // [data-testid="activity-bar"] (ActivityBar.tsx) is the better, POLICY-
  // INDEPENDENT signal: useRunningActivity() (src/hooks/useRunningActivity.ts)
  // reads every message's spans unconditionally (no shouldRenderSubagentSpan
  // filter anywhere in that hook or in ActivityBar.tsx) and the pill mounts
  // the instant runningCount > 0. It renders as soon as the child span
  // exists with status "running" — same underlying data source and same
  // "start of the descendant's run" timing the old collapsed-block wait
  // relied on, just without the verbose-chat dependency. 150s headroom
  // matches the sibling delegation tests; the empirical CI tail is
  // single-digit seconds.
  const activityBar = page.locator('[data-testid="activity-bar"]')
  await expect(activityBar).toBeVisible({ timeout: 150_000 })

  // Click Stop while the subagent is running.
  const stopBtn = page.locator('[data-testid="stop-btn"]')
  await expect(stopBtn).toBeVisible({ timeout: 10_000 })
  await stopBtn.click()

  // Assert the parent message shows "(interrupted)". Budget: RequestCancel's
  // own escalation ladder (pkg/agent/cancel.go) is graceful-immediate → 3s
  // hard-abort → +5s detach, so a hard-abort-bound cancel can legitimately
  // take up to ~3s server-side before the parent turn actually ends, plus the
  // MIN_STOPPING_DISPLAY_MS (1s, useCancelState.ts) the Stop button enforces
  // before it's even eligible to reset, plus WS/render round-trip. A 5s
  // window leaves ~1s of margin over that ~4s floor — too tight under
  // parallel-shard CI load (the same class of flake already fixed for
  // TestConcurrentSessions_TwoSessions/FiveSessions in this same wave, whose
  // sibling doc note says "the prior budget flaked under the parallel
  // matrix"). Widened to 10s for headroom; the sync cancel path itself was
  // verified to unblock in ~3s via a dedicated Go repro
  // (TestRepro_SyncDelegateCancel_RequestCancel, pkg/agent).
  await expect(stopBtn).not.toBeVisible({ timeout: 10_000 })
  await expect(chatInput(page)).toBeEnabled({ timeout: 10_000 })
  const interruptedLabels = page.locator('text=(interrupted)')
  await expect(interruptedLabels.first()).toBeVisible({ timeout: 10_000 })

  // Assert transcript.jsonl contains {type: "turn_canceled"} entry with a
  // non-empty descendants_canceled array. Allow a short settling window (max 3s)
  // for the transcript write to flush.
  await page.waitForTimeout(3_000)

  // Discover this turn's session by diffing the sessions dir. A delegated
  // sub-turn can spawn its own ephemeral session dir, so there may be more than
  // one new dir — scan them (newest first) for the one whose transcript actually
  // recorded the cancel (the turn_canceled entry lives in the PARENT session).
  // Sessions are stored at OMNIPUS_HOME/sessions/<id>/<YYYY-MM-DD>.jsonl
  // (day-partitioned JSONL); the legacy transcript.jsonl name is also tried.
  const today = new Date().toISOString().slice(0, 10) // YYYY-MM-DD
  const sessionsDir = path.join(OMNIPUS_HOME, 'sessions')
  const sessionsAfter = listSessionDirs(OMNIPUS_HOME)
  const newSessions = sessionsAfter.filter((s) => !sessionsBefore.has(s))
  // Prefer newly-created dirs; fall back to all sessions if the diff is empty
  // (e.g. the session dir already existed). Newest mtime first.
  const scanList = (newSessions.length > 0 ? newSessions : sessionsAfter)
    .map((s) => ({ s, m: safeMtimeMs(path.join(sessionsDir, s)) }))
    .sort((a, b) => b.m - a.m)
    .map((x) => x.s)

  let entries: TranscriptEntry[] = []
  let chosenSession = ''
  for (const sid of scanList) {
    const dir = path.join(sessionsDir, sid)
    const files = [path.join(dir, `${today}.jsonl`), path.join(dir, 'transcript.jsonl')]
    let parsed: TranscriptEntry[] = []
    for (const f of files) {
      const p = readJsonl<TranscriptEntry>(f)
      if (p.length > 0) {
        parsed = p
        break
      }
    }
    if (parsed.some((e) => e.type === 'turn_canceled')) {
      entries = parsed
      chosenSession = sid
      break
    }
    // Keep the first non-empty transcript as a fallback for the error message.
    if (entries.length === 0 && parsed.length > 0) {
      entries = parsed
      chosenSession = sid
    }
  }

  const cancelledEntry = entries.find((e) => e.type === 'turn_canceled')
  if (!cancelledEntry) {
    throw new Error(
      'BLOCKED or INCOMPLETE: no session transcript contains a {type:"turn_canceled"} entry ' +
        `after cancel (mode=delegate async=${mode.asyncArg}). Scanned sessions: ${JSON.stringify(scanList)} ` +
        `(new: ${JSON.stringify(newSessions)}). Chosen: ${chosenSession || '(none)'}. ` +
        `Entries found in chosen: ${JSON.stringify(entries.map((e) => ({ type: e.type, role: e.role })))}. ` +
        'Traces to: cancel-cross-channel-spec.md T24, US-4.1, FR-15.',
    )
  }

  // descendants_canceled must be a non-empty array (cascade fired).
  expect(
    Array.isArray(cancelledEntry.descendants_canceled) &&
      (cancelledEntry.descendants_canceled as string[]).length > 0,
    `turn_canceled entry must have a non-empty descendants_canceled array (cascade wired per FR-6a, mode=delegate async=${mode.asyncArg})`,
  ).toBe(true)
}

test(
  'T24a — cancel cascades to background subagent (delegate async=true): transcript records turn_canceled with descendants',
  async ({ page }) => {
    // glm-5.2 (the standard e2e model) is reliable but slower than the old gemini
    // pick — the delegation turn + the subagent's inline essay + cancel can exceed
    // the 270s test.slow() ceiling under suite load. Use an explicit higher budget.
    test.setTimeout(360_000)
    await assertCancelCascadesToSubagent(page, {
      asyncArg: 'true',
      closer: 'Do not reply in prose. Call the delegate tool immediately.',
    })
  },
)

test(
  'T24b — cancel cascades to awaited subagent (delegate async=false): transcript records turn_canceled with descendants',
  async ({ page }) => {
    // Await mode blocks the parent turn on the descendant's full run, so under
    // glm-5.2's slower streaming this needs more headroom than the background variant.
    test.setTimeout(420_000)
    await assertCancelCascadesToSubagent(page, {
      asyncArg: 'false',
      closer: 'Do not reply in prose. Do not call any other tool. Call delegate now with async set to false.',
    })
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

    // Let the route settle before inspecting the DOM. This test visits '/' first
    // (above), which now redirects into the default workspace's Chat tab
    // (DefaultWorkspaceRedirect), mounting WorkspaceTabContainer/ChatControls — the
    // ONLY render site of the agent-picker-trigger banner. /#/sessions/<id> resolves
    // via TanStack Router's async route loader (fetchSessionDetail); src/main.tsx's
    // defaultPendingMs (150ms) only swaps in the pending skeleton after that delay,
    // so the router keeps the PREVIOUS route mounted for any loader fetch faster than
    // that threshold (this repro's ~70ms gap included). That leaves a short window,
    // right after this goto, where the old workspace route (WITH the picker, roster
    // incl. Jim) is still fully mounted and clickable.
    //
    // Live repro (trace timeline, ms from test start): goto() to /#/sessions/<id>
    // completes at t=3598; the loader's GET /api/v1/sessions/<id> starts at t=3671
    // and doesn't resolve until t=3741. An isVisible() snapshot taken in that gap
    // (as this code used to do immediately after goto) reads "true" for a picker
    // that's mid-teardown: picker.click() lands (t=3688-3809, spanning the loader's
    // resolution), transiently opening a portaled dropdown that unmounts with its
    // parent route a moment later — so the "Jim" menuitem search a few lines below
    // then hangs for the full 270s test timeout, having nothing left to find. This
    // reproduced deterministically (3/3 local runs) before this wait was added.
    //
    // waitForLoadState('networkidle') gives the loader's fetch (and the effect it
    // drives) time to resolve, so every check below reflects the FINAL settled
    // route rather than a transient one. An open WebSocket does not block networkidle
    // (see tests/e2e/idle-no-reconnect.spec.ts, which asserts exactly that).
    await page.waitForLoadState('networkidle')

    const input = chatInput(page)
    await expect(input).toBeEnabled({ timeout: 20_000 })

    // Best-effort agent switch to Jim for reliable long streaming output.
    //
    // /#/sessions/<id> is a legacy standalone route that renders ChatScreen
    // inline WITHOUT the workspace ChatControls top-bar (which lives in
    // WorkspaceTabContainer, mounted only on the / workspace view). The
    // agent-picker trigger (data-testid="agent-picker-trigger") is part of
    // ChatControls, so it is genuinely absent on this route once settled (see the
    // networkidle wait above). Additionally, createSession() above already defaults
    // to agentID:'jim', so Jim is already the active agent — the switch is redundant.
    // Attempt it only if the picker happens to be visible (e.g. the route is later
    // migrated to use WorkspaceTabContainer), otherwise proceed with the already-active Jim.
    const picker = agentPicker(page)
    const pickerVisible = await picker.isVisible().catch(() => false)
    if (pickerVisible) {
      await picker.click()
      await page.getByRole('menuitem', { name: /Jim/i }).click()
      await expect(picker).toContainText(/Jim/i, { timeout: 5_000 })
    } else {
      // Jim is already the active agent (session was created with agentID:'jim').
      // No picker on this legacy session route — proceed directly.
      console.log('T26: agent-picker not visible on /#/sessions route (expected — legacy route has no ChatControls top-bar); proceeding with active Jim agent')
    }

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

    // Wait for cancel to complete. Under suite load (llm-agents shard) the
    // OpenRouter cancel + gateway turn teardown can exceed 15s — the stop
    // button stays on aria-label="Stopping..." until the turn fully ends.
    // Match the T24a/T24b cascade budget (test.slow() already multiplies
    // the test timeout); give the button 60s to clear.
    await expect(stopBtn).not.toBeVisible({ timeout: 60_000 })
    await expect(chatInput(page)).toBeEnabled({ timeout: 30_000 })

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

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
import { chatInput, agentPicker, selectAgent, waitForConnected } from './fixtures/selectors'

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

// NOTE: a `createSession()` REST helper used to live here, used only by T26 to
// mint a session up-front and then `page.goto('/#/sessions/<id>')` into it. It
// was REMOVED (2026-07-28) because that binding does not work: the SPA ignores
// the pre-minted id and lazily mints its OWN session on first send, so the turn
// ran in a different session than the test believed (observed live: both cancel
// requests carried the literal `__pending` sentinel as session_id). The same
// defect is already documented in T24's helper below. Every test in this file
// now discovers the session the SPA actually used by diffing the sessions dir.

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
 * A prompt that reliably produces multi-second, tool-free streaming output.
 *
 * Forbidding tools and demanding inline prose: on a bare "write 500 words" prompt,
 * gemini-2.5-flash intermittently shortcuts to the write_file TOOL (Jim has an
 * explicit "allow" policy entry for it), which ends the turn instantly with
 * zero inline stream and no cancellable window. Forcing long inline prose
 * keeps stop-btn live for several seconds so Stop/Escape/cancel land mid-stream.
 * The explicit high word count ("700-word essay ... beginning immediately and
 * continuing without stopping") guarantees hundreds of words of remaining
 * generation budget once a cancel is fired — see waitForActiveStream's doc
 * comment for why that headroom is what makes cancel timing deterministic
 * under load, regardless of how slow the environment is.
 */
const LONG_PROSE_PROMPT =
  'Do not use any tools. Reply only with inline prose, no files. Write a detailed ' +
  '700-word essay about renewable energy, beginning immediately and continuing ' +
  'without stopping until you reach 700 words.'

/**
 * Wait until a turn is OBSERVABLY ACTIVE: the stop button is visible AND the
 * live streaming message has accumulated a substantial chunk of real text —
 * not just the pre-first-token placeholder.
 *
 * Why both conditions, and why "substantial text" rather than merely "stop
 * button rendered": the stop button (and even the streaming-message
 * placeholder) can appear BEFORE the first token arrives, and under a loaded
 * shard the WS event that drives the frontend's "isStreaming" state can lag
 * the true backend state — in either direction, by seconds. A cancel fired
 * the instant the stop button appears can therefore land in one of two bad
 * windows:
 *   (a) zero tokens produced yet — the turn aborts with an empty message and
 *       nothing is ever marked "(interrupted)" (observed T23 flake: token
 *       count 0, empty thread).
 *   (b) the backend turn has ALREADY FINISHED by the time the cancel request
 *       lands server-side (observed T26 flake under a loaded shard: audit log
 *       records turn.cancel.attempt with was_fired:false — "no turn was
 *       active" — the whole run just took ~10x longer than in isolation, so
 *       by the time Playwright's click reached the backend the short turn
 *       had already completed).
 * Requiring a substantial (>80 char) chunk of REAL accumulated text proves the
 * turn is genuinely mid-generation right now, and — combined with
 * LONG_PROSE_PROMPT's enforced word count — guarantees hundreds of words of
 * remaining output, so the turn cannot plausibly finish between this check
 * and the cancel click landing server-side, no matter how loaded the shard
 * is. This is a wait-on-condition (page.waitForFunction polls until true, no
 * fixed sleep), not a race against a timer.
 */
async function waitForActiveStream(page: Page): Promise<void> {
  // Wait for the stop button (request in-flight)…
  const stopBtn = page.locator('[data-testid="stop-btn"]')
  await expect(stopBtn).toBeVisible({ timeout: 30_000 })

  // …AND for the assistant to have actually STREAMED real text.
  await page.waitForFunction(
    () => {
      const el = document.querySelector('[data-testid="streaming-message-anchor"]')
      const text = (el?.textContent ?? '').replace(/\s+/g, ' ').trim()
      return text.length > 80
    },
    { timeout: 30_000 },
  )
}

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
  // toBeEnabled() alone no longer implies "connected" (2fa26e6a, #105 fix —
  // see waitForConnected's doc comment in fixtures/selectors.ts). Without
  // this, the long-streaming prompt below can land in the outbound queue
  // instead of the wire, and every T2x test built on this helper hangs
  // waiting for a stop-btn that never appears.
  await waitForConnected(page, { timeout: 20_000 })

  // Switch to Jim — long inline generation, multi-second stream window.
  const picker = agentPicker(page)
  await expect(picker).toBeVisible({ timeout: 15_000 })
  await picker.click()
  await page.getByRole('menuitem', { name: /Jim/i }).click()
  await expect(picker).toContainText(/Jim/i, { timeout: 5_000 })

  await input.fill(LONG_PROSE_PROMPT)
  await input.press('Enter')

  // Wait until the turn is observably active (see waitForActiveStream's doc
  // comment) before returning — a subsequent cancel always has a real,
  // in-flight turn with hundreds of words of runway left.
  await waitForActiveStream(page)
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
  // toBeEnabled() alone no longer implies "connected" (2fa26e6a, #105 fix —
  // see waitForConnected's doc comment in fixtures/selectors.ts).
  await waitForConnected(page, { timeout: 20_000 })

  // ── ORDER IS LOAD-BEARING: switch the agent FIRST, then `/new`. ───────────
  //
  // RC6 (2026-07-28) root-caused T24b failing 3/3 on the llm-agents shard to
  // the OPPOSITE order this helper used to have. The chain, from that run's
  // artefacts:
  //
  //   1. `expect(input).toBeEnabled()` is NOT a readiness gate for the
  //      composer. The textarea enables while the chat surface is still
  //      mounting — the trace's DOM snapshots show the agent picker rendering
  //      NO agent at all (t=3538/3678/3724ms) at the exact moment `/new` was
  //      submitted (t=3660ms), and `GET /api/v1/commands?surface=web` had only
  //      been ISSUED 236ms earlier (t=3424ms).
  //   2. `interceptClientCommand` (src/hooks/useSlashMenu.ts) resolves `/new`
  //      against `allCommands`, which is populated by that fetch. Empty list →
  //      `return false` → the text is sent to the BACKEND as an ordinary chat
  //      message.
  //   3. The backend honours it as the `clear` command (pkg/commands/cmd_clear.go
  //      replies "Chat history cleared!" — that exact string was in the failed
  //      run's DOM and transcript) and mints a session bound to the DEFAULT
  //      agent, Mia.
  //   4. `selectAgent(/Jim/i)` then genuinely succeeded — but the SPA's
  //      subsequent attach to that Mia-bound session called
  //      `setActiveSession(sessionId, 'mia', …)` and clobbered it straight
  //      back. Picker timeline from the trace: none → Mia → Jim → Mia.
  //   5. The delegate prompt therefore executed as MIA, whose seeded policy
  //      denies `delegate` (pkg/coreagent/core.go:793 — deny-by-default, and
  //      `delegate` is deliberately not in her allow-list; this is correct
  //      least-privilege, not a regression). Gateway log, 4×:
  //      `ToolSearch(load): no valid tools to load. Rejected: delegate —
  //      denied by this agent's policy`. No subagent ever ran, so the
  //      ActivityBar never mounted and the 150s wait below timed out.
  //
  // Selecting Jim BEFORE any session exists removes step 4 entirely: there is
  // no session to attach to, so nothing can clobber the selection. It is safe
  // with respect to `/new` because `runClientCommand('new')` calls
  // `startNewSession()` with NO arguments, and `startNewSession` preserves the
  // current agent (`activeAgentId: agentId ?? state.activeAgentId`,
  // src/store/session.ts) — the switch survives the reset.
  //
  // `selectAgent` also doubles as the real readiness gate the old
  // `toBeEnabled()` was standing in for: it waits for the picker to be
  // visible, opens the dropdown (which requires `GET /api/v1/agents` to have
  // resolved) and asserts the label changed. By the time it returns, the
  // composer is genuinely mounted.
  //
  // Route to Jim (Planner & Orchestrator), NOT the default agent Mia. An
  // earlier CI investigation (run 27296266639) found Mia's "guide" persona
  // makes the model REFUSE to delegate even when it CAN; RC6 showed she also
  // structurally cannot. Either way the parent never emits a delegation frame.
  // The cancel window comes from the SUBAGENT running long enough (it streams
  // a multi-hundred-word inline essay), so the parent agent's prose behaviour
  // is irrelevant — only that it delegates.
  await selectAgent(page, /Jim/i)

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
  // `/new` only nullifies activeSessionId — it does NOT mint a new
  // session. The SPA creates the session lazily on the first sent message. The
  // workspace-scoped chat IA keeps the page at /#/workspaces/<id>/chat (no
  // session id in the URL), so we discover the new session by diffing the
  // OMNIPUS_HOME/sessions directory before vs. after the turn (below).
  //
  // RC6: drive `/new` through the PALETTE, never `fill` + a blind `Enter`.
  // Typing the text and pressing Enter races the command-list fetch: if
  // `allCommands` is not yet populated, `interceptClientCommand` falls through
  // and the literal text "/new" is sent to the backend as chat (see the
  // ordering note above for the full chain). Selecting the row instead goes
  // through `executeSlashCommand`, which can only be reached once the command
  // actually exists in the palette — so the client-side path is guaranteed,
  // not merely likely. This is also the real user path.
  //
  // MUST be real keystrokes, not `fill()`. The palette opens from
  // `onInputChange` (useSlashMenu.ts: `if (val.startsWith('/')) setSlashOpen(true)`),
  // which is driven by the composer's own change handler. `fill()` sets the
  // value in one shot without driving that handler, so the menu never opens —
  // verified on ci-omnipus-2 at a1d77d58, where a `fill()` version of this
  // block timed out waiting for the row (31.8s ≈ the 30s gate). T22 above is
  // the in-repo precedent for the working form: `click()` then type.
  await input.click()
  await input.pressSequentially('/new')

  // Palette rows are role="option" buttons whose accessible name starts with
  // the command label (ChatScreen.tsx renders `item.label` then
  // `item.description`). Anchored so it can never match a future "/newfoo".
  // Scoped to the page rather than to [data-testid="slash-menu"], matching
  // T22's `page.getByRole('option', …)` convention.
  const newSessionCommand = page.getByRole('option', { name: /^\/new\b/ }).first()
  await expect(newSessionCommand).toBeVisible({ timeout: 30_000 })
  await newSessionCommand.click()

  // Proof the CLIENT path ran: `executeSlashCommand` clears the composer via
  // `composerRuntime.setText('')` before invoking `runClientCommand`. We never
  // pressed Enter, so an empty composer can only mean the palette handled it —
  // if `/new` had escaped to the backend this would still hold text.
  await expect(input).toHaveValue('', { timeout: 10_000 })
  await expect(input).toBeEnabled({ timeout: 20_000 })

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

  // RC6 GUARD — re-assert the routed agent at the LAST possible moment before
  // the send. `selectAgent` already asserted this above, but in the failure
  // this guard was written for the picker silently reverted to Mia AFTERWARDS
  // (a session attach calling `setActiveSession(id, 'mia', …)` clobbered it),
  // and the only symptom was the ActivityBar wait below dying 150s later with
  // a bare "element(s) not found". That error names neither the agent nor the
  // cause. Checking here turns the same defect into an immediate, truthful
  // failure that says which agent the turn was about to run as. Cheap: this is
  // a rendered-label read with no network round-trip.
  await expect(
    agentPicker(page),
    'the routed agent must still be Jim at send time — Mia is denied `delegate` ' +
      'by her seeded policy (pkg/coreagent/core.go), so a silent revert to her ' +
      'makes the delegation impossible rather than merely slow (RC6)',
  ).toContainText(/Jim/i, { timeout: 10_000 })

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
  // matrix"). Widened to 10s for headroom.
  //
  // CORRECTION (2026-07-28): this comment used to claim the sync cancel path
  // "was verified to unblock in ~3s via a dedicated Go repro
  // (TestRepro_SyncDelegateCancel_RequestCancel, pkg/agent)". That citation is
  // wrong and the margin it implies does not exist. The cited test asserts a
  // FIFTEEN second bound, not three. The real server-side ladder is 3s
  // hard-abort + 5s detach = 8s, plus the SPA's own 1s MIN_STOPPING_DISPLAY_MS
  // before the button may disappear — so ~9s against this 10s budget, i.e.
  // roughly 1s of margin, not the ~7s the old note implied.
  //
  // Left at 10s deliberately: it passes 6/6 including under load, and raising a
  // green timeout on a hunch is how real regressions get masked. But do NOT
  // derive a new budget from the old claim — measure the ladder instead.
  await expect(stopBtn).not.toBeVisible({ timeout: 10_000 })
  await expect(chatInput(page)).toBeEnabled({ timeout: 10_000 })
  const interruptedLabels = page.locator('text=(interrupted)')
  await expect(interruptedLabels.first()).toBeVisible({ timeout: 10_000 })

  // Assert transcript.jsonl contains {type: "turn_canceled"} entry with a
  // non-empty descendants_canceled array.
  //
  // POLL for it rather than sleeping a fixed window then scanning once. Root
  // cause (live-reproduced locally, server-side instrumented, 2026-07-27):
  // RequestCancel's "graceful" cascade does not always write the
  // turn_canceled entry quickly. When the cancel lands while the LLM still
  // owes a response, the turn loop's gracefulTerminal branch (pkg/agent/loop.go,
  // guarded by turnState.gracefulInterruptRequested) makes ONE more real LLM
  // call — interruptHintMessage(), "Stop scheduling tools and provide a short
  // final summary" — and only once THAT call returns does Finish() run and
  // pkg/agent/cancel.go's onCancelFinish callback append the turn_canceled
  // entry. That extra round-trip's latency is real, variable LLM latency, not
  // bounded by the 3s/8s hard-abort escalation timers (those only fire the
  // hard-abort *signal* at those marks — PHASE B/C in RequestCancel — the
  // transcript write still waits for Finish() to actually run afterward).
  // Locally reproduced latency for this exact scenario ranged from ~3ms to
  // ~12.56s across otherwise-identical runs (same test, same prompts) — a
  // fixed 3s sleep-then-check races that variance directly and is the
  // confirmed cause of T24a/T24b's historical flakiness (fails, then often
  // passes on retry — never a real absence of the cascade, always a
  // too-early read). Poll on a ceiling well past the observed worst case
  // instead of asserting less.
  //
  // Discover this turn's session by diffing the sessions dir. A delegated
  // sub-turn can spawn its own ephemeral session dir, so there may be more than
  // one new dir — scan them (newest first) for the one whose transcript actually
  // recorded the cancel (the turn_canceled entry lives in the PARENT session).
  // Sessions are stored at OMNIPUS_HOME/sessions/<id>/<YYYY-MM-DD>.jsonl
  // (day-partitioned JSONL); the legacy transcript.jsonl name is also tried.
  const today = new Date().toISOString().slice(0, 10) // YYYY-MM-DD
  const sessionsDir = path.join(OMNIPUS_HOME, 'sessions')

  function scanForCancelEntry(): {
    entries: TranscriptEntry[]
    chosenSession: string
    scanList: string[]
    newSessions: string[]
  } {
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
    return { entries, chosenSession, scanList, newSessions }
  }

  // 30s ceiling: comfortably past the ~12.56s worst case observed locally,
  // with headroom for CI-under-load variance, while staying well inside
  // T24a/T24b's own 360s/420s test.setTimeout budgets.
  const pollDeadline = Date.now() + 30_000
  let scan = scanForCancelEntry()
  while (!scan.entries.some((e) => e.type === 'turn_canceled') && Date.now() < pollDeadline) {
    await page.waitForTimeout(500)
    scan = scanForCancelEntry()
  }
  const { entries, chosenSession, scanList, newSessions } = scan

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
//
// ─── 2026-07-28: two independent defects fixed, both of which made this test
// fail 3/3 deterministically (verified on the CI worker at both branch HEAD and
// the epic base 0da704cb — pre-existing, not an epic regression):
//
// 1. THE TEST RACED ITSELF. It hand-rolled its send and clicked Stop the instant
//    `stop-btn` became visible. `stop-btn` renders off the CLIENT-side
//    `isStreaming` flag (ChatScreen.tsx), which is set at SEND time — before the
//    gateway has registered a cancellable turn. The click landed ~1.1–2.9s after
//    send, `RequestCancel` found no active-turn hook via
//    GetActiveTurnHookForSession (pkg/agent/cancel.go:240), emitted
//    `was_fired:false`, and no-oped; the assistant then streamed to completion
//    (11,788 tokens, untruncated). Proof nothing had claimed the turn at click
//    time: the orphan watchdog's ClaimCancel on that same session SUCCEEDED 22s
//    later. Its passing siblings (T21/T22/T23/T25) never hit this because they
//    all go through `triggerLongStreamingTurn`, which additionally waits for the
//    streaming anchor to accumulate >80 chars of REAL streamed text — the
//    helper's own comment already named the gap ("The stop button (and even the
//    streaming-message placeholder) appear BEFORE the first token"). Streamed
//    assistant tokens can only come from a live, registered server-side turn, so
//    that wait is a genuine server-side readiness gate, not a sleep. FIX: T26 now
//    uses the same helper.
//
// 2. THE SPA NEVER BOUND TO THE TEST'S SESSION. It called a `createSession()`
//    REST helper then `page.goto('/#/sessions/<id>')`. That does not bind — the
//    SPA lazily mints its own session on first send (T24's helper documents the
//    exact same finding). Live evidence: `createSession` minted one id, the turn
//    ran on a different SPA-minted id, and BOTH cancel requests carried the
//    literal `__pending` sentinel as session_id. FIX: drop the pre-minted session
//    entirely, drive the turn from `/` like every other test here, and discover
//    the session the SPA actually used by diffing OMNIPUS_HOME/sessions (T24's
//    approach). The session-id assertion is STRENGTHENED, not weakened: the audit
//    entries' session_id must now equal a session directory THIS test created —
//    which the `__pending` sentinel can never satisfy.
//
// A note on WHERE the "before" snapshot is taken, because the obvious placement is
// wrong and it fails in a way that looks like a pass. T24's helper says the SPA
// "creates the session lazily on the first sent message"; for THIS route that is
// not what happens. Measured on the CI worker (`stat` birth times vs. audit
// timestamps, 3 consecutive runs against one warm gateway):
//
//     repeat  session dir born   cancel attempt   turn_id
//       0     07:33:49.96        07:34:15.69      jim-turn-1
//       1     07:34:21.54        07:34:35.84      mia-turn-2
//       2     07:34:42.08        07:34:54.91      mia-turn-3
//
// The directory is minted EAGERLY, within ~3s of the route mounting and ~13-26s
// BEFORE the turn is cancelled — not on send. So a snapshot taken after `/new`
// already contains this test's own session, the diff comes out empty, and the
// binding assertion fires. (The `mia-turn-*` ids corroborate the eager mint: the
// session was already bound to the default agent before triggerLongStreamingTurn
// switched the picker to Jim, so the picker label and the executing agent
// disagree. Harmless here — T26 only needs a cancellable stream, and the cancel
// fired 3/3 — but it is why the ids are not all `jim-turn-*`.)
//
// Snapshot BEFORE page.goto('/') therefore, not after `/new`. Taking it earlier
// only ever SHRINKS the "before" set, so a session minted at any point during the
// test still counts as new — the assertion stays exactly as strict while becoming
// independent of when the SPA decides to mint. Do not move it back down: with the
// late snapshot, run 0 passed and runs 1 and 2 failed, and run 0 passed only
// because the gateway was cold and the mint happened to land ~1s AFTER the
// snapshot. A sub-second accidental pass is not coverage.

test(
  'T26 — audit log contains turn_cancel_attempt and turn_canceled entries after cancel',
  async ({ page }) => {
    test.slow()

    // Baselines FIRST — before anything can mount a route and mint a session.
    // See the "note on WHERE the before snapshot is taken" in this test's header
    // comment: the session directory is created eagerly on route mount, so any
    // snapshot taken after page.goto('/') already contains this test's own
    // session and the newSessions diff silently comes out empty.
    const auditPath = path.join(OMNIPUS_HOME, 'system', 'audit.jsonl')
    const sessionsBefore = new Set(listSessionDirs(OMNIPUS_HOME))
    const entriesBefore = readJsonl<AuditEntry>(auditPath).length

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
    if (!fs.existsSync(auditPath)) {
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

    // Start from a clean session. `/new` is intercepted client-side
    // (useSlashMenu.ts runClientCommand 'new' → startNewSession()) and only
    // NULLIFIES activeSessionId — it does not mint a session; the SPA creates one
    // lazily on the first sent message. That is exactly what we want: the session
    // dir that appears on disk after this point is unambiguously THIS turn's.
    const input = chatInput(page)
    await expect(input).toBeEnabled({ timeout: 20_000 })
    await input.fill('/new')
    await input.press('Enter')
    await expect(input).toBeEnabled({ timeout: 20_000 })

    // Send, and — critically — do not proceed until the assistant has actually
    // STREAMED text back. See defect (1) in this test's header comment: waiting on
    // `stop-btn` alone returns before the gateway has a cancellable turn, so the
    // cancel no-ops with was_fired:false and this test's central assertion becomes
    // unfalsifiable. `triggerLongStreamingTurn` also switches to Jim and asks for a
    // 700-word tool-free essay, so the stream stays live for seconds after this
    // returns and the Stop click lands comfortably mid-turn.
    await triggerLongStreamingTurn(page)

    // Cancel the turn.
    const stopBtn = page.locator('[data-testid="stop-btn"]')
    await stopBtn.click()

    // Wait for cancel to complete. Under suite load the stop button can stay
    // on aria-label="Stopping..." after the turn has actually ended (composer
    // re-enables first). Poll until the composer is enabled OR the stop
    // button clears — either means cancel finished. Budget 90s (test.slow()).
    const cancelDeadline = Date.now() + 90_000
    let cancelDone = false
    while (Date.now() < cancelDeadline) {
      const inputEnabled = await chatInput(page).isEnabled().catch(() => false)
      const stopGone = !(await stopBtn.isVisible().catch(() => true))
      if (inputEnabled || stopGone) {
        cancelDone = true
        break
      }
      await page.waitForTimeout(500)
    }
    expect(cancelDone, 'cancel must complete within 90s (composer enabled or stop-btn gone)').toBe(
      true,
    )
    await expect(chatInput(page)).toBeEnabled({ timeout: 15_000 })

    // Both lookups below are scoped to a session THIS test created (i.e. a
    // directory that appeared under OMNIPUS_HOME/sessions since the snapshot taken
    // at the very top of the test). Sessions live at OMNIPUS_HOME/sessions/<id>/,
    // so the directory NAME is the session id — the same id the gateway stamps
    // into the audit rows (pkg/agent/cancel.go:245-250). Two reasons to scope:
    //
    //  a) It is the anti-sentinel guard. Mutual session_id equality between the
    //     two rows is satisfiable by two rows that are both wrong in the SAME way
    //     — exactly what the pre-2026-07-28 version of this test produced, with
    //     both cancel requests carrying the literal `__pending` sentinel. Binding
    //     the id to a real on-disk session directory is what makes the assertion
    //     falsifiable.
    //  b) It keeps a neighbouring turn's cancel from being mistaken for ours.
    //     Playwright runs this shard with workers:1 (playwright.config.ts), so no
    //     sibling TEST interleaves — but the gateway's own orphan watchdog can
    //     fire an unattended cancel on a leaked turn from an EARLIER test at any
    //     moment (runci.sh sets OMNIPUS_GATEWAY_ORPHANED_TURN_GRACE_SECONDS=20,
    //     and such a watchdog ClaimCancel was observed landing 22s into a run).
    //     That writes a perfectly well-formed turn.cancel.attempt{was_fired:true}
    //     for someone else's session, which an unscoped `.find()` would happily
    //     accept as proof that OUR cancel fired.
    //
    // POLLED rather than read once after a fixed sleep: the audit rows and the
    // transcript flush land a beat after the UI reports the cancel complete, and
    // the gap stretches under CI load (this suite shares its worker). A bounded
    // poll converges as soon as the evidence is on disk and cannot be tuned wrong
    // in the way a fixed `waitForTimeout` can. Nothing is weakened — the same
    // predicates must hold, we just stop reading too early.
    let attemptEntry: AuditEntry | undefined
    let cancelledEntry: AuditEntry | undefined
    let newSessions: string[] = []
    let cancelRowSummary: string
    const auditDeadline = Date.now() + 30_000
    for (;;) {
      newSessions = listSessionDirs(OMNIPUS_HOME).filter((s) => !sessionsBefore.has(s))
      const newEntries = readJsonl<AuditEntry>(auditPath).slice(entriesBefore)

      const isThisTurn = (e: AuditEntry) => {
        const sid = e.fields?.session_id
        return typeof sid === 'string' && newSessions.includes(sid)
      }

      // Dumped on failure so a no-op cancel (was_fired:false) or a sentinel
      // session_id is immediately legible instead of just "not found".
      cancelRowSummary = JSON.stringify(
        newEntries
          .filter((e) => typeof e.event === 'string' && e.event.startsWith('turn.cancel'))
          .map((e) => ({
            event: e.event,
            session_id: e.fields?.session_id,
            was_fired: e.fields?.was_fired,
          })),
      )

      // turn_cancel_attempt with was_fired:true, for THIS test's session.
      // events.go: EventTurnCancelAttempt = "turn.cancel.attempt"; tag json:"event".
      // Audit entries nest their payload under "fields" (pkg/audit/audit.go Emit).
      // was_fired:true is the load-bearing bit — it is set only when
      // GetActiveTurnHookForSession found a live turn AND ClaimCancel won it
      // (pkg/agent/cancel.go:240-250). A cancel that arrives before the gateway has
      // registered the turn emits was_fired:false and changes nothing; without this
      // predicate the test cannot tell the two apart.
      attemptEntry = newEntries.find(
        (e) => e.event === 'turn.cancel.attempt' && e.fields?.was_fired === true && isThisTurn(e),
      )
      // events.go: EventTurnCancelled = "turn.cancelled"; tag json:"event".
      cancelledEntry = newEntries.find((e) => e.event === 'turn.cancelled' && isThisTurn(e))

      if (attemptEntry && cancelledEntry) break
      if (Date.now() > auditDeadline) break
      await page.waitForTimeout(500)
    }

    // Shared diagnostic tail — every failure below needs the same three facts, and
    // omitting the session lists once already cost a full CI round-trip to explain
    // an "it just says none appeared" failure.
    const diag =
      `Sessions that appeared during this test: ${JSON.stringify(newSessions)}. ` +
      `Sessions already present at test start: ${sessionsBefore.size}. ` +
      `turn.cancel* rows written since test start: ${cancelRowSummary}.`

    if (!attemptEntry) {
      throw new Error(
        'INCOMPLETE: audit log has no turn.cancel.attempt{was_fired:true} for a session this ' +
          `test created. ${diag} ` +
          'was_fired:false means the cancel reached the gateway but found no registered turn ' +
          'to claim (a no-op — the exact pre-2026-07-28 failure); an empty session list means ' +
          'the SPA never persisted a new session; a session_id outside the list means the turn ' +
          'ran somewhere else. Traces to: cancel-cross-channel-spec.md T26, US-5.1, FR-18.',
      )
    }

    if (!cancelledEntry) {
      throw new Error(
        'INCOMPLETE: audit log has no turn.cancelled entry for a session this test created. ' +
          `${diag} Traces to: cancel-cross-channel-spec.md T26, US-5.2, FR-19.`,
      )
    }

    // Assert: session_id matches between the two entries (US-5 — the attempt and
    // the completion must describe the same turn). Not implied by the scoping
    // above: `isThisTurn` only requires membership in newSessions, which can hold
    // more than one directory (a delegated sub-turn gets its own), so this still
    // rules out an attempt on one session paired with a cancellation of another.
    // session_id is nested under fields (pkg/audit/audit.go Emit structure).
    expect(attemptEntry.fields?.session_id).toBeTruthy()
    expect(cancelledEntry.fields?.session_id).toBeTruthy()
    expect(attemptEntry.fields?.session_id).toEqual(cancelledEntry.fields?.session_id)
  },
)

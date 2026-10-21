/**
 * cancel-cross-channel.spec.ts — E2E tests for cross-channel /cancel feature.
 *
 * Scope: T21–T26 from cancel-cross-channel-spec.md §10 TDD Plan.
 * Traces to: docs/internal/specs/cancel-cross-channel-spec.md US-1, US-4, US-5; T21–T26.
 *
 * Tests T21–T24, T26 drive a real LLM (OPENROUTER_API_KEY_CI required in CI).
 * T25 uses the existing isolated GatewayProcess provider-endpoint seam with a
 * controlled, slow text stream. The browser, gateway and cancellation stay real;
 * only the external model is scripted. T21–T24 and T26 retain live-model coverage.
 * T25 covers the immediate "Stopping..." label, not stuck-loop escalation.
 *
 * Architecture note: Tests switch the active agent to Jim, which generates
 * long inline prose rather than handing off to another agent (Mia has strong
 * "no long enumerations" guardrails that finish too quickly for the Stop button
 * to be visible). This mirrors the pattern in tests/e2e/chat.spec.ts test (e).
 */

import * as fs from 'fs'
import * as path from 'path'
import http from 'node:http'
import { expect, errors, type BrowserContext, type Page, type Locator } from '@playwright/test'
import { test } from './fixtures/console-errors'
import { chatInput, closeSidebar, openSidebar, selectAgent, selectedAgentRow, waitForConnected } from './fixtures/selectors'
import { freshChat } from './fixtures/session-setup'
import { GatewayProcess } from './fixtures/gateway-process'
import { E2E_MODEL } from './fixtures/e2e-model'

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

// ── Shared helper: switch to Jim and trigger a long-running turn ──────────────

/**
 * A prompt that reliably produces multi-second, tool-free streaming output.
 *
 * Forbidding tools and demanding inline prose: on a bare "write 500 words" prompt,
 * the earlier pick intermittently shortcuts to the write_file TOOL (Jim has an
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
  //
  // FAIL FAST (llm-agents shard, run 38112714098): the { timeout } object
  // below used to be passed as the SECOND argument of waitForFunction — which
  // is the pageFunction's `arg`, not its options (signature:
  // waitForFunction(pageFunction, arg, options)) — so the wait was UNBOUNDED:
  // whenever the turn never streamed >80 chars into the anchor, this poll
  // rode the locator default (config sets no actionTimeout) out to the whole
  // test timeout and every T2x attempt burned 4.7-7.3 min in CI. The 30s
  // bound is now applied (third argument) and expiry is rethrown with the
  // predicate spelled out, so a dead stream fails in 30s with the reason
  // named instead of at the spec timeout.
  try {
    await page.waitForFunction(
      () => {
        const el = document.querySelector('[data-testid="streaming-message-anchor"]')
        const text = (el?.textContent ?? '').replace(/\s+/g, ' ').trim()
        return text.length > 80
      },
      undefined,
      { timeout: 30_000 },
    )
  } catch (err) {
    if (!(err instanceof errors.TimeoutError)) throw err
    throw new Error(
      'waitForActiveStream: the stop button appeared but the streaming anchor ' +
        '([data-testid="streaming-message-anchor"]) never accumulated more than 80 chars ' +
        'of real streamed text within 30s — the turn never produced a live stream, so ' +
        'no cancel can land mid-turn. The send, the session binding, or the LLM stream ' +
        'is broken upstream of the cancel behaviour this file tests.',
      { cause: err },
    )
  }
}

/**
 * Switch the agent picker to Jim (who generates long inline prose) and send
 * a prompt that reliably produces multi-second streaming output.
 * Returns once a message is ACTIVELY streaming (stop button visible AND the live
 * streaming-message anchor is present) — so a subsequent cancel always has an
 * incomplete message to mark interrupted.
 */
async function triggerLongStreamingTurn(page: Page): Promise<string> {
  const input = chatInput(page)
  await expect(input).toBeEnabled({ timeout: 20_000 })
  // toBeEnabled() alone no longer implies "connected" (2fa26e6a, #105 fix —
  // see waitForConnected's doc comment in fixtures/selectors.ts). Without
  // this, the long-streaming prompt below can land in the outbound queue
  // instead of the wire, and every T2x test built on this helper hangs
  // waiting for a stop-btn that never appears.
  await waitForConnected(page, { timeout: 20_000 })

  // Switch to Jim — long inline generation, multi-second stream window.
  // Sidebar row click is the only agent switch since the composer AgentPicker
  // was removed (nav commit e84ac0532; FR-029 no in-chat handover).
  await selectAgent(page, /Jim/i)
  // Then start a fresh chat WITH Jim so every caller's turn runs in its OWN
  // session. The agent-row "New chat" button this used to click is REMOVED
  // (founder decision D7 — capability stays, UI access point gone;
  // FR-029/FR-031 still govern, "/new" stays deleted). freshChat creates the
  // session via the public REST API and deep-links it, and RETURNS its id —
  // callers that bind on-disk evidence (transcript/audit rows) to the turn
  // use the exact id instead of inferring it from a sessions-dir diff.
  const sessionId = (await freshChat(page, /Jim/i)).sessionId

  await input.fill(LONG_PROSE_PROMPT)
  await input.press('Enter', { timeout: 15_000 })

  // Wait until the turn is observably active (see waitForActiveStream's doc
  // comment) before returning — a subsequent cancel always has a real,
  // in-flight turn with hundreds of words of runway left.
  await waitForActiveStream(page)

  // Returned LAST: the id is only handed to callers once the turn this
  // helper exists to start is actually streaming (g1-code-reviewer F2: an
  // early return here silently skipped the send entirely).
  return sessionId
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
    await stopBtn.click({ timeout: 15_000 })

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
    await slashInput.click({ timeout: 15_000 })
    await slashInput.type('/c', { timeout: 15_000 })

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
    await cancelMenuItem.first().click({ timeout: 10_000 })

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

    // U6: assert the interruption suffix inside an actual message row, not
    // the old conversation-level duplicate. Preserve the attached-state
    // oracle for the virtualized thread; Stop/input behavior is pinned above.
    const interruptedMarker = page.locator('[data-testid="assistant-message"]').getByText('(interrupted)', { exact: true })
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
// T24a/T24b once covered two delegation modes of the single unified `delegate`
// tool (ADR-036), selected by its `async` argument:
//   T24a — `delegate` async=true  (background): the descendant streams in the
//                                   background while the parent turn stays live.
//   T24b — `delegate` async=false (await):      the parent turn BLOCKS on the
//                                   descendant's run until it returns (or is
//                                   cancelled).
// ADR-091 D4 deleted the async=false/true argument outright — every `delegate`
// call is now what T24a used to mean (launcher-ack background, non-blocking),
// and the tool rejects any call still carrying `async`
// (pkg/tools/delegate_run.go's retired-argument loop → "invalid_argument:
// async", pinned by pkg/tools/delegate_test.go's "retired async" case).
// T24b's specific "parent turn BLOCKS" mode no longer exists. Pre-ADR-091,
// both routed through spawnSubTurn (pkg/agent/subturn.go, since deleted);
// today a delegated child registers via turn.go's
// registerActiveTurn/registerTurnIfAbsent (steer_launcher.go's
// dispatchSteeredSessionWithReservation) — so RequestCancel → InterruptSession
// cascades to the descendant in BOTH cases (the Go-level proof is
// TestCancel_SubAgentCascade).
//
// FAILURE HISTORY (CI run 36327766952, 2026-09-27): the prompt used to
// instruct the model to pass `async: true`/`async: false` verbatim. The
// central e2e model (tests/e2e/e2e-model.json — the same model the release
// branch's picker-first-option onboarding resolves to, so the model did NOT
// change vs release) sent the instructed retired key in T24b attempt 1, the
// gateway rejected the whole call with "unexpected property \"async\""
// (gateway log 15:01:38), no delegation fired, and the activity-bar wait 150s
// later died with a bare "element(s) not found". The three release llm-agents
// runs before this stayed green only because deepseek happened to OMIT the
// retired key there (zero "Tool argument validation failed" lines in their
// gateway logs) — survivorship, not a model difference. As long as the prompt
// named the retired argument the pair was flaky by construction: model
// compliance = guaranteed red, model silence = green.
//
// RESOLUTION (this change): the pair now exercises the one surviving
// launcher-ack path — the prompt never names any argument the current tool
// schema does not declare, and the two variants differ only in their closer
// wording (two samples of the same variance-prone LLM-driven cascade,
// matching the spec's single T24 row). Renaming/retiring the pair stays with
// the spec owner; the historical names are kept for CI triage continuity.

/**
 * Drive the cancel-cascade scenario and assert the transcript records
 * turn_canceled with a non-empty descendants_canceled.
 *
 * `mode.label` names the variant in failure diagnostics; `mode.closer` is the
 * final instruction line that nudges the central e2e model (tests/e2e/e2e-model.json)
 * to emit exactly one delegate
 * tool call and nothing else. The prompt must NEVER name an argument the
 * current tool schema does not declare: the tool rejects such a call outright
 * (see the failure-history note above), so model compliance would produce a
 * guaranteed rejection instead of a delegation.
 */
async function cancelPreamble(page: Page) {
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
  // with respect to the fresh-chat step below because `startNewSession` (the
  // store action behind the agent-row "New chat" action, pairExtra.ts
  // ::beginPairExtra) preserves the current agent
  // (`activeAgentId: agentId ?? state.activeAgentId`, src/store/session.ts) —
  // the switch survives the reset.
  //
  // `selectAgent` also doubles as the real readiness gate the old
  // `toBeEnabled()` was standing in for: it opens the sidebar, resolves the
  // workspace roster (which requires `GET /api/v1/agents` and the workspaces
  // list to have resolved), clicks Jim's row and asserts the row's
  // aria-current. By the time it returns, the chat surface is genuinely
  // mounted.
  //
  // Route to Jim (Planner & Orchestrator), NOT the default agent Mia. An
  // earlier CI investigation (run 27296266639) found Mia's "guide" persona
  // makes the model REFUSE to delegate even when it CAN; RC6 showed she also
  // structurally cannot. Either way the parent never emits a delegation frame.
  // The cancel window comes from the SUBAGENT running long enough (it streams
  // a multi-hundred-word inline essay), so the parent agent's prose behaviour
  // is irrelevant — only that it delegates.
  await selectAgent(page, /Jim/i)

  // Fresh chat via the agent row's "New chat with Jim" icon button — the ONLY
  // new-chat path since the navigation redesign (session-core-spec FR-029
  // "only agent-row New chat creates extra MAIN chat"; FR-031, founder WC-1
  // 2026-10-09, deleted "/new" from the command table AND forbids the SPA
  // from sending it — typing /new now shows a refusal and sends nothing,
  // src/hooks/useSlashMenu.ts RETIRED_COMMAND_NAMES). The /new-palette flow
  // this block used to drive is therefore gone for good. That button itself
  // is gone too now: founder decision D7 (DECISIONS.md, squad
  // nav-agent-row-menu-20261011) removed the new-chat UI access point
  // everywhere — capability stays, button does not. freshChat creates this
  // turn's session via the public REST API (POST /api/v1/sessions) and
  // deep-links it through the SPA's own /sessions/<id> route, and RETURNS
  // the id: the parent session below is KNOWN exactly, not inferred from a
  // sessions-dir diff (the old lazy-mint flow left no id to bind with). The
  // selectAgent-first ordering above stays load-bearing — freshChat resolves
  // /Jim/i through the sidebar row just like the removed button did.
  const parentSessionId = (await freshChat(page, /Jim/i)).sessionId
  await expect(input).toBeEnabled({ timeout: 20_000 })

  // Diagnostics only: dirs present before the turn (earlier tests in the same
  // gateway leave their own session dirs behind). The turn's session is bound
  // by the exact id above — this set is no longer the identifier.
  const sessionsBefore = new Set(listSessionDirs(OMNIPUS_HOME))
  return { input, sessionsBefore, parentSessionId };
}

async function cancelSendAndStop(
  page: Page,
  input: Locator,
  mode: { label: string; closer: string },
): Promise<void> {
  // The subagent task must keep the descendant RUNNING for several seconds so a
  // Stop click lands while it's live. A long inline essay streams for several
  // seconds; an instant-rejected tool task (e.g. a sandbox-escaping read)
  // finishes in ~0s before Stop can fire. Explicit single-tool instruction
  // with a hard "no prose" guardrail so the central e2e model
  // (tests/e2e/e2e-model.json) reliably emits the delegation call.
  //
  // Do NOT name an argument the delegate schema no longer declares. The tool
  // rejects any call carrying a retired key ("invalid_argument: async",
  // pkg/tools/delegate_run.go's retired-argument loop), so a model that obeys
  // such an instruction produces a guaranteed rejection — no delegation, no
  // Activity Bar, and a bare "element(s) not found" failure at the wait
  // deadline (CI run 36327766952, 2026-09-27: deepseek-v4.1-flash sent the
  // instructed `async: false`; gateway log 15:01:38). Align the prompt with
  // the CURRENT schema only.
  await input.fill(
    [
      'Call the `delegate` tool exactly once, now, with these arguments:',
      '  label: "cancel cascade test"',
      '  task: "You are a subagent. Do not use any tools. Write a detailed 800-word essay about renewable energy as continuous inline prose, writing without stopping until you reach 800 words."',
      mode.closer,
    ].join('\n'),
  )

  // RC6 GUARD — re-assert the routed agent at the LAST possible moment before
  // the send. `selectAgent` already asserted this above, but in the failure
  // this guard was written for the agent silently reverting to Mia AFTERWARDS
  // (a session attach calling `setActiveSession(id, 'mia', …)` clobbered it),
  // and the only symptom was the ActivityBar wait below dying 150s later with
  // a bare "element(s) not found". That error names neither the agent nor the
  // cause. Checking here turns the same defect into an immediate, truthful
  // failure that says which agent the turn was about to run as. Cheap: this is
  // a rendered-row read with no network round-trip. (The deleted composer
  // picker's label was the old read; the selected sidebar row is the current
  // user-visible signal for the same fact.)
  // The selected row lives in the sidebar, which is unmounted while closed - and
  // it IS closed here: selecting a row closes an unpinned sidebar and freshChat's
  // page.goto reloads the SPA with it closed. Open it the way a user does before
  // reading the row, or the guard fails "element(s) not found" for a reason that
  // has nothing to do with the routed agent (T24a/T24b, 6 of 6 runs).
  await openSidebar(page)
  await expect(
    selectedAgentRow(page),
    'the routed agent must still be Jim at send time — Mia is denied `delegate` ' +
      'by her seeded policy (pkg/coreagent/core.go), so a silent revert to her ' +
      'makes the delegation impossible rather than merely slow (RC6)',
  ).toHaveAttribute('aria-label', /Jim/i, { timeout: 10_000 })
  // Back to the state a user is in while typing: the open sidebar's click-catcher
  // would otherwise swallow the Stop click later in this test.
  await closeSidebar(page)

  await input.press('Enter', { timeout: 15_000 })

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
  await stopBtn.click({ timeout: 15_000 })
}

async function assertCancelInterrupted(page: Page): Promise<void> {
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
  const stopBtn = page.locator('[data-testid="stop-btn"]')
  await expect(stopBtn).not.toBeVisible({ timeout: 10_000 })
  await expect(chatInput(page)).toBeEnabled({ timeout: 10_000 })
  const interruptedLabels = page.locator('text=(interrupted)')
  await expect(interruptedLabels.first()).toBeVisible({ timeout: 10_000 })
}

async function assertCancelTranscriptCascade(
  page: Page,
  sessionsBefore: Set<string>,
  parentSessionId: string,
  mode: { label: string; closer: string },
): Promise<void> {
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
 // The turn's PARENT session is known exactly — freshChat created it and
  // returned its id, and the turn_canceled entry lives in the PARENT session
  // — so the scan reads exactly that one directory. A delegated sub-turn can
  // spawn its own ephemeral session dir; those stay OUT of the predicate (an
  // ephemeral dir can never satisfy it) and are listed only in the failure
  // diagnostics. Sessions are stored at OMNIPUS_HOME/sessions/<id>/… (the
  // directory NAME is the session id); day-partitioned <YYYY-MM-DD>.jsonl,
  // with the legacy transcript.jsonl name also tried.
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
    // Exactly the parent session — the id is known, no inference.
    const scanList = [parentSessionId]

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
        `after cancel (mode=${mode.label}). Scanned sessions: ${JSON.stringify(scanList)} ` +
        `(new: ${JSON.stringify(newSessions)}). Chosen: ${chosenSession || '(none)'}. ` +
        `Entries found in chosen: ${JSON.stringify(entries.map((e) => ({ type: e.type, role: e.role })))}. ` +
        'Traces to: cancel-cross-channel-spec.md T24, US-4.1, FR-15.',
    )
  }

  // descendants_canceled must be a non-empty array (cascade fired).
  expect(
    Array.isArray(cancelledEntry.descendants_canceled) &&
      (cancelledEntry.descendants_canceled as string[]).length > 0,
    `turn_canceled entry must have a non-empty descendants_canceled array (cascade wired per FR-6a, mode=${mode.label})`,
  ).toBe(true)
}

async function assertCancelCascadesToSubagent(
  page: Page,
  mode: { label: string; closer: string },
) {
  const { input, sessionsBefore, parentSessionId } = await cancelPreamble(page)
  await cancelSendAndStop(page, input, mode)
  await assertCancelInterrupted(page)
  await assertCancelTranscriptCascade(page, sessionsBefore, parentSessionId, mode)
}

test(
  'T24a — cancel cascades to background subagent (delegate async=true): transcript records turn_canceled with descendants',
  async ({ page }) => {
    // The central e2e model (tests/e2e/e2e-model.json) is reliable but slower than the
    // earlier pick — the delegation turn + the subagent's inline essay + cancel can exceed
    // the 270s test.slow() ceiling under suite load. Use an explicit higher budget.
    test.setTimeout(360_000)
    await assertCancelCascadesToSubagent(page, {
      label: 'delegate launcher-ack (ADR-091; historical name async=true)',
      closer: 'Do not reply in prose. Call the delegate tool immediately.',
    })
  },
)

test(
  'T24b — cancel cascades to awaited subagent (delegate async=false): transcript records turn_canceled with descendants',
  async ({ page }) => {
    // Await mode blocks the parent turn on the descendant's full run, so under
    // the central e2e model's (tests/e2e/e2e-model.json) slower streaming this needs
    // more headroom than the background variant.
    test.setTimeout(420_000)
    await assertCancelCascadesToSubagent(page, {
      label: 'delegate launcher-ack (ADR-091; historical name async=false)',
      closer: 'No prose reply. No other tool. Call delegate now.',
    })
  },
)

// ── T25: Stop button UI progression (EC-15, FR-21) ──────────────────────────
// Traces to: cancel-cross-channel-spec.md §8 Flow G, §10 T25, FR-21.
// The spec calls for immediate (<100ms) feedback; retain the original browser
// assertion's 500ms bound, without making it depend on a vendor's first token.
// Stuck-loop force (3s) and detach (6s) progression are not exercised here and
// were not asserted by the original T25. No manual verification is claimed.

/** Script only the external provider, using the same apiBase seam as agent-grep. */
async function startStopLabelProvider() {
  const evidence = { streamRequests: 0, chunksSent: 0, streamsClosed: 0, errors: [] as string[] }
  // External OpenAI-compatible protocol, not a gateway/SPA wire format.
  const chunk = (content: string) => 'data: ' + JSON.stringify({
    id: 'stop-label-stream', object: 'chat.completion.chunk', model: E2E_MODEL,
    created: Math.floor(Date.now() / 1000),
    choices: [{ index: 0, delta: { role: 'assistant', content }, finish_reason: null }],
  }) + '\n\n'
  const server = http.createServer((req, res) => {
    const chunks: Buffer[] = []
    req.on('data', (data: Buffer) => chunks.push(data))
    req.on('error', (error) => evidence.errors.push(`provider request: ${error.message}`))
    const fail = (message: string) => {
      evidence.errors.push(message)
      res.writeHead(500, { 'Content-Type': 'application/json' })
      res.end(JSON.stringify({ error: { message } }))
    }
    req.on('end', () => {
      if (req.method === 'GET' && req.url?.includes('/models')) {
        res.writeHead(200, { 'Content-Type': 'application/json' })
        res.end(JSON.stringify({ object: 'list', data: [{ id: E2E_MODEL, object: 'model' }] }))
        return
      }
      if (req.method !== 'POST' || !req.url?.includes('/chat/completions')) {
        fail(`unexpected provider route: ${req.method} ${req.url}`)
        return
      }
      let body: Record<string, unknown>
      try {
        body = JSON.parse(Buffer.concat(chunks).toString('utf8')) as Record<string, unknown>
      } catch (error) {
        fail(`invalid provider request JSON: ${String(error)}`)
        return
      }
      if (body.stream !== true) {
        // Onboarding/key validation does not start the acceptance stream.
        res.writeHead(200, { 'Content-Type': 'application/json' })
        res.end(JSON.stringify({
          id: 'stop-label-validation', object: 'chat.completion', model: E2E_MODEL,
          choices: [{ index: 0, message: { role: 'assistant', content: 'probe ok' }, finish_reason: 'stop' }],
          usage: { prompt_tokens: 1, completion_tokens: 2, total_tokens: 3 },
        }))
        return
      }
      const messages = Array.isArray(body.messages) ? body.messages as Record<string, unknown>[] : []
      // The gateway can append a current-time user message after the person's
      // message. Require the original prompt, not a position in that history.
      const hasPrompt = messages.some((message) => {
        if (message.role !== 'user') return false
        const content = message.content
        const text = typeof content === 'string' ? content : Array.isArray(content) ?
          content.map((part: Record<string, unknown>) => part.type === 'text' ? part.text : '').join('') : ''
        return text.includes(LONG_PROSE_PROMPT)
      })
      if (!hasPrompt || evidence.streamRequests !== 0) {
        fail('expected exactly one streaming request for the original long-prose prompt')
        return
      }
      evidence.streamRequests += 1
      res.writeHead(200, { 'Content-Type': 'text/event-stream' })
      const sendToken = () => {
        res.write(chunk('Solar and wind power sustain this controlled stream. '))
        evidence.chunksSent += 1
      }
      // Two chunks exceed the unchanged >80-character readiness gate. Keep
      // streaming every 250ms until the REAL gateway cancels its HTTP request;
      // never emit a finish chunk or [DONE], so natural completion cannot pass.
      const timer = setInterval(sendToken, 250)
      res.once('close', () => {
        clearInterval(timer)
        evidence.streamsClosed += 1
      })
      res.on('error', (error) => evidence.errors.push(`provider response: ${error.message}`))
      sendToken()
    })
  })
  const url = await new Promise<string>((resolve, reject) => {
    server.once('error', reject)
    server.listen(0, '127.0.0.1', () => {
      const address = server.address()
      if (!address || typeof address === 'string') return reject(new Error('Stop-label provider: no port'))
      resolve(`http://127.0.0.1:${address.port}`)
    })
  })
  return { url, evidence, close: () => new Promise<void>((resolve, reject) => {
    server.close((error) => error ? reject(error) : resolve())
    server.closeAllConnections()
  }) }
}

test.describe(() => {
  // Scope zero retries to this deterministic acceptance, not its live-model siblings.
  test.describe.configure({ retries: 0 })
  test(
    'T25 — stop button morphs to "Stopping..." immediately on click (EC-15, FR-21)',
    async ({ browser }, info) => {
      test.slow()
      const provider = await startStopLabelProvider()
      let gateway: GatewayProcess | undefined
      let context: BrowserContext | undefined
      try {
        gateway = await GatewayProcess.start({ apiBase: provider.url, model: E2E_MODEL })
        context = await browser.newContext({
          baseURL: gateway.baseURL, storageState: await gateway.browserStorageState(),
        })
        const page = await context.newPage()
        await page.goto('/')
        // Preserve Stop visibility AND >80 genuinely delivered characters.
        await triggerLongStreamingTurn(page)
        expect(provider.evidence.streamRequests, 'the real turn reached the scripted provider once').toBe(1)
        expect(provider.evidence.streamsClosed, 'the provider stream is still open before Stop').toBe(0)

        const stopBtn = page.locator('[data-testid="stop-btn"]')
        await stopBtn.click({ timeout: 15_000 })

        // Original label and settlement assertions, with unchanged deadlines.
        await expect(stopBtn).toContainText('Stopping...', { timeout: 500 })
        await expect(stopBtn).not.toBeVisible({ timeout: 10_000 })
        await expect(chatInput(page)).toBeEnabled({ timeout: 5_000 })

        // Check before teardown: closing our context/provider must not supply
        // the abort that only the real Stop action is allowed to demonstrate.
        await expect.poll(() => provider.evidence.streamsClosed, {
          timeout: 5_000, message: 'Stop must close the actual in-flight provider stream',
        }).toBe(1)
        expect(provider.evidence.errors, 'no unexpected or failed scripted-provider request').toEqual([])
      } finally {
        try {
          await info.attach('stop-label-provider-evidence', {
            body: JSON.stringify(provider.evidence, null, 2), contentType: 'application/json',
          })
          console.log(`[T25] provider evidence: ${JSON.stringify(provider.evidence)}`)
        } finally {
          try { await context?.close() } finally {
            try { await gateway?.stop() } finally { await provider.close() }
          }
        }
      }
    },
  )
})

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

// ─── 2026-10-11: the diff-based session binding is SUPERSEDED. Founder
// decision D7 (DECISIONS.md, squad nav-agent-row-menu-20261011) removed the
// agent-row "New chat" button, and triggerLongStreamingTurn now creates the
// turn's session via freshChat (public REST POST /api/v1/sessions + the SPA's
// own /sessions/<id> deep-link route) — which RETURNS the session id. The
// audit rows below are bound to THAT exact id (was_fired attempt and
// turn.cancelled must both carry it), which is strictly stronger than the old
// "session_id ∈ dirs that appeared since the snapshot" set membership: the
// __pending sentinel still cannot satisfy it, a neighbouring turn's id still
// cannot either, and the whole eager-mint/snapshot-timing hazard this header
// documents is gone because nothing is inferred from directory diffs anymore.
// The snapshot stays only as failure diagnostics.

test(
  'T26 — audit log contains turn_cancel_attempt and turn_canceled entries after cancel',
  async ({ page }) => {
    test.slow()

    // sessionsBefore is DIAGNOSTICS now (printed on failure): the turn's
    // session is bound by the exact id freshChat returns below, so nothing
    // depends on when a directory minted. The audit-entry cursor below is
    // still a real baseline — it scopes "rows written since test start".
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

    // Start from a clean session — provided by `triggerLongStreamingTurn`
    // itself, which now creates it via freshChat (the agent-row "New chat"
    // button is removed by founder decision D7; the `/new` slash command
    // stays deleted too — typing it shows a refusal and sends nothing) and
    // RETURNS the session id. Binding below uses THAT exact id, so the
    // assertion no longer depends on when (or whether) a directory appeared.
    const input = chatInput(page)
    await expect(input).toBeEnabled({ timeout: 20_000 })

    // Send, and — critically — do not proceed until the assistant has actually
    // STREAMED text back. See defect (1) in this test's header comment: waiting on
    // `stop-btn` alone returns before the gateway has a cancellable turn, so the
    // cancel no-ops with was_fired:false and this test's central assertion becomes
    // unfalsifiable. `triggerLongStreamingTurn` also switches to Jim and asks for a
    // 700-word tool-free essay, so the stream stays live for seconds after this
    // returns and the Stop click lands comfortably mid-turn.
    const turnSessionId = await triggerLongStreamingTurn(page)

    // Cancel the turn.
    const stopBtn = page.locator('[data-testid="stop-btn"]')
    await stopBtn.click({ timeout: 15_000 })

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

    // Both lookups below are scoped to THIS test's own turn session — the id
    // freshChat created and returned (Sessions live at
    // OMNIPUS_HOME/sessions/<id>/; the same id the gateway stamps into the
    // audit rows, pkg/agent/cancel.go:245-250). Two reasons to scope:
    //
    //  a) It is the anti-sentinel guard. Mutual session_id equality between the
    //     two rows is satisfiable by two rows that are both wrong in the SAME way
    //     — exactly what the pre-2026-07-28 version of this test produced, with
    //     both cancel requests carrying the literal `__pending` sentinel. Binding
    //     the id to the exact session this test created is what makes the
    //     assertion falsifiable (equality with turnSessionId — the sentinel can
    //     never satisfy it).
    //  b) It keeps a neighbouring turn's cancel from being mistaken for ours.
    //     Playwright runs this shard with workers:1 (playwright.config.ts), so no
    //     sibling TEST interleaves — but a leaked turn from an EARLIER test that
    //     gets canceled by some other means still writes a perfectly well-formed
    //     turn.cancel.attempt{was_fired:true} for someone else's session, which
    //     an unscoped `.find()` would happily accept as proof that OUR cancel
    //     fired.
    //
    // POLLED rather than read once after a fixed sleep: the audit rows and the
    // transcript flush land a beat after the UI reports the cancel complete, and
    // the gap stretches under CI load (this suite shares its worker). A bounded
    // poll converges as soon as the evidence is on disk and cannot be tuned wrong
    // in the way a fixed `waitForTimeout` can. Nothing is weakened — the same
    // predicates must hold, we just stop reading too early.
    let attemptEntry: AuditEntry | undefined
    let cancelledEntry: AuditEntry | undefined
    let cancelRowSummary: string
    const auditDeadline = Date.now() + 30_000
    for (;;) {
      const newEntries = readJsonl<AuditEntry>(auditPath).slice(entriesBefore)

      const isThisTurn = (e: AuditEntry) => e.fields?.session_id === turnSessionId

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

    // Shared diagnostic tail — every failure below needs the same facts, and
    // omitting the session lists once already cost a full CI round-trip to explain
    // an "it just says none appeared" failure.
    const diag =
      `This test's turn session id: ${turnSessionId}. ` +
      `Sessions already present at test start: ${sessionsBefore.size} ` +
      `(${JSON.stringify([...sessionsBefore])}). ` +
      `turn.cancel* rows written since test start: ${cancelRowSummary}.`

    if (!attemptEntry) {
      throw new Error(
        'INCOMPLETE: audit log has no turn.cancel.attempt{was_fired:true} for this test\'s ' +
          `turn session. ${diag} ` +
          'was_fired:false means the cancel reached the gateway but found no registered turn ' +
          'to claim (a no-op — the exact pre-2026-07-28 failure); a session_id other than the ' +
          "test's own means the turn ran somewhere else. Traces to: " +
          'cancel-cross-channel-spec.md T26, US-5.1, FR-18.',
      )
    }

    if (!cancelledEntry) {
      throw new Error(
        'INCOMPLETE: audit log has no turn.cancelled entry for a session this test created. ' +
          `${diag} Traces to: cancel-cross-channel-spec.md T26, US-5.2, FR-19.`,
      )
    }

    // Assert: session_id matches between the two entries (US-5 — the attempt and
    // the completion must describe the same turn). Both rows already carry THIS
    // test's exact turnSessionId (the scoping above), so this equality also
    // rules out an attempt on one session paired with a cancellation of another.
    // session_id is nested under fields (pkg/audit/audit.go Emit structure).
    expect(attemptEntry.fields?.session_id).toBeTruthy()
    expect(cancelledEntry.fields?.session_id).toBeTruthy()
    expect(attemptEntry.fields?.session_id).toEqual(cancelledEntry.fields?.session_id)
  },
)

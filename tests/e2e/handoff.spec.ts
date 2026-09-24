import * as fs from 'fs'
import * as path from 'path'
import { expect, type Page } from '@playwright/test';
import { test } from './fixtures/console-errors';
import { expectA11yClean } from './fixtures/a11y';
import { chatInput, selectAgent, waitForConnected } from './fixtures/selectors';

// Global storageState provides pre-authenticated session (see playwright.config.ts + global-setup.ts).

// ARCHITECTURE NOTE: The sprint-h-subagent-block-spec.md (TDD row 20) called for using a
// "scenario-provider path" for determinism via a Go-level scenario provider gated behind the
// `test_harness` build tag. That mechanism was removed 2026-05-10 — both Go and Playwright
// suites now drive a real LLM (requires OPENROUTER_API_KEY_CI) and rely on tightened prompts +
// structural assertions for determinism.
// Traces to: sprint-h-subagent-block-spec.md line 380 (TDD row 20, BDD Scenarios 1, 4)

// ── Transcript helpers (mirrored from replay-fidelity.spec.ts) ─────────────────

const BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6060'

const OMNIPUS_HOME =
  process.env.OMNIPUS_HOME ||
  (process.env.HOME ? path.join(process.env.HOME, '.omnipus') : '/tmp/omnipus-e2e-test')

interface TranscriptEntry {
  id: string
  type?: string
  role: string
  content?: string
  timestamp: string
  agent_id?: string
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

function getStoredAuthToken(): string | null {
  // Respect OMNIPUS_AUTH_FILE env var set by isolated test runs (e.g. port 6062)
  // to avoid using a token minted for a different gateway instance.
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
    // Auth file may not exist in first run
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
    data: { agent_id: 'mia', type: 'chat' },
  })
  if (!resp.ok()) {
    const body = await resp.text()
    throw new Error(`POST /api/v1/sessions failed: ${resp.status()} ${resp.statusText()} — ${body}`)
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
    throw new Error(
      `PUT /api/v1/sessions/${sessionId} failed: ${resp.status()} ${resp.statusText()} — ${body}`,
    )
  }
}

/**
 * Open a seeded session via its deep-link route, `/#/sessions/{id}`.
 *
 * Was: click a button named "Open sessions panel", then one named "Open
 * session: <title>". Neither accessible name exists anywhere in current
 * `src/` since the session panel was redesigned into the two-mode
 * SearchModal (`src/components/search/SearchModal.tsx`) — see
 * tests/e2e/fixtures/session-setup.ts's `openSessionByDeepLink` doc comment
 * for the full history.
 *
 * This test only cares that a seeded multi-agent transcript replays with
 * correct per-agent labels — not that the panel-open UI affordance itself
 * works (that's what retention.spec.ts's panel-based interaction now
 * covers) — so the deep link, which drives the same real WS attach +
 * replay, is the right seam here.
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

async function waitForReplayDone(page: Page): Promise<void> {
  await expect(chatInput(page)).toBeEnabled({ timeout: 30_000 })
  // toBeEnabled() alone no longer implies "connected" (2fa26e6a, #105 fix —
  // see waitForConnected's doc comment in fixtures/selectors.ts).
  await waitForConnected(page, { timeout: 30_000 })
}

// ── Activity panel helpers (ADR-091 D7/FR-E-004) ────────────────────────────────
// Mirrors replay-fidelity.spec.ts's / delegation-hidden.spec.ts's own helper of
// the same name. Idempotent open avoids steered-session-stop.spec.ts's
// documented trap: a second unconditional click while the panel is already
// open lands on the Radix Sheet's own overlay, not the bar.
async function openActivityPanel(page: Page): Promise<void> {
  const bar = page.locator('[data-testid="activity-bar"]');
  await expect(bar).toBeVisible({ timeout: 15_000 });
  if ((await bar.getAttribute('aria-expanded')) !== 'true') {
    await bar.click();
  }
  await expect(bar).toHaveAttribute('aria-expanded', 'true', { timeout: 15_000 });
}

// ── Tests ──────────────────────────────────────────────────────────────────────

test.beforeEach(async ({ page }) => {
  // UPDATE 2026-09-24 (lane sq-gwfix): this file used to opt into verbose
  // chat here so test (b) could use [data-testid="subagent-collapsed"] as
  // its thread-based signal. ADR-091 D7/D10 (66362240d) deleted that
  // component and its gate unconditionally — there is nothing left to opt
  // into verbose chat FOR. Test (b) now asserts the `delegate` tool-call
  // chip and the child's own bash call, both of which
  // src/lib/toolVisibility.ts's shouldRenderToolCall renders in the
  // DEFAULT, non-verbose thread already (ADR-091 D7/AC-7; bash foreground
  // calls are visible unconditionally too) — matching
  // replay-fidelity.spec.ts's own "no verbose-chat opt-in anywhere in this
  // file" precedent. Test (a) never depended on verbose chat either (it
  // seeds a transcript with no tool_calls at all).
  await page.goto('/');
});

test(
  '(a) Ray→Ava→Jim chain: transcript shows all three agent labels',
  // Implements #111: assistant messages from different agents show a visible label.
  // Uses transcript-seeding (same approach as replay-fidelity.spec.ts) for determinism —
  // no real LLM needed. Seeds three assistant messages with agent_ids 'ray', 'ava', 'jim'
  // then asserts that [data-testid="agent-label"] elements appear for each.
  //
  // Roster note: this chain originally used 'max', but Max was RETIRED from the
  // seeded base roster in the v0.1.0 recast (current base: Mia·Assistant, Jim·
  // Orchestrator, Ava·Builder, Ray·Scout — see pkg/coreagent/core.go::All). It now
  // uses Ava, a real base agent. Jim's seeded trust graph (coreAgentDelegation)
  // delegates to [ava, ray, worker], so a Ray→Ava→Jim multi-agent transcript is a
  // realistic handoff chain. The test only asserts the per-agent label rendering;
  // it does not drive a real LLM.
  async ({ page }) => {
    await expect(page.getByRole('banner')).toBeVisible({ timeout: 15_000 })
    await expect(chatInput(page)).toBeEnabled({ timeout: 15_000 })
    await waitForConnected(page, { timeout: 15_000 })

    // Create a session and seed a transcript with messages from three agents.
    const sessionId = await createSession(page)
    const sessionTitle = `handoff-a-labels-${Date.now()}`
    await renameSession(page, sessionId, sessionTitle)

    seedTranscript(sessionId, [
      {
        id: 'entry-user-1',
        role: 'user',
        content: 'Start a handoff chain.',
        timestamp: new Date(Date.now() - 6000).toISOString(),
        agent_id: '',
      },
      {
        id: 'entry-ray-1',
        role: 'assistant',
        content: 'Ray here. Handing off to Ava.',
        timestamp: new Date(Date.now() - 5000).toISOString(),
        agent_id: 'ray',
      },
      {
        id: 'entry-ava-1',
        role: 'assistant',
        content: 'Ava here. Handing off to Jim.',
        timestamp: new Date(Date.now() - 4000).toISOString(),
        agent_id: 'ava',
      },
      {
        id: 'entry-jim-1',
        role: 'assistant',
        content: 'Jim here. Handoff chain complete.',
        timestamp: new Date(Date.now() - 3000).toISOString(),
        agent_id: 'jim',
      },
    ])

    // Navigate to the session via its deep-link route.
    await openSession(page, sessionId)
    await waitForReplayDone(page)

    // Assert: three agent-label elements appear — one per assistant message.
    const agentLabels = page.locator('[data-testid="agent-label"]')
    await expect(agentLabels).toHaveCount(3, { timeout: 15_000 })

    // Assert: each agent's id/name appears in at least one label.
    // The label shows the agent name if known, or falls back to agent_id.
    // Ray, Ava, Jim are seeded base (core) agents whose names render from the
    // agents store; their IDs equal their lower-cased names.
    await expect(agentLabels.filter({ hasText: /ray/i })).toBeVisible()
    await expect(agentLabels.filter({ hasText: /ava/i })).toBeVisible()
    await expect(agentLabels.filter({ hasText: /jim/i })).toBeVisible()
  },
);

// REWRITTEN 2026-09-24 (lane sq-gwfix, coordinator-flagged loose end from CI
// run 35997069836's delegation-hidden.spec.ts fix).
//
// This test used to wait up to 150s (test.slow() budget 270s) for
// [data-testid="subagent-collapsed"] to appear, then click it and assert a
// nested [data-testid="tool-call-badge"] inside its expanded region — the
// child's own `bash` call rendered NESTED inside the parent's block. That
// element has zero producers left in src/ (ADR-091 D7/D10, commit
// 66362240d deleted SubagentBlock/shouldRenderSubagentSpan unconditionally)
// so the wait was guaranteed to burn its full real-LLM budget and fail —
// not a flake, a structural dead end that was also paying for two live LLM
// round-trips (parent delegate + subagent bash) on every run for nothing.
//
// BDD Scenario 1/4's underlying guarantee — "a delegation is visible, and
// the reader can see the child's own step(s) by drilling in" — is NOT gone;
// it moved. Re-pointed at the surfaces that replaced the card (same
// pattern as replay-fidelity.spec.ts's test (b)):
//   1. THREAD — the `delegate` tool-call chip is the parent's now-only
//      delegation surface (shouldRenderToolCall's `delegate` case, ADR-091
//      D7/AC-7 — visible unconditionally, not gated by verbose chat).
//   2. THREAD — zero subagent-collapsed elements, ever (the guard: proves
//      the deleted card hasn't quietly come back).
//   3. THREAD — the child's OWN bash call does NOT render in the PARENT's
//      thread (a child's frames carry the child's own session_id now, I-4
//      — nothing to nest).
//   4. PANEL — the Activity panel row for this delegation, with its open
//      control into the child's own session (ActivityPanel.tsx, ADR-091
//      D7/FR-E-004) — the surface that replaced "click the collapsed
//      header".
//   5. CHILD SESSION — opening it reveals the child's own bash tool-call
//      chip (`data-tool="bash"`) — this is where "the expanded region
//      contains ≥1 tool-call-badge" moved to. Same user-visible guarantee
//      (a reader CAN see the child's own step), different surface.
//
// Timeout re-derived, not inherited (per this lane's brief): the old 150s
// existed because the OLD flow needed the SAME two LLM round-trips to
// complete before the collapsed block could even mount — the box was
// downstream of both calls finishing. The delegate chip below needs only
// the PARENT's turn to emit the tool call, not the child's own execution —
// structurally faster, so it gets its own shorter budget, and the
// child's-own-bash-visible assertion (which DOES depend on the child
// finishing) gets a budget sized to that step alone rather than the old
// combined number.
test(
  '(b) delegate output is visible via the delegate chip and Activity panel; the child\'s own bash call is visible only in its own session',
  async ({ page }) => {
    // T0.1: OPENROUTER_API_KEY_CI soft-skip removed. The key is required in CI;
    // its absence is a CI configuration failure, not a per-test skip condition.
    // If OPENROUTER_API_KEY_CI is unset, the LLM call below will fail and the
    // test will fail honestly — which is the correct behavior.

    // 300s: sized to the two waits that actually gate this test, not
    // inherited from the old combined 270s. The delegate badge only needs
    // the PARENT's own tool-call emission (a single round trip — other
    // specs in this shard document 40-90s for that alone); the child's own
    // bash badge needs the CHILD to actually start executing, the same
    // underlying "two round trips" wait the old 150s collapsed-block
    // budget was measuring, so that one keeps a comparable number rather
    // than being guessed smaller.
    test.setTimeout(300_000);

    const input = chatInput(page);
    await expect(input).toBeVisible({ timeout: 15_000 });
    // Confirm the composer is genuinely usable (enabled AND the socket is
    // actually open, not merely queueing — toBeEnabled() alone no longer
    // implies "connected", see waitForConnected's doc comment in
    // fixtures/selectors.ts) before driving the real-LLM delegate flow
    // below. Without this, a page-load-time reconnect blip can leave the
    // composer looking usable while the first message it sends lands in
    // the outbound queue instead of the wire — the delegate call never
    // fires, and the test hangs to its full timeout waiting on a chip that
    // will never appear.
    await expect(input).toBeEnabled({ timeout: 15_000 });
    await waitForConnected(page, { timeout: 15_000 });

    // Route to Jim: the default agent Mia is a guide whose policy excludes the
    // `delegate` tool (verified in CI: `ToolSearch(load): ... Rejected: delegate — denied
    // by this agent's policy`) and whose persona declines to delegate — she answers
    // in prose offering create_task/switch_agent instead, so no delegate chip ever
    // renders. Every delegate-dependent spec switches to Jim (see subagent.spec.ts
    // startFreshChat); Jim is the general-purpose task agent and can delegate.
    await selectAgent(page, /Jim/i);

    const label = `handoff-b-${Date.now()}`;

    // Deterministic prompt: explicit tool name, exact arguments, no prose allowed.
    // The subagent's tool policy is the RESOLVED TARGET agent's policy, never the
    // parent's (pre-ADR-091: pkg/agent/subturn.go
    // StoreToolPolicy(execSource.LoadToolPolicy()), since deleted; today the
    // target's own AgentInstance/toolPolicy is what
    // steer_reconstruct.go::reconstructSteeredTurn resolves via
    // al.GetRegistry().GetAgent(rec.AgentID) and hands to newTurnState).
    // Worker (id "worker") is the only target in Jim's delegation graph whose
    // ADR-090 policy allows bash, so the delegate call pins agent_id="worker" —
    // the mandated bash call would be denied under any other target.
    await input.fill(
      [
        'Call the `delegate` tool exactly once, right now, with these arguments:',
        '  agent_id: "worker"',
        `  label: "${label}"`,
        '  task: "You are the subagent. Call the `bash` tool ONCE with action=\\"run\\" and command=\\"echo hello\\". Then reply with the single word \\"done\\". Do not use any other tool."',
        'Do not reply in prose. Do not call any other tool. Call delegate now.',
      ].join('\n'),
    );
    await input.press('Enter');

    // (1) THREAD — the delegate tool-call chip is the parent's only
    // delegation surface, visible unconditionally (ADR-091 D7/AC-7). This
    // needs only the PARENT's own turn to emit the call — no child
    // round-trip required — so it resolves fast.
    const delegateBadge = page.locator('[data-testid="tool-call-badge"][data-tool="delegate"]');
    await expect(delegateBadge.first()).toBeVisible({ timeout: 60_000 });

    // (2) THREAD — the guard: zero subagent-collapsed elements, ever.
    await expect(page.locator('[data-testid="subagent-collapsed"]')).toHaveCount(0);

    // (3) THREAD — the child's own bash call does NOT render in the
    // PARENT's thread — a child's frames carry the child's own session_id
    // now (I-4), so there is nothing to nest here regardless of how long
    // this test waits.
    await expect(page.locator('[data-testid="tool-call-badge"][data-tool="bash"]')).toHaveCount(0);

    // a11y baseline check on the delegate chip, BEFORE navigating away to
    // the child's session below (the parent's thread, delegate chip
    // included, leaves the DOM once the chat surface rebinds to the child).
    // Traces to: sprint-h-subagent-block-spec.md line 316 (Scenario 11) —
    // same accessibility guarantee SubagentBlock used to carry, re-pointed
    // at the surface that replaced it.
    await expectA11yClean(page, {
      include: ['[data-testid="tool-call-badge"]'],
    });

    // (4) PANEL — the row that replaced "click the collapsed header":
    // opens the Activity panel, finds this delegation's row, and its open
    // control into the child's own session.
    await openActivityPanel(page);
    const row = page.locator('[data-testid="activity-row"]', { hasText: label });
    await expect(row).toBeVisible({ timeout: 30_000 });
    // child_session_id arrives WITH subagent_start (steer_frames.go's
    // deliverSubagentStart sets it unconditionally at launch, not once the
    // child finishes) — the open control should already be present the
    // moment the row itself is.
    const openControl = row.locator('[data-testid="activity-row-open"]');
    await expect(openControl).toBeVisible({ timeout: 15_000 });

    // (5) CHILD SESSION — this is where "the expanded region contains
    // tool-call-badge elements" moved to. Not a `toHaveURL(/sessions\//)`
    // check: SessionRoute redirects a worker session's deep link straight
    // to its workspace chat tab (same reasoning as
    // steered-session-reachability.spec.ts's own comment on this exact
    // seam) — the reliable "which session is this chat surface bound to
    // now" signal is ChatScreen's own data-active-session-id attribute.
    const parentSurface = page.locator('[data-active-session-id]').first();
    const parentSessionID = await parentSurface.getAttribute('data-active-session-id');
    await openControl.click();
    const boundSurface = page.locator('[data-active-session-id]').first();
    await expect
      .poll(
        async () => {
          const id = await boundSurface.getAttribute('data-active-session-id');
          return id && id !== parentSessionID && id !== '__pending' ? id : null;
        },
        { timeout: 15_000 },
      )
      .not.toBeNull();

    // The child's own bash call, visible in its OWN session (bash is
    // foreground-by-default and therefore unconditionally visible per
    // toolVisibility.ts — no verbose-chat opt-in needed here either).
    const childBashBadge = page.locator('[data-testid="tool-call-badge"][data-tool="bash"]');
    await expect(
      childBashBadge.first(),
      'the child\'s own bash call must be visible in the child\'s own session — this is where the deleted nested-tool-call-badge assertion moved to',
    ).toBeVisible({ timeout: 150_000 });

    // a11y baseline check on the child's own bash chip, now that the chat
    // surface is bound to the child's session — completes the Scenario 11
    // coverage the two checks in this test now split across both surfaces.
    await expectA11yClean(page, {
      include: ['[data-testid="tool-call-badge"]'],
    });
  },
);

// (c) 6th-handoff refusal — DELETED.
// The concept no longer exists in Omnipus. Per owner decision (2026-04-20, documented
// in Sprint H / Plan 3 §1 reversal), handoffs are 1-level only. There is no "chain"
// or "depth limit" to refuse — the second-handoff refusal invariant is tested
// deterministically at the Go tool layer:
//   - pkg/gateway/handoff_summary_test.go :: TestHandoff_RejectsSecondHandoffInSession
//   - pkg/tools/handoff_test.go :: TestHandoffTool_RejectsSecondHandoff
// A Playwright placeholder for a deleted concept is dead code; removed.

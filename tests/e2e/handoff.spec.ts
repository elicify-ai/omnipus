import * as fs from 'fs'
import * as path from 'path'
import { expect, type Page } from '@playwright/test';
import { test } from './fixtures/console-errors';
import { expectA11yClean } from './fixtures/a11y';
import { chatInput, selectAgent, waitForConnected } from './fixtures/selectors';
import { enableVerboseChat } from './fixtures/verbose-chat';

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

// ── Tests ──────────────────────────────────────────────────────────────────────

test.beforeEach(async ({ page }) => {
  // Delegation visuals (SubagentBlock cards) are verbose-only in the chat
  // thread since commit 8e1bf1b9 (shouldRenderSubagentSpan gates on
  // verboseChatEnabled, default false — src/store/chatPreferences.ts). Test
  // (b) below asserts the sub-turn MECHANICS (collapsed→expanded, nested
  // tool-call badges) via [data-testid="subagent-collapsed"] as its
  // thread-based signal, so this file opts into verbose chat to keep that
  // signal working — independent of the default (non-verbose) display
  // policy, which is covered separately by delegation-hidden.spec.ts.
  await enableVerboseChat(page);
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

// BDD Scenario 1 (sprint-h-subagent-block-spec.md line 207):
//   Given the chat view is mounted on a live session
//   And the assistant issues a delegate tool call with label="audit go files"
//   When the backend fires EventKindSubTurnSpawn
//   Then [data-testid="subagent-collapsed"] appears
//   And clicking it reveals [data-testid="subagent-expanded"]
//   And the expanded region contains ≥1 [data-testid="tool-call-badge"] (FR-H-008)
//
// BDD Scenario 4 (line 241):
//   Given a collapsed SubagentBlock with 2 nested tool calls
//   When the user clicks the collapsed header
//   Then [data-testid="subagent-expanded"] is rendered
//   And the expanded region contains tool-call-badge elements
//
// Traces to: sprint-h-subagent-block-spec.md TDD row 20, SC-H-001
test(
  '(b) collapsed subagent display: delegate output renders as collapsed block, expandable',
  async ({ page }) => {
    // T0.1: OPENROUTER_API_KEY_CI soft-skip removed. The key is required in CI;
    // its absence is a CI configuration failure, not a per-test skip condition.
    // If OPENROUTER_API_KEY_CI is unset, the LLM call below will fail and the
    // test will fail honestly — which is the correct behavior.

    // test.slow() triples the global 90s test timeout to 270s. Real-LLM
    // delegate under suite load occasionally takes 40-60s; the test passes
    // in 5-15s alone.
    test.slow();

    const input = chatInput(page);
    await expect(input).toBeVisible({ timeout: 15_000 });
    // Confirm the composer is genuinely usable (enabled AND the socket is
    // actually open, not merely queueing — toBeEnabled() alone no longer
    // implies "connected", see waitForConnected's doc comment in
    // fixtures/selectors.ts) before driving the real-LLM delegate flow
    // below. Without this, a page-load-time reconnect blip can leave the
    // composer looking usable while the first message it sends lands in
    // the outbound queue instead of the wire — the delegate call never
    // fires, and the test hangs to its full test.slow() timeout waiting on
    // a subagent-collapsed block that will never appear.
    await expect(input).toBeEnabled({ timeout: 15_000 });
    await waitForConnected(page, { timeout: 15_000 });

    // Route to Jim: the default agent Mia is a guide whose policy excludes the
    // `delegate` tool (verified in CI: `ToolSearch(load): ... Rejected: delegate — denied
    // by this agent's policy`) and whose persona declines to delegate — she answers
    // in prose offering create_task/hand_off instead, so no SubagentBlock ever
    // renders. Every delegate-dependent spec switches to Jim (see subagent.spec.ts
    // startFreshChat); Jim is the general-purpose task agent and can delegate.
    await selectAgent(page, /Jim/i);

    // Deterministic prompt: explicit tool name, exact arguments, no prose allowed.
    // temperature=0 + seed=42 are plumbed into OpenRouter requests for determinism.
    // The delegated subagent inherits Jim's toolset (which includes `bash`); it calls
    // `bash` once, producing the tool_call_badge the expanded block asserts.
    await input.fill(
      [
        'Call the `delegate` tool exactly once, right now, with these arguments:',
        '  label: "handoff-b test"',
        '  task: "You are the subagent. Call the `bash` tool ONCE with action=\\"run\\" and command=\\"echo hello\\". Then reply with the single word \\"done\\". Do not use any other tool."',
        'Do not reply in prose. Do not call any other tool. Call delegate now.',
      ].join('\n'),
    );
    await input.press('Enter');

    // Wait up to 150s for a subagent-collapsed block to appear under
    // real-LLM determinism — delegate requires two LLM round-trips (parent
    // tool call + subagent execution). google/gemini-2.5-flash's combined
    // round-trip latency can still exceed 90s under OpenRouter load (the
    // generous budget below stays safe; gemini is generally faster than the
    // prior model). test.slow() gives 270s total; 150s here leaves
    // 120s for the click + expand assertions below.
    // Structural assertion: if no delegate occurred the test fails honestly.
    const collapsedBlock = page.locator('[data-testid="subagent-collapsed"]');
    await expect(collapsedBlock).toBeVisible({ timeout: 150_000 });

    // Assert: at least one collapsed block is present with correct structure.
    const blockCount = await collapsedBlock.count();
    expect(blockCount, 'at least one SubagentBlock must be rendered').toBeGreaterThanOrEqual(1);

    // BDD Scenario 4: click the collapsed header → expanded region appears.
    await collapsedBlock.first().click();

    const expandedBlock = page.locator('[data-testid="subagent-expanded"]');
    await expect(expandedBlock).toBeVisible({ timeout: 10_000 });

    // Assert: expanded block has at least one tool-call-badge (the subagent called shell).
    // Structural assertion: checks [data-testid="tool-call-badge"] presence.
    // 60s budget: the subagent's first LLM round-trip (tool-call emission) can
    // take 10-50s under suite load on google/gemini-2.5-flash; the previous
    // 10s budget was tighter than the LLM's documented latency floor.
    const toolCallBadges = expandedBlock.locator('[data-testid="tool-call-badge"]');
    await expect(toolCallBadges.first()).toBeVisible({ timeout: 60_000 });

    // a11y baseline check on subagent elements (BDD Scenario 11, FR-H-008).
    // Traces to: sprint-h-subagent-block-spec.md line 316 (Scenario 11)
    await expectA11yClean(page, {
      include: ['[data-testid^="subagent-"]'],
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

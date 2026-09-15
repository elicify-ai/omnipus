/**
 * agent-grep.spec.ts — E2E proof for the agent-facing `grep` tool
 * (docs/internal/specs/unified-search-and-grep-spec.md, US-3 "Best-in-class
 * agent grep tool" — spec test 35: "e2e agent-grep flow ... real turn with
 * context lines").
 *
 * Regression class caught:
 *   Unit/integration tests (pkg/tools/grep_test.go, pkg/agent/
 *   grep_execute_policy_test.go) already prove the tool's wiring, argument
 *   handling, and rendering in isolation. This test is the only gate that
 *   proves the FULL chain under a real LLM and a real browser: the model
 *   must actually choose to call `grep` (not merely be told it exists), the
 *   gateway must actually execute it against a real seeded file, and the
 *   SPA must actually render the tool call AND the result content the model
 *   read back into its reply — including the context lines the request
 *   asked for.
 *
 * Harness pattern: closely mirrors memory-remember-recall.spec.ts (same
 * auth/API helpers, same disk-seeding-before-prompting approach, same
 * "resolve the default agent, resolve the active workspace, seed BOTH
 * candidate roots since e2e chat may or may not be workspace-scoped"
 * defensive strategy, same waitForTurnFullyDone follow-up-turn handling).
 *
 * Verification is two-layered, deliberately:
 *   1. A DETERMINISTIC proof the tool really ran: the tool-call badge
 *      (`[data-testid="tool-call-badge"][data-tool="grep"]`, the same
 *      selector convention tool-order.spec.ts establishes) is visible —
 *      independent of whatever the model chooses to say afterward.
 *   2. A content proof: the assistant's reply contains the matched line's
 *      nonce and at least one of the context-line tokens grep only returns
 *      when `context_lines` was actually honored — same content-fidelity
 *      pattern memory-remember-recall.spec.ts already uses for recall_memory.
 */

import { test, expect, type Page } from '@playwright/test';
import * as fs from 'fs';
import * as path from 'path';
import { startNewChat, waitForConnected } from './fixtures/selectors';

// ── Constants ─────────────────────────────────────────────────────────────────

const BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6062';

const OMNIPUS_HOME =
  process.env.OMNIPUS_HOME ||
  (process.env.HOME ? path.join(process.env.HOME, '.omnipus') : '/tmp/omnipus-e2e-test');

// Unique nonce for this test run (Date.now(), matching memory-remember-recall's
// own reasoning: no seeding issues in Playwright's Node environment).
const NONCE = `GREPTEST-${Date.now()}`;
const NEEDLE_LINE = `NEEDLE-${NONCE}`;
const ABOVE1_LINE = `ABOVE1-${NONCE}`;
const ABOVE2_LINE = `ABOVE2-${NONCE}`;
const BELOW1_LINE = `BELOW1-${NONCE}`;
const BELOW2_LINE = `BELOW2-${NONCE}`;
const SEED_FILE_NAME = 'e2e-grep-haystack.txt';

// ── Auth helpers (same pattern as memory-remember-recall.spec.ts / retention.spec.ts) ──

const AUTH_FILE = process.env.OMNIPUS_AUTH_FILE
  ? path.resolve(process.env.OMNIPUS_AUTH_FILE)
  : path.join(path.dirname(new URL(import.meta.url).pathname), 'fixtures/.auth/admin.json');

function getStoredAuthToken(): string | null {
  if (!fs.existsSync(AUTH_FILE)) return null;
  try {
    const raw = fs.readFileSync(AUTH_FILE, 'utf-8');
    const state = JSON.parse(raw) as {
      origins?: Array<{ origin: string; localStorage?: Array<{ name: string; value: string }> }>;
    };
    for (const origin of state.origins ?? []) {
      for (const item of origin.localStorage ?? []) {
        if (item.name === 'omnipus_auth_token') return item.value;
      }
    }
  } catch {
    // Auth file may not exist on first run
  }
  return null;
}

async function apiHeaders(page: Page): Promise<Record<string, string>> {
  const token = getStoredAuthToken();
  const cookies = await page.context().cookies();
  const csrf = cookies.find((c) => c.name === '__Host-csrf' || c.name === 'csrf')?.value ?? null;
  return {
    'Content-Type': 'application/json',
    ...(token ? { Authorization: `Bearer ${token}` } : {}),
    ...(csrf ? { 'X-CSRF-Token': csrf } : {}),
  };
}

// ── DOM helpers (same selectors as memory-remember-recall.spec.ts) ────────────

const chatInput = (page: Page) => page.locator('textarea[aria-label="Message input"]');

const assistantMessages = (page: Page) =>
  page.locator('[data-message-id]:not(.flex-row-reverse):not([data-status="running"])');

/**
 * Wait for any follow-up LLM call the agent starts after a tool call — the
 * stop button reappearing within gapMs signals a second in-flight LLM call
 * (mirrors memory-remember-recall.spec.ts's identically-named helper).
 */
async function waitForTurnFullyDone(page: Page, gapMs = 10_000): Promise<void> {
  const stopBtn = page.locator('[data-testid="stop-btn"]');
  try {
    await expect(stopBtn).toBeVisible({ timeout: gapMs });
    await expect(stopBtn).not.toBeVisible({ timeout: 180_000 });
  } catch {
    // Stop button did not reappear within gapMs — turn is fully done.
  }
}

// ── Default agent / workspace resolvers (same pattern as memory-remember-recall.spec.ts) ──

async function resolveDefaultAgentId(page: Page): Promise<string | null> {
  const resp = await page.request.get(`${BASE_URL}/api/v1/agents`, {
    headers: await apiHeaders(page),
    failOnStatusCode: false,
  });
  if (!resp.ok()) return null;
  const agents = (await resp.json()) as Array<{ id: string; default?: boolean; name?: string }>;
  const def = agents.find((a) => a.default === true);
  if (def) return def.id;
  const mia = agents.find((a) => /mia/i.test(a.name ?? ''));
  return mia?.id ?? null;
}

async function resolveWorkspaceId(page: Page): Promise<string | null> {
  try {
    const resp = await page.request.get(`${BASE_URL}/api/v1/workspaces`, {
      headers: await apiHeaders(page),
      failOnStatusCode: false,
    });
    if (resp.ok()) {
      const workspaces = (await resp.json()) as Array<{
        id: string;
        is_default?: boolean;
        status?: string;
      }>;
      if (Array.isArray(workspaces) && workspaces.length > 0) {
        const def = workspaces.find((w) => w.is_default === true);
        if (def?.id) return def.id;
        const first = workspaces.find((w) => !w.status || w.status === 'active') ?? workspaces[0];
        if (first?.id) return first.id;
      }
    }
  } catch {
    // Network error — fall through to null; the disk seed still covers the
    // agent-private root.
  }
  return null;
}

/**
 * Seed the haystack file into EVERY location grep might actually search,
 * mirroring resolveWorkspaceId's own justification: an e2e chat session MAY
 * be workspace-scoped (TurnWorkspaceDir re-roots grep into
 * $OMNIPUS_HOME/workspaces/<id>/work/) or not (grep falls back to the
 * agent's own fixed home, $OMNIPUS_HOME/agents/<id>/) — this test does not
 * assume which, it covers both so the assertion is about grep's real
 * behavior, not this harness's guess at routing.
 */
function seedHaystack(agentId: string, workspaceId: string | null): string[] {
  const content =
    [ABOVE2_LINE, ABOVE1_LINE, NEEDLE_LINE, BELOW1_LINE, BELOW2_LINE].join('\n') + '\n';

  const dirs = [path.join(OMNIPUS_HOME, 'agents', agentId)];
  if (workspaceId) {
    dirs.push(path.join(OMNIPUS_HOME, 'workspaces', workspaceId, 'work'));
  }

  const written: string[] = [];
  for (const dir of dirs) {
    fs.mkdirSync(dir, { recursive: true });
    const filePath = path.join(dir, SEED_FILE_NAME);
    fs.writeFileSync(filePath, content, 'utf-8');
    written.push(filePath);
  }
  return written;
}

// ── The test ──────────────────────────────────────────────────────────────────

test(
  'agent grep tool finds a seeded match with context lines',
  async ({ page }) => {
    // Budget: app load(15) + agent/workspace resolution(10) + turn(120) +
    // assertions(15) = 160s -> 180s with margin.
    test.setTimeout(180_000);

    // ── Arrange ──────────────────────────────────────────────────────────────
    await page.goto('/');
    await expect(page.getByRole('banner')).toBeVisible({ timeout: 20_000 });

    const agentId = await resolveDefaultAgentId(page);
    if (!agentId) {
      throw new Error(
        'BLOCKED: Could not resolve the default agent ID from GET /api/v1/agents. ' +
          'The gateway may not be seeded correctly (coreagent.SeedConfig seeds Mia as default). ' +
          'Seeding the grep haystack file requires knowing which agent ID to write into.',
      );
    }
    const workspaceId = await resolveWorkspaceId(page);

    console.log(`[agent-grep] agentId=${agentId} workspaceId=${workspaceId ?? '(none)'}`);

    const seededPaths = seedHaystack(agentId, workspaceId);

    console.log(`[agent-grep] Seeded haystack at: ${seededPaths.join(', ')}`);

    await startNewChat(page);
    await expect(assistantMessages(page)).toHaveCount(0, { timeout: 10_000 });

    const input = chatInput(page);
    await expect(input).toBeEnabled({ timeout: 15_000 });
    await waitForConnected(page, { timeout: 15_000 });

    // ── The turn ─────────────────────────────────────────────────────────────
    //
    // BDD (US-3 AS-1, AS-4):
    //   Given a file containing a unique pattern
    //   When the user forcefully instructs the agent to grep for it with
    //     context_lines 2
    //   Then the assistant emits exactly one grep tool call
    //   And its reply contains the matched line and at least one context line
    //
    // Prompt strategy: short, unique, single-token line markers (rather than
    // natural-language sentences) so a verbatim-copy instruction is easy for
    // the model to satisfy exactly, matching memory-remember-recall.spec.ts's
    // own "forceful, single-purpose imperative" convention for glm-class
    // models.
    await input.fill(
      `Call the grep tool NOW with pattern "${NEEDLE_LINE}" and context_lines 2. ` +
        `Then reply with ONLY the matched line and its four surrounding context lines, ` +
        `copied verbatim from the tool result, one per line. Call grep immediately.`,
    );
    await input.press('Enter');

    await expect(assistantMessages(page)).toHaveCount(1, { timeout: 120_000 });
    await waitForTurnFullyDone(page, 10_000);

    // ── Assert (1): the grep TOOL actually ran — deterministic, independent
    // of anything the model chose to say afterward.
    await expect(
      page.locator('[data-testid="tool-call-badge"][data-tool="grep"]').first(),
      'expected a visible grep tool-call badge — the model must have actually ' +
        'invoked the grep tool, not merely described doing so',
    ).toBeVisible({ timeout: 5_000 });

    // ── Assert (2): content fidelity — the matched line's nonce reached the
    // rendered reply.
    const msgsWithNeedle = assistantMessages(page).filter({ hasText: NEEDLE_LINE });
    const needleCount = await msgsWithNeedle.count();
    if (needleCount === 0) {
      const allMsgs = assistantMessages(page);
      const msgCount = await allMsgs.count();
      const msgTexts: string[] = [];
      for (let i = 0; i < msgCount; i++) {
        try {
          msgTexts.push((await allMsgs.nth(i).textContent()) ?? '(empty)');
        } catch {
          msgTexts.push('(could not read)');
        }
      }
      throw new Error(
        [
          `BLOCKED or INCOMPLETE: matched line "${NEEDLE_LINE}" does NOT appear in any`,
          `  completed assistant message after grepping for it.`,
          '',
          `  Seeded haystack file(s): ${seededPaths.join(', ')}`,
          `  (a visible grep tool-call badge WAS asserted above — the tool did run;`,
          `   the gap, if any, is in the tool's real, or the model's echo of it)`,
          '',
          `  Assistant messages (${msgCount} total):`,
          ...msgTexts.map((t, i) => `    [${i}] ${t.slice(0, 300)}`),
          '',
          'Traces to: pkg/tools/grep.go (GrepTool.Execute / renderGrepResult).',
        ].join('\n'),
      );
    }

    // ── Assert (3): at least one context line made it into the reply too —
    // proof `context_lines` was actually honored, not merely accepted and
    // ignored. Soft in WHICH token (the model may echo any subset), hard in
    // that at least one must appear.
    const contextTokens = [ABOVE1_LINE, ABOVE2_LINE, BELOW1_LINE, BELOW2_LINE];
    let sawContext = false;
    for (const token of contextTokens) {
      const count = await assistantMessages(page).filter({ hasText: token }).count();
      if (count > 0) {
        sawContext = true;
        break;
      }
    }
    expect(
      sawContext,
      `expected at least one context-line token (${contextTokens.join(', ')}) in the ` +
        'reply — context_lines: 2 was requested; if none appear, either the tool did not ' +
        'honor context_lines or the model did not echo any context back',
    ).toBe(true);
  },
);

/**
 * recap-continuity.spec.ts — recap → last-session.md → next-session injection
 * continuity, on the CURRENT trigger.
 *
 * PORT (delta-review R2 gap 6). The former recap-continuity.spec.ts was deleted
 * (d491e68b7, S5 M5) because it drove the recap with a WS `session_close` frame,
 * RETIRED by DEL-08 — with that trigger gone the spec could not pass at all, and
 * only Go tests remained (config toggle, idle ticker, last-session.md,
 * next-session injection). This is the end-to-end replacement: the SAME
 * assertions, driven through the trigger the product actually has — the
 * per-session IDLE TICKER (pkg/agent/loop.go → CloseSession("idle") →
 * runRecap → WriteLastSession). No `session_close` frame anywhere.
 *
 * WHAT THIS CATCHES
 * -----------------
 * The "agent remembers last session" feature is three independent pipes wired
 * together: the idle ticker firing, the recap pipeline writing
 * agents/<id>/.omnipus/last-session.md, and the NEXT session's context builder
 * injecting that file. Every Go test covers one pipe in isolation; nothing
 * covers the join across the real gateway — a break anywhere in the chain
 * (a ticker that never fires, a writer pointed at another directory, an
 * injector that reads the wrong agent's file) is invisible to all of them.
 *
 * BDD:
 *   Given auto_recap_enabled is true and the idle timeout is one minute
 *   And  a fresh chat with the agent the sidebar has selected
 *
 *   When the user states a distinctive fact the agent would not otherwise know
 *   And  the session sits idle past the idle timeout
 *   Then last-session.md under agents/<THAT agent>/.omnipus/ is written and
 *        carries the fact
 *
 *   When the user opens a NEW chat with the same agent
 *   And  asks a question only answerable from that recap
 *   Then the agent's reply contains the fact
 *   This proves last-session.md was injected into the new session's context.
 *
 * Traces to: BRD FR-019 (auto-recap), FR-023 (session-end recap pipeline),
 * FR-028/FR-030 (continuity), pkg/agent/idle_timeout_seam_test.go (the Go half
 * of the same chain), pkg/memrooms/rooms.go (last-session.md path).
 *
 * S5 fixes applied here:
 *   S5-01 — the roster is a BARE ARRAY (contracts listAgents;
 *           rest_agents.go::listAgents), not `{agents: [...]}`. The agent id no
 *           longer comes from a hand-rolled roster read at all: `freshChat`
 *           (fixtures/session-setup.ts) resolves it the way the SPA does.
 *   S5-02 — every mutating request carries X-CSRF-Token, read from the page's
 *           own cookie jar (the sibling's pageCsrfHeaders rule). The afterEach
 *           restore no longer swallows its failure: a swallowed restore leaks a
 *           one-minute idle timeout into every later spec in the shard.
 *   S5-03 — the recap file is polled for the agent of the session THIS test
 *           opened, not the first roster entry, which need not be the page's
 *           selected agent.
 */

import { test, expect, type Page } from '@playwright/test';
import * as fs from 'fs';
import * as path from 'path';
import { chatInput, assistantMessages } from './fixtures/selectors';
import { freshChat, originOf } from './fixtures/session-setup';

const OMNIPUS_HOME = process.env.OMNIPUS_HOME || path.join(process.env.HOME || '', '.omnipus');

/** One idle timeout of margin on top of the ticker's own minute. */
const IDLE_TIMEOUT_MINUTES = 1;
const IDLE_WAIT_MS = (IDLE_TIMEOUT_MINUTES + 1) * 60_000;
/** The recap pipeline's own overall budget, plus margin (see pollLastSession). */
const RECAP_BUDGET_MS = 120_000;

interface MemorySettingsWire {
  auto_recap_enabled?: boolean;
  idle_timeout_minutes?: number;
  bootstrap_recap_enabled?: boolean;
  bootstrap_recap_max_per_minute?: number;
  session_days?: number;
  memory_retros_days?: number;
}

/**
 * The CSRF token for this page's own session, from the page's cookie jar —
 * the same rule fixtures/session-setup.ts::pageCsrfHeaders applies, so a
 * mutating request from a spec is authorised exactly like one from the SPA.
 * Omitting it is what S5-02 caught: the PUT was rejected, and the swallowed
 * restore then leaked the settings it was meant to undo.
 */
async function pageCsrfHeaders(page: Page): Promise<Record<string, string>> {
  const cookies = await page.context().cookies();
  const csrfCookie = cookies.find((c) => c.name === '__Host-csrf' || c.name === 'csrf');
  return csrfCookie ? { 'X-CSRF-Token': csrfCookie.value } : {};
}

async function putMemorySettings(page: Page, body: MemorySettingsWire): Promise<void> {
  const resp = await page.request.put(`${originOf(page)}/api/v1/settings/memory`, {
    headers: { 'Content-Type': 'application/json', ...(await pageCsrfHeaders(page)) },
    data: JSON.stringify(body),
  });
  expect(
    resp.ok(),
    `PUT /api/v1/settings/memory failed: ${resp.status()} ${await resp.text()}`,
  ).toBe(true);
}

/**
 * Poll for the recap file of THIS test's agent. The budget is deliberately
 * larger than the recap pipeline's own overall cap: a budget equal to that cap
 * races the pipeline with zero margin even on the successful path.
 */
async function pollLastSession(page: Page, agentId: string, needle: string): Promise<string> {
  const lastSessionPath = path.join(OMNIPUS_HOME, 'agents', agentId, '.omnipus', 'last-session.md');
  const deadline = Date.now() + RECAP_BUDGET_MS;
  let lastContents = '';
  while (Date.now() < deadline) {
    if (fs.existsSync(lastSessionPath)) {
      lastContents = fs.readFileSync(lastSessionPath, 'utf-8');
      if (lastContents.includes(needle)) return lastContents;
    }
    await page.waitForTimeout(2_000);
  }
  throw new Error(
    `recap-continuity: last-session.md did not carry the fact within ${RECAP_BUDGET_MS / 1000}s.\n` +
      `Path: ${lastSessionPath}\n` +
      (fs.existsSync(lastSessionPath)
        ? `The file exists but does not contain ${needle}. Last contents:\n${lastContents.slice(0, 2000)}`
        : 'The file was never written — the idle ticker did not fire, or the recap pipeline did not run.'),
  );
}

let baseline: MemorySettingsWire | null = null;

test.beforeEach(async ({ page }) => {
  // PR #1260 (E2E ui-heavy): the hook read the gateway settings BEFORE any
  // navigation, so the page was still on about:blank and originOf() refused —
  // every attempt failed at line 131 with a message that (misleadingly) named
  // freshChat. Playwright hands a test a blank page; open the app first, the
  // same way every spec that reads the page's own origin does.
  await page.goto('/');
  const resp = await page.request.get(`${originOf(page)}/api/v1/settings/memory`, {
    headers: await pageCsrfHeaders(page),
  });
  expect(resp.ok(), `GET /api/v1/settings/memory failed: ${resp.status()}`).toBe(true);
  baseline = (await resp.json()) as MemorySettingsWire;
});

test.afterEach(async ({ page }) => {
  if (!baseline) return;
  const b = baseline;
  baseline = null;
  // NOT swallowed (S5-02): these are GLOBAL gateway settings, and a failed
  // restore leaves a one-minute idle timeout in force for every later spec
  // sharing this shard's gateway. A restore failure must fail loudly here.
  await putMemorySettings(page, {
    auto_recap_enabled: b.auto_recap_enabled ?? false,
    idle_timeout_minutes: b.idle_timeout_minutes ?? 30,
    bootstrap_recap_enabled: b.bootstrap_recap_enabled ?? false,
    bootstrap_recap_max_per_minute: b.bootstrap_recap_max_per_minute ?? 5,
    session_days: b.session_days ?? 90,
    memory_retros_days: b.memory_retros_days ?? 180,
  });
});

test(
  'recap_continuity: a fact survives into the next session via last-session.md injection',
  async ({ page }) => {
    // Worst case: setup ~20s, turn 1 ~90s, the idle wait itself
    // (IDLE_WAIT_MS = 120s), the recap poll 120s, new-session setup ~30s,
    // turn 2 ~90s = ~470s. 900s leaves real headroom for CI jitter.
    test.setTimeout(900_000);

    await putMemorySettings(page, {
      auto_recap_enabled: true,
      idle_timeout_minutes: IDLE_TIMEOUT_MINUTES,
    });

    // The app is already open (test.beforeEach navigated to '/' so the
    // settings read could resolve the page's origin); this is the readiness
    // assertion, kept as it was.
    await expect(chatInput(page)).toBeVisible({ timeout: 20_000 });

    // The session this test drives, and the agent it belongs to. `freshChat`
    // resolves the agent the SPA has selected and opens a real chat for it, so
    // the recap file polled below belongs to THIS session's agent (S5-03).
    const first = await freshChat(page);

    // A nonce no model could produce from its own priors: the only way it can
    // reach the second session is through last-session.md.
    const nonce = `recap-nonce-${Date.now()}`;
    const input = chatInput(page);
    await input.fill(`Remember for later: the launch codename is ${nonce}.`);
    await input.press('Enter');
    await expect(assistantMessages(page)).toHaveCount(1, { timeout: 90_000 });

    // ── The idle ticker, which is the ONLY recap trigger since DEL-08 ────────
    await page.waitForTimeout(IDLE_WAIT_MS);
    await pollLastSession(page, first.agentId, nonce);

    // ── A NEW chat with the same agent: the recap must be injected ──────────
    const second = await freshChat(page, first.agentName);
    expect(second.sessionId).not.toBe(first.sessionId);
    await expect(assistantMessages(page)).toHaveCount(0, { timeout: 15_000 });
    const fresh = chatInput(page);
    await fresh.fill('What was the launch codename I told you about? Answer with just the codename.');
    await fresh.press('Enter');
    await expect(assistantMessages(page)).toHaveCount(1, { timeout: 90_000 });
    await expect(assistantMessages(page).first()).toContainText(nonce, { timeout: 90_000 });
  },
);

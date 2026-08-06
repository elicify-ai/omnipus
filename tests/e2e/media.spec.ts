import * as fs from 'fs';
import * as path from 'path';
import { expect, type Page } from '@playwright/test';
import { test } from './fixtures/console-errors';
import { expectA11yClean } from './fixtures/a11y';
import { chatInput, agentPicker, assistantMessages, selectAgent } from './fixtures/selectors';

// Global storageState provides pre-authenticated session (see playwright.config.ts + global-setup.ts).

const BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6060';

const OMNIPUS_HOME =
  process.env.OMNIPUS_HOME ||
  (process.env.HOME ? path.join(process.env.HOME, '.omnipus') : '/tmp/omnipus-e2e-test');

/**
 * Resolve the active workspace ID so a fixture file can be placed inside its
 * work/ directory (the ADR-046 P1 per-turn filesystem re-root every CoreTeam
 * agent's tools are confined to — pkg/workspace/instructions.go WorkDir() =
 * `<OMNIPUS_HOME>/workspaces/<id>/work`). Mirrors
 * memory-remember-recall.spec.ts's resolveWorkspaceId — duplicated locally
 * per this suite's established "shared harness pattern, not extracted to a
 * fixture" convention (see waitForTurnFullyDone's doc comment above for the
 * same rationale applied to a different helper).
 *
 * Strategy: GET /api/v1/workspaces first (dev_mode_bypass admin session rides
 * along via cookies), falling back to a disk scan of
 * $OMNIPUS_HOME/workspaces/ if the API is unreachable.
 */
async function resolveWorkspaceId(page: Page): Promise<string> {
  try {
    const resp = await page.request.get(`${BASE_URL}/api/v1/workspaces`, {
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
    // Network error — fall through to disk scan.
  }

  const workspacesDir = path.join(OMNIPUS_HOME, 'workspaces');
  if (fs.existsSync(workspacesDir)) {
    const wsDirs = fs
      .readdirSync(workspacesDir, { withFileTypes: true })
      .filter((e) => e.isDirectory())
      .map((e) => e.name);
    if (wsDirs.length > 0) return wsDirs[0];
  }

  throw new Error(
    'BLOCKED: could not resolve an active workspace ID via GET /api/v1/workspaces or a disk ' +
      `scan of ${workspacesDir}. send_file needs a workspace-scoped work/ directory to deliver ` +
      'its fixture from — see resolveWorkspaceId in this file.',
  );
}

/**
 * Wait for any follow-up LLM call after the current assistant message settles.
 *
 * Mirrors waitForTurnFullyDone() from chat.spec.ts / memory-remember-recall.spec.ts /
 * recap-continuity.spec.ts (shared harness pattern, not extracted to a fixture —
 * see those files for the same copy). Root cause: the prompt below asks the
 * model for TWO sequential tool calls (browser_navigate, then browser_screenshot).
 * Each individual LLM completion segment can end with done:true — including the
 * one right after browser_navigate, BEFORE browser_screenshot has even been
 * called — which briefly flips isStreaming to false and makes data-status
 * "complete" appear on the assistant message. The `assistantMessages` count
 * assertion below only checks data-status !== "running", so it can resolve on
 * that premature mid-turn blip, long before the screenshot tool has run and the
 * media frame exists. Without this guard, the subsequent img assertion's 90s
 * budget starts from that early point instead of from real turn completion,
 * silently eating into the time glm-5.2 has to finish the second tool call.
 */
async function waitForTurnFullyDone(page: Page, gapMs = 8_000): Promise<void> {
  const stopBtn = page.locator('[data-testid="stop-btn"]');
  try {
    // If the stop button reappears within gapMs, a follow-up LLM call is in progress.
    await expect(stopBtn).toBeVisible({ timeout: gapMs });
    // Stop button appeared — wait for it to vanish (follow-up LLM call done).
    await expect(stopBtn).not.toBeVisible({ timeout: 180_000 });
  } catch {
    // Stop button did not reappear within gapMs — the turn is fully done.
  }
}

test.beforeEach(async ({ page }) => {
  await page.goto('/');
});

// (a) screenshot test was updated for the v0.1.0-foundation 4-base roster.
// Max was retired per the .preview-doc/ concept (see pkg/coreagent/core.go).
// Switched to Mia (the Assistant), the canonical default agent, which has the
// web_search / browser tools and is what an end-user would actually use.
test(
  '(a) screenshot inline render: Mia screenshots example.com and renders an img',
  async ({ page }) => {
    // The screenshot flow exercises the LLM (tool selection) + Chromium
    // (screenshot) + SPA (media render). glm-5.2 (the standard e2e model) is
    // reliable but slower than the old gemini pick, so use an explicit generous
    // budget rather than the 270s test.slow() ceiling.
    //
    // Budget (WORST-CASE ceiling, recomputed the same way chat.spec.ts (b) /
    // recap-continuity.spec.ts were fixed: test.setTimeout is a hard governor,
    // so it must sum every step's own worst-case timeout, INCLUDING the
    // waitForTurnFullyDone leg added below for the browser_navigate ->
    // browser_screenshot two-tool-call race):
    //   Agent picker + Jim selection + input visibility ................ ~35s
    //   assistantMessages toHaveCount ceiling ........................... 180s
    //   waitForTurnFullyDone: gapMs(8s) + follow-up-call ceiling(180s) .. 188s
    //   mediaImg toBeVisible ceiling ...................................... 90s
    //   a11y scan ......................................................... 10s
    // Worst-case total: 35 + 180 + 188 + 90 + 10 = 503s.
    // Previous budget (420s) undercounted this the same way chat.spec.ts's old
    // budget did: it never accounted for the mid-turn done:true blip between
    // the two tool calls, so a real (if slow) completion could still hit the
    // outer test timeout.
    // New budget: 503s worst-case ceiling + ~19% margin for CI scheduling
    // jitter = 600s (10 min), a round number with real headroom.
    test.setTimeout(600_000);
    // Select Jim — the Planner & Orchestrator. Jim is the generalist *doer* with
    // browser automation in his soul; Mia is a router persona that (correctly)
    // hands browser tasks off rather than executing them, so she is the wrong
    // agent for a "do it now" screenshot. (Ray, the Scout, also has the browser
    // tools — either doer works; Jim is the canonical generalist.)
    const picker = agentPicker(page);
    await expect(picker).toBeVisible({ timeout: 15_000 });
    await picker.click();

    // Find Jim in the dropdown items (Radix DropdownMenuItem)
    const jimItem = page.locator('[role="menuitem"]').filter({ hasText: /jim/i }).first();
    await expect(jimItem).toBeVisible({ timeout: 10_000 });
    await jimItem.click();

    const input = chatInput(page);
    await expect(input).toBeVisible({ timeout: 10_000 });

    const countBefore = await assistantMessages(page).count();
    // Explicit, single-tool instruction — mirrors the reliable phrasing used by
    // the delegate-based specs so glm-5.2 takes the screenshot itself instead of
    // narrating or delegating.
    await input.fill(
      'Use the browser tools to take a screenshot of https://example.com and show it to me. ' +
        'Call browser_navigate then browser_screenshot yourself — do not delegate this.',
    );
    await input.press('Enter');

    await expect(assistantMessages(page)).toHaveCount(countBefore + 1, { timeout: 180_000 });

    // Guard against the premature-"complete" race described above: the prompt
    // forces two sequential tool calls (browser_navigate, browser_screenshot),
    // and the first LLM completion segment can end (done:true) before the
    // second tool call even starts. Wait out any follow-up call so the img
    // assertion's 90s budget starts from REAL turn completion, not a mid-turn
    // blip that happens to land before the screenshot has been taken.
    await waitForTurnFullyDone(page, 8_000);

    // InlineMedia in ChatScreen renders img tags for image media (ChatScreen.tsx:219)
    const mediaImg = page.locator('img[src*="/api/v1/media/"]').first();
    await expect(mediaImg).toBeVisible({ timeout: 90_000 });

    const dimensions = await mediaImg.evaluate((img: HTMLImageElement) => ({
      naturalWidth: img.naturalWidth,
      naturalHeight: img.naturalHeight,
    }));

    expect(dimensions.naturalWidth).toBeGreaterThanOrEqual(600);
    expect(dimensions.naturalHeight).toBeGreaterThanOrEqual(300);

    await expectA11yClean(page);
  },
);

test(
  '(b) file-download fallback: send_file delivers a non-image file that renders a working download link',
  // T0.1 (re-investigated): the #107 blocker no longer holds. It was written
  // when no tool could reliably produce a non-image media frame without a
  // mock. This branch (sendfile-fix) now ships a real `send_file` tool
  // (pkg/tools/send_file.go, allowed for Jim/Mia/Ray — pkg/coreagent/core.go)
  // that registers ANY local file through the exact same MediaStore pipeline
  // browser_screenshot already uses in test (a) above. Pre-seeding a plain
  // fixture file on disk and instructing the agent to call send_file with
  // its exact relative path is exactly as deterministic as the
  // "call read_file with path=/etc/hostname" pattern subagent.spec.ts
  // already relies on elsewhere in this suite — no LLM judgment call, no
  // mock gateway tool needed.
  //
  // Why this exercises the NON-image branch deterministically: InlineMedia
  // (ChatScreen.tsx:366-387) renders <img> only when a media part's `type`
  // is "image"; every other type renders the `<a download>` branch. `type`
  // comes from inferMediaType(filename, contentType) (pkg/agent/loop.go),
  // which classifies a `.txt` file as "file" (it's in none of the
  // image/audio/video extension lists) regardless of what content-type
  // detection returns for a plain-text file — so this is robust even if
  // magic-byte detection is inconclusive for text.
  async ({ page }) => {
    test.setTimeout(120_000);

    // Seed a real fixture file directly on disk inside the workspace's work/
    // directory (the ADR-046 P1 filesystem re-root every CoreTeam agent's
    // tools are confined to), so the agent needs only ONE forced tool call
    // (send_file) — no write_file round-trip, no room for the LLM to
    // improvise content.
    const workspaceId = await resolveWorkspaceId(page);
    const relDir = 'e2e-media-download';
    const fileName = `report-${Date.now()}.txt`;
    const nonce = `OMNIPUS-E2E-${Date.now()}-${Math.random().toString(36).slice(2)}`;
    const fixtureContent = `Omnipus E2E download fixture\nnonce: ${nonce}\n`;
    const workDir = path.join(OMNIPUS_HOME, 'workspaces', workspaceId, 'work', relDir);
    fs.mkdirSync(workDir, { recursive: true });
    fs.writeFileSync(path.join(workDir, fileName), fixtureContent, 'utf-8');
    const relPath = `${relDir}/${fileName}`;

    // Jim has send_file:allow (pkg/coreagent/core.go IDJim case) and is the
    // established "does real work now" agent elsewhere in this suite
    // (subagent.spec.ts's startFreshChat / selectAgent default).
    await selectAgent(page, /Jim/i);

    const input = chatInput(page);
    await expect(input).toBeVisible({ timeout: 10_000 });

    const countBefore = await assistantMessages(page).count();
    await input.fill(
      [
        `Call the send_file tool exactly once with path "${relPath}".`,
        'Do not pass a filename argument. Do not call any other tool.',
        'After the tool result, reply with exactly: SENT',
      ].join(' '),
    );
    await input.press('Enter');

    await expect(assistantMessages(page)).toHaveCount(countBefore + 1, { timeout: 60_000 });

    // Structural assertion: the non-image branch of InlineMedia renders an
    // <a download="..."> link (ChatScreen.tsx:378-386), never an <img>, for
    // a file classified as "file" — not "some link appeared".
    const downloadLink = page.locator(`a[download="${fileName}"]`);
    await expect(downloadLink).toBeVisible({ timeout: 15_000 });
    await expect(page.locator(`img[src*="${fileName}"]`)).toHaveCount(0);

    const href = await downloadLink.getAttribute('href');
    expect(href).toMatch(/\/api\/v1\/media\//);

    // Differentiation + persistence test: actually download the file through
    // the real HTTP endpoint and assert the exact nonce-bearing bytes round-
    // tripped (local disk -> send_file -> MediaStore -> HTTP -> browser).
    // A hardcoded/stub send_file, or one that registers a ref without really
    // persisting the file, cannot reproduce this nonce.
    const [download] = await Promise.all([
      page.waitForEvent('download'),
      downloadLink.click(),
    ]);
    expect(download.suggestedFilename()).toBe(fileName);
    const downloadPath = await download.path();
    expect(downloadPath).toBeTruthy();
    const downloadedContent = fs.readFileSync(downloadPath as string, 'utf-8');
    expect(downloadedContent).toBe(fixtureContent);

    await expectA11yClean(page);
  },
);

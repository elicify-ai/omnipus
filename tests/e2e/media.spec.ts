import * as fs from 'fs';
import * as path from 'path';
import { expect, type Page } from '@playwright/test';
import { test } from './fixtures/console-errors';
import { expectA11yClean } from './fixtures/a11y';
import { chatInput, assistantMessages, userMessages, selectAgent, DEFAULT_WORKSPACE_NAME } from './fixtures/selectors';
import { Agent, Session, SessionDetail, Workspace } from '../../src/lib/api/generated/schemas';
import type { SessionCreateRequest } from '../../src/lib/api/generated/openapi-types';
import { GatewayProcess } from './fixtures/gateway-process';
import { openSessionByDeepLink } from './fixtures/session-setup';
import { MEDIA_DOWNLOAD_CALL_ID, startMediaDownloadProvider } from './fixtures/media-download-provider';

// Global storageState provides test (a)'s shared-gateway session. Test (b)
// uses its own GatewayProcess, cookies, workspace and fresh Jim chat instead.

/**
 * Wait for any follow-up LLM call after the current assistant message settles.
 *
 * Mirrors waitForTurnFullyDone() from chat.spec.ts / memory-remember-recall.spec.ts /
 * recap-continuity.spec.ts (shared harness pattern, not extracted to a fixture —
 * see those files for the same copy). Root cause: the prompt in test (a) asks the
 * model for TWO sequential tool calls (browser_navigate, then browser_screenshot).
 * Each individual LLM completion segment can end with done:true — including the
 * one right after browser_navigate, BEFORE browser_screenshot has even been
 * called — which briefly flips isStreaming to false and makes data-status
 * "complete" appear on the assistant message. The `assistantMessages` count
 * assertion below only checks data-status !== "running", so it can resolve on
 * that premature mid-turn blip, long before the screenshot tool has run and the
 * media frame exists. Without this guard, the subsequent img assertion's 90s
 * budget starts from that early point instead of from real turn completion,
 * silently eating into the time the central e2e model (tests/e2e/e2e-model.json) has
 * to finish the second tool call.
 *
 * `followUpCeilingMs` is how long a follow-up LLM call (stop button visible
 * again) may run before this guard gives up waiting. Default 180s — the same
 * ceiling as test (a)'s own count assertion, kept for the two-browser-tool-call
 * flow. Test (b) does not use this advisory helper: its scripted provider
 * proves the send_file tool result returned through the loop, and its final
 * SENT reply and download assertions establish delivery directly.
 */
async function waitForTurnFullyDone(page: Page, gapMs = 8_000, followUpCeilingMs = 180_000): Promise<void> {
  const stopBtn = page.locator('[data-testid="stop-btn"]');
  try {
    // If the stop button reappears within gapMs, a follow-up LLM call is in progress.
    await expect(stopBtn).toBeVisible({ timeout: gapMs });
    // Stop button appeared — wait for it to vanish (follow-up LLM call done).
    await expect(stopBtn).not.toBeVisible({ timeout: followUpCeilingMs });
  } catch {
    // ADVISORY ONLY (inherited catch, not completion proof): this runs when
    // the stop button did not reappear within gapMs (the usual meaning: no
    // follow-up call) OR when the follow-up call's disappearance wait expired
    // (the turn may STILL be running). It deliberately does not decide
    // anything; the delivery assertions that follow are the oracle.
  }
}

// Scope the original shared-gateway navigation to (a), like the shared/live
// scopes in catchup-scenarios. The anonymous scope preserves its full title.
test.describe(() => {
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
    // (screenshot) + SPA (media render). The central e2e model
    // (tests/e2e/e2e-model.json) is reliable but slower than the earlier pick,
    // so use an explicit generous budget rather than the 270s test.slow() ceiling.
    //
    // Budget (WORST-CASE ceiling, recomputed the same way chat.spec.ts (b) /
    // recap-continuity.spec.ts were fixed: test.setTimeout is a hard governor,
    // so it must sum every step's own worst-case timeout, INCLUDING the
    // waitForTurnFullyDone leg added below for the browser_navigate ->
    // browser_screenshot two-tool-call race):
    //   Sidebar agent selection + input visibility ..................... ~35s
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
    // Sidebar row click is the only agent switch since the composer
    // AgentPicker was removed (nav commit e84ac0532; FR-029 no in-chat handover).
    await selectAgent(page, /Jim/i);

    const input = chatInput(page);
    await expect(input).toBeVisible({ timeout: 10_000 });

    const countBefore = await assistantMessages(page).count();
    // Explicit, single-tool instruction — mirrors the reliable phrasing used by
    // the delegate-based specs so the central e2e model (tests/e2e/e2e-model.json)
    // takes the screenshot itself instead of narrating or delegating.
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

});

// A file is the shard unit: keep this scripted case in llm-light with live (a),
// as catchup-scenarios keeps scripted b/c beside its live cases in llm-agents.
test.describe(() => {
  // A deterministic download regression cannot be established by retrying.
  test.describe.configure({ retries: 0 });
  test.use({ storageState: { cookies: [], origins: [] } });

  test(
    '(b) file-download fallback: send_file delivers a non-image file that renders a working download button',
    async ({ page }, info) => {
      // Only the external model is scripted. Whether a live model obeys the
      // send_file instruction is deliberately NOT tested here: that is the
      // model's behaviour, not the product's download path. Agent execution,
      // MediaStore, media HTTP serving, SPA and browser download remain real.
      // History: the old live-model case failed 12 of 12 CI attempts (#1764);
      // shared-session interference and model refusal motivated this isolation.
      // 120s allows boot/onboarding, fresh-chat UI, local tool round-trip,
      // download and accessibility scan; no paid-model latency is budgeted.
      test.setTimeout(120_000);

      const relDir = 'e2e-media-download';
      const fileName = `report-${Date.now()}.txt`;
      const nonce = `OMNIPUS-E2E-${Date.now()}-${Math.random().toString(36).slice(2)}`;
      const fixtureContent = `Omnipus E2E download fixture\nnonce: ${nonce}\n`;
      const relPath = `${relDir}/${fileName}`;
      const prompt = [
        `Call the send_file tool exactly once with path "${relPath}".`,
        'Do not pass a filename argument. Do not call any other tool.',
        'After the tool result, reply with exactly: SENT',
      ].join(' ');
      const provider = await startMediaDownloadProvider({ prompt, relativePath: relPath });
      let gw: GatewayProcess | undefined;
      try {
        gw = await GatewayProcess.start({ apiBase: provider.url });
        // Same public-API + exact deep-link setup as agent-parity-approval:
        // no cold landing-page/sidebar detour and no reuse of Jim's main chat.
        const agentsResult = await gw.apiFetch('GET', '/api/v1/agents');
        expect(agentsResult.status, 'the isolated gateway must expose its real roster').toBe(200);
        const jim = Agent.array().parse(agentsResult.body).find((agent) => agent.name === 'Jim');
        expect(jim, 'the isolated roster must contain Jim').toBeDefined();
        if (!jim) throw new Error('BLOCKED: Jim is required by media (b)');
        const workspacesResult = await gw.apiFetch('GET', '/api/v1/workspaces');
        expect(workspacesResult.status, 'the isolated workspace must be readable').toBe(200);
        const workspace = Workspace.array().parse(workspacesResult.body).find((ws) => ws.name === DEFAULT_WORKSPACE_NAME);
        expect(workspace, 'the isolated gateway must seed its default workspace').toBeDefined();
        if (!workspace) throw new Error('BLOCKED: a workspace is required by media (b)');
        const created = await gw.apiFetch('POST', '/api/v1/sessions', {
          type: 'chat', agent_id: jim.id, workspace_id: workspace.id,
        } satisfies SessionCreateRequest);
        expect(created.status, 'create a new Jim chat, not the shared main session').toBe(201);
        const session = Session.parse(created.body);
        expect(session.agent_id, 'the fresh chat must belong to Jim').toBe(jim.id);
        expect(session.type, 'the download must run in a chat session').toBe('chat');
        expect(session.workspace_id, 'seed the file in this chat’s workspace, never a shared home').toBe(workspace.id);
        const detail = await gw.apiFetch('GET', `/api/v1/sessions/${session.id}`);
        expect(detail.status, 'the fresh session history must be readable').toBe(200);
        const history = SessionDetail.parse(detail.body);
        expect(history.history_state.status, 'freshness must not be inferred from unavailable history').toBe('available');
        expect(history.messages, 'the new Jim chat must have no prior messages').toEqual([]);
        const workDir = path.join(gw.homeDir, 'workspaces', workspace.id, 'work', relDir);
        fs.mkdirSync(workDir, { recursive: true });
        fs.writeFileSync(path.join(workDir, fileName), fixtureContent, 'utf-8');
        await page.context().addCookies((await gw.browserStorageState()).cookies);
        await openSessionByDeepLink(page, session.id, gw.baseURL);
        await expect(userMessages(page), 'a fresh Jim chat has no prior user messages').toHaveCount(0);

        const input = chatInput(page);
        // 10s is SPA readiness slack; the provider is local and answers at once.
        await expect(input).toBeVisible({ timeout: 10_000 });
        await expect(assistantMessages(page), 'a fresh chat cannot inherit (a)’s reply').toHaveCount(0);
        const countBefore = await assistantMessages(page).count();
        await input.fill(prompt);
        await input.press('Enter');

        // 15s covers local send_file plus its follow-up; a final SENT paragraph
        // is completion evidence, unlike the old advisory mid-turn gap wait.
        await expect(assistantMessages(page)).toHaveCount(countBefore + 1, { timeout: 15_000 });
        await expect(assistantMessages(page).getByText('SENT', { exact: true })).toHaveText('SENT', { timeout: 15_000 });
        expect(provider.evidence.streamRequests, 'exactly the tool call and its follow-up reached the provider').toBe(2);
        expect(provider.evidence.issuedCalls, 'exactly one send_file call was issued').toBe(1);
        expect(provider.evidence.toolResults, 'send_file returned through the real agent loop on request two').toEqual([
          { request: 2, callId: MEDIA_DOWNLOAD_CALL_ID, name: 'send_file', content: expect.stringMatching(/\S/) },
        ]);
        expect(provider.evidence.errors, 'no unexpected scripted-provider traffic').toEqual([]);

        // Structural oracle: a non-image is a file download button, never an img.
        const downloadButton = page.getByRole('button', { name: `Download ${fileName}`, exact: true });
        // 10s is render slack after the local tool result, not a model budget.
        await expect(downloadButton, 'the sent file must render its Download button').toBeVisible({ timeout: 10_000 });
        await expect(page.locator(`img[src*="${fileName}"]`)).toHaveCount(0);
        await expect(page.locator('img[src*="/api/v1/media/"]'), 'non-image media must not render as an image').toHaveCount(0);

        // Real disk -> send_file -> MediaStore -> HTTP -> browser, with exact
        // nonce-bearing bytes. The provider never knows the fixture contents.
        // 10s per event/click is slack for a tiny local file, with no network model.
        const [download, mediaRequest] = await Promise.all([
          page.waitForEvent('download', { timeout: 10_000 }),
          page.waitForRequest((req) => /\/api\/v1\/media\//.test(req.url()), { timeout: 10_000 }),
          downloadButton.click({ timeout: 10_000 }),
        ]);
        expect(mediaRequest.url()).toMatch(/\/api\/v1\/media\//);
        expect(download.suggestedFilename(), 'the browser download must retain the original filename').toBe(fileName);
        const downloadPath = await download.path();
        expect(downloadPath).toBeTruthy();
        const downloadedContent = fs.readFileSync(downloadPath as string, 'utf-8');
        expect(downloadedContent, 'downloaded bytes must exactly match the nonce-bearing disk fixture').toBe(fixtureContent);
        expect(fs.readFileSync(downloadPath as string), 'download must preserve every fixture byte').toEqual(Buffer.from(fixtureContent, 'utf-8'));
        await expectA11yClean(page);
      } finally {
        try {
          await info.attach('media-download-provider-evidence', {
            body: JSON.stringify(provider.evidence, null, 2), contentType: 'application/json',
          });
          if (gw) await info.attach('media-download-gateway', { body: gw.outputTail(), contentType: 'text/plain' });
        } finally {
          try { await gw?.stop(); }
          finally { await provider.close(); }
        }
      }
    },
  );
});

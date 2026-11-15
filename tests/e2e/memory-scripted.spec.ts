import { randomUUID } from 'node:crypto';
import * as fs from 'node:fs';
import * as path from 'node:path';
import { expect, type Page } from '@playwright/test';
import { test } from './fixtures/console-errors';
import { Agent, Session, SessionDetail, Workspace } from '../../src/lib/api/generated/schemas';
import type { SessionCreateRequest } from '../../src/lib/api/generated/openapi-types';
import { GatewayProcess } from './fixtures/gateway-process';
import { startMemoryRoundTripProvider } from './fixtures/memory-round-trip-provider';
import { assistantMessages, chatInput, DEFAULT_WORKSPACE_NAME, userMessages } from './fixtures/selectors';
import { openSessionByDeepLink } from './fixtures/session-setup';

// One always-on scripted spec (stubs-3), replacing #1800's deleted twins:
// tests/e2e/memory-scripted-round-trip.spec.ts,
// tests/e2e/fixtures/memory-scripted-provider.ts and
// tests/e2e/fixtures/memory-scripted-provider.test.ts. Their assertion trace is
// /workspace/logs/memory-scripted/ta/twin-trace.md. Reordered recall distinguishes
// token-index lookup from substring scan; exact shared success is required.
// The original memory-remember-recall.spec.ts remains the live-model smoke.
// Probe budget: one onboarding validation, no extra model calls for memory.
test.describe.configure({ retries: 0 });
test.use({ storageState: { cookies: [], origins: [] } });

// Same shared-then-private path logic as the live memory spec, but rooted in
// this GatewayProcess's own home. Do not seed a file: remember must create it.
function memoryFiles(home: string, workspaceId: string, agentId: string, nonce: string): string[] {
  const directories = [
    path.join(home, 'workspaces', workspaceId, '.omnipus', 'memories'),
    path.join(home, 'agents', agentId, '.omnipus', 'memories'),
  ];
  return directories.flatMap((directory) => fs.existsSync(directory)
    ? fs.readdirSync(directory, { withFileTypes: true })
      .filter((entry) => entry.isFile() && entry.name.endsWith('.md'))
      .map((entry) => path.join(directory, entry.name))
      .filter((file) => fs.readFileSync(file, 'utf8').includes(nonce))
    : []);
}

async function settled(page: Page, turn: number) {
  await expect(assistantMessages(page), `(e) turn ${turn}: exact settled assistant count`).toHaveCount(turn);
  await expect(assistantMessages(page).nth(turn - 1), `(e) turn ${turn}: normal completion, not interruption/error`)
    .toHaveAttribute('data-status', 'complete');
  await expect(page.locator('[data-message-id][data-status="running"]'), `(e) turn ${turn}: no running row remains`).toHaveCount(0);
  await expect(page.getByTestId('stop-btn'), `(e) turn ${turn}: generation has stopped`).toHaveCount(0);
  await expect(chatInput(page), `(e) turn ${turn}: composer is enabled after settlement`).toBeEnabled();
}

// Only the external model HTTP edge is scripted. Tools, room routing, disk,
// index, provider tool messages, gateway socket and SPA rendering stay real.
test('scripted memory round trip: remember writes, index recalls, SPA renders; absent nonce stays absent', async ({ page }, info) => {
  // Boot/onboard/deep-link plus three immediate local two-request turns. No
  // live-model latency or retries; individual assertions keep the normal 15s.
  test.setTimeout(120_000);
  // #1800's index-only oracle: reversed tokens cannot match a substring scan.
  // The never-stored control has no shared analyzer terms with the stored note.
  const stamp = randomUUID().replaceAll('-', '');
  const nonce = `MEMSCRIPT-${stamp}`;
  const recallQuery = `${stamp}-MEMSCRIPT`;
  const missingNonce = `unrecorded${randomUUID().replaceAll('-', '')}`;
  const prompts = [
    `Scripted memory turn 1: remember ${nonce} in shared memory as a reference.`,
    `Scripted memory turn 2: recall ${recallQuery} from shared memory.`,
    `Scripted memory turn 3: recall ${missingNonce} from shared memory.`,
  ] as const;
  const provider = await startMemoryRoundTripProvider({ nonce, recallQuery, missingNonce, prompts });
  let gw: GatewayProcess | undefined;
  try {
    gw = await GatewayProcess.start({ apiBase: provider.url });
    const agents = await gw.apiFetch('GET', '/api/v1/agents');
    expect(agents.status, 'isolated roster is readable').toBe(200);
    const mia = Agent.array().parse(agents.body).find((agent) => agent.name === 'Mia');
    expect(mia, 'use Mia, the same agent as the live memory spec').toBeDefined();
    if (!mia) throw new Error('BLOCKED: Mia is required by scripted memory (#1661)');
    const workspaces = await gw.apiFetch('GET', '/api/v1/workspaces');
    expect(workspaces.status, 'isolated workspaces are readable').toBe(200);
    const workspace = Workspace.array().parse(workspaces.body).find((ws) => ws.name === DEFAULT_WORKSPACE_NAME);
    expect(workspace, 'the isolated gateway must seed its workspace').toBeDefined();
    if (!workspace) throw new Error('BLOCKED: a workspace is required by scripted memory (#1661)');
    const created = await gw.apiFetch('POST', '/api/v1/sessions', {
      type: 'chat', agent_id: mia.id, workspace_id: workspace.id,
    } satisfies SessionCreateRequest);
    expect(created.status, 'create a fresh API chat, never reuse Mia’s main chat').toBe(201);
    const session = Session.parse(created.body);
    expect({ agent: session.agent_id, workspace: session.workspace_id, type: session.type }, 'bind this chat to Mia and her workspace')
      .toEqual({ agent: mia.id, workspace: workspace.id, type: 'chat' });
    const detail = await gw.apiFetch('GET', `/api/v1/sessions/${session.id}`);
    expect(detail.status, 'new session history is readable').toBe(200);
    const history = SessionDetail.parse(detail.body);
    expect(history.history_state.status, 'history is genuinely available').toBe('available');
    expect(history.messages, 'new session starts empty').toEqual([]);
    await page.context().addCookies((await gw.browserStorageState()).cookies);
    await openSessionByDeepLink(page, session.id, gw.baseURL);
    await expect(assistantMessages(page), 'fresh chat has zero assistant replies').toHaveCount(0);
    await expect(userMessages(page), 'fresh chat has zero user messages').toHaveCount(0);
    const input = chatInput(page);
    await expect(input, 'fresh chat composer is enabled').toBeEnabled();

    await test.step('(a/b/e) remember writes a nonce-bearing memory and reports success', async () => {
      await input.fill(prompts[0]);
      await input.press('Enter');
      await expect(assistantMessages(page).getByText('Memory stored.', { exact: true }), '(e) turn 1: final provider text arrived')
        .toHaveText('Memory stored.');
      await expect(assistantMessages(page).getByText('Memory stored.', { exact: true }), '(e) turn 1: acknowledgement is visible').toBeVisible();
      await settled(page, 1);
      expect(provider.evidence.streamRequests, '(e) turn 1: tool call plus its follow-up, exactly two requests').toBe(2);
      expect(provider.evidence.issuedCalls, '(e) turn 1: exactly one tool call').toBe(1);
      const remembered = provider.evidence.toolResults.filter((item) => item.request === 2 && item.callId === 'memory-scripted-remember');
      expect(remembered, '(b) provider received exactly one correlated remember result').toEqual([
        { request: 2, callId: 'memory-scripted-remember', name: 'remember', content: "ok — saved to your workspace team's shared memory" },
      ]);
      const home = gw!.homeDir;
      await expect.poll(() => memoryFiles(home, workspace.id, mia.id, nonce), {
        message: '(a) remember must create exactly one nonce-bearing Markdown memory file on disk',
      }).toHaveLength(1);
      const [file] = memoryFiles(home, workspace.id, mia.id, nonce);
      expect(path.dirname(file), '(a) explicit shared-room write lands in this workspace, not a private fallback')
        .toBe(path.join(home, 'workspaces', workspace.id, '.omnipus', 'memories'));
      expect(fs.readFileSync(file, 'utf8'), '(a) the actual memory file contains the per-test nonce').toContain(nonce);
      expect(fs.readFileSync(file, 'utf8').toLowerCase(), '(c) reordered recall query is not a substring of the stored memory file')
        .not.toContain(recallQuery.toLowerCase());
      await info.attach('memory-scripted-memory-file', { path: file, contentType: 'text/markdown' });
    });

    await test.step('(c/d/e) recall returns the indexed nonce to the provider and renders its echo', async () => {
      await input.fill(prompts[1]);
      await input.press('Enter');
      await expect.poll(() => provider.evidence.streamRequests, { message: '(e) turn 2: recall and follow-up both reached the provider' }).toBe(4);
      const recalled = provider.evidence.toolResults.filter((item) => item.request === 4 && item.callId === 'memory-scripted-recall');
      // Core oracle: not a transcript/query/tool-card match. These bytes were
      // returned by the REAL recall_memory through the agent loop to the model.
      expect(recalled, '(c) provider received recall_memory result containing the stored nonce').toEqual([
        { request: 4, callId: 'memory-scripted-recall', name: 'recall_memory', content: expect.stringContaining(nonce) },
      ]);
      await settled(page, 2);
      // The provider puts only its received result in a fenced text block.
      // Scope to that rendered answer, never the tool badge (its QUERY also
      // contains both nonce tokens and could let a missing-answer mutant survive).
      const answer = assistantMessages(page).nth(1).locator('pre:not([data-history-tool-row] pre):not([data-testid="tool-call-badge"] pre)');
      await expect(answer, '(d) turn-2 assistant answer renders exactly one echoed text block').toHaveCount(1);
      await expect(answer, '(d) the turn-2 echoed answer is visible, not a hidden transcript value').toBeVisible();
      await expect(answer, '(d) the SPA renders the received recall content, not the user query').toHaveText(recalled[0].content);
      await expect(answer, '(d) the turn-2 assistant answer visibly includes the stored nonce').toContainText(nonce);
      await settled(page, 2);
      expect(provider.evidence.streamRequests, '(e) exactly four requests after two settled turns').toBe(4);
      expect(provider.evidence.issuedCalls, '(e) exactly remember then recall_memory').toBe(2);
    });

    await test.step('positive control: never-stored nonce is absent from recall result and assistant answer', async () => {
      await input.fill(prompts[2]);
      await input.press('Enter');
      await expect.poll(() => provider.evidence.streamRequests, { message: 'control: third recall and follow-up reached the provider' }).toBe(6);
      const missing = provider.evidence.toolResults.filter((item) => item.request === 6 && item.callId === 'memory-scripted-missing');
      expect(missing, 'control: provider received exactly one result from the real absent-memory lookup').toHaveLength(1);
      expect(missing[0].name, 'control: result is recall_memory, not remembered user text').toBe('recall_memory');
      expect(missing[0].content, 'control (c): tool result does not manufacture the never-stored nonce').not.toContain(missingNonce);
      expect(missing[0].content, 'control (c): a different query must not return the stored memory either').not.toContain(nonce);
      expect(missing[0].content, 'control (c): the never-stored query has no matching entries').toBe('no matching entries');
      await settled(page, 3);
      const answer = assistantMessages(page).nth(2).locator('pre:not([data-history-tool-row] pre):not([data-testid="tool-call-badge"] pre)');
      await expect(answer, 'control (d): the absent lookup still renders its real result').toHaveCount(1);
      await expect(answer, 'control (d): absent lookup answer is visible').toBeVisible();
      await expect(answer, 'control (d): answer echoes only the absent lookup result').toHaveText(missing[0].content);
      await expect(answer, 'control (d): answer does not manufacture the never-stored nonce').not.toContainText(missingNonce);
      await expect(answer, 'control (d): answer does not echo the old stored nonce').not.toContainText(nonce);
      await expect(assistantMessages(page).filter({ hasText: missingNonce }), 'control (d): no assistant row contains the never-stored nonce').toHaveCount(0);
      await settled(page, 3);
      expect(provider.evidence.streamRequests, '(e) three two-request turns total exactly six provider requests').toBe(6);
      expect(provider.evidence.probeRequests, '(e) one onboarding probe and no extra memory-loop model probes').toBe(1);
      expect(provider.evidence.issuedCalls, '(e) exactly one remember and two recall calls').toBe(3);
      const currentResults = provider.evidence.toolResults.filter((item) =>
        (item.request === 2 && item.callId === 'memory-scripted-remember') ||
        (item.request === 4 && item.callId === 'memory-scripted-recall') ||
        (item.request === 6 && item.callId === 'memory-scripted-missing'));
      expect(currentResults, '(e) exactly three current correlated tool results').toHaveLength(3);
      expect(provider.evidence.errors, '(e) no unexpected scripted-provider traffic').toEqual([]);
      await expect(userMessages(page), '(e) exactly three user turns in this fresh chat').toHaveCount(3);
    });
  } finally {
    console.log(`[memory-scripted] provider evidence ${JSON.stringify(provider.evidence)}`);
    try {
      await info.attach('memory-scripted-provider-evidence', { body: JSON.stringify(provider.evidence, null, 2), contentType: 'application/json' });
      await info.attach('memory-scripted-provider-requests', { body: JSON.stringify(provider.requests, null, 2), contentType: 'application/json' });
      if (gw) await info.attach('memory-scripted-gateway', { body: gw.outputTail(), contentType: 'text/plain' });
    } finally {
      try { await gw?.stop(); }
      finally { await provider.close(); }
    }
  }
});

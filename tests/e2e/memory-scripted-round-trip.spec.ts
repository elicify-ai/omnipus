/**
 * memory-scripted-round-trip.spec.ts — deterministic twin of
 * memory-remember-recall.spec.ts (which stays as the live-model test).
 *
 * Only the paid model edge is scripted (fixtures/memory-scripted-provider.ts).
 * The real agent loop runs the real `remember` and `recall_memory` tools against
 * the real memory store and bleve index, and the real SPA renders the reply:
 *   1. the memory file is on disk (shared workspace room);
 *   2. the provider receives a `remember` success;
 *   3. the provider receives a `recall_memory` result containing the nonce
 *      (the index lookup through the real loop);
 *   4. the SPA shows the nonce — the scripted reply echoes only what it found in
 *      the tool result, so the SPA cannot show it unless the recall returned it;
 *   5. exact request counts (six agent-loop requests, no retries);
 *   6. positive control: a never-stored nonce comes back empty and never shows.
 */
import * as fs from 'fs';
import * as path from 'path';
import { expect } from '@playwright/test';
import { test } from './fixtures/console-errors';
import { assistantMessages, waitForConnected, DEFAULT_WORKSPACE_NAME } from './fixtures/selectors';
import { openSessionByDeepLink } from './fixtures/session-setup';
import { Agent, Session, Workspace } from '../../src/lib/api/generated/schemas';
import type { SessionCreateRequest } from '../../src/lib/api/generated/openapi-types';
import { GatewayProcess } from './fixtures/gateway-process';
import { startMemoryScriptedProvider } from './fixtures/memory-scripted-provider';

test.describe(() => {
  test.describe.configure({ retries: 0 });
  test.use({ storageState: { cookies: [], origins: [] } });

  test('scripted remember→recall round trip: file on disk, nonce through the real index, SPA shows it, control stays empty', async ({ page }, info) => {
    test.setTimeout(120_000);
    const stamp = Date.now().toString(36);
    const NONCE = `MEMSCRIPT-${stamp}`;
    const CONTROL = `UNSTORED-${Math.random().toString(36).slice(2, 10)}`;
    const P1 = `Store this fact in memory now: ${NONCE}`;
    // Reordered tokens: NOT a substring of the stored text, so only the index (which
    // matches by token, see #1799) can find it. A substring-scan fallback cannot.
    const RECALL_QUERY = `${stamp}-MEMSCRIPT`;
    const P2 = `Look up ${RECALL_QUERY} in memory and tell me what you found`;
    const P3 = `Look up ${CONTROL} in memory and tell me what you found`;

    const provider = await startMemoryScriptedProvider({
      nonce: NONCE, recallQuery: RECALL_QUERY, controlNonce: CONTROL, rememberPrompt: P1, recallPrompt: P2, controlPrompt: P3,
    });
    let gw: GatewayProcess | undefined;
    try {
      gw = await GatewayProcess.start({ apiBase: provider.url });
      const agentsResult = await gw.apiFetch('GET', '/api/v1/agents');
      expect(agentsResult.status, 'the isolated gateway must expose its real roster').toBe(200);
      const mia = Agent.array().parse(agentsResult.body).find((agent) => agent.name === 'Mia');
      if (!mia) throw new Error('BLOCKED: Mia is required by the scripted memory test');
      const workspacesResult = await gw.apiFetch('GET', '/api/v1/workspaces');
      expect(workspacesResult.status).toBe(200);
      const workspace = Workspace.array().parse(workspacesResult.body).find((ws) => ws.name === DEFAULT_WORKSPACE_NAME);
      if (!workspace) throw new Error('BLOCKED: the default workspace is required by the scripted memory test');
      const created = await gw.apiFetch('POST', '/api/v1/sessions', {
        type: 'chat', agent_id: mia.id, workspace_id: workspace.id,
      } satisfies SessionCreateRequest);
      expect(created.status, 'create a fresh Mia chat').toBe(201);
      const session = Session.parse(created.body);
      await page.context().addCookies((await gw.browserStorageState()).cookies);
      await openSessionByDeepLink(page, session.id, gw.baseURL);
      await waitForConnected(page, { timeout: 15_000 });
      await expect(assistantMessages(page)).toHaveCount(0);
      const chatInput = page.locator('[data-testid="chat-input"]').first();
      await expect(chatInput).toBeEnabled({ timeout: 15_000 });

      const send = async (text: string, count: number) => {
        await chatInput.fill(text);
        await chatInput.press('Enter');
        await expect(assistantMessages(page)).toHaveCount(count, { timeout: 30_000 });
        await expect(chatInput).toBeEnabled({ timeout: 15_000 });
      };

      // Turn 1: remember. The provider must see a success result for the call.
      await send(P1, 1);
      await expect(assistantMessages(page).getByText('STORED-OK', { exact: true })).toBeVisible({ timeout: 15_000 });
      const memoriesDir = path.join(gw.homeDir, 'workspaces', workspace.id, '.omnipus', 'memories');
      await expect.poll(() => memoryFileWith(memoriesDir, NONCE), {
        message: `a memory file containing ${NONCE} must be on disk in ${memoriesDir}`, timeout: 15_000,
      }).not.toBeNull();
      expect(provider.evidence.toolResults.map((r) => r.name)).toEqual(['remember']);
      expect(provider.evidence.streamRequests).toBe(2);

      // Turn 2: recall through the real index; the SPA shows what the tool returned.
      await send(P2, 2);
      await expect(assistantMessages(page).getByText(`RECALLED:${NONCE}`, { exact: true })).toBeVisible({ timeout: 15_000 });
      const recall = provider.evidence.toolResults.find((r) => r.name === 'recall_memory' && r.request === 4);
      expect(recall?.content, 'the recall_memory result must contain the stored nonce, found by token (index lookup)').toContain(NONCE);
      expect(provider.evidence.streamRequests).toBe(4);

      // Turn 3 (positive control): a never-stored nonce must not come back.
      await send(P3, 3);
      await expect(assistantMessages(page).getByText('NOTHING-FOUND', { exact: true })).toBeVisible({ timeout: 15_000 });
      const control = provider.evidence.toolResults.find((r) => r.request === 6);
      expect(control?.content, 'the control recall result must not contain the control nonce').not.toContain(CONTROL);
      expect(control?.content, 'a control search must not return the stored memory either').not.toContain(NONCE);
      await expect(assistantMessages(page).filter({ hasText: CONTROL })).toHaveCount(0);
      expect(provider.evidence.streamRequests, 'exactly six agent-loop requests').toBe(6);
      expect(provider.evidence.toolResults).toHaveLength(3);
      expect(provider.evidence.errors, 'no unexpected scripted-provider traffic').toEqual([]);
    } finally {
      try {
        await info.attach('memory-scripted-provider-evidence', {
          body: JSON.stringify(provider.evidence, null, 2), contentType: 'application/json',
        });
        if (gw) await info.attach('memory-scripted-gateway', { body: gw.outputTail(), contentType: 'text/plain' });
      } finally {
        try { await gw?.stop(); }
        finally { await provider.close(); }
      }
    }
  });
});

/** The path of a .md file in `dir` whose text contains `needle`, or null. */
function memoryFileWith(dir: string, needle: string): string | null {
  if (!fs.existsSync(dir)) return null;
  for (const name of fs.readdirSync(dir)) {
    if (!name.endsWith('.md')) continue;
    const file = path.join(dir, name);
    if (fs.readFileSync(file, 'utf-8').includes(needle)) return file;
  }
  return null;
}

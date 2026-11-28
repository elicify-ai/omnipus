import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import http from 'node:http';
import { test as base, expect, type Page } from '@playwright/test';
import { z } from 'zod';
import { Agent, Workspace, Session, SecretNameEntry, SecretRequestState } from '../../../src/lib/api/generated/schemas';
import type { AgentCreateRequestMain, WorkspaceCreateRequest, SessionCreateRequest } from '../../../src/lib/api/generated/openapi-types';
import { GatewayProcess } from './gateway-process';
import { E2E_FALLBACK } from './e2e-model';
import { openSessionByDeepLink } from './session-setup';
import { chatInput } from './selectors';

// Test-fixture state, not a gateway wire type. Every retry owns a new gateway,
// account, home, browser cookies and fake executable. No paid model is called.
type Fixture = {
  gateway: GatewayProcess;
  devId: string;
  workspaceId: string;
  starts: () => number;
  releaseCheck: () => void;
  holdCheck: () => void;
  seedPrivateGH_TOKEN: () => Promise<void>;
};

// S-12's Given ("Dev has a selected private variable GH_TOKEN in W") is built
// on the product's only path that creates one: Dev's own web-chat turn calls
// request_secret with `variable: "GH_TOKEN"` and the signed-in person submits
// the secure card. The scripted provider below plays Dev's model for that one
// turn; no paid model is called.
const S12_SEED_MESSAGE = 'S12_SEED_PRIVATE_GH_TOKEN: ask me for your private GH_TOKEN variable.';
const S12_SEED_PURPOSE = 'S-12 Given: a private GH_TOKEN for Dev in S1 Home';
const S12_PRIVATE_VALUE = 'gh_private_1234567890';
const S12_SEED_DONE = 'The private variable GH_TOKEN is stored.';

function sseChunk(delta: Record<string, unknown>, finishReason: string | null) {
  return 'data: ' + JSON.stringify({ id: 's1-seed-stream', object: 'chat.completion.chunk', model: E2E_FALLBACK.model,
    created: Math.floor(Date.now() / 1000), choices: [{ index: 0, delta, finish_reason: finishReason }] }) + '\n\n';
}
function sseToolCall(name: string, args: unknown, id: string) {
  return sseChunk({ tool_calls: [{ index: 0, id, type: 'function', function: { name, arguments: JSON.stringify(args) } }] }, null)
    + sseChunk({}, 'tool_calls') + 'data: [DONE]\n\n';
}

/** The loopback model provider every S1 browser gateway onboards against:
 * answers the onboarding key probe, scripts S-12's one seeding turn, and
 * records any other call as unexpected. Shared with connectors-programs.spec.ts. */
export async function startProbeProvider() {
  const unexpected: string[] = [];
  let searched = false;
  let issued = false;
  const server = http.createServer((req, res) => {
    const chunks: Buffer[] = [];
    req.on('data', (chunk: Buffer) => chunks.push(chunk));
    req.on('end', () => {
      res.setHeader('Content-Type', 'application/json');
      if (req.method === 'GET' && req.url?.includes('/models')) {
        res.end(JSON.stringify({ object: 'list', data: [{ id: E2E_FALLBACK.model, object: 'model' }] }));
        return;
      }
      if (!(req.method === 'POST' && req.url?.includes('/chat/completions'))) {
        unexpected.push(`${req.method} ${req.url}`); res.statusCode = 404; res.end('{}');
        return;
      }
      let body: Record<string, unknown>;
      try { body = JSON.parse(Buffer.concat(chunks).toString('utf8')) as Record<string, unknown>; }
      catch (err) { unexpected.push(`invalid provider JSON: ${String(err)}`); res.statusCode = 400; res.end('{}'); return; }
      if (body.stream !== true) {
        res.end(JSON.stringify({ id: 's1-onboarding-probe', object: 'chat.completion', model: E2E_FALLBACK.model,
          choices: [{ index: 0, message: { role: 'assistant', content: 'probe ok' }, finish_reason: 'stop' }],
          usage: { prompt_tokens: 1, completion_tokens: 1, total_tokens: 2 } }));
        return;
      }
      // A streamed call is an agent turn. The only turn these tests start is
      // S-12's seeding turn; any other is unexpected and fails the fixture.
      res.setHeader('Content-Type', 'text/event-stream');
      if (!JSON.stringify(body.messages ?? []).includes(S12_SEED_MESSAGE)) {
        unexpected.push('agent turn without the S-12 seed message');
        res.end(sseChunk({ role: 'assistant', content: 'unexpected turn' }, 'stop') + 'data: [DONE]\n\n');
        return;
      }
      if (issued) {
        res.end(sseChunk({ role: 'assistant', content: S12_SEED_DONE }, 'stop') + 'data: [DONE]\n\n');
        return;
      }
      const tools = new Set((Array.isArray(body.tools) ? body.tools : []).flatMap((entry) => {
        const name = (entry as { function?: { name?: string } }).function?.name;
        return name ? [name] : [];
      }));
      if (!tools.has('request_secret')) {
        if (searched || !tools.has('ToolSearch')) {
          unexpected.push('BLOCKED: request_secret was not exposed to Dev by the real registry — required by the S-12 Given');
          res.end(sseChunk({ role: 'assistant', content: 'request_secret unavailable' }, 'stop') + 'data: [DONE]\n\n');
          return;
        }
        searched = true;
        res.end(sseToolCall('ToolSearch', { names: ['request_secret'] }, 's1-seed-search'));
        return;
      }
      issued = true;
      res.end(sseToolCall('request_secret', { purpose: S12_SEED_PURPOSE, variable: 'GH_TOKEN' }, 's1-seed-request-secret'));
    });
  });
  await new Promise<void>((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve); });
  const address = server.address();
  if (!address || typeof address === 'string') throw new Error('S1 probe provider did not bind');
  return { url: `http://127.0.0.1:${address.port}`, unexpected,
    close: () => new Promise<void>((resolve, reject) => { server.close((err) => err ? reject(err) : resolve()); server.closeAllConnections(); }) };
}

/** S-12's Given through the real product path: Dev's web-chat turn in W asks
 * for its private variable GH_TOKEN with request_secret, the signed-in person
 * submits the secure card, and the stored-secrets list then shows exactly one
 * SELECTED agent-private GH_TOKEN owned by (W, Dev). An ordinary credential Set
 * is not that Given. The value is never read back. */
async function seedPrivateGH_TOKEN(page: Page, gateway: GatewayProcess, devId: string, workspaceId: string) {
  const created = await gateway.apiFetch('POST', '/api/v1/sessions',
    { type: 'chat', agent_id: devId, workspace_id: workspaceId } satisfies SessionCreateRequest);
  expect(created.status, created.raw).toBe(201);
  const sessionId = Session.parse(created.body).id;
  await openSessionByDeepLink(page, sessionId, gateway.baseURL);
  await chatInput(page).fill(S12_SEED_MESSAGE);
  await chatInput(page).press('Enter');

  const card = page.getByRole('region', { name: 'Secret requested by Dev', exact: true }).filter({ hasText: S12_SEED_PURPOSE });
  await expect(card, 'Dev\'s request_secret card for GH_TOKEN is shown').toBeVisible({ timeout: 30_000 });
  const field = card.locator('[data-testid^="secret-request-field-"]');
  await expect(field).toHaveCount(1);
  await field.fill(S12_PRIVATE_VALUE);
  const submitted = page.waitForResponse((r) => r.request().method() === 'POST' && /\/api\/v1\/secret-requests\/[^/]+\/submit$/.test(new URL(r.url()).pathname));
  await card.getByTestId('secret-request-submit').click();
  const response = await submitted;
  expect(response.status(), 'the person\'s secure-card submit is accepted').toBe(200);
  const state = SecretRequestState.parse(await response.json());
  expect(state.state).toBe('submitted');
  await expect(card).toHaveAttribute('data-state', 'submitted');

  const listed = await gateway.apiFetch('GET', '/api/v1/secrets');
  expect(listed.status, listed.raw).toBe(200);
  expect(listed.raw, 'the secrets list never carries a value').not.toContain(S12_PRIVATE_VALUE);
  const privates = z.array(SecretNameEntry).parse(listed.body).filter((entry) => entry.kind === 'agent_private');
  expect(privates.map((entry) => ({ owner: entry.owner, state: entry.state, names: entry.recipient.lines.some((line) => line.includes('GH_TOKEN')) })),
    'the Given: exactly one selected private GH_TOKEN for Dev in W').toEqual([
    { owner: { workspace_id: workspaceId, agent_id: devId }, state: 'selected', names: true },
  ]);
}

export const test = base.extend<{ programsS1: Fixture }>({
  programsS1: async ({ page }, use) => {
    const directory = fs.mkdtempSync(path.join(os.tmpdir(), 's1-programs-browser-'));
    const marker = path.join(directory, 'starts');
    const release = path.join(directory, 'release');
    const hold = path.join(directory, 'hold');
    const fake = path.join(directory, 'gh.js');
    fs.writeFileSync(fake, `#!${process.execPath}\n` +
      `const fs = require('node:fs');\n` +
      `fs.appendFileSync(${JSON.stringify(marker)}, 'started\\n');\n` +
      `if (JSON.stringify(process.argv.slice(2)) !== JSON.stringify(['api','user','--jq','.login'])) process.exit(9);\n` +
      `const run = () => { if (fs.existsSync(${JSON.stringify(hold)}) && !fs.existsSync(${JSON.stringify(release)})) { setTimeout(run,10); return; }\n` +
      `const account = {'tok_valid_12345':'octocat','tok_new_654321':'hubot'}[process.env.GH_TOKEN];\n` +
      `if (!account) { process.stderr.write('HTTP 401: Bad credentials\\n'); process.exit(1); }\n` +
      `process.stdout.write(account+'\\n'); }; run();\n`, { mode: 0o755 });
    if (process.platform === 'win32') {
      fs.writeFileSync(path.join(directory, 'gh.cmd'), `@"${process.execPath}" "${fake}" %*\r\n`);
    } else { fs.renameSync(fake, path.join(directory, 'gh')); }
    // The gateway treats its per-user temp folder as agent-writable wherever
    // that folder is not the shared /tmp (S-08, FR-005:
    // pkg/thirdpartycli/executor_settings.go::runtimeTempRoots, read from the
    // gateway's own environment). On the cloud pods (TMPDIR=/workspace/.cache/tmp),
    // the Fly CI cluster (TMPDIR=/cache/tmp) and macOS, a fake gh made under
    // os.tmpdir() therefore sat inside that folder and was rightly refused, so
    // the account check never ran (saved_not_checked). The gateway gets its own
    // empty temp folder instead, and the fake gh stays outside it — the same
    // placement as the Go fixtures' pgFakeProgramDir (324d1787a).
    const gatewayTemp = fs.mkdtempSync(path.join(os.tmpdir(), 's1-programs-gateway-tmp-'));
    const provider = await startProbeProvider();
    let gateway: GatewayProcess | undefined;
    try {
      gateway = await GatewayProcess.start({ apiBase: provider.url, env: {
        PATH: directory + path.delimiter + (process.env.PATH ?? ''),
        TMPDIR: gatewayTemp, TMP: gatewayTemp, TEMP: gatewayTemp,
      } });
      const made = await gateway.apiFetch('POST', '/api/v1/agents', { type: 'Main', name: 'Dev', soul: 'S1 test colleague' } satisfies AgentCreateRequestMain);
      expect(made.status, made.raw).toBe(201);
      const dev = Agent.parse(made.body);
      const workspaceResult = await gateway.apiFetch('POST', '/api/v1/workspaces', { name: 'S1 Home', core_team: [dev.id] } satisfies WorkspaceCreateRequest);
      expect(workspaceResult.status, workspaceResult.raw).toBe(201);
      const workspace = Workspace.parse(workspaceResult.body);
      const listed = await gateway.apiFetch('GET', '/api/v1/programs');
      if (listed.status === 404) throw new Error('BLOCKED: GET /programs not implemented — required by S1 browser fixture/FR-001');
      expect(listed.status, listed.raw).toBe(200);
      const doc = listed.body as Record<string, unknown>;
      const entries = doc.entries as Array<Record<string, unknown>>;
      expect(entries.map((entry) => entry.id).sort()).toEqual(['fly', 'github']);
      expect(entries.find((entry) => entry.id === 'github')?.connection).toMatchObject({ state: 'not_connected', generation: 0 });
      const auth = await gateway.browserStorageState();
      await page.context().clearCookies(); await page.context().addCookies(auth.cookies);
      await use({ gateway, devId: dev.id, workspaceId: workspace.id,
        starts: () => { try { return fs.readFileSync(marker, 'utf8').split('started\n').length - 1; } catch (err) { if ((err as NodeJS.ErrnoException).code === 'ENOENT') return 0; throw err; } },
        holdCheck: () => { fs.rmSync(release, { force: true }); fs.writeFileSync(hold, 'hold'); },
        releaseCheck: () => fs.writeFileSync(release, 'release'),
        seedPrivateGH_TOKEN: () => seedPrivateGH_TOKEN(page, gateway!, dev.id, workspace.id),
      });
      expect(provider.unexpected).toEqual([]);
    } finally {
      fs.writeFileSync(release, 'release');
      try { if (gateway) await gateway.stop(); }
      finally {
        await provider.close();
        for (const folder of [directory, gatewayTemp]) fs.rmSync(folder, { recursive: true, force: true });
      }
    }
  },
});
export { expect } from '@playwright/test';

import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import http from 'node:http';
import { test as base, expect } from '@playwright/test';
import { Agent, Workspace } from '../../../src/lib/api/generated/schemas';
import type { AgentCreateRequestMain, WorkspaceCreateRequest } from '../../../src/lib/api/generated/openapi-types';
import { GatewayProcess } from './gateway-process';
import { E2E_FALLBACK } from './e2e-model';

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

async function startProbeProvider() {
  const unexpected: string[] = [];
  const server = http.createServer((req, res) => {
    req.resume();
    req.on('end', () => {
      res.setHeader('Content-Type', 'application/json');
      if (req.method === 'GET' && req.url?.includes('/models')) {
        res.end(JSON.stringify({ object: 'list', data: [{ id: E2E_FALLBACK.model, object: 'model' }] }));
      } else if (req.method === 'POST' && req.url?.includes('/chat/completions')) {
        res.end(JSON.stringify({ id: 's1-onboarding-probe', object: 'chat.completion', model: E2E_FALLBACK.model,
          choices: [{ index: 0, message: { role: 'assistant', content: 'probe ok' }, finish_reason: 'stop' }],
          usage: { prompt_tokens: 1, completion_tokens: 1, total_tokens: 2 } }));
      } else {
        unexpected.push(`${req.method} ${req.url}`); res.statusCode = 404; res.end('{}');
      }
    });
  });
  await new Promise<void>((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve); });
  const address = server.address();
  if (!address || typeof address === 'string') throw new Error('S1 probe provider did not bind');
  return { url: `http://127.0.0.1:${address.port}`, unexpected,
    close: () => new Promise<void>((resolve, reject) => { server.close((err) => err ? reject(err) : resolve()); server.closeAllConnections(); }) };
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
    const provider = await startProbeProvider();
    let gateway: GatewayProcess | undefined;
    try {
      gateway = await GatewayProcess.start({ apiBase: provider.url, env: { PATH: directory + path.delimiter + (process.env.PATH ?? '') } });
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
        seedPrivateGH_TOKEN: async () => { throw new Error('BLOCKED: selected KindAgentPrivate GH_TOKEN publication/readback fixture is not connected to this private browser gateway — required by S-12; an ordinary credential Set is not the Given'); },
      });
      expect(provider.unexpected).toEqual([]);
    } finally {
      fs.writeFileSync(release, 'release');
      try { if (gateway) await gateway.stop(); }
      finally { await provider.close(); fs.rmSync(directory, { recursive: true, force: true }); }
    }
  },
});
export { expect } from '@playwright/test';

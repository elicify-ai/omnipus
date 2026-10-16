import { createServer } from 'node:http';
import { once } from 'node:events';
import { execFileSync } from 'node:child_process';
import * as fs from 'node:fs';
import * as path from 'node:path';
import { request as apiRequest } from '@playwright/test';
import { DEFAULT_OMNIPUS_BINARY, getFreePort, startGateway, stopGateway, type GatewayHandle } from '../setup.js';

/** Transport address for a disposable gateway, never a production config type. */
export interface OwnershipBrowserTarget {
  baseURL: string;
  token: string;
}

export interface OwnershipCookieFixture extends OwnershipBrowserTarget {
  origin: string;
  agentA: string;
  agentB: string;
  chatA: string;
  chatB: string;
  workspaceA: string;
  workspaceB: string;
  profileA: string;
  profileB: string;
  close(): Promise<void>;
}

/**
 * Reuse the CI installer, not Playwright's browser and not a shared profile.
 * The path is an executable only; the private gateway keeps its own profiles.
 */
function provisionedChrome(binary: string): string {
  const explicit = process.env.OMNIPUS_TOOLS_BROWSER_EXEC_PATH;
  if (explicit) return explicit;
  if (!process.env.OMNIPUS_HOME) throw new Error('BLOCKED: OMNIPUS_HOME is required to resolve the test installer, never the real user home');
  const result = execFileSync(binary, ['browser', 'provision'], { encoding: 'utf8' });
  const chrome = /^exec_path=(.+)$/m.exec(result)?.[1]?.trim();
  if (!chrome || !fs.existsSync(chrome)) throw new Error('BLOCKED: browser provision did not return an installed Chrome executable');
  return chrome;
}

/**
 * UAT-09b originally used httpbin.org, whose transient HTTP 502 reproduced the
 * CI positive-control failure. This process-edge fixture echoes the REAL
 * browser cookie header. It never fabricates a workspace jar or browser state.
 * Only this throwaway gateway allows the loopback fixture; shared CI and
 * production configuration are never read-modify-written.
 */
export async function startOwnershipCookieFixture(): Promise<OwnershipCookieFixture> {
  const server = createServer((req, res) => {
    const url = new URL(req.url ?? '/', 'http://127.0.0.1');
    res.setHeader('Cache-Control', 'no-store');
    if (url.pathname === '/cookies/set' && url.searchParams.get('uatws') === 'ownA') {
      res.writeHead(302, { 'Set-Cookie': 'uatws=ownA; Path=/; SameSite=Lax', Location: '/cookies' });
      res.end();
      return;
    }
    if (url.pathname !== '/cookies') {
      res.writeHead(404);
      res.end('fixture path not found');
      return;
    }
    const cookies: Record<string, string> = {};
    for (const part of (req.headers.cookie ?? '').split(';')) {
      const [name, ...value] = part.trim().split('=');
      if (name === 'uatws') cookies[name] = value.join('=');
    }
    res.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8' });
    // Fill the viewport so the production point-inspect at (200,100) reads
    // this echo rather than an unrelated empty body margin.
    res.end(`<!doctype html><html><body style="margin:0"><pre style="margin:0;min-height:100vh">${JSON.stringify({ cookies }, null, 2)}</pre></body></html>`);
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  const address = server.address();
  if (address === null || typeof address === 'string') throw new Error('cookie fixture did not bind a TCP port');
  let gateway: GatewayHandle | null = null;
  const close = async (): Promise<void> => {
    if (gateway && gateway.process.exitCode === null) {
      const exited = once(gateway.process, 'exit');
      gateway.process.kill('SIGTERM');
      await exited;
    }
    await stopGateway(gateway);
    await new Promise<void>((resolve, reject) => server.close((err) => err ? reject(err) : resolve()));
  };
  try {
    const binary = process.env.OMNIPUS_BINARY ?? DEFAULT_OMNIPUS_BINARY;
    const chrome = provisionedChrome(binary);
    gateway = await startGateway({
      port: await getFreePort(), binary, skipOnboarding: true,
      // Fixture-internal configuration of THIS disposable instance only.
      // No provider is configured; this is browser ownership, not an LLM test.
      fixtureConfig: {
        gateway: { host: '127.0.0.1', dev_mode_bypass: true },
        sandbox: { ssrf: { enabled: true, allow_internal: ['127.0.0.1'] } },
        tools: { browser: { enabled: true, headless: true, exec_path: chrome } },
      },
    });
    const api = await apiRequest.newContext({ baseURL: gateway.baseURL });
    try {
      const state = await api.get('/api/v1/state');
      if (!state.ok()) throw new Error(`private gateway state returned ${state.status()}`);
      const csrf = (await api.storageState()).cookies.find((cookie) => cookie.name === 'csrf')?.value;
      if (!csrf) throw new Error('private gateway did not issue its own csrf cookie');
      const headers = { 'X-CSRF-Token': csrf, Origin: gateway.baseURL };
      const createPair = async (suffix: string): Promise<{ agent: string; chat: string; workspace: string }> => {
        const created = await api.post('/api/v1/agents', { headers, data: { type: 'Main', name: `cookie-${suffix}-${Date.now()}`, soul: 'Browser cookie isolation probe; never a model turn.' } });
        if (!created.ok()) throw new Error(`private probe agent returned ${created.status()}: ${await created.text()}`);
        const agent = ((await created.json()) as { id: string }).id;
        const workspace = await api.post('/api/v1/workspaces', { headers, data: { name: `Cookie-${suffix}`, core_team: [agent] } });
        if (!workspace.ok()) throw new Error(`private workspace returned ${workspace.status()}: ${await workspace.text()}`);
        const chat = await api.post('/api/v1/sessions', { headers, data: { agent_id: agent, type: 'chat' } });
        if (!chat.ok()) throw new Error(`private chat returned ${chat.status()}: ${await chat.text()}`);
        return { agent, chat: ((await chat.json()) as { id: string }).id, workspace: ((await workspace.json()) as { id: string }).id };
      };
      const a = await createPair('A');
      const b = await createPair('B');
      // No session workspace preference is forged. Read back the actual
      // unique-membership premise used by the production browser resolver.
      const rosterResponse = await api.get('/api/v1/workspaces');
      if (!rosterResponse.ok()) throw new Error(`private workspace roster returned ${rosterResponse.status()}`);
      const roster = (await rosterResponse.json()) as Array<{ id: string; core_team?: string[] }>;
      for (const pair of [a, b]) {
        const memberships = roster.filter((row) => row.core_team?.includes(pair.agent)).map((row) => row.id);
        if (memberships.length !== 1 || memberships[0] !== pair.workspace) {
          throw new Error(`BLOCKED: cookie probe ${pair.agent} membership [${memberships.join(',')}] does not identify exactly its workspace ${pair.workspace}`);
        }
      }
      const token = fs.readFileSync(path.join(gateway.homeDir, 'cli.token'), 'utf8').trim();
      if (!token) throw new Error('private gateway did not mint a WebSocket CLI credential');
      return {
        baseURL: gateway.baseURL, token, origin: `http://127.0.0.1:${address.port}`,
        agentA: a.agent, agentB: b.agent, chatA: a.chat, chatB: b.chat,
        workspaceA: a.workspace, workspaceB: b.workspace,
        profileA: path.join(gateway.homeDir, 'browser', 'profiles', `ws-${a.workspace}`),
        profileB: path.join(gateway.homeDir, 'browser', 'profiles', `ws-${b.workspace}`), close,
      };
    } finally {
      await api.dispose();
    }
  } catch (err) {
    await close();
    throw err;
  }
}

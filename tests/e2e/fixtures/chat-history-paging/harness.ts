import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { test as base, expect, type Page, type TestInfo, type Route } from '@playwright/test';
import { Agent, Workspace, Session, SessionHistoryResponse } from '../../../../src/lib/api/generated/schemas';
import type { AgentCreateRequestMain, WorkspaceCreateRequest, SessionCreateRequest } from '../../../../src/lib/api/generated/openapi-types';
import { WsFrame } from '../../../../src/lib/api/generated/ws-schemas';
import { GatewayProcess } from '../gateway-process';
import { startTerminalOutcomeProvider } from '../terminal-outcome-provider';
import { createTerminalSession } from '../terminal-outcome-acceptance';
import { openSessionByDeepLink } from '../session-setup';
import { chatInput, waitForConnected } from '../selectors';
import { seedChatHistoryDayPartitions, type ChatHistoryFixtureDay } from '../chat-history';
import { E2E_MODEL } from '../e2e-model';
import { startHistoryControlProvider, type HistoryControlKind, type HistoryControlProvider } from './control-provider';
import { assertNativeBashPolicy } from './agent-tool-policy';
type HistoryProvider = Awaited<ReturnType<typeof startTerminalOutcomeProvider>> | HistoryControlProvider;

const REPO = fileURLToPath(new URL('../../../../', import.meta.url));
export const UNAVAILABLE = 'Session history is unavailable. Retry after the operator restores its index.';
export const BUDGET = 4_194_304; // D4a: 4 MiB, not an observed product value.
export const POSITION = /^hp1_[A-Za-z0-9_-]{43}$/;
export const historyScroller = (page: Page) => page.getByTestId('virtualized-message-list');
export const historyRow = (page: Page, id: string) => page.locator(`[data-message-id="${id}"]`);
export const fixtureId = (label: string, n: number) => `${label}-${String(n).padStart(6, '0')}`;

// not-wire-format: fixture configuration only; appends use pkg/session types.
export type Seed = { label: string; count: number; start?: number; shape?: 'text' | 'varied' | 'tools' | 'normal'; bodyBytes?: number; clearAt?: number[]; imageURL?: string };
export type Traffic = ReturnType<typeof observeTraffic>;

/** Observe real network delivery. No WebSocket replacement and no synthetic frames. */
export function observeTraffic(page: Page) {
  const frames: WsFrame[] = [];
  const attaches: Extract<WsFrame, { type: 'attach_session' }>[] = [];
  const failures: string[] = [];
  const pages: { url: string; status: number; body: unknown }[] = [];
  const sends: Extract<WsFrame, { type: 'message' }>[] = [];
  page.on('websocket', (socket) => {
    // A development build also opens Vite's HMR socket. Validate every real
    // gateway chat frame, not unrelated development-server protocol messages.
    if (new URL(socket.url()).pathname !== '/api/v1/chat/ws') return;
    socket.on('framereceived', ({ payload }) => {
      const parsed = WsFrame.safeParse(JSON.parse(payload.toString()));
      if (!parsed.success) {
        failures.push(`gateway frame violates generated paging contract: ${parsed.error.message}`);
      } else frames.push(parsed.data);
    });
    socket.on('framesent', ({ payload }) => {
      const frame = WsFrame.parse(JSON.parse(payload.toString()));
      if (frame.type === 'attach_session') attaches.push(frame);
      if (frame.type === 'message') sends.push(frame);
    });
  });
  page.on('response', async (response) => {
    const url = new URL(response.url());
    if (!/^\/api\/v1\/sessions\/[^/]+\/messages$/.test(url.pathname)) return;
    try {
      pages.push({ url: response.url(), status: response.status(), body: await response.json() });
    } catch (error) {
      failures.push(`history response decoding failed: ${String(error)}`);
    }
  });
  return { frames, attaches, failures, pages, sends };
}

/** CI prebuilds this test-tree appender once, just like the session test driver.
 * Failed Playwright worker restarts must not trigger repeated Go compilation. */
function canonicalSeed(home: string, sessionId: string, seed: Seed) {
  const seedBinary = process.env.OMNIPUS_CHAT_HISTORY_SEED_BINARY;
  if (!seedBinary || !path.isAbsolute(seedBinary) || !fs.existsSync(seedBinary)) {
    throw new Error('BLOCKED: prebuilt canonical append fixture not supplied (OMNIPUS_CHAT_HISTORY_SEED_BINARY); compile the test-tree seed command once before E2E, never inside an assertion');
  }
  const args = ['--sessions-root', path.join(home, 'sessions'), '--session', sessionId,
    '--label', seed.label, '--count', String(seed.count), '--start', String(seed.start ?? 0),
    '--shape', seed.shape ?? 'text', '--body-bytes', String(seed.bodyBytes ?? 0)];
  if (seed.clearAt) args.push('--clear-at', seed.clearAt.join(','));
  if (seed.imageURL) args.push('--image-url', seed.imageURL);
  const receipt = execFileSync(seedBinary, args, { cwd: REPO, encoding: 'utf8' });
  const decoded: unknown = JSON.parse(receipt);
  expect(decoded, 'the real canonical writer accepted exactly the requested fixture').toMatchObject({
    session_id: sessionId, count: seed.count, canonical_writer: 'UnifiedStore.AppendTranscriptAddressed',
  });
  return decoded;
}

export class HistoryHarness {
  readonly traffic: Traffic;
  constructor(readonly page: Page, readonly gateway: GatewayProcess, readonly ids: Awaited<ReturnType<typeof createTerminalSession>>,
    readonly provider: HistoryProvider, readonly info: TestInfo, readonly logPath: string) {
    this.traffic = observeTraffic(page);
  }
  get sessionId() { return this.ids.session.id; }
  async seed(seed: Seed, sessionId = this.sessionId) {
    await this.gateway.kill9();
    this.gateway.assertStoppedForFixture(); // Observe actual exit, not kill9's timeout completion.
    canonicalSeed(this.gateway.homeDir, sessionId, seed);
    await this.gateway.restart();
  }
  async seedDays(days: readonly ChatHistoryFixtureDay[], sessionId = this.sessionId) {
    await this.gateway.kill9();
    this.gateway.assertStoppedForFixture();
    const receipt = await seedChatHistoryDayPartitions(this.gateway, sessionId, days);
    await this.gateway.restart();
    return receipt;
  }
  async createSession() {
    const result = await this.gateway.apiFetch('POST', '/api/v1/sessions', {
      type: 'chat', agent_id: this.ids.agent.id, workspace_id: this.ids.session.workspace_id,
    } satisfies SessionCreateRequest);
    expect(result.status, result.raw).toBe(201);
    return Session.parse(result.body).id;
  }
  async authenticate() {
    const state = await this.gateway.browserStorageState();
    // Reuse the fixture's current cookie, never log in again and overwrite its
    // single session-token slot. Every test owns an empty browser context.
    await this.page.context().clearCookies();
    await this.page.context().addCookies(state.cookies);
  }
  async open(sessionId = this.sessionId, options: { currentControlMayLockComposer?: boolean } = {}) {
    const prior = this.traffic.frames.length;
    if (options.currentControlMayLockComposer) {
      // A real pending AskUserQuestion intentionally locks the composer. Its
      // enabled state is NOT a catch-up-completion barrier for this fresh viewer.
      await this.page.goto(`${this.gateway.baseURL}/#/sessions/${sessionId}`);
      await expect(this.page.locator(`[data-active-session-id="${sessionId}"]`)).toBeVisible();
      await expect(chatInput(this.page)).toBeVisible();
      await waitForConnected(this.page);
    } else await openSessionByDeepLink(this.page, sessionId, this.gateway.baseURL);
    await expect.poll(() => this.traffic.frames.slice(prior).filter((frame) =>
      frame.type === 'catch_up_complete' && frame.session_id === sessionId).length,
    { message: 'opening readiness waits for catch_up_complete, never replay done' }).toBe(1);
    expect(this.traffic.failures, 'real gateway emits the spec/generated paging shapes').toEqual([]);
    await settled(this.page);
  }
  snapshot(sessionId = this.sessionId) {
    const frame = [...this.traffic.frames].reverse().find((f) => f.type === 'session_snapshot' && f.session_id === sessionId);
    expect(frame?.type, 'a successful history snapshot is required, not empty/live-only success').toBe('session_snapshot');
    if (!frame || frame.type !== 'session_snapshot') throw new Error('BLOCKED: bounded session_snapshot required by D7');
    return frame;
  }
  async read(query: Record<string, string | number> = {}, sessionId = this.sessionId) {
    const response = await this.page.request.get(`${this.gateway.baseURL}/api/v1/sessions/${sessionId}/messages`, { params: query });
    expect(response.status(), await response.text()).toBe(200);
    const parsed = SessionHistoryResponse.parse(await response.json());
    expect(parsed.session_id, 'same addressed session').toBe(sessionId);
    assertPage(parsed);
    return parsed;
  }
  async record(stage: string, extra: unknown = {}) {
    await this.info.attach(stage, { contentType: 'application/json', body: JSON.stringify({
      stage, session: this.sessionId, geometry: await geometry(this.page), ...object(extra),
      attaches: this.traffic.attaches, pages: this.traffic.pages,
    }, null, 2) });
    await this.info.attach(`${stage}-screenshot`, { contentType: 'image/png', body: await this.page.screenshot({ fullPage: true }) });
  }
}
function object(value: unknown): Record<string, unknown> {
  return typeof value === 'object' && value !== null ? value as Record<string, unknown> : { value };
}

async function createControlSessions(gateway: GatewayProcess, kind: HistoryControlKind) {
  // Eight rounds accommodate discovery + actual control + resumed stream; the
  // old terminal-outcome fixture deliberately stays cap1 and is not weakened.
  const result = await gateway.apiFetch('POST', '/api/v1/agents', { type: 'Main', name: 'History control acceptance',
    soul: 'Use the requested tool. Ask the human when their format choice is needed.', provider: 'openrouter', model: E2E_MODEL,
    // The native approval scenario needs the real operator-facing grant on
    // this custom agent. Ask remains Ask under the global ceiling; it does
    // not auto-approve or bypass the actual decision being tested.
    tool_policy_changes: kind === 'approval' ? { set: { bash: 'ask' } } : undefined,
    max_tool_iterations: 8 } satisfies AgentCreateRequestMain);
  expect(result.status, result.raw).toBe(201);
  const agent = Agent.parse(result.body);
  expect(agent.max_tool_iterations).toBe(8);
  const workspaceResult = await gateway.apiFetch('POST', '/api/v1/workspaces', {
    name: 'History control acceptance', core_team: [agent.id],
  } satisfies WorkspaceCreateRequest);
  expect(workspaceResult.ok, workspaceResult.raw).toBe(true);
  const workspace = Workspace.parse(workspaceResult.body);
  const sessionResult = await gateway.apiFetch('POST', '/api/v1/sessions', {
    type: 'chat', agent_id: agent.id, workspace_id: workspace.id,
  } satisfies SessionCreateRequest);
  expect(sessionResult.status, sessionResult.raw).toBe(201);
  const otherResult = await gateway.apiFetch('POST', '/api/v1/sessions', {
    type: 'chat', agent_id: agent.id, workspace_id: workspace.id,
  } satisfies SessionCreateRequest);
  expect(otherResult.status, otherResult.raw).toBe(201);
  return { agent, session: Session.parse(sessionResult.body), otherSession: Session.parse(otherResult.body) };
}

export const test = base.extend<{ history: HistoryHarness; historyControlKind: 'none' | HistoryControlKind }>({
  historyControlKind: ['none', { option: true }],
  history: async ({ page, historyControlKind }, use, info) => {
    const provider = historyControlKind === 'none' ? await startTerminalOutcomeProvider() : await startHistoryControlProvider(historyControlKind);
    let gateway: GatewayProcess | undefined;
    const logPath = info.outputPath('history-gateway.jsonl');
    fs.mkdirSync(path.dirname(logPath), { recursive: true });
    try {
      gateway = await GatewayProcess.start({ apiBase: provider.url, adminUsername: 'history-reader',
        env: { OMNIPUS_LOG_FILE: logPath, OMNIPUS_LOG_LEVEL: 'debug' } });
      const ids = historyControlKind === 'none' ? await createTerminalSession(gateway) : await createControlSessions(gateway, historyControlKind);
      if (historyControlKind === 'approval') {
        await gateway.kill9();
        gateway.assertStoppedForFixture();
        // not-wire-format: isolated private gateway config, not a SPA payload.
        const filename = path.join(gateway.homeDir, 'config.json');
        const config = JSON.parse(fs.readFileSync(filename, 'utf8')) as { sandbox?: { auto_approve?: boolean; tool_policies?: Record<string, string> } };
        config.sandbox = { ...config.sandbox, auto_approve: false,
          tool_policies: { ...config.sandbox?.tool_policies, bash: 'ask' } };
        fs.writeFileSync(filename, JSON.stringify(config), { mode: 0o600 });
        await gateway.restart();
        await assertNativeBashPolicy(gateway, ids.agent.id, 'ask', 'ask');
      }
      const harness = new HistoryHarness(page, gateway, ids, provider, info, logPath);
      await use(harness);
    } finally {
      provider.finishToolRound();
      try { if (gateway) { await gateway.kill9(); await gateway.stop(); } }
      finally { await provider.close(); }
    }
  },
});
// Per-file test.use/retries are explicit in each spec: module caching means
// top-level configuration in this shared import only reaches its first importer.

export function assertPage(page: ReturnType<typeof SessionHistoryResponse.parse>) {
  expect(page.page_info.soft_byte_budget, 'D4a positive budget').toBeGreaterThan(0);
  expect(page.entries.length, 'server source-group request cap').toBeLessThanOrEqual(100);
  expect(page.has_earlier, 'earlier cursor is present iff the flag is true').toBe(page.next_before !== undefined);
  expect(page.has_later, 'later cursor is present iff the flag is true').toBe(page.next_after !== undefined);
  for (const entry of page.entries) expect(entry.history_position, 'opaque same-occurrence identity').toMatch(POSITION);
  if (page.entries.length) {
    expect(page.page_info.first_position).toBe(page.entries[0].history_position);
    expect(page.page_info.last_position).toBe(page.entries.at(-1)?.history_position);
  } else expect([page.has_earlier, page.has_later, page.next_before, page.next_after]).toEqual([false, false, undefined, undefined]);
}

/** Two browser layout/paint turns are an event barrier, not a wall-clock sleep. */
export async function settled(page: Page) {
  await page.evaluate(() => new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))));
}
export async function geometry(page: Page) {
  return historyScroller(page).evaluate((element) => {
    const box = element.getBoundingClientRect();
    const rows = [...element.querySelectorAll<HTMLElement>('[data-message-id]')].map((row) => {
      const rect = (row.closest('[data-index]') ?? row).getBoundingClientRect();
      return { id: row.dataset.messageId!, top: rect.top - box.top, height: rect.height };
    });
    return { H: element.clientHeight, top: element.scrollTop, height: element.scrollHeight,
      bottom: element.scrollHeight - element.scrollTop - element.clientHeight, rows };
  });
}
export async function anchor(page: Page) {
  const view = await geometry(page);
  const row = view.rows.find((r) => r.top <= 0 && r.top + r.height > 0) ?? view.rows.find((r) => r.top >= 0 && r.top < view.H);
  expect(row, 'instrument must see a genuinely visible stable row').toBeDefined();
  if (!row) throw new Error('BLOCKED: no visible row for geometry instrument');
  return row;
}
export async function expectAnchor(page: Page, saved: Awaited<ReturnType<typeof anchor>>) {
  await settled(page);
  const top = await historyRow(page, saved.id).evaluate((row) => {
    const scroller = row.closest('[data-testid="virtualized-message-list"]');
    if (!scroller) throw new Error('anchor is outside Conversation history');
    return (row.closest('[data-index]') ?? row).getBoundingClientRect().top - scroller.getBoundingClientRect().top;
  });
  expect(Math.abs(top - saved.top), 'D8 settled current-reader drift ≤1 CSS pixel').toBeLessThanOrEqual(1);
}
export async function scrollToDistance(page: Page, distance: number) {
  await historyScroller(page).evaluate((el, top) => { el.scrollTop = top; el.dispatchEvent(new Event('scroll')); }, distance);
  await settled(page);
}

/** Hold delivery of the REAL selected page; change only transport success/failure. */
export async function holdPage(page: Page, predicate: (url: URL) => boolean) {
  let release!: (status?: number) => void;
  const released = new Promise<number | undefined>((resolve) => { release = resolve; });
  let observed!: (route: Route) => void;
  const requested = new Promise<Route>((resolve) => { observed = resolve; });
  const requests: string[] = [];
  const abortedOrHandledRequests: string[] = [];
  let abortedOrHandled = false;
  const handler = async (route: Route) => {
    if (!predicate(new URL(route.request().url()))) { await route.continue(); return; }
    requests.push(route.request().url());
    observed(route);
    const response = await route.fetch();
    const status = await released;
    try {
      if (status !== undefined) await route.fulfill({ status, json: { error: 'deterministic history transport failure' } });
      else await route.fulfill({ response });
    } catch (error) {
      if (!(error instanceof Error) || !['Route is already handled!', 'route.fulfill: Route is already handled!'].includes(error.message)) throw error;
      // Keep the interrupted app transition observable; it is not a successful delivery.
      abortedOrHandled = true;
      abortedOrHandledRequests.push(route.request().url());
    }
  };
  await page.route('**/api/v1/sessions/*/messages?*', handler);
  return { requested, requests, release, get abortedOrHandled() { return abortedOrHandled; }, abortedOrHandledRequests,
    dispose: () => page.unroute('**/api/v1/sessions/*/messages?*', handler) };
}
export async function assertComposerReady(page: Page) {
  await expect(chatInput(page)).toBeEnabled();
}

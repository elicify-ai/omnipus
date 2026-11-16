import fs from 'node:fs';
import path from 'node:path';
import { expect, type Page, type TestInfo } from '@playwright/test';
import { Agent, Session, SessionDetail, Workspace, WorkspaceDelegation } from '../../../src/lib/api/generated/schemas';
import type { AgentCreateRequest, SessionCreateRequest } from '../../../src/lib/api/generated/openapi-types';
import { GatewayProcess } from './gateway-process';
import { E2E_FALLBACK } from './e2e-model';
import { openSessionByDeepLink } from './session-setup';
import { waitForConnected, chatInput } from './selectors';
import { startHelperNoticesProvider, type NoticeScriptOptions } from './helper-notices-provider';
import { installNoticeLink } from './helper-notices-link';
import { removeSessionHistoryIndex } from './chat-history';
import { visibleNamedRows } from './helper-notices-visible';
import { installNoticePaintRecorder } from './helper-notices-paint';
import { installNoticeCatchupRecorder } from './helper-notices-catchup';
import { directNoticeLink } from './helper-notices-native';

// not-wire-format: private scenario/traffic instrumentation, not a gateway DTO.
export type NoticeScene = Awaited<ReturnType<typeof createNoticeScene>>;
export type NoticeTraffic = ReturnType<typeof observeNotices>;
export function observeNotices(page: Page, existing?: { frames: Record<string, unknown>[]; receivedAt: number[]; sends: Record<string, unknown>[] }) {
  const frames: Record<string, unknown>[] = existing?.frames ?? [];
  const receivedAt: number[] = existing?.receivedAt ?? [];
  const sends: Record<string, unknown>[] = existing?.sends ?? [];
  page.on('websocket', socket => {
    if (!socket.url().includes('/api/v1/chat/ws')) return;
    socket.on('framereceived', ({ payload }) => {
      receivedAt.push(performance.now());
      frames.push(JSON.parse(payload.toString()) as Record<string, unknown>);
    });
    socket.on('framesent', ({ payload }) => { sends.push(JSON.parse(payload.toString()) as Record<string, unknown>); });
  });
  return { frames, receivedAt, sends };
}

export async function createNoticeScene(page: Page, info: TestInfo, options: NoticeScriptOptions & { duplicateNames?: boolean; unavailableHistory?: boolean; helperNames?: string[]; nativeNotices?: boolean }) {
  if (process.env.HEAVY_SLOT && process.env.HEAVY_SLOT !== 'remote') throw new Error('REFUSED: helper-notice browser tests require a runner, never local heavy fallback');
  const provider = await startHelperNoticesProvider(options);
  let gateway: GatewayProcess | undefined;
  const traffic = observeNotices(page);
  const childrenPages: Page[] = [];
  try {
    gateway = await GatewayProcess.start({ apiBase: provider.url });
    // Configure only the owned, STOPPED throwaway home. No production source,
    // live instance, paid key or command-line model service is involved.
    await gateway.kill9();
    gateway.assertStoppedForFixture();
    const file = path.join(gateway.homeDir, 'config.json');
    const cfg = JSON.parse(fs.readFileSync(file, 'utf8')) as Record<string, unknown>;
    const models = cfg.providers as Array<Record<string, unknown>>;
    expect(models.length, 'instrument: onboarding installed the real provider row').toBeGreaterThan(0);
    for (const m of models) { m.api_base = provider.url; m.request_timeout = options.script === 'timeout' ? 3 : 120; }
    const sandbox = (cfg.sandbox ?? {}) as Record<string, unknown>;
    sandbox.tool_policies = { ...(sandbox.tool_policies as object), bash: 'allow', delegate: 'allow' };
    cfg.sandbox = sandbox;
    fs.writeFileSync(file, JSON.stringify(cfg), { mode: 0o600 });
    await gateway.restart();
    // Duplicate-name dataset is two chats delegated to ONE agent, not two
    // different agents with the same display name (spec row 9).
    const delegationCount = options.nested ? 1 : options.count ?? 1;
    const workerCount = options.nested || options.duplicateNames ? 1 : delegationCount;
    const agents: Array<ReturnType<typeof Agent.parse>> = [];
    for (let i = 0; i < workerCount; i++) {
      const data = { type: 'Subagent', name: options.helperNames?.[i] ?? (options.duplicateNames ? 'Researcher' : i === 0 ? 'Researcher' : i === 1 ? 'Writer' : `Helper ${i}`),
        description: 'Isolated model notice acceptance worker', soul: 'Only execute the exact HM_HELPER task. Never explore.',
        provider: E2E_FALLBACK.provider, model: E2E_FALLBACK.model,
        tool_policy_changes: { set: { bash: 'allow', delegate: 'allow' } },
        fallback_models: options.script === 'fallback' ? [{ provider: E2E_FALLBACK.provider, model: 'hm-fallback' }] : [],
      } satisfies AgentCreateRequest;
      const result = await gateway.apiFetch('POST', '/api/v1/agents', data);
      expect(result.status, result.raw).toBe(201); agents.push(Agent.parse(result.body));
    }
    const parentResult = await gateway.apiFetch('POST', '/api/v1/agents', {
      type: 'Main', name: 'Notice Main', soul: 'Delegate the exact HM_ROOT task and do nothing else.',
      provider: E2E_FALLBACK.provider, model: E2E_FALLBACK.model, fallback_models: [],
      tool_policy_changes: { set: { delegate: 'allow' } },
    } satisfies AgentCreateRequest);
    expect(parentResult.status, parentResult.raw).toBe(201);
    const parent = Agent.parse(parentResult.body);
    provider.setAgents(Array.from({ length: delegationCount }, (_, i) => agents[options.duplicateNames ? 0 : i].id));
    const createdWs = await gateway.apiFetch('POST', '/api/v1/workspaces', { name: 'Helper Notice Test', core_team: [parent.id, ...agents.map(a => a.id)] });
    expect(createdWs.status, createdWs.raw).toBe(201);
    const workspace = Workspace.parse(createdWs.body);
    const graphResult = await gateway.apiFetch('GET', `/api/v1/workspaces/${workspace.id}/delegation`);
    expect(graphResult.status).toBe(200);
    const graph = WorkspaceDelegation.parse(graphResult.body);
    const graphSaved = await gateway.apiFetch('PUT', `/api/v1/workspaces/${workspace.id}/delegation`, {
      revision: graph.revision, edges: [...agents.map(a => ({ from_agent: parent.id, to_agent: a.id, modes: ['direct'] })),
        ...(options.nested ? [{ from_agent: agents[0].id, to_agent: agents[0].id, modes: ['direct'] }] : [])],
    });
    expect(graphSaved.status, graphSaved.raw).toBe(200);
    const created = await gateway.apiFetch('POST', '/api/v1/sessions', { type: 'chat', agent_id: parent.id, workspace_id: workspace.id } satisfies SessionCreateRequest);
    expect(created.status, created.raw).toBe(201);
    const root = Session.parse(created.body);
    if (options.unavailableHistory) {
      // Fault the derived index BEFORE any helper exists, with the owned
      // gateway stopped. Later live_only catch-up must not restart a turn.
      await gateway.kill9();
      await removeSessionHistoryIndex(gateway, root.id);
      await gateway.restart();
      const unavailable = await gateway.apiFetch('GET', `/api/v1/sessions/${root.id}`);
      expect(unavailable.status).toBe(200);
      expect(SessionDetail.parse(unavailable.body).history_state).toMatchObject({ status: 'unavailable', reason: 'index_missing' });
    }
    const catchupReceipt = await installNoticeCatchupRecorder(page, root.id);
    const link = options.nativeNotices ? directNoticeLink(page, traffic) : await installNoticeLink(page);
    let nativeMain = options.nativeNotices ?? false;
    link.setRoot(root.id);
    await page.context().addCookies((await gateway.browserStorageState()).cookies);
    let mainPaints = await installNoticePaintRecorder(page, info, 'main');
    await openSessionByDeepLink(page, root.id, gateway.baseURL);
    await waitForConnected(page, { timeout: 15_000 });
    await catchupReceipt.first(info, 'initial');
    await chatInput(page).fill('HM_ROOT_TASK. Delegate the exact scripted helper tasks.');
    await chatInput(page).press('Enter');
    await expect.poll(() => provider.helpers.filter(h => h.calls > 0).length, { message: 'positive control: real helper requests reached the fake model', timeout: 20_000 }).toBe(options.count ?? 1);
    await expect.poll(() => provider.evidence.delegateResults.length, { message: 'positive control: real delegate execution returned to the parent', timeout: 15_000 }).toBeGreaterThanOrEqual(delegationCount);
    const childResult = await gateway.apiFetch('GET', `/api/v1/sessions?parent_session_id=${root.id}`);
    expect(childResult.status).toBe(200);
    let children = Session.array().parse((childResult.body as Record<string, unknown>).sessions);
    if (options.nested) {
      expect(children).toHaveLength(1);
      const grand = await gateway.apiFetch('GET', `/api/v1/sessions?parent_session_id=${children[0].id}`);
      expect(grand.status).toBe(200);
      children = [...children, ...Session.array().parse((grand.body as Record<string, unknown>).sessions)];
    }
    if (!options.nested) children.sort((a, b) => Number(a.title.replace('helper ', '')) - Number(b.title.replace('helper ', '')));
    if (options.duplicateNames) expect(children.map(c => c.agent_id), 'two distinct chats share the same agent identity').toEqual([agents[0].id, agents[0].id]);
    expect(children).toHaveLength(options.count ?? 1);
    const own = await page.context().newPage(); childrenPages.push(own);
    const ownTraffic = observeNotices(own);
    const ownPaints = await installNoticePaintRecorder(own, info, 'helper');
    await openSessionByDeepLink(own, children.at(-1)!.id, gateway.baseURL);
    await waitForConnected(own, { timeout: 15_000 });
    const detail = await gateway.apiFetch('GET', `/api/v1/sessions/${root.id}`);
    expect(detail.status).toBe(200);
    const parsedDetail = SessionDetail.parse(detail.body);
    if (options.unavailableHistory) {
      expect(parsedDetail.history_state.status).toBe('unavailable');
      // The real model's correlated tool result remains the positive control
      // even when the damaged derived index correctly withholds REST history.
      for (const child of children) expect(JSON.stringify(provider.evidence.delegateResults)).toContain(child.id);
    } else {
      const calls = parsedDetail.messages?.flatMap(m => m.tool_calls ?? []).filter(c => c.tool === 'delegate') ?? [];
      expect(calls.map(c => c.status), 'real delegate successes, not just a scripted sentence').toEqual(Array.from({ length: delegationCount }, () => 'success'));
    }
    const gw = gateway;
    return { gateway: gw, provider, get page() { return page; }, own, traffic, ownTraffic, children, agents, parent, root, workspace, link, catchupReceipt,
      async nativeCountdownPage() {
        if (!nativeMain) {
          // T10 needs the real held end BEFORE measurement. Archive it, then
          // park the routed page (and close its socket). Keep the original
          // fixture page alive for its independent teardown hooks.
          await mainPaints.attach();
          const context = page.context();
          await page.goto('about:blank');
          page = await context.newPage(); childrenPages.push(page);
          observeNotices(page, traffic);
          const warmup = await installNoticeCatchupRecorder(page, root.id);
          mainPaints = await installNoticePaintRecorder(page, info, 'main-native-reload');
          await openSessionByDeepLink(page, root.id, gw.baseURL);
          await waitForConnected(page);
          // Drain the actual page/parser completion BEFORE the measured fence.
          await warmup.first(info, 'native-warmup-before-measured-reload');
          nativeMain = true;
        }
        return page;
      },
      async releaseFailures() {
        if (!options.parentWriting && !options.parentRetry && !options.parentSilenceRetry) { provider.parentFinish.resolve(); await expect.poll(() => provider.evidence.parentDone).toBe(true); }
        for (let i = 0; i < provider.fail.length; i++) {
          const before = traffic.frames.filter(f => f.type === 'provider_retry').length;
          provider.fail[i].resolve();
          // Order the external failures by receipt of an actual existing
          // top retry frame, so first-notice order is deterministic even
          // when helper model requests arrived concurrently. No new child
          // fields are needed for this preimplementation instrument.
          const script = options.helperScripts?.[i] ?? options.script;
          if (['retry', 'repeat', 'fallback'].includes(script) && !(options.nested && i === 0)) {
            await expect.poll(() => traffic.frames.filter(f => f.type === 'provider_retry').length,
              { message: `real retry publication after releasing helper ${i}`, timeout: 15_000 }).toBeGreaterThan(before);
          }
        }
      },
      async close() {
        try {
          await info.attach('helper-model-provider-evidence', { body: JSON.stringify(provider.evidence, null, 2), contentType: 'application/json' });
          await info.attach('helper-model-raw-notice-traffic', { body: JSON.stringify({ top: traffic, own: ownTraffic, linkEvents: link.events,
            linkForwardedNotices: link.forwarded.filter(f => f.type === 'provider_retry' || f.type === 'provider_status') }, null, 2), contentType: 'application/json' });
          const paints = await Promise.allSettled([mainPaints.attach(), ownPaints.attach()]);
          for (const result of paints) if (result.status === 'rejected') throw result.reason;
        } finally {
          for (const p of childrenPages) await p.close();
          // Tear down the owned PID before releasing scripted answers:
          // cleanup must not accidentally start a new real 30 s tool.
          try { await gw.kill9(); } finally {
            try { await provider.close(); } finally { await gw.stop(); }
          }
        }
      },
    };
  } catch (error) {
    await info.attach('helper-model-setup-evidence', { body: JSON.stringify(provider.evidence, null, 2), contentType: 'application/json' });
    try { await gateway?.stop(); } finally { await provider.close(); }
    throw error;
  }
}

export async function visibleNamed(page: Page, name: string, clause: string) {
  return (await visibleNamedRows(page, name, clause)).map(row => row.text);
}
export async function expectNamed(scene: NoticeScene, name = 'Researcher', clause = 'Retrying automatically', receivedAt = performance.now()) {
  // Timing/FR-005: a prerequisite wait never grants a new display budget. For
  // recovery, callers supply the actual relevant main-copy receipt time.
  await expect.poll(() => visibleNamed(scene.page, name, clause), {
    message: `FR-005: ${name} named row in the real main chat`,
    timeout: Math.max(1, receivedAt + 2_000 - performance.now()),
  }).toHaveLength(1);
  expect(performance.now() - receivedAt, 'Timing: named row painted within 2 s of the relevant main-chat copy').toBeLessThanOrEqual(2_000);
}
export async function recordScene(scene: NoticeScene, info: TestInfo, label: string) {
  await info.attach(`${label}-main-admin`, { body: await scene.page.screenshot({ fullPage: true }), contentType: 'image/png' });
  await info.attach(`${label}-helper-admin`, { body: await scene.own.screenshot({ fullPage: true }), contentType: 'image/png' });
}

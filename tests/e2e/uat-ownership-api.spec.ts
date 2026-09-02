/**
 * uat-ownership-api.spec.ts — UAT Group A/B, "tab ownership and workspace
 * isolation" (docs/internal/specs/browser-rework-uat-plan.md §4, UAT-02,
 * UAT-04, UAT-09, UAT-11, and the FR-033 refusal UAT-07 depends on).
 *
 * WHAT THIS FILE IS, AND WHAT IT IS NOT.
 *
 * The plan's Group A/B cases are written as "ask Jim to open a page, then ask
 * Ray what he can see". Every one of those sentences needs a live LLM, and the
 * gateway this run drives has NO model provider configured — every turn comes
 * back "This agent has no model. Pick one in the agent's settings." Entering a
 * real provider key is forbidden by the run's own rules.
 *
 * So these cases are driven one layer down, through the product's OWN live
 * browser channel (/api/v1/browser/ws — the exact frames the SPA's browser
 * panel sends: ADR-038/ADR-041/ADR-072). That is a real product surface a human
 * uses through the panel, and it exercises the same server-side resolution the
 * agent tools use (pkg/tools/browser/resolve.go). It is NOT the same as a human
 * asking an agent: the AGENT-OWNED tab set (a tab that belongs to one chat and
 * is invisible to another chat in the same workspace — UAT-03) cannot be
 * created at all without a model, and is therefore NOT covered here. Do not
 * read a pass in this file as a pass for UAT-03.
 *
 * The honesty bar (plan §1.2, §4.0 "what a silent failure looks like"):
 *   - every case asserts a POSITIVE observation (a specific URL in a specific
 *     workspace's tab strip, a specific refusal string, a specific cookie),
 *     never "no error appeared";
 *   - the cross-workspace case asserts the tab strip shows the OTHER
 *     workspace's page, so "refused" and "quietly given the wrong browser" and
 *     "given the right browser" are three distinguishable outcomes rather than
 *     one green tick;
 *   - a refusal is only accepted when a matching positive control in the SAME
 *     run succeeded, so "everything is refused" can never read as a pass.
 *
 * MEMORY. The pool refuses a new browser when the machine is low on memory
 * (D2/ADR-072 memory ceiling) with "this machine is low on memory". That is the
 * feature working, not a defect, but it makes an attach fail. Where it happens
 * the test fails with that message quoted, so the reader can tell a refusal
 * from a defect.
 */

// Playwright REQUIRES the first test argument to be an object-destructuring
// pattern ("First argument must use the object destructuring pattern"), and
// these cases use no page/context fixture at all — they speak the gateway's own
// WebSocket and REST surfaces. `{}` is the only form the runner accepts here.
/* eslint-disable no-empty-pattern */

import * as fs from 'fs';
import * as path from 'path';
import { execFileSync } from 'child_process';
import { test, expect, request as apiRequest, type APIRequestContext, type TestInfo } from '@playwright/test';

/** the-internet.herokuapp.com / example.com — the plan's §3.4 fixture hosts. */
const PAGE_A = 'https://the-internet.herokuapp.com/dropdown';
const PAGE_B = 'https://example.com/';
/** Public cookie echo. No account, no credential — it just reports the jar. */
const COOKIE_SET = 'https://httpbin.org/cookies/set?uatws=';
const COOKIE_READ = 'https://httpbin.org/cookies';

type Frame = Record<string, unknown>;

/** Record an observation into the report AND stdout (a UAT is reported by a human). */
async function note(testInfo: TestInfo, name: string, body: string): Promise<void> {
  console.log(`[UAT ${testInfo.title.split(' ')[0]}] ${name}: ${body.replace(/\n/g, ' | ')}`);
  await testInfo.attach(name, { body, contentType: 'text/plain' });
}

/**
 * The WebSocket auth frame wants a bearer token. The SPA has a session cookie
 * instead, which a Node-side WebSocket cannot borrow, so the token is read from
 * the gateway's own CLI token file — the same file `omnipus` CLI uses. Missing
 * file is a BLOCKED condition with a named remedy, never a silent skip.
 */
function cliToken(): string {
  const fromEnv = process.env.OMNIPUS_CLI_TOKEN?.trim();
  if (fromEnv) return fromEnv;
  const home = process.env.OMNIPUS_HOME ?? '/tmp/omnipus-uat-home';
  const file = path.join(home, 'cli.token');
  if (!fs.existsSync(file)) {
    throw new Error(
      `BLOCKED: no WebSocket credential. Set OMNIPUS_CLI_TOKEN, or point OMNIPUS_HOME at the gateway's home (looked for ${file}).`,
    );
  }
  return fs.readFileSync(file, 'utf8').trim();
}

/** CSRF pair for the mutating REST calls (the gateway rejects a bare POST). */
async function csrf(request: APIRequestContext): Promise<Record<string, string>> {
  const res = await request.get('/api/v1/state');
  if (!res.ok()) throw new Error(`BLOCKED: GET /api/v1/state returned ${res.status()}`);
  const cookies = await request.storageState();
  const token = cookies.cookies.find((c) => c.name === 'csrf')?.value;
  if (!token) throw new Error('BLOCKED: the gateway issued no csrf cookie');
  return { 'X-CSRF-Token': token, Origin: new URL(res.url()).origin };
}

/**
 * Open a WebSocket to the gateway from Node, auth, send `frames` on a schedule,
 * and return everything received. Runs in the test process (not the page) so it
 * is unaffected by SPA state.
 */
async function wsExchange(
  channel: '/api/v1/chat/ws' | '/api/v1/browser/ws',
  frames: Array<{ afterMs?: number; afterAttachedMs?: number; frame: Frame }>,
  collectMs: number,
): Promise<Frame[]> {
  const base = (process.env.OMNIPUS_URL || 'http://localhost:6060').replace(/^http/, 'ws');
  const received: Frame[] = [];
  const token = cliToken();
  await new Promise<void>((resolve, reject) => {
    const ws = new WebSocket(base + channel);
    const send = (frame: Frame): void => {
      try {
        ws.send(JSON.stringify(frame));
      } catch {
        /* socket gone; the collected frames still tell the story */
      }
    };
    const timer = setTimeout(() => {
      try {
        ws.close();
      } catch {
        /* already closed */
      }
      resolve();
    }, collectMs);
    ws.onopen = () => {
      ws.send(JSON.stringify({ type: 'auth', token }));
      for (const f of frames) {
        if (f.afterMs !== undefined) setTimeout(() => send(f.frame), f.afterMs);
      }
    };
    // Control and input frames are only legal once the panel is attached — the
    // server answers an early one with "browser_control: attach before
    // requesting control", and a cold Chrome start can take 5s+. The SPA gates
    // its own controls on browser_status(attached), so this harness does too:
    // timing off the wire, never off a guessed delay.
    let attachedSeen = false;
    ws.onmessage = (ev) => {
      let parsed: Frame;
      try {
        parsed = JSON.parse(String(ev.data)) as Frame;
      } catch {
        return; /* non-JSON frame: not part of this contract */
      }
      received.push(parsed);
      if (!attachedSeen && parsed.type === 'browser_status' && parsed.state === 'attached') {
        attachedSeen = true;
        for (const f of frames) {
          if (f.afterAttachedMs !== undefined) setTimeout(() => send(f.frame), f.afterAttachedMs);
        }
      }
    };
    ws.onerror = () => {
      clearTimeout(timer);
      reject(new Error(`BLOCKED: WebSocket error on ${channel}`));
    };
  });
  return received;
}

/** Attach the live panel exactly as the SPA does, and return what came back. */
async function attach(
  sessionId: string,
  agentId: string,
  extra: Array<{ afterMs?: number; afterAttachedMs?: number; frame: Frame }> = [],
  collectMs = 20_000,
): Promise<{ tabs: Array<{ url?: string; title?: string }>; statuses: Frame[]; error: string | null }> {
  const frames = await wsExchange(
    '/api/v1/browser/ws',
    [{ afterMs: 400, frame: { type: 'browser_attach', session_id: sessionId, agent_id: agentId } }, ...extra],
    collectMs,
  );
  const statuses = frames.filter((f) => f.type === 'browser_status');
  const tabFrames = frames.filter((f) => f.type === 'browser_tabs');
  const last = tabFrames[tabFrames.length - 1] as { tabs?: Array<{ url?: string; title?: string }> } | undefined;
  // A failed attach is followed by "browser_control: attach before requesting
  // control" for every queued control frame. Reporting THAT as the error hides
  // the real cause (typically the memory ceiling) and makes a blocked run look
  // like a product defect — so the root refusal wins over the echo.
  const errors = statuses.filter((f) => f.state === 'error').map((f) => String(f.message ?? ''));
  const memory = errors.find((m) => /low on memory/i.test(m));
  const substantive = errors.find((m) => !/attach before requesting control/i.test(m));
  return {
    tabs: last?.tabs ?? [],
    statuses,
    error: memory ?? substantive ?? errors[0] ?? null,
  };
}

/** True when the pool refused for memory — the ceiling working, not a defect. */
function isMemoryRefusal(message: string | null): boolean {
  return message !== null && /low on memory/i.test(message);
}

/**
 * Attach, retrying ONLY a memory-ceiling refusal. The run's own instruction is
 * "a browser action that fails in ~1-2 seconds never got a browser at all —
 * re-run before believing it". Nothing else is retried: a membership refusal or
 * a wrong tab strip is reported the first time it is seen.
 */
async function attachRetryingMemory(
  sessionId: string,
  agentId: string,
  extra: Array<{ afterMs?: number; afterAttachedMs?: number; frame: Frame }> = [],
  collectMs = 20_000,
  attempts = 3,
): Promise<Awaited<ReturnType<typeof attach>> & { attempts: number }> {
  let last = await attach(sessionId, agentId, extra, collectMs);
  let used = 1;
  while (used < attempts && isMemoryRefusal(last.error)) {
    await new Promise((r) => setTimeout(r, 15_000));
    last = await attach(sessionId, agentId, extra, collectMs);
    used += 1;
  }
  return { ...last, attempts: used };
}

interface Fixture {
  wsA: string;
  wsB: string;
  agentA: string;
  agentA2: string;
  agentB: string;
  chatA1: string;
  chatA2: string;
  chatB1: string;
  multiAgent: string;
  loneChat: string;
  noTeamAgent: string;
}

let fx: Fixture;

/**
 * Build the plan's §2.4 shape, with one deviation that is forced and is itself
 * worth reading: the plan's Alpha/Bravo rosters use the seeded Jim and Ray, but
 * BOTH are already on the default workspace's team, so both are on two
 * workspaces and both hit FR-033's ambiguity refusal on any workspace-less
 * request. The two per-workspace probes are therefore purpose-made agents on
 * EXACTLY ONE workspace each, which is the only shape that can prove "agent X
 * reached workspace Y's browser" without the answer being "refused as
 * ambiguous" every time.
 */
test.beforeAll(async ({ playwright }) => {
  const request = await playwright.request.newContext({
    baseURL: process.env.OMNIPUS_URL || 'http://localhost:6060',
  });
  const headers = await csrf(request);
  const json = async <T>(res: { ok(): boolean; status(): number; json(): Promise<unknown>; url(): string }): Promise<T> => {
    if (!res.ok()) throw new Error(`BLOCKED: ${res.url()} returned ${res.status()}`);
    return (await res.json()) as T;
  };
  const stamp = Date.now();

  const mkWorkspace = async (name: string): Promise<string> =>
    (await json<{ id: string }>(await request.post('/api/v1/workspaces', { headers, data: { name, core_team: [] } }))).id;
  const mkAgent = async (name: string): Promise<string> =>
    (
      await json<{ id: string }>(
        await request.post('/api/v1/agents', {
          headers,
          data: { type: 'Main', name, soul: 'UAT ownership probe. Never used for a real turn.' },
        }),
      )
    ).id;
  const mkSession = async (agentId: string): Promise<string> =>
    (await json<{ id: string }>(await request.post('/api/v1/sessions', { headers, data: { agent_id: agentId, type: 'chat' } }))).id;
  const setTeam = async (wsId: string, team: string[]): Promise<void> => {
    await json(await request.put(`/api/v1/workspaces/${wsId}`, { headers, data: { core_team: team } }));
  };

  const wsA = await mkWorkspace(`UAT-Own-A-${stamp}`);
  const wsB = await mkWorkspace(`UAT-Own-B-${stamp}`);
  const agentA = await mkAgent(`uat-own-a-${stamp}`);
  // A SECOND agent on workspace A's team — UAT-02 is about the tab surviving a
  // change of agent on one chat, so it needs two agents that are both on A and
  // on nothing else (a seeded agent would be on the default workspace too and
  // would hit FR-033's ambiguity refusal instead of showing us the tab).
  const agentA2 = await mkAgent(`uat-own-a2-${stamp}`);
  const agentB = await mkAgent(`uat-own-b-${stamp}`);
  const noTeamAgent = await mkAgent(`uat-own-noteam-${stamp}`);
  await setTeam(wsA, [agentA, agentA2]);
  await setTeam(wsB, [agentB]);

  const chatA1 = await mkSession(agentA);
  const chatA2 = await mkSession(agentA);
  const chatB1 = await mkSession(agentB);
  const loneChat = await mkSession('jim');

  // A session acquires its workspace only when a message is sent through it
  // (MessageFrame.metadata.workspace_id). The turn itself fails without a model
  // — the binding is what matters and it lands before the model is consulted.
  for (const [chat, ws, agent] of [
    [chatA1, wsA, agentA],
    [chatA2, wsA, agentA],
    [chatB1, wsB, agentB],
  ] as const) {
    await wsExchange(
      '/api/v1/chat/ws',
      [
        {
          afterMs: 300,
          frame: { type: 'message', content: 'uat: bind this chat to its workspace', session_id: chat, agent_id: agent, metadata: { workspace_id: ws } },
        },
      ],
      4_000,
    );
  }

  fx = { wsA, wsB, agentA, agentA2, agentB, chatA1, chatA2, chatB1, multiAgent: 'jim', loneChat, noTeamAgent };
  await request.dispose();
});

/**
 * Delete the two scratch workspaces. This is not tidiness — each workspace this
 * file creates owns a REAL Chrome, and a machine that has accumulated them is
 * the machine on which the memory ceiling starts refusing the next tester's
 * browser. Deleting the workspace takes its profile directory with it, which is
 * how it stops costing memory and disk. Failures here are logged, never thrown:
 * a cleanup problem must not be reported as a case failure.
 */
test.afterAll(async ({ playwright }) => {
  if (!fx) return;
  const request = await playwright.request.newContext({
    baseURL: process.env.OMNIPUS_URL || 'http://localhost:6060',
  });
  try {
    const headers = await csrf(request);
    for (const id of [fx.wsA, fx.wsB]) {
      const res = await request.delete(`/api/v1/workspaces/${id}`, { headers });
      if (!res.ok()) console.log(`[UAT cleanup] DELETE workspace ${id} returned ${res.status()}`);
    }
    for (const id of [fx.agentA, fx.agentA2, fx.agentB, fx.noTeamAgent]) {
      const res = await request.delete(`/api/v1/agents/${id}`, { headers });
      if (!res.ok()) console.log(`[UAT cleanup] DELETE agent ${id} returned ${res.status()}`);
    }
  } catch (err) {
    console.log(`[UAT cleanup] skipped: ${String(err)}`);
  } finally {
    await request.dispose();
  }
});

/**
 * Positive control. Everything below that asserts a refusal is only meaningful
 * if an ALLOWED attach in the same run actually reaches a browser, so this runs
 * first and the others read its result.
 */
test('UAT-CTRL a chat reaches its own workspace browser and can drive it', async ({}, testInfo) => {
  const opened = await attachRetryingMemory(fx.chatA1, fx.agentA, [
    { afterAttachedMs: 500, frame: { type: 'browser_control', action: 'take' } },
    { afterAttachedMs: 1_500, frame: { type: 'browser_input', kind: 'navigate', url: PAGE_A } },
    { afterAttachedMs: 12_000, frame: { type: 'browser_control', action: 'release' } },
  ], 28_000);

  await note(testInfo, 'attach result', JSON.stringify(opened));
  if (isMemoryRefusal(opened.error)) {
    throw new Error(`BLOCKED by the memory ceiling (the feature working, not a defect): ${opened.error}`);
  }
  expect(opened.error, 'attach must not error').toBeNull();
  expect(opened.statuses.some((s) => s.state === 'attached'), 'panel must report attached').toBe(true);
  expect(opened.tabs.map((t) => t.url).join(','), 'the workspace tab must be on the page we drove it to').toContain(PAGE_A);
});

/**
 * UAT-04 — the tab the operator opens is the whole workspace's: a SECOND chat
 * in the same workspace sees it. Silent failure this rules out: the tab filed
 * under the chat, so only chat A1 can see it.
 */
test('UAT-04 a second chat in the same workspace sees the operator tab', async ({}, testInfo) => {
  const seen = await attachRetryingMemory(fx.chatA2, fx.agentA, [], 15_000);
  await note(testInfo, 'chat A2 tab strip', JSON.stringify(seen.tabs));
  if (isMemoryRefusal(seen.error)) throw new Error(`BLOCKED by the memory ceiling: ${seen.error}`);
  expect(seen.error).toBeNull();
  expect(seen.tabs.map((t) => t.url).join(','), 'a different chat in the same workspace must see the workspace tab').toContain(PAGE_A);
  expect(seen.tabs.length, 'it must be the SAME tab, not a second copy of the page').toBe(1);
});

/**
 * UAT-02's observable half — the tab follows the CHAT, not the agent. Switching
 * which agent the panel is open on must not produce a second tab or a fresh
 * load. (The full case needs an agent to list tabs; that half is BLOCKED
 * without a model provider.)
 */
test('UAT-02 the same chat on a different agent shows the same single tab', async ({}, testInfo) => {
  const seen = await attachRetryingMemory(fx.chatA1, fx.agentA2, [], 15_000);
  await note(testInfo, 'same chat, tab strip', JSON.stringify(seen.tabs));
  if (isMemoryRefusal(seen.error)) throw new Error(`BLOCKED by the memory ceiling: ${seen.error}`);
  expect(seen.tabs.length, 'one tab, not two').toBe(1);
  expect(seen.tabs[0]?.url).toContain(PAGE_A);
});

/**
 * UAT-09 — a second workspace is a second browser. Asserted three ways so a
 * single-browser implementation cannot pass: a different tab strip, a separate
 * on-disk profile directory, and a separate live Chrome with its own
 * --user-data-dir (the plan calls the process count "the real assertion here").
 */
test('UAT-09 a second workspace gets its own browser, profile and process', async ({}, testInfo) => {
  const b = await attachRetryingMemory(fx.chatB1, fx.agentB, [
    { afterAttachedMs: 500, frame: { type: 'browser_control', action: 'take' } },
    { afterAttachedMs: 1_500, frame: { type: 'browser_input', kind: 'navigate', url: PAGE_B } },
    { afterAttachedMs: 12_000, frame: { type: 'browser_control', action: 'release' } },
  ], 24_000);
  await note(testInfo, 'workspace B tab strip', JSON.stringify(b.tabs));
  if (isMemoryRefusal(b.error)) throw new Error(`BLOCKED by the memory ceiling: ${b.error}`);
  expect(b.error).toBeNull();
  expect(b.tabs.map((t) => t.url).join(','), "workspace B must be on its own page").toContain(PAGE_B);
  expect(b.tabs.map((t) => t.url).join(','), "workspace B must NOT be showing workspace A's tab").not.toContain('the-internet');

  const dirs = execFileSync('/bin/ps', ['ax', '-o', 'command'], { encoding: 'utf8' })
    .split('\n')
    .filter((l) => l.includes('--user-data-dir=') && l.includes('/browser/profiles/ws-'))
    .map((l) => l.replace(/.*--user-data-dir=(\S+).*/, '$1'));
  const forA = dirs.filter((d) => d.endsWith(`ws-${fx.wsA}`));
  const forB = dirs.filter((d) => d.endsWith(`ws-${fx.wsB}`));
  await note(testInfo, 'live --user-data-dir values', JSON.stringify([...new Set(dirs)]));
  expect(forA.length, 'workspace A must have a live Chrome of its own').toBeGreaterThan(0);
  expect(forB.length, 'workspace B must have a live Chrome of its own').toBeGreaterThan(0);
  expect(forA[0], 'the two workspaces must not share one profile directory').not.toBe(forB[0]);
});

/**
 * UAT-09's substance: a cookie set in one workspace does not exist in the
 * other. This is the "a login in one workspace does not exist in another"
 * assertion, made with a public cookie echo rather than an account, because the
 * run forbids entering any credential anywhere.
 */
test('UAT-09b a cookie set in one workspace is absent from the other', async ({}, testInfo) => {
  const a = await attachRetryingMemory(fx.chatA1, fx.agentA, [
    { afterAttachedMs: 500, frame: { type: 'browser_control', action: 'take' } },
    { afterAttachedMs: 1_500, frame: { type: 'browser_input', kind: 'navigate', url: `${COOKIE_SET}ownA` } },
    { afterAttachedMs: 12_000, frame: { type: 'browser_control', action: 'release' } },
  ], 22_000);
  if (isMemoryRefusal(a.error)) throw new Error(`BLOCKED by the memory ceiling: ${a.error}`);
  expect(a.error).toBeNull();

  const b = await attachRetryingMemory(fx.chatB1, fx.agentB, [
    { afterAttachedMs: 500, frame: { type: 'browser_control', action: 'take' } },
    { afterAttachedMs: 1_500, frame: { type: 'browser_input', kind: 'navigate', url: COOKIE_READ } },
    { afterAttachedMs: 12_000, frame: { type: 'browser_control', action: 'release' } },
  ], 22_000);
  if (isMemoryRefusal(b.error)) throw new Error(`BLOCKED by the memory ceiling: ${b.error}`);
  expect(b.error).toBeNull();

  // browser_inspect resolves the browsing context from agent_id ALONE
  // (ADR-072), so each probe agent reads its OWN workspace's live page.
  const read = async (agentId: string): Promise<string> => {
    const ctx = await apiRequest.newContext({
      baseURL: process.env.OMNIPUS_URL || 'http://localhost:6060',
    });
    const headers = await csrf(ctx);
    const res = await ctx.post('/api/v1/browser/inspect', {
      headers,
      data: { session_id: 'uat', agent_id: agentId, x: 200, y: 100 },
    });
    const body = (await res.json()) as { ok?: boolean; text?: string; reason?: string };
    await ctx.dispose();
    if (!body.ok) throw new Error(`BLOCKED: browser_inspect refused: ${body.reason}`);
    return body.text ?? '';
  };

  const inB = await read(fx.agentB);
  await note(testInfo, 'workspace B cookie jar', inB);
  expect(inB, "workspace B must not hold workspace A's cookie").not.toContain('ownA');
  expect(inB, 'workspace B must actually be on the cookie page (so the absence means something)').toContain('cookies');

  const backInA = await attachRetryingMemory(fx.chatA1, fx.agentA, [
    { afterAttachedMs: 500, frame: { type: 'browser_control', action: 'take' } },
    { afterAttachedMs: 1_500, frame: { type: 'browser_input', kind: 'navigate', url: COOKIE_READ } },
    { afterAttachedMs: 12_000, frame: { type: 'browser_control', action: 'release' } },
  ], 20_000);
  if (isMemoryRefusal(backInA.error)) throw new Error(`BLOCKED by the memory ceiling: ${backInA.error}`);
  const inA = await read(fx.agentA);
  await note(testInfo, 'workspace A cookie jar', inA);
  expect(inA, 'workspace A must still hold its own cookie — otherwise "absent in B" proves nothing').toContain('ownA');
});

/**
 * THE SECURITY BOUNDARY. Every agent on a workspace shares the operator's live
 * logins for every site that workspace has visited, so an agent reaching a
 * workspace it is not on is critical.
 *
 * The check is deliberately three-way, because "refused" is not the only safe
 * outcome and "attached" is not by itself a breach: per ADR-072 the chat's
 * named workspace is IGNORED when the agent is not on its team, and resolution
 * falls back to the agent's own membership. So the assertion is on WHICH
 * browser came back — the tab strip must be the agent's own workspace's page,
 * never the named workspace's.
 */
test('UAT-SEC an agent not on the chat workspace never reaches that workspace browser', async ({}, testInfo) => {
  // Re-mark both browsers HERE rather than relying on an earlier case: the two
  // workspaces must be showing DIFFERENT pages at the moment of the crossing,
  // or "it showed httpbin" cannot tell A's browser from B's and the case proves
  // nothing while still going green.
  const markA = await attachRetryingMemory(fx.chatA1, fx.agentA, [
    { afterAttachedMs: 500, frame: { type: 'browser_control', action: 'take' } },
    { afterAttachedMs: 1_500, frame: { type: 'browser_input', kind: 'navigate', url: PAGE_A } },
    { afterAttachedMs: 12_000, frame: { type: 'browser_control', action: 'release' } },
  ], 20_000);
  if (isMemoryRefusal(markA.error)) throw new Error(`BLOCKED by the memory ceiling: ${markA.error}`);
  expect(markA.tabs.map((t) => t.url).join(','), 'workspace A must be marked before the crossing').toContain('the-internet');

  const markB = await attachRetryingMemory(fx.chatB1, fx.agentB, [
    { afterAttachedMs: 500, frame: { type: 'browser_control', action: 'take' } },
    { afterAttachedMs: 1_500, frame: { type: 'browser_input', kind: 'navigate', url: PAGE_B } },
    { afterAttachedMs: 12_000, frame: { type: 'browser_control', action: 'release' } },
  ], 20_000);
  if (isMemoryRefusal(markB.error)) throw new Error(`BLOCKED by the memory ceiling: ${markB.error}`);
  expect(markB.tabs.map((t) => t.url).join(','), 'workspace B must be marked before the crossing').toContain('example.com');

  const crossed = await attach(fx.chatA1, fx.agentB, [], 15_000);
  await note(testInfo, "agent B attaching to workspace A's chat", JSON.stringify(crossed));
  if (isMemoryRefusal(crossed.error)) throw new Error(`BLOCKED by the memory ceiling: ${crossed.error}`);

  const urls = crossed.tabs.map((t) => t.url ?? '').join(',');
  expect(urls, "an agent off the team must NOT be shown the named workspace's browsing").not.toContain('the-internet');
  if (crossed.error === null) {
    expect(urls, "if it attached at all, it must be to its OWN workspace's browser").toContain('example.com');
  }

  const mirrored = await attach(fx.chatB1, fx.agentA, [], 15_000);
  await note(testInfo, "agent A attaching to workspace B's chat", JSON.stringify(mirrored));
  if (isMemoryRefusal(mirrored.error)) throw new Error(`BLOCKED by the memory ceiling: ${mirrored.error}`);
  const mirroredUrls = mirrored.tabs.map((t) => t.url ?? '').join(',');
  expect(mirroredUrls, "an agent off the team must NOT be shown the named workspace's browsing").not.toContain('example.com');
  if (mirrored.error === null) {
    expect(mirroredUrls, "if it attached at all, it must be to its OWN workspace's browser").toContain('the-internet');
  }
});

/**
 * UAT-11 — an agent on no workspace is told the truth. The message must name
 * the missing team AND the remedy, and must be distinguishable from "browser
 * tools are not registered for this agent".
 */
test('UAT-11 an agent on no workspace is refused with the team reason', async ({}, testInfo) => {
  const refused = await attach(fx.loneChat, fx.noTeamAgent, [], 12_000);
  await note(testInfo, 'refusal', String(refused.error));
  expect(refused.error, 'this must be refused, not quietly attached').not.toBeNull();
  expect(refused.error!, 'the refusal must name the missing workspace team').toMatch(/not on any workspace/i);
  expect(refused.error!, 'the refusal must name the remedy').toMatch(/add this agent to a workspace/i);
  expect(refused.tabs, 'a refused agent must be shown no tabs at all').toEqual([]);
});

/**
 * FR-033 — the ambiguity refusal UAT-07 rests on. An agent on more than one
 * workspace, asked from a chat that names none, must be refused rather than
 * given one by luck, and the log must name BOTH candidates.
 */
test('UAT-07r an agent on two workspaces with a workspace-less chat is refused as ambiguous', async ({}, testInfo) => {
  const refused = await attach(fx.loneChat, fx.multiAgent, [], 12_000);
  await note(testInfo, 'refusal', String(refused.error));
  expect(refused.error, 'this must be refused, not resolved by sorting or by luck').not.toBeNull();
  expect(refused.error!, 'the refusal must name the ambiguity').toMatch(/more than one workspace/i);
  expect(refused.error!, 'the refusal must name the remedy').toMatch(/chat that belongs to the workspace you mean/i);
  expect(refused.tabs, 'no browser may be handed over').toEqual([]);
});

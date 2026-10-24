import { expect, test, type Page, type TestInfo } from '@playwright/test';
import { Agent, Session, SessionDetail, Workspace } from '../../src/lib/api/generated/schemas';
import type { SessionCreateRequest, Workspace as WorkspaceRecord, WorkspaceCreateRequest } from '../../src/lib/api/generated/openapi-types';
import { GatewayProcess } from './fixtures/gateway-process';
import { startTerminalOutcomeProvider } from './fixtures/terminal-outcome-provider';
import { chatInput } from './fixtures/selectors';
import { openSessionByDeepLink } from './fixtures/session-setup';
import {
  HistoryHarness, anchor, fixtureId, geometry, historyRow,
  observeTraffic, settled,
} from './fixtures/chat-history-paging/harness';

// Oracle: founder Q79 / chief 2026-10-24. Same account, two REAL pages:
// opening B never changes A's in-app header strip, composer owner or rows.
// Incident: coordination/l1-uat/evidence/1266-bd037a35c/T/P08-recovered.png.
// The strip, NOT document.title, was wrong and survived focus/Return to latest.
//
// Label source: src/components/chat/FeedKindLabel.tsx::FeedKindLabel reads this
// page's sessionByWorkspace[activeWorkspaceId], gated by activeSessionId.
// src/store/session.ts::attachToSession persists that descriptor to shared
// localStorage; setActiveSession also writes it and clears attachedSessionType.
// src/components/chat/ChatScreen.tsx::OmnipusComposer separately derives its
// placeholder from activeAgentId and the agents query. No session-label
// BroadcastChannel/storage listener was found; the actual cross-tab trigger
// is UNVERIFIED. Do not inject a descriptor or force reconnect to invent it.
//
// REAL: two pages, same context/cookie, SPA, gateway, stores and history reads.
// Only the external provider is doubled for isolated onboarding; no chat turn
// is sent. Transcript seeding uses the existing canonical history writer.
// Cases: exact A baseline; B open while A is historical; A focus; A latest.
// Gaps: connection loss/reload and other chat kinds are separate regressions.
// GREEN/mutations deferred to CHECK: change A's descriptor kind/title, retarget
// its active session, or replace its composer/rows with B's; each must fail.

test.use({ storageState: { cookies: [], origins: [] }, viewport: { width: 1280, height: 900 } });
test.describe.configure({ retries: 0 });
test.setTimeout(240_000); // Private gateway setup and real history traversal, not a timing oracle.

const MAIN_LABEL = 'Jim - Main Session';
const MAIN_PREFIX = 'q79-jim';
const EXTRA_PREFIX = 'q79-mia';
const MAIN_GROUPS = 120; // More than the bounded opening page; leaves a genuine historical reader.

async function createSessions(gateway: GatewayProcess) {
  const roster = await gateway.apiFetch('GET', '/api/v1/agents');
  expect(roster.status, roster.raw).toBe(200);
  const jim = Agent.array().parse(roster.body).find((agent) => agent.id === 'jim');
  if (!jim) throw new Error('BLOCKED: seeded Jim agent missing — required by Q79');
  expect(jim.name, 'fixture uses the Q79 agent, not a name copied from the header').toBe('Jim');
  const created = await gateway.apiFetch('POST', '/api/v1/workspaces', {
    name: 'Q79 cross-tab header', core_team: ['jim', 'mia'],
  } satisfies WorkspaceCreateRequest);
  expect(created.status, created.raw).toBe(201);
  const workspace: WorkspaceRecord = Workspace.parse(created.body);
  const mainId = workspace.member_configs?.jim?.main_session_id;
  if (!mainId) throw new Error('BLOCKED: server-owned Jim main_session_id missing — required by Q79');
  const detail = await gateway.apiFetch('GET', `/api/v1/sessions/${mainId}`);
  expect(detail.status, detail.raw).toBe(200);
  const main = SessionDetail.parse(detail.body).session;
  expect({ type: main.type, owner: main.agent_id }).toEqual({ type: 'main', owner: 'jim' });
  const extra = await gateway.apiFetch('POST', '/api/v1/sessions', {
    type: 'chat', agent_id: 'mia', workspace_id: workspace.id,
  } satisfies SessionCreateRequest);
  expect(extra.status, extra.raw).toBe(201);
  return { agent: jim, session: main, otherSession: Session.parse(extra.body) };
}

async function assertJimView(page: Page, sessionId: string, info: TestInfo, stage: string) {
  await settled(page);
  const snapshot = await page.evaluate(() => ({
    label: document.querySelector('[data-testid="feed-kind-label"]')?.textContent?.trim() ?? null,
    session: document.querySelector('[data-active-session-id]')?.getAttribute('data-active-session-id') ?? null,
    composer: document.querySelector('[data-testid="chat-input"]')?.getAttribute('placeholder') ?? null,
    rows: [...document.querySelectorAll('[data-message-id]')].map((row) => ({
      id: row.getAttribute('data-message-id'), text: row.textContent,
    })),
  }));
  await info.attach(`${stage}-page-1`, { body: JSON.stringify(snapshot, null, 2), contentType: 'application/json' });
  await info.attach(`${stage}-page-1-screenshot`, { body: await page.screenshot(), contentType: 'image/png' });
  // Soft product checks keep the failure RED while still exercising focus and
  // Return to latest. No missing/wrong header can bypass the remaining checks.
  expect.soft(snapshot.label, `${stage}: opening B must not rename A's in-app header strip`).toBe(MAIN_LABEL);
  expect.soft(snapshot.session, `${stage}: page 1 remains bound to session A`).toBe(sessionId);
  expect.soft(snapshot.composer, `${stage}: header and composer must still identify Jim`).toBe('Message Jim…');
  expect.soft(snapshot.rows.length, `${stage}: actual Jim history rows are drawn`).toBeGreaterThan(0);
  expect.soft(snapshot.rows.map((row) => row.id).every((id) => id?.startsWith(`${MAIN_PREFIX}-`)),
    `${stage}: no extra-chat row may replace A's transcript`).toBe(true);
  expect.soft(snapshot.rows.every((row) => row.id !== null && row.text?.includes(row.id)),
    `${stage}: the drawn contents agree with the canonically seeded Jim row identities`).toBe(true);
}

test('Q79: opening Extra chat in a second same-account tab preserves the first tab header, Jim composer and Jim rows', async ({ page, context }, info) => {
  const provider = await startTerminalOutcomeProvider();
  let gateway: GatewayProcess | undefined;
  let second: Page | undefined;
  try {
    gateway = await GatewayProcess.start({ apiBase: provider.url, adminUsername: 'q79-reader' });
    const ids = await createSessions(gateway);
    const history = new HistoryHarness(page, gateway, ids, provider, info, info.outputPath('q79-gateway.jsonl'));
    await history.seed({ label: MAIN_PREFIX, count: MAIN_GROUPS });
    await history.seed({ label: EXTRA_PREFIX, count: 4 }, ids.otherSession.id);
    await history.authenticate(); // One login cookie reused by BOTH pages; never log in again.
    await history.open();
    await expect(page.getByTestId('feed-kind-label'), 'positive detector control: A starts with its correct strip').toHaveText(MAIN_LABEL);
    await expect(chatInput(page)).toHaveAttribute('placeholder', 'Message Jim…');
    // Use the shipped reader action, not a scrollTop assignment that may
    // still be treated as the live tail. It explicitly enters historical mode.
    const pagesBefore = history.traffic.pages.length;
    await page.getByRole('button', { name: 'Show earlier messages', exact: true }).click();
    await expect.poll(() => history.traffic.pages.length).toBeGreaterThan(pagesBefore);
    const latest = page.getByRole('button', { name: 'Return to latest', exact: true });
    await expect(latest, 'page 1 is actually historical before page 2 opens').toBeVisible();
    const old = await anchor(page);
    await expect(historyRow(page, old.id)).toBeVisible();
    await assertJimView(page, ids.session.id, info, 'before-second-tab');

    second = await context.newPage(); // Same BrowserContext means the same real account/session cookie.
    expect(second.context()).toBe(page.context());
    expect(context.pages()).toHaveLength(2);
    const otherTraffic = observeTraffic(second);
    await openSessionByDeepLink(second, ids.otherSession.id, gateway.baseURL);
    await expect.poll(() => otherTraffic.frames.filter((frame) =>
      frame.type === 'catch_up_complete' && frame.session_id === ids.otherSession.id).length).toBe(1);
    await expect(second.getByTestId('feed-kind-label'), 'B really renders the distinct extra-chat strip').toHaveText('Extra chat');
    await expect(chatInput(second)).toHaveAttribute('placeholder', 'Message Mia…');
    await expect(historyRow(second, fixtureId(EXTRA_PREFIX, 3))).toBeVisible();
    await expect(page.getByTestId('feed-kind-label'), 'instrument can see B without confusing it with A').toHaveCount(1);
    await assertJimView(page, ids.session.id, info, 'after-second-tab');
    await expect(historyRow(page, old.id), 'opening B must not swap A away from its historical rows').toBeVisible();

    await page.bringToFront();
    await assertJimView(page, ids.session.id, info, 'after-focus');
    await latest.click();
    await expect(historyRow(page, fixtureId(MAIN_PREFIX, MAIN_GROUPS - 1))).toBeVisible();
    await expect.poll(async () => (await geometry(page)).bottom).toBeLessThanOrEqual(1);
    await assertJimView(page, ids.session.id, info, 'after-return-to-latest');
    expect(history.traffic.failures).toEqual([]);
    expect(otherTraffic.failures).toEqual([]);
    expect(history.traffic.sends, 'navigation must not send user messages').toEqual([]);
    expect(otherTraffic.sends, 'opening B must not send user messages').toEqual([]);
    expect(provider.calls(), 'this navigation test makes no model turns').toBe(0);
    expect(provider.failures()).toEqual([]);
  } finally {
    provider.finishToolRound();
    try { if (second) await second.close(); }
    finally {
      try { if (gateway) { await gateway.kill9(); await gateway.stop(); } }
      finally { await provider.close(); }
    }
  }
});

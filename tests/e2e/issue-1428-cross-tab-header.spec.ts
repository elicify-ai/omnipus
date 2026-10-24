import fs from 'node:fs';
import path from 'node:path';
import { createHash } from 'node:crypto';
import { expect, test, type Page, type TestInfo } from '@playwright/test';
import { Agent, Session, SessionDetail, Workspace } from '../../src/lib/api/generated/schemas';
import type { SessionCreateRequest, SessionRenameRequest, Workspace as WorkspaceRecord, WorkspaceCreateRequest } from '../../src/lib/api/generated/openapi-types';
import { GatewayProcess } from './fixtures/gateway-process';
import { startTerminalOutcomeProvider } from './fixtures/terminal-outcome-provider';
import { chatInput, openSidebar } from './fixtures/selectors';
import { removeSessionHistoryIndex } from './fixtures/chat-history';
import { openSessionByDeepLink } from './fixtures/session-setup';
import {
  HistoryHarness, UNAVAILABLE, anchor, fixtureId, geometry, historyRow, historyScroller,
  holdPage, observeTraffic, settled,
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
// Recovered P08 trigger (chief 2026-10-24; seed-t.receipt / P08-measurements):
// B already exists, is Mia-owned and UNFILED, has title below and a DELETED
// derived history index. Sessions-list opening is the reported likely path.
// A is Jim main, reading older history during offline older-page/Retry recovery.
// REAL: two pages, same context/cookie, SPA, gateway, stores and history reads.
// Only the external onboarding provider and network availability/delivery are
// controlled. The history writer is canonical; missing-index setup never edits
// transcript bytes. No synthetic header/store/frame or model turn is injected.
// Cases: exact A baseline; B open during A's held real Retry; A focus; A latest.
// Gaps: the unobserved original scheduling is not claimed byte-for-byte replay;
// the held real response makes the supplied in-progress Retry state observable.
// GREEN/mutations deferred to CHECK: change A's descriptor kind/title, retarget
// its active session, or replace its composer/rows with B's; each must fail.

test.use({ storageState: { cookies: [], origins: [] }, viewport: { width: 1280, height: 900 } });
test.describe.configure({ retries: 0 });
test.setTimeout(240_000); // Private gateway setup and real history traversal, not a timing oracle.

const MAIN_LABEL = 'Jim - Main Session';
const MAIN_PREFIX = 'q79-jim';
const EXTRA_PREFIX = 'q79-mia';
const MAIN_GROUPS = 300; // Recovered seed-t.receipt: 300 groups in Jim main.
const EXTRA_TITLE = 'Reply with exactly: p5-ok'; // Recovered P08 status e1852.
const EXTRA_LABEL = `Extra chat — ${EXTRA_TITLE}`;

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
    type: 'chat', agent_id: 'mia', // Deliberately OMIT workspace_id: P08 B is unfiled.
  } satisfies SessionCreateRequest);
  expect(extra.status, extra.raw).toBe(201);
  const extraId = Session.parse(extra.body).id;
  const renamed = await gateway.apiFetch('PUT', `/api/v1/sessions/${extraId}`, {
    title: EXTRA_TITLE,
  } satisfies SessionRenameRequest);
  expect(renamed.status, renamed.raw).toBe(200);
  const otherSession = Session.parse(renamed.body);
  expect(otherSession.workspace_id ?? null, 'P08 fixture is unfiled, never a workspace chat').toBeNull();
  expect(otherSession.agent_id).toBe('mia');
  expect(otherSession.title).toBe(EXTRA_TITLE);
  return { agent: jim, session: main, otherSession };
}

async function seedP08(history: HistoryHarness) {
  await history.seed({ label: MAIN_PREFIX, count: MAIN_GROUPS });
  await history.seed({ label: EXTRA_PREFIX, count: 6 }, history.ids.otherSession.id); // seed-t.receipt: six B groups.
  await history.gateway.kill9();
  history.gateway.assertStoppedForFixture();
  const transcript = path.join(history.gateway.homeDir, 'sessions', history.ids.otherSession.id, 'transcript.jsonl');
  const before = createHash('sha256').update(fs.readFileSync(transcript)).digest('hex');
  await removeSessionHistoryIndex(history.gateway, history.ids.otherSession.id);
  expect(createHash('sha256').update(fs.readFileSync(transcript)).digest('hex'), 'index deletion preserves canonical B bytes').toBe(before);
  await history.gateway.restart();
  await history.authenticate();
  const response = await history.page.request.get(`${history.gateway.baseURL}/api/v1/sessions/${history.ids.otherSession.id}`);
  expect(response.status()).toBe(200);
  const detail = SessionDetail.parse(await response.json());
  expect(detail.session.title).toBe(EXTRA_TITLE);
  expect(detail.session.workspace_id ?? null).toBeNull();
  expect(detail.history_state).toEqual({ status: 'unavailable', reason: 'index_missing', message: UNAVAILABLE });
}

async function assertJimView(page: Page, sessionId: string, info: TestInfo, stage: string) {
  await settled(page);
  const snapshot = await page.evaluate(() => ({
    label: document.querySelector('[data-testid="feed-kind-label"]')?.textContent?.trim() ?? null,
    session: document.querySelector('[data-active-session-id]')?.getAttribute('data-active-session-id') ?? null,
    composer: document.querySelector('[data-testid="chat-input"]')?.getAttribute('placeholder') ?? null,
    // Observe the shared remembered descriptor; do not force A/B's in-memory store.
    persistedWorkspaceSessions: localStorage.getItem('omnipus.sessionByWorkspace.v1'),
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

async function existingUnfiledInSessions(second: Page, history: HistoryHarness) {
  // Bootstrap the normal app in the same workspace, then use the real list.
  // B already exists before either browser page opens; no New chat control.
  await openSessionByDeepLink(second, history.sessionId, history.gateway.baseURL);
  await openSidebar(second);
  await second.getByRole('button', { name: 'Search sessions', exact: true }).click();
  const dialog = second.getByRole('dialog', { name: 'Sessions', exact: true });
  await expect(dialog).toBeVisible();
  await dialog.getByRole('textbox', { name: 'Search sessions', exact: true }).fill(EXTRA_TITLE);
  await expect(dialog.locator('#search-ws-unfiled'), 'existing B is in the actual Unfiled group').toBeVisible();
  const open = dialog.locator(`[data-session-id="${history.ids.otherSession.id}"]`)
    .getByRole('button', { name: `Open ${EXTRA_TITLE}`, exact: true });
  await expect(open).toBeVisible();
  return open;
}

async function beginOfflineRetry(history: HistoryHarness) {
  const page = history.page;
  const failed: string[] = [];
  const observeFailure = (request: import('@playwright/test').Request) => {
    const url = new URL(request.url());
    if (decodeURIComponent(url.pathname) === `/api/v1/sessions/${history.sessionId}/messages` && url.searchParams.has('before')) failed.push(url.href);
  };
  page.on('requestfailed', observeFailure);
  let held: Awaited<ReturnType<typeof holdPage>> | undefined;
  try {
    await page.bringToFront();
    await page.context().setOffline(true);
    await historyScroller(page).hover();
    await page.mouse.wheel(0, -4 * (await geometry(page)).H);
    const error = page.getByRole('alert').filter({ hasText: 'Could not load earlier messages.' });
    await expect(error).toBeVisible();
    const retry = error.getByRole('button', { name: 'Retry', exact: true });
    await retry.click();
    await expect.poll(() => failed.length, { message: 'real older read AND explicit Retry failed offline' }).toBeGreaterThanOrEqual(2);
    await expect(error).toContainText('Could not load earlier messages.');
    await history.info.attach('p08-offline-read-and-retry', { body: JSON.stringify({ failed, error: await error.textContent() }), contentType: 'application/json' });
    held = await holdPage(page, (url) => decodeURIComponent(url.pathname) === `/api/v1/sessions/${history.sessionId}/messages` && url.searchParams.has('before'));
    const before = history.traffic.frames.length;
    await page.context().setOffline(false);
    await expect.poll(() => history.traffic.frames.slice(before).filter((frame) =>
      frame.type === 'catch_up_complete' && frame.session_id === history.sessionId).length,
    { message: 'real online recovery finishes A reattach without reload' }).toBe(1);
    await retry.click();
    await held.requested;
    await expect(page.getByText('Loading earlier messages…', { exact: true }), 'A Retry is in progress when B opens').toBeVisible();
    return held;
  } catch (error) {
    if (held) { held.release(); await page.unrouteAll({ behavior: 'wait' }); await held.dispose(); }
    throw error;
  } finally {
    page.removeListener('requestfailed', observeFailure);
    await page.context().setOffline(false);
  }
}

test('Q79: opening Extra chat in a second same-account tab preserves the first tab header, Jim composer and Jim rows', async ({ page, context }, info) => {
  const provider = await startTerminalOutcomeProvider();
  let gateway: GatewayProcess | undefined;
  let second: Page | undefined;
  let pendingRetry: Awaited<ReturnType<typeof holdPage>> | undefined;
  try {
    gateway = await GatewayProcess.start({ apiBase: provider.url, adminUsername: 'q79-reader' });
    const ids = await createSessions(gateway);
    const history = new HistoryHarness(page, gateway, ids, provider, info, info.outputPath('q79-gateway.jsonl'));
    await seedP08(history); // One login cookie reused by BOTH pages; never log in again.
    await history.open();
    await expect(page.getByTestId('feed-kind-label'), 'positive detector control: A starts with its correct strip').toHaveText(MAIN_LABEL);
    await expect(chatInput(page)).toHaveAttribute('placeholder', 'Message Jim…');
    // A real upward wheel enters reading mode and reaches the older-page
    // trigger even when the candidate virtualizes its boundary button away.
    const pagesBefore = history.traffic.pages.length;
    await historyScroller(page).hover();
    await page.mouse.wheel(0, -4 * (await geometry(page)).H); // Traverse the four-screen opening window.
    await expect.poll(() => history.traffic.pages.length).toBeGreaterThan(pagesBefore);
    const latest = page.getByRole('button', { name: 'Return to latest', exact: true });
    await expect(latest, 'page 1 is actually historical before page 2 opens').toBeVisible();
    await assertJimView(page, ids.session.id, info, 'before-second-tab');

    second = await context.newPage(); // Same BrowserContext means the same real account/session cookie.
    expect(second.context()).toBe(page.context());
    expect(context.pages()).toHaveLength(2);
    const otherTraffic = observeTraffic(second);
    const openUnfiled = await existingUnfiledInSessions(second, history);
    pendingRetry = await beginOfflineRetry(history);
    // The new scenario deliberately scrolls again while offline. Protect the
    // actual row immediately BEFORE B opens, not a row from before that scroll.
    const old = await anchor(page);
    await expect(historyRow(page, old.id)).toBeVisible();
    await assertJimView(page, ids.session.id, info, 'before-unfiled-open-during-retry');
    await second.bringToFront();
    await openUnfiled.click();
    await expect(second.locator(`[data-active-session-id="${ids.otherSession.id}"]`)).toBeVisible();
    await expect.poll(() => otherTraffic.frames.filter((frame) =>
      frame.type === 'catch_up_complete' && frame.session_id === ids.otherSession.id).length).toBe(1);
    // Opening B overlapped A's real held read; release before any later
    // assertion can trigger cleanup, so its handler finishes on a live page.
    pendingRetry.release();
    await page.unrouteAll({ behavior: 'wait' });
    await info.attach('unfiled-page-2-screenshot', { body: await second.screenshot(), contentType: 'image/png' });
    await expect(second.getByTestId('feed-kind-label'), 'B renders its recovered unfiled title').toHaveText(EXTRA_LABEL);
    await expect(chatInput(second)).toHaveAttribute('placeholder', 'Message Mia…');
    // The brief requires the exact visible error, not an unspecified ARIA role.
    await expect(second.getByText(UNAVAILABLE, { exact: true }), 'B really has the missing-index error').toBeVisible();
    await expect(second.locator('[data-message-id]'), 'unavailable history must not pretend B rows loaded').toHaveCount(0);
    await expect(page.getByTestId('feed-kind-label'), 'instrument can see B without confusing it with A').toHaveCount(1);
    await assertJimView(page, ids.session.id, info, 'after-second-tab');
    await expect(historyRow(page, old.id), 'opening B must not swap A away from its historical rows').toBeVisible();
    pendingRetry.release();
    await expect(page.getByText('Loading earlier messages…', { exact: true })).toHaveCount(0);
    await expect(page.getByRole('alert').filter({ hasText: 'Could not load earlier messages.' })).toHaveCount(0);

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
    try {
      if (pendingRetry) { pendingRetry.release(); await page.unrouteAll({ behavior: 'wait' }); await pendingRetry.dispose(); }
      await context.setOffline(false);
    } finally {
      try { if (second) await second.close(); }
      finally {
        try { if (gateway) { await gateway.kill9(); await gateway.stop(); } }
        finally { await provider.close(); }
      }
    }
  }
});

// #1537 order 7. Approved spec BDD/FR oracle. Only the model HTTP edge is
// scripted; real delegate, turn engine, tools, hub, schemas, store and SPA.
import { setTimeout as delay } from 'node:timers/promises';
import { expect } from '@playwright/test';
import { test } from './fixtures/console-errors';
import { chatInput, waitForConnected } from './fixtures/selectors';
import { openSessionByDeepLink } from './fixtures/session-setup';
import { createNoticeScene, expectNamed, visibleNamed, recordScene, type NoticeScene } from './fixtures/helper-notices-harness';
import type { HelperScript } from './fixtures/helper-notices-provider';
import { SessionDetail } from '../../src/lib/api/generated/schemas';

test.use({ storageState: { cookies: [], origins: [] } });
test.describe.configure({ retries: 0 });
test.setTimeout(150_000);

async function afterRealWait(scene: NoticeScene, seconds: number) {
  const initial = scene.traffic.frames.find(f => f.type === 'provider_retry' && f.session_id === scene.root.id);
  expect(initial?.sent_at, 'positive timing instrument: real server retry frame').toEqual(expect.any(String));
  await delay(Math.max(0, Date.parse(String(initial!.sent_at)) + seconds * 1000 - Date.now()));
}
async function workingTool(scene: NoticeScene, index = 0) {
  await expect.poll(() => scene.ownTraffic.frames.filter(f => f.type === 'tool_call_start' &&
    (f.call_id === `hm-hold-${index}` || f.tool_call_id === `hm-hold-${index}`)).length,
  { message: 'positive control: real 30 s tool started', timeout: 15_000 }).toBeGreaterThan(0);
}
async function ended(scene: NoticeScene, index = 0) {
  const result = await scene.gateway.apiFetch('GET', `/api/v1/sessions/${scene.children[index].id}`);
  expect(result.status).toBe(200);
  return SessionDetail.parse(result.body).session.lifecycle_state;
}

for (const writing of [false, true]) {
  test(`429 then success: helper before first byte, named main row while writing=${writing}, recovered then ended`, async ({ page }, info) => {
    const scene = await createNoticeScene(page, info, { script: 'retry', wait: 10, parentWriting: writing });
    try {
      await scene.releaseFailures();
      await expectNamed(scene);
      await expect(scene.own.getByTestId('provider-retry-indicator'), 'FR-001/004: helper own retry before any answer').toBeVisible();
      await expect(scene.own.getByTestId('provider-retry-indicator')).not.toContainText('Researcher:');
      expect(scene.ownTraffic.frames.filter(f => f.type === 'token' && f.session_id === scene.children[0].id)).toEqual([]);
      await recordScene(scene, info, '429-before-byte');
      scene.provider.answer[0].resolve();
      await expectNamed(scene, 'Researcher', 'after 1 retry');
      await expect(scene.own.getByText('Connection came back — the model answered after 1 retry.', { exact: true })).toBeVisible();
      await workingTool(scene);
      expect(await ended(scene)).toBe('working');
      scene.provider.finish[0].resolve();
      await expect.poll(() => ended(scene), { timeout: 40_000 }).toBe('done');
      await expect.poll(() => visibleNamed(page, 'Researcher', 'retry')).toEqual([]);
      const tombstones = scene.traffic.frames.filter(f => f.type === 'provider_status' && f.state === 'ended' && f.child_session_id === scene.children[0].id);
      expect(tombstones).toHaveLength(1);
      expect(scene.provider.evidence.errors).toEqual([]);
      scene.provider.parentFinish.resolve();
      await expect.poll(() => scene.provider.evidence.parentDone).toBe(true);
      await page.reload(); await waitForConnected(page);
      await scene.own.reload(); await waitForConnected(scene.own);
      await expect(page.getByText('HM_ROOT_OK', { exact: false })).toBeVisible();
      await expect(scene.own.getByText('HM_CHILD_0_OK', { exact: true })).toBeVisible();
      await expect(page.getByTestId('provider-retry-indicator')).toHaveCount(0);
      await expect(scene.own.getByTestId('provider-waiting-indicator')).toHaveCount(0);
    } finally { await scene.close(); }
  });
}

test('minimum Retry-After 1 is announced and recovers without an invented extra retry', async ({ page }, info) => {
  const scene = await createNoticeScene(page, info, { script: 'retry', wait: 1 });
  try {
    await scene.releaseFailures(); await expectNamed(scene);
    const namedRetry = scene.traffic.frames.find(f => f.type === 'provider_retry' && f.child_session_id === scene.children[0].id);
    expect(namedRetry?.retry_after_seconds).toBe(1);
    scene.provider.answer[0].resolve();
    await expectNamed(scene, 'Researcher', 'after 1 retry');
  } finally { await scene.close(); }
});

for (const script of ['empty', 'timeout'] as const) {
  test(`${script}: both retry sentences and responding clear while real 30 s tool still runs, including reload`, async ({ page }, info) => {
    const scene = await createNoticeScene(page, info, { script });
    try {
      await scene.releaseFailures();
      const clause = script === 'empty' ? 'sent an empty reply' : 'did not start answering in time';
      await expectNamed(scene, 'Researcher', clause);
      await expect(scene.own.getByTestId('provider-retry-indicator')).toContainText(clause);
      scene.provider.answer[0].resolve(); await workingTool(scene);
      await expect.poll(() => visibleNamed(page, 'Researcher', 'Retrying')).toEqual([]);
      expect(await ended(scene)).toBe('working');
      await page.reload(); await waitForConnected(page);
      await expect.poll(() => visibleNamed(page, 'Researcher', 'Retrying')).toEqual([]);
      expect(scene.traffic.frames.filter(f => f.type === 'provider_status' && f.child_session_id === scene.children[0].id && f.state === 'recovered')).toEqual([]);
    } finally { await scene.close(); }
  });
}

test('fallback first-call tool answer: primary 429 x3 + fallback x1 clears named row, no top fallback note', async ({ page }, info) => {
  const scene = await createNoticeScene(page, info, { script: 'fallback', wait: 2 });
  try {
    await scene.releaseFailures(); await expectNamed(scene);
    scene.provider.answer[0].resolve();
    await expect.poll(() => scene.provider.evidence.requests.filter(r => r.role === 'helper-0' && r.model === 'hm-fallback').length, { timeout: 15_000 }).toBeGreaterThanOrEqual(1);
    const requests = scene.provider.evidence.requests.filter(r => r.role === 'helper-0').slice(0, 4);
    expect(requests.map(r => r.model)).toEqual([requests[0].model, requests[0].model, requests[0].model, 'hm-fallback']);
    expect(requests.slice(0, 3).map(r => r.status)).toEqual([429, 429, 429]);
    await workingTool(scene);
    await expect.poll(() => visibleNamed(page, 'Researcher', 'Retrying')).toEqual([]);
    expect(await ended(scene)).toBe('working');
    await expect(page.getByText(/Answered by the Fallback model/)).toHaveCount(0);
    await page.reload(); await waitForConnected(page);
    await expect.poll(() => visibleNamed(page, 'Researcher', 'Retrying')).toEqual([]);
    scene.provider.finish[0].resolve();
    await expect.poll(() => ended(scene), { timeout: 40_000 }).toBe('done');
    await scene.own.reload(); await waitForConnected(scene.own);
    await expect(scene.own.getByText(/Answered by the Fallback model/)).toBeVisible();
    await expect(scene.own.getByText('HM_CHILD_0_OK', { exact: true })).toBeVisible();
  } finally { await scene.close(); }
});

for (const seconds of [29, 35]) {
  test(`silence ${seconds}s: real threshold, both chats, responding clear`, async ({ page }, info) => {
    const scene = await createNoticeScene(page, info, { script: 'silence' });
    try {
      await scene.releaseFailures();
      const start = scene.provider.helpers[0].firstAt;
      if (seconds === 29) {
        await delay(Math.max(0, start + 29_000 - Date.now()));
        expect(scene.traffic.frames.filter(f => f.type === 'provider_status' && f.state === 'waiting')).toEqual([]);
        await expect(scene.own.getByTestId('provider-waiting-indicator')).toHaveCount(0);
      } else {
        await expect.poll(() => scene.traffic.frames.filter(f => f.type === 'provider_status' && f.state === 'waiting').length,
          { message: 'real 30 second silence watcher must fire', timeout: 35_000 }).toBeGreaterThan(0);
        await expectNamed(scene, 'Researcher', "hasn't responded");
        await expect(scene.own.getByTestId('provider-waiting-indicator')).toBeVisible();
        await delay(Math.max(0, start + 35_000 - Date.now()));
      }
      scene.provider.answer[0].resolve(); await workingTool(scene);
      await expect.poll(() => visibleNamed(page, 'Researcher', "hasn't responded")).toEqual([]);
      await expect(scene.own.getByTestId('provider-waiting-indicator')).toHaveCount(0);
    } finally { await scene.close(); }
  });
}

test('silence then 429: clear-before-retry live and after reload in both chats', async ({ page }, info) => {
  const scene = await createNoticeScene(page, info, { script: 'silence-retry', wait: 20 });
  try {
    await scene.releaseFailures();
    await expect.poll(() => scene.link.frames.some(f => f.type === 'provider_status' && f.state === 'waiting'),
      { message: 'positive control: silence watcher reached its real 30 s threshold', timeout: 35_000 }).toBe(true);
    await expectNamed(scene, 'Researcher', "hasn't responded");
    scene.provider.answer[0].resolve();
    await expectNamed(scene);
    for (const [sid, frames] of [[scene.root.id, scene.traffic.frames], [scene.children[0].id, scene.ownTraffic.frames]] as const) {
      const relevant = frames.filter(f => f.session_id === sid && (f.type === 'provider_status' || f.type === 'provider_retry'));
      expect(relevant.slice(0, 3).map(f => f.type === 'provider_retry' ? 'retry' : f.state)).toEqual(['waiting', 'responding', 'retry']);
    }
    await afterRealWait(scene, 5);
    await page.reload(); await waitForConnected(page);
    await scene.own.reload(); await waitForConnected(scene.own);
    await expect.poll(() => visibleNamed(page, 'Researcher', '0:1[4-6]'), { timeout: 2_000 }).toHaveLength(1);
    await expect(scene.own.getByTestId('provider-retry-indicator')).toContainText(/0:1[4-6]/, { timeout: 2_000 });
    await afterRealWait(scene, 21); await workingTool(scene);
    await expectNamed(scene, 'Researcher', 'after 1 retry');
    await page.reload(); await waitForConnected(page);
    await expectNamed(scene, 'Researcher', 'after 1 retry');
    await expect.poll(() => visibleNamed(page, 'Researcher', 'Retrying')).toEqual([]);
  } finally { await scene.close(); }
});

test('reload at s5 after root done at s3 retains 0:24..0:26 in top and helper', async ({ page }, info) => {
  const scene = await createNoticeScene(page, info, { script: 'retry', wait: 30, parentWriting: true });
  try {
    await scene.releaseFailures(); await expectNamed(scene);
    await afterRealWait(scene, 3); scene.provider.parentFinish.resolve();
    await expect.poll(() => scene.provider.evidence.parentDone).toBe(true);
    await afterRealWait(scene, 5);
    await Promise.all([page.reload(), scene.own.reload()]);
    await Promise.all([waitForConnected(page), waitForConnected(scene.own)]);
    await expect.poll(() => visibleNamed(page, 'Researcher', '0:2[4-6]'), { timeout: 2_000 }).toHaveLength(1);
    await expect(scene.own.getByTestId('provider-retry-indicator')).toContainText(/0:2[4-6]/, { timeout: 2_000 });
    await recordScene(scene, info, 'reattach-five-seconds');
  } finally { await scene.close(); }
});

test('same turn retry → recovery → real tool → retry: only current row replays', async ({ page }, info) => {
  const scene = await createNoticeScene(page, info, { script: 'repeat', wait: 10 });
  try {
    await scene.releaseFailures(); await expectNamed(scene);
    scene.provider.answer[0].resolve(); await expectNamed(scene, 'Researcher', 'after 1 retry');
    await page.reload(); await waitForConnected(page);
    await expectNamed(scene, 'Researcher', 'after 1 retry');
    await expect.poll(() => visibleNamed(page, 'Researcher', 'Retrying automatically')).toEqual([]);
    await expect.poll(() => scene.provider.evidence.requests.filter(r => r.role === 'helper-0' && r.status === 429).length, { timeout: 40_000 }).toBe(2);
    await expectNamed(scene);
    const retries = scene.traffic.frames.filter(f => f.type === 'provider_retry' && f.child_session_id === scene.children[0].id);
    expect(new Set(retries.map(f => f.turn_id)).size).toBe(1);
    await page.reload(); await waitForConnected(page); await expectNamed(scene);
    await expect.poll(() => visibleNamed(page, 'Researcher', 'after 1 retry')).toEqual([]);
  } finally { await scene.close(); }
});

for (const finish of ['failed', 'stopped'] as const) {
  test(`named row leaves when helper ${finish}`, async ({ page }, info) => {
    const script: HelperScript = finish === 'failed' ? 'failure' : 'retry';
    const scene = await createNoticeScene(page, info, { script, wait: finish === 'failed' ? 1 : 60 });
    try {
      await scene.releaseFailures(); await expectNamed(scene);
      if (finish === 'stopped') {
        await afterRealWait(scene, 5);
        await scene.own.getByTestId('stop-btn').click();
      }
      await expect.poll(() => ended(scene), { timeout: 95_000 }).toBe(finish);
      await expect.poll(() => visibleNamed(page, 'Researcher', 'Retrying')).toEqual([]);
      const clears = scene.traffic.frames.filter(f => f.type === 'provider_status' && f.state === 'ended' && f.child_session_id === scene.children[0].id);
      expect(clears).toHaveLength(1);
    } finally { await scene.close(); }
  });
}

for (const duplicateNames of [false, true]) {
  test(`two helpers, one ends; independent rows and own countdown; duplicate names=${duplicateNames}`, async ({ page }, info) => {
    const scene = await createNoticeScene(page, info, { script: 'retry', wait: 5, count: 2, duplicateNames, parentRetry: true });
    try {
      await scene.releaseFailures();
      if (duplicateNames) await expect.poll(() => visibleNamed(page, 'Researcher', 'Retrying')).toHaveLength(2);
      else { await expectNamed(scene); await expectNamed(scene, 'Writer'); }
      const ownBefore = scene.traffic.frames.find(f => f.type === 'provider_retry' && !f.child_session_id && f.session_id === scene.root.id);
      expect(ownBefore?.retry_after_seconds).toBe(20);
      await afterRealWait(scene, 5);
      scene.provider.answer[0].resolve();
      await expectNamed(scene, 'Researcher', 'after 1 retry');
      await expect(page.getByTestId('provider-retry-indicator').filter({ hasText: 'attempt' }).first()).toBeVisible();
      expect(scene.traffic.frames.filter(f => f.type === 'provider_retry' && !f.child_session_id && f.session_id === scene.root.id), 'helper recovery cannot restart the root retry time').toEqual([ownBefore]);
      scene.provider.finish[0].resolve();
      await expect.poll(() => ended(scene), { timeout: 40_000 }).toBe('done');
      await expect.poll(() => visibleNamed(page, duplicateNames ? 'Researcher' : 'Writer', 'Retrying')).toHaveLength(1);
    } finally { await scene.close(); }
  });
}

test('four helpers: first-three order, truthful summary and promotion; 320px composer and Stop remain usable', async ({ page }, info) => {
  // Spec long-name edge; 100 is the public Agent display-name maximum.
  const longName = 'Researcher with a deliberately long display name '.repeat(3).slice(0, 100);
  const scene = await createNoticeScene(page, info, { script: 'retry', wait: 60, count: 4, parentWriting: true, helperNames: [longName] });
  try {
    await scene.releaseFailures(); await expectNamed(scene, longName);
    await expectNamed(scene, 'Writer'); await expectNamed(scene, 'Helper 2');
    await expect.poll(() => visibleNamed(page, 'Helper 3', 'Retrying')).toEqual([]);
    await expect(page.getByText('1 more helper has a model notice', { exact: true })).toBeVisible();
    await page.setViewportSize({ width: 320, height: 800 });
    const lineCount = await page.locator('span, p, div').filter({ hasText: new RegExp(`^${longName}: .*Retrying automatically`) }).evaluateAll(nodes => {
      const visible = nodes.filter(n => !(n as HTMLElement).closest('[aria-live], .sr-only, [aria-hidden="true"]') && (n as HTMLElement).checkVisibility());
      const row = visible.find(n => !visible.some(other => other !== n && n.contains(other)));
      if (!row) throw new Error('FR-009: visible long-name row missing before reflow measurement');
      const range = document.createRange(); range.selectNodeContents(row);
      return new Set([...range.getClientRects()].map(rect => Math.round(rect.top))).size;
    });
    expect(lineCount, 'FR-009: the maximum-length helper name wraps onto multiple lines at 320px').toBeGreaterThan(1);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'FR-009 narrow row wraps without horizontal scrolling').toBe(true);
    await expect(chatInput(page)).toBeEnabled(); await expect(page.getByTestId('stop-btn')).toBeEnabled();
    await chatInput(page).fill('composer remains usable');
    await scene.own.goto(`${scene.gateway.baseURL}/#/sessions/${scene.children[0].id}`);
    await waitForConnected(scene.own); await scene.own.getByTestId('stop-btn').click();
    await expectNamed(scene, 'Helper 3');
    await expect(page.getByText('1 more helper has a model notice', { exact: true })).toHaveCount(0);
    await recordScene(scene, info, 'narrow-promotion');
  } finally { await scene.close(); }
});

test('grandchild copies go top and own only, then clear while middle helper is still running', async ({ page }, info) => {
  const scene = await createNoticeScene(page, info, { script: 'retry', wait: 10, nested: true, count: 2 });
  try {
    await scene.releaseFailures(); await expectNamed(scene);
    const middle = await page.context().newPage();
    try {
      await openSessionByDeepLink(middle, scene.children[0].id, scene.gateway.baseURL); await waitForConnected(middle);
      await expect.poll(() => visibleNamed(middle, 'Researcher', 'Retrying')).toEqual([]);
      scene.provider.answer[1].resolve(); await expectNamed(scene, 'Researcher', 'after 1 retry');
      scene.provider.finish[1].resolve();
      await expect.poll(() => ended(scene, 1), { timeout: 40_000 }).toBe('done');
      await expect.poll(() => visibleNamed(page, 'Researcher', 'retry')).toEqual([]);
      expect(await ended(scene, 0)).not.toBe('done');
    } finally { await middle.close(); }
  } finally { await scene.close(); }
});

for (const roster of ['missing', 'loading', 'failed'] as const) {
  test(`unknown helper roster ${roster}: Helper prefix without any raw id`, async ({ page }, info) => {
    const scene = await createNoticeScene(page, info, { script: 'retry', wait: 60 });
    try {
      let heldRosterRequests = 0;
      await page.route('**/api/v1/agents', async route => {
        if (roster === 'loading') {
          // Hold the shared roster query, not the separate REST lookup that
          // workspace entry also needs before it can mount the chat at all.
          if (heldRosterRequests === 0) {
            heldRosterRequests++;
            await new Promise<void>(resolve => page.once('close', () => resolve()));
          } else await route.continue();
        } else await route.fulfill({ status: roster === 'missing' ? 200 : 503, contentType: 'application/json', body: roster === 'missing' ? '[]' : '{"error":"roster unavailable"}' });
      });
      await page.reload(); await waitForConnected(page);
      await expect(chatInput(page), 'positive control: roster loading must not stall chat restoration').toBeVisible();
      if (roster === 'loading') expect(heldRosterRequests, 'one real roster query remains pending').toBe(1);
      await scene.releaseFailures();
      await expectNamed(scene, 'Helper');
      const rows = await visibleNamed(page, 'Helper', 'Retrying automatically');
      expect(rows, 'FR-005: exact fallback prefix belongs to one visible notice').toHaveLength(1);
      for (const id of [scene.agents[0].id, scene.children[0].id]) expect(rows[0], 'FR-005: named notice never exposes an identity').not.toContain(id);
    } finally { await scene.close(); }
  });
}

// FR-006/014: fault only transport, never notice production/reducer state.
for (const catchup of ['incremental', 'snapshot', 'restart', 'live_only'] as const) {
  test(`missed helper end reconnects through ${catchup}; Writer survives and Researcher leaves`, async ({ page }, info) => {
    const scene = await createNoticeScene(page, info, { script: 'retry', helperScripts: ['retry', 'silence'], wait: 10, count: 2, unavailableHistory: catchup === 'live_only' });
    try {
      await scene.releaseFailures(); await expectNamed(scene);
      await expect.poll(() => scene.link.frames.some(f => f.type === 'provider_status' && f.state === 'waiting' && f.child_session_id === scene.children[1].id),
        { message: 'positive control: real Writer reaches the 30 s silence threshold', timeout: 35_000 }).toBe(true);
      await expectNamed(scene, 'Writer', "hasn't responded");
      scene.link.disconnect();
      scene.provider.answer[0].resolve(); scene.provider.finish[0].resolve();
      await expect.poll(() => ended(scene, 0), { message: 'positive control: actual helper ends while top is disconnected', timeout: 45_000 }).toBe('done');
      expect(await ended(scene, 1), 'Writer genuinely remains active before catch-up').toBe('working');
      const before = scene.link.frames.length;
      if (catchup === 'restart') {
        // Drive an actual restart, including native-helper resume. Keep the
        // approved Writer-survives oracle; an injected boot frame is not proof.
        await scene.gateway.kill9(); await scene.gateway.restart({ relogin: false });
      }
      scene.link.reconnect(catchup === 'snapshot' || catchup === 'live_only');
      await waitForConnected(page, { timeout: 15_000 });
      await expect.poll(() => scene.link.frames.slice(before).filter(f => f.type === 'catch_up_complete' && f.session_id === scene.root.id).length).toBeGreaterThan(0);
      const completion = scene.link.frames.slice(before).find(f => f.type === 'catch_up_complete' && f.session_id === scene.root.id);
      expect(completion?.mode, 'real server selected the specified catch-up path').toBe(catchup === 'incremental' ? 'incremental' : catchup === 'live_only' ? 'live_only' : 'snapshot');
      if (catchup === 'snapshot') expect(scene.link.frames.slice(before).find(f => f.type === 'session_snapshot')?.reason).toBe('retention_exceeded');
      if (catchup === 'restart') expect(scene.link.frames.slice(before).find(f => f.type === 'session_snapshot')?.reason).toBe('boot_mismatch');
      await expect.poll(() => visibleNamed(page, 'Researcher', 'retry|responded')).toEqual([]);
      await expectNamed(scene, 'Writer', "hasn't responded");
      await recordScene(scene, info, `missed-end-${catchup}`);
    } finally { await scene.close(); }
  });
}

test('late real ended for the old turn cannot remove the newer turn in the same helper chat', async ({ page }, info) => {
  const scene = await createNoticeScene(page, info, { script: 'retry', wait: 60 });
  try {
    await scene.releaseFailures(); await expectNamed(scene);
    const old = scene.traffic.frames.find(f => f.type === 'provider_retry' && f.child_session_id === scene.children[0].id);
    expect(old?.turn_id).toEqual(expect.any(String));
    scene.link.holdEnded();
    await scene.own.getByTestId('stop-btn').click();
    await expect.poll(() => ended(scene)).toBe('stopped');
    scene.provider.nextHelperTurn(0);
    await chatInput(scene.own).fill('HM_HELPER_0_TASK. Start the next turn.'); await chatInput(scene.own).press('Enter');
    await expect.poll(() => scene.traffic.frames.filter(f => f.type === 'provider_retry' && f.child_session_id === scene.children[0].id && f.turn_id !== old?.turn_id).length).toBeGreaterThan(0);
    await expectNamed(scene);
    scene.link.deliverHeldEnd();
    await expectNamed(scene);
    expect(await ended(scene)).toBe('working');
  } finally { await scene.close(); }
});

for (const composition of ['3 waiting', '4 recovered', '2 waiting + 2 recovered'] as const) {
  test(`overflow composition ${composition}: exact first-three and summary survive reload`, async ({ page }, info) => {
    const count = composition === '3 waiting' ? 3 : 4;
    const scene = await createNoticeScene(page, info, { script: 'retry', wait: 10, count });
    try {
      await scene.releaseFailures(); await expectNamed(scene);
      const recover = composition === '4 recovered' ? count : composition === '2 waiting + 2 recovered' ? 2 : 0;
      for (let i = 0; i < recover; i++) scene.provider.answer[i].resolve();
      for (let i = 0; i < 3; i++) await expectNamed(scene, scene.agents[i].name, i < recover ? 'after 1 retry' : 'Retrying');
      const summary = page.getByText('1 more helper has a model notice', { exact: true });
      if (count === 4) {
        await expect(summary).toBeVisible();
        await expect(summary).not.toContainText('waiting');
        await expect.poll(() => visibleNamed(page, scene.agents[3].name, 'retry|Retrying')).toEqual([]);
      } else await expect(summary).toHaveCount(0);
      await page.reload(); await waitForConnected(page);
      for (let i = 0; i < 3; i++) await expectNamed(scene, scene.agents[i].name, i < recover ? 'after 1 retry' : 'Retrying');
    } finally { await scene.close(); }
  });
}

test('named announcement has one polite owner and frozen countdown text', async ({ page }, info) => {
  const scene = await createNoticeScene(page, info, { script: 'retry', helperScripts: ['retry', 'silence'], wait: 60, count: 2 });
  try {
    await scene.releaseFailures(); await expectNamed(scene);
    const live = page.locator('[aria-live="polite"]').filter({ hasText: /^(Researcher|Writer):/ });
    await expect(live).toHaveCount(1);
    await expect(live).toHaveText(/^Researcher:/);
    const frozen = await live.innerText();
    await delay(1100);
    await expect(live).toHaveText(frozen);
    await expect(live.locator('[aria-live], [role="status"]')).toHaveCount(0);
    await expect.poll(() => scene.link.frames.some(f => f.type === 'provider_status' && f.state === 'waiting' && f.child_session_id === scene.children[1].id), { timeout: 35_000 }).toBe(true);
    await expect(live).toHaveCount(1); await expect(live).toHaveText(/^Writer:/);
    const writerText = await live.innerText(); await delay(1100);
    await expect(live).toHaveText(writerText);
  } finally { await scene.close(); }
});

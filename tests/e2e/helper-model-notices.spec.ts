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
import { recoverHelper } from './fixtures/helper-notices-recovery';
import { expectPainted, paintedText, visibleNamedRows } from './fixtures/helper-notices-visible';
import { checkNoticeVisibilityInstrument } from './fixtures/helper-notices-instrument';
import { expectRestartedWriter } from './fixtures/helper-notices-restart';
import { reloadCountdownPair } from './fixtures/helper-notices-reload';

test.use({ storageState: { cookies: [], origins: [] } });
test.describe.configure({ retries: 0 });
test.setTimeout(150_000);

async function afterRealWait(scene: NoticeScene, seconds: number) {
  const initial = scene.traffic.frames.find(f => f.type === 'provider_retry' && f.session_id === scene.root.id &&
    f.child_session_id === scene.children[0].id);
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
    if (!writing) await checkNoticeVisibilityInstrument(page, info);
    const scene = await createNoticeScene(page, info, { script: 'retry', wait: 10, parentWriting: writing });
    try {
      await scene.releaseFailures();
      await expectNamed(scene);
      await expect(scene.own.getByTestId('provider-retry-indicator'), 'FR-001/004: helper own retry before any answer').toBeVisible();
      await expectPainted(scene.own.getByTestId('provider-retry-indicator'), /Retrying automatically/);
      expect((await paintedText(scene.own.getByTestId('provider-retry-indicator')))[0]).not.toContain('Researcher:');
      expect(scene.ownTraffic.frames.filter(f => f.type === 'token' && f.session_id === scene.children[0].id)).toEqual([]);
      await recordScene(scene, info, '429-before-byte');
      await recoverHelper(scene);
      await expectPainted(scene.own.getByText('Connection came back — the model answered after 1 retry.', { exact: true }),
        'Connection came back — the model answered after 1 retry.');
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
      // FR-011 preserves ordinary saved answers; it does not forbid a second
      // real parent turn after handback. Each accepted answer must be painted.
      await expect.poll(() => paintedText(page.getByText('HM_ROOT_OK', { exact: false })), {
        message: 'FR-011: a genuine saved root answer remains painted after reload', timeout: 2_000,
      }).toEqual(expect.arrayContaining([expect.stringMatching(/HM_ROOT_OK/)]));
      await expectPainted(scene.own.getByText('HM_CHILD_0_OK', { exact: true }), 'HM_CHILD_0_OK');
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
    await recoverHelper(scene);
  } finally { await scene.close(); }
});

for (const script of ['empty', 'timeout'] as const) {
  test(`${script}: both retry sentences and responding clear while real 30 s tool still runs, including reload`, async ({ page }, info) => {
    const scene = await createNoticeScene(page, info, { script });
    try {
      await scene.releaseFailures();
      const clause = script === 'empty' ? 'sent an empty reply' : 'did not start answering in time';
      await expectNamed(scene, 'Researcher', clause);
      await expectPainted(scene.own.getByTestId('provider-retry-indicator'), new RegExp(clause));
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
    await expectPainted(scene.own.getByText(/Answered by the Fallback model/), /Answered by the Fallback model/);
    await expectPainted(scene.own.getByText('HM_CHILD_0_OK', { exact: true }), 'HM_CHILD_0_OK');
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
        await expectPainted(scene.own.getByTestId('provider-waiting-indicator'), /hasn't responded/);
        await delay(Math.max(0, start + 35_000 - Date.now()));
      }
      scene.provider.answer[0].resolve(); await workingTool(scene);
      await expect.poll(() => visibleNamed(page, 'Researcher', "hasn't responded")).toEqual([]);
      await expect(scene.own.getByTestId('provider-waiting-indicator')).toHaveCount(0);
    } finally { await scene.close(); }
  });
}

test('silence then 429: clear-before-retry live and after reload in both chats', async ({ page }, info) => {
  const scene = await createNoticeScene(page, info, { script: 'silence-retry', wait: 20, parentSilenceRetry: true });
  try {
    await scene.releaseFailures();
    // Derive the opaque own turn id from an actual main-agent token, not a
    // helper copy or an assumed turn-id format. Legacy status has no agent_id.
    const ownStart = scene.link.forwarded.find(f => f.type === 'token' && f.session_id === scene.root.id &&
      f.agent_id === scene.parent.id && f.content === 'HM_PARENT_DELEGATING ');
    const ownTurn = ownStart?.turn_id;
    expect(ownTurn, 'FR-013 own positive control: real main-agent token identifies the OWN turn').toEqual(expect.any(String));
    const ownWaiting = () => scene.link.forwarded.find(f => f.type === 'provider_status' && f.state === 'waiting' &&
      f.session_id === scene.root.id && !f.child_session_id && f.turn_id === ownTurn);
    await expect.poll(ownWaiting, { message: 'FR-013 own positive control: real main-agent silence threshold', timeout: 35_000 }).toBeDefined();
    const ownRequest = scene.provider.evidence.requests.find(r => r.role === 'parent' && r.call === 2);
    expect(ownRequest, 'own continuation actually reached the scripted model edge').toBeDefined();
    expect(ownRequest!.status, 'own request is still silent before the external HTTP failure').toBeUndefined();
    await expectPainted(page.getByTestId('provider-waiting-indicator'), /hasn't responded/);
    scene.provider.parentFailure.resolve();
    const ownRetry = () => scene.link.forwarded.find(f => f.type === 'provider_retry' && f.session_id === scene.root.id &&
      !f.child_session_id && f.turn_id === ownTurn);
    await expect.poll(ownRetry, { message: 'FR-013 own positive control: real retry after own HTTP 429' }).toBeDefined();
    expect(ownRequest!.status, 'the actual own model request failed with 429').toBe(429);
    expect(ownRetry()!.retry_after_seconds, 'own backoff is the scripted 60 s, independent of the helper 20 s').toBe(60);
    const ownEvents = scene.link.forwarded.filter(f => f.session_id === scene.root.id && !f.child_session_id &&
      f.turn_id === ownTurn && (f.type === 'provider_status' || f.type === 'provider_retry'));
    expect(ownEvents.slice(0, 3).map(f => f.type === 'provider_retry' ? 'retry' : f.state),
      'FR-013 own live: silence clear is published BEFORE the new own retry').toEqual(['waiting', 'responding', 'retry']);
    const ownSentence = /^openrouter is rate-limiting requests right now\. Retrying automatically in \d:\d{2} \(attempt 2 of 6\)\.$/;
    await expectPainted(page.getByTestId('provider-retry-indicator'), ownSentence);
    await expect(page.getByTestId('provider-waiting-indicator'), 'FR-013 own live: old silence is gone').toHaveCount(0);
    const helperWaitingIndex = () => scene.traffic.frames.findIndex(f => f.type === 'provider_status' && f.state === 'waiting' &&
      f.session_id === scene.root.id && f.turn_id !== ownTurn);
    await expect.poll(helperWaitingIndex, { message: 'positive control: the helper silence copy actually arrived',
      timeout: Math.max(1, scene.provider.helpers[0].firstAt + 35_000 - Date.now()) }).toBeGreaterThanOrEqual(0);
    await expectNamed(scene, 'Researcher', "hasn't responded", scene.traffic.receivedAt[helperWaitingIndex()]);
    scene.provider.answer[0].resolve();
    await expectNamed(scene);
    for (const [sid, frames] of [[scene.root.id, scene.link.forwarded], [scene.children[0].id, scene.ownTraffic.frames]] as const) {
      const relevant = frames.filter(f => f.session_id === sid && f.turn_id !== ownTurn && (f.type === 'provider_status' || f.type === 'provider_retry'));
      expect(relevant.slice(0, 3).map(f => f.type === 'provider_retry' ? 'retry' : f.state)).toEqual(['waiting', 'responding', 'retry']);
    }
    await afterRealWait(scene, 5);
    const beforeReload = scene.link.forwarded.length;
    // Chief correction b57b0f8713: true remaining at FIRST page-side paint
    // ±1s; independent measured navigation→paint <=2s, never poll-return time.
    await reloadCountdownPair(scene, info, 'silence-429', [0], 0,
      [{ sessionId: scene.root.id, kind: 'own', retryAt: String(ownRetry()!.retry_at) }]);
    await expectPainted(page.getByTestId('provider-retry-indicator'), ownSentence);
    await expect(page.getByTestId('provider-waiting-indicator'), 'FR-013 own reload: old silence cannot erase the retry').toHaveCount(0);
    await expect.poll(() => scene.link.forwarded.slice(beforeReload).filter(f => f.type === 'provider_retry' &&
      f.session_id === scene.root.id && !f.child_session_id && f.turn_id === ownTurn).map(f => f.retry_at),
    { message: 'FR-013 own replay: the same real countdown survives actual reload', timeout: 2_000 }).toEqual([ownRetry()!.retry_at]);
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
    // Historical case title/identity retained; b57b0f8713 replaces the old
    // fixed range with the page-measured ±1s oracle in BOTH chats.
    await reloadCountdownPair(scene, info, 'thirty-second-wait');
    await recordScene(scene, info, 'reattach-five-seconds');
  } finally { await scene.close(); }
});

test('same turn retry → recovery → real tool → retry: only current row replays', async ({ page }, info) => {
  const scene = await createNoticeScene(page, info, { script: 'repeat', wait: 10 });
  try {
    await scene.releaseFailures(); await expectNamed(scene);
    await recoverHelper(scene);
    await page.reload(); await waitForConnected(page);
    await expectNamed(scene, 'Researcher', 'after 1 retry');
    await expect.poll(() => visibleNamed(page, 'Researcher', 'Retrying automatically')).toEqual([]);
    await expect.poll(() => scene.provider.evidence.requests.filter(r => r.role === 'helper-0' && r.status === 429).length, { timeout: 40_000 }).toBe(2);
    await expectNamed(scene);
    const retries = scene.traffic.frames.filter(f => f.type === 'provider_retry' && f.child_session_id === scene.children[0].id);
    expect(new Set(retries.map(f => f.turn_id)).size).toBe(1);
    await reloadCountdownPair(scene, info, 'same-turn-later-retry');
    await expectNamed(scene);
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
      await recoverHelper(scene);
      const ownIndicator = page.getByTestId('provider-retry-indicator').first();
      await expectPainted(ownIndicator, /^openrouter is rate-limiting.*attempt/);
      expect(scene.traffic.frames.filter(f => f.type === 'provider_retry' && !f.child_session_id && f.session_id === scene.root.id), 'helper recovery cannot restart the root retry time').toEqual([ownBefore]);
      scene.provider.finish[0].resolve();
      await expect.poll(() => ended(scene), { timeout: 40_000 }).toBe('done');
      // Writer's second HTTP call has now been silent through Researcher's
      // real30s tool. FR-006 requires the next notice, not the obsolete5s retry.
      const writerCall = scene.provider.evidence.requests.find(r => r.role === 'helper-1' && r.call === 2);
      expect(writerCall, 'positive control: Writer actually made its second model request').toBeDefined();
      const writerRetry = scene.traffic.frames.find(f => f.type === 'provider_retry' && f.child_session_id === scene.children[1].id);
      expect(writerRetry?.turn_id, 'positive control: second helper has its own real retry turn').toEqual(expect.any(String));
      const waitingIndex = () => scene.traffic.frames.findIndex(f => f.type === 'provider_status' && f.state === 'waiting' &&
        f.child_session_id === scene.children[1].id && f.turn_id === writerRetry!.turn_id);
      // Actual second request→silence threshold30s plus the spec's2s show budget.
      await expect.poll(waitingIndex, { message: 'FR-006: actual Writer silence notice after its unanswered retry',
        timeout: Math.max(1, writerCall!.at + 32_000 - Date.now()) }).toBeGreaterThanOrEqual(0);
      await expectNamed(scene, duplicateNames ? 'Researcher' : 'Writer', "hasn't responded", scene.traffic.receivedAt[waitingIndex()]);
      await expect.poll(() => visibleNamed(page, duplicateNames ? 'Researcher' : 'Writer', '.*'),
        { message: 'FR-007: exactly the still-active helper remains, in its current state' }).toHaveLength(1);
      if (!duplicateNames) expect(await visibleNamed(page, 'Researcher', '.*'), 'FR-006: ended Researcher has no row').toEqual([]);
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
    await expectPainted(page.getByText('1 more helper has a model notice', { exact: true }), '1 more helper has a model notice');
    await page.setViewportSize({ width: 320, height: 800 });
    const measured = await visibleNamedRows(page, longName, 'Retrying automatically');
    expect(measured, 'FR-009: one painted long-name row before reflow measurement').toHaveLength(1);
    const lineCount = measured[0].lines;
    expect(lineCount, 'FR-009: the maximum-length helper name wraps onto multiple lines at 320px').toBeGreaterThan(1);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'FR-009 narrow row wraps without horizontal scrolling').toBe(true);
    await expect(chatInput(page)).toBeEnabled(); await expect(page.getByTestId('stop-btn')).toBeEnabled();
    await chatInput(page).fill('composer remains usable');
    await openSessionByDeepLink(scene.own, scene.children[0].id, scene.gateway.baseURL);
    await scene.own.getByTestId('stop-btn').click();
    await expect.poll(() => ended(scene, 0), { message: 'positive control: Stop targeted the oldest helper, not the previous route' }).toBe('stopped');
    expect(await ended(scene, 3), 'the overflow helper genuinely remains active to promote').toBe('working');
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
      await recoverHelper(scene, 1);
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
  test(`missed helper end reconnects through ${catchup}; ${catchup === 'restart' ? 'old rows clear before Writer resumes its notice' : 'Writer survives and Researcher leaves'}`, async ({ page }, info) => {
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
      const oldWriter = scene.traffic.frames.find(f => f.type === 'provider_status' && f.state === 'waiting' && f.child_session_id === scene.children[1].id);
      const writerCalls = scene.provider.evidence.requests.filter(r => r.role === 'helper-1').length;
      const restartAt = Date.now();
      if (catchup === 'restart') {
        // Corrected spec f900e29a72: interrupted notices are not persisted;
        // an actual resumed turn must earn a new notice, not replay the old one.
        await scene.gateway.kill9(); await scene.gateway.restart({ relogin: false });
      }
      await scene.link.reconnect(catchup === 'snapshot' || catchup === 'live_only');
      await waitForConnected(page, { timeout: 15_000 });
      await expect.poll(() => scene.link.frames.slice(before).filter(f => f.type === 'catch_up_complete' && f.session_id === scene.root.id).length).toBeGreaterThan(0);
      const completion = scene.link.frames.slice(before).find(f => f.type === 'catch_up_complete' && f.session_id === scene.root.id);
      expect(completion?.mode, 'real server selected the specified catch-up path').toBe(catchup === 'incremental' ? 'incremental' : catchup === 'live_only' ? 'live_only' : 'snapshot');
      if (catchup === 'snapshot') expect(scene.link.frames.slice(before).find(f => f.type === 'session_snapshot')?.reason).toBe('retention_exceeded');
      if (catchup === 'restart') expect(scene.link.frames.slice(before).find(f => f.type === 'session_snapshot')?.reason).toBe('boot_mismatch');
      if (catchup === 'restart') {
        expect(await visibleNamed(page, 'Writer', '.*'), 'restart BDD: no stale Writer at the first post-catch-up check').toEqual([]);
        expect(await visibleNamed(page, 'Researcher', '.*'), 'restart BDD: no stale Researcher at the first post-catch-up check').toEqual([]);
      }
      // FR-006: the ended turn has no row of ANY notice state. A lowercase
      // "retry" alternative would also miss a stale "Retrying now…" row.
      await expect.poll(() => visibleNamed(page, 'Researcher', '.*')).toEqual([]);
      if (catchup === 'restart') await expectRestartedWriter(scene, restartAt, writerCalls, oldWriter?.turn_id);
      else await expectNamed(scene, 'Writer', "hasn't responded");
      await recordScene(scene, info, `missed-end-${catchup}`);
    } finally { await scene.close(); }
  });
}

test('ordered real held end removes only its helper; two rows become one and replay stays current', async ({ page }, info) => {
  // T8 approved split B: browser covers legal ordered transport/replay for two
  // real helper turns; the same-chat late-publication race is the sequenced
  // reducer case (old retry1, newer retry2, old ended3), not transport reordering.
  const scene = await createNoticeScene(page, info, { script: 'retry', helperWaits: [60, 120], count: 2, duplicateNames: true });
  try {
    await scene.releaseFailures();
    await expect.poll(() => visibleNamed(page, 'Researcher', 'Retrying automatically'), {
      message: 'FR-005/007: two real same-name helper turns are separately visible', timeout: 2_000,
    }).toHaveLength(2);
    await afterRealWait(scene, 2);
    const oldClause = 'Retrying automatically in 0:[0-5][0-9] ';
    const survivorClause = 'Retrying automatically in (?:1:[0-5][0-9]|2:00) ';
    await expectNamed(scene, 'Researcher', oldClause);
    await expectNamed(scene, 'Researcher', survivorClause);
    const old = scene.traffic.frames.find(f => f.type === 'provider_retry' && f.child_session_id === scene.children[0].id);
    expect(old?.turn_id).toEqual(expect.any(String));
    scene.link.holdEnded();
    await openSessionByDeepLink(scene.own, scene.children[0].id, scene.gateway.baseURL);
    await scene.own.getByTestId('stop-btn').click();
    await expect.poll(() => ended(scene, 0)).toBe('stopped');
    await expect.poll(() => scene.link.heldEnded().filter(f => f.turn_id === old!.turn_id).length,
      { message: 'positive control: the actual stopped-turn end is held before its ordered suffix' }).toBe(1);
    expect(scene.link.forwarded.filter(f => f.type === 'provider_retry').map(f => f.child_session_id),
      'positive delivery instrument: both real retries were forwarded to the page').toEqual(scene.children.map(c => c.id));
    expect(scene.link.forwarded.filter(f => f.type === 'provider_status' && f.state === 'ended' && f.turn_id === old!.turn_id),
      'FR-006 delivery control: no old end is forwarded on ANY owned socket before release').toEqual([]);
    await expect.poll(() => visibleNamed(page, 'Researcher', 'Retrying automatically'),
      { message: 'FR-005/006: both rows remain until ordered ended delivery', timeout: 2_000 }).toHaveLength(2);
    const suffix = scene.link.deliverHeldEnd();
    const end = suffix.find(f => f.type === 'provider_status' && f.state === 'ended' && f.turn_id === old!.turn_id);
    expect(end?.seq, 'positive control: the real held end retains its original server sequence').toEqual(expect.any(Number));
    expect(scene.link.forwarded.filter(f => f.type === 'provider_status' && f.state === 'ended' && f.turn_id === old!.turn_id),
      'FR-006 delivery control: the exact original end is forwarded once, only on release').toEqual([end]);
    await expect.poll(() => visibleNamed(page, 'Researcher', 'Retrying automatically'),
      { message: 'FR-006/007: ordered end leaves exactly the other helper turn', timeout: 2_000 }).toHaveLength(1);
    expect(await visibleNamed(page, 'Researcher', oldClause)).toEqual([]);
    await expectNamed(scene, 'Researcher', survivorClause);
    expect(await ended(scene, 1), 'positive control: the surviving helper is still working').toBe('working');
    const before = scene.traffic.frames.length;
    await reloadCountdownPair(scene, info, 'ordered-survivor', [1], 1);
    await expect.poll(() => scene.traffic.frames.slice(before).filter(f => f.type === 'catch_up_complete').length,
      { message: 'positive control: actual server replay completed after reload' }).toBeGreaterThan(0);
    await expect.poll(() => visibleNamed(page, 'Researcher', 'Retrying automatically'),
      { message: 'FR-008: replay retains exactly the still-active turn', timeout: 2_000 }).toHaveLength(1);
    expect(await visibleNamed(page, 'Researcher', oldClause)).toEqual([]);
    await expectNamed(scene, 'Researcher', survivorClause);
  } finally { await scene.close(); }
});

for (const composition of ['3 waiting', '4 recovered', '2 waiting + 2 recovered'] as const) {
  test(`overflow composition ${composition}: exact first-three and summary survive reload`, async ({ page }, info) => {
    const count = composition === '3 waiting' ? 3 : 4;
    const scene = await createNoticeScene(page, info, { script: 'retry', wait: 10, count });
    try {
      await scene.releaseFailures(); await expectNamed(scene);
      const recover = composition === '4 recovered' ? count : composition === '2 waiting + 2 recovered' ? 2 : 0;
      await Promise.all(Array.from({ length: recover }, (_, i) =>
        recoverHelper(scene, i, scene.agents[i].name, i < 3)));
      for (let i = 0; i < 3; i++) await expectNamed(scene, scene.agents[i].name, i < recover ? 'after 1 retry' : 'Retrying');
      const summary = page.getByText('1 more helper has a model notice', { exact: true });
      if (count === 4) {
        await expectPainted(summary, '1 more helper has a model notice');
        expect((await paintedText(summary))[0]).not.toContain('waiting');
        await expect.poll(() => visibleNamed(page, scene.agents[3].name, 'retry|Retrying')).toEqual([]);
      } else await expect(summary).toHaveCount(0);
      if (recover < count) {
        const waiting = Array.from({ length: Math.min(3, count) - recover }, (_, i) => i + recover);
        await reloadCountdownPair(scene, info, `overflow-${composition}`, waiting, recover);
      } else {
        // All rows are recovered: no countdown is expected or fabricated.
        await page.reload(); await waitForConnected(page);
      }
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
    const area = page.getByTestId('provider-helper-rows');
    const announcementNodes = '[aria-live], [role="status"], [role="alert"], [role="log"]';
    await expect(area, 'FR-010: inspect all named rows, not only descendants of the announcement owner').toHaveCount(1);
    await expect(area.locator(announcementNodes), 'FR-010: retry row has no indicator-owned live region').toHaveCount(0);
    await expect.poll(() => scene.link.frames.some(f => f.type === 'provider_status' && f.state === 'waiting' && f.child_session_id === scene.children[1].id), { timeout: 35_000 }).toBe(true);
    await expect(live).toHaveCount(1); await expect(live).toHaveText(/^Writer:/);
    await expect(area.locator(announcementNodes), 'FR-010: waiting row has no indicator-owned live region either').toHaveCount(0);
    const writerText = await live.innerText(); await delay(1100);
    await expect(live).toHaveText(writerText);
  } finally { await scene.close(); }
});

import { expect } from '@playwright/test';
import { SessionHistoryResponse } from '../../src/lib/api/generated/schemas';
import { WsFrame } from '../../src/lib/api/generated/ws-schemas';
import type { WebSocketRoute } from '@playwright/test';
import { chatInput, waitForConnected } from './fixtures/selectors';
import { test, HistoryHarness, observeTraffic, anchor, expectAnchor, fixtureId, geometry, historyRow, historyScroller, holdPage, scrollToDistance, settled } from './fixtures/chat-history-paging/harness';
import { browseBack, dropSocket, expectedReaderOrdinals } from './fixtures/chat-history-paging/actions';
import { ageRolledHistoryPartitions, sweepChatHistoryFixture } from './fixtures/chat-history';
import { readerFixtureDays } from './fixtures/chat-history-paging/day-fixtures';
import { openSessionByDeepLink } from './fixtures/session-setup';
import { assertRestoredControl, controlProvider, floodRealJournal, pendingAction, reconnectWithPending,
  sendControlInstruction, stopPendingControl, stopRunningControlTurn, waitForPendingControl } from './fixtures/chat-history-paging/control-actions';
import { CONTROL_RUNNING } from './fixtures/chat-history-paging/control-provider';

test.use({ storageState: { cookies: [], origins: [] }, viewport: { width: 1280, height: 1000 } });
test.describe.configure({ retries: 0 });
test.setTimeout(240_000); // Real3,000-row UI traversal, not a clock-based oracle.

test('T24 B04:transport loss after real replay but before complete retains the prior reader and never claims catch-up success', async ({ history, page }) => {
  await history.seed({ label: 'incomplete', count: 800, shape: 'varied' });
  await history.authenticate();
  let armed = false;
  let held!: () => void;
  const completeHeld = new Promise<void>((resolve) => { held = resolve; });
  let interruptedRoute: WebSocketRoute | undefined;
  let upstreamCompletions = 0;
  const deliveredToPage: WsFrame[] = [];
  // Real server forwarding is explicit. Only DELIVERY of its actual final
  // frame is held; no generated snapshot/replay/control frame is fabricated.
  await page.routeWebSocket((url) => url.origin.replace(/^ws/, 'http') === history.gateway.baseURL, (socket) => {
    const server = socket.connectToServer();
    server.onMessage((message) => {
      const frame = WsFrame.parse(JSON.parse(message.toString()));
      if (armed && frame.type === 'catch_up_complete' && frame.session_id === history.sessionId) {
        upstreamCompletions++;
        interruptedRoute = socket;
        held();
      } else {
        socket.send(message);
        deliveredToPage.push(frame); // Native server receipt is NOT page delivery.
      }
    });
  });
  await history.open();
  expect(deliveredToPage.filter(frame => frame.type === 'catch_up_complete' && frame.session_id === history.sessionId),
    'positive delivery instrument sees the initial completion actually forwarded to the page').toHaveLength(1);
  await scrollToDistance(page, (await geometry(page)).H);
  const saved = await anchor(page);
  const before = deliveredToPage.length;
  armed = true;
  await history.gateway.kill9();
  await history.gateway.restart({ relogin: false });
  await completeHeld;
  expect(upstreamCompletions, 'positive transport instrument saw one actual server completion').toBe(1);
  await settled(page);
  await expectAnchor(page, saved);
  await page.context().setOffline(true);
  try {
    if (!interruptedRoute) throw new Error('real attach completion was not intercepted');
    await interruptedRoute.close({ code: 1001, reason: 'history-acceptance-transport-loss' });
    await settled(page);
    expect(deliveredToPage.slice(before).filter((f) => f.type === 'catch_up_complete' && f.session_id === history.sessionId),
      'undelivered control catch-up cannot manufacture successful complete').toEqual([]);
    await expectAnchor(page, saved);
    await expect(historyRow(page, saved.id)).toBeVisible();
    await history.record('interrupted-before-complete');
  } finally { armed = false; await page.context().setOffline(false); }
});

test('T24 B26: boot-mismatch snapshot3000 rows back retains reader/focus/draft and directly refreshes its closed interval', async ({ history, page }) => {
  await history.seed({ label: 'snapshot', count: 3400 });
  await history.authenticate();
  await history.open();
  await browseBack(history, 300);
  const old = await anchor(page);
  await chatInput(page).fill('DRAFT_NOT_SENT_DURING_RECONNECT');
  await chatInput(page).focus();
  const before = history.traffic.frames.length;
  const pagesBefore = history.traffic.pages.length;
  await history.gateway.kill9();
  await history.gateway.restart({ relogin: false }); // Keep this browser's single-slot cookie/session.
  await waitForConnected(page);
  await expect.poll(() => history.traffic.frames.slice(before).filter((f) => f.type === 'catch_up_complete' && f.session_id === history.sessionId).length).toBe(1);
  const fresh = history.snapshot();
  expect(fresh.reason).toBe('boot_mismatch');
  await expect(historyRow(page, old.id)).toBeVisible();
  await expectAnchor(page, old);
  await expect(chatInput(page)).toBeFocused();
  await expect(chatInput(page)).toHaveValue('DRAFT_NOT_SENT_DURING_RECONNECT');
  await expect.poll(() => history.traffic.pages.slice(pagesBefore).filter((p) => new URL(p.url).searchParams.has('window_start')).length).toBeGreaterThan(0);
  const refresh = history.traffic.pages.slice(pagesBefore);
  for (const response of refresh) {
    const url = new URL(response.url);
    expect(url.searchParams.has('window_start'), 'no walk backward from a fresh live tail').toBe(true);
    expect(url.searchParams.has('window_end')).toBe(true);
    const parsed = SessionHistoryResponse.parse(response.body);
    expect(parsed.page_info.mode).toBe('window_refresh');
    expect(parsed.entries.length).toBeLessThanOrEqual(100);
    expect(parsed.entries.map((entry) => Number(entry.id.slice(-6))).every((n) => n < 1000), 'range is the old reader, not its distant new tail').toBe(true);
  }
  expect(history.traffic.sends).toEqual([]);
  await history.record('historical-boot-snapshot', { anchor: old });
});

test('T24 B47 B10: failed range refresh keeps cached rows/out-of-date/anchor, pauses prefetch and Retry repeats the SAME range', async ({ history, page }) => {
  await history.seed({ label: 'refresh', count: 3400 });
  await history.authenticate();
  await history.open();
  await browseBack(history, 300);
  const saved = await anchor(page);
  await chatInput(page).fill('KEEP_REFRESH_DRAFT');
  const held = await holdPage(page, (url) => url.searchParams.has('window_start'));
  try {
    const completeBefore = history.traffic.frames.length;
    await history.gateway.kill9();
    await history.gateway.restart({ relogin: false });
    await waitForConnected(page);
    await expect.poll(() => history.traffic.frames.slice(completeBefore).filter((f) => f.type === 'catch_up_complete').length).toBe(1);
    await held.requested;
    await expect(page.getByText('Refreshing loaded messages…', { exact: true })).toBeVisible();
    await expectAnchor(page, saved);
    held.release(500);
    const alert = page.getByRole('alert').filter({ hasText: 'Could not refresh loaded messages.' });
    await expect(alert).toBeVisible();
    await expect(page.getByText(/out.of.date/i)).toBeVisible();
    await expect(historyScroller(page)).not.toHaveAttribute('aria-busy', 'true');
    await expectAnchor(page, saved);
    await expect(chatInput(page)).toHaveValue('KEEP_REFRESH_DRAFT');
    const allBefore = history.traffic.pages.length;
    await historyScroller(page).evaluate((el) => { el.dispatchEvent(new Event('scroll')); el.dispatchEvent(new Event('scroll')); });
    await settled(page);
    expect(history.traffic.pages.length, 'range failure pauses normal earlier/newer prefetch').toBe(allBefore);
    await held.dispose();
    const retry = await holdPage(page, (url) => url.searchParams.has('window_start'));
    try {
      const button = alert.getByRole('button', { name: 'Retry', exact: true });
      await button.focus();
      await page.keyboard.press('Enter');
      await retry.requested;
      await expect(button).toBeFocused();
      const first = new URL(held.requests[0]).searchParams;
      const again = new URL(retry.requests[0]).searchParams;
      expect([again.get('window_start'), again.get('window_end')]).toEqual([first.get('window_start'), first.get('window_end')]);
      const originalFailure = await alert.textContent();
      retry.release(500);
      await expect(alert).toContainText('Could not refresh loaded messages.');
      await expect.poll(() => alert.textContent(), { message: 'D8 repeated failure is re-announced using a changed attempt message' }).not.toBe(originalFailure);
      await expect(button).toBeFocused();
      await expectAnchor(page, saved);
    } finally { retry.release(); await retry.dispose(); }
    expect(history.traffic.sends).toEqual([]);
  } finally { held.release(); await held.dispose(); }
  await history.record('cached-refresh-failure');
});

test('T24 B06: ordinary same-boot incremental reconnect never treats old reader as a latest-navigation request', async ({ history, page }) => {
  await history.seed({ label: 'incremental', count: 500, shape: 'varied' });
  await history.authenticate();
  await history.open();
  await scrollToDistance(page, (await geometry(page)).H);
  const saved = await anchor(page);
  const before = history.traffic.frames.length;
  await dropSocket(page);
  await waitForConnected(page);
  await expect.poll(() => history.traffic.frames.slice(before).filter((f) => f.type === 'catch_up_complete' && f.session_id === history.sessionId).length).toBe(1);
  const completions = history.traffic.frames.slice(before).filter((f) => f.type === 'catch_up_complete');
  expect(completions.map((f) => f.type === 'catch_up_complete' ? f.mode : undefined)).toEqual(['incremental']);
  expect(history.traffic.frames.slice(before).filter((f) => f.type === 'session_snapshot')).toEqual([]);
  await expectAnchor(page, saved);
  await history.record('incremental-old-reader');
});

for (const outcome of ['success', 'failure'] as const) {
test(`T24 B11: obsolete A→B→A ${outcome} cannot change the replacement reader or its error state`, async ({ history, page }) => {
  await history.seed({ label: 'stale-a', count: 300 });
  await history.seed({ label: 'stale-b', count: 3 }, history.ids.otherSession.id);
  await history.authenticate();
  await history.open();
  const held = await holdPage(page, (url) => url.pathname.includes(history.sessionId) && url.searchParams.has('before'));
  try {
    await scrollToDistance(page, 0);
    const obsolete = (await held.requested).request();
    await history.open(history.ids.otherSession.id);
    await history.open();
    await page.evaluate(() => document.fonts.ready);
    await settled(page);
    const saved = await anchor(page);
    const replacementHeight = (await geometry(page)).height;
    held.release(outcome === 'failure' ? 500 : undefined);
    const delivered = await obsolete.response();
    if (delivered) {
      const deliveryError = await delivered.finished();
      if (deliveryError) expect(obsolete.failure()?.errorText).toBe('net::ERR_ABORTED');
    } else expect(obsolete.failure()?.errorText, 'an actual aborted read is also a valid stale-generation fence').toBe('net::ERR_ABORTED');
    await settled(page);
    expect((await geometry(page)).height, 'even an anchored stale prepend may not grow/change the replacement window').toBe(replacementHeight);
    await expect(page.getByRole('alert').filter({ hasText: 'Could not load earlier messages.' })).toHaveCount(0);
    await expect(historyRow(page, fixtureId('stale-a', 299))).toBeVisible();
    await expectAnchor(page, saved);
  } finally { held.release(); await held.dispose(); }
  await history.record(`obsolete-generation-${outcome}`);
});
}

for (const survival of ['anchor-kept', 'anchor-gone', 'none'] as const) {
  test(`T24 B27 B48: real retained range expiry (${survival}) uses exact copy and never passively returns to latest`, async ({ history, page }) => {
    const days = readerFixtureDays('expire', history.ids.agent.id);
    await history.seedDays(days);
    await history.authenticate();
    await history.open();
    await browseBack(history, 300);
    const saved = await anchor(page);
    await historyScroller(page).focus();
    const heldOrdinals = expectedReaderOrdinals(history);
    expect(heldOrdinals, 'D9 real backward traversal reached its500-group window').toHaveLength(500);
    expect(heldOrdinals.every((n) => n < 1000), 'the whole retained reader is in genuine rolled fixture partitions').toBe(true);
    const savedOrdinal = Number(saved.id.slice(-6));
    const fallback = (await geometry(page)).rows.find((row) => row.id === fixtureId('expire', savedOrdinal + 1));
    if (survival === 'anchor-gone') expect(fallback, 'pre-refresh offset of nearest later old-range survivor is observable').toBeDefined();
    const expired = survival === 'none' ? heldOrdinals : survival === 'anchor-gone' ? [savedOrdinal] : [heldOrdinals[0]];
    if (survival === 'anchor-kept') expect(expired).not.toContain(savedOrdinal);
    const before = history.traffic.frames.length;
    await history.gateway.kill9();
    history.gateway.assertStoppedForFixture();
    // Actual rollover exists before aging. Only the real retention owner deletes
    // the chosen rolled files and updates the real index; no fabricated frames.
    ageRolledHistoryPartitions(history.gateway, history.sessionId, expired.map((n) => days[n].day), new Date(Date.now() - 60 * 24 * 60 * 60 * 1000));
    const receipt = await sweepChatHistoryFixture(history.gateway, 30);
    expect(receipt.removed_partitions).toBe(expired.length);
    await history.gateway.restart({ relogin: false });
    await waitForConnected(page);
    await expect.poll(() => history.traffic.frames.slice(before).filter((f) => f.type === 'catch_up_complete').length).toBe(1);
    if (survival === 'none') {
      await expect(page.getByText('These loaded messages have expired.', { exact: true })).toBeVisible();
      await expect(page.getByRole('button', { name: 'Return to latest', exact: true })).toBeVisible();
      await expect(historyRow(page, saved.id)).toHaveCount(0);
      await expect(historyRow(page, fixtureId('expire', 3399))).toHaveCount(0);
    } else {
      await expect(page.getByText('Some loaded messages have expired.', { exact: true })).toBeVisible();
      if (survival === 'anchor-kept') await expectAnchor(page, saved);
      else {
        // The scenario removes exactly the anchor, leaving its next authored
        // source group. D7 chooses that nearest later OLD-range survivor.
        // Never take an expected anchor from a producer's reported output.
        const expected = fixtureId('expire', savedOrdinal + 1);
        await expect(historyRow(page, expected)).toBeVisible();
        if (!fallback) throw new Error('nearest later survivor pre-refresh geometry is missing');
        await expectAnchor(page, fallback);
        await expect(historyRow(page, saved.id)).toHaveCount(0);
      }
    }
    await expect(page.getByLabel('Conversation history', { exact: true })).toBeFocused();
    expect(history.traffic.sends).toEqual([]);
    await history.record(`range-expiry-${survival}`, { receipt });
  });
}

test('T24 B47:500 held tool groups can require MORE than5 bounded byte-limited refresh requests', async ({ history, page }) => {
  await history.seedDays(readerFixtureDays('range-bytes', history.ids.agent.id, true));
  await history.authenticate();
  await history.open();
  await browseBack(history, 300);
  const saved = await anchor(page);
  expect(expectedReaderOrdinals(history), 'held500-group interval is the real large-tool fixture').toHaveLength(500);
  await history.gateway.kill9();
  history.gateway.assertStoppedForFixture();
  const before = history.traffic.pages.length;
  await history.gateway.restart({ relogin: false });
  await waitForConnected(page);
  await expect.poll(() => history.traffic.pages.slice(before).filter((p) => new URL(p.url).searchParams.has('window_start')).length,
    { message: 'D-F1 range refresh must not stop at an invented five-request cap' }).toBeGreaterThan(5);
  await expect(page.getByText('Refreshing loaded messages…', { exact: true })).toHaveCount(0);
  const responses = history.traffic.pages.slice(before).map((r) => ({ url: new URL(r.url), page: SessionHistoryResponse.parse(r.body) }));
  expect(responses.every((r) => r.url.searchParams.has('window_start'))).toBe(true);
  expect(responses.reduce((sum, r) => sum + r.page.page_info.source_group_count, 0)).toBe(500);
  for (let i = 1; i < responses.length; i++) expect(responses[i].url.searchParams.get('before')).toBe(responses[i - 1].page.next_before);
  await expectAnchor(page, saved);
  await history.record('range-more-than-five-byte-pages');
});

for (const kind of ['approval', 'question'] as const) {
  test.describe(`T24 genuine pending ${kind}`, () => {
    test.use({ historyControlKind: kind });
    test(`T24 B05 B06 B26: pending ${kind} survives old-reader reconnect and a fresh viewer gets the real current snapshot`, async ({ history, page, browser }, info) => {
      await history.seed({ label: `pending-${kind}`, count: 3400 });
      await history.authenticate();
      await history.open();
      await browseBack(history, 300);
      const saved = await anchor(page);
      const viewers = await browser.newContext({ storageState: await page.context().storageState() });
      try {
        const producer = await viewers.newPage();
        await openSessionByDeepLink(producer, history.sessionId, history.gateway.baseURL);
        await sendControlInstruction(producer); // Other viewer: no intentional latest navigation in this old reader.
        const pending = await waitForPendingControl(history);
        const calls = controlProvider(history).calls();
        await expectAnchor(page, saved);
        await reconnectWithPending(history, pending, false);
        await expectAnchor(page, saved);
        const freshPage = await viewers.newPage();
        const fresh = new HistoryHarness(freshPage, history.gateway, history.ids, history.provider, info, history.logPath);
        await fresh.open(history.sessionId, { currentControlMayLockComposer: true }); // REAL snapshot; a genuine question may lock input.
        expect(fresh.snapshot().page_info.source_group_count).toBeLessThanOrEqual(100);
        await assertRestoredControl(fresh, pending, 0, false);
        await expect(pendingAction(freshPage, pending)).toBeVisible();
        expect(controlProvider(history).calls(), 'new viewer/reattach never executes another control request').toBe(calls);
        await stopPendingControl(fresh, pending);
        await expectAnchor(page, saved);
        expect(history.traffic.sends, 'a passive historical viewer never sends or restores old model context').toEqual([]);
        await history.record(`pending-${kind}-snapshot-and-stop`);
      } finally { await viewers.close(); }
    });
  });
}

test.describe('T24 actual journal retention overflow', () => {
  test.use({ historyControlKind: 'stream' });
  test('T24 B26: retention_exceeded (same boot) refreshes the held interval without teleporting to the newly published tail', async ({ history, page, browser }) => {
    await history.seed({ label: 'journal-reader', count: 3400 });
    await history.authenticate();
    await history.open();
    await browseBack(history, 300);
    const saved = await anchor(page);
    const oldBoot = history.snapshot().boot_id;
    const producerContext = await browser.newContext({ storageState: await page.context().storageState() });
    try {
      const producer = await producerContext.newPage();
      const traffic = observeTraffic(producer);
      await openSessionByDeepLink(producer, history.sessionId, history.gateway.baseURL);
      await sendControlInstruction(producer);
      await expect.poll(() => history.traffic.frames.some((f) => f.type === 'token' && f.session_id === history.sessionId && f.content === CONTROL_RUNNING)).toBe(true);
      await expectAnchor(page, saved);
      await waitForConnected(page); // Positive network-loss instrument before going offline.
      const before = history.traffic.frames.length;
      const pagesBefore = history.traffic.pages.length;
      await page.context().setOffline(true);
      await page.evaluate(() => {
        for (const socket of (window as unknown as { __ws_instances?: WebSocket[] }).__ws_instances ?? []) {
          if (socket.readyState === WebSocket.OPEN) socket.close(1000, 'history-retention-disconnect');
        }
      });
      await floodRealJournal(producer, traffic, history.sessionId, history.ids.agent.id);
      await page.context().setOffline(false);
      await waitForConnected(page);
      await expect.poll(() => history.traffic.frames.slice(before).filter((f) => f.type === 'catch_up_complete' && f.session_id === history.sessionId).length).toBe(1);
      const snapshot = history.snapshot();
      expect(snapshot.reason, 'real1-MiB journal was evicted; no injected cursor/reason or gateway restart').toBe('retention_exceeded');
      expect(snapshot.boot_id).toBe(oldBoot);
      await expectAnchor(page, saved);
      await expect.poll(() => history.traffic.pages.slice(pagesBefore).filter((r) => new URL(r.url).searchParams.has('window_start')).length).toBeGreaterThan(0);
      expect(history.traffic.pages.slice(pagesBefore).every((r) => new URL(r.url).searchParams.has('window_start')), 'refresh addresses only the old held interval, not the omitted middle').toBe(true);
      await stopRunningControlTurn(history);
      await expectAnchor(page, saved);
      expect(history.traffic.sends).toEqual([]);
      await history.record('same-boot-retention-exceeded-reader');
    } finally { await page.context().setOffline(false); await producerContext.close(); }
  });
});

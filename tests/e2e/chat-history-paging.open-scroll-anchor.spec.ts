import { expect, type Page } from '@playwright/test';
import { chatInput } from './fixtures/selectors';
import { openSessionByDeepLink } from './fixtures/session-setup';
import { test, anchor, expectAnchor, fixtureId, geometry, historyRow, historyScroller, holdPage, scrollToDistance, settled } from './fixtures/chat-history-paging/harness';

test.use({ storageState: { cookies: [], origins: [] }, viewport: { width: 1280, height: 1000 } });
test.describe.configure({ retries: 0 }); // Deterministic fixtures; never retry an assertion into green.

// T16; independent oracles: D5/D8/D9, D-A3/4, D-G1/2/3. No injected
// virtualizer, row heights or styles. GREEN/mutation are deferred to CHECK.
test('T16 B01: opens only the newest whole groups and settles at bottom after complete', async ({ history, page }) => {
  await history.seed({ label: 'open', count: 800 });
  await history.authenticate();
  await history.open();
  const snapshot = history.snapshot();
  const attach = history.traffic.attaches.find((f) => f.session_id === history.sessionId);
  expect(attach, 'one real foreground attach').toBeDefined();
  const limit = attach?.history_limit ?? 40;
  expect(snapshot.page_info.limit, 'D5 supplied attach limit or bootstrap40').toBe(limit);
  expect(snapshot.page_info.source_group_count).toBe(limit);
  expect(snapshot.has_earlier).toBe(true);
  const replay = history.traffic.frames.filter((f) => f.type === 'replay_message' && f.session_id === history.sessionId);
  expect(replay.map((f) => f.type === 'replay_message' ? f.id : undefined), 'authored newest suffix, not800-row lifetime replay')
    .toEqual(Array.from({ length: limit }, (_, i) => fixtureId('open', 800 - limit + i)));
  await expect.poll(async () => (await geometry(page)).bottom, { message: 'settled recent end, not replay done alone' }).toBeLessThanOrEqual(1);
  await expect(historyRow(page, fixtureId('open', 799))).toBeVisible();
  expect(history.traffic.attaches.filter((f) => f.session_id === history.sessionId)).toHaveLength(1);
  await history.record('recent-open');
});

test('T16 B07: real virtual prefix triggers once at2H, never at2H+1, and uses measured drawn rows', async ({ history, page }) => {
  await history.seed({ label: 'pixels', count: 900 });
  await history.authenticate();
  await history.open();
  await expect.poll(async () => { const g = await geometry(page); return g.height / g.H; }).toBeGreaterThanOrEqual(4);
  await expect(page.getByText('Loading earlier messages…', { exact: true })).toHaveCount(0);
  const initial = await geometry(page);
  const A = initial.rows.reduce((sum, row) => sum + row.height, 0) / initial.rows.length;
  expect(A, 'real stylesheet produced positive drawn-row samples').toBeGreaterThan(0);
  // Natural one-line rows must exercise the non80 estimate class (D-G1d),
  // otherwise this fixture cannot expose the fixed80 virtualizer mutant.
  expect(Math.abs(A - 80), 'positive control: actual row average differs from bootstrap80').toBeGreaterThan(1);
  const loaded = new Set(history.traffic.frames.filter((f) => f.type === 'replay_message' && f.session_id === history.sessionId)
    .map((f) => f.type === 'replay_message' ? f.id : undefined));
  const schemas = await import('../../src/lib/api/generated/schemas');
  for (const reply of history.traffic.pages.filter((r) => r.status === 200)) {
    for (const entry of schemas.SessionHistoryResponse.parse(reply.body).entries) loaded.add(entry.id);
  }
  const canvasHeight = await historyScroller(page).locator('[data-index]').first().evaluate((row) => {
    const canvas = row.parentElement?.parentElement;
    if (!canvas) throw new Error('real virtualizer canvas is absent; do not replace it with injected estimates');
    return canvas.getBoundingClientRect().height;
  });
  expect(Math.abs(canvasHeight - loaded.size * A), 'D5 unmounted uniform-row prefix uses CURRENT measured A, not fixed80').toBeLessThanOrEqual(1);
  const lastOpeningPage = history.traffic.pages.filter((r) => r.status === 200).at(-1);
  const boundary = lastOpeningPage ? schemas.SessionHistoryResponse.parse(lastOpeningPage.body).next_before : history.snapshot().next_before;
  expect(boundary).toMatch(/^hp1_/);
  const held = await holdPage(page, (url) => url.searchParams.get('before') === boundary);
  try {
    await scrollToDistance(page, 2 * initial.H + 1);
    expect(held.requests, 'D-G1 threshold+1 must not request').toEqual([]);
    await scrollToDistance(page, 2 * initial.H);
    const request = await held.requested;
    const query = new URL(request.request().url()).searchParams;
    const expected = Math.min(100, Math.max(20, Math.ceil(2 * initial.H / A)));
    expect(Number(query.get('limit')), 'D5 clamp(ceil(2H/A),20,100), measured real rows').toBe(expected);
    expect(Number(query.get('min_rows'))).toBe(Math.min(expected, Math.max(1, Math.ceil(2 * initial.H / A))));
    await scrollToDistance(page, 2 * initial.H - 1);
    await historyScroller(page).evaluate((el) => { el.dispatchEvent(new Event('scroll')); el.dispatchEvent(new Event('scroll')); });
    await settled(page);
    expect(held.requests, 'repeated observer/scroll events join the same boundary owner').toHaveLength(1);
    held.release();
    await expect(page.getByText('Loading earlier messages…', { exact: true })).toHaveCount(0);
  } finally { held.release(); await held.dispose(); }
  await history.record('pixel-threshold', { A, H: initial.H });
});

test('T16 B08 B09: pending prepend, reader movement and loader-to-error-to-Retry preserve the CURRENT anchor', async ({ history, page }) => {
  await history.seed({ label: 'moving', count: 420, shape: 'varied' });
  await history.authenticate();
  await history.open();
  const held = await holdPage(page, (url) => url.searchParams.has('before'));
  try {
    const H = (await geometry(page)).H;
    await scrollToDistance(page, 2 * H);
    await held.requested;
    const beforeMove = await anchor(page);
    await historyScroller(page).hover();
    await page.mouse.wheel(0, 400); // D-G2 genuine reader movement, not a saved request-time anchor.
    await settled(page);
    const current = await anchor(page);
    expect(current.id !== beforeMove.id || current.top !== beforeMove.top, 'instrument observed the reader move while request was held').toBe(true);
    // D-G2's live growth is real traffic from a SECOND viewer. Sending from
    // this reader would intentionally request follow-latest (D9), a different
    // action. Reuse its actual cookie instead of logging in over the single slot.
    const producer = await page.context().newPage();
    try {
      await openSessionByDeepLink(producer, history.sessionId, history.gateway.baseURL);
      for (let n = 0; n < 10; n++) {
        await chatInput(producer).fill(`OTHER_VIEWER_LIVE_ROW_${n}`);
        await chatInput(producer).press('Enter');
        await expect(chatInput(producer)).toHaveValue('');
      }
      await expect.poll(() => history.traffic.frames.filter((f) => f.type === 'user_message' &&
        f.session_id === history.sessionId && f.content.startsWith('OTHER_VIEWER_LIVE_ROW_')).length,
      { message: 'positive control:10 real live source rows reached the historical viewer' }).toBe(10);
      await expectAnchor(page, current);
    } finally { await producer.close(); }
    held.release(500);
    const alert = page.getByRole('alert').filter({ hasText: 'Could not load earlier messages.' });
    await expect(alert).toBeVisible();
    await expectAnchor(page, current);
    await held.dispose();
    const retry = await holdPage(page, (url) => url.searchParams.has('before'));
    try {
      const beforeRetry = await anchor(page);
      const retryButton = alert.getByRole('button', { name: 'Retry', exact: true });
      await retryButton.evaluate(element => (element as HTMLElement).focus({ preventScroll: true }));
      await page.keyboard.press('Enter');
      await retry.requested;
      await expectAnchor(page, beforeRetry);
      const requestedBoundary = new URL(held.requests[0]).searchParams.get('before');
      expect(new URL(retry.requests[0]).searchParams.get('before')).toBe(requestedBoundary);
      retry.release();
      await expect(alert).toHaveCount(0);
      await expectAnchor(page, beforeRetry); // Signed negative Retry/header shrink, not total scrollHeight.
    } finally { retry.release(); await retry.dispose(); }
  } finally { held.release(); await held.dispose(); }
  await history.record('moving-reader-retry');
});

test('T16 B09: a real delayed image growing above the reader preserves the visible occurrence', async ({ history, page }) => {
  let release!: () => void;
  const ready = new Promise<void>((resolve) => { release = resolve; });
  let requested!: () => void;
  const seen = new Promise<void>((resolve) => { requested = resolve; });
  // Only the image response is controlled. Production layout/rendering
  // decides the before/after height; no CSS or ResizeObserver stub is involved.
  // Same-origin on purpose: the shipped SPA policy is img-src 'self' data:
  // blob: (pkg/gateway/embed.go::spaBaseContentSecurityPolicy, ADR-083 D-C),
  // so a foreign image host is refused before any request leaves and only
  // ever renders "Image unavailable" — it can never grow late.
  const imageURL = `${history.origin}/history-fixture/growth.svg`;
  await page.route(imageURL, async (route) => {
    requested(); await ready;
    await route.fulfill({ contentType: 'image/svg+xml', body: '<svg xmlns="http://www.w3.org/2000/svg" width="800" height="640"><rect width="800" height="640" fill="navy"/></svg>' });
  });
  try {
    await history.seed({ label: 'image', start: 0, count: 40, imageURL });
    await history.authenticate();
    await history.open();
    await scrollToDistance(page, 0);
    await seen;
    await historyScroller(page).hover();
    await page.mouse.wheel(0, 160);
    await settled(page);
    const saved = await anchor(page);
    const image = page.getByRole('button', { name: 'View: Deferred history image', exact: true });
    const before = await image.evaluate((el) => el.getBoundingClientRect().height);
    release();
    await expect.poll(() => image.evaluate((el) => (el as HTMLImageElement).naturalHeight)).toBe(640);
    expect(await image.evaluate((el) => el.getBoundingClientRect().height), 'late content actually expanded above the anchor').toBeGreaterThan(before);
    await expectAnchor(page, saved);
    await history.record('late-image-anchor');
  } finally { release(); }
});

test('T16 B13: explicit earlier crosses ONE clear barrier without restoring context or opening the next barrier', async ({ history, page }) => {
  await history.seed({ label: 'clear', count: 331, clearAt: [250, 311] }); // D-A3:250,C1,60,C2,19.
  await history.authenticate();
  await history.open();
  await scrollToDistance(page, 0);
  const earlier = page.getByRole('button', { name: 'Show earlier messages', exact: true }).first();
  await expect(earlier).toBeVisible();
  const before = await anchor(page);
  const held = await holdPage(page, (url) => url.searchParams.has('before'));
  try {
    await earlier.focus();
    await page.keyboard.press('Enter');
    await held.requested;
    await expect(earlier).toBeFocused();
    held.release();
    await expect.poll(() => history.traffic.pages.filter((r) => r.status === 200).length).toBeGreaterThan(0);
    await expectAnchor(page, before);
    expect(history.traffic.sends, 'reading earlier never sends /clear or user input').toEqual([]);
    const response = await history.read({ before: history.snapshot().next_before!, limit: 100 });
    expect(response.entries[0].id, 'one crossing stops at C1, not lifetime beginning').toBe(fixtureId('clear', 250));
    expect(response.entries.at(-1)?.id).toBe(fixtureId('clear', 310));
    expect(response.page_info.stopped_at_clear).toBe(true);
    expect(response.has_earlier).toBe(true);
  } finally { held.release(); await held.dispose(); }
  await history.record('one-clear-crossing');
});

test('T16 B12: traversal past500 groups keeps the new prepend readable and can reload the evicted newer edge', async ({ history, page }) => {
  await history.seed({ label: 'window', count: 1000 });
  await history.authenticate();
  await history.open();
  const schemas = await import('../../src/lib/api/generated/schemas');
  const openingResponse = history.traffic.pages.filter(reply => reply.status === 200).at(-1);
  const openingPage = openingResponse ? schemas.SessionHistoryResponse.parse(openingResponse.body) : undefined;
  const openingReplay = history.traffic.frames.find(frame => frame.type === 'replay_message' && frame.session_id === history.sessionId);
  const openingFirst = openingPage?.entries[0]?.id ?? (openingReplay?.type === 'replay_message' ? openingReplay.id : undefined);
  expect(openingFirst, 'PREREQUISITE: actual opening carrier identifies its first source row').toBeDefined();
  if (!openingFirst) throw new Error('Actual opening first row missing');
  let firstId = openingFirst;
  // next_before is the generated carrier's top-level continuation field.
  let nextBefore = openingPage ? openingPage.next_before : history.snapshot().next_before;
  const seen = new Set<string>();
  let held = await holdPage(page, url => url.searchParams.has('before'));
  async function commitHeldPage(boundary: number) {
    const before = history.traffic.pages.length;
    const route = await held.requested;
    expect(new URL(route.request().url()).searchParams.get('before'),
      'B12: every older request continues exactly the previous successful page boundary').toBe(nextBefore);
    await expect(historyRow(page, firstId), 'the previous page first row is mounted at the reader top before commit').toBeVisible();
    const prior = (await geometry(page)).rows.find(row => row.id === firstId);
    expect(prior, 'the previous page first row has independently observed pre-commit geometry').toBeDefined();
    if (!prior) throw new Error('Previous page first-row geometry missing');
    held.release();
    await expect.poll(() => history.traffic.pages.length, { message: `boundary${boundary} must actually fetch older retained content` }).toBeGreaterThan(before);
    const responses = history.traffic.pages.slice(before);
    expect(responses, 'held physical request commits one page, never an unexamined extra fill').toHaveLength(1);
    const response = responses.find((r) => r.status === 200);
    expect(response, 'a failed request is not a successful500-group traversal').toBeDefined();
    if (!response) throw new Error('No successful history page');
    const parsed = schemas.SessionHistoryResponse.parse(response.body);
    expect(parsed.entries.length, 'a successful traversal page contains retained source rows').toBeGreaterThan(0);
    for (const entry of parsed.entries) {
      expect(seen.has(entry.id), `B12: new page source ${entry.id} is disjoint from every previously fetched page`).toBe(false);
      seen.add(entry.id);
    }
    await expect(historyRow(page, firstId), 'B12: the previous page first row stays mounted through the prepend').toBeAttached();
    await expectAnchor(page, prior); // Same <=1px oracle, never the new offscreen row.
    firstId = parsed.entries[0].id;
    nextBefore = parsed.next_before;
    await settled(page);
    await held.dispose();
  }
  try {
    await scrollToDistance(page, 0);
    for (let boundary = 0; boundary < 28; boundary++) {
      await commitHeldPage(boundary);
      // Hold the next real fill BEFORE user movement, so it cannot race the
      // newly prepended first row's actual readability judgment.
      held = await holdPage(page, url => url.searchParams.has('before'));
      await scrollToDistance(page, 0);
      await expect(historyRow(page, firstId), 'B12: the new prepend is readable after the reader moves to its top').toBeVisible();
    }
    // The final readability movement legitimately starts one more automatic
    // fill. Account for its boundary/IDs/anchor too; never discard that response.
    await commitHeldPage(28);
    await scrollToDistance(page, 0);
    await expect(historyRow(page, firstId), 'B12: the final automatic page is readable after the reader moves to its top').toBeVisible();
  } finally { held.release(); await held.dispose(); }
  expect(seen.size, 'positive traversal control actually crossed the500 source-group edge').toBeGreaterThan(500);
  const beforeForward = history.traffic.pages.length;
  await historyScroller(page).evaluate((el) => { el.scrollTop = el.scrollHeight; el.dispatchEvent(new Event('scroll')); });
  await expect.poll(() => history.traffic.pages.slice(beforeForward).filter((r) => new URL(r.url).searchParams.has('after')).length).toBeGreaterThan(0);
  await expect(page.getByRole('button', { name: 'Return to latest', exact: true })).toBeVisible();
  await history.record('far-edge-reload', { traversed: seen.size });
});

for (const predecessors of [0, 19, 20]) {
  test(`T16 B17: terminal${predecessors}-predecessor page removes continuation without an empty-page loop or focus jump`, async ({ history, page }) => {
    await history.seed({ label: 'terminal', count: predecessors + 20, clearAt: [predecessors] });
    await history.authenticate();
    await history.open();
    await scrollToDistance(page, 0);
    const earlier = page.getByRole('button', { name: 'Show earlier messages', exact: true });
    const before = history.traffic.pages.length;
    if (predecessors === 0) {
      expect(history.snapshot().has_earlier).toBe(false);
      await expect(earlier).toHaveCount(0);
    } else {
      const held = await holdPage(page, (url) => url.searchParams.has('before'));
      try {
        await earlier.focus();
        const saved = await anchor(page);
        await page.keyboard.press('Enter');
        await held.requested;
        held.release();
        await expect(earlier).toHaveCount(0);
        await expect(page.getByLabel('Conversation history', { exact: true })).toBeFocused();
        await expectAnchor(page, saved);
        const reply = history.traffic.pages.slice(before).find((r) => r.status === 200);
        expect(reply, 'final page actually arrived').toBeDefined();
        const parsed = (await import('../../src/lib/api/generated/schemas')).SessionHistoryResponse.parse(reply?.body);
        expect(parsed.entries.map((entry) => entry.id)).toEqual(Array.from({ length: predecessors }, (_, n) => fixtureId('terminal', n)));
        expect([parsed.has_earlier, parsed.next_before]).toEqual([false, undefined]);
      } finally { held.release(); await held.dispose(); }
    }
    const terminalCount = history.traffic.pages.length;
    await scrollToDistance(page, 0);
    await settled(page);
    expect(history.traffic.pages.length, 'true terminal state stops repeated empty requests').toBe(terminalCount);
    await history.record(`terminal-predecessors-${predecessors}`);
  });
}

// FE185 regression: real React, virtualizer, ResizeObserver and wheel input.
// D5/D8 require continuous reading, not merely a successful initial render.
// history.origin is the embedded SPA, or this checkout's dev build when
// OMNIPUS_HISTORY_DEV=1 (fixtures/chat-history-paging/dev-origin.ts).
function watchReader(page: Page) {
  const errors: string[] = [];
  const older: string[] = [];
  page.on('pageerror', error => errors.push(error.stack ?? error.message));
  page.on('console', message => { if (message.type() === 'error') errors.push(message.text()); });
  page.on('request', request => {
    const url = new URL(request.url());
    if (url.pathname.endsWith('/messages') && url.searchParams.has('before')) older.push(request.url());
  });
  return { errors, older };
}
/** Wheel/smooth-scroll movement has finished: scrollTop unchanged over 5 layout turns. */
async function scrollIdle(page: Page) {
  let previous = Number.NaN;
  for (let stable = 0; stable < 5;) {
    await settled(page);
    const top = await historyScroller(page).evaluate(element => element.scrollTop);
    stable = top === previous ? stable + 1 : 0;
    previous = top;
  }
}
const focusIdentity = (page: Page) => page.evaluate(() => {
  const element = document.activeElement;
  return element ? `${element.tagName}#${element.id}[${element.getAttribute('data-testid') ?? element.getAttribute('aria-label') ?? ''}]` : null;
});

test('FE185: slow real wheel reading stays mounted, prefetches and keeps the anchor and focus', async ({ history, page }, info) => {
  test.setTimeout(420_000); // Canonical fixture writes, gateway restart and (dev) Vite's cold compilation.
  const watch = watchReader(page);
  const samples: { top: number; height: number; H: number }[] = [];
  let held: Awaited<ReturnType<typeof holdPage>> | undefined;
  try {
    await history.seed({ label: 'wheel185', count: 240, shape: 'varied' });
    await history.authenticate();
    await openSessionByDeepLink(page, history.sessionId, history.origin);
    await expect.poll(() => history.traffic.frames.filter(frame => frame.type === 'catch_up_complete' && frame.session_id === history.sessionId).length).toBe(1);
    await settled(page);
    const initial = await geometry(page);
    expect(initial.height, 'positive control: real layout has scrollable older content').toBeGreaterThan(4 * initial.H);
    const baseline = watch.older.length;
    // Hold ONLY the delivery of the real older page so the reader can be
    // measured at rest before it commits (D8 settled anchor, not total height).
    held = await holdPage(page, url => url.searchParams.has('before'));
    const scroller = historyScroller(page);
    await scroller.hover();
    const focusBefore = await focusIdentity(page);
    // B25's normal-reading bound: 0.075H per >=100ms is <=0.75H/s.
    // Never assign scrollTop or dispatch a synthetic scroll event here.
    for (let tick = 0; tick < 240 && watch.older.length === baseline; tick++) {
      await page.mouse.wheel(0, -initial.H * 0.075);
      await page.waitForTimeout(100);
      expect(watch.errors, 'no render/update-depth errors during genuine reader movement').toEqual([]);
      await expect(scroller, 'the router error boundary must not replace Conversation history').toBeVisible();
      samples.push(await scroller.evaluate(element => ({ top: element.scrollTop, height: element.scrollHeight, H: element.clientHeight })));
    }
    expect(samples.some(sample => sample.top < initial.top), 'positive control: trusted wheel input actually moved the reader upward').toBe(true);
    expect(watch.older.length, 'normal reading must reach a real older-page request, not loop in layout').toBeGreaterThan(baseline);
    await held.requested;
    expect(samples.at(-1)!.top, 'D-G1: prefetch starts while older loaded content remains above the reader').toBeGreaterThan(0);
    await scrollIdle(page);
    const saved = await anchor(page);
    held.release();
    await expect.poll(() => history.traffic.pages.filter(reply => watch.older.slice(baseline).includes(reply.url) && reply.status === 200).length).toBeGreaterThan(0);
    await expect(scroller, 'busy state resets after the real older page commits').not.toHaveAttribute('aria-busy', 'true');
    await expectAnchor(page, saved);
    expect(await focusIdentity(page), 'D8: a passive prefetch never moves focus').toBe(focusBefore);
    await expect(scroller).toBeVisible();
    expect(watch.errors, 'the real older response also commits without a render error').toEqual([]);
    expect(history.traffic.failures, 'real frames remain contract-valid').toEqual([]);
    await history.record('wheel185-after-older', { initial, samples, older: watch.older, saved });
  } finally {
    held?.release();
    await held?.dispose();
    await info.attach('wheel185-diagnostics', { contentType: 'application/json', body: JSON.stringify({ frontend: process.env.OMNIPUS_HISTORY_DEV === '1' ? 'non-minified Vite checkout' : 'embedded gateway', gateway: history.gateway.baseURL, errors: watch.errors, older: watch.older, samples }, null, 2) });
    await info.attach('wheel185-final', { contentType: 'image/png', body: await page.screenshot() });
  }
});

test('FE185: fast real wheel flicks cross several older pages without a loop, jump to latest or unsettled anchor', async ({ history, page }, info) => {
  test.setTimeout(420_000);
  const watch = watchReader(page);
  const samples: { top: number; height: number }[] = [];
  try {
    await history.seed({ label: 'flick185', count: 600, shape: 'varied' });
    await history.authenticate();
    await openSessionByDeepLink(page, history.sessionId, history.origin);
    await expect.poll(() => history.traffic.frames.filter(frame => frame.type === 'catch_up_complete' && frame.session_id === history.sessionId).length).toBe(1);
    await settled(page);
    const initial = await geometry(page);
    const committed = () => history.traffic.pages.filter(reply => reply.status === 200 && new URL(reply.url).searchParams.has('before')).length;
    const scroller = historyScroller(page);
    await scroller.hover();
    // A fast trackpad/wheel flick: 1.5 viewports per 50ms (30 viewports/s).
    for (let tick = 0; tick < 400 && committed() < 3; tick++) {
      await page.mouse.wheel(0, -initial.H * 1.5);
      await page.waitForTimeout(50);
      expect(watch.errors, 'no render/update-depth errors during fast reader movement').toEqual([]);
      await expect(scroller, 'the router error boundary must not replace Conversation history').toBeVisible();
      samples.push(await scroller.evaluate(element => ({ top: element.scrollTop, height: element.scrollHeight })));
    }
    expect(committed(), 'fast reading keeps prefetching: at least three real older pages commit').toBeGreaterThanOrEqual(3);
    await scrollIdle(page);
    await expect(page.getByRole('button', { name: 'Return to latest', exact: true }), 'still reading history, never thrown back to latest').toBeVisible();
    const view = await geometry(page);
    expect(view.bottom, 'the reader stays far from the recent end after flicking upward').toBeGreaterThan(2 * view.H);
    // Settled means settled: with no input, no layout loop keeps moving rows.
    const saved = await anchor(page);
    await page.waitForTimeout(500);
    await expectAnchor(page, saved);
    expect(watch.errors).toEqual([]);
    expect(history.traffic.failures, 'real frames remain contract-valid').toEqual([]);
    await history.record('flick185-after-older', { initial, samples, older: watch.older, saved });
  } finally {
    await info.attach('flick185-diagnostics', { contentType: 'application/json', body: JSON.stringify({ frontend: process.env.OMNIPUS_HISTORY_DEV === '1' ? 'non-minified Vite checkout' : 'embedded gateway', errors: watch.errors, older: watch.older, samples }, null, 2) });
  }
});

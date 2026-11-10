import { expect } from '@playwright/test';
import { openSessionByDeepLink } from './fixtures/session-setup';
import { test, anchor, expectAnchor, fixtureId, geometry, historyRow, historyScroller, holdPage, settled } from './fixtures/chat-history-paging/harness';

test.use({ storageState: { cookies: [], origins: [] }, viewport: { width: 1280, height: 1000 } });
test.describe.configure({ retries: 0 }); // Deterministic fixture; never retry an assertion into green.

// Founder ruling (B34 / D8 T21 clarification): "Return to latest" is a floating
// arrow button at the bottom of the chat, above the composer. It appears as soon
// as the reader actually scrolls away from the live end, its appearance never
// moves the reader, and activating it returns to the newest messages without a
// jump. D8 screen-reader row: one polite loading status per request attempt.
// Real gateway, real React/virtualizer/ResizeObserver and trusted wheel input;
// only delivery of the real latest page is held.
const LOADING_LATEST = 'Loading latest messages…';

test('floating Return to latest: a small real scroll shows it without moving the reader; activation returns to the newest messages once', async ({ history, page }) => {
  test.setTimeout(300_000);
  await history.seed({ label: 'return', count: 240, shape: 'varied' });
  await history.authenticate();
  await openSessionByDeepLink(page, history.sessionId, history.origin);
  await expect.poll(() => history.traffic.frames.filter(frame => frame.type === 'catch_up_complete' && frame.session_id === history.sessionId).length).toBe(1);
  await expect.poll(async () => (await geometry(page)).bottom, { message: 'opened at the recent end' }).toBeLessThanOrEqual(1);
  const returnButton = page.getByRole('button', { name: 'Return to latest', exact: true });
  await expect(returnButton, 'following latest: no floating Return yet').toHaveCount(0);

  // Count every polite loading-status insertion for the latest read, so a
  // duplicate announcement cannot hide behind a single final DOM node.
  await page.evaluate((text) => {
    const seen = { count: 0 };
    (window as unknown as { __latestStatus: typeof seen }).__latestStatus = seen;
    const record = (node: Node) => {
      if (node instanceof Element) {
        for (const status of [node, ...node.querySelectorAll('[role="status"]')]) {
          if (status.getAttribute('role') === 'status' && status.textContent === text) seen.count += 1;
        }
      }
    };
    new MutationObserver(records => { for (const r of records) r.addedNodes.forEach(record); }).observe(document.body, { subtree: true, childList: true });
  }, LOADING_LATEST);

  // A SMALL genuine scroll away from the live end (well under one screen).
  const scroller = historyScroller(page);
  await scroller.hover();
  const start = await geometry(page);
  await page.mouse.wheel(0, -start.H * 0.25);
  await expect.poll(async () => (await geometry(page)).bottom, { message: 'positive control: trusted wheel input left the live end' }).toBeGreaterThan(1);
  await expect(returnButton, 'appears as soon as the reader actually scrolls away').toBeVisible();
  await settled(page);
  const reading = await anchor(page);
  const readingTop = (await geometry(page)).top;
  expect(readingTop, 'still near the end after a small scroll, not thrown elsewhere').toBeGreaterThan(start.top - start.H);
  // Its appearance and later layout passes never move the reader.
  await page.waitForTimeout(300);
  await expectAnchor(page, reading);
  const box = await returnButton.boundingBox();
  const area = await scroller.boundingBox();
  expect(box && area, 'button and conversation area have layout').toBeTruthy();
  expect(box!.y + box!.height, 'floats inside the visible chat area').toBeLessThanOrEqual(area!.y + area!.height + 1);
  expect(box!.y, 'at the bottom of the chat area, not at the top of history').toBeGreaterThan(area!.y + area!.height / 2);

  const latestHeld = await holdPage(page, url => !url.searchParams.has('before') && !url.searchParams.has('after'));
  try {
    await returnButton.click();
    await latestHeld.requested;
    await expect(page.getByRole('status').filter({ hasText: LOADING_LATEST }), 'one loading status while the latest read is pending').toHaveCount(1);
    await expectAnchor(page, reading); // Pending: the old reader stays put.
    // Repeated activation while pending joins the same attempt. The pending
    // control is aria-disabled (D8), which Playwright's click() treats as
    // disabled, so repeat it as a real keyboard press on the focused button.
    await expect(returnButton, 'focus stays on the triggering control while pending').toBeFocused();
    await page.keyboard.press('Enter');
    await settled(page);
    expect(latestHeld.requests, 'repeated activation starts no second latest read').toHaveLength(1);
    await expect(page.getByRole('status').filter({ hasText: LOADING_LATEST })).toHaveCount(1);
    latestHeld.release();
    await expect(returnButton, 'back at the live end: following resumes').toHaveCount(0);
    await expect.poll(async () => (await geometry(page)).bottom, { message: 'settled at the newest messages' }).toBeLessThanOrEqual(1);
    await expect(historyRow(page, fixtureId('return', 239)), 'the newest saved message is on screen').toBeVisible();
  } finally { latestHeld.release(); await latestHeld.dispose(); }

  // No jump after landing: with no input the view stays at the live end.
  const landed = await geometry(page);
  await page.waitForTimeout(500);
  await settled(page);
  const after = await geometry(page);
  expect(after.bottom, 'no later jump away from the newest messages').toBeLessThanOrEqual(1);
  expect(Math.abs(after.top - landed.top), 'no settling movement after landing').toBeLessThanOrEqual(1);
  expect(await page.evaluate(() => (window as unknown as { __latestStatus: { count: number } }).__latestStatus.count),
    'exactly one loading announcement for one latest read attempt').toBe(1);
  expect(history.traffic.failures, 'real frames remain contract-valid').toEqual([]);
  await history.record('floating-return-to-latest', { start, reading, landed, after });
});

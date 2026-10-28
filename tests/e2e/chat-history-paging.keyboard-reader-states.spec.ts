import { expect } from '@playwright/test';
import { chatInput } from './fixtures/selectors';
import { test, anchor, expectAnchor, geometry, historyScroller, holdPage, scrollToDistance, settled } from './fixtures/chat-history-paging/harness';

test.use({ storageState: { cookies: [], origins: [] }, viewport: { width: 1280, height: 1000 } });
test.describe.configure({ retries: 0 }); // No paid-model timing variance in these scripted fixtures.

// T21: D8 is the focus/announcement oracle, not current implementation output.
for (const key of ['Enter', 'Space'] as const) {
  test(`T21 B28: ${key} activates Show earlier once; focused pending control survives failure and Retry`, async ({ history, page }) => {
    await history.seed({ label: 'keyboard', count: 40, clearAt: [19] });
    await history.authenticate();
    await history.open();
    await scrollToDistance(page, 0);
    const earlier = page.getByRole('button', { name: 'Show earlier messages', exact: true });
    const held = await holdPage(page, (url) => url.searchParams.has('before'));
    try {
      await earlier.focus();
      const saved = await anchor(page);
      await page.keyboard.press(key);
      await held.requested;
      await page.keyboard.press(key);
      await expect(earlier, 'aria-disabled must not native-disable/remove the focused trigger').toBeFocused();
      await expect(earlier).toHaveAttribute('aria-disabled', 'true');
      expect(held.requests).toHaveLength(1);
      held.release(500);
      const alert = page.getByRole('alert').filter({ hasText: 'Could not load earlier messages.' });
      await expect(alert).toBeVisible();
      await expectAnchor(page, saved);
      await held.dispose();
      const retry = alert.getByRole('button', { name: 'Retry', exact: true });
      const final = await holdPage(page, (url) => url.searchParams.has('before'));
      try {
        await retry.focus();
        await page.keyboard.press(key);
        await final.requested;
        await expect(retry).toBeFocused();
        expect(final.requests).toHaveLength(1);
        expect(new URL(final.requests[0]).searchParams.get('before')).toBe(new URL(held.requests[0]).searchParams.get('before'));
        final.release();
        await expect(alert).toHaveCount(0);
        await expect(page.getByRole('button', { name: 'Show earlier messages', exact: true })).toHaveCount(0);
        await expect(page.getByLabel('Conversation history', { exact: true }), 'D8 terminal removed-control fallback').toBeFocused();
        await expectAnchor(page, saved);
        expect(history.traffic.sends, 'reading and Retry do not resend /clear or user input').toEqual([]);
      } finally { final.release(); await final.dispose(); }
    } finally { held.release(); await held.dispose(); }
    await history.record(`keyboard-${key}`);
  });
}

test('T21 B34: Return-to-latest failure keeps the old reader, focused action and draft; Retry fetches latest without resending', async ({ history, page }) => {
  await history.seed({ label: 'latest', count: 360, shape: 'varied' });
  await history.authenticate();
  await history.open();
  await scrollToDistance(page, (await geometry(page)).H);
  const latest = page.getByRole('button', { name: 'Return to latest', exact: true });
  await expect(latest).toBeVisible();
  const draft = 'UNSENT: keep this text through a latest-read failure';
  await chatInput(page).fill(draft);
  const saved = await anchor(page);
  const held = await holdPage(page, (url) => !['before', 'after', 'window_start'].some((key) => url.searchParams.has(key)));
  try {
    await latest.focus();
    await page.keyboard.press('Enter');
    await held.requested;
    await expect(latest).toBeFocused();
    await expect(page.getByText('Loading latest messages…', { exact: true })).toBeVisible();
    held.release(500);
    const alert = page.getByRole('alert').filter({ hasText: 'Could not load latest messages.' });
    await expect(alert).toBeVisible();
    await expectAnchor(page, saved);
    await expect(chatInput(page)).toHaveValue(draft);
    await held.dispose();
    const again = await holdPage(page, (url) => !['before', 'after', 'window_start'].some((key) => url.searchParams.has(key)));
    try {
      await alert.getByRole('button', { name: 'Retry', exact: true }).focus();
      await page.keyboard.press('Space');
      await again.requested;
      expect(again.requests).toHaveLength(1);
      again.release();
      await expect(alert).toHaveCount(0);
      await expect.poll(async () => (await geometry(page)).bottom).toBeLessThanOrEqual(1);
      await expect(chatInput(page)).toHaveValue(draft);
      expect(history.traffic.sends).toEqual([]);
      await expect(page.getByLabel('Conversation history', { exact: true })).toBeFocused();
    } finally { again.release(); await again.dispose(); }
  } finally { held.release(); await held.dispose(); }
  await history.record('latest-retry-draft');
});

for (const presentation of ['reduced-motion-reflow', 'plain-no-observer'] as const) {
  test(`T21 B29: ${presentation} announces once per request, not historical New response, and clears busy`, async ({ history, page }) => {
    await page.emulateMedia({ reducedMotion: 'reduce' });
    if (presentation === 'plain-no-observer') {
      // Platform capability boundary only; actual plain list/store/styles remain real.
      await page.addInitScript(() => { Object.defineProperty(window, 'ResizeObserver', { value: undefined }); });
    } else {
      // A640px CSS viewport is the reflow surface of1280px at200% zoom.
      // This tests reflow; it does not pretend deviceScaleFactor changes CSS zoom.
      await page.setViewportSize({ width: 640, height: 500 });
    }
    // D5/D8: 300 post-clear groups leave older content for BOTH requests.
    // Varied real paragraphs are chosen to cover the 2H goal in one page;
    // a tiny single-line page could require a legitimate delayed top-up that
    // the old fixture accidentally charged to the later keyboard attempt.
    await history.seed({ label: 'a11y', count: 320, clearAt: [19], shape: 'varied' });
    await history.authenticate();
    await history.open();
    await page.evaluate(() => {
      const events: string[] = [];
      Object.assign(window, { __historyAnnouncements: events });
      new MutationObserver((mutations) => {
        const live = new Set<Element>();
        for (const mutation of mutations) {
          const node = mutation.target instanceof Element ? mutation.target : mutation.target.parentElement;
          const owner = node?.closest('[role="status"], [role="alert"], [aria-live="polite"], [aria-live="assertive"]');
          if (owner) live.add(owner);
          for (const child of mutation.addedNodes) if (child instanceof Element) {
            if (child.matches('[role="status"], [role="alert"], [aria-live]')) live.add(child);
            child.querySelectorAll('[role="status"], [role="alert"], [aria-live]').forEach((el) => live.add(el));
          }
        }
        for (const owner of live) if (owner.getAttribute('aria-hidden') !== 'true' && owner.textContent?.trim()) events.push(owner.textContent.trim());
      }).observe(document.body, { subtree: true, childList: true, characterData: true });
    });
    // D8 / architect Q1: prefetch is its own request transition, not an
    // announcement to discard or charge to the later keyboard attempt.
    const prefetch = await holdPage(page, (url) => url.searchParams.has('before'));
    const beforePrefetch = await geometry(page);
    let prefetchAnnouncements: string[];
    try {
      await scrollToDistance(page, 0);
      const prefetchRoute = await prefetch.requested;
      expect(prefetch.requests, 'one real automatic prefetch before keyboard activation').toHaveLength(1);
      await expect(page.getByRole('status').filter({ hasText: 'Loading earlier messages…' })).toHaveCount(1);
      await expect(historyScroller(page)).toHaveAttribute('aria-busy', 'true');
      prefetch.release();
      const prefetchResponse = await prefetchRoute.request().response();
      expect(prefetchResponse, 'prefetch must actually finish delivery before Enter, not remain held or aborted').not.toBeNull();
      if (!prefetchResponse) throw new Error('PREREQUISITE: automatic prefetch was aborted before a distinct keyboard attempt');
      expect(prefetchResponse.status(), 'prefetch settlement is a real successful page, not a hidden transport failure').toBe(200);
      expect(await prefetchResponse.finished(), 'the prefetch body finishes before keyboard activation').toBeNull();
      expect(prefetch.abortedOrHandled, 'an interrupted app transition cannot masquerade as a settled prefetch').toBe(false);
      await expect(page.getByText('Loading earlier messages…', { exact: true })).toHaveCount(0);
      await expect(historyScroller(page)).not.toHaveAttribute('aria-busy', 'true');
      await settled(page);
      const afterPrefetch = await geometry(page);
      expect(afterPrefetch.height - beforePrefetch.height,
        'fixture must actually add at least 2H before testing a distinct keyboard attempt').toBeGreaterThanOrEqual(2 * afterPrefetch.H);
      prefetchAnnouncements = await page.evaluate(() => (window as unknown as { __historyAnnouncements: string[] }).__historyAnnouncements);
      expect(prefetchAnnouncements.filter((value) => value === 'Loading earlier messages…'), 'exactly one polite announcement for the settled automatic prefetch').toHaveLength(1);
      expect(prefetch.requests, 'prefetch settled before a distinct keyboard request').toHaveLength(1);
    } finally { prefetch.release(); await prefetch.dispose(); }
    const held = await holdPage(page, (url) => url.searchParams.has('before'));
    try {
      const earlier = page.getByRole('button', { name: 'Show earlier messages', exact: true });
      // Native focus scrolling to this offscreen boundary would itself start
      // ANOTHER automatic prefetch before Enter. Focus without navigation so
      // this phase really is the separate keyboard action named by the test.
      await earlier.evaluate(element => (element as HTMLElement).focus({ preventScroll: true }));
      await expect(earlier, 'keyboard input targets the actual focused recovery control').toBeFocused();
      await settled(page);
      await expect(earlier, 'the distinct keyboard attempt starts only after the prefetch guard clears').not.toHaveAttribute('aria-disabled', 'true');
      await expect(historyScroller(page), 'no automatic request remains in flight before Enter').not.toHaveAttribute('aria-busy', 'true');
      expect(held.requests, 'keyboard attempt has not started before its Enter action').toHaveLength(0);
      await page.keyboard.press('Enter');
      await held.requested;
      await expect(page.getByRole('status').filter({ hasText: 'Loading earlier messages…' })).toHaveCount(1);
      await expect(historyScroller(page)).toHaveAttribute('aria-busy', 'true');
      const before = await anchor(page);
      const pendingAnnouncements = await page.evaluate(() => (window as unknown as { __historyAnnouncements: string[] }).__historyAnnouncements);
      expect(pendingAnnouncements.slice(prefetchAnnouncements.length).filter((value) => value === 'Loading earlier messages…'), 'one announcement for the distinct keyboard attempt while pending').toHaveLength(1);
      await page.keyboard.press('Enter'); // Joining the held attempt is not a new request transition.
      await settled(page);
      expect(held.requests, 'repeated Enter joins/guards the same in-flight keyboard request').toHaveLength(1);
      const joinedAnnouncements = await page.evaluate(() => (window as unknown as { __historyAnnouncements: string[] }).__historyAnnouncements);
      expect(joinedAnnouncements.filter((value) => value === 'Loading earlier messages…'), 'joined in-flight trigger adds no loading announcement').toEqual(pendingAnnouncements.filter((value) => value === 'Loading earlier messages…'));
      held.release();
      await expect(page.getByText('Loading earlier messages…', { exact: true })).toHaveCount(0);
      await expect(historyScroller(page)).not.toHaveAttribute('aria-busy', 'true');
      await expectAnchor(page, before);
      const announcements = await page.evaluate(() => (window as unknown as { __historyAnnouncements: string[] }).__historyAnnouncements);
      expect(announcements.slice(0, prefetchAnnouncements.length), 'the fully asserted prefetch events remain in the append-only announcement ledger').toEqual(prefetchAnnouncements);
      expect(announcements.slice(prefetchAnnouncements.length).filter((value) => value === 'Loading earlier messages…'), 'one polite transition, not Button pending plus duplicate live region').toHaveLength(1);
      expect(held.requests, 'keyboard settlement did not create a second in-flight request').toHaveLength(1);
      expect(announcements.filter((value) => value.includes('New response')), 'historical hydration is not a new assistant answer').toEqual([]);
      const smooth = await historyScroller(page).evaluate((el) => getComputedStyle(el).scrollBehavior);
      expect(smooth, 'reduced-motion anchor correction is not smooth').not.toBe('smooth');
      await settled(page);
    } finally { held.release(); await held.dispose(); }
    await history.record(presentation);
  });
}

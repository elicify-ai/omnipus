import { expect } from '@playwright/test';
import { chatInput, waitForConnected } from './fixtures/selectors';
import { test } from './fixtures/chat-history-paging/harness';

test.use({ storageState: { cookies: [], origins: [] }, viewport: { width: 1280, height: 1000 } });
test.describe.configure({ retries: 0 }); // Deterministic fixture; never retry an assertion into green.

// D8 / T24 B26 (reader, focus and draft retention): a reconnect replay
// temporarily disables the composer. A real browser drops focus to <body> the
// moment a focused control becomes disabled and never gives it back, so the
// person who was typing loses the composer. This must run in a real browser:
// jsdom keeps focus on a disabled element and cannot show the failure.
// Real gateway restart (boot mismatch -> snapshot replay); no injected frames.
test('composer focus and draft survive the reconnect replay that temporarily disables it', async ({ history, page }) => {
  test.setTimeout(300_000);
  await history.seed({ label: 'focus', count: 40 });
  await history.authenticate();
  await history.open();
  const composer = chatInput(page);
  await composer.fill('DRAFT_KEPT_THROUGH_REPLAY');
  await composer.focus();
  await expect(composer, 'positive control: the person is typing in the composer').toBeFocused();
  // Record every disabled transition of the real composer, so the test proves
  // the replay actually disabled it rather than passing because nothing happened.
  await page.evaluate(() => {
    const seen: boolean[] = [];
    (window as unknown as { __composerDisabled: boolean[] }).__composerDisabled = seen;
    const input = document.querySelector<HTMLTextAreaElement>('[data-testid="chat-input"]')!;
    new MutationObserver(() => seen.push(input.disabled)).observe(input, { attributes: true, attributeFilter: ['disabled'] });
  });
  const before = history.traffic.frames.length;
  await history.gateway.kill9();
  await history.gateway.restart({ relogin: false }); // Keep this browser's session cookie.
  await waitForConnected(page);
  await expect.poll(() => history.traffic.frames.slice(before).filter(f => f.type === 'catch_up_complete' && f.session_id === history.sessionId).length).toBe(1);
  await expect.poll(() => (page.evaluate(() => (window as unknown as { __composerDisabled: boolean[] }).__composerDisabled)),
    { message: 'positive control: the replay really disabled and then re-enabled the composer' }).toEqual(expect.arrayContaining([true, false]));
  await expect(composer, 'the composer is usable again after the replay').toBeEnabled();
  await expect(composer, 'focus returns to the composer the person was typing in').toBeFocused();
  await expect(composer, 'the unsent draft is kept').toHaveValue('DRAFT_KEPT_THROUGH_REPLAY');
  expect(history.traffic.sends, 'reconnecting never sends the draft').toEqual([]);
});

import fs from 'node:fs';
import { fileURLToPath } from 'node:url';
import { expect, test } from '@playwright/test';
import { installPixels, instrumentRoutes, stateIs, type InputState } from './input-connection-probe';

// Harness-only tests: native page events and canvas→video decode are real;
// no gateway, encoder or remote transport claim is made by this suite.
const html = fs.readFileSync(fileURLToPath(new URL('./input-connection-fixture.html', import.meta.url)), 'utf8');
const initial: InputState = { nonce: 12345, clicks: 0, downs: 0, ups: 0, held: 0, scroll: 0, drags: 0, errors: 0, text: '' };
test.beforeEach(async ({ page }) => {
  await instrumentRoutes(page);
  await page.route('http://fixture.test/**', route => route.fulfill({ contentType: 'text/html', body: html }));
  await page.goto('http://fixture.test/?nonce=12345');
  await page.evaluate(async () => {
    const canvas = document.querySelector('canvas')!;
    const video = document.createElement('video');
    video.dataset.testid = 'browser-live-video'; video.muted = true;
    video.style.cssText = 'position:fixed;top:0;left:0;width:320px;height:320px;pointer-events:none';
    const stream = canvas.captureStream(0);
    video.srcObject = stream; document.body.append(video);
    const track = stream.getVideoTracks()[0] as CanvasCaptureMediaStreamTrack;
    setInterval(() => { canvas.getContext('2d')!.drawImage(canvas, 0, 0); track.requestFrame(); }, 33); // Local source pump; target state is unchanged.
    const playing = video.play();
    window.dispatchEvent(new Event('resize')); // Produce a frame after captureStream subscribes.
    await playing;
  });
  await installPixels(page); await stateIs(page, initial);
});
test('fixture preserves full Unicode text, click, native scrolling and held-key state', async ({ page }) => {
  await page.getByRole('button', { name: 'Count click' }).click();
  await page.getByRole('textbox').fill('Zażółć 世界');
  const expected = { ...initial, clicks: 1, downs: 1, ups: 1, text: 'Zażółć 世界' };
  await stateIs(page, expected);
  await page.locator('#scroll').hover(); await page.mouse.wheel(0, 300);
  await stateIs(page, { ...expected, scroll: 300 });
  await page.keyboard.down('ArrowLeft'); await stateIs(page, { ...expected, scroll: 300, held: 2 });
  await page.keyboard.up('ArrowLeft'); await stateIs(page, { ...expected, scroll: 300 });
});
for (const fault of ['text', 'extra click', 'scroll'] as const) {
  test(`pixel oracle rejects ${fault} mismatch`, async ({ page }) => {
    if (fault === 'text') await page.getByRole('textbox').fill('wrong');
    if (fault === 'extra click') await page.getByRole('button', { name: 'Count click' }).click();
    if (fault === 'scroll') { await page.locator('#scroll').hover(); await page.mouse.wheel(0, 1); }
    const changed = fault === 'text' ? { ...initial, text: 'wrong' }
      : fault === 'extra click' ? { ...initial, clicks: 1, downs: 1, ups: 1 } : { ...initial, scroll: 1 };
    await stateIs(page, changed); // Wait for the changed video frame before testing rejection.
    await expect(stateIs(page, initial)).rejects.toThrow();
  });
}

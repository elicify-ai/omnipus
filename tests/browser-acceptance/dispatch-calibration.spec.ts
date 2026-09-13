import fs from 'node:fs';
import { fileURLToPath } from 'node:url';
import { expect, test } from '@playwright/test';
import { browserLivePanel, browserLiveVideo, selectAgent } from '../e2e/fixtures/selectors';
import { instrumentRoutes, routeEvidence } from './input-connection-probe';

const fixture = fs.readFileSync(fileURLToPath(new URL('./dispatch-calibration-fixture.html', import.meta.url)), 'utf8');
const target = new URL(JSON.parse(fs.readFileSync(process.env.BROWSER_INPUT_FIXTURE_CONFIG!, 'utf8')).url);
if (target.origin !== 'https://uat-omnipus.fly.dev' || !target.pathname.startsWith('/preview/') || target.search || target.hash || target.username || target.password) throw Error('Exact approved preview URL required');
const provenance = JSON.parse(fs.readFileSync(process.env.BROWSER_INPUT_PROVENANCE!, 'utf8'));

test('isolated wheel with intentional three-second page work then without page work', async ({ page }, info) => {
  const observations: unknown[] = [], errors: string[] = [];
  const nonce = 47321;
  page.on('pageerror', error => errors.push(error.message));
  await instrumentRoutes(page);
  const read = () => browserLiveVideo(page).evaluate(node => {
    const video = node as HTMLVideoElement, canvas = document.createElement('canvas');
    canvas.width = video.videoWidth; canvas.height = video.videoHeight;
    const ctx = canvas.getContext('2d')!; ctx.drawImage(video, 0, 0);
    const pixel = (x: number, y: number) => ctx.getImageData(Math.floor(x * canvas.width), Math.floor(y * canvas.height), 1, 1).data;
    const border = pixel(.05, .05);
    const words = Array.from({ length: 4 }, (_, row) => {
      let value = 0;
      for (let bit = 0; bit < 32; bit++) if (pixel(.1 + .8 * (bit + .5) / 32, .21 + row * .18)[0] > 128) value += 2 ** bit;
      return value;
    });
    return { atMs: performance.now(), signature: border[0] > 200 && border[1] < 50 && border[2] > 200, nonce: words[0], count: words[1], blockedMs: words[2], errors: words[3] };
  });
  try {
    const served = await page.request.get(target.href, { maxRedirects: 0 });
    expect(served.status()).toBe(200); expect(await served.text()).toBe(fixture);
    await page.goto('/workspaces/01M01TTSDZBFGM28NPHGTFZ17T/chat'); await selectAgent(page, 'Browser UAT Test');
    await page.getByRole('button', { name: 'Open browser', exact: true }).click();
    await expect(browserLivePanel(page)).toBeVisible();
    // Navigation retires input ownership; wait for initial negotiation first.
    await expect(page.locator('[data-input-mode="dedicated"]')).toHaveAttribute('data-input-state', 'ready');
    const address = page.getByRole('textbox', { name: 'Address bar' });
    const navigation = new URL(target); navigation.searchParams.set('nonce', String(nonce));
    await address.fill(navigation.href); await address.press('Enter');
    await expect.poll(async () => { try { const s = await read(); return s.signature && s.nonce === nonce && s.count === 0; } catch { return false; } }, { timeout: 45000 }).toBe(true);
    let injectedWorkMs: number | undefined;
    for (const count of [1, 2]) {
      await expect(page.locator('[data-input-mode="dedicated"]')).toHaveAttribute('data-input-state', 'ready');
      await expect(browserLivePanel(page).getByRole('status').filter({ hasText: /Waiting for the current page|Pointer input is unavailable|Browser input is unavailable|Reconnecting video to restore browser input/ })).toHaveCount(0);
      const box = await browserLiveVideo(page).boundingBox(); if (!box) throw Error('Video has no geometry');
      await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
      // Drain the setup hover before the measured, isolated reliable gesture.
      await page.waitForTimeout(250);
      const before = await read(), routeStart = (await routeEvidence(page)).routes.length;
      observations.push({ phase: count, checkpoint: 'before', sample: before, wallTime: new Date().toISOString() });
      await page.mouse.wheel(0, 10);
      await expect.poll(async () => (await routeEvidence(page)).routes.slice(routeStart).filter(r => r.kind === 'wheel').length).toBe(1);
      await expect.poll(async () => { const s = await read(); return { signature: s.signature, nonce: s.nonce, count: s.count, errors: s.errors }; }, { timeout: 15000 }).toEqual({ signature: true, nonce, count, errors: 0 });
      const after = await read(), routes = (await routeEvidence(page)).routes.slice(routeStart);
      expect(routes.filter(r => r.kind !== 'mouse_move').map(({ route, kind }) => ({ route, kind }))).toEqual([{ route: 'input-reliable', kind: 'wheel' }]);
      expect(after.blockedMs).toBeGreaterThanOrEqual(3000);
      if (count === 1) injectedWorkMs = after.blockedMs;
      else expect(after.blockedMs).toBe(injectedWorkMs);
      observations.push({ phase: count, checkpoint: 'after', sample: after, routes, viewerFeedbackMs: after.atMs - routes.find(r => r.kind === 'wheel')!.at, inputState: await page.locator('[data-input-mode="dedicated"]').getAttribute('data-input-state'), alerts: await browserLivePanel(page).getByRole('alert').allTextContents() });
      // Recovery is explicit and recorded; the failed action is never replayed.
      if (count === 1 && await page.locator('[data-input-mode="dedicated"]').getAttribute('data-input-state') === 'failed') {
        observations.push({ checkpoint: 'explicit-retry-after-injected-work' });
        await page.getByRole('button', { name: 'Retry input', exact: true }).click();
      }
    }
    expect(errors).toEqual([]);
  } finally {
    const routing = await routeEvidence(page).catch(() => null), final = await read().catch(() => null);
    const panel = { address: await page.getByRole('textbox', { name: 'Address bar' }).inputValue().catch(() => null), inputState: await page.locator('[data-input-mode="dedicated"]').getAttribute('data-input-state').catch(() => null), statuses: await browserLivePanel(page).getByRole('status').allTextContents().catch(() => []), alerts: await browserLivePanel(page).getByRole('alert').allTextContents().catch(() => []) };
    await page.screenshot({ path: info.outputPath('calibration-before-cleanup.png') }).catch(() => {});
    fs.writeFileSync(info.outputPath('calibration-evidence.json'), JSON.stringify({ provenance, purpose: 'Injected target-page work; not a spontaneous-delay reproduction or latency gate. Correlate server cdp_start/budget/outcome to establish whether Chrome withheld its response.', observations, final, panel, errors, routing }, null, 2));
    await page.getByRole('button', { name: 'Close live browser panel', exact: true }).click({ timeout: 5000 }).catch(() => {});
  }
});

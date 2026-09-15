import fs from 'node:fs';
import { fileURLToPath } from 'node:url';
import { randomInt } from 'node:crypto';
import { expect, test, type Page } from '@playwright/test';
import { browserLiveFrame, selectAgent } from '../e2e/fixtures/selectors';
import { installPixels, instrumentRoutes, point, routeEvidence, type InputState } from './input-connection-probe';

const fixture = fs.readFileSync(fileURLToPath(new URL('./viewport-recovery-fixture.html', import.meta.url)), 'utf8');
const target = new URL(JSON.parse(fs.readFileSync(process.env.BROWSER_INPUT_FIXTURE_CONFIG!, 'utf8')).url);
if (target.origin !== 'https://uat-omnipus.fly.dev' || !target.pathname.startsWith('/preview/') || target.search || target.hash || target.username || target.password) throw Error('Exact approved fixture URL required');
const provenance = JSON.parse(fs.readFileSync(process.env.BROWSER_INPUT_PROVENANCE!, 'utf8'));
if (provenance.remoteVerified !== true || typeof provenance.source !== 'string' || !/^[a-f0-9]{40}$/.test(provenance.source)) throw Error('Verified committed runtime provenance required');
async function sample(page: Page) {
  const s = await page.evaluate(() => (window as unknown as { __inputSmoke?: { sample(): { state: InputState } | null } }).__inputSmoke?.sample()?.state);
  return s && { nonce: s.nonce, clicks: s.clicks, armed: s.downs, started: s.ups, width: s.held, completed: s.scroll, height: s.drags, errors: s.errors, text: s.text };
}

test('one eleven-second renderer resize stall recovers after exactly one Retry', async ({ page }, info) => {
  const nonce = randomInt(1, 65536), errors: string[] = [], observations: unknown[] = [];
  page.on('pageerror', error => errors.push(error.message));
  await instrumentRoutes(page);
  const viewer = page.locator('[data-input-mode="dedicated"]');
  const ready = async (timeout = 15000) => {
    await expect(viewer).toHaveAttribute('data-input-state', 'ready', { timeout });
    await expect(viewer.getByRole('alert')).toHaveCount(0);
    await expect(viewer.getByRole('status').filter({ hasText: /Waiting for the current page|Pointer input is unavailable|Browser input is unavailable|Reconnecting video to restore browser input/ })).toHaveCount(0);
  };
  const geometry = async () => {
    await expect.poll(async () => {
      const s = await sample(page), box = await browserLiveFrame(page).boundingBox();
      return !!s && !!box && s.nonce === nonce && s.errors === 0 && Math.abs(s.width - box.width) <= 1 && Math.abs(s.height - box.height) <= 1;
    }, { timeout: 15000 }).toBe(true);
    await ready();
  };
  try {
    const served = await page.request.get(target.href, { maxRedirects: 0 });
    expect(served.status()).toBe(200); expect(await served.text()).toBe(fixture);
    await page.goto('/workspaces/01M01TTSDZBFGM28NPHGTFZ17T/chat'); await selectAgent(page, 'Browser UAT Test');
    const startupAt = Date.now();
    await page.getByRole('button', { name: 'Open browser', exact: true }).click();
    // Initial media negotiation has its own existing 45-second product budget.
    // Keep subsequent input/resize recovery checks at their tighter deadlines.
    await ready(45000);
    observations.push({ checkpoint: 'initial-ready', at: new Date().toISOString(), startupMs: Date.now() - startupAt });
    const url = new URL(target); url.searchParams.set('nonce', String(nonce)); url.searchParams.set('duration', '11000');
    const address = page.getByRole('textbox', { name: 'Address bar' }); await address.fill(url.href); await address.press('Enter');
    await expect.poll(() => page.evaluate(() => {
      const video = document.querySelector<HTMLVideoElement>('[data-testid="browser-live-video"]');
      return !!video && video.srcObject instanceof MediaStream && video.srcObject.getVideoTracks().some(track => track.readyState === 'live') && video.readyState >= 2 && video.videoWidth > 0;
    })).toBe(true);
    await installPixels(page); await geometry();
    const initial = await sample(page);
    expect(initial).toMatchObject({ nonce, clicks: 0, armed: 0, started: 0, completed: 0, errors: 0, text: '' });
    // Retain the exhausted operation failure separately from subsequent recovery.
    // A successful Retry must not erase the evidence that the first resize failed.
    await page.evaluate(() => {
      const w = window as unknown as { __resizeFailures: string[]; __resizeObserver: MutationObserver };
      w.__resizeFailures = [];
      const record = () => {
        const viewer = document.querySelector('[data-input-mode="dedicated"]');
        const state = viewer?.getAttribute('data-input-state');
        const alerts = [...(viewer?.querySelectorAll('[role="alert"]') || [])].map(node => node.textContent || '');
        const text = viewer?.textContent || '';
        if (state === 'failed' || alerts.length || /Retry input|Retry browser|Resume input|deadline exceeded|input dispatch failed/i.test(text)) w.__resizeFailures.push(JSON.stringify({ state, alerts }));
      };
      w.__resizeObserver = new MutationObserver(record);
      w.__resizeObserver.observe(document.body, { subtree: true, childList: true, characterData: true, attributes: true, attributeFilter: ['data-input-state'] });
      record();
    });
    const arm = await point(page, .25, .68); await page.mouse.click(arm.x, arm.y);
    await expect.poll(async () => (await sample(page))?.armed, { timeout: 5000 }).toBe(1); await ready();
    observations.push({ checkpoint: 'armed', at: new Date().toISOString(), state: await sample(page) });
    const resizeStartedAt = Date.now();
    await page.setViewportSize({ width: 1700, height: 1100 });
    await expect(viewer).toHaveAttribute('data-input-state', 'failed', { timeout: 12000 });
    const retry = viewer.getByRole('button', { name: 'Retry input', exact: true });
    await expect(retry).toBeVisible();
    observations.push({ checkpoint: 'failed-before-retry', at: new Date().toISOString(), state: await sample(page), failures: await page.evaluate(() => (window as unknown as { __resizeFailures: string[] }).__resizeFailures) });
    // Wait only for the deliberately injected stall window, not for readiness:
    // the independently decoded completed counter below must still prove it ended.
    await page.waitForTimeout(Math.max(0, resizeStartedAt + 12000 - Date.now()));
    await retry.click();
    observations.push({ checkpoint: 'retry-clicked-once', at: new Date().toISOString() });
    await expect.poll(async () => { const s = await sample(page); return s && { nonce: s.nonce, started: s.started, completed: s.completed, errors: s.errors }; }, { timeout: 15000 }).toEqual({ nonce, started: 1, completed: 1, errors: 0 });
    await geometry();
    const resized = await sample(page);
    expect(resized?.width === initial?.width && resized?.height === initial?.height).toBe(false);
    observations.push({ checkpoint: 'recovered-after-retry', at: new Date().toISOString(), state: resized });
    const button = await point(page, .73, .68); await page.mouse.click(button.x, button.y); await page.keyboard.insertText('@é');
    await expect.poll(async () => { const s = await sample(page); return s && { nonce: s.nonce, clicks: s.clicks, text: s.text, errors: s.errors }; }, { timeout: 5000 }).toEqual({ nonce, clicks: 1, text: '@é', errors: 0 });
    await ready();
    expect((await page.evaluate(() => (window as unknown as { __resizeFailures: string[] }).__resizeFailures)).length).toBeGreaterThan(0);
    await expect(viewer.getByRole('button', { name: 'Retry input', exact: true })).toHaveCount(0);
    const routes = (await routeEvidence(page)).routes;
    expect(routes.filter(row => row.kind === 'mouse_down')).toHaveLength(2); expect(routes.filter(row => row.kind === 'mouse_up')).toHaveLength(2);
    expect(routes.filter(row => row.kind === 'text')).toHaveLength(1); expect(routes.filter(row => row.route === 'websocket')).toEqual([]);
    expect(routes.every(row => row.encoding === 'binary-v1')).toBe(true); expect(errors).toEqual([]);
  } finally {
    const failures = await page.evaluate(() => { const w = window as unknown as { __resizeFailures?: string[]; __resizeObserver?: MutationObserver }; w.__resizeObserver?.disconnect(); return w.__resizeFailures; }).catch(() => null);
    const output = info.outputPath('viewport-retry-evidence.json');
    fs.writeFileSync(output, JSON.stringify({ provenance, nonce, deliberateResizeStallMs: 11000, observations, failures, errors, final: await sample(page).catch(() => null), routes: await routeEvidence(page).catch(() => null) }, null, 2));
    await info.attach('viewport-retry-evidence', { path: output, contentType: 'application/json' });
    await page.getByRole('button', { name: 'Close live browser panel', exact: true }).click({ timeout: 5000 }).catch(() => {});
  }
});

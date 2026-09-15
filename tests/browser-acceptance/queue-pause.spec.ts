import fs from 'node:fs';
import { fileURLToPath } from 'node:url';
import { randomInt } from 'node:crypto';
import { expect, test } from '@playwright/test';
import { browserLiveFrame, browserLivePanel, browserLiveVideo, selectAgent } from '../e2e/fixtures/selectors';
import { installPixels, instrumentRoutes, point, routeEvidence, stateIs, type InputState } from './input-connection-probe';
const fixture = fs.readFileSync(fileURLToPath(new URL('./queue-pause-fixture.html', import.meta.url)), 'utf8');
const target = new URL(JSON.parse(fs.readFileSync(process.env.BROWSER_INPUT_FIXTURE_CONFIG!, 'utf8')).url);
if (target.origin !== 'https://uat-omnipus.fly.dev' || !target.pathname.startsWith('/preview/') || target.search || target.hash || target.username || target.password) throw Error('Exact approved fixture URL required');
const provenance = JSON.parse(fs.readFileSync(process.env.BROWSER_INPUT_PROVENANCE!, 'utf8'));
for (const automatic of [false, true]) test(`renderer queue expiry preserves peer and fresh input with ${automatic ? 'safe automatic' : 'explicit held-key'} recovery`, async ({ page }, info) => {
  const nonce = randomInt(1, 65535);
  let state: InputState = { nonce, clicks: 0, downs: 0, ups: 0, held: 0, scroll: 0, drags: 0, errors: 0, text: '' };
  const errors: string[] = [], marks: Array<{ label: string; at: string }> = [];
  page.on('pageerror', error => errors.push(error.message));
  await instrumentRoutes(page);
  const ready = async () => {
    await expect(page.locator('[data-input-mode="dedicated"]')).toHaveAttribute('data-input-state', 'ready');
    await expect(browserLivePanel(page).getByRole('status').filter({ hasText: /Waiting for the current page|Pointer input is unavailable|Browser input is unavailable|Reconnecting video to restore browser input/ })).toHaveCount(0);
    await expect(browserLivePanel(page).getByRole('alert')).toHaveCount(0);
  };
  try {
    const served = await page.request.get(target.href, { maxRedirects: 0 }); expect(served.status()).toBe(200); expect(await served.text()).toBe(fixture);
    await page.goto('/workspaces/01M01TTSDZBFGM28NPHGTFZ17T/chat'); await selectAgent(page, 'Browser UAT Test');
    await page.getByRole('button', { name: 'Open browser', exact: true }).click(); await ready();
    await expect.poll(() => browserLiveVideo(page).evaluate(node => (node as HTMLVideoElement).readyState), { timeout: 45000 }).toBeGreaterThanOrEqual(2);
    const url = new URL(target); url.searchParams.set('nonce', String(nonce));
    const address = page.getByRole('textbox', { name: 'Address bar' }); await address.fill(url.href); await address.press('Enter');
    await installPixels(page); await stateIs(page, state); await ready();
    const chat = await point(page, .25, .68); await page.mouse.click(chat.x, chat.y);
    await expect(browserLivePanel(page).getByRole('textbox', { name: 'Remote browser text input' })).toBeFocused();
    await ready();
    const before = await routeEvidence(page);
    expect(before.peers.filter(peer => peer.labels.includes('input-reliable'))).toHaveLength(1);
    await page.evaluate(() => {
      const create = RTCPeerConnection.prototype.createDataChannel;
      Object.assign(window, { __queuePauseNewChannels: 0 });
      RTCPeerConnection.prototype.createDataChannel = function (label, options) {
        if (label === 'input-reliable' || label === 'input-hover') (window as unknown as { __queuePauseNewChannels: number }).__queuePauseNewChannels++;
        return create.call(this, label, options);
      };
    });
    marks.push({ label: 'busy-key-start', at: new Date().toISOString() });
    await page.keyboard.down('b'); await page.waitForTimeout(250);
    await page.keyboard.down('a'); await page.keyboard.up('a');
    if (automatic) await page.keyboard.up('b');
    const resume = page.getByRole('button', { name: 'Resume input', exact: true });
    if (automatic) {
      await expect(page.getByText('Input resumed. Some recent actions were not sent; they were not replayed.', { exact: true })).toBeVisible();
      await ready();
      await expect(resume).toHaveCount(0);
    } else {
      await expect(page.locator('[data-input-mode="dedicated"]')).toHaveAttribute('data-input-state', 'paused');
      await expect(resume).toBeEnabled();
      await page.keyboard.up('b');
    }
    state = { ...state, downs: 1, ups: 1, held: 0 }; await stateIs(page, state);
    await page.waitForTimeout(400); await stateIs(page, state);
    const paused = await routeEvidence(page);
    expect(paused.sameMedia).toBe(true);
    expect(paused.peers.filter(peer => peer.labels.includes('input-reliable'))).toEqual(before.peers.filter(peer => peer.labels.includes('input-reliable')));
    expect(await page.evaluate(() => (window as unknown as { __queuePauseNewChannels: number }).__queuePauseNewChannels)).toBe(0);
    if (!automatic) await resume.click();
    await ready();
    await browserLiveFrame(page).focus();
    await page.keyboard.down('c'); state = { ...state, downs: 2, clicks: 1, held: 1 }; await stateIs(page, state);
    await page.keyboard.up('c'); state = { ...state, ups: 2, held: 0 }; await stateIs(page, state);
    const routes = (await routeEvidence(page)).routes;
    expect(routes.length).toBeGreaterThan(0);
    expect(routes.every(row => row.encoding === 'binary-v1')).toBe(true);
    expect(routes.filter(row => row.route === 'websocket')).toEqual([]);
    expect(await page.evaluate(() => (window as unknown as { __queuePauseNewChannels: number }).__queuePauseNewChannels)).toBe(0);
    expect(errors).toEqual([]);
  } finally {
    const final = await page.evaluate(() => (window as unknown as { __inputSmoke?: { sample(): { state: InputState } | null } }).__inputSmoke?.sample()?.state).catch(() => null);
    const route = await routeEvidence(page).catch(() => null);
    fs.writeFileSync(info.outputPath('queue-pause-evidence.json'), JSON.stringify({ provenance, automatic, marks, final, route, errors }, null, 2));
    await info.attach('queue-pause-evidence', { path: info.outputPath('queue-pause-evidence.json'), contentType: 'application/json' });
    await page.getByRole('button', { name: 'Close live browser panel', exact: true }).click({ timeout: 5000 }).catch(() => {});
  }
});

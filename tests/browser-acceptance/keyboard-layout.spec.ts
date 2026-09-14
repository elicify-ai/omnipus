import fs from 'node:fs';
import { fileURLToPath } from 'node:url';
import { randomInt } from 'node:crypto';
import { expect, test } from '@playwright/test';
import { browserLivePanel, browserLiveVideo, selectAgent } from '../e2e/fixtures/selectors';
import { installPixels, instrumentRoutes, point, routeEvidence, stateIs, type InputState } from './input-connection-probe';
const fixture = fs.readFileSync(fileURLToPath(new URL('./keyboard-layout-fixture.html', import.meta.url)), 'utf8');
const target = new URL(JSON.parse(fs.readFileSync(process.env.BROWSER_INPUT_FIXTURE_CONFIG!, 'utf8')).url);
if (target.origin !== 'https://uat-omnipus.fly.dev' || !target.pathname.startsWith('/preview/') || target.search || target.hash || target.username || target.password) throw Error('Exact approved fixture URL required');
const provenance = JSON.parse(fs.readFileSync(process.env.BROWSER_INPUT_PROVENANCE!, 'utf8'));
test('focused game chat preserves Space, A, arrows and logical German Mac at-sign', async ({ page }, info) => {
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
    // The remote textarea is focused, while game handlers listen on document.
    await page.keyboard.down('Space'); state = { ...state, clicks: 1, downs: 1, held: 1 }; await stateIs(page, state);
    await page.keyboard.up('Space'); state = { ...state, ups: 1, held: 0 }; await stateIs(page, state);
    await page.keyboard.down('a'); state = { ...state, downs: 2, held: 1, drags: 1, text: 'a' }; await stateIs(page, state);
    await page.keyboard.up('a'); state = { ...state, ups: 2, held: 0 }; await stateIs(page, state);
    await page.keyboard.down('ArrowRight'); state = { ...state, downs: 3, held: 1, scroll: 1 }; await stateIs(page, state);
    await page.keyboard.up('ArrowRight'); state = { ...state, ups: 3, held: 0 }; await stateIs(page, state);
    // Native viewer events model logical Option+L/@, not physical German hardware.
    await page.keyboard.down('Alt'); state = { ...state, downs: 4, held: 1 }; await stateIs(page, state);
    const cdp = await page.context().newCDPSession(page);
    try {
      await cdp.send('Input.dispatchKeyEvent', { type: 'keyDown', key: '@', code: 'KeyL', modifiers: 1, text: '@', unmodifiedText: 'l', windowsVirtualKeyCode: 76 });
      state = { ...state, downs: 5, held: 2, text: 'a@' }; await stateIs(page, state);
      await cdp.send('Input.dispatchKeyEvent', { type: 'keyUp', key: '@', code: 'KeyL', modifiers: 1, windowsVirtualKeyCode: 76 });
      state = { ...state, ups: 4, held: 1 }; await stateIs(page, state);
      await page.keyboard.up('Alt'); state = { ...state, ups: 5, held: 0 }; await stateIs(page, state);
    } finally { await cdp.detach(); }
    await ready(); marks.push({ label: 'exact-game-keys-and-layout-complete', at: new Date().toISOString() });
    const routes = (await routeEvidence(page)).routes;
    expect(routes.filter(row => row.route === 'websocket')).toEqual([]);
    expect(errors).toEqual([]);
  } finally {
    const final = await page.evaluate(() => (window as unknown as { __inputSmoke?: { sample(): { state: InputState } | null } }).__inputSmoke?.sample()?.state).catch(() => null);
    const route = await routeEvidence(page).catch(() => null);
    fs.writeFileSync(info.outputPath('keyboard-layout-evidence.json'), JSON.stringify({ provenance, marks, final, route, errors }, null, 2));
    await info.attach('keyboard-layout-evidence', { path: info.outputPath('keyboard-layout-evidence.json'), contentType: 'application/json' });
    await page.getByRole('button', { name: 'Close live browser panel', exact: true }).click({ timeout: 5000 }).catch(() => {});
  }
});

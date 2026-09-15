import fs from 'node:fs';
import { fileURLToPath } from 'node:url';
import { randomInt } from 'node:crypto';
import { expect, test } from '@playwright/test';
import { browserLiveFrame, browserLivePanel, selectAgent } from '../e2e/fixtures/selectors';
import { installPixels, instrumentRoutes, point, routeEvidence, stateIs, type InputState } from './input-connection-probe';
const fixture = fs.readFileSync(fileURLToPath(new URL('./slow-wheel-fixture.html', import.meta.url)), 'utf8');
const target = new URL(JSON.parse(fs.readFileSync(process.env.BROWSER_INPUT_FIXTURE_CONFIG!, 'utf8')).url);
if (target.origin !== 'https://uat-omnipus.fly.dev' || !target.pathname.startsWith('/preview/') || target.search || target.hash || target.username || target.password) throw Error('Exact approved fixture URL required');
const provenance = JSON.parse(fs.readFileSync(process.env.BROWSER_INPUT_PROVENANCE!, 'utf8'));
type WheelWindow = Window & { __slowWheel: Array<{ at: number; delta: number }>; __inputSmoke: { sample(): { state: InputState } | null } };

test('native wheel overload preserves 900 delta through 75ms target handler and ordered release', async ({ page }, info) => {
  const nonce = randomInt(1, 65536);
  let state: InputState = { nonce, clicks: 0, downs: 0, ups: 0, held: 0, scroll: 0, drags: 0, errors: 0, text: '' };
  const marks: unknown[] = [], errors: string[] = [], dispatchErrors: string[] = [];
  page.on('pageerror', error => errors.push(error.message));
  await instrumentRoutes(page);
  await page.addInitScript(() => {
    const sent: Array<{ at: number; delta: number }> = [];
    const native = RTCPeerConnection.prototype.createDataChannel;
    RTCPeerConnection.prototype.createDataChannel = function (label, options) {
      const channel = native.call(this, label, options), send = channel.send.bind(channel);
      if (label === 'input-reliable') channel.send = ((data: string) => { send(data); try { const f = JSON.parse(data); if (f.kind === 'wheel') sent.push({ at: performance.now(), delta: f.delta_y }); } catch { /* Binary channels are outside this probe. */ } }) as typeof channel.send;
      return channel;
    };
    (window as unknown as WheelWindow).__slowWheel = sent;
  });
  const ready = async () => {
    await expect(page.locator('[data-input-mode="dedicated"]')).toHaveAttribute('data-input-state', 'ready');
    await expect(browserLivePanel(page).getByRole('status').filter({ hasText: /Waiting for the current page|Pointer input is unavailable|Browser input is unavailable|Reconnecting video to restore browser input/ })).toHaveCount(0);
    await expect(browserLivePanel(page).getByRole('alert')).toHaveCount(0);
  };
  const cdp = await page.context().newCDPSession(page);
  try {
    const served = await page.request.get(target.href, { maxRedirects: 0 }); expect(served.status()).toBe(200); expect(await served.text()).toBe(fixture);
    await page.goto('/workspaces/01M01TTSDZBFGM28NPHGTFZ17T/chat'); await selectAgent(page, 'Browser UAT Test');
    await page.getByRole('button', { name: 'Open browser', exact: true }).click(); await ready();
    const url = new URL(target); url.searchParams.set('nonce', String(nonce));
    const address = page.getByRole('textbox', { name: 'Address bar' }); await address.fill(url.href); await address.press('Enter');
    await installPixels(page); await stateIs(page, state); await ready();
    await browserLiveFrame(page).focus();
    const wheel = await point(page, .25, .86); await page.mouse.move(wheel.x, wheel.y);
    const start = performance.now(); marks.push({ label: 'burst-start', at: new Date().toISOString() });
    const pending: Promise<unknown>[] = [];
    for (let i = 0; i < 60; i++) {
      const delay = start + i * 35 - performance.now(); if (delay > 0) await new Promise(resolve => setTimeout(resolve, delay));
      pending.push(cdp.send('Input.dispatchMouseEvent', { type: 'mouseWheel', x: wheel.x, y: wheel.y, deltaX: 0, deltaY: 15 }).catch(() => { dispatchErrors.push('viewer wheel dispatch rejected'); }));
    }
    await Promise.all(pending); marks.push({ label: 'burst-finished', at: new Date().toISOString() });
    // CDP acknowledgment can precede the viewer's final animation-frame flush.
    await expect.poll(() => page.evaluate(() => (window as unknown as WheelWindow).__slowWheel.reduce((sum, event) => sum + event.delta, 0)), { timeout: 2000 }).toBe(900);
    const sent = await page.evaluate(() => (window as unknown as WheelWindow).__slowWheel);
    const intervals = sent.slice(1).map((s, i) => s.at - sent[i].at).sort((a, b) => a - b);
    marks.push({ label: 'actual-wheel-cadence', sent, intervals });
    expect(dispatchErrors).toEqual([]); expect(sent.reduce((sum, s) => sum + s.delta, 0)).toBe(900);
    expect(sent.length).toBeGreaterThanOrEqual(40); expect(intervals[Math.floor(intervals.length / 2)]).toBeGreaterThanOrEqual(30); expect(intervals[Math.floor(intervals.length / 2)]).toBeLessThanOrEqual(60);
    state = { ...state, scroll: 900 }; await stateIs(page, state); await ready();
    await page.keyboard.down('ArrowLeft'); state = { ...state, held: 2 }; await stateIs(page, state);
    await page.keyboard.up('ArrowLeft'); state = { ...state, held: 0 }; await stateIs(page, state);
    const click = await point(page, .73, .68); await page.mouse.click(click.x, click.y); state = { ...state, clicks: 1, downs: 1, ups: 1 }; await stateIs(page, state);
    const text = await point(page, .25, .68); await page.mouse.click(text.x, text.y); await page.keyboard.type('x');
    state = { ...state, downs: 2, ups: 2, text: 'x' }; await stateIs(page, state);
    expect((await routeEvidence(page)).routes.filter(row => row.route === 'websocket')).toEqual([]); expect(errors).toEqual([]);
  } finally {
    const final = await page.evaluate(() => (window as unknown as WheelWindow).__inputSmoke?.sample()?.state).catch(() => null);
    const sent = await page.evaluate(() => (window as unknown as WheelWindow).__slowWheel).catch(() => null);
    const route = await routeEvidence(page).catch(() => null);
    const inputError = await page.getByTestId('browser-input-error').textContent({ timeout: 500 }).catch(() => null);
    fs.writeFileSync(info.outputPath('slow-wheel-evidence.json'), JSON.stringify({ provenance, marks, final, sent, route, errors, dispatchErrors, inputError }, null, 2));
    await info.attach('slow-wheel-evidence', { path: info.outputPath('slow-wheel-evidence.json'), contentType: 'application/json' });
    await cdp.detach();
    await page.getByRole('button', { name: 'Close live browser panel', exact: true }).click({ timeout: 5000 }).catch(() => {});
  }
});

import fs from 'node:fs';
import { fileURLToPath } from 'node:url';
import { randomInt } from 'node:crypto';
import { expect, test } from '@playwright/test';
import { browserLiveFrame, browserLivePanel, selectAgent } from '../e2e/fixtures/selectors';
import { installPixels, instrumentRoutes, point, routeEvidence, stateIs, type InputState } from './input-connection-probe';

// A comparative performance workload, not a claim that every baseline must fail.
// Expected counters derive from authored gestures, independently of dispatch logs.
const fixture = fs.readFileSync(fileURLToPath(new URL('./pressure-stress-fixture.html', import.meta.url)), 'utf8');
const target = new URL(JSON.parse(fs.readFileSync(process.env.BROWSER_INPUT_FIXTURE_CONFIG!, 'utf8')).url);
if (target.origin !== 'https://uat-omnipus.fly.dev' || !target.pathname.startsWith('/preview/') || target.search || target.hash || target.username || target.password) throw Error('Exact approved fixture URL required');
const provenance = JSON.parse(fs.readFileSync(process.env.BROWSER_INPUT_PROVENANCE!, 'utf8'));

for (const delay of [0, 120]) test(`mixed input stress with ${delay}ms deliberate key-handler work`, async ({ page }, info) => {
  let state: InputState = { nonce: randomInt(1, 65536), clicks: 0, downs: 0, ups: 0, held: 0, scroll: 0, drags: 0, errors: 0, text: '' };
  const errors: string[] = [], marks: Array<{ label: string; at: string; ms?: number }> = [];
  page.on('pageerror', error => errors.push(error.message));
  await instrumentRoutes(page);
  const ready = async () => {
    await expect(page.locator('[data-input-mode="dedicated"]')).toHaveAttribute('data-input-state', 'ready');
    await expect(browserLivePanel(page).getByRole('status').filter({ hasText: /Waiting for the current page|Pointer input is unavailable|Browser input is unavailable|Reconnecting video to restore browser input/ })).toHaveCount(0);
    await expect(browserLivePanel(page).getByRole('alert')).toHaveCount(0);
  };
  const observe = async (label: string, start: number) => {
    await stateIs(page, state); await ready();
    marks.push({ label, at: new Date().toISOString(), ms: performance.now() - start });
  };
  try {
    const served = await page.request.get(target.href, { maxRedirects: 0 });
    expect(served.status()).toBe(200); expect(await served.text()).toBe(fixture);
    await page.goto('/workspaces/01M01TTSDZBFGM28NPHGTFZ17T/chat'); await selectAgent(page, 'Browser UAT Test');
    await page.getByRole('button', { name: 'Open browser', exact: true }).click(); await ready();
    const url = new URL(target); url.searchParams.set('nonce', String(state.nonce)); url.searchParams.set('delay', String(delay));
    const address = page.getByRole('textbox', { name: 'Address bar' }); await address.fill(url.href); await address.press('Enter');
    await installPixels(page); await stateIs(page, state); await ready();
    marks.push({ label: 'workload-start', at: new Date().toISOString() });
    // Twelve rounds retain enough text for the exact 32-code-unit visual oracle.
    for (let round = 0; round < 12; round++) {
      await browserLiveFrame(page).focus();
      await expect(browserLivePanel(page).getByRole('textbox', { name: 'Remote browser text input' })).toBeFocused();
      await ready();
      let start = performance.now();
      // Preserve every repeat: eight presses of one held physical key, one release.
      for (let i = 0; i < 8; i++) { await page.keyboard.down('ArrowLeft'); await page.waitForTimeout(180); }
      await page.keyboard.up('ArrowLeft');
      state = { ...state, downs: state.downs + 8, ups: state.ups + 1 };
      await observe(`keys-${round}`, start);
      const wheel = await point(page, .25, .86); await page.mouse.move(wheel.x, wheel.y);
      start = performance.now();
      for (let i = 0; i < 30; i++) { await page.mouse.wheel(0, 10); await page.waitForTimeout(20); }
      state = { ...state, scroll: state.scroll + 300 }; await observe(`wheel-${round}`, start);
      const left = await point(page, .1, .59), right = await point(page, .9, .59);
      for (let i = 0; i < 60; i++) await page.mouse.move(left.x + (right.x - left.x) * i / 59, left.y);
      const button = await point(page, .73, .68); start = performance.now(); await page.mouse.click(button.x, button.y);
      state = { ...state, clicks: state.clicks + 1, downs: state.downs + 1, ups: state.ups + 1 }; await observe(`click-${round}`, start);
      const text = await point(page, .25, .68); await page.mouse.click(text.x, text.y); start = performance.now();
      await page.keyboard.insertText('@é');
      state = { ...state, text: state.text + '@é', downs: state.downs + 1, ups: state.ups + 1 }; await observe(`text-${round}`, start);
      const a = await point(page, .6, .86), b = await point(page, .87, .86);
      await page.mouse.move(a.x, a.y); await page.mouse.down(); await page.mouse.move(b.x, b.y, { steps: 12 }); await page.mouse.up();
      state = { ...state, drags: state.drags + 1, downs: state.downs + 1, ups: state.ups + 1 }; await stateIs(page, state); await ready();
    }
    marks.push({ label: 'workload-end', at: new Date().toISOString() });
    const routes = (await routeEvidence(page)).routes;
    expect(routes.filter(row => row.kind === 'key_down')).toHaveLength(12 * 8);
    expect(routes.filter(row => row.kind === 'key_up')).toHaveLength(12);
    expect(routes.filter(row => row.kind === 'text')).toHaveLength(12);
    expect(routes.some(row => row.kind === 'mouse_move' && row.route === 'input-hover')).toBe(true);
    expect(routes.filter(row => row.route === 'websocket')).toEqual([]);
    expect(routes.every(row => row.encoding === 'binary-v1')).toBe(true);
    expect(errors).toEqual([]);
  } finally {
    const final = await page.evaluate(() => (window as unknown as { __inputSmoke?: { sample(): { state: InputState } | null } }).__inputSmoke?.sample()?.state).catch(() => null);
    const route = await routeEvidence(page).catch(() => null);
    fs.writeFileSync(info.outputPath('pressure-stress-evidence.json'), JSON.stringify({ provenance, deliberateKeyHandlerMs: delay, deliberateWheelHandlerMs: delay ? 75 : 0, marks, expected: state, final, route, errors }, null, 2));
    await info.attach('pressure-stress-evidence', { path: info.outputPath('pressure-stress-evidence.json'), contentType: 'application/json' });
    await page.getByRole('button', { name: 'Close live browser panel', exact: true }).click({ timeout: 5000 }).catch(() => {});
  }
});

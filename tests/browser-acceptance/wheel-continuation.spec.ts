import fs from 'node:fs';
import { fileURLToPath } from 'node:url';
import { randomInt } from 'node:crypto';
import { expect, test } from '@playwright/test';
import { browserLivePanel, selectAgent } from '../e2e/fixtures/selectors';
import { installPixels, instrumentRoutes, point, routeEvidence, stateIs, type InputState } from './input-connection-probe';

const fixture = fs.readFileSync(fileURLToPath(new URL('./wheel-continuation-fixture.html', import.meta.url)), 'utf8');
const target = new URL(JSON.parse(fs.readFileSync(process.env.BROWSER_INPUT_FIXTURE_CONFIG!, 'utf8')).url);
if (target.origin !== 'https://uat-omnipus.fly.dev' || !target.pathname.startsWith('/preview/') || target.search || target.hash || target.username || target.password) throw Error('Exact approved fixture URL required');
const provenance = JSON.parse(fs.readFileSync(process.env.BROWSER_INPUT_PROVENANCE!, 'utf8'));

test('one slow wheel retains every continuation and subsequent input', async ({ page }, info) => {
  let state: InputState = { nonce: randomInt(1, 65536), clicks: 0, downs: 0, ups: 0, held: 0, scroll: 0, drags: 0, errors: 0, text: '' };
  const errors: string[] = [], marks: Array<{ label: string; at: string }> = [];
  page.on('pageerror', error => errors.push(error.message));
  await instrumentRoutes(page);
  const ready = async () => {
    await expect(page.locator('[data-input-mode="dedicated"]')).toHaveAttribute('data-input-state', 'ready');
    await expect(browserLivePanel(page).getByRole('status').filter({ hasText: /Waiting for the current page|Pointer input is unavailable|Browser input is unavailable|Reconnecting video to restore browser input/ })).toHaveCount(0);
    await expect(browserLivePanel(page).getByRole('alert')).toHaveCount(0);
  };
  try {
    const served = await page.request.get(target.href, { maxRedirects: 0 });
    expect(served.status()).toBe(200); expect(await served.text()).toBe(fixture);
    await page.goto('/workspaces/01M01TTSDZBFGM28NPHGTFZ17T/chat');
    await selectAgent(page, 'Browser UAT Test');
    await page.getByRole('button', { name: 'Open browser', exact: true }).click();
    await ready();
    const url = new URL(target); url.searchParams.set('nonce', String(state.nonce));
    const address = page.getByRole('textbox', { name: 'Address bar' });
    await address.fill(url.href); await address.press('Enter');
    await installPixels(page); await stateIs(page, state); await ready();
    const wheel = await point(page, .25, .86);
    await page.mouse.click(wheel.x, wheel.y);
    state = { ...state, downs: 1, ups: 1 };
    await stateIs(page, state); await ready();
    await page.evaluate(() => {
      const states: string[] = [];
      const root = document.querySelector('[data-input-mode="dedicated"]');
      if (!root) throw Error('Dedicated input state element required');
      const read = () => states.push(root.getAttribute('data-input-state') ?? 'missing');
      read();
      new MutationObserver(read).observe(root, { attributes: true, attributeFilter: ['data-input-state'] });
      (window as unknown as { __wheelStates: string[] }).__wheelStates = states;
    });
    await page.mouse.move(wheel.x, wheel.y);
    marks.push({ label: 'first-wheel-start', at: new Date().toISOString() });
    for (let i = 0; i < 21; i++) {
      await page.mouse.wheel(0, 10);
      await page.waitForTimeout(20);
    }
    marks.push({ label: 'all-wheel-events-sent', at: new Date().toISOString() });
    state = { ...state, scroll: 210 };
    await stateIs(page, state); await ready();
    marks.push({ label: 'exact-scroll-presented', at: new Date().toISOString() });
    const button = await point(page, .73, .68);
    await page.mouse.click(button.x, button.y);
    state = { ...state, clicks: 1, downs: 2, ups: 2 };
    await stateIs(page, state); await ready();
    const text = await point(page, .25, .68);
    await page.mouse.click(text.x, text.y); await page.keyboard.insertText('@é');
    state = { ...state, text: '@é', downs: 3, ups: 3 };
    await stateIs(page, state); await ready();
    const routes = (await routeEvidence(page)).routes;
    expect(routes.filter(row => row.kind === 'wheel')).toHaveLength(21);
    expect(routes.filter(row => row.route === 'websocket')).toEqual([]);
    expect(routes.every(row => row.encoding === 'binary-v1')).toBe(true);
    const states = await page.evaluate(() => (window as unknown as { __wheelStates: string[] }).__wheelStates);
    expect(states.every(value => value === 'ready')).toBe(true);
    expect(errors).toEqual([]);
  } finally {
    const final = await page.evaluate(() => (window as unknown as { __inputSmoke?: { sample(): { state: InputState } | null } }).__inputSmoke?.sample()?.state).catch(() => null);
    const route = await routeEvidence(page).catch(() => null);
    const states = await page.evaluate(() => (window as unknown as { __wheelStates?: string[] }).__wheelStates).catch(() => null);
    const output = info.outputPath('wheel-continuation-evidence.json');
    fs.writeFileSync(output, JSON.stringify({ provenance, deliberateFirstWheelHandlerMs: 1200, requiredServerProof: 'A completed wheel within the marked window spent more than1000ms in Chrome dispatch; verify server timing logs separately.', marks, expected: state, final, route, states, errors }, null, 2));
    await info.attach('wheel-continuation-evidence', { path: output, contentType: 'application/json' });
    await page.getByRole('button', { name: 'Close live browser panel', exact: true }).click({ timeout: 5000 }).catch(() => {});
  }
});

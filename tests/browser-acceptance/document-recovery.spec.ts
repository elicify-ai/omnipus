import fs from 'node:fs';
import { fileURLToPath } from 'node:url';
import { randomInt } from 'node:crypto';
import { expect, test } from '@playwright/test';
import { browserLiveFrame, browserLivePanel, browserLiveVideo, selectAgent } from '../e2e/fixtures/selectors';
import { installPixels, instrumentRoutes, point, routeEvidence, stateIs, type InputState } from './input-connection-probe';
const fixture = fs.readFileSync(fileURLToPath(new URL('./document-recovery-fixture.html', import.meta.url)), 'utf8');
const target = new URL(JSON.parse(fs.readFileSync(process.env.BROWSER_INPUT_FIXTURE_CONFIG!, 'utf8')).url);
if (target.origin !== 'https://uat-omnipus.fly.dev' || !target.pathname.startsWith('/preview/') || target.search || target.hash || target.username || target.password) throw Error('Exact approved fixture URL required');
const provenance = JSON.parse(fs.readFileSync(process.env.BROWSER_INPUT_PROVENANCE!, 'utf8'));
test('current picture and dedicated input recover through history, redirect and document replacement', async ({ page }, info) => {
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
    const navigate = await point(page, .73, .68); marks.push({ label: 'transition-start', at: new Date().toISOString() });
    await page.mouse.click(navigate.x, navigate.y);
    // Distinct nonce and reset counters require the final new document's pixels.
    state = { ...state, nonce: nonce + 1 }; await stateIs(page, state); await ready();
    marks.push({ label: 'final-document-ready', at: new Date().toISOString() });
    await browserLiveFrame(page).focus(); await page.keyboard.down('ArrowLeft'); state = { ...state, held: 2 }; await stateIs(page, state);
    await page.keyboard.up('ArrowLeft'); state = { ...state, held: 0 }; await stateIs(page, state);
    const click = await point(page, .73, .68); await page.mouse.click(click.x, click.y); state = { ...state, clicks: 1, downs: 1, ups: 1 }; await stateIs(page, state);
    const text = await point(page, .25, .68); await page.mouse.click(text.x, text.y); await page.keyboard.type('x');
    state = { ...state, downs: 2, ups: 2, text: 'x' }; await stateIs(page, state); await ready();
    const routes = (await routeEvidence(page)).routes;
    expect(routes.filter(row => row.route === 'websocket')).toEqual([]);
    expect(routes.filter(row => row.kind === 'key_down')).toHaveLength(1);
    expect(routes.filter(row => row.kind === 'key_up')).toHaveLength(1);
    expect(routes.filter(row => row.kind === 'text')).toHaveLength(1);
    expect(errors).toEqual([]);
  } finally {
    const final = await page.evaluate(() => (window as unknown as { __inputSmoke?: { sample(): { state: InputState } | null } }).__inputSmoke?.sample()?.state).catch(() => null);
    const route = await routeEvidence(page).catch(() => null);
    fs.writeFileSync(info.outputPath('document-recovery-evidence.json'), JSON.stringify({ provenance, marks, final, route, errors }, null, 2));
    await info.attach('document-recovery-evidence', { path: info.outputPath('document-recovery-evidence.json'), contentType: 'application/json' });
    await page.getByRole('button', { name: 'Close live browser panel', exact: true }).click({ timeout: 5000 }).catch(() => {});
  }
});

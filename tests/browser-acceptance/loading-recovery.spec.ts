import fs from 'node:fs';
import { randomBytes, randomInt } from 'node:crypto';
import { expect, test } from '@playwright/test';
import { browserLiveFrame, browserLivePanel, selectAgent } from '../e2e/fixtures/selectors';
import { installPixels, instrumentRoutes, point, routeEvidence, stateIs, type InputState } from './input-connection-probe';

const target = new URL(JSON.parse(fs.readFileSync(process.env.BROWSER_INPUT_FIXTURE_CONFIG!, 'utf8')).url);
if (target.origin !== 'https://uat-omnipus.fly.dev' || !target.pathname.startsWith('/preview/') || !target.pathname.endsWith('/fixture') || target.search || target.hash || target.username || target.password) throw Error('Approved Amsterdam preview /fixture URL required');
const provenance = JSON.parse(fs.readFileSync(process.env.BROWSER_INPUT_PROVENANCE!, 'utf8'));

for (const mode of ['stop', 'late'] as const) test(`native loading beyond 15 seconds recovers through ${mode === 'stop' ? 'Stop' : 'late commit'}`, async ({ page }, info) => {
  const nonce = randomInt(1, 65534), request = randomBytes(12).toString('hex');
  let state: InputState = { nonce, clicks: 0, downs: 0, ups: 0, held: 0, scroll: 0, drags: 0, errors: 0, text: '' };
  const marks: Array<{ label: string; at: string }> = [], errors: string[] = [];
  let pendingProof: unknown, canceledProof: unknown;
  const url = new URL(target); url.searchParams.set('nonce', String(nonce)); url.searchParams.set('request', request); url.searchParams.set('mode', mode);
  const statusURL = new URL('status', target); statusURL.searchParams.set('request', request);
  const status = async (): Promise<{ phase: string; ageMs: number }> => {
    const response = await page.request.get(statusURL.href, { maxRedirects: 0 });
    expect(response.status()).toBe(200);
    return response.json();
  };
  const ready = async () => {
    await expect(page.locator('[data-input-mode="dedicated"]')).toHaveAttribute('data-input-state', 'ready');
    await expect(browserLivePanel(page).getByRole('status').filter({ hasText: /Waiting for the current page|Pointer input is unavailable|Browser input is unavailable|Reconnecting video to restore browser input/ })).toHaveCount(0);
    await expect(browserLivePanel(page).getByRole('alert')).toHaveCount(0);
  };
  page.on('pageerror', error => errors.push(error.message));
  await instrumentRoutes(page);
  try {
    const served = await page.request.get(url.href, { maxRedirects: 0 });
    expect(served.status()).toBe(200);
    expect(await served.text()).toContain('<form action="hang" method="GET">');
    await page.goto('/workspaces/01M01TTSDZBFGM28NPHGTFZ17T/chat');
    await selectAgent(page, 'Browser UAT Test');
    await page.getByRole('button', { name: 'Open browser', exact: true }).click();
    await ready();
    const address = page.getByRole('textbox', { name: 'Address bar' });
    await address.fill(url.href); await address.press('Enter');
    await installPixels(page); await stateIs(page, state); await ready();
    const submit = await point(page, .73, .68);
    marks.push({ label: 'native-submit', at: new Date().toISOString() });
    await page.mouse.click(submit.x, submit.y);
    await expect.poll(async () => (await status()).phase).toBe('pending');
    // This deliberate wait crosses the former 15-second document timeout.
    await page.waitForTimeout(16000);
    pendingProof = await status();
    expect(pendingProof).toMatchObject({ phase: 'pending' });
    expect((pendingProof as { ageMs: number }).ageMs).toBeGreaterThanOrEqual(16000);
    await expect(browserLivePanel(page).getByRole('alert')).toHaveCount(0);
    marks.push({ label: 'pending-beyond-15s', at: new Date().toISOString() });
    if (mode === 'stop') {
      await page.getByRole('button', { name: 'Stop loading', exact: true }).click();
      await expect.poll(async () => (await status()).phase).toBe('canceled');
      state = { ...state, clicks: 1, downs: 1, ups: 1 };
    } else {
      await expect.poll(async () => (await status()).phase, { timeout: 30000 }).toBe('completed');
      state = { ...state, nonce: nonce + 1 };
    }
    canceledProof = await status();
    await stateIs(page, state); await ready();
    marks.push({ label: mode === 'stop' ? 'stopped-original-picture-ready' : 'late-new-picture-ready', at: new Date().toISOString() });
    await browserLiveFrame(page).focus();
    await page.keyboard.down('ArrowLeft'); state = { ...state, held: 2 }; await stateIs(page, state);
    await page.keyboard.up('ArrowLeft'); state = { ...state, held: 0 }; await stateIs(page, state);
    const text = await point(page, .25, .68); await page.mouse.click(text.x, text.y);
    await page.keyboard.insertText('@é日本');
    state = { ...state, downs: state.downs + 1, ups: state.ups + 1, text: '@é日本' }; await stateIs(page, state); await ready();
    marks.push({ label: 'fresh-input-exact', at: new Date().toISOString() });
    const routes = (await routeEvidence(page)).routes;
    expect(routes.filter(row => row.route === 'websocket')).toEqual([]);
    expect(routes.every(row => row.encoding === 'binary-v1')).toBe(true);
    expect(routes.filter(row => row.kind === 'key_down')).toHaveLength(1);
    expect(routes.filter(row => row.kind === 'key_up')).toHaveLength(1);
    expect(routes.filter(row => row.kind === 'text')).toHaveLength(1);
    expect(errors).toEqual([]);
  } finally {
    const final = await page.evaluate(() => (window as unknown as { __inputSmoke?: { sample(): { state: InputState } | null } }).__inputSmoke?.sample()?.state).catch(() => null);
    const route = await routeEvidence(page).catch(() => null);
    const artifact = info.outputPath('loading-recovery-evidence.json');
    fs.writeFileSync(artifact, JSON.stringify({ provenance, mode, request, marks, pendingProof, canceledProof, expected: state, final, route, errors }, null, 2));
    await info.attach('loading-recovery-evidence', { path: artifact, contentType: 'application/json' });
    await page.getByRole('button', { name: 'Close live browser panel', exact: true }).click({ timeout: 5000 }).catch(() => {});
  }
});

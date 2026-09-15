import fs from 'node:fs';
import { fileURLToPath } from 'node:url';
import { randomInt } from 'node:crypto';
import { expect, test } from '@playwright/test';
import { browserLivePanel, browserLiveVideo, selectAgent } from '../e2e/fixtures/selectors';
import { installPixels, instrumentRoutes, point, routeEvidence, stateIs, type InputState } from './input-connection-probe';
const fixture = fs.readFileSync(fileURLToPath(new URL('./international-keyboard-fixture.html', import.meta.url)), 'utf8');
const target = new URL(JSON.parse(fs.readFileSync(process.env.BROWSER_INPUT_FIXTURE_CONFIG!, 'utf8')).url);
if (target.origin !== 'https://uat-omnipus.fly.dev' || !target.pathname.startsWith('/preview/') || target.search || target.hash || target.username || target.password) throw Error('Exact approved fixture URL required');
const provenance = JSON.parse(fs.readFileSync(process.env.BROWSER_INPUT_PROVENANCE!, 'utf8'));
test('native composition commits Unicode once and discards canceled or blurred candidates', async ({ page }, info) => {
  const nonce = randomInt(1, 65535);
  let state: InputState = { nonce, clicks: 0, downs: 0, ups: 0, held: 0, scroll: 0, drags: 0, errors: 0, text: '' };
  const errors: string[] = [], marks: Array<{ label: string; at: string }> = [];
  page.on('pageerror', error => errors.push(error.message));
  await instrumentRoutes(page);
  await page.addInitScript(() => {
    const counts = { starts: 0, ends: 0, canceled: 0, lifecycle: [] as Array<{ type: string; trusted: boolean }> };
    Object.assign(window, { __internationalComposition: counts });
    document.addEventListener('compositionstart', event => { counts.starts++; counts.lifecycle.push({ type: 'start', trusted: event.isTrusted }); }, true);
    document.addEventListener('compositionend', event => { counts.ends++; if (!event.data) counts.canceled++; counts.lifecycle.push({ type: 'end', trusted: event.isTrusted }); }, true);
  });
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
    await page.keyboard.down('a'); state = { ...state, downs: 1, held: 1, clicks: 1, text: 'a' }; await stateIs(page, state);
    await page.keyboard.up('a'); state = { ...state, ups: 1, held: 0 }; await stateIs(page, state);
    await page.keyboard.down('Alt'); state = { ...state, downs: 2, held: 1 }; await stateIs(page, state);
    const cdp = await page.context().newCDPSession(page);
    const unchangedFor = async (milliseconds: number) => {
      const until = Date.now() + milliseconds;
      do { await stateIs(page, state); await page.waitForTimeout(50); } while (Date.now() < until);
    };
    try {
      await cdp.send('Input.dispatchKeyEvent', { type: 'keyDown', key: '@', code: 'KeyL', modifiers: 1, text: '@', unmodifiedText: 'l', windowsVirtualKeyCode: 76 });
      state = { ...state, downs: 3, held: 2, clicks: 2, text: 'a@' }; await stateIs(page, state);
      await cdp.send('Input.dispatchKeyEvent', { type: 'keyUp', key: '@', code: 'KeyL', modifiers: 1, windowsVirtualKeyCode: 76 });
      state = { ...state, ups: 2, held: 1 }; await stateIs(page, state);
      await page.keyboard.up('Alt'); state = { ...state, ups: 3, held: 0 }; await stateIs(page, state);
      await cdp.send('Input.imeSetComposition', { text: 'に', selectionStart: 1, selectionEnd: 1 });
      await expect.poll(() => page.evaluate(() => (window as unknown as { __internationalComposition: { starts: number } }).__internationalComposition.starts)).toBe(1);
      await unchangedFor(400);
      await cdp.send('Input.imeSetComposition', { text: '日本é🙂', selectionStart: 5, selectionEnd: 5 });
      await unchangedFor(400);
      await cdp.send('Input.insertText', { text: '日本é🙂' });
      state = { ...state, clicks: 3, text: 'a@日本é🙂' }; await stateIs(page, state); await unchangedFor(400);
      marks.push({ label: 'unicode-committed-once', at: new Date().toISOString() });
      // Native text services can commit without a physical key or composition session.
      await cdp.send('Input.insertText', { text: '🙂' });
      state = { ...state, clicks: 4, text: 'a@日本é🙂🙂' }; await stateIs(page, state); await unchangedFor(400);
      await cdp.send('Input.imeSetComposition', { text: '消す', selectionStart: 2, selectionEnd: 2 });
      await unchangedFor(400);
      await cdp.send('Input.imeSetComposition', { text: '', selectionStart: 0, selectionEnd: 0 });
      await unchangedFor(600);
      marks.push({ label: 'canceled-without-commit', at: new Date().toISOString() });
      await cdp.send('Input.imeSetComposition', { text: '未確定', selectionStart: 3, selectionEnd: 3 });
      await unchangedFor(400);
      await address.focus();
      await cdp.send('Input.imeSetComposition', { text: '', selectionStart: 0, selectionEnd: 0 });
      await cdp.send('Input.insertText', { text: 'late' });
      await unchangedFor(600);
      marks.push({ label: 'blur-rejected-late-text', at: new Date().toISOString() });
    } finally { await cdp.detach(); }
    const composition = await page.evaluate(() => (window as unknown as { __internationalComposition: { starts: number; ends: number; canceled: number; lifecycle: Array<{ type: string; trusted: boolean }> } }).__internationalComposition);
    expect(composition.starts).toBe(3); expect(composition.ends).toBe(3); expect(composition.canceled).toBeGreaterThanOrEqual(1);
    // Pristine Chromium characterization: the CDP IME driver emits trusted
    // starts and untrusted ends. This asserts the test driver, not OS IME trust.
    expect(composition.lifecycle).toEqual(Array.from({ length: 3 }, () => [
      { type: 'start', trusted: true }, { type: 'end', trusted: false },
    ]).flat());
    const routes = (await routeEvidence(page)).routes;
    expect(routes.filter(row => row.route === 'websocket')).toEqual([]);
    expect(routes.length).toBeGreaterThan(0);
    expect(routes.every(row => row.encoding === 'binary-v1')).toBe(true);
    expect(errors).toEqual([]);
  } finally {
    const final = await page.evaluate(() => (window as unknown as { __inputSmoke?: { sample(): { state: InputState } | null } }).__inputSmoke?.sample()?.state).catch(() => null);
    const route = await routeEvidence(page).catch(() => null);
    const composition = await page.evaluate(() => (window as unknown as { __internationalComposition?: unknown }).__internationalComposition).catch(() => null);
    fs.writeFileSync(info.outputPath('international-keyboard-evidence.json'), JSON.stringify({ provenance, marks, final, route, composition, errors }, null, 2));
    await info.attach('international-keyboard-evidence', { path: info.outputPath('international-keyboard-evidence.json'), contentType: 'application/json' });
    await page.getByRole('button', { name: 'Close live browser panel', exact: true }).click({ timeout: 5000 }).catch(() => {});
  }
});

import fs from 'node:fs';
import { fileURLToPath } from 'node:url';
import { randomInt } from 'node:crypto';
import { expect, test } from '@playwright/test';
import { browserLiveFrame, browserLivePanel, browserLiveVideo, selectAgent } from '../e2e/fixtures/selectors';
import { disconnect, installPixels, instrumentRoutes, point, routeEvidence, stateIs, type InputState } from './input-connection-probe';

const fixtureHTML = fs.readFileSync(fileURLToPath(new URL('./input-connection-fixture.html', import.meta.url)), 'utf8');
const fixtureConfig = JSON.parse(fs.readFileSync(process.env.BROWSER_INPUT_FIXTURE_CONFIG!, 'utf8')) as { url: string };
const fixtureURL = new URL(fixtureConfig.url);
if (fixtureURL.origin !== 'https://uat-omnipus.fly.dev' || !fixtureURL.pathname.startsWith('/preview/') || fixtureURL.username || fixtureURL.password || fixtureURL.search || fixtureURL.hash) throw Error('An exact approved UAT preview fixture URL is required');
const provenance = JSON.parse(fs.readFileSync(process.env.BROWSER_INPUT_PROVENANCE!, 'utf8')) as { source: string; binarySHA256: string };
if (!/^[a-f0-9]{9,40}$/.test(provenance.source) || !/^[a-f0-9]{64}$/.test(provenance.binarySHA256)) throw Error('Verified source and binary SHA256 provenance is required');

test.describe.configure({ retries: 0 });
for (const mode of ['websocket', 'dedicated'] as const) {
  test(`${mode}: remote input text, clicks, scroll, drag and recovery stay exact`, async ({ page }, info) => {
    const nonce = randomInt(1, 65536);
    let state: InputState = { nonce, clicks: 0, downs: 0, ups: 0, held: 0, scroll: 0, drags: 0, errors: 0, text: '' };
    const checkpoints: Array<{ label: string; state: InputState }> = [], clickFeedbackMs: number[] = [], errors: string[] = [];
    let scrollCatchUpMs: number | undefined;
    page.on('pageerror', error => errors.push(error.message));
    await instrumentRoutes(page);
    const checkpoint = async (label: string) => {
      await stateIs(page, state);
      expect((await routeEvidence(page)).sameMedia, `original media at ${label}`).toBe(true);
      expect(await browserLiveVideo(page).evaluate(node => {
        const stream = (node as HTMLVideoElement).srcObject as MediaStream;
        return { audio: stream.getAudioTracks().map(track => track.readyState), video: stream.getVideoTracks().map(track => track.readyState) };
      })).toEqual({ audio: ['live'], video: ['live'] });
      checkpoints.push({ label, state: { ...state } });
      console.log(`[input-smoke] ${mode}: ${label}`);
    };
    const awaitInputReady = async () => {
      const panel = browserLivePanel(page);
      await expect(panel.getByRole('status').filter({ hasText: /Waiting for the current page|Pointer input is unavailable|Browser input is unavailable|Reconnecting video to restore browser input/ })).toHaveCount(0, { timeout: 15000 });
      await expect(panel.getByRole('alert')).toHaveCount(0);
      if (mode === 'dedicated') await expect(page.locator('[data-input-mode="dedicated"]')).toHaveAttribute('data-input-state', 'ready');
    };
    const clickAt = async (x: number, y: number) => { await awaitInputReady(); const p = await point(page, x, y); await page.mouse.click(p.x, p.y); state = { ...state, downs: state.downs + 1, ups: state.ups + 1 }; };
    try {
      const served = await page.request.get(fixtureURL.href, { maxRedirects: 0 });
      expect(served.status()).toBe(200); expect(await served.text()).toBe(fixtureHTML);
      await page.goto(`/workspaces/01M01TTSDZBFGM28NPHGTFZ17T/chat?browserInput=${mode}`);
      await selectAgent(page, 'Browser UAT Test');
      expect(new URL(page.url()).pathname).toBe('/workspaces/01M01TTSDZBFGM28NPHGTFZ17T/chat');
      expect(new URL(page.url()).searchParams.get('browserInput')).toBe(mode);
      await page.getByRole('button', { name: 'Open browser', exact: true }).click();
      await expect(browserLivePanel(page)).toBeVisible();
      await expect(page.locator(`[data-input-mode="${mode}"]`)).toBeVisible();
      if (mode === 'dedicated') await expect(page.locator('[data-input-mode="dedicated"]')).toHaveAttribute('data-input-state', 'ready');
      await expect.poll(() => browserLiveVideo(page).evaluate(node => (node as HTMLVideoElement).readyState), { timeout: 45000 }).toBeGreaterThanOrEqual(2);
      const target = new URL(fixtureURL); target.searchParams.set('nonce', String(nonce));
      const address = page.getByRole('textbox', { name: 'Address bar' });
      await address.fill(target.href); await address.press('Enter');
      await installPixels(page); await checkpoint('fixture-ready');
      try {
        await awaitInputReady();
      } finally {
        const clickPoint = await point(page, .73, .68);
        const readiness = await page.evaluate(p => {
          const hit = document.elementFromPoint(p.x, p.y);
          const mode = document.querySelector('[data-input-mode]');
          return { point: p, hit: hit && { tag: hit.tagName, testId: hit.getAttribute('data-testid'), role: hit.getAttribute('role') }, inputState: mode?.getAttribute('data-input-state'), status: Array.from(document.querySelectorAll('[role="status"]')).map(node => node.textContent) };
        }, clickPoint);
        await fs.promises.writeFile(info.outputPath('pre-click-readiness.json'), JSON.stringify(readiness, null, 2));
        await page.screenshot({ path: info.outputPath('pre-click.png') });
      }
      console.log(`[input-smoke] ${mode}: input-gate-ready`);
      const tabA = await page.locator('[data-testid^="browser-tab-"][aria-pressed="true"]').getAttribute('data-testid');
      expect(tabA).toMatch(/^browser-tab-\d+$/);
      for (let i = 0; i < 10; i++) {
        const start = performance.now();
        await clickAt(.73, .68); state = { ...state, clicks: i + 1 }; await stateIs(page, state);
        clickFeedbackMs.push(performance.now() - start);
      }
      await checkpoint('ten-clicks');
      await clickAt(.25, .68); await stateIs(page, state);
      // Trusted viewer key events exercise the UI handler for non-US keys;
      // no command is sent directly to target Chromium by this test.
      await awaitInputReady();
      const viewerCDP = await page.context().newCDPSession(page);
      const text = 'Zażółć 世界';
      for (const key of text) {
        await viewerCDP.send('Input.dispatchKeyEvent', { type: 'keyDown', key });
        await viewerCDP.send('Input.dispatchKeyEvent', { type: 'keyUp', key });
      }
      await viewerCDP.detach(); state = { ...state, text }; await checkpoint('unicode-text');
      await awaitInputReady();
      const wheel = await point(page, .25, .86); await page.mouse.move(wheel.x, wheel.y);
      const scrollStart = performance.now();
      await page.mouse.wheel(0, 120); await page.mouse.wheel(0, 180);
      state = { ...state, scroll: 300 }; await stateIs(page, state); scrollCatchUpMs = performance.now() - scrollStart;
      await checkpoint('scroll-300');
      await awaitInputReady();
      const from = await point(page, .60, .86), to = await point(page, .87, .86);
      await page.mouse.move(from.x, from.y); await page.mouse.down();
      state = { ...state, downs: state.downs + 1, held: 1 }; await stateIs(page, state);
      await page.mouse.move(to.x, to.y, { steps: 8 }); await page.mouse.up();
      state = { ...state, ups: state.ups + 1, held: 0, drags: 1 }; await checkpoint('drag-complete');
      const tabs = await page.locator('[data-testid^="browser-tab-"][aria-pressed]').count();
      await page.getByTestId('browser-tab-new').click();
      await expect(page.locator('[data-testid^="browser-tab-"][aria-pressed]')).toHaveCount(tabs + 1);
      await page.getByTestId(tabA!).click();
      await expect(page.getByTestId(tabA!)).toHaveAttribute('aria-pressed', 'true');
      await checkpoint('tab-return');
      await clickAt(.73, .68); state = { ...state, clicks: state.clicks + 1 }; await checkpoint('tab-return-click');
      const before = await browserLiveVideo(page).evaluate(node => `${(node as HTMLVideoElement).videoWidth}x${(node as HTMLVideoElement).videoHeight}`);
      await page.setViewportSize({ width: 1100, height: 720 });
      await expect.poll(() => browserLiveVideo(page).evaluate(node => `${(node as HTMLVideoElement).videoWidth}x${(node as HTMLVideoElement).videoHeight}`)).not.toBe(before);
      await checkpoint('resized');
      await clickAt(.73, .68); state = { ...state, clicks: state.clicks + 1 }; await checkpoint('resized-click');
      await awaitInputReady();
      await browserLiveFrame(page).focus(); await page.keyboard.down('ArrowLeft');
      state = { ...state, held: 2 }; await checkpoint('key-held');
      const openedBefore = (await routeEvidence(page)).openedSockets;
      await disconnect(page, mode);
      if (mode === 'dedicated') {
        await expect(page.getByTestId('browser-input-error')).toBeVisible();
        state = { ...state, held: 0 }; await checkpoint('input-lost-released');
        await page.keyboard.up('ArrowLeft');
        const retry = page.getByRole('button', { name: 'Retry input', exact: true });
        await expect(retry).toHaveAttribute('tabindex', '0');
        await retry.focus();
        await retry.press('Enter');
        await expect(page.locator('[data-input-mode="dedicated"]')).toHaveAttribute('data-input-state', 'ready');
      } else {
        await page.keyboard.up('ArrowLeft');
        await expect.poll(async () => (await routeEvidence(page)).openedSockets, { timeout: 45000 }).toBe(openedBefore + 1);
        await expect.poll(() => browserLiveVideo(page).evaluate(node => (node as HTMLVideoElement).readyState), { timeout: 45000 }).toBeGreaterThanOrEqual(2);
        await installPixels(page); state = { ...state, held: 0 };
      }
      await checkpoint('recovered');
      await clickAt(.73, .68); state = { ...state, clicks: state.clicks + 1 }; await checkpoint('post-recovery-click');
      const evidence = await routeEvidence(page);
      const actionRoutes = [...new Set(evidence.routes.map(event => event.route))];
      if (mode === 'dedicated') {
        expect(actionRoutes).toContain('input-reliable'); expect(actionRoutes).not.toContain('websocket');
        expect(evidence.peers.filter(peer => peer.labels.includes('input-reliable') && peer.state !== 'closed')).toEqual([{ labels: ['input-reliable', 'input-hover'], transceivers: 0, state: 'connected' }]);
      } else expect(actionRoutes).toEqual(['websocket']);
      expect(evidence.routes.filter(event => event.kind === 'mouse_down')).toHaveLength(state.downs);
      expect(evidence.routes.filter(event => event.kind === 'mouse_up')).toHaveLength(state.ups);
      expect(errors).toEqual([]);
      await browserLiveVideo(page).screenshot({ path: info.outputPath('final-video.png') });
    } finally {
      const evidence = await routeEvidence(page).catch(() => null);
      let cleanupError: string | null = null;
      try { await page.getByRole('button', { name: 'Close live browser panel', exact: true }).click({ timeout: 5000 }); }
      catch { cleanupError = 'Panel close failed; Playwright context teardown still follows'; }
      fs.mkdirSync(info.outputDir, { recursive: true });
      fs.writeFileSync(info.outputPath('input-connection-evidence.json'), JSON.stringify({ mode, provenance, checkpoints, state, errors, cleanupError, clickFeedbackMs, scrollCatchUpMs, evidence, limits: ['click timings include runner/pixel sampling overhead; not 100-click latency acceptance', 'live audio track does not prove audible content', 'baseline signaling reconnect may replace media'] }, null, 2));
    }
  });
}

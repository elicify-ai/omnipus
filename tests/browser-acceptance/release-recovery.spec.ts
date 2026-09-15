import { browserTestWorkspacePath } from './test-workspace';
import fs from 'node:fs';
import { fileURLToPath } from 'node:url';
import { randomInt } from 'node:crypto';
import { expect, test } from '@playwright/test';
import { browserLiveFrame, selectAgent } from '../e2e/fixtures/selectors';
import { installPixels, instrumentRoutes, point, routeEvidence, stateIs, type InputState } from './input-connection-probe';

const fixture = fs.readFileSync(fileURLToPath(new URL('./release-recovery-fixture.html', import.meta.url)), 'utf8');
const target = new URL(JSON.parse(fs.readFileSync(process.env.BROWSER_INPUT_FIXTURE_CONFIG!, 'utf8')).url);
if (target.origin !== 'https://uat-omnipus.fly.dev' || !target.pathname.startsWith('/preview/') || target.search || target.hash || target.username || target.password) throw Error('Exact approved fixture URL required');
const provenance = JSON.parse(fs.readFileSync(process.env.BROWSER_INPUT_PROVENANCE!, 'utf8'));
if (provenance.remoteVerified !== true || typeof provenance.source !== 'string' || !/^[a-f0-9]{40}$/.test(provenance.source)) throw Error('Verified committed runtime provenance required');

test('matching mouse and key releases survive one 1200ms press handler without Retry', async ({ page }, info) => {
  let state: InputState = { nonce: randomInt(1, 65536), clicks: 0, downs: 0, ups: 0, held: 0, scroll: 0, drags: 0, errors: 0, text: '' };
  const errors: string[] = [], observations: unknown[] = [];
  page.on('pageerror', error => errors.push(error.message));
  await instrumentRoutes(page);
  const viewer = page.locator('[data-input-mode="dedicated"]');
  const ready = async (timeout = 15000) => {
    await expect(viewer).toHaveAttribute('data-input-state', 'ready', { timeout });
    await expect(viewer.getByRole('alert')).toHaveCount(0);
    await expect(viewer.getByRole('status').filter({ hasText: /Waiting for the current page|Pointer input is unavailable|Browser input is unavailable|Reconnecting video to restore browser input/ })).toHaveCount(0);
  };
  const observe = async (checkpoint: string) => {
    await stateIs(page, state); // Existing five-second visible-effect bound, unchanged.
    await ready();
    observations.push({ checkpoint, at: new Date().toISOString(), state: { ...state }, routes: await routeEvidence(page) });
  };
  try {
    const served = await page.request.get(target.href, { maxRedirects: 0 });
    expect(served.status()).toBe(200); expect(await served.text()).toBe(fixture);
    await page.goto(browserTestWorkspacePath);
    await expect(page).toHaveURL(url => url.hash === browserTestWorkspacePath.slice(1)); await selectAgent(page, 'Browser UAT Test');
    const startupAt = Date.now();
    await page.getByRole('button', { name: 'Open browser', exact: true }).click(); await ready(45000);
    observations.push({ checkpoint: 'initial-ready', startupMs: Date.now() - startupAt });
    const url = new URL(target); url.searchParams.set('nonce', String(state.nonce));
    const address = page.getByRole('textbox', { name: 'Address bar' }); await address.fill(url.href); await address.press('Enter');
    await expect.poll(() => page.evaluate(() => {
      const video = document.querySelector<HTMLVideoElement>('[data-testid="browser-live-video"]');
      return !!video && video.srcObject instanceof MediaStream && video.srcObject.getVideoTracks().some(track => track.readyState === 'live') && video.readyState >= 2 && video.videoWidth > 0;
    })).toBe(true);
    await installPixels(page); await observe('initial');
    // Retain transient failure states: neither late pixels nor source cleanup
    // may turn an expired release into a successful uninterrupted interaction.
    await page.evaluate(() => {
      const w = window as unknown as { __releaseFailures: string[]; __releaseObserver: MutationObserver };
      w.__releaseFailures = [];
      const record = () => {
        const viewer = document.querySelector('[data-input-mode="dedicated"]');
        const input = viewer?.getAttribute('data-input-state');
        const alerts = [...(viewer?.querySelectorAll('[role="alert"]') || [])].map(node => node.textContent || '');
        if (input === 'failed' || input === 'paused' || alerts.length || /Retry input|Resume input|deadline exceeded|dispatch failed/i.test(viewer?.textContent || '')) w.__releaseFailures.push(JSON.stringify({ input, alerts }));
      };
      w.__releaseObserver = new MutationObserver(record);
      w.__releaseObserver.observe(document.body, { subtree: true, childList: true, characterData: true, attributes: true, attributeFilter: ['data-input-state'] });
      record();
    });
    const armMouse = await point(page, .25, .68); await page.mouse.click(armMouse.x, armMouse.y);
    state = { ...state, scroll: 1 }; await observe('mouse-armed');
    const button = await point(page, .73, .68);
    await page.mouse.click(button.x, button.y); // Native down/up, no delay between them.
    state = { ...state, clicks: 1, downs: 1, ups: 1 }; await observe('mouse-released');
    const armKey = await point(page, .25, .86); await page.mouse.click(armKey.x, armKey.y);
    state = { ...state, drags: 1 }; await observe('key-armed');
    await browserLiveFrame(page).focus();
    await expect(viewer.getByRole('textbox', { name: 'Remote browser text input' })).toBeFocused();
    await page.keyboard.down('ArrowLeft'); await page.keyboard.up('ArrowLeft');
    state = { ...state, downs: 2, ups: 2 }; await observe('key-released');
    expect(await page.evaluate(() => (window as unknown as { __releaseFailures: string[] }).__releaseFailures)).toEqual([]);
    const routes = (await routeEvidence(page)).routes;
    expect(routes.filter(row => row.kind === 'mouse_down')).toHaveLength(3);
    expect(routes.filter(row => row.kind === 'mouse_up')).toHaveLength(3);
    expect(routes.filter(row => row.kind === 'key_down')).toHaveLength(1);
    expect(routes.filter(row => row.kind === 'key_up')).toHaveLength(1);
    expect(routes.filter(row => row.route === 'websocket')).toEqual([]);
    expect(routes.every(row => row.encoding === 'binary-v1')).toBe(true); expect(errors).toEqual([]);
  } finally {
    const final = await page.evaluate(() => {
      const w = window as unknown as { __releaseFailures?: string[]; __releaseObserver?: MutationObserver; __inputSmoke?: { sample(): { state: InputState } | null } };
      w.__releaseObserver?.disconnect(); return { failures: w.__releaseFailures, state: w.__inputSmoke?.sample()?.state };
    }).catch(() => null);
    const output = info.outputPath('release-recovery-evidence.json');
    fs.writeFileSync(output, JSON.stringify({ provenance, testWorkspace: browserTestWorkspacePath, deliberatePressHandlerMs: 1200, expected: state, observations, final, errors, routes: await routeEvidence(page).catch(() => null) }, null, 2));
    await info.attach('release-recovery-evidence', { path: output, contentType: 'application/json' });
    await page.getByRole('button', { name: 'Close live browser panel', exact: true }).click({ timeout: 5000 }).catch(() => {});
  }
});

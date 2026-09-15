import fs from 'node:fs';
import { fileURLToPath } from 'node:url';
import { randomInt } from 'node:crypto';
import { expect, test, type Page } from '@playwright/test';
import { browserLiveFrame, browserLivePanel, selectAgent } from '../e2e/fixtures/selectors';
import { installPixels, instrumentRoutes, point, routeEvidence, type InputState } from './input-connection-probe';

const fixture = fs.readFileSync(fileURLToPath(new URL('./new-tab-fixture.html', import.meta.url)), 'utf8');
const target = new URL(JSON.parse(fs.readFileSync(process.env.BROWSER_INPUT_FIXTURE_CONFIG!, 'utf8')).url);
if (target.origin !== 'https://uat-omnipus.fly.dev' || !target.pathname.startsWith('/preview/') || target.search || target.hash || target.username || target.password) throw Error('Exact approved fixture URL required');
const provenance = JSON.parse(fs.readFileSync(process.env.BROWSER_INPUT_PROVENANCE!, 'utf8'));
async function sample(page: Page) {
  const state = await page.evaluate(() => (window as unknown as { __inputSmoke?: { sample(): { state: InputState } | null } }).__inputSmoke?.sample()?.state);
  return state && { nonce: state.nonce, width: state.held, height: state.drags, errors: state.errors, text: state.text };
}

test('new remote tab replaces the popout picture before typing and resizing', async ({ page: source, context }, info) => {
  const nonce = randomInt(1, 65535); // Parent1..65534; child is independently expected N+1.
  const observations: unknown[] = [], errors: string[] = [];
  let popup: Page | undefined;
  let latestTabs: Array<{ index: number; url: string }> = [];
  let activeTab = -1;
  const cleanup: Array<{ url: string; outcome: string }> = [];
  const parent = new URL(target); parent.searchParams.set('nonce', String(nonce));
  const child = new URL(parent); child.searchParams.set('nonce', String(nonce + 1)); child.searchParams.set('child', '1');
  source.on('pageerror', error => errors.push(error.message));
  const ready = async (page: Page) => {
    const viewer = page.locator('[data-input-mode="dedicated"]');
    await expect(viewer).toHaveCount(1);
    await expect(viewer).toHaveAttribute('data-input-state', 'ready');
    await expect(viewer.getByRole('status').filter({ hasText: /Waiting for the current page|Pointer input is unavailable|Browser input is unavailable|Reconnecting video to restore browser input/ })).toHaveCount(0);
    await expect(viewer.getByRole('alert')).toHaveCount(0);
  };
  const geometry = async (page: Page, expectedNonce: number, expectedText: string) => {
    // The target fixture has no scrollbars. Expected dimensions come from the
    // viewer's frame, never from the video or backend geometry being tested.
    await expect.poll(async () => {
      const actual = await sample(page), box = await browserLiveFrame(page).boundingBox();
      return !!actual && !!box && actual.nonce === expectedNonce && actual.errors === 0 && actual.text === expectedText && Math.abs(actual.width - box.width) <= 1 && Math.abs(actual.height - box.height) <= 1;
    }, { timeout: 10000 }).toBe(true);
    await ready(page);
    observations.push({ at: new Date().toISOString(), actual: await sample(page), viewerFrame: await browserLiveFrame(page).boundingBox() });
  };
  try {
    const served = await source.request.get(target.href, { maxRedirects: 0 });
    expect(served.status()).toBe(200); expect(await served.text()).toBe(fixture);
    await source.goto('/workspaces/01M01TTSDZBFGM28NPHGTFZ17T/chat');
    await selectAgent(source, 'Browser UAT Test');
    await source.getByRole('button', { name: 'Open browser', exact: true }).click(); await ready(source);
    [popup] = await Promise.all([context.waitForEvent('page'), source.getByRole('button', { name: 'Pop out', exact: true }).click()]);
    popup.on('pageerror', error => errors.push(error.message));
    await popup.waitForURL(url => url.hash.split('?')[0] === '#/browser-live');
    await expect(browserLivePanel(source)).toHaveCount(0);
    // Instrument a fresh popout document before starting the remote scenario;
    // a popup's first document can load before its Page event reaches the test.
    popup.on('websocket', socket => {
      if (new URL(socket.url()).pathname !== '/api/v1/browser/ws') return;
      socket.on('framereceived', frame => {
        try {
          const data = JSON.parse(frame.payload.toString());
          if (data.type !== 'browser_tabs' || !Array.isArray(data.tabs) || !Number.isInteger(data.active_index)) return;
          if (!data.tabs.every((tab: { index?: unknown; url?: unknown }) => Number.isInteger(tab.index) && typeof tab.url === 'string')) return;
          latestTabs = data.tabs.map((tab: { index: number; url: string }) => ({ index: tab.index, url: tab.url }));
          activeTab = data.active_index;
        } catch { /* Ignore non-tab protocol messages; never infer cleanup ownership from them. */ }
      });
    });
    await instrumentRoutes(popup); await popup.reload(); await ready(popup);
    await expect(browserLivePanel(source)).toHaveCount(0);
    const address = popup.getByRole('textbox', { name: 'Address bar' });
    await address.fill(parent.href); await address.press('Enter');
    await expect.poll(() => popup!.evaluate(() => {
      const video = document.querySelector<HTMLVideoElement>('[data-testid="browser-live-video"]');
      return !!video && video.srcObject instanceof MediaStream && video.srcObject.getVideoTracks().some(track => track.readyState === 'live') && video.readyState >= 2 && video.videoWidth > 0;
    })).toBe(true);
    await installPixels(popup); await geometry(popup, nonce, '');
    const link = await point(popup, .73, .68);
    await popup.mouse.click(link.x, link.y); // Real target=_blank link inside the received page.
    await expect(address).toHaveValue(child.href);
    await expect.poll(async () => (await sample(popup!))?.nonce, { timeout: 5000 }).toBe(nonce + 1);
    await ready(popup); await geometry(popup, nonce + 1, '');
    observations.push({ at: new Date().toISOString(), checkpoint: 'child-picture-before-input', expectedNonce: nonce + 1, actual: await sample(popup) });
    const button = await point(popup, .73, .68); await popup.mouse.click(button.x, button.y);
    await popup.keyboard.insertText('@é');
    await expect.poll(async () => (await sample(popup!))?.text, { timeout: 5000 }).toBe('@é');
    for (const size of [{ width: 1000, height: 700 }, { width: 1700, height: 1100 }, { width: 1440, height: 1000 }]) {
      await popup.setViewportSize(size); await geometry(popup, nonce + 1, '@é');
      await expect(browserLivePanel(source)).toHaveCount(0);
    }
    const routes = (await routeEvidence(popup)).routes;
    expect(routes.filter(row => row.kind === 'mouse_down')).toHaveLength(2);
    expect(routes.filter(row => row.kind === 'mouse_up')).toHaveLength(2);
    expect(routes.filter(row => row.kind === 'text')).toHaveLength(1);
    expect(routes.filter(row => row.route === 'websocket')).toEqual([]);
    expect(routes.every(row => row.encoding === 'binary-v1')).toBe(true);
    expect(errors).toEqual([]);
  } finally {
    if (popup && !popup.isClosed()) await popup.screenshot({ path: info.outputPath('popout-final.png'), timeout: 5000 }).catch(() => {});
    const output = info.outputPath('new-tab-evidence.json');
    fs.writeFileSync(output, JSON.stringify({ provenance, parentNonce: nonce, childNonce: nonce + 1, observations, errors,
      final: popup ? await sample(popup).catch(() => null) : null,
      routes: popup ? await routeEvidence(popup).catch(() => null) : null }, null, 2));
    await info.attach('new-tab-evidence', { path: output, contentType: 'application/json' });
    if (popup && !popup.isClosed()) {
      // URL ownership comes from real tab metadata; labels alone are shared by
      // every fixture run. Verify the selected address again before closing.
      for (const ownedURL of [child.href, parent.href]) {
        try {
          const matches = latestTabs.filter(tab => tab.url === ownedURL);
          if (matches.length !== 1) {
            cleanup.push({ url: ownedURL, outcome: matches.length ? 'ambiguous-left-open' : 'not-tracked' });
            continue;
          }
          await popup.getByTestId(`browser-tab-${matches[0].index}`).click({ timeout: 1500 });
          await expect(popup.getByRole('textbox', { name: 'Address bar' })).toHaveValue(ownedURL, { timeout: 1500 });
          const current = latestTabs.filter(tab => tab.url === ownedURL);
          if (current.length !== 1 || current[0].index !== activeTab) {
            cleanup.push({ url: ownedURL, outcome: 'ownership-changed-left-open' });
            break;
          }
          await popup.getByTestId(`browser-tab-close-${current[0].index}`).click({ timeout: 1500 });
          await expect.poll(() => latestTabs.some(tab => tab.url === ownedURL), { timeout: 1500 }).toBe(false);
          cleanup.push({ url: ownedURL, outcome: 'closed-exact-run-tab' });
        } catch {
          cleanup.push({ url: ownedURL, outcome: 'cleanup-unconfirmed-left-remaining-tabs-untouched' });
          break;
        }
      }
    }
    fs.writeFileSync(info.outputPath('new-tab-cleanup.json'), JSON.stringify(cleanup, null, 2));
    if (popup) await popup.close().catch(() => {});
    await source.getByRole('button', { name: 'Close live browser panel', exact: true }).click({ timeout: 5000 }).catch(() => {});
  }
});

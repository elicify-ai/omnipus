import fs from 'node:fs/promises';
import { expect, test, type Page } from '@playwright/test';
import { browserLivePanel, browserLiveVideo, selectAgent } from '../e2e/fixtures/selectors';
import { installPixels, instrumentRoutes, routeEvidence } from './input-connection-probe';
type Wheel = { route: string; x: number; y: number };
type LandingWindow = Window & { __landingProof: { wheels: Wheel[]; url: string } };
const target = 'https://www.w3.org/TR/2025/REC-webrtc-20250313/';
async function sample(page: Page) {
  return browserLiveVideo(page).evaluate(node => {
    const video = node as HTMLVideoElement;
    if (video.readyState < 2 || video.paused || !video.videoWidth) throw Error('Current live video required');
    const c = document.createElement('canvas'); c.width = 96; c.height = 54;
    const ctx = c.getContext('2d', { willReadFrequently: true })!;
    ctx.drawImage(video, 0, 0, 96, 54);
    const p = ctx.getImageData(0, 0, 96, 54).data;
    return Array.from({ length: 96 * 54 }, (_, i) => (p[i * 4] + p[i * 4 + 1] + p[i * 4 + 2]) / 3);
  });
}
const difference = (a: number[], b: number[]) => a.reduce((sum, v, i) => sum + Math.abs(v - b[i]), 0) / a.length;
for (const mode of ['websocket', 'dedicated'] as const) test(`${mode}: real documentation page scrolls down and returns through the stated input route`, async ({ page }, info) => {
  const errors: string[] = [], checkpoints: string[] = [];
  let downDifference: number | undefined, returnedDifference: number | undefined;
  let downVisibleChangeMs: number | undefined, returnToInitialPictureMs: number | undefined;
  page.on('pageerror', error => errors.push(error.message));
  await instrumentRoutes(page);
  await page.addInitScript(() => {
    const probe = { wheels: [] as Wheel[], url: '' };
    (window as unknown as LandingWindow).__landingProof = probe;
    const record = (route: string, data: unknown) => {
      if (typeof data !== 'string') return;
      let f; try { f = JSON.parse(data); } catch { return; }
      if (f.type === 'browser_input' && f.kind === 'wheel') probe.wheels.push({ route, x: f.delta_x, y: f.delta_y });
    };
    const NativePeer = window.RTCPeerConnection;
    window.RTCPeerConnection = class extends NativePeer {
      createDataChannel(label: string, options?: RTCDataChannelInit) {
        const channel = super.createDataChannel(label, options), send = channel.send.bind(channel);
        channel.send = ((data: string) => { send(data); record(label, data); }) as typeof channel.send;
        return channel;
      }
    };
    const NativeSocket = window.WebSocket;
    window.WebSocket = class extends NativeSocket {
      private browserSocket: boolean;
      constructor(...args: ConstructorParameters<typeof NativeSocket>) {
        super(...args); this.browserSocket = new URL(String(args[0]), location.href).pathname === '/api/v1/browser/ws';
        if (this.browserSocket) this.addEventListener('message', event => {
          let f; try { f = JSON.parse(event.data); } catch { return; }
          if (f.type === 'browser_tabs') probe.url = f.tabs?.[f.active_index]?.url ?? '';
        });
      }
      send(data: Parameters<WebSocket['send']>[0]) { super.send(data); if (this.browserSocket) record('websocket', data); }
    };
  });
  const ready = async () => {
    if (mode === 'dedicated') await expect(page.locator('[data-input-mode="dedicated"]')).toHaveAttribute('data-input-state', 'ready');
    await expect(browserLivePanel(page).getByRole('status').filter({ hasText: /Waiting for the current page|Pointer input is unavailable|Browser input is unavailable|Reconnecting video to restore browser input/ })).toHaveCount(0);
    await expect(browserLivePanel(page).getByRole('alert')).toHaveCount(0);
    await expect.poll(() => browserLiveVideo(page).evaluate(v => (v as HTMLVideoElement).readyState)).toBeGreaterThanOrEqual(2);
  };
  try {
    await page.goto(`/workspaces/01M01TTSDZBFGM28NPHGTFZ17T/chat?browserInput=${mode}`);
    await selectAgent(page, 'Browser UAT Test');
    expect(new URL(page.url()).searchParams.get('browserInput')).toBe(mode);
    await page.getByRole('button', { name: 'Open browser', exact: true }).click();
    await expect(browserLivePanel(page)).toBeVisible(); await ready();
    const address = page.getByRole('textbox', { name: 'Address bar' });
    await address.fill(target); await address.press('Enter');
    await expect.poll(() => page.evaluate(() => (window as unknown as LandingWindow).__landingProof.url)).toBe(target);
    await ready(); await installPixels(page);
    // Initial target navigation and frame readiness are outside all interaction evidence.
    const original = await sample(page);
    await browserLiveVideo(page).screenshot({ path: info.outputPath('landing-top.png') });
    checkpoints.push('real-target-ready');
    const bounds = await browserLiveVideo(page).boundingBox();
    if (!bounds) throw Error('Visible live-video bounds required');
    await page.mouse.move(bounds.x + bounds.width / 2, bounds.y + bounds.height / 2);
    const downStarted = performance.now();
    await page.mouse.wheel(0, 900); await page.mouse.wheel(0, 900);
    // Luma difference distinguishes a material streamed-picture change from codec noise.
    await expect.poll(async () => difference(original, await sample(page)), { intervals: [25, 50, 100] }).toBeGreaterThan(5);
    downVisibleChangeMs = performance.now() - downStarted;
    downDifference = difference(original, await sample(page));
    await browserLiveVideo(page).screenshot({ path: info.outputPath('landing-down.png') });
    checkpoints.push('streamed-content-changed-down');
    await ready();
    const returnStarted = performance.now();
    await page.mouse.wheel(0, -900); await page.mouse.wheel(0, -900);
    await expect.poll(async () => difference(original, await sample(page)), { intervals: [25, 50, 100] }).toBeLessThan(Math.min(5, downDifference / 2));
    returnToInitialPictureMs = performance.now() - returnStarted;
    returnedDifference = difference(original, await sample(page));
    await browserLiveVideo(page).screenshot({ path: info.outputPath('landing-returned.png') });
    checkpoints.push('streamed-content-returned');
    const wheels = await page.evaluate(() => (window as unknown as LandingWindow).__landingProof.wheels);
    expect([...new Set(wheels.map(w => w.route))]).toEqual([mode === 'dedicated' ? 'input-reliable' : 'websocket']);
    expect(wheels.every(w => w.x === 0 && Number.isFinite(w.y))).toBe(true);
    expect(wheels.filter(w => w.y > 0).reduce((sum, w) => sum + w.y, 0)).toBe(1800);
    expect(wheels.filter(w => w.y < 0).reduce((sum, w) => sum + w.y, 0)).toBe(-1800);
    expect((await routeEvidence(page)).sameMedia).toBe(true); expect(errors).toEqual([]);
    expect(downVisibleChangeMs).toBeGreaterThan(0); expect(returnToInitialPictureMs).toBeGreaterThan(0);
  } finally {
    await fs.mkdir(info.outputDir, { recursive: true });
    const wheels = await page.evaluate(() => (window as unknown as LandingWindow).__landingProof).catch(() => null);
    const routes = await routeEvidence(page).catch(() => null);
    let cleanupError: string | null = null;
    try { await page.getByRole('button', { name: 'Close live browser panel', exact: true }).click({ timeout: 5000 }); } catch { cleanupError = 'Panel close failed; context teardown follows'; }
    await fs.writeFile(info.outputPath('landing-evidence.json'), JSON.stringify({ mode, target, provenance: JSON.parse(await fs.readFile(process.env.BROWSER_INPUT_PROVENANCE!, 'utf8')), checkpoints, errors, cleanupError, wheels, routes, downDifference, returnedDifference, scrollFeedback: { downVisibleChangeMs, returnToInitialPictureMs, clock: 'runner performance.now', pollIntervalsMs: [25, 50, 100] }, limits: ['Visual scroll-and-return feedback; screenshots require review.', 'Outgoing wheel totals do not prove an exact remote scroll offset because page bounds clamp scrolling.', 'Luma threshold is a visual-change heuristic, not OCR or DOM confirmation.', 'Timing starts after navigation, picture readiness and pointer placement; it includes two wheel commands, viewer/runner overhead and sampled-image polling.', 'These are observed visual-feedback/catch-up durations, not pure transport latency, exact scroll-offset completion or a statistical performance gate.'] }, null, 2));
  }
});

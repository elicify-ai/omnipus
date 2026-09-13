import fs from 'node:fs';
import { fileURLToPath } from 'node:url';
import { randomInt } from 'node:crypto';
import { expect, test, type Page } from '@playwright/test';
import { browserLiveFrame, browserLivePanel, browserLiveVideo, selectAgent } from '../e2e/fixtures/selectors';
import { disconnect, installPixels, instrumentRoutes, point, routeEvidence, stateIs, type InputState } from './input-connection-probe';

type Stage = 'media-close' | 'before-offer' | 'answer-pending';
type FrameIdentity = { type: string; input_epoch?: number; control_epoch?: number; offer_id?: number; kind?: string; ok?: boolean };
type FinalProbe = {
  held: boolean; arm(): void; release(): void; sent: FrameIdentity[]; received: FrameIdentity[];
  bindInput(): void; sameInput(): boolean; closeMedia(): void;
};
type FinalWindow = Window & { __browserFinal: FinalProbe };
const fixture = fs.readFileSync(fileURLToPath(new URL('./input-connection-fixture.html', import.meta.url)), 'utf8');
const fixtureURL = new URL(JSON.parse(fs.readFileSync(process.env.BROWSER_INPUT_FIXTURE_CONFIG!, 'utf8')).url);
if (fixtureURL.origin !== 'https://uat-omnipus.fly.dev' || !fixtureURL.pathname.startsWith('/preview/') || fixtureURL.username || fixtureURL.password || fixtureURL.search || fixtureURL.hash) throw Error('Exact approved UAT fixture URL required');
const provenance = JSON.parse(fs.readFileSync(process.env.BROWSER_INPUT_PROVENANCE!, 'utf8'));
if (!/^[a-f0-9]{9,40}$/.test(provenance.source) || !/^[a-f0-9]{64}$/.test(provenance.binarySHA256)) throw Error('Verified deployed source and binary provenance required');

async function instrumentFault(page: Page, stage: Stage) {
  await instrumentRoutes(page);
  await page.addInitScript((fault: Stage) => {
    const peers: Array<{ pc: RTCPeerConnection; channels: RTCDataChannel[] }> = [];
    let firstInput: RTCPeerConnection | null = null;
    let armed = false;
    let input: { pc: RTCPeerConnection; channels: RTCDataChannel[] } | undefined;
    let release = () => {};
    const sent: FrameIdentity[] = [], received: FrameIdentity[] = [];
    const identity = (data: unknown): FrameIdentity | null => {
      if (typeof data !== 'string') return null;
      let f; try { f = JSON.parse(data); } catch { return null; }
      return { type: f.type, input_epoch: f.input_epoch, control_epoch: f.control_epoch, offer_id: f.offer_id, kind: f.kind, ok: f.ok };
    };
    const probe: FinalProbe = {
      held: false, sent, received, arm() { armed = true; firstInput = null; }, release: () => release(),
      bindInput() {
        const active = peers.filter(row => row.channels.some(ch => ch.label === 'input-reliable') && row.pc.connectionState === 'connected');
        if (active.length !== 1) throw Error('Exactly one connected input peer required');
        input = active[0];
      },
      sameInput() {
        return !!input && input.pc.connectionState === 'connected' && input.channels.length === 2 && input.channels.every(ch => ch.readyState === 'open') && peers.filter(row => row.channels.some(ch => ch.label === 'input-reliable')).length === 1;
      },
      closeMedia() {
        const media = peers.filter(row => row.pc.connectionState === 'connected' && row.pc.getTransceivers().some(t => t.receiver.track.kind === 'video'));
        if (media.length !== 1 || media[0] === input) throw Error('Exactly one separate connected media peer required');
        media[0].pc.close(); // Real receiver transport closure; no fabricated state events.
      },
    };
    const hold = () => { probe.held = true; return new Promise<void>(resolve => { release = () => { probe.held = false; resolve(); }; }); };
    const NativePeer = window.RTCPeerConnection;
    window.RTCPeerConnection = class extends NativePeer {
      constructor(...args: ConstructorParameters<typeof NativePeer>) { super(...args); peers.push({ pc: this, channels: [] }); }
      createDataChannel(label: string, options?: RTCDataChannelInit) {
        const channel = super.createDataChannel(label, options);
        peers.find(row => row.pc === this)!.channels.push(channel);
        if (label === 'input-reliable' && firstInput === null) firstInput = this;
        return channel;
      }
      createOffer(options?: RTCOfferOptions): Promise<RTCSessionDescriptionInit>;
      createOffer(success: RTCSessionDescriptionCallback, failure: RTCPeerConnectionErrorCallback, options?: RTCOfferOptions): Promise<void>;
      async createOffer(options?: RTCOfferOptions | RTCSessionDescriptionCallback, failure?: RTCPeerConnectionErrorCallback, legacyOptions?: RTCOfferOptions): Promise<RTCSessionDescriptionInit | void> {
        if (armed && fault === 'before-offer' && this === firstInput) await hold();
        if (typeof options === 'function') return super.createOffer(options, failure!, legacyOptions);
        return super.createOffer(options);
      }
      async setRemoteDescription(description: RTCSessionDescriptionInit) {
        if (armed && fault === 'answer-pending' && this === firstInput) await hold();
        return super.setRemoteDescription(description);
      }
    };
    const NativeSocket = window.WebSocket;
    window.WebSocket = class extends NativeSocket {
      private browserSocket: boolean;
      constructor(...args: ConstructorParameters<typeof NativeSocket>) {
        super(...args); this.browserSocket = new URL(String(args[0]), location.href).pathname === '/api/v1/browser/ws';
        if (this.browserSocket) this.addEventListener('message', event => { const f = identity(event.data); if (f) received.push(f); });
      }
      send(data: Parameters<WebSocket['send']>[0]) { super.send(data); if (this.browserSocket) { const f = identity(data); if (f) sent.push(f); } }
    };
    (window as unknown as FinalWindow).__browserFinal = probe;
  }, stage);
}
async function ready(page: Page) {
  await expect(page.locator('[data-input-mode="dedicated"]')).toHaveAttribute('data-input-state', 'ready');
  await expect(browserLivePanel(page).getByRole('status').filter({ hasText: /Waiting for the current page|Pointer input is unavailable|Browser input is unavailable|Reconnecting video to restore browser input/ })).toHaveCount(0);
  await expect(browserLivePanel(page).getByRole('alert')).toHaveCount(0);
}
async function clickFixture(page: Page, state: InputState) {
  await ready(page);
  const p = await point(page, .73, .68);
  await page.mouse.click(p.x, p.y);
  const next = { ...state, clicks: state.clicks + 1, downs: state.downs + 1, ups: state.ups + 1 };
  await stateIs(page, next);
  return next;
}

for (const stage of ['media-close', 'before-offer', 'answer-pending'] as const) test(`dedicated ${stage}: exact input survives the independent lifecycle`, async ({ page }, info) => {
  const errors: string[] = [], checkpoints: string[] = [];
  let state: InputState = { nonce: randomInt(1, 65536), clicks: 0, downs: 0, ups: 0, held: 0, scroll: 0, drags: 0, errors: 0, text: '' };
  page.on('pageerror', error => errors.push(error.message));
  await instrumentFault(page, stage);
  try {
    const served = await page.request.get(fixtureURL.href, { maxRedirects: 0 });
    expect(served.status()).toBe(200); expect(await served.text()).toBe(fixture);
    await page.goto('/workspaces/01M01TTSDZBFGM28NPHGTFZ17T/chat?browserInput=dedicated');
    await selectAgent(page, 'Browser UAT Test');
    expect(new URL(page.url()).searchParams.get('browserInput')).toBe('dedicated');
    await page.getByRole('button', { name: 'Open browser', exact: true }).click();
    await expect(browserLivePanel(page)).toBeVisible();
    await expect.poll(() => browserLiveVideo(page).evaluate(v => (v as HTMLVideoElement).readyState), { timeout: 45000 }).toBeGreaterThanOrEqual(2);
    const target = new URL(fixtureURL); target.searchParams.set('nonce', String(state.nonce));
    const address = page.getByRole('textbox', { name: 'Address bar' });
    await ready(page); await address.fill(target.href); await address.press('Enter');
    await installPixels(page); await stateIs(page, state); await ready(page);
    if (stage !== 'media-close') {
      const before = await page.evaluate(() => (window as unknown as FinalWindow).__browserFinal.sent);
      const priorOffers = before.filter(f => f.type === 'browser_input_offer');
      const priorEpoch = priorOffers.at(-1)!.input_epoch!;
      await page.evaluate(() => (window as unknown as FinalWindow).__browserFinal.arm());
      await disconnect(page, 'dedicated');
      await expect(page.getByTestId('browser-input-error')).toBeVisible();
      await page.getByRole('button', { name: 'Retry input', exact: true }).click();
      await expect.poll(() => page.evaluate(() => (window as unknown as FinalWindow).__browserFinal.held)).toBe(true);
      // Failure retirement completes before Retry starts this held attempt.
      // Derive navigation ownership after that real control acknowledgment.
      const negotiationFrames = await page.evaluate(() => (window as unknown as FinalWindow).__browserFinal.sent);
      const priorControl = Math.max(0, ...negotiationFrames.map(f => f.control_epoch ?? 0));
      const expectedEpoch = stage === 'before-offer' ? priorEpoch : priorEpoch + 1;
      const expectedControl = priorControl + 1;
      await address.fill(target.href); await address.press('Enter');
      await expect.poll(() => page.evaluate(() => (window as unknown as FinalWindow).__browserFinal.sent.filter(f => f.type === 'browser_input' && f.kind === 'navigate').at(-1))).toMatchObject({ input_epoch: expectedEpoch, control_epoch: expectedControl });
      await expect.poll(() => page.evaluate(() => (window as unknown as FinalWindow).__browserFinal.received.filter(f => f.type === 'browser_input_control_ack').at(-1))).toMatchObject({ input_epoch: expectedEpoch, control_epoch: expectedControl, ok: true });
      await page.evaluate(() => (window as unknown as FinalWindow).__browserFinal.release());
      await ready(page);
      const offers = await page.evaluate(() => (window as unknown as FinalWindow).__browserFinal.sent.filter(f => f.type === 'browser_input_offer'));
      expect(offers.slice(priorOffers.length).map(f => [f.input_epoch, f.control_epoch])).toEqual(stage === 'before-offer' ? [[priorEpoch + 2, expectedControl]] : [[priorEpoch + 1, priorControl]]);
      expect((await routeEvidence(page)).sameMedia).toBe(true);
      checkpoints.push('retry-startup-control-ack-and-peer-ready');
    }
    await installPixels(page); await stateIs(page, state); await ready(page);
    state = await clickFixture(page, state); checkpoints.push('first-exact-click');
    if (stage === 'media-close') {
      await page.evaluate(() => (window as unknown as FinalWindow).__browserFinal.bindInput());
      const before = await routeEvidence(page);
      await browserLiveFrame(page).focus();
      await page.evaluate(() => (window as unknown as FinalWindow).__browserFinal.closeMedia());
      // The real picture gate must close before we attempt input on unsafe pixels.
      await expect(browserLivePanel(page).getByRole('status').filter({ hasText: /Waiting for the current page|Pointer input is unavailable|Browser input is unavailable|Reconnecting video to restore browser input/ }).or(page.locator('[data-testid="browser-live-retry"], [data-testid="browser-live-retry-overlay"]')).first()).toBeVisible({ timeout: 45000 });
      expect(await page.evaluate(() => (window as unknown as FinalWindow).__browserFinal.sameInput())).toBe(true);
      const routesBefore = (await routeEvidence(page)).routes.length;
      await page.keyboard.press('x');
      expect((await routeEvidence(page)).routes).toHaveLength(routesBefore);
      checkpoints.push('unsafe-picture-input-blocked-same-input-peer');
      const retry = page.locator('[data-testid="browser-live-retry"], [data-testid="browser-live-retry-overlay"]').first();
      if (await retry.isVisible()) await retry.click();
      await ready(page);
      await expect.poll(() => browserLiveVideo(page).evaluate(v => (v as HTMLVideoElement).readyState), { timeout: 45000 }).toBeGreaterThanOrEqual(2);
      await installPixels(page); await stateIs(page, state);
      expect(await page.evaluate(() => (window as unknown as FinalWindow).__browserFinal.sameInput())).toBe(true);
      expect((await routeEvidence(page)).openedSockets).toBe(before.openedSockets);
      state = await clickFixture(page, state); checkpoints.push('media-recovered-exact-click-same-input-and-socket');
    }
    const evidence = await routeEvidence(page);
    expect(evidence.routes.filter(f => f.route === 'websocket')).toEqual([]);
    expect(evidence.routes.filter(f => f.route === 'input-reliable' && f.kind === 'mouse_down')).toHaveLength(state.downs);
    expect(evidence.routes.filter(f => f.route === 'input-reliable' && f.kind === 'mouse_up')).toHaveLength(state.ups);
    expect(errors).toEqual([]);
  } finally {
    await fs.promises.mkdir(info.outputDir, { recursive: true });
    const identities = await page.evaluate(() => { const p = (window as unknown as FinalWindow).__browserFinal; return p && { sent: p.sent, received: p.received, held: p.held }; }).catch(() => null);
    let cleanupError: string | null = null;
    try { await page.getByRole('button', { name: 'Close live browser panel', exact: true }).click({ timeout: 5000 }); }
    catch { cleanupError = 'Panel close failed; Playwright context teardown still follows'; }
    await fs.promises.writeFile(info.outputPath('inverse-evidence.json'), JSON.stringify({ cleanupError, stage, provenance, state, checkpoints, errors, identities, routes: await routeEvidence(page).catch(() => null), limits: ['Controlled receiver closure and bounded negotiation stalls, not natural packet loss.', 'No performance or audible-content claim.', 'Startup runs during input Retry after stable media, avoiding unrelated initial viewport traffic; initial epoch-zero ordering has separate unit proof.', 'Pending-answer holds native description application after the real answer arrives; server pre-install ordering requires separate Go proof.'] }, null, 2));
  }
});

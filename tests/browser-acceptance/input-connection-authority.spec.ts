import fs from 'node:fs';
import { fileURLToPath } from 'node:url';
import { randomInt } from 'node:crypto';
import { expect, test } from '@playwright/test';
import type { BrowserInputFrame } from '../../src/lib/api/generated/asyncapi-types';
import { browserLivePanel, browserLiveVideo, selectAgent } from '../e2e/fixtures/selectors';
import { installPixels, instrumentRoutes, point, routeEvidence, stateIs, type InputState } from './input-connection-probe';

type CaptureClaim = { channel: number; mutated: boolean; original: BrowserInputFrame; sent: BrowserInputFrame };
type AuthorityProbe = { arm(): void; records: CaptureClaim[]; remaining(): number; sameChannel(): boolean };
type AuthorityWindow = Window & { __captureAuthority: AuthorityProbe };
const fixture = fs.readFileSync(fileURLToPath(new URL('./input-connection-fixture.html', import.meta.url)), 'utf8');
const fixtureURL = new URL(JSON.parse(fs.readFileSync(process.env.BROWSER_INPUT_FIXTURE_CONFIG!, 'utf8')).url);
if (fixtureURL.origin !== 'https://uat-omnipus.fly.dev' || !fixtureURL.pathname.startsWith('/preview/') || fixtureURL.username || fixtureURL.password || fixtureURL.search || fixtureURL.hash) throw Error('Exact approved UAT fixture URL required');
const provenance = JSON.parse(fs.readFileSync(process.env.BROWSER_INPUT_PROVENANCE!, 'utf8'));
if (!/^[a-f0-9]{9,40}$/.test(provenance.source) || !/^[a-f0-9]{64}$/.test(provenance.binarySHA256)) throw Error('Verified deployed provenance required');

test('dedicated input refuses wrong capture claims before a valid ordered click completes', async ({ page }, info) => {
  const errors: string[] = [], checkpoints: string[] = [];
  const initial: InputState = { nonce: randomInt(1, 65536), clicks: 0, downs: 0, ups: 0, held: 0, scroll: 0, drags: 0, errors: 0, text: '' };
  page.on('pageerror', error => errors.push(error.message));
  await instrumentRoutes(page);
  await page.addInitScript(() => {
    const channels: Array<{ id: number; pc: RTCPeerConnection; channel: RTCDataChannel }> = [];
    const records: CaptureClaim[] = [];
    let armed: typeof channels[number] | undefined, remaining = 0;
    const NativePeer = window.RTCPeerConnection;
    window.RTCPeerConnection = class extends NativePeer {
      createDataChannel(label: string, options?: RTCDataChannelInit) {
        const channel = super.createDataChannel(label, options);
        if (label !== 'input-reliable') return channel;
        const entry = { id: channels.length + 1, pc: this, channel };
        channels.push(entry);
        const send = channel.send.bind(channel);
        channel.send = ((data: string) => {
          if (typeof data !== 'string') throw Error('Expected the actual UI JSON frame');
          const original = JSON.parse(data) as BrowserInputFrame;
          if (original.type !== 'browser_input' || !['mouse_down', 'mouse_up'].includes(original.kind)) { send(data); return; }
          let sent = original, mutated = false;
          if (remaining > 0) {
            if (entry !== armed || original.kind !== (remaining === 2 ? 'mouse_down' : 'mouse_up')) throw Error('Unexpected channel or click ordering during capture fault');
            const capture = original.capture_id;
            if (!capture || !/^[a-f0-9]{64}$/.test(capture)) throw Error('A valid current capture claim is required before mutation');
            // Change only the capture claim, preserving live sequence/control/coordinates.
            sent = { ...original, capture_id: (capture[0] === '0' ? '1' : '0') + capture.slice(1) };
            mutated = true;
            remaining--;
          }
          send(mutated ? JSON.stringify(sent) : data);
          records.push({ channel: entry.id, mutated, original, sent });
        }) as typeof channel.send;
        return channel;
      }
    };
    (window as unknown as AuthorityWindow).__captureAuthority = {
      records, remaining: () => remaining,
      arm() {
        const active = channels.filter(row => row.pc.connectionState === 'connected' && row.channel.readyState === 'open');
        if (active.length !== 1 || active[0].pc.getTransceivers().length !== 0 || records.length !== 0) throw Error('One unused, connected, data-only input channel required');
        armed = active[0]; remaining = 2;
      },
      sameChannel() { return !!armed && channels.length === 1 && armed.channel.readyState === 'open' && armed.pc.connectionState === 'connected'; },
    };
  });
  const ready = async () => {
    await expect(page.locator('[data-input-mode="dedicated"]')).toHaveAttribute('data-input-state', 'ready');
    await expect(browserLivePanel(page).getByRole('status').filter({ hasText: /Waiting for the current page|Pointer input is unavailable|Browser input is unavailable|Reconnecting video to restore browser input/ })).toHaveCount(0);
    await expect(browserLivePanel(page).getByRole('alert')).toHaveCount(0);
  };
  try {
    const served = await page.request.get(fixtureURL.href, { maxRedirects: 0 });
    expect(served.status()).toBe(200); expect(await served.text()).toBe(fixture);
    await page.goto('/workspaces/01M01TTSDZBFGM28NPHGTFZ17T/chat?browserInput=dedicated');
    await selectAgent(page, 'Browser UAT Test');
    expect(new URL(page.url()).searchParams.get('browserInput')).toBe('dedicated');
    await page.getByRole('button', { name: 'Open browser', exact: true }).click();
    await expect(browserLivePanel(page)).toBeVisible(); await ready();
    await expect.poll(() => browserLiveVideo(page).evaluate(v => (v as HTMLVideoElement).readyState), { timeout: 45000 }).toBeGreaterThanOrEqual(2);
    const target = new URL(fixtureURL); target.searchParams.set('nonce', String(initial.nonce));
    const address = page.getByRole('textbox', { name: 'Address bar' });
    await address.fill(target.href); await address.press('Enter');
    await installPixels(page); await stateIs(page, initial); await ready();
    checkpoints.push('exact-fixture-ready');
    const originalSockets = (await routeEvidence(page)).openedSockets;
    const rejectedLocation = await point(page, .25, .68);
    const validLocation = await point(page, .73, .68);
    await page.evaluate(() => (window as unknown as AuthorityWindow).__captureAuthority.arm());
    await page.mouse.click(rejectedLocation.x, rejectedLocation.y);
    expect(await page.evaluate(() => (window as unknown as AuthorityWindow).__captureAuthority.remaining())).toBe(0);
    // No short "nothing changed" assertion: completion of the following valid
    // button click uniquely witnesses completion: the rejected click targets
    // the text field and cannot itself increment the fixture's click counter.
    await ready(); await page.mouse.click(validLocation.x, validLocation.y);
    const expected = { ...initial, clicks: 1, downs: 1, ups: 1 };
    await stateIs(page, expected);
    checkpoints.push('later-valid-click-completed-exactly-once');
    const claims = await page.evaluate(() => (window as unknown as AuthorityWindow).__captureAuthority.records);
    expect(claims).toHaveLength(4);
    expect(claims.map(r => [r.mutated, r.sent.kind])).toEqual([[true, 'mouse_down'], [true, 'mouse_up'], [false, 'mouse_down'], [false, 'mouse_up']]);
    expect([...new Set(claims.map(r => r.channel))]).toHaveLength(1);
    const first = claims[0].original;
    expect(first.reliable_seq).toBeGreaterThanOrEqual(1);
    for (const [index, claim] of claims.entries()) {
      expect(claim.original).toMatchObject({ input_epoch: first.input_epoch, control_epoch: first.control_epoch, capture_id: first.capture_id, capture_generation: first.capture_generation });
      expect(claim.original.reliable_seq).toBe(first.reliable_seq! + index);
      expect(claim.sent).toEqual(claim.mutated ? { ...claim.original, capture_id: (first.capture_id![0] === '0' ? '1' : '0') + first.capture_id!.slice(1) } : claim.original);
    }
    expect(claims[0].original.x).not.toBe(claims[2].original.x);
    expect(await page.evaluate(() => (window as unknown as AuthorityWindow).__captureAuthority.sameChannel())).toBe(true);
    const routes = await routeEvidence(page);
    expect(routes.openedSockets).toBe(originalSockets); expect(routes.sameMedia).toBe(true);
    expect(routes.routes.filter(r => r.route === 'websocket')).toEqual([]);
    expect(routes.routes.filter(r => r.kind === 'mouse_down' || r.kind === 'mouse_up').map(r => r.route)).toEqual(['input-reliable', 'input-reliable', 'input-reliable', 'input-reliable']);
    expect(errors).toEqual([]);
    await browserLiveVideo(page).screenshot({ path: info.outputPath('authority-final-video.png') });
  } finally {
    await fs.promises.mkdir(info.outputDir, { recursive: true });
    const claims = await page.evaluate(() => { const p = (window as unknown as AuthorityWindow).__captureAuthority; return p && { records: p.records, remaining: p.remaining(), sameChannel: p.sameChannel() }; }).catch(() => null);
    const routes = await routeEvidence(page).catch(() => null);
    let cleanupError: string | null = null;
    try { await page.getByRole('button', { name: 'Close live browser panel', exact: true }).click({ timeout: 5000 }); } catch { cleanupError = 'Panel close failed; context teardown follows'; }
    await fs.promises.writeFile(info.outputPath('authority-evidence.json'), JSON.stringify({ provenance, initial, expected: { ...initial, clicks: 1, downs: 1, ups: 1 }, checkpoints, errors, cleanupError, claims, routes, limits: ['Wrong-capture refusal proof only; not expired credentials or another viewer impersonation.', 'Later valid action on the same ordered channel is the completion witness; no rejection acknowledgment is expected.', 'No target CDP input, direct target DOM injection, or interaction latency claim.'] }, null, 2));
  }
});

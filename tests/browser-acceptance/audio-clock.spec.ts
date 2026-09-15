import fs from 'node:fs';
import { fileURLToPath } from 'node:url';
import { expect, test } from '@playwright/test';
import { browserLiveFrame, browserLivePanel, browserLiveVideo, selectAgent } from '../e2e/fixtures/selectors';

import { instrumentRoutes, routeEvidence } from './input-connection-probe';
import { browserTestWorkspacePath } from './test-workspace';

const fixture = fs.readFileSync(fileURLToPath(new URL('./audio-clock-fixture.html', import.meta.url)), 'utf8');
const target = new URL(JSON.parse(fs.readFileSync(process.env.BROWSER_INPUT_FIXTURE_CONFIG!, 'utf8')).url);
if (target.origin !== 'https://uat-omnipus.fly.dev' || !target.pathname.startsWith('/preview/') || target.search || target.hash || target.username || target.password) throw Error('Exact approved fixture URL required');
const provenance = JSON.parse(fs.readFileSync(process.env.BROWSER_INPUT_PROVENANCE!, 'utf8'));
type AudioWindow = Window & { __audioClockPeers: RTCPeerConnection[] };

test('same peer silence-tone-silence with viewer muted then unmuted', async ({ page }, info) => {
  const observations: unknown[] = [], errors: string[] = [];
  page.on('pageerror', error => errors.push(error.message));
  await instrumentRoutes(page);
  await page.addInitScript(() => {
    const peers: RTCPeerConnection[] = [];
    const Native = window.RTCPeerConnection;
    window.RTCPeerConnection = class extends Native { constructor(...args: ConstructorParameters<typeof Native>) { super(...args); peers.push(this); } };
    (window as unknown as AudioWindow).__audioClockPeers = peers;
  });
  const read = () => browserLiveVideo(page).evaluate(async node => {
    const video = node as HTMLVideoElement, stream = video.srcObject as MediaStream;
    const canvas = document.createElement('canvas'); canvas.width = video.videoWidth; canvas.height = video.videoHeight;
    const ctx = canvas.getContext('2d')!; ctx.drawImage(video, 0, 0);
    const pixel = (x: number, y: number) => Array.from(ctx.getImageData(Math.floor(x * canvas.width), Math.floor(y * canvas.height), 1, 1).data).slice(0, 3);
    const signature = pixel(.1, .92);
    const fixture = signature[0] > 200 && signature[1] > 80 && signature[1] < 170 && signature[2] < 50;
    const phaseColor = pixel(.1, .08), running = pixel(.5, .08), energy = pixel(.85, .08);
    const phase = phaseColor[1] > 180 && phaseColor[0] < 80 ? 1 : phaseColor[2] > 180 && phaseColor[0] < 80 ? 2 : phaseColor[0] > 180 && phaseColor[2] < 80 ? 0 : -1;
    let samples = 0;
    for (let bit = 0; bit < 24; bit++) if (pixel(.1 + .8 * (bit + .5) / 24, .25)[0] > 128) samples += 2 ** bit;
    const peers = (window as unknown as AudioWindow).__audioClockPeers;
    const media = peers.filter(pc => pc.connectionState === 'connected' && pc.getReceivers().some(r => stream.getTracks().includes(r.track)));
    if (media.length !== 1) throw Error('Exactly one media peer must own the displayed tracks');
    const pc = media[0], report = await pc.getStats();
    const stats: Record<string, unknown>[] = [];
    for (const row of report.values()) {
      if (row.type !== 'inbound-rtp') continue;
      const out: Record<string, unknown> = { kind: row.kind };
      for (const key of ['timestamp', 'framesDecoded', 'framesDropped', 'packetsReceived', 'packetsLost', 'jitter', 'jitterBufferDelay', 'jitterBufferTargetDelay', 'jitterBufferMinimumDelay', 'jitterBufferEmittedCount', 'totalDecodeTime', 'totalProcessingDelay', 'totalSamplesReceived', 'concealedSamples', 'silentConcealedSamples', 'insertedSamplesForDeceleration', 'removedSamplesForAcceleration', 'estimatedPlayoutTimestamp', 'audioLevel', 'totalAudioEnergy', 'totalSamplesDuration']) if (typeof row[key] === 'number' && Number.isFinite(row[key])) out[key] = row[key];
      stats.push(out);
    }
    return { atMs: performance.now(), decodedWidth: video.videoWidth, decodedHeight: video.videoHeight, viewerWidth: video.clientWidth, viewerHeight: video.clientHeight, fixture, phase, running: running[1] > 180 && running[0] < 80, tone: energy[1] > 180 && energy[0] < 80, samples, muted: video.muted, paused: video.paused, peer: peers.indexOf(pc), tracks: stream.getTracks().map(t => ({ kind: t.kind, state: t.readyState })), stats };
  });
  try {
    const served = await page.request.get(target.href, { maxRedirects: 0 }); expect(served.status()).toBe(200); expect(await served.text()).toBe(fixture);
    await page.goto(browserTestWorkspacePath);
    await expect(page).toHaveURL(url => url.hash === browserTestWorkspacePath.slice(1)); await selectAgent(page, 'Browser UAT Test');
    const startupAt = Date.now();
    await page.getByRole('button', { name: 'Open browser', exact: true }).click(); await expect(browserLivePanel(page)).toBeVisible();
    await expect(page.locator('[data-input-mode="dedicated"]')).toHaveAttribute('data-input-state', 'ready', { timeout: 45000 });
    observations.push({ checkpoint: 'initial-ready', startupMs: Date.now() - startupAt });
    const address = page.getByRole('textbox', { name: 'Address bar' }); await address.fill(target.href); await address.press('Enter');
    await expect.poll(async () => { try { const s = await read(); return s.fixture && s.phase === -1; } catch { return false; } }, { timeout: 45000 }).toBe(true);
    let owner: number | undefined;
    for (const muted of [true, false]) {
      await browserLiveVideo(page).evaluate((node, value) => { (node as HTMLVideoElement).muted = value; }, muted);
      for (const phase of [0, 1, 2]) {
        await expect(page.locator('[data-input-mode="dedicated"]')).toHaveAttribute('data-input-state', 'ready');
        await expect(browserLivePanel(page).getByRole('status').filter({ hasText: /Waiting for the current page|Pointer input is unavailable|Browser input is unavailable|Reconnecting video to restore browser input/ })).toHaveCount(0);
        await expect(browserLivePanel(page).getByRole('alert')).toHaveCount(0);
        const routesBefore = (await routeEvidence(page)).routes.length;
        const center = await browserLiveVideo(page).evaluate(node => { const r = node.getBoundingClientRect(); return { x: r.left + r.width / 2, y: r.top + r.height / 2 }; });
        await expect(browserLiveFrame(page)).toBeVisible();
        observations.push({ checkpoint: 'before-click', requestedPhase: phase, requestedMuted: muted, center, sample: await read() });
        await page.mouse.click(center.x, center.y);
        const sent = (await routeEvidence(page)).routes.slice(routesBefore).filter(row => row.kind === 'mouse_down' || row.kind === 'mouse_up');
        observations.push({ checkpoint: 'click-routes', sent });
        expect(sent.map(({ route, kind }) => ({ route, kind }))).toEqual([{ route: 'input-reliable', kind: 'mouse_down' }, { route: 'input-reliable', kind: 'mouse_up' }]);
        await expect.poll(async () => { const s = await read(); return { phase: s.phase, running: s.running, tone: s.tone }; }).toEqual({ phase, running: true, tone: phase === 1 });
        const first = await read(); owner ??= first.peer;
        let stableStart: Awaited<ReturnType<typeof read>> | undefined;
        for (let second = 0; second < 20; second++) {
          await page.waitForTimeout(1000);
          const sample = await read(); observations.push({ requestedPhase: phase, requestedMuted: muted, ...sample });
          if (second === 9) stableStart = sample;
          expect(sample.fixture).toBe(true); expect(sample.peer).toBe(owner); expect(sample.phase).toBe(phase); expect(sample.running).toBe(true); expect(sample.tone).toBe(phase === 1); expect(sample.muted).toBe(muted); expect(sample.paused).toBe(false);
          expect(sample.tracks).toEqual(expect.arrayContaining([{ kind: 'audio', state: 'live' }, { kind: 'video', state: 'live' }]));
        }
        expect((await read()).samples).toBeGreaterThan(first.samples);
        const stableEnd = await read();
        // Use the final ~10s of each phase: the initial 10s allow the source
        // gain ramp, codec transitions and buffered pre-transition audio out.
        // Fixture: sine amplitude .02 => expected mean-square power .02²/2.
        // Permit substantial codec/gain variation: tone >=5% of that power;
        // silence <=0.5%. Source pixels alone cannot satisfy these assertions.
        const counters = (sample: Awaited<ReturnType<typeof read>>) => {
          const audio = sample.stats.filter(row => row.kind === 'audio');
          expect(audio, 'one inbound audio stream must supply receiver energy').toHaveLength(1);
          const values = Object.fromEntries(['totalAudioEnergy', 'totalSamplesDuration', 'totalSamplesReceived', 'packetsReceived'].map(key => {
            const value = audio[0][key];
            expect(typeof value, `required receiver metric ${key}`).toBe('number');
            expect(Number.isFinite(value), `finite receiver metric ${key}`).toBe(true);
            expect(Number(value), `nonnegative receiver metric ${key}`).toBeGreaterThanOrEqual(0);
            return [key, Number(value)];
          }));
          return values;
        };
        expect(stableStart, 'stable receiver observation must exist').toBeDefined();
        const before = counters(stableStart!), after = counters(stableEnd);
        const receivedPackets = after.packetsReceived - before.packetsReceived;
        const receivedSamples = after.totalSamplesReceived - before.totalSamplesReceived;
        const receivedDuration = after.totalSamplesDuration - before.totalSamplesDuration;
        const receivedEnergy = after.totalAudioEnergy - before.totalAudioEnergy;
        expect(receivedPackets, 'audio packets must advance even while viewer is muted').toBeGreaterThan(0);
        expect(receivedSamples, 'audio samples must advance even while viewer is muted').toBeGreaterThan(0);
        expect(receivedDuration).toBeGreaterThan(0);
        expect(receivedEnergy).toBeGreaterThanOrEqual(0);
        const meanSquare = receivedEnergy / receivedDuration;
        const authoredTonePower = 0.02 ** 2 / 2;
        observations.push({ checkpoint: 'receiver-energy', requestedPhase: phase, requestedMuted: muted,
          stableStartMs: stableStart!.atMs, stableEndMs: stableEnd.atMs, receivedPackets, receivedSamples, receivedDuration, receivedEnergy, meanSquare });
        // LibWebRTC ChannelReceive measures output energy after volume scaling:
        // muted playback must be silent even when the received source is a tone.
        // https://webrtc.googlesource.com/src/+/refs/heads/main/audio/channel_receive.cc
        if (!muted && phase === 1) expect(meanSquare, 'receiver must contain the authored tone energy').toBeGreaterThanOrEqual(authoredTonePower * 0.05);
        else expect(meanSquare, 'receiver must return to silence').toBeLessThanOrEqual(authoredTonePower * 0.005);
      }
    }
    expect(errors).toEqual([]);
  } finally {
    const final = await read().catch(() => null);
    const routing = await routeEvidence(page).catch(() => null);
    fs.writeFileSync(info.outputPath('audio-clock-evidence.json'), JSON.stringify({ provenance, testWorkspace: browserTestWorkspacePath, observations, errors, final, routing }, null, 2));
    await info.attach('audio-clock-evidence', { path: info.outputPath('audio-clock-evidence.json'), contentType: 'application/json' });
    await page.getByRole('button', { name: 'Close live browser panel', exact: true }).click({ timeout: 5000 }).catch(() => {});
  }
});

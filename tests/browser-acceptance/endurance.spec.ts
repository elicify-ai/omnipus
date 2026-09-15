import { browserTestWorkspacePath } from './test-workspace';
import { installAudioPressureProbe, sampleAudioPressure } from './audio-pressure-probe';
import fs from 'node:fs';
import { fileURLToPath } from 'node:url';
import { randomInt } from 'node:crypto';
import { expect, test } from '@playwright/test';
import { browserLiveFrame, browserLivePanel, selectAgent } from '../e2e/fixtures/selectors';
import { installPixels, instrumentRoutes, point, routeEvidence, stateIs, type InputState } from './input-connection-probe';

// User acceptance: at least twenty minutes of real mixed input with an independent video oracle.
// Expected counters derive from authored gestures, independently of dispatch logs.
const stimulusMode = process.env.BROWSER_ENDURANCE_AUDIO_STIMULUS;
if (stimulusMode !== undefined && stimulusMode !== '0' && stimulusMode !== '1') throw Error('BROWSER_ENDURANCE_AUDIO_STIMULUS must be 0 or 1');
const audioStimulus = stimulusMode === '1';
const fixture = fs.readFileSync(fileURLToPath(new URL(audioStimulus ? './audio-pressure-fixture.html' : './pressure-stress-fixture.html', import.meta.url)), 'utf8');
const target = new URL(JSON.parse(fs.readFileSync(process.env.BROWSER_INPUT_FIXTURE_CONFIG!, 'utf8')).url);
if (target.origin !== 'https://uat-omnipus.fly.dev' || !target.pathname.startsWith('/preview/') || target.search || target.hash || target.username || target.password) throw Error('Exact approved fixture URL required');
const provenance = JSON.parse(fs.readFileSync(process.env.BROWSER_INPUT_PROVENANCE!, 'utf8'));

const seconds = Number(process.env.BROWSER_ENDURANCE_SECONDS || 1200);
if (![15, 120, 1200].includes(seconds)) throw Error('Use 15 seconds for calibration, 120 for diagnosis, or 1200 for acceptance');
if (audioStimulus && seconds !== 1200) throw Error('Audio stimulus requires 1200 seconds for all four fixed phases');
const resizeEvery = Number(process.env.BROWSER_ENDURANCE_RESIZE_EVERY || 12);
if (![2, 12].includes(resizeEvery)) throw Error('Use 2 rounds for accelerated resize diagnosis or 12 for standard endurance');
const audioMode = process.env.BROWSER_ENDURANCE_AUDIO_INACTIVE;
if (audioMode !== undefined && audioMode !== '0' && audioMode !== '1') throw Error('BROWSER_ENDURANCE_AUDIO_INACTIVE must be 0 or 1');
const audioInactive = audioMode === '1';
if (audioStimulus && audioInactive) throw Error('Audio stimulus requires negotiated active audio');
const diagnosticMode = audioStimulus ? 'audio-stimulus' : audioInactive ? 'audio-inactive' : 'normal';
const fullFeatureAcceptanceEligible = !audioStimulus && !audioInactive && seconds === 1200 && resizeEvery === 12;
const delay = 120;
test(`${seconds}-second continuous mixed input endurance (${diagnosticMode})`, async ({ page }, info) => {
  let state: InputState = { nonce: randomInt(1, 65536), clicks: 0, downs: 0, ups: 0, held: 0, scroll: 0, drags: 0, errors: 0, text: '' };
  let rounds = 0, started = 0, clientStart = 0;
  const mediaStats: unknown[] = [];
  const audioStimulusSamples: Array<NonNullable<Awaited<ReturnType<typeof sampleAudioPressure>>>> = [];
  let videoPlayoutNegotiated = false;
  const clientBrowserVersion = page.context().browser()?.version() ?? 'unavailable';
  const errors: string[] = [], marks: Array<{ label: string; at: string; ms?: number }> = [];
  page.on('pageerror', error => errors.push(error.message));
  await instrumentRoutes(page, audioInactive);
  if (audioStimulus) await installAudioPressureProbe(page);
  const ready = async () => {
    await expect(page.locator('[data-input-mode="dedicated"]')).toHaveAttribute('data-input-state', 'ready');
    await expect(browserLivePanel(page).getByRole('status').filter({ hasText: /Waiting for the current page|Pointer input is unavailable|Browser input is unavailable|Reconnecting video to restore browser input/ })).toHaveCount(0);
    await expect(browserLivePanel(page).getByRole('alert')).toHaveCount(0);
  };
  const observe = async (label: string, start: number) => {
    await stateIs(page, state); await ready();
    marks.push({ label, at: new Date().toISOString(), ms: performance.now() - start });
  };
  try {
    const served = await page.request.get(target.href, { maxRedirects: 0 });
    expect(served.status()).toBe(200); expect(await served.text()).toBe(fixture);
    await page.goto(browserTestWorkspacePath);
    await expect(page).toHaveURL(url => url.hash === browserTestWorkspacePath.slice(1)); await selectAgent(page, 'Browser UAT Test');
    await page.getByRole('button', { name: 'Open browser', exact: true }).click(); await ready();
    const url = new URL(target); url.searchParams.set('nonce', String(state.nonce)); url.searchParams.set('delay', String(delay));
    const address = page.getByRole('textbox', { name: 'Address bar' }); await address.fill(url.href); await address.press('Enter');
    await installPixels(page); await stateIs(page, state); await ready();
    if (audioStimulus) {
      await page.locator('[data-testid="browser-live-video"]').evaluate(node => { (node as HTMLVideoElement).muted = false; });
    }
    const initialMediaStats = await page.evaluate(async () => (window as unknown as { __inputSmoke: { mediaStats(): Promise<Array<Record<string, unknown>>> } }).__inputSmoke.mediaStats());
    const videoReceivers = initialMediaStats.filter(row => row.type === 'receiver' && row.kind === 'video' && (row.currentDirection === 'recvonly' || row.currentDirection === 'sendrecv'));
    videoPlayoutNegotiated = videoReceivers.length > 0 && videoReceivers.every(row =>
      Array.isArray(row.headerExtensions) && row.headerExtensions.some((extension: { uri?: string; id?: number }) =>
        extension.uri === 'http://www.webrtc.org/experiments/rtp-hdrext/playout-delay' && Number.isInteger(extension.id) && Number(extension.id) > 0));
    expect(videoPlayoutNegotiated, 'viewer video must negotiate the playout-delay extension before endurance begins').toBe(true);
    // Browser-local sampling avoids a Playwright action/trace every second.
    // Recursive scheduling serializes this sampler's async getStats calls.
    await page.evaluate(() => {
      type Sample = { at: string; startedAt: number; completedAt: number; stats?: unknown; error?: string };
      const w = window as unknown as {
        __inputSmoke: { mediaStats(): Promise<unknown> };
        __stopAudioTimingSampler?: () => { samples: Sample[]; inFlight: boolean; limitReached: boolean; stoppedAt: string };
      };
      w.__stopAudioTimingSampler?.();
      const samples: Sample[] = [];
      let stopped = false, inFlight = false;
      let timer: number | undefined;
      const sample = async () => {
        if (stopped || samples.length >= 1500) return;
        inFlight = true;
        const at = new Date().toISOString(), startedAt = performance.now();
        let stats: unknown, error: string | undefined;
        try { stats = await w.__inputSmoke.mediaStats(); }
        catch (cause) { error = cause instanceof Error ? cause.name : 'UnknownError'; }
        inFlight = false;
        if (stopped) return;
        samples.push({ at, startedAt, completedAt: performance.now(), ...(error ? { error } : { stats }) });
        if (samples.length < 1500) timer = window.setTimeout(() => { void sample(); }, Math.max(0, 1000 - (performance.now() - startedAt)));
      };
      w.__stopAudioTimingSampler = () => {
        stopped = true;
        if (timer !== undefined) window.clearTimeout(timer);
        return { samples: samples.slice(), inFlight, limitReached: samples.length >= 1500, stoppedAt: new Date().toISOString() };
      };
      void sample();
    });
    started = performance.now();
    clientStart = await page.evaluate(() => {
      const w = window as unknown as { __enduranceStates: Array<{at:number;text:string;alerts:string[]}>; __enduranceObserver:MutationObserver };
      w.__enduranceStates = [];
      let previous = '';
      const observeState = () => {
        const input = document.querySelector('[data-input-mode="dedicated"]')?.getAttribute('data-input-state');
        const text = 'input=' + input + ' | ' + [...document.querySelectorAll('[role="alert"],[role="status"]')].map(e=>e.textContent).join(' | ');
        if (text !== previous) { w.__enduranceStates.push({at:performance.now(),text,alerts:[...document.querySelectorAll('[role="alert"]')].map(e=>e.textContent || '')}); previous=text; }
      };
      w.__enduranceObserver = new MutationObserver(observeState);
      w.__enduranceObserver.observe(document.body,{subtree:true,childList:true,characterData:true,attributes:true,attributeFilter:['data-input-state']});
      observeState();
      return performance.now();
    });
    marks.push({ label: 'workload-start', at: new Date().toISOString() });
    while (performance.now() - started < seconds * 1000) {
      const round = rounds;
      await browserLiveFrame(page).focus();
      await expect(browserLivePanel(page).getByRole('textbox', { name: 'Remote browser text input' })).toBeFocused();
      await ready();
      let start = performance.now();
      // Preserve every repeat: eight presses of one held physical key, one release.
      for (let i = 0; i < 8; i++) { await page.keyboard.down('ArrowLeft'); await page.waitForTimeout(180); }
      await page.keyboard.up('ArrowLeft');
      state = { ...state, downs: state.downs + 8, ups: state.ups + 1 };
      await observe(`keys-${round}`, start);
      const wheel = await point(page, .25, .86); await page.mouse.move(wheel.x, wheel.y);
      start = performance.now();
      const wheelDelta = round % 2 === 0 ? 10 : -10;
      for (let i = 0; i < 30; i++) { await page.mouse.wheel(0, wheelDelta); await page.waitForTimeout(20); }
      state = { ...state, scroll: state.scroll + 30 * wheelDelta }; await observe(`wheel-${round}`, start);
      const left = await point(page, .1, .59), right = await point(page, .9, .59);
      for (let i = 0; i < 60; i++) await page.mouse.move(left.x + (right.x - left.x) * i / 59, left.y);
      const button = await point(page, .73, .68); start = performance.now(); await page.mouse.click(button.x, button.y);
      state = { ...state, clicks: state.clicks + 1, downs: state.downs + 1, ups: state.ups + 1 }; await observe(`click-${round}`, start);
      const text = await point(page, .25, .68); await page.mouse.click(text.x, text.y); start = performance.now();
      // Delete only the exact two characters authored in the previous round.
      if (round > 0) { await page.keyboard.press('Backspace'); await page.keyboard.press('Backspace'); }
      await page.keyboard.insertText('@é');
      state = { ...state, text: '@é', downs: state.downs + 1, ups: state.ups + 1 }; await observe(`text-${round}`, start);
      const a = await point(page, .6, .86), b = await point(page, .87, .86);
      await page.mouse.move(a.x, a.y); await page.mouse.down(); await page.mouse.move(b.x, b.y, { steps: 12 }); await page.mouse.up();
      state = { ...state, drags: state.drags + 1, downs: state.downs + 1, ups: state.ups + 1 }; await stateIs(page, state); await ready();
      mediaStats.push(await page.evaluate(async () => ({ at: new Date().toISOString(), stats: await (window as unknown as { __inputSmoke: { mediaStats(): Promise<unknown> } }).__inputSmoke.mediaStats() })));
      if (audioStimulus) {
        let stimulus: Awaited<ReturnType<typeof sampleAudioPressure>> = null;
        await expect.poll(async () => { stimulus = await sampleAudioPressure(page); return stimulus !== null; }, { timeout: 5000 }).toBe(true);
        const captured = stimulus as NonNullable<Awaited<ReturnType<typeof sampleAudioPressure>>> | null;
        if (!captured) throw Error('Video stimulus observation required');
        audioStimulusSamples.push(captured);
        expect(captured.muted).toBe(false);
        expect(captured.started).toBe(true);
        expect(captured.fault).toBe(false);
        expect(captured.matchingMediaPeers).toBe(1);
        expect(captured.audio).toHaveLength(1);
        for (const key of ['totalAudioEnergy', 'totalSamplesDuration', 'totalSamplesReceived', 'packetsReceived']) {
          const value = captured.audio[0][key];
          expect(typeof value, `required stimulus receiver metric ${key}`).toBe('number');
          expect(Number.isFinite(value), `finite stimulus receiver metric ${key}`).toBe(true);
          expect(Number(value), `nonnegative stimulus receiver metric ${key}`).toBeGreaterThanOrEqual(0);
        }
      }
      rounds++;
      if (rounds % resizeEvery === 0) {
        await page.setViewportSize(rounds % (resizeEvery * 2) === 0 ? {width:1440,height:1000} : {width:1600,height:1100});
        await stateIs(page,state); await ready();
      }
      const progress = {at:new Date().toISOString(),rounds,activeSeconds:(performance.now()-started)/1000,expected:state};
      fs.writeFileSync(info.outputPath('endurance-progress.json'),JSON.stringify(progress,null,2));
      console.log('ENDURANCE_PROGRESS',JSON.stringify({rounds,activeSeconds:Math.floor(progress.activeSeconds)}));
    }
    marks.push({ label: 'workload-end', at: new Date().toISOString() });
    expect(performance.now()-started).toBeGreaterThanOrEqual(seconds*1000);
    const routes = (await routeEvidence(page)).routes;
    expect(routes.filter(row => row.kind === 'key_down')).toHaveLength(rounds * 8 + (rounds-1)*2);
    expect(routes.filter(row => row.kind === 'key_up')).toHaveLength(rounds + (rounds-1)*2);
    expect(routes.filter(row => row.kind === 'text')).toHaveLength(rounds);
    expect(routes.some(row => row.kind === 'mouse_move' && row.route === 'input-hover')).toBe(true);
    expect(routes.filter(row => row.route === 'websocket')).toEqual([]);
    expect(routes.every(row => row.encoding === 'binary-v1')).toBe(true);
    const states = await page.evaluate(() => (window as unknown as {__enduranceStates:Array<{at:number;text:string;alerts:string[]}>}).__enduranceStates);
    expect(states.filter(s=>s.alerts.length>0)).toEqual([]);
    expect(states.filter(s => /input=(?:failed|paused)|Retry input|Resume input|Input connection failed|Browser fell behind|deadline exceeded|dispatch failed/i.test(s.text))).toEqual([]);
    for (let minute=0; minute<seconds/60; minute++) {
      const bucket = routes.filter(r=>r.at>=clientStart+minute*60000 && r.at<clientStart+(minute+1)*60000);
      for (const kind of ['mouse_move','wheel','mouse_down','key_down']) expect(bucket.some(r=>r.kind===kind), `minute${minute+1} missing${kind}`).toBe(true);
    }
    if (audioInactive) {
      const stats = await page.evaluate(async () => (window as unknown as { __inputSmoke: { mediaStats(): Promise<Array<Record<string, unknown>>> } }).__inputSmoke.mediaStats());
      const audio = stats.filter(row => row.type === 'receiver' && row.kind === 'audio');
      expect(audio.length, 'diagnostic must negotiate an actual audio transceiver').toBeGreaterThan(0);
      expect(audio.every(row => row.currentDirection === 'inactive'), 'audio must be negotiated inactive, not just muted').toBe(true);
      expect(stats.some(row => row.type === 'inbound-rtp' && row.kind === 'video' && Number(row.framesReceived) > 0 && Number(row.framesDecoded) > 0), 'diagnostic must still receive and decode video').toBe(true);
    }
    if (audioStimulus) {
      for (const phase of [0, 1, 2, 3]) expect(audioStimulusSamples.some(sample => sample.phase === phase), `video must show stimulus phase ${phase}`).toBe(true);
      for (const phase of [1, 2]) {
        const samples = audioStimulusSamples.filter(sample => sample.phase === phase && sample.contextState === 2);
        expect(samples.length, `running source observations in phase ${phase}`).toBeGreaterThanOrEqual(2);
        expect(samples.at(-1)!.contextMs, `source clock must advance in phase ${phase}`).toBeGreaterThan(samples[0].contextMs);
      }
      // Verify the authored stimulus itself after its transition ramp, without
      // imposing a receiver quality or real-time clock-rate threshold.
      expect(audioStimulusSamples.some(sample => sample.phase === 1 && sample.elapsedMs >= 490000 && sample.analyserRMS === 0), 'steady zero-gain source must measure zero').toBe(true);
      expect(audioStimulusSamples.some(sample => sample.phase === 2 && sample.elapsedMs >= 730000 && sample.analyserRMS >= 0.005 && sample.analyserRMS <= 0.025), 'steady source must contain the authored tone').toBe(true);
      const closedSamples = audioStimulusSamples.filter(sample => sample.phase === 3 && sample.elapsedMs >= 970000);
      expect(closedSamples.length, 'settled source observations after audio shutdown').toBeGreaterThanOrEqual(2);
      for (const sample of closedSamples) {
        expect([0, 3], 'audio context must be absent or closed after shutdown').toContain(sample.contextState);
        expect(sample.contextMs, 'closed source clock must be cleared').toBe(0);
      }
    }
    expect(errors).toEqual([]);
  } finally {
    // Do not wait forever for getStats during teardown. An unfinished request
    // is explicit in the snapshot; it cannot append after this stop boundary.
    const audioVideoTimingSeries = await page.evaluate(() => {
      const w = window as unknown as { __stopAudioTimingSampler?: () => unknown };
      return w.__stopAudioTimingSampler ? w.__stopAudioTimingSampler() : { notStarted: true };
    }).catch(() => ({ snapshotError: 'Browser context unavailable', at: new Date().toISOString() }));
    mediaStats.push(await page.evaluate(async () => ({ at: new Date().toISOString(), stats: await (window as unknown as { __inputSmoke?: { mediaStats(): Promise<unknown> } }).__inputSmoke?.mediaStats() })).catch(error => ({ statsError: String(error) })));
    const final = await page.evaluate(() => (window as unknown as { __inputSmoke?: { sample(): { state: InputState } | null } }).__inputSmoke?.sample()?.state).catch(() => null);
    const route = await routeEvidence(page).catch(() => null);
    const recentVideoFrames = await page.evaluate(() => (window as unknown as { __inputSmoke?: { frameTiming(): unknown } }).__inputSmoke?.frameTiming()).catch(() => null);
    const viewerStates = await page.evaluate(() => {const w=window as unknown as {__enduranceStates:unknown;__enduranceObserver:MutationObserver};w.__enduranceObserver?.disconnect();return w.__enduranceStates}).catch(()=>null);
    fs.writeFileSync(info.outputPath('pressure-stress-evidence.json'), JSON.stringify({ audioStimulusSamples, testWorkspace: browserTestWorkspacePath, videoPlayoutNegotiated, clientBrowserVersion, audioVideoTimingSeries, diagnosticMode, fullFeatureAcceptanceEligible, mediaStats, recentVideoFrames, requestedSeconds:seconds, resizeEvery, activeSeconds:started ? (performance.now()-started)/1000 : 0, rounds, viewerStates, provenance, deliberateKeyHandlerMs: delay, deliberateWheelHandlerMs: delay ? 75 : 0, marks, expected: state, final, route, errors }, null, 2));
    await info.attach('pressure-stress-evidence', { path: info.outputPath('pressure-stress-evidence.json'), contentType: 'application/json' });
    await page.getByRole('button', { name: 'Close live browser panel', exact: true }).click({ timeout: 5000 }).catch(() => {});
  }
});

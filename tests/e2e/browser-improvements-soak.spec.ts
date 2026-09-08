/** Real decoded-video acceptance. Preparation alone is not runtime evidence.
 * Run only against the isolated gateway on 11094 with a runtime-owned config;
 * the repository's global E2E setup is deliberately not required by this spec.
 */
import * as fs from 'node:fs';
import * as path from 'node:path';
import { randomInt } from 'node:crypto';
import { expect, test, type Page } from '@playwright/test';
import { assistantMessages, browserLiveFrame, browserLivePanel, browserLiveVideo, chatInput, selectAgent, watchLiveButton } from './fixtures/selectors';

const PHASE_MS = 10 * 60_000;
const CLICK_COUNT = 100;
const MAX_P95_MS = 200;
test.describe.configure({ retries: 0 });

type PixelState = { count: number; hash: number; last: number; held: number; firstError: number; nonce: number };
type Decoded = PixelState & { left: number; top: number; width: number; height: number; canvasWidth: number; canvasHeight: number };
type WireEvent = { at: number; direction: string; type: string; captureId?: string; generation?: number; offerId?: number; state?: string };
type Probe = {
  sample(): Decoded | null;
  continuity(): boolean;
  arm(expected: PixelState): void;
  latency(): number | null;
  error(): string | null;
};
type ProbeWindow = Window & { __omnipusSoak?: Probe };

// The independent event alphabet: left/right mouse down1/2, up3/4,
// click5/6; A down/up7/8; B down/up9/10. Unexpected events are255.
function cyclePlan(index: number) {
  const zone = (index * 7) % 11 < 5 ? 0 : 1;
  const key = index % 2 === 0 ? 'a' : 'b';
  const events = [1 + zone, 3 + zone, 5 + zone, key === 'a' ? 7 : 9, key === 'a' ? 8 : 10];
  if (index % 10 === 9) events.push(2, 7, 8, 4, 6);
  return { zone, key, events };
}

function fixtureHTML(expectedEvents: number[], nonce: number): string {
  return `<!doctype html><meta charset="utf-8"><title>Omnipus exact input soak</title>
<style>html,body{margin:0;width:100%;height:100%;overflow:hidden;background:#f0f}canvas{display:block;width:100%;height:100%}</style>
<canvas id="probe"></canvas><script>
const expected=${JSON.stringify(expectedEvents)}, nonce=${nonce};
const canvas=document.getElementById('probe'), ctx=canvas.getContext('2d');
let count=0, hash=0, last=0, held=0, firstError=0;
function bits(value,size){return Array.from({length:size},(_,i)=>(value>>>i)&1)}
function paint(){
 const w=canvas.width=innerWidth,h=canvas.height=innerHeight;
 ctx.fillStyle='#ff00ff';ctx.fillRect(0,0,w,h);
 ctx.fillStyle='#151515';ctx.fillRect(w*.03,h*.03,w*.94,h*.94);
 const data=[...bits(count,16),...bits(hash,32),...bits(last,8),...bits(held,8),...bits(firstError,16),...bits(nonce,16)];
 data.forEach((bit,i)=>{ctx.fillStyle=bit?'#fff':'#000';ctx.fillRect(w*(.1+(i%12)*.8/12),h*(.1+Math.floor(i/12)*.55/8),w*.8/12,h*.55/8)});
 ctx.fillStyle='#fff';ctx.font=Math.max(10,h*.035)+'px monospace';
 ctx.fillText('events '+count+' | order '+hash.toString(16)+' | held '+held,w*.07,h*.72);
 ctx.fillText('first mismatch '+firstError+' | run '+nonce,w*.07,h*.77);
 ctx.fillStyle='#13418a';ctx.fillRect(w*.05,h*.82,w*.44,h*.12);
 ctx.fillStyle='#246b35';ctx.fillRect(w*.51,h*.82,w*.44,h*.12);
 ctx.fillStyle='#fff';ctx.fillText('LEFT',w*.2,h*.9);ctx.fillText('RIGHT',w*.65,h*.9);
}
function record(code,trusted){
 if(!trusted)code=255;
 if(!firstError && expected[count]!==code) firstError=count+1;
 count++;hash=(Math.imul(hash,257)+code)>>>0;last=code;paint();
}
addEventListener('mousedown',e=>{held|=1;record(e.button===0?1+(e.clientX>=innerWidth/2):255,e.isTrusted)});
addEventListener('mouseup',e=>{held&=~1;record(e.button===0?3+(e.clientX>=innerWidth/2):255,e.isTrusted)});
addEventListener('click',e=>record(e.button===0?5+(e.clientX>=innerWidth/2):255,e.isTrusted));
addEventListener('keydown',e=>{e.preventDefault();const bit=e.code==='KeyA'?2:e.code==='KeyB'?4:0;held|=bit;record(e.repeat?255:bit===2?7:bit===4?9:255,e.isTrusted)});
addEventListener('keyup',e=>{e.preventDefault();const bit=e.code==='KeyA'?2:e.code==='KeyB'?4:0;held&=~bit;record(bit===2?8:bit===4?10:255,e.isTrusted)});
addEventListener('resize',paint);paint();
</script>`;
}

function advance(state: PixelState, codes: number[]): PixelState {
  const next = { ...state };
  for (const code of codes) {
    next.count++;
    next.hash = (Math.imul(next.hash, 257) + code) >>> 0;
    next.last = code;
    if (code === 1 || code === 2) next.held |= 1;
    if (code === 3 || code === 4) next.held &= ~1;
    if (code === 7) next.held |= 2;
    if (code === 8) next.held &= ~2;
    if (code === 9) next.held |= 4;
    if (code === 10) next.held &= ~4;
  }
  return next;
}

async function installVideoProbe(page: Page): Promise<void> {
  await page.evaluate(() => {
    const video = document.querySelector<HTMLVideoElement>('[data-testid="browser-live-video"]');
    const surface = document.querySelector<HTMLElement>('[data-testid="browser-live-frame"]');
    if (!video || !surface || !(video.srcObject instanceof MediaStream)) throw new Error('Real live-video stream unavailable');
    const stream = video.srcObject;
    const track = stream.getVideoTracks()[0];
    if (!track || typeof video.requestVideoFrameCallback !== 'function') throw new Error('Decoded video frame callbacks unavailable');
    const canvas = document.createElement('canvas');
    const ctx = canvas.getContext('2d', { willReadFrequently: true });
    if (!ctx) throw new Error('Decoded pixel sampling unavailable');
    let expected: PixelState | null = null;
    let started: number | null = null;
    let latency: number | null = null;
    let error: string | null = null;
    const continuity = () => document.contains(video) && video.srcObject === stream && stream.getVideoTracks()[0] === track && track.readyState === 'live' && !video.paused && video.readyState >= 2;
    const sample = (): Decoded | null => {
      if (!continuity() || !video.videoWidth || !video.videoHeight) return null;
      canvas.width = 384;
      canvas.height = Math.max(1, Math.round(384 * video.videoHeight / video.videoWidth));
      ctx.drawImage(video, 0, 0, canvas.width, canvas.height);
      const pixels = ctx.getImageData(0, 0, canvas.width, canvas.height).data;
      let left = canvas.width, right = -1, top = canvas.height, bottom = -1;
      // The authored magenta border identifies the CSS viewport INSIDE any
      // encoder padding. Never assume video dimensions equal page dimensions.
      for (let y = 0; y < canvas.height; y++) {
        let run = -1;
        for (let x = 0; x <= canvas.width; x++) {
          const p = (y * canvas.width + x) * 4;
          const pink = x < canvas.width && pixels[p] > 175 && pixels[p + 1] < 85 && pixels[p + 2] > 175;
          if (pink && run < 0) run = x;
          if (!pink && run >= 0) {
            // Long border runs exclude isolated colored encoder-marker cells.
            if (x - run >= canvas.width * .2) {
              left = Math.min(left, run); right = Math.max(right, x - 1);
              top = Math.min(top, y); bottom = Math.max(bottom, y);
            }
            run = -1;
          }
        }
      }
      if (right <= left || bottom <= top) return null;
      const width = right - left + 1, height = bottom - top + 1;
      const bits: number[] = [];
      for (let i = 0; i < 96; i++) {
        const x = Math.floor(left + width * (.1 + ((i % 12) + .5) * .8 / 12));
        const y = Math.floor(top + height * (.1 + (Math.floor(i / 12) + .5) * .55 / 8));
        const p = (y * canvas.width + x) * 4;
        const light = (pixels[p] + pixels[p + 1] + pixels[p + 2]) / 3;
        if (light > 70 && light < 185) return null;
        bits.push(light >= 185 ? 1 : 0);
      }
      let offset = 0;
      const read = (size: number) => { let value = 0; for (let i = 0; i < size; i++) value += bits[offset++] * 2 ** i; return value; };
      return { count: read(16), hash: read(32), last: read(8), held: read(8), firstError: read(16), nonce: read(16), left, top, width, height, canvasWidth: canvas.width, canvasHeight: canvas.height };
    };
    surface.addEventListener('pointerdown', event => {
      if (expected && started === null && event.isTrusted) started = performance.now();
    }, true);
    const onFrame = () => {
      if (expected && started !== null && latency === null) {
        const decoded = sample();
        if (decoded?.nonce === expected.nonce) {
          if (decoded.firstError) error = `Fixture event order mismatch at event ${decoded.firstError}`;
          if (decoded.count === expected.count && decoded.hash === expected.hash && decoded.last === expected.last && decoded.held === expected.held && decoded.firstError === 0) latency = performance.now() - started;
        }
      }
      if (document.contains(video)) video.requestVideoFrameCallback(onFrame);
    };
    video.requestVideoFrameCallback(onFrame);
    (window as ProbeWindow).__omnipusSoak = {
      sample, continuity, arm(value) { expected = value; started = null; latency = null; error = null; },
      latency: () => latency, error: () => error,
    };
  });
}

async function readState(page: Page, expected: PixelState): Promise<Decoded> {
  let decoded: Decoded | null = null;
  await expect.poll(async () => {
    decoded = await page.evaluate(() => (window as ProbeWindow).__omnipusSoak!.sample());
    if (decoded?.nonce === expected.nonce && decoded.firstError !== 0) throw new Error(`Unexpected event order at ${decoded.firstError}; actual=${JSON.stringify(decoded)}`);
    return decoded ? { count: decoded.count, hash: decoded.hash, last: decoded.last, held: decoded.held, firstError: decoded.firstError, nonce: decoded.nonce } : null;
  }, { timeout: 3_000, intervals: [16, 32, 50] }).toEqual(expected);
  return decoded!;
}

async function pointerPoint(page: Page, state: PixelState, zone: number) {
  const decoded = await readState(page, state);
  const video = browserLiveVideo(page);
  return video.evaluate((element, geometry) => {
    const v = element as HTMLVideoElement;
    const box = v.getBoundingClientRect();
    const scale = Math.min(box.width / v.videoWidth, box.height / v.videoHeight);
    const left = box.left + (box.width - v.videoWidth * scale) / 2;
    const top = box.top + (box.height - v.videoHeight * scale) / 2;
    return {
      x: left + ((geometry.left + geometry.width * (geometry.zone === 0 ? .25 : .75)) / geometry.canvasWidth) * v.videoWidth * scale,
      y: top + ((geometry.top + geometry.height * .87) / geometry.canvasHeight) * v.videoHeight * scale,
    };
  }, { ...decoded, zone });
}

async function waitUntil(page: Page, deadline: number) {
  while (performance.now() < deadline) await page.waitForTimeout(Math.min(1_000, Math.max(0, deadline - performance.now())));
}

test('isolated browser remains idle 10min then preserves exact mixed input for 10min', async ({ page }, testInfo) => {
  test.setTimeout(32 * 60_000);
  const origin = new URL(process.env.OMNIPUS_URL || '');
  if (!['localhost', '127.0.0.1'].includes(origin.hostname) || origin.port !== '11094') throw new Error('This acceptance test requires the isolated gateway on port11094');
  const runtimeHome = fs.realpathSync(process.env.SOAK_RUNTIME_HOME || '');
  const project = fs.realpathSync('/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus');
  if (!runtimeHome.startsWith(project + path.sep)) throw new Error('SOAK_RUNTIME_HOME must be the isolated runtime home under the Omnipus workspace');
  const nonce = randomInt(1, 65_536);
  const plans = Array.from({ length: CLICK_COUNT }, (_, i) => cyclePlan(i));
  const expectedEvents = plans.flatMap(p => p.events);
  const wire: WireEvent[] = [];
  const pageErrors: string[] = [];
  const latencies: number[] = [];
  const idleSamples: Array<{ at: number; count: number; hash: number }> = [];
  const clicks: Array<{ index: number; at: number; latencyMs: number }> = [];
  const timing: { idleStart?: number; idleEnd?: number; mixedStart?: number; mixedEnd?: number; idleElapsedMs?: number; mixedElapsedMs?: number } = {};
  let baselineWire = 0;
  page.on('pageerror', error => pageErrors.push(error.message));
  page.on('websocket', socket => {
    if (!new URL(socket.url()).pathname.endsWith('/browser/ws')) return;
    const record = (direction: string, payload: string | Buffer) => {
      let frame: Record<string, unknown>;
      try { frame = JSON.parse(payload.toString()) as Record<string, unknown>; } catch { return; }
      if (!['browser_video_health', 'browser_webrtc_answer', 'browser_webrtc_offer', 'browser_webrtc_state', 'browser_status'].includes(String(frame.type))) return;
      wire.push({ at: Date.now(), direction, type: String(frame.type), captureId: frame.capture_id as string | undefined, generation: frame.capture_generation as number | undefined, offerId: frame.offer_id as number | undefined, state: frame.type === 'browser_webrtc_state' && (frame.available === false || frame.active === false) ? 'error' : frame.state as string | undefined });
    };
    socket.on('framereceived', frame => record('received', frame.payload));
    socket.on('framesent', frame => record('sent', frame.payload));
    socket.on('close', () => wire.push({ at: Date.now(), direction: 'closed', type: 'socket_closed' }));
  });
  let state: PixelState = { count: 0, hash: 0, last: 0, held: 0, firstError: 0, nonce };
  try {
    await page.goto(origin.href);
    const response = await page.request.get(new URL('/api/v1/workspaces', origin).href);
    expect(response.ok(), 'authenticated workspace discovery').toBe(true);
    const workspaces = await response.json() as Array<{ id: string; is_default?: boolean; status?: string }>;
    const workspace = workspaces.find(w => w.is_default) ?? workspaces.find(w => !w.status || w.status === 'active');
    if (!workspace || !/^[a-zA-Z0-9_-]+$/.test(workspace.id)) throw new Error('No safely addressed active workspace');
    const relative = `browser-soak-${nonce}-${Date.now()}`;
    const workspaceWork = fs.realpathSync(path.join(runtimeHome, 'workspaces', workspace.id, 'work'));
    if (!workspaceWork.startsWith(runtimeHome + path.sep)) throw new Error('Runtime work directory escapes the isolated home');
    const fixtureDir = path.join(workspaceWork, relative);
    fs.mkdirSync(fixtureDir, { recursive: true });
    fs.writeFileSync(path.join(fixtureDir, 'index.html'), fixtureHTML(expectedEvents, nonce));
    await selectAgent(page, /Jim/i);
    await chatInput(page).fill(`Use only these two tools yourself, in order: serve_web with path "${relative}" and no command; then browser_navigate to exactly its returned preview URL. Do not click, type, or call any other tool. Reply exactly SOAK_READY_${nonce} after both succeed.`);
    await chatInput(page).press('Enter');
    await expect(assistantMessages(page).last()).toContainText(`SOAK_READY_${nonce}`, { timeout: 240_000 });
    await expect(page.locator('[data-testid="stop-btn"]')).not.toBeVisible({ timeout: 60_000 });
    await watchLiveButton(page).click();
    await expect(browserLivePanel(page)).toBeVisible();
    const address = page.getByRole('textbox', { name: 'Address bar' });
    await expect(address).toHaveValue(/\/preview\//, { timeout: 30_000 });
    const previewURL = new URL(await address.inputValue());
    if (previewURL.port !== '11094' || !['localhost', '127.0.0.1'].includes(previewURL.hostname) || !previewURL.pathname.startsWith('/preview/')) throw new Error('Fixture did not use the allowed isolated preview origin');
    // Exercise the real panel's navigation path too, then start the soak only
    // after the authored zero-event picture has actually reached its video.
    await address.fill(previewURL.href);
    await address.press('Enter');
    await expect(browserLiveVideo(page)).toBeVisible({ timeout: 90_000 });
    await expect.poll(() => browserLiveVideo(page).evaluate(el => (el as HTMLVideoElement).readyState), { timeout: 45_000 }).toBeGreaterThanOrEqual(2);
    // Let initial layout/offer convergence finish before the timed acceptance
    // window; this delay is not counted as idle stability or input latency.
    await page.waitForTimeout(3_000);
    await installVideoProbe(page);
    await readState(page, state);
    const answer = wire.filter(e => e.type === 'browser_webrtc_answer').at(-1);
    if (!answer?.captureId || !answer.generation || !answer.offerId) throw new Error('Missing exact negotiated capture/generation/offer identity');
    const baselineCapture = wire.filter(e => e.direction === 'received' && e.captureId && e.generation).at(-1);
    if (!baselineCapture) throw new Error('Missing current capture identity');
    baselineWire = wire.length;
    const assertContinuous = async () => {
      expect(await page.evaluate(() => (window as ProbeWindow).__omnipusSoak!.continuity()), 'original video stream/track must remain live').toBe(true);
      for (const event of wire.slice(baselineWire)) {
        expect(event.type, 'original browser socket closed').not.toBe('socket_closed');
        expect(event.type, 'unexpected viewer renegotiation').not.toBe('browser_webrtc_offer');
        expect(event.type, 'unexpected replacement answer').not.toBe('browser_webrtc_answer');
        expect(['lost', 'recovering', 'unrecoverable', 'error'].includes(event.state || ''), 'unexpected lifecycle failure').toBe(false);
        if (event.captureId) expect(event.captureId).toBe(baselineCapture.captureId);
        if (event.generation) expect(event.generation).toBe(baselineCapture.generation);
      }
    };
    await testInfo.attach('before-idle.png', { body: await browserLiveVideo(page).screenshot(), contentType: 'image/png' });
    const idleStart = performance.now();
    timing.idleStart = Date.now();
    while (performance.now() - idleStart < PHASE_MS) {
      await waitUntil(page, Math.min(idleStart + PHASE_MS, performance.now() + 5_000));
      await assertContinuous();
      const decoded = await readState(page, state);
      idleSamples.push({ at: Date.now(), count: decoded.count, hash: decoded.hash });
    }
    timing.idleEnd = Date.now();
    timing.idleElapsedMs = performance.now() - idleStart;
    expect(timing.idleElapsedMs).toBeGreaterThanOrEqual(PHASE_MS);
    await testInfo.attach('after-idle.png', { body: await browserLiveVideo(page).screenshot(), contentType: 'image/png' });
    const mixedStart = performance.now();
    timing.mixedStart = Date.now();
    for (let i = 0; i < CLICK_COUNT; i++) {
      await waitUntil(page, mixedStart + i * PHASE_MS / CLICK_COUNT);
      const plan = plans[i];
      const point = await pointerPoint(page, state, plan.zone);
      state = advance(state, plan.events.slice(0, 3));
      await page.evaluate(expected => (window as ProbeWindow).__omnipusSoak!.arm(expected), state);
      await page.mouse.click(point.x, point.y);
      await expect.poll(async () => {
        const error = await page.evaluate(() => (window as ProbeWindow).__omnipusSoak!.error());
        if (error) throw new Error(error);
        return page.evaluate(() => (window as ProbeWindow).__omnipusSoak!.latency());
      }, { timeout: 3_000, intervals: [16, 32, 50] }).not.toBeNull();
      const latencyMs = (await page.evaluate(() => (window as ProbeWindow).__omnipusSoak!.latency()))!;
      latencies.push(latencyMs);
      clicks.push({ index: i, at: Date.now(), latencyMs });
      await readState(page, state);
      await browserLiveFrame(page).focus();
      await page.keyboard.down(plan.key);
      await page.keyboard.up(plan.key);
      state = advance(state, plan.events.slice(3, 5));
      await readState(page, state);
      if (i % 10 === 9) {
        const holdPoint = await pointerPoint(page, state, 1);
        await page.mouse.move(holdPoint.x, holdPoint.y);
        await page.mouse.down();
        await page.keyboard.down('a');
        state = advance(state, [2, 7]);
        await readState(page, state); // Both holds must be visible remotely.
        await page.keyboard.up('a');
        await page.mouse.up();
        state = advance(state, [8, 4, 6]);
        await readState(page, state); // Both releases, exactly once, must arrive.
      }
      await assertContinuous();
    }
    await waitUntil(page, mixedStart + PHASE_MS);
    await readState(page, state);
    await assertContinuous();
    timing.mixedEnd = Date.now();
    timing.mixedElapsedMs = performance.now() - mixedStart;
    expect(timing.mixedElapsedMs).toBeGreaterThanOrEqual(PHASE_MS);
    expect(state.count).toBe(expectedEvents.length);
    expect(state.held).toBe(0);
    expect(latencies).toHaveLength(CLICK_COUNT);
    const ordered = [...latencies].sort((a, b) => a - b);
    const p95 = ordered[Math.ceil(.95 * CLICK_COUNT) - 1];
    await testInfo.attach('after-mixed.png', { body: await browserLiveVideo(page).screenshot(), contentType: 'image/png' });
    expect(pageErrors, 'uncaught viewer errors').toEqual([]);
    expect(p95, '100-click decoded-video p95, including trusted viewer input dispatch').toBeLessThanOrEqual(MAX_P95_MS);
  } finally {
    await testInfo.attach('soak-evidence.json', { body: Buffer.from(JSON.stringify({ nonce, expectedEventCount: expectedEvents.length, finalExpected: state, timing, clicks, latenciesMs: latencies, idleSamples, wire, pageErrors, unsupported: ['audio content', 'native macOS app', 'remote network/TURN path'] }, null, 2)), contentType: 'application/json' });
  }
});

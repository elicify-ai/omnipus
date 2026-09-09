/** Real decoded-video acceptance. Preparation alone is not runtime evidence.
 * Run only against the designated local or Amsterdam UAT gateway;
 * the repository's global E2E setup is deliberately not required by this spec.
 */
import * as fs from 'node:fs';
import * as path from 'node:path';
import { randomInt } from 'node:crypto';
import { browserRuntimeTarget } from '../../browser-runtime-target';
import { expect, type Page, type TestInfo } from '@playwright/test';
import { assistantMessages, browserLiveFrame, browserLivePanel, browserLiveVideo, chatInput, selectAgent, watchLiveButton } from './selectors';

const PHASE_MS = 10 * 60_000;
const CLICK_COUNT = 100;
const MAX_P95_MS = 200;

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
type StatsRow = { pc: number; id: string; type: string; [key: string]: unknown };
type StatsSnapshot = { label: string; at: number; rows: StatsRow[]; errors: string[]; receivers: Array<Record<string, unknown>>; transceivers: Array<{ pc: number; kind: string; direction: RTCRtpTransceiverDirection; currentDirection: RTCRtpTransceiverDirection | null }> };
type ClickStages = {
  count: number; armedAt: number; pointerAt?: number;
  sends: Array<{ route: string; kind: string; attemptedAt: number; returnedAt?: number; succeeded?: boolean }>;
  presentation?: Record<string, number>;
};
type LatencyEvidence = { clicks: ClickStages[]; stats: StatsSnapshot[]; audioOverrides: number };
type Diagnostics = {
  begin(count: number): void;
  pointer(at: number): void;
  presented(metadata: Record<string, number>): void;
  snapshot(label: string): Promise<void>;
  evidence(): LatencyEvidence;
};
type ProbeWindow = Window & { __omnipusSoak?: Probe; __omnipusLatency?: Diagnostics };

async function installLatencyDiagnostics(page: Page, videoOnly = false): Promise<void> {
  await page.addInitScript(disableAudio => {
    const peers: RTCPeerConnection[] = [];
    const clicks: ClickStages[] = [];
    const stats: StatsSnapshot[] = [];
    let current: ClickStages | undefined;
    let audioOverrides = 0;
    const NativePeer = window.RTCPeerConnection;
    window.RTCPeerConnection = class extends NativePeer {
      constructor(...args: ConstructorParameters<typeof NativePeer>) { super(...args); peers.push(this); }
      addTransceiver(trackOrKind: MediaStreamTrack | string, init?: RTCRtpTransceiverInit): RTCRtpTransceiver {
        const kind = typeof trackOrKind === 'string' ? trackOrKind : trackOrKind.kind;
        if (disableAudio && kind === 'audio') {
          audioOverrides++;
          return super.addTransceiver(trackOrKind, { ...init, direction: 'inactive' });
        }
        return super.addTransceiver(trackOrKind, init);
      }
    };
    const observeSend = (route: string, payload: unknown, send: () => void) => {
      let entry: ClickStages['sends'][number] | undefined;
      if (current && typeof payload === 'string') {
        let frame: Record<string, unknown> | undefined;
        try { frame = JSON.parse(payload) as Record<string, unknown>; } catch { /* Non-JSON transport messages carry no input timing. */ }
        if (frame && (frame.kind === 'mouse_down' || frame.kind === 'mouse_up')) {
          entry = { route, kind: frame.kind, attemptedAt: performance.now() };
          current.sends.push(entry);
        }
      }
      try { send(); if (entry) entry.succeeded = true; }
      catch (error) { if (entry) entry.succeeded = false; throw error; }
      finally { if (entry) entry.returnedAt = performance.now(); }
    };
    const wsSend = WebSocket.prototype.send;
    WebSocket.prototype.send = function (data: unknown) { observeSend('websocket', data, () => Reflect.apply(wsSend, this, [data])); };
    const dcSend = RTCDataChannel.prototype.send;
    RTCDataChannel.prototype.send = function (data: unknown) { observeSend('datachannel', data, () => Reflect.apply(dcSend, this, [data])); };
    const fields = ['timestamp', 'kind', 'mediaType', 'transportId', 'codecId', 'packetsReceived', 'packetsLost', 'bytesReceived', 'jitter', 'jitterBufferDelay', 'jitterBufferTargetDelay', 'jitterBufferMinimumDelay', 'jitterBufferEmittedCount', 'totalDecodeTime', 'framesDecoded', 'framesDropped', 'framesPerSecond', 'totalProcessingDelay', 'nackCount', 'pliCount', 'currentRoundTripTime', 'totalRoundTripTime', 'roundTripTime', 'roundTripTimeMeasurements', 'responsesReceived', 'availableIncomingBitrate', 'state', 'nominated', 'selectedCandidatePairId', 'totalSamplesReceived', 'totalSamplesDuration', 'totalAudioEnergy', 'audioLevel', 'concealedSamples', 'silentConcealedSamples', 'concealmentEvents', 'insertedSamplesForDeceleration', 'removedSamplesForAcceleration'];
    (window as ProbeWindow).__omnipusLatency = {
      begin(count) { current = { count, armedAt: performance.now(), sends: [] }; clicks.push(current); },
      pointer(at) { if (current) current.pointerAt = at; },
      presented(metadata) { if (current) current.presentation = metadata; },
      async snapshot(label) {
        const snapshot: StatsSnapshot = { label, at: performance.now(), rows: [], errors: [], receivers: [], transceivers: [] };
        for (const [index, pc] of peers.entries()) {
          if (pc.connectionState === 'closed') continue;
          for (const transceiver of pc.getTransceivers()) {
            snapshot.transceivers.push({ pc: index, kind: transceiver.receiver.track.kind, direction: transceiver.direction, currentDirection: transceiver.currentDirection });
          }
          for (const receiver of pc.getReceivers()) {
            const settings = receiver as unknown as Record<string, unknown>;
            snapshot.receivers.push({ pc: index, kind: receiver.track.kind, jitterBufferTarget: settings.jitterBufferTarget ?? null, playoutDelayHint: settings.playoutDelayHint ?? null });
          }
          try {
            const report = await pc.getStats();
            report.forEach(raw => {
              const row = raw as Record<string, unknown>;
              if (!['inbound-rtp', 'remote-inbound-rtp', 'candidate-pair', 'transport'].includes(String(row.type))) return;
              const selected: StatsRow = { pc: index, id: String(row.id), type: String(row.type) };
              for (const field of fields) selected[field] = row[field] ?? null;
              snapshot.rows.push(selected);
            });
          } catch (error) { snapshot.errors.push(`peer${index}: ${String(error)}`); }
        }
        stats.push(snapshot);
      },
      evidence: () => ({ clicks, stats, audioOverrides }),
    };
  }, videoOnly);
}

async function captureRTCStats(page: Page, label: string): Promise<void> {
  await page.evaluate(async name => { await (window as ProbeWindow).__omnipusLatency!.snapshot(name); }, label);
}

function receiverDeltas(evidence: LatencyEvidence | null) {
  const first = evidence?.stats[0], last = evidence?.stats.at(-1);
  if (!first || !last) return [];
  return last.rows.filter(row => row.type === 'inbound-rtp' && (row.kind === 'video' || row.mediaType === 'video')).map(row => {
    const before = first.rows.find(value => value.pc === row.pc && value.id === row.id);
    const delta = (key: string) => {
      const a = before?.[key], b = row[key];
      return typeof a === 'number' && typeof b === 'number' && b >= a ? b - a : null;
    };
    const averageMs = (total: string, count: string) => {
      const seconds = delta(total), samples = delta(count);
      return seconds !== null && samples !== null && samples > 0 ? seconds * 1000 / samples : null;
    };
    return { pc: row.pc, id: row.id, intervalMs: last.at - first.at,
      jitterBufferMs: averageMs('jitterBufferDelay', 'jitterBufferEmittedCount'),
      jitterBufferTargetMs: averageMs('jitterBufferTargetDelay', 'jitterBufferEmittedCount'),
      decodeMs: averageMs('totalDecodeTime', 'framesDecoded'),
      processingMs: averageMs('totalProcessingDelay', 'framesDecoded'),
      framesDecoded: delta('framesDecoded'), packetsLost: delta('packetsLost'), framesPerSecond: row.framesPerSecond,
    };
  });
}


// The independent event alphabet: left/right mouse down1/2, up3/4,
// click5/6; ArrowLeft down/up7/8; ArrowRight down/up9/10. Unexpected events are255.
// Nonprintable keys use the real key-down/up route; printable text uses insertText.
function cyclePlan(index: number) {
  const zone = (index * 7) % 11 < 5 ? 0 : 1;
  const key = index % 2 === 0 ? 'ArrowLeft' : 'ArrowRight';
  const events = [1 + zone, 3 + zone, 5 + zone, key === 'ArrowLeft' ? 7 : 9, key === 'ArrowLeft' ? 8 : 10];
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
addEventListener('keydown',e=>{e.preventDefault();const bit=e.code==='ArrowLeft'?2:e.code==='ArrowRight'?4:0;held|=bit;record(e.repeat?255:bit===2?7:bit===4?9:255,e.isTrusted)});
addEventListener('keyup',e=>{e.preventDefault();const bit=e.code==='ArrowLeft'?2:e.code==='ArrowRight'?4:0;held&=~bit;record(bit===2?8:bit===4?10:255,e.isTrusted)});
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
    const locatorCanvas = document.createElement('canvas');
    const locatorContext = locatorCanvas.getContext('2d', { willReadFrequently: true });
    const gridCanvas = document.createElement('canvas');
    gridCanvas.width = 12;
    gridCanvas.height = 8;
    const gridContext = gridCanvas.getContext('2d', { willReadFrequently: true });
    if (!locatorContext || !gridContext) throw new Error('Decoded pixel sampling unavailable');
    gridContext.imageSmoothingEnabled = false;
    type Geometry = Pick<Decoded, 'left' | 'top' | 'width' | 'height' | 'canvasWidth' | 'canvasHeight'> & { videoWidth: number; videoHeight: number };
    let geometry: Geometry | null = null;
    let expected: PixelState | null = null;
    let started: number | null = null;
    let latency: number | null = null;
    let error: string | null = null;
    const continuity = () => document.contains(video) && video.srcObject === stream && stream.getVideoTracks()[0] === track && track.readyState === 'live' && !video.paused && video.readyState >= 2;
    const locate = (): Geometry | null => {
      locatorCanvas.width = 384;
      locatorCanvas.height = Math.max(1, Math.round(384 * video.videoHeight / video.videoWidth));
      locatorContext.drawImage(video, 0, 0, locatorCanvas.width, locatorCanvas.height);
      const pixels = locatorContext.getImageData(0, 0, locatorCanvas.width, locatorCanvas.height).data;
      let left = locatorCanvas.width, right = -1, top = locatorCanvas.height, bottom = -1;
      // Locate the authored viewport border once per decoded video size. These
      // coordinates also remain the pointer mapping's source of truth.
      for (let y = 0; y < locatorCanvas.height; y++) {
        let run = -1;
        for (let x = 0; x <= locatorCanvas.width; x++) {
          const p = (y * locatorCanvas.width + x) * 4;
          const pink = x < locatorCanvas.width && pixels[p] > 175 && pixels[p + 1] < 85 && pixels[p + 2] > 175;
          if (pink && run < 0) run = x;
          if (!pink && run >= 0) {
            // Long border runs exclude isolated colored encoder-marker cells.
            if (x - run >= locatorCanvas.width * .2) {
              left = Math.min(left, run); right = Math.max(right, x - 1);
              top = Math.min(top, y); bottom = Math.max(bottom, y);
            }
            run = -1;
          }
        }
      }
      if (right <= left || bottom <= top) return null;
      return { left, top, width: right - left + 1, height: bottom - top + 1,
        canvasWidth: locatorCanvas.width, canvasHeight: locatorCanvas.height,
        videoWidth: video.videoWidth, videoHeight: video.videoHeight };
    };
    const sample = (): Decoded | null => {
      if (!continuity() || !video.videoWidth || !video.videoHeight) return null;
      if (!geometry || geometry.videoWidth !== video.videoWidth || geometry.videoHeight !== video.videoHeight) geometry = locate();
      if (!geometry) return null;
      const { left, top, width, height, canvasWidth, canvasHeight } = geometry;
      // One nearest-neighbor reduction maps each authored cell's center to one
      // output pixel. Read 96 observed pixels, not an entire rescaled video frame;
      // preserve the same thresholds and every count/order/hold/nonce bit.
      const scaleX = video.videoWidth / canvasWidth;
      const scaleY = video.videoHeight / canvasHeight;
      gridContext.drawImage(video, (left + width * .1) * scaleX, (top + height * .1) * scaleY,
        width * .8 * scaleX, height * .55 * scaleY, 0, 0, 12, 8);
      const pixels = gridContext.getImageData(0, 0, 12, 8).data;
      const bits: number[] = [];
      for (let i = 0; i < 96; i++) {
        const p = i * 4;
        const light = (pixels[p] + pixels[p + 1] + pixels[p + 2]) / 3;
        if (light > 70 && light < 185) return null;
        bits.push(light >= 185 ? 1 : 0);
      }
      let offset = 0;
      const read = (size: number) => { let value = 0; for (let i = 0; i < size; i++) value += bits[offset++] * 2 ** i; return value; };
      return { count: read(16), hash: read(32), last: read(8), held: read(8), firstError: read(16), nonce: read(16), left, top, width, height, canvasWidth, canvasHeight };
    };
    surface.addEventListener('pointerdown', event => {
      if (expected && started === null && event.isTrusted) {
        started = performance.now();
        (window as ProbeWindow).__omnipusLatency?.pointer(started);
      }
    }, true);
    const onFrame: VideoFrameRequestCallback = (callbackTime, metadata) => {
      if (expected && started !== null && latency === null) {
        const sampleStarted = performance.now();
        const decoded = sample();
        const sampleFinished = performance.now();
        if (decoded?.nonce === expected.nonce) {
          if (decoded.firstError) error = `Fixture event order mismatch at event ${decoded.firstError}`;
          if (decoded.count === expected.count && decoded.hash === expected.hash && decoded.last === expected.last && decoded.held === expected.held && decoded.firstError === 0) {
            latency = performance.now() - started;
            const detail: Record<string, number> = { callbackTime, sampleStarted, sampleFinished, sampleCostMs: sampleFinished - sampleStarted };
            const available = metadata as unknown as Record<string, unknown>;
            for (const key of ['presentationTime', 'expectedDisplayTime', 'processingDuration', 'captureTime', 'receiveTime', 'rtpTimestamp', 'mediaTime', 'presentedFrames']) {
              if (typeof available[key] === 'number') detail[key] = available[key] as number;
            }
            (window as ProbeWindow).__omnipusLatency?.presented(detail);
          }
        }
      }
      if (document.contains(video)) video.requestVideoFrameCallback(onFrame);
    };
    video.requestVideoFrameCallback(onFrame);
    (window as ProbeWindow).__omnipusSoak = {
      sample, continuity, arm(value) { expected = value; started = null; latency = null; error = null; (window as ProbeWindow).__omnipusLatency?.begin(value.count); },
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

async function persistScreenshot(page: Page, testInfo: TestInfo, name: string) {
  const file = testInfo.outputPath(name);
  fs.mkdirSync(path.dirname(file), { recursive: true });
  await browserLiveVideo(page).screenshot({ path: file });
  await testInfo.attach(name, { path: file, contentType: 'image/png' });
}

async function waitUntil(page: Page, deadline: number) {
  while (performance.now() < deadline) await page.waitForTimeout(Math.min(1_000, Math.max(0, deadline - performance.now())));
}

function directFixturePreparation(preview: string | undefined, directory: string | undefined, origin: URL, workspaceWork: string): { preview: URL; directory: string } | undefined {
  if (preview === undefined && directory === undefined) return undefined;
  if (!preview || !directory) throw new Error('BROWSER_PROBE_PREVIEW_URL and BROWSER_PROBE_FIXTURE_DIR must be supplied together');
  const url = new URL(preview);
  if (url.origin !== origin.origin || !url.pathname.startsWith('/preview/') || url.username || url.password || url.search || url.hash) throw new Error('Direct fixture preview must use the designated runtime preview origin without credentials, query or fragment');
  if (!path.isAbsolute(directory)) throw new Error('BROWSER_PROBE_FIXTURE_DIR must be absolute');
  const resolved = fs.realpathSync(directory);
  if (!resolved.startsWith(workspaceWork + path.sep) || !fs.statSync(resolved).isDirectory()) throw new Error('Direct fixture directory must be strictly inside the selected workspace work directory');
  const index = path.join(resolved, 'index.html');
  if (fs.lstatSync(index, { throwIfNoEntry: false })?.isSymbolicLink()) throw new Error('Direct fixture index must not be a symbolic link');
  return { preview: url, directory: resolved };
}

export async function runBrowserInputProbe(page: Page, testInfo: TestInfo, mode: 'soak' | 'latency' | 'latency-video-only'): Promise<void> {
  testInfo.setTimeout(mode === 'soak' ? 32 * 60_000 : 8 * 60_000);
  const mixedDuration = mode === 'soak' ? PHASE_MS : CLICK_COUNT * 500;
  const diagnostic = mode !== 'soak';
  const videoOnly = mode === 'latency-video-only';
  const label = mode === 'soak' ? 'browser-soak' : videoOnly ? 'browser-latency-video-only' : 'browser-latency';
  const origin = browserRuntimeTarget(process.env.OMNIPUS_URL);
  const runtimeHome = fs.realpathSync(process.env.SOAK_RUNTIME_HOME || '');
  if (!path.isAbsolute(process.env.SOAK_RUNTIME_HOME || '') || !fs.statSync(runtimeHome).isDirectory()) throw new Error('SOAK_RUNTIME_HOME must name an absolute isolated runtime directory');
  const nonce = randomInt(1, 65_536);
  const plans = Array.from({ length: CLICK_COUNT }, (_, i) => {
    const plan = cyclePlan(i);
    return mode === 'soak' ? plan : { ...plan, events: plan.events.slice(0, 3) };
  });
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
  if (diagnostic) await installLatencyDiagnostics(page, videoOnly);
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
    const direct = directFixturePreparation(process.env.BROWSER_PROBE_PREVIEW_URL, process.env.BROWSER_PROBE_FIXTURE_DIR, origin, workspaceWork);
    const fixtureDir = direct?.directory ?? path.join(workspaceWork, relative);
    fs.mkdirSync(fixtureDir, { recursive: true });
    const html = fixtureHTML(expectedEvents, nonce);
    fs.writeFileSync(path.join(fixtureDir, 'index.html'), html);
    await selectAgent(page, process.env.BROWSER_PROBE_AGENT_NAME || /Jim/i);
    console.log(`[${label}] agent selected`);
    let previewURL: URL;
    if (direct) {
      previewURL = direct.preview;
      const served = await page.request.get(previewURL.href, { timeout: 15_000, maxRedirects: 0, headers: { 'Cache-Control': 'no-cache' } });
      expect(served.ok(), 'existing preview serves the fresh fixture').toBe(true);
      expect(await served.text(), 'served fixture contains this run’s exact event plan and nonce').toContain(`const expected=${JSON.stringify(expectedEvents)}, nonce=${nonce};`);
      console.log(`[${label}] setup ready`);
      const openBrowser = page.getByRole('button', { name: 'Open browser', exact: true });
      await expect(openBrowser).toBeVisible({ timeout: 15_000 });
      await openBrowser.click({ timeout: 15_000 });
      await expect.poll(() => wire.some(event => event.direction === 'received' && event.type === 'browser_status' && event.state === 'attached'), { timeout: 45_000 }).toBe(true);
      await expect.poll(() => browserLiveVideo(page).evaluate(element => (element as HTMLVideoElement).readyState), { timeout: 45_000 }).toBeGreaterThanOrEqual(2);
    } else {
      await chatInput(page).fill(`Prepare this browser test yourself. You may first use ToolSearch to load exactly serve_web and browser_navigate. Then call serve_web with path "${relative}" and no command, followed by browser_navigate to exactly its returned preview URL. Do not click, type, or use unrelated tools. Only after both tools succeed, reply with exactly SOAK_READY_${nonce} and nothing else. If any step fails, reply SOAK_SETUP_FAILED with the reason and never include the success marker.`);
      await chatInput(page).press('Enter');
      // Finalized messages render markdown directly in the text body, without
      // the live renderer's .prose-sm wrapper. Keep every top-level markdown
      // block so a separate denial paragraph cannot be hidden by a ready line.
      const reply = assistantMessages(page).last()
        .locator(':scope > div > div.text-sm.leading-relaxed')
        .locator(':scope > p, :scope > h1, :scope > h2, :scope > h3, :scope > h4, :scope > h5, :scope > h6, :scope > ul, :scope > ol, :scope > blockquote, :scope > hr, :scope > pre, :scope > .overflow-x-auto, :scope > .rounded.overflow-hidden');
      await expect.poll(async () => (await reply.allTextContents()).join('\n'), { timeout: 240_000 }).toMatch(new RegExp(`SOAK_READY_${nonce}|SOAK_SETUP_FAILED`));
      await expect(page.locator('[data-testid="stop-btn"]')).not.toBeVisible({ timeout: 60_000 });
      await expect(reply).toHaveText([`SOAK_READY_${nonce}`], { timeout: 5_000 });
      console.log(`[${label}] setup ready`);
      await expect(watchLiveButton(page)).toBeVisible({ timeout: 15_000 });
      await watchLiveButton(page).click({ timeout: 15_000 });
      await expect(browserLivePanel(page)).toBeVisible();
      const initialAddress = page.getByRole('textbox', { name: 'Address bar' });
      await expect(initialAddress).toHaveValue(/\/preview\//, { timeout: 30_000 });
      previewURL = new URL(await initialAddress.inputValue());
      if (previewURL.origin !== origin.origin || !previewURL.pathname.startsWith('/preview/')) throw new Error('Fixture did not use the designated runtime preview origin');
    }
    await expect(browserLivePanel(page)).toBeVisible({ timeout: 15_000 });
    const address = page.getByRole('textbox', { name: 'Address bar' });
    // Exercise the real panel's navigation path too, then start the soak only
    // after the authored zero-event picture has actually reached its video.
    await address.fill(previewURL.href);
    await address.press('Enter');
    await expect(browserLiveVideo(page)).toBeVisible({ timeout: 90_000 });
    await expect.poll(() => browserLiveVideo(page).evaluate(el => (el as HTMLVideoElement).readyState), { timeout: 45_000 }).toBeGreaterThanOrEqual(2);
    console.log(`[${label}] initial video ready`);
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
    if (mode === 'soak') {
      await persistScreenshot(page, testInfo, 'before-idle.png');
      const idleStart = performance.now();
      timing.idleStart = Date.now();
      console.log('[browser-soak] idle phase started: target600000ms');
      while (performance.now() - idleStart < PHASE_MS) {
        await waitUntil(page, Math.min(idleStart + PHASE_MS, performance.now() + 5_000));
        await assertContinuous();
        const decoded = await readState(page, state);
        idleSamples.push({ at: Date.now(), count: decoded.count, hash: decoded.hash });
      }
      timing.idleEnd = Date.now();
      timing.idleElapsedMs = performance.now() - idleStart;
      expect(timing.idleElapsedMs).toBeGreaterThanOrEqual(PHASE_MS);
      console.log(`[browser-soak] idle complete: ${timing.idleElapsedMs.toFixed(1)}ms, ${idleSamples.length} unchanged decoded checkpoints`);
      await persistScreenshot(page, testInfo, 'after-idle.png');
    } else {
      await persistScreenshot(page, testInfo, 'before-input.png');
      await captureRTCStats(page, 'before-input');
    }
    const mixedStart = performance.now();
    timing.mixedStart = Date.now();
    console.log(`[${label}] input phase started: ${CLICK_COUNT} clicks, ${expectedEvents.length} exact events, target${mixedDuration}ms`);
    for (let i = 0; i < CLICK_COUNT; i++) {
      await waitUntil(page, mixedStart + i * mixedDuration / CLICK_COUNT);
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
      if (mode === 'soak') {
        await browserLiveFrame(page).focus();
        await page.keyboard.down(plan.key);
        await page.keyboard.up(plan.key);
        state = advance(state, plan.events.slice(3, 5));
        await readState(page, state);
        if (i % 10 === 9) {
          const holdPoint = await pointerPoint(page, state, 1);
          await page.mouse.move(holdPoint.x, holdPoint.y);
          await page.mouse.down();
          await page.keyboard.down('ArrowLeft');
          state = advance(state, [2, 7]);
          await readState(page, state); // Both holds must be visible remotely.
          await page.keyboard.up('ArrowLeft');
          await page.mouse.up();
          state = advance(state, [8, 4, 6]);
          await readState(page, state); // Both releases, exactly once, must arrive.
        }
      } else if ((i + 1) % 20 === 0) {
        await captureRTCStats(page, `after-click-${i + 1}`);
        console.log(`[${label}] ${i + 1}/${CLICK_COUNT} clicks decoded`);
      }
      await assertContinuous();
    }
    await waitUntil(page, mixedStart + mixedDuration);
    await readState(page, state);
    await assertContinuous();
    timing.mixedEnd = Date.now();
    timing.mixedElapsedMs = performance.now() - mixedStart;
    expect(timing.mixedElapsedMs).toBeGreaterThanOrEqual(mixedDuration);
    expect(state.count).toBe(expectedEvents.length);
    expect(state.held).toBe(0);
    expect(latencies).toHaveLength(CLICK_COUNT);
    console.log(`[${label}] measurement done`);
    const ordered = [...latencies].sort((a, b) => a - b);
    const p95 = ordered[Math.ceil(.95 * CLICK_COUNT) - 1];
    console.log(`[${label}] input complete: ${timing.mixedElapsedMs.toFixed(1)}ms, ${latencies.length} clicks, ${state.count}/${expectedEvents.length} exact events, p95=${p95.toFixed(3)}ms, target<=${MAX_P95_MS}ms`);
    await persistScreenshot(page, testInfo, 'after-mixed.png');
    expect(pageErrors, 'uncaught viewer errors').toEqual([]);
    expect(p95, '100-click decoded-video p95, including trusted viewer input dispatch').toBeLessThanOrEqual(MAX_P95_MS);
  } finally {
    const ordered = [...latencies].sort((a, b) => a - b);
    const p95Ms = ordered.length ? ordered[Math.ceil(.95 * ordered.length) - 1] : null;
    let diagnostics: LatencyEvidence | null = null;
    let diagnosticsError: string | null = null;
    if (diagnostic) {
      try {
        await captureRTCStats(page, 'final');
        diagnostics = await page.evaluate(() => (window as ProbeWindow).__omnipusLatency!.evidence());
      } catch (error) { diagnosticsError = String(error); }
    }
    const rtcDeltas = receiverDeltas(diagnostics);
    const evidenceName = mode === 'soak' ? 'soak-evidence.json' : videoOnly ? 'latency-video-only-evidence.json' : 'latency-evidence.json';
    const file = testInfo.outputPath(evidenceName);
    fs.mkdirSync(path.dirname(file), { recursive: true });
    fs.writeFileSync(file, JSON.stringify({ mode, nonce, expectedEventCount: expectedEvents.length, finalExpected: state, timing, clicks, latenciesMs: latencies, p95Ms, thresholdMs: MAX_P95_MS, diagnostics, diagnosticsError, rtcDeltas, idleSamples, wire, pageErrors, unsupported: ['audio content', 'native macOS app', 'remote network/TURN path'] }, null, 2));
    console.log(`[${label}] evidence persisted: ${file}; clicks=${latencies.length}, p95=${p95Ms === null ? 'unavailable' : p95Ms.toFixed(3)}ms`);
    if (diagnostic) console.log(`[${label}] receiver deltas: ${JSON.stringify(rtcDeltas)}; statsError=${diagnosticsError ?? 'none'}`);
    await testInfo.attach(evidenceName, { path: file, contentType: 'application/json' });
    if (diagnostic && latencies.length === CLICK_COUNT) {
      expect(diagnosticsError, 'latency instrumentation must remain readable').toBeNull();
      expect(diagnostics?.clicks, 'one outbound/presentation trace per click').toHaveLength(CLICK_COUNT);
      for (const click of diagnostics!.clicks) {
        expect(click.pointerAt, 'trusted viewer pointer timestamp').toBeGreaterThan(0);
        expect(click.sends.map(send => send.kind), 'exact native outbound click pair').toEqual(['mouse_down', 'mouse_up']);
        expect(click.sends.every(send => send.succeeded), 'outbound sends must succeed').toBe(true);
        expect(click.presentation?.sampleFinished, 'matching decoded frame timestamp').toBeGreaterThan(0);
      }
      expect(rtcDeltas.length, 'real video receiver statistics must be available').toBeGreaterThan(0);
      if (videoOnly) {
        expect(diagnostics!.audioOverrides, 'experiment must intercept actual audio negotiation').toBeGreaterThan(0);
        const audioTransceivers = diagnostics!.stats.flatMap(snapshot => snapshot.transceivers).filter(value => value.kind === 'audio');
        expect(audioTransceivers.length, 'observe the disabled audio transceiver').toBeGreaterThan(0);
        for (const transceiver of audioTransceivers) {
          expect(transceiver.direction, 'audio must remain explicitly inactive').toBe('inactive');
          expect(['inactive', null], 'no negotiated sending or receiving audio').toContain(transceiver.currentDirection);
        }
        const audioReports = diagnostics!.stats.flatMap(snapshot => snapshot.rows).filter(row => row.type === 'inbound-rtp' && (row.kind === 'audio' || row.mediaType === 'audio'));
        for (const report of audioReports) expect(report.packetsReceived, 'video-only experiment received audio packets').toBe(0);
      } else {
        expect(diagnostics!.audioOverrides, 'normal diagnostic must not alter audio negotiation').toBe(0);
      }
    }
  }
}

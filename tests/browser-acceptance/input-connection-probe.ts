import { expect, type Page } from '@playwright/test';
import { browserLiveVideo } from '../e2e/fixtures/selectors';

export type InputState = { nonce: number; clicks: number; downs: number; ups: number; held: number; scroll: number; drags: number; errors: number; text: string };
export type RouteEvent = { route: string; kind: string; at: number; encoding: 'json' | 'binary-v1'; bytes: number };
type Geometry = { left: number; top: number; width: number; height: number; canvasWidth: number; canvasHeight: number };
type Observation = { state: InputState; geometry: Geometry };
type RuntimeProbe = {
  sample(): Observation | null;
  sameMedia(): boolean;
  routes: RouteEvent[];
  closeInput(): void;
  closeSocket(): void;
  openedSockets(): number;
  peers(): Array<{ labels: string[]; transceivers: number; state: string }>;
};
type ProbeWindow = Window & { __inputSmoke: RuntimeProbe };

export async function instrumentRoutes(page: Page) {
  await page.addInitScript(() => {
    const routes: RouteEvent[] = [], peers: Array<{ pc: RTCPeerConnection; labels: string[] }> = [], sockets: WebSocket[] = [];
    function record(route: string, data: unknown) {
      const kinds = ['mouse_move', 'mouse_down', 'mouse_up', 'wheel', 'key_down', 'key_up', 'text'];
      if (typeof data === 'string') {
        let frame; try { frame = JSON.parse(data); } catch { return; }
        if (frame.type === 'browser_input' && kinds.includes(frame.kind)) routes.push({ route, kind: frame.kind, at: performance.now(), encoding: 'json', bytes: new TextEncoder().encode(data).length });
        return;
      }
      // Observe the versioned packet header without retaining user payloads.
      // Full framing/schema admission is independently tested at the server.
      const bytes = data instanceof ArrayBuffer ? new Uint8Array(data) : ArrayBuffer.isView(data) ? new Uint8Array(data.buffer, data.byteOffset, data.byteLength) : null;
      if (bytes && bytes.length >= 9 && bytes.length <= 65536 && bytes[0] === 79 && bytes[1] === 66 && bytes[2] === 73 && bytes[3] === 1 && kinds[bytes[4] - 1]) {
        routes.push({ route, kind: kinds[bytes[4] - 1], at: performance.now(), encoding: 'binary-v1', bytes: bytes.length });
      }
    }
    let openedSockets = 0;
    const NativePeer = window.RTCPeerConnection;
    window.RTCPeerConnection = class extends NativePeer {
      constructor(...args: ConstructorParameters<typeof NativePeer>) { super(...args); peers.push({ pc: this, labels: [] }); }
      createDataChannel(label: string, options?: RTCDataChannelInit) {
        const channel = super.createDataChannel(label, options);
        peers.find(row => row.pc === this)!.labels.push(label);
        const send = channel.send.bind(channel);
        channel.send = ((data: Parameters<RTCDataChannel['send']>[0]) => { send(data); record(label, data); }) as typeof channel.send;
        return channel;
      }
    };
    const NativeSocket = window.WebSocket;
    window.WebSocket = class extends NativeSocket {
      constructor(...args: ConstructorParameters<typeof NativeSocket>) {
        super(...args);
        if (new URL(String(args[0]), location.href).pathname === '/api/v1/browser/ws') { sockets.push(this); this.addEventListener('open', () => { openedSockets++; }); }
      }
      send(data: Parameters<WebSocket['send']>[0]) { super.send(data); if (sockets.includes(this)) record('websocket', data); }
    };
    (window as unknown as ProbeWindow).__inputSmoke = {
      openedSockets: () => openedSockets, routes, sample: () => null, sameMedia: () => false,
      closeInput() {
        const active = peers.filter(row => row.labels.includes('input-reliable') && row.pc.connectionState !== 'closed');
        if (active.length !== 1 || active[0].pc.getTransceivers().length !== 0) throw Error('Expected exactly one data-only input peer');
        active[0].pc.close();
      },
      closeSocket() {
        const active = sockets.filter(socket => socket.readyState === WebSocket.OPEN);
        if (active.length !== 1) throw Error('Expected exactly one live browser socket');
        active[0].close(4000, 'bounded input smoke');
      },
      peers: () => peers.map(row => ({ labels: row.labels, transceivers: row.pc.getTransceivers().length, state: row.pc.connectionState })),
    };
  });
}

// Same authored-border/object-contain mapping as the existing browser input probe.
// This smoke carries full UTF-16 text, not a hash, in addition to exact counters.
export async function installPixels(page: Page) {
  await page.evaluate(() => {
    const video = document.querySelector<HTMLVideoElement>('[data-testid="browser-live-video"]');
    if (!video || !(video.srcObject instanceof MediaStream)) throw Error('Real receiver video required');
    const stream = video.srcObject, track = stream.getVideoTracks()[0];
    const locator = document.createElement('canvas'), grid = document.createElement('canvas');
    const ctx = locator.getContext('2d', { willReadFrequently: true })!, cells = grid.getContext('2d', { willReadFrequently: true })!;
    grid.width = 32; grid.height = 24; cells.imageSmoothingEnabled = false;
    const probe = (window as unknown as ProbeWindow).__inputSmoke;
    probe.sameMedia = () => document.contains(video) && video.srcObject === stream && stream.getVideoTracks()[0] === track && track.readyState === 'live' && !video.paused && video.readyState >= 2;
    probe.sample = () => {
      if (!probe.sameMedia() || !video.videoWidth || !video.videoHeight) return null;
      locator.width = 384; locator.height = Math.round(384 * video.videoHeight / video.videoWidth);
      ctx.drawImage(video, 0, 0, locator.width, locator.height);
      const pixels = ctx.getImageData(0, 0, locator.width, locator.height).data;
      let left = locator.width, right = -1, top = locator.height, bottom = -1;
      for (let y = 0; y < locator.height; y++) {
        let start = -1;
        for (let x = 0; x <= locator.width; x++) {
          const p = (y * locator.width + x) * 4;
          const pink = x < locator.width && pixels[p] > 175 && pixels[p + 1] < 85 && pixels[p + 2] > 175;
          if (pink && start < 0) start = x;
          if (!pink && start >= 0) {
            if (x - start >= locator.width * .2) { left = Math.min(left, start); right = Math.max(right, x - 1); top = Math.min(top, y); bottom = Math.max(bottom, y); }
            start = -1;
          }
        }
      }
      if (right <= left || bottom <= top) return null;
      const geometry = { left, top, width: right - left + 1, height: bottom - top + 1, canvasWidth: locator.width, canvasHeight: locator.height };
      const sx = video.videoWidth / locator.width, sy = video.videoHeight / locator.height;
      cells.drawImage(video, (left + geometry.width * .1) * sx, (top + geometry.height * .06) * sy, geometry.width * .8 * sx, geometry.height * .5 * sy, 0, 0, 32, 24);
      const data = cells.getImageData(0, 0, 32, 24).data, bits: number[] = [];
      for (let i = 0; i < 768; i++) {
        const light = (data[i * 4] + data[i * 4 + 1] + data[i * 4 + 2]) / 3;
        if (light > 70 && light < 185) return null;
        bits.push(light >= 185 ? 1 : 0);
      }
      let offset = 0;
      const read = (size: number) => { let n = 0; for (let i = 0; i < size; i++) n += bits[offset++] * 2 ** i; return n; };
      const values = Array.from({ length: 8 }, () => read(32));
      const units = Array.from({ length: 32 }, () => read(16));
      const end = units.indexOf(0);
      return { geometry, state: { nonce: values[0], clicks: values[1], downs: values[2], ups: values[3], held: values[4], scroll: values[5], drags: values[6], errors: values[7], text: String.fromCharCode(...units.slice(0, end < 0 ? units.length : end)) } };
    };
  });
}

export async function stateIs(page: Page, state: InputState) {
  await expect.poll(() => page.evaluate(() => (window as unknown as ProbeWindow).__inputSmoke.sample()?.state), { timeout: 5000, intervals: [25, 50, 100] }).toEqual(state);
}

export async function point(page: Page, x: number, y: number) {
  const observation = await page.evaluate(() => (window as unknown as ProbeWindow).__inputSmoke.sample());
  if (!observation) throw Error('Current fixture pixels unavailable for pointer mapping');
  return browserLiveVideo(page).evaluate((video, args) => {
    const v = video as HTMLVideoElement, box = v.getBoundingClientRect(), g = args.geometry;
    const scale = Math.min(box.width / v.videoWidth, box.height / v.videoHeight);
    return {
      x: box.left + (box.width - v.videoWidth * scale) / 2 + (g.left + g.width * args.x) / g.canvasWidth * v.videoWidth * scale,
      y: box.top + (box.height - v.videoHeight * scale) / 2 + (g.top + g.height * args.y) / g.canvasHeight * v.videoHeight * scale,
    };
  }, { geometry: observation.geometry, x, y });
}

export async function routeEvidence(page: Page) {
  return page.evaluate(() => { const p = (window as unknown as ProbeWindow).__inputSmoke; return { routes: p.routes, peers: p.peers(), sameMedia: p.sameMedia(), openedSockets: p.openedSockets() }; });
}
export async function disconnect(page: Page, mode: string) {
  await page.evaluate(dedicated => { const p = (window as unknown as ProbeWindow).__inputSmoke; if (dedicated) p.closeInput(); else p.closeSocket(); }, mode === 'dedicated');
}

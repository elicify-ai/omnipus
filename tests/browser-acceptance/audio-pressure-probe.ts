import type { Page } from '@playwright/test';

export function decodeAudioPressureWords(words: number[]) {
  if (words.length !== 4 || words.some(word => !Number.isInteger(word) || word < 0 || word > 0xffffffff)) throw Error('Four unsigned stimulus words required');
  const header = words[0];
  if ((header & 0xffff) !== 0xa531 || (header >>> 22) !== 0) throw Error('Invalid audio stimulus signature or reserved bits');
  return { phase: (header >>> 16) & 3, contextState: (header >>> 18) & 3, started: !!(header & (1 << 20)), fault: !!(header & (1 << 21)), elapsedMs: words[1], contextMs: words[2], analyserRMS: words[3] / 1e6 };
}

export async function installAudioPressureProbe(page: Page) {
  await page.addInitScript(() => {
    const peers: RTCPeerConnection[] = [];
    const Native = window.RTCPeerConnection;
    window.RTCPeerConnection = class extends Native {
      constructor(...args: ConstructorParameters<typeof Native>) { super(...args); peers.push(this); }
    };
    (window as unknown as { __audioPressurePeers: RTCPeerConnection[] }).__audioPressurePeers = peers;
  });
}

export async function sampleAudioPressure(page: Page) {
  const sample = await page.evaluate(async () => {
    type Geometry = { left: number; top: number; width: number; height: number; canvasWidth: number; canvasHeight: number };
    const w = window as unknown as { __inputSmoke?: { sample(): { geometry: Geometry } | null }; __audioPressurePeers: RTCPeerConnection[] };
    const video = document.querySelector<HTMLVideoElement>('[data-testid="browser-live-video"]');
    const g = w.__inputSmoke?.sample()?.geometry;
    if (!g || !video || !(video.srcObject instanceof MediaStream) || video.readyState < 2) return null;
    const canvas = document.createElement('canvas'); canvas.width = video.videoWidth; canvas.height = video.videoHeight;
    const ctx = canvas.getContext('2d', { willReadFrequently: true })!;
    ctx.drawImage(video, 0, 0);
    const words: number[] = [];
    for (let row = 0; row < 4; row++) {
      let word = 0;
      for (let bit = 0; bit < 32; bit++) {
        const x = (g.left + g.width * (.1 + .8 * (bit + .5) / 32)) / g.canvasWidth * canvas.width;
        const y = (g.top + g.height * (.581 + row * .012)) / g.canvasHeight * canvas.height;
        const pixel = ctx.getImageData(Math.floor(x), Math.floor(y), 1, 1).data;
        const light = (pixel[0] + pixel[1] + pixel[2]) / 3;
        if (light > 70 && light < 185) return null;
        if (light >= 185) word += 2 ** bit;
      }
      words.push(word);
    }
    const tracks = video.srcObject.getTracks();
    const peers = w.__audioPressurePeers.filter(pc => pc.connectionState === 'connected' && pc.getReceivers().some(receiver => tracks.includes(receiver.track)));
    const audio: Array<Record<string, number | string>> = [];
    if (peers.length === 1) {
      const report = await peers[0].getStats();
      for (const row of report.values()) {
        if (row.type !== 'inbound-rtp' || row.kind !== 'audio') continue;
        const values: Record<string, number | string> = { kind: 'audio' };
        for (const key of ['timestamp', 'ssrc', 'totalAudioEnergy', 'totalSamplesDuration', 'totalSamplesReceived', 'packetsReceived', 'packetsLost', 'concealedSamples', 'silentConcealedSamples', 'jitterBufferDelay', 'jitterBufferEmittedCount', 'jitterBufferTargetDelay', 'jitterBufferMinimumDelay']) if (typeof row[key] === 'number' && Number.isFinite(row[key])) values[key] = row[key];
        audio.push(values);
      }
    }
    return { words, muted: video.muted, at: new Date().toISOString(), audio, matchingMediaPeers: peers.length };
  });
  return sample && { ...sample, ...decodeAudioPressureWords(sample.words) };
}

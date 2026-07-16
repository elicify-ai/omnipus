// Node test for the encoder page's envelope + codec-selection logic.
//
// Why this shape: encoder.html MUST ship as one fully self-contained file
// (no external <script src>, per FR-016/the epic's isolation requirement),
// so the pure logic cannot live in its own importable module without
// either (a) duplicating it (drift risk) or (b) fetching it at runtime
// (forbidden — no external assets on the encoder page). Instead this test
// extracts the EXACT substring the page ships between the
// `OMNIPUS_TESTABLE_LOGIC_START`/`_END` markers in encoder.html, appends a
// test-only `export` shim, and runs it as a real ES module under Node — so
// what's under test is byte-for-byte what the browser executes, not a
// hand-maintained copy.
//
// Run: node --test pkg/tools/browser/encoderpage/encoder_logic.test.mjs
// (vitest's `include` glob is scoped to src/**/*.test.{ts,tsx} and does not
// reach this file — see vite.config.ts — so this repo's vitest run does
// not, and should not be expected to, pick this file up.)

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, mkdtempSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, dirname } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const htmlPath = join(here, 'encoder.html');
const html = readFileSync(htmlPath, 'utf8');

const START_MARKER = '// >>> OMNIPUS_TESTABLE_LOGIC_START';
const END_MARKER = '// <<< OMNIPUS_TESTABLE_LOGIC_END';

const startIdx = html.indexOf(START_MARKER);
const endIdx = html.indexOf(END_MARKER);
assert.ok(startIdx !== -1, `encoder.html is missing ${START_MARKER}`);
assert.ok(endIdx !== -1, `encoder.html is missing ${END_MARKER}`);
assert.ok(endIdx > startIdx, 'END marker precedes START marker in encoder.html');

const logicSource = html.slice(startIdx, endIdx);

const moduleSource = `${logicSource}
export {
  encodeFrameFeed,
  decodeFrameFeed,
  encodeChunkEnvelope,
  decodeChunkEnvelope,
  tagIngestPayload,
  untagIngestPayload,
  msFromMicros,
  microsFromMs,
  selectVideoCodec,
  INGEST_PAYLOAD_KIND_VIDEO,
  INGEST_PAYLOAD_KIND_AUDIO,
  VIDEO_CODEC_CANDIDATES,
};
`;

const tmpDir = mkdtempSync(join(tmpdir(), 'omnipus-encoderpage-'));
const tmpModulePath = join(tmpDir, 'extracted-logic.mjs');
writeFileSync(tmpModulePath, moduleSource, 'utf8');

const logic = await import(pathToFileURL(tmpModulePath).href);

test('INGEST_PAYLOAD_KIND constants are 0 (video) and 1 (audio)', () => {
  assert.equal(logic.INGEST_PAYLOAD_KIND_VIDEO, 0);
  assert.equal(logic.INGEST_PAYLOAD_KIND_AUDIO, 1);
});

test('msFromMicros / microsFromMs convert between WebCodecs microseconds and the wire millisecond field', () => {
  assert.equal(logic.msFromMicros(33_000), 33);
  assert.equal(logic.microsFromMs(33), 33_000);
  // Round-trip is lossy at sub-millisecond precision (by design — the wire
  // field is milliseconds); assert the documented rounding, not exactness.
  assert.equal(logic.msFromMicros(33_499), 33);
  assert.equal(logic.msFromMicros(33_501), 34);
});

test('encodeFrameFeed / decodeFrameFeed round-trip (downstream browser_frame_feed, BrowserFrameFeedEnvelope)', () => {
  const payload = new Uint8Array([0xff, 0xd8, 0xff, 0xe0, 1, 2, 3]); // fake JPEG-ish bytes
  const buf = logic.encodeFrameFeed(42, 1_700_000, payload);

  // Byte layout: [0:4) seq u32 BE, [4:12) ts u64 BE (ms), [12:16) len u32 BE, [16:) payload
  assert.equal(buf.byteLength, 16 + payload.byteLength);
  const view = new DataView(buf);
  assert.equal(view.getUint32(0, false), 42);
  assert.equal(Number(view.getBigUint64(4, false)), 1_700_000);
  assert.equal(view.getUint32(12, false), payload.byteLength);

  const decoded = logic.decodeFrameFeed(buf);
  assert.equal(decoded.seq, 42);
  assert.equal(decoded.tsMillis, 1_700_000);
  assert.equal(decoded.len, payload.byteLength);
  assert.deepEqual(new Uint8Array(decoded.payload), payload);
});

test('encodeFrameFeed / decodeFrameFeed: zero-length payload round-trips (empty frame)', () => {
  const buf = logic.encodeFrameFeed(5, 132, new Uint8Array(0));
  assert.equal(buf.byteLength, 16);
  const decoded = logic.decodeFrameFeed(buf);
  assert.equal(decoded.len, 0);
  assert.equal(decoded.payload.byteLength, 0);
});

test('decodeFrameFeed rejects a truncated header', () => {
  assert.throws(() => logic.decodeFrameFeed(new ArrayBuffer(8)), RangeError);
});

test('decodeFrameFeed rejects a len that overruns the buffer', () => {
  const buf = new ArrayBuffer(20);
  const view = new DataView(buf);
  view.setUint32(12, 999, false); // claims 999 bytes of payload in a 20-byte buffer
  assert.throws(() => logic.decodeFrameFeed(buf), RangeError);
});

test('encodeChunkEnvelope / decodeChunkEnvelope round-trip: video keyframe (DS-2 row: 2MB-class keyframe), matches BrowserChunkEnvelope', () => {
  const payload = new Uint8Array(16); // small stand-in for a real keyframe payload
  payload.fill(0xab);
  const buf = logic.encodeChunkEnvelope(0, 0, true, payload);

  // Byte layout: [0:4) seq u32 BE, [4:12) ts u64 BE (ms), [12:13) key u8,
  // [13:17) len u32 BE, [17:) payload — 17-byte header, matches
  // contracts/components/schemas/BrowserChunkEnvelope.yaml exactly (no
  // extra kind byte at the envelope level).
  assert.equal(buf.byteLength, 17 + payload.byteLength);
  const view = new DataView(buf);
  assert.equal(view.getUint32(0, false), 0);
  assert.equal(Number(view.getBigUint64(4, false)), 0);
  assert.equal(view.getUint8(12), 1);
  assert.equal(view.getUint32(13, false), payload.byteLength);

  const decoded = logic.decodeChunkEnvelope(buf);
  assert.equal(decoded.seq, 0);
  assert.equal(decoded.key, true);
  assert.deepEqual(new Uint8Array(decoded.payload), payload);
});

test('encodeChunkEnvelope / decodeChunkEnvelope round-trip: delta chunk (DS-2 row: typical delta)', () => {
  const payload = new Uint8Array(32).fill(7);
  const buf = logic.encodeChunkEnvelope(1, 33, false, payload);
  const decoded = logic.decodeChunkEnvelope(buf);
  assert.equal(decoded.seq, 1);
  assert.equal(decoded.tsMillis, 33);
  assert.equal(decoded.key, false);
});

test('encodeChunkEnvelope / decodeChunkEnvelope round-trip: DS-2 max seq/ts row (4294967295 / u64 max)', () => {
  const payload = new Uint8Array(4).fill(9);
  const buf = logic.encodeChunkEnvelope(4294967295, 18446744073709551615n, false, payload);
  const decoded = logic.decodeChunkEnvelope(buf);
  assert.equal(decoded.seq, 4294967295);
  // 2^64-1 exceeds Number.MAX_SAFE_INTEGER, so getUint64BE returns a BigInt.
  assert.equal(decoded.tsMillis, 18446744073709551615n);
});

test('decodeChunkEnvelope rejects a truncated header', () => {
  assert.throws(() => logic.decodeChunkEnvelope(new ArrayBuffer(10)), RangeError);
});

test('decodeChunkEnvelope rejects a len that overruns the buffer', () => {
  const buf = new ArrayBuffer(20);
  const view = new DataView(buf);
  view.setUint32(13, 999, false);
  assert.throws(() => logic.decodeChunkEnvelope(buf), RangeError);
});

// ---- Ingest kind-tagging (video vs audio multiplexed over browser_ingest_chunk) --

test('tagIngestPayload / untagIngestPayload round-trip: video', () => {
  const raw = new Uint8Array([1, 2, 3, 4, 5]);
  const tagged = logic.tagIngestPayload(logic.INGEST_PAYLOAD_KIND_VIDEO, raw);
  assert.equal(tagged.byteLength, raw.byteLength + 1);
  assert.equal(tagged[0], logic.INGEST_PAYLOAD_KIND_VIDEO);

  const { kind, payload } = logic.untagIngestPayload(tagged);
  assert.equal(kind, logic.INGEST_PAYLOAD_KIND_VIDEO);
  assert.deepEqual(new Uint8Array(payload), raw);
});

test('tagIngestPayload / untagIngestPayload round-trip: audio', () => {
  const raw = new Uint8Array([9, 8, 7]);
  const tagged = logic.tagIngestPayload(logic.INGEST_PAYLOAD_KIND_AUDIO, raw);
  const { kind, payload } = logic.untagIngestPayload(tagged);
  assert.equal(kind, logic.INGEST_PAYLOAD_KIND_AUDIO);
  assert.deepEqual(new Uint8Array(payload), raw);
});

test('untagIngestPayload rejects an empty (missing kind-tag) payload', () => {
  assert.throws(() => logic.untagIngestPayload(new Uint8Array(0)), RangeError);
});

test('full upstream round-trip: encode a tagged audio payload inside a BrowserChunkEnvelope, then decode both layers', () => {
  const rawOpus = new Uint8Array([10, 20, 30]);
  const tagged = logic.tagIngestPayload(logic.INGEST_PAYLOAD_KIND_AUDIO, rawOpus);
  const envelope = logic.encodeChunkEnvelope(7, 99, false, tagged);

  const decodedEnvelope = logic.decodeChunkEnvelope(envelope);
  assert.equal(decodedEnvelope.seq, 7);
  assert.equal(decodedEnvelope.tsMillis, 99);
  assert.equal(decodedEnvelope.key, false);
  // len covers the 1-byte kind tag + the real payload.
  assert.equal(decodedEnvelope.len, rawOpus.byteLength + 1);

  const { kind, payload } = logic.untagIngestPayload(decodedEnvelope.payload);
  assert.equal(kind, logic.INGEST_PAYLOAD_KIND_AUDIO);
  assert.deepEqual(new Uint8Array(payload), rawOpus);
});

// ---- Codec selection ------------------------------------------------------

function fakeVideoEncoderCtor(supportedCodecs) {
  return {
    isConfigSupported: async (config) => {
      if (supportedCodecs.includes(config.codec)) {
        return { supported: true, config };
      }
      return { supported: false, config };
    },
  };
}

test('selectVideoCodec prefers h264-main (avc1.4D4028) when supported (FR-006)', async () => {
  const ctor = fakeVideoEncoderCtor(['avc1.4D4028', 'vp8']);
  const result = await logic.selectVideoCodec(1280, 720, 30, 2_500_000, ctor);
  assert.equal(result.label, 'h264-main');
  assert.equal(result.config.codec, 'avc1.4D4028');
  assert.deepEqual(result.config.avc, { format: 'annexb' });
});

test('selectVideoCodec falls back to vp8 when h264-main is unsupported', async () => {
  const ctor = fakeVideoEncoderCtor(['vp8']);
  const result = await logic.selectVideoCodec(1280, 720, 30, 2_500_000, ctor);
  assert.equal(result.label, 'vp8');
  assert.equal(result.config.codec, 'vp8');
});

test('selectVideoCodec returns null when neither codec is supported (US-6/AC-3 -> unavailable state)', async () => {
  const ctor = fakeVideoEncoderCtor([]);
  const result = await logic.selectVideoCodec(1280, 720, 30, 2_500_000, ctor);
  assert.equal(result, null);
});

test('selectVideoCodec tolerates isConfigSupported throwing for one candidate and still finds the next', async () => {
  const ctor = {
    isConfigSupported: async (config) => {
      if (config.codec === 'avc1.4D4028') {
        throw new Error('unsupported codec string');
      }
      return { supported: true, config };
    },
  };
  const result = await logic.selectVideoCodec(1280, 720, 30, 2_500_000, ctor);
  assert.equal(result.label, 'vp8');
});

test('selectVideoCodec never offers H.264 baseline (avc1.42E01E) — S-1b', () => {
  const offeredCodecs = logic.VIDEO_CODEC_CANDIDATES.map((c) => c.codec);
  assert.deepEqual(offeredCodecs, ['avc1.4D4028', 'vp8'], 'the candidate list must be exactly h264-main then vp8 — never H.264 baseline');
});

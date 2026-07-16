/**
 * browserLiveWs.binary-frames.test.ts — Test 20 (live-browser-video-streaming
 * spec, TDD plan). Covers FR-005/FR-006/FR-011/FR-017/FR-018:
 *
 *  - `binaryType` is set to 'arraybuffer' on the underlying WebSocket.
 *  - `decodeChunkEnvelope` parses the manual BigEndian envelope (and rejects
 *    malformed input) — DS-2-style edge cases including the seq/ts u32/u64
 *    max boundary row.
 *  - A binary ArrayBuffer message is routed to a mocked VideoDecoder/
 *    AudioDecoder (configure + decode), fed from a preceding
 *    browser_stream_init; decoded output is delivered via onVideoFrame/
 *    onAudioData and the frame/data is closed afterwards.
 *  - The existing JSON control-frame path (browser_screencast/browser_status/
 *    browser_tabs/error) still routes exactly as before — this is additive,
 *    not a replacement.
 *  - FR-005 capability probing populates video_caps/audio_caps on
 *    browser_attach via VideoDecoder/AudioDecoder.isConfigSupported.
 *  - FR-018 bring-up-timeout → onUnavailable when no live frame ever arrives;
 *    never fires once a live frame (of any kind) has been received.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import {
  BrowserLiveWsConnection,
  decodeChunkEnvelope,
  getDroppedBinaryChunkCount,
  resetDroppedBinaryChunkCount,
  type BrowserLiveWsCallbacks,
} from './browserLiveWs'

// ── Mock WebSocket (mirrors browserLiveWs.test.ts's own fixture) ───────────

let lastWsInstance: {
  onopen: (() => void) | null
  onmessage: ((ev: { data: unknown }) => void) | null
  onclose: ((ev: { code: number; reason: string }) => void) | null
  onerror: (() => void) | null
  send: ReturnType<typeof vi.fn>
  close: ReturnType<typeof vi.fn>
  readyState: number
  binaryType: string
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
const MockWebSocket = vi.fn(function (this: any) {
  this.onopen = null
  this.onmessage = null
  this.onclose = null
  this.onerror = null
  this.send = vi.fn()
  this.close = vi.fn()
  this.readyState = 1 // OPEN
  this.binaryType = 'blob' // default the real WebSocket ships with — proves the code under test flips it
  lastWsInstance = this
}) as unknown as typeof WebSocket & {
  OPEN: number
  CLOSED: number
  mockClear: () => void
  mock: { calls: unknown[][] }
}

MockWebSocket.OPEN = 1
MockWebSocket.CLOSED = 3

function openSocket() {
  lastWsInstance.onopen?.()
}

function sentJsonFrames(): unknown[] {
  return lastWsInstance.send.mock.calls
    .map((call) => call[0] as string)
    .filter((data) => {
      try {
        JSON.parse(data)
        return true
      } catch {
        return false
      }
    })
    .map((data) => JSON.parse(data) as unknown)
}

/** Flushes the microtask queue — the caps-probing attach path awaits Promise.all before sending. */
async function flushMicrotasks() {
  await Promise.resolve()
  await Promise.resolve()
  await Promise.resolve()
}

function makeCallbacks(overrides: Partial<BrowserLiveWsCallbacks> = {}): BrowserLiveWsCallbacks {
  return {
    onScreencast: vi.fn(),
    onStatus: vi.fn(),
    onTabs: vi.fn(),
    onError: vi.fn(),
    onConnected: vi.fn(),
    onDisconnected: vi.fn(),
    onStreamInit: vi.fn(),
    onVideoFrame: vi.fn(),
    onAudioData: vi.fn(),
    onDecodeError: vi.fn(),
    onUnavailable: vi.fn(),
    onCaps: vi.fn(),
    ...overrides,
  }
}

// ── Envelope builder — BigEndian kind(u8)+seq(u32)+ts(u64)+key(u8)+len(u32)+payload ──

function buildEnvelope(opts: { kind: 0 | 1; seq: number; ts: bigint; key: boolean; payload: Uint8Array }): ArrayBuffer {
  const buf = new ArrayBuffer(18 + opts.payload.length)
  const view = new DataView(buf)
  view.setUint8(0, opts.kind)
  view.setUint32(1, opts.seq, false)
  view.setBigUint64(5, opts.ts, false)
  view.setUint8(13, opts.key ? 1 : 0)
  view.setUint32(14, opts.payload.length, false)
  new Uint8Array(buf).set(opts.payload, 18)
  return buf
}

// ── Mock WebCodecs decoders ─────────────────────────────────────────────────

interface FakeVideoFrame {
  displayWidth: number
  displayHeight: number
  close: ReturnType<typeof vi.fn>
}

interface FakeAudioData {
  numberOfChannels: number
  numberOfFrames: number
  sampleRate: number
  close: ReturnType<typeof vi.fn>
}

let lastVideoDecoderInstance: {
  output: (f: FakeVideoFrame) => void
  error: (e: DOMException) => void
  state: string
  configure: ReturnType<typeof vi.fn>
  decode: ReturnType<typeof vi.fn>
  close: ReturnType<typeof vi.fn>
} | null = null

let lastAudioDecoderInstance: {
  output: (d: FakeAudioData) => void
  error: (e: DOMException) => void
  state: string
  configure: ReturnType<typeof vi.fn>
  decode: ReturnType<typeof vi.fn>
  close: ReturnType<typeof vi.fn>
} | null = null

const videoConfigureShouldThrow = { value: false }

// Kept as plain `vi.fn()` Mock-typed handles (never cast away) purely so
// `.mockClear()` stays available in beforeEach — the `as unknown as typeof
// VideoDecoder`-cast `MockVideoDecoder`/`MockAudioDecoder` consts below
// (needed for vi.stubGlobal, which expects the real global's type) lose
// vitest's Mock members once intersected with the strict DOM constructor
// signature (isConfigSupported's real param type, AudioDecoderConfig,
// requires numberOfChannels/sampleRate — this file's mocks only need codec).
const rawVideoDecoderCtor = vi.fn(function (
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  this: any,
  init: { output: (f: FakeVideoFrame) => void; error: (e: DOMException) => void },
) {
  this.output = init.output
  this.error = init.error
  this.state = 'unconfigured'
  this.configure = vi.fn(() => {
    if (videoConfigureShouldThrow.value) {
      throw new DOMException('unsupported config', 'NotSupportedError')
    }
    this.state = 'configured'
  })
  this.decode = vi.fn()
  this.close = vi.fn(() => {
    this.state = 'closed'
  })
  lastVideoDecoderInstance = this
})
const rawVideoIsConfigSupported = vi.fn(async (config: VideoDecoderConfig) => ({
  supported: config.codec === 'avc1.4D4028',
  config,
}))
const MockVideoDecoder = rawVideoDecoderCtor as unknown as typeof VideoDecoder & {
  isConfigSupported: typeof rawVideoIsConfigSupported
}
MockVideoDecoder.isConfigSupported = rawVideoIsConfigSupported

const rawAudioDecoderCtor = vi.fn(function (
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  this: any,
  init: { output: (d: FakeAudioData) => void; error: (e: DOMException) => void },
) {
  this.output = init.output
  this.error = init.error
  this.state = 'unconfigured'
  this.configure = vi.fn(() => {
    this.state = 'configured'
  })
  this.decode = vi.fn()
  this.close = vi.fn(() => {
    this.state = 'closed'
  })
  lastAudioDecoderInstance = this
})
const rawAudioIsConfigSupported = vi.fn(async (config: AudioDecoderConfig) => ({
  supported: config.codec === 'opus',
  config,
}))
const MockAudioDecoder = rawAudioDecoderCtor as unknown as typeof AudioDecoder & {
  isConfigSupported: typeof rawAudioIsConfigSupported
}
MockAudioDecoder.isConfigSupported = rawAudioIsConfigSupported

const rawEncodedVideoChunkCtor = vi.fn(function (
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  this: any,
  init: { type: string; timestamp: number; data: Uint8Array },
) {
  Object.assign(this, init)
})
const MockEncodedVideoChunk = rawEncodedVideoChunkCtor as unknown as typeof EncodedVideoChunk

const rawEncodedAudioChunkCtor = vi.fn(function (
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  this: any,
  init: { type: string; timestamp: number; data: Uint8Array },
) {
  Object.assign(this, init)
})
const MockEncodedAudioChunk = rawEncodedAudioChunkCtor as unknown as typeof EncodedAudioChunk

beforeEach(() => {
  MockWebSocket.mockClear()
  rawVideoDecoderCtor.mockClear()
  rawVideoIsConfigSupported.mockClear()
  rawAudioDecoderCtor.mockClear()
  rawAudioIsConfigSupported.mockClear()
  rawEncodedVideoChunkCtor.mockClear()
  rawEncodedAudioChunkCtor.mockClear()
  videoConfigureShouldThrow.value = false
  lastVideoDecoderInstance = null
  lastAudioDecoderInstance = null
  resetDroppedBinaryChunkCount()
  vi.stubGlobal('WebSocket', MockWebSocket)
  vi.stubGlobal('VideoDecoder', MockVideoDecoder)
  vi.stubGlobal('AudioDecoder', MockAudioDecoder)
  vi.stubGlobal('EncodedVideoChunk', MockEncodedVideoChunk)
  vi.stubGlobal('EncodedAudioChunk', MockEncodedAudioChunk)
  vi.stubGlobal('sessionStorage', { getItem: vi.fn(() => 'test-token') })
  vi.stubGlobal('localStorage', { getItem: vi.fn(() => null) })
  vi.useFakeTimers()
})

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('decodeChunkEnvelope', () => {
  it('round-trips a typical video keyframe envelope', () => {
    const payload = new Uint8Array([1, 2, 3, 4, 5])
    const buf = buildEnvelope({ kind: 0, seq: 1, ts: 33n, key: true, payload })
    const decoded = decodeChunkEnvelope(buf)
    expect(decoded).toEqual({ kind: 'video', seq: 1, ts: 33n, key: true, payload })
  })

  it('round-trips a typical audio delta chunk envelope', () => {
    const payload = new Uint8Array([9, 9])
    const buf = buildEnvelope({ kind: 1, seq: 7, ts: 231n, key: false, payload })
    const decoded = decodeChunkEnvelope(buf)
    expect(decoded).toEqual({ kind: 'audio', seq: 7, ts: 231n, key: false, payload })
  })

  it('round-trips the DS-2 seq/ts max-boundary row (u32 max seq, u64 max ts)', () => {
    const payload = new Uint8Array([0xff])
    const buf = buildEnvelope({ kind: 0, seq: 4294967295, ts: 18446744073709551615n, key: false, payload })
    const decoded = decodeChunkEnvelope(buf)
    expect(decoded?.seq).toBe(4294967295)
    expect(decoded?.ts).toBe(18446744073709551615n)
  })

  it('parses a zero-length payload without error (client-side; ingest-side rejection is a backend concern, FR-014)', () => {
    const buf = buildEnvelope({ kind: 0, seq: 5, ts: 132n, key: false, payload: new Uint8Array(0) })
    const decoded = decodeChunkEnvelope(buf)
    expect(decoded).toEqual({ kind: 'video', seq: 5, ts: 132n, key: false, payload: new Uint8Array(0) })
  })

  it('rejects a buffer too short to hold the header', () => {
    expect(decodeChunkEnvelope(new ArrayBuffer(4))).toBeNull()
  })

  it('rejects an unrecognized kind byte', () => {
    const buf = buildEnvelope({ kind: 0, seq: 1, ts: 1n, key: false, payload: new Uint8Array(1) })
    new DataView(buf).setUint8(0, 2) // neither CHUNK_KIND_VIDEO(0) nor CHUNK_KIND_AUDIO(1)
    expect(decodeChunkEnvelope(buf)).toBeNull()
  })

  it('rejects a declared len that does not match the actual trailing byte count (truncated/oversize)', () => {
    const buf = buildEnvelope({ kind: 0, seq: 1, ts: 1n, key: true, payload: new Uint8Array(10) })
    new DataView(buf).setUint32(14, 999, false) // lie about the length
    expect(decodeChunkEnvelope(buf)).toBeNull()
  })
})

describe('BrowserLiveWsConnection — binary transport wiring', () => {
  it('sets binaryType to arraybuffer on the underlying WebSocket', () => {
    const conn = new BrowserLiveWsConnection('sess-1', 'agent-1', makeCallbacks())
    conn.connect()
    expect(lastWsInstance.binaryType).toBe('arraybuffer')
  })

  it('advertises video_caps/audio_caps in browser_attach once capability probing resolves', async () => {
    const conn = new BrowserLiveWsConnection('sess-1', 'agent-1', makeCallbacks())
    conn.connect()
    openSocket()
    await flushMicrotasks()

    expect(MockVideoDecoder.isConfigSupported).toHaveBeenCalledWith({ codec: 'avc1.4D4028' })
    expect(MockVideoDecoder.isConfigSupported).toHaveBeenCalledWith({ codec: 'vp8' })
    expect(MockAudioDecoder.isConfigSupported).toHaveBeenCalledWith({ codec: 'opus', sampleRate: 48000, numberOfChannels: 2 })

    const attach = sentJsonFrames().find((f) => (f as { type: string }).type === 'browser_attach') as {
      video_caps?: string[]
      audio_caps?: string[]
    }
    expect(attach.video_caps).toEqual(['avc1.4D4028']) // only avc1.4D4028 is "supported" per the mock
    expect(attach.audio_caps).toEqual(['opus'])
  })

  it('routes a browser_stream_init frame to onStreamInit and configures the VideoDecoder + AudioDecoder', async () => {
    const callbacks = makeCallbacks()
    const conn = new BrowserLiveWsConnection('sess-1', 'agent-1', callbacks)
    conn.connect()
    openSocket()
    await flushMicrotasks()

    const initFrame = {
      type: 'browser_stream_init',
      codec: 'avc1.4D4028',
      width: 1280,
      height: 720,
      keyframe_interval: 60,
      has_audio: true,
    }
    lastWsInstance.onmessage?.({ data: JSON.stringify(initFrame) })

    expect(lastVideoDecoderInstance?.configure).toHaveBeenCalledWith({
      codec: 'avc1.4D4028',
      codedWidth: 1280,
      codedHeight: 720,
    })
    expect(lastAudioDecoderInstance?.configure).toHaveBeenCalledWith({
      codec: 'opus',
      sampleRate: 48000,
      numberOfChannels: 2,
    })
    expect(callbacks.onStreamInit).toHaveBeenCalledWith(initFrame)
  })

  it('does NOT call onStreamInit when the negotiated codec fails to configure, and calls onUnavailable instead', async () => {
    videoConfigureShouldThrow.value = true
    const callbacks = makeCallbacks()
    const conn = new BrowserLiveWsConnection('sess-1', 'agent-1', callbacks)
    conn.connect()
    openSocket()
    await flushMicrotasks()

    lastWsInstance.onmessage?.({
      data: JSON.stringify({
        type: 'browser_stream_init',
        codec: 'av1.unsupported',
        width: 100,
        height: 100,
        keyframe_interval: 60,
        has_audio: false,
      }),
    })

    expect(callbacks.onStreamInit).not.toHaveBeenCalled()
    expect(callbacks.onUnavailable).toHaveBeenCalledWith('video-decoder-configure-failed')
    expect(callbacks.onDecodeError).toHaveBeenCalledWith('video', expect.any(String))
  })

  it('feeds a binary video chunk to the mocked VideoDecoder.decode as an EncodedVideoChunk', async () => {
    const callbacks = makeCallbacks()
    const conn = new BrowserLiveWsConnection('sess-1', 'agent-1', callbacks)
    conn.connect()
    openSocket()
    await flushMicrotasks()
    lastWsInstance.onmessage?.({
      data: JSON.stringify({
        type: 'browser_stream_init',
        codec: 'avc1.4D4028',
        width: 640,
        height: 480,
        keyframe_interval: 60,
        has_audio: false,
      }),
    })

    const payload = new Uint8Array([1, 2, 3])
    const buf = buildEnvelope({ kind: 0, seq: 1, ts: 33n, key: true, payload })
    lastWsInstance.onmessage?.({ data: buf })

    expect(lastVideoDecoderInstance?.decode).toHaveBeenCalledTimes(1)
    expect(MockEncodedVideoChunk).toHaveBeenCalledWith({ type: 'key', timestamp: 33, data: payload })
  })

  it('feeds a binary audio chunk to the mocked AudioDecoder.decode as an EncodedAudioChunk when has_audio', async () => {
    const conn = new BrowserLiveWsConnection('sess-1', 'agent-1', makeCallbacks())
    conn.connect()
    openSocket()
    await flushMicrotasks()
    lastWsInstance.onmessage?.({
      data: JSON.stringify({
        type: 'browser_stream_init',
        codec: 'avc1.4D4028',
        width: 640,
        height: 480,
        keyframe_interval: 60,
        has_audio: true,
      }),
    })

    const payload = new Uint8Array([9, 9])
    const buf = buildEnvelope({ kind: 1, seq: 2, ts: 66n, key: false, payload })
    lastWsInstance.onmessage?.({ data: buf })

    expect(lastAudioDecoderInstance?.decode).toHaveBeenCalledTimes(1)
    expect(MockEncodedAudioChunk).toHaveBeenCalledWith({ type: 'delta', timestamp: 66, data: payload })
  })

  it('delivers decoded VideoFrame output via onVideoFrame and closes the frame afterwards', async () => {
    const callbacks = makeCallbacks()
    const conn = new BrowserLiveWsConnection('sess-1', 'agent-1', callbacks)
    conn.connect()
    openSocket()
    await flushMicrotasks()
    lastWsInstance.onmessage?.({
      data: JSON.stringify({
        type: 'browser_stream_init',
        codec: 'avc1.4D4028',
        width: 640,
        height: 480,
        keyframe_interval: 60,
        has_audio: false,
      }),
    })

    const fakeFrame: FakeVideoFrame = { displayWidth: 640, displayHeight: 480, close: vi.fn() }
    lastVideoDecoderInstance?.output(fakeFrame)

    expect(callbacks.onVideoFrame).toHaveBeenCalledWith(fakeFrame)
    expect(fakeFrame.close).toHaveBeenCalledTimes(1)
  })

  it('routes a VideoDecoder error callback to onDecodeError', async () => {
    const callbacks = makeCallbacks()
    const conn = new BrowserLiveWsConnection('sess-1', 'agent-1', callbacks)
    conn.connect()
    openSocket()
    await flushMicrotasks()
    lastWsInstance.onmessage?.({
      data: JSON.stringify({
        type: 'browser_stream_init',
        codec: 'avc1.4D4028',
        width: 640,
        height: 480,
        keyframe_interval: 60,
        has_audio: false,
      }),
    })

    lastVideoDecoderInstance?.error(new DOMException('boom', 'EncodingError'))
    expect(callbacks.onDecodeError).toHaveBeenCalledWith('video', 'boom')
  })

  it('drops a malformed binary message, counts it, and never calls decode', async () => {
    const callbacks = makeCallbacks()
    const conn = new BrowserLiveWsConnection('sess-1', 'agent-1', callbacks)
    conn.connect()
    openSocket()
    await flushMicrotasks()
    lastWsInstance.onmessage?.({
      data: JSON.stringify({
        type: 'browser_stream_init',
        codec: 'avc1.4D4028',
        width: 640,
        height: 480,
        keyframe_interval: 60,
        has_audio: false,
      }),
    })

    const before = getDroppedBinaryChunkCount()
    lastWsInstance.onmessage?.({ data: new ArrayBuffer(2) }) // too short

    expect(getDroppedBinaryChunkCount()).toBe(before + 1)
    expect(lastVideoDecoderInstance?.decode).not.toHaveBeenCalled()
  })

  it('still routes a browser_screencast (JSON) frame to onScreencast, unaffected by the binary path', async () => {
    const callbacks = makeCallbacks()
    const conn = new BrowserLiveWsConnection('sess-1', 'agent-1', callbacks)
    conn.connect()
    openSocket()
    await flushMicrotasks()

    const frame = { type: 'browser_screencast', session_id: 'sess-1', seq: 1, data: 'base64jpeg', width: 1280, height: 720 }
    lastWsInstance.onmessage?.({ data: JSON.stringify(frame) })

    expect(callbacks.onScreencast).toHaveBeenCalledWith(frame)
  })

  it('still routes browser_status/browser_tabs/error JSON frames exactly as before', async () => {
    const callbacks = makeCallbacks()
    const conn = new BrowserLiveWsConnection('sess-1', 'agent-1', callbacks)
    conn.connect()
    openSocket()
    await flushMicrotasks()

    lastWsInstance.onmessage?.({ data: JSON.stringify({ type: 'browser_status', state: 'controlling' }) })
    expect(callbacks.onStatus).toHaveBeenCalledWith({ type: 'browser_status', state: 'controlling' })

    const tabsFrame = { type: 'browser_tabs', active_index: 0, tabs: [{ index: 0, title: 'A', url: 'https://a', active: true }] }
    lastWsInstance.onmessage?.({ data: JSON.stringify(tabsFrame) })
    expect(callbacks.onTabs).toHaveBeenCalledWith(tabsFrame)

    lastWsInstance.onmessage?.({ data: JSON.stringify({ type: 'error', message: 'boom' }) })
    expect(callbacks.onError).toHaveBeenCalledWith('boom')
  })
})

describe('BrowserLiveWsConnection — FR-018 bring-up timeout → onUnavailable', () => {
  it('calls onUnavailable if no live frame of any kind arrives before the bring-up timeout', async () => {
    const callbacks = makeCallbacks()
    const conn = new BrowserLiveWsConnection('sess-1', 'agent-1', callbacks)
    conn.connect()
    openSocket()
    await flushMicrotasks()

    await vi.advanceTimersByTimeAsync(20_000)

    expect(callbacks.onUnavailable).toHaveBeenCalledWith('bring-up-timeout')
  })

  it('never calls onUnavailable once a screencast frame has been received before the timeout', async () => {
    const callbacks = makeCallbacks()
    const conn = new BrowserLiveWsConnection('sess-1', 'agent-1', callbacks)
    conn.connect()
    openSocket()
    await flushMicrotasks()

    lastWsInstance.onmessage?.({
      data: JSON.stringify({ type: 'browser_screencast', session_id: 'sess-1', seq: 1, data: 'x', width: 100, height: 100 }),
    })

    await vi.advanceTimersByTimeAsync(20_000)

    expect(callbacks.onUnavailable).not.toHaveBeenCalled()
  })
})

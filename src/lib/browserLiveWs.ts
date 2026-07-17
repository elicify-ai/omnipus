// browserLiveWs — WebSocket client for /api/v1/browser/ws (ADR-038 D1/D5).
//
// A second, self-contained WS connection separate from the chat WS
// (src/lib/ws.ts). Deliberately lighter-weight than WsConnection: no Web
// Worker parse offload, no visibility/online listeners, no application-level
// ping/pong heartbeat — the panel is user-opened/closed on demand, liveness
// is left to the browser's own WS transport ping/pong plus the socket's
// close event (see ws.onclose below), and the screencast is repaint-driven
// (CDP only emits a frame when the page's compositor actually paints), so an
// idle-but-healthy page can legitimately go quiet with no frames for a long
// stretch — that is not, by itself, a liveness signal. Reuses the same
// first-message `{type:"auth",token}` handshake and the same sessionStorage
// → localStorage token lookup as ws.ts.
//
// Wire types are sourced exclusively from the generated AsyncAPI types/Zod —
// hand-written interface declarations for wire-format frames are FORBIDDEN
// (CLAUDE.md hard-constraint #8).
//
// ── live-browser-video-streaming epic (FR-005/FR-006/FR-011/FR-017/FR-018/
//    FR-020) — binary decode path ──────────────────────────────────────────
//
// This connection now ALSO speaks the binary video/audio relay introduced
// alongside the legacy JSON `browser_screencast` transport: `binaryType` is
// set to 'arraybuffer' so `onmessage` can branch on `event.data instanceof
// ArrayBuffer` — a binary message is one `browser_video_chunk`/
// `browser_audio_chunk` envelope (see decodeChunkEnvelope below), fed to a
// WebCodecs VideoDecoder/AudioDecoder configured from the preceding
// `browser_stream_init`; anything else is the existing JSON control-frame
// path (validated via the generated Zod WsFrame schema, unchanged). The JSON
// `browser_screencast` path is KEPT working unchanged: on a video-enabled
// install (the default), the gateway serves a video-capable viewer the
// binary relay as its PRIMARY transport, and `browser_screencast` is the
// FALLBACK for a viewer that isn't video-capable (no VideoDecoder, or an
// empty `video_caps` in browser_attach) — not the other way around.
// `browser_screencast` is not yet removed (FR-010/M-10/F-02: removal is a
// later wave, once the video path is reachable end-to-end). Note: per the
// operator's Option-B decision this feature ships DORMANT this increment —
// the display/audio sidecars real headful video depends on aren't wired at
// gateway boot yet, so every real install's managed launch is headless-shell
// today regardless of this transport-selection logic; that's a separate,
// server-side gate (see ADR-044) and doesn't change what this file's code
// actually does once video IS negotiated.
//
// Binary envelope = 18-byte big-endian header `seq:u32 | ts:u64 | key:u8 |
// kind:u8 (offset 13: 0=video/1=audio) | len:u32`, produced by
// `browser.EncodeChunk` (stream_relay.go) and fanned out by the
// `wsVideoViewer` adapter (pkg/gateway/browser_ws_video.go);
// `decodeChunkEnvelope` below parses that exact shape.
//
// FR-007/FR-018/FR-020 (unavailable state): the SPA-visible "unavailable"
// signal is derived here, not carried by a dedicated wire message (none
// exists in the W0 contract). Two triggers:
//   1. A bring-up timeout (STREAM_BRINGUP_TIMEOUT_MS) with NO live frame of
//      any kind (screencast, stream_init, or a chunk) ever received since
//      attach — FR-018's "no infinite spinner". Cleared permanently on the
//      first live frame; never re-armed mid-stream (an idle-but-healthy
//      screencast legitimately sends nothing for long stretches — see this
//      file's own header above — so there is deliberately no ongoing
//      mid-stream liveness watchdog, only the initial bring-up guard).
//   2. VideoDecoder.configure() rejecting the negotiated codec/resolution —
//      a genuine "this client cannot decode what the server is sending"
//      case, distinct from the client simply lacking VideoDecoder (which
//      empties video_caps in browser_attach and is expected to make the
//      server never negotiate video in the first place).
// FR-020's backend kill-switch (gateway.browser_video_enabled) IS wired now,
// but not as a dedicated decline message: disabling it server-side fails the
// viewer's video relay (wsVideoViewer.Failed), which surfaces here as an
// ordinary `browser_status{state:"error"}` frame — the ws.onmessage JSON
// branch's existing `browser_status` case, routed to `callbacks.onStatus`
// like any other status frame. It does NOT go through onUnavailable (there
// is no separate wire signal for "video was declined" vs. any other
// status-level error) — the caller's onStatus handler is responsible for
// surfacing it via its own error-banner path.

import { WsFrame as WsFrameSchema } from '@/lib/api/generated/schemas'
import type {
  BrowserAttachFrame,
  BrowserControlFrame,
  BrowserDetachFrame,
  BrowserInputFrame,
  BrowserScreencastFrame,
  BrowserStatusFrame,
  BrowserStreamInitFrame,
  BrowserTabActionFrame,
  BrowserTabsFrame,
  ErrorFrame,
} from '@/lib/api/generated/asyncapi-types'
import { maybeDevToast } from '@/lib/dev-toast'

/** Frame types this socket ever receives over the JSON (Text) path — a narrow slice of the full WsFrame union. */
type BrowserServerFrame =
  | BrowserScreencastFrame
  | BrowserStreamInitFrame
  | BrowserStatusFrame
  | BrowserTabsFrame
  | ErrorFrame

export interface BrowserLiveWsCallbacks { // not-wire-format: SPA-only callback interface passed to BrowserLiveWsConnection's constructor. Never serialized to or from the gateway.
  onScreencast: (frame: BrowserScreencastFrame) => void
  onStatus: (frame: BrowserStatusFrame) => void
  /** ADR-041 D4 — the current tab list + active index, broadcast on any tab open/close/switch/title-change. */
  onTabs: (frame: BrowserTabsFrame) => void
  /** Fires for both server-sent `error` frames and local transport errors (create/send/reconnect-exhausted). */
  onError: (message: string) => void
  onConnected?: () => void
  onDisconnected?: () => void
  /**
   * FR-005/FR-006 — sent once by the server immediately before the first
   * `browser_video_chunk` (and `browser_audio_chunk`, if `has_audio`),
   * announcing the negotiated codec/resolution/keyframe-interval/audio
   * availability for this viewer's stream. This connection configures its
   * own VideoDecoder/AudioDecoder from it internally; it is also handed to
   * the caller (once decoders are successfully configured) so it can size
   * its canvas. Not called at all if this client cannot actually decode the
   * negotiated codec (see onUnavailable).
   */
  onStreamInit?: (frame: BrowserStreamInitFrame) => void
  /**
   * One decoded video frame, ready to paint (FR-005). This connection CLOSES
   * the frame immediately after this callback returns — draw it
   * synchronously (e.g. `ctx.drawImage(frame, ...)`); never retain it past
   * the callback.
   */
  onVideoFrame?: (frame: VideoFrame) => void
  /**
   * One decoded audio chunk, ready to schedule for playback (FR-011). This
   * connection CLOSES the data immediately after this callback returns —
   * copy any samples out synchronously; never retain it past the callback.
   */
  onAudioData?: (data: AudioData) => void
  /** A binary chunk failed to decode, or a configured decoder reported an error. Dropped, never crashes (FR-007 posture). */
  onDecodeError?: (kind: 'video' | 'audio', message: string) => void
  /**
   * FR-007/FR-018/FR-020 — this viewer cannot/will not get video: no
   * VideoDecoder in this browser, the bring-up timeout expired with no live
   * frame ever received, or the negotiated codec failed to configure.
   * `reason` is an internal, operator-facing label — the caller MUST show
   * only the generic FR-007 string to the end user, never this value.
   */
  onUnavailable?: (reason: string) => void
  /** Observability/testing hook — the video/audio codec caps this connection advertised in browser_attach (FR-005). */
  onCaps?: (caps: { video: string[]; audio: string[] }) => void
}

// ── Reconnect schedule ────────────────────────────────────────────────────
// Deliberately lighter than ws.ts's two-phase (fast/slow) schedule — this is
// a short-lived, user-opened panel, not the always-on chat connection. A
// handful of exponential-backoff attempts, then give up with a clear message
// (the user can close and reopen the panel to retry from a clean state).

const MAX_RECONNECT_ATTEMPTS = 5
const RECONNECT_BASE_DELAY_MS = 1000
const RECONNECT_MAX_DELAY_MS = 8000

// FR-018 — client-side "no infinite spinner" backstop: if NEITHER a legacy
// browser_screencast frame NOR a browser_stream_init/video-chunk ever
// arrives within this window after attach, the viewer moves to the
// unavailable state. Deliberately generous — well above the SC-001a ≤3s
// cold-start placeholder — since this is a backstop for a genuinely stuck
// connection, not the authoritative bring-up timeout (that's server-side,
// W2/component M). Cleared permanently the first time ANY live frame
// arrives; never re-armed afterwards (see the file header comment on why
// there is no ongoing mid-stream liveness watchdog here).
const STREAM_BRINGUP_TIMEOUT_MS = 20_000

// FR-005 — capability-probe candidates. Video: H.264 main (avc1.4D4028) then
// VP8 (FR-006's priority order, Gate-0/EC-4-gated — see that FR's own doc
// comment on the codec-negotiation surface being built/tunable last).
// Audio: Opus only (FR-011/FR-023).
const VIDEO_CODEC_CANDIDATES = ['avc1.4D4028', 'vp8'] as const
const AUDIO_CODEC_CANDIDATES = ['opus'] as const

// Binary envelope: seq(u32) + ts(u64) + key(u8) + kind(u8) + len(u32), BigEndian throughout (DS-2).
const CHUNK_KIND_VIDEO = 0
const CHUNK_KIND_AUDIO = 1
const ENVELOPE_HEADER_BYTES = 4 + 8 + 1 + 1 + 4

let _droppedBinaryChunkCount = 0

/** Count of binary WS messages dropped as malformed (too short, bad kind byte at offset 13, length mismatch) since the last reset. Test/observability hook — mirrors ws.ts's getDroppedFrameCount pattern. */
export function getDroppedBinaryChunkCount(): number {
  return _droppedBinaryChunkCount
}

export function resetDroppedBinaryChunkCount(): void {
  _droppedBinaryChunkCount = 0
}

export interface DecodedChunkEnvelope { // not-wire-format: local parsed shape of one binary browser_video_chunk/browser_audio_chunk WS message, never re-serialized
  kind: 'video' | 'audio'
  seq: number
  ts: bigint
  key: boolean
  payload: Uint8Array
}

/**
 * Parses one binary WS message into a DecodedChunkEnvelope. Returns null
 * (never throws) on any malformed input: too short to hold a header, an
 * unrecognized kind byte at offset 13, a zero-length payload, or a declared
 * `len` that doesn't match the actual trailing byte count — mirrors the
 * backend's own FR-014 "reject, never fragment/partially-reassemble, never
 * accept an empty chunk" posture on the client side. BigEndian throughout,
 * per the contract (DS-2).
 */
export function decodeChunkEnvelope(buffer: ArrayBuffer): DecodedChunkEnvelope | null {
  if (buffer.byteLength < ENVELOPE_HEADER_BYTES) return null
  const view = new DataView(buffer)
  const seq = view.getUint32(0, false)
  const ts = view.getBigUint64(4, false)
  const key = view.getUint8(12) !== 0
  const kindByte = view.getUint8(13)
  if (kindByte !== CHUNK_KIND_VIDEO && kindByte !== CHUNK_KIND_AUDIO) return null
  const len = view.getUint32(14, false)
  if (len === 0) return null
  const payloadOffset = ENVELOPE_HEADER_BYTES
  if (len !== buffer.byteLength - payloadOffset) return null
  return {
    kind: kindByte === CHUNK_KIND_VIDEO ? 'video' : 'audio',
    seq,
    ts,
    key,
    payload: new Uint8Array(buffer, payloadOffset, len),
  }
}

/**
 * Probes VideoDecoder.isConfigSupported for each FR-006 candidate codec.
 * Returns `[]` with NO probing at all when VideoDecoder doesn't exist (old
 * Safari — FR-007's "no VideoDecoder" guard) — never throws.
 */
export async function probeVideoCaps(): Promise<string[]> {
  if (typeof VideoDecoder === 'undefined') return []
  const supported: string[] = []
  for (const codec of VIDEO_CODEC_CANDIDATES) {
    try {
      const result = await VideoDecoder.isConfigSupported({ codec })
      if (result.supported) supported.push(codec)
    } catch {
      // isConfigSupported can reject on a codec string the engine doesn't
      // recognize at all — treat as unsupported, not an error.
    }
  }
  return supported
}

/**
 * Probes AudioDecoder.isConfigSupported for Opus. Returns `[]` when
 * AudioDecoder doesn't exist — audio is simply absent, never blocks video
 * (FR-011).
 */
export async function probeAudioCaps(): Promise<string[]> {
  if (typeof AudioDecoder === 'undefined') return []
  const supported: string[] = []
  for (const codec of AUDIO_CODEC_CANDIDATES) {
    try {
      const result = await AudioDecoder.isConfigSupported({ codec, sampleRate: 48000, numberOfChannels: 2 })
      if (result.supported) supported.push(codec)
    } catch {
      // as above
    }
  }
  return supported
}

function getBrowserWsUrl(): string {
  const httpBase = (import.meta.env.VITE_API_URL as string | undefined) || window.location.origin
  const wsBase = httpBase.replace(/^http/, 'ws')
  return `${wsBase}/api/v1/browser/ws`
}

/** Validates + narrows an incoming JSON payload to the frames this socket cares about. Never throws. */
export function parseBrowserFrame(data: unknown): BrowserServerFrame | null {
  let raw: unknown
  if (typeof data === 'string') {
    try {
      raw = JSON.parse(data)
    } catch {
      return null
    }
  } else {
    raw = data
  }

  const result = WsFrameSchema.safeParse(raw)
  if (!result.success) return null

  const frame = result.data
  if (
    frame.type === 'browser_screencast' ||
    frame.type === 'browser_stream_init' ||
    frame.type === 'browser_status' ||
    frame.type === 'browser_tabs' ||
    frame.type === 'error'
  ) {
    return frame
  }
  // Any other (chat-only) frame type is not relevant to this socket — the
  // gateway is expected to never emit one here, but drop defensively rather
  // than forwarding something the panel doesn't understand.
  return null
}

export class BrowserLiveWsConnection {
  private ws: WebSocket | null = null
  private readonly sessionId: string
  private readonly agentId: string
  private readonly callbacks: BrowserLiveWsCallbacks
  private intentionalClose = false
  private reconnectAttempts = 0
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null
  private bringupTimer: ReturnType<typeof setTimeout> | null = null
  private everReceivedLiveFrame = false
  private videoDecoder: VideoDecoder | null = null
  private audioDecoder: AudioDecoder | null = null
  /** CR2 — dropped-until-first-keyframe gate for the CURRENT stream. Reset on every browser_stream_init (see _configureDecoders); set once the first keyframe chunk has been handed to the video decoder. */
  private seenKeyframe = false

  constructor(sessionId: string, agentId: string, callbacks: BrowserLiveWsCallbacks) {
    this.sessionId = sessionId
    this.agentId = agentId
    this.callbacks = callbacks
  }

  connect(): void {
    this.intentionalClose = false
    this.reconnectAttempts = 0
    this._createSocket()
  }

  private _createSocket(): void {
    let ws: WebSocket
    try {
      ws = new WebSocket(getBrowserWsUrl())
    } catch (err) {
      this.callbacks.onError(
        `Failed to create browser WebSocket: ${err instanceof Error ? err.message : String(err)}`,
      )
      return
    }
    // FR-017 — binary chunks arrive as ArrayBuffer (not Blob) so onmessage
    // can synchronously DataView-parse the envelope with no extra async hop.
    ws.binaryType = 'arraybuffer'
    this.ws = ws

    ws.onopen = () => {
      this.reconnectAttempts = 0
      const token = sessionStorage.getItem('omnipus_auth_token') ?? localStorage.getItem('omnipus_auth_token')
      if (!token) {
        this.callbacks.onError('No auth token found — cannot open the live browser view.')
        ws.close(1000, 'no auth token')
        return
      }
      this._rawSend({ type: 'auth', token })

      const attachBase = { session_id: this.sessionId, agent_id: this.agentId }
      // FR-005 — keep the no-decoder path (today's reality in every existing
      // test environment, and on old Safari) FULLY SYNCHRONOUS: sending the
      // handshake must not gain a microtask delay when there is nothing to
      // probe. Only await isConfigSupported when at least one WebCodecs
      // decoder actually exists in this browser.
      if (typeof VideoDecoder === 'undefined' && typeof AudioDecoder === 'undefined') {
        this._sendAttach(attachBase, [], [])
      } else {
        void this._probeCapsAndSendAttach(attachBase)
      }
    }

    ws.onmessage = (event: MessageEvent) => {
      if (event.data instanceof ArrayBuffer) {
        this._handleBinaryMessage(event.data)
        return
      }
      const frame = parseBrowserFrame(event.data as string)
      if (!frame) return
      if (frame.type === 'browser_screencast') {
        this._markLiveFrameReceived()
        this.callbacks.onScreencast(frame)
      } else if (frame.type === 'browser_stream_init') {
        this._markLiveFrameReceived()
        // Only surface the stream to the caller if this client can actually
        // decode it — otherwise we'd hand the caller stream dimensions for a
        // picture that will never paint a single frame (see the file header
        // comment on why onUnavailable and onStreamInit are mutually
        // exclusive for a given stream_init).
        if (this._configureDecoders(frame)) {
          this.callbacks.onStreamInit?.(frame)
        }
      } else if (frame.type === 'browser_status') {
        this.callbacks.onStatus(frame)
      } else if (frame.type === 'browser_tabs') {
        this.callbacks.onTabs(frame)
      } else {
        this.callbacks.onError(frame.message)
      }
    }

    ws.onerror = () => {
      this.callbacks.onError('Live browser connection error — will retry.')
    }

    ws.onclose = (event: CloseEvent) => {
      this.ws = null
      this._clearBringupTimer()
      this._closeDecoders()
      this.callbacks.onDisconnected?.()
      if (this.intentionalClose) return

      // 1008 = policy violation / auth rejected by the gateway. Retrying
      // with the same token would loop forever — surface once and stop.
      if (event.code === 1008) {
        this.callbacks.onError('Authentication failed for the live browser view. Reload and try again.')
        return
      }

      if (this.reconnectAttempts >= MAX_RECONNECT_ATTEMPTS) {
        this.callbacks.onError(
          'Lost connection to the live browser view. Close and reopen the panel to retry.',
        )
        return
      }

      const delay = Math.min(
        RECONNECT_BASE_DELAY_MS * 2 ** this.reconnectAttempts,
        RECONNECT_MAX_DELAY_MS,
      )
      this.reconnectAttempts++
      this.reconnectTimer = setTimeout(() => this._createSocket(), delay)
    }
  }

  private async _probeCapsAndSendAttach(attachBase: { session_id: string; agent_id: string }): Promise<void> {
    const [videoCaps, audioCaps] = await Promise.all([probeVideoCaps(), probeAudioCaps()])
    this._sendAttach(attachBase, videoCaps, audioCaps)
  }

  private _sendAttach(
    attachBase: { session_id: string; agent_id: string },
    videoCaps: string[],
    audioCaps: string[],
  ): void {
    const attach: BrowserAttachFrame = {
      type: 'browser_attach',
      ...attachBase,
      ...(videoCaps.length > 0 ? { video_caps: videoCaps } : {}),
      ...(audioCaps.length > 0 ? { audio_caps: audioCaps } : {}),
    }
    this._rawSend(attach)
    this.callbacks.onCaps?.({ video: videoCaps, audio: audioCaps })
    this._startBringupTimer()
    this.callbacks.onConnected?.()
  }

  private _startBringupTimer(): void {
    this._clearBringupTimer()
    this.everReceivedLiveFrame = false
    this.bringupTimer = setTimeout(() => {
      this.bringupTimer = null
      if (!this.everReceivedLiveFrame) {
        this.callbacks.onUnavailable?.(
          typeof VideoDecoder === 'undefined' ? 'no-video-decoder' : 'bring-up-timeout',
        )
      }
    }, STREAM_BRINGUP_TIMEOUT_MS)
  }

  private _clearBringupTimer(): void {
    if (this.bringupTimer !== null) {
      clearTimeout(this.bringupTimer)
      this.bringupTimer = null
    }
  }

  private _markLiveFrameReceived(): void {
    this.everReceivedLiveFrame = true
    this._clearBringupTimer()
  }

  /**
   * Configures this connection's VideoDecoder (and, best-effort, its
   * AudioDecoder) from a browser_stream_init. Returns true only if the video
   * decoder configured successfully — a false return means this client
   * cannot render this stream at all, and onUnavailable has already been
   * called; the caller must not treat the stream as active. Audio
   * configuration failure is always best-effort (FR-011/FR-023: never
   * blocks video) and never affects the return value.
   */
  private _configureDecoders(init: BrowserStreamInitFrame): boolean {
    this._closeDecoders()
    // CR2 — a fresh stream_init means a fresh GOP: any decoder previously
    // configured is gone (_closeDecoders just ran), so any delta chunk must
    // wait for this stream's own first keyframe before it can be decoded.
    this.seenKeyframe = false

    if (typeof VideoDecoder === 'undefined') {
      // Defensive only: a spec-compliant server shouldn't negotiate video
      // for a viewer that advertised empty video_caps, but guard anyway.
      this.callbacks.onUnavailable?.('no-video-decoder')
      return false
    }

    this.videoDecoder = new VideoDecoder({
      output: (frame) => {
        try {
          this.callbacks.onVideoFrame?.(frame)
        } finally {
          frame.close()
        }
      },
      // SF-H1 — WebCodecs only invokes this callback once the decoder has
      // already transitioned to `state='closed'` (a fatal decode error, not
      // a per-chunk warning): every decode() call after this point would
      // just throw again. Route through `_handleVideoDecodeError` rather
      // than only logging, or the canvas freezes on its last painted frame
      // forever with zero production signal (see that method's doc comment).
      error: (err) => {
        this._handleVideoDecodeError(err.message)
      },
    })
    try {
      this.videoDecoder.configure({ codec: init.codec, codedWidth: init.width, codedHeight: init.height })
    } catch (err) {
      this.callbacks.onDecodeError?.('video', err instanceof Error ? err.message : String(err))
      this.callbacks.onUnavailable?.('video-decoder-configure-failed')
      this.videoDecoder = null
      return false
    }

    if (init.has_audio && typeof AudioDecoder !== 'undefined') {
      this.audioDecoder = new AudioDecoder({
        output: (data) => {
          try {
            this.callbacks.onAudioData?.(data)
          } finally {
            data.close()
          }
        },
        error: (err) => {
          this.callbacks.onDecodeError?.('audio', err.message)
        },
      })
      try {
        // The negotiated audio codec/rate/channels aren't carried on
        // BrowserStreamInitFrame — FR-011's audio path is Opus-only, matching
        // probeAudioCaps' own probe config (48kHz stereo).
        this.audioDecoder.configure({ codec: 'opus', sampleRate: 48000, numberOfChannels: 2 })
      } catch (err) {
        // Audio failure never blocks/degrades video (FR-011/FR-023) — drop
        // audio only, video proceeds.
        this.callbacks.onDecodeError?.('audio', err instanceof Error ? err.message : String(err))
        this.audioDecoder = null
      }
    }

    return true
  }

  /**
   * SF-H1 — the single handler for a fatal video decode failure, reached
   * from both the VideoDecoder's own async `error` callback (the decoder has
   * already closed itself) and a synchronous throw out of `decode()` in
   * `_handleBinaryMessage` (e.g. a chunk fed in just as/after the decoder
   * closed). Either way, this decoder can no longer be trusted: continuing
   * to feed it would either keep throwing forever (async case: every later
   * decode() call throws once `state==='closed'`) or risk decoding
   * out-of-GOP data. Rather than silently freezing the canvas on its last
   * painted frame with zero production signal, this:
   *   1. reports the failure via onDecodeError (existing dev-toast plumbing
   *      in the caller — unchanged),
   *   2. re-gates: even if a decoder is configured again later, no delta
   *      chunk is fed until a fresh keyframe is seen (mirrors the CR2 gate
   *      `_configureDecoders` already applies on every stream_init),
   *   3. tears the decoder down (`_closeDecoders` nulls `this.videoDecoder`,
   *      so `_handleBinaryMessage` drops every subsequent video chunk before
   *      it can reach `decode()` again — this call becomes a safe no-op if
   *      it somehow fires twice for the same failure), and
   *   4. transitions to the unavailable state via the existing onUnavailable
   *      path — the same generic FR-007 "this client cannot render this
   *      stream" banner every other unavailable trigger already uses, and
   *      the simplest, most user-visible way out of a frozen picture (chosen
   *      over a bare socket-teardown/reconnect: it reuses plumbing the SPA
   *      already renders correctly, with no risk of a reconnect loop against
   *      a server that keeps re-negotiating the same broken codec).
   */
  private _handleVideoDecodeError(message: string): void {
    this.callbacks.onDecodeError?.('video', message)
    this.seenKeyframe = false
    this._closeDecoders()
    this.callbacks.onUnavailable?.('video-decode-error')
  }

  private _handleBinaryMessage(buffer: ArrayBuffer): void {
    const decoded = decodeChunkEnvelope(buffer)
    if (!decoded) {
      _droppedBinaryChunkCount++
      void maybeDevToast('[browser-live] Dropped malformed binary chunk', 'browser-live-chunk-malformed', 'warning')
      return
    }

    // FR-018 (SF3 fix) — only clear the bring-up backstop when this chunk is
    // actually decodable, i.e. this.videoDecoder is configured (a
    // browser_stream_init already succeeded). If stream_init was NEVER
    // processed — decoder still null — any binary chunk that arrives is
    // dropped below regardless; clearing the backstop here too would
    // permanently disarm FR-018's "no infinite spinner" guard while nothing
    // is actually decoding, leaving a silently blank panel with no
    // unavailable state.
    if (this.videoDecoder) {
      this._markLiveFrameReceived()
    }

    // A4 — WebCodecs' EncodedVideoChunk/EncodedAudioChunk `timestamp` is in
    // MICROSECONDS; the wire envelope's `ts` field (DS-2) is milliseconds.
    // Scaled uniformly for both video and audio so the relative timeline
    // stays correct once audio playback lands and needs real A/V sync —
    // video-only decode order is unaffected by the scale factor either way.
    const timestampUs = Number(decoded.ts) * 1000

    if (decoded.kind === 'video') {
      if (!this.videoDecoder) {
        _droppedBinaryChunkCount++
        return
      }
      // CR2 — client-side hardening: never feed a delta chunk to the
      // decoder before this stream's first keyframe has been decoded.
      // Guards both the warm-attach ordering race (this viewer's chunks can
      // start arriving mid-GOP) and mid-stream keyframe loss. seenKeyframe
      // is reset per stream in _configureDecoders.
      if (!decoded.key && !this.seenKeyframe) {
        _droppedBinaryChunkCount++
        return
      }
      try {
        this.videoDecoder.decode(
          new EncodedVideoChunk({
            type: decoded.key ? 'key' : 'delta',
            timestamp: timestampUs,
            data: decoded.payload,
          }),
        )
        if (decoded.key) this.seenKeyframe = true
      } catch (err) {
        // SF-H1 — a synchronous throw out of decode() (e.g. this chunk
        // arrived just as/after the decoder closed) is just as fatal as the
        // decoder's own async `error` callback — see
        // _handleVideoDecodeError's doc comment.
        void maybeDevToast('[browser-live] Video decode error', 'browser-live-video-decode', 'error')
        this._handleVideoDecodeError(err instanceof Error ? err.message : String(err))
      }
      return
    }

    // Audio: silently dropped if not negotiated/configured (FR-011 — never
    // an error, video is unaffected).
    if (!this.audioDecoder) return
    try {
      this.audioDecoder.decode(
        new EncodedAudioChunk({
          type: decoded.key ? 'key' : 'delta',
          timestamp: timestampUs,
          data: decoded.payload,
        }),
      )
    } catch (err) {
      this.callbacks.onDecodeError?.('audio', err instanceof Error ? err.message : String(err))
      void maybeDevToast('[browser-live] Audio decode error', 'browser-live-audio-decode', 'error')
    }
  }

  private _closeDecoders(): void {
    if (this.videoDecoder) {
      try {
        if (this.videoDecoder.state !== 'closed') this.videoDecoder.close()
      } catch {
        // Already closed/errored — nothing further to release.
      }
      this.videoDecoder = null
    }
    if (this.audioDecoder) {
      try {
        if (this.audioDecoder.state !== 'closed') this.audioDecoder.close()
      } catch {
        // as above
      }
      this.audioDecoder = null
    }
  }

  private _rawSend(frame: unknown): boolean {
    if (this.ws?.readyState === WebSocket.OPEN) {
      try {
        this.ws.send(JSON.stringify(frame))
        return true
      } catch (err) {
        console.error(
          `[browserLiveWs] send failed on OPEN socket: ${err instanceof Error ? err.message : String(err)}`,
        )
        return false
      }
    }
    return false
  }

  /** Sends a browser_input frame. `type` is added internally — pass just the input payload. */
  sendInput(input: Omit<BrowserInputFrame, 'type'>): boolean {
    const frame: BrowserInputFrame = { type: 'browser_input', ...input }
    return this._rawSend(frame)
  }

  sendControl(action: 'take' | 'release'): boolean {
    const frame: BrowserControlFrame = { type: 'browser_control', action }
    return this._rawSend(frame)
  }

  /**
   * ADR-041 D4 — switch/close/open a tab. `index` is required for
   * 'switch'/'close' (identifies which tab) and omitted for 'open' (the
   * backend appends a fresh tab and reports it back on the next
   * `browser_tabs` frame). Carries session_id/agent_id explicitly, same as
   * `browser_attach` (unlike `detach()`, whose `BrowserDetachFrame` carries
   * only `session_id` — no `agent_id` field exists on that frame), so the
   * backend can route the action even if this connection is ever
   * multiplexed across sessions.
   */
  sendTabAction(action: 'switch' | 'close' | 'open', index?: number): boolean {
    const frame: BrowserTabActionFrame = {
      type: 'browser_tab_action',
      session_id: this.sessionId,
      agent_id: this.agentId,
      action,
      ...(index !== undefined ? { index } : {}),
    }
    return this._rawSend(frame)
  }

  /** Tells the backend this viewer is done watching (ref-counted engine stops screencast when the last viewer detaches — ADR-038 D3). Does not close the socket. */
  detach(): void {
    const frame: BrowserDetachFrame = { type: 'browser_detach', session_id: this.sessionId }
    this._rawSend(frame)
  }

  /** Closes the socket and cancels any pending reconnect/bring-up timer. Call detach() first if the viewer is going away cleanly. */
  close(): void {
    this.intentionalClose = true
    this._clearBringupTimer()
    this._closeDecoders()
    if (this.reconnectTimer !== null) {
      clearTimeout(this.reconnectTimer)
      this.reconnectTimer = null
    }
    this.ws?.close(1000, 'panel closed')
    this.ws = null
  }

  get isConnected(): boolean {
    return this.ws?.readyState === WebSocket.OPEN
  }
}

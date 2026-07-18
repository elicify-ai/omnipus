// browserWebRTC — the viewer-side WebRTC peer-connection state machine for the
// live browser panel (ADR-047 "live-browser WebRTC", wave-plan.md W2-B).
//
// Owns exactly ONE `RTCPeerConnection` (the "egress"/viewer leg of the
// gateway's Pion SFU relay — ADR-047 D1) across its lifecycle: idle →
// offering → connected → fallback. Deliberately knows NOTHING about the
// signaling transport (no WebSocket import here) — the caller supplies a
// `sendOffer(sdp)` callback to `start()` and feeds inbound
// `browser_webrtc_answer`/`browser_webrtc_state` frames in via
// `applyAnswer`/`applyState`. This keeps the class unit-testable with an
// injected fake RTCPeerConnection factory (jsdom has no real WebRTC
// implementation — see BrowserLiveView.webrtcSink.test.tsx's own note on
// this) and keeps `browserLiveWs.ts` free of any PC/ICE concerns.
//
// Non-trickle offer/answer only (ADR-047 D4 — spike-proven simplest): the
// offer is not sent until `RTCPeerConnection.iceGatheringState` reaches
// 'complete', so there is no `onicecandidate`/trickle handling here at all.
//
// Fallback contract (wave-plan W2-B): JPEG screencast NEVER stops running
// underneath (that's owned entirely by browserLiveWs.ts/BrowserLiveView,
// outside this class) — this class's only job on fallback is to clean up its
// own PC/data-channel state and tell the caller via `onFallback(reason)` so
// the caller can drop back to rendering the JPEG `<img>` sink. Triggers:
//   - a `browser_webrtc_state{available:false}` frame while offering/connected
//   - no answer within `answerTimeoutMs` (default 5s) of sending the offer
//   - ICE connection state 'failed'
//   - ICE connection state 'disconnected' for longer than `disconnectedGraceMs`
//     (default 5s) without recovering to 'connected'/'completed'
// Retry: exactly ONE automatic re-offer `retryDelayMs` (default 15s) after a
// fallback. If that retry also falls back, no further automatic retry is
// scheduled — the caller must call `start()` again (a fresh re-attach) to
// re-arm it. `start()` always resets the one-shot retry budget.

import type { BrowserWebRTCStateFrame } from '@/lib/api/generated/asyncapi-types'

/** The four states this machine is ever in. 'failed' is folded into
 * 'fallback' as a *reason string* passed to `onFallback` rather than a
 * separate public state — from the caller's point of view "ICE failed" and
 * "gateway reported unavailable" both mean exactly one thing: stop trying to
 * render WebRTC video and let the JPEG sink carry on. */
export type BrowserWebRTCState = 'idle' | 'offering' | 'connected' | 'fallback'

export interface BrowserWebRTCSessionOptions {
  /**
   * Factory for the underlying `RTCPeerConnection` — injected so tests can
   * supply a fake without a real WebRTC implementation. Defaults to a real
   * `RTCPeerConnection` configured with a hardcoded public STUN server
   * (wave-plan W2-B: "hardcode the Google STUN default; the gateway relay
   * uses its own [config]" — this is the VIEWER leg only).
   */
  pcFactory?: () => RTCPeerConnection
  /** ms to wait for `applyAnswer` after the offer is actually sent (i.e.
   * after ICE gathering completes) before falling back. Default 5000. */
  answerTimeoutMs?: number
  /** ms an ICE `disconnected` state is tolerated before falling back.
   * Default 5000. */
  disconnectedGraceMs?: number
  /** ms to wait after a fallback before the single automatic re-offer.
   * Default 15000. */
  retryDelayMs?: number
  /** ms to wait for `RTCPeerConnection.iceGatheringState` to reach
   * 'complete' before giving up and sending the offer with whatever
   * candidates have gathered so far (non-trickle still works with a partial
   * candidate set — better than wedging in 'offering' forever on a stuck
   * gathering). Default 3000. */
  iceGatheringTimeoutMs?: number
}

const DEFAULT_STUN_SERVER = 'stun:stun.l.google.com:19302'
const DEFAULT_ANSWER_TIMEOUT_MS = 5000
const DEFAULT_DISCONNECTED_GRACE_MS = 5000
const DEFAULT_RETRY_DELAY_MS = 15000
const DEFAULT_ICE_GATHERING_TIMEOUT_MS = 3000

function defaultPcFactory(): RTCPeerConnection {
  return new RTCPeerConnection({ iceServers: [{ urls: DEFAULT_STUN_SERVER }] })
}

export class BrowserWebRTCSession {
  private readonly pcFactory: () => RTCPeerConnection
  private readonly answerTimeoutMs: number
  private readonly disconnectedGraceMs: number
  private readonly retryDelayMs: number
  private readonly iceGatheringTimeoutMs: number

  private pc: RTCPeerConnection | null = null
  private inputChannel: RTCDataChannel | null = null
  private remoteStream: MediaStream | null = null
  /** An explicit `false` return means the caller's transport failed to
   * actually send the offer (e.g. a closed WebSocket mid-ICE-gathering) —
   * see `_beginOffer`, which triggers an immediate 'offer-send-failed'
   * fallback on `false` rather than waiting out the full answer timeout. A
   * `void`/`undefined` return (a caller that doesn't report send success) is
   * treated as "no signal, assume it went out" for backward compatibility —
   * only a literal `false` is a failure signal. */
  private sendOfferFn: ((sdp: string) => boolean | void) | null = null

  private _state: BrowserWebRTCState = 'idle'
  private stopped = true
  private retriedOnce = false

  private answerTimeoutTimer: ReturnType<typeof setTimeout> | null = null
  private disconnectedTimer: ReturnType<typeof setTimeout> | null = null
  private retryTimer: ReturnType<typeof setTimeout> | null = null

  private streamCb: ((stream: MediaStream) => void) | null = null
  private inputOpenCb: (() => void) | null = null
  private inputCloseCb: (() => void) | null = null
  private fallbackCb: ((reason: string) => void) | null = null

  constructor(options: BrowserWebRTCSessionOptions = {}) {
    this.pcFactory = options.pcFactory ?? defaultPcFactory
    this.answerTimeoutMs = options.answerTimeoutMs ?? DEFAULT_ANSWER_TIMEOUT_MS
    this.disconnectedGraceMs = options.disconnectedGraceMs ?? DEFAULT_DISCONNECTED_GRACE_MS
    this.retryDelayMs = options.retryDelayMs ?? DEFAULT_RETRY_DELAY_MS
    this.iceGatheringTimeoutMs = options.iceGatheringTimeoutMs ?? DEFAULT_ICE_GATHERING_TIMEOUT_MS
  }

  get state(): BrowserWebRTCState {
    return this._state
  }

  // ── Observer registration — single-slot (one owner per session, matching
  // how BrowserLiveView wires exactly one machine per WS-connection effect
  // instance; re-registering overwrites, it does not fan out to many). ──

  onStream(cb: (stream: MediaStream) => void): void {
    this.streamCb = cb
  }

  onInputChannelOpen(cb: () => void): void {
    this.inputOpenCb = cb
  }

  onInputChannelClose(cb: () => void): void {
    this.inputCloseCb = cb
  }

  onFallback(cb: (reason: string) => void): void {
    this.fallbackCb = cb
  }

  /**
   * Begins (or re-begins) the non-trickle offer/answer exchange. No-ops if
   * already offering or connected — the caller (BrowserLiveView) may see
   * repeated `browser_webrtc_state{available:true}` frames while a session
   * is already up; this keeps that idempotent. Resets the one-shot retry
   * budget — call this again after an intentional `stop()` (a fresh
   * re-attach) to re-arm automatic fallback retry.
   *
   * `sendOffer` may optionally return a boolean — an explicit `false` means
   * the caller's transport failed to actually send the offer (e.g. a closed
   * WebSocket), which triggers an immediate 'offer-send-failed' fallback
   * instead of the full answer timeout (see `_beginOffer`). A `void` return
   * is treated as "assume it sent," so existing callers that don't report
   * send success are unaffected.
   */
  start(sendOffer: (sdp: string) => boolean | void): void {
    if (this._state === 'offering' || this._state === 'connected') return
    // Bugfix (HIGH, fix-wave B): a pending automatic-retry timer armed by an
    // earlier fallback must be cleared here too, not just in stop(). Without
    // this, the sequence available:false → (retry armed) → available:true →
    // caller re-invokes start() to reconnect → the STALE retry timer still
    // fires later and calls _beginOffer on what is now a healthy session,
    // creating a second RTCPeerConnection alongside the one this start()
    // just began and leaking the first (its close() is never called).
    if (this.retryTimer !== null) {
      clearTimeout(this.retryTimer)
      this.retryTimer = null
    }
    this.stopped = false
    this.retriedOnce = false
    this.sendOfferFn = sendOffer
    void this._beginOffer()
  }

  /** Feed in the gateway's `browser_webrtc_answer.sdp` once it arrives. */
  applyAnswer(sdp: string): void {
    if (!this.pc || this._state !== 'offering') return
    this._clearAnswerTimeout()
    const pc = this.pc
    pc.setRemoteDescription({ type: 'answer', sdp }).catch((err: unknown) => {
      if (this.pc !== pc) return // superseded by a stop()/retry in the meantime
      this._fallback(`set-remote-description-failed: ${err instanceof Error ? err.message : String(err)}`)
    })
  }

  /**
   * Feed in every `browser_webrtc_state` frame the gateway sends. Only acts
   * when it signals unavailability WHILE a session is actually in flight —
   * deciding whether/when to `start()` on an *available* signal is the
   * caller's job (wave-plan W2-B wiring note), so an available:true frame is
   * intentionally a no-op here UNLESS it also reports `active:false` after
   * this session had already connected (fix-wave B, MED): the gateway pushes
   * `available:false` on an ordinary stop, but the relay can also drop out
   * from under an established connection while the capability itself stays
   * available (`active` flips false without `available` following it) — the
   * contract calls that a fallback trigger too, same as any other
   * mid-session failure.
   */
  applyState(frame: Pick<BrowserWebRTCStateFrame, 'available' | 'reason' | 'active'>): void {
    if (!frame.available) {
      if (this._state === 'offering' || this._state === 'connected') {
        this._fallback(frame.reason ?? 'unavailable')
      }
      return
    }
    if (frame.active === false && this._state === 'connected') {
      this._fallback(frame.reason ?? 'stream-stopped')
    }
  }

  /**
   * Sends a JSON-serialized payload over the "input" data channel. Returns
   * false (never throws) when the channel isn't open — the caller falls
   * back to the WS `browser_input` path in that case (wave-plan W2-B input
   * routing rule).
   */
  sendInput(json: string): boolean {
    if (!this.inputChannel || this.inputChannel.readyState !== 'open') return false
    try {
      this.inputChannel.send(json)
      return true
    } catch {
      return false
    }
  }

  /** Full, intentional teardown — cancels the automatic retry too. Safe to
   * call repeatedly (idle → idle is a no-op besides re-clearing timers). */
  stop(): void {
    this.stopped = true
    if (this.retryTimer !== null) {
      clearTimeout(this.retryTimer)
      this.retryTimer = null
    }
    this._cleanupPeer()
    this._setState('idle')
  }

  // ── Internals ─────────────────────────────────────────────────────────

  private _setState(next: BrowserWebRTCState): void {
    this._state = next
  }

  private async _beginOffer(): Promise<void> {
    // Defensive guard (HIGH, fix-wave B): makes "at most one PC" structural
    // rather than relying solely on every caller correctly clearing timers
    // before invoking this. Any leftover `this.pc` here (e.g. from a bug in
    // a future caller, or a path this fix wave didn't anticipate) is torn
    // down first so a fresh offer never starts alongside a live one.
    if (this.pc !== null) this._cleanupPeer()
    this._setState('offering')
    let pc: RTCPeerConnection
    try {
      pc = this.pcFactory()
    } catch (err) {
      this._fallback(`pc-create-failed: ${err instanceof Error ? err.message : String(err)}`)
      return
    }
    this.pc = pc
    this._wirePeerConnectionEvents(pc)

    try {
      // Non-trickle recvonly video+audio (ADR-047 D1/D4) + the "input" data
      // channel the gateway's InputDC wiring expects (pkg/tools/browser/webrtc/inputdc.go).
      pc.addTransceiver('video', { direction: 'recvonly' })
      pc.addTransceiver('audio', { direction: 'recvonly' })
      this._wireInputChannel(pc.createDataChannel('input'))

      const offer = await pc.createOffer()
      if (this.pc !== pc) return // stopped/superseded mid-flight
      await pc.setLocalDescription(offer)
      if (this.pc !== pc) return

      await this._waitForIceGatheringComplete(pc)
      if (this.pc !== pc) return

      const sdp = pc.localDescription?.sdp
      if (!sdp) {
        this._fallback('no-local-description')
        return
      }
      // fix-wave B (MED): an explicit `false` return means the caller's
      // transport (e.g. a closed WebSocket mid-ICE-gathering) never actually
      // got the offer out — fall back immediately with a specific reason
      // rather than silently burning the full answerTimeoutMs waiting for an
      // answer that was never going to arrive. A `void`/`undefined` return
      // (existing callers/tests that don't report send success) is NOT
      // treated as failure — only a literal `false` is.
      const sendResult = this.sendOfferFn?.(sdp)
      if (sendResult === false) {
        this._fallback('offer-send-failed')
        return
      }
      this._startAnswerTimeout()
    } catch (err) {
      if (this.pc !== pc) return
      this._fallback(`offer-setup-failed: ${err instanceof Error ? err.message : String(err)}`)
    }
  }

  private _waitForIceGatheringComplete(pc: RTCPeerConnection): Promise<void> {
    if (pc.iceGatheringState === 'complete') return Promise.resolve()
    return new Promise((resolve) => {
      // fix-wave B (LOW): bound the wait — a gathering process that never
      // reaches 'complete' (a stuck STUN round trip, a flaky network) used to
      // wedge this Promise forever, leaving the machine stuck in 'offering'
      // with no offer ever sent and no fallback ever triggered. Non-trickle
      // still works fine with whatever candidates gathered in the timeout
      // window (worst case: fewer candidate types, not zero), so proceeding
      // with a partial set beats never proceeding at all. The caller
      // (`_beginOffer`) re-checks `this.pc !== pc` right after this resolves,
      // so a timeout firing after the session was superseded/stopped is
      // harmless.
      const timer = setTimeout(() => {
        pc.onicegatheringstatechange = null
        resolve()
      }, this.iceGatheringTimeoutMs)
      pc.onicegatheringstatechange = () => {
        if (pc.iceGatheringState === 'complete') {
          clearTimeout(timer)
          pc.onicegatheringstatechange = null
          resolve()
        }
      }
    })
  }

  private _wirePeerConnectionEvents(pc: RTCPeerConnection): void {
    pc.ontrack = (event: RTCTrackEvent) => {
      const incoming = event.streams[0]
      if (incoming) {
        this.remoteStream = incoming
      } else if (this.remoteStream) {
        try {
          this.remoteStream.addTrack(event.track)
        } catch {
          // Duplicate/unsupported add — nothing useful to do client-side.
        }
      } else if (typeof MediaStream !== 'undefined') {
        this.remoteStream = new MediaStream([event.track])
      } else {
        return
      }
      this.streamCb?.(this.remoteStream)
    }

    pc.oniceconnectionstatechange = () => {
      const iceState = pc.iceConnectionState
      if (iceState === 'connected' || iceState === 'completed') {
        this._clearAnswerTimeout()
        this._clearDisconnectedTimer()
        this._setState('connected')
      } else if (iceState === 'failed') {
        this._fallback('ice-failed')
      } else if (iceState === 'disconnected') {
        this._startDisconnectedTimer()
      }
    }
  }

  private _wireInputChannel(dc: RTCDataChannel): void {
    this.inputChannel = dc
    dc.onopen = () => this.inputOpenCb?.()
    dc.onclose = () => this.inputCloseCb?.()
    // No onerror handling beyond the readyState-gated `sendInput` return —
    // a channel error surfaces to the caller as "DC not open, use WS" the
    // next time it tries to send, with no separate error plumbing needed.
  }

  private _startAnswerTimeout(): void {
    this._clearAnswerTimeout()
    this.answerTimeoutTimer = setTimeout(() => {
      this.answerTimeoutTimer = null
      if (this._state === 'offering') this._fallback('answer-timeout')
    }, this.answerTimeoutMs)
  }

  private _clearAnswerTimeout(): void {
    if (this.answerTimeoutTimer !== null) {
      clearTimeout(this.answerTimeoutTimer)
      this.answerTimeoutTimer = null
    }
  }

  private _startDisconnectedTimer(): void {
    if (this.disconnectedTimer !== null) return // already counting down
    this.disconnectedTimer = setTimeout(() => {
      this.disconnectedTimer = null
      this._fallback('ice-disconnected-timeout')
    }, this.disconnectedGraceMs)
  }

  private _clearDisconnectedTimer(): void {
    if (this.disconnectedTimer !== null) {
      clearTimeout(this.disconnectedTimer)
      this.disconnectedTimer = null
    }
  }

  private _fallback(reason: string): void {
    if (this._state === 'fallback') return
    this._cleanupPeer()
    this._setState('fallback')
    this.fallbackCb?.(reason)
    if (this.stopped) return
    if (this.retriedOnce) return
    this.retriedOnce = true
    this.retryTimer = setTimeout(() => {
      this.retryTimer = null
      if (!this.stopped) void this._beginOffer()
    }, this.retryDelayMs)
  }

  private _cleanupPeer(): void {
    this._clearAnswerTimeout()
    this._clearDisconnectedTimer()
    if (this.inputChannel) {
      try {
        this.inputChannel.close()
      } catch {
        // best-effort
      }
      this.inputChannel = null
    }
    if (this.pc) {
      try {
        this.pc.close()
      } catch {
        // best-effort
      }
      this.pc = null
    }
    this.remoteStream = null
  }
}

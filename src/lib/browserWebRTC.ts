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
// Fallback contract (operator directive — JPEG-fallback removal): there is
// NO second sink to drop back to any more. This class's only job on
// fallback is to clean up its own PC/data-channel state and tell the caller
// via `onFallback(reason)`; BrowserLiveView.tsx (the sole caller) turns that
// into a persistent, honest, user-visible failure state — never a silent
// degrade — since a fallback here now means live video simply stopped
// working, full stop. (What the backend capture pipeline does independently
// of this — pause, stop, keep running — is out of this class's and this
// file's scope; it observes only the WebRTC signaling surface.) Triggers:
//   - a `browser_webrtc_state{available:false}` frame while offering/connected
//   - no answer within the current answer timeout of sending the offer (see
//     `hasConnectedOnce`/`firstAnswerTimeoutMs` below — it is NOT always
//     `answerTimeoutMs`)
//   - ICE connection state 'failed'
//   - ICE connection state 'disconnected' for longer than `disconnectedGraceMs`
//     (default 15s) without recovering to 'connected'/'completed'
// Retry (revised 2026-07-30 UAT — see DEFAULT_MAX_RETRIES): up to
// `maxRetries` (default 5) CONSECUTIVE automatic re-offers, with exponential
// backoff starting at `retryDelayMs` (default 15s) and doubling each attempt.
// The counter RESETS on every successful connect, so only a sustained
// inability to connect exhausts it; once exhausted the caller must call
// `start()` again (a fresh re-attach) to re-arm. `start()` always resets the
// budget. This replaced a one-shot boolean that stranded the panel in a
// failed state permanently after a single failed re-offer.
//
// Answer-timeout duration (fix-wave, MED — reviewer finding 4): the gateway's
// legitimate COLD START (capture start ~20s + bringToFront ~5s + tracks wait)
// can run past 25s worst case, well past the regular `answerTimeoutMs`
// (default 5s) — a plain 5s timeout on a cold start is a FALSE fallback that
// then connects fine on its own retry, needlessly surfacing the panel's
// error state and a spurious warning. `hasConnectedOnce` (a public,
// read-only, lifetime flag — never reset once true, not per-attempt) is what
// decides which timeout governs any given negotiation: false (this instance
// has never once reached 'connected') uses the generous `firstAnswerTimeoutMs`
// (default 30s); true (it connected before, so THIS failure is a genuine
// degradation of a previously-working stream, not a slow cold start) uses the
// regular short `answerTimeoutMs`. This applies to every attempt made while
// still cold — including the one automatic retry above, if that retry also
// starts from `hasConnectedOnce === false`.

/** The four states this machine is ever in. 'failed' is folded into
 * 'fallback' as a *reason string* passed to `onFallback` rather than a
 * separate public state — from the caller's point of view "ICE failed" and
 * "gateway reported unavailable" both mean exactly one thing: stop trying to
 * render WebRTC video and tell the caller why (there is no second sink to
 * carry on with any more — see `onFallback`'s doc comment above). */
export type BrowserWebRTCState = 'idle' | 'offering' | 'connected' | 'fallback'

export interface BrowserWebRTCSessionOptions { // not-wire-format: local constructor options for the client-side PC state machine (timer durations + injected RTCPeerConnection factory for tests); never serialized, never crosses the gateway/SPA boundary
  /**
   * Factory for the underlying `RTCPeerConnection` — injected so tests can
   * supply a fake without a real WebRTC implementation. Defaults to a real
   * `RTCPeerConnection` configured with a hardcoded public STUN server
   * (wave-plan W2-B: "hardcode the Google STUN default; the gateway relay
   * uses its own [config]" — this is the VIEWER leg only).
   */
  pcFactory?: () => RTCPeerConnection
  /** ms to wait for `applyAnswer` after the offer is actually sent (i.e.
   * after ICE gathering completes) before falling back. Default 5000. Only
   * governs a negotiation attempt made AFTER this session has connected at
   * least once (`hasConnectedOnce === true`) — see `firstAnswerTimeoutMs`
   * for the cold-start case, and the class-level doc comment above for why
   * the split exists. */
  answerTimeoutMs?: number
  /** ms to wait for `applyAnswer` on a negotiation attempt made BEFORE this
   * session has ever reached 'connected' (`hasConnectedOnce === false`) —
   * i.e. the cold-start case. Default 30000 (fix-wave, MED: the gateway's
   * legitimate cold start can take 25s+, well past the regular
   * `answerTimeoutMs` default of 5s). */
  firstAnswerTimeoutMs?: number
  /** ms an ICE `disconnected` state is tolerated before falling back.
   * Default 15000. */
  disconnectedGraceMs?: number
  /** ms before the FIRST automatic re-offer after a fallback; each further
   * consecutive retry doubles it. Default 15000. */
  retryDelayMs?: number
  /** Max CONSECUTIVE failed re-offers before giving up until the next
   * `start()`. Reset by a successful connect. Default 5. */
  maxRetries?: number
  /** ms to wait for `RTCPeerConnection.iceGatheringState` to reach
   * 'complete' before giving up and sending the offer with whatever
   * candidates have gathered so far (non-trickle still works with a partial
   * candidate set — better than wedging in 'offering' forever on a stuck
   * gathering). Default 3000. */
  iceGatheringTimeoutMs?: number
}

/**
 * The subset of `BrowserWebRTCStateFrame` `applyState` reacts to. `reason` is
 * deliberately widened from the frame's generated (currently 4-value) enum
 * to a plain `string` — fix-wave C: this class only ever checks *truthiness*
 * of `reason` and forwards it verbatim to `onFallback`, it never pattern-
 * matches against the enum's specific literal members, so it must not
 * require the wire enum to already enumerate a value (e.g. a prospective
 * "ingest_timeout") before this class can react to it correctly. Not itself
 * a wire type — never constructed from raw JSON; the one real call site
 * (BrowserLiveView.tsx) always narrows an already-Zod-validated
 * `BrowserWebRTCStateFrame`, which satisfies this structurally (its `reason`
 * union is a subtype of `string`).
 */
interface BrowserWebRTCStateSignal { // not-wire-format: locally widened view of BrowserWebRTCStateFrame (reason enum -> string) so applyState reacts correctly to a reason value the generated wire enum doesn't list yet; never constructed from raw JSON itself
  available: boolean
  reason?: string
  active?: boolean
}

const DEFAULT_STUN_SERVER = 'stun:stun.l.google.com:19302'
const DEFAULT_ANSWER_TIMEOUT_MS = 5000
const DEFAULT_FIRST_ANSWER_TIMEOUT_MS = 30000
// DEFAULT_DISCONNECTED_GRACE_MS (2026-07-30 UAT, Batam→Fly over VPN on iPad
// Safari): was 5000. A 5s grace is far too tight for a high-latency mobile /
// VPN path, where ICE 'disconnected' is a routine, self-recovering blip. The
// live evidence: every viewer connection died 2-22s after connecting, the
// client closed the PeerConnection itself (the gateway logged the SCTP
// "User Initiated Abort: Close called" from OUR side), and the panel dropped
// to the JPEG sink. 15s also sits comfortably under the relay's own
// disconnect eviction (pkg/tools/browser/webrtc/viewer.go's
// disconnectGracePeriod, raised to 30s in the same fix) so the server no
// longer evicts a viewer the client is still trying to recover — before,
// the client (5s) and server (10s) both gave up, in that order, on a link
// that would have come back.
const DEFAULT_DISCONNECTED_GRACE_MS = 15000
const DEFAULT_RETRY_DELAY_MS = 15000
// DEFAULT_MAX_RETRIES (same UAT): the retry budget used to be a single
// one-shot boolean (`retriedOnce`) — after ONE failed re-offer the panel was
// stranded in picture mode for the rest of the session with no way back
// except a full re-attach. On a flaky mobile link that is precisely the
// wrong policy: the transport recovers, but nothing ever tries again. The
// budget is now a counter with exponential backoff, and it RESETS on every
// successful connection, so only a sustained inability to connect
// (5 consecutive failures, ~15s/30s/60s/120s/240s apart) gives up for good.
const DEFAULT_MAX_RETRIES = 5
// 3000 was a hair-trigger that macOS loses. Measured on darwin 2026-08-12 in a
// real browser on this machine: gathering completes at ~3224ms and yields
// exactly ONE candidate, an mDNS `<uuid>.local` host candidate. Chrome
// registers that name with the system mDNS responder before it will emit the
// candidate, and on macOS that registration is what costs the ~3s.
//
// Losing this race is not a degraded connection, it is a guaranteed dead one:
// signaling here is NON-TRICKLE, so candidates that arrive after the offer is
// sent are never delivered at all. The old value shipped an offer with ZERO
// candidates, Pion had nothing to pair against, and ICE failed 30s later with
// no indication why. Every live-browser session on macOS hit this.
const DEFAULT_ICE_GATHERING_TIMEOUT_MS = 12000

function defaultPcFactory(): RTCPeerConnection {
  return new RTCPeerConnection({ iceServers: [{ urls: DEFAULT_STUN_SERVER }] })
}

/**
 * Operator directive (JPEG-fallback removal) — turns a raw `onFallback`
 * reason string into the honest, actionable message BrowserLiveView.tsx
 * shows as the panel's PRIMARY content once WebRTC fails, not a toast:
 * WebRTC is the only live-video path left, so every one of these reasons —
 * including the three "capability gate" reasons that used to be swallowed
 * silently because the JPEG sink kept working underneath — now means the
 * user is looking at a broken panel and deserves to know why.
 *
 * Grouped by what the user can actually do about it:
 *   - `disabled`/`not_capable`/`lite_build`: this install/build genuinely
 *     doesn't offer live video here — retrying changes nothing; the copy
 *     says so plainly rather than inviting a doomed retry.
 *   - everything else (ice-failed, ice-disconnected-timeout, answer-timeout,
 *     offer-send-failed, set-remote-description-failed, pc-create-failed,
 *     offer-setup-failed, no-local-description, stream-stopped, `error`, the
 *     `unavailable` default, or any future reason string not in this list
 *     yet): a genuine connection/negotiation failure that a fresh attempt
 *     can plausibly recover from — the caller's Retry button is meaningful.
 *
 * Deliberately a plain switch over known strings with a generic fallback
 * (not an exhaustive union) — `reason` is intentionally widened to `string`
 * (see `BrowserWebRTCStateSignal`'s own doc comment) so a future gateway
 * build can introduce a new wire reason without this function needing to
 * enumerate it first; the fallback branch keeps the raw reason visible
 * (never swallows it) so a support engineer can still search logs/console
 * for the exact string even when the copy above it is generic.
 */
export function translateWebRTCFallbackReason(reason: string): string {
  switch (reason) {
    case 'disabled':
      return 'Live video is turned off for this installation. Ask your operator to enable it in Settings.'
    case 'not_capable':
      return "Live video isn't supported on this server (WebRTC capture isn't available on this platform or build)."
    case 'lite_build':
      return "Live video isn't available in this lite build."
    case 'error':
      return 'The live browser reported an error starting video. Retry, or reload the page if it keeps failing.'
    default:
      return `Live video connection failed (${reason}). Retry, or reload the page if it keeps failing.`
  }
}

export class BrowserWebRTCSession {
  private readonly pcFactory: () => RTCPeerConnection
  private readonly answerTimeoutMs: number
  private readonly firstAnswerTimeoutMs: number
  private readonly disconnectedGraceMs: number
  private readonly retryDelayMs: number
  private readonly maxRetries: number
  private readonly iceGatheringTimeoutMs: number
  /** Lifetime flag (never reset once true) — has this session's PC EVER
   * reached ICE 'connected'/'completed', across every attempt this instance
   * has ever made? Backs both the answer-timeout duration choice
   * (`_startAnswerTimeout`) and the public `hasConnectedOnce` getter, which
   * BrowserLiveView.tsx reads to decide whether an 'answer-timeout' fallback
   * is a cold-start false positive (never connected — stay quiet) or a
   * genuine degradation of a previously-working stream (connected before —
   * warn the user). */
  private _hasConnectedOnce = false

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
  /** Consecutive failed re-offer attempts. Reset to 0 by `start()` and by
   * every successful transition to 'connected' — see DEFAULT_MAX_RETRIES. */
  private retryCount = 0

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
    this.firstAnswerTimeoutMs = options.firstAnswerTimeoutMs ?? DEFAULT_FIRST_ANSWER_TIMEOUT_MS
    this.disconnectedGraceMs = options.disconnectedGraceMs ?? DEFAULT_DISCONNECTED_GRACE_MS
    this.retryDelayMs = options.retryDelayMs ?? DEFAULT_RETRY_DELAY_MS
    this.maxRetries = options.maxRetries ?? DEFAULT_MAX_RETRIES
    this.iceGatheringTimeoutMs = options.iceGatheringTimeoutMs ?? DEFAULT_ICE_GATHERING_TIMEOUT_MS
  }

  get state(): BrowserWebRTCState {
    return this._state
  }

  /** True once this session's PC has EVER reached ICE 'connected'/'completed'
   * — a lifetime flag, never reset by a later fallback/retry. See the
   * class-level doc comment's "Answer-timeout duration" section for why this
   * exists: BrowserLiveView.tsx reads it to distinguish a cold-start false
   * positive from a genuine stream degradation on an 'answer-timeout'
   * fallback. */
  /** Consecutive failed re-offers so far. Lets a caller distinguish a first
   * cold-start attempt (where an answer-timeout is plausibly just a slow
   * capture start) from a repeated failure that deserves surfacing. */
  get retryAttempts(): number {
    return this.retryCount
  }

  get hasConnectedOnce(): boolean {
    return this._hasConnectedOnce
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
    this.retryCount = 0
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
   * caller's job (wave-plan W2-B wiring note), so an available:true frame
   * with neither a `reason` nor `active:false` is intentionally a no-op
   * here — that's the routine post-attach "you may offer" re-announcement
   * (`announceWebRTCAvailability`, sent with no reason at all).
   *
   * fix-wave C (the "first-offer failure never surfaces" defect,
   * 2026-07-28): a HandleViewerOffer failure is reported as
   * `available:true` (WebRTC as a *capability* is still fine) with a
   * populated `reason` (e.g. "error", or a future distinct value like
   * "ingest_timeout") — `active` is NOT sent as an explicit `false` on that
   * frame (the gateway's Go `*bool` + `omitempty` encoding only ever
   * serializes a literal `true` or omits the field entirely, see
   * `sendWebRTCState`/pkg/gateway/browser_webrtc.go), so it arrives on the
   * wire as `undefined`, not `false`. The OLD logic here only ever reacted
   * to `active === false` while `this._state === 'connected'` — for a FIRST
   * offer, the machine has never left `'offering'` (no PC was ever built
   * server-side), so that check never matched, and the frame was a complete
   * no-op: the user then waited out the full `firstAnswerTimeoutMs` (30s)
   * local timer before anything visibly changed, even though the gateway
   * had already reported failure within seconds.
   *
   * The fix: a truthy `reason` alongside `available:true` is ALWAYS a
   * genuine failure report for THIS connection's in-flight attempt — the
   * routine re-announcement never carries one — so react to `reason`
   * presence directly (while `offering` OR `connected`), regardless of
   * `active`. This is forward-compatible with any future reason string
   * (e.g. a prospective "ingest_timeout") without this file needing to know
   * its exact spelling — the wire's `reason` is passed straight through as
   * the fallback reason, exactly like the `available:false` branch already
   * does.
   *
   * The `active === false` check is kept as a fallback trigger in its own
   * right (defensive — some future/test caller may construct a frame with
   * an explicit `false` and no `reason`) but is only reachable once this
   * session had already connected: an ordinary in-flight `offering` attempt
   * with `active:false` and NO reason is still treated as ambiguous/benign
   * rather than assumed to be a failure.
   */
  applyState(frame: BrowserWebRTCStateSignal): void {
    if (!frame.available) {
      if (this._state === 'offering' || this._state === 'connected') {
        this._fallback(frame.reason ?? 'unavailable')
      }
      return
    }
    if (this._state !== 'offering' && this._state !== 'connected') return
    if (frame.reason) {
      this._fallback(frame.reason)
      return
    }
    if (frame.active === false && this._state === 'connected') {
      this._fallback('stream-stopped')
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
      // still works with whatever candidates gathered in the timeout window,
      // so proceeding with a PARTIAL set beats never proceeding at all. NOTE:
      // an earlier revision of this comment claimed the worst case was 'fewer
      // candidate types, not zero'. That is false on macOS, where the sole
      // candidate is an mDNS name whose registration can outlast the deadline
      // — measured 3224ms against a 3000ms timeout — leaving exactly zero. The caller
      // (`_beginOffer`) re-checks `this.pc !== pc` right after this resolves,
      // so a timeout firing after the session was superseded/stopped is
      // harmless.
      const timer = setTimeout(() => {
        pc.onicegatheringstatechange = null
        // Proceeding with a PARTIAL candidate set is fine. Proceeding with an
        // EMPTY one never is: non-trickle means an offer carrying no
        // candidates can only ever fail, 30s later, with ICE 'failed' and
        // nothing pointing at the cause. Surface it here instead.
        const gathered = (pc.localDescription?.sdp ?? '')
          .split('\n')
          .filter((l) => l.trim().startsWith('a=candidate:')).length
        if (gathered === 0) {
          console.warn(
            `[browserWebRTC] ICE gathering timed out after ${this.iceGatheringTimeoutMs}ms with ZERO ` +
              'candidates. The offer is undeliverable — signaling is non-trickle, so candidates ' +
              'gathered after this point are never sent. Expect ICE to fail ~30s from now. On macOS ' +
              'this is usually slow or blocked mDNS (.local) candidate registration.',
          )
        }
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
        this._hasConnectedOnce = true
        // Reaching 'connected' proves the path works RIGHT NOW, so the
        // retry budget starts fresh: a later blip gets its own full set of
        // attempts rather than inheriting exhaustion from an earlier,
        // already-recovered one. Without this reset the counter would be a
        // session-lifetime cap and a long session on a flaky link would
        // still end up permanently stranded in the panel's failure state.
        this.retryCount = 0
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
    // fix-wave (MED): a cold-start attempt (never connected before) gets the
    // generous firstAnswerTimeoutMs; once this session has connected at
    // least once, a later negotiation (e.g. after an ICE failure) reverts to
    // the short, regular answerTimeoutMs — see the class-level doc comment.
    const timeoutMs = this._hasConnectedOnce ? this.answerTimeoutMs : this.firstAnswerTimeoutMs
    this.answerTimeoutTimer = setTimeout(() => {
      this.answerTimeoutTimer = null
      if (this._state === 'offering') this._fallback('answer-timeout')
    }, timeoutMs)
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
    if (this.retryCount >= this.maxRetries) return
    // Exponential backoff, capped by the attempt count: a transient blip
    // recovers on the first retry (retryDelayMs), while a genuinely-down
    // relay is not hammered every 15s for the life of the panel.
    const delay = this.retryDelayMs * 2 ** this.retryCount
    this.retryCount++
    this.retryTimer = setTimeout(() => {
      this.retryTimer = null
      if (!this.stopped) void this._beginOffer()
    }, delay)
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

// browserLiveWs — WebSocket client for /api/v1/browser/ws (ADR-038 D1/D5).
//
// A second, self-contained WS connection separate from the chat WS
// (src/lib/ws.ts), carrying control/input/tab/signaling frames — live VIDEO
// itself rides the WebRTC data plane (browserWebRTC.ts), not this socket
// (the JPEG-screencast-over-WS path was removed outright, ADR-047). Kept
// deliberately lighter-weight than WsConnection: no Web Worker parse
// offload, no visibility/online listeners, no application-level ping/pong
// heartbeat — the panel is user-opened/closed on demand, and liveness is
// left to the browser's own WS transport ping/pong plus the socket's close
// event (see ws.onclose below). Auth rides the same-origin
// `omnipus-session` HttpOnly cookie on the WS handshake (ADR-044), like
// ws.ts — no client-sent auth frame, no JS-readable token.
//
// Wire types are sourced exclusively from the generated AsyncAPI types/Zod —
// hand-written interface declarations for wire-format frames are FORBIDDEN
// (CLAUDE.md hard-constraint #8).

import { WsFrame as WsFrameSchema } from '@/lib/api/generated/schemas'
import type {
  BrowserAttachFrame,
  BrowserControlFrame,
  BrowserDetachFrame,
  BrowserInputFrame,
  BrowserStatusFrame,
  BrowserTabActionFrame,
  BrowserViewportFrame,
  BrowserTabsFrame,
  BrowserVideoHealthFrame,
  BrowserWebRTCAnswerFrame,
  BrowserWebRTCOfferFrame,
  BrowserWebRTCStateFrame,
  ErrorFrame,
} from '@/lib/api/generated/asyncapi-types'

/** Frame types this socket ever receives — a narrow slice of the full WsFrame union.
 * Deliberately excludes `browser_screencast` (JPEG fallback, removed — ADR-047
 * WebRTC is the only live-video path now); if the gateway still emits one
 * mid-flight, `parseBrowserFrame` below drops it like any other frame type
 * this socket doesn't understand. */
type BrowserServerFrame =
  | BrowserStatusFrame
  | BrowserTabsFrame
  | BrowserWebRTCAnswerFrame
  | BrowserWebRTCStateFrame
  | BrowserVideoHealthFrame
  | ErrorFrame

export interface BrowserLiveWsCallbacks { // not-wire-format: SPA-only callback interface passed to BrowserLiveWsConnection's constructor. Never serialized to or from the gateway.
  onStatus: (frame: BrowserStatusFrame) => void
  /** ADR-041 D4 — the current tab list + active index, broadcast on any tab open/close/switch/title-change. */
  onTabs: (frame: BrowserTabsFrame) => void
  /** ADR-047 (WebRTC build) — the gateway's non-trickle SDP answer to a
   * `browser_webrtc_offer` this connection sent. Feed straight into
   * `BrowserWebRTCSession.applyAnswer` (browserWebRTC.ts). */
  onWebRTCAnswer: (frame: BrowserWebRTCAnswerFrame) => void
  /** ADR-047 — availability/fallback-reason/audio-presence, sent after
   * attach and again whenever it changes (encoder died, tab-capable build
   * absent, etc.). Feed into `BrowserWebRTCSession.applyState`; the caller
   * also reads `available`/`has_audio` directly to decide whether to call
   * the machine's `start()`. */
  onWebRTCState: (frame: BrowserWebRTCStateFrame) => void
  /** Issue #674 — health of the SHARED capture feeding every viewer:
   * `lost` / `recovering` (with `attempt`/`max_attempts`) / `recovered` /
   * `unrecoverable`. Pushed the instant the gateway knows, so the panel
   * states the real reason instead of waiting out its first-frame timeout.
   * Distinct from `onWebRTCState`, which is about THIS viewer's signalling. */
  onVideoHealth: (frame: BrowserVideoHealthFrame) => void
  /** Fires for both server-sent `error` frames and local transport errors (create/send/reconnect-exhausted). */
  onError: (message: string) => void
  onConnected?: () => void
  onDisconnected?: () => void
}

// ── Reconnect schedule ────────────────────────────────────────────────────
// Deliberately lighter than ws.ts's two-phase (fast/slow) schedule — this is
// a short-lived, user-opened panel, not the always-on chat connection. A
// handful of exponential-backoff attempts, then give up with a clear message
// (the user can close and reopen the panel to retry from a clean state).

const MAX_RECONNECT_ATTEMPTS = 5
const RECONNECT_BASE_DELAY_MS = 1000
const RECONNECT_MAX_DELAY_MS = 8000

function getBrowserWsUrl(): string {
  const httpBase = (import.meta.env.VITE_API_URL as string | undefined) || window.location.origin
  const wsBase = httpBase.replace(/^http/, 'ws')
  return `${wsBase}/api/v1/browser/ws`
}

// fix-wave B (LOW) — Constraint #8's "drop + counter + dev-mode toast on
// failure" was only half-honored here: invalid payloads were dropped, but
// silently, with no counter and no dev-visible signal. Deliberately
// lightweight (no toast — "no prod toast" per the fix-wave scope; a toast per
// dropped screencast/status frame in a lossy dev session would be noise, not
// signal) — a module-level counter plus a dev-only console.debug is enough to
// make schema drift grep-able without adding UI chrome to this socket.
let _zodDropCount = 0

/** Test/debug hook — the running count of frames dropped by a failed
 * `WsFrameSchema.safeParse` since module load. */
export function getBrowserFrameDropCount(): number {
  return _zodDropCount
}

/** Validates + narrows an incoming payload to the frames this socket cares about. Never throws. */
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
  if (!result.success) {
    _zodDropCount++
    if (import.meta.env.DEV) {
      console.debug('[browserLiveWs] dropped a frame that failed schema validation', result.error, raw)
    }
    return null
  }

  const frame = result.data
  if (
    frame.type === 'browser_status' ||
    frame.type === 'browser_tabs' ||
    frame.type === 'browser_webrtc_answer' ||
    frame.type === 'browser_webrtc_state' ||
    frame.type === 'browser_video_health' ||
    frame.type === 'error'
  ) {
    return frame
  }
  // Any other frame type — including a `browser_screencast` from a gateway
  // that hasn't dropped the JPEG path yet, and any chat-only frame — is not
  // relevant to this socket; drop defensively rather than forwarding
  // something the panel doesn't understand.
  return null
}

// D5 fix (UAT): the backend surfaces raw Go error/protocol strings verbatim
// on TWO distinct wire paths over this connection — a terminal
// `browser_status{state:'error'}` frame's `message` (handled by
// BrowserLiveView's onStatus callback) AND this connection's own `error`-type
// ErrorFrame (handled by `onmessage` below, D5 "Site 2") — e.g. `browser
// input failed: browser live: navigate blocked: ... SSRF: blocked cloud
// metadata endpoint 169.254.169.254`, Go's url.Parse format `parse "...":
// invalid character " " in host name`, or protocol-internal strings such as
// `browser_attach: agent_id and session_id are required` / `frame schema
// validation failed (...): ...` / `unknown frame type "..."`.
// translateBrowserErrorMessage maps every known, user-triggerable case to
// plain language; anything unrecognized passes through unchanged rather than
// risk hiding real diagnostic information.
//
// Deliberately excluded from the patterns below (left to pass through
// as-is): the `sessionErrorStatus(...)` messages in pkg/gateway/browser_ws.go
// (e.g. "another viewer already controls this browser", "take-control is
// disabled by the operator", "no browser manager for agent ...") — those are
// already hand-written, human-readable copy, not Go-internal jargon, and the
// auth-handshake ErrorFrame messages (pkg/gateway/websocket.go /
// browser_ws.go's `wsAuthErr*` constants), which were fixed at the SOURCE to
// already be human-readable rather than translated client-side.
//
// Exported (moved here from BrowserLiveView.tsx, which was the sole D5
// "Site 1" caller) so BOTH leak sites share one function — they can never
// drift into two different translations of the identical underlying string.
export function translateBrowserErrorMessage(raw: string): string {
  // Order matters: the backend wraps EVERY navigate failure — a Go url.Parse
  // error, an ordinary DNS/network failure, AND an actual SSRF policy block
  // — identically as "... navigate blocked: ...", so classification must run
  // from MOST specific to LEAST specific. Reviewer finding F2: the old
  // `navigate blocked|SSRF` catch-all also matched the ordinary "domain
  // doesn't resolve / typo / site down" path — pkg/security/ssrf.go's
  // resolver returns e.g. "SSRF: DNS resolution failed for <host>" or
  // "SSRF: no addresses found for <host>" for that path (string-prefixed
  // "SSRF:" because the resolver lives in the SSRF-guarded dial path, not
  // because the address was actually policy-blocked) — so a mistyped or
  // unreachable URL was wrongly told it was "blocked for security reasons".
  // The invalid-URL and unreachable branches below are checked BEFORE the
  // real-block branch, and the real-block branch is narrowed to the actual
  // policy-deny messages (cloud metadata / private IP range / explicit
  // "SSRF: blocked ...") rather than any string merely containing "SSRF".
  if (/invalid character .* in host name|parse "[^"]*":/i.test(raw)) {
    console.debug('[browser] invalid URL:', raw)
    return "That doesn't look like a valid web address."
  }
  if (/DNS resolution failed|no addresses found|too many redirects|cannot extract host|could not resolve/i.test(raw)) {
    console.debug('[browser] address unreachable:', raw)
    return "That address couldn't be reached — check the URL and try again."
  }
  if (/blocked cloud metadata|blocked private IP|blocked .* range|SSRF: blocked/i.test(raw)) {
    console.debug('[browser] navigation blocked:', raw)
    return 'That address is blocked for security reasons.'
  }
  // D5 (BrowserLiveView.tsx D5 write-up) — the 9-message closed set of
  // protocol-internal `errorStatus(...)`/ErrorFrame strings from
  // pkg/gateway/browser_ws.go's readLoop/handleAttach/handleControl/
  // handleTabAction: `frame schema validation failed (%s): %s` (readLoop
  // inbound-schema gate), `browser_attach: invalid frame` /
  // `browser_attach: agent_id and session_id are required` (handleAttach),
  // `browser_control: attach before requesting control` /
  // `browser_control: invalid frame` / `browser_control: unknown action %q`
  // (handleControl), `browser_tab_action: attach before managing tabs` /
  // `browser_tab_action: invalid frame` / `browser_tab_action: unknown
  // action %q` (handleTabAction), plus the readLoop `unknown frame type
  // %q` / `invalid frame: not JSON` ErrorFrame-only cases (D5 Site 2). Every
  // one of these means "the client and server got out of sync" — reconnect
  // (reopen the panel) is the correct recovery for all of them, so they
  // collapse to a small number of distinct user-facing messages rather than
  // needing an exact 1:1 copy per string.
  if (/^frame schema validation failed/i.test(raw) || /^invalid frame: not JSON/i.test(raw) || /^unknown frame type /i.test(raw)) {
    console.debug('[browser] protocol frame rejected:', raw)
    return "That action couldn't be sent — try again."
  }
  if (/^browser_attach:/i.test(raw)) {
    console.debug('[browser] attach failed:', raw)
    return "Couldn't connect to the live browser — try reopening the panel."
  }
  if (/^browser_control: attach before requesting control/i.test(raw)) {
    console.debug('[browser] control before attach:', raw)
    return 'Reconnect to the live browser before taking control.'
  }
  if (/^browser_control:/i.test(raw)) {
    console.debug('[browser] control failed:', raw)
    return "That action couldn't be sent — try again."
  }
  if (/^browser_tab_action: attach before managing tabs/i.test(raw)) {
    console.debug('[browser] tab action before attach:', raw)
    return 'Reconnect to the live browser before managing tabs.'
  }
  if (/^browser_tab_action:/i.test(raw)) {
    console.debug('[browser] tab action failed:', raw)
    return "That action couldn't be sent — try again."
  }
  return raw
}

/**
 * Issue #674 — turns a `browser_video_health` frame into the sentence the
 * panel shows, or `null` when there is nothing to say.
 *
 * Why the panel needs this at all: the gateway learns the capture's video
 * feed died within microseconds, but the SPA used to learn only by
 * exhausting FIRST_FRAME_TIMEOUT_MS (45s). That constant is NOT the bug and
 * must not be shortened — its value was derived after a live incident where
 * a healthy-but-slow cold start showed a red error and then connected fine.
 * The fix is that the deadline stops being the only source of news.
 *
 * `recovered` returns null: the panel's job then is to stop showing an
 * error, not to show a different one.
 *
 * `detail` is appended VERBATIM (matching translateWebRTCFallbackReason's
 * "Reported cause:" convention) rather than pattern-matched into friendlier
 * prose — a cause we do not recognise is exactly the case where the raw text
 * matters most, and it is already whitespace-collapsed, credential-redacted
 * and length-bounded server-side.
 */
export function describeVideoHealth(frame: BrowserVideoHealthFrame | null | undefined): string | null {
  if (!frame || frame.state === 'recovered') return null
  const detail = frame.detail?.trim()
  const withCause = (headline: string) => (detail ? `${headline} Reported cause: ${detail}` : headline)

  switch (frame.state) {
    case 'lost':
      return withCause("The live browser's video stopped. Reconnecting automatically…")
    case 'recovering': {
      // Stating the bound is the point: an unbounded spinner and a bounded
      // retry look identical to a user, and only one of them is going to end.
      const attempt = frame.attempt ?? 0
      const max = frame.max_attempts ?? 0
      const progress = attempt > 0 && max > 0 ? ` (attempt ${attempt} of ${max})` : ''
      return withCause(`The live browser's video stopped. Reconnecting${progress}…`)
    }
    case 'unrecoverable': {
      const max = frame.max_attempts ?? 0
      const attempts = max > 0 ? ` after ${max} attempts` : ''
      return withCause(
        `The live browser could not restart its video${attempts}. Retry, or reload the page if it keeps failing.`,
      )
    }
    default:
      return null
  }
}

export class BrowserLiveWsConnection {
  private ws: WebSocket | null = null
  private readonly sessionId: string
  private readonly agentId: string
  private readonly callbacks: BrowserLiveWsCallbacks
  private intentionalClose = false
  private reconnectAttempts = 0
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null

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
    this.ws = ws

    ws.onopen = () => {
      this.reconnectAttempts = 0
      // Auth rides the WS handshake via the same-origin `omnipus-session`
      // HttpOnly cookie (ADR-044): the browser attaches it automatically and
      // the gateway's browser-WS handler authenticates the handshake from it.
      // No client-sent `{type:"auth",token}` frame — there is no JS-readable
      // token any more.
      const attach: BrowserAttachFrame = {
        type: 'browser_attach',
        session_id: this.sessionId,
        agent_id: this.agentId,
      }
      this._rawSend(attach)
      this.callbacks.onConnected?.()
    }

    ws.onmessage = (event: MessageEvent) => {
      const frame = parseBrowserFrame(event.data as string)
      if (!frame) return
      if (frame.type === 'browser_status') {
        this.callbacks.onStatus(frame)
      } else if (frame.type === 'browser_tabs') {
        this.callbacks.onTabs(frame)
      } else if (frame.type === 'browser_webrtc_answer') {
        this.callbacks.onWebRTCAnswer(frame)
      } else if (frame.type === 'browser_webrtc_state') {
        this.callbacks.onWebRTCState(frame)
      } else if (frame.type === 'browser_video_health') {
        this.callbacks.onVideoHealth(frame)
      } else {
        // D5 fix (Site 2) — this was an unconditional raw pass-through of
        // the server's ErrorFrame.message (protocol-internal Go strings like
        // `first message must be {"type":"auth",...}` or `browser_control:
        // attach before requesting control`); route it through the same
        // translation BrowserLiveView uses for browser_status error frames
        // so both leak sites produce identical, human-readable copy.
        this.callbacks.onError(translateBrowserErrorMessage(frame.message))
      }
    }

    ws.onerror = () => {
      this.callbacks.onError('Live browser connection error — will retry.')
    }

    ws.onclose = (event: CloseEvent) => {
      this.ws = null
      this.callbacks.onDisconnected?.()
      if (this.intentionalClose) return

      // 1008 = policy violation / auth rejected by the gateway; reconnecting
      // would just be rejected again (the cookie hasn't changed), so surface
      // once and stop.
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

  /** ADR-047 (WebRTC build) — sends this connection's non-trickle SDP offer
   * (all ICE candidates already gathered by the caller — browserWebRTC.ts's
   * `BrowserWebRTCSession` waits for `iceGatheringState === 'complete'`
   * before calling this). Carries session_id/agent_id explicitly like
   * `sendTabAction`, since both are required fields on the wire frame. */
  sendWebRTCOffer(sdp: string): boolean {
    const frame: BrowserWebRTCOfferFrame = {
      type: 'browser_webrtc_offer',
      session_id: this.sessionId,
      agent_id: this.agentId,
      sdp,
    }
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

  /**
   * Reports this viewer's panel geometry so the backend can size the captured
   * tab to match, instead of the hardcoded 1280x720 it was pinned to.
   *
   * Operator UAT 2026-07-31: the docked panel is an arbitrary resizable shape
   * (measured ~890x1010, portrait) while the capture was fixed 16:9. Because
   * `object-fit: contain` preserves the SOURCE aspect, the page could fill
   * only one dimension and the rest of the panel was letterboxed black — too
   * small to interact with. `deviceScaleFactor` addresses the same report's
   * second half (blur): the managed headless Chrome renders at DPR 1, so a
   * capture displayed larger than its CSS size upscales.
   *
   * Caller is responsible for debouncing — the backend applies the metrics and
   * then forces a capture rebuild, which is a visible (if brief) stream blip,
   * so this must not be sent per resize event.
   */
  sendViewport(width: number, height: number, deviceScaleFactor: number): boolean {
    const frame: BrowserViewportFrame = {
      type: 'browser_viewport',
      session_id: this.sessionId,
      agent_id: this.agentId,
      // Integers only — the wire schema declares these as `integer`, and a
      // fractional CSS size (common on fractional-DPR displays) would fail
      // server-side schema validation and be dropped.
      width: Math.max(1, Math.round(width)),
      height: Math.max(1, Math.round(height)),
      // Clamped to the schema's 1..3 range. Cost scales with the SQUARE of
      // this, and some devices report 4+.
      device_scale_factor: Math.min(3, Math.max(1, deviceScaleFactor)),
    }
    return this._rawSend(frame)
  }

  /** Tells the backend this viewer is done watching (ref-counted engine stops screencast when the last viewer detaches — ADR-038 D3). Does not close the socket. */
  detach(): void {
    const frame: BrowserDetachFrame = { type: 'browser_detach', session_id: this.sessionId }
    this._rawSend(frame)
  }

  /** Closes the socket and cancels any pending reconnect. Call detach() first if the viewer is going away cleanly. */
  close(): void {
    this.intentionalClose = true
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

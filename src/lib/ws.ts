// WebSocket connection manager for /api/v1/chat/ws
// Handles: connect, authenticate, streaming frames, reconnect with exponential backoff

import { maybeDevToast } from '@/lib/dev-toast'

// ── Generated type imports ────────────────────────────────────────────────────
// All wire-format frame types are sourced from the generated AsyncAPI types.
// Hand-written interface declarations for wire-format frames are FORBIDDEN —
// see CLAUDE.md hard-constraint #8.

import { WsFrame as WsFrameSchema, WsFrameType as WsFrameTypeSchema } from '@/lib/api/generated/schemas'
import { ClientFrameTypes } from '@/lib/api/generated/asyncapi-types'

import type {
  WsFrameType,
  WsFrame,
  ServerFrame,
  ClientFrame,
  // Server → client frames
  SessionStartedFrame,
  TokenFrame,
  DoneFrame,
  DoneStats,
  ErrorFrame,
  ToolCallStartFrame,
  ToolCallResultFrame,
  TruncatedResult,
  MarshalErrorResult,
  SubagentStartFrame,
  SubagentEndFrame,
  ExecApprovalRequestFrame,
  TaskStatusChangedFrame,
  ReplayMessageFrame,
  RateLimitFrame,
  MediaFrame,
  MediaPart,
  AgentSwitchedFrame,
  ToolApprovalRequiredFrame,
  SessionStatePendingApproval,
  SessionStateFrame,
  SystemOverloadFrame,
  ReplayWarningFrame,
  ReplayWarningStats,
  CancelStageFrame,
  SessionCloseAckFrame,
  ExecApprovalResponseAckFrame,
  DevicePairingRequestFrame,
  // Client → server frames
  AuthFrame,
  MessageFrame,
  CancelFrame,
  ExecApprovalResponseFrame,
  PingFrame,
  AttachSessionFrame,
  DevicePairingResponseFrame,
  SessionCloseFrame,
} from '@/lib/api/generated/asyncapi-types'

// Re-export canonical names from generated file
export { ClientFrameTypes }

export type {
  WsFrameType,
  WsFrame,
  ServerFrame,
  ClientFrame,
  SessionStartedFrame,
  TokenFrame,
  DoneFrame,
  DoneStats,
  ErrorFrame,
  ToolCallStartFrame,
  ToolCallResultFrame,
  TruncatedResult,
  MarshalErrorResult,
  SubagentStartFrame,
  SubagentEndFrame,
  ExecApprovalRequestFrame,
  TaskStatusChangedFrame,
  ReplayMessageFrame,
  RateLimitFrame,
  MediaFrame,
  MediaPart,
  AgentSwitchedFrame,
  ToolApprovalRequiredFrame,
  SessionStatePendingApproval,
  SessionStateFrame,
  SystemOverloadFrame,
  ReplayWarningFrame,
  ReplayWarningStats,
  CancelStageFrame,
  SessionCloseAckFrame,
  ExecApprovalResponseAckFrame,
  DevicePairingRequestFrame,
  AuthFrame,
  MessageFrame,
  CancelFrame,
  ExecApprovalResponseFrame,
  PingFrame,
  AttachSessionFrame,
  DevicePairingResponseFrame,
  SessionCloseFrame,
}

// ── WsXxx legacy aliases (active callers only) ───────────────────────────────
//
// Aliases retained for active callers in src/store/chat.ts,
// src/store/toolApproval.ts, src/components/chat/SubagentBlock.tsx,
// src/components/agents/ToolApprovalModal.test.tsx, and src/lib/wsParser.test.ts.
// New code should import canonical names from @/lib/api/generated/asyncapi-types.

export type WsSubagentStartFrame = SubagentStartFrame
export type WsSubagentEndFrame = SubagentEndFrame
export type WsExecApprovalRequestFrame = ExecApprovalRequestFrame
export type WsReplayMessageFrame = ReplayMessageFrame
export type WsRateLimitFrame = RateLimitFrame
export type WsToolApprovalRequiredFrame = ToolApprovalRequiredFrame
export type WsSessionStateFrame = SessionStateFrame

// WsReceiveFrame: union of all server→client frames.
export type WsReceiveFrame = ServerFrame

// ── Dropped-frame counter ─────────────────────────────────────────────────────
//
// Module-level mutable counters. No locking needed in single-threaded JS.
// Exported for tests and telemetry.
//
// _droppedFrameCount — frames dropped because they failed schema validation
//   (known type but missing/invalid fields, or client→server frame received).
// _unknownFrameTypeCount — frames dropped because their `type` field is not
//   in the WsFrameType enum (forward-compat path: new server frame types not
//   yet in the spec; logged separately so we can distinguish "spec drift" from
//   "malformed payload").

let _droppedFrameCount = 0
let _unknownFrameTypeCount = 0

export function getDroppedFrameCount(): number {
  return _droppedFrameCount
}

export function resetDroppedFrameCount(): void {
  _droppedFrameCount = 0
}

export function getUnknownFrameTypeCount(): number {
  return _unknownFrameTypeCount
}

export function resetUnknownFrameTypeCount(): void {
  _unknownFrameTypeCount = 0
}

// ── Test/dev telemetry hooks ──────────────────────────────────────────────────
//
// Expose counters on window.__omnipus_test_hooks in dev and test builds so
// Playwright tests can assert dropped/unknown frame counts without needing
// to import the module directly. Not exposed in production builds.

if ((import.meta.env.DEV || import.meta.env.MODE === 'test' || (typeof navigator !== 'undefined' && navigator.webdriver)) && typeof window !== 'undefined') {
  const w = window as unknown as {
    __omnipus_test_hooks?: Record<string, unknown>
  }
  w.__omnipus_test_hooks ??= {}
  w.__omnipus_test_hooks.getDroppedFrameCount = getDroppedFrameCount
  w.__omnipus_test_hooks.resetDroppedFrameCount = resetDroppedFrameCount
  w.__omnipus_test_hooks.getUnknownFrameTypeCount = getUnknownFrameTypeCount
  w.__omnipus_test_hooks.resetUnknownFrameTypeCount = resetUnknownFrameTypeCount
}

// ── Dev-mode toast helper ─────────────────────────────────────────────────────
//
// Thin wrapper over the shared maybeDevToast helper (src/lib/dev-toast.ts).
// Throttling and store interaction are handled there.

function _maybeDevToast(frameType: string, message: string): void {
  void maybeDevToast(message, frameType)
}

// ── safeJsonParse ────────────────────────────────────────────────────────────
//
// Shared JSON-parse helper. Avoids duplicating try/catch in multiple callers.

function safeJsonParse(data: unknown): { ok: true; raw: unknown } | { ok: false } {
  if (typeof data === 'string') {
    try {
      return { ok: true, raw: JSON.parse(data) }
    } catch {
      return { ok: false }
    }
  }
  return { ok: true, raw: data }
}

// ── Client-frame discriminators ───────────────────────────────────────────────
//
// The WsFrame discriminated union includes both client→server and server→client
// frames. An incoming server message that carries a client-direction `type`
// (e.g. a spoofed `{type:"auth"}`) would pass Zod validation (the schema covers
// all directions) but must NOT be forwarded to the SPA frame reducer.
//
// CLIENT_FRAME_TYPES is built from the generated ClientFrameTypes constant
// (contracts/asyncapi.yaml source of truth). The hand-written literal set that
// lived here previously has been deleted — if the spec changes, re-run
// scripts/_gen-asyncapi-types.mjs and ClientFrameTypes auto-updates.

const CLIENT_FRAME_TYPES = new Set<string>(ClientFrameTypes)

// ── parseFrameSafe ────────────────────────────────────────────────────────────
//
// Validates an incoming WebSocket payload against the generated WsFrame Zod
// discriminated-union schema. Returns the typed WsFrame on success, null on
// any failure. Never throws.
//
// Failure modes:
//   - Non-JSON / bad input         → null, _droppedFrameCount++
//   - No/bad `type` field          → null, _droppedFrameCount++
//   - Known type, missing field    → null, _droppedFrameCount++, dev toast
//   - Unknown type                 → null, _droppedFrameCount++
//   - Client→server direction type → null, _droppedFrameCount++
//
// This is the strict public API for callers (including tests) that want
// contract-validated server frames only.

export function parseFrameSafe(data: unknown): WsFrame | null {
  const parsed = safeJsonParse(data)
  if (!parsed.ok) {
    _droppedFrameCount++
    _maybeDevToast('_json_parse', '[ws] Dropped frame: JSON parse error')
    return null
  }
  const raw = parsed.raw

  const frameType =
    raw !== null && typeof raw === 'object' && 'type' in (raw as object)
      ? String((raw as Record<string, unknown>).type)
      : '_unknown'

  const result = WsFrameSchema.safeParse(raw)
  if (result.success) {
    // Zod 3 infers z.unknown() as `unknown`, and `undefined extends unknown`
    // is true, so addQuestionMarks() makes every z.unknown() field optional in
    // the inferred type. The ToolCallResultFrame.result field in asyncapi-types
    // is required (required: [result] in the JSON Schema spec). The cast is safe:
    // the Zod schema IS the contract — if it parsed successfully, the data
    // matches the spec and result was present in the JSON payload.
    return result.data as WsFrame
  }

  _droppedFrameCount++
  const first = result.error.issues[0]
  const desc = first
    ? `${first.path.join('.') || 'root'}: ${first.message}`
    : result.error.message
  _maybeDevToast(frameType, `[ws] Dropped invalid frame (${frameType}): ${desc}`)
  return null
}

// ── _parseServerFrame ─────────────────────────────────────────────────────────
//
// Used internally by WsConnection.onmessage. Strict-by-default: validates via
// parseFrameSafe first. Adds two additional layers:
//
// 1. Client→server direction filter: if the `type` field is a client-only
//    discriminator (e.g. spoofed `{type:"auth"}`), the frame is dropped with
//    _droppedFrameCount even though Zod accepted it.
//
// 2. Forward-compat path: if the `type` field is a non-empty string that is
//    NOT in the known WsFrameType enum AND not a client-direction type, the
//    frame is counted in _unknownFrameTypeCount and dropped (dev toast warns
//    operators that the spec needs updating). This is the only exception to
//    "drop unknown" — it gives us a distinct signal for "future frame" vs
//    "malformed frame" vs "client-frame injection".

function _parseServerFrame(data: unknown): ServerFrame | null {
  const parsed = safeJsonParse(data)
  if (!parsed.ok) {
    _droppedFrameCount++
    return null
  }
  const raw = parsed.raw

  const frameType =
    raw !== null && typeof raw === 'object' && 'type' in (raw as object)
      ? String((raw as Record<string, unknown>).type)
      : '_unknown'

  // Try strict schema validation first.
  const result = WsFrameSchema.safeParse(raw)
  if (result.success) {
    // Direction filter: client→server frames that somehow passed Zod must be
    // dropped. A spoofed `{type:"auth",token:"x"}` is spec-valid but must not
    // reach the SPA reducer.
    if (CLIENT_FRAME_TYPES.has(frameType)) {
      _droppedFrameCount++
      _maybeDevToast(
        frameType,
        `[ws] Dropped client-direction frame received from server (type="${frameType}") — possible injection attempt`,
      )
      return null
    }
    return result.data as ServerFrame
  }

  // Forward-compat: unknown type field from a future server frame not yet in
  // the spec. Count separately so operators can distinguish spec drift from
  // malformed payloads.
  //
  // WsFrameTypeSchema.options is the exhaustive list of known frame type
  // discriminators from the AsyncAPI spec. Any type string that is NOT in
  // this list AND NOT a client-direction type is assumed to be a future
  // server frame — counted separately so operators can track spec drift.
  //
  // IMPORTANT: a frame whose type IS in the spec but that failed Zod
  // validation (e.g. tool_approval_required with args:null) must NOT go
  // to the forward-compat path — it is a known-type validation failure and
  // must increment _droppedFrameCount instead.
  const knownTypes: readonly string[] = WsFrameTypeSchema.options
  const isKnownType = knownTypes.includes(frameType)

  if (frameType !== '_unknown' && !CLIENT_FRAME_TYPES.has(frameType) && !isKnownType) {
    _unknownFrameTypeCount++
    _maybeDevToast(
      frameType,
      `[ws] Dropped unknown-type frame (type="${frameType}") — add to spec if this is a new server frame`,
    )
    return null
  }

  // Known-type validation failure, client-type validation failure, or no
  // type field at all — all go to _droppedFrameCount.
  _droppedFrameCount++
  const first = result.error.issues[0]
  const desc = first
    ? `${first.path.join('.') || 'root'}: ${first.message}`
    : result.error.message
  _maybeDevToast(frameType, `[ws] Dropped invalid frame (${frameType}): ${desc}`)
  return null
}

// ── URL helper ────────────────────────────────────────────────────────────────

const BASE_URL = import.meta.env.VITE_API_URL ?? ''

function getWsUrl(): string {
  const httpBase = BASE_URL || window.location.origin
  const wsBase = httpBase.replace(/^http/, 'ws')
  return `${wsBase}/api/v1/chat/ws`
}

// ── Connection ────────────────────────────────────────────────────────────────

export interface WsConnectionCallbacks { // not-wire-format: SPA-only callback interface passed to WsConnection constructor. Never serialized to or from the gateway — purely internal to the SPA's WebSocket connection manager.
  /** Batch callback: called at most once per rAF with all validated frames. Preferred over onFrame. */
  onFrames?: (frames: ServerFrame[]) => void
  /** Legacy single-frame callback. Used when onFrames is not provided. */
  onFrame?: (frame: ServerFrame) => void
  onConnected: () => void
  onDisconnected: () => void
  onError: (error: string) => void
  /** Reconnect progress. phase=null when connected; gave_up when all retries exhausted. */
  onReconnectStateChange?: (phase: 'reconnecting' | 'slow' | 'gave_up' | null, attempt: number) => void
}

// ── Reconnect schedule constants ──────────────────────────────────────────────
// Fast phase: exponential backoff capped at MAX_FAST_DELAY_MS (10 attempts).
// Slow phase: fixed SLOW_RETRY_DELAY_MS (20 attempts). Then onError + give up.

const MAX_FAST_ATTEMPTS = 10
const MAX_FAST_DELAY_MS = 30_000
const MAX_SLOW_ATTEMPTS = 20
const SLOW_RETRY_DELAY_MS = 60_000

export class WsConnection {
  private ws: WebSocket | null = null
  /** Fast-phase attempt counter — resets on successful connect. */
  private reconnectAttempts = 0
  /** Slow-phase attempt counter — resets on successful connect. */
  private slowRetryAttempts = 0
  /** Whether we are currently in the slow-retry phase. */
  private inSlowPhase = false
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null
  private heartbeatTimer: ReturnType<typeof setInterval> | null = null
  private intentionalClose = false
  private callbacks: WsConnectionCallbacks
  /** Consecutive malformed/invalid frame counter — reset on every valid frame */
  private droppedFrameCount = 0
  private readonly droppedFrameThreshold = 5
  /**
   * Fix 4 — ping-based liveness.
   * Incremented each time the heartbeat fires without a server frame having
   * arrived since the previous ping. Reset to 0 whenever any server frame
   * is received. When this reaches 2, the socket is force-closed to trigger
   * a reconnect rather than waiting for the OS-level TCP timeout.
   */
  private missedPingCount = 0
  /** True when at least one server frame arrived since the last ping sent. */
  private _receivedSinceLastPing = false

  // ── Frame batching ──────────────────────────────────────────────────────────
  // Parsed frames queue into _inboundBatch; a single rAF (or setTimeout fallback
  // when hidden) drains them all in one Zustand update per display frame.

  /** Validated frames waiting for the next rAF/microtask flush. */
  private _inboundBatch: ServerFrame[] = []
  /** True when a rAF/setTimeout flush is already scheduled for this batch. */
  private _rafPending = false

  // ── Web Worker ──────────────────────────────────────────────────────────────
  // Zod parse runs off-main-thread. Falls back to inline parse when Worker
  // construction fails (older Safari, strict CSP, jsdom).

  private _worker: Worker | null = null
  private _workerAvailable = false
  /** Monotonic counter for worker request ids. */
  private _nextWorkerId = 0
  /**
   * Pending worker entries. raw+sentAt are set immediately on postMessage;
   * frame+droppedReason+replied are set when the worker responds.
   */
  private _workerPending: Map<number, { frame: ServerFrame | null; droppedReason?: string; raw: string; sentAt: number; replied: boolean }> = new Map()
  /** Next worker response id we expect to drain (FIFO ordering). */
  private _workerNextExpected = 0

  // Bound event handler references so they can be removed on disconnect.
  private _onVisibilityChange: (() => void) | null = null
  private _onOnline: (() => void) | null = null
  private _onOffline: (() => void) | null = null

  constructor(callbacks: WsConnectionCallbacks) {
    this.callbacks = callbacks
  }

  connect(): void {
    this.intentionalClose = false
    this.reconnectAttempts = 0
    this.slowRetryAttempts = 0
    this.inSlowPhase = false
    this._inboundBatch = []
    this._rafPending = false
    this._nextWorkerId = 0
    this._workerNextExpected = 0
    this._workerPending.clear()
    this._startWorker()
    this._attachWindowListeners()
    this._createSocket()
  }

  disconnect(): void {
    this.intentionalClose = true
    this._clearReconnectTimer()
    this._stopHeartbeat()
    this._detachWindowListeners()
    this._stopWorker()
    // Flush any pending batch before tearing down so callers don't lose frames
    // queued just before disconnect() was called.
    this._flushBatch()
    this.ws?.close(1000, 'User disconnected')
    this.ws = null
  }

  // ── Worker lifecycle ──────────────────────────────────────────────────────

  // One-time flag so the construction-failure log fires exactly once per process.
  private static _workerConstructionFailureLogged = false

  private _startWorker(): void {
    if (this._worker !== null) return

    try {
      // Vite requires `new URL(...)` inlined inside `new Worker(...)` so its
      // static analysis bundles the worker entry point at build time and emits
      // it under dist/spa/assets/ with a content hash for the Go binary embed.
      this._worker = new Worker(
        new URL('../workers/ws-parser.worker.ts', import.meta.url),
        { type: 'module' },
      )
      this._workerAvailable = true

      this._worker.onmessage = (event: MessageEvent) => {
        // eslint-disable-next-line @typescript-eslint/no-unsafe-assignment
        const { id, frame, droppedReason } = event.data as {
          id: number
          frame: ServerFrame | null
          droppedReason?: string
        }
        const pending = this._workerPending.get(id)
        if (pending) {
          pending.frame = frame
          pending.droppedReason = droppedReason
          pending.replied = true
        }
        this._drainWorkerPending()
      }

      this._worker.onerror = () => {
        this._workerAvailable = false
        this._worker = null
      }
    } catch (err) {
      if (!WsConnection._workerConstructionFailureLogged) {
        WsConnection._workerConstructionFailureLogged = true
        console.warn(
          '[ws] Web Worker construction failed — WS frame parsing will run inline on the main thread; this is a performance regression under burst input',
          err,
        )
      }
      this._workerAvailable = false
      this._worker = null
    }
  }

  private _stopWorker(): void {
    if (this._worker) {
      this._worker.terminate()
      this._worker = null
    }
    this._workerAvailable = false
  }

  /** Drain worker responses in FIFO order; fall back to inline parse on 5s timeout. */
  private _drainWorkerPending(): void {
    const WORKER_TIMEOUT_MS = 5_000

    // Check for a stalled head-of-line entry (oldest pending id older than 5s).
    if (this._workerPending.size > 0) {
      const now = Date.now()
      let staleCount = 0
      let oldestAgeMs = 0
      for (const [, entry] of this._workerPending) {
        const age = now - entry.sentAt
        if (age > WORKER_TIMEOUT_MS) {
          staleCount++
          if (age > oldestAgeMs) oldestAgeMs = age
        }
      }
      if (staleCount > 0) {
        console.error(
          '[ws] WS parser worker timeout — falling back to inline parse; pending frames replayed inline',
          { staleCount, oldestAgeMs },
        )
        this._workerAvailable = false
        // Collect raw payloads for all pending entries in id order.
        const pendingIds = Array.from(this._workerPending.keys()).sort((a, b) => a - b)
        const rawsToReplay = pendingIds.map((id) => this._workerPending.get(id)!.raw)
        this._workerPending.clear()
        this._workerNextExpected = this._nextWorkerId
        // Inline-parse each stale raw and push to batch.
        for (const raw of rawsToReplay) {
          const parsed = _parseServerFrame(raw)
          this._onFrameParsed(parsed)
        }
        return
      }
    }

    // Drain only entries that have received a worker reply (replied=true).
    // Entries without a reply yet block the FIFO drain until they arrive.
    while (this._workerPending.has(this._workerNextExpected)) {
      const resp = this._workerPending.get(this._workerNextExpected)!
      if (!resp.replied) break
      this._workerPending.delete(this._workerNextExpected)
      this._workerNextExpected++
      this._onFrameParsed(resp.frame, resp.droppedReason)
    }
  }

  // ── Batch flush ───────────────────────────────────────────────────────────

  /** Enqueue a parsed frame into the batch (or count the drop). */
  private _onFrameParsed(frame: ServerFrame | null, droppedReason?: string): void {
    if (frame === null) {
      this.droppedFrameCount++
      if (droppedReason) {
        _maybeDevToast('_worker_drop', `[ws] ${droppedReason}`)
      }
      if (this.droppedFrameCount >= this.droppedFrameThreshold) {
        this.callbacks.onError(
          `Received ${this.droppedFrameCount} invalid frames in a row — the connection may be unstable.`,
        )
      }
      return
    }
    // Valid frame — reset consecutive drop counter.
    this.droppedFrameCount = 0

    // Pong is transport-layer only: it already set _receivedSinceLastPing /
    // missedPingCount in the synchronous onmessage path (before the worker
    // path). Do not enqueue it; surfacing it would trip the unknown-frame toast
    // in chatStore.handleFrame.
    if (frame.type === 'pong') return

    this._inboundBatch.push(frame)
    this._scheduleFlush()
  }

  /** Schedule the next rAF/setTimeout flush. Idempotent. */
  private _scheduleFlush(): void {
    if (this._rafPending) return
    this._rafPending = true

    if (typeof requestAnimationFrame !== 'undefined' && document.visibilityState !== 'hidden') {
      requestAnimationFrame(() => this._flushBatch())
    } else {
      // Tab is hidden OR rAF not available (jsdom / node). Use setTimeout(0)
      // as a near-zero-delay microtask equivalent that still drains the batch.
      setTimeout(() => this._flushBatch(), 0)
    }
  }

  /** Drain the entire inbound batch in one call. */
  private _flushBatch(): void {
    this._rafPending = false
    if (this._inboundBatch.length === 0) return

    const batch = this._inboundBatch
    this._inboundBatch = []

    if (this.callbacks.onFrames) {
      this.callbacks.onFrames(batch)
    } else if (this.callbacks.onFrame) {
      for (const frame of batch) {
        this.callbacks.onFrame(frame)
      }
    }
  }

  send(frame: ClientFrame): boolean {
    if (this.ws?.readyState === WebSocket.OPEN) {
      try {
        this.ws.send(JSON.stringify(frame))
        return true
      } catch (err) {
        // An OPEN socket can still throw on send (broken pipe, send-buffer full,
        // abrupt teardown). Treat it as a failed send and return false so callers
        // — notably chat sendMessage — run the no-loss recovery path (keep the
        // user message, mark it error, offer Retry) instead of believing the
        // frame was delivered. Without this, a throw here silently loses the turn.
        // See #253. Surfaced via console (not onError) to avoid double-toasting:
        // the caller already shows the user-facing "could not send" state on false.
        console.error(
          `WebSocket send failed on OPEN socket: ${err instanceof Error ? err.message : String(err)}`,
        )
        return false
      }
    }
    return false
  }

  get isConnected(): boolean {
    return this.ws?.readyState === WebSocket.OPEN
  }

  /** Current attempt count within the active reconnect phase — exposed for tests and UI state. */
  get currentReconnectAttempt(): number {
    return this.inSlowPhase ? this.slowRetryAttempts : this.reconnectAttempts
  }

  private _createSocket(): void {
    try {
      this.ws = new WebSocket(getWsUrl())
    } catch (err) {
      this.callbacks.onError(`Failed to create WebSocket: ${err instanceof Error ? err.message : String(err)}`)
      return
    }

    // Expose live WebSocket on window so tests can deterministically simulate a
    // network drop by calling ws.close() — there is no other reliable hook for
    // closing only the SPA's WS without disabling the entire network context.
    // See tests/e2e/ws-reconnect.spec.ts. Also enabled when navigator.webdriver
    // is true (Playwright automation) so E2E tests against production builds work.
    if (typeof window !== 'undefined' && (import.meta.env.DEV || import.meta.env.MODE === 'test' || (typeof navigator !== 'undefined' && navigator.webdriver))) {
      const w = window as unknown as { __ws_instances?: WebSocket[] }
      w.__ws_instances ??= []
      w.__ws_instances.push(this.ws)
    }

    this.ws.onopen = () => {
      this.reconnectAttempts = 0
      this.slowRetryAttempts = 0
      this.inSlowPhase = false
      this.missedPingCount = 0
      this._receivedSinceLastPing = false
      // Notify UI that we are now connected (phase=null, attempt=0).
      this.callbacks.onReconnectStateChange?.(null, 0)
      // Auth token is re-read on every (re-)connect so changes after
      // initial load take effect without a page refresh.
      // Check sessionStorage first (XSS protection), fall back to localStorage.
      const token = sessionStorage.getItem('omnipus_auth_token') ?? localStorage.getItem('omnipus_auth_token')
      if (token) {
        this.send({ type: 'auth', token })
      } else {
        console.warn('[ws] No auth token found — connecting unauthenticated')
      }
      this._startHeartbeat()
      this.callbacks.onConnected()
    }

    this.ws.onmessage = (event: MessageEvent) => {
      const raw = event.data as string

      // ── Synchronous liveness sniff ────────────────────────────────────────────
      // Update heartbeat state before the worker round-trip. Intercept pong here
      // so it never enters the batch (transport-layer only).
      let maybePong = false
      try {
        const sniff = JSON.parse(raw) as { type?: string }
        if (sniff && typeof sniff.type === 'string') {
          // Any parseable frame with a type field counts as "alive".
          this._receivedSinceLastPing = true
          this.missedPingCount = 0
          if (sniff.type === 'pong') maybePong = true
        }
      } catch {
        // Not JSON — will be caught and dropped during full parse below.
      }
      if (maybePong) return

      // ── Parse path (worker or inline) ────────────────────────────────────────
      if (this._workerAvailable && this._worker) {
        const id = this._nextWorkerId++
        this._workerPending.set(id, { frame: null, raw, sentAt: Date.now(), replied: false })
        this._worker.postMessage({ id, raw })
      } else {
        // Inline fallback: synchronous parse on the main thread.
        const parsed = _parseServerFrame(raw)
        this._onFrameParsed(parsed)
      }
    }

    this.ws.onerror = () => {
      // onerror fires before onclose; onclose produces a richer message with
      // close code and reason. We emit a minimal diagnostic here so the
      // connection-error banner has a message, but avoid duplicating the
      // onclose banner message.
      this.callbacks.onError(`Connection error reaching ${getWsUrl()} — will retry`)
    }

    this.ws.onclose = (event: CloseEvent) => {
      // Drop this socket from the test-visible registry before nulling.
      if (typeof window !== 'undefined' && (import.meta.env.DEV || import.meta.env.MODE === 'test' || (typeof navigator !== 'undefined' && navigator.webdriver))) {
        const w = window as unknown as { __ws_instances?: WebSocket[] }
        if (w.__ws_instances && this.ws) {
          const idx = w.__ws_instances.indexOf(this.ws)
          if (idx >= 0) w.__ws_instances.splice(idx, 1)
        }
      }

      // Flush frames already queued before the drop so callers don't lose them.
      // Chat reducers are idempotent on replay, so flushing on unintentional
      // close is safe. Also clear any in-flight worker entries whose responses
      // will never arrive so they don't pile up against the next connection.
      this._flushBatch()
      this._workerPending.clear()
      this._workerNextExpected = this._nextWorkerId

      this.ws = null
      this._stopHeartbeat()
      this.callbacks.onDisconnected()
      // Any non-intentional close should reconnect. The persistent banner is
      // driven by isConnected=false in the connection store (set by
      // onDisconnected above) — ChatScreen renders the reconnect-banner div
      // for the entire disconnected interval. We surface a richer onError
      // message for unexpected close codes (≠ 1000 / 1001) so the user sees a
      // diagnostic toast as well; for clean-but-unintentional 1000/1001 the
      // banner alone is sufficient.
      if (!this.intentionalClose) {
        if (event.code !== 1000 && event.code !== 1001) {
          const codeLabel = event.code ? ` code ${event.code}` : ''
          const reasonLabel = event.reason ? `: ${event.reason}` : ''
          this.callbacks.onError(
            `Disconnected from gateway —${codeLabel}${reasonLabel || ' connection lost'}. Reconnecting…`
          )
        }
        this._scheduleReconnect()
      }
    }
  }

  private _scheduleReconnect(): void {
    // ── Slow-phase exhausted → give up ────────────────────────────────────────
    if (this.inSlowPhase && this.slowRetryAttempts >= MAX_SLOW_ATTEMPTS) {
      this.callbacks.onReconnectStateChange?.('gave_up', this.slowRetryAttempts)
      this.callbacks.onError(
        `Connection lost after ${MAX_FAST_ATTEMPTS + MAX_SLOW_ATTEMPTS} attempts. Click "Reconnect now" to try again.`
      )
      return
    }

    // ── Fast-phase exhausted → transition to slow phase ───────────────────────
    if (!this.inSlowPhase && this.reconnectAttempts >= MAX_FAST_ATTEMPTS) {
      this.inSlowPhase = true
      this.slowRetryAttempts = 0
    }

    if (this.inSlowPhase) {
      this.slowRetryAttempts++
      this.callbacks.onReconnectStateChange?.('slow', this.slowRetryAttempts)
      this.reconnectTimer = setTimeout(() => {
        this._createSocket()
      }, SLOW_RETRY_DELAY_MS)
      return
    }

    // ── Fast phase: exponential backoff capped at MAX_FAST_DELAY_MS ───────────
    const delay = Math.min(Math.pow(2, this.reconnectAttempts) * 1000, MAX_FAST_DELAY_MS)
    this.reconnectAttempts++
    this.callbacks.onReconnectStateChange?.('reconnecting', this.reconnectAttempts)

    this.reconnectTimer = setTimeout(() => {
      this._createSocket()
    }, delay)
  }

  private _clearReconnectTimer(): void {
    if (this.reconnectTimer !== null) {
      clearTimeout(this.reconnectTimer)
      this.reconnectTimer = null
    }
  }

  // Attach window-level listeners that trigger reconnect on
  // visibilitychange (tab re-focused after backgrounding) and online (network
  // recovered). Both fire a reconnect immediately if in disconnected state,
  // resetting the backoff counter so the next attempt is fast.
  private _attachWindowListeners(): void {
    if (this._onVisibilityChange) return // already attached

    this._onVisibilityChange = () => {
      if (
        document.visibilityState === 'visible' &&
        !this.intentionalClose &&
        this.ws === null
      ) {
        // Reset backoff — user is actively looking at the page. Schedule
        // through the existing reconnect timer with a short delay so the
        // disconnected banner is visible at least one render cycle even
        // when the new socket connects in <50ms (localhost / LAN). This
        // avoids the banner flicker that would otherwise be invisible.
        this.reconnectAttempts = 0
        this.slowRetryAttempts = 0
        this.inSlowPhase = false
        this._clearReconnectTimer()
        this.reconnectTimer = setTimeout(() => this._createSocket(), 250)
      }
    }

    this._onOnline = () => {
      if (!this.intentionalClose && this.ws === null) {
        // Network recovered — reset backoff and reconnect with the same
        // 250ms minimum delay as visibilitychange to keep the banner
        // observable on fast networks.
        this.reconnectAttempts = 0
        this.slowRetryAttempts = 0
        this.inSlowPhase = false
        this._clearReconnectTimer()
        this.reconnectTimer = setTimeout(() => this._createSocket(), 250)
      }
    }

    // The browser's WebSocket close handler does not always fire promptly when
    // the underlying network drops. Listen for the offline event and force-close
    // so onclose fires synchronously and the UI flips to disconnected.
    this._onOffline = () => {
      if (this.ws && this.ws.readyState !== WebSocket.CLOSED) {
        try {
          this.ws.close(1000, 'offline')
        } catch {
          // ignore — onclose will run regardless
        }
      }
    }

    document.addEventListener('visibilitychange', this._onVisibilityChange)
    window.addEventListener('online', this._onOnline)
    window.addEventListener('offline', this._onOffline)
  }

  private _detachWindowListeners(): void {
    if (this._onVisibilityChange) {
      document.removeEventListener('visibilitychange', this._onVisibilityChange)
      this._onVisibilityChange = null
    }
    if (this._onOnline) {
      window.removeEventListener('online', this._onOnline)
      this._onOnline = null
    }
    if (this._onOffline) {
      window.removeEventListener('offline', this._onOffline)
      this._onOffline = null
    }
  }

  private _startHeartbeat(): void {
    this._stopHeartbeat()
    // Send a ping every 30s to keep the connection alive through proxies and firewalls.
    // Fix 4 — ping-based liveness: if 2 consecutive pings elapse with no server frame
    // received in between, the connection is silently dead (e.g. NAT timeout, Chrome
    // background throttling dropped the OS-level TCP keepalive). Force-close the socket
    // so onclose fires and _scheduleReconnect takes over — this is far faster than
    // waiting for the OS-level TCP timeout (which can take minutes).
    this.heartbeatTimer = setInterval(() => {
      if (this.ws?.readyState === WebSocket.OPEN) {
        if (!this._receivedSinceLastPing) {
          this.missedPingCount++
          if (this.missedPingCount >= 2) {
            // No server frame in 60s — force close to trigger reconnect.
            console.warn(
              `[ws] ${this.missedPingCount} consecutive pings with no server response — forcing reconnect`
            )
            try {
              this.ws.close(1006, 'ping timeout')
            } catch {
              // ignore — onclose will fire regardless
            }
            return
          }
        } else {
          this.missedPingCount = 0
        }
        this._receivedSinceLastPing = false
        this.ws.send(JSON.stringify({ type: 'ping' }))
      }
    }, 30_000)
  }

  private _stopHeartbeat(): void {
    if (this.heartbeatTimer !== null) {
      clearInterval(this.heartbeatTimer)
      this.heartbeatTimer = null
    }
  }
}

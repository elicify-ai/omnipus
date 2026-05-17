// WebSocket connection manager for /api/v1/chat/ws
// Handles: connect, authenticate, streaming frames, reconnect with exponential backoff

// ── Generated type imports ────────────────────────────────────────────────────
//
// All wire-format frame types are sourced from the generated AsyncAPI types.
// Hand-written interface declarations for wire-format frames are FORBIDDEN —
// see CLAUDE.md hard-constraint #8. This file re-exports and aliases only.

import { WsFrame as WsFrameSchema, WsFrameType as WsFrameTypeSchema } from '@/lib/api/generated/schemas'
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
} from '@/lib/api/generated/asyncapi-types'

// Re-export canonical names from generated file
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
}

// ── WsXxx legacy aliases ──────────────────────────────────────────────────────
//
// Existing consumers (stores, components, tests) use Ws-prefixed type names.
// These aliases let them keep their current imports. New code should use the
// canonical names above.

export type WsAuthFrame = AuthFrame
export type WsMessageFrame = MessageFrame
export type WsCancelFrame = CancelFrame
export type WsExecApprovalResponseFrame = ExecApprovalResponseFrame
export type WsPingFrame = PingFrame
export type WsAttachSessionFrame = AttachSessionFrame
export type WsDevicePairingResponseFrame = DevicePairingResponseFrame

export type WsSessionStartedFrame = SessionStartedFrame
export type WsTokenFrame = TokenFrame
export type WsDoneFrame = DoneFrame
export type WsErrorFrame = ErrorFrame
export type WsToolCallStartFrame = ToolCallStartFrame
export type WsToolCallResultFrame = ToolCallResultFrame
export type WsSubagentStartFrame = SubagentStartFrame
export type WsSubagentEndFrame = SubagentEndFrame
export type WsExecApprovalRequestFrame = ExecApprovalRequestFrame
export type WsTaskStatusChangedFrame = TaskStatusChangedFrame
export type WsReplayMessageFrame = ReplayMessageFrame
export type WsRateLimitFrame = RateLimitFrame
export type WsMediaFrame = MediaFrame
export type WsMediaPart = MediaPart
export type WsAgentSwitchedFrame = AgentSwitchedFrame
export type WsToolApprovalRequiredFrame = ToolApprovalRequiredFrame
export type WsSessionStatePendingApproval = SessionStatePendingApproval
export type WsSessionStateFrame = SessionStateFrame
export type WsSystemOverloadFrame = SystemOverloadFrame
export type WsReplayWarningFrame = ReplayWarningFrame
export type WsCancelStageFrame = CancelStageFrame
export type WsSessionCloseAckFrame = SessionCloseAckFrame
export type WsExecApprovalResponseAckFrame = ExecApprovalResponseAckFrame
export type WsDevicePairingRequestFrame = DevicePairingRequestFrame

// WsSendFrame: union of all client→server frames.
export type WsSendFrame = ClientFrame

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

if ((import.meta.env.DEV || import.meta.env.MODE === 'test') && typeof window !== 'undefined') {
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
// Throttled: at most one toast per frame type per second to avoid flooding the
// UI when a burst of malformed frames arrives.

const _toastThrottleMap: Record<string, number> = {}
const TOAST_THROTTLE_MS = 1000

function _maybeDevToast(frameType: string, message: string): void {
  if (!import.meta.env.DEV) return
  const now = Date.now()
  if (now - (_toastThrottleMap[frameType] ?? 0) < TOAST_THROTTLE_MS) return
  _toastThrottleMap[frameType] = now
  try {
    // Zustand stores expose getState() for non-React callers.
    // Dynamic require avoids circular-dep issues at module init time.
    // eslint-disable-next-line @typescript-eslint/no-require-imports
    const { useUiStore } = require('@/store/ui') as {
      useUiStore: { getState: () => { addToast: (t: { message: string; variant: 'warning' }) => void } }
    }
    useUiStore.getState().addToast({ message, variant: 'warning' })
  } catch {
    console.warn('[ws]', message)
  }
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
// CLIENT_FRAME_TYPES is the set of `type` values exclusively used by
// client→server frames. These are derived from the ClientFrame union in
// asyncapi-types.ts — kept in sync with the spec by construction.

const CLIENT_FRAME_TYPES = new Set<string>([
  'auth',
  'message',
  'cancel',
  'exec_approval_response',
  'ping',
  'attach_session',
  'device_pairing_response',
])

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

export interface WsConnectionCallbacks {
  onFrame: (frame: ServerFrame) => void
  onConnected: () => void
  onDisconnected: () => void
  onError: (error: string) => void
}

export class WsConnection {
  private ws: WebSocket | null = null
  private reconnectAttempts = 0
  private maxReconnectAttempts = 3
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null
  private heartbeatTimer: ReturnType<typeof setInterval> | null = null
  private intentionalClose = false
  private callbacks: WsConnectionCallbacks
  /** Consecutive malformed/invalid frame counter — reset on every valid frame */
  private droppedFrameCount = 0
  private readonly droppedFrameThreshold = 5

  // B1.3c: bound event handler references so they can be removed on disconnect.
  private _onVisibilityChange: (() => void) | null = null
  private _onOnline: (() => void) | null = null
  private _onOffline: (() => void) | null = null

  constructor(callbacks: WsConnectionCallbacks) {
    this.callbacks = callbacks
  }

  connect(): void {
    this.intentionalClose = false
    this.reconnectAttempts = 0
    this._attachWindowListeners()
    this._createSocket()
  }

  disconnect(): void {
    this.intentionalClose = true
    this._clearReconnectTimer()
    this._stopHeartbeat()
    this._detachWindowListeners()
    this.ws?.close(1000, 'User disconnected')
    this.ws = null
  }

  send(frame: ClientFrame): boolean {
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify(frame))
      return true
    }
    this.callbacks.onError('Not connected — message not sent')
    return false
  }

  get isConnected(): boolean {
    return this.ws?.readyState === WebSocket.OPEN
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
    // See tests/e2e/ws-reconnect.spec.ts.
    if (typeof window !== 'undefined') {
      const w = window as unknown as { __ws_instances?: WebSocket[] }
      w.__ws_instances ??= []
      w.__ws_instances.push(this.ws)
    }

    this.ws.onopen = () => {
      this.reconnectAttempts = 0
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
      const parsed = _parseServerFrame(event.data as string)
      if (parsed === null) {
        this.droppedFrameCount++
        if (this.droppedFrameCount >= this.droppedFrameThreshold) {
          this.callbacks.onError(
            `Received ${this.droppedFrameCount} invalid frames in a row — the connection may be unstable.`,
          )
        }
        return
      }
      // Valid frame received — reset the consecutive drop counter
      this.droppedFrameCount = 0
      this.callbacks.onFrame(parsed)
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
      if (typeof window !== 'undefined') {
        const w = window as unknown as { __ws_instances?: WebSocket[] }
        if (w.__ws_instances && this.ws) {
          const idx = w.__ws_instances.indexOf(this.ws)
          if (idx >= 0) w.__ws_instances.splice(idx, 1)
        }
      }
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
    if (this.reconnectAttempts >= this.maxReconnectAttempts) {
      this.callbacks.onError('Connection failed after 3 attempts. Click retry to reconnect.')
      return
    }

    const delay = Math.pow(2, this.reconnectAttempts) * 1000 // 1s, 2s, 4s
    this.reconnectAttempts++

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

  // B1.3c: attach window-level listeners that trigger reconnect on
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
    // Send a ping every 30s to keep the connection alive through proxies and firewalls
    this.heartbeatTimer = setInterval(() => {
      if (this.ws?.readyState === WebSocket.OPEN) {
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

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

import { WsFrame as WsFrameSchema } from '@/lib/api/generated/schemas'
import type {
  BrowserAttachFrame,
  BrowserControlFrame,
  BrowserDetachFrame,
  BrowserInputFrame,
  BrowserScreencastFrame,
  BrowserStatusFrame,
  BrowserTabActionFrame,
  BrowserTabsFrame,
  ErrorFrame,
} from '@/lib/api/generated/asyncapi-types'

/** Frame types this socket ever receives — a narrow slice of the full WsFrame union. */
type BrowserServerFrame = BrowserScreencastFrame | BrowserStatusFrame | BrowserTabsFrame | ErrorFrame

export interface BrowserLiveWsCallbacks { // not-wire-format: SPA-only callback interface passed to BrowserLiveWsConnection's constructor. Never serialized to or from the gateway.
  onScreencast: (frame: BrowserScreencastFrame) => void
  onStatus: (frame: BrowserStatusFrame) => void
  /** ADR-041 D4 — the current tab list + active index, broadcast on any tab open/close/switch/title-change. */
  onTabs: (frame: BrowserTabsFrame) => void
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
  if (!result.success) return null

  const frame = result.data
  if (
    frame.type === 'browser_screencast' ||
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
      const token = sessionStorage.getItem('omnipus_auth_token') ?? localStorage.getItem('omnipus_auth_token')
      if (!token) {
        this.callbacks.onError('No auth token found — cannot open the live browser view.')
        ws.close(1000, 'no auth token')
        return
      }
      this._rawSend({ type: 'auth', token })
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
      if (frame.type === 'browser_screencast') {
        this.callbacks.onScreencast(frame)
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
   * `browser_attach`/`detach()`, so the backend can route the action even if
   * this connection is ever multiplexed across sessions.
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

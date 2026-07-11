// BrowserLiveView — shared live-view core for the ADR-038 interactive
// browser panel. Rendered by two callers:
//   1. BrowserLivePanel.tsx  — inside the app-root Sheet overlay
//   2. routes/_app/browser-live.tsx — the fullscreen pop-out window
//
// Owns the second WS connection (browserLiveWs.ts), the latest screencast
// frame, the take/release control toggle, and pointer/keyboard capture while
// controlling. Deliberately has NO knowledge of how it's being hosted (Sheet
// vs. fullscreen route) — onPopOut/onClose are optional callbacks so each
// host wires up its own chrome semantics (window.open vs. store close vs.
// window.close).

import { useCallback, useEffect, useRef, useState } from 'react'
import {
  ArrowSquareOut,
  Cursor,
  HandGrabbing,
  HandPalm,
  Monitor,
  SpinnerGap,
  WarningCircle,
  X,
} from '@phosphor-icons/react'
import { cn } from '@/lib/utils'
import { BrowserLiveWsConnection } from '@/lib/browserLiveWs'
import { computeModifiers, isPrintableKey, mapClientToDevice, mapMouseButton } from '@/lib/browserLiveCoords'
import type { BrowserScreencastFrame, BrowserStatusFrame } from '@/lib/api/generated/asyncapi-types'

export interface BrowserLiveViewProps {
  sessionId: string
  agentId: string
  /** Rendered as a header "Pop out" button when provided. */
  onPopOut?: () => void
  /** Rendered as a header "Close" button when provided. */
  onClose?: () => void
  className?: string
}

type PillConfig = { label: string; className: string }

// Local-only pill states layered on top of the wire `BrowserStatusFrame.state`
// enum: 'connecting' (never attached yet) and 'disconnected' (was attached,
// the WS transport dropped, a reconnect is in flight) both describe SPA
// connection lifecycle, not anything the backend ever sends as a status.
type LiveStatus = BrowserStatusFrame['state'] | 'connecting' | 'disconnected'

function pillConfig(state: LiveStatus): PillConfig {
  switch (state) {
    case 'controlling':
      return { label: "You're driving", className: 'bg-[var(--color-accent)]/15 text-[var(--color-accent)]' }
    case 'error':
      return { label: 'Error', className: 'bg-[var(--color-error)]/15 text-[var(--color-error)]' }
    case 'detached':
      return { label: 'Detached', className: 'bg-[var(--color-surface-3)] text-[var(--color-muted)]' }
    case 'connecting':
      return { label: 'Connecting…', className: 'bg-[var(--color-surface-3)] text-[var(--color-muted)]' }
    case 'disconnected':
      return { label: 'Reconnecting…', className: 'bg-[var(--color-surface-3)] text-[var(--color-muted)]' }
    default:
      // 'attached' | 'idle' | 'released' — no human holds the control-lock.
      return { label: 'Agent driving', className: 'bg-[var(--color-surface-3)] text-[var(--color-secondary)]' }
  }
}

export function BrowserLiveView({ sessionId, agentId, onPopOut, onClose, className }: BrowserLiveViewProps) {
  const wsRef = useRef<BrowserLiveWsConnection | null>(null)
  const containerRef = useRef<HTMLDivElement | null>(null)
  const frameRef = useRef<BrowserScreencastFrame | null>(null)
  const controllingRef = useRef(false)
  // mouse_move RAF-coalescing (see handlePointerMove below): only the latest
  // pointer position per animation frame is ever sent, so the highest rate
  // this can flood the server's input rate limiter at is one frame's worth
  // of paint cadence — never the native pointermove rate (60-120Hz+, higher
  // on gaming mice/some trackpads).
  const pendingMoveRef = useRef<{ x: number; y: number; modifiers: number } | null>(null)
  const moveFlushScheduledRef = useRef(false)

  const [frame, setFrame] = useState<BrowserScreencastFrame | null>(null)
  const [statusState, setStatusState] = useState<LiveStatus>('connecting')
  // The human-readable text carried on the latest browser_status frame (set
  // whenever state === 'error' — already-controlled, take-control-disabled,
  // no-manager-for-agent, live-view-disabled, malformed control, etc.).
  // Distinct from connError, which is transport-level (WS create/send/auth
  // failures) rather than a semantic status the backend reported.
  const [statusMessage, setStatusMessage] = useState<string | null>(null)
  const [connError, setConnError] = useState<string | null>(null)
  const [connected, setConnected] = useState(false)
  const [cursorPos, setCursorPos] = useState<{ x: number; y: number } | null>(null)

  const isControlling = statusState === 'controlling'
  // Unified error surface: a transport-level error always wins; otherwise a
  // terminal browser_status{state:'error'} frame's message is shown. This is
  // what actually renders (both before and after the first frame arrives) —
  // see the "!frame" branch and the persistent error strip below.
  const displayError = connError ?? (statusState === 'error' ? statusMessage ?? 'The live browser session reported an error.' : null)

  useEffect(() => {
    frameRef.current = frame
  }, [frame])
  useEffect(() => {
    controllingRef.current = isControlling
    // Losing control (agent takes it back / released) clears the stale
    // cursor overlay rather than leaving it frozen at the last position.
    if (!isControlling) setCursorPos(null)
  }, [isControlling])

  // ── WS lifecycle — one connection per mount (host keys this component by
  // `${sessionId}:${agentId}` so a new target always gets a fresh mount). ──
  useEffect(() => {
    const conn = new BrowserLiveWsConnection(sessionId, agentId, {
      onScreencast: (f) => setFrame(f),
      onStatus: (f) => {
        setStatusState(f.state)
        setStatusMessage(f.message ?? null)
      },
      onError: (message) => setConnError(message),
      onConnected: () => {
        setConnected(true)
        setConnError(null)
      },
      onDisconnected: () => {
        setConnected(false)
        // The control-lock is server-side and per-connection — once the
        // transport drops, whatever control state we last knew is stale (the
        // human is no longer "driving" anything). Move to the local
        // 'disconnected' pill state so the UI stops claiming control is
        // held, the synthetic cursor clears (via the isControlling effect
        // below), and every pointer/keyboard/wheel handler's `controllingRef`
        // guard starts short-circuiting for the whole reconnect window —
        // re-establishing control requires an explicit take-control action
        // once the fresh browser_attach → browser_status round-trip lands.
        setStatusState('disconnected')
        pendingMoveRef.current = null
      },
    })
    wsRef.current = conn
    conn.connect()
    return () => {
      conn.detach()
      conn.close()
      wsRef.current = null
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionId, agentId])

  // ── Native (non-passive) wheel listener — React's synthetic onWheel is
  // passive by default, so preventDefault() inside a JSX handler would warn
  // and no-op. Attached once; reads live state via refs to avoid re-binding
  // on every incoming frame. ──
  useEffect(() => {
    const el = containerRef.current
    if (!el) return undefined
    function onWheel(e: WheelEvent) {
      if (!controllingRef.current || !frameRef.current) return
      e.preventDefault()
      const rect = el!.getBoundingClientRect()
      const device = mapClientToDevice(
        e.clientX,
        e.clientY,
        rect,
        frameRef.current.width,
        frameRef.current.height,
        frameRef.current.page_scale,
      )
      if (!device) return
      wsRef.current?.sendInput({
        kind: 'wheel',
        x: device.x,
        y: device.y,
        delta_x: e.deltaX,
        delta_y: e.deltaY,
        modifiers: computeModifiers(e),
      })
    }
    el.addEventListener('wheel', onWheel, { passive: false })
    return () => el.removeEventListener('wheel', onWheel)
  }, [frame !== null])

  // ── mouse_move RAF coalescing ────────────────────────────────────────────
  // Native pointermove fires far faster than the backend's input rate limiter
  // (50 events/sec) can absorb — a 120Hz+ mouse/trackpad would flood it,
  // causing silent server-side drops and a janky remote cursor. Coalesce to
  // "at most one send per animation frame": every handlePointerMove call
  // overwrites pendingMoveRef with the latest position; a single scheduled
  // flush (idempotent — moveFlushScheduledRef guards re-scheduling) drains
  // whatever is pending when the frame/timer fires. Mirrors the rAF-or-
  // setTimeout(0) fallback ws.ts already uses for inbound batching (rAF is
  // unavailable in jsdom/node and a hidden tab never fires it).
  const flushPendingMove = useCallback(() => {
    moveFlushScheduledRef.current = false
    const pending = pendingMoveRef.current
    if (!pending) return
    pendingMoveRef.current = null
    wsRef.current?.sendInput({ kind: 'mouse_move', x: pending.x, y: pending.y, modifiers: pending.modifiers })
  }, [])

  const scheduleMoveFlush = useCallback(() => {
    if (moveFlushScheduledRef.current) return
    moveFlushScheduledRef.current = true
    if (typeof requestAnimationFrame !== 'undefined' && document.visibilityState !== 'hidden') {
      requestAnimationFrame(flushPendingMove)
    } else {
      setTimeout(flushPendingMove, 0)
    }
  }, [flushPendingMove])

  // Cancel any in-flight coalesced move on unmount — nothing to flush once
  // the socket is about to be closed/detached by the WS lifecycle effect.
  useEffect(() => {
    return () => {
      moveFlushScheduledRef.current = false
      pendingMoveRef.current = null
    }
  }, [])

  const handleToggleControl = useCallback(() => {
    if (!wsRef.current) return
    wsRef.current.sendControl(isControlling ? 'release' : 'take')
  }, [isControlling])

  const handlePointerMove = useCallback((e: React.PointerEvent<HTMLDivElement>) => {
    if (!controllingRef.current || !frameRef.current || !containerRef.current) return
    const rect = containerRef.current.getBoundingClientRect()
    // Local cursor overlay updates immediately every event — only the
    // network send is throttled, so the synthetic cursor still tracks the
    // pointer at full native resolution.
    setCursorPos({ x: e.clientX - rect.left, y: e.clientY - rect.top })
    const device = mapClientToDevice(
      e.clientX,
      e.clientY,
      rect,
      frameRef.current.width,
      frameRef.current.height,
      frameRef.current.page_scale,
    )
    if (!device) return
    pendingMoveRef.current = { x: device.x, y: device.y, modifiers: computeModifiers(e) }
    scheduleMoveFlush()
  }, [scheduleMoveFlush])

  const handlePointerDown = useCallback((e: React.PointerEvent<HTMLDivElement>) => {
    if (!controllingRef.current || !frameRef.current || !containerRef.current) return
    containerRef.current.focus()
    // Capture the pointer so a drag that leaves the container bounds (e.g.
    // a fast selection or slider drag) still delivers move/up events here —
    // without this, releasing outside the frame would leave the remote page
    // thinking the mouse button is still held down.
    try {
      e.currentTarget.setPointerCapture(e.pointerId)
    } catch {
      // Pointer capture is best-effort — unsupported/jsdom environments fall
      // back to normal bounds-limited pointer events.
    }
    const rect = containerRef.current.getBoundingClientRect()
    const device = mapClientToDevice(
      e.clientX,
      e.clientY,
      rect,
      frameRef.current.width,
      frameRef.current.height,
      frameRef.current.page_scale,
    )
    if (!device) return
    wsRef.current?.sendInput({
      kind: 'mouse_down',
      x: device.x,
      y: device.y,
      button: mapMouseButton(e.button),
      modifiers: computeModifiers(e),
    })
  }, [])

  const handlePointerUp = useCallback((e: React.PointerEvent<HTMLDivElement>) => {
    if (!controllingRef.current || !frameRef.current || !containerRef.current) return
    const rect = containerRef.current.getBoundingClientRect()
    const device = mapClientToDevice(
      e.clientX,
      e.clientY,
      rect,
      frameRef.current.width,
      frameRef.current.height,
      frameRef.current.page_scale,
    )
    if (!device) return
    wsRef.current?.sendInput({
      kind: 'mouse_up',
      x: device.x,
      y: device.y,
      button: mapMouseButton(e.button),
      modifiers: computeModifiers(e),
    })
  }, [])

  const handleKeyDown = useCallback((e: React.KeyboardEvent<HTMLDivElement>) => {
    if (!controllingRef.current) return
    // Escape is deliberately NOT forwarded and NOT preventDefault()'d: Radix's
    // Dialog/Sheet listens for it via a capture-phase document listener
    // (fires before this bubble-phase handler ever runs — no event-handler
    // trick on this element can outrun it), so when hosted in the Sheet
    // overlay, Escape always closes the panel first regardless. Treating
    // Escape as "exit local control" rather than fighting that is also the
    // conventional behaviour for remote-control/remote-desktop widgets.
    if (e.key === 'Escape') return
    e.preventDefault()
    const modifiers = computeModifiers(e)
    if (isPrintableKey(e)) {
      wsRef.current?.sendInput({ kind: 'text', text: e.key, modifiers })
    } else {
      wsRef.current?.sendInput({ kind: 'key_down', key: e.key, code: e.code, modifiers })
    }
  }, [])

  const handleKeyUp = useCallback((e: React.KeyboardEvent<HTMLDivElement>) => {
    if (!controllingRef.current) return
    if (e.key === 'Escape') return
    e.preventDefault()
    // 'text' input is a one-shot insert (no matching key_up — mirrors
    // Input.insertText on the backend, which has no down/up phase).
    if (!isPrintableKey(e)) {
      wsRef.current?.sendInput({ kind: 'key_up', key: e.key, code: e.code, modifiers: computeModifiers(e) })
    }
  }, [])

  const pill = pillConfig(connError ? 'error' : statusState)

  return (
    <div className={cn('flex h-full min-h-0 flex-col bg-[var(--color-primary)]', className)}>
      {/* Header. pr-14 (not just px-4) reserves room past the row's own buttons:
          when hosted in the Sheet overlay (BrowserLivePanel), Radix renders its
          own built-in close (absolute right-2 top-2, h-11 w-11) on top of
          whatever sits underneath — without this reserved gap the rightmost
          header button here would sit directly under it. */}
      <div className="flex shrink-0 items-center gap-2 border-b border-[var(--color-border)] pl-4 pr-14 py-3">
        <Monitor size={16} weight="duotone" className="text-[var(--color-accent)]" />
        <h2 className="font-headline text-sm font-semibold text-[var(--color-secondary)]">Live Browser</h2>
        <span
          data-testid="browser-live-status-pill"
          className={cn('rounded px-2 py-0.5 text-[10px] font-medium', pill.className)}
        >
          {pill.label}
        </span>
        <div className="flex-1" />
        <button
          type="button"
          onClick={handleToggleControl}
          disabled={!connected}
          aria-label={isControlling ? 'Release control' : 'Take control'}
          title={isControlling ? 'Release control' : 'Take control'}
          className={cn(
            'flex items-center gap-1.5 rounded px-2.5 py-1.5 text-xs font-medium transition-colors',
            'disabled:cursor-not-allowed disabled:opacity-40',
            isControlling
              ? 'bg-[var(--color-accent)] text-[var(--color-primary)] hover:opacity-90'
              : 'border border-[var(--color-border)] text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)]',
          )}
        >
          {isControlling ? <HandPalm size={13} /> : <HandGrabbing size={13} />}
          {isControlling ? 'Release control' : 'Take control'}
        </button>
        {onPopOut && (
          <button
            type="button"
            onClick={onPopOut}
            aria-label="Pop out"
            title="Pop out into its own window"
            className="rounded p-1.5 text-[var(--color-muted)] transition-colors hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)] focus-visible:outline focus-visible:outline-2 focus-visible:outline-[var(--color-accent)]"
          >
            <ArrowSquareOut size={15} />
          </button>
        )}
        {onClose && (
          <button
            type="button"
            onClick={onClose}
            aria-label="Close live browser panel"
            title="Close"
            className="rounded p-1.5 text-[var(--color-muted)] transition-colors hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)] focus-visible:outline focus-visible:outline-2 focus-visible:outline-[var(--color-accent)]"
          >
            <X size={15} />
          </button>
        )}
      </div>

      {/* Body */}
      <div className="relative flex min-h-0 flex-1 items-center justify-center overflow-hidden bg-black p-2">
        {!frame && (
          <div className="flex flex-col items-center gap-2 p-6 text-center text-sm text-[var(--color-muted)]">
            {displayError ? (
              <>
                <WarningCircle size={22} className="text-[var(--color-error)]" />
                <p className="text-[var(--color-error)]">{displayError}</p>
              </>
            ) : (
              <>
                <SpinnerGap size={20} className="animate-spin" />
                <p>{connected ? 'Waiting for the first frame…' : 'Connecting to the live browser…'}</p>
              </>
            )}
          </div>
        )}

        {frame && (
          <div
            ref={containerRef}
            tabIndex={0}
            role="application"
            aria-label="Live browser view"
            data-testid="browser-live-frame"
            className="relative inline-block max-h-full max-w-full select-none outline-none"
            style={{ cursor: isControlling ? 'none' : 'default' }}
            onPointerMove={handlePointerMove}
            onPointerDown={handlePointerDown}
            onPointerUp={handlePointerUp}
            onKeyDown={handleKeyDown}
            onKeyUp={handleKeyUp}
            onDragStart={(e) => e.preventDefault()}
          >
            <img
              src={`data:image/jpeg;base64,${frame.data}`}
              alt="Live browser session"
              draggable={false}
              className="block h-auto max-h-full w-auto max-w-full select-none"
            />
            {isControlling && cursorPos && (
              <div
                data-testid="synthetic-cursor"
                className="pointer-events-none absolute z-10"
                style={{ left: cursorPos.x, top: cursorPos.y, transform: 'translate(-2px, -2px)' }}
              >
                <Cursor size={20} weight="fill" className="text-[var(--color-accent)] drop-shadow" />
              </div>
            )}
          </div>
        )}
      </div>

      {/* Persistent error strip — shown alongside the frame once one has rendered (the
          empty-state branch above already surfaces displayError before any frame arrives).
          Covers both transport errors (connError) and a terminal browser_status{state:'error'}
          (already-controlled, take-control-disabled, no-manager-for-agent, live-view-disabled,
          malformed control, …) so a semantic status error is just as visible as a transport one. */}
      {frame && displayError && (
        <div role="alert" className="shrink-0 border-t border-[var(--color-error)]/30 bg-[var(--color-error)]/10 px-4 py-2 text-xs text-[var(--color-error)]">
          {displayError}
        </div>
      )}
    </div>
  )
}

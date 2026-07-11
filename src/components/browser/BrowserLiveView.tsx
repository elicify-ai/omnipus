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

function pillConfig(state: BrowserStatusFrame['state'] | 'connecting'): PillConfig {
  switch (state) {
    case 'controlling':
      return { label: "You're driving", className: 'bg-[var(--color-accent)]/15 text-[var(--color-accent)]' }
    case 'error':
      return { label: 'Error', className: 'bg-[var(--color-error)]/15 text-[var(--color-error)]' }
    case 'detached':
      return { label: 'Detached', className: 'bg-[var(--color-surface-3)] text-[var(--color-muted)]' }
    case 'connecting':
      return { label: 'Connecting…', className: 'bg-[var(--color-surface-3)] text-[var(--color-muted)]' }
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

  const [frame, setFrame] = useState<BrowserScreencastFrame | null>(null)
  const [statusState, setStatusState] = useState<BrowserStatusFrame['state'] | 'connecting'>('connecting')
  const [connError, setConnError] = useState<string | null>(null)
  const [connected, setConnected] = useState(false)
  const [cursorPos, setCursorPos] = useState<{ x: number; y: number } | null>(null)

  const isControlling = statusState === 'controlling'

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
      onStatus: (f) => setStatusState(f.state),
      onError: (message) => setConnError(message),
      onConnected: () => {
        setConnected(true)
        setConnError(null)
      },
      onDisconnected: () => setConnected(false),
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
      const device = mapClientToDevice(e.clientX, e.clientY, rect, frameRef.current.width, frameRef.current.height)
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

  const handleToggleControl = useCallback(() => {
    if (!wsRef.current) return
    wsRef.current.sendControl(isControlling ? 'release' : 'take')
  }, [isControlling])

  const handlePointerMove = useCallback((e: React.PointerEvent<HTMLDivElement>) => {
    if (!controllingRef.current || !frameRef.current || !containerRef.current) return
    const rect = containerRef.current.getBoundingClientRect()
    setCursorPos({ x: e.clientX - rect.left, y: e.clientY - rect.top })
    const device = mapClientToDevice(e.clientX, e.clientY, rect, frameRef.current.width, frameRef.current.height)
    if (!device) return
    wsRef.current?.sendInput({ kind: 'mouse_move', x: device.x, y: device.y, modifiers: computeModifiers(e) })
  }, [])

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
    const device = mapClientToDevice(e.clientX, e.clientY, rect, frameRef.current.width, frameRef.current.height)
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
    const device = mapClientToDevice(e.clientX, e.clientY, rect, frameRef.current.width, frameRef.current.height)
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
            {connError ? (
              <>
                <WarningCircle size={22} className="text-[var(--color-error)]" />
                <p className="text-[var(--color-error)]">{connError}</p>
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
          empty-state branch above already surfaces connError before any frame arrives). */}
      {frame && connError && (
        <div role="alert" className="shrink-0 border-t border-[var(--color-error)]/30 bg-[var(--color-error)]/10 px-4 py-2 text-xs text-[var(--color-error)]">
          {connError}
        </div>
      )}
    </div>
  )
}

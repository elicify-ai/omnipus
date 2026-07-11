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
  ChatCircleDots,
  Cursor,
  HandGrabbing,
  HandPalm,
  Monitor,
  PaperPlaneTilt,
  SpinnerGap,
  WarningCircle,
  X,
} from '@phosphor-icons/react'
import { cn } from '@/lib/utils'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { BrowserLiveWsConnection } from '@/lib/browserLiveWs'
import {
  computeCropRect,
  computeModifiers,
  framePixelToDeviceCoords,
  isPrintableKey,
  mapClientToDevice,
  mapClientToFramePixels,
  mapMouseButton,
  type FrameCropRect,
} from '@/lib/browserLiveCoords'
import { normalizeNavigateUrl } from '@/lib/browserLiveUrl'
import { submitAnnotation, AnnotationBusyError } from '@/lib/browserAnnotate'
import { useUiStore } from '@/store/ui'
import type { BrowserScreencastFrame, BrowserStatusFrame } from '@/lib/api/generated/asyncapi-types'

export interface BrowserLiveViewProps {
  sessionId: string
  agentId: string
  /** Rendered as a header "Pop out" button when provided. */
  onPopOut?: () => void
  /** Rendered as a header "Close" button when provided. */
  onClose?: () => void
  /**
   * ADR-039 D-A3 "Hand to agent" — release control and let the caller drop a
   * hint into the chat composer. Only meaningful when this view is hosted in
   * the SAME JS realm as the AssistantUI runtime that owns the composer
   * (ChatScreen) — i.e. the docked Sheet panel (BrowserLivePanel.tsx), never
   * the fullscreen pop-out (`routes/_app/browser-live.tsx`), which is a
   * separate `window.open` document with its own module/store instances, so
   * writing composerPrefill there is invisible to the original tab's chat.
   * The "Hand to agent" button is rendered ONLY when this is provided —
   * omitting it (as the pop-out route does) hides the button entirely rather
   * than rendering a control that would silently no-op.
   */
  onHandToAgent?: () => void
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

/** A finalized region selection, cropped to a File and ready to send (ADR-039 D-B1/B2). */
interface PendingAnnotation { // not-wire-format: local annotate-popover state, never serialized across the gateway/SPA boundary
  file: File
  previewUrl: string
  /** Device (CSS) pixel point — center of the crop — for the D-B3 inspect call. */
  point: { x: number; y: number }
}

export function BrowserLiveView({ sessionId, agentId, onPopOut, onClose, onHandToAgent, className }: BrowserLiveViewProps) {
  const wsRef = useRef<BrowserLiveWsConnection | null>(null)
  const containerRef = useRef<HTMLDivElement | null>(null)
  const imgRef = useRef<HTMLImageElement | null>(null)
  const frameRef = useRef<BrowserScreencastFrame | null>(null)
  const controllingRef = useRef(false)
  // ── Annotate-a-region state (ADR-039 D-B1/B2) — a third interaction mode,
  // mutually exclusive with driving (isControlling). annotateDraggingRef +
  // selectionStartClientRef are refs (not state) so the pointerup handler
  // always reads the exact values captured on pointerdown, never a stale
  // closure — same pattern as frameRef/controllingRef above.
  const annotateDraggingRef = useRef(false)
  const selectionStartClientRef = useRef<{ x: number; y: number } | null>(null)
  const pendingAnnotationRef = useRef<PendingAnnotation | null>(null)
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
  // True exactly when the LATEST browser_status frame processed was a
  // terminal error one — tracked independently of `statusState` (see the
  // onStatus handler below: a routine per-request error like a blocked
  // navigate must surface a message WITHOUT overwriting statusState away
  // from 'controlling'), so displayError can't rely on `statusState ===
  // 'error'` alone.
  const [statusIsError, setStatusIsError] = useState(false)
  const [connError, setConnError] = useState<string | null>(null)
  const [connected, setConnected] = useState(false)
  const [cursorPos, setCursorPos] = useState<{ x: number; y: number } | null>(null)

  // ── URL bar (ADR-039 D-A2) ──────────────────────────────────────────────
  const [urlInput, setUrlInput] = useState('')

  // ── Annotate mode (ADR-039 D-B1/B2) — container-relative CSS coords for
  // the live selection-box overlay; frozen (not cleared) once a selection
  // finalizes into pendingAnnotation, so the box stays visible behind the
  // comment popover.
  const [annotateMode, setAnnotateMode] = useState(false)
  const [selectionStart, setSelectionStart] = useState<{ x: number; y: number } | null>(null)
  const [selectionCurrent, setSelectionCurrent] = useState<{ x: number; y: number } | null>(null)
  const [pendingAnnotation, setPendingAnnotation] = useState<PendingAnnotation | null>(null)
  const [annotateComment, setAnnotateComment] = useState('')
  const [annotateSubmitting, setAnnotateSubmitting] = useState(false)
  const [annotateError, setAnnotateError] = useState<string | null>(null)

  const isControlling = statusState === 'controlling'
  // Unified error surface: a transport-level error always wins; otherwise
  // the latest browser_status{state:'error'} frame's message is shown —
  // gated on `statusIsError` rather than `statusState === 'error'` so this
  // still surfaces even when onStatus deliberately left statusState alone
  // (the "error while controlling" case below). This is what actually
  // renders (both before and after the first frame arrives) — see the
  // "!frame" branch and the persistent error strip below.
  const displayError = connError ?? (statusIsError ? statusMessage ?? 'The live browser session reported an error.' : null)

  useEffect(() => {
    frameRef.current = frame
  }, [frame])
  useEffect(() => {
    controllingRef.current = isControlling
    // Losing control (agent takes it back / released) clears the stale
    // cursor overlay rather than leaving it frozen at the last position.
    if (!isControlling) setCursorPos(null)
  }, [isControlling])

  // Keep pendingAnnotationRef in sync so the unmount-cleanup effect below
  // (which must run with empty deps, i.e. read only refs) always revokes the
  // CURRENT preview object URL rather than a stale one captured on mount.
  useEffect(() => {
    pendingAnnotationRef.current = pendingAnnotation
  }, [pendingAnnotation])

  // Revoke any outstanding annotation preview object URL when the component
  // unmounts with a comment popover still open (e.g. the Sheet was closed
  // mid-annotation) — otherwise the blob URL leaks for the tab's lifetime.
  useEffect(() => {
    return () => {
      if (pendingAnnotationRef.current) {
        URL.revokeObjectURL(pendingAnnotationRef.current.previewUrl)
      }
    }
  }, [])

  // NOTE: annotate mode and driving (isControlling) are mutually exclusive
  // (ADR-039 D-B1/B2), enforced PROCEDURALLY rather than by a reactive
  // effect: handleToggleAnnotate releases control before entering annotate
  // mode, the Take-control button is disabled while annotating, and the
  // pointer handlers (handlePointerMove/Down/Up) branch on `annotateMode`
  // BEFORE ever consulting `controllingRef` — so no CDP input can be
  // double-dispatched during the async release gap. A reactive
  // `if (isControlling && annotateMode) setAnnotateMode(false)` effect used
  // to live here as a "belt and braces" guard, but it was actively harmful:
  // `sendControl('release')` is async (isControlling only flips once the
  // server's browser_status frame round-trips back), so the effect fired on
  // the very next render — while isControlling was still stale-true — and
  // immediately reverted the annotate-mode toggle the user just clicked,
  // making "Annotate" a silent no-op on the first click while driving.

  // ── WS lifecycle — one connection per mount (host keys this component by
  // `${sessionId}:${agentId}` so a new target always gets a fresh mount). ──
  useEffect(() => {
    const conn = new BrowserLiveWsConnection(sessionId, agentId, {
      onScreencast: (f) => setFrame(f),
      // Reviewer finding: a terminal browser_status{state:'error'} (e.g. a
      // blocked `navigate`) used to overwrite statusState unconditionally —
      // including while `controlling` — which flipped isControlling to
      // false (URL bar/cursor vanish, Take-control reappears) even though
      // the server never actually released control. `statusMessage` and
      // `statusIsError` always update (the error must still surface), but
      // `statusState` — the sole source of `isControlling` — is now only
      // overwritten by TRUE lifecycle frames (attached/controlling/
      // released/detached/idle); an error frame arriving while controlling
      // leaves the prior 'controlling' state alone via the functional
      // updater (correct even for two onStatus calls delivered in the same
      // tick, since it reads the latest pending value, not a stale closure).
      onStatus: (f) => {
        setStatusMessage(f.message ?? null)
        setStatusIsError(f.state === 'error')
        setStatusState((prev) => (f.state === 'error' && prev === 'controlling' ? prev : f.state))
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
        // A stale error surface (e.g. a blocked-navigate message from just
        // before the drop) must not keep showing through a disconnect.
        setStatusIsError(false)
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

  // ── Annotate mode (ADR-039 D-B1/B2) ─────────────────────────────────────

  const resetSelection = useCallback(() => {
    annotateDraggingRef.current = false
    selectionStartClientRef.current = null
    setSelectionStart(null)
    setSelectionCurrent(null)
  }, [])

  const handleCancelAnnotation = useCallback(() => {
    if (pendingAnnotationRef.current) URL.revokeObjectURL(pendingAnnotationRef.current.previewUrl)
    setPendingAnnotation(null)
    setAnnotateComment('')
    setAnnotateError(null)
    resetSelection()
  }, [resetSelection])

  const handleToggleAnnotate = useCallback(() => {
    if (annotateMode) {
      setAnnotateMode(false)
      handleCancelAnnotation()
      return
    }
    // Mutually exclusive with driving (ADR-039 D-B1/B2) — release control
    // first so pointer events stop being forwarded as remote input the
    // instant annotate mode takes over.
    if (isControlling) {
      wsRef.current?.sendControl('release')
    }
    setAnnotateMode(true)
  }, [annotateMode, isControlling, handleCancelAnnotation])

  // Crops the CURRENTLY-RENDERED screencast frame's <img> to a PNG File
  // (mirrors the canvas pattern in media-actions.ts's fetchImagePng). Reads
  // the live <img> at call time (not a stale snapshot) so the crop always
  // reflects exactly what the user was looking at when they finished the
  // drag/click.
  const cropFrameToFile = useCallback(async (rect: FrameCropRect): Promise<File | null> => {
    const img = imgRef.current
    if (!img || !img.complete || img.naturalWidth === 0) return null
    // Reviewer finding: drawImage/getContext can throw synchronously (e.g.
    // IndexSizeError on a degenerate zero-width/height rect, or a tainted
    // canvas) — this function is awaited from finalizeSelection, which is
    // itself invoked fire-and-forget (`void finalizeSelection(...)` from the
    // pointerup handler), so an uncaught rejection here would surface as an
    // unhandled promise rejection with no toast and a frozen selection box.
    // Returning null on ANY failure routes through finalizeSelection's
    // existing `if (!file) return fail()` path instead.
    try {
      const canvas = document.createElement('canvas')
      canvas.width = rect.width
      canvas.height = rect.height
      const ctx = canvas.getContext('2d')
      if (!ctx) return null
      ctx.drawImage(img, rect.x, rect.y, rect.width, rect.height, 0, 0, rect.width, rect.height)
      const blob = await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, 'image/png'))
      if (!blob) return null
      return new File([blob], 'annotation.png', { type: 'image/png' })
    } catch {
      return null
    }
  }, [])

  // Finalizes a drag/click selection into a pendingAnnotation (crop + open
  // the comment popover). Never forwards anything over the control-input WS
  // path — annotate mode is a purely local, client-side interaction until
  // the user hits Send (submitAnnotation).
  const finalizeSelection = useCallback(
    async (containerRect: DOMRect, startClient: { x: number; y: number }, endClient: { x: number; y: number }) => {
      // No popover is open yet at any failure point here (pendingAnnotation
      // is only set on full success below) — setAnnotateError would render
      // into a popover that doesn't exist, and silently returning would leave
      // the frozen selection box up with no feedback (a stuck-looking UI).
      // Every failure path — frame gone, unmeasurable container, degenerate
      // crop rect, or the crop itself failing — toasts and resets instead.
      const fail = () => {
        useUiStore.getState().addToast({ message: 'Could not capture that region — try again.', variant: 'error' })
        resetSelection()
      }
      const frame = frameRef.current
      if (!frame) return fail()
      const startPx = mapClientToFramePixels(startClient.x, startClient.y, containerRect, frame.width, frame.height)
      const endPx = mapClientToFramePixels(endClient.x, endClient.y, containerRect, frame.width, frame.height)
      if (!startPx || !endPx) return fail()
      const cropRect = computeCropRect(startPx, endPx, frame.width, frame.height)
      if (!cropRect) return fail()

      const file = await cropFrameToFile(cropRect)
      if (!file) return fail()
      const center = framePixelToDeviceCoords(
        cropRect.x + cropRect.width / 2,
        cropRect.y + cropRect.height / 2,
        frame.page_scale,
      )
      setPendingAnnotation({ file, previewUrl: URL.createObjectURL(file), point: center })
    },
    [cropFrameToFile, resetSelection],
  )

  // ── URL bar (ADR-039 D-A2) ───────────────────────────────────────────────

  const handleNavigateSubmit = useCallback(
    (e: React.FormEvent) => {
      e.preventDefault()
      const normalized = normalizeNavigateUrl(urlInput)
      if (!normalized) return
      wsRef.current?.sendInput({ kind: 'navigate', url: normalized })
      setUrlInput(normalized)
    },
    [urlInput],
  )

  // ── Hand to agent (ADR-039 D-A3) ─────────────────────────────────────────
  // The composer-prefill bridge (useUiStore.composerPrefill → ChatScreen)
  // only works when this view shares a JS realm with ChatScreen, which the
  // fullscreen pop-out (routes/_app/browser-live.tsx) does NOT — it's a
  // separate `window.open` document with its own store instances. Rather
  // than reaching into useUiStore directly (which would silently no-op from
  // the pop-out while still showing a success toast), that responsibility is
  // delegated entirely to the caller-supplied `onHandToAgent`; this view
  // only owns the one thing every host CAN do — release the WS control-lock
  // — and the button itself is hidden whenever `onHandToAgent` is absent.

  const handleHandToAgent = useCallback(() => {
    wsRef.current?.sendControl('release')
    onHandToAgent?.()
  }, [onHandToAgent])

  const handlePointerMove = useCallback((e: React.PointerEvent<HTMLDivElement>) => {
    if (annotateMode) {
      if (!annotateDraggingRef.current || !containerRef.current) return
      const rect = containerRef.current.getBoundingClientRect()
      setSelectionCurrent({ x: e.clientX - rect.left, y: e.clientY - rect.top })
      return
    }
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
  }, [scheduleMoveFlush, annotateMode])

  // Focuses the frame container (so keyboard input starts flowing) and
  // best-effort captures the pointer so a drag that leaves the container
  // bounds (a fast selection, or a slider drag while driving) still
  // delivers move/up events here — without this, releasing outside the
  // frame would leave the remote page thinking the mouse button is still
  // held down. Shared by both handlePointerDown branches (annotate-drag
  // start and remote mouse_down) — the capture step is identical either way.
  const focusAndCapturePointer = useCallback((e: React.PointerEvent<HTMLDivElement>) => {
    containerRef.current?.focus()
    try {
      e.currentTarget.setPointerCapture(e.pointerId)
    } catch {
      // Pointer capture is best-effort — unsupported/jsdom environments fall
      // back to normal bounds-limited pointer events.
    }
  }, [])

  const handlePointerDown = useCallback((e: React.PointerEvent<HTMLDivElement>) => {
    if (annotateMode) {
      // pendingAnnotationRef (not state) — a selection popover already open
      // must be Cancelled or Sent before starting a new drag; reading the
      // ref (rather than adding pendingAnnotation to the dep array) keeps
      // this callback's identity stable across every popover open/close.
      if (!frameRef.current || !containerRef.current || pendingAnnotationRef.current) return
      focusAndCapturePointer(e)
      const rect = containerRef.current.getBoundingClientRect()
      const point = { x: e.clientX - rect.left, y: e.clientY - rect.top }
      selectionStartClientRef.current = { x: e.clientX, y: e.clientY }
      annotateDraggingRef.current = true
      setSelectionStart(point)
      setSelectionCurrent(point)
      return
    }
    if (!controllingRef.current || !frameRef.current || !containerRef.current) return
    focusAndCapturePointer(e)
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
  }, [annotateMode, focusAndCapturePointer])

  const handlePointerUp = useCallback((e: React.PointerEvent<HTMLDivElement>) => {
    if (annotateMode) {
      if (!annotateDraggingRef.current || !containerRef.current) {
        annotateDraggingRef.current = false
        return
      }
      annotateDraggingRef.current = false
      const startClient = selectionStartClientRef.current
      const rect = containerRef.current.getBoundingClientRect()
      setSelectionCurrent({ x: e.clientX - rect.left, y: e.clientY - rect.top })
      if (startClient) {
        void finalizeSelection(rect, startClient, { x: e.clientX, y: e.clientY })
      }
      return
    }
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
  }, [annotateMode, finalizeSelection])

  const handleSendAnnotation = useCallback(() => {
    const annotation = pendingAnnotation
    const comment = annotateComment.trim()
    if (!annotation || comment.length === 0) return
    setAnnotateSubmitting(true)
    setAnnotateError(null)
    // sessionId/agentId — this view's own pinned props, NOT re-read from
    // useSessionStore — so the annotation always targets the browser being
    // annotated even if the globally-active chat has since changed.
    submitAnnotation({ comment, file: annotation.file, point: annotation.point, sessionId, agentId })
      .then(() => {
        useUiStore.getState().addToast({ message: 'Annotation sent to the agent.', variant: 'success' })
        URL.revokeObjectURL(annotation.previewUrl)
        setPendingAnnotation(null)
        setAnnotateComment('')
        setAnnotateMode(false)
        resetSelection()
      })
      .catch((err: unknown) => {
        if (err instanceof AnnotationBusyError) {
          // Never a silent no-op: surface via toast AND keep the popover
          // open (pendingAnnotation is untouched) so the user can just hit
          // Send again once the agent is free.
          useUiStore.getState().addToast({ message: err.message, variant: 'error' })
          return
        }
        setAnnotateError(err instanceof Error ? err.message : String(err))
      })
      .finally(() => {
        setAnnotateSubmitting(false)
      })
  }, [pendingAnnotation, annotateComment, resetSelection, sessionId, agentId])

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
      <div
        className="flex shrink-0 items-center gap-2 overflow-x-auto border-b border-[var(--color-border)] pl-4 pr-14 py-3"
        style={{ scrollbarWidth: 'none', msOverflowStyle: 'none' } as React.CSSProperties}
      >
        <Monitor size={16} weight="duotone" className="shrink-0 text-[var(--color-accent)]" />
        <h2 className="shrink-0 font-headline text-sm font-semibold text-[var(--color-secondary)]">Live Browser</h2>
        <span
          data-testid="browser-live-status-pill"
          className={cn('shrink-0 rounded px-2 py-0.5 text-[10px] font-medium whitespace-nowrap', pill.className)}
        >
          {pill.label}
        </span>
        <div className="flex-1 min-w-2" />
        {/* Annotate toggle (ADR-039 D-B1/B2) — a third interaction mode,
            mutually exclusive with driving: entering it releases control
            (handleToggleAnnotate), and Take control is disabled while it's
            active (below), so the two states can never overlap. */}
        <button
          type="button"
          onClick={handleToggleAnnotate}
          disabled={!connected}
          aria-label={annotateMode ? 'Exit annotate mode' : 'Annotate a region'}
          title={annotateMode ? 'Exit annotate mode' : 'Drag a region (or click a spot) to comment on it'}
          className={cn(
            'flex shrink-0 items-center gap-1.5 whitespace-nowrap rounded px-2.5 py-1.5 text-xs font-medium transition-colors',
            'disabled:cursor-not-allowed disabled:opacity-40',
            annotateMode
              ? 'bg-[var(--color-accent)] text-[var(--color-primary)] hover:opacity-90'
              : 'border border-[var(--color-border)] text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)]',
          )}
        >
          <ChatCircleDots size={13} />
          {annotateMode ? 'Exit annotate' : 'Annotate'}
        </button>
        <button
          type="button"
          onClick={handleToggleControl}
          disabled={!connected || annotateMode}
          aria-label={isControlling ? 'Release control' : 'Take control'}
          title={isControlling ? 'Release control' : 'Take control'}
          className={cn(
            'flex shrink-0 items-center gap-1.5 whitespace-nowrap rounded px-2.5 py-1.5 text-xs font-medium transition-colors',
            'disabled:cursor-not-allowed disabled:opacity-40',
            isControlling
              ? 'bg-[var(--color-accent)] text-[var(--color-primary)] hover:opacity-90'
              : 'border border-[var(--color-border)] text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)]',
          )}
        >
          {isControlling ? <HandPalm size={13} /> : <HandGrabbing size={13} />}
          {isControlling ? 'Release control' : 'Take control'}
        </button>
        {isControlling && onHandToAgent && (
          <button
            type="button"
            onClick={handleHandToAgent}
            aria-label="Hand to agent"
            title="Release control and let the agent continue from here"
            className="flex shrink-0 items-center gap-1.5 whitespace-nowrap rounded px-2.5 py-1.5 text-xs font-medium border border-[var(--color-border)] text-[var(--color-secondary)] transition-colors hover:bg-[var(--color-surface-2)]"
          >
            <PaperPlaneTilt size={13} />
            Hand to agent
          </button>
        )}
        {onPopOut && (
          <button
            type="button"
            onClick={onPopOut}
            aria-label="Pop out"
            title="Pop out into its own window"
            className="shrink-0 rounded p-1.5 text-[var(--color-muted)] transition-colors hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)] focus-visible:outline focus-visible:outline-2 focus-visible:outline-[var(--color-accent)]"
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
            className="shrink-0 rounded p-1.5 text-[var(--color-muted)] transition-colors hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)] focus-visible:outline focus-visible:outline-2 focus-visible:outline-[var(--color-accent)]"
          >
            <X size={15} />
          </button>
        )}
      </div>

      {/* URL bar (ADR-039 D-A2) — shown + enabled only while the viewer holds
          control; the server's ValidateURL SSRF gate (run on every `navigate`
          input frame) is the real authority, and any rejection surfaces via
          the existing browser_status(error) strip below.
          `!annotateMode` matters beyond the isControlling check alone:
          entering annotate mode releases control asynchronously (the
          server's browser_status('released') round-trip hasn't landed yet),
          so isControlling can still read stale-true for a brief window —
          without this, a real `navigate` could be submitted through the
          still-visible address bar while nominally in local-only annotate
          mode. */}
      {isControlling && !annotateMode && (
        <form
          onSubmit={handleNavigateSubmit}
          className="flex shrink-0 items-center gap-2 border-b border-[var(--color-border)] px-4 py-2"
        >
          <Input
            type="text"
            value={urlInput}
            onChange={(e) => setUrlInput(e.target.value)}
            placeholder="Enter a URL and press Go…"
            aria-label="Navigate to URL"
            className="h-8 flex-1 text-xs"
          />
          <button
            type="submit"
            disabled={urlInput.trim().length === 0}
            className="shrink-0 rounded-md bg-[var(--color-accent)] px-3 h-8 text-xs font-medium text-[var(--color-primary)] transition-opacity hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-40"
          >
            Go
          </button>
        </form>
      )}

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
            style={{ cursor: annotateMode ? 'crosshair' : isControlling ? 'none' : 'default' }}
            onPointerMove={handlePointerMove}
            onPointerDown={handlePointerDown}
            onPointerUp={handlePointerUp}
            onKeyDown={handleKeyDown}
            onKeyUp={handleKeyUp}
            onDragStart={(e) => e.preventDefault()}
          >
            <img
              ref={imgRef}
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
            {/* Selection-box overlay (ADR-039 D-B1/B2) — container-relative CSS
                coords, drawn live while dragging and frozen once the
                selection finalizes into pendingAnnotation (comment popover
                below stays anchored to it). */}
            {annotateMode && selectionStart && selectionCurrent && (
              <div
                data-testid="annotate-selection-box"
                className="pointer-events-none absolute z-10 border-2 border-[var(--color-accent)] bg-[var(--color-accent)]/15"
                style={{
                  left: Math.min(selectionStart.x, selectionCurrent.x),
                  top: Math.min(selectionStart.y, selectionCurrent.y),
                  width: Math.abs(selectionCurrent.x - selectionStart.x),
                  height: Math.abs(selectionCurrent.y - selectionStart.y),
                }}
              />
            )}
          </div>
        )}

        {/* Annotate comment popover (ADR-039 D-B1/B2) — appears once a
            drag/click selection finalizes into a cropped pendingAnnotation.
            Bottom-anchored (rather than positioned relative to the possibly
            edge-of-frame selection box) to sidestep viewport-clamping math;
            the frozen selection-box overlay above stays visible so the
            connection to what's being discussed is still clear. */}
        {annotateMode && pendingAnnotation && (
          <div
            data-testid="annotate-popover"
            className="absolute inset-x-0 bottom-0 z-20 border-t border-[var(--color-border)] bg-[var(--color-surface-1)] p-3 shadow-lg"
          >
            <div className="flex items-start gap-3">
              <img
                src={pendingAnnotation.previewUrl}
                alt="Selected region"
                className="h-16 w-16 shrink-0 rounded border border-[var(--color-border)] object-cover"
              />
              <div className="min-w-0 flex-1">
                <Textarea
                  value={annotateComment}
                  onChange={(e) => setAnnotateComment(e.target.value)}
                  onKeyDown={(e) => {
                    // Cancel the pending annotation on Escape rather than
                    // letting it bubble to Radix's Sheet (which would close
                    // the whole panel and discard the drafted comment).
                    if (e.key === 'Escape') {
                      e.stopPropagation()
                      handleCancelAnnotation()
                    }
                  }}
                  placeholder="What would you like to discuss about this?"
                  aria-label="Annotation comment"
                  className="min-h-[60px] text-xs"
                  disabled={annotateSubmitting}
                  autoFocus
                />
                {annotateError && (
                  <p role="alert" className="mt-1 text-[11px] text-[var(--color-error)]">
                    {annotateError}
                  </p>
                )}
                <div className="mt-2 flex justify-end gap-2">
                  <button
                    type="button"
                    onClick={handleCancelAnnotation}
                    disabled={annotateSubmitting}
                    className="rounded px-2.5 py-1 text-xs text-[var(--color-muted)] transition-colors hover:bg-[var(--color-surface-2)] disabled:cursor-not-allowed disabled:opacity-40"
                  >
                    Cancel
                  </button>
                  <button
                    type="button"
                    onClick={handleSendAnnotation}
                    disabled={annotateSubmitting || annotateComment.trim().length === 0}
                    className="rounded bg-[var(--color-accent)] px-3 py-1 text-xs font-medium text-[var(--color-primary)] transition-opacity hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-40"
                  >
                    {annotateSubmitting ? 'Sending…' : 'Send'}
                  </button>
                </div>
              </div>
            </div>
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

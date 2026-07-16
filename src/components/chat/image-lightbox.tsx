// ImageLightbox — full-screen overlay for clicking images in markdown.
// Close on backdrop click or Escape key.
//
// Extended props (all optional, back-compat preserved):
//   svg?     — render sanitized SVG markup instead of an <img> (Mermaid).
//              When both `svg` and `src` are provided, `svg` wins.
//   toolbar? — render a MediaActionToolbar (or any ReactNode) in a top bar.
//   title?   — caption shown above the image (below the close button row).
//
// Zoom/pan: mouse-wheel / ctrl+wheel to zoom (centered on cursor), drag to pan
// when zoomed, double-click to reset. Scale is clamped 0.5–8×. Esc and
// backdrop-click close (but a drag does NOT close).

import { useEffect, useRef, useState, useCallback, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { X } from '@phosphor-icons/react'
import DOMPurify from 'dompurify'

// ── Types ──────────────────────────────────────────────────────────────────────

interface ImageLightboxProps {
  /** Image URL — used when `svg` is not provided. Required for the original API. */
  src?: string
  alt?: string
  onClose: () => void
  /** Sanitized SVG markup (e.g. from Mermaid). If provided, renders instead of <img>. */
  svg?: string
  /** Toolbar content — rendered in a top action bar above the media. */
  toolbar?: ReactNode
  /** Optional caption displayed above the media area (below the toolbar row). */
  title?: string
}

// ── Constants ─────────────────────────────────────────────────────────────────

const MIN_SCALE = 0.5
const MAX_SCALE = 8
const RESET_SCALE = 1
const RESET_TRANSLATE = { x: 0, y: 0 }

// ── ImageLightbox ──────────────────────────────────────────────────────────────

export function ImageLightbox({ src, alt, onClose, svg, toolbar, title }: ImageLightboxProps) {
  // Transform state
  const [scale, setScale] = useState(RESET_SCALE)
  const [translate, setTranslate] = useState(RESET_TRANSLATE)

  // Drag state (stored in a ref to avoid re-renders on every mousemove)
  const dragRef = useRef<{
    active: boolean
    startX: number
    startY: number
    startTx: number
    startTy: number
    moved: boolean
  } | null>(null)

  // Reference to the media wrapper for centering zoom on cursor
  const mediaWrapRef = useRef<HTMLDivElement>(null)

  const resetTransform = useCallback(() => {
    setScale(RESET_SCALE)
    setTranslate(RESET_TRANSLATE)
  }, [])

  // ── Keyboard handler ────────────────────────────────────────────────────────
  useEffect(() => {
    function handleKey(e: KeyboardEvent) {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', handleKey)
    return () => window.removeEventListener('keydown', handleKey)
  }, [onClose])

  // ── Wheel zoom ──────────────────────────────────────────────────────────────
  const handleWheel = useCallback(
    (e: React.WheelEvent<HTMLDivElement>) => {
      e.preventDefault()
      const wrap = mediaWrapRef.current
      if (!wrap) return

      const rect = wrap.getBoundingClientRect()
      // Cursor position relative to the media wrapper center
      const cursorX = e.clientX - rect.left - rect.width / 2
      const cursorY = e.clientY - rect.top - rect.height / 2

      const zoomFactor = e.deltaY < 0 ? 1.15 : 1 / 1.15
      const newScale = Math.min(MAX_SCALE, Math.max(MIN_SCALE, scale * zoomFactor))
      const scaleDelta = newScale / scale

      // Shift translate so the point under the cursor stays fixed
      setTranslate((prev) => ({
        x: cursorX - (cursorX - prev.x) * scaleDelta,
        y: cursorY - (cursorY - prev.y) * scaleDelta,
      }))
      setScale(newScale)
    },
    [scale],
  )

  // ── Drag to pan ─────────────────────────────────────────────────────────────
  const handleMouseDown = useCallback(
    (e: React.MouseEvent<HTMLDivElement>) => {
      if (e.button !== 0) return
      dragRef.current = {
        active: true,
        startX: e.clientX,
        startY: e.clientY,
        startTx: translate.x,
        startTy: translate.y,
        moved: false,
      }
    },
    [translate],
  )

  const handleMouseMove = useCallback((e: React.MouseEvent<HTMLDivElement>) => {
    const drag = dragRef.current
    if (!drag?.active) return
    const dx = e.clientX - drag.startX
    const dy = e.clientY - drag.startY
    if (Math.abs(dx) > 2 || Math.abs(dy) > 2) {
      drag.moved = true
    }
    setTranslate({ x: drag.startTx + dx, y: drag.startTy + dy })
  }, [])

  const handleMouseUp = useCallback(() => {
    if (dragRef.current) {
      dragRef.current.active = false
    }
  }, [])

  // ── Double-click to reset ───────────────────────────────────────────────────
  const handleDoubleClick = useCallback(() => {
    resetTransform()
  }, [resetTransform])

  // ── Backdrop click (don't close if a drag just ended) ──────────────────────
  const handleBackdropClick = useCallback(() => {
    if (dragRef.current?.moved) {
      dragRef.current.moved = false
      return
    }
    onClose()
  }, [onClose])

  // Media stop propagation (prevents backdrop close when clicking the media)
  const handleMediaClick = useCallback((e: React.MouseEvent) => {
    e.stopPropagation()
  }, [])

  // ── Sanitize SVG if provided ────────────────────────────────────────────────
  const sanitizedSvg = svg
    ? DOMPurify.sanitize(svg, { USE_PROFILES: { svg: true }, ADD_TAGS: ['foreignObject'] })
    : null

  // Cursor style reflects drag state
  const cursorClass = scale > 1 ? 'cursor-grab active:cursor-grabbing' : 'cursor-zoom-in'

  return createPortal(
    <div
      className="fixed inset-0 z-[200] flex flex-col bg-black/85 backdrop-blur-sm"
      onClick={handleBackdropClick}
      role="dialog"
      aria-modal
      aria-label={title ?? alt ?? 'Image preview'}
    >
      {/* ── Top bar: close + optional toolbar ──────────────────────────── */}
      <div
        className="flex items-center justify-between px-4 py-3 shrink-0"
        onClick={(e) => e.stopPropagation()}
      >
        {/* Title / toolbar slot */}
        <div className="flex items-center gap-3 min-w-0 flex-1">
          {title && (
            <span className="text-sm font-medium text-[var(--color-secondary)] truncate">
              {title}
            </span>
          )}
          {toolbar && <div className="flex items-center">{toolbar}</div>}
        </div>

        {/* Close button */}
        <button tabIndex={0}
          type="button"
          className="ml-3 w-9 h-9 shrink-0 flex items-center justify-center rounded-full bg-[var(--color-surface-2)] border border-[var(--color-border)] text-[var(--color-secondary)] hover:bg-[var(--color-surface-3,var(--color-surface-2))] transition-colors z-10"
          onClick={onClose}
          aria-label="Close image preview"
        >
          <X size={16} weight="bold" />
        </button>
      </div>

      {/* ── Media area ─────────────────────────────────────────────────── */}
      <div
        ref={mediaWrapRef}
        className={`flex-1 flex items-center justify-center overflow-hidden select-none ${cursorClass}`}
        onWheel={handleWheel}
        onMouseDown={handleMouseDown}
        onMouseMove={handleMouseMove}
        onMouseUp={handleMouseUp}
        onMouseLeave={handleMouseUp}
        onDoubleClick={handleDoubleClick}
        onClick={handleMediaClick}
      >
        <div
          style={{
            transform: `translate(${translate.x}px, ${translate.y}px) scale(${scale})`,
            transformOrigin: 'center center',
            willChange: 'transform',
            transition: dragRef.current?.active ? 'none' : 'transform 0.05s ease-out',
          }}
        >
          {sanitizedSvg ? (
            <div
              className="max-w-[90vw] max-h-[80vh] rounded-lg overflow-hidden shadow-2xl ring-1 ring-[var(--color-border)] bg-[var(--color-surface-2)] p-4"
              dangerouslySetInnerHTML={{ __html: sanitizedSvg }}
            />
          ) : src ? (
            <img
              src={src}
              alt={alt || ''}
              draggable={false}
              className="max-w-[90vw] max-h-[80vh] rounded-lg object-contain shadow-2xl ring-1 ring-[var(--color-border)]"
            />
          ) : null}
        </div>
      </div>

      {/* ── Zoom indicator (shown when not at 1×) ──────────────────────── */}
      {scale !== 1 && (
        <div
          className="absolute bottom-10 right-4 text-[10px] text-[var(--color-muted)] bg-[var(--color-surface-2)]/80 px-2 py-0.5 rounded-full pointer-events-none"
          onClick={(e) => e.stopPropagation()}
        >
          {Math.round(scale * 100)}%
        </div>
      )}

      {/* ── Alt caption ────────────────────────────────────────────────── */}
      {alt && (
        <p
          className="absolute bottom-4 left-1/2 -translate-x-1/2 text-xs text-[var(--color-muted)] bg-[var(--color-surface-2)]/80 px-3 py-1.5 rounded-full pointer-events-none"
          onClick={(e) => e.stopPropagation()}
        >
          {alt}
        </p>
      )}
    </div>,
    document.body,
  )
}

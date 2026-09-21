// ImageLightbox — full-screen overlay for enlarging an image or an SVG
// diagram, used by chat (MediaLightbox.tsx) and, through the same shared
// component, the Library image/diagram previews. Built entirely on the
// ZoomableView media-viewer face (D18,
// docs/internal/design/components/zoomable-view.md): the ZoomPill lives in
// the toolbar, and drag-to-pan / wheel-zoom-on-pointer / pinch /
// double-click-to-toggle-fit / the shared 25-400% range all come from
// `useZoomableMedia`. This component owns no zoom math of its own.
//
//   svg?     — render sanitized SVG markup instead of an <img> (Mermaid).
//              When both `svg` and `src` are provided, `svg` wins. Its
//              intrinsic size is resolved from its viewBox
//              (resolveSvgIntrinsicSize) rather than trusted to survive
//              sanitization — defect 1 (zoomable-view.md): an <svg> that
//              loses its width/height/style attributes to DOMPurify falls
//              back to the browser's default replaced-element size
//              (300x150), so a wide diagram "opens" collapsed to ~300px at
//              every viewport regardless of its real content size.
//   toolbar? — render a MediaActionToolbar (or any ReactNode) in the top
//              bar, alongside the zoom pill.
//   title?   — caption shown above the media (below the close-button row).
//
// Close on backdrop click, the close button, or Escape; every close path
// returns focus to whatever was focused when the viewer opened (D18: "Escape
// closes the media viewer and restores focus to the enlarge action that
// opened it").

import { useEffect, useMemo, useRef, useState, useCallback, type ReactNode, type SyntheticEvent } from 'react'
import { createPortal } from 'react-dom'
import { X } from '@phosphor-icons/react'
import DOMPurify from 'dompurify'
import { IconButton } from '@/components/ui/icon-button'
import { ZoomPill, useZoomableMedia, resolveSvgIntrinsicSize, computeFittedScale, type ZoomableSize } from '@/components/ui/zoomable-view'

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

// ── ImageLightbox ──────────────────────────────────────────────────────────────

export function ImageLightbox({ src, alt, onClose, svg, toolbar, title }: ImageLightboxProps) {
  // `svg` is fixed for the lifetime of this component instance — the caller
  // (MediaLightbox) keys the lightbox by content, so a different diagram is
  // a fresh mount, never a prop swap on the same instance.
  const sanitizedSvg = useMemo(
    () => (svg ? DOMPurify.sanitize(svg, { USE_PROFILES: { svg: true }, ADD_TAGS: ['foreignObject'] }) : null),
    [svg],
  )

  // Defect 1 fix: resolve the SVG's TRUE intrinsic size from its viewBox,
  // from the markup string directly — no need to wait for it to mount.
  const svgSize = useMemo(() => (sanitizedSvg ? resolveSvgIntrinsicSize(sanitizedSvg) : null), [sanitizedSvg])

  // An <img>'s natural size is known only once it has loaded.
  const [imgSize, setImgSize] = useState<ZoomableSize | null>(null)
  const handleImgLoad = useCallback((event: SyntheticEvent<HTMLImageElement>) => {
    setImgSize({ width: event.currentTarget.naturalWidth, height: event.currentTarget.naturalHeight })
  }, [])

  const contentSize = sanitizedSvg ? svgSize : imgSize

  // The raw hook, not <ZoomableMediaSurface>: this component already owns the
  // frame element (the media area below the top bar) and needs `scale` as
  // live React state so the toolbar's ZoomPill — a SIBLING of the frame, not
  // a child — re-renders with it. `useZoomableMedia` is published exactly
  // for this ("a caller that already owns its own wrapper element ... can
  // bind these handlers directly without an extra DOM layer").
  // A photo opens fitted but never above its real pixel size: enlarging a
  // 200x120 image 4x to fill the screen only shows blocky pixels. A diagram
  // is vector and stays sharp at any scale, so it keeps the plain fit.
  const getFitScale = useCallback(
    (frame: ZoomableSize) => {
      if (sanitizedSvg || !imgSize) return svgSize ? computeFittedScale(svgSize, frame) : 1
      return Math.min(computeFittedScale(imgSize, frame), 1)
    },
    [sanitizedSvg, imgSize, svgSize],
  )
  const { scale, translate, controller, frameProps, frameRef } = useZoomableMedia({ contentSize, getFitScale })

  // Focus restore (D18): capture whatever was focused when the viewer opened
  // and give it back on unmount, for every close path — Escape, the close
  // button, or the backdrop.
  const openerRef = useRef<HTMLElement | null>(null)
  useEffect(() => {
    openerRef.current = document.activeElement as HTMLElement | null
    return () => {
      openerRef.current?.focus?.()
    }
  }, [])

  useEffect(() => {
    function handleKey(event: KeyboardEvent) {
      if (event.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', handleKey)
    return () => window.removeEventListener('keydown', handleKey)
  }, [onClose])

  const handleBackdropClick = useCallback(() => onClose(), [onClose])
  // Pointer capture (set by useZoomableMedia's pointer-down handler) keeps a
  // drag's eventual `click` targeted on the frame even if the pointer ends up
  // over the backdrop, so this alone is enough to stop a drag-release from
  // also closing the viewer — no separate "did we just drag" tracking needed.
  const handleMediaClick = useCallback((event: React.MouseEvent) => {
    event.stopPropagation()
  }, [])

  return createPortal(
    <div
      className="fixed inset-0 z-[200] flex flex-col bg-[var(--color-primary)]/85 backdrop-blur-sm"
      onClick={handleBackdropClick}
      role="dialog"
      aria-modal
      aria-label={title ?? alt ?? 'Image preview'}
    >
      {/* ── Top bar: close + zoom pill + optional toolbar ──────────────── */}
      <div
        className="flex items-center justify-between px-[var(--space-3)] py-[var(--space-2-5)] shrink-0"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center gap-[var(--space-2-5)] min-w-0 flex-1">
          {title && (
            <span className="text-[length:var(--type-body-compact-size)] font-medium text-[var(--color-secondary)] truncate">
              {title}
            </span>
          )}
          <ZoomPill
            zoom={scale}
            min={controller.minScale}
            onZoomIn={controller.zoomIn}
            onZoomOut={controller.zoomOut}
            onFit={controller.zoomToFit}
            onZoomTo100={controller.zoomTo100}
          />
          {toolbar && <div className="flex items-center">{toolbar}</div>}
        </div>

        {/* Close button */}
        <IconButton
          variant="secondary"
          className="ml-[var(--space-2-5)] shrink-0 rounded-full border border-[var(--color-border)] z-10"
          onClick={onClose}
          aria-label="Close image preview"
        >
          <X size={16} weight="bold" />
        </IconButton>
      </div>

      {/* ── Media area — the ZoomableView media-viewer frame ───────────── */}
      <div
        {...frameProps}
        ref={frameRef}
        tabIndex={0}
        role="group"
        aria-label={title ?? alt ?? 'Zoomable media'}
        data-testid="image-lightbox-frame"
        data-scale={scale}
        data-fit={controller.isFit}
        onClick={handleMediaClick}
        className={`relative flex-1 flex items-center justify-center overflow-hidden select-none ${
          controller.isFit ? 'cursor-zoom-in' : 'cursor-grab active:cursor-grabbing'
        }`}
      >
        <div
          style={{
            transform: `translate(${translate.x}px, ${translate.y}px) scale(${scale})`,
            transformOrigin: 'center center',
          }}
        >
          {sanitizedSvg ? (
            <div
              style={svgSize ? { width: svgSize.width, height: svgSize.height } : undefined}
              className="[&>svg]:block [&>svg]:h-full [&>svg]:w-full rounded-lg overflow-hidden shadow-2xl ring-1 ring-[var(--color-border)] bg-[var(--color-surface-2)] p-[var(--space-3)]"
              data-testid="image-lightbox-svg"
              dangerouslySetInnerHTML={{ __html: sanitizedSvg }}
            />
          ) : src ? (
            <img
              src={src}
              alt={alt || ''}
              draggable={false}
              onLoad={handleImgLoad}
              // Laid out at its natural size, like the SVG branch: the zoom
              // transform must be the only thing that scales it. Without this
              // the preflight `img { max-width: 100% }` pre-shrinks a large
              // image to the frame width and the fitted transform then shrinks
              // it again (a 4000x3000 image opened at 337x253 instead of
              // 1123x842).
              style={imgSize ? { width: imgSize.width, height: imgSize.height } : undefined}
              className="max-w-none shrink-0 rounded-lg object-contain shadow-2xl ring-1 ring-[var(--color-border)]"
            />
          ) : null}
        </div>
      </div>

      {/* ── Alt caption ────────────────────────────────────────────────── */}
      {alt && (
        <p
          className="absolute bottom-4 left-1/2 -translate-x-1/2 text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] bg-[var(--color-surface-2)]/80 px-[var(--space-2-5)] py-[var(--space-1)] rounded-full pointer-events-none"
          onClick={(e) => e.stopPropagation()}
        >
          {alt}
        </p>
      )}
    </div>,
    document.body,
  )
}

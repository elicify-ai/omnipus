// ZoomableView — the shared zoom-and-pan behaviour contract (D18,
// `docs/internal/design/components/zoomable-view.md`). Two faces, one
// control and one range:
//
//   - The media viewer face (`useZoomableMedia` / `ZoomableMediaSurface`,
//     below) — a pointer-drag-and-wheel-zoom surface for an image or SVG,
//     for the chat media viewer, the Library image/diagram/PDF previews.
//   - The canvas preset for React Flow (`zoomableCanvasFlowProps` +
//     `useZoomableCanvasPill`, `./zoomable-view-canvas.tsx`) — the workspace
//     task graph and team graph. It is a SEPARATE, non-published file: this
//     file is re-exported from the `@omnipus/ui` package entry
//     (`src/index.ts`), and React Flow is an app-specific dependency, not a
//     Sovereign Deep foundation (see that file's header comment).
//
// Both faces share `ZoomPill` (below): zoom out, the current percentage (a
// menu with Fit / 100% / Zoom to selection), zoom in. Range is 25%–400%
// everywhere (`ZOOMABLE_VIEW_MIN_SCALE`/`ZOOMABLE_VIEW_MAX_SCALE`) — EXCEPT
// that Fit must always be reachable: when content needs less than 25% to
// fit, the fitted scale itself becomes the lower bound (`effectiveMinScale`,
// founder-approved clarification, 2026-09-21). See the "Range" line in
// `docs/internal/design/components/zoomable-view.md`.
//
// This is Phase 1 (2026-09-21, founder-approved): the component, its two
// faces, and its publication. No consumer screen is migrated here — Phase 2
// lanes wire GraphView, WorkspaceTeamGraph, the chat media viewer, the
// Library image/Mermaid/PDF previews onto it.

import * as React from 'react'
import { MagnifyingGlassMinus, MagnifyingGlassPlus, CaretDown } from '@phosphor-icons/react'
import { Button } from './button'
import { IconButton } from './icon-button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuShortcut,
  DropdownMenuTrigger,
} from './dropdown-menu'
import { cn } from '@/lib/utils'

// ── Shared range & math (D18: "One zoom range: 25% to 400% on every surface") ──

export const ZOOMABLE_VIEW_MIN_SCALE = 0.25
export const ZOOMABLE_VIEW_MAX_SCALE = 4
/** The label-legibility floor (D2), reused by the opening-size rule. */
export const ZOOMABLE_VIEW_LABEL_FLOOR_PX = 12

export interface ZoomableSize {
  width: number
  height: number
}

/** Clamps a zoom fraction (1 = 100%) to the shared 25%–400% range, or a
 *  caller-narrowed subrange. Non-finite input clamps to `min`, never NaN. */
export function clampZoomScale(
  scale: number,
  min: number = ZOOMABLE_VIEW_MIN_SCALE,
  max: number = ZOOMABLE_VIEW_MAX_SCALE,
): number {
  if (!Number.isFinite(scale)) return min
  return Math.min(max, Math.max(min, scale))
}

/** The effective lower zoom bound for a given fitted scale (founder-approved
 *  clarification, 2026-09-21 — see the "Range" line in
 *  `docs/internal/design/components/zoomable-view.md`): Fit must always be
 *  reachable, so when content needs LESS than the configured floor (`min`,
 *  25% by default) to fit, the fitted scale itself becomes the floor — the
 *  user can zoom out no further than Fit. Otherwise the floor stays at
 *  `min`. A non-finite or non-positive `fit` (fit not yet known) falls back
 *  to `min` unchanged. This is the ONE place the floor is computed; every
 *  opening scale, pill zoom-out step, "Fit" menu action, wheel, pinch and
 *  keyboard `-` in the media face routes through it, so there is exactly one
 *  floor rule, not a duplicated one. */
export function effectiveMinScale(fit: number, min: number = ZOOMABLE_VIEW_MIN_SCALE): number {
  if (!Number.isFinite(fit) || fit <= 0) return min
  return Math.min(min, fit)
}

/** Plain fit-to-frame scale for arbitrary content: the largest scale that
 *  keeps the whole content inside the frame on both axes. Works identically
 *  for content wider than it is tall or the reverse — there is no special
 *  case for "wide" vs "tall", only the binding axis. */
export function computeFittedScale(content: ZoomableSize, frame: ZoomableSize): number {
  if (content.width <= 0 || content.height <= 0 || frame.width <= 0 || frame.height <= 0) return 1
  return Math.min(frame.width / content.width, frame.height / content.height)
}

export interface OpeningScaleParams {
  content: ZoomableSize
  frame: ZoomableSize
  /** The smallest label in the content, measured in content-space px
   *  (i.e. before any scaling is applied) — e.g. a graph node's label font
   *  size, or a diagram's smallest text element as authored. */
  smallestLabelPx: number
  /** The 12px floor (D2). Only ever overridden by a unit test. */
  labelFloorPx?: number
}

/** D18's opening-size rule: "Fit all content if every label stays at or
 *  above the 12px floor at the fitted scale. Otherwise open at the smallest
 *  scale that keeps labels at 12px." "Fit" (the menu action / `0` shortcut)
 *  reproduces this exact computation, so opening and Fit always agree. */
export function computeOpeningScale({
  content,
  frame,
  smallestLabelPx,
  labelFloorPx = ZOOMABLE_VIEW_LABEL_FLOOR_PX,
}: OpeningScaleParams): number {
  const fitted = computeFittedScale(content, frame)
  if (smallestLabelPx <= 0) return clampZoomScale(fitted)
  const labelAtFitted = smallestLabelPx * fitted
  if (labelAtFitted >= labelFloorPx) return clampZoomScale(fitted)
  return clampZoomScale(labelFloorPx / smallestLabelPx)
}

/** Defect 1 fix ("Wide diagrams collapse... the size came from a percentage
 *  width"): resolves an SVG's INTRINSIC size from its `viewBox`, never from
 *  a percentage `width`/`height` attribute, which carries no size at all
 *  without a containing block. Accepts either a live element (the media
 *  viewer measures the rendered SVG) or raw markup (a diagram not yet
 *  mounted, e.g. before it is handed to the viewer). Returns `null` when
 *  no reliable intrinsic size can be resolved. */
export function resolveSvgIntrinsicSize(svg: SVGSVGElement | string): ZoomableSize | null {
  const attrs =
    typeof svg === 'string'
      ? (() => {
          const tag = svg.match(/<svg\b[^>]*>/i)?.[0] ?? ''
          return {
            width: tag.match(/\bwidth="([^"]*)"/i)?.[1] ?? null,
            height: tag.match(/\bheight="([^"]*)"/i)?.[1] ?? null,
            viewBox: tag.match(/\bviewBox="([^"]*)"/i)?.[1] ?? null,
          }
        })()
      : {
          width: svg.getAttribute('width'),
          height: svg.getAttribute('height'),
          viewBox: svg.getAttribute('viewBox'),
        }
  const { width: widthAttr, height: heightAttr, viewBox: viewBoxAttr } = attrs

  if (viewBoxAttr) {
    const parts = viewBoxAttr.trim().split(/[\s,]+/).map(Number)
    if (parts.length === 4 && parts.every((n) => Number.isFinite(n))) {
      const [, , vbWidth, vbHeight] = parts
      if (vbWidth > 0 && vbHeight > 0) return { width: vbWidth, height: vbHeight }
    }
  }

  // Fall back to an explicit pixel size ONLY — a bare number or a `px`
  // suffix, never a percentage: `width="100%"` is not an intrinsic size.
  const pxPattern = /^\s*[\d.]+(px)?\s*$/i
  if (widthAttr && heightAttr && pxPattern.test(widthAttr) && pxPattern.test(heightAttr)) {
    const width = parseFloat(widthAttr)
    const height = parseFloat(heightAttr)
    if (width > 0 && height > 0) return { width, height }
  }

  return null
}

// ── Keyboard shortcuts (D18: "+, −, 0 for fit and 1 for 100%") ─────────────

export interface ZoomableViewKeyboardHandlers {
  onZoomIn: () => void
  onZoomOut: () => void
  onFit: () => void
  onZoomTo100: () => void
}

/** Binds `+`/`−`/`0`/`1` to the pill's own actions. Attached as `onKeyDown`
 *  on the frame that owns the view (the canvas wrapper, or the media
 *  viewer surface) — shortcuts fire "while the view is focused" (D18), not
 *  globally, so they never shadow the same keys typed into an unrelated
 *  focused input elsewhere on the page. */
export function useZoomableViewKeyboard(
  handlers: ZoomableViewKeyboardHandlers,
): (event: React.KeyboardEvent) => void {
  const { onZoomIn, onZoomOut, onFit, onZoomTo100 } = handlers
  return React.useCallback(
    (event: React.KeyboardEvent) => {
      switch (event.key) {
        case '+':
        case '=':
          event.preventDefault()
          onZoomIn()
          break
        case '-':
        case '_':
          event.preventDefault()
          onZoomOut()
          break
        case '0':
          event.preventDefault()
          onFit()
          break
        case '1':
          event.preventDefault()
          onZoomTo100()
          break
        default:
          break
      }
    },
    [onZoomIn, onZoomOut, onFit, onZoomTo100],
  )
}

// ── ZoomPill — the one compact zoom control, shared by both faces ──────────

export interface ZoomPillProps {
  /** Current zoom as a fraction (1 = 100%). Displayed rounded to a percent. */
  zoom: number
  onZoomIn: () => void
  onZoomOut: () => void
  onFit: () => void
  onZoomTo100: () => void
  /** Graphs only (D18: "and, on graphs, Zoom to selection"). Omit entirely
   *  to leave it out of the menu — the media viewer face never passes it. */
  onZoomToSelection?: () => void
  zoomToSelectionDisabled?: boolean
  min?: number
  max?: number
  className?: string
  /** Accessible name for the `role="group"` wrapper. Defaults to "Zoom". */
  'aria-label'?: string
}

/** Built entirely from catalogued `IconButton`/`Button` and the catalogued
 *  `DropdownMenu` — no raw `<button>`, per the `controls/raw-button` lock.
 *  Every control keeps its own explicit `data-ds-action` (inherited from
 *  `Button`), so it already carries D17's 44px touch hit-region without any
 *  extra pointer-coarse sizing rule of its own. */
const ZoomPill = React.forwardRef<HTMLDivElement, ZoomPillProps>(function ZoomPill(
  {
    zoom,
    onZoomIn,
    onZoomOut,
    onFit,
    onZoomTo100,
    onZoomToSelection,
    zoomToSelectionDisabled = false,
    min = ZOOMABLE_VIEW_MIN_SCALE,
    max = ZOOMABLE_VIEW_MAX_SCALE,
    className,
    'aria-label': ariaLabel = 'Zoom',
  },
  ref,
) {
  const clamped = clampZoomScale(zoom, min, max)
  const percent = Math.round(clamped * 100)
  return (
    <div
      ref={ref}
      role="group"
      aria-label={ariaLabel}
      className={cn(
        // D17/D18: "44px hit regions without enlarging its chrome." The
        // buttons stay their normal size; each one's shared [data-ds-action]
        // hit region (src/styles/library.css) grows to 44px under a coarse
        // pointer regardless, and at the default 2px gap that 44px region
        // overflows into the next button's own real box — the later sibling
        // wins the overlap (same failure mode SegmentedControlItem's own
        // comment documents), so a tap meant for zoom-out lands on the
        // percent trigger instead. D17's own escape hatch is exactly this:
        // "Where an invisible hit region cannot fit without overlap, the
        // least disruptive visible adjustment is used" — widening the GAP,
        // not the buttons, is that adjustment; the pointer check
        // (zoomable-view-pointer) is the regression guard.
        'inline-flex items-center gap-[var(--space-0-5)] pointer-coarse:gap-[var(--space-2-5)] rounded-md border border-[var(--color-border)] bg-[var(--color-surface-2)] p-[var(--space-0-5)]',
        className,
      )}
    >
      <IconButton
        size="sm"
        variant="ghost"
        onClick={onZoomOut}
        disabled={clamped <= min}
        aria-label="Zoom out"
        title="Zoom out (−)"
        data-testid="zoomable-view-zoom-out"
      >
        <MagnifyingGlassMinus size={14} />
      </IconButton>
      {/* modal=false: Radix's default modal DropdownMenu marks every OTHER
          element on the page aria-hidden while open, including the zoom
          out/in buttons that sit right next to this trigger inside the same
          pill — they keep their normal tabIndex (Button always stamps one),
          so axe correctly flags "aria-hidden element must not be
          focusable". A percentage menu is a lightweight utility popup, not
          a page-blocking modal, so it should never hide its own siblings. */}
      <DropdownMenu modal={false}>
        <DropdownMenuTrigger asChild>
          <Button
            variant="ghost"
            size="sm"
            aria-label={`Zoom ${percent} percent — open zoom menu`}
            title="Zoom menu"
            data-testid="zoomable-view-percent"
            className="h-7 gap-[var(--space-1)] rounded px-[var(--space-1)] py-0 font-[var(--font-weight-regular)] text-[length:var(--type-caption-size)] tabular-nums text-[var(--color-muted)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-secondary)]"
          >
            {percent}%
            <CaretDown size={10} aria-hidden="true" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="center" data-testid="zoomable-view-menu">
          <DropdownMenuItem onSelect={onFit} data-testid="zoomable-view-menu-fit">
            Fit
            <DropdownMenuShortcut>0</DropdownMenuShortcut>
          </DropdownMenuItem>
          <DropdownMenuItem onSelect={onZoomTo100} data-testid="zoomable-view-menu-100">
            100%
            <DropdownMenuShortcut>1</DropdownMenuShortcut>
          </DropdownMenuItem>
          {onZoomToSelection && (
            <DropdownMenuItem
              onSelect={onZoomToSelection}
              disabled={zoomToSelectionDisabled}
              data-testid="zoomable-view-menu-selection"
            >
              Zoom to selection
            </DropdownMenuItem>
          )}
        </DropdownMenuContent>
      </DropdownMenu>
      <IconButton
        size="sm"
        variant="ghost"
        onClick={onZoomIn}
        disabled={clamped >= max}
        aria-label="Zoom in"
        title="Zoom in (+)"
        data-testid="zoomable-view-zoom-in"
      >
        <MagnifyingGlassPlus size={14} />
      </IconButton>
    </div>
  )
})
ZoomPill.displayName = 'ZoomPill'

// Canvas preset (React Flow face) — `zoomableCanvasFlowProps`,
// `useZoomableCanvasPill`, `contentExceedsFrame` — lives in
// `./zoomable-view-canvas.tsx`, deliberately NOT this file: this file is
// re-exported from the published `@omnipus/ui` package entry
// (`src/index.ts`), and `@xyflow/react` is an app-specific graphing
// dependency, not a Sovereign Deep foundation (see that file's header
// comment for the boundary this keeps).

// ── Media viewer face ───────────────────────────────────────────────────────

export interface ZoomableMediaController {
  scale: number
  isFit: boolean
  /** The effective lower bound for THIS content/frame pair — `min(min,
   *  fitScale)` (`effectiveMinScale`). A caller wiring its own `<ZoomPill>`
   *  (rather than `<ZoomableMediaSurface>`'s own toolbar-free frame) passes
   *  this as the pill's `min` prop so the pill's zoom-out button disables at
   *  the true floor and its percentage never displays a value the surface
   *  itself refuses to go below. */
  minScale: number
  zoomIn: () => void
  zoomOut: () => void
  zoomToFit: () => void
  zoomTo100: () => void
}

export interface UseZoomableMediaOptions {
  /** The content's intrinsic size — an image's natural size, or an SVG's
   *  size from `resolveSvgIntrinsicSize`. `null` while still unknown (e.g.
   *  an image that hasn't finished loading); the surface stays at `min`
   *  scale until it resolves. */
  contentSize: ZoomableSize | null
  /** Overrides the plain fit-to-frame computation — a diagram viewer passes
   *  `(frame) => computeOpeningScale({ content, frame, smallestLabelPx })`
   *  so "Fit" (and the opening scale) honour the 12px label floor. Defaults
   *  to `computeFittedScale(contentSize, frame)`. */
  getFitScale?: (frame: ZoomableSize) => number
  min?: number
  max?: number
  /** The Library video preview passes `disabled` — D18's scope extension
   *  gives video a full-screen action only, no zoom (see the spec's
   *  "Scope extended 2026-09-21" note). */
  disabled?: boolean
  onScaleChange?: (scale: number) => void
}

interface PointerPoint {
  x: number
  y: number
}

function pointerDistance(a: PointerPoint, b: PointerPoint): number {
  return Math.hypot(a.x - b.x, a.y - b.y)
}

/** The media-viewer face: pointer drag to pan, wheel (and ctrl-wheel /
 *  two-finger touch pinch) to zoom centred on the pointer, double-click to
 *  toggle fit/zoomed, `+ − 0 1` keyboard shortcuts, the shared 25%–400%
 *  range. A raw hook (rather than only the `ZoomableMediaSurface` component
 *  below) so a caller that already owns its own wrapper element — the PDF
 *  preview's existing scroll container, for instance — can bind these
 *  handlers directly without an extra DOM layer. */
export function useZoomableMedia(options: UseZoomableMediaOptions) {
  const {
    contentSize,
    getFitScale,
    min = ZOOMABLE_VIEW_MIN_SCALE,
    max = ZOOMABLE_VIEW_MAX_SCALE,
    disabled = false,
    onScaleChange,
  } = options

  const frameRef = React.useRef<HTMLDivElement | null>(null)
  const [frameSize, setFrameSize] = React.useState<ZoomableSize | null>(null)
  const [scale, setScaleState] = React.useState(min)
  const [translate, setTranslate] = React.useState({ x: 0, y: 0 })
  const pointersRef = React.useRef<Map<number, PointerPoint>>(new Map())
  const dragRef = React.useRef<{ startX: number; startY: number; startTx: number; startTy: number } | null>(null)
  const pinchRef = React.useRef<{ startDistance: number; startScale: number } | null>(null)
  const openedRef = React.useRef(false)

  // The RAW fit-to-frame scale, unclamped from below — the input the
  // effective-floor rule (`effectiveMinScale`) needs. `null` while the frame
  // or content size isn't known yet.
  const rawFit = React.useMemo(() => {
    if (!contentSize || !frameSize) return null
    return getFitScale ? getFitScale(frameSize) : computeFittedScale(contentSize, frameSize)
  }, [contentSize, frameSize, getFitScale])

  // The effective floor for THIS content/frame pair (founder-approved
  // clarification, 2026-09-21): `min(min, rawFit)`. Every clamp below
  // routes through this single value, not the raw `min` prop, so Fit is
  // always reachable.
  const effectiveMin = React.useMemo(() => effectiveMinScale(rawFit ?? min, min), [rawFit, min])

  const fitScale = React.useMemo(() => {
    if (rawFit === null) return clampZoomScale(1, effectiveMin, max)
    return clampZoomScale(rawFit, effectiveMin, max)
  }, [rawFit, effectiveMin, max])

  // Frame measurement. ResizeObserver where available; jsdom (unit tests)
  // has none, so the surface simply keeps its initial `null` frame size
  // there and callers assert against the pure `computeFittedScale`/
  // `computeOpeningScale` functions directly instead.
  React.useEffect(() => {
    const el = frameRef.current
    if (!el) return undefined
    const measure = () => setFrameSize({ width: el.clientWidth, height: el.clientHeight })
    measure()
    if (typeof ResizeObserver === 'undefined') return undefined
    const observer = new ResizeObserver(measure)
    observer.observe(el)
    return () => observer.disconnect()
  }, [])

  // D18 opening size: seed the scale at the fitted/floor-respecting scale
  // once, the first time both the frame and the content are known.
  React.useEffect(() => {
    if (openedRef.current || !frameSize || !contentSize) return
    setScaleState(fitScale)
    openedRef.current = true
  }, [frameSize, contentSize, fitScale])

  const setScale = React.useCallback(
    (next: number | ((prev: number) => number)) => {
      setScaleState((prev) => {
        const raw = typeof next === 'function' ? (next as (p: number) => number)(prev) : next
        const clamped = clampZoomScale(raw, effectiveMin, max)
        onScaleChange?.(clamped)
        return clamped
      })
    },
    [effectiveMin, max, onScaleChange],
  )

  const zoomIn = React.useCallback(() => setScale((s) => s * 1.25), [setScale])
  const zoomOut = React.useCallback(() => setScale((s) => s / 1.25), [setScale])
  const zoomToFit = React.useCallback(() => {
    setTranslate({ x: 0, y: 0 })
    setScale(fitScale)
  }, [setScale, fitScale])
  const zoomTo100 = React.useCallback(() => setScale(1), [setScale])

  const isFit = Math.abs(scale - fitScale) < 0.001

  // A NATIVE, non-passive listener — not React's `onWheel` prop. React
  // attaches its delegated wheel listener PASSIVELY at the root, so
  // `event.preventDefault()` inside a synthetic `onWheel` handler cannot
  // cancel anything: a ctrl-wheel trackpad pinch would zoom the media AND
  // the whole browser page at once, exactly what D18 forbids ("the page's
  // own zoom never changes"). `LibraryPdfPreview.tsx`'s own D-37 zoom
  // control hit this identical bug and fixed it the same way (see its
  // comment, "Claude review 2026-09-14, cut-list"). Registered directly on
  // the frame element with `{ passive: false }`, so `preventDefault()`
  // actually suppresses the browser's own gesture for this element.
  React.useEffect(() => {
    const frame = frameRef.current
    if (!frame) return undefined
    const onWheel = (event: WheelEvent) => {
      event.preventDefault()
      if (disabled) return
      const rect = frame.getBoundingClientRect()
      const pointerX = event.clientX - rect.left - rect.width / 2
      const pointerY = event.clientY - rect.top - rect.height / 2
      const factor = event.deltaY < 0 ? 1.15 : 1 / 1.15
      setScaleState((prev) => {
        const nextScale = clampZoomScale(prev * factor, effectiveMin, max)
        const delta = nextScale / prev
        setTranslate((t) => ({
          x: pointerX - (pointerX - t.x) * delta,
          y: pointerY - (pointerY - t.y) * delta,
        }))
        onScaleChange?.(nextScale)
        return nextScale
      })
    }
    frame.addEventListener('wheel', onWheel, { passive: false })
    return () => frame.removeEventListener('wheel', onWheel)
  }, [disabled, effectiveMin, max, onScaleChange])

  const handlePointerDown = React.useCallback(
    (event: React.PointerEvent<HTMLDivElement>) => {
      if (disabled) return
      ;(event.target as Element).setPointerCapture?.(event.pointerId)
      pointersRef.current.set(event.pointerId, { x: event.clientX, y: event.clientY })
      if (pointersRef.current.size === 2) {
        const [a, b] = Array.from(pointersRef.current.values())
        pinchRef.current = { startDistance: pointerDistance(a, b), startScale: scale }
        dragRef.current = null
      } else if (pointersRef.current.size === 1) {
        dragRef.current = { startX: event.clientX, startY: event.clientY, startTx: translate.x, startTy: translate.y }
      }
    },
    [disabled, scale, translate],
  )

  const handlePointerMove = React.useCallback(
    (event: React.PointerEvent<HTMLDivElement>) => {
      if (disabled || !pointersRef.current.has(event.pointerId)) return
      pointersRef.current.set(event.pointerId, { x: event.clientX, y: event.clientY })
      if (pointersRef.current.size === 2 && pinchRef.current) {
        const [a, b] = Array.from(pointersRef.current.values())
        const ratio = pointerDistance(a, b) / pinchRef.current.startDistance
        setScale(pinchRef.current.startScale * ratio)
        return
      }
      const drag = dragRef.current
      if (!drag) return
      setTranslate({ x: drag.startTx + (event.clientX - drag.startX), y: drag.startTy + (event.clientY - drag.startY) })
    },
    [disabled, setScale],
  )

  const endPointer = React.useCallback((event: React.PointerEvent<HTMLDivElement>) => {
    pointersRef.current.delete(event.pointerId)
    if (pointersRef.current.size < 2) pinchRef.current = null
    if (pointersRef.current.size === 0) dragRef.current = null
  }, [])

  const handleDoubleClick = React.useCallback(
    (event: React.MouseEvent<HTMLDivElement>) => {
      if (disabled) return
      if (!isFit) {
        zoomToFit()
        return
      }
      const frame = frameRef.current
      const target = clampZoomScale(fitScale * 2, effectiveMin, max)
      if (!frame) {
        setScale(target)
        return
      }
      const rect = frame.getBoundingClientRect()
      const pointerX = event.clientX - rect.left - rect.width / 2
      const pointerY = event.clientY - rect.top - rect.height / 2
      const delta = target / scale
      setTranslate((t) => ({ x: pointerX - (pointerX - t.x) * delta, y: pointerY - (pointerY - t.y) * delta }))
      setScale(target)
    },
    [disabled, isFit, zoomToFit, fitScale, effectiveMin, max, scale, setScale],
  )

  const onKeyDown = useZoomableViewKeyboard({ onZoomIn: zoomIn, onZoomOut: zoomOut, onFit: zoomToFit, onZoomTo100: zoomTo100 })

  const controller: ZoomableMediaController = { scale, isFit, minScale: effectiveMin, zoomIn, zoomOut, zoomToFit, zoomTo100 }

  // Spread directly onto the element that should own drag/wheel/pinch/
  // keyboard — `touchAction: 'none'` stops the browser's own native pinch
  // and scroll gestures from racing the pointer-event pinch above.
  const frameProps = {
    onPointerDown: handlePointerDown,
    onPointerMove: handlePointerMove,
    onPointerUp: endPointer,
    onPointerCancel: endPointer,
    onPointerLeave: endPointer,
    onDoubleClick: handleDoubleClick,
    onKeyDown,
    style: { touchAction: 'none' as const },
  }

  // NOT pre-built into a `React.CSSProperties` object here: the transform
  // is a runtime pan/zoom value, not a design-system spacing/color value,
  // but a `style={someVariable}` indirection is exactly what the spacing
  // and color locks (scripts/design-system-locks/{spacing,ts-colors}.mjs)
  // cannot statically prove safe and reject as "unsupported". Callers
  // build the inline `style={{ transform: ..., transformOrigin: ... }}`
  // object literal directly at their own JSX call site instead (see
  // `ZoomableMediaSurface` below) — an inline literal at the render site is
  // the provable shape those locks require.
  return { scale, translate, isFit, fitScale, effectiveMin, controller, frameProps, frameRef }
}

export interface ZoomableMediaSurfaceProps extends UseZoomableMediaOptions {
  children: React.ReactNode
  className?: string
  'aria-label'?: string
  /** Exposes the imperative controller to a `ZoomPill` living in the
   *  viewer's own toolbar, outside this surface's DOM (D18: "in the media
   *  viewer it lives in the viewer toolbar"). */
  controllerRef?: React.Ref<ZoomableMediaController>
}

/** The media-viewer face as a ready-to-drop-in surface: a focusable frame
 *  that owns pointer/wheel/keyboard zoom-and-pan and renders its children
 *  (an `<img>` or sanitized SVG markup) transformed inside it. */
const ZoomableMediaSurface = React.forwardRef<HTMLDivElement, ZoomableMediaSurfaceProps>(function ZoomableMediaSurface(
  { children, className, 'aria-label': ariaLabel, controllerRef, ...options },
  forwardedRef,
) {
  const { scale, translate, frameProps, frameRef, controller } = useZoomableMedia(options)
  React.useImperativeHandle(controllerRef, () => controller, [controller])

  const setRefs = React.useCallback(
    (node: HTMLDivElement | null) => {
      frameRef.current = node
      if (typeof forwardedRef === 'function') forwardedRef(node)
      else if (forwardedRef) (forwardedRef as React.MutableRefObject<HTMLDivElement | null>).current = node
    },
    [forwardedRef, frameRef],
  )

  return (
    <div
      {...frameProps}
      ref={setRefs}
      tabIndex={0}
      role="group"
      aria-label={ariaLabel ?? 'Zoomable content'}
      data-testid="zoomable-media-surface"
      data-scale={controller.scale}
      data-fit={controller.isFit}
      className={cn(
        'relative flex h-full w-full select-none items-center justify-center overflow-hidden',
        options.disabled ? '' : controller.isFit ? 'cursor-zoom-in' : 'cursor-grab active:cursor-grabbing',
        className,
      )}
    >
      <div
        style={{ transform: `translate(${translate.x}px, ${translate.y}px) scale(${scale})`, transformOrigin: 'center center' }}
        data-testid="zoomable-media-content"
      >
        {children}
      </div>
    </div>
  )
})
ZoomableMediaSurface.displayName = 'ZoomableMediaSurface'

export { ZoomPill, ZoomableMediaSurface }

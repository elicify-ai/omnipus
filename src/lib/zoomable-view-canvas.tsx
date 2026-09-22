// ZoomableView's canvas preset — the React Flow face of D18
// (`docs/internal/design/components/zoomable-view.md`), split out of
// `zoomable-view.tsx` deliberately: `zoomable-view.tsx` is part of the
// published `@omnipus/ui` package (re-exported from `src/index.ts`, whose
// dependency graph `scripts/design-system/public-api.test.mjs` proves stays
// "app-independent foundations" — no domain state, no app-specific
// libraries). React Flow (`@xyflow/react`) is exactly that kind of
// app-specific dependency (a graphing library the workspace task graph and
// team graph use, not a Sovereign Deep foundation), so this file is
// catalogued (`design-system/catalog.json`, classification `domain` — same
// precedent as `model-selector.tsx`/`AutoSaveIndicator.tsx`) but
// deliberately NOT re-exported from `src/index.ts` and NOT `@source`'d in
// `src/styles/library.css`. App code (GraphView, WorkspaceTeamGraph) imports
// it directly from `@/components/ui/zoomable-view-canvas`.

import * as React from 'react'
import type { FitViewOptions, ReactFlowState } from '@xyflow/react'
import { getNodesBounds, getViewportForBounds, useReactFlow, useStore } from '@xyflow/react'
import {
  ZOOMABLE_VIEW_MAX_SCALE,
  ZOOMABLE_VIEW_MIN_SCALE,
  clampZoomScale,
  effectiveMinScale,
  type ZoomableSize,
} from './zoomable-view'

/** Spread onto `<ReactFlow>`. Range and gesture behaviour D18 requires for a
 *  full-frame canvas: wheel zooms, pinch (touch or trackpad ctrl-wheel)
 *  zooms the content only — React Flow's own pinch/wheel handling never lets
 *  the page's own zoom change, which is what D18 requires — drag pans, and
 *  double-click zooms in at that point.
 *
 *  Deliberately does NOT include `minZoom`: the interactive zoom-out floor
 *  is content-dependent (2026-09-21 dynamic-floor fix — see
 *  `useZoomableCanvasPill`'s `min` below), so a STATIC value here would
 *  either block manual zoom-out from reaching a below-25% Fit on an
 *  oversized graph, or silently be overridden and misleadingly suggest a
 *  fixed floor to a reader. Every consumer spreads this object and THEN
 *  passes `minZoom={pill.min}` explicitly (`GraphView.tsx`,
 *  `WorkspaceTeamGraph.tsx`). */
export const zoomableCanvasFlowProps = {
  maxZoom: ZOOMABLE_VIEW_MAX_SCALE,
  zoomOnScroll: true,
  zoomOnPinch: true,
  panOnScroll: false,
  panOnDrag: true,
  zoomOnDoubleClick: true,
} as const

const selectZoom = (state: ReactFlowState) => state.transform[2]
const selectHasSelectedNodes = (state: ReactFlowState) => state.nodes.some((node) => node.selected)
// Two scalar selectors, not one object-returning selector: `useStore`
// (zustand) compares by reference by default, and an object literal
// selector would re-render on every store tick regardless of whether width
// or height actually changed.
const selectWidth = (state: ReactFlowState) => state.width
const selectHeight = (state: ReactFlowState) => state.height
// The live node array reference — NOT re-derived into a new array/object by
// this selector, so `useStore`'s default reference equality only re-renders
// when React Flow actually replaces the array (a real node add/remove/data
// change), never on an unrelated store tick (e.g. a pan). Needed so the
// dynamic floor (`effectiveFitMin` below) and the opening fit
// (`useZoomableCanvasOpeningFit`) recompute when the NODE SET changes even
// if the frame size does not — calling `getNodes()` inside a `useMemo` (the
// pre-fix shape) reads live data through a stable function identity, so the
// memo never re-ran on a node-only change.
const selectNodes = (state: ReactFlowState) => state.nodes

export interface UseZoomableCanvasPillOptions {
  /** Passed to `fitView` for both "Fit" and the opening `fitView` prop —
   *  callers should reuse the same object so opening and Fit agree exactly,
   *  as D18 requires ("Fit reproduces the opening frame identically"). */
  fitViewOptions?: FitViewOptions
  min?: number
  max?: number
}

/** Wires a `ZoomPill` (`./zoomable-view`) to a live `<ReactFlow>` instance.
 *  Must be called inside a `<ReactFlowProvider>` (both GraphView and
 *  WorkspaceTeamGraph already wrap their canvas in one). Returns props ready
 *  to spread onto `<ZoomPill>`, reactive to the instance's own zoom and
 *  selection state — not a one-shot read, so the pill's percentage tracks a
 *  wheel-zoom or a drag-to-pan-triggered rescale the same as a pill click. */
export function useZoomableCanvasPill(options: UseZoomableCanvasPillOptions = {}) {
  const { fitViewOptions, min = ZOOMABLE_VIEW_MIN_SCALE, max = ZOOMABLE_VIEW_MAX_SCALE } = options
  const zoom = useStore(selectZoom)
  const hasSelection = useStore(selectHasSelectedNodes)
  const width = useStore(selectWidth)
  const height = useStore(selectHeight)
  const storeNodes = useStore(selectNodes)
  const { zoomIn, zoomOut, fitView, getViewport, setViewport, getNodes } = useReactFlow()

  // The effective Fit floor for the CURRENT nodes and frame (same rule as
  // the media face's `effectiveMinScale`, founder-approved clarification
  // 2026-09-21): `getViewportForBounds` with `minZoom: 0` returns the TRUE
  // unclamped zoom `fitView` would compute — mirroring React Flow's own fit
  // math instead of re-deriving it, so it stays exact if that algorithm or
  // its default padding ever changes. Reads `storeNodes` (the reactive
  // `useStore` selector above), NOT `getNodes()` — `getNodes` has a stable
  // function identity across a node-only change, so a `useMemo` keyed on it
  // would never recompute when the node SET changed without the frame size
  // also changing, which is exactly the case that matters for an oversized
  // graph. This is the ONE computation the dynamic `<ReactFlow minZoom>`
  // prop, the pill's own zoom-out disable (`min` below) and the explicit
  // "Fit" action's per-call override (`onFit`) all share.
  const effectiveFitMin = React.useMemo(() => {
    if (!width || !height) return min
    if (storeNodes.length === 0) return min
    const bounds = getNodesBounds(storeNodes)
    if (bounds.width <= 0 || bounds.height <= 0) return min
    const { zoom: rawFit } = getViewportForBounds(bounds, width, height, 0, max, fitViewOptions?.padding ?? 0.1)
    return effectiveMinScale(rawFit, min)
  }, [width, height, storeNodes, max, fitViewOptions?.padding, min])

  const onZoomIn = React.useCallback(() => {
    void zoomIn()
  }, [zoomIn])
  const onZoomOut = React.useCallback(() => {
    void zoomOut()
  }, [zoomOut])
  // `minZoom` overrides the store's static floor for THIS call only —
  // `FitViewOptions.minZoom` is read by `fitViewport` in preference to the
  // store's own value, so "Fit" reaches the true fit even when it needs
  // less than the configured floor (25%) — same rule as the media face.
  // Manual zoom-out/wheel/pinch reach the SAME floor a different way: the
  // consumer passes this hook's own `min` (== `effectiveFitMin`, returned
  // below) as `<ReactFlow minZoom>`, which is what sets React Flow's live
  // scaleExtent — see `GraphView.tsx` / `WorkspaceTeamGraph.tsx`. The
  // opening fit is driven by `useZoomableCanvasOpeningFit` below instead of
  // the declarative `fitView` boolean prop, which read that same scaleExtent
  // before any per-graph measurement was possible.
  const onFit = React.useCallback(() => {
    void fitView({ ...fitViewOptions, minZoom: effectiveFitMin })
  }, [fitView, fitViewOptions, effectiveFitMin])
  const onZoomTo100 = React.useCallback(() => {
    setViewport({ ...getViewport(), zoom: 1 })
  }, [getViewport, setViewport])
  const onZoomToSelection = React.useCallback(() => {
    const selected = getNodes().filter((node) => node.selected)
    if (selected.length === 0) return
    void fitView({ ...fitViewOptions, nodes: selected })
  }, [getNodes, fitView, fitViewOptions])

  return {
    // Clamped by the effective floor, not the raw `min` — otherwise, once
    // "Fit" (above) legitimately sets the store's zoom below 25%, this
    // would clamp the DISPLAYED percentage back up to 25%, contradicting
    // what "Fit" just did.
    zoom: clampZoomScale(zoom, effectiveFitMin, max),
    onZoomIn,
    onZoomOut,
    onFit,
    onZoomTo100,
    onZoomToSelection,
    zoomToSelectionDisabled: !hasSelection,
    min: effectiveFitMin,
    max,
  }
}

/** Drives the OPENING fit imperatively (2026-09-21 dynamic-floor fix — the
 *  "Canvas preset, effective-floor gap" note in
 *  `docs/internal/design/components/zoomable-view.md`). React Flow's
 *  declarative `fitView` boolean prop performs its one-shot opening fit
 *  reading the STATIC `minZoom` on `<ReactFlow>` before any per-graph
 *  measurement is possible — so an oversized graph (more nodes than fit at
 *  25%) opened stuck at 25% with content cut off, instead of fitted. Call
 *  this hook INSTEAD of the `fitView` prop (drop the prop entirely), inside
 *  the same component that renders `<ReactFlow>` — it fires the imperative
 *  `fitView(fitViewOptions)` exactly once, the first render where both the
 *  frame size and the node set are known, the same one-shot semantics the
 *  declarative prop had, just computed AFTER measurement instead of before.
 *
 *  Deliberately uses the CALLER's OWN `fitViewOptions` unmodified — e.g.
 *  GraphView's `minZoom: 0.8` legibility floor (S3 UAT fix #26) — rather
 *  than `useZoomableCanvasPill`'s dynamic `effectiveFitMin`. The two floors
 *  answer different questions and are not meant to converge: the OPENING
 *  size is governed by D18's 12px-label-floor rule (the spec's "Opening
 *  size" paragraph) — a caller expresses its own measured legibility floor
 *  as `fitViewOptions.minZoom`, and a big graph may open at that floor,
 *  anchored at the start of the content, rather than fully fitted; the
 *  dynamic floor instead bounds how far the USER can manually zoom out
 *  (`<ReactFlow minZoom>`, the pill's own zoom-out disable) and the
 *  explicit "Fit" action (`onFit` above), both of which always reach the
 *  TRUE fit regardless of any legibility floor — clicking Fit (or `0`) must
 *  still be able to show the whole graph, even below that floor. */
export function useZoomableCanvasOpeningFit(fitViewOptions?: FitViewOptions): void {
  const { fitView } = useReactFlow()
  const width = useStore(selectWidth)
  const height = useStore(selectHeight)
  const storeNodes = useStore(selectNodes)
  const firedRef = React.useRef(false)

  React.useEffect(() => {
    if (firedRef.current) return
    if (!width || !height || storeNodes.length === 0) return
    firedRef.current = true
    void fitView(fitViewOptions)
  }, [width, height, storeNodes, fitView, fitViewOptions])
}

/** D18: "Graph canvases show a mini-map whenever content exceeds the
 *  frame." A pure comparison so a Phase-2 lane can call it with
 *  `getNodesBounds(getNodes())` and the canvas element's own
 *  `getBoundingClientRect()` without this component reaching into a
 *  specific graph's DOM itself. */
export function contentExceedsFrame(content: ZoomableSize, frame: ZoomableSize): boolean {
  return content.width > frame.width || content.height > frame.height
}

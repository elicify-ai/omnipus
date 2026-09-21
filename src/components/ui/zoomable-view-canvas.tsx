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
import { useReactFlow, useStore } from '@xyflow/react'
import { ZOOMABLE_VIEW_MAX_SCALE, ZOOMABLE_VIEW_MIN_SCALE, clampZoomScale, type ZoomableSize } from './zoomable-view'

/** Spread onto `<ReactFlow>`. Range and gesture behaviour D18 requires for a
 *  full-frame canvas: wheel zooms, pinch (touch or trackpad ctrl-wheel)
 *  zooms the content only — React Flow's own pinch/wheel handling never lets
 *  the page's own zoom change, which is what D18 requires — drag pans, and
 *  double-click zooms in at that point. */
export const zoomableCanvasFlowProps = {
  minZoom: ZOOMABLE_VIEW_MIN_SCALE,
  maxZoom: ZOOMABLE_VIEW_MAX_SCALE,
  zoomOnScroll: true,
  zoomOnPinch: true,
  panOnScroll: false,
  panOnDrag: true,
  zoomOnDoubleClick: true,
} as const

const selectZoom = (state: ReactFlowState) => state.transform[2]
const selectHasSelectedNodes = (state: ReactFlowState) => state.nodes.some((node) => node.selected)

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
  const { zoomIn, zoomOut, fitView, getViewport, setViewport, getNodes } = useReactFlow()

  const onZoomIn = React.useCallback(() => {
    void zoomIn()
  }, [zoomIn])
  const onZoomOut = React.useCallback(() => {
    void zoomOut()
  }, [zoomOut])
  const onFit = React.useCallback(() => {
    void fitView(fitViewOptions)
  }, [fitView, fitViewOptions])
  const onZoomTo100 = React.useCallback(() => {
    setViewport({ ...getViewport(), zoom: 1 })
  }, [getViewport, setViewport])
  const onZoomToSelection = React.useCallback(() => {
    const selected = getNodes().filter((node) => node.selected)
    if (selected.length === 0) return
    void fitView({ ...fitViewOptions, nodes: selected })
  }, [getNodes, fitView, fitViewOptions])

  return {
    zoom: clampZoomScale(zoom, min, max),
    onZoomIn,
    onZoomOut,
    onFit,
    onZoomTo100,
    onZoomToSelection,
    zoomToSelectionDisabled: !hasSelection,
    min,
    max,
  }
}

/** D18: "Graph canvases show a mini-map whenever content exceeds the
 *  frame." A pure comparison so a Phase-2 lane can call it with
 *  `getNodesBounds(getNodes())` and the canvas element's own
 *  `getBoundingClientRect()` without this component reaching into a
 *  specific graph's DOM itself. */
export function contentExceedsFrame(content: ZoomableSize, frame: ZoomableSize): boolean {
  return content.width > frame.width || content.height > frame.height
}

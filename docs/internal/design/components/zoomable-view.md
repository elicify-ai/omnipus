# zoomable-view

Status: **planned for the C3 repair batch; not yet built.** Rule: definition D18 (founder decision 2026-09-19). Owner: design-system. Planned source: `src/components/ui/zoomable-view.tsx`. A manifest, stories and tests are added when it is built, per the public-component coverage lock.

`ZoomableView` owns zooming and panning for content that is larger than its frame. It has two faces sharing one behaviour contract:

- **Canvas preset** for React Flow graphs: the workspace task graph (`src/components/workspaces/graph/GraphView.tsx`) and the workspace team graph (`src/components/workspaces/team/WorkspaceTeamGraph.tsx`).
- **Media viewer zoom** for the enlarge viewer used by chat images and Mermaid diagrams (`src/components/chat/image-lightbox.tsx`, opened through `MediaLightbox`).

## Contract

**Opening size.** The component computes the fitted scale for the content. If labels compute to at least 12px at that scale, it opens fitted. Otherwise it opens at the smallest scale that keeps labels at 12px, anchored at the start of the content: the first node, the top-left of a diagram, or the selected item. "Fit" reproduces the opening frame exactly. Every diagram, wide or tall, opens fitted in the media viewer. A diagram's intrinsic size is resolved from its viewBox, never from a percentage width without a containing block.

**Control.** One compact zoom pill: zoom out, the current percentage, zoom in. The percentage is a menu button offering Fit, 100% and, on graphs, Zoom to selection. The pill sits in the same corner on every canvas; in the media viewer it lives in the viewer toolbar. Every control has an accessible name. Keyboard shortcuts while the view is focused: `+`, `−`, `0` for fit and `1` for 100%, shown in tooltips. In touch mode (D17) its hit regions are 44px without enlarging its chrome.

**Gestures.**

- **Pinch.** A touch pinch and a trackpad pinch (a ctrl-wheel event) zoom the content only. The page's own zoom never changes during a pinch inside the view.
- **Drag.** Drag pans.
- **Wheel.** The mouse wheel zooms full-frame canvases and the media viewer, centred on the pointer. Inline previews in the chat stream never capture the wheel.
- **Double-click and double-tap.** On a canvas, they zoom in at that point. In the media viewer, they toggle between fitted and zoomed at that point.

**Range.** 25% to 400% on every surface.

**Mini-map.** Graph canvases show a mini-map whenever content exceeds the frame. Clicking it navigates: the single-pointer alternative to dragging.

**Closing.** Escape closes the media viewer and restores focus to the enlarge action that opened it.

## Defects this component must fix (in scope, not deferred)

1. **Wide diagrams collapse.** An enlarged wide Mermaid diagram currently collapses to about 300px wide at every viewport.
2. **Graph tab blocked at phone width.** At 390px, the Tasks "Graph" view tab tap is intercepted by the "Filter by agent" control. Confirm on a real device or by human emulation, then fix.

## Evidence required when built

- **Storybook interaction checks** cover the pill, the menu, the keyboard shortcuts, reset and accessible names.
- **Browser tests** measure the opening scale against the 12px label floor for small and large graphs and for wide and tall diagrams.
- **The in-context touch check** runs pinch, pan and opening scale on the real routes at phone and tablet sizes, and asserts the page's own zoom stays at 1 during a pinch.
- **Regression tests** cover both defects above.
- **Before-and-after measurements** are compared with `docs/internal/design/evidence/zoom-live-2026-09-19/`.

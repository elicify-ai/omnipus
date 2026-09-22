# zoomable-view

Status: **Phase 1 built (2026-09-21): the component, its two faces, and its publication.** Rule: definition D18 (founder decision 2026-09-19). Owner: design-system. Source: `src/components/ui/zoomable-view.tsx` (the published media-viewer face and the shared `ZoomPill`) and `src/components/ui/zoomable-view-canvas.tsx` (the React Flow canvas preset, catalogued but not published — see that file's header comment for why). Manifest, stories and tests: `design-system/manifests/zoomable-view.json`, `src/components/ui/zoomable-view.stories.tsx`, `src/components/ui/zoomable-view.test.tsx`. **No consumer screen is migrated in Phase 1** — GraphView, WorkspaceTeamGraph, the chat media viewer, and the Library previews below move onto it in Phase 2.

Scope extended 2026-09-21 by founder decision.

`ZoomableView` owns zooming and panning for content that is larger than its frame. It has two faces sharing one behaviour contract:

- **Canvas preset** for React Flow graphs: the workspace task graph (`src/components/workspaces/graph/GraphView.tsx`) and the workspace team graph (`src/components/workspaces/team/WorkspaceTeamGraph.tsx`).
- **Media viewer zoom** for the enlarge viewer used by chat images and Mermaid diagrams (`src/components/chat/image-lightbox.tsx`, opened through `MediaLightbox`), and — as of the 2026-09-21 scope extension — the Library image preview, the Library diagram (Mermaid) preview, and the Library PDF preview (`src/components/library/preview/LibraryImagePreview.tsx`, `LibraryMermaidPreview.tsx`, `LibraryPdfPreview.tsx`). The Library video preview (`LibraryVideoPreview.tsx`) gets a full-screen action only — no zoom pill, no pan/wheel/pinch zoom (see "Scope extension" below for why).

## Contract

**Opening size.** The component computes the fitted scale for the content. If labels compute to at least 12px at that scale, it opens fitted. Otherwise it opens at the smallest scale that keeps labels at 12px, anchored at the start of the content: the first node, the top-left of a diagram, or the selected item. "Fit" always shows all of the content. When the opening frame is fitted, the two are identical; when labels forced a larger opening scale, "Fit" goes further and shows everything, even with labels below 12px, because that is the one thing the reader is asking for (founder-approved clarification, 2026-09-21). Every diagram, wide or tall, opens fitted in the media viewer. A diagram's intrinsic size is resolved from its viewBox, never from a percentage width without a containing block.

**Control.** One compact zoom pill: zoom out, the current percentage, zoom in. The percentage is a menu button offering Fit, 100% and, on graphs, Zoom to selection. The pill sits in the same corner on every canvas; in the media viewer it lives in the viewer toolbar. Every control has an accessible name. Keyboard shortcuts while the view is focused: `+`, `−`, `0` for fit and `1` for 100%, shown in tooltips. In touch mode (D17) its hit regions are 44px without enlarging its chrome.

**Gestures.**

- **Pinch.** A touch pinch and a trackpad pinch (a ctrl-wheel event) zoom the content only. The page's own zoom never changes during a pinch inside the view.
- **Drag.** Drag pans.
- **Wheel.** The mouse wheel zooms full-frame canvases and the media viewer, centred on the pointer. Inline previews in the chat stream never capture the wheel.
- **Double-click and double-tap.** On a canvas, they zoom in at that point. In the media viewer, they toggle between fitted and zoomed at that point.

**Range.** 25% to 400% on every surface; when content needs less than 25% to fit, the fitted scale becomes the lower bound, so Fit is always reachable and content always opens fitted. (Founder-approved clarification, 2026-09-21.)

**Mini-map.** Graph canvases show a mini-map whenever content exceeds the frame. Clicking it navigates: the single-pointer alternative to dragging.

**Closing.** Escape closes the media viewer and restores focus to the enlarge action that opened it.

## Scope extension — 2026-09-21 founder decision

The founder extended D18's coverage beyond chat images/diagrams and the two React Flow graphs, to close the remaining zoomable/full-screen gaps in the Library preview pane:

- **Library image preview** (`LibraryImagePreview.tsx`) gets a new full-screen button that opens the same media viewer (`ZoomableMediaSurface` + `ZoomPill`) already used for chat images. Same 25%–400% range, same gestures, same opening-size rule.
- **Library diagram (Mermaid) preview** (`LibraryMermaidPreview.tsx`, which renders through the shared `MermaidDiagram`/`mermaid-renderer.tsx`) gets the same new full-screen button, opening the same media viewer used for chat Mermaid diagrams — including the viewBox-based opening-size fix (defect 1 below), since a Library-previewed diagram is exactly as likely to be wide or tall as a chat one.
- **Library video preview** (`LibraryVideoPreview.tsx`) gets a full-screen action only, with **no zoom**. Decision: use the **browser's native `<video>` full-screen** (the Fullscreen API on the `<video>` element), not `ZoomableMediaSurface`. Reasons: (1) the founder ruling is explicit that zooming a *playing* video is not standard player behaviour, so the zoom pill and its pan/wheel/pinch gestures are out of scope entirely, not just hidden; (2) native fullscreen keeps the browser's own play/pause/seek/volume chrome, which `ZoomableMediaSurface`'s pointer-drag-to-pan handlers would fight for the same pointer input (a drag on a video is normally a scrub or a volume gesture, never a pan); (3) it needs zero new chrome to build or maintain — the browser already ships it. `ZoomableMediaSurface`'s `disabled` option exists for exactly this "full-screen without zoom" shape, but for video specifically native fullscreen is the simpler, more standard choice and is what phase 2 should use.
- **Library PDF preview** (`LibraryPdfPreview.tsx`) moves its existing bespoke zoom control (its own `PDF_ZOOM_STEPS`/`nextPdfZoom`/D-37 CSS-`zoom` implementation) onto the same `ZoomPill` and the same 25%–400% range as every other surface. Its existing native, non-passive ctrl-wheel listener (registered directly with `{ passive: false }` — see that file's own D-37 comment) is the precedent `ZoomableMediaSurface`'s wheel handling in `zoomable-view.tsx` follows for the same reason: React's synthetic `onWheel` cannot reliably cancel a trackpad pinch.

## Defects this component must fix (in scope, not deferred)

1. **Wide diagrams collapse.** An enlarged wide Mermaid diagram currently collapses to about 300px wide at every viewport.
2. **Graph tab blocked at phone width.** At 390px, the Tasks "Graph" view tab tap is intercepted by the "Filter by agent" control. Confirm on a real device or by human emulation, then fix.

## Evidence

Phase 1 (component-level, delivered):

- **Storybook interaction checks** (`zoomable-view.stories.tsx`) cover the pill, the menu, the keyboard shortcuts, reset/Fit and accessible names.
- **Unit tests** (`zoomable-view.test.tsx`) cover the shared 25%–400% range clamp, the opening-scale 12px-label-floor rule for small/large/wide/tall content, the viewBox-based SVG intrinsic-size fix (defect 1 above), and that a ctrl-wheel (and a plain wheel) inside the media surface calls `preventDefault`.
- **Browser checks** (`tests/design-system/browser.spec.ts`, generated from `design-system/manifests/zoomable-view.json`) cover axe, keyboard, pointer (including the 44px touch hit-region, verified on `chromium-coarse-pointer`), reduced-motion, forced-colors, root-size, zoom and reflow, against the static Storybook build.

Phase 2 (route-level, still required before this closes):

- **The in-context touch check** runs pinch, pan and opening scale on the real routes (task graph, team graph, chat media viewer, Library previews) at phone and tablet sizes, and asserts the page's own zoom stays at 1 during a pinch.
- **Regression tests** cover both defects above, on the real consumer routes.
- **Before-and-after measurements** are compared with `docs/internal/design/evidence/zoom-live-2026-09-19/`.
- **Canvas preset, effective-floor gap — closed (2026-09-21).** `useZoomableCanvasPill`'s "Fit" action (and the `0` shortcut) already reached the true fit below 25% via a per-call `fitView({ minZoom })` override. The two related gaps this note tracked are now closed: `zoomableCanvasFlowProps` no longer carries a static `minZoom` at all; `GraphView.tsx`/`WorkspaceTeamGraph.tsx` each pass `minZoom={pill.min}` — `useZoomableCanvasPill`'s own reactive floor — to `<ReactFlow>` directly, so manual zoom-out, wheel and pinch (which read React Flow's live store `minZoom`) reach the true fit the same as "Fit" does. The opening fit is driven imperatively by the new `useZoomableCanvasOpeningFit` hook (`zoomable-view-canvas.tsx`) instead of the declarative `fitView`/`fitViewOptions` boolean prop, which read that same static floor before any per-graph measurement was possible. Each consumer's own legibility floor (GraphView's `minZoom: 0.8`, S3 UAT fix #26) still governs the OPENING size per the "Opening size" rule above — a caller's own `fitViewOptions.minZoom` is passed to `useZoomableCanvasOpeningFit` unmodified — while the interactive floor and the explicit "Fit" action both always reach the TRUE fit regardless of that legibility floor. Proof: `zoomable-view-canvas.test.tsx` (dynamic-floor unit coverage) and `zoomable-view.stories.tsx`'s `HugeGraphOpensFittedAndZoomsBelowFloor` real-browser story.

// LibraryPdfPreview — renders a PDF *inside the SPA* with PDF.js, as a React
// component alongside LibraryImagePreview / LibraryVideoPreview
// (ADR-067 D15.3, spec FR-018).
//
// The point of this file, stated once: **a PDF never becomes a browser
// document.** The bytes are fetched over the authenticated Library endpoint —
// which still serves `Content-Disposition: attachment`, exactly as today,
// because a disposition header governs navigation, not `fetch()` — and are
// handed to PDF.js, which draws them into a <canvas> we own. There is no
// `<iframe src="…pdf">`, no browser PDF viewer, no download card. That is why
// `.pdf` is deliberately ABSENT from the inline allow-list in spec §10.4.
//
// ── Hardening (D15.7). These are requirements, not defaults. ────────────────
// Rendering PDFs moves untrusted parsing onto the authenticated SPA origin,
// next to the session cookie. Four controls, and the reason each is shaped the
// way it is:
//
//  1. XFA disabled — `enableXfa: false` on getDocument. A real, live option in
//     6.2.108 (verified in types/src/display/api.d.ts): the assertion can fail.
//
//  2. PDF scripting: the interpreter is NOT SHIPPED. `pdf.sandbox*.mjs` is the
//     engine that runs a PDF's own JavaScript; vite.config.ts never emits it
//     and fails the build if it ever appears. Absence beats a flag someone can
//     flip back.
//     **`enableScripting: false` is deliberately not passed here, and that is
//     not an oversight.** Measured against 6.2.108: `enableScripting` is not a
//     `getDocument` parameter at all — it is an `AnnotationLayer.render`
//     parameter (types/src/display/annotation_layer.d.ts). The one place this
//     file DOES construct an AnnotationLayer (Edit mode, below) passes
//     `enableScripting: false` explicitly there — where PDF.js actually reads
//     it — rather than on `getDocument`, which would set a key PDF.js ignores.
//
//  3. No `isEvalSupported` — verified, that option no longer EXISTS in
//     6.2.108 (zero occurrences in build/pdf.mjs, build/pdf.worker.mjs and
//     every published .d.ts). Do not add it back. The no-eval property is
//     asserted against the shipped artefact and enforced at runtime by the
//     SPA CSP having no 'unsafe-eval'.
//
//  4. Parsing stays on a REAL worker (FR-019c). PDF.js's own worker bootstrap
//     silently falls back to main-thread parsing on any failure — `warn(
//     "Setting up fake worker.")` is the only symptom. So we construct the
//     Worker ourselves and hand PDF.js the port: `PDFWorker#initializeFromPort`
//     has no fallback branch. If the Worker cannot be constructed (e.g. a CSP
//     without `worker-src`), we surface a visible error instead of degrading
//     into the thing the requirement forbids.
//
// ── Read-only BASE render (NB-17) — unchanged by Edit mode ─────────────────
// The canvas itself always renders with `annotationMode: ENABLE` (never
// ENABLE_FORMS/ENABLE_STORAGE) and `isEditing: false` — it draws a flat
// picture of the document's CURRENT appearance streams, exactly as it always
// has. That picture never changes live as fields are filled; entering Edit
// mode below overlays a SEPARATE, real PDF.js AnnotationLayer on top of it.
//
// ── Edit mode (library-b-c-design-2026-09-07.md "B") ────────────────────────
// Builds on ADR-067 D15.3's measured feasibility: `pdfjs-dist`'s
// `annotationStorage` + `PDFDocumentProxy.saveDocument()` round-trip a filled
// AcroForm field and a drawn signature into real, standard PDF bytes (tested
// against pdfjs-dist 6.2.108, verified by re-rendering the saved file in an
// engine unrelated to PDF.js). Two independent pieces share one
// `annotationStorage` (`doc.annotationStorage`, the SAME object `saveDocument`
// reads):
//   - AcroForm fields — a real `pdfjs.AnnotationLayer` (the sanctioned,
//     interactive form-widget renderer; NOT the editor-UI-manager machinery,
//     see pdfInkAnnotation.ts's header for why) mounted per page, only while
//     `mode === 'edit'`. Widget elements wire their own `input`/`change`
//     listeners straight into `annotationStorage.setValue` — this component
//     does not touch field values directly.
//   - A drawn signature — LibrarySignaturePad captures freehand strokes in
//     its own fixed pixel space; `placeSignatureOnViewport` below converts
//     them to PDF-space points via the target page's own
//     `PageViewport.convertToPdfPoint`, and `buildInkAnnotationEntry`
//     (pdfInkAnnotation.ts) turns those into the exact plain-object shape
//     PDF.js's worker expects for a brand-new `/Subtype /Ink` annotation.
// Honest states (library-b-c-design-2026-09-07.md): a PDF with no AcroForm
// fields (`doc.getFieldObjects()` resolves null/empty) mounts no annotation
// layer and says so, but the signature affordance is offered regardless. A
// save failure surfaces the reason (AutoSaveIndicator + toast) and changes
// nothing else — `annotationStorage` is untouched by a failed
// `saveDocument()`/PUT, so every entered value and placed signature is still
// there for a retry.
// Out of scope (unchanged from the ADR): PKI/cryptographic signatures, XFA
// forms, agent-driven filling.
//
// ── How a load reports failure, and who ends the worker ────────────────────
// Three rules, each fixing a failure that used to be completely invisible:
//
//  1. **`failLoad` is the ONE way this load reports a failure.** It is
//     reachable from the async chain, from the worker's `error` listener at
//     any point in the component's life, and from the first-page watchdog;
//     it is idempotent. The listener outlives the `Promise.race` that used to
//     be the only consumer of its rejection, so "reject and hope someone is
//     listening" left a worker death after opening with no effect at all.
//
//  2. **`status === 'ready'` means a page is genuinely on screen.** It used
//     to flip as soon as the DOCUMENT opened, which made a wedged worker show
//     an empty, visible container — a white box a reader cannot tell from a
//     blank page. A first-page watchdog (PDF_FIRST_PAGE_TIMEOUT_MS) bounds
//     the wait, because a wedge throws nothing for a `catch` to catch.
//
//  3. **This component ends the Worker thread it started.** `loadingTask
//     .destroy()` does NOT terminate a caller-supplied worker, and does not
//     even settle when that worker has stopped replying — both measured
//     against pdfjs-dist 6.2.108, in full at the load effect's cleanup.
//
// ── Laziness (FR-018) ──────────────────────────────────────────────────────
// `pdfjs-dist` is reached ONLY through the dynamic import below, so it stays
// out of the initial payload even if a parent imports this component eagerly.
// vite.config.ts gives that chunk the name `pdfjs`, because a bare dynamic
// import produces a hash-named chunk and a name-matching laziness test would
// then match nothing and pass.

import { useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { SpinnerGap, Eye, PencilSimple, FloppyDisk, Signature, X } from '@phosphor-icons/react'
import { ApiError, downloadLibraryFileVersioned, putLibraryContentBinary, isLibraryVersionConflict } from '@/lib/api'
import type { LibraryEntry } from '@/lib/api'
import type { AutoSaveStatus } from '@/hooks/useAutoSave'
import { AutoSaveIndicator } from '@/components/ui/AutoSaveIndicator'
import { cn } from '@/lib/utils'
import { useUiStore } from '@/store/ui'
import { PreviewHeaderPortal } from './previewHeaderSlot'
import { SegmentedControl, SegmentedControlItem } from '@/components/ui/segmented-control'
import { IconButton } from '@/components/ui/icon-button'
import { Button } from '@/components/ui/button'
import { ZoomPill, clampZoomScale, useZoomableViewKeyboard } from '@/components/ui/zoomable-view'
import { setLibraryEditorDirty } from './unsavedGuard'
import { getLibraryErrorMessage } from '../libraryErrorMessage'
import { LibrarySignaturePad, SIGNATURE_PAD_WIDTH, SIGNATURE_PAD_HEIGHT } from './LibrarySignaturePad'
import { buildInkAnnotationEntry } from './pdfInkAnnotation'
import type { SignatureStroke } from './pdfInkAnnotation'
import { uint8ArrayToBase64 } from './pdfBinaryEncoding'
import { PDF_WORKER_POOL_CEILING } from './pdfWorkerPool'
import type { LibraryPreviewVariant } from './libraryPreviewVariant'
import { usePdfLoadEffect } from './LibraryPdfPreview.loadEffect'

// Type-only: erased at build time, so it does not pull pdfjs-dist into the
// eager module graph.
import type { PDFDocumentProxy, PDFPageProxy, PageViewport } from 'pdfjs-dist'

/** URL prefix the PDF.js runtime assets and worker are served from. Mirrors
 *  `PDFJS_ASSET_PREFIX` in vite.config.ts, which emits them there. The gateway
 *  MUST return a real 404 under this prefix rather than its index.html
 *  fallback (FR-018b) — otherwise a missing character map arrives as HTTP 200
 *  HTML, the page renders blank, and nothing names the cause. */
export const ASSET_BASE = `${import.meta.env.BASE_URL}pdfjs/`

/** Human name per asset directory, used in the error a missing one produces.
 *  Each failure mode below is the SILENT one this naming exists to end. */
const ASSET_DIR_MEANING: Record<string, string> = {
  cmaps: 'character maps (Japanese, Chinese and Korean PDFs render blank without them)',
  standard_fonts: 'the 14 base fonts (a PDF that embeds no fonts renders with wrong metrics)',
  wasm: 'the JPEG 2000 / JBIG2 decoders (a scanned PDF loses its images)',
  iccs: 'colour profiles',
}

/** Rendering scale bounds. Below 0.25 text is unreadable; above 4 a large page
 *  exceeds browsers' canvas area limits and renders as a blank bitmap. */
export const MIN_SCALE = 0.25
export const MAX_SCALE = 4

/** The reader's own magnification (UAT D-37), applied to the pages container
 *  as a CSS `zoom` on top of the automatic fit-to-width render scale
 *  (`MIN_SCALE`/`MAX_SCALE` above clamp that render scale, a separate
 *  concern: how many pixels a page's canvas is drawn with). It uses the
 *  shared `ZoomPill` and the shared 25%-400% range (D18,
 *  docs/internal/design/components/zoomable-view.md).
 *
 *  The CSS `zoom` is layout only — every page's element and canvas keep the
 *  SAME fit-width CSS pixel size at every zoom level, so the text layer,
 *  annotation layer and signature overlays (all positioned in that same
 *  CSS-pixel space) stay aligned automatically as the whole container is
 *  magnified uniformly. What DOES change with zoom is the canvas's backing
 *  resolution: `targetRasterScale` above raises the device-pixels-per-CSS-
 *  pixel a page is drawn at as zoom rises past 100%, so text stays sharp
 *  rather than being magnified from a fixed bitmap. Re-rasterising a page
 *  keeps the SAME loaded `PDFDocumentProxy` and `PDFPageProxy` (`docRef`,
 *  `pagesRef`) — there is no re-fetch and no reopen — and is scoped to
 *  pages currently near the viewport (`nearViewportPageNumbers`), debounced
 *  while the reader is actively zooming or scrolling
 *  (`RERASTER_DEBOUNCE_MS`), and capped in backing-store size
 *  (`MAX_CANVAS_DIMENSION_PX`/`MAX_CANVAS_PIXELS`) so a long document never
 *  re-rasterises every page at once and a single page never grows without
 *  bound. `rasterizePage` draws the new resolution into a fresh, detached
 *  canvas and swaps it in only once rendering finishes, so the previously
 *  magnified (softer but complete) picture stays on screen the whole time —
 *  never a blank page while the sharp one is being drawn.
 *
 *  Fit and 100% are the same action here (see `zoomToFit`/`zoomTo100`):
 *  the base render is already fitted, and this zoom multiplies it. */
const PDF_READER_ZOOM_DEFAULT = 1

/** UAT D-63 (2026-09-13): the page a reader is LOOKING AT — the first
 *  rendered page whose bottom edge is below the container's scroll top — so
 *  the signature dialog defaults to it rather than to the last page of the
 *  document. Falls back to 1 when nothing is rendered yet.
 *
 *  Claude review 2026-09-14, cut-list: measured with getBoundingClientRect
 *  against the CONTAINER's own rect, never with offsetTop. offsetTop is
 *  relative to the nearest positioned ancestor, which in this layout is a
 *  wrapper above the preview header — dozens of pixels above the scroll
 *  container — so comparing it against scrollTop over-reported every page
 *  and the answer landed on an earlier page than the one on screen. The
 *  rect pair is also correct under the D-37 CSS `zoom`, where offsetTop's
 *  coordinate space is not the container's. */
export function firstVisiblePdfPage(container: HTMLElement): number {
  const pages = container.querySelectorAll<HTMLElement>('[data-page-number]')
  const viewTop = container.getBoundingClientRect().top
  for (const el of pages) {
    if (el.getBoundingClientRect().bottom > viewTop) {
      const n = Number(el.getAttribute('data-page-number'))
      if (Number.isInteger(n) && n >= 1) return n
    }
  }
  return 1
}

/** Cap the canvas backing store at 2x for the UNZOOMED (100%) render. Beyond
 *  that the memory cost per page grows faster than the visible gain on a 3x
 *  display. The reader's own zoom (`PDF_READER_ZOOM_DEFAULT` below) is layered
 *  on top of this, separately capped by `MAX_CANVAS_DIMENSION_PX`/
 *  `MAX_CANVAS_PIXELS`. */
export const MAX_PIXEL_RATIO = 2

/** Ceilings on a single page canvas's backing-store resolution once the
 *  reader's zoom (not just device pixel ratio) is included. Two independent
 *  caps, because either alone has a blind spot: the area cap alone lets a
 *  very tall, narrow page reach an extreme single-side dimension before the
 *  area limit engages; the dimension cap alone lets a very wide, short page
 *  reach an extreme pixel count while staying under either side limit.
 *  `MAX_CANVAS_DIMENSION_PX` sits well under the ~16,384px per-side ceiling
 *  the engines this product ships on enforce; `MAX_CANVAS_PIXELS` is Safari's
 *  hard canvas-area limit on iOS and iPadOS (4096 * 4096 = 16,777,216 pixels,
 *  about 64MB of RGBA) — a larger canvas silently renders blank there. On a 2x display a typical
 *  fit-width page keeps full detail up to about 200% zoom; above that the
 *  cap holds it near 5x its CSS size (a measured 400% crop keeps 98% of the
 *  uncapped sharpness), and a large page or ultra-wide pane never reaches
 *  for hundreds of megabytes — and only pages visible or near the scroll viewport are ever
 *  rasterised at these ceilings (`rasterizePage`/`nearViewportPageNumbers`
 *  below) — never every page of a long document at once. */
export const MAX_CANVAS_DIMENSION_PX = 8192
export const MAX_CANVAS_PIXELS = 4096 * 4096

/** Computes the backing-store scale (device pixels per CSS pixel of the
 *  page's FIT-WIDTH viewport — the same viewport `pageEl`/`canvas.style`
 *  size, unaffected by zoom) a page's canvas should be drawn at for a given
 *  reader zoom level. At `zoom <= 1` this is exactly `deviceRatio` — the
 *  same number every page has always rendered at — so 100% and below are
 *  byte-for-byte the pre-existing behaviour. Above 100% it grows with zoom
 *  so the canvas keeps roughly one device pixel per screen pixel at the
 *  CURRENT magnification, clamped so a page's backing store never exceeds
 *  `MAX_CANVAS_DIMENSION_PX` per side or `MAX_CANVAS_PIXELS` total — the
 *  clamp can only ever raise the result above `deviceRatio`, never below it,
 *  so it can only soften a very large page at extreme zoom, never regress a
 *  small one below what it already rendered at. */
export function targetRasterScale(fitViewportWidth: number, fitViewportHeight: number, zoom: number, deviceRatio: number): number {
  if (zoom <= 1) return deviceRatio
  const baseW = fitViewportWidth * deviceRatio
  const baseH = fitViewportHeight * deviceRatio
  const byDimension = Math.min(MAX_CANVAS_DIMENSION_PX / baseW, MAX_CANVAS_DIMENSION_PX / baseH)
  const byArea = Math.sqrt(MAX_CANVAS_PIXELS / (baseW * baseH))
  const zoomFactor = Math.max(1, Math.min(zoom, byDimension, byArea))
  return deviceRatio * zoomFactor
}

/** How long to wait, in ms, after the LAST zoom step (wheel tick, pinch
 *  frame, or pill click) or scroll movement before re-rasterising
 *  near-viewport pages at the new target resolution (`rasterizePage` below).
 *  A wheel/pinch gesture fires many steps in a row; without this each one
 *  would start its own redraw, competing for the same canvas. The magnified
 *  (but not yet re-rasterised) picture stays on screen throughout — nothing
 *  is blanked while this timer is pending. */
export const RERASTER_DEBOUNCE_MS = 150

/** How far beyond the container's own visible rectangle, in CSS pixels, a
 *  page still counts as "near" and gets proactively re-rasterised — roughly
 *  one screenful, so the next page a reader scrolls to is already sharp
 *  rather than rasterising on arrival. Pages further away keep whatever
 *  resolution they last had until they come this close. */
const NEAR_VIEWPORT_MARGIN_PX = 600

/** The page numbers whose element is within `NEAR_VIEWPORT_MARGIN_PX` of the
 *  container's own visible rectangle — the set `rasterizePage` calls are
 *  scoped to, so a long document never re-rasterises every page at once for
 *  one zoom step. Uses `getBoundingClientRect`, the same measurement
 *  `firstVisiblePdfPage` above already relies on being correct under the
 *  D-37 CSS `zoom` (see that function's own comment) — jsdom's all-zero
 *  rects make every page "near" by this same math, which is the safe
 *  default for a test environment that never lays anything out. */
function nearViewportPageNumbers(container: HTMLElement, pageEls: Map<number, HTMLDivElement>): number[] {
  const containerRect = container.getBoundingClientRect()
  const top = containerRect.top - NEAR_VIEWPORT_MARGIN_PX
  const bottom = containerRect.bottom + NEAR_VIEWPORT_MARGIN_PX
  const result: number[] = []
  for (const [n, el] of pageEls) {
    const rect = el.getBoundingClientRect()
    if (rect.bottom >= top && rect.top <= bottom) result.push(n)
  }
  return result
}

/** Page width, in CSS pixels, used ONLY when this component's own box
 *  genuinely measures zero — it is not laid out yet, or its parent is
 *  detached. It is a guess, and a guess that renders plausibly is the exact
 *  shape of a defect nobody reports, so whenever it is used the pages
 *  container carries `data-width-source="fallback"` (see
 *  `measureRenderWidth`). */
const FALLBACK_RENDER_WIDTH = 800

/** How long one load may go without putting its FIRST page on screen before
 *  it is reported as a failure.
 *
 *  Why this exists: every OTHER failure in this file is an error someone
 *  throws. A worker that WEDGES — out of memory inside the JPEG 2000 / JBIG2
 *  wasm decoders, or throttled to a standstill in a background tab — throws
 *  nothing, resolves nothing and emits no `error` event. Without a deadline
 *  the component waits for it forever, which is indistinguishable from a slow
 *  network to a reader and indistinguishable from success to a test. 45s is
 *  deliberately far above any real first-page render (a 200MB scanned
 *  document opens in single-digit seconds on a throttled CPU) so it only ever
 *  fires on a genuine wedge. */
export const PDF_FIRST_PAGE_TIMEOUT_MS = 45_000

/** How long the byte DOWNLOAD alone may take before it is reported as a
 *  failure — separate from `PDF_FIRST_PAGE_TIMEOUT_MS` above
 *  (SILENT-FAILURES-pdf-pool.md finding 3). A download is bounded by the
 *  network, not by whether the PARSING worker is wedged, and the two used to
 *  share one clock: a large, legitimate PDF on a slow connection could be cut
 *  off mid-download by the SAME 45s deadline, with an error that blamed "the
 *  parsing worker" for a stage it had not even reached. Generous relative to
 *  the first-page deadline for exactly that reason — a slow but honest
 *  download can legitimately take longer than 45s without the worker being
 *  involved at all. */
export const PDF_DOWNLOAD_TIMEOUT_MS = 120_000

/** Test-only override for `PDF_DOWNLOAD_TIMEOUT_MS`, mirroring
 *  `__setPdfFirstPageTimeoutForTests` below. Production never calls this;
 *  `null` restores the real value. */
let downloadTimeoutOverrideMs: number | null = null
export function __setPdfDownloadTimeoutForTests(ms: number | null): void {
  downloadTimeoutOverrideMs = ms
}
export function downloadTimeoutMs(): number {
  return downloadTimeoutOverrideMs ?? PDF_DOWNLOAD_TIMEOUT_MS
}

/** How long a SINGLE PDF.js runtime-asset request may hang before the probe
 *  gives up (SILENT-FAILURES-pdf-pool.md finding 4, "one hung asset request
 *  breaks every PDF"). `fetchAsset` used to have no timeout at all, so one
 *  request that never answered wedged the shared, page-wide `assetProbe`
 *  memo forever — every PDF on the page failed only after the (then-shared)
 *  45s watchdog, and "Try again" waited on the identical stuck promise,
 *  since the memo only clears on rejection. */
const PDF_ASSET_FETCH_TIMEOUT_MS = 15_000

/** Test-only override for `PDF_ASSET_FETCH_TIMEOUT_MS`. Production never
 *  calls this; `null` restores the real value. */
let assetFetchTimeoutOverrideMs: number | null = null
export function __setPdfAssetFetchTimeoutForTests(ms: number | null): void {
  assetFetchTimeoutOverrideMs = ms
}
function assetFetchTimeoutMs(): number {
  return assetFetchTimeoutOverrideMs ?? PDF_ASSET_FETCH_TIMEOUT_MS
}

/** How long `PDFDocumentLoadingTask.destroy()` gets to shut the transport
 *  down politely before the Worker thread is terminated outright. `destroy()`
 *  ends by asking the worker to `Terminate` and AWAITING its reply
 *  (`WorkerTransport.destroy` in build/pdf.mjs 6.2.108), so a worker that has
 *  stopped replying makes that promise never settle — the grace timeout is
 *  what stops "tear down politely" from meaning "never tear down". */
export const WORKER_TERMINATE_GRACE_MS = 2000

/** The width a page should be rendered at, and whether that number was
 *  MEASURED or guessed.
 *
 *  Measured off the component ROOT, never off the pages container: the
 *  container carries the `hidden` class (`display:none`) until the first page
 *  is on screen, and CSSOM reports `clientWidth === 0` for a `display:none`
 *  element — so measuring it returns 0 on every first load and the fallback
 *  silently becomes the ONLY width this component ever renders at. The root is
 *  never hidden, so it is the real box. */
export function measureRenderWidth(container: HTMLElement): { width: number; fallback: boolean } {
  const width = container.parentElement?.clientWidth ?? 0
  if (width > 0) return { width, fallback: false }
  return { width: FALLBACK_RENDER_WIDTH, fallback: true }
}

/** Test-only override for `PDF_FIRST_PAGE_TIMEOUT_MS`, so a wedged-load test
 *  does not have to wait 45 real seconds (and does not have to install fake
 *  timers, which React Testing Library's `waitFor` does not detect under
 *  Vitest). Production never calls this; `null` restores the real value. */
let firstPageTimeoutOverrideMs: number | null = null
export function __setPdfFirstPageTimeoutForTests(ms: number | null): void {
  firstPageTimeoutOverrideMs = ms
}
export function firstPageTimeoutMs(): number {
  return firstPageTimeoutOverrideMs ?? PDF_FIRST_PAGE_TIMEOUT_MS
}

/** True for the cancellation every abort path in this file produces, whatever
 *  class it arrives as: `fetch` rejects with a `DOMException`, but a helper
 *  that wraps or re-creates one may not. Matching on the NAME is what the
 *  platform itself documents as the discriminator. */
export function isAbortError(err: unknown): boolean {
  return typeof err === 'object' && err !== null && (err as { name?: string }).name === 'AbortError'
}

interface LibraryPdfPreviewProps {
  workspaceId: string
  entry: LibraryEntry
  /** `pane` (default) fills the Library preview pane's own bounds; `inline`
   *  sizes to a bounded, self-determined box so a PDF embed sits in a note's
   *  text flow instead of claiming the pane's height. Layout only
   *  (EMB-027/028, libraryPreviewVariant.ts) — worker pooling, Edit mode and
   *  every other behaviour below are identical on both. */
  variant?: LibraryPreviewVariant
  /** ADR-083 embedded-content spec, Step 6 / EMB-105, US-12 AS-4 — a
   *  `![[doc.pdf#page=3]]` embed fragment: render ONLY this 1-based page,
   *  never the whole document. This is the SAME open/parse path as every
   *  other PDF (the same worker-pool lease, the same asset probe, the same
   *  Worker construction — EMB-032's "ride the pool, do not spawn a second
   *  worker" applies here unchanged), so the only thing this prop changes is
   *  WHICH of the already-opened document's pages get a canvas. A fragment
   *  naming a page outside the document's range is a real, honest failure —
   *  it surfaces through the same `status === 'error'` state every other
   *  open failure uses, naming the page and the document's real page count,
   *  never a blank box. Edit mode (mode toggle, save, signature) has no
   *  header to reach it from on a fragment (there is no
   *  `PreviewHeaderSlotProvider` outside the pane — see previewHeaderSlot.tsx),
   *  so it is left wired but unreachable rather than special-cased here. */
  pageFragment?: number
}

class PdfAssetError extends Error {}

/** One probe per runtime asset directory, memoised for the page's lifetime.
 *  Reset on failure so a fixed deployment recovers without a reload. */
let assetProbe: Promise<void> | null = null

async function fetchAsset(url: string, what: string): Promise<Response> {
  // Finding 4 — bounded so one hung request cannot wedge the shared,
  // page-wide asset memo forever (see `PDF_ASSET_FETCH_TIMEOUT_MS`'s own
  // comment). A dedicated controller, not the caller's own abort signal:
  // this timeout is about the REQUEST, not about this document's load being
  // cancelled, and `ensureRuntimeAssets`'s memo is intentionally shared
  // across every mounted PDF — it has no single caller's signal to use.
  const controller = new AbortController()
  const timedOut = { current: false }
  const timer = setTimeout(() => {
    timedOut.current = true
    controller.abort()
  }, assetFetchTimeoutMs())
  let res: Response
  try {
    res = await fetch(url, { credentials: 'same-origin', signal: controller.signal })
  } catch (err) {
    throw new PdfAssetError(
      timedOut.current
        ? `Could not load ${what} from ${url}: the request did not finish within ` +
            `${Math.round(assetFetchTimeoutMs() / 1000)} seconds.`
        : `Could not load ${what} from ${url}: ${String(err)}`,
    )
  } finally {
    clearTimeout(timer)
  }
  if (!res.ok) {
    throw new PdfAssetError(`Could not load ${what}: ${url} returned HTTP ${res.status}`)
  }
  // The sharp edge FR-018b names: an SPA handler that answers every unknown
  // path with index.html and HTTP 200. `res.ok` alone would read that as
  // success and the PDF would render blank with no explanation.
  const type = res.headers.get('content-type') ?? ''
  if (type.includes('text/html')) {
    throw new PdfAssetError(
      `Could not load ${what}: ${url} returned the app's HTML shell instead of the file. ` +
        `The PDF.js runtime assets are not being served from ${ASSET_BASE}.`,
    )
  }
  return res
}

async function probeRuntimeAssets(): Promise<void> {
  const manifestUrl = `${ASSET_BASE}asset-manifest.json`
  const res = await fetchAsset(manifestUrl, 'the PDF.js asset manifest')
  let manifest: Record<string, string[]>
  try {
    manifest = (await res.json()) as Record<string, string[]>
  } catch (err) {
    throw new PdfAssetError(`Could not read the PDF.js asset manifest at ${manifestUrl}: ${String(err)}`)
  }
  const dirs = Object.keys(manifest)
  if (dirs.length === 0) {
    throw new PdfAssetError(`The PDF.js asset manifest at ${manifestUrl} is empty.`)
  }
  // One file per directory is enough to tell "this directory shipped" from
  // "this directory did not", which is the failure that degrades silently.
  await Promise.all(
    dirs.map(async (dir) => {
      const first = manifest[dir]?.[0]
      const meaning = ASSET_DIR_MEANING[dir] ?? dir
      if (!first) {
        throw new PdfAssetError(`PDF.js asset directory "${dir}" is empty — missing ${meaning}.`)
      }
      await fetchAsset(`${ASSET_BASE}${dir}/${first}`, `PDF.js ${dir}/ — ${meaning}`)
    }),
  )
}

export function ensureRuntimeAssets(): Promise<void> {
  if (!assetProbe) {
    assetProbe = probeRuntimeAssets().catch((err: unknown) => {
      assetProbe = null
      throw err
    })
  }
  return assetProbe
}

/** Exported for tests only: forget the memoised asset probe. */
export function __resetPdfAssetProbeForTests(): void {
  assetProbe = null
}

/** What this document's current bytes came with: the raw bytes PDF.js parses,
 *  and the version token (ADR-083 EMB-007) — bare, unquoted — the byte-stream
 *  download's `ETag` header carried. `version` is `null` only when the
 *  server didn't send one (an older gateway build); `handleSave` refuses to
 *  send an empty/invented token in that case rather than pretending it has
 *  one. */
interface PdfBytesRead {
  bytes: ArrayBuffer
  version: string | null
}

export async function fetchPdfBytes(workspaceId: string, path: string, signal: AbortSignal): Promise<PdfBytesRead> {
  // ADR-083 EMB-007/EMB-007c — this download IS the only read on this
  // component's save path (there is no JSON `GET .../content` call here at
  // all), so it is the sole source of the version token `handleSave` below
  // must send back on PUT .../content-binary. `downloadLibraryFileVersioned`
  // is api.ts's bespoke fetch for exactly this: `request<T>()` discards
  // response headers, so a raw fetch — which this already was — is the only
  // way to reach the `ETag` at all; the authenticated session cookie and
  // `attachment` disposition (`fetch` ignores it; nothing navigates) are
  // unchanged from before.
  const { data, version } = await downloadLibraryFileVersioned(workspaceId, path, signal)
  return { bytes: data, version }
}

// ── Edit-mode link handling ──────────────────────────────────────────────
// A minimal, deliberately inert `PDFLinkService`-shaped object. Constructing
// PDF.js's real `AnnotationLayer` needs SOMETHING at `linkService` — Link
// annotations and push-button widgets call into it — but following a link
// (internal or external) is out of scope for a fill/sign editing surface,
// and pdfjs-dist's real `PDFLinkService`/`SimpleLinkService` classes live in
// the separate `pdfjs-dist/web/pdf_viewer` bundle this file deliberately does
// not import (see the module doc above: this file builds its own Worker and
// TextLayer rather than adopting the full viewer). Every method below is
// exactly what `AnnotationLayer`'s Link/PushButton element classes call
// (verified against pdf.mjs) — each one is a safe no-op or a link stripped to
// `href="#"` with navigation cancelled, never left `undefined` (an
// unimplemented method PDF.js calls unconditionally is a runtime crash, not
// a soft failure).
const PDF_EDIT_LINK_SERVICE = {
  externalLinkTarget: null,
  externalLinkRel: 'noopener noreferrer nofollow',
  getDestinationHash: () => '#',
  getAnchorUrl: () => '#',
  addLinkAttributes: (link: HTMLAnchorElement) => {
    link.href = '#'
    link.removeAttribute('target')
    link.rel = 'noopener noreferrer nofollow'
    link.onclick = () => false
  },
  executeNamedAction: () => {},
  executeSetOCGState: () => {},
  goToDestination: async () => {},
}

/** Margin, in that page's own viewport pixels, between a placed signature and
 * the page edge. */
const SIGNATURE_MARGIN_VIEWPORT_PX = 16
/** A placed signature is sized to whichever is smaller: 40% of the page's
 * on-screen width, or this many viewport pixels — so it reads as
 * signature-sized on both a business card and a poster-sized page. */
const SIGNATURE_MAX_WIDTH_FRACTION = 0.4
const SIGNATURE_MAX_WIDTH_VIEWPORT_PX = 220

/**
 * Places a LibrarySignaturePad capture (in the pad's own fixed pixel space)
 * at the bottom-right of the given page, converting through that page's own
 * `PageViewport` — which already encodes scale AND rotation — into PDF-space
 * points. Returns strokes ready for `buildInkAnnotationEntry`.
 */
function placeSignatureOnViewport(padStrokes: SignatureStroke[], viewport: PageViewport): SignatureStroke[] {
  const targetWidth = Math.min(viewport.width * SIGNATURE_MAX_WIDTH_FRACTION, SIGNATURE_MAX_WIDTH_VIEWPORT_PX)
  const targetHeight = targetWidth * (SIGNATURE_PAD_HEIGHT / SIGNATURE_PAD_WIDTH)
  const originX = viewport.width - SIGNATURE_MARGIN_VIEWPORT_PX - targetWidth
  const originY = viewport.height - SIGNATURE_MARGIN_VIEWPORT_PX - targetHeight
  const scaleX = targetWidth / SIGNATURE_PAD_WIDTH
  const scaleY = targetHeight / SIGNATURE_PAD_HEIGHT

  return padStrokes.map((stroke) =>
    stroke.map(({ x, y }) => {
      const [pdfX, pdfY] = viewport.convertToPdfPoint(originX + x * scaleX, originY + y * scaleY) as [
        number,
        number,
      ]
      return { x: pdfX, y: pdfY }
    }),
  )
}

interface PlacedSignature {
  key: string
  pageNumber: number
}

export function LibraryPdfPreview({ workspaceId, entry, variant = 'pane', pageFragment }: LibraryPdfPreviewProps) {
  const containerRef = useRef<HTMLDivElement | null>(null)
  // 'queued' is EMB-032's visible waiting state — set only when the pool
  // could not grant a worker slot immediately (pdfWorkerPool's `onQueued`
  // callback below), never on the common under-ceiling path.
  const [status, setStatus] = useState<'queued' | 'loading' | 'ready' | 'error'>('loading')
  const [error, setError] = useState<string | null>(null)
  const [pageCount, setPageCount] = useState(0)
  // D-37: display zoom (see PDF_READER_ZOOM_DEFAULT above). D-63: the page
  // the signature dialog should default to, captured at the moment it is
  // opened.
  const [zoom, setZoom] = useState<number>(PDF_READER_ZOOM_DEFAULT)
  const [signatureDefaultPage, setSignatureDefaultPage] = useState(1)
  // Distinct from `status === 'ready'`: that flips as soon as the DOCUMENT
  // opens, while pages still render progressively afterwards (existing
  // behaviour, unchanged). Edit mode needs every page's viewport/annotations
  // captured first — entering it while page 3 of 5 is still mid-render would
  // silently skip building a form layer for pages 4-5.
  const [allPagesRendered, setAllPagesRendered] = useState(false)

  const [mode, setMode] = useState<'view' | 'edit'>('view')
  const [hasFormFields, setHasFormFields] = useState<boolean | null>(null)
  // Why `hasFormFields === null` is not enough on its own: null means BOTH
  // "not asked yet" and "asked, and the answer never came". Those are
  // different things to a reader deciding whether this PDF is fillable, and
  // collapsing them is how the failure disappears. This holds the reason the
  // probe failed, and only the second case sets it.
  const [fieldProbeError, setFieldProbeError] = useState<string | null>(null)
  // A page whose interactive form layer could not be built. Without this the
  // page silently shows a blank overlay — no fields, no message — which reads
  // exactly like "this page has no fields".
  const [editLayerError, setEditLayerError] = useState<string | null>(null)
  const [dirty, setDirty] = useState(false)
  const [saveStatus, setSaveStatus] = useState<AutoSaveStatus>('idle')
  const [saveError, setSaveError] = useState<string>()
  const [lastSavedAt, setLastSavedAt] = useState<Date>()
  const [signaturePadOpen, setSignaturePadOpen] = useState(false)
  const [placedSignatures, setPlacedSignatures] = useState<PlacedSignature[]>([])
  // Bumping this re-runs the load effect against `pendingSaveBytesRef`
  // instead of a network fetch — how a successful Save re-opens the document
  // showing what was just written, without a round trip for bytes already in
  // memory (library-b-c-design-2026-09-07.md: "A saved PDF re-opens showing
  // the entered values").
  const [reloadNonce, setReloadNonce] = useState(0)

  const addToast = useUiStore((s) => s.addToast)

  const docRef = useRef<PDFDocumentProxy | null>(null)
  const pdfjsRef = useRef<typeof import('pdfjs-dist') | null>(null)
  const pagesRef = useRef<Map<number, PDFPageProxy>>(new Map())
  const pageViewportsRef = useRef<Map<number, PageViewport>>(new Map())
  const pageElsRef = useRef<Map<number, HTMLDivElement>>(new Map())
  const pageAnnotationsRef = useRef<Map<number, unknown[]>>(new Map())
  const annotationLayerDivsRef = useRef<Map<number, HTMLDivElement>>(new Map())
  const signaturePreviewElsRef = useRef<Map<string, HTMLDivElement>>(new Map())
  const pendingSaveBytesRef = useRef<Uint8Array | null>(null)
  const fadeTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  // ── Zoom re-rasterisation (see `targetRasterScale`'s comment above) ───────
  // The <canvas> currently mounted for each page — `rasterizePage` swaps this
  // out for a freshly-drawn one, so later callers always redraw against
  // whichever canvas is actually on screen, not a stale reference to the
  // FIRST one.
  const pageCanvasElsRef = useRef<Map<number, HTMLCanvasElement>>(new Map())
  // The backing-store scale (`targetRasterScale`'s return value) currently
  // painted on each page's canvas. `rasterizePage` reads this to skip a
  // redraw that would produce an identical result — every zoom step below
  // the cap, or a zoom step that lands on an already-capped page, is a
  // real no-op rather than a wasted render.
  const paintedScaleRef = useRef<Map<number, number>>(new Map())
  // Bumped on every `rasterizePage` call for a given page. A redraw that
  // finishes after a NEWER one already committed (e.g. two zoom steps fired
  // close together) checks its own generation before swapping in the canvas
  // it just built — the newer redraw's result must win, never the older one
  // landing last and silently downgrading the resolution just shown.
  const pageRenderGenerationRef = useRef<Map<number, number>>(new Map())
  // The in-flight `page.render()` task for each page's CURRENT redraw, so a
  // second zoom step arriving before the first redraw finishes cancels the
  // now-pointless one instead of letting two renders race for the same slot.
  const pendingRasterTasksRef = useRef<Map<number, { cancel: () => void }>>(new Map())
  // `Math.min(window.devicePixelRatio || 1, MAX_PIXEL_RATIO)` for the CURRENT
  // load, captured once when the pages are drawn (device pixel ratio does
  // not change mid-session) and reused by every later re-rasterisation so it
  // matches the number the base render used.
  const deviceRatioRef = useRef<number>(1)
  // A live mirror of the `zoom` state, read by the scroll-triggered
  // re-raster effect below (registered once, with no `zoom` dependency, so
  // scrolling never re-subscribes the listener) and by the load effect's
  // initial per-page render (which does not depend on `zoom` either — a
  // Save-triggered reload must draw at whatever zoom the reader is
  // currently at, not silently reset to 100%).
  const zoomRef = useRef<number>(PDF_READER_ZOOM_DEFAULT)
  // True only while `handleSave` is awaiting `doc.saveDocument()` / the PUT.
  // `saveDocument()` talks to the worker and never settles if that worker
  // died, so a load failure discovered mid-save has to unwedge the indicator
  // — otherwise "Saving…" is shown forever with nothing ever coming.
  const savingRef = useRef(false)
  // ADR-083 EMB-007 — the version token handleSave sends as expect_version.
  // Set from the download's ETag on a real read (the load effect's `else`
  // branch below); REPLACED from putLibraryContentBinary's own response on
  // every successful save, and from a 409's actual_version on a refused one
  // — never left pointing at a token already proven stale. Deliberately NOT
  // reset when the load effect reuses `pendingSaveBytesRef` (a just-saved
  // reload) — that path performs no new read, and the ref already holds the
  // fresh token the save that produced those bytes just returned.
  const versionRef = useRef<string | null>(null)

  // Dirty-state guard (library-spec.md: "warn before navigating away from
  // unsaved edits") — same wiring useLibraryFileEditor.ts uses for the text
  // editors, so switching to a different Library file (or closing the pane)
  // while a form field or signature is unsaved goes through the same
  // confirm-discard prompt.
  useEffect(() => {
    setLibraryEditorDirty(dirty)
  }, [dirty])
  useEffect(() => {
    return () => setLibraryEditorDirty(false)
  }, [])
  useEffect(() => {
    return () => {
      if (fadeTimerRef.current) clearTimeout(fadeTimerRef.current)
    }
  }, [])

  // D-37's zoom actions, moved onto the shared ZoomableView contract (D18,
  // docs/internal/design/components/zoomable-view.md, "Scope extension
  // 2026-09-21"). These feed the catalogued `ZoomPill` in the header below
  // (this file's own zoom-pill JSX is gone) and the keyboard hook just
  // below, over the shared `clampZoomScale` 25%-400% range — replacing the
  // old fixed 50%-200% six-step ladder. Deliberately NOT
  // `useZoomableMedia`/`ZoomableMediaSurface`: those model a single
  // pannable image/SVG inside a fixed frame (drag-to-pan, pinch, a
  // frame-relative fit scale) — this preview is a SCROLLABLE, multi-page
  // document, where "zoom" is a uniform reader magnification layered on top
  // of the already-fitted per-page render (`MIN_SCALE`/`MAX_SCALE` above),
  // never a pan/frame transform. Only the parts of the shared contract that
  // actually fit a paginated document move over: the pill, the clamp, and
  // the keyboard shortcuts.
  function zoomIn() {
    setZoom((z) => clampZoomScale(z * 1.25))
  }
  function zoomOut() {
    setZoom((z) => clampZoomScale(z / 1.25))
  }
  // "Fit" and "100%" are the SAME action here: this reader zoom is a pure
  // multiplier on top of the per-page fit-to-width render (see the
  // pagesToRender loop below), so 1 — no extra magnification — is
  // simultaneously "the fitted page" and "100%".
  function zoomToFit() {
    setZoom(PDF_READER_ZOOM_DEFAULT)
  }
  function zoomTo100() {
    setZoom(PDF_READER_ZOOM_DEFAULT)
  }
  const handleZoomKeyDown = useZoomableViewKeyboard({
    onZoomIn: zoomIn,
    onZoomOut: zoomOut,
    onFit: zoomToFit,
    onZoomTo100: zoomTo100,
  })

  // The Ctrl/Cmd+wheel zoom gesture, as a NATIVE non-passive listener (Claude
  // review 2026-09-14, cut-list — the precedent `ZoomableMediaSurface`'s own
  // wheel handling in zoomable-view.tsx follows for the identical reason;
  // see that file's header comment). It used to live in the container's
  // onWheel prop, and React attaches delegated wheel listeners PASSIVELY at
  // the root — calling preventDefault() there cannot cancel anything, so
  // Ctrl/Cmd+wheel zoomed the PDF *and* the whole browser page at once.
  // Registered directly on the pages container with { passive: false },
  // preventDefault actually suppresses the browser's own zoom gesture for
  // the container. Only the Ctrl/Cmd case is cancelled — a plain wheel is
  // ordinary scrolling, since this is a scrollable document, not a
  // fixed-frame media surface.
  useEffect(() => {
    const el = containerRef.current
    if (!el) return
    const onWheel = (event: WheelEvent) => {
      if (!(event.ctrlKey || event.metaKey)) return
      event.preventDefault()
      setZoom((z) => clampZoomScale(event.deltaY < 0 ? z * 1.25 : z / 1.25))
    }
    el.addEventListener('wheel', onWheel, { passive: false })
    return () => el.removeEventListener('wheel', onWheel)
  }, [])

  useEffect(() => {
    zoomRef.current = zoom
  }, [zoom])

  /** Redraws ONE page's canvas at `targetScale` device-pixels-per-CSS-pixel,
   *  reusing the SAME `PDFPageProxy`/`PageViewport` the base render already
   *  holds (`pagesRef`/`pageViewportsRef`) — no re-fetch, no reopen. Builds
   *  the new bitmap into a detached canvas and swaps it in only once it is
   *  actually ready, so the page never goes blank: the previous canvas
   *  (softer, but complete) stays on screen for the entire redraw. A no-op
   *  when the page is already painted at `targetScale` (`paintedScaleRef`).
   *
   *  Failure here — the page's worker already torn down (an inline embed
   *  past its base render, see EMB-032's note above `endWorkerThreadThen
   *  ReleaseLease`'s inline branch), or a stale generation losing a race to
   *  a newer redraw — leaves the CURRENT canvas exactly as it was: always a
   *  complete, correct picture at some resolution, never a blank or
   *  half-drawn one. That is why this catches and returns rather than
   *  surfacing a toast: the fallback is itself a working display, not a
   *  lost user action, and an inline embed hitting this on every zoom step
   *  (expected, not a bug) would otherwise toast on every step. */
  async function rasterizePage(pageNumber: number, targetScale: number) {
    if (paintedScaleRef.current.get(pageNumber) === targetScale) return
    const pdfjs = pdfjsRef.current
    const page = pagesRef.current.get(pageNumber)
    const viewport = pageViewportsRef.current.get(pageNumber)
    const pageEl = pageElsRef.current.get(pageNumber)
    const oldCanvas = pageCanvasElsRef.current.get(pageNumber)
    if (!pdfjs || !page || !viewport || !pageEl || !oldCanvas) return

    pendingRasterTasksRef.current.get(pageNumber)?.cancel()

    const generation = (pageRenderGenerationRef.current.get(pageNumber) ?? 0) + 1
    pageRenderGenerationRef.current.set(pageNumber, generation)

    const newCanvas = document.createElement('canvas')
    newCanvas.width = Math.floor(viewport.width * targetScale)
    newCanvas.height = Math.floor(viewport.height * targetScale)
    // The CSS box size is the FIT-WIDTH size, unaffected by targetScale —
    // that is what keeps the page's layout, and every overlay positioned
    // against it, identical at every zoom level (see this file's zoom
    // comment above `PDF_READER_ZOOM_DEFAULT`).
    newCanvas.style.width = `${viewport.width}px`
    newCanvas.style.height = `${viewport.height}px`
    newCanvas.className = oldCanvas.className
    const ctx = newCanvas.getContext('2d')
    if (!ctx) return

    let renderTask: ReturnType<PDFPageProxy['render']>
    try {
      renderTask = page.render({
        canvas: newCanvas,
        canvasContext: ctx,
        viewport,
        transform: targetScale === 1 ? undefined : [targetScale, 0, 0, targetScale, 0, 0],
        // Same read-only, static-appearance mode the base render uses — see
        // the module doc's "Read-only BASE render (NB-17)" note. A redraw is
        // the identical picture at a different resolution, never a live one.
        annotationMode: pdfjs.AnnotationMode.ENABLE,
        isEditing: false,
      })
    } catch {
      return
    }
    pendingRasterTasksRef.current.set(pageNumber, renderTask)
    try {
      await renderTask.promise
    } catch {
      return
    } finally {
      if (pendingRasterTasksRef.current.get(pageNumber) === renderTask) {
        pendingRasterTasksRef.current.delete(pageNumber)
      }
    }
    // A newer redraw for this SAME page already committed while this one was
    // in flight — its result must win, not this stale one landing last.
    if (pageRenderGenerationRef.current.get(pageNumber) !== generation) return
    if (!pageEl.isConnected || !oldCanvas.isConnected) return
    pageEl.replaceChild(newCanvas, oldCanvas)
    pageCanvasElsRef.current.set(pageNumber, newCanvas)
    paintedScaleRef.current.set(pageNumber, targetScale)
  }

  // Re-rasterise near-viewport pages once the reader STOPS zooming
  // (RERASTER_DEBOUNCE_MS after the last `zoom` change) — a wheel/pinch
  // gesture fires many steps, and this coalesces them into one redraw per
  // gesture rather than one per step. Skipped entirely before the base
  // render has finished (`allPagesRendered`): there is nothing to
  // re-rasterise yet, and `pagesRef`/`pageViewportsRef` are still being
  // populated by the load effect below.
  useEffect(() => {
    if (!allPagesRendered) return
    const container = containerRef.current
    if (!container) return
    const deviceRatio = deviceRatioRef.current
    const timer = setTimeout(() => {
      for (const n of nearViewportPageNumbers(container, pageElsRef.current)) {
        const viewport = pageViewportsRef.current.get(n)
        if (!viewport) continue
        void rasterizePage(n, targetRasterScale(viewport.width, viewport.height, zoom, deviceRatio))
      }
    }, RERASTER_DEBOUNCE_MS)
    return () => clearTimeout(timer)
  }, [zoom, allPagesRendered])

  // Re-rasterise near-viewport pages once SCROLLING settles, at whatever
  // zoom is currently active (`zoomRef`, not `zoom` — this effect has no
  // `zoom` dependency so it registers the listener exactly once). Handles
  // the case the zoom-triggered effect above cannot: a reader who zoomed in,
  // then scrolled to a page that was never near the viewport while zoom was
  // settling, and so was never re-rasterised at the current zoom's target
  // resolution.
  useEffect(() => {
    const container = containerRef.current
    if (!container) return
    let timer: ReturnType<typeof setTimeout> | null = null
    const onScroll = () => {
      if (timer) clearTimeout(timer)
      timer = setTimeout(() => {
        const deviceRatio = deviceRatioRef.current
        for (const n of nearViewportPageNumbers(container, pageElsRef.current)) {
          const viewport = pageViewportsRef.current.get(n)
          if (!viewport) continue
          void rasterizePage(n, targetRasterScale(viewport.width, viewport.height, zoomRef.current, deviceRatio))
        }
      }, RERASTER_DEBOUNCE_MS)
    }
    container.addEventListener('scroll', onScroll, { passive: true })
    return () => {
      container.removeEventListener('scroll', onScroll)
      if (timer) clearTimeout(timer)
    }
  }, [])

  usePdfLoadEffect({
    containerRef,
    workspaceId,
    entryName: entry.name,
    entryPath: entry.path,
    reloadNonce,
    pageFragment,
    variant,
    refs: {
      pagesRef,
      pageViewportsRef,
      pageElsRef,
      pageCanvasElsRef,
      paintedScaleRef,
      pageAnnotationsRef,
      annotationLayerDivsRef,
      signaturePreviewElsRef,
      docRef,
      pdfjsRef,
      pendingRasterTasksRef,
      pageRenderGenerationRef,
      pendingSaveBytesRef,
      versionRef,
      savingRef,
      zoomRef,
      deviceRatioRef,
    },
    setters: {
      setStatus,
      setError,
      setPageCount,
      setAllPagesRendered,
      setMode,
      setHasFormFields,
      setFieldProbeError,
      setEditLayerError,
      setDirty,
      setSaveStatus,
      setSaveError,
      setLastSavedAt,
      setSignaturePadOpen,
      setPlacedSignatures,
    },
  })

  // Edit-mode AnnotationLayer mount/unmount. Runs only once every page has
  // finished its base render (see `allPagesRendered` above) — entering Edit
  // before then would silently skip whichever pages hadn't reached the loop
  // yet. Leaving Edit tears the layers down again; nothing in
  // `annotationStorage` is touched by that — field values and placed
  // signatures live in the SAME storage object regardless of which mode is
  // showing, so switching back to Edit (or Save) sees them unchanged.
  useEffect(() => {
    if (!allPagesRendered) return
    const pdfjs = pdfjsRef.current
    const doc = docRef.current
    if (!pdfjs || !doc) return

    if (mode !== 'edit') {
      for (const div of annotationLayerDivsRef.current.values()) div.remove()
      annotationLayerDivsRef.current.clear()
      // Placed-signature previews are UNSAVED edits with a live remove button;
      // they must not float over — or be mutable in — the read-only View render.
      // Hide (not remove) so switching back to Edit restores them without
      // needing the original strokes, which are not retained in state.
      for (const el of signaturePreviewElsRef.current.values()) el.style.display = 'none'
      return
    }

    // Re-entering edit: any previews hidden on the last View toggle come back.
    for (const el of signaturePreviewElsRef.current.values()) el.style.display = ''
    setEditLayerError(null)

    let cancelled = false
    void (async () => {
      for (const [pageNumber, viewport] of pageViewportsRef.current) {
        if (cancelled) return
        if (annotationLayerDivsRef.current.has(pageNumber)) continue
        const pageEl = pageElsRef.current.get(pageNumber)
        const page = pagesRef.current.get(pageNumber)
        const annotations = pageAnnotationsRef.current.get(pageNumber)
        if (!pageEl || !page || !annotations) continue

        const div = document.createElement('div')
        div.className = 'omnipus-pdf-annotation-layer'
        div.setAttribute('data-testid', 'library-pdf-annotation-layer')
        div.setAttribute('data-page-number', String(pageNumber))
        pageEl.appendChild(div)

        // `AnnotationLayer`'s CONSTRUCTOR is genuinely loosely typed (every
        // field in its own .d.ts is `any` — verified against
        // types/src/display/annotation_layer.d.ts) but still requires every
        // key to be PRESENT (an object-literal shape, not all-optional), so
        // the unused optional collaborators are passed as explicit
        // `undefined`. Its `.render()` method is typed against the separate,
        // stricter `AnnotationLayerParameters` alias, which requires a real
        // `PDFLinkService` instance — even though PDF.js's OWN real
        // `render(params)` body (verified against build/pdf.mjs) only ever
        // reads `annotations` and `optionalContentConfig` off that argument;
        // `linkService` was already captured, and used, by the constructor.
        // `PDF_EDIT_LINK_SERVICE` (this file's module-level constant) is
        // deliberately NOT the real class — see its own doc comment — so the
        // render call is cast past that one field rather than constructing a
        // `PDFLinkService` this editing surface has no use for.
        const viewportClone = viewport.clone({ dontFlip: true })
        try {
          const layer = new pdfjs.AnnotationLayer({
            div,
            page,
            // Matches pdfjs-dist's own `web/pdf_viewer` AnnotationLayerBuilder
            // (verified against the shipped `web/pdf_viewer.mjs`): the
            // annotation layer is built from a `dontFlip: true` clone of the
            // page's viewport, not the viewport used for the canvas/text layer.
            viewport: viewportClone,
            linkService: PDF_EDIT_LINK_SERVICE,
            annotationStorage: doc.annotationStorage,
            accessibilityManager: undefined,
            annotationCanvasMap: undefined,
            annotationEditorUIManager: undefined,
            structTreeLayer: undefined,
            commentManager: undefined,
          })
          await layer.render({
            div,
            page,
            viewport: viewportClone,
            linkService: PDF_EDIT_LINK_SERVICE,
            annotationStorage: doc.annotationStorage,
            annotations,
            renderForms: true,
            enableScripting: false,
          } as unknown as Parameters<typeof layer.render>[0])
        } catch (err) {
          // Three failures this replaces, every one of them silent:
          //  - this loop ran inside a bare `void (async () => …)()` with no
          //    catch at all, so ONE page's rejection ended the loop and every
          //    LATER page went without a form layer too;
          //  - the div was registered in `annotationLayerDivsRef` BEFORE the
          //    render, so the `has()` guard at the top of this loop skipped
          //    that page on every subsequent Edit toggle — the failure could
          //    never be retried, only carried;
          //  - the reader saw an empty overlay and nothing else. The "no
          //    fillable fields" note renders only for
          //    `hasFormFields === false`, so a page whose fields FAILED said
          //    exactly what a page with no fields says: nothing.
          div.remove()
          const detail = err instanceof Error ? err.message : String(err)
          const message = `The fillable fields on page ${pageNumber} could not be shown: ${detail}`
          if (!cancelled) {
            setEditLayerError(message)
            addToast({ message, variant: 'error' })
          }
          continue
        }
        if (cancelled) {
          // Not registered below, so the mode-change teardown will never find
          // it — remove it here or it survives into the read-only View render.
          div.remove()
          return
        }
        // Registered only now, and only because it holds a real, rendered
        // layer. A page that failed stays unregistered and is retried the
        // next time Edit is entered.
        annotationLayerDivsRef.current.set(pageNumber, div)
      }
    })()

    return () => {
      cancelled = true
    }
  }, [mode, allPagesRendered])

  function handleToggleMode(next: 'view' | 'edit') {
    setMode(next)
  }

  function handleInsertSignature(padStrokes: SignatureStroke[], pageNumber: number) {
    const doc = docRef.current
    const pdfjs = pdfjsRef.current
    const viewport = pageViewportsRef.current.get(pageNumber)
    if (!doc || !pdfjs || !viewport) {
      // Was a bare `return`: the pad closed, nothing appeared, and nothing
      // said why. Pressing Insert has to produce either a signature or a
      // reason.
      addToast({
        message: `Page ${pageNumber} is not ready yet, so the signature was not placed. Wait for the page to finish rendering and try again.`,
        variant: 'error',
      })
      return
    }

    const strokesPdfSpace = placeSignatureOnViewport(padStrokes, viewport)
    const { key, value } = buildInkAnnotationEntry({
      strokesPdfSpace,
      pageIndex: pageNumber - 1,
      rotation: viewport.rotation,
      annotationEditorTypeInk: pdfjs.AnnotationEditorType.INK,
    })

    // DRAW FIRST, then commit. The other order wrote the ink annotation into
    // `annotationStorage` — the exact object `saveDocument()` serialises —
    // and only then tried to draw it. `renderSignaturePreview` can bail three
    // ways, and every one of them was silent, so a reader saw nothing appear,
    // pressed Insert again, and saved a file carrying four invisible ink
    // annotations they never knowingly added. Nothing reaches the document
    // unless it is on screen.
    const drawn = renderSignaturePreview(pageNumber, key, strokesPdfSpace, viewport)
    if (!drawn) {
      addToast({
        message: `The signature could not be drawn on page ${pageNumber}, so it was not added to the document. Nothing was changed.`,
        variant: 'error',
      })
      return
    }
    doc.annotationStorage.setValue(key, value)
    setPlacedSignatures((prev) => [...prev, { key, pageNumber }])
  }

  /** Draws a lightweight, non-interactive preview of a just-placed signature
   * directly on its page — the underlying PDF.js canvas never repaints from
   * annotationStorage live (see the module doc's "Read-only BASE render"
   * note), so without this the signature would be invisible until Save
   * re-opens the document.
   *
   * Returns whether ink actually reached the screen. The caller commits the
   * annotation to the document only on `true` — see `handleInsertSignature`
   * for why the opposite order shipped invisible ink into saved files. */
  function renderSignaturePreview(
    pageNumber: number,
    key: string,
    strokesPdfSpace: SignatureStroke[],
    viewport: PageViewport,
  ): boolean {
    const pageEl = pageElsRef.current.get(pageNumber)
    if (!pageEl) return false

    const viewportStrokes = strokesPdfSpace.map((stroke) =>
      stroke.map(({ x, y }) => {
        const [vx, vy] = viewport.convertToViewportPoint(x, y) as [number, number]
        return { x: vx, y: vy }
      }),
    )
    let minX = Infinity
    let minY = Infinity
    let maxX = -Infinity
    let maxY = -Infinity
    for (const stroke of viewportStrokes) {
      for (const p of stroke) {
        if (p.x < minX) minX = p.x
        if (p.y < minY) minY = p.y
        if (p.x > maxX) maxX = p.x
        if (p.y > maxY) maxY = p.y
      }
    }
    // No finite point anywhere in the capture — there is nothing to draw, so
    // there is nothing to write into the document either.
    if (!Number.isFinite(minX)) return false
    const pad = 8
    const left = minX - pad
    const top = minY - pad
    const width = Math.max(1, maxX - minX + pad * 2)
    const height = Math.max(1, maxY - minY + pad * 2)

    const wrapper = document.createElement('div')
    wrapper.className = 'omnipus-pdf-signature-preview'
    wrapper.style.left = `${left}px`
    wrapper.style.top = `${top}px`
    wrapper.style.width = `${width}px`
    wrapper.style.height = `${height}px`
    wrapper.setAttribute('data-testid', 'library-pdf-signature-preview')
    wrapper.setAttribute('data-signature-key', key)

    const canvas = document.createElement('canvas')
    const ratio = Math.min(window.devicePixelRatio || 1, MAX_PIXEL_RATIO)
    canvas.width = Math.max(1, Math.floor(width * ratio))
    canvas.height = Math.max(1, Math.floor(height * ratio))
    canvas.style.width = `${width}px`
    canvas.style.height = `${height}px`
    wrapper.appendChild(canvas)
    const ctx = canvas.getContext('2d')
    // Was `if (ctx) { …draw… }` with no else: without a 2D context the
    // wrapper was still appended, so an EMPTY box was added to the page and
    // the caller went on to commit the ink regardless. Bail before anything
    // is attached to the page.
    if (!ctx) return false
    ctx.scale(ratio, ratio)
    // No fitting design-system colour token: `--color-primary` (Deep Space
    // Black, #0A0A0B) is the nearest by meaning but is not byte-identical to
    // this ink colour, and a canvas `strokeStyle` needs a real CSS <color>
    // value it can parse, not a `var(--token)` reference. Reported to the
    // token lane — see LibrarySignaturePad.tsx's identical ink colour.
    ctx.strokeStyle = '#111111'
    ctx.lineWidth = 2
    ctx.lineCap = 'round'
    ctx.lineJoin = 'round'
    for (const stroke of viewportStrokes) {
      if (stroke.length === 0) continue
      ctx.beginPath()
      stroke.forEach((p, i) => {
        const x = p.x - left
        const y = p.y - top
        if (i === 0) ctx.moveTo(x, y)
        else ctx.lineTo(x, y)
      })
      ctx.stroke()
    }

    // The remove affordance itself is NOT built here as a raw DOM node — a
    // catalogued `IconButton` is portalled into `wrapper` from this
    // component's own render (see the `placedSignatures.map(...)` block near
    // the JSX return) once `handleInsertSignature` commits this key to
    // `placedSignatures` state. `wrapper` is registered in
    // `signaturePreviewElsRef` synchronously, just below, before that state
    // update is dispatched, so the portal target always exists by the time
    // React looks for it.

    pageEl.appendChild(wrapper)
    signaturePreviewElsRef.current.set(key, wrapper)
    return true
  }

  function handleRemoveSignature(key: string) {
    docRef.current?.annotationStorage.remove(key)
    signaturePreviewElsRef.current.get(key)?.remove()
    signaturePreviewElsRef.current.delete(key)
    setPlacedSignatures((prev) => prev.filter((s) => s.key !== key))
  }

  /** ADDITION — the PDF pane had no way back from a failed load. Its sibling
   *  surfaces both offer one (LibraryPreviewPane's "Try again",
   *  KnowledgePanel's "Check again"); this one named its cause and stopped,
   *  so a transient blip — a dropped byte fetch that succeeds on the very
   *  next request — was a permanently dead pane until the whole file was
   *  reselected. Bumping the nonce re-runs the load effect from the top:
   *  fresh `loadFailed`, fresh state, and (because `ensureRuntimeAssets`
   *  clears its memo on failure) a genuinely repeated asset probe too. */
  function handleRetry() {
    setReloadNonce((n) => n + 1)
  }

  async function handleSave() {
    const doc = docRef.current
    if (!doc) return
    savingRef.current = true
    setSaveStatus('saving')
    setSaveError(undefined)
    try {
      // ADR-083 EMB-001/EMB-004/EMB-007/EMB-007c, founder ruling N2:
      // expect_version is REQUIRED on this door with no exemption — including
      // for this editor, whose only read (the download in fetchPdfBytes) is
      // what versionRef was populated from. Refuse locally rather than send
      // an empty/invented token: this can only happen if the read genuinely
      // never returned one (e.g. an older gateway build pre-EMB-007).
      const expectVersion = versionRef.current
      if (!expectVersion) {
        throw new ApiError(
          400,
          'Could not confirm this file’s current version — reload it and try again.',
          { code: 'expect_version_unavailable' },
        )
      }
      // Round-trips through the worker. If the worker dies mid-flight this
      // never settles — `failLoad` is what unwedges the indicator in that
      // case (see `savingRef`).
      const bytes = await doc.saveDocument()
      savingRef.current = false
      const content_base64 = uint8ArrayToBase64(bytes)
      const { version } = await putLibraryContentBinary(workspaceId, {
        path: entry.path,
        content_base64,
        expect_version: expectVersion,
      })
      // EMB-007 — replace with the token THIS save's response returned, not
      // the one the original download returned, so a second save in the
      // same session compares against the file's actual current state.
      versionRef.current = version
      doc.annotationStorage.resetModified()
      setSaveStatus('saved')
      setLastSavedAt(new Date())
      addToast({ message: 'Saved.', variant: 'success' })
      if (fadeTimerRef.current) clearTimeout(fadeTimerRef.current)
      fadeTimerRef.current = setTimeout(() => setSaveStatus((s) => (s === 'saved' ? 'idle' : s)), 2000)
      // Re-open from the exact bytes just written — proves the round-trip and
      // shows the entered values as real, static content (library-b-c-design-
      // 2026-09-07.md: "A saved PDF re-opens showing the entered values").
      pendingSaveBytesRef.current = bytes
      setReloadNonce((n) => n + 1)
    } catch (err) {
      savingRef.current = false
      // Deliberately do NOT touch annotationStorage, mode, or
      // placedSignatures here — every entered value and placed signature
      // stays in the tab for a retry (library-b-c-design-2026-09-07.md: "a
      // save failure surfaces the reason and keeps the user's entries in the
      // tab, never a silent no-op").
      if (isLibraryVersionConflict(err)) {
        // ADR-083 EMB-004/EMB-007 — a refused save because someone else
        // changed the file is a CONFLICT, distinct from a generic failure:
        // 'conflict' status (AutoSaveIndicator renders it distinctly) plus
        // the fresh token from the 409 body, so the NEXT press of this SAME
        // Save button (the only retry affordance this editor has) sends
        // that one — never the stale token this attempt sent. Nothing here
        // resends automatically (EMB-004).
        versionRef.current = err.actualVersion ?? null
        setSaveStatus('conflict')
        setSaveError(err.userMessage)
        addToast({ message: err.userMessage, variant: 'error' })
        return
      }
      const message = getLibraryErrorMessage(err, 'Save failed')
      setSaveStatus('error')
      setSaveError(message)
      addToast({ message, variant: 'error' })
    }
  }

  const canEdit = status === 'ready'
  const readyForFormsAndSignature = allPagesRendered && mode === 'edit'
  const inline = variant === 'inline'

  return (
    <div
      className={
        inline
          ? 'flex h-[28rem] max-h-[70vh] min-h-0 flex-col overflow-hidden rounded-md border border-[var(--color-border)] bg-[var(--color-surface-0)]'
          : 'flex flex-1 min-h-0 flex-col overflow-hidden bg-[var(--color-surface-0)]'
      }
      data-testid="library-pdf-preview"
      data-variant={variant}
      {...(pageFragment !== undefined ? { 'data-page-fragment': pageFragment } : {})}
    >
      {/* PDF.js positions every text run absolutely and sizes it from
          --total-scale-factor. These rules are the minimum from pdfjs-dist's
          own web/pdf_viewer.css needed for selection and in-page search to
          land on the right glyphs; they are scoped to this component rather
          than imported wholesale so the viewer's chrome styles stay out of the
          SPA. The layer is transparent — the canvas is what you see.

          The annotation-layer and signature-preview rules below are a
          deliberately TRIMMED subset of pdfjs-dist's own
          web/annotation_layer_builder.css (fetched and verified against the
          6.2.108 tag) — positioning and the handful of cosmetic rules this
          editing surface actually needs, dropping the parts that depend on
          machinery this file does not build (the canvas-swap checkbox/radio
          look needs `annotationCanvasMap`; forced-colors and comment-popup
          styling are print/accessibility polish this scope doesn't require
          to be FUNCTIONAL). Checkboxes/radios keep the browser's native
          appearance rather than pdfjs's custom canvas-swapped one — correctly
          reflecting :checked either way, and simpler without that map. */}
      <style>{`
.omnipus-pdf-text-layer {
  position: absolute;
  inset: 0;
  overflow: clip;
  text-align: initial;
  line-height: 1;
  letter-spacing: normal;
  word-spacing: normal;
  text-size-adjust: none;
  forced-color-adjust: none;
  transform-origin: 0 0;
  caret-color: CanvasText;
  color-scheme: only light;
  z-index: 0;
  --min-font-size: 1;
  --text-scale-factor: calc(var(--total-scale-factor) * var(--min-font-size));
  --min-font-size-inv: calc(1 / var(--min-font-size));
}
.omnipus-pdf-text-layer span,
.omnipus-pdf-text-layer br {
  color: transparent;
  position: absolute;
  white-space: pre;
  cursor: text;
  transform-origin: 0% 0%;
  user-select: text;
}
.omnipus-pdf-text-layer > :not(.markedContent),
.omnipus-pdf-text-layer .markedContent span:not(.markedContent) {
  z-index: 1;
  --font-height: 0;
  --scale-x: 1;
  --rotate: 0deg;
  font-size: calc(var(--text-scale-factor) * var(--font-height));
  transform: rotate(var(--rotate)) scaleX(var(--scale-x)) scale(var(--min-font-size-inv));
}
.omnipus-pdf-text-layer .markedContent { display: contents; }
.omnipus-pdf-text-layer span[role="img"] { user-select: none; cursor: default; }
.omnipus-pdf-text-layer ::selection { background: rgb(0 0 255 / 0.25); }

.omnipus-pdf-annotation-layer {
  position: absolute;
  inset: 0;
  pointer-events: none;
  transform-origin: 0 0;
  z-index: 2;
}
.omnipus-pdf-annotation-layer section {
  position: absolute;
  pointer-events: auto;
  box-sizing: border-box;
  transform-origin: 0 0;
}
.omnipus-pdf-annotation-layer :is(.linkAnnotation, .buttonWidgetAnnotation.pushButton) > a {
  position: absolute;
  top: 0;
  left: 0;
  width: 100%;
  height: 100%;
}
.omnipus-pdf-annotation-layer .textWidgetAnnotation :is(input, textarea),
.omnipus-pdf-annotation-layer .choiceWidgetAnnotation select,
.omnipus-pdf-annotation-layer .buttonWidgetAnnotation:is(.checkBox, .radioButton) input {
  width: 100%;
  height: 100%;
  box-sizing: border-box;
  margin: 0;
  vertical-align: top;
  font: calc(9px * var(--total-scale-factor)) sans-serif;
  background: color-mix(in srgb, var(--color-accent) 12%, transparent);
  border: 1.5px solid var(--color-accent);
  border-radius: 2px;
}
.omnipus-pdf-annotation-layer .textWidgetAnnotation textarea { resize: none; }
.omnipus-pdf-annotation-layer .textWidgetAnnotation :is(input, textarea):focus,
.omnipus-pdf-annotation-layer .choiceWidgetAnnotation select:focus {
  outline: 2px solid var(--color-accent);
  background: color-mix(in srgb, var(--color-accent) 20%, transparent);
}
.omnipus-pdf-annotation-layer .textWidgetAnnotation :is(input, textarea)[disabled],
.omnipus-pdf-annotation-layer .choiceWidgetAnnotation select[disabled] {
  background: none;
  cursor: not-allowed;
}
.omnipus-pdf-annotation-layer .popupAnnotation { display: none; }

.omnipus-pdf-signature-preview {
  position: absolute;
  z-index: 3;
}
.omnipus-pdf-signature-preview canvas { display: block; pointer-events: none; }
.omnipus-pdf-signature-remove {
  position: absolute;
  top: -10px;
  right: -10px;
  width: 20px;
  height: 20px;
  border-radius: 9999px;
  border: none;
  background: var(--color-error);
  color: var(--color-secondary);
  font-size: 13px;
  line-height: 20px;
  text-align: center;
  padding: 0;
  cursor: pointer;
  pointer-events: auto;
}
`}</style>

      <PreviewHeaderPortal>
        <SegmentedControl
          aria-label="View mode"
          value={mode}
          onValueChange={(v) => handleToggleMode(v as 'view' | 'edit')}
          className="gap-0 p-[var(--space-0-5)]"
        >
          <SegmentedControlItem
            value="view"
            aria-label="View"
            title="View"
            data-testid="library-pdf-mode-view"
            className="h-7 w-7 p-0"
          >
            <Eye size={15} weight={mode === 'view' ? 'fill' : 'regular'} />
          </SegmentedControlItem>
          <SegmentedControlItem
            value="edit"
            disabled={!canEdit}
            aria-label="Edit"
            title={canEdit ? 'Fill fields or add a signature' : 'Edit'}
            data-testid="library-pdf-mode-edit"
            className="h-7 w-7 p-0"
          >
            <PencilSimple size={15} weight={mode === 'edit' ? 'fill' : 'regular'} />
          </SegmentedControlItem>
        </SegmentedControl>
        <ZoomPill zoom={zoom} onZoomIn={zoomIn} onZoomOut={zoomOut} onFit={zoomToFit} onZoomTo100={zoomTo100} />
        {mode === 'edit' && (
          <IconButton
            onClick={() => {
              // D-63: read the page on screen NOW, not the document's last page.
              const c = containerRef.current
              setSignatureDefaultPage(c ? firstVisiblePdfPage(c) : 1)
              setSignaturePadOpen(true)
            }}
            disabled={!allPagesRendered}
            aria-label="Add signature"
            title="Draw and place a signature"
            data-testid="library-pdf-add-signature"
            className="flex h-7 w-7 shrink-0 items-center justify-center rounded transition-colors text-[var(--color-muted)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)] disabled:cursor-not-allowed disabled:opacity-40 pointer-coarse:min-h-[44px] pointer-coarse:min-w-[44px]"
          >
            <Signature size={15} />
          </IconButton>
        )}
        <AutoSaveIndicator status={saveStatus} error={saveError} lastSavedAt={lastSavedAt} />
        {mode === 'edit' && (
          <IconButton
            onClick={() => void handleSave()}
            disabled={!dirty || saveStatus === 'saving'}
            aria-label={saveStatus === 'saving' ? 'Saving' : 'Save'}
            title={saveStatus === 'saving' ? 'Saving…' : 'Save'}
            data-testid="library-pdf-save"
            className={cn(
              'flex h-7 w-7 shrink-0 items-center justify-center rounded transition-colors text-[var(--color-muted)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)] disabled:cursor-not-allowed disabled:opacity-40 pointer-coarse:min-h-[44px] pointer-coarse:min-w-[44px]',
              dirty && saveStatus !== 'saving' ? 'text-[var(--color-accent)]' : undefined,
            )}
          >
            <FloppyDisk size={15} weight={dirty ? 'fill' : 'regular'} />
          </IconButton>
        )}
      </PreviewHeaderPortal>

      {mode === 'edit' && allPagesRendered && hasFormFields === false && (
        <div
          className="flex shrink-0 items-center gap-[var(--space-2)] border-b border-[var(--color-border)] bg-[var(--color-surface-1)] px-[var(--space-2-5)] py-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-muted)]"
          data-testid="library-pdf-no-fields-note"
        >
          This PDF has no fillable form fields — you can still add a signature.
        </div>
      )}

      {mode === 'edit' && fieldProbeError !== null && (
        // The honest version of what used to be an unexplained `null`: we
        // ASKED whether this document has fillable fields and the answer
        // never came. Filling is not blocked by that — the per-page
        // annotation layer builds from the page's own annotations — so this
        // says what is true rather than guessing either way.
        <div
          className="flex shrink-0 items-center gap-[var(--space-2)] border-b border-[var(--color-border)] bg-[var(--color-surface-1)] px-[var(--space-2-5)] py-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-muted)]"
          data-testid="library-pdf-field-probe-error"
          title={fieldProbeError}
        >
          Could not check this PDF for fillable fields — any it has should still work.
        </div>
      )}

      {mode === 'edit' && editLayerError !== null && (
        <div
          role="alert"
          className="flex shrink-0 items-center gap-[var(--space-2)] border-b border-[var(--color-error)] bg-[var(--color-surface-1)] px-[var(--space-2-5)] py-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-secondary)]"
          data-testid="library-pdf-edit-layer-error"
        >
          {editLayerError}
        </div>
      )}

      {mode === 'edit' && placedSignatures.length > 0 && (
        <div
          className="flex shrink-0 flex-wrap items-center gap-[var(--space-1)] border-b border-[var(--color-border)] bg-[var(--color-surface-1)] px-[var(--space-2-5)] py-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-muted)]"
          data-testid="library-pdf-signature-list"
        >
          <span>Signatures placed (not yet saved):</span>
          {placedSignatures.map((sig) => (
            <Button
              key={sig.key}
              variant="outline"
              onClick={() => handleRemoveSignature(sig.key)}
              className="h-auto gap-[var(--space-1)] rounded px-[var(--space-1)] py-[var(--space-0-5)] font-[var(--font-weight-regular)] text-[length:inherit]"
              title={`Remove the signature on page ${sig.pageNumber}`}
              data-testid={`library-pdf-signature-chip-${sig.key}`}
            >
              Page {sig.pageNumber} <X size={10} />
            </Button>
          ))}
        </div>
      )}

      {status === 'queued' && (
        // EMB-032's visible waiting state. Deliberately names the real,
        // finite reason (the page-wide worker ceiling) rather than a
        // generic "Loading" that would read as a stalled fetch — a reader
        // who sees this on a note with several PDFs is not looking at a
        // bug, and the copy says so and says what resolves it, honestly:
        // this document opens the moment another releases its worker
        // (closes, or is scrolled far enough away to unmount), which may be
        // immediate or may be a genuine wait if nothing does — never a
        // silent, unexplained hang.
        <div
          className="flex flex-1 items-center justify-center gap-[var(--space-2)] p-[var(--space-4)] text-center text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]"
          data-testid="library-pdf-queued"
        >
          <SpinnerGap className="h-4 w-4 shrink-0 animate-spin" aria-hidden />
          <span>
            Only {PDF_WORKER_POOL_CEILING} PDFs can be open on this page at once. This one will
            open automatically once another PDF closes or scrolls out of view.
          </span>
        </div>
      )}

      {status === 'loading' && (
        <div
          className="flex flex-1 items-center justify-center gap-[var(--space-2)] text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]"
          data-testid="library-pdf-loading"
        >
          <SpinnerGap className="h-4 w-4 animate-spin" aria-hidden />
          <span>Opening {entry.name}…</span>
        </div>
      )}

      {status === 'error' && (
        <div className="flex flex-1 items-center justify-center p-[var(--space-4)]" data-testid="library-pdf-error">
          <div
            role="alert"
            className="max-w-lg rounded-md border border-[var(--color-error)] bg-[var(--color-surface-1)] p-[var(--space-3)] text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]"
          >
            <p className="font-medium">This PDF could not be displayed.</p>
            <p className="mt-[var(--space-2)] text-[var(--color-error)]">{error}</p>
            <Button
              variant="outline"
              onClick={handleRetry}
              data-testid="library-pdf-retry"
              className="mt-[var(--space-2-5)] h-auto rounded px-[var(--space-2-5)] py-[var(--space-1)] text-[length:var(--type-utility-xs-size)] font-[var(--font-weight-regular)]"
            >
              Try again
            </Button>
          </div>
        </div>
      )}

      <div
        ref={containerRef}
        className={`min-h-0 flex-1 overflow-auto p-[var(--space-2)] ${status === 'ready' ? '' : 'hidden'}`}
        // D-37: the reader's magnification. `zoom` (not `transform`) so the
        // scroll extents follow the magnified content.
        style={{ zoom }}
        data-zoom={zoom}
        data-testid="library-pdf-pages"
        // D18's shared keyboard contract ("+, −, 0 for fit and 1 for 100%,
        // while the view is focused") — this is the view the pill above
        // controls, so it is the frame that owns focus and the shortcuts.
        tabIndex={0}
        onKeyDown={handleZoomKeyDown}
        aria-label={
          pageFragment !== undefined
            ? `${entry.name}, page ${pageFragment} of ${pageCount}`
            : `${entry.name}, ${pageCount} page${pageCount === 1 ? '' : 's'}`
        }
      />

      <LibrarySignaturePad
        open={signaturePadOpen && readyForFormsAndSignature}
        onOpenChange={setSignaturePadOpen}
        pageCount={pageCount}
        defaultPageNumber={signatureDefaultPage}
        onInsert={handleInsertSignature}
      />

      {/* The per-signature remove button drawn on the page canvas
          (`renderSignaturePreview`'s `wrapper`, above) is a catalogued
          `IconButton`, portalled into that imperatively-created DOM node —
          `wrapper` is not part of the React tree at all (PDF.js's own pages
          are built with `document.createElement`, not JSX), so a real
          `<button>` element inside it can only be React's if React is told
          to render there via a portal rather than by hand-building one.
          `.omnipus-pdf-signature-remove` (this file's own <style> block
          above) is unlayered CSS, so it still wins over every conflicting
          Tailwind utility Button/IconButton bring along (Tailwind's own
          utilities are emitted inside `@layer utilities`, which always loses
          to unlayered CSS regardless of specificity or source order) — the
          button keeps its exact prior size, shape, color and position. */}
      {placedSignatures.map((sig) => {
        const portalTarget = signaturePreviewElsRef.current.get(sig.key)
        if (!portalTarget) return null
        return createPortal(
          <IconButton
            aria-label="Remove signature"
            data-testid={`library-pdf-signature-remove-${sig.key}`}
            className="omnipus-pdf-signature-remove"
            onClick={() => handleRemoveSignature(sig.key)}
          >
            ×
          </IconButton>,
          portalTarget,
          sig.key,
        )
      })}
    </div>
  )
}

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
import { SpinnerGap, Eye, PencilSimple, FloppyDisk, Signature, X } from '@phosphor-icons/react'
import { ApiError, downloadLibraryFileVersioned, putLibraryContentBinary, isLibraryVersionConflict } from '@/lib/api'
import type { LibraryEntry } from '@/lib/api'
import type { AutoSaveStatus } from '@/hooks/useAutoSave'
import { AutoSaveIndicator } from '@/components/ui/AutoSaveIndicator'
import { cn } from '@/lib/utils'
import { useUiStore } from '@/store/ui'
import { PreviewHeaderPortal } from './previewHeaderSlot'
import { LIBRARY_ICON_BTN } from '../LibraryPreviewPane'
import { setLibraryEditorDirty } from './unsavedGuard'
import { getLibraryErrorMessage } from '../libraryErrorMessage'
import { LibrarySignaturePad, SIGNATURE_PAD_WIDTH, SIGNATURE_PAD_HEIGHT } from './LibrarySignaturePad'
import { buildInkAnnotationEntry } from './pdfInkAnnotation'
import type { SignatureStroke } from './pdfInkAnnotation'
import { uint8ArrayToBase64 } from './pdfBinaryEncoding'
import { pdfWorkerPool, PDF_WORKER_POOL_CEILING } from './pdfWorkerPool'
import type { PdfWorkerLease } from './pdfWorkerPool'
import { INLINE_PREVIEW_BOX_CLASS } from './libraryPreviewVariant'
import type { LibraryPreviewVariant } from './libraryPreviewVariant'

// Type-only: erased at build time, so it does not pull pdfjs-dist into the
// eager module graph.
import type { PDFDocumentProxy, PDFPageProxy, PageViewport } from 'pdfjs-dist'

/** URL prefix the PDF.js runtime assets and worker are served from. Mirrors
 *  `PDFJS_ASSET_PREFIX` in vite.config.ts, which emits them there. The gateway
 *  MUST return a real 404 under this prefix rather than its index.html
 *  fallback (FR-018b) — otherwise a missing character map arrives as HTTP 200
 *  HTML, the page renders blank, and nothing names the cause. */
const ASSET_BASE = `${import.meta.env.BASE_URL}pdfjs/`

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
const MIN_SCALE = 0.25
const MAX_SCALE = 4

/** Cap the canvas backing store at 2x. Beyond that the memory cost per page
 *  grows faster than the visible gain on a 3x display. */
const MAX_PIXEL_RATIO = 2

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

/** How long `PDFDocumentLoadingTask.destroy()` gets to shut the transport
 *  down politely before the Worker thread is terminated outright. `destroy()`
 *  ends by asking the worker to `Terminate` and AWAITING its reply
 *  (`WorkerTransport.destroy` in build/pdf.mjs 6.2.108), so a worker that has
 *  stopped replying makes that promise never settle — the grace timeout is
 *  what stops "tear down politely" from meaning "never tear down". */
const WORKER_TERMINATE_GRACE_MS = 2000

/** The width a page should be rendered at, and whether that number was
 *  MEASURED or guessed.
 *
 *  Measured off the component ROOT, never off the pages container: the
 *  container carries the `hidden` class (`display:none`) until the first page
 *  is on screen, and CSSOM reports `clientWidth === 0` for a `display:none`
 *  element — so measuring it returns 0 on every first load and the fallback
 *  silently becomes the ONLY width this component ever renders at. The root is
 *  never hidden, so it is the real box. */
function measureRenderWidth(container: HTMLElement): { width: number; fallback: boolean } {
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
function firstPageTimeoutMs(): number {
  return firstPageTimeoutOverrideMs ?? PDF_FIRST_PAGE_TIMEOUT_MS
}

/** True for the cancellation every abort path in this file produces, whatever
 *  class it arrives as: `fetch` rejects with a `DOMException`, but a helper
 *  that wraps or re-creates one may not. Matching on the NAME is what the
 *  platform itself documents as the discriminator. */
function isAbortError(err: unknown): boolean {
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
  let res: Response
  try {
    res = await fetch(url, { credentials: 'same-origin' })
  } catch (err) {
    throw new PdfAssetError(`Could not load ${what} from ${url}: ${String(err)}`)
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

function ensureRuntimeAssets(): Promise<void> {
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

async function fetchPdfBytes(workspaceId: string, path: string, signal: AbortSignal): Promise<PdfBytesRead> {
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

  useEffect(() => {
    const container = containerRef.current
    if (!container) return

    let cancelled = false
    // Set by `failLoad` below — the "this load has already reported a
    // failure" latch that makes reporting one idempotent no matter how many
    // discoverers race to it (the async chain, the worker error listener, the
    // first-page watchdog).
    let loadFailed = false
    const abort = new AbortController()
    let doc: PDFDocumentProxy | null = null
    let loadingTask: { destroy: () => Promise<void> } | null = null
    const cancelRender: Array<() => void> = []
    // Hoisted out of the async chain below so this effect's CLEANUP can reach
    // it. This component constructs the Worker thread, so this component is
    // the only thing that can end it — see the cleanup for the three measured
    // reasons `loadingTask.destroy()` does not.
    let port: Worker | null = null

    // Where this load has got to, in words a reader can act on. Used by the
    // first-page watchdog to name what it was waiting for instead of saying
    // "something went wrong".
    let stage = 'waiting for a PDF worker slot'

    // EMB-032 — the bounded worker pool. `lease` is null until the pool
    // grants a slot; `releaseLease` is idempotent so it is safe to call from
    // the worker's own error handler AND again from this effect's cleanup.
    let lease: PdfWorkerLease | null = null
    let leaseReleased = false
    const releaseLease = () => {
      if (leaseReleased || !lease) return
      leaseReleased = true
      lease.release()
    }

    let firstPageWatchdog: ReturnType<typeof setTimeout> | null = null
    const clearFirstPageWatchdog = () => {
      if (firstPageWatchdog === null) return
      clearTimeout(firstPageWatchdog)
      firstPageWatchdog = null
    }

    // THE one way this load reports a failure.
    //
    // It exists because the failure that is hardest to see is the one
    // discovered by something that is not the async chain. The worker `error`
    // listener below stays attached for this component's whole life
    // (`{ once: true }` means fire-once, not load-only); when it fires AFTER
    // `Promise.race([task.promise, workerFailed])` has already settled, its
    // `reject` lands on a promise nobody is listening to — no throw, no
    // unhandledrejection, no state change. The pane is left `ready` with an
    // empty container, no spinner and no error, indefinitely. So every
    // discoverer of a failure calls THIS, which is reachable at any time and
    // is idempotent, rather than relying on a rejection reaching the catch.
    const failLoad = (err: unknown) => {
      if (loadFailed) return
      loadFailed = true
      clearFirstPageWatchdog()
      releaseLease()
      if (!cancelled) {
        setError(err instanceof Error ? err.message : String(err))
        setStatus('error')
        if (savingRef.current) {
          // `doc.saveDocument()` round-trips through the worker. If that
          // worker is what just died, this promise never settles — so the
          // "Saving…" indicator has to be told here or it never stops.
          savingRef.current = false
          setSaveStatus('error')
          setSaveError(err instanceof Error ? err.message : String(err))
        }
      }
      // Nothing further from THIS load may paint, and any in-flight network
      // work for it is now pointless.
      cancelled = true
      abort.abort()
    }

    setStatus('loading')
    setError(null)
    setPageCount(0)
    setAllPagesRendered(false)
    setMode('view')
    setHasFormFields(null)
    setFieldProbeError(null)
    setEditLayerError(null)
    setDirty(false)
    setSaveStatus('idle')
    setSaveError(undefined)
    setLastSavedAt(undefined)
    setSignaturePadOpen(false)
    setPlacedSignatures([])
    pagesRef.current.clear()
    pageViewportsRef.current.clear()
    pageElsRef.current.clear()
    pageAnnotationsRef.current.clear()
    annotationLayerDivsRef.current.clear()
    signaturePreviewElsRef.current.clear()
    docRef.current = null
    pdfjsRef.current = null
    container.replaceChildren()

    void (async () => {
      try {
        // EMB-032 — wait for a worker-pool slot BEFORE doing any of the work
        // that slot exists to bound (asset probing, the byte fetch, and the
        // Worker construction itself). `onQueued` only fires when the
        // ceiling was actually the reason this document is waiting, so the
        // common, under-ceiling case never flashes the waiting state.
        lease = await pdfWorkerPool.acquire(abort.signal, () => {
          if (!cancelled) setStatus('queued')
        })
        if (cancelled) {
          releaseLease()
          return
        }
        setStatus('loading')

        // The deadline starts HERE, not at mount: time spent queued behind
        // the pool's ceiling is a legitimate, explained wait (the `queued`
        // state says so), and counting it would turn a page holding several
        // PDFs into a page that reports false failures.
        firstPageWatchdog = setTimeout(() => {
          failLoad(
            new Error(
              `This PDF did not put a page on screen within ${Math.round(firstPageTimeoutMs() / 1000)} seconds. ` +
                `It stopped at: ${stage}. The parsing worker may have run out of memory or stopped responding — ` +
                `try again, and if it keeps happening this document may be too large or too damaged to render here.`,
            ),
          )
        }, firstPageTimeoutMs())

        // Assets first: a missing directory must fail with a name, not with a
        // blank page (FR-018b).
        stage = 'checking the PDF.js runtime assets'
        await ensureRuntimeAssets()
        if (cancelled) return

        // The one and only reference to pdfjs-dist. Keep it dynamic.
        stage = 'loading the PDF.js runtime'
        const pdfjs = await import('pdfjs-dist')
        if (cancelled) return

        // A just-completed Save already has the new bytes in memory — reuse
        // them instead of re-fetching what we just uploaded. Consumed once.
        let data: ArrayBuffer | Uint8Array
        if (pendingSaveBytesRef.current) {
          data = pendingSaveBytesRef.current
          pendingSaveBytesRef.current = null
          // versionRef already holds the fresh token handleSave's own
          // response returned for these exact bytes (EMB-007) — no read
          // happened on this path, so nothing to update it from.
        } else {
          stage = `downloading ${entry.name}`
          const read = await fetchPdfBytes(workspaceId, entry.path, abort.signal)
          data = read.bytes
          versionRef.current = read.version
        }
        if (cancelled) return

        // FR-019c — our own worker, handed to PDF.js as a port, so there is no
        // fake-worker fallback branch to fall into.
        stage = 'starting the PDF parsing worker'
        let workerPort: Worker
        try {
          workerPort = new Worker(`${ASSET_BASE}pdf.worker.min.mjs`, { type: 'module' })
        } catch (err) {
          throw new Error(
            `The PDF parsing worker could not start, so this PDF was not opened. ` +
              `Parsing never runs on the main thread. Cause: ${String(err)}`,
            { cause: err },
          )
        }
        port = workerPort
        // `PDFWorker.create` rather than `new PDFWorker`: same object, but the
        // published .d.ts types the constructor's `port` as `null | undefined`
        // (a JSDoc default-value artefact) while `create`'s PDFWorkerParameters
        // types it as `Worker`.
        const pdfWorker = pdfjs.PDFWorker.create({ name: 'omnipus-library-pdf', port: workerPort })

        // A missing worker file (or the SPA fallback serving index.html with a
        // 200) makes `new Worker` succeed synchronously but fail asynchronously
        // with an `error` event; the worker then never replies and
        // `task.promise` hangs on "Opening…" forever. Race the load against that
        // error so the catch below surfaces a visible error instead — the exact
        // silent-degrade this component's header says it prevents (FR-018b).
        //
        // ⚠️ This listener outlives the race. `{ once: true }` means fire-ONCE,
        // not fire-only-during-load: a worker that dies AFTER the document
        // opened still fires it, and by then `reject` is shouting into a
        // promise the already-settled `Promise.race` discarded. That is why
        // the body below reports through `failLoad` — reachable at any time —
        // and treats `reject` as the merely-useful-if-anyone-is-still-
        // listening extra, not the mechanism.
        const workerFailed = new Promise<never>((_, reject) => {
          workerPort.addEventListener(
            'error',
            (ev: ErrorEvent) => {
              // EMB-032 — poisoned-worker eviction. This worker is done for
              // THIS document only (a worker error rejects only the leases
              // held on that worker — there is one lease and one worker per
              // document, never shared); free its pool slot immediately
              // rather than waiting for unmount (failLoad does that), and
              // terminate it so nothing keeps talking to a dead transport.
              try {
                workerPort.terminate()
              } catch {
                // Already gone; nothing to do.
              }
              const cause = ev.message ? ` Cause: ${ev.message}` : ''
              // Two genuinely different failures, said differently, because
              // "was not opened" is a lie once it HAS been opened and the
              // reader is looking at its pages.
              const err = doc
                ? new Error(
                    `The PDF parsing worker stopped after this document was opened, so it can no longer be ` +
                      `rendered or saved. Any unsaved entries are still in this tab but cannot be written ` +
                      `until it is reopened.${cause}`,
                  )
                : new Error(
                    `The PDF parsing worker at ${ASSET_BASE}pdf.worker.min.mjs failed to load, ` +
                      `so this PDF was not opened. It may be missing or served as an HTML fallback.${cause}`,
                  )
              failLoad(err)
              reject(err)
            },
            { once: true },
          )
        })
        // The race below is what normally consumes this rejection. If the
        // chain throws BEFORE the race is constructed, nothing would —
        // attaching an inert handler keeps a real, already-reported failure
        // from also surfacing as an unhandled rejection.
        workerFailed.catch(() => {})

        const task = pdfjs.getDocument({
          data,
          worker: pdfWorker,
          // D15.7 — XFA is a scripting surface and is unsupported anyway.
          enableXfa: false,
          // FR-018a — fetched per document, not bundled. See the header of
          // vite.config.ts for what each one being absent does.
          cMapUrl: `${ASSET_BASE}cmaps/`,
          cMapPacked: true,
          standardFontDataUrl: `${ASSET_BASE}standard_fonts/`,
          wasmUrl: `${ASSET_BASE}wasm/`,
          useWasm: true,
          iccUrl: `${ASSET_BASE}iccs/`,
        })
        loadingTask = task
        stage = 'opening the document'
        doc = await Promise.race([task.promise, workerFailed])
        if (cancelled) return
        docRef.current = doc
        pdfjsRef.current = pdfjs
        // Any AcroForm fill or placed signature mutates this SAME object —
        // this is the one hook point for "is there an unsaved edit" that
        // covers both mechanisms without this component having to intercept
        // every widget's own change listener. `onSetModified`/`onResetModified`
        // are typed as bare `null` in annotation_storage.d.ts (a JSDoc
        // initial-value artefact — the class assigns and calls them as
        // callback slots at runtime; verified against build/pdf.mjs's
        // `#setModified`/`resetModified`), so a documented cast is needed to
        // assign a real function.
        const annotationStorage = doc.annotationStorage as unknown as {
          onSetModified: (() => void) | null
          onResetModified: (() => void) | null
        }
        annotationStorage.onSetModified = () => {
          if (!cancelled) setDirty(true)
        }
        annotationStorage.onResetModified = () => {
          if (!cancelled) setDirty(false)
        }
        void doc
          .getFieldObjects()
          .then((fields) => {
            if (!cancelled) setHasFormFields(!!fields && Object.keys(fields).length > 0)
          })
          .catch((err: unknown) => {
            // A field-object read failure costs the "has fields" banner only —
            // the AnnotationLayer render below still tries per-page
            // annotations regardless, so filling still works if the fields
            // ARE there; this just can't promise it up front. It is NOT a
            // reason to fail the whole load.
            //
            // But it is also not nothing: swallowing the error left
            // `hasFormFields === null`, which is the same value as "not asked
            // yet" — so a probe that failed and a probe that never ran looked
            // identical to every reader of that state. The reason is kept and
            // shown in Edit mode instead.
            if (cancelled) return
            setHasFormFields(null)
            setFieldProbeError(err instanceof Error ? err.message : String(err))
          })
        setPageCount(doc.numPages)

        // EMB-105 / US-12 AS-4 — a page-fragment embed renders ONE page, not
        // the whole document. Validated against the REAL page count this
        // document just reported (not against any earlier guess), so a
        // fragment naming a page beyond the document's end is a genuine,
        // honest failure — never a silently empty page.
        let pagesToRender: number[]
        if (pageFragment !== undefined) {
          if (!Number.isInteger(pageFragment) || pageFragment < 1 || pageFragment > doc.numPages) {
            throw new Error(
              `Page ${pageFragment} does not exist in this ${doc.numPages}-page PDF.`,
            )
          }
          pagesToRender = [pageFragment]
        } else {
          pagesToRender = Array.from({ length: doc.numPages }, (_, i) => i + 1)
        }
        if (pagesToRender.length === 0) {
          // A zero-page document would otherwise fall straight through the
          // loop below with nothing appended and nothing thrown — the
          // silently empty pane, arrived at by a different road.
          throw new Error('This PDF reports no pages, so there is nothing to display.')
        }

        // `status` deliberately does NOT flip to 'ready' here. It used to,
        // one statement before the first `getPage()` — from that instant the
        // spinner was gone and the (empty) container was visible, so a worker
        // that wedged without erroring showed a white box indistinguishable
        // from a blank first page. Ready now means "a page is actually on
        // screen"; until then this stays `loading` and the watchdog above is
        // what bounds it.
        const { width, fallback: widthIsFallback } = measureRenderWidth(container)
        if (widthIsFallback) {
          // Make the guess VISIBLE. Rendering every PDF at a plausible
          // hardcoded width is precisely the failure nobody can see, so the
          // state is written where a developer and a test can both read it.
          container.setAttribute('data-width-source', 'fallback')
        } else {
          container.removeAttribute('data-width-source')
        }
        const ratio = Math.min(window.devicePixelRatio || 1, MAX_PIXEL_RATIO)
        let firstPageOnScreen = false

        for (const n of pagesToRender) {
          stage = `rendering page ${n}`
          const page: PDFPageProxy = await doc.getPage(n)
          if (cancelled) return

          const unscaled = page.getViewport({ scale: 1 })
          const fit = (width - 32) / unscaled.width
          const scale = Math.min(MAX_SCALE, Math.max(MIN_SCALE, fit))
          const viewport = page.getViewport({ scale })

          const pageEl = document.createElement('div')
          pageEl.className = 'relative mx-auto my-4 shadow-lg'
          pageEl.style.width = `${viewport.width}px`
          pageEl.style.height = `${viewport.height}px`
          // The text layer sizes its spans from these; they must match the
          // scale the canvas was rendered at or selection lands off the glyphs.
          pageEl.style.setProperty('--scale-factor', String(scale))
          pageEl.style.setProperty('--total-scale-factor', String(scale))
          pageEl.setAttribute('data-testid', 'library-pdf-page')
          pageEl.setAttribute('data-page-number', String(n))

          const canvas = document.createElement('canvas')
          canvas.width = Math.floor(viewport.width * ratio)
          canvas.height = Math.floor(viewport.height * ratio)
          canvas.style.width = `${viewport.width}px`
          canvas.style.height = `${viewport.height}px`
          canvas.className = 'block h-full w-full bg-white'
          pageEl.appendChild(canvas)

          const textLayerEl = document.createElement('div')
          textLayerEl.className = 'omnipus-pdf-text-layer'
          pageEl.appendChild(textLayerEl)

          container.appendChild(pageEl)

          pagesRef.current.set(n, page)
          pageViewportsRef.current.set(n, viewport)
          pageElsRef.current.set(n, pageEl)

          const ctx = canvas.getContext('2d')
          if (!ctx) throw new Error('This browser did not provide a 2D canvas context.')

          const renderTask = page.render({
            canvas,
            canvasContext: ctx,
            viewport,
            transform: ratio === 1 ? undefined : [ratio, 0, 0, ratio, 0, 0],
            // NB-17 — the BASE canvas stays read-only. ENABLE draws annotation
            // appearance streams (including already-filled form values) as
            // static graphics. ENABLE_FORMS and ENABLE_STORAGE are the modes
            // that make the CANVAS ITSELF paint live widgets, which this file
            // still never uses — Edit mode's interactivity comes entirely
            // from the separate AnnotationLayer overlaid on top (below).
            annotationMode: pdfjs.AnnotationMode.ENABLE,
            isEditing: false,
          })
          cancelRender.push(() => renderTask.cancel())

          const textLayer = new pdfjs.TextLayer({
            textContentSource: page.streamTextContent(),
            container: textLayerEl,
            viewport,
          })
          cancelRender.push(() => textLayer.cancel())

          await Promise.all([renderTask.promise, textLayer.render()])
          if (cancelled) return

          if (!firstPageOnScreen) {
            // The first page is drawn and in the DOM — the one moment at
            // which showing the container is honest. The watchdog's job is
            // done at exactly the same instant, and not before: clearing it
            // merely on `appendChild` would leave a render that never
            // finishes covered by nothing at all.
            firstPageOnScreen = true
            clearFirstPageWatchdog()
            setStatus('ready')
          }

          const annotations = await page.getAnnotations({ intent: 'display' })
          if (cancelled) return
          pageAnnotationsRef.current.set(n, annotations)
        }

        // EMB-032 — deliberately NO releaseLease() here, and that is not an
        // oversight. Reaching this point means the document is open and its
        // first pass over every page is done, but its `PDFWorker` is not
        // finished being used: entering Edit mode below mounts a real
        // `AnnotationLayer` against this SAME `doc`, and `handleSave` calls
        // `doc.saveDocument()` — both keep talking to this worker for as
        // long as the component stays mounted. The lease (and the worker
        // instance it stands for) is held for the component's WHOLE mounted
        // lifetime, released only on failure, abandonment before its turn,
        // or unmount (see the effect's cleanup below, and pdfWorkerPool.ts's
        // own header for why releasing on render success would break
        // EMB-032's "at most two worker instances" ceiling rather than
        // honour it).
        if (!cancelled) setAllPagesRendered(true)
      } catch (err) {
        if (cancelled) {
          clearFirstPageWatchdog()
          releaseLease()
          return
        }
        if (isAbortError(err)) {
          clearFirstPageWatchdog()
          releaseLease()
          return
        }
        // PDF.js aborts in-flight renders by rejecting; that is not a failure.
        if (err && typeof err === 'object' && (err as { name?: string }).name === 'RenderingCancelledException') {
          clearFirstPageWatchdog()
          releaseLease()
          return
        }
        // Every OTHER failure ends this load attempt for good. `failLoad`
        // releases the pool slot (so a queued document is not held behind one
        // that is never going to finish), stops the watchdog, and renders the
        // reason. The `workerFailed` path above already went through the same
        // function before this catch was reached; its `loadFailed` latch is
        // what makes calling it twice safe.
        failLoad(err)
      }
    })()

    return () => {
      cancelled = true
      abort.abort()
      clearFirstPageWatchdog()
      // Frees this document's pool slot immediately on unmount (rather than
      // waiting for the async chain above to notice `cancelled`), so a
      // component unmounted by lazy-mount's "well outside the viewport"
      // (EMB-065) does not keep a queued sibling waiting.
      releaseLease()
      for (const cancel of cancelRender) {
        try {
          cancel()
        } catch {
          // A task that already settled throws on cancel; nothing to do.
        }
      }

      // ── Ending the Worker THREAD. This is ours to do. ───────────────────
      // This used to be `void loadingTask?.destroy()` alone, under a comment
      // claiming that "destroys the worker we handed in — one call covers
      // both". Measured against the real pdfjs-dist 6.2.108 in node_modules,
      // that is false three separate ways for a CALLER-SUPPLIED worker:
      //
      //  1. `getDocument` assigns `task._worker` only inside `if (!worker)`
      //     (build/pdf.mjs) — i.e. only when IT created the worker. Ours is
      //     passed in, so `_worker` stays null and
      //     `PDFDocumentLoadingTask.destroy`'s `this._worker?.destroy()` is a
      //     no-op.
      //  2. Even when reached, `PDFWorker.destroy()` terminates `#webWorker`
      //     — and `#initializeFromPort` (the path `PDFWorker.create({port})`
      //     takes) never assigns `#webWorker`. It sets `#port` and
      //     `#messageHandler` only.
      //  3. `WorkerTransport.destroy()` SENDS a "Terminate" message and
      //     awaits the worker's reply. It never touches the port.
      //
      // So the thread outlived every unmount. `LazyEmbedMount` unmounts and
      // remounts PDF embeds as they scroll, which made every scroll-past-and-
      // back leak one live worker holding a parsed PDF, while `pdfWorkerPool`
      // — which counts LEASES, not threads — kept reporting a tidy "at most
      // two". Nothing surfaced until the renderer was OOM-killed.
      //
      // Point 3 also means `destroy()` NEVER SETTLES when the worker has
      // stopped replying (its reply is what resolves it), so the polite
      // teardown gets a bounded grace period and the thread is terminated
      // either way. We constructed it; we end it.
      const thread = port
      let terminated = false
      const terminateThread = () => {
        if (terminated) return
        terminated = true
        try {
          thread?.terminate()
        } catch {
          // Already gone; nothing to do.
        }
      }
      if (!loadingTask) {
        terminateThread()
        return
      }
      const grace = setTimeout(terminateThread, WORKER_TERMINATE_GRACE_MS)
      void loadingTask
        .destroy()
        .catch(() => {})
        .finally(() => {
          clearTimeout(grace)
          terminateThread()
        })
    }
  }, [workspaceId, entry.path, reloadNonce, pageFragment])

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

    const removeBtn = document.createElement('button')
    removeBtn.type = 'button'
    removeBtn.className = 'omnipus-pdf-signature-remove'
    removeBtn.setAttribute('aria-label', 'Remove signature')
    removeBtn.setAttribute('data-testid', `library-pdf-signature-remove-${key}`)
    removeBtn.textContent = '×'
    removeBtn.addEventListener('click', () => handleRemoveSignature(key))
    wrapper.appendChild(removeBtn)

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
          ? `flex ${INLINE_PREVIEW_BOX_CLASS} flex-col overflow-hidden rounded-md border border-[var(--color-border)] bg-[var(--color-surface-0)]`
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
  background: rgba(212, 175, 55, 0.12);
  border: 1.5px solid var(--color-accent);
  border-radius: 2px;
}
.omnipus-pdf-annotation-layer .textWidgetAnnotation textarea { resize: none; }
.omnipus-pdf-annotation-layer .textWidgetAnnotation :is(input, textarea):focus,
.omnipus-pdf-annotation-layer .choiceWidgetAnnotation select:focus {
  outline: 2px solid var(--color-accent);
  background: rgba(212, 175, 55, 0.2);
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
        <div className="flex items-center gap-0.5" role="group" aria-label="View mode">
          <button
            type="button"
            tabIndex={0}
            onClick={() => handleToggleMode('view')}
            aria-pressed={mode === 'view'}
            aria-label="View"
            title="View"
            data-testid="library-pdf-mode-view"
            className={cn(LIBRARY_ICON_BTN, mode === 'view' && 'text-[var(--color-accent)]')}
          >
            <Eye size={15} weight={mode === 'view' ? 'fill' : 'regular'} />
          </button>
          <button
            type="button"
            tabIndex={0}
            onClick={() => handleToggleMode('edit')}
            disabled={!canEdit}
            aria-pressed={mode === 'edit'}
            aria-label="Edit"
            title={canEdit ? 'Fill fields or add a signature' : 'Edit'}
            data-testid="library-pdf-mode-edit"
            className={cn(LIBRARY_ICON_BTN, mode === 'edit' && 'text-[var(--color-accent)]')}
          >
            <PencilSimple size={15} weight={mode === 'edit' ? 'fill' : 'regular'} />
          </button>
        </div>
        {mode === 'edit' && (
          <button
            type="button"
            tabIndex={0}
            onClick={() => setSignaturePadOpen(true)}
            disabled={!allPagesRendered}
            aria-label="Add signature"
            title="Draw and place a signature"
            data-testid="library-pdf-add-signature"
            className={LIBRARY_ICON_BTN}
          >
            <Signature size={15} />
          </button>
        )}
        <AutoSaveIndicator status={saveStatus} error={saveError} lastSavedAt={lastSavedAt} />
        {mode === 'edit' && (
          <button
            type="button"
            tabIndex={0}
            onClick={() => void handleSave()}
            disabled={!dirty || saveStatus === 'saving'}
            aria-label={saveStatus === 'saving' ? 'Saving' : 'Save'}
            title={saveStatus === 'saving' ? 'Saving…' : 'Save'}
            data-testid="library-pdf-save"
            className={cn(LIBRARY_ICON_BTN, dirty && saveStatus !== 'saving' && 'text-[var(--color-accent)]')}
          >
            <FloppyDisk size={15} weight={dirty ? 'fill' : 'regular'} />
          </button>
        )}
      </PreviewHeaderPortal>

      {mode === 'edit' && allPagesRendered && hasFormFields === false && (
        <div
          className="flex shrink-0 items-center gap-2 border-b border-[var(--color-border)] bg-[var(--color-surface-1)] px-3 py-1.5 text-[11px] text-[var(--color-muted)]"
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
          className="flex shrink-0 items-center gap-2 border-b border-[var(--color-border)] bg-[var(--color-surface-1)] px-3 py-1.5 text-[11px] text-[var(--color-muted)]"
          data-testid="library-pdf-field-probe-error"
          title={fieldProbeError}
        >
          Could not check this PDF for fillable fields — any it has should still work.
        </div>
      )}

      {mode === 'edit' && editLayerError !== null && (
        <div
          role="alert"
          className="flex shrink-0 items-center gap-2 border-b border-[var(--color-error)] bg-[var(--color-surface-1)] px-3 py-1.5 text-[11px] text-[var(--color-secondary)]"
          data-testid="library-pdf-edit-layer-error"
        >
          {editLayerError}
        </div>
      )}

      {mode === 'edit' && placedSignatures.length > 0 && (
        <div
          className="flex shrink-0 flex-wrap items-center gap-1.5 border-b border-[var(--color-border)] bg-[var(--color-surface-1)] px-3 py-1.5 text-[11px] text-[var(--color-muted)]"
          data-testid="library-pdf-signature-list"
        >
          <span>Signatures placed (not yet saved):</span>
          {placedSignatures.map((sig) => (
            <button
              key={sig.key}
              type="button"
              tabIndex={0}
              onClick={() => handleRemoveSignature(sig.key)}
              className="inline-flex items-center gap-1 rounded border border-[var(--color-border)] px-1.5 py-0.5 hover:bg-[var(--color-surface-2)]"
              title={`Remove the signature on page ${sig.pageNumber}`}
              data-testid={`library-pdf-signature-chip-${sig.key}`}
            >
              Page {sig.pageNumber} <X size={10} />
            </button>
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
          className="flex flex-1 items-center justify-center gap-2 p-6 text-center text-sm text-[var(--color-muted)]"
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
          className="flex flex-1 items-center justify-center gap-2 text-sm text-[var(--color-muted)]"
          data-testid="library-pdf-loading"
        >
          <SpinnerGap className="h-4 w-4 animate-spin" aria-hidden />
          <span>Opening {entry.name}…</span>
        </div>
      )}

      {status === 'error' && (
        <div className="flex flex-1 items-center justify-center p-6" data-testid="library-pdf-error">
          <div
            role="alert"
            className="max-w-lg rounded-md border border-[var(--color-error)] bg-[var(--color-surface-1)] p-4 text-sm text-[var(--color-secondary)]"
          >
            <p className="font-medium">This PDF could not be displayed.</p>
            <p className="mt-2 text-[var(--color-muted)]">{error}</p>
            <button
              type="button"
              tabIndex={0}
              onClick={handleRetry}
              data-testid="library-pdf-retry"
              className="mt-3 rounded border border-[var(--color-border)] px-3 py-1 text-xs text-[var(--color-secondary)] transition-colors hover:bg-[var(--color-surface-2)]"
            >
              Try again
            </button>
          </div>
        </div>
      )}

      <div
        ref={containerRef}
        className={`min-h-0 flex-1 overflow-auto p-2 ${status === 'ready' ? '' : 'hidden'}`}
        data-testid="library-pdf-pages"
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
        defaultPageNumber={pageCount}
        onInsert={handleInsertSignature}
      />
    </div>
  )
}

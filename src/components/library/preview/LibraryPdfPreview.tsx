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
// ── Laziness (FR-018) ──────────────────────────────────────────────────────
// `pdfjs-dist` is reached ONLY through the dynamic import below, so it stays
// out of the initial payload even if a parent imports this component eagerly.
// vite.config.ts gives that chunk the name `pdfjs`, because a bare dynamic
// import produces a hash-named chunk and a name-matching laziness test would
// then match nothing and pass.

import { useEffect, useRef, useState } from 'react'
import { SpinnerGap, Eye, PencilSimple, FloppyDisk, Signature, X } from '@phosphor-icons/react'
import { libraryDownloadUrl, putLibraryContentBinary } from '@/lib/api'
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

interface LibraryPdfPreviewProps {
  workspaceId: string
  entry: LibraryEntry
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

async function fetchPdfBytes(workspaceId: string, path: string, signal: AbortSignal): Promise<ArrayBuffer> {
  // The authenticated Library endpoint — session cookie, `attachment`
  // disposition unchanged. `fetch` ignores the disposition; nothing navigates.
  const res = await fetch(libraryDownloadUrl(workspaceId, path), {
    credentials: 'include',
    signal,
  })
  if (!res.ok) {
    throw new Error(`Could not read this file from the workspace (HTTP ${res.status}).`)
  }
  return res.arrayBuffer()
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

export function LibraryPdfPreview({ workspaceId, entry }: LibraryPdfPreviewProps) {
  const containerRef = useRef<HTMLDivElement | null>(null)
  const [status, setStatus] = useState<'loading' | 'ready' | 'error'>('loading')
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
    const abort = new AbortController()
    let doc: PDFDocumentProxy | null = null
    let loadingTask: { destroy: () => Promise<void> } | null = null
    const cancelRender: Array<() => void> = []

    setStatus('loading')
    setError(null)
    setPageCount(0)
    setAllPagesRendered(false)
    setMode('view')
    setHasFormFields(null)
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
        // Assets first: a missing directory must fail with a name, not with a
        // blank page (FR-018b).
        await ensureRuntimeAssets()
        if (cancelled) return

        // The one and only reference to pdfjs-dist. Keep it dynamic.
        const pdfjs = await import('pdfjs-dist')
        if (cancelled) return

        // A just-completed Save already has the new bytes in memory — reuse
        // them instead of re-fetching what we just uploaded. Consumed once.
        let data: ArrayBuffer | Uint8Array
        if (pendingSaveBytesRef.current) {
          data = pendingSaveBytesRef.current
          pendingSaveBytesRef.current = null
        } else {
          data = await fetchPdfBytes(workspaceId, entry.path, abort.signal)
        }
        if (cancelled) return

        // FR-019c — our own worker, handed to PDF.js as a port, so there is no
        // fake-worker fallback branch to fall into.
        let port: Worker
        try {
          port = new Worker(`${ASSET_BASE}pdf.worker.min.mjs`, { type: 'module' })
        } catch (err) {
          throw new Error(
            `The PDF parsing worker could not start, so this PDF was not opened. ` +
              `Parsing never runs on the main thread. Cause: ${String(err)}`,
          )
        }
        // `PDFWorker.create` rather than `new PDFWorker`: same object, but the
        // published .d.ts types the constructor's `port` as `null | undefined`
        // (a JSDoc default-value artefact) while `create`'s PDFWorkerParameters
        // types it as `Worker`.
        const pdfWorker = pdfjs.PDFWorker.create({ name: 'omnipus-library-pdf', port })

        // A missing worker file (or the SPA fallback serving index.html with a
        // 200) makes `new Worker` succeed synchronously but fail asynchronously
        // with an `error` event; the worker then never replies and
        // `task.promise` hangs on "Opening…" forever. Race the load against that
        // error so the catch below surfaces a visible error instead — the exact
        // silent-degrade this component's header says it prevents (FR-018b).
        const workerFailed = new Promise<never>((_, reject) => {
          port.addEventListener(
            'error',
            (ev: ErrorEvent) => {
              reject(
                new Error(
                  `The PDF parsing worker at ${ASSET_BASE}pdf.worker.min.mjs failed to load, ` +
                    `so this PDF was not opened. It may be missing or served as an HTML fallback.` +
                    (ev.message ? ` Cause: ${ev.message}` : ''),
                ),
              )
            },
            { once: true },
          )
        })

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
          .catch(() => {
            // A field-object read failure costs the "has fields" banner only —
            // the AnnotationLayer render below still tries per-page
            // annotations regardless, so filling still works if the fields
            // ARE there; this just can't promise it up front.
            if (!cancelled) setHasFormFields(null)
          })
        setPageCount(doc.numPages)
        setStatus('ready')

        const width = container.clientWidth || 800
        const ratio = Math.min(window.devicePixelRatio || 1, MAX_PIXEL_RATIO)

        for (let n = 1; n <= doc.numPages; n++) {
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

          const annotations = await page.getAnnotations({ intent: 'display' })
          if (cancelled) return
          pageAnnotationsRef.current.set(n, annotations)
        }

        if (!cancelled) setAllPagesRendered(true)
      } catch (err) {
        if (cancelled) return
        if (err instanceof DOMException && err.name === 'AbortError') return
        // PDF.js aborts in-flight renders by rejecting; that is not a failure.
        if (err && typeof err === 'object' && (err as { name?: string }).name === 'RenderingCancelledException') return
        setError(err instanceof Error ? err.message : String(err))
        setStatus('error')
      }
    })()

    return () => {
      cancelled = true
      abort.abort()
      for (const cancel of cancelRender) {
        try {
          cancel()
        } catch {
          // A task that already settled throws on cancel; nothing to do.
        }
      }
      // Aborts in-flight network work, tears down the transport AND destroys
      // the worker we handed in — one call covers both.
      void loadingTask?.destroy().catch(() => {})
    }
  }, [workspaceId, entry.path, reloadNonce])

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
        annotationLayerDivsRef.current.set(pageNumber, div)

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
    if (!doc || !pdfjs || !viewport) return

    const strokesPdfSpace = placeSignatureOnViewport(padStrokes, viewport)
    const { key, value } = buildInkAnnotationEntry({
      strokesPdfSpace,
      pageIndex: pageNumber - 1,
      rotation: viewport.rotation,
      annotationEditorTypeInk: pdfjs.AnnotationEditorType.INK,
    })
    doc.annotationStorage.setValue(key, value)
    setPlacedSignatures((prev) => [...prev, { key, pageNumber }])
    renderSignaturePreview(pageNumber, key, strokesPdfSpace, viewport)
  }

  /** Draws a lightweight, non-interactive preview of a just-placed signature
   * directly on its page — the underlying PDF.js canvas never repaints from
   * annotationStorage live (see the module doc's "Read-only BASE render"
   * note), so without this the signature would be invisible until Save
   * re-opens the document. */
  function renderSignaturePreview(
    pageNumber: number,
    key: string,
    strokesPdfSpace: SignatureStroke[],
    viewport: PageViewport,
  ) {
    const pageEl = pageElsRef.current.get(pageNumber)
    if (!pageEl) return

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
    if (!Number.isFinite(minX)) return
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
    if (ctx) {
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
  }

  function handleRemoveSignature(key: string) {
    docRef.current?.annotationStorage.remove(key)
    signaturePreviewElsRef.current.get(key)?.remove()
    signaturePreviewElsRef.current.delete(key)
    setPlacedSignatures((prev) => prev.filter((s) => s.key !== key))
  }

  async function handleSave() {
    const doc = docRef.current
    if (!doc) return
    setSaveStatus('saving')
    setSaveError(undefined)
    try {
      const bytes = await doc.saveDocument()
      const content_base64 = uint8ArrayToBase64(bytes)
      await putLibraryContentBinary(workspaceId, { path: entry.path, content_base64 })
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
      // Deliberately do NOT touch annotationStorage, mode, or
      // placedSignatures here — every entered value and placed signature
      // stays in the tab for a retry (library-b-c-design-2026-09-07.md: "a
      // save failure surfaces the reason and keeps the user's entries in the
      // tab, never a silent no-op").
      const message = getLibraryErrorMessage(err, 'Save failed')
      setSaveStatus('error')
      setSaveError(message)
      addToast({ message, variant: 'error' })
    }
  }

  const canEdit = status === 'ready'
  const readyForFormsAndSignature = allPagesRendered && mode === 'edit'

  return (
    <div
      className="flex flex-1 min-h-0 flex-col overflow-hidden bg-[var(--color-surface-0)]"
      data-testid="library-pdf-preview"
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
          </div>
        </div>
      )}

      <div
        ref={containerRef}
        className={`min-h-0 flex-1 overflow-auto p-2 ${status === 'ready' ? '' : 'hidden'}`}
        data-testid="library-pdf-pages"
        aria-label={`${entry.name}, ${pageCount} page${pageCount === 1 ? '' : 's'}`}
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

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
//     parameter (types/src/display/annotation_layer.d.ts), and this component
//     never constructs an AnnotationLayer. Passing it to getDocument would set
//     a key PDF.js ignores and give a call-site test that passes forever while
//     proving nothing — the exact false-green the spec documents for the
//     removed `isEvalSupported` option. So the control is structural: no
//     annotation layer, no scripting host, no interpreter in the bundle.
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
// ── Read-only (NB-17) ──────────────────────────────────────────────────────
// Annotation *editor* off (no AnnotationEditorUIManager, no editor layer) and
// interactive form entry off (`annotationMode: ENABLE`, never ENABLE_FORMS or
// ENABLE_STORAGE — those are the modes that turn form fields into live HTML
// widgets). Form filling and drawn signing are proven feasible and are
// deliberately out of scope for this release; they are a separate decision
// with their own user stories.
//
// ── Laziness (FR-018) ──────────────────────────────────────────────────────
// `pdfjs-dist` is reached ONLY through the dynamic import below, so it stays
// out of the initial payload even if a parent imports this component eagerly.
// vite.config.ts gives that chunk the name `pdfjs`, because a bare dynamic
// import produces a hash-named chunk and a name-matching laziness test would
// then match nothing and pass.

import { useEffect, useRef, useState } from 'react'
import { SpinnerGap } from '@phosphor-icons/react'
import { libraryDownloadUrl } from '@/lib/api'
import type { LibraryEntry } from '@/lib/api'

// Type-only: erased at build time, so it does not pull pdfjs-dist into the
// eager module graph.
import type { PDFDocumentProxy, PDFPageProxy } from 'pdfjs-dist'

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

export function LibraryPdfPreview({ workspaceId, entry }: LibraryPdfPreviewProps) {
  const containerRef = useRef<HTMLDivElement | null>(null)
  const [status, setStatus] = useState<'loading' | 'ready' | 'error'>('loading')
  const [error, setError] = useState<string | null>(null)
  const [pageCount, setPageCount] = useState(0)

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

        const bytes = await fetchPdfBytes(workspaceId, entry.path, abort.signal)
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

        const task = pdfjs.getDocument({
          data: bytes,
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
        doc = await task.promise
        if (cancelled) return
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

          const ctx = canvas.getContext('2d')
          if (!ctx) throw new Error('This browser did not provide a 2D canvas context.')

          const renderTask = page.render({
            canvas,
            canvasContext: ctx,
            viewport,
            transform: ratio === 1 ? undefined : [ratio, 0, 0, ratio, 0, 0],
            // NB-17 — read-only. ENABLE draws annotation appearance streams
            // (including filled form values) as static graphics. ENABLE_FORMS
            // and ENABLE_STORAGE are the modes that make fields editable.
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
        }
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
  }, [workspaceId, entry.path])

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
          SPA. The layer is transparent — the canvas is what you see. */}
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
`}</style>

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
    </div>
  )
}

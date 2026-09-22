// LibraryPdfPreview.loadEffect — the PDF document/page LOAD effect for
// LibraryPdfPreview.tsx, kept in a sibling module in the same package
// directory per docs/internal/architecture/draft-module-map.md's "a
// function moved to a sibling file in the same package (a file split)
// keeps its grandfathered number" matching rule (function-size budget
// gate, scripts/check-function-budget.sh).
//
// See LibraryPdfPreview.tsx's own module header ("How a load reports
// failure, and who ends the worker") for the three rules this follows.
// Every mid-flow cancellation throws the local `PdfLoadCancelled` sentinel,
// caught once in `runPdfLoad`'s own catch alongside its `if (cancelled)`
// branch — both paths call the same two IDEMPOTENT functions
// (`clearFirstPageWatchdog`, `endWorkerThreadThenReleaseLease`), so
// unifying them changes nothing a caller can observe.

import { useEffect } from 'react'
import type { Dispatch, SetStateAction } from 'react'
import type { PDFDocumentProxy, PDFPageProxy, PageViewport } from 'pdfjs-dist'
import type { AutoSaveStatus } from '@/hooks/useAutoSave'
import { pdfWorkerPool } from './pdfWorkerPool'
import type { PdfWorkerLease } from './pdfWorkerPool'
import type { LibraryPreviewVariant } from './libraryPreviewVariant'
import {
  ASSET_BASE,
  MAX_PIXEL_RATIO,
  MAX_SCALE,
  MIN_SCALE,
  WORKER_TERMINATE_GRACE_MS,
  downloadTimeoutMs,
  ensureRuntimeAssets,
  fetchPdfBytes,
  firstPageTimeoutMs,
  isAbortError,
  measureRenderWidth,
  targetRasterScale,
} from './LibraryPdfPreview'

/** Every ref this effect reads or writes, bundled so the load functions
 *  below take one object instead of seventeen positional arguments. Typed
 *  as plain `{ current: T }` (not `RefObject<T>`) so a `useRef` result —
 *  mutable, unlike `RefObject`'s `readonly current` in some React type
 *  versions — is always assignable here. */
export interface PdfLoadRefs {
  pagesRef: { current: Map<number, PDFPageProxy> }
  pageViewportsRef: { current: Map<number, PageViewport> }
  pageElsRef: { current: Map<number, HTMLDivElement> }
  pageCanvasElsRef: { current: Map<number, HTMLCanvasElement> }
  paintedScaleRef: { current: Map<number, number> }
  pageAnnotationsRef: { current: Map<number, unknown[]> }
  annotationLayerDivsRef: { current: Map<number, HTMLDivElement> }
  signaturePreviewElsRef: { current: Map<string, HTMLDivElement> }
  docRef: { current: PDFDocumentProxy | null }
  pdfjsRef: { current: typeof import('pdfjs-dist') | null }
  pendingRasterTasksRef: { current: Map<number, { cancel: () => void }> }
  pageRenderGenerationRef: { current: Map<number, number> }
  pendingSaveBytesRef: { current: Uint8Array | null }
  versionRef: { current: string | null }
  savingRef: { current: boolean }
  zoomRef: { current: number }
  deviceRatioRef: { current: number }
}

/** Every setState setter this effect calls. */
export interface PdfLoadSetters {
  setStatus: Dispatch<SetStateAction<'queued' | 'loading' | 'ready' | 'error'>>
  setError: Dispatch<SetStateAction<string | null>>
  setPageCount: Dispatch<SetStateAction<number>>
  setAllPagesRendered: Dispatch<SetStateAction<boolean>>
  setMode: Dispatch<SetStateAction<'view' | 'edit'>>
  setHasFormFields: Dispatch<SetStateAction<boolean | null>>
  setFieldProbeError: Dispatch<SetStateAction<string | null>>
  setEditLayerError: Dispatch<SetStateAction<string | null>>
  setDirty: Dispatch<SetStateAction<boolean>>
  setSaveStatus: Dispatch<SetStateAction<AutoSaveStatus>>
  setSaveError: Dispatch<SetStateAction<string | undefined>>
  setLastSavedAt: Dispatch<SetStateAction<Date | undefined>>
  setSignaturePadOpen: Dispatch<SetStateAction<boolean>>
  setPlacedSignatures: Dispatch<SetStateAction<{ key: string; pageNumber: number }[]>>
}

export interface UsePdfLoadEffectParams {
  containerRef: { current: HTMLDivElement | null }
  workspaceId: string
  entryName: string
  entryPath: string
  reloadNonce: number
  pageFragment: number | undefined
  variant: LibraryPreviewVariant
  refs: PdfLoadRefs
  setters: PdfLoadSetters
}

/** Per-load mutable state — one fresh instance per effect run, replacing the
 *  original effect's local `let` variables (`cancelled`, `doc`, `lease`, …)
 *  with a single object every extracted function threads through. */
interface PdfLoadRun {
  cancelled: boolean
  loadFailed: boolean
  /** Where this load has got to, in words a reader can act on. Used by the
   *  first-page watchdog to name what it was waiting for instead of saying
   *  "something went wrong". */
  stage: string
  abort: AbortController
  doc: PDFDocumentProxy | null
  loadingTask: { destroy: () => Promise<void> } | null
  /** Hoisted so this effect's CLEANUP can reach it — this component
   *  constructs the Worker thread, so this component is the only thing
   *  that can end it. */
  port: Worker | null
  lease: PdfWorkerLease | null
  leaseReleased: boolean
  endingWorker: boolean
  workerTerminated: boolean
  firstPageWatchdog: ReturnType<typeof setTimeout> | null
  cancelRender: Array<() => void>
}

function createPdfLoadRun(): PdfLoadRun {
  return {
    cancelled: false,
    loadFailed: false,
    stage: 'waiting for a PDF worker slot',
    abort: new AbortController(),
    doc: null,
    loadingTask: null,
    port: null,
    lease: null,
    leaseReleased: false,
    endingWorker: false,
    workerTerminated: false,
    firstPageWatchdog: null,
    cancelRender: [],
  }
}

/** Thrown by the load sub-steps below to unwind out of `runPdfLoad`'s `try`
 *  the instant `run.cancelled` flips true mid-flow — the load was already
 *  torn down by whoever set it (`failLoad`, or this effect's own cleanup on
 *  unmount/reload), so nothing further should run. Not a real failure: the
 *  catch below treats it the same as the original's silent
 *  `if (cancelled) return`. */
class PdfLoadCancelled extends Error {}

interface PdfLoadControls {
  failLoad: (err: unknown) => void
  endWorkerThreadThenReleaseLease: () => void
  clearFirstPageWatchdog: () => void
  terminateWorkerThread: () => void
}

/** Builds the four control functions every load path (the async chain, the
 *  worker's own `error` listener, the first-page watchdog, and this
 *  effect's cleanup) shares — see LibraryPdfPreview.tsx's module header,
 *  "How a load reports failure, and who ends the worker", for why each one
 *  must be idempotent and reachable from anywhere in this load's life. */
function createPdfLoadControls(
  run: PdfLoadRun,
  savingRef: { current: boolean },
  setters: Pick<PdfLoadSetters, 'setError' | 'setStatus' | 'setSaveStatus' | 'setSaveError'>,
): PdfLoadControls {
  const releaseLease = () => {
    if (run.leaseReleased || !run.lease) return
    run.leaseReleased = true
    run.lease.release()
  }

  const terminateWorkerThread = () => {
    if (run.workerTerminated) return
    run.workerTerminated = true
    try {
      run.port?.terminate()
    } catch {
      // Already gone; nothing to do.
    }
  }

  // SILENT-FAILURES-pdf-pool.md findings 1 & 9 — ends the Worker THREAD
  // before releasing the pool SLOT, no matter which path this load ends
  // through, and holds the slot for exactly as long as the thread is alive
  // — never less. See the module header this file replaces for the full
  // reasoning.
  const endWorkerThreadThenReleaseLease = () => {
    if (run.endingWorker) return
    run.endingWorker = true
    if (run.workerTerminated || !run.loadingTask) {
      terminateWorkerThread()
      releaseLease()
      return
    }
    const grace = setTimeout(terminateWorkerThread, WORKER_TERMINATE_GRACE_MS)
    void run.loadingTask
      .destroy()
      .catch(() => {})
      .finally(() => {
        clearTimeout(grace)
        terminateWorkerThread()
        releaseLease()
      })
  }

  const clearFirstPageWatchdog = () => {
    if (run.firstPageWatchdog === null) return
    clearTimeout(run.firstPageWatchdog)
    run.firstPageWatchdog = null
  }

  // THE one way this load reports a failure — reachable at any time
  // (the async chain, the worker's own `error` listener, the first-page
  // watchdog) and idempotent, because the failure hardest to see is the one
  // discovered by something that is not the async chain.
  const failLoad = (err: unknown) => {
    if (run.loadFailed) return
    run.loadFailed = true
    clearFirstPageWatchdog()
    endWorkerThreadThenReleaseLease()
    if (!run.cancelled) {
      setters.setError(err instanceof Error ? err.message : String(err))
      setters.setStatus('error')
      if (savingRef.current) {
        // `doc.saveDocument()` round-trips through the worker. If that
        // worker is what just died, this promise never settles — so the
        // "Saving…" indicator has to be told here or it never stops.
        savingRef.current = false
        setters.setSaveStatus('error')
        setters.setSaveError(err instanceof Error ? err.message : String(err))
      }
    }
    run.cancelled = true
    run.abort.abort()
  }

  return { failLoad, endWorkerThreadThenReleaseLease, clearFirstPageWatchdog, terminateWorkerThread }
}

function resetForNewLoad(container: HTMLElement, refs: PdfLoadRefs, setters: PdfLoadSetters): void {
  setters.setStatus('loading')
  setters.setError(null)
  setters.setPageCount(0)
  setters.setAllPagesRendered(false)
  setters.setMode('view')
  setters.setHasFormFields(null)
  setters.setFieldProbeError(null)
  setters.setEditLayerError(null)
  setters.setDirty(false)
  setters.setSaveStatus('idle')
  setters.setSaveError(undefined)
  setters.setLastSavedAt(undefined)
  setters.setSignaturePadOpen(false)
  setters.setPlacedSignatures([])
  refs.pagesRef.current.clear()
  refs.pageViewportsRef.current.clear()
  refs.pageElsRef.current.clear()
  refs.pageAnnotationsRef.current.clear()
  refs.annotationLayerDivsRef.current.clear()
  refs.signaturePreviewElsRef.current.clear()
  refs.docRef.current = null
  refs.pdfjsRef.current = null
  // A fresh load means every previously-mounted canvas is gone with
  // `container.replaceChildren()` below — carrying a stale "painted at
  // scale X" entry forward would make `rasterizePage` treat a BRAND NEW
  // canvas as already matching a scale it has never actually drawn at, and
  // skip painting it.
  for (const task of refs.pendingRasterTasksRef.current.values()) task.cancel()
  refs.pendingRasterTasksRef.current.clear()
  refs.pageRenderGenerationRef.current.clear()
  refs.paintedScaleRef.current.clear()
  refs.pageCanvasElsRef.current.clear()
  container.replaceChildren()
}

interface PdfLoadParams {
  container: HTMLElement
  workspaceId: string
  entryName: string
  entryPath: string
  pageFragment: number | undefined
  variant: LibraryPreviewVariant
  refs: PdfLoadRefs
  setters: PdfLoadSetters
}

/** A just-completed Save already has the new bytes in memory — reuse them
 *  instead of re-fetching what was just uploaded. Consumed once. Otherwise
 *  downloads on the byte-stream's OWN deadline (SILENT-FAILURES-pdf-pool.md
 *  finding 3), separate from the parsing watchdog `openPdfDocument` starts
 *  once these bytes are in hand. */
async function acquirePdfBytes(run: PdfLoadRun, params: PdfLoadParams): Promise<ArrayBuffer | Uint8Array> {
  const { refs } = params
  if (refs.pendingSaveBytesRef.current) {
    const data = refs.pendingSaveBytesRef.current
    refs.pendingSaveBytesRef.current = null
    // versionRef already holds the fresh token handleSave's own response
    // returned for these exact bytes (EMB-007) — no read happened on this
    // path, so nothing to update it from.
    return data
  }
  run.stage = `downloading ${params.entryName}`
  let downloadTimedOut = false
  const downloadTimer = setTimeout(() => {
    downloadTimedOut = true
    run.abort.abort()
  }, downloadTimeoutMs())
  try {
    const read = await fetchPdfBytes(params.workspaceId, params.entryPath, run.abort.signal)
    refs.versionRef.current = read.version
    return read.bytes
  } catch (err) {
    if (downloadTimedOut) {
      throw new Error(
        `This PDF did not finish downloading within ${Math.round(downloadTimeoutMs() / 1000)} seconds. ` +
          `That is the network connection, not the PDF parser — check the connection and try again.`,
        { cause: err },
      )
    }
    throw err
  } finally {
    clearTimeout(downloadTimer)
  }
}

/** Races the document-open promise against this worker's own `error` event
 *  (FR-018b) — a missing worker file, or the SPA fallback serving
 *  index.html with 200, makes `new Worker` succeed synchronously but fail
 *  asynchronously, and `task.promise` would otherwise hang on "Opening…"
 *  forever.
 *
 *  ⚠️ The listener outlives the race. `{ once: true }` means fire-ONCE, not
 *  fire-only-during-load: a worker that dies AFTER the document opened
 *  still fires it, and by then `reject` is shouting into a promise the
 *  already-settled `Promise.race` discarded. That is why the body reports
 *  through `failLoad` — reachable at any time — and treats `reject` as the
 *  merely-useful-if-anyone-is-still-listening extra, not the mechanism. */
function createWorkerFailedRace(run: PdfLoadRun, controls: PdfLoadControls, workerPort: Worker): Promise<never> {
  const workerFailed = new Promise<never>((_, reject) => {
    workerPort.addEventListener(
      'error',
      (ev: ErrorEvent) => {
        // EMB-032 — poisoned-worker eviction. This worker is done for THIS
        // document only (a worker error rejects only the leases held on
        // that worker — there is one lease and one worker per document,
        // never shared); terminate it immediately so nothing keeps talking
        // to a dead transport — `failLoad`'s own
        // `endWorkerThreadThenReleaseLease` then sees it is already done
        // and releases the slot right away rather than waiting out a
        // `destroy()` grace period against a worker that is already gone.
        controls.terminateWorkerThread()
        const cause = ev.message ? ` Cause: ${ev.message}` : ''
        // Two genuinely different failures, said differently, because "was
        // not opened" is a lie once it HAS been opened and the reader is
        // looking at its pages.
        const err = run.doc
          ? new Error(
              `The PDF parsing worker stopped after this document was opened, so it can no longer be ` +
                `rendered or saved. Any unsaved entries are still in this tab but cannot be written ` +
                `until it is reopened.${cause}`,
            )
          : new Error(
              `The PDF parsing worker at ${ASSET_BASE}pdf.worker.min.mjs failed to load, ` +
                `so this PDF was not opened. It may be missing or served as an HTML fallback.${cause}`,
            )
        controls.failLoad(err)
        reject(err)
      },
      { once: true },
    )
  })
  // The race above is what normally consumes this rejection. If the chain
  // throws BEFORE the race is constructed, nothing would — attaching an
  // inert handler keeps a real, already-reported failure from also
  // surfacing as an unhandled rejection.
  workerFailed.catch(() => {})
  return workerFailed
}

/** Any AcroForm fill or placed signature mutates this SAME
 *  `annotationStorage` object — the one hook point for "is there an unsaved
 *  edit" that covers both mechanisms without this component intercepting
 *  every widget's own change listener. `onSetModified`/`onResetModified`
 *  are typed as bare `null` in annotation_storage.d.ts (a JSDoc
 *  initial-value artefact — the class assigns and calls them as callback
 *  slots at runtime; verified against build/pdf.mjs's
 *  `#setModified`/`resetModified`), so a documented cast is needed to
 *  assign a real function. */
function wireAnnotationStorage(doc: PDFDocumentProxy, run: PdfLoadRun, setters: PdfLoadSetters): void {
  const annotationStorage = doc.annotationStorage as unknown as {
    onSetModified: (() => void) | null
    onResetModified: (() => void) | null
  }
  annotationStorage.onSetModified = () => {
    if (!run.cancelled) setters.setDirty(true)
  }
  annotationStorage.onResetModified = () => {
    if (!run.cancelled) setters.setDirty(false)
  }
  void doc
    .getFieldObjects()
    .then((fields) => {
      if (!run.cancelled) setters.setHasFormFields(!!fields && Object.keys(fields).length > 0)
    })
    .catch((err: unknown) => {
      // A field-object read failure costs the "has fields" banner only —
      // the AnnotationLayer render still tries per-page annotations
      // regardless, so filling still works if the fields ARE there; this
      // just can't promise it up front. It is NOT a reason to fail the
      // whole load.
      //
      // But it is also not nothing: swallowing the error left
      // `hasFormFields === null`, which is the same value as "not asked
      // yet" — so a probe that failed and a probe that never ran looked
      // identical to every reader of that state. The reason is kept and
      // shown in Edit mode instead.
      if (run.cancelled) return
      setters.setHasFormFields(null)
      setters.setFieldProbeError(err instanceof Error ? err.message : String(err))
    })
}

/** Everything from "wait for a worker-pool slot" through "the document is
 *  open, its field probe is running, and its page count is known" — see
 *  LibraryPdfPreview.tsx's module header for the hardening controls
 *  (XFA/scripting/eval/worker) this construction enforces. */
async function openPdfDocument(
  run: PdfLoadRun,
  controls: PdfLoadControls,
  params: PdfLoadParams,
): Promise<{ pdfjs: typeof import('pdfjs-dist'); doc: PDFDocumentProxy }> {
  const { refs, setters } = params

  // EMB-032 — wait for a worker-pool slot BEFORE doing any of the work that
  // slot exists to bound (asset probing, the byte fetch, and the Worker
  // construction itself). `onQueued` only fires when the ceiling was
  // actually the reason this document is waiting, so the common,
  // under-ceiling case never flashes the waiting state.
  run.lease = await pdfWorkerPool.acquire(run.abort.signal, () => {
    if (!run.cancelled) setters.setStatus('queued')
  })
  if (run.cancelled) throw new PdfLoadCancelled()
  setters.setStatus('loading')

  // Assets first: a missing directory must fail with a name, not with a
  // blank page (FR-018b). Deliberately NOT covered by the first-page
  // watchdog below — see its own comment for why.
  run.stage = 'checking the PDF.js runtime assets'
  await ensureRuntimeAssets()
  if (run.cancelled) throw new PdfLoadCancelled()

  // The one and only reference to pdfjs-dist. Keep it dynamic.
  run.stage = 'loading the PDF.js runtime'
  const pdfjs = await import('pdfjs-dist')
  if (run.cancelled) throw new PdfLoadCancelled()

  const data = await acquirePdfBytes(run, params)
  if (run.cancelled) throw new PdfLoadCancelled()

  // The first-page deadline starts HERE — once the bytes are in hand — not
  // at mount and not while they were still downloading. Time spent queued
  // behind the pool's ceiling, checking assets, or downloading is a
  // legitimate, separately-explained wait; counting it against the
  // PARSER's deadline is what let a slow network masquerade as a wedged
  // worker.
  run.firstPageWatchdog = setTimeout(() => {
    controls.failLoad(
      new Error(
        `This PDF did not put a page on screen within ${Math.round(firstPageTimeoutMs() / 1000)} seconds. ` +
          `It stopped at: ${run.stage}. The parsing worker may have run out of memory or stopped responding — ` +
          `try again, and if it keeps happening this document may be too large or too damaged to render here.`,
      ),
    )
  }, firstPageTimeoutMs())

  // FR-019c — our own worker, handed to PDF.js as a port, so there is no
  // fake-worker fallback branch to fall into.
  run.stage = 'starting the PDF parsing worker'
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
  run.port = workerPort
  // `PDFWorker.create` rather than `new PDFWorker`: same object, but the
  // published .d.ts types the constructor's `port` as `null | undefined` (a
  // JSDoc default-value artefact) while `create`'s PDFWorkerParameters
  // types it as `Worker`.
  const pdfWorker = pdfjs.PDFWorker.create({ name: 'omnipus-library-pdf', port: workerPort })
  const workerFailed = createWorkerFailedRace(run, controls, workerPort)

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
  run.loadingTask = task
  run.stage = 'opening the document'
  const doc = await Promise.race([task.promise, workerFailed])
  if (run.cancelled) throw new PdfLoadCancelled()
  run.doc = doc
  refs.docRef.current = doc
  refs.pdfjsRef.current = pdfjs
  wireAnnotationStorage(doc, run, setters)
  setters.setPageCount(doc.numPages)

  return { pdfjs, doc }
}

/** Builds and renders ONE page's DOM (element, canvas, text layer),
 *  registers it in the per-page refs, and — the first time any page reaches
 *  screen — clears the first-page watchdog and flips `status` to `'ready'`.
 *  Returns whether the first page is now on screen, threaded back into the
 *  caller's loop instead of a shared mutable closure variable. */
async function renderOnePdfPage(
  n: number,
  page: PDFPageProxy,
  run: PdfLoadRun,
  controls: PdfLoadControls,
  params: PdfLoadParams,
  pdfjs: typeof import('pdfjs-dist'),
  width: number,
  ratio: number,
  firstPageOnScreen: boolean,
): Promise<boolean> {
  const { container, refs, setters } = params
  const unscaled = page.getViewport({ scale: 1 })
  const fit = (width - 32) / unscaled.width
  const scale = Math.min(MAX_SCALE, Math.max(MIN_SCALE, fit))
  const viewport = page.getViewport({ scale })
  // The reader may already be zoomed in before this load finishes — a Save
  // re-opens the SAME document at whatever zoom was active (`reloadNonce`,
  // not a fresh mount). `zoomRef` (not the `zoom` state) because this
  // effect does not depend on zoom and must read whatever is current when
  // it actually runs, not whatever it captured at closure-creation time.
  const renderScale = targetRasterScale(viewport.width, viewport.height, refs.zoomRef.current, ratio)

  const pageEl = document.createElement('div')
  pageEl.className = 'relative mx-auto my-4 shadow-lg'
  pageEl.style.width = `${viewport.width}px`
  pageEl.style.height = `${viewport.height}px`
  // The text layer sizes its spans from these; they must match the scale
  // the canvas was rendered at or selection lands off the glyphs.
  pageEl.style.setProperty('--scale-factor', String(scale))
  pageEl.style.setProperty('--total-scale-factor', String(scale))
  pageEl.setAttribute('data-testid', 'library-pdf-page')
  pageEl.setAttribute('data-page-number', String(n))

  const canvas = document.createElement('canvas')
  canvas.width = Math.floor(viewport.width * renderScale)
  canvas.height = Math.floor(viewport.height * renderScale)
  canvas.style.width = `${viewport.width}px`
  canvas.style.height = `${viewport.height}px`
  canvas.className = 'block h-full w-full bg-white'
  pageEl.appendChild(canvas)

  const textLayerEl = document.createElement('div')
  textLayerEl.className = 'omnipus-pdf-text-layer'
  pageEl.appendChild(textLayerEl)

  container.appendChild(pageEl)

  refs.pagesRef.current.set(n, page)
  refs.pageViewportsRef.current.set(n, viewport)
  refs.pageElsRef.current.set(n, pageEl)
  refs.pageCanvasElsRef.current.set(n, canvas)
  refs.paintedScaleRef.current.set(n, renderScale)

  const ctx = canvas.getContext('2d')
  if (!ctx) throw new Error('This browser did not provide a 2D canvas context.')

  const renderTask = page.render({
    canvas,
    canvasContext: ctx,
    viewport,
    transform: renderScale === 1 ? undefined : [renderScale, 0, 0, renderScale, 0, 0],
    // NB-17 — the BASE canvas stays read-only. ENABLE draws annotation
    // appearance streams (including already-filled form values) as static
    // graphics. ENABLE_FORMS and ENABLE_STORAGE are the modes that make the
    // CANVAS ITSELF paint live widgets, which this file still never uses —
    // Edit mode's interactivity comes entirely from the separate
    // AnnotationLayer overlaid on top.
    annotationMode: pdfjs.AnnotationMode.ENABLE,
    isEditing: false,
  })
  run.cancelRender.push(() => renderTask.cancel())

  const textLayer = new pdfjs.TextLayer({
    textContentSource: page.streamTextContent(),
    container: textLayerEl,
    viewport,
  })
  run.cancelRender.push(() => textLayer.cancel())

  await Promise.all([renderTask.promise, textLayer.render()])
  if (run.cancelled) throw new PdfLoadCancelled()

  let nowOnScreen = firstPageOnScreen
  if (!nowOnScreen) {
    // The first page is drawn and in the DOM — the one moment at which
    // showing the container is honest. The watchdog's job is done at
    // exactly the same instant, and not before: clearing it merely on
    // `appendChild` would leave a render that never finishes covered by
    // nothing at all.
    nowOnScreen = true
    controls.clearFirstPageWatchdog()
    setters.setStatus('ready')
  }

  const annotations = await page.getAnnotations({ intent: 'display' })
  if (run.cancelled) throw new PdfLoadCancelled()
  refs.pageAnnotationsRef.current.set(n, annotations)

  return nowOnScreen
}

/** Renders every page in the fragment- or whole-document page list, in
 *  order, waiting for each page's canvas + text layer before starting the
 *  next, then decides whether THIS load keeps holding its worker-pool
 *  lease. */
async function renderPdfPages(
  run: PdfLoadRun,
  controls: PdfLoadControls,
  params: PdfLoadParams,
  pdfjs: typeof import('pdfjs-dist'),
  doc: PDFDocumentProxy,
): Promise<void> {
  const { container, refs, setters, pageFragment, variant } = params

  // EMB-105 / US-12 AS-4 — a page-fragment embed renders ONE page, not the
  // whole document. Validated against the REAL page count this document
  // just reported (not against any earlier guess), so a fragment naming a
  // page beyond the document's end is a genuine, honest failure — never a
  // silently empty page.
  let pagesToRender: number[]
  if (pageFragment !== undefined) {
    if (!Number.isInteger(pageFragment) || pageFragment < 1 || pageFragment > doc.numPages) {
      throw new Error(`Page ${pageFragment} does not exist in this ${doc.numPages}-page PDF.`)
    }
    pagesToRender = [pageFragment]
  } else {
    pagesToRender = Array.from({ length: doc.numPages }, (_, i) => i + 1)
  }
  if (pagesToRender.length === 0) {
    // A zero-page document would otherwise fall straight through the loop
    // below with nothing appended and nothing thrown — the silently empty
    // pane, arrived at by a different road.
    throw new Error('This PDF reports no pages, so there is nothing to display.')
  }

  // `status` deliberately does NOT flip to 'ready' until a page is actually
  // on screen — flipping it earlier (right before the first `getPage()`)
  // would clear the spinner while the container is still empty, so a
  // worker that wedges without erroring shows a white box indistinguishable
  // from a blank first page. Until then this stays `loading`, and the
  // watchdog is what bounds it.
  const { width, fallback: widthIsFallback } = measureRenderWidth(container)
  if (widthIsFallback) {
    // Make the guess VISIBLE. Rendering every PDF at a plausible hardcoded
    // width is precisely the failure nobody can see, so the state is
    // written where a developer and a test can both read it.
    container.setAttribute('data-width-source', 'fallback')
  } else {
    container.removeAttribute('data-width-source')
  }
  const ratio = Math.min(window.devicePixelRatio || 1, MAX_PIXEL_RATIO)
  refs.deviceRatioRef.current = ratio
  let firstPageOnScreen = false

  for (const n of pagesToRender) {
    run.stage = `rendering page ${n}`
    const page: PDFPageProxy = await doc.getPage(n)
    if (run.cancelled) throw new PdfLoadCancelled()
    firstPageOnScreen = await renderOnePdfPage(n, page, run, controls, params, pdfjs, width, ratio, firstPageOnScreen)
  }

  // EMB-032 — deliberately NO releaseLease() here, and that is not an
  // oversight. Reaching this point means the document is open and its
  // first pass over every page is done, but its `PDFWorker` is not
  // finished being used: entering Edit mode mounts a real `AnnotationLayer`
  // against this SAME `doc`, and `handleSave` calls `doc.saveDocument()` —
  // both keep talking to this worker for as long as the component stays
  // mounted. The lease is held for the component's WHOLE mounted lifetime,
  // released only on failure, abandonment before its turn, or unmount (see
  // `usePdfLoadEffect`'s cleanup, and pdfWorkerPool.ts's own header for why
  // releasing on render success would break EMB-032's "at most two worker
  // instances" ceiling rather than honour it).
  if (!run.cancelled) {
    setters.setAllPagesRendered(true)
    if (variant === 'inline') {
      // SILENT-FAILURES-pdf-pool.md finding 2 — "a waiting PDF never opens
      // on a short note". An inline embed has no header to reach Edit mode
      // from at all, so nothing past this point — a static canvas already
      // drawn, and the D-37 zoom control, which is CSS `zoom` only, never a
      // re-render — ever talks to this worker again. Holding its pool slot
      // for the rest of this component's mounted lifetime is correct for
      // the PANE (Edit/Save keep using it), but on an inline embed it only
      // starves a queued sibling on a note too short to ever unmount
      // anything via LazyEmbedMount's 1800px margin. Ending the thread and
      // freeing the slot HERE is what makes "will open automatically once
      // another PDF finishes loading" true on a short page, not only a
      // long one.
      controls.endWorkerThreadThenReleaseLease()
    }
  }
}

async function runPdfLoad(run: PdfLoadRun, controls: PdfLoadControls, params: PdfLoadParams): Promise<void> {
  try {
    const { pdfjs, doc } = await openPdfDocument(run, controls, params)
    await renderPdfPages(run, controls, params, pdfjs, doc)
  } catch (err) {
    if (err instanceof PdfLoadCancelled || run.cancelled) {
      controls.clearFirstPageWatchdog()
      controls.endWorkerThreadThenReleaseLease()
      return
    }
    // SILENT-FAILURES-pdf-pool.md finding 8 — every abort THIS load starts
    // sets `cancelled` first (see `failLoad` and this effect's cleanup), so
    // reaching here with `cancelled` still false means something else
    // cancelled work this load never asked to end — latent today, but a
    // real, reportable failure if it ever happens, not a silent,
    // watchdog-disarmed return.
    if (isAbortError(err) || (err && typeof err === 'object' && (err as { name?: string }).name === 'RenderingCancelledException')) {
      controls.failLoad(err instanceof Error ? err : new Error(String(err)))
      return
    }
    // Every OTHER failure ends this load attempt for good. `failLoad` ends
    // the worker thread and releases the pool slot (so a queued document is
    // not held behind one that is never going to finish), stops the
    // watchdog, and renders the reason. The `workerFailed` path may already
    // have gone through the same function before this catch was reached;
    // its `loadFailed` latch is what makes calling it twice safe.
    controls.failLoad(err)
  }
}

/** The PDF document/page load effect — construction, the async open+render
 *  chain, and teardown. See this module's own header and
 *  LibraryPdfPreview.tsx's module header for the behavioural contract; this
 *  is a direct extraction with no behaviour change. */
export function usePdfLoadEffect(params: UsePdfLoadEffectParams): void {
  const { containerRef, workspaceId, entryName, entryPath, reloadNonce, pageFragment, variant, refs, setters } = params

  useEffect(() => {
    const container = containerRef.current
    if (!container) return

    const run = createPdfLoadRun()
    const controls = createPdfLoadControls(run, refs.savingRef, setters)

    resetForNewLoad(container, refs, setters)

    void runPdfLoad(run, controls, {
      container,
      workspaceId,
      entryName,
      entryPath,
      pageFragment,
      variant,
      refs,
      setters,
    })

    return () => {
      run.cancelled = true
      run.abort.abort()
      controls.clearFirstPageWatchdog()
      for (const cancel of run.cancelRender) {
        try {
          cancel()
        } catch {
          // A task that already settled throws on cancel; nothing to do.
        }
      }
      // A zoom/scroll-triggered redraw in flight when this load ends
      // (unmount, or a retry/reload starting a fresh load) is pointless
      // work against a page about to be torn down or replaced.
      for (const task of refs.pendingRasterTasksRef.current.values()) {
        try {
          task.cancel()
        } catch {
          // Already settled; nothing to do.
        }
      }
      refs.pendingRasterTasksRef.current.clear()

      // Ending the Worker THREAD is ours to do — see
      // `createPdfLoadControls` for the three measured reasons a bare
      // `loadingTask.destroy()` does not end a CALLER-SUPPLIED worker, and
      // for why the slot is held until the thread is confirmed gone rather
      // than freed first. Idempotent with whatever the async chain above
      // already did (a worker crash or any other in-flight failure).
      controls.endWorkerThreadThenReleaseLease()
    }
    // workspaceId/entryPath/reloadNonce/pageFragment/variant are this load's
    // real identity. containerRef/refs/setters/entryName are stable across
    // renders (refs and setState setters never change identity; entryName
    // tracks entryPath 1:1 for a given Library entry).
  }, [workspaceId, entryPath, reloadNonce, pageFragment, variant])
}

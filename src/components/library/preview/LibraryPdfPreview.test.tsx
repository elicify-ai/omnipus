// LibraryPdfPreview.test.tsx — the hardening and wiring contract of the PDF.js
// preview (ADR-067 spec tests 67, 74, and the component half of FR-018a/b and
// FR-019c).
//
// What this file can and cannot prove, stated up front, because the spec is
// explicit that a security test which cannot fail is worse than none:
//
//  CAN prove — the options this component actually hands PDF.js (XFA off,
//  read-only annotation mode, the four runtime-asset URLs), that a real Worker
//  is constructed and handed over as the transport (so PDF.js's silent
//  fake-worker fallback is unreachable), that a worker failure surfaces as a
//  visible error instead of a silent main-thread degrade, that a missing asset
//  directory produces an error naming it, that the bytes come from the
//  AUTHENTICATED Library endpoint, and that pdfjs-dist is not in the eager
//  module graph.
//
//  CANNOT prove — that the shipped bundle carries no eval path, that
//  pdf.sandbox*.mjs is absent, or that parsing genuinely ran off the main
//  thread in a real browser. Those are properties of the BUILT ARTEFACT and of
//  a live engine; they belong to spec tests 121, 84 and 96. Nothing here should
//  be read as covering them.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { useState } from 'react'
import type { ReactNode } from 'react'
import { render, screen, waitFor, fireEvent, act } from '@testing-library/react'
import type { PreviewHeaderSlotProvider as PreviewHeaderSlotProviderType } from './previewHeaderSlot'

// Rebound fresh in beforeEach, from the SAME `vi.resetModules()` generation
// as `./LibraryPdfPreview` — React Context identity is per-module-instance,
// and `LibraryPdfPreview.tsx`'s `PreviewHeaderPortal` reads
// `previewHeaderSlot.ts`'s context object from whichever module generation
// IT was imported under. A `PreviewHeaderSlotProvider` captured once at
// file-load time (before the first reset) would write into a stale, DIFFERENT
// context object than the fresh one the freshly-reset component reads from —
// the portal would then always see the default (`null`) value and never
// render its children. Same reasoning as `mockedPutBinary` below.
let PreviewHeaderSlotProvider: typeof PreviewHeaderSlotProviderType

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    putLibraryContentBinary: vi.fn(),
  }
})

import { libraryDownloadUrl } from '@/lib/api'
import type { LibraryEntry } from '@/lib/api'
import { useUiStore } from '@/store/ui'

// Rebound fresh in beforeEach after `vi.resetModules()` — like the
// `pdfjs-dist` mock, `vi.mock('@/lib/api', ...)`'s factory re-runs on every
// reset, so a binding captured once at file-load time would silently detach
// from the instance the component under test actually calls after the first
// test's reset.
let mockedPutBinary: ReturnType<typeof vi.fn>


// Real PDF.js values, copied from pdfjs-dist 6.2.108's AnnotationMode so the
// read-only assertion is against the library's own numbering rather than
// against whatever the component happens to pass.
const ANNOTATION_MODE = { DISABLE: 0, ENABLE: 1, ENABLE_FORMS: 2, ENABLE_STORAGE: 3 }
// Arbitrary but fixed test-only value — LibraryPdfPreview only ever reads
// this constant back off the SAME mocked module and forwards it verbatim
// into an ink annotationStorage entry, so its real pdfjs-dist numbering is
// irrelevant to what this suite can prove.
const ANNOTATION_EDITOR_TYPE = { INK: 15 }

const h = vi.hoisted(() => ({
  /** How many times the pdfjs-dist module was actually imported. Stays 0 until
   *  something renders — that is the laziness assertion. */
  moduleLoads: 0,
  /** Never reset. Guards the laziness test against being reordered — see it. */
  everLoaded: false,
  getDocumentArgs: [] as Record<string, unknown>[],
  renderArgs: [] as Record<string, unknown>[],
  textLayerArgs: [] as Record<string, unknown>[],
  pdfWorkerCreateArgs: [] as Record<string, unknown>[],
  lastPdfWorker: null as unknown,
  numPages: 1,
  /** Per-page raw annotation data `page.getAnnotations()` resolves with —
   *  what a real AcroForm widget's `getAnnotations({intent:'display'})`
   *  entry looks like is irrelevant here: the component only ever forwards
   *  this array untouched into `AnnotationLayer.render()`. */
  pageAnnotations: [] as unknown[],
  /** `doc.getFieldObjects()` resolution — null models "no AcroForm fields". */
  fieldObjects: null as Record<string, unknown[]> | null,
  /** `doc.saveDocument()` resolution. */
  savedBytes: new Uint8Array([1, 2, 3]),
  saveDocumentCalls: 0,
  annotationStorageSetValueCalls: [] as { key: string; value: unknown }[],
  annotationStorageRemoveCalls: [] as string[],
  annotationLayerConstructorArgs: [] as Record<string, unknown>[],
  annotationLayerRenderArgs: [] as Record<string, unknown>[],
}))

vi.mock('pdfjs-dist', () => {
  h.moduleLoads++
  h.everLoaded = true

  function makeAnnotationStorage() {
    let modified = false
    const storage = {
      onSetModified: null as (() => void) | null,
      onResetModified: null as (() => void) | null,
      setValue: (key: string, value: unknown) => {
        h.annotationStorageSetValueCalls.push({ key, value })
        modified = true
        storage.onSetModified?.()
      },
      remove: (key: string) => {
        h.annotationStorageRemoveCalls.push(key)
      },
      resetModified: () => {
        if (modified) {
          modified = false
          storage.onResetModified?.()
        }
      },
      get size() {
        return h.annotationStorageSetValueCalls.length - h.annotationStorageRemoveCalls.length
      },
    }
    return storage
  }

  const page = {
    getViewport: ({ scale }: { scale: number }) => ({
      width: 600 * scale,
      height: 800 * scale,
      rotation: 0,
      rawDims: { pageWidth: 600, pageHeight: 800 },
      clone: () => page.getViewport({ scale }),
      convertToPdfPoint: (x: number, y: number) => [x, y],
      convertToViewportPoint: (x: number, y: number) => [x, y],
    }),
    render: (args: Record<string, unknown>) => {
      h.renderArgs.push(args)
      return { promise: Promise.resolve(), cancel: () => {} }
    },
    streamTextContent: () => 'text-content-stream',
    getAnnotations: () => Promise.resolve(h.pageAnnotations),
  }
  class TextLayer {
    constructor(args: Record<string, unknown>) {
      h.textLayerArgs.push(args)
    }
    render() {
      return Promise.resolve()
    }
    cancel() {}
  }
  class AnnotationLayer {
    constructor(args: Record<string, unknown>) {
      h.annotationLayerConstructorArgs.push(args)
    }
    render(args: Record<string, unknown>) {
      h.annotationLayerRenderArgs.push(args)
      return Promise.resolve()
    }
  }
  return {
    AnnotationMode: ANNOTATION_MODE,
    AnnotationEditorType: ANNOTATION_EDITOR_TYPE,
    AnnotationLayer,
    TextLayer,
    PDFWorker: {
      create: (args: Record<string, unknown>) => {
        h.pdfWorkerCreateArgs.push(args)
        h.lastPdfWorker = { destroy: () => {} }
        return h.lastPdfWorker
      },
    },
    getDocument: (args: Record<string, unknown>) => {
      h.getDocumentArgs.push(args)
      return {
        promise: Promise.resolve({
          numPages: h.numPages,
          getPage: () => Promise.resolve(page),
          annotationStorage: makeAnnotationStorage(),
          getFieldObjects: () => Promise.resolve(h.fieldObjects),
          saveDocument: () => {
            h.saveDocumentCalls++
            return Promise.resolve(h.savedBytes)
          },
        }),
        destroy: () => Promise.resolve(),
      }
    },
  }
})

const ENTRY: LibraryEntry = {
  name: 'doc.pdf',
  path: 'reports/doc.pdf',
  is_dir: false,
  is_hidden: false,
  size: 4096,
  modified_at: '2026-08-22T10:15:00Z',
  is_text_editable: false,
}

const MANIFEST = {
  cmaps: ['78-EUC-H.bcmap'],
  standard_fonts: ['FoxitSans.pfb'],
  wasm: ['openjpeg.wasm'],
  iccs: ['sRGB.icc'],
}

interface FetchLog {
  url: string
  init?: RequestInit
}

let fetchLog: FetchLog[] = []
/** Asset paths (relative to /pdfjs/) the fake server should answer with the
 *  SPA's index.html and HTTP 200 — the FR-018b trap. */
let htmlShellFor: string[] = []
let workerConstructions: { url: string; options?: WorkerOptions }[] = []
let workerThrows: Error | null = null

function jsonResponse(body: unknown): Response {
  return {
    ok: true,
    status: 200,
    headers: new Headers({ 'content-type': 'application/json' }),
    json: () => Promise.resolve(body),
  } as unknown as Response
}

function binaryResponse(): Response {
  return {
    ok: true,
    status: 200,
    headers: new Headers({ 'content-type': 'application/octet-stream' }),
    arrayBuffer: () => Promise.resolve(new ArrayBuffer(8)),
  } as unknown as Response
}

function htmlShellResponse(): Response {
  return {
    ok: true,
    status: 200,
    headers: new Headers({ 'content-type': 'text/html; charset=utf-8' }),
    text: () => Promise.resolve('<!doctype html><div id="root"></div>'),
  } as unknown as Response
}

// LibraryPreviewPane normally owns the ONE header row every editable preview
// body portals its View/Edit/Save controls into (see previewHeaderSlot.tsx) —
// without a real slot element, PreviewHeaderPortal renders nothing at all,
// which would make every Edit-mode test below unable to find its own
// buttons. This harness reproduces just that slot, not the rest of the pane.
function HeaderSlotHarness({ children }: { children: ReactNode }) {
  const [slot, setSlot] = useState<HTMLDivElement | null>(null)
  return (
    <div>
      <div ref={setSlot} data-testid="header-slot" />
      <PreviewHeaderSlotProvider slot={slot}>{children}</PreviewHeaderSlotProvider>
    </div>
  )
}

async function renderPreview() {
  const mod = await import('./LibraryPdfPreview')
  return {
    mod,
    ...render(
      <HeaderSlotHarness>
        <mod.LibraryPdfPreview workspaceId="ws-1" entry={ENTRY} />
      </HeaderSlotHarness>,
    ),
  }
}

beforeEach(async () => {
  // A mocked module's factory runs once per module registry. Without a reset
  // the laziness counter would be stuck at 1 from whichever test ran first,
  // and the laziness assertion would be measuring test order rather than the
  // import graph.
  vi.resetModules()
  h.moduleLoads = 0
  h.getDocumentArgs = []
  h.renderArgs = []
  h.textLayerArgs = []
  h.pdfWorkerCreateArgs = []
  h.lastPdfWorker = null
  h.numPages = 1
  h.pageAnnotations = []
  h.fieldObjects = null
  h.savedBytes = new Uint8Array([1, 2, 3])
  h.saveDocumentCalls = 0
  h.annotationStorageSetValueCalls = []
  h.annotationStorageRemoveCalls = []
  h.annotationLayerConstructorArgs = []
  h.annotationLayerRenderArgs = []
  fetchLog = []
  htmlShellFor = []
  workerConstructions = []
  workerThrows = null
  useUiStore.setState({ toasts: [] })

  const mod = await import('./LibraryPdfPreview')
  mod.__resetPdfAssetProbeForTests()

  // Same module-registry generation as `mod` above — see the `let
  // PreviewHeaderSlotProvider` doc comment for why this must be re-imported
  // per test rather than bound once at file load.
  const headerSlotModule = await import('./previewHeaderSlot')
  PreviewHeaderSlotProvider = headerSlotModule.PreviewHeaderSlotProvider

  const api = await import('@/lib/api')
  mockedPutBinary = vi.mocked(api.putLibraryContentBinary)
  mockedPutBinary.mockReset()
  mockedPutBinary.mockResolvedValue({
    name: 'doc.pdf',
    path: 'reports/doc.pdf',
    is_dir: false,
    is_hidden: false,
    size: 4096,
    modified_at: '2026-08-22T10:15:00Z',
    is_text_editable: false,
  })

  vi.stubGlobal(
    'fetch',
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      fetchLog.push({ url, init })
      if (url.startsWith('/pdfjs/')) {
        const rel = url.slice('/pdfjs/'.length)
        if (htmlShellFor.includes(rel)) return Promise.resolve(htmlShellResponse())
        if (rel === 'asset-manifest.json') return Promise.resolve(jsonResponse(MANIFEST))
        return Promise.resolve(binaryResponse())
      }
      if (url.includes('/api/v1/library/')) return Promise.resolve(binaryResponse())
      return Promise.reject(new Error(`unexpected fetch: ${url}`))
    }),
  )

  vi.stubGlobal(
    'Worker',
    class FakeWorker {
      constructor(url: string | URL, options?: WorkerOptions) {
        if (workerThrows) throw workerThrows
        workerConstructions.push({ url: String(url), options })
      }
      terminate() {}
    },
  )

  // jsdom has no 2D canvas implementation. The main page canvas only needs a
  // context object to hand to PDF.js (mocked, never calls back into it), but
  // LibrarySignaturePad's freehand drawing and LibraryPdfPreview's own
  // signature-preview overlay both draw with REAL Canvas 2D calls — so this
  // stub needs every method/property either actually touches.
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue({
    scale: () => {},
    beginPath: () => {},
    moveTo: () => {},
    lineTo: () => {},
    stroke: () => {},
    clearRect: () => {},
    lineCap: '',
    lineJoin: '',
    lineWidth: 0,
    strokeStyle: '',
  } as unknown as CanvasRenderingContext2D)

  // jsdom does not implement the Pointer Events capture methods at all (not
  // even as a throwing stub `vi.spyOn` could wrap) — the real
  // signature-drawing canvas calls them directly (setPointerCapture on
  // pointerdown, release/has on pointerup) so drawing keeps tracking a
  // pointer that leaves the canvas mid-stroke.
  const elementProto = HTMLElement.prototype as unknown as {
    setPointerCapture: (id: number) => void
    releasePointerCapture: (id: number) => void
    hasPointerCapture: (id: number) => boolean
  }
  elementProto.setPointerCapture = () => {}
  elementProto.releasePointerCapture = () => {}
  elementProto.hasPointerCapture = () => true
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

/** The single getDocument() call the component made. */
async function documentOptions(): Promise<Record<string, unknown>> {
  await waitFor(() => expect(h.getDocumentArgs).toHaveLength(1))
  return h.getDocumentArgs[0]
}

describe('LibraryPdfPreview — laziness (FR-018)', () => {
  // MUST BE THE FIRST describe IN THIS FILE. Vitest instantiates a mocked
  // module once per file and `vi.resetModules()` does not re-run the factory,
  // so `moduleLoads` can only be observed transitioning 0 -> 1 on the very
  // first render. Reordering this block does NOT make it pass silently — the
  // guard below fails with this explanation.
  it('does not load pdfjs-dist until a PDF is actually opened', async () => {
    expect(
      h.everLoaded,
      'pdfjs-dist was already loaded before this test ran — move this describe block back to the top of the file (see the comment above it)',
    ).toBe(false)

    // Importing the component module must not drag the parser in with it.
    await import('./LibraryPdfPreview')
    // MUTATION THIS DIES ON: convert the `await import('pdfjs-dist')` inside
    // the effect into a top-level `import ... from 'pdfjs-dist'`. The mock
    // factory then runs at module-import time and this count is 1 before
    // anything rendered — which is exactly the ~1.6 MB of parser landing in
    // the initial payload.
    expect(h.moduleLoads).toBe(0)

    await renderPreview()
    await documentOptions()
    expect(h.moduleLoads).toBe(1)
  })
})

describe('LibraryPdfPreview — hardening at the call site (spec test 67)', () => {
  it('disables XFA on getDocument', async () => {
    await renderPreview()
    // MUTATION THIS DIES ON: delete `enableXfa: false` from the getDocument
    // options (or flip it to true) in LibraryPdfPreview.tsx. XFA is a scripting
    // surface and D15.7 requires it off.
    expect(await documentOptions()).toMatchObject({ enableXfa: false })
  })

  it('does not pass isEvalSupported, which no longer exists in pdfjs-dist 6.2.108', async () => {
    await renderPreview()
    // MUTATION THIS DIES ON: adding `isEvalSupported: false` back to the
    // getDocument options. It reads like hardening and is not: the option was
    // removed upstream, so PDF.js ignores the key and any assertion on it
    // passes forever. The no-eval property belongs to the build gate over the
    // shipped artefact (spec test 121), not here.
    expect(await documentOptions()).not.toHaveProperty('isEvalSupported')
  })
})

describe('LibraryPdfPreview — read-only rendering (spec test 74, NB-17)', () => {
  it('renders annotations read-only: never ENABLE_FORMS, never ENABLE_STORAGE, never editing', async () => {
    await renderPreview()
    await waitFor(() => expect(h.renderArgs).toHaveLength(1))
    const opts = h.renderArgs[0]
    // MUTATION THIS DIES ON: change `annotationMode` to
    // AnnotationMode.ENABLE_FORMS (or ENABLE_STORAGE), or set
    // `isEditing: true`. Those are the modes that turn a read-only page into
    // live, editable form widgets — the capability NB-17 keeps out of this
    // release.
    expect(opts.annotationMode).toBe(ANNOTATION_MODE.ENABLE)
    expect(opts.annotationMode).not.toBe(ANNOTATION_MODE.ENABLE_FORMS)
    expect(opts.annotationMode).not.toBe(ANNOTATION_MODE.ENABLE_STORAGE)
    expect(opts.isEditing).toBe(false)
  })

  it('builds a text layer over every page so text can be selected and searched', async () => {
    h.numPages = 2
    await renderPreview()
    // MUTATION THIS DIES ON: delete the `new pdfjs.TextLayer({...})` block.
    // The canvas alone renders a picture of a document: no selection, no
    // in-page search, nothing for a screen reader.
    await waitFor(() => expect(h.textLayerArgs).toHaveLength(2))
    expect(h.textLayerArgs[0]).toMatchObject({ textContentSource: 'text-content-stream' })
    expect(h.textLayerArgs[0].container).toBeInstanceOf(HTMLElement)
  })
})

describe('LibraryPdfPreview — runtime assets (FR-018a/b)', () => {
  it('points PDF.js at all four runtime asset directories', async () => {
    await renderPreview()
    const opts = await documentOptions()
    // MUTATION THIS DIES ON: drop any one of cMapUrl / standardFontDataUrl /
    // wasmUrl / iccUrl (or cMapPacked / useWasm). Each omission degrades
    // SILENTLY and specifically — no cmaps means a CJK PDF renders blank, no
    // standard_fonts means wrong metrics, no wasm means a scanned PDF loses
    // its images. None of them raises an error on its own.
    expect(opts).toMatchObject({
      cMapUrl: '/pdfjs/cmaps/',
      cMapPacked: true,
      standardFontDataUrl: '/pdfjs/standard_fonts/',
      wasmUrl: '/pdfjs/wasm/',
      useWasm: true,
      iccUrl: '/pdfjs/iccs/',
    })
  })

  it('names the missing directory when an asset 404s, instead of rendering blank', async () => {
    vi.mocked(fetch).mockImplementation((input: RequestInfo | URL) => {
      const url = String(input)
      if (url === '/pdfjs/asset-manifest.json') return Promise.resolve(jsonResponse(MANIFEST))
      if (url === '/pdfjs/cmaps/78-EUC-H.bcmap') {
        return Promise.resolve({ ok: false, status: 404, headers: new Headers() } as unknown as Response)
      }
      return Promise.resolve(binaryResponse())
    })
    await renderPreview()
    // MUTATION THIS DIES ON: delete the `if (!res.ok)` branch in fetchAsset, or
    // stop probing the asset directories at all. The pane would then show a
    // blank page with no indication that character maps are missing.
    const alert = await screen.findByRole('alert')
    expect(alert.textContent).toContain('cmaps')
    expect(alert.textContent).toContain('404')
    expect(h.getDocumentArgs).toHaveLength(0)
  })

  it('treats an HTTP 200 index.html for an asset as a failure, not a success', async () => {
    // The exact FR-018b trap: newSPAHandler answers any unknown path with
    // index.html and HTTP 200, so `res.ok` is true for a file that does not
    // exist.
    htmlShellFor = ['standard_fonts/FoxitSans.pfb']
    await renderPreview()
    // MUTATION THIS DIES ON: delete the `content-type: text/html` check in
    // fetchAsset. With only `res.ok` consulted, this scenario reports success
    // and the PDF renders with wrong metrics and no error anywhere.
    const alert = await screen.findByRole('alert')
    expect(alert.textContent).toContain('standard_fonts')
    expect(h.getDocumentArgs).toHaveLength(0)
  })
})

describe('LibraryPdfPreview — the parser runs on a real worker (FR-019c)', () => {
  it('constructs a module Worker and hands it to PDF.js as the transport', async () => {
    await renderPreview()
    await documentOptions()
    // MUTATION THIS DIES ON: remove `worker: pdfWorker` from the getDocument
    // options (or stop constructing the Worker and let PDF.js bootstrap its
    // own). PDF.js's own bootstrap has a fallback branch that silently moves
    // parsing to the main thread with `warn("Setting up fake worker.")` as the
    // only symptom. Handing it a port removes that branch entirely.
    expect(workerConstructions).toHaveLength(1)
    expect(workerConstructions[0].url).toBe('/pdfjs/pdf.worker.min.mjs')
    expect(workerConstructions[0].options).toMatchObject({ type: 'module' })
    expect(h.pdfWorkerCreateArgs).toHaveLength(1)
    expect(h.pdfWorkerCreateArgs[0].port).toBeInstanceOf(Worker)
    expect(h.getDocumentArgs[0].worker).toBe(h.lastPdfWorker)
  })

  it('fails visibly when the worker cannot start, rather than parsing on the main thread', async () => {
    workerThrows = new Error('Refused to create a worker: violates Content-Security-Policy')
    await renderPreview()
    // MUTATION THIS DIES ON: swallow the Worker constructor failure and call
    // getDocument without a worker. That is the silent main-thread fallback
    // FR-019c forbids — it looks like success and is the slowest, least
    // isolated path through the parser.
    const alert = await screen.findByRole('alert')
    expect(alert.textContent).toContain('worker')
    expect(h.getDocumentArgs).toHaveLength(0)
  })
})

describe('LibraryPdfPreview — where the bytes come from', () => {
  it('reads the PDF over the authenticated Library endpoint with credentials', async () => {
    await renderPreview()
    await documentOptions()
    const expected = libraryDownloadUrl('ws-1', 'reports/doc.pdf')
    const call = fetchLog.find((f) => f.url === expected)
    // MUTATION THIS DIES ON: switch the byte fetch to the unauthenticated
    // /library-preview/<token>/ path, or drop `credentials: 'include'`. The
    // token path exists for sandboxed DOCUMENTS; a PDF is never a document
    // here, and §10.4 deliberately leaves .pdf off the inline allow-list.
    expect(call, `no fetch of ${expected} in ${JSON.stringify(fetchLog.map((f) => f.url))}`).toBeTruthy()
    expect(call?.init?.credentials).toBe('include')
    expect(fetchLog.some((f) => f.url.includes('/library-preview/'))).toBe(false)
  })

  it('surfaces a failed read instead of an empty pane', async () => {
    vi.mocked(fetch).mockImplementation((input: RequestInfo | URL) => {
      const url = String(input)
      if (url.startsWith('/pdfjs/')) {
        const rel = url.slice('/pdfjs/'.length)
        return Promise.resolve(rel === 'asset-manifest.json' ? jsonResponse(MANIFEST) : binaryResponse())
      }
      return Promise.resolve({ ok: false, status: 403, headers: new Headers() } as unknown as Response)
    })
    await renderPreview()
    // MUTATION THIS DIES ON: drop the `if (!res.ok)` check in fetchPdfBytes.
    // PDF.js would then be handed the error page's bytes and fail with an
    // opaque parser message, or with nothing at all.
    const alert = await screen.findByRole('alert')
    expect(alert.textContent).toContain('403')
  })
})

// ── Feature B — PDF form-fill and drawn signature ──────────────────────────
// (library-b-c-design-2026-09-07.md). These tests exercise the boundary this
// component actually owns: the Edit-mode toggle, wiring a REAL
// `annotationStorage` object into PDF.js's `AnnotationLayer` so a widget's
// own change listener can mark the document dirty, the honest "no fields"
// state, and the save round-trip to `putLibraryContentBinary`. What PDF.js
// itself does inside a real `<input>` once `AnnotationLayer.render()` runs is
// out of this suite's reach (that mechanism is measured separately — ADR-067
// D15.3 — against a real pdfjs-dist build, not this mock).

async function enterEditMode() {
  const editButton = await screen.findByTestId('library-pdf-mode-edit')
  await waitFor(() => expect(editButton).not.toBeDisabled())
  fireEvent.click(editButton)
}

describe('LibraryPdfPreview — Edit mode toggle', () => {
  it('starts in View mode and offers a disabled Edit toggle while the document is still loading', async () => {
    await renderPreview()
    const viewButton = screen.getByTestId('library-pdf-mode-view')
    const editButton = screen.getByTestId('library-pdf-mode-edit')
    expect(viewButton).toHaveAttribute('aria-pressed', 'true')
    expect(editButton).toHaveAttribute('aria-pressed', 'false')
    // No Save/Add-signature affordance outside Edit mode.
    expect(screen.queryByTestId('library-pdf-save')).not.toBeInTheDocument()
    expect(screen.queryByTestId('library-pdf-add-signature')).not.toBeInTheDocument()
  })

  it('switches to Edit mode and mounts a real PDF.js AnnotationLayer, wired to the document\'s own annotationStorage', async () => {
    await renderPreview()
    await enterEditMode()

    expect(screen.getByTestId('library-pdf-mode-edit')).toHaveAttribute('aria-pressed', 'true')
    // MUTATION THIS DIES ON: entering Edit mode without ever constructing an
    // AnnotationLayer — AcroForm fields would render, but stay inert.
    await waitFor(() => expect(h.annotationLayerConstructorArgs).toHaveLength(1))
    await waitFor(() => expect(h.annotationLayerRenderArgs).toHaveLength(1))
    expect(h.annotationLayerRenderArgs[0]).toMatchObject({ renderForms: true, enableScripting: false })
    // MUTATION THIS DIES ON: constructing the layer with a FRESH
    // AnnotationStorage instead of the document's own — a widget's fill
    // would then never reach saveDocument().
    expect(h.annotationLayerConstructorArgs[0].annotationStorage).toBeTruthy()

    // "Add signature" is offered the moment Edit mode is entered.
    expect(screen.getByTestId('library-pdf-add-signature')).toBeInTheDocument()
  })

  it('tears the AnnotationLayer back down when returning to View, without touching annotationStorage', async () => {
    await renderPreview()
    await enterEditMode()
    await waitFor(() => expect(h.annotationLayerConstructorArgs).toHaveLength(1))

    fireEvent.click(screen.getByTestId('library-pdf-mode-view'))

    // MUTATION THIS DIES ON: leaving the annotation-layer <div> mounted in
    // View mode — the read-only pane must show a flat picture, not live
    // widgets (NB-17's base-render contract still applies in View mode).
    await waitFor(() => expect(document.querySelector('.omnipus-pdf-annotation-layer')).toBeNull())
    expect(h.annotationStorageRemoveCalls).toHaveLength(0)
  })
})

describe('LibraryPdfPreview — no AcroForm fields (honest state)', () => {
  it('offers no fill affordance but still offers a signature when the PDF has no fields', async () => {
    h.fieldObjects = null
    await renderPreview()
    await enterEditMode()

    await waitFor(() =>
      expect(screen.getByTestId('library-pdf-no-fields-note').textContent).toContain('no fillable form fields'),
    )
    // MUTATION THIS DIES ON: hiding "Add signature" whenever there are no
    // form fields — library-b-c-design-2026-09-07.md is explicit that the
    // two are independent: "a drawn signature is still offered."
    expect(screen.getByTestId('library-pdf-add-signature')).toBeInTheDocument()
  })

  it('does not show the no-fields note when the PDF does have AcroForm fields', async () => {
    h.fieldObjects = { name: [{ id: '1' }] }
    h.pageAnnotations = [{ id: '1', fieldName: 'name', rect: [0, 0, 10, 10] }]
    await renderPreview()
    await enterEditMode()

    await waitFor(() => expect(h.annotationLayerRenderArgs).toHaveLength(1))
    expect(screen.queryByTestId('library-pdf-no-fields-note')).not.toBeInTheDocument()
  })
})

describe('LibraryPdfPreview — a fill interaction marks the document dirty', () => {
  it('enables Save once the wired annotationStorage reports a field was filled', async () => {
    h.fieldObjects = { name: [{ id: '1' }] }
    h.pageAnnotations = [{ id: '1', fieldName: 'name', rect: [0, 0, 10, 10] }]
    await renderPreview()
    await enterEditMode()
    await waitFor(() => expect(h.annotationLayerConstructorArgs).toHaveLength(1))

    const saveButton = screen.getByTestId('library-pdf-save')
    expect(saveButton).toBeDisabled()

    // Exactly what a real AcroForm text widget's own `input` listener does
    // internally (verified against pdf.mjs's TextWidgetAnnotationElement.render:
    // `storage.setValue(id, { value: event.target.value })`) — called here on
    // the SAME storage object the component wired into AnnotationLayer.
    const annotationStorage = h.annotationLayerConstructorArgs[0].annotationStorage as {
      setValue: (key: string, value: unknown) => void
    }
    fireEvent.click(screen.getByTestId('library-pdf-mode-edit')) // no-op, already in edit; keeps intent explicit
    act(() => annotationStorage.setValue('1', { value: 'Jane Doe' }))

    // MUTATION THIS DIES ON: not wiring `annotationStorage.onSetModified` to
    // dirty state — a filled field would silently fail to enable Save.
    await waitFor(() => expect(saveButton).not.toBeDisabled())
  })
})

describe('LibraryPdfPreview — signature placement', () => {
  it('places a drawn signature into the SAME annotationStorage the AnnotationLayer uses, and marks the document dirty', async () => {
    await renderPreview()
    await enterEditMode()
    await waitFor(() => expect(h.annotationLayerConstructorArgs).toHaveLength(1))

    fireEvent.click(screen.getByTestId('library-pdf-add-signature'))
    const canvas = await screen.findByTestId('library-pdf-signature-canvas')

    fireEvent.pointerDown(canvas, { clientX: 10, clientY: 10, pointerId: 1 })
    fireEvent.pointerMove(canvas, { clientX: 40, clientY: 30, pointerId: 1 })
    fireEvent.pointerUp(canvas, { clientX: 40, clientY: 30, pointerId: 1 })

    fireEvent.click(screen.getByTestId('library-pdf-signature-insert'))

    // MUTATION THIS DIES ON: building the ink entry against a DIFFERENT
    // AnnotationStorage than the one AnnotationLayer/saveDocument share —
    // the signature would be invisible to saveDocument().
    expect(h.annotationStorageSetValueCalls).toHaveLength(1)
    const [{ key, value }] = h.annotationStorageSetValueCalls as [{ key: string; value: Record<string, unknown> }]
    expect(key.startsWith('pdfjs_internal_editor_')).toBe(true)
    expect(value.annotationType).toBe(15)
    expect(value.deleted).toBe(false)

    // A visible, removable preview appears, and Save is now enabled.
    expect(screen.getByTestId('library-pdf-signature-preview')).toBeInTheDocument()
    await waitFor(() => expect(screen.getByTestId('library-pdf-save')).not.toBeDisabled())

    // Removing it clears both the preview and the storage entry.
    fireEvent.click(screen.getByTestId(`library-pdf-signature-remove-${key}`))
    expect(h.annotationStorageRemoveCalls).toEqual([key])
    expect(screen.queryByTestId('library-pdf-signature-preview')).not.toBeInTheDocument()
  })
})

describe('LibraryPdfPreview — Save', () => {
  it('saves via doc.saveDocument() -> base64 -> putLibraryContentBinary, and keeps entries on failure', async () => {
    h.fieldObjects = { name: [{ id: '1' }] }
    h.pageAnnotations = [{ id: '1', fieldName: 'name', rect: [0, 0, 10, 10] }]
    h.savedBytes = new Uint8Array([1, 2, 3])
    await renderPreview()
    await enterEditMode()
    await waitFor(() => expect(h.annotationLayerConstructorArgs).toHaveLength(1))

    const annotationStorage = h.annotationLayerConstructorArgs[0].annotationStorage as {
      setValue: (key: string, value: unknown) => void
    }
    act(() => annotationStorage.setValue('1', { value: 'Jane Doe' }))
    const saveButton = await screen.findByTestId('library-pdf-save')
    await waitFor(() => expect(saveButton).not.toBeDisabled())

    // --- failure path first: entries must survive it ---
    mockedPutBinary.mockRejectedValueOnce(new Error('network down'))
    fireEvent.click(saveButton)
    await screen.findByText(/network down/i)
    // MUTATION THIS DIES ON: clearing dirty state or annotationStorage on a
    // failed save — library-b-c-design-2026-09-07.md requires the entry
    // survive a failed save for a retry.
    expect(screen.getByTestId('library-pdf-save')).not.toBeDisabled()
    expect(screen.getByTestId('library-pdf-mode-edit')).toHaveAttribute('aria-pressed', 'true')

    // --- retry, succeeds ---
    mockedPutBinary.mockResolvedValueOnce({
      name: 'doc.pdf',
      path: 'reports/doc.pdf',
      is_dir: false,
      is_hidden: false,
      size: 3,
      modified_at: '2026-08-22T10:15:00Z',
      is_text_editable: false,
    })
    fireEvent.click(screen.getByTestId('library-pdf-save'))

    // 2, not 1: the earlier failed attempt also called doc.saveDocument() —
    // building the bytes and encoding them is not what failed, the PUT was.
    await waitFor(() => expect(h.saveDocumentCalls).toBe(2))
    // 2, not 1: the earlier failed attempt also called putLibraryContentBinary
    // — it rejected, it wasn't skipped. Index [1] is that SECOND, successful
    // call.
    await waitFor(() => expect(mockedPutBinary).toHaveBeenCalledTimes(2))
    const [wsArg, bodyArg] = mockedPutBinary.mock.calls[1] as [string, { path: string; content_base64: string }]
    expect(wsArg).toBe('ws-1')
    expect(bodyArg.path).toBe('reports/doc.pdf')
    // Independent oracle: decode the base64 back to bytes via the platform's
    // own atob, rather than re-deriving it with the encoder under test.
    const decoded = Uint8Array.from(atob(bodyArg.content_base64), (c) => c.charCodeAt(0))
    expect(Array.from(decoded)).toEqual([1, 2, 3])

    // A saved PDF re-opens showing the entered values: the component
    // re-parses the just-saved bytes (no extra network fetch for them) and
    // returns to View mode.
    await waitFor(() => expect(h.getDocumentArgs).toHaveLength(2))
    expect(h.getDocumentArgs[1].data).toBe(h.savedBytes)
    await waitFor(() => expect(screen.getByTestId('library-pdf-mode-view')).toHaveAttribute('aria-pressed', 'true'))
    const libraryGetCount = fetchLog.filter((f) => f.url.includes('/api/v1/library/')).length
    expect(libraryGetCount).toBe(1)
  })
})


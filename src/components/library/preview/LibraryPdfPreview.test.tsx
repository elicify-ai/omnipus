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
import { render, screen, waitFor } from '@testing-library/react'
import { libraryDownloadUrl } from '@/lib/api'
import type { LibraryEntry } from '@/lib/api'

// Real PDF.js values, copied from pdfjs-dist 6.2.108's AnnotationMode so the
// read-only assertion is against the library's own numbering rather than
// against whatever the component happens to pass.
const ANNOTATION_MODE = { DISABLE: 0, ENABLE: 1, ENABLE_FORMS: 2, ENABLE_STORAGE: 3 }

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
}))

vi.mock('pdfjs-dist', () => {
  h.moduleLoads++
  h.everLoaded = true
  const page = {
    getViewport: ({ scale }: { scale: number }) => ({
      width: 600 * scale,
      height: 800 * scale,
      rotation: 0,
      rawDims: { pageWidth: 600, pageHeight: 800 },
    }),
    render: (args: Record<string, unknown>) => {
      h.renderArgs.push(args)
      return { promise: Promise.resolve(), cancel: () => {} }
    },
    streamTextContent: () => 'text-content-stream',
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
  return {
    AnnotationMode: ANNOTATION_MODE,
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

async function renderPreview() {
  const mod = await import('./LibraryPdfPreview')
  return { mod, ...render(<mod.LibraryPdfPreview workspaceId="ws-1" entry={ENTRY} />) }
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
  fetchLog = []
  htmlShellFor = []
  workerConstructions = []
  workerThrows = null

  const mod = await import('./LibraryPdfPreview')
  mod.__resetPdfAssetProbeForTests()

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

  // jsdom has no 2D canvas implementation; the component only needs a context
  // object to hand to PDF.js, which is mocked.
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(
    {} as unknown as CanvasRenderingContext2D,
  )
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


// LibraryPdfPreview.variant.test.tsx — the `pane`/`inline` layout switch
// ONLY (ADR-083 embedded-content spec, EMB-027/028). Worker pooling, Edit
// mode, and every other behaviour of this component are covered by
// LibraryPdfPreview.test.tsx and LibraryPdfPreview.pool.test.tsx and must not
// move; this file exists purely to prove `variant` changes layout and
// nothing else.
//
// The false-green trap named in the spec's own review for this family of
// test: asserting state identifiers and text are equal across variants is
// trivially true of a component that ignores `variant` entirely. The
// positive control — the outermost container's class list actually DIFFERS
// — is what proves the prop was read at all.
//
// This file's pdfjs-dist / Worker / fetch mocks are a deliberately MINIMAL
// subset of LibraryPdfPreview.test.tsx's own (no Edit-mode annotation layer,
// no signature machinery) — this suite only needs a document that opens
// cleanly, or one whose Worker construction fails synchronously, neither of
// which touches that machinery.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, waitFor } from '@testing-library/react'
import type { LibraryEntry } from '@/lib/api'

vi.mock('pdfjs-dist', () => {
  const page = {
    getViewport: ({ scale }: { scale: number }) => ({ width: 600 * scale, height: 800 * scale, rotation: 0 }),
    render: () => ({ promise: Promise.resolve(), cancel: () => {} }),
    streamTextContent: () => ({}),
    getAnnotations: () => Promise.resolve([]),
  }
  class TextLayer {
    render() {
      return Promise.resolve()
    }
    cancel() {}
  }
  return {
    AnnotationMode: { ENABLE: 1 },
    AnnotationEditorType: { INK: 15 },
    AnnotationLayer: class {},
    TextLayer,
    PDFWorker: {
      create: () => ({ destroy: () => Promise.resolve() }),
    },
    getDocument: () => ({
      promise: Promise.resolve({
        numPages: 1,
        getPage: () => Promise.resolve(page),
        annotationStorage: { onSetModified: null, onResetModified: null, setValue: () => {}, remove: () => {}, resetModified: () => {} },
        getFieldObjects: () => Promise.resolve(null),
        saveDocument: () => Promise.resolve(new Uint8Array()),
      }),
      destroy: () => Promise.resolve(),
    }),
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

let workerThrows: Error | null = null

beforeEach(async () => {
  vi.resetModules()
  workerThrows = null

  vi.stubGlobal(
    'fetch',
    vi.fn((input: RequestInfo | URL) => {
      const url = String(input)
      if (url.startsWith('/pdfjs/')) {
        const rel = url.slice('/pdfjs/'.length)
        return Promise.resolve(rel === 'asset-manifest.json' ? jsonResponse(MANIFEST) : binaryResponse())
      }
      if (url.includes('/api/v1/library/')) return Promise.resolve(binaryResponse())
      return Promise.reject(new Error(`unexpected fetch: ${url}`))
    }),
  )

  vi.stubGlobal(
    'Worker',
    class FakeWorker extends EventTarget {
      constructor() {
        super()
        if (workerThrows) throw workerThrows
      }
      terminate() {}
      postMessage() {}
    },
  )

  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue({} as unknown as CanvasRenderingContext2D)
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

async function renderVariant(variant: 'pane' | 'inline' | undefined) {
  const mod = await import('./LibraryPdfPreview')
  return render(<mod.LibraryPdfPreview workspaceId="ws-1" entry={ENTRY} {...(variant !== undefined ? { variant } : {})} />)
}

describe('LibraryPdfPreview — variant default', () => {
  it('defaults to "pane" when the prop is omitted, matching every existing call site', async () => {
    const r = await renderVariant(undefined)
    const container = await r.findByTestId('library-pdf-preview')
    expect(container).toHaveAttribute('data-variant', 'pane')
  })
})

describe('LibraryPdfPreview — inline vs. pane (EMB-027: same renderer, EMB-028: layout only)', () => {
  it('reaches the SAME ready state (page count, no error) in both variants', async () => {
    const pane = await renderVariant('pane')
    await waitFor(() => expect(pane.queryByTestId('library-pdf-loading')).not.toBeInTheDocument())
    const panePages = pane.getByTestId('library-pdf-pages')
    const paneAriaLabel = panePages.getAttribute('aria-label')
    const paneContainerClass = pane.getByTestId('library-pdf-preview').className
    pane.unmount()

    const inline = await renderVariant('inline')
    await waitFor(() => expect(inline.queryByTestId('library-pdf-loading')).not.toBeInTheDocument())
    const inlinePages = inline.getByTestId('library-pdf-pages')

    // The state — page count, readiness — is identical.
    expect(inlinePages.getAttribute('aria-label')).toBe(paneAriaLabel)

    // The positive control: the outermost container's layout classes DIFFER.
    // MUTATION THIS DIES ON: a component that computes the same className
    // string regardless of `variant` — every assertion above this line would
    // still pass on that mutant.
    const inlineContainerClass = inline.getByTestId('library-pdf-preview').className
    expect(inlineContainerClass).not.toBe(paneContainerClass)
    expect(inlineContainerClass).not.toContain('flex-1')
    expect(inline.getByTestId('library-pdf-preview')).toHaveAttribute('data-variant', 'inline')
  })

  it('renders the SAME error text in both variants when the worker cannot start (EMB-028: non-happy states are unchanged)', async () => {
    workerThrows = new Error('Refused to create a worker: violates Content-Security-Policy')
    const pane = await renderVariant('pane')
    const paneAlert = await pane.findByRole('alert')
    const paneAlertText = paneAlert.textContent
    pane.unmount()

    workerThrows = new Error('Refused to create a worker: violates Content-Security-Policy')
    const inline = await renderVariant('inline')
    const inlineAlert = await inline.findByRole('alert')
    // MUTATION THIS DIES ON: a variant branch that swaps in a different
    // error surface, or suppresses it, for `inline`.
    expect(inlineAlert.textContent).toBe(paneAlertText)
  })
})

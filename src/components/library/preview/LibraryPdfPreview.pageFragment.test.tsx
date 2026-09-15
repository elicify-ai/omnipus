// LibraryPdfPreview.pageFragment.test.tsx — ADR-083 embedded-content spec,
// Step 6 (EMB-105, US-12 AS-4): "Given a note embedding a PDF with a page
// number, When it is read, Then that page is shown" / "And the surrounding
// pages are not."
//
// This is a MINIMAL mock — a real, auto-resolving multi-page document, no
// Edit-mode/annotation/signature machinery — because a page fragment never
// reaches that surface (no PreviewHeaderSlotProvider outside the pane). The
// pool-sharing test at the bottom is the direct proof of this task's own
// brief: "A PDF page fragment must ride this pool, not spawn its own
// worker" — it is the EXACT `LibraryPdfPreview` component and the EXACT
// `pdfWorkerPool` singleton, so a fragment mount competing for the same two
// slots as an ordinary whole-document mount is a property of the shared
// code path, not of anything this test file adds. Every negative assertion
// (surrounding pages absent) sits beside the positive one (the target page
// present) — a renderer that draws NOTHING would otherwise pass the negative
// half trivially.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, waitFor, within } from '@testing-library/react'
import type { LibraryEntry } from '@/lib/api'

const h = vi.hoisted(() => ({
  numPages: 5,
  getPageCalls: [] as number[],
}))

function makePage(n: number) {
  return {
    pageNumber: n,
    getViewport: ({ scale }: { scale: number }) => ({ width: 600 * scale, height: 800 * scale, rotation: 0 }),
    render: () => ({ promise: Promise.resolve(), cancel: () => {} }),
    streamTextContent: () => 'text-content-stream',
    getAnnotations: () => Promise.resolve([]),
  }
}

vi.mock('pdfjs-dist', () => {
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
        numPages: h.numPages,
        getPage: (n: number) => {
          h.getPageCalls.push(n)
          return Promise.resolve(makePage(n))
        },
        annotationStorage: { onSetModified: null, onResetModified: null },
        getFieldObjects: () => Promise.resolve(null),
        saveDocument: () => Promise.resolve(new Uint8Array()),
      }),
      destroy: () => Promise.resolve(),
    }),
  }
})

function entryFor(path: string): LibraryEntry {
  return {
    name: path.split('/').pop() as string,
    path,
    is_dir: false,
    is_hidden: false,
    size: 4096,
    modified_at: '2026-08-22T10:15:00Z',
    is_text_editable: false,
  }
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

class FakeWorker extends EventTarget {
  terminate() {}
  postMessage() {}
}

beforeEach(async () => {
  vi.resetModules()
  h.numPages = 5
  h.getPageCalls = []

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
  vi.stubGlobal('Worker', FakeWorker)
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue({} as unknown as CanvasRenderingContext2D)

  const mod = await import('./LibraryPdfPreview')
  mod.__resetPdfAssetProbeForTests()
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('LibraryPdfPreview — page fragment (EMB-105, US-12 AS-4)', () => {
  it('renders ONLY the named page, not the surrounding ones', async () => {
    const mod = await import('./LibraryPdfPreview')
    const { container, unmount } = render(
      <mod.LibraryPdfPreview workspaceId="ws-1" entry={entryFor('reports/doc.pdf')} pageFragment={3} />,
    )

    await waitFor(() => expect(within(container).getByTestId('library-pdf-pages')).toBeInTheDocument())
    const pages = await waitFor(() => {
      const found = within(container).getAllByTestId('library-pdf-page')
      expect(found).toHaveLength(1)
      return found
    })

    // Positive: the target page is the one drawn.
    expect(pages[0]).toHaveAttribute('data-page-number', '3')
    // Negative, paired with the positive above: no other page number
    // appears anywhere in the container — a renderer that silently drew
    // page 1 instead (or every page) fails this half.
    for (const n of [1, 2, 4, 5]) {
      expect(
        container.querySelector(`[data-testid="library-pdf-page"][data-page-number="${n}"]`),
      ).not.toBeInTheDocument()
    }
    // Only the requested page was ever fetched from the document — proves
    // this is a real single-page render, not "draw everything, hide the rest".
    expect(h.getPageCalls).toEqual([3])

    expect(within(container).getByTestId('library-pdf-pages')).toHaveAttribute(
      'aria-label',
      'doc.pdf, page 3 of 5',
    )

    unmount()
  })

  it('renders every page when no fragment is given — the default path is unaffected (regression pairing)', async () => {
    const mod = await import('./LibraryPdfPreview')
    const { container, unmount } = render(
      <mod.LibraryPdfPreview workspaceId="ws-1" entry={entryFor('reports/doc.pdf')} />,
    )

    await waitFor(() => {
      expect(within(container).getAllByTestId('library-pdf-page')).toHaveLength(5)
    })
    expect(h.getPageCalls).toEqual([1, 2, 3, 4, 5])
    expect(within(container).getByTestId('library-pdf-pages')).toHaveAttribute(
      'aria-label',
      'doc.pdf, 5 pages',
    )

    unmount()
  })

  it('shows a visible, named error for a page fragment beyond the document — never a blank page', async () => {
    const mod = await import('./LibraryPdfPreview')
    const { container, unmount } = render(
      <mod.LibraryPdfPreview workspaceId="ws-1" entry={entryFor('reports/doc.pdf')} pageFragment={99} />,
    )

    const alert = await within(container).findByRole('alert')
    expect(alert.textContent).toMatch(/page 99/i)
    expect(alert.textContent).toMatch(/5-page/i)
    // Paired negative: nothing was drawn for an out-of-range fragment.
    expect(within(container).queryByTestId('library-pdf-page')).not.toBeInTheDocument()
    expect(h.getPageCalls).toEqual([])

    unmount()
  })

  it('shares the SAME page-wide worker pool as an ordinary whole-document mount (EMB-032) — a fragment does not spawn its own worker', async () => {
    const mod = await import('./LibraryPdfPreview')
    // Fill both pool slots with fragment mounts, deliberately never
    // released. Settled ONE AT A TIME (not concurrently) — this test's only
    // claim is about the WORKER POOL ceiling, so there is no reason to also
    // exercise two real PDF.js page-render loops racing against each other
    // in jsdom at once; LibraryPdfPreview.pool.test.tsx proves the identical
    // property for whole-document mounts by resolving its documents one at
    // a time for the same reason.
    const doc1 = render(
      <mod.LibraryPdfPreview workspaceId="ws-1" entry={entryFor('reports/a.pdf')} pageFragment={1} />,
    )
    await waitFor(() => expect(within(doc1.container).getAllByTestId('library-pdf-page')).toHaveLength(1))

    const doc2 = render(
      <mod.LibraryPdfPreview workspaceId="ws-1" entry={entryFor('reports/b.pdf')} pageFragment={2} />,
    )
    await waitFor(() => expect(within(doc2.container).getAllByTestId('library-pdf-page')).toHaveLength(1))

    // A THIRD fragment mount, at the ceiling: it must wait, exactly as an
    // ordinary whole-document third mount does in
    // LibraryPdfPreview.pool.test.tsx. If a page fragment spawned a worker
    // OUTSIDE the pool instead, this would render its page immediately with
    // no queued state at all.
    const doc3 = render(
      <mod.LibraryPdfPreview workspaceId="ws-1" entry={entryFor('reports/c.pdf')} pageFragment={1} />,
    )
    await waitFor(() => expect(within(doc3.container).getByTestId('library-pdf-queued')).toBeInTheDocument())
    expect(within(doc3.container).queryByTestId('library-pdf-page')).not.toBeInTheDocument()

    doc1.unmount()
    doc2.unmount()
    doc3.unmount()
  })
})

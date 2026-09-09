// LibraryPdfPreview.pool.test.tsx — the bounded PDF worker pool (ADR-083
// embedded-content spec, EMB-032): "At most two PDF worker instances MAY
// exist page-wide; a lease beyond that MUST show a visible waiting state; a
// worker error MUST reject only the leases held on that worker; and a failed
// worker MUST be terminated and removed so the next lease creates a fresh
// one."
//
// WHY THIS FILE EXISTS SEPARATELY, stated because the spec's own review
// calls it out by name: LibraryPdfPreview already constructs one real
// `Worker` per document instance (see that file's header), which ALONE
// already makes "two documents render concurrently", "a failure is isolated
// to one document" and "a third mount afterwards gets its own worker" true —
// with or without a pool. A test that only asserts those three things proves
// nothing about this work. The tests below are built around the ONE
// assertion that cannot pass without a real ceiling: the COUNT of
// `new Worker(...)` calls, sampled WHILE three documents are mounted at once
// and none has been released.
//
// This file's pdfjs-dist / Worker / fetch mocks are a MINIMAL subset of
// LibraryPdfPreview.test.tsx's own (no Edit-mode annotation layer, no
// signature machinery, no per-call customisation) — every document in this
// suite is the same trivial one-page PDF; what varies between them is only
// whether and when their worker is made to fail.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, waitFor, within } from '@testing-library/react'
import type { LibraryEntry } from '@/lib/api'

// `getDocument()` DELIBERATELY never resolves on its own — every call parks
// its resolver in `h.pendingDocs`, in call order, and a test resolves
// exactly the ones it needs. This is what makes the failure test below
// actually reproduce the race FR-019c's own header describes ("the worker
// then never replies and `task.promise` hangs on 'Opening…' forever"): with
// an auto-resolving mock, `Promise.race([task.promise, workerFailed])`
// resolves via `task.promise` in the same microtask it is created, and a
// LATER `dispatchEvent('error', ...)` lands on a race that already
// settled — proving nothing about the error path at all.
const h = vi.hoisted(() => ({
  pendingDocs: [] as Array<{ resolve: (doc: unknown) => void }>,
}))

function makePdfDoc() {
  const page = {
    getViewport: ({ scale }: { scale: number }) => ({ width: 600 * scale, height: 800 * scale, rotation: 0 }),
    render: () => ({ promise: Promise.resolve(), cancel: () => {} }),
    streamTextContent: () => ({}),
    getAnnotations: () => Promise.resolve([]),
  }
  return {
    numPages: 1,
    getPage: () => Promise.resolve(page),
    annotationStorage: {
      onSetModified: null,
      onResetModified: null,
      setValue: () => {},
      remove: () => {},
      resetModified: () => {},
    },
    getFieldObjects: () => Promise.resolve(null),
    saveDocument: () => Promise.resolve(new Uint8Array()),
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
    getDocument: () => {
      let resolveDoc: (doc: unknown) => void = () => {}
      const promise = new Promise((resolve) => {
        resolveDoc = resolve
      })
      h.pendingDocs.push({ resolve: resolveDoc })
      return { promise, destroy: () => Promise.resolve() }
    },
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

/** Every `new Worker(...)` constructed during a test, in construction order —
 *  the metric the spec's own review says is the only one that can actually
 *  detect a pool (as opposed to satisfy the tests trivially). */
class FakeWorker extends EventTarget {
  terminated = false
  constructor() {
    super()
    constructedWorkers.push(this)
  }
  terminate() {
    this.terminated = true
  }
  postMessage() {}
}
let constructedWorkers: FakeWorker[] = []

beforeEach(() => {
  vi.resetModules()
  constructedWorkers = []
  h.pendingDocs = []

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
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

/**
 * Renders one document and returns its queries SCOPED to its own container
 * via `within` — React Testing Library's `render()` result binds its
 * `getByTestId`/`findByRole`/etc. to `baseElement`, which DEFAULTS TO
 * `document.body`, not to that render's own container. With three documents
 * mounted at once (the whole point of this suite), an unscoped
 * `doc2.queryByTestId('library-pdf-error')` searches the WHOLE page and can
 * return doc1's element — which is exactly the false failure this caused
 * during development of this file (doc2 appeared to show doc1's dispatched
 * error). `within(container)` (used at every call site below, never
 * destructured) is what makes "doc1" and "doc2" mean what their names say.
 */
async function mountDoc(path: string) {
  const mod = await import('./LibraryPdfPreview')
  const { container, unmount } = render(<mod.LibraryPdfPreview workspaceId="ws-1" entry={entryFor(path)} />)
  return { container, unmount }
}

describe('LibraryPdfPreview — bounded worker pool (EMB-032, ceiling 2)', () => {
  it('third-lease-queues-visibly: a third PDF at the ceiling waits, and at most 2 workers are EVER constructed', async () => {
    const doc1 = await mountDoc('reports/a.pdf')
    const doc2 = await mountDoc('reports/b.pdf')

    // Both under-ceiling documents are admitted immediately — neither ever
    // shows the waiting state.
    await waitFor(() => expect(constructedWorkers).toHaveLength(2))
    expect(within(doc1.container).queryByTestId('library-pdf-queued')).not.toBeInTheDocument()
    expect(within(doc2.container).queryByTestId('library-pdf-queued')).not.toBeInTheDocument()

    const doc3 = await mountDoc('reports/c.pdf')

    // MUTATION THIS DIES ON: constructing a worker per instance with no
    // ceiling (today's shape, absent this pool) — the third document would
    // construct its OWN worker immediately, `constructedWorkers` would reach
    // 3, and `library-pdf-queued` would never render for it.
    await waitFor(() => expect(within(doc3.container).getByTestId('library-pdf-queued')).toBeInTheDocument())

    // The count assertion the spec's review calls the only one that can
    // actually fail: sampled while all three are mounted and nothing has
    // been released, it must never exceed the ceiling.
    expect(constructedWorkers).toHaveLength(2)
  })

  it('failure-attributed-to-one-document-only: a failed worker is terminated and evicted, freeing its slot for a fresh document', async () => {
    const doc1 = await mountDoc('reports/a.pdf')
    const doc2 = await mountDoc('reports/b.pdf')
    await waitFor(() => expect(constructedWorkers).toHaveLength(2))
    // Both `getDocument()` calls are now parked (see the mock's own header
    // for why they never auto-resolve) — doc1's is index 0, doc2's is index
    // 1, since doc1 was mounted strictly before doc2 and both share the same
    // sequential await chain up to this call.
    await waitFor(() => expect(h.pendingDocs).toHaveLength(2))

    const failedWorker = constructedWorkers[0]

    // Fail doc1's own worker via the async `error` event (the race
    // FR-019c's own header describes — the worker never replies), the same
    // shape LibraryPdfPreview's load effect actually listens for. Its own
    // `getDocument()` call (index 0) is STILL PENDING at this point, so this
    // is what settles `Promise.race([task.promise, workerFailed])` — not a
    // stale event landing on an already-resolved race.
    failedWorker.dispatchEvent(new ErrorEvent('error', { message: 'worker crashed' }))

    // doc1 shows its own error and NOTHING ELSE — this half alone is true
    // with or without a pool (one worker per instance already isolates
    // failure), which is why it is only half of this test.
    const alert = await within(doc1.container).findByRole('alert')
    expect(alert.textContent).toContain('worker')

    // doc2's OWN document now resolves normally — proving it renders for
    // real, not just that it failed to also error out.
    h.pendingDocs[1]?.resolve(makePdfDoc())
    await waitFor(() => expect(within(doc2.container).queryByTestId('library-pdf-loading')).not.toBeInTheDocument())
    expect(within(doc2.container).queryByTestId('library-pdf-error')).not.toBeInTheDocument()
    expect(within(doc2.container).queryByTestId('library-pdf-queued')).not.toBeInTheDocument()
    expect(within(doc2.container).getByTestId('library-pdf-pages')).toHaveAttribute(
      'aria-label',
      expect.stringContaining('1 page'),
    )

    // Poisoned-worker eviction: EMB-032 requires the failed worker be
    // TERMINATED, not merely abandoned — the property that has no meaning at
    // all in a no-pool world.
    expect(failedWorker.terminated).toBe(true)

    // A third document, mounted AFTER the failure, gets a genuinely FRESH
    // worker — not a queued wait (the failed lease's slot was freed the
    // moment the failure was known, not only on eventual unmount) and not
    // the terminated instance.
    const doc3 = await mountDoc('reports/c.pdf')
    await waitFor(() => expect(constructedWorkers).toHaveLength(3))
    expect(within(doc3.container).queryByTestId('library-pdf-queued')).not.toBeInTheDocument()
    const freshWorker = constructedWorkers[2]
    expect(freshWorker).not.toBe(failedWorker)
    expect(freshWorker.terminated).toBe(false)

    await waitFor(() => expect(h.pendingDocs).toHaveLength(3))
    h.pendingDocs[2]?.resolve(makePdfDoc())
    await waitFor(() => expect(within(doc3.container).queryByTestId('library-pdf-loading')).not.toBeInTheDocument())
    expect(within(doc3.container).queryByTestId('library-pdf-error')).not.toBeInTheDocument()
  })

  it('releases a queued lease when the component unmounts before its turn, without ever constructing that worker', async () => {
    const doc1 = await mountDoc('reports/a.pdf')
    await mountDoc('reports/b.pdf') // occupies the pool's second slot; never queried further
    await waitFor(() => expect(constructedWorkers).toHaveLength(2))

    const doc3 = await mountDoc('reports/c.pdf')
    await waitFor(() => expect(within(doc3.container).getByTestId('library-pdf-queued')).toBeInTheDocument())

    // Scrolled far out of view before its turn (EMB-065 applied to a still-
    // queued lease) — LazyEmbedMount would unmount it; this simulates that
    // directly at the component boundary this pool integrates with.
    doc3.unmount()

    // Releasing doc1 must now grant the NEXT queued waiter — but doc3 gave
    // up its place, so nothing new should be constructed on its behalf.
    doc1.unmount()
    await new Promise((resolve) => setTimeout(resolve, 0))

    // MUTATION THIS DIES ON: forgetting to remove an aborted request from
    // the queue — a leaked entry would construct a worker for a component
    // that no longer exists once doc1's slot frees.
    expect(constructedWorkers).toHaveLength(2)
  })
})

// LibraryPdfPreview.silentFailures.test.tsx — the failures this component
// used to have NO WAY of reporting.
//
// Every test in this file was written against a defect that produced a
// perfectly normal-looking pane: no error, no spinner, no console entry,
// nothing for a reader or a test to catch hold of. That shape is why they all
// live together rather than being scattered through the existing suites — the
// oracle in each case is "the failure became VISIBLE", and the mutation each
// one dies on is "go back to hiding it".
//
//   F2  a worker that dies AFTER the document opened was discarded entirely:
//       the `{ once: true }` error listener outlives the
//       `Promise.race([task.promise, workerFailed])`, so its `reject` landed
//       on a promise the race had already dropped. Status stayed 'ready',
//       container stayed empty, forever.
//   F3  `setStatus('ready')` fired BEFORE any page existed, so a wedged
//       worker (out of memory in the wasm decoders, or throttled) showed a
//       white box indistinguishable from a blank first page — and no page
//       render had a deadline, so it stayed that way.
//   F4  page width was measured off an element carrying the `hidden` class,
//       which CSSOM reports as `clientWidth === 0` — so the `|| 800` fallback
//       fired on EVERY first load and every PDF rendered at a hardcoded 800px
//       regardless of its box. jsdom reports 0 for every `clientWidth`, which
//       is why the existing suites pass with or WITHOUT that bug and why this
//       file stubs the property to model a real layout.
//   F1  (second half) `loadingTask.destroy()` never settles when the worker
//       has stopped replying — it ends by awaiting that worker's answer — so
//       a teardown that waits for it politely waits forever.
//   +   a failed load had no way back: no retry anywhere in the PDF path,
//       while both sibling preview surfaces offer one.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, waitFor, within, fireEvent } from '@testing-library/react'
import type { LibraryEntry } from '@/lib/api'

const h = vi.hoisted(() => ({
  /** `getDocument()` never resolves on its own — a test resolves the exact
   *  one it means to. Same reasoning as LibraryPdfPreview.pool.test.tsx's
   *  header: with an auto-resolving mock, an error dispatched later lands on
   *  a race that has already settled and proves nothing. Here that is not a
   *  hazard to avoid but the very thing under test, so both orderings have to
   *  be reachable deliberately. */
  pendingDocs: [] as Array<{ resolve: (doc: unknown) => void }>,
  /** 'never' models the wedge F3 exists for: a worker that stops answering
   *  without erroring, throwing, or closing. */
  getPageBehaviour: 'resolve' as 'resolve' | 'never',
  /** How many times `getPage()` has been called. Lets a test WAIT for the
   *  load to be demonstrably inside the page loop before it asserts on which
   *  stage the watchdog names — otherwise that assertion is a race against
   *  the timer and passes or fails on machine speed. */
  getPageCalls: 0,
  /** 'never' models what `WorkerTransport.destroy()` really does when the
   *  worker is dead — it sends "Terminate" and awaits a reply that never
   *  comes (verified against pdfjs-dist 6.2.108's build/pdf.mjs). */
  destroyBehaviour: 'resolve' as 'resolve' | 'never',
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
    getPage: () => {
      h.getPageCalls++
      return h.getPageBehaviour === 'never' ? new Promise(() => {}) : Promise.resolve(page)
    },
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
      return {
        promise,
        destroy: () => (h.destroyBehaviour === 'never' ? new Promise<void>(() => {}) : Promise.resolve()),
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
    headers: new Headers({ 'content-type': 'application/octet-stream', ETag: '"v1:initial"' }),
    arrayBuffer: () => Promise.resolve(new ArrayBuffer(8)),
  } as unknown as Response
}

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

/** How many more times the byte download should fail before succeeding. The
 *  transport-layer blip the retry affordance exists for: the same URL that
 *  rejects now answers 200 a moment later. */
let downloadFailuresRemaining = 0

/**
 * Models a REAL layout, which jsdom does not have: `clientWidth` is 0 for
 * every element in jsdom, so a component that measures the wrong element
 * behaves identically to one that measures the right element and both pass.
 * Here the component ROOT has a width and the pages container — which carries
 * the `hidden` class until a page is on screen — reports 0, exactly as CSSOM
 * reports a `display:none` element. A test that stubs both the same way could
 * not tell the fix from the bug.
 */
function stubLayout(rootWidth: number): void {
  Object.defineProperty(HTMLElement.prototype, 'clientWidth', {
    configurable: true,
    get(this: HTMLElement) {
      return this.dataset.testid === 'library-pdf-preview' ? rootWidth : 0
    },
  })
}

beforeEach(() => {
  vi.resetModules()
  constructedWorkers = []
  h.pendingDocs = []
  h.getPageBehaviour = 'resolve'
  h.getPageCalls = 0
  h.destroyBehaviour = 'resolve'
  downloadFailuresRemaining = 0

  vi.stubGlobal(
    'fetch',
    vi.fn((input: RequestInfo | URL) => {
      const url = String(input)
      if (url.startsWith('/pdfjs/')) {
        const rel = url.slice('/pdfjs/'.length)
        return Promise.resolve(rel === 'asset-manifest.json' ? jsonResponse(MANIFEST) : binaryResponse())
      }
      if (url.includes('/api/v1/library/')) {
        if (downloadFailuresRemaining > 0) {
          downloadFailuresRemaining--
          return Promise.reject(new TypeError('Failed to fetch'))
        }
        return Promise.resolve(binaryResponse())
      }
      return Promise.reject(new Error(`unexpected fetch: ${url}`))
    }),
  )

  vi.stubGlobal('Worker', FakeWorker)
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue({} as unknown as CanvasRenderingContext2D)
})

afterEach(async () => {
  const mod = await import('./LibraryPdfPreview')
  mod.__setPdfFirstPageTimeoutForTests(null)
  // `vi.restoreAllMocks()` knows nothing about a defineProperty — left in
  // place it would silently give every later test in this file a fake layout.
  delete (HTMLElement.prototype as unknown as Record<string, unknown>).clientWidth
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

async function mountDoc() {
  const mod = await import('./LibraryPdfPreview')
  const { container, unmount } = render(<mod.LibraryPdfPreview workspaceId="ws-1" entry={ENTRY} />)
  return { container, unmount, mod }
}

/** Mounts, lets the document open, and waits until its first page is on
 *  screen — the fully-healthy starting point every "…and THEN it broke" test
 *  below needs. */
async function mountAndOpen() {
  const mounted = await mountDoc()
  await waitFor(() => expect(h.pendingDocs).toHaveLength(1))
  h.pendingDocs[0]?.resolve(makePdfDoc())
  await waitFor(() => expect(within(mounted.container).getByTestId('library-pdf-page')).toBeInTheDocument())
  return mounted
}

describe('LibraryPdfPreview — a worker that dies AFTER the document opened (F2)', () => {
  it('reports the failure instead of leaving a ready pane that will never change again', async () => {
    const doc1 = await mountAndOpen()
    // Healthy: opened, rendered, no error anywhere.
    expect(within(doc1.container).queryByTestId('library-pdf-error')).not.toBeInTheDocument()

    // The worker dies now — long after `Promise.race([task.promise,
    // workerFailed])` settled via `task.promise`. The listener is still
    // attached (`{ once: true }` is fire-once, not load-only) and still
    // fires; the question is whether anything HEARS it.
    constructedWorkers[0]?.dispatchEvent(new ErrorEvent('error', { message: 'worker crashed' }))

    // MUTATION THIS DIES ON: reporting the failure only by `reject`-ing the
    // `workerFailed` promise, as the code used to. The race consumed that
    // promise long ago, so the rejection goes nowhere — not even to
    // `unhandledrejection` — and the pane stays 'ready' and unchanged
    // forever.
    const alert = await within(doc1.container).findByTestId('library-pdf-error')
    // Named honestly for WHICH failure this is: "was not opened" would be a
    // lie to someone looking at the document's pages.
    expect(alert.textContent).toMatch(/stopped after this document was opened/i)
    expect(alert.textContent).toMatch(/worker crashed/)
  })

  it('frees its pool slot when it dies late, so the next documents are not starved behind a dead worker', async () => {
    const doc1 = await mountAndOpen()
    expect(constructedWorkers).toHaveLength(1)

    constructedWorkers[0]?.dispatchEvent(new ErrorEvent('error', { message: 'worker crashed' }))
    await within(doc1.container).findByTestId('library-pdf-error')

    // EMB-032's ceiling is 2. With the dead document's lease released, two
    // fresh documents must BOTH be admitted immediately.
    const doc2 = await mountDoc()
    const doc3 = await mountDoc()
    // MUTATION THIS DIES ON: dropping `releaseLease()` from the late-failure
    // path — doc3 would sit in the queued state behind a worker that is
    // never going to answer.
    await waitFor(() => expect(constructedWorkers).toHaveLength(3))
    expect(within(doc2.container).queryByTestId('library-pdf-queued')).not.toBeInTheDocument()
    expect(within(doc3.container).queryByTestId('library-pdf-queued')).not.toBeInTheDocument()
  })
})

describe('LibraryPdfPreview — ready means a page is on screen (F3)', () => {
  it('keeps the pages container hidden and the spinner up while the first page has not arrived', async () => {
    h.getPageBehaviour = 'never'
    const doc1 = await mountDoc()
    await waitFor(() => expect(h.pendingDocs).toHaveLength(1))
    h.pendingDocs[0]?.resolve(makePdfDoc())

    // Give the chain every chance to reach the old `setStatus('ready')`,
    // which sat one statement before the first `getPage()`.
    await new Promise((resolve) => setTimeout(resolve, 50))

    // MUTATION THIS DIES ON: flipping to 'ready' as soon as the DOCUMENT
    // opens (the old shape) — the spinner would be gone and the empty
    // container visible, which is a white box a reader cannot tell from a
    // blank first page.
    expect(within(doc1.container).getByTestId('library-pdf-loading')).toBeInTheDocument()
    expect(within(doc1.container).getByTestId('library-pdf-pages').className).toContain('hidden')
    expect(within(doc1.container).queryByTestId('library-pdf-page')).not.toBeInTheDocument()
  })

  it('turns a first page that never arrives into a visible, named error rather than an endless wait', async () => {
    const mod = await import('./LibraryPdfPreview')
    // The real deadline is 45s; the test hook only shortens the wait, it does
    // not change which code path runs.
    expect(mod.PDF_FIRST_PAGE_TIMEOUT_MS).toBe(45_000)
    mod.__setPdfFirstPageTimeoutForTests(400)

    h.getPageBehaviour = 'never'
    const doc1 = await mountDoc()
    await waitFor(() => expect(h.pendingDocs).toHaveLength(1))
    h.pendingDocs[0]?.resolve(makePdfDoc())
    // Wait for the load to be demonstrably inside the page loop before
    // asserting on the stage the watchdog names — asserting without this is a
    // race against the timer, and it passed on a fast machine for the wrong
    // reason while it was being written.
    await waitFor(() => expect(h.getPageCalls).toBe(1))

    // MUTATION THIS DIES ON: removing the first-page watchdog — nothing ever
    // rejects, nothing ever resolves, and the spinner spins for the life of
    // the tab.
    let alert: HTMLElement | null = null
    await waitFor(
      () => {
        alert = within(doc1.container).getByTestId('library-pdf-error')
      },
      { timeout: 4000 },
    )
    // Names WHAT it was waiting for, so the message is actionable rather
    // than "something went wrong".
    expect(alert!.textContent).toMatch(/rendering page 1/i)
    expect(alert!.textContent).toMatch(/did not put a page on screen/i)
  })

  it('stops the watchdog once a page IS on screen, so a slow LATER page never fails a healthy document', async () => {
    const mod = await import('./LibraryPdfPreview')
    mod.__setPdfFirstPageTimeoutForTests(60)

    const doc1 = await mountAndOpen()
    await new Promise((resolve) => setTimeout(resolve, 150))

    // MUTATION THIS DIES ON: leaving the watchdog running after the first
    // page renders — every document that took a normal amount of time to
    // open would then error out 45 seconds later while the reader was
    // looking at it.
    expect(within(doc1.container).queryByTestId('library-pdf-error')).not.toBeInTheDocument()
    expect(within(doc1.container).getByTestId('library-pdf-page')).toBeInTheDocument()
  })
})

describe('LibraryPdfPreview — page width comes from the real box (F4)', () => {
  it('renders at the width of the component root, not at the hardcoded fallback', async () => {
    stubLayout(1000)
    const doc1 = await mountAndOpen()

    // 1000px box − 32px of container padding = 968 CSS px of page, against
    // the mock's 600pt unscaled page. The number is derived from the layout,
    // not read off the implementation: fit = (1000 − 32) / 600 = 1.6133…,
    // within [MIN_SCALE, MAX_SCALE], so viewport.width = 600 × 1.6133… = 968.
    const canvas = within(doc1.container).getByTestId('library-pdf-page').querySelector('canvas')
    // MUTATION THIS DIES ON: measuring `container.clientWidth` (the element
    // carrying the `hidden` class) — it reports 0 here exactly as CSSOM
    // reports it in a browser, so the fallback would fire and this would be
    // 768px.
    expect(canvas?.style.width).toBe('968px')

    // And the guess-marker must be ABSENT when the measurement was real.
    expect(within(doc1.container).getByTestId('library-pdf-pages')).not.toHaveAttribute('data-width-source')
  })

  it('marks the fallback width VISIBLY when the box genuinely measures zero', async () => {
    stubLayout(0)
    const doc1 = await mountAndOpen()

    const canvas = within(doc1.container).getByTestId('library-pdf-page').querySelector('canvas')
    expect(canvas?.style.width).toBe('768px') // (800 − 32) / 600 × 600

    // MUTATION THIS DIES ON: falling back silently (`|| 800`). A guess that
    // renders plausibly and says nothing is the whole reason this defect
    // survived — the marker is what makes "we guessed" detectable at all.
    expect(within(doc1.container).getByTestId('library-pdf-pages')).toHaveAttribute('data-width-source', 'fallback')
  })
})

describe('LibraryPdfPreview — the worker thread is ended even when PDF.js will not end it (F1)', () => {
  it('terminates the thread after the grace period when destroy() never settles', async () => {
    // What `WorkerTransport.destroy()` really does to a dead worker: sends
    // "Terminate", awaits a reply, and waits for ever. A teardown that only
    // awaits that promise leaks the thread permanently.
    h.destroyBehaviour = 'never'
    const doc1 = await mountAndOpen()
    const worker = constructedWorkers[0]
    expect(worker?.terminated).toBe(false)

    doc1.unmount()

    // MUTATION THIS DIES ON: dropping the grace timeout and terminating only
    // in `.finally()` — that callback never runs here, so `terminated` stays
    // false for ever.
    await waitFor(() => expect(worker?.terminated).toBe(true), { timeout: 6000 })
  }, 12_000)
})

describe('LibraryPdfPreview — a failed load can be retried', () => {
  it('offers Try again on the error pane, and a retry that succeeds renders the document', async () => {
    // The transport-layer blip this exists for: the byte download rejects
    // once, and the identical request succeeds immediately afterwards.
    downloadFailuresRemaining = 1
    const doc1 = await mountDoc()

    const alert = await within(doc1.container).findByTestId('library-pdf-error')
    expect(alert.textContent).toMatch(/network unavailable/i)

    // MUTATION THIS DIES ON: no retry affordance at all — which is what this
    // pane had. It named its cause and stopped; the only way back was to
    // reselect the file.
    const retry = within(doc1.container).getByTestId('library-pdf-retry')
    fireEvent.click(retry)

    await waitFor(() => expect(h.pendingDocs).toHaveLength(1))
    h.pendingDocs[0]?.resolve(makePdfDoc())
    await waitFor(() => expect(within(doc1.container).getByTestId('library-pdf-page')).toBeInTheDocument())
    expect(within(doc1.container).queryByTestId('library-pdf-error')).not.toBeInTheDocument()
  })
})

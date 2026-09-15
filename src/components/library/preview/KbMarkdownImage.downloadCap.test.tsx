// KbMarkdownImage.downloadCap.test.tsx — UAT D-101 (row U-48, re-test
// FAIL): "a knowledge note with 40 embedded images has at most 4 image
// downloads in flight at once, all images eventually mount."
//
// WHY THIS FILE EXISTS SEPARATELY FROM KbMarkdownImage.test.tsx (mirroring
// BasePreview.pool.test.tsx / LibraryPdfPreview.pool.test.tsx's own
// header): LazyEmbedMount's viewport gate alone does NOT bound this —
// EVERY instance inside the (generous, 600px-margin) mount window mounts
// at once, by design (LazyEmbedMount has no cap of its own). A test that
// only asserts "40 KbMarkdownImage instances render" proves nothing about
// this — every one of them already renders fine with no ceiling at all.
// The assertion that actually requires a real ceiling is the COUNT of real
// `<img>` elements (i.e. picture downloads actually STARTED) while several
// are near-viewport and none has settled.
//
// jsdom never fires `load`/`error` on its own (KbMarkdownImage.test.tsx's
// own header notes this) — that is what lets this suite drive 40
// independent pictures through load/error deterministically, with no
// network mocking, and prove the in-flight count never exceeds the
// ceiling at any instant, exactly the "count, never a clock" method the
// embedded-content spec's own Dataset I uses (adr-083-embedded-content-spec.md).

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'

import { KbMarkdownImage } from './KbMarkdownImage'
import { KB_IMAGE_DOWNLOAD_POOL_CEILING } from './kbImageDownloadPool'

interface ObserverInstance {
  callback: IntersectionObserverCallback
  observedElements: Element[]
}

let observerInstances: ObserverInstance[] = []

// Same fake IntersectionObserver shape KbMarkdownImage.test.tsx and
// LazyEmbedMount.test.tsx already use — construction order is
// deterministic: each LazyEmbedMount instance builds [mount, unmount] in
// that order, so instance `i` (0-based) owns observers `2*i` (mount) and
// `2*i + 1` (unmount).
class FakeIntersectionObserver implements IntersectionObserver {
  readonly root: Element | Document | null = null
  readonly rootMargin = ''
  readonly scrollMargin = ''
  readonly thresholds: ReadonlyArray<number> = []
  private instance: ObserverInstance
  constructor(callback: IntersectionObserverCallback) {
    this.instance = { callback, observedElements: [] }
    observerInstances.push(this.instance)
  }
  observe(el: Element) {
    this.instance.observedElements.push(el)
  }
  unobserve(el: Element) {
    this.instance.observedElements = this.instance.observedElements.filter((e) => e !== el)
  }
  disconnect() {}
  takeRecords(): IntersectionObserverEntry[] {
    return []
  }
}

function fireIntersection(observerIndex: number, isIntersecting: boolean) {
  const inst = observerInstances[observerIndex]
  if (!inst) throw new Error(`no observer constructed at index ${observerIndex}`)
  act(() => {
    inst.callback(
      inst.observedElements.map((target) => ({ isIntersecting, target }) as IntersectionObserverEntry),
      inst as unknown as IntersectionObserver,
    )
  })
}

beforeEach(() => {
  observerInstances = []
  vi.stubGlobal('IntersectionObserver', FakeIntersectionObserver)
})
afterEach(() => {
  vi.unstubAllGlobals()
})

const IMAGE_COUNT = 40

function renderGallery() {
  return render(
    <div>
      {Array.from({ length: IMAGE_COUNT }, (_, i) => (
        <KbMarkdownImage key={i} src={`https://example.test/gallery/${i}.png`} alt={`gallery ${i}`} />
      ))}
    </div>,
  )
}

/** Simulates a "realistic scroll gap" worst case: EVERY embed on the page
 *  is near-viewport at once (the mount gate has already let all 40
 *  through) — the exact scenario UAT's second, 300×200px fixture measured
 *  ("request #40, the last, furthest off-screen image, already dispatched
 *  while earlier requests were still resolving"). If the ceiling holds
 *  even in this worst case, it holds under any real, partial-visibility
 *  scroll too. */
async function mountAllNearViewport() {
  await act(async () => {
    for (let i = 0; i < IMAGE_COUNT; i++) {
      const mountObserverIndex = i * 2
      const inst = observerInstances[mountObserverIndex]
      inst?.callback(
        inst.observedElements.map((target) => ({ isIntersecting: true, target }) as IntersectionObserverEntry),
        inst as unknown as IntersectionObserver,
      )
    }
    await Promise.resolve()
    await Promise.resolve()
  })
}

function srcIndexOf(img: Element): number {
  const src = img.getAttribute('src') ?? ''
  const match = /\/gallery\/(\d+)\.png$/.exec(src)
  if (!match) throw new Error(`unexpected image src: ${src}`)
  return Number(match[1])
}

describe('KbMarkdownImage — UAT D-101: page-wide image download cap (row U-48)', () => {
  it('at most the ceiling have a real src assigned before any load completes, even when all 40 are near the viewport at once', async () => {
    renderGallery()
    await mountAllNearViewport()

    // DIES ON the old code: KbMarkdownImage rendered an `<img src>` the
    // instant LazyEmbedMount mounted it, with no further gate — all 40
    // near-viewport pictures would have a real `<img>` (and therefore a
    // dispatched request) at once.
    const inFlightImages = screen.getAllByTestId('kb-markdown-image')
    expect(inFlightImages).toHaveLength(KB_IMAGE_DOWNLOAD_POOL_CEILING)

    const queued = screen.getAllByTestId('kb-markdown-image-queued')
    expect(queued).toHaveLength(IMAGE_COUNT - KB_IMAGE_DOWNLOAD_POOL_CEILING)
  })

  it('completing a load admits the next queued image, and a FAILED load also releases its slot', async () => {
    renderGallery()
    await mountAllNearViewport()

    const settledLoaded = new Set<number>()
    const settledFailed = new Set<number>()

    const firstBatch = screen.getAllByTestId('kb-markdown-image')
    expect(firstBatch).toHaveLength(KB_IMAGE_DOWNLOAD_POOL_CEILING)

    // Succeed the first one.
    await act(async () => {
      fireEvent.load(firstBatch[0])
      await Promise.resolve()
    })
    settledLoaded.add(srcIndexOf(firstBatch[0]))

    let rendered = screen.getAllByTestId('kb-markdown-image')
    // One new slot was admitted — total real `<img>` elements grows by one
    // (the loaded one stays rendered; the released slot lets a queued
    // picture start).
    expect(rendered).toHaveLength(KB_IMAGE_DOWNLOAD_POOL_CEILING + 1)
    let inFlight = rendered.filter((img) => !settledLoaded.has(srcIndexOf(img)))
    // MUTATION THIS DIES ON: releasing on mount instead of on load/error —
    // the in-flight count would never be bounded by the ceiling at all.
    expect(inFlight.length).toBeLessThanOrEqual(KB_IMAGE_DOWNLOAD_POOL_CEILING)

    // Fail a STILL-PENDING one (never loaded) — its slot must release too,
    // not wedge the queue.
    const stillPending = inFlight[0]
    await act(async () => {
      fireEvent.error(stillPending)
      await Promise.resolve()
    })
    settledFailed.add(srcIndexOf(stillPending))

    rendered = screen.getAllByTestId('kb-markdown-image')
    const unavailable = screen.getAllByTestId('kb-markdown-image-unavailable')
    // The failed picture is no longer a `kb-markdown-image` (it renders the
    // unavailable notice instead) but a queued one WAS admitted in its
    // place — total real `<img>` count is unchanged by the failure alone.
    expect(unavailable).toHaveLength(1)
    inFlight = rendered.filter((img) => !settledLoaded.has(srcIndexOf(img)))
    expect(inFlight.length).toBeLessThanOrEqual(KB_IMAGE_DOWNLOAD_POOL_CEILING)
  })

  it('all 40 eventually settle (loaded or failed) and the page never wedges', async () => {
    renderGallery()
    await mountAllNearViewport()

    // A successfully-loaded picture keeps rendering as `kb-markdown-image`
    // (only a FAILED one switches to the unavailable notice) — so "pending"
    // for the purposes of this drain loop means "rendered AND not already
    // one of the indices this loop itself settled", tracked explicitly
    // rather than re-deriving it from the DOM alone.
    const settledLoaded = new Set<number>()
    let loadedCount = 0
    let failedCount = 0
    let iterations = 0
    // Alternates success/failure to exercise both release paths while
    // draining the whole gallery. Bounded well beyond 40 so an actual
    // wedge (queue stuck, nothing ever admitted) fails the test instead of
    // hanging it.
    while (loadedCount + failedCount < IMAGE_COUNT && iterations < IMAGE_COUNT * 2) {
      iterations++
      const pending = screen
        .queryAllByTestId('kb-markdown-image')
        .filter((img) => !settledLoaded.has(srcIndexOf(img)))
      if (pending.length === 0) break
      const target = pending[0]
      const targetIndex = srcIndexOf(target)
      const shouldFail = targetIndex % 5 === 0
      await act(async () => {
        if (shouldFail) fireEvent.error(target)
        else fireEvent.load(target)
        await Promise.resolve()
      })
      if (shouldFail) {
        failedCount++
      } else {
        loadedCount++
        settledLoaded.add(targetIndex)
      }

      // Never more than the ceiling's worth of genuinely pending (not yet
      // settled) images at any sampled instant.
      const stillPending = screen
        .queryAllByTestId('kb-markdown-image')
        .filter((img) => !settledLoaded.has(srcIndexOf(img))).length
      expect(stillPending).toBeLessThanOrEqual(KB_IMAGE_DOWNLOAD_POOL_CEILING)
    }

    expect(loadedCount + failedCount).toBe(IMAGE_COUNT)
    expect(screen.queryAllByTestId('kb-markdown-image-queued')).toHaveLength(0)
    expect(screen.queryAllByTestId('kb-markdown-image-unavailable')).toHaveLength(failedCount)
  })

  it('a picture scrolled away before its turn gives up its QUEUED slot without ever downloading', async () => {
    const { container } = renderGallery()
    await mountAllNearViewport()

    const queuedBefore = screen.getAllByTestId('kb-markdown-image-queued')
    expect(queuedBefore.length).toBe(IMAGE_COUNT - KB_IMAGE_DOWNLOAD_POOL_CEILING)

    // The very last instance (index 39) is queued. Unmount it directly via
    // its own unmount observer — the same boundary LazyEmbedMount crosses
    // when a reader scrolls far enough past it (EMB-065's "stops when well
    // outside", applied to a still-queued lease).
    const lastUnmountObserverIndex = (IMAGE_COUNT - 1) * 2 + 1
    fireIntersection(lastUnmountObserverIndex, false)

    // Freeing every active slot must never grant a lease to the picture
    // that gave up its place — it stays gone (no img, no queued notice, no
    // unavailable notice — it simply never re-entered the DOM).
    const activeImages = screen.getAllByTestId('kb-markdown-image')
    for (const img of activeImages) {
      await act(async () => {
        fireEvent.load(img)
        await Promise.resolve()
      })
    }

    expect(container.querySelector('img[src="https://example.test/gallery/39.png"]')).toBeNull()
  })
})

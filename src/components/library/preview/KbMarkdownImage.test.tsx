// KbMarkdownImage.test.tsx — the knowledge composition's own image slot
// (UAT D-40 / D-134 / D-101, fan-out round).
//
// ORACLE. The three defects, as measured in the UAT campaign:
//   D-40  `![[img|400]]` inline rendered at container width — the hint was
//         dropped (T4 measured it ignored across eleven founder notes).
//   D-134 a viewBox-only SVG loaded (natural 168×150) and laid out at 0×0.
//   D-101 40 images issued 40 concurrent downloads with 6 in view.
//
// jsdom has no IntersectionObserver, so this suite fakes one (the same shape
// LazyEmbedMount.test.tsx documents) — that is what lets the D-101 test
// prove the mount gate rather than lean on LazyEmbedMount's fail-open path.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'

import { KbMarkdownImage } from './KbMarkdownImage'

interface ObserverInstance {
  callback: IntersectionObserverCallback
  observedElements: Element[]
}

let observerInstances: ObserverInstance[] = []

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

/** Fires the Nth constructed observer's callback (0 = this instance's mount
 *  observer, 1 = its unmount observer — construction order is deterministic,
 *  see LazyEmbedMount.test.tsx). */
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

describe('KbMarkdownImage — UAT D-101: the mount budget applies to plain pictures', () => {
  it('renders NO img until the picture is near the viewport — the download waits for the mount', () => {
    render(<KbMarkdownImage src="https://example.test/a.png" alt="a" />)
    const mount = screen.getByTestId('lazy-embed-mount')
    expect(mount).toHaveAttribute('data-mounted', 'false')
    // DIES ON the old code: ChatImage leaned on the browser's native
    // loading="lazy" — an <img> was in the DOM immediately, and UAT D-101
    // measured 40 concurrent downloads with 6 pictures in view.
    expect(mount.querySelector('img')).toBeNull()
    fireIntersection(0, true)
    expect(mount).toHaveAttribute('data-mounted', 'true')
    expect(screen.getByTestId('kb-markdown-image')).toBeInTheDocument()
  })

  it('an inline picture stays INLINE — the wrapper is inline-block, so a picture inside a sentence keeps its place', () => {
    render(<KbMarkdownImage src="https://example.test/a.png" alt="a" />)
    expect(screen.getByTestId('lazy-embed-mount').className).toContain('inline-block')
  })
})

describe('KbMarkdownImage — UAT D-40: the |N width hint is honoured', () => {
  it('applies the hint carried by the AST (data-kb-embed-width) as the rendered width', () => {
    render(<KbMarkdownImage src="https://example.test/a.png" alt="a" data-kb-embed-width="400" />)
    fireIntersection(0, true)
    const img = screen.getByTestId('kb-markdown-image')
    // DIES ON the old code: chat's MarkdownImage/ChatImage read only
    // src/alt — the hint data reached the img slot and was dropped.
    expect(img.style.width).toBe('400px')
    expect(img.style.maxWidth).toBe('100%')
  })

  it('no hint, no invented width — the picture renders at its own size, capped by the container', () => {
    render(<KbMarkdownImage src="https://example.test/a.png" alt="a" />)
    fireIntersection(0, true)
    const img = screen.getByTestId('kb-markdown-image')
    expect(img.style.width).toBe('')
  })
})

describe('KbMarkdownImage — UAT D-134: an intrinsically-sizeless picture gets a bounded box', () => {
  it('a load reporting naturalWidth 0 switches to an explicit bounded box instead of laying out at 0×0', () => {
    render(<KbMarkdownImage src="https://example.test/mark.svg" alt="mark" data-kb-embed-width="180" />)
    fireIntersection(0, true)
    const img = screen.getByTestId('kb-markdown-image')
    expect(img.style.width).toBe('180px')
    expect(img.style.height).toBe('')
    // The viewBox-only SVG of the defect: loads fine, reports no natural
    // size, laid out at nothing. Simulate exactly that load.
    Object.defineProperty(img, 'naturalWidth', { value: 0 })
    Object.defineProperty(img, 'naturalHeight', { value: 0 })
    fireEvent.load(img)
    // DIES ON the old code: nothing ever gave the picture a box that did
    // not depend on intrinsic metrics — it stayed invisible.
    expect(img.style.width).toBe('180px')
    expect(img.style.height).toBe('240px')
    expect(img.style.objectFit).toBe('contain')
  })

  it('no hint: the bounded box defaults to its own size, not 0', () => {
    render(<KbMarkdownImage src="https://example.test/mark.svg" alt="mark" />)
    fireIntersection(0, true)
    const img = screen.getByTestId('kb-markdown-image')
    Object.defineProperty(img, 'naturalWidth', { value: 0 })
    Object.defineProperty(img, 'naturalHeight', { value: 0 })
    fireEvent.load(img)
    expect(img.style.width).toBe('320px')
    expect(img.style.height).toBe('240px')
  })

  it('a picture WITH natural size never gets the fallback box (control)', () => {
    render(<KbMarkdownImage src="https://example.test/a.png" alt="a" />)
    fireIntersection(0, true)
    const img = screen.getByTestId('kb-markdown-image')
    Object.defineProperty(img, 'naturalWidth', { value: 200 })
    Object.defineProperty(img, 'naturalHeight', { value: 100 })
    fireEvent.load(img)
    expect(img.style.height).toBe('')
    expect(img.style.objectFit).toBe('')
  })
})

describe('KbMarkdownImage — honest failure and unsafe sources', () => {
  it('a failed load is a stated unavailability, never the browser\'s broken-image glyph', () => {
    render(<KbMarkdownImage src="https://example.test/gone.png" alt="gone.png" />)
    fireIntersection(0, true)
    fireEvent.error(screen.getByTestId('kb-markdown-image'))
    expect(screen.getByTestId('kb-markdown-image-unavailable')).toHaveTextContent('gone.png')
  })

  it('an unsafe scheme never becomes an img at all', () => {
    const { container } = render(<KbMarkdownImage src="javascript:alert(1)" alt="x" />)
    expect(container.querySelector('img')).toBeNull()
  })
})

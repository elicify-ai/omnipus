// LazyEmbedMount.notice.test.tsx — the unmounted-embed notice (M2 / EMB-071,
// ADR-083 spec ~line 513/1990, ADR ~line 671): "While any embed on the page
// is unmounted, the reader shows one line stating that find-in-page and
// printing will not reach modules that have not been scrolled to. The
// notice disappears once every embed on the page is mounted, and never
// appears on a note with no embeds."
//
// Same fake-IntersectionObserver shape LazyEmbedMount.test.tsx already uses
// (jsdom has no real implementation) — duplicated locally rather than
// imported, since that file's fake is not exported and this suite's concern
// (the page-wide registry `UnmountedEmbedsNotice` reads) is orthogonal to
// that file's own margin/hysteresis assertions.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, act } from '@testing-library/react'
import { LazyEmbedMount, UnmountedEmbedsNotice, UNMOUNTED_EMBEDS_NOTICE_TEXT } from './LazyEmbedMount'

interface ObserverInstance {
  callback: IntersectionObserverCallback
  observedElements: Element[]
  disconnected: boolean
}

let observerInstances: ObserverInstance[] = []

class FakeIntersectionObserver implements IntersectionObserver {
  readonly root = null
  readonly rootMargin = ''
  readonly scrollMargin = ''
  readonly thresholds: ReadonlyArray<number> = []
  private instance: ObserverInstance

  constructor(callback: IntersectionObserverCallback) {
    this.instance = { callback, observedElements: [], disconnected: false }
    observerInstances.push(this.instance)
  }
  observe(el: Element) {
    this.instance.observedElements.push(el)
  }
  unobserve(el: Element) {
    this.instance.observedElements = this.instance.observedElements.filter((e) => e !== el)
  }
  disconnect() {
    this.instance.disconnected = true
  }
  takeRecords(): IntersectionObserverEntry[] {
    return []
  }
}

/** Fires a synthetic intersection callback on the Nth observer CONSTRUCTED
 *  (0-indexed, across the whole test — every LazyEmbedMount instance
 *  constructs its "mount" observer immediately before its "unmount"
 *  observer, matching LazyEmbedMount.test.tsx's own indexing convention). */
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

function Child({ label }: { label: string }) {
  return <div data-testid={`embed-${label}`}>{label}</div>
}

describe('UnmountedEmbedsNotice — never appears on a note with no embeds (EMB-071)', () => {
  it('renders nothing when no LazyEmbedMount instance exists at all', () => {
    render(<UnmountedEmbedsNotice />)
    expect(screen.queryByTestId('unmounted-embeds-notice')).not.toBeInTheDocument()
  })
})

describe('UnmountedEmbedsNotice — appears while at least one embed is unmounted, disappears once all are mounted (M2 / EMB-071)', () => {
  it('shows the notice while one of two embeds is unmounted, hides it once both mount, and shows it again once one scrolls back out', () => {
    render(
      <>
        <UnmountedEmbedsNotice />
        <LazyEmbedMount reservedHeight={40}>
          <Child label="a" />
        </LazyEmbedMount>
        <LazyEmbedMount reservedHeight={40}>
          <Child label="b" />
        </LazyEmbedMount>
      </>,
    )

    // Both embeds start unmounted (neither has ever intersected) — DIRECTION
    // ONE: the notice is present, and names both find-in-page and printing.
    expect(screen.getByTestId('unmounted-embeds-notice')).toBeInTheDocument()
    expect(screen.getByTestId('unmounted-embeds-notice').textContent).toContain(
      UNMOUNTED_EMBEDS_NOTICE_TEXT,
    )
    expect(UNMOUNTED_EMBEDS_NOTICE_TEXT).toMatch(/find-in-page/i)
    expect(UNMOUNTED_EMBEDS_NOTICE_TEXT).toMatch(/print/i)

    // Mount the first embed (its "mount" observer is index 0).
    fireIntersection(0, true)
    // Second embed still unmounted — the notice must still be present.
    expect(screen.getByTestId('unmounted-embeds-notice')).toBeInTheDocument()

    // Mount the second embed too (its "mount" observer is index 2).
    fireIntersection(2, true)
    // DIRECTION TWO: both embeds are now mounted — the notice must be gone.
    // MUTATION THIS DIES ON: a registry/notice that never re-checks its
    // entries, or a snapshot function that ignores updated values — with
    // both embeds mounted, this would still (wrongly) show the notice.
    expect(screen.queryByTestId('unmounted-embeds-notice')).not.toBeInTheDocument()

    // Scroll the first back out of view (its "unmount" observer is index 1)
    // — a real, later transition, not the initial state. The notice must
    // reappear, proving this is driven by CURRENT state, not a one-shot
    // "any embed was EVER unmounted" flag.
    fireIntersection(1, false)
    expect(screen.getByTestId('unmounted-embeds-notice')).toBeInTheDocument()
  })

  it('removes an instance from the registry on unmount from the tree, so a note whose only embed leaves shows no notice', () => {
    const { unmount } = render(
      <LazyEmbedMount reservedHeight={40}>
        <Child label="a" />
      </LazyEmbedMount>,
    )
    render(<UnmountedEmbedsNotice />)
    // The one embed has never intersected yet — unmounted, notice present.
    expect(screen.getByTestId('unmounted-embeds-notice')).toBeInTheDocument()

    // Removed from the tree entirely (e.g. navigating to a different note),
    // not merely scrolled away — its registry entry must go with it.
    unmount()
    // MUTATION THIS DIES ON: cleaning up the registry entry on every
    // `mounted` transition instead of only on true unmount (or never
    // cleaning up at all) — either way a leaked entry would keep the notice
    // showing for a note with zero embeds left in the tree.
    expect(screen.queryByTestId('unmounted-embeds-notice')).not.toBeInTheDocument()
  })

  it('shows nothing once every embed on a multi-embed note is mounted, across three instances', () => {
    render(
      <>
        <UnmountedEmbedsNotice />
        <LazyEmbedMount reservedHeight={40}>
          <Child label="a" />
        </LazyEmbedMount>
        <LazyEmbedMount reservedHeight={40}>
          <Child label="b" />
        </LazyEmbedMount>
        <LazyEmbedMount reservedHeight={40}>
          <Child label="c" />
        </LazyEmbedMount>
      </>,
    )
    expect(screen.getByTestId('unmounted-embeds-notice')).toBeInTheDocument()
    fireIntersection(0, true) // a's mount observer
    fireIntersection(2, true) // b's mount observer
    // c (mount observer index 4) still unmounted — notice still present.
    expect(screen.getByTestId('unmounted-embeds-notice')).toBeInTheDocument()
    fireIntersection(4, true) // c's mount observer
    expect(screen.queryByTestId('unmounted-embeds-notice')).not.toBeInTheDocument()
  })
})

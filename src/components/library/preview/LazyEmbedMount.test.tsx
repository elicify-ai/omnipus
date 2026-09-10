// LazyEmbedMount.test.tsx — the mount-budget primitive (ADR-083
// embedded-content spec, EMB-065/066, US-8): "An embed MUST begin work when
// it enters the viewport plus a margin and MUST stop when it is well
// outside; there MUST be no limit on how many embeds a note may contain."
//
// jsdom has no IntersectionObserver implementation, so this suite fakes one
// that records every constructed instance (in construction order — the
// component always creates its "mount" observer before its "unmount"
// observer, per instance) and lets the test fire a synthetic intersection
// callback directly, rather than trying to simulate real scroll geometry.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { useState } from 'react'
import { render, screen, act } from '@testing-library/react'
import { LazyEmbedMount, DEFAULT_MOUNT_MARGIN_PX, DEFAULT_UNMOUNT_MARGIN_PX } from './LazyEmbedMount'

interface ObserverInstance {
  callback: IntersectionObserverCallback
  observedElements: Element[]
  disconnected: boolean
  /** The `rootMargin` this instance was actually constructed with — this is
   *  the mount budget itself (EMB-065/066's two named constants), and until
   *  this fake captured it, NOTHING in this suite could tell a component
   *  that reads `mountMarginPx`/`unmountMarginPx` apart from one that
   *  ignores them entirely and hardcodes '' (the prior shape of this fake,
   *  which the earlier constructor signature below made impossible to catch
   *  — it never even received the `options` argument). */
  rootMargin: string
}

let observerInstances: ObserverInstance[] = []

class FakeIntersectionObserver implements IntersectionObserver {
  readonly root = null
  readonly rootMargin: string
  readonly scrollMargin = ''
  readonly thresholds: ReadonlyArray<number> = []
  private instance: ObserverInstance

  constructor(callback: IntersectionObserverCallback, options?: IntersectionObserverInit) {
    this.rootMargin = options?.rootMargin ?? ''
    this.instance = { callback, observedElements: [], disconnected: false, rootMargin: this.rootMargin }
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
 *  observer, so for one instance index 0 is "mount", index 1 is "unmount").
 *  Wrapped in `act` because the real callback is invoked outside any React
 *  event handler — exactly like a real IntersectionObserver's — so the
 *  resulting `setState` needs an explicit flush to land before the test's
 *  next assertion runs. */
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

function Child() {
  return <div data-testid="embed-child">real content</div>
}

describe('LazyEmbedMount — reserved height before mounting (EMB-066)', () => {
  it('reserves the given height and renders no children before the near observer ever reports intersecting', () => {
    render(
      <LazyEmbedMount reservedHeight={240}>
        <Child />
      </LazyEmbedMount>,
    )
    const wrapper = screen.getByTestId('lazy-embed-mount')
    expect(wrapper.style.minHeight).toBe('240px')
    expect(screen.queryByTestId('embed-child')).not.toBeInTheDocument()
    expect(wrapper).toHaveAttribute('data-mounted', 'false')
  })
})

describe('LazyEmbedMount — the mount budget itself: margins actually reach the browser IntersectionObserver', () => {
  it('uses DEFAULT_MOUNT_MARGIN_PX for the near observer and DEFAULT_UNMOUNT_MARGIN_PX for the far one, when no override is given', () => {
    render(
      <LazyEmbedMount reservedHeight={240}>
        <Child />
      </LazyEmbedMount>,
    )
    // Hardcoded expected values, deliberately NOT read off the constants
    // under test — an oracle built from `` `${DEFAULT_MOUNT_MARGIN_PX}px` ``
    // would pass unchanged no matter what that constant is set to, which is
    // exactly the blocker this test exists to close (the bug report:
    // "Change DEFAULT_MOUNT_MARGIN_PX from 600 to 100000 ... every embed
    // mounts on first paint, the entire bound gone — and all 8 tests ...
    // STILL PASS"). These two literals ARE the mount budget's shipped
    // values today; a change to either constant must change these too.
    expect(DEFAULT_MOUNT_MARGIN_PX).toBe(600)
    expect(DEFAULT_UNMOUNT_MARGIN_PX).toBe(1800)
    expect(observerInstances[0]?.rootMargin).toBe('600px')
    expect(observerInstances[1]?.rootMargin).toBe('1800px')
  })

  it('passes custom mountMarginPx/unmountMarginPx straight through to the observers, not the defaults', () => {
    render(
      <LazyEmbedMount reservedHeight={240} mountMarginPx={50} unmountMarginPx={9000}>
        <Child />
      </LazyEmbedMount>,
    )
    // MUTATION THIS DIES ON: a component that reads its props but always
    // constructs its observers with the DEFAULT_* constants — the previous
    // shape of this suite could not tell that apart from correct wiring,
    // because nothing asserted rootMargin at all.
    expect(observerInstances[0]?.rootMargin).toBe('50px')
    expect(observerInstances[1]?.rootMargin).toBe('9000px')
  })
})

describe('LazyEmbedMount — mounts on entering the viewport plus a margin (EMB-065)', () => {
  it('mounts its children once the near (mount) observer reports intersecting', () => {
    render(
      <LazyEmbedMount reservedHeight={240}>
        <Child />
      </LazyEmbedMount>,
    )
    fireIntersection(0, true) // the mount observer, constructed first
    expect(screen.getByTestId('embed-child')).toBeInTheDocument()
    // MUTATION THIS DIES ON: leaving the reserved height applied after
    // mount — the child is then fighting a fixed min-height instead of
    // sizing to its own content (EMB-066's "one reflow is accepted").
    expect(screen.getByTestId('lazy-embed-mount').style.minHeight).toBe('')
    expect(screen.getByTestId('lazy-embed-mount')).toHaveAttribute('data-mounted', 'true')
  })

  it('reports the mount transition through onMountedChange', () => {
    const onMountedChange = vi.fn()
    render(
      <LazyEmbedMount reservedHeight={240} onMountedChange={onMountedChange}>
        <Child />
      </LazyEmbedMount>,
    )
    onMountedChange.mockClear()
    fireIntersection(0, true)
    expect(onMountedChange).toHaveBeenCalledWith(true)
  })
})

describe('LazyEmbedMount — stops when well outside (EMB-065), with hysteresis against the two boundaries', () => {
  it('stays mounted when the FAR (unmount) observer still reports intersecting', () => {
    render(
      <LazyEmbedMount reservedHeight={240}>
        <Child />
      </LazyEmbedMount>,
    )
    fireIntersection(0, true) // mounts
    fireIntersection(1, true) // still within the far margin — unmount observer, constructed second
    expect(screen.getByTestId('embed-child')).toBeInTheDocument()
  })

  it('unmounts once the far observer reports NOT intersecting, and restores the reserved height', () => {
    render(
      <LazyEmbedMount reservedHeight={240}>
        <Child />
      </LazyEmbedMount>,
    )
    fireIntersection(0, true) // mounts
    fireIntersection(1, false) // well outside — the unmount observer's own signal
    // MUTATION THIS DIES ON: a component that only ever mounts and never
    // unmounts — this file's OTHER tests could not tell that apart from a
    // correct implementation, which is exactly why this negative case is
    // asserted here rather than assumed.
    expect(screen.queryByTestId('embed-child')).not.toBeInTheDocument()
    expect(screen.getByTestId('lazy-embed-mount').style.minHeight).toBe('240px')
    expect(screen.getByTestId('lazy-embed-mount')).toHaveAttribute('data-mounted', 'false')
  })

  it('disconnects both observers on unmount', () => {
    const { unmount } = render(
      <LazyEmbedMount reservedHeight={240}>
        <Child />
      </LazyEmbedMount>,
    )
    unmount()
    expect(observerInstances[0]?.disconnected).toBe(true)
    expect(observerInstances[1]?.disconnected).toBe(true)
  })
})

describe('LazyEmbedMount — no hard cap (EMB-065: "there MUST be no limit on how many embeds a note may contain")', () => {
  it('mounts every instance simultaneously when all are in view — no shared ceiling across instances', () => {
    const COUNT = 12
    for (let i = 0; i < COUNT; i++) {
      render(
        <LazyEmbedMount reservedHeight={40}>
          <div data-testid={`embed-${i}`}>{i}</div>
        </LazyEmbedMount>,
      )
    }
    // Each instance constructs its own mount observer at index 2*i.
    for (let i = 0; i < COUNT; i++) {
      fireIntersection(2 * i, true)
    }
    // MUTATION THIS DIES ON: introducing any module-level counter that caps
    // simultaneous mounts — EMB-065 explicitly forbids one for THIS
    // primitive (a per-kind resource pool like the PDF worker pool is a
    // separate, deliberate exception, not implemented here).
    for (let i = 0; i < COUNT; i++) {
      expect(screen.getByTestId(`embed-${i}`)).toBeInTheDocument()
    }
  })
})

describe('LazyEmbedMount — no IntersectionObserver available (graceful degradation)', () => {
  it('mounts immediately rather than never mounting at all', () => {
    vi.stubGlobal('IntersectionObserver', undefined)
    render(
      <LazyEmbedMount reservedHeight={240}>
        <Child />
      </LazyEmbedMount>,
    )
    expect(screen.getByTestId('embed-child')).toBeInTheDocument()
  })
})

describe('LazyEmbedMount — onMountedChange cannot loop on an unmemoized callback (M13)', () => {
  it('reports a real mount transition exactly once, even when the caller passes a fresh callback every render and setStates from it', () => {
    const calls: boolean[] = []
    function Harness() {
      const [, forceRerender] = useState(0)
      return (
        <LazyEmbedMount
          reservedHeight={240}
          // Deliberately a fresh arrow function every render — no useCallback
          // — which is exactly what nothing today happens to pass, and
          // exactly what EMB-071's future mount-count consumer is likely to
          // pass by default. It also setStates from inside the callback,
          // the real reason this prop exists at all.
          onMountedChange={(m) => {
            calls.push(m)
            forceRerender((n) => n + 1)
          }}
        >
          <Child />
        </LazyEmbedMount>
      )
    }
    render(<Harness />)
    calls.length = 0 // drop the initial-mount call (mounted=false), same as the sibling test above

    // MUTATION THIS DIES ON: keying the notifying effect on
    // `[mounted, onMountedChange]` instead of `[mounted]` alone — a fresh
    // callback identity every render re-runs the effect on every render, the
    // callback's own setState produces another fresh identity, and the two
    // feed each other forever. That shape hangs this test (and, for real,
    // the reader's browser tab) rather than settling on a single call.
    fireIntersection(0, true)
    expect(calls).toEqual([true])
  })
})

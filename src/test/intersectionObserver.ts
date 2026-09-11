// A controllable IntersectionObserver for the whole vitest suite.
//
// WHY THIS EXISTS. jsdom implements no IntersectionObserver at all, and
// `LazyEmbedMount` (the inline-embed mount budget, ADR-083 EMB-065/066)
// FAILS OPEN when it is absent — `useState(() => typeof IntersectionObserver
// === 'undefined')` starts `mounted` at `true`. That is the right production
// choice (a browser without the API renders everything rather than nothing),
// but under test it meant the budget was never exercised: every suite that
// mounted an embed ran with the gate disabled, so the four Step 6 mount files
// advertised "the same lazy-mount budget (EMB-065)" in their headers while
// asserting nothing about it. The query fence is the sharpest case — it
// issues a REAL network request, which is the entire reason gating exists.
//
// THE COMPATIBILITY CONSTRAINT, AND HOW THIS MEETS IT. `setup.ts` installs
// this for EVERY suite, so the default mode must leave suites that have
// nothing to do with lazy mounting seeing what they saw before. In the
// default `auto-visible` mode `observe()` reports the target as intersecting
// SYNCHRONOUSLY, from inside the caller's own `useEffect` — so `mounted`
// flips to true within the same `act()` that `render()` already wraps, and a
// test that queried its content synchronously before still finds it.
// Deliberately synchronous, not a microtask: an async first report would
// leave every existing embed unmounted at the point those suites assert,
// which is precisely the breakage this mode exists to avoid.
//
// WHAT IT DOES NOT PRESERVE, STATED PRECISELY. The RENDERED state settles
// identically; the TRANSITION does not, and "identical behaviour" would be
// an overstatement. Without an IntersectionObserver `LazyEmbedMount` starts
// `mounted` at `true`, so a caller's `onMountedChange` fires once, with
// `true`. With this installed it starts `false` and flips, so the same
// callback fires `false` and then `true`. Any test asserting call counts or
// first-call arguments on `onMountedChange` therefore changes meaning. At
// the time of writing the only suite using that prop is
// `LazyEmbedMount.test.tsx`, which installs its own observer and is
// unaffected — but a future caller that counts those calls should know this
// is where the extra one comes from.
//
// `holdEmbedsOutOfView()` switches to `manual`, where `observe()` reports
// nothing and the test drives visibility itself. That is the mode that
// actually exercises the budget.
import { act } from '@testing-library/react'

type IntersectionMode = 'auto-visible' | 'manual'

interface Registration {
  callback: IntersectionObserverCallback
  observer: IntersectionObserver
  rootMargin: string
  targets: Set<Element>
}

const registrations = new Set<Registration>()
let mode: IntersectionMode = 'auto-visible'

/** A minimal entry. Only `target` and `isIntersecting` carry meaning for any
 *  consumer in this codebase; the geometry fields are filled from the real
 *  element so nothing reads `undefined` if a future consumer looks. */
function makeEntry(target: Element, isIntersecting: boolean): IntersectionObserverEntry {
  const rect = target.getBoundingClientRect()
  return {
    target,
    isIntersecting,
    intersectionRatio: isIntersecting ? 1 : 0,
    boundingClientRect: rect,
    intersectionRect: rect,
    rootBounds: null,
    time: 0,
  } as IntersectionObserverEntry
}

class TestIntersectionObserver implements IntersectionObserver {
  readonly root: Element | Document | null = null
  readonly rootMargin: string
  readonly thresholds: ReadonlyArray<number> = []
  readonly scrollMargin: string = ''
  private readonly registration: Registration

  constructor(callback: IntersectionObserverCallback, options?: IntersectionObserverInit) {
    this.rootMargin = options?.rootMargin ?? ''
    this.registration = {
      callback,
      observer: this,
      rootMargin: this.rootMargin,
      targets: new Set(),
    }
    registrations.add(this.registration)
  }

  observe(target: Element): void {
    this.registration.targets.add(target)
    // See the module header: synchronous, and only in the default mode.
    if (mode === 'auto-visible') {
      this.registration.callback([makeEntry(target, true)], this)
    }
  }

  unobserve(target: Element): void {
    this.registration.targets.delete(target)
  }

  disconnect(): void {
    this.registration.targets.clear()
    registrations.delete(this.registration)
  }

  takeRecords(): IntersectionObserverEntry[] {
    return []
  }
}

/** Installs the double as the global. Called once by `src/test/setup.ts`; a
 *  suite that wants the no-IntersectionObserver degradation path still gets
 *  it the usual way, with `vi.stubGlobal('IntersectionObserver', undefined)`. */
export function installTestIntersectionObserver(): void {
  globalThis.IntersectionObserver = TestIntersectionObserver as unknown as typeof IntersectionObserver
}

/** Drops every observer and returns to the default mode. Called from
 *  `setup.ts`'s `afterEach`, so one test's `holdEmbedsOutOfView()` can never
 *  leak the gate into the next test in the same file. */
export function resetTestIntersectionObservers(): void {
  registrations.clear()
  mode = 'auto-visible'
}

/**
 * Switch to manual mode: from here until the end of the test, nothing an
 * observer watches is reported as visible until `scrollIntoView` says so.
 *
 * Call it BEFORE `render()` — `LazyEmbedMount` decides whether to mount from
 * the report it gets during its own mount effect.
 */
export function holdEmbedsOutOfView(): void {
  mode = 'manual'
}

/** Every observer currently watching `target`, in construction order.
 *  `LazyEmbedMount` builds two per instance (mount margin, then unmount
 *  margin), which is why `rootMargin` is exposed for filtering. */
function observersWatching(target: Element): Registration[] {
  return [...registrations].filter((r) => r.targets.has(target))
}

/**
 * Report `target` to its observers, wrapped in `act` because a real
 * IntersectionObserver callback fires outside any React event handler and the
 * resulting `setState` needs an explicit flush.
 *
 * `rootMargin`, when given, fires only the observer constructed with that
 * exact margin — the seam that lets a test drive `LazyEmbedMount`'s two
 * boundaries independently (its mount margin and its larger unmount margin)
 * instead of only the both-agree cases.
 */
export function fireIntersection(
  target: Element,
  isIntersecting: boolean,
  options?: { rootMargin?: string },
): void {
  const matching = observersWatching(target).filter(
    (r) => options?.rootMargin === undefined || r.rootMargin === options.rootMargin,
  )
  if (matching.length === 0) {
    throw new Error(
      'fireIntersection: no IntersectionObserver is watching that element' +
        (options?.rootMargin !== undefined ? ` with rootMargin "${options.rootMargin}"` : '') +
        '. Did you call holdEmbedsOutOfView() before render(), and pass the observed element?',
    )
  }
  act(() => {
    for (const r of matching) {
      r.callback([makeEntry(target, isIntersecting)], r.observer)
    }
  })
}

/** The reader scrolls `target` into view. */
export function scrollIntoView(target: Element, options?: { rootMargin?: string }): void {
  fireIntersection(target, true, options)
}

/** The reader scrolls `target` away. */
export function scrollOutOfView(target: Element, options?: { rootMargin?: string }): void {
  fireIntersection(target, false, options)
}

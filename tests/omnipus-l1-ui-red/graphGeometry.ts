// Test-only browser geometry adapter for the REAL React Flow graph.
// jsdom has no layout engine. React Flow 12 requires measured node dimensions
// and handle bounds before rendering an edge (useNodeObserver/getEdgePosition).
// No graph/component/model is mocked. Call from beforeEach; restore with the
// caller's afterEach vi.restoreAllMocks() and vi.unstubAllGlobals().
import { vi } from 'vitest'

class MeasuringResizeObserver {
  constructor(private readonly callback: ResizeObserverCallback) {}

  observe(target: Element) {
    this.callback(
      [{ target, contentRect: target.getBoundingClientRect() } as ResizeObserverEntry],
      this as unknown as ResizeObserver,
    )
  }

  unobserve() {}
  disconnect() {}
}

export function installGraphTestGeometry() {
  // Existing graph-test viewport fixture: 800 × 600, not a product size oracle.
  const width = 800
  const height = 600
  vi.stubGlobal('ResizeObserver', MeasuringResizeObserver)
  vi.stubGlobal('DOMMatrixReadOnly', class { m22 = 1 })
  for (const key of ['offsetWidth', 'offsetHeight', 'clientWidth', 'clientHeight'] as const) {
    vi.spyOn(HTMLElement.prototype, key, 'get').mockReturnValue(key.endsWith('Width') ? width : height)
  }
  vi.spyOn(Element.prototype, 'getBoundingClientRect').mockReturnValue({
    x: 0, y: 0, width, height, top: 0, left: 0, right: width, bottom: height, toJSON: () => ({}),
  })
}

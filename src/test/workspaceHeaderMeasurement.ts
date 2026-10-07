import { act } from '@testing-library/react'
import { vi } from 'vitest'

/** Browser geometry edge: jsdom has no layout. Synthetic natural widths are
 * independent fixtures, not values read from the mode-selection implementation. */
export function mockWorkspaceHeaderMeasurements(available: number = 792, full: number = 537, icons: number = 300) {
  let availableWidth = available
  let fullWidth = full
  let iconsWidth = icons
  const observers = new Set<ResizeObserverCallback>()
  const observed = new Set<Element>()
  let disconnects = 0
  const nativeRect = HTMLElement.prototype.getBoundingClientRect
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
    let width: number | undefined
    if (this.dataset.testid === 'workspace-header-entries') width = availableWidth
    if (this.dataset.workspaceHeaderMeasure === 'full') width = fullWidth
    if (this.dataset.workspaceHeaderMeasure === 'icons') width = iconsWidth
    return width === undefined ? nativeRect.call(this) : DOMRect.fromRect({ width, height: 44 })
  })
  vi.stubGlobal('ResizeObserver', class {
    constructor(private readonly callback: ResizeObserverCallback) { observers.add(callback) }
    observe(element: Element) { observed.add(element) }
    unobserve(element: Element) { observed.delete(element) }
    disconnect() { observers.delete(this.callback); disconnects++ }
  })
  function notify() {
    act(() => {
      for (const callback of observers) callback([], {} as ResizeObserver)
    })
  }
  return {
    observed,
    get disconnects() { return disconnects },
    setAvailableWidth(width: number) { availableWidth = width; notify() },
    setNaturalWidths(nextFullWidth: number, nextIconsWidth: number) { fullWidth = nextFullWidth; iconsWidth = nextIconsWidth; notify() },
  }
}

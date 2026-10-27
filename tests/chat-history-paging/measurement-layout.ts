// Deterministic BROWSER boundary for jsdom; no paging/measurement/virtualizer
// function is replaced. Row rectangles come from physical fixture heights.
// Virtual row offsets come ONLY from the production-rendered translateY style,
// and canvas height comes ONLY from its real spacer, never an injected prefix.
import { vi } from 'vitest'

export function installHistoryLayout({ virtual = false, hidden = false } = {}) {
  const state = {
    height: 800, width: 1024, rowHeight: 80, controlHeight: 32, hidden,
    rowHeights: new Map<string, number>(), toolHeights: new Map<string, number>(),
  }
  const descriptors = new Map<string, PropertyDescriptor | undefined>()
  const originalRect = HTMLElement.prototype.getBoundingClientRect
  const originalScrollTo = Object.getOwnPropertyDescriptor(Element.prototype, 'scrollTo')
  const originalHidden = Object.getOwnPropertyDescriptor(document, 'hidden')
  const originalVisibility = Object.getOwnPropertyDescriptor(document, 'visibilityState')
  const scrollTops = new WeakMap<Element, number>()
  const observers = new Set<LayoutResizeObserver>()
  const isViewport = (element: Element) => element.getAttribute('aria-label') === 'Conversation history'
  const viewportFor = (element: Element) => element.closest<HTMLElement>('[aria-label="Conversation history"]')
  const rowHeight = (element: HTMLElement) => {
    if (element.hidden) return 0
    return state.rowHeights.get(element.dataset.historyPosition ?? '')
      ?? state.rowHeight * (1 + element.querySelectorAll('[data-history-tool-row]').length)
  }
  const controlBlockHeight = (element: Element) => {
    const buttons = element.querySelectorAll('button').length
    const statuses = element.querySelectorAll('[role="status"], [role="alert"]').length
    return (buttons + statuses) * state.controlHeight
  }
  const contentFor = (viewport: Element) => viewport.querySelector<HTMLElement>('[data-history-content]')
  const prefixHeight = (content: HTMLElement) => {
    const siblings = [...(content.parentElement?.children ?? [])]
    return siblings.slice(0, siblings.indexOf(content)).reduce((sum, item) => sum + controlBlockHeight(item), 0)
  }
  const contentHeight = (content: HTMLElement) => content.style.height
    ? Number.parseFloat(content.style.height)
    : [...content.querySelectorAll<HTMLElement>('[data-history-anchor]')].reduce((sum, row) => sum + rowHeight(row), 0)
  const scrollHeight = (viewport: Element) => {
    const content = contentFor(viewport)
    if (!content) return state.height
    const siblings = [...(content.parentElement?.children ?? [])]
    const suffix = siblings.slice(siblings.indexOf(content) + 1).reduce((sum, item) => sum + controlBlockHeight(item), 0)
    return Math.max(state.height, prefixHeight(content) + contentHeight(content) + suffix)
  }
  const rowTop = (row: HTMLElement, viewport: HTMLElement) => {
    const content = contentFor(viewport)
    const parent = row.parentElement
    if (!content || !parent) return 0
    // Read the REAL virtualizer's physical translation. Do not infer a prefix
    // from data-index, source rank, or the desired average.
    const translate = /translateY\(([-\d.]+)px\)/.exec(parent.style.transform)
    const before = [...parent.children].slice(0, [...parent.children].indexOf(row))
    return prefixHeight(content) + Number(translate?.[1] ?? 0)
      + before.reduce((sum, item) => sum + (item instanceof HTMLElement && item.hasAttribute('data-history-anchor') ? rowHeight(item) : 0), 0)
      - viewport.scrollTop
  }
  const rectFor = (element: HTMLElement): DOMRect => {
    const viewport = viewportFor(element)
    if (!viewport) return originalRect.call(element)
    if (isViewport(element)) return new DOMRect(0, 0, state.width, state.height)
    if (element.hasAttribute('data-history-anchor')) return new DOMRect(0, rowTop(element, viewport), state.width, rowHeight(element))
    if (element.hasAttribute('data-history-tool-row')) {
      const height = state.toolHeights.get(element.dataset.historyToolRow ?? '') ?? state.rowHeight
      return new DOMRect(0, 0, state.width, height)
    }
    if (element.hasAttribute('data-history-content')) return new DOMRect(0, prefixHeight(element) - viewport.scrollTop, state.width, contentHeight(element))
    return originalRect.call(element)
  }
  HTMLElement.prototype.getBoundingClientRect = function () { return rectFor(this) }
  for (const name of ['clientHeight', 'clientWidth', 'offsetHeight', 'offsetWidth', 'scrollHeight', 'scrollTop']) {
    const original = Object.getOwnPropertyDescriptor(HTMLElement.prototype, name)
    descriptors.set(name, original)
    Object.defineProperty(HTMLElement.prototype, name, {
      configurable: true,
      get(this: HTMLElement) {
        if (name === 'scrollTop') return isViewport(this) ? scrollTops.get(this) ?? 0 : original?.get?.call(this) ?? 0
        if (name === 'scrollHeight' && isViewport(this)) return scrollHeight(this)
        if (isViewport(this)) return name.endsWith('Width') ? state.width : state.height
        if ((name === 'offsetHeight' || name === 'offsetWidth') && viewportFor(this)) {
          const rect = rectFor(this)
          return name === 'offsetHeight' ? rect.height : rect.width
        }
        return original?.get?.call(this) ?? 0
      },
      ...(name === 'scrollTop' ? { set(this: HTMLElement, value: number) {
        if (isViewport(this)) scrollTops.set(this, Math.max(0, Math.min(value, scrollHeight(this) - state.height)))
        else original?.set?.call(this, value)
      } } : {}),
    })
  }
  Object.defineProperty(Element.prototype, 'scrollTo', { configurable: true, value(this: HTMLElement, first: ScrollToOptions | number, second?: number) {
    this.scrollTop = typeof first === 'number' ? second ?? 0 : first.top ?? this.scrollTop
    this.dispatchEvent(new Event('scroll'))
  } })
  Object.defineProperty(document, 'hidden', { configurable: true, get: () => state.hidden })
  Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => state.hidden ? 'hidden' : 'visible' })

  class LayoutResizeObserver implements ResizeObserver {
    readonly targets = new Set<Element>()
    constructor(readonly callback: ResizeObserverCallback) { observers.add(this) }
    observe(target: Element) { this.targets.add(target) }
    unobserve(target: Element) { this.targets.delete(target) }
    disconnect() { this.targets.clear(); observers.delete(this) }
  }
  vi.stubGlobal('ResizeObserver', virtual ? LayoutResizeObserver : undefined)

  return {
    state,
    flushResize() {
      for (const observer of [...observers]) {
        const entries: ResizeObserverEntry[] = [...observer.targets].filter(target => target.isConnected).map(target => {
          const rect = target.getBoundingClientRect()
          const size = { inlineSize: rect.width, blockSize: rect.height }
          return { target, contentRect: rect, borderBoxSize: [size], contentBoxSize: [size], devicePixelContentBoxSize: [size] }
        })
        if (entries.length) observer.callback(entries, observer)
      }
    },
    visibility(hidden: boolean) {
      state.hidden = hidden
      document.dispatchEvent(new Event('visibilitychange'))
    },
    restore() {
      for (const observer of [...observers]) observer.disconnect()
      HTMLElement.prototype.getBoundingClientRect = originalRect
      for (const [name, original] of descriptors) {
        if (original) Object.defineProperty(HTMLElement.prototype, name, original)
        else Reflect.deleteProperty(HTMLElement.prototype, name)
      }
      for (const [object, name, original] of [
        [Element.prototype, 'scrollTo', originalScrollTo],
        [document, 'hidden', originalHidden], [document, 'visibilityState', originalVisibility],
      ] as const) {
        if (original) Object.defineProperty(object, name, original)
        else Reflect.deleteProperty(object, name)
      }
    },
  }
}

// Deterministic BROWSER boundary for jsdom; no paging/measurement/virtualizer
// function is replaced. Row rectangles come from physical fixture heights.
// Virtual row offsets come ONLY from the production-rendered translateY style,
// and canvas height comes ONLY from its real spacer, never an injected prefix.
import { vi } from 'vitest'
import { createPhysicalLayoutCache } from './physical-layout-cache'

export function installHistoryLayout({ virtual = false, hidden = false, innerParts = false } = {}) {
  const state = {
    height: 800, width: 1024, rowHeight: 80, controlHeight: 32, hidden,
    rowHeights: new Map<string, number>(), toolHeights: new Map<string, number>(),
    // S11/T83 opt-in physical browser metrics. Defaults above are unchanged.
    // Heights are independent fixture inputs, never read from store parts.
    partHeights: new Map<string, number>(), paragraphHeight: 60,
  }
  const cache = createPhysicalLayoutCache(state)
  const descriptors = new Map<string, PropertyDescriptor | undefined>()
  const originalRect = HTMLElement.prototype.getBoundingClientRect
  const originalScrollTo = Object.getOwnPropertyDescriptor(Element.prototype, 'scrollTo')
  const originalHidden = Object.getOwnPropertyDescriptor(document, 'hidden')
  const originalVisibility = Object.getOwnPropertyDescriptor(document, 'visibilityState')
  const scrollTops = new WeakMap<Element, number>()
  const observers = new Set<LayoutResizeObserver>()
  const isViewport = (element: Element) => element.getAttribute('aria-label') === 'Conversation history'
  const viewportFor = (element: Element) => element.closest<HTMLElement>('[aria-label="Conversation history"]')
  const partsFor = (row: HTMLElement) => cache.read(row, 'parts', () => [...row.querySelectorAll<HTMLElement>('[data-history-part]')]
    .filter(part => part.closest('[data-history-anchor]') === row && !part.parentElement?.closest('[data-history-part]')))
  const partHeight = (part: HTMLElement) => cache.read(part, 'part-height', () => (state.partHeights.get(part.dataset.historyPart ?? '') ?? state.rowHeight)
    + [...part.querySelectorAll<HTMLElement>('[data-history-tool-row]')].reduce((sum, tool) => sum + (state.toolHeights.get(tool.dataset.historyToolRow ?? '') ?? state.rowHeight), 0))
  const rowHeight = (element: HTMLElement) => cache.read(element, 'row-height', () => {
    if (element.hidden) return 0
    const parts = innerParts ? partsFor(element) : []
    if (parts.length) return parts.reduce((sum, part) => sum + partHeight(part), 0)
    return state.rowHeights.get(element.dataset.historyPosition ?? '')
      ?? state.rowHeight * (1 + element.querySelectorAll('[data-history-tool-row]').length)
  })
  const controlBlockHeight = (element: Element) => cache.read(element, 'control-height', () => {
    const buttons = element.querySelectorAll('button').length
    const statuses = element.querySelectorAll('[role="status"], [role="alert"]').length
    return (buttons + statuses) * state.controlHeight
  })
  const contentFor = (viewport: Element) => cache.read(viewport, 'content', () => viewport.querySelector<HTMLElement>('[data-history-content]'))
  const prefixHeight = (content: HTMLElement) => cache.read(content, 'prefix-height', () => {
    let height = 0
    for (let item = content.parentElement?.firstElementChild; item && item !== content; item = item.nextElementSibling) height += controlBlockHeight(item)
    return height
  })
  const contentHeight = (content: HTMLElement) => cache.read(content, 'content-height', () => content.style.height
    ? Number.parseFloat(content.style.height)
    : [...content.querySelectorAll<HTMLElement>('[data-history-anchor]')].reduce((sum, row) => sum + rowHeight(row), 0))
  const scrollHeight = (viewport: Element) => {
    cache.prepare(viewport)
    return cache.read(viewport, 'scroll-height', () => {
      const content = contentFor(viewport)
      if (!content) return state.height
      let suffix = 0
      for (let item = content.nextElementSibling; item; item = item.nextElementSibling) suffix += controlBlockHeight(item)
      return Math.max(state.height, prefixHeight(content) + contentHeight(content) + suffix)
    })
  }
  const rowTop = (row: HTMLElement, viewport: HTMLElement) => {
    const content = contentFor(viewport)
    const parent = row.parentElement
    if (!content || !parent) return 0
    // Read the REAL virtualizer's physical translation. Do not infer a prefix
    // from data-index, source rank, or the desired average.
    const translate = /translateY\(([-\d.]+)px\)/.exec(parent.style.transform)
    // Sum actual sibling heights once per unchanged physical snapshot, not
    // once per measured row. DOM/map edits synchronously invalidate the cache;
    // the real virtual translation and browser scroll are still read fresh.
    const offsets = cache.read(parent, 'row-offsets', () => {
      const measured = new WeakMap<HTMLElement, number>()
      let before = 0
      for (let item = parent.firstElementChild; item; item = item.nextElementSibling) {
        if (item instanceof HTMLElement && item.hasAttribute('data-history-anchor')) {
          measured.set(item, before)
          before += rowHeight(item)
        }
      }
      return measured
    })
    return prefixHeight(content) + Number(translate?.[1] ?? 0) + (offsets.get(row) ?? 0) - viewport.scrollTop
  }
  const rectFor = (element: HTMLElement): DOMRect => {
    const viewport = viewportFor(element)
    if (!viewport) return originalRect.call(element)
    cache.prepare(viewport)
    if (isViewport(element)) return new DOMRect(0, 0, state.width, state.height)
    if (element.hasAttribute('data-history-anchor')) return new DOMRect(0, rowTop(element, viewport), state.width, rowHeight(element))
    if (innerParts) {
      const part = element.closest<HTMLElement>('[data-history-part]')
      const row = part?.closest<HTMLElement>('[data-history-anchor]')
      if (part && row) {
        const parts = partsFor(row)
        const prefix = parts.slice(0, parts.indexOf(part)).reduce((sum, prior) => sum + partHeight(prior), 0)
        const paragraph = element.matches('p') ? element : element.closest<HTMLElement>('p')
        const paragraphs = [...part.querySelectorAll<HTMLElement>('p')]
        const paragraphOffset = paragraph ? Math.max(0, paragraphs.indexOf(paragraph)) * state.paragraphHeight : 0
        const tool = element.closest<HTMLElement>('[data-history-tool-row]')
        const toolOffset = tool ? state.partHeights.get(part.dataset.historyPart ?? '') ?? state.rowHeight : 0
        const height = tool ? state.toolHeights.get(tool.dataset.historyToolRow ?? '') ?? state.rowHeight
          : paragraph ? state.paragraphHeight : partHeight(part)
        return new DOMRect(0, rowTop(row, viewport) + prefix + paragraphOffset + toolOffset, state.width, height)
      }
    }
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
      cache.disconnect()
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

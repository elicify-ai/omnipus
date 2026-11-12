// #1697 — @tanstack/virtual-core's own scroll observer arms a 150 ms
// "stopped scrolling" debounce that its cleanup never cancels, so a virtualizer
// torn down right after a scroll left a timer that fired after the page (and,
// in a test file, the jsdom window) was gone. This observer is the same
// behaviour with a cleanup that cancels it.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { observeElementOffsetCancellable } from './observeElementOffsetCancellable'

type Instance = Parameters<typeof observeElementOffsetCancellable>[0]

function makeInstance(overrides: Record<string, unknown> = {}): { instance: Instance; element: HTMLDivElement } {
  const element = document.createElement('div')
  const instance = {
    scrollElement: element,
    targetWindow: window,
    options: { horizontal: false, isRtl: false, isScrollingResetDelay: 150 },
    ...overrides,
  } as unknown as Instance
  return { instance, element }
}

describe('observeElementOffsetCancellable', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  it('reports the offset while scrolling and "stopped" after the reset delay', () => {
    const { instance, element } = makeInstance()
    const cb = vi.fn()
    observeElementOffsetCancellable(instance, cb)
    element.scrollTop = 40
    element.dispatchEvent(new Event('scroll'))
    expect(cb).toHaveBeenLastCalledWith(40, true)
    vi.advanceTimersByTime(149)
    expect(cb).toHaveBeenCalledTimes(1)
    vi.advanceTimersByTime(1)
    expect(cb).toHaveBeenLastCalledWith(40, false)
  })

  it('restarts the delay on every scroll event', () => {
    const { instance, element } = makeInstance()
    const cb = vi.fn()
    observeElementOffsetCancellable(instance, cb)
    element.dispatchEvent(new Event('scroll'))
    vi.advanceTimersByTime(100)
    element.dispatchEvent(new Event('scroll'))
    vi.advanceTimersByTime(100)
    expect(cb).not.toHaveBeenCalledWith(expect.anything(), false)
    vi.advanceTimersByTime(50)
    expect(cb).toHaveBeenLastCalledWith(0, false)
  })

  it('cleanup cancels the pending debounce and stops listening', () => {
    const { instance, element } = makeInstance()
    const cb = vi.fn()
    const cleanup = observeElementOffsetCancellable(instance, cb)
    element.dispatchEvent(new Event('scroll'))
    expect(vi.getTimerCount()).toBe(1)
    ;(cleanup as () => void)()
    expect(vi.getTimerCount()).toBe(0)
    cb.mockClear()
    element.dispatchEvent(new Event('scroll'))
    vi.advanceTimersByTime(1_000)
    expect(cb).not.toHaveBeenCalled()
  })

  it('reads scrollLeft, negated for rtl, when horizontal', () => {
    const { instance, element } = makeInstance({ options: { horizontal: true, isRtl: true, isScrollingResetDelay: 150 } })
    const cb = vi.fn()
    observeElementOffsetCancellable(instance, cb)
    element.scrollLeft = 25
    element.dispatchEvent(new Event('scroll'))
    expect(cb).toHaveBeenLastCalledWith(-25, true)
  })

  it('does nothing without a scroll element or a window', () => {
    const cb = vi.fn()
    expect(observeElementOffsetCancellable(makeInstance({ scrollElement: null }).instance, cb)).toBeUndefined()
    expect(observeElementOffsetCancellable(makeInstance({ targetWindow: null }).instance, cb)).toBeUndefined()
  })
})

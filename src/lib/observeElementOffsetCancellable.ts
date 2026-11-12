// #1697 — the scroll observer of @tanstack/virtual-core (observeElementOffset)
// with one change: its cleanup also cancels the pending "stopped scrolling"
// debounce. The library's cleanup only removes the listeners, so a virtualizer
// torn down within isScrollingResetDelay of a scroll left a timer that fired
// afterwards, against a page that no longer existed.
//
// Everything else follows the library's fallback path (the one used when
// useScrollendEvent is off, which is its default): offset read per scroll event,
// cb(offset, true) on each event, cb(offset, false) after the quiet period.
//
// PINNED TO @tanstack/virtual-core 3.17.6 (observeElementOffset, fallback path).
// DELETE THIS COPY when an upgraded @tanstack/virtual-core cancels its
// isScrollingResetDelay debounce in the observer's cleanup (check
// dist/esm/index.js observeOffset: the returned cleanup must call
// targetWindow.clearTimeout on the debounce), and remove the
// `observeElementOffset` option in ChatMessageList. A version bump that changes
// observeOffset must be diffed against this file first.
import type { Virtualizer } from '@tanstack/react-virtual'

export function observeElementOffsetCancellable<TScrollElement extends Element, TItemElement extends Element>(
  instance: Virtualizer<TScrollElement, TItemElement>,
  cb: (offset: number, isScrolling: boolean) => void,
): (() => void) | undefined {
  const element = instance.scrollElement
  const targetWindow = instance.targetWindow
  if (!element || !targetWindow) return undefined

  let offset = 0
  let timer: number | undefined
  const onScroll = (): void => {
    const { horizontal, isRtl } = instance.options
    offset = horizontal ? element.scrollLeft * ((isRtl && -1) || 1) : element.scrollTop
    targetWindow.clearTimeout(timer)
    timer = targetWindow.setTimeout(() => cb(offset, false), instance.options.isScrollingResetDelay)
    cb(offset, true)
  }
  element.addEventListener('scroll', onScroll, { passive: true })
  return () => {
    element.removeEventListener('scroll', onScroll)
    targetWindow.clearTimeout(timer)
    timer = undefined
  }
}

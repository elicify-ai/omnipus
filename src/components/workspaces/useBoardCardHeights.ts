import { useLayoutEffect, type RefObject } from 'react'
import type { Task } from '@/lib/api'

/** Measure intrinsic inner content, never the equalized outer card. Applying
 * the maximum to the outer minimum therefore cannot ratchet its own input.
 * The CSS property is runtime geometry, not a fixed design-system size.
 */
export function useBoardCardHeights(rootRef: RefObject<HTMLDivElement | null>, tasks: readonly Task[]) {
  useLayoutEffect(() => {
    const root = rootRef.current
    if (!root) return
    const contents = [...root.querySelectorAll<HTMLElement>('[data-task-card-content="item"]')]
    let pending = 0
    const measure = () => {
      pending = 0
      const heights = contents.map((content) => {
        const card = content.parentElement!
        const style = getComputedStyle(card)
        return content.getBoundingClientRect().height + (parseFloat(style.borderTopWidth) || 0) + (parseFloat(style.borderBottomWidth) || 0)
      })
      const maximum = Math.ceil(Math.max(0, ...heights))
      if (maximum > 0) root.style.setProperty('--tasks-board-card-height', `${maximum}px`)
      else root.style.removeProperty('--tasks-board-card-height')
    }
    const schedule = () => { if (!pending) pending = requestAnimationFrame(measure) }
    measure()
    const observer = typeof ResizeObserver === 'undefined' ? undefined : new ResizeObserver(schedule)
    for (const content of contents) observer?.observe(content)
    observer?.observe(root)
    return () => {
      observer?.disconnect()
      cancelAnimationFrame(pending)
      root.style.removeProperty('--tasks-board-card-height')
    }
  }, [rootRef, tasks])
}

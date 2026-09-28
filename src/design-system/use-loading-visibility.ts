import { useLayoutEffect, useRef, useState } from 'react'
import { motionResolvedTokens } from './tokens'

const delayMs = Number.parseFloat(motionResolvedTokens['motion.loading.delay'])
const minimumVisibleMs = Number.parseFloat(motionResolvedTokens['motion.loading.minimumVisible'])

/** Controls indicator visibility only; callers retain their content and operation state. */
export function useLoadingVisibility(pending: boolean): boolean {
  const [visible, setVisible] = useState(false)
  const shownAt = useRef<number | null>(null)

  // useLayoutEffect, not useEffect: the reserve timer must be scheduled in the
  // synchronous commit phase. A passive effect flushes as a post-paint task, and
  // under browser-test contention (sibling iframes on one main thread) that flush
  // was measured landing ~800ms after play start — the story's waitFor deadline
  // (1000ms from play start) then expired before the 400ms reserve could finish
  // (webkit-only CI red: run 36302909447, skeleton Loading, data-visible=false).
  // A layout effect flushes inside the commit task, and the play can only start
  // after that task completes, so the flip target is always ≥600ms inside the
  // waitFor window regardless of load. Delay value: motion.loading.delay token
  // (design-system/tokens/foundations.json, 400ms).
  useLayoutEffect(() => {
    let timer: ReturnType<typeof setTimeout> | undefined
    if (pending) {
      if (shownAt.current === null) {
        timer = setTimeout(() => {
          shownAt.current = performance.now()
          setVisible(true)
        }, delayMs)
      }
    } else if (shownAt.current !== null) {
      const remaining = Math.max(0, minimumVisibleMs - (performance.now() - shownAt.current))
      const hide = () => {
        shownAt.current = null
        setVisible(false)
      }
      if (remaining === 0) hide()
      else timer = setTimeout(hide, remaining)
    }
    return () => { if (timer !== undefined) clearTimeout(timer) }
  }, [pending])

  return visible
}

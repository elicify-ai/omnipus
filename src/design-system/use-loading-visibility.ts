import { useEffect, useRef, useState } from 'react'
import { motionResolvedTokens } from './tokens'

const delayMs = Number.parseFloat(motionResolvedTokens['motion.loading.delay'])
const minimumVisibleMs = Number.parseFloat(motionResolvedTokens['motion.loading.minimumVisible'])

/** Controls indicator visibility only; callers retain their content and operation state. */
export function useLoadingVisibility(pending: boolean): boolean {
  const [visible, setVisible] = useState(false)
  const shownAt = useRef<number | null>(null)

  useEffect(() => {
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

import { useEffect, useState } from 'react'

export type AttentionMotion = 'loop' | 'static'

export function readAttentionMotion(): AttentionMotion {
  // #1449: the group page draws this cue too; an environment without
  // matchMedia (embedded views, test doubles) keeps the default loop
  // instead of failing the whole page.
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return 'loop'
  return window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 'static' : 'loop'
}

export function useAttentionMotion(): AttentionMotion {
  const [motion, setMotion] = useState<AttentionMotion>(readAttentionMotion)
  useEffect(() => {
    if (typeof window.matchMedia !== 'function') return undefined
    const mq = window.matchMedia('(prefers-reduced-motion: reduce)')
    const apply = () => setMotion(mq.matches ? 'static' : 'loop')
    apply()
    mq.addEventListener('change', apply)
    return () => mq.removeEventListener('change', apply)
  }, [])
  return motion
}

/** Distinct confirmed mains. Never phrases a zero as a count. */
export function attentionCountLabel(count: number): string {
  if (count === 1) return '1 main chat needs your attention'
  return `${count} main chats need your attention`
}

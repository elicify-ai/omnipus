import { useEffect, useState } from 'react'

export type AttentionMotion = 'loop' | 'static'

export function readAttentionMotion(): AttentionMotion {
  if (typeof window === 'undefined') return 'loop'
  return window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 'static' : 'loop'
}

export function useAttentionMotion(): AttentionMotion {
  const [motion, setMotion] = useState<AttentionMotion>(readAttentionMotion)
  useEffect(() => {
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

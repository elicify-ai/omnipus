import { useEffect, useRef, useState } from 'react'
import { cn } from '@/lib/utils'

export interface WordBoundaryTextProps {
  text: string
  as?: 'span' | 'p' | 'h3'
  className?: string
  title?: string
}

/** Preserve source text and complete word boundaries, including hyphenated
 * compounds. Only words wider than the owner's whole line remain breakable.
 * Browser font metrics are geometry, not a guessed character-count limit.
 */
export function WordBoundaryText({ text, as: Tag = 'span', className, title }: WordBoundaryTextProps) {
  const root = useRef<HTMLElement | null>(null)
  const [fits, setFits] = useState<ReadonlySet<number>>(() => new Set())
  const words = text.match(/\s+|\S+/g) ?? []
  useEffect(() => {
    const owner = root.current
    if (!owner) return
    let alive = true
    const measure = () => {
      if (!alive) return
      const style = getComputedStyle(owner)
      const width = owner.clientWidth - (Number.parseFloat(style.paddingLeft) || 0) - (Number.parseFloat(style.paddingRight) || 0)
      if (!(width > 0)) return
      const canvas = document.createElement('canvas').getContext('2d')
      if (!canvas) return
      canvas.font = `${style.fontStyle} ${style.fontWeight} ${style.fontSize} ${style.fontFamily}`
      const next = new Set<number>()
      const parts = text.match(/\s+|\S+/g) ?? []
      parts.forEach((word, index) => { if (/\S/.test(word) && canvas.measureText(word).width <= width - 1) next.add(index) })
      if (alive) setFits((previous) => previous.size === next.size && [...next].every((index) => previous.has(index)) ? previous : next)
    }
    measure()
    void document.fonts?.ready.then(measure)
    if (typeof ResizeObserver === 'undefined') return () => { alive = false }
    const observer = new ResizeObserver(measure)
    observer.observe(owner)
    return () => { alive = false; observer.disconnect() }
  }, [text])
  return <Tag ref={(element) => { root.current = element }} title={title} className={cn('block min-w-0 max-w-full', className)}>{words.map((word, index) => fits.has(index)
    ? <span key={index} data-word-boundary="" className="inline-block whitespace-nowrap align-baseline">{word}</span>
    : word)}</Tag>
}

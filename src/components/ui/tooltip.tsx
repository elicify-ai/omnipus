import * as React from 'react'
import { useEffect, useId, useLayoutEffect, useRef, useState } from 'react'
import { cn } from '@/lib/utils'

export interface TooltipProps {
  /** Supplementary explanation shown on hover or keyboard focus. */
  content: React.ReactNode
  /**
   * Accessible name read for the trigger on its own — only needed when
   * `children` carries no readable text of its own (an icon-only trigger).
   * A trigger with visible text already has an accessible name from that
   * text, so `label` is optional.
   */
  label?: string
  /** The trigger — anything hoverable/focusable/tappable. */
  children: React.ReactNode
  /** Which side of the trigger the tooltip bubble opens toward. */
  side?: 'top' | 'bottom'
  className?: string
  'data-testid'?: string
}

// Matches the global spacing scale's --space-2 (8px) — the minimum gap kept
// between the bubble and either viewport edge when it has to shift off its
// default centered position.
const VIEWPORT_MARGIN_PX = 8

// Tooltip — WAI-ARIA tooltip pattern: a trigger carrying `aria-describedby`
// while open, referencing a `role="tooltip"` bubble. Deliberately NOT built
// on a Radix primitive (no @radix-ui/react-tooltip dependency exists in this
// project) — the interaction surface here (hover/focus reveal, Escape
// dismiss, no focus trap) is small enough that a full positioning engine
// would add more risk than it removes, and the existing hand-rolled
// precedent (ConnectionStatus.tsx's local StatusTooltip) already proves the
// simple approach works. This is that pattern, generalized and catalogued so
// a future caller extends the kit instead of writing a sixth copy.
//
// The one piece a pure-CSS version can't do: a bubble centered under a
// trigger near either edge of the screen (e.g. a chat-header badge sitting
// top-right) pushes itself off-screen by construction — half the bubble's
// width extends past whichever edge the trigger is close to, no matter how
// narrow the bubble is. `clampToViewport` below is a single bounding-rect
// measurement on open, not a continuous positioning engine: it nudges the
// bubble back inside the viewport with a small horizontal shift and leaves
// vertical placement (`side`) exactly where the caller asked for it.
export function Tooltip({ content, label, children, side = 'top', className, ...rest }: TooltipProps) {
  const [open, setOpen] = useState(false)
  const tooltipId = useId()
  const bubbleRef = useRef<HTMLSpanElement>(null)
  const [shiftPx, setShiftPx] = useState(0)

  useLayoutEffect(() => {
    if (!open) return
    const bubble = bubbleRef.current
    if (!bubble) return
    const rect = bubble.getBoundingClientRect()
    const viewportWidth = window.innerWidth
    if (rect.left < VIEWPORT_MARGIN_PX) {
      setShiftPx(VIEWPORT_MARGIN_PX - rect.left)
    } else if (rect.right > viewportWidth - VIEWPORT_MARGIN_PX) {
      setShiftPx(viewportWidth - VIEWPORT_MARGIN_PX - rect.right)
    } else {
      setShiftPx(0)
    }
  }, [open])

  // Escape dismisses the bubble WITHOUT moving focus off the trigger — the
  // "not a focus trap" requirement. The operator's keyboard position never
  // changes; only the supplementary text goes away.
  useEffect(() => {
    if (!open) return
    function onKeyDown(event: KeyboardEvent) {
      if (event.key !== 'Escape') return
      event.stopPropagation()
      setOpen(false)
    }
    document.addEventListener('keydown', onKeyDown)
    return () => document.removeEventListener('keydown', onKeyDown)
  }, [open])

  return (
    <span className="relative inline-flex">
      <span
        tabIndex={0}
        data-ds-action=""
        aria-label={label}
        aria-describedby={open ? tooltipId : undefined}
        onMouseEnter={() => setOpen(true)}
        onMouseLeave={() => setOpen(false)}
        onFocus={() => setOpen(true)}
        onBlur={() => setOpen(false)}
        // Touch has no hover — a tap toggles the bubble so a coarse-pointer
        // operator can reach the same explanation a mouse/keyboard user gets
        // for free.
        onClick={() => setOpen((value) => !value)}
        className="relative inline-flex"
        {...rest}
      >
        {children}
      </span>
      {open && (
        <span
          ref={bubbleRef}
          id={tooltipId}
          role="tooltip"
          style={{ transform: `translateX(calc(-50% + ${shiftPx}px))` }}
          className={cn(
            'pointer-events-none absolute left-1/2 z-50 w-64 max-w-[calc(100vw-var(--space-4))] rounded-md border border-[var(--color-border)] bg-[var(--color-surface-3)] px-[var(--space-2)] py-[var(--space-1)] text-left text-[length:var(--type-caption-size)] text-[var(--color-secondary)] shadow-lg',
            side === 'top' ? 'bottom-full mb-[var(--space-1)]' : 'top-full mt-[var(--space-1)]',
            className,
          )}
        >
          {content}
        </span>
      )}
    </span>
  )
}

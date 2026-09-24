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

// CLOSE_GRACE_MS bridges the small visual gap (the bubble's mb/mt-[--space-1]
// offset) between the trigger and the bubble below/above it. Without a
// grace period, moving the pointer from the trigger toward the bubble
// crosses a sliver of dead space that isn't part of either element's own
// box, firing the trigger's mouseleave (closing, and un-rendering the
// bubble) before the pointer ever reaches it — the bubble would then never
// be hoverable at all, however its own handlers are wired. 120ms is ample
// time to cross a 4px gap and short enough that a genuine "moved away"
// still reads as prompt.
const CLOSE_GRACE_MS = 120

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
  const closeTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  function cancelScheduledClose() {
    if (closeTimerRef.current !== null) {
      clearTimeout(closeTimerRef.current)
      closeTimerRef.current = null
    }
  }

  // openNow cancels any pending close and opens immediately — used by both
  // the trigger and the bubble's own mouseenter, so re-entering either one
  // while a close is pending (mid-grace-period) keeps the tooltip open.
  function openNow() {
    cancelScheduledClose()
    setOpen(true)
  }

  // scheduleClose (not an immediate setOpen(false)) is WCAG 1.4.13's
  // "hoverable" requirement in practice: leaving the trigger toward the
  // bubble must not close it before the pointer arrives — see
  // CLOSE_GRACE_MS above.
  function scheduleClose() {
    cancelScheduledClose()
    closeTimerRef.current = setTimeout(() => setOpen(false), CLOSE_GRACE_MS)
  }

  function closeNow() {
    cancelScheduledClose()
    setOpen(false)
  }

  useEffect(() => cancelScheduledClose, [])

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
      closeNow()
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
        onMouseEnter={openNow}
        onMouseLeave={scheduleClose}
        onFocus={openNow}
        onBlur={closeNow}
        // A real tap synthesizes mouseenter/mouseover AND focus AND click,
        // all for the SAME gesture (in that rough order) — a click handler
        // that TOGGLES used to undo the hover/focus-driven open before the
        // operator ever saw the bubble (issue: "a real tap ... may never
        // open on touch"). openNow is idempotent: it only ever opens, so a
        // tap that already opened the bubble via hover/focus leaves it
        // open, and a tap on a device where hover/focus didn't fire still
        // opens it. Touch has no hover to dismiss with — closing then
        // relies on Escape or moving focus elsewhere (tapping away blurs).
        onClick={openNow}
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
          // WCAG 1.4.13 (Content on Hover or Focus) requires hover-revealed
          // content to be "hoverable" — the pointer must be able to move
          // onto it without it disappearing. pointer-events-none defeated
          // that outright (the bubble could never receive its own
          // mouseenter/mouseleave, and anything under it "showed through"
          // to hover/click instead). The bubble now tracks its own
          // hover state, coordinating with the trigger via
          // openNow/scheduleClose's shared grace period.
          onMouseEnter={openNow}
          onMouseLeave={scheduleClose}
          className={cn(
            // pointer-coarse:pointer-events-none — a coarse (touch) pointer has no
            // hover state for WCAG 1.4.13 to protect: this rule exists for a mouse
            // that can travel from trigger to bubble. Left pointer-events:auto on
            // touch, the bubble (z-50, painted above the trigger) sits inside the
            // trigger's own touch-target-minimum hit region (`[data-ds-action]`'s
            // 44px expansion reaches past the mb/mt-[--space-1] gap into the open
            // bubble above it) and — the later sibling wins an overlap — steals
            // taps meant for the trigger. Touch already opens on tap and closes on
            // Escape/tap-away (see the trigger's onClick comment below), so nothing
            // is lost by not receiving pointer events here on a coarse pointer.
            'pointer-coarse:pointer-events-none',
            // forced-colors: without an explicit system-color repaint, forced-colors
            // mode's own default color substitution for an un-opted-out element does
            // not reliably keep border/background/text distinguishable (precedent:
            // 69d61f59d's WebKit finding of #ffffff-on-#c0c0c0, 1.81:1, from the same
            // default-heuristic gap) — matches Button/Progress/Slider/Switch's own
            // forced-color-adjust:none + explicit system-color pattern.
            'absolute left-1/2 z-50 w-64 max-w-[calc(100vw-var(--space-4))] rounded-md border border-[var(--color-border)] bg-[var(--color-surface-3)] px-[var(--space-2)] py-[var(--space-1)] text-left text-[length:var(--type-caption-size)] text-[var(--color-secondary)] shadow-[var(--elevation-floating)] forced-colors:border-[CanvasText] forced-colors:bg-[Canvas] forced-colors:text-[CanvasText] forced-colors:[forced-color-adjust:none]',
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

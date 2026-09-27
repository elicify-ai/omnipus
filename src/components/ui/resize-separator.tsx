// resize-separator.tsx — the interactive resize separator for the shared
// side-panel shell (side-panel-shell-spec.md US-2/US-9, SP-17). A focusable
// `role="separator"` with complete assistive state (US-9 AS-1): accessible
// name, aria-controls to the panel element, aria-valuenow/min/max, and
// `aria-valuetext` ("640 pixels wide"). Keyboard adjusts by a fixed step
// (16px, US-9 AS-2), Home/End to the bounds; pointer drag follows the
// pointer live (US-2 AS-1); a settled choice (drag release; keyboard 300ms
// after the last keypress, MIN-205) is committed via onCommit so the caller
// persists the width (SP-13) — a transient re-clamp never commits (MAJ-009).
// Double-click resets via onReset (US-2 AS-3).
//
// Published through the design system's four-part contract (catalog entry,
// barrel re-export, @source line, manifest) — this is a shared control, not
// a one-off (design-system skill rule 14 step 3).

import { useCallback, useEffect, useRef, useState } from 'react'
import type { KeyboardEvent as ReactKeyboardEvent, PointerEvent as ReactPointerEvent } from 'react'
import { cn } from '@/lib/utils'

/** What triggered a width change. `min`/`max` are the Home/End keys. */
export type ResizeSeparatorSource = 'drag' | 'keyboard' | 'min' | 'max'

export interface ResizeSeparatorProps {
  /** Accessible name — "Resize <Panel title> panel" (US-9 AS-1). */
  label: string
  /** Current width in px (controlled). */
  value: number
  /** Minimum width in px (US-2: 320px). */
  min: number
  /** Maximum width in px — the SP-17 ceiling (US-9 AS-1: updates on window resize, MIN-003). */
  max: number
  /** id of the element the separator controls (the panel column). */
  controls?: string
  /** Keyboard step in px (US-9: 16px per keypress). */
  step?: number
  /** Which side the resizable panel sits on. Default 'right'. */
  panelSide?: 'left' | 'right'
  /** Live width change (every pointermove / keypress). */
  onValueChange: (px: number, source: ResizeSeparatorSource) => void
  /** Settled width — drag release, or 300ms after the last keypress (MIN-205). */
  onCommit?: (px: number, source: ResizeSeparatorSource) => void
  /** Double-click reset (US-2 AS-3). */
  onReset?: () => void
  testId?: string
}

const KEYBOARD_SETTLE_MS = 300

export const ResizeSeparator = function ResizeSeparator({
  label,
  value,
  min,
  max,
  controls,
  step = 16,
  panelSide = 'right',
  onValueChange,
  onCommit,
  onReset,
  testId = 'resize-separator',
}: ResizeSeparatorProps) {
  const [dragging, setDragging] = useState(false)
  const dragState = useRef<{ pointerId: number; startX: number; startValue: number } | null>(null)
  const valueRef = useRef(value)
  valueRef.current = value
  const settleTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const latest = useRef<{ px: number; source: ResizeSeparatorSource } | null>(null)
  // A fresh value from the parent starts a new interaction — UNLESS the
  // fresh value is exactly what the control last emitted (the parent
  // confirming the emit; e.g. the shell previews keyboard moves through its
  // store, so each keypress re-renders with the emitted px). A genuinely
  // external change (panel switch, external re-clamp) drops `latest` so
  // keyboard stepping never continues from a stale width.
  const lastSeenValue = useRef(value)
  if (lastSeenValue.current !== value) {
    if (latest.current === null || latest.current.px !== value) {
      latest.current = null
    }
    lastSeenValue.current = value
  }

  const clearSettle = useCallback(() => {
    if (settleTimer.current !== null) {
      clearTimeout(settleTimer.current)
      settleTimer.current = null
    }
  }, [])

  // Keyboard settle: commit 300ms after the LAST keypress (MIN-205). Each
  // keypress reschedules; only real settling commits.
  const scheduleKeyboardCommit = useCallback(() => {
    clearSettle()
    settleTimer.current = setTimeout(() => {
      settleTimer.current = null
      const pending = latest.current
      if (pending) onCommit?.(pending.px, pending.source)
    }, KEYBOARD_SETTLE_MS)
  }, [clearSettle, onCommit])

  // Pointer drag: capture the pointer, follow it live, commit on release.
  const handlePointerDown = (e: ReactPointerEvent<HTMLDivElement>) => {
    if (e.button !== 0) return
    e.preventDefault()
    const el = e.currentTarget
    el.setPointerCapture(e.pointerId)
    dragState.current = { pointerId: e.pointerId, startX: e.clientX, startValue: valueRef.current }
    setDragging(true)
  }

  const handlePointerMove = (e: ReactPointerEvent<HTMLDivElement>) => {
    const drag = dragState.current
    if (!drag || drag.pointerId !== e.pointerId) return
    // Panel on the RIGHT: dragging LEFT widens it (delta = startX − clientX);
    // mirrored for a left-side panel.
    const raw = panelSide === 'right'
      ? drag.startValue + (drag.startX - e.clientX)
      : drag.startValue + (e.clientX - drag.startX)
    const clamped = Math.max(min, Math.min(raw, max))
    latest.current = { px: clamped, source: 'drag' }
    onValueChange(clamped, 'drag')
  }

  const handlePointerUp = (e: ReactPointerEvent<HTMLDivElement>) => {
    const drag = dragState.current
    if (!drag || drag.pointerId !== e.pointerId) return
    dragState.current = null
    try { e.currentTarget.releasePointerCapture(e.pointerId) } catch { /* already released */ }
    setDragging(false)
    const pending = latest.current
    if (pending) onCommit?.(pending.px, pending.source)
    latest.current = null
  }

  // Keyboard: 16px step with clamping; Home/End to the bounds (US-9 AS-2).
  // The step base is the last EMITTED width (keyboard continues a press
  // series even when the parent doesn't re-render between presses, and
  // continues a drag series after release); a fresh parent value resets
  // `latest` above, so a new interaction steps from the prop.
  const handleKeyDown = (e: ReactKeyboardEvent<HTMLDivElement>) => {
    const value = latest.current?.px ?? valueRef.current
    let next: number | null = null
    let source: ResizeSeparatorSource = 'keyboard'
    if (e.key === 'ArrowLeft') {
      // Panel on the right: ArrowLeft widens it (the border moves left).
      next = panelSide === 'right' ? value + step : value - step
    } else if (e.key === 'ArrowRight') {
      next = panelSide === 'right' ? value - step : value + step
    } else if (e.key === 'Home') {
      next = min
      source = 'min'
    } else if (e.key === 'End') {
      next = max
      source = 'max'
    } else {
      return
    }
    e.preventDefault()
    const clamped = Math.max(min, Math.min(next, max))
    latest.current = { px: clamped, source }
    onValueChange(clamped, source)
    scheduleKeyboardCommit()
  }

  useEffect(() => clearSettle, [clearSettle])

  return (
    <div
      role="separator"
      data-ds-action=""
      aria-orientation="vertical"
      aria-label={label}
      aria-controls={controls}
      aria-valuemin={min}
      aria-valuemax={max}
      aria-valuenow={Math.round(value)}
      aria-valuetext={`${Math.round(value)} pixels wide`}
      tabIndex={0}
      data-testid={testId}
      data-resizing={dragging ? 'true' : undefined}
      onPointerDown={handlePointerDown}
      onPointerMove={handlePointerMove}
      onPointerUp={handlePointerUp}
      onKeyDown={handleKeyDown}
      onDoubleClick={onReset}
      className={cn(
        // [data-ds-action] expands the effective hit region to the 24px
        // pointer minimum without widening the visible 2px rail (skill rule
        // 12 — adjacent narrow targets). The global :focus-visible ring owns
        // the focus cue (rule 7); the rail itself lights accent-colored.
        'group relative h-full w-2 shrink-0 cursor-col-resize touch-none select-none',
        'flex items-center justify-center',
      )}
    >
      <div
        aria-hidden
        data-testid={`${testId}-rail`}
        className={cn(
          'h-full w-px transition-colors',
          // Forced colors strips background fills to Canvas — the rail would
          // vanish, leaving the resize affordance invisible. A 1px border
          // keeps the line: forced colors maps borders to the system border
          // color (ButtonBorder) when the author sets none.
          'forced-colors:border-l',
          dragging
            ? 'bg-[var(--color-accent)]'
            : 'bg-[var(--color-border)] group-hover:bg-[var(--color-accent)] group-focus-visible:bg-[var(--color-accent)]',
        )}
      />
    </div>
  )
}

// useFocusRestore — restore focus to the trigger that opened a Radix dialog
//
// Wave 6 / B-fix: Wave A's CreateAgentModal (175-213) and Wave B's
// AgentProfile (147-204) each implemented a near-verbatim copy of this
// pattern: capture document.activeElement in `onOpenAutoFocus` BEFORE
// Radix shifts focus into the dialog, then restore on close via
// requestAnimationFrame + try/catch. The 30-line block was the #1 source
// of silent focus-restore regressions across both waves.
//
// Extract it into one hook so the third consumer (Wave C's ModelFooter
// slide-over per plan §3.2) adopts the proven pattern instead of forking
// it.
//
// Usage:
//   const { onOpenAutoFocus } = useFocusRestore(isOpen)
//   <SheetContent onOpenAutoFocus={onOpenAutoFocus} onCloseAutoFocus={...} />
//
// Contract:
//   - Capture happens in `onOpenAutoFocus`, BEFORE Radix shifts focus.
//   - Capture predicate: skip if `document.activeElement` is `<body>`
//     (the modal would have nothing meaningful to restore to).
//   - Restore happens in a `useEffect` on the open→close transition.
//   - Restore uses `requestAnimationFrame` to wait for Radix's own
//     teardown focus (Radix moves focus to `<body>` on unmount).
//   - Restore wraps `focus()` in try/catch to survive route-change races
//     where the trigger is detached from the DOM.

import { useEffect, useRef } from 'react'

export interface UseFocusRestoreReturn {
  /** Forward to Radix's `onOpenAutoFocus` (or `DialogPrimitive.Content`'s
   *  equivalent). Captures `document.activeElement` before Radix shifts
   *  focus into the dialog. */
  onOpenAutoFocus: (e: Event) => void
}

export function useFocusRestore(isOpen: boolean): UseFocusRestoreReturn {
  const triggerRef = useRef<HTMLElement | null>(null)
  const prevOpenRef = useRef(isOpen)

  const onOpenAutoFocus = (_e: Event) => {
    const active = document.activeElement
    if (active instanceof HTMLElement && active !== document.body) {
      triggerRef.current = active
    }
  }

  useEffect(() => {
    if (isOpen && !prevOpenRef.current) {
      // Fallback capture if onOpenAutoFocus didn't fire (e.g. programmatic
      // route-mount opens, where onOpenAutoFocus is bypassed).
      if (!triggerRef.current) {
        const active = document.activeElement
        if (active instanceof HTMLElement && active !== document.body) {
          triggerRef.current = active
        }
      }
    } else if (!isOpen && prevOpenRef.current) {
      const trigger = triggerRef.current
      triggerRef.current = null
      if (trigger && typeof trigger.focus === 'function' && document.contains(trigger)) {
        // Defer to next frame so Radix has finished its own teardown
        // focus (Radix moves focus to <body> on unmount). Try/catch
        // guards against detached-node `focus()` throwing in old
        // browsers / route-change races.
        requestAnimationFrame(() => {
          try {
            trigger.focus()
          } catch {
            // Silent — focus restore is best-effort UX, not a correctness
            // signal. Users can re-tab.
          }
        })
      }
    }
    prevOpenRef.current = isOpen
  }, [isOpen])

  return { onOpenAutoFocus }
}

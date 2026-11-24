import { useCallback, useRef } from 'react'
import { useSettingsShell } from './SettingsShellContext'

const FOCUSABLE =
  'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'

/**
 * FR-069 focus return for a Settings Sheet or Dialog that opens from state,
 * not from a Radix Trigger. Radix's modal close handler cancels the default
 * focus restore and focuses its trigger ref, which is empty without a
 * Trigger, so focus would fall to the page body. Spread the two handlers on
 * the Content element:
 * - on open, the control that has focus is remembered as the opener;
 * - on close, focus goes back to that opener; when it is gone, to the first
 *   focusable control of a dialog that is still open; when none is open, to
 *   the section heading.
 * `handOff()` is for a surface that closes by moving on to another one (the
 * provider picker opening the configure sheet): the closing surface leaves
 * focus to the next one and returns its opener, so the next surface can use
 * it as its own outermost opener.
 */
export function useReturnFocus() {
  const shell = useSettingsShell()
  const openerRef = useRef<HTMLElement | null>(null)
  const handedOff = useRef(false)

  const onOpenAutoFocus = useCallback(() => {
    const active = document.activeElement
    openerRef.current = active instanceof HTMLElement && active !== document.body ? active : null
    handedOff.current = false
  }, [])

  const onCloseAutoFocus = useCallback(
    (event: Event) => {
      const opener = openerRef.current
      openerRef.current = null
      // Stops Radix's own handler, which would focus an empty trigger ref.
      event.preventDefault()
      if (handedOff.current) {
        handedOff.current = false
        return
      }
      if (opener?.isConnected && !opener.closest('[inert]')) {
        opener.focus()
        return
      }
      const stillOpen = Array.from(
        document.querySelectorAll<HTMLElement>('[role="dialog"][data-state="open"], [role="alertdialog"][data-state="open"]'),
      ).pop()
      if (stillOpen) {
        stillOpen.querySelector<HTMLElement>(FOCUSABLE)?.focus()
        return
      }
      shell.focusHeading?.()
    },
    [shell],
  )

  const handOff = useCallback((): HTMLElement | null => {
    handedOff.current = true
    return openerRef.current
  }, [])

  return { onOpenAutoFocus, onCloseAutoFocus, handOff }
}

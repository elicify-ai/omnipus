import type { KeyboardEvent as ReactKeyboardEvent } from 'react'
import { getDiscardConfirmDialogOpen } from '@/components/library/preview/unsavedGuard'
import type { PanelId } from './types'

/** The docked shell and full-screen route share one Escape policy. */
export function shouldClosePanelOnEscape(
  panelId: PanelId | undefined,
  event: KeyboardEvent | ReactKeyboardEvent,
): boolean {
  const native = 'nativeEvent' in event ? event.nativeEvent : event
  if (!panelId || event.key !== 'Escape' || event.defaultPrevented || native.isComposing) return false
  if (getDiscardConfirmDialogOpen() || panelId === 'browser') return false
  // A dialog owns Escape even if focus has moved outside its content.
  if (document.querySelector(
    'dialog[open], [role="dialog"]:not([hidden]):not([aria-hidden="true"]):not([data-state="closed"]), ' +
    '[role="alertdialog"]:not([hidden]):not([aria-hidden="true"]):not([data-state="closed"])',
  )) return false
  const target = event.target
  if (!(target instanceof Element)) return true
  return target.closest('input, textarea, select, [contenteditable]:not([contenteditable="false"])') === null
}

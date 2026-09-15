// returnFocusToCreateMenu — where keyboard focus goes when a dialog opened
// from the Library's Create menu closes (UAT D-104, 2026-09-13).
//
// Radix restores focus to whatever was focused when the dialog OPENED. For
// New folder / New knowledge base that element is the menu ITEM the user
// activated — which the menu unmounts in the same tick — so focus fell to
// <body> and a keyboard user who had just created a folder was dumped at the
// top of the document, Tabbing all the way back. The Create menu's own
// trigger is the one stable element in that chain, and it is where the menu
// itself returns focus, so the dialog does the same.

export const CREATE_MENU_TRIGGER_SELECTOR = '[data-testid="library-create-menu-trigger"]'

/** Radix `onCloseAutoFocus` handler: focus the Create menu trigger when it
 *  exists, else leave Radix's default behaviour alone. */
export function returnFocusToCreateMenu(event: Event): void {
  const trigger = document.querySelector<HTMLElement>(CREATE_MENU_TRIGGER_SELECTOR)
  if (!trigger) return
  event.preventDefault()
  trigger.focus()
}

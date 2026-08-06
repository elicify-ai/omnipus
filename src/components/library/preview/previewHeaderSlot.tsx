// previewHeaderSlot — lets a nested preview body contribute controls to the
// Library preview's ONE header row (operator direction, 2026-08-04: three
// header bars reduced to a single small row).
//
// Why a portal rather than lifting state: whether the open file is editable at
// all is not known when the header renders. LibraryPreviewPane fires off the
// content query, and the answer arrives several branches deep — a file can turn
// out to be too large, or binary, and fall back to a download card with no
// view/edit/save at all. Hoisting the editor state into the pane would mean
// hoisting that whole decision with it, and running the editor hook against
// content that may never arrive.
//
// So the pane owns the row and publishes an empty element as a slot; whichever
// body actually mounts an editor portals its controls into it. Bodies that have
// no controls portal nothing, and the row degrades to title + close on its own.
// The slot is held in STATE (callback ref) rather than a plain ref so that
// consumers re-render once the element exists — a ref alone would be null on
// the first pass and the controls would never appear.

import { createContext, useContext } from 'react'
import { createPortal } from 'react-dom'
import type { ReactNode } from 'react'

const PreviewHeaderSlotContext = createContext<HTMLElement | null>(null)

export function PreviewHeaderSlotProvider({
  slot,
  children,
}: {
  slot: HTMLElement | null
  children: ReactNode
}) {
  return <PreviewHeaderSlotContext.Provider value={slot}>{children}</PreviewHeaderSlotContext.Provider>
}

/**
 * Renders `children` into the preview header row. A no-op (renders nothing)
 * when there is no slot — which is the correct behaviour for any host that
 * reuses these preview components outside the pane, rather than a crash or a
 * second stray toolbar.
 */
export function PreviewHeaderPortal({ children }: { children: ReactNode }) {
  const slot = useContext(PreviewHeaderSlotContext)
  if (!slot) return null
  return createPortal(children, slot)
}

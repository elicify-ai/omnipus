// openLibraryAt.ts — the one way chat surfaces open the docked Library on a
// file: a chat link click, the sent-file card's "Open in Library", the
// "Open in Library" notice, and an auto-open from an `open_in_library` frame
// (chat-links spec FR-008, R6).
//
// Every path runs the outgoing panel's leave gate first (`leaveGateThen`), so
// unsaved Library or Mail edits are never discarded without the user's
// Discard choice. While a discard decision is already pending, a further open
// is DROPPED rather than stacked behind it: two dialogs, or a second target
// silently winning after the first Discard, are both worse than a click the
// user can repeat.
//
// The context is a workspace panel context WITHOUT `sessionId`: the Library
// panel ignores any context that carries one (`LibraryPanel.tsx::asWorkspaceContext`).

import { leaveGateThen } from '@/components/panel-shell/leaveGate'
import { getPanelDefinition } from '@/components/panel-shell/registry'
import { getDiscardConfirmDialogOpen } from '@/components/library/preview/unsavedGuard'
import { getMailDiscardConfirmDialogOpen } from '@/components/workspaces/mail/mailUnsavedGuard'
import { useUiStore } from '@/store/ui'

/** A Library location: workspace id plus a non-empty workspace-relative path. */
export interface LibraryOpenTarget { // not-wire-format: SPA-internal open request, built from generated frame/ref fields
  workspaceId: string
  path: string
}

/** True while a panel leave decision (a discard dialog) is still unanswered. */
export function isLeaveDecisionPending(): boolean {
  return useUiStore.getState().guardPending || getDiscardConfirmDialogOpen() || getMailDiscardConfirmDialogOpen()
}

/**
 * True when replacing the open panel would ask the user first — the same
 * condition under which `leaveGateThen` shows its dialog instead of moving.
 * Today that is a Library or Mail panel holding unsaved edits.
 */
export function outgoingPanelNeedsLeaveConfirmation(): boolean {
  const outgoing = useUiStore.getState().activePanel?.id ?? null
  if (outgoing === null) return false
  const definition = getPanelDefinition(outgoing)
  if (definition?.beforeLeave === undefined) return false
  return definition.beforeLeaveRequired?.() !== false
}

/** Open the docked Library at `target`, through the leave gate. No-op while a leave decision is pending. */
export function openLibraryAt(target: LibraryOpenTarget): void {
  if (isLeaveDecisionPending()) return
  const outgoing = useUiStore.getState().activePanel?.id ?? null
  leaveGateThen(outgoing, () => {
    useUiStore.getState().openPanel('library', { workspaceId: target.workspaceId, path: target.path })
  })
}

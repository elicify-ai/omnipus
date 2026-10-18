/**
 * The sidebar agent row's "⋯" menu (DECISIONS.md D1–D8, ADR-hide-from-sidebar).
 *
 * ONE catalogued DropdownMenu replaces the row's two icon buttons. Items in
 * order: History · Hide from sidebar · Edit · Remove. Keyboard and focus
 * (Arrow/Escape/typeahead) come from the Radix primitive under
 * `src/components/ui/dropdown-menu.tsx` — nothing is hand-built here.
 *
 * D8: Remove carries its refusal reason as visible text on the disabled item
 * (disabled Radix items take no pointer events, so a hover tooltip could
 * never show it). Hide is omitted from Admin's row entirely: Admin is not a
 * member, so no member entry exists to carry the flag (ADR §Rules Q3).
 */
import { useState } from 'react'
import { DotsThree } from '@phosphor-icons/react'
import type { Agent, Workspace } from '@/lib/api'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { IconButton } from '@/components/ui/icon-button'
import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { useUiStore } from '@/store/ui'
import { runWorkspaceEntry } from '@/store/session/workspaceEntryFlow'
import {
  invalidateWorkspaceRoster,
  removeDisabledReason,
  removeWorkspaceMember,
  setHiddenFromSidebar,
  surfaceActionError,
} from './agentRowActions'

export function AgentRowMenu({
  workspace,
  agent,
  agentId,
  agentName,
}: {
  workspace: Workspace
  agent: Agent | undefined
  agentId: string
  agentName: string
}) {
  const [confirmOpen, setConfirmOpen] = useState(false)
  const removeReason = removeDisabledReason(agent, workspace)

  const openHistory = () => {
    // D3: the same past-chats view the old "Past sessions with X" button opened.
    useUiStore.getState().openSearchModal(workspace.id, agentId)
  }

  const openEditor = () => {
    // D6: the Team-tab entry point — the same store-driven AgentProfile
    // slide-over, no second editor, opened with this workspace's context.
    useUiStore.getState().openEditAgentSlideOver(agentId, workspace.id)
  }

  const hide = () => {
    void setHiddenFromSidebar(workspace, [agentId], true)
      .then(invalidateWorkspaceRoster)
      .catch((err: unknown) => surfaceActionError(`Could not hide ${agentName}`, err))
  }

  const confirmRemove = () => {
    setConfirmOpen(false)
    void (async () => {
      await removeWorkspaceMember(workspace, agentId)
      invalidateWorkspaceRoster()
      // Invalidate the removed member's remembered/active binding (SFH F3).
      // workspaceEntryFlow.execute returns immediately when the workspace's
      // remembered descriptor id still equals activeSessionId — so before any
      // recovery, the pointer to the removed member's chat is cleared through
      // the existing selection owner (set to null, "explicitly fresh" — the
      // retained main session is NOT deleted, and chats of OTHER agents are
      // untouched). When that chat is also the OPEN one in this workspace,
      // entry recovery is awaited so it is never detached from the action.
      const state = useSessionStore.getState()
      const remembered = state.sessionByWorkspace[workspace.id]
      const remembersRemoved = remembered?.agentId === agentId
      const ownsSelectedChat =
        state.activeAgentId === agentId
        && useWorkspacesStore.getState().activeWorkspaceId === workspace.id
      if (remembersRemoved || ownsSelectedChat) {
        state.setWorkspaceSessionDescriptor(workspace.id, null)
      }
      if (ownsSelectedChat) {
        try {
          await runWorkspaceEntry(workspace.id)
        } catch (err: unknown) {
          // S4-02: the removal HAS succeeded here — a failed follow-up
          // recovery reports the true state (removed; reopening the chat
          // failed), never "Could not remove".
          surfaceActionError(`Removed ${agentName}, but could not refresh the chat view`, err)
        }
      }
    })().catch((err: unknown) => surfaceActionError(`Could not remove ${agentName}`, err))
  }

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <IconButton
          size="sm"
          aria-label={`More actions for ${agentName}`}
          className="h-[var(--icon-size-feature)] w-[var(--icon-size-feature)] shrink-0 font-[var(--font-weight-regular)] text-[length:var(--type-caption-size)] text-[var(--color-muted)] hover:text-[var(--color-secondary)] pointer-coarse:min-h-[var(--target-touch-minimum)] pointer-coarse:min-w-[var(--target-touch-minimum)]"
        >
          <DotsThree size={14} weight="bold" aria-hidden="true" />
        </IconButton>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start">
        <DropdownMenuItem onSelect={openHistory}>History</DropdownMenuItem>
        <DropdownMenuItem onSelect={hide}>Hide from sidebar</DropdownMenuItem>
        <DropdownMenuItem onSelect={openEditor}>Edit</DropdownMenuItem>
        {removeReason === null ? (
          <DropdownMenuItem onSelect={() => setConfirmOpen(true)}>Remove</DropdownMenuItem>
        ) : (
          <DropdownMenuItem
            disabled
            // D8: the refusal must never be a silent no-op. The reason rides
            // three surfaces: this title (what the e2e reason-reader and
            // assistive tech see), the described-by link below, and the
            // visible caption inside the item. The caption is aria-hidden so
            // the item's accessible NAME stays exactly "Remove" — the reason
            // is the item's DESCRIPTION (aria-describedby), not its name.
            title={removeReason}
            aria-describedby={`agent-row-remove-reason-${agentId}`}
          >
            <span className="flex min-w-0 flex-col">
              <span>Remove</span>
              <span
                id={`agent-row-remove-reason-${agentId}`}
                aria-hidden="true"
                className="whitespace-normal text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]"
              >
                {removeReason}
              </span>
            </span>
          </DropdownMenuItem>
        )}
      </DropdownMenuContent>
      <ConfirmDialog
        open={confirmOpen}
        onOpenChange={setConfirmOpen}
        title={`Remove ${agentName} from ${workspace.name}?`}
        description={`${agentName} leaves only this workspace's team; the agent still exists, stays in its other workspaces and keeps its chat history.`}
        confirmLabel="Remove"
        destructive
        onConfirm={confirmRemove}
      />
    </DropdownMenu>
  )
}

// #1428 step 2 (spec C r9 4.9, DEFECT 2): "Open" on an Unsent item of a saved
// chat lands on that chat's OWN workspace and session. Opening only the
// session left the workspace (address, header, Team panel) on whatever
// workspace was showing, and the route's `open` sync then wrote the other
// workspace's chat id into this workspace's address. Same tuple as a sidebar
// or search selection (useSelectSession.ts): workspace, session, then the
// workspace chat route naming the chat in `open` (#1437).

import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { useUiStore } from '@/store/ui'
import type { Session } from '@/lib/api'
import { supersedeNavigationIntent } from '@/store/session/workspaceEntryFlow'
import { appNavigate } from '@/lib/appNavigate'

/** The saved chat to open: its id, workspace and, when the list knows it, its type and title. */
export interface SavedChatTarget {
  sessionId: string
  workspaceId: string | null
  type?: Session['type']
  title?: string
}

/**
 * Attaches first, then navigates (useSelectSession's order): the chat route's
 * entry skips a session that is already active, so only this attach loads the
 * transcript. A failed attach switches nothing and is reported.
 */
export function openSavedChat(target: SavedChatTarget, agentId: string | undefined): void {
  const { sessionId, workspaceId } = target
  const workspaces = useWorkspacesStore.getState()
  const previousWorkspaceId = workspaces.activeWorkspaceId
  if (workspaceId && workspaceId !== previousWorkspaceId) workspaces.setActiveWorkspaceId(workspaceId)
  if (!useSessionStore.getState().attachToSession(sessionId, target.type ?? 'chat', target.title, agentId)) {
    if (workspaceId && workspaceId !== previousWorkspaceId) workspaces.setActiveWorkspaceId(previousWorkspaceId)
    useUiStore.getState().addToast({ message: "Connection lost — couldn't open that session. It will not be switched.", variant: 'error' })
    return
  }
  if (!workspaceId) return
  supersedeNavigationIntent()
  // Unregistered (no workspace chat route mounted): the stores above already switched.
  appNavigate({ workspaceId, open: sessionId })
}

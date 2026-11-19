// #1428 step 2 (spec C r9 4.9, DEFECT 2): "Open" on an Unsent item of a saved
// chat lands on that chat's OWN workspace and session. Opening only the
// session left the workspace (address, header, Team panel) on whatever
// workspace was showing, and the route's `open` sync then wrote the other
// workspace's chat id into this workspace's address. Same tuple as a sidebar
// or search selection (useSelectSession.ts): workspace, session, then the
// workspace chat route naming the chat in `open` (#1437).

import type { useNavigate } from '@tanstack/react-router'
import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { supersedeNavigationIntent } from '@/store/session/workspaceEntryFlow'

export type OpenChatNavigate = ReturnType<typeof useNavigate>

/** Opens saved chat `sessionId`; with a known `workspaceId`, in that workspace. */
export function openSavedChat(sessionId: string, workspaceId: string | null, agentId: string | undefined, navigate: OpenChatNavigate): void {
  const workspaces = useWorkspacesStore.getState()
  if (workspaceId && workspaceId !== workspaces.activeWorkspaceId) workspaces.setActiveWorkspaceId(workspaceId)
  useSessionStore.getState().setActiveSession(sessionId, agentId)
  if (!workspaceId) return
  supersedeNavigationIntent()
  void navigate({ to: '/workspaces/$workspaceId/chat', params: { workspaceId }, search: { open: sessionId } })
}

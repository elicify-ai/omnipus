// useWorkspaceSetupKickoff — Unit C: fires Ava's (or whichever agent leads
// the workspace's core_team) workspace-setup interview exactly once, the
// first time a freshly-created workspace (server-seeded `setup_pending:
// true` — see Wave 1, POST /api/v1/workspaces) is opened. See
// useChatStore.sendWorkspaceSetupKickoff for the wire mechanics (kickoff
// content, `metadata.workspace_setup_kickoff`, new-turn placeholder).
//
// Three independent layers guard against a duplicate kickoff:
//   1. This hook's own per-workspace-id useRef guard (below) — the primary
//      defense against re-firing within one component lifetime (repeated
//      renders, dependency churn, etc.).
//   2. The optimistic queryClient.setQueryData cache clear (below) — so a
//      remount that re-reads the workspace lists/detail before the server's
//      own state has caught up still sees `setup_pending: false`.
//   3. Server-side rejection of a duplicate kickoff frame — the final,
//      authoritative backstop (the backend clears `setup_pending` on first
//      accepted kickoff and errors a repeat).
//
// Wired into WorkspaceTabContainer (called unconditionally, once the
// workspace is resolved) so it fires regardless of which tab is active —
// frames are handled at the chat-store level, so Ava's greeting streams
// into the chat bucket even while the user is looking at the Board tab.

import { useEffect, useRef } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import type { Workspace } from '@/lib/api'
import { workspacesQueryKeys } from '@/lib/api'
import { useChatAgents } from '@/hooks/useChatAgents'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { useChatStore } from '@/store/chat'

export function useWorkspaceSetupKickoff(workspace: Workspace | undefined): void {
  const queryClient = useQueryClient()
  const isConnected = useConnectionStore((s) => s.isConnected)
  const activeSessionId = useSessionStore((s) => s.activeSessionId)
  const { chatAgents } = useChatAgents()

  // Keyed by workspace id: fires at most once per workspace per mount of
  // the containing component. Reset is implicit — a DIFFERENT workspace id
  // simply doesn't match, so navigating to a new workspace can fire again;
  // revisiting the SAME workspace after a successful fire is blocked by
  // `workspace.setup_pending` already being false (cache or server), not by
  // this ref alone.
  const firedForWorkspaceId = useRef<string | null>(null)

  useEffect(() => {
    if (!workspace) return
    if (!workspace.setup_pending) return
    if (!isConnected) return
    if (activeSessionId !== null) return
    if (firedForWorkspaceId.current === workspace.id) return

    // Resolve the workspace's lead agent — its core_team's first member, or
    // 'ava' when core_team is unset (matches the Wave 1 seeding default).
    // Only fire once that agent is actually ready to chat (present, active/
    // idle, non-worker) — resolved via the same shared query AgentPicker and
    // the "@" mention menu use, so this doesn't issue a second network call.
    const targetAgentId = workspace.core_team?.[0] ?? 'ava'
    const agent = chatAgents.find((a) => a.id === targetAgentId)
    if (!agent) return

    // Claim the guard BEFORE sending — a synchronous re-render triggered by
    // the send itself (setActiveSession, bucket writes) must not re-enter
    // this effect body and fire a second frame.
    firedForWorkspaceId.current = workspace.id

    const sent = useChatStore.getState().sendWorkspaceSetupKickoff({
      workspaceId: workspace.id,
      workspaceName: workspace.name,
      agentId: agent.id,
      agentType: agent.type ?? null,
    })

    if (!sent) {
      // The store bailed (offline / mid-stream / a session raced in between
      // the guard reads above and the store's own re-check) — release the
      // guard so the next render (e.g. once isConnected flips true) retries.
      firedForWorkspaceId.current = null
      return
    }

    // Optimistically clear the flag on every cache a consumer might read it
    // from, mirroring the established pattern in WorkspaceTeamTab.tsx (the
    // no-op `invalidateQueries({queryKey: workspacesQueryKeys.list()})`
    // pitfall documented there applies here too — write the exact
    // {status:'active'}/{status:'archived'} keys directly rather than
    // relying on partial-match invalidation). This prevents a remount from
    // re-reading `setup_pending: true` before the server's own state (which
    // the backend clears on accepting this kickoff) is refetched.
    const clearFlag = (list: Workspace[] | undefined) =>
      list?.map((w) => (w.id === workspace.id ? { ...w, setup_pending: false } : w))
    for (const status of ['active', 'archived'] as const) {
      queryClient.setQueryData<Workspace[]>(workspacesQueryKeys.list({ status }), clearFlag)
    }
    queryClient.setQueryData<Workspace>(workspacesQueryKeys.detail(workspace.id), (w) =>
      w ? { ...w, setup_pending: false } : w,
    )
  }, [workspace, isConnected, activeSessionId, chatAgents, queryClient])
}

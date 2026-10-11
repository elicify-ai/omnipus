import { useEffect, useState } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { fetchAgents, fetchSessions, fetchWorkspaces, workspacesQueryKeys } from '@/lib/api'
import { adminMainSessionIdOfWorkspace } from '@/lib/nav/sessionCoreSeam'
import { useSelectSession } from '@/components/chat/useSelectSession'
import { useSessionStore } from '@/store/session'
import { Button } from '@/components/ui/button'

// /admin/chat — REDIRECT STUB (repo convention for retired screens, like
// /tasks and /automations). The sidebar's standalone "Admin chat" entry was
// removed (founder request 2026-10-11): Admin has ONE main chat, in the
// default workspace (ADR-20261007), reached from that workspace's agent rows
// like any agent. Old links land there too — the route resolves Admin's main
// through the same seam the sidebar roster uses
// (sessionCoreSeam.adminMainSessionIdOfWorkspace) and enters it via the SAME
// entry path the sidebar row uses (useSelectSession: attach → workspace
// switch → the workspace chat route). It NEVER mints a standalone Admin
// session (the old screen called createSession('admin') on every visit,
// outside any workspace) and never fakes a membership. Until resolution is
// known it shows the neutral "Restoring your conversation…" state (C7) —
// the old code flashed "Main chat unavailable" + Retry during the
// mid-navigation window. When Admin's main cannot be resolved (or the
// attach is refused), it shows the sidebar's unavailable state with Retry —
// never a blank page.
const ADMIN_AGENT_ID = 'admin'

function AdminChatRedirect() {
  // The same three shared queries the sidebar observes, under the same keys —
  // this stub reads whatever the sidebar already has cached instead of
  // refetching the world (same approach as DefaultWorkspaceRedirect).
  const { data: workspaces = [], isPending: workspacesPending, refetch: refetchWorkspaces } = useQuery({
    queryKey: workspacesQueryKeys.list({ status: 'active' }),
    queryFn: () => fetchWorkspaces({ status: 'active' }),
    staleTime: 30_000,
  })
  const { data: agents = [], isPending: agentsPending, refetch: refetchAgents } = useQuery({
    queryKey: ['agents'],
    queryFn: fetchAgents,
    staleTime: 30_000,
  })
  const { data: sessions = [], isPending: sessionsPending, refetch: refetchSessions } = useQuery({
    queryKey: ['sessions'],
    queryFn: () => fetchSessions(),
    staleTime: 15_000,
  })

  // The roster rule, verbatim from sidebar/workspaceRoster.adminDefault:
  // Admin rides only the DEFAULT workspace, the row only exists once the
  // Admin agent itself is loaded, its main id comes only from
  // Workspace.admin_main_session_id, and it only counts once the session
  // list actually carries that id. Anything else is unresolved.
  const defaultWorkspace = workspaces.find((candidate) => candidate.is_default)
  const adminLoaded = agents.some((candidate) => candidate.id === ADMIN_AGENT_ID)
  const mainSessionId = defaultWorkspace && adminLoaded
    ? adminMainSessionIdOfWorkspace(defaultWorkspace)
    : undefined
  const session = mainSessionId
    ? sessions.find((candidate) => candidate.id === mainSessionId)
    : undefined

  // The sidebar row's exact entry path (AgentMainRow.openMain → selectSession):
  // attach, switch the active workspace, navigate to the workspace chat route.
  // replace: true — this is a redirect stub (team-lead ruling 2026-10-11):
  // a pushed /admin/chat would trap the back button, every Back bouncing off
  // the stub's redirect again. No overlay is open on a deep-link arrival, so
  // there is nothing to close.
  const selectSession = useSelectSession({ agents, workspaces, onClose: () => {}, replace: true })
  const [attemptedId, setAttemptedId] = useState<string | null>(null)
  // The entry path's own outcome signal: attachToSession sets the store's
  // activeSessionId to the attached session when the attach is sent, and
  // leaves it alone when it refuses.
  const activeSessionId = useSessionStore((s) => s.activeSessionId)

  useEffect(() => {
    if (!session || attemptedId === session.id) return
    setAttemptedId(session.id)
    selectSession(session)
  }, [session, attemptedId, selectSession])

  const pending = workspacesPending || agentsPending || sessionsPending
  // C7 (R3 delta re-review): after the attempt the stub is either MID-REDIRECT
  // (the attach went out and the navigation is in flight) or genuinely stuck
  // (the attach refused; its toast already said so). Reading the store's
  // activeSessionId keeps the mid-navigation window on the neutral resolving
  // state below — the old `attemptedId === session.id` test alone flashed
  // "Main chat unavailable" + Retry at a redirect that was succeeding.
  const attemptMade = !!session && attemptedId === session.id
  const attachProceeded = attemptMade && activeSessionId === session.id
  // Truly unavailable: nothing to enter (the queries resolved without
  // Admin's main), or the entry path refused the attach and the stub is
  // still mounted — offer Retry instead of spinning forever.
  const unavailable = (!session && !pending) || (attemptMade && !attachProceeded)

  const retry = () => {
    setAttemptedId(null)
    void refetchWorkspaces()
    void refetchAgents()
    void refetchSessions()
  }

  if (unavailable) {
    // The sidebar's own unavailable state (WorkspaceAgentList's
    // UnavailableNote wording), never a blank page.
    return (
      <div className="flex h-full items-center justify-center gap-[var(--space-2)] px-[var(--space-3)]">
        <span
          role="status"
          aria-label="Main chat unavailable"
          className="text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]"
        >
          Main chat unavailable
        </span>
        <Button
          variant="ghost"
          onClick={retry}
          className="h-auto shrink-0 px-[var(--space-2)] py-[var(--space-1)] font-[var(--font-weight-regular)] text-[length:var(--type-caption-size)]"
        >
          Retry
        </Button>
      </div>
    )
  }

  // Resolving: queries in flight, or the entry attempt mid-redirect. Same
  // presentation as WorkspaceChatTab's ChatRestoreSkeleton — the shared
  // wording, the shared spinner shape — because it is the same moment (a
  // conversation restore in flight); a fourth catalogued loading primitive
  // for one spinner + caption would not generalize (design-system rule 14,
  // local-copy reason).
  return (
    <div
      className="flex h-full flex-col items-center justify-center gap-[var(--space-2-5)]"
      aria-live="polite"
      data-testid="admin-chat-restoring"
    >
      <div
        className="h-8 w-8 rounded-full border-2 border-[var(--color-accent)] border-t-transparent animate-spin"
        aria-hidden="true"
      />
      <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">Restoring your conversation…</p>
    </div>
  )
}

export const Route = createFileRoute('/_app/admin/chat')({
  component: AdminChatRedirect,
})

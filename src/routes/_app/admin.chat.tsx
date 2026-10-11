import { useEffect, useState } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { fetchAgents, fetchSessions, fetchWorkspaces, workspacesQueryKeys } from '@/lib/api'
import { adminMainSessionIdOfWorkspace } from '@/lib/nav/sessionCoreSeam'
import { useSelectSession } from '@/components/chat/useSelectSession'
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
// outside any workspace) and never fakes a membership. When Admin's main
// cannot be resolved, it shows the sidebar's unavailable state with Retry —
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
  // No overlay is open on a deep-link arrival, so there is nothing to close.
  const selectSession = useSelectSession({ agents, workspaces, onClose: () => {} })
  const [attemptedId, setAttemptedId] = useState<string | null>(null)

  useEffect(() => {
    if (!session || attemptedId === session.id) return
    setAttemptedId(session.id)
    selectSession(session)
  }, [session, attemptedId, selectSession])

  const pending = workspacesPending || agentsPending || sessionsPending
  // Still mounted after the attempt ⇒ the attach/navigation did not happen
  // (its toast already said so) — offer Retry instead of spinning forever.
  const attemptedButStuck = !!session && attemptedId === session.id
  const unavailable = attemptedButStuck || (!session && !pending)

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

  return (
    <div className="flex h-full items-center justify-center" aria-live="polite">
      <div
        className="w-6 h-6 rounded-full border-2 border-[var(--color-accent)] border-t-transparent animate-spin"
        aria-hidden="true"
      />
    </div>
  )
}

export const Route = createFileRoute('/_app/admin/chat')({
  component: AdminChatRedirect,
})

// CrossWorkspaceApprovalBanner — "waiting in another chat" (L1 amendment §1.2
// R6, BDD-11.3, FR-092; generalised from the workspace-only notice of the
// founder decision of 2026-09-14). Name and testid kept.
//
// The inline approval card (ToolApprovalPanel) renders only in the approval's
// root chat. Any approval rooted elsewhere — another workspace, another chat in
// the same workspace, a chat in no workspace, Omnipus's chat — is announced
// here, mounted once in AppShell above the Outlet so it shows on every screen.
// It never blocks, never takes focus and never resolves anything: it is a
// role="status" region whose one action opens the root chat, where the card is.
// An Omnipus root opens /omnipus through the Omnipus attach path (the active
// workspace stays); any other root opens through the ordinary session
// selection (its workspace chat, or /sessions/$id for a chat in no workspace).
// It disappears when those approvals resolve (tool_approval_resolved →
// markResolved).

import { useMemo } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { Shield } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { approvalChatId, useToolApprovalStore, type PendingToolApproval } from '@/store/toolApproval'
import { useSessionStore } from '@/store/session'
import { fetchAgents, fetchSessions, fetchWorkspaces, workspacesQueryKeys } from '@/lib/api'
import type { Agent, Session, Workspace } from '@/lib/api'
import { OMNIPUS_AGENT_ID } from '@/lib/omnipus'
import { summarizeApprovalsElsewhere } from '@/lib/crossWorkspaceApprovals'
import { useSelectSession } from '@/components/chat/useSelectSession'

type ApprovalTarget = Pick<PendingToolApproval, 'rootSessionId' | 'sessionId' | 'workspaceId' | 'agentId'> // not-wire-format: local alias

export function CrossWorkspaceApprovalBanner() {
  const navigate = useNavigate()
  const queue = useToolApprovalStore((s) => s.queue)
  const activeSessionId = useSessionStore((s) => s.activeSessionId)

  // status: 'all' — a held-open approval's workspace can be archived and
  // still have a real name.
  const { data: workspaces = [] } = useQuery({
    queryKey: workspacesQueryKeys.list({ status: 'all' }),
    queryFn: () => fetchWorkspaces({ status: 'all' }),
    staleTime: 30_000,
  })
  const { data: sessions = [] } = useQuery({ queryKey: ['sessions'], queryFn: () => fetchSessions(), staleTime: 15_000 })
  const { data: agents = [] } = useQuery({ queryKey: ['agents'], queryFn: fetchAgents, staleTime: 30_000 })
  const selectSession = useSelectSession({ agents, workspaces: workspaces as Workspace[], onClose: () => {} })

  const resolveRoot = useMemo(() => rootResolver(sessions, workspaces, agents), [sessions, workspaces, agents])

  const summary = useMemo(
    () => summarizeApprovalsElsewhere(queue, activeSessionId, (approval) => resolveRoot(approval).label),
    [queue, activeSessionId, resolveRoot],
  )

  const goThere = () => {
    if (!summary) return
    const root = resolveRoot(summary.target)
    if (root.isOmnipus) {
      // The Omnipus chat opens without switching the active workspace.
      const attached = useSessionStore.getState().attachToSession(
        summary.targetChatId, root.session?.type ?? 'main', root.session?.title ?? undefined, OMNIPUS_AGENT_ID,
        { recordForWorkspace: false },
      )
      if (attached) void navigate({ to: '/omnipus' })
      return
    }
    selectSession(root.session ?? ({
      id: summary.targetChatId,
      agent_id: summary.target.agentId,
      ...(summary.target.workspaceId ? { workspace_id: summary.target.workspaceId } : {}),
      type: 'main',
    } as Session))
  }

  // The wrapper stays mounted (empty) so aria-live announces the notice the
  // instant it appears.
  return (
    <div role="status" aria-live="polite" className="shrink-0">
      {summary && (
        <Button
          type="button"
          variant="ghost"
          data-testid="cross-workspace-approval-banner"
          onClick={goThere}
          className="h-auto w-full items-center justify-start gap-[var(--space-2)] rounded-none px-[var(--space-3)] py-[var(--space-2)] bg-[var(--color-accent)]/10 border-b border-[var(--color-accent)]/30 text-[length:var(--type-utility-xs-size)] font-medium text-[var(--color-accent)] hover:bg-[var(--color-accent)]/15 hover:text-[var(--color-accent)] text-left"
        >
          <Shield size={14} weight="bold" className="shrink-0" aria-hidden="true" />
          <span className="flex-1 truncate">{summary.label}</span>
          <span className="shrink-0 underline underline-offset-2">Go there</span>
        </Button>
      )}
    </div>
  )
}

/** Names an approval's root chat from the cached lists (never a raw id when a name is known). */
function rootResolver(sessions: Session[], workspaces: Workspace[], agents: Agent[]) {
  return (approval: ApprovalTarget) => {
    const chatId = approvalChatId(approval)
    const session = sessions.find((candidate) => candidate.id === chatId)
    const agentId = session?.agent_id ?? (approval.rootSessionId ? undefined : approval.agentId)
    const isOmnipus = agentId === OMNIPUS_AGENT_ID
    if (isOmnipus) return { session, isOmnipus, label: 'Omnipus' }
    const workspaceId = session?.workspace_id ?? approval.workspaceId
    const agentName = agents.find((agent) => agent.id === agentId)?.name
    const workspaceName = workspaceId ? workspaces.find((w) => w.id === workspaceId)?.name : undefined
    const label = agentName && workspaceName
      ? `${agentName}'s chat in ${workspaceName}`
      : agentName
        ? `${agentName}'s chat`
        : workspaceName
          ? `a chat in ${workspaceName}`
          : 'another chat'
    return { session, isOmnipus, label }
  }
}

// CrossWorkspaceApprovalBanner — founder decision 2026-09-14.
//
// commit 0c79af19 scoped ToolApprovalModal to the active workspace: the
// tool_approval_required WS frame and reconnect-snapshot pending entries
// carry an optional workspace_id, and useToolApprovalStore keeps every
// pending approval (every tab receives every frame) while the modal only
// shows the ones that match the workspace currently open
// (isApprovalInScope, src/store/toolApproval.ts). Consequence: an approval
// waiting in a workspace the user isn't looking at is invisible until they
// happen to switch tabs, or the server's 10-minute timeout denies it on
// their behalf.
//
// This banner is a small, persistent, ambient notice — mounted once in
// AppShell above the Outlet, so it renders on every screen regardless of
// which workspace tab is open. It never blocks or steals focus: it's a
// role="status" region, not a dialog.
//
// Clicking it switches the active workspace and navigates to that
// workspace's chat tab, where ToolApprovalModal picks the approval straight
// back up. It disappears the moment those approvals resolve — the
// tool_approval_resolved WS frame already drives markResolved, which drops
// the entry from the shared queue this banner reads.

import { useMemo } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { Shield } from '@phosphor-icons/react'
import { useToolApprovalStore } from '@/store/toolApproval'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { fetchWorkspaces, workspacesQueryKeys } from '@/lib/api'
import { summarizeCrossWorkspaceApprovals } from '@/lib/crossWorkspaceApprovals'

export function CrossWorkspaceApprovalBanner() {
  const navigate = useNavigate()
  const queue = useToolApprovalStore((s) => s.queue)
  const activeWorkspaceId = useWorkspacesStore((s) => s.activeWorkspaceId)
  const setActiveWorkspaceId = useWorkspacesStore((s) => s.setActiveWorkspaceId)

  // status: 'all' — a held-open approval's workspace can be archived and
  // still have a real name; an 'active'-only list would fall back to the
  // raw id for it even though the name is available.
  const { data: workspaces } = useQuery({
    queryKey: workspacesQueryKeys.list({ status: 'all' }),
    queryFn: () => fetchWorkspaces({ status: 'all' }),
    staleTime: 30_000,
  })

  const workspaceName = useMemo(() => {
    const byId = new Map((workspaces ?? []).map((w) => [w.id, w.name]))
    return (workspaceId: string) => byId.get(workspaceId) ?? workspaceId
  }, [workspaces])

  const summary = useMemo(
    () => summarizeCrossWorkspaceApprovals(queue, activeWorkspaceId, workspaceName),
    [queue, activeWorkspaceId, workspaceName],
  )

  const goToWorkspace = (workspaceId: string) => {
    setActiveWorkspaceId(workspaceId)
    navigate({ to: '/workspaces/$workspaceId/chat', params: { workspaceId } })
  }

  // The wrapper stays mounted (empty) even with nothing to show, so
  // aria-live="polite" announces the notice the instant it appears —
  // announcing depends on the LIVE REGION already being present in the DOM
  // before its content changes, not on the region itself being added.
  return (
    <div role="status" aria-live="polite" className="shrink-0">
      {summary && (
        <button
          type="button"
          tabIndex={0}
          data-testid="cross-workspace-approval-banner"
          onClick={() => goToWorkspace(summary.targetWorkspaceId)}
          className="flex w-full items-center gap-[var(--space-2)] px-[var(--space-3)] py-[var(--space-2)] bg-[var(--color-accent)]/10 border-b border-[var(--color-accent)]/30 text-[length:var(--type-utility-xs-size)] font-medium text-[var(--color-accent)] hover:bg-[var(--color-accent)]/15 transition-colors text-left"
        >
          <Shield size={14} weight="bold" className="shrink-0" aria-hidden="true" />
          <span className="flex-1 truncate">{summary.label}</span>
          <span className="shrink-0 underline underline-offset-2">Go there</span>
        </button>
      )}
    </div>
  )
}

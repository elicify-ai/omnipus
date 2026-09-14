// Pure helper for the cross-workspace tool-approval indicator (founder
// decision 2026-09-14). ToolApprovalModal (src/components/agents/
// ToolApprovalModal.tsx) only shows approvals that are in scope for the
// active workspace (isApprovalInScope, src/store/toolApproval.ts) — an
// approval waiting in a workspace the user isn't currently looking at is
// otherwise invisible until they switch tabs or the server's 10-minute
// timeout denies it for them.
//
// This module computes the notice text and click target for those
// out-of-scope approvals. Kept separate from the rendering component
// (CrossWorkspaceApprovalBanner.tsx) so the copy logic can be unit-tested
// without mounting React or a QueryClient.

import type { PendingToolApproval } from '@/store/toolApproval'

export interface CrossWorkspaceApprovalSummary {
  /** Total pending approvals whose workspace differs from the active one. */
  count: number
  /** Distinct workspace ids holding those approvals, in queue (arrival) order. */
  workspaceIds: string[]
  /** Workspace to navigate to on click — the first one, in queue order. */
  targetWorkspaceId: string
  /** Human-readable notice text, plural-aware. */
  label: string
}

/**
 * Summarizes pending approvals that belong to a workspace other than
 * `activeWorkspaceId`, or returns null when there is nothing to show.
 *
 * Mirrors isApprovalInScope's own rule for what counts as "elsewhere": an
 * approval with no workspaceId is in scope everywhere (never "elsewhere"),
 * and when there is no active workspace at all (activeWorkspaceId === null,
 * e.g. the "All workspaces" view) EVERY approval is already in scope for the
 * modal, so there is nothing left for this indicator to surface.
 */
export function summarizeCrossWorkspaceApprovals(
  queue: Pick<PendingToolApproval, 'workspaceId'>[],
  activeWorkspaceId: string | null,
  workspaceName: (workspaceId: string) => string,
): CrossWorkspaceApprovalSummary | null {
  if (!activeWorkspaceId) return null

  const workspaceIds: string[] = []
  let count = 0
  for (const approval of queue) {
    if (!approval.workspaceId || approval.workspaceId === activeWorkspaceId) continue
    count += 1
    if (!workspaceIds.includes(approval.workspaceId)) workspaceIds.push(approval.workspaceId)
  }
  if (count === 0) return null

  const targetWorkspaceId = workspaceIds[0]
  const approvalWord = count === 1 ? 'approval' : 'approvals'
  const label =
    workspaceIds.length === 1
      ? `${count} ${approvalWord} waiting in ${workspaceName(targetWorkspaceId)}`
      : `${count} ${approvalWord} waiting in ${workspaceIds.length} other workspaces`

  return { count, workspaceIds, targetWorkspaceId, label }
}

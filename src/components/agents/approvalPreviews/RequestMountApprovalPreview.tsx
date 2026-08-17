// Readable summary for `request_mount` tool-approval requests (ADR-063
// FR-7.2) — the highest-consequence approval in the product: granting an
// agent read/write access to a real folder on the operator's machine.
//
// Operator-approved copy direction: the user-facing concept is "Add folder",
// never "mount", never "work outside the workspace". Registered as a
// 'replace'-mode registry entry (registry.ts) — the raw host_path/reason
// argument names are jargon the fields below already translate in full, so
// the generic Tool line + Arguments JSON dump is hidden entirely rather than
// shown alongside this (see ToolApprovalPreviewEntry's mode doc in types.ts).
// No "Technical details" escape hatch either: the full request already lives
// in the conversation transcript for anyone with Verbose chat enabled, and a
// dialog asking for a decision should carry exactly what the decision needs.

import { FolderPlus } from '@phosphor-icons/react'
import { queryClient } from '@/lib/queryClient'
import { workspacesQueryKeys } from '@/lib/api'
import type { Session, Workspace } from '@/lib/api'
import type { ToolApprovalPreviewContext } from './types'

/**
 * Best-effort workspace name for the session this approval belongs to.
 *
 * Deliberately a read-only `queryClient.getQueryData` lookup against the
 * SAME `['sessions']` / `workspacesQueryKeys.list({status:'active'})` cache
 * entries the Sidebar and OmnipusRuntimeProvider already populate — not a
 * fresh `useQuery` subscription. This card can be shown for a background or
 * delegated session the operator never opened in this tab, so there is
 * nothing worth fetching-and-waiting for; it's a nice-to-have label, not
 * load-bearing for the decision (the Folder path is). Same pattern as
 * BrowserLiveView.tsx's `resolvedAgentName` lookup.
 */
function resolveWorkspaceName(sessionId: string): string {
  const sessions = queryClient.getQueryData<Session[]>(['sessions'])
  const workspaceId = sessions?.find((s) => s.id === sessionId)?.workspace_id
  if (!workspaceId) return 'Unknown workspace'
  const workspaces = queryClient.getQueryData<Workspace[]>(
    workspacesQueryKeys.list({ status: 'active' }),
  )
  return workspaces?.find((w) => w.id === workspaceId)?.name ?? workspaceId
}

export function RequestMountApprovalPreview({ args, agentName, sessionId }: ToolApprovalPreviewContext) {
  const hostPath = (
    typeof args.host_path === 'string' ? args.host_path
    : typeof args.path === 'string' ? args.path
    : ''
  ).trim()
  const reason = typeof args.reason === 'string' ? args.reason.trim() : ''
  const workspaceName = resolveWorkspaceName(sessionId)

  return (
    <div className="px-5 py-4 space-y-4">
      <div>
        <p className="text-xs text-[var(--color-muted)] mb-1 flex items-center gap-1">
          <FolderPlus size={13} aria-hidden="true" />
          Folder
        </p>
        {hostPath ? (
          <p className="font-mono text-sm font-medium text-[var(--color-secondary)] break-all">
            {hostPath}
          </p>
        ) : (
          <p className="text-sm text-[var(--color-error)]">
            No folder path was included with this request.
          </p>
        )}
      </div>

      <div>
        <p className="text-xs text-[var(--color-muted)] mb-1">Why</p>
        <p className="text-sm text-[var(--color-secondary)]">
          {reason || 'No reason was given.'}
        </p>
      </div>

      <div>
        <p className="text-xs text-[var(--color-muted)] mb-1">Workspace</p>
        <p className="text-sm text-[var(--color-secondary)]">{workspaceName}</p>
      </div>

      <p className="text-xs text-[var(--color-warning)] pt-3 border-t border-[var(--color-border)]">
        {agentName} will be able to read and change files in this folder until you remove it.
      </p>
    </div>
  )
}

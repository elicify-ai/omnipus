// TeamPanel — the Team side-panel content shared by the docked shell and its
// chrome-less full-screen route (side-panel-wave3 §5, spec §10 Wave 3:
// "Graph gets zoom/scroll inside the panel"). It is the SAME delegation-graph
// editor as the workspace Team tab — WorkspaceTeamTab, whose graph carries
// the catalogued ZoomableView canvas preset (ZoomPill + / –, wheel/pinch
// zoom, drag pan, `+ − 0 1` keys, overflow mini-map; see
// team/WorkspaceTeamGraph.tsx) — mounted under a WorkspaceContextProvider so
// the tab's useActiveWorkspace() resolves outside the workspace route.
//
// This file adds only workspace-loading and recovery states: the docked shell
// owns the panel header (title / expand / close) and the full-screen route owns
// the chrome-less "← Back to chat" bar. Once loaded, the tab stays usable below
// a persistent recovery notice if refreshing that workspace fails.
// Registration into src/components/panel-shell/registry.tsx is the panel
// registration lane's change (same shape as LibraryPanelContent there).

import { useCallback, useEffect, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { fetchWorkspace, workspacesQueryKeys } from '@/lib/api'
import { isApiError } from '@/lib/api-error'
import { QueryErrorState } from '@/components/shared/QueryErrorState'
import { Skeleton } from '@/components/ui/skeleton'
import { useUiStore } from '@/store/ui'
import type {
  PanelContentProps,
  PanelContext,
  WorkspacePanelContext,
} from '@/components/panel-shell/types'
import { WorkspaceContextProvider } from '../WorkspaceTabContainer'
import { WorkspaceTeamTab } from '../WorkspaceTeamTab'

export interface TeamPanelProps {
  shellProps?: PanelContentProps
}

function asWorkspaceContext(context: PanelContext | null): WorkspacePanelContext | null {
  return context !== null && context.sessionId === undefined ? context : null
}

/** The Team content shared by the docked shell and its chrome-less route. */
export function TeamPanel({ shellProps }: TeamPanelProps = {}) {
  const activePanel = useUiStore((state) => state.activePanel)
  const suppliedContext = asWorkspaceContext(
    shellProps?.context ?? (activePanel?.id === 'team' ? activePanel.context : null),
  )
  const workspaceId = suppliedContext?.workspaceId

  // The shell hands the panel a workspaceId, not a workspace record; the tab
  // needs the record (core_team, revision). Same resolve-one-workspace-by-id
  // job fetchWorkspace already does for the AgentProfile Heartbeat tab.
  const { data: workspace, isPending, isError, error, refetch } = useQuery({
    queryKey: workspacesQueryKeys.detail(workspaceId ?? ''),
    queryFn: () => fetchWorkspace(workspaceId as string),
    enabled: !!workspaceId,
    staleTime: 30_000,
  })
  // A cold refetch clears the query's error while pending; keep its safe reason
  // visible until that attempt settles, scoped to the workspace being retried.
  const [retryFailure, setRetryFailure] = useState<{ workspaceId: string; message: string } | null>(null)

  // Team's panel address is just the workspace — a constant getter is the
  // whole expand context (the shell re-targets the shared full-screen route
  // through it, and the fullscreen route keeps its `?workspace=` fresh).
  const getExpandContext = useCallback((): PanelContext => ({ workspaceId }), [workspaceId])
  useEffect(() => {
    const register = shellProps?.registerExpandContext
    if (!register) return undefined
    register(getExpandContext)
    return () => register(null)
  }, [getExpandContext, shellProps?.registerExpandContext])

  if (!workspaceId) return null
  if (shellProps?.presentation === 'docked' && activePanel?.id !== 'team') return null

  const failureMessage = isError
    ? isApiError(error) ? error.userMessage : 'Failed to load team. Please try again.'
    : retryFailure?.workspaceId === workspaceId ? retryFailure.message : null
  const retryTeam = failureMessage ? () => {
    setRetryFailure({ workspaceId, message: failureMessage })
    void refetch().then(() => {
      setRetryFailure((current) => current?.workspaceId === workspaceId ? null : current)
    })
  } : undefined

  return (
    <div
      data-testid={
        shellProps?.presentation === 'fullscreen' ? 'team-panel-fullscreen' : 'team-panel-docked'
      }
      aria-label="Team panel"
      className="relative flex h-full min-h-0 w-full min-w-0 flex-col overflow-hidden bg-[var(--color-surface-0)]"
    >
      {!workspace && failureMessage ? (
        <QueryErrorState
          layout="fill"
          message={failureMessage}
          onRetry={retryTeam}
        />
      ) : !workspace && isPending ? (
        <div
          role="status"
          className="flex h-full flex-col items-center justify-center gap-[var(--space-2-5)] p-[var(--space-4)] text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]"
        >
          <Skeleton className="h-[var(--space-3)] w-[var(--space-8)]" />
          <span>Loading team…</span>
        </div>
      ) : workspace ? (
        <>
          {failureMessage ? (
            <QueryErrorState
              layout="fill"
              message={`Could not refresh Team. ${failureMessage} Showing the last known Team data.`}
              onRetry={retryTeam}
              className="h-auto flex-none shrink-0 flex-row justify-start gap-[var(--space-2)] border-b border-[var(--color-border)] bg-[var(--color-surface-1)] p-[var(--space-2-5)] text-left [&>p]:min-w-0 [&>p]:flex-1 [&>svg]:shrink-0 [&>button]:shrink-0"
            />
          ) : null}
          {/* The tab fills this remaining area, never the recovery notice. */}
          <div className="relative min-h-0 flex-1">
            <WorkspaceContextProvider workspace={workspace}>
              <WorkspaceTeamTab workspaceId={workspaceId} />
            </WorkspaceContextProvider>
          </div>
        </>
      ) : null}
    </div>
  )
}

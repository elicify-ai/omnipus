// TeamPanel — the Team side-panel content shared by the docked shell and its
// chrome-less full-screen route (side-panel-wave3 §5, spec §10 Wave 3:
// "Graph gets zoom/scroll inside the panel"). It is the SAME delegation-graph
// editor as the workspace Team tab — WorkspaceTeamTab, whose graph carries
// the catalogued ZoomableView canvas preset (ZoomPill + / –, wheel/pinch
// zoom, drag pan, `+ − 0 1` keys, overflow mini-map; see
// team/WorkspaceTeamGraph.tsx) — mounted under a WorkspaceContextProvider so
// the tab's useActiveWorkspace() resolves outside the workspace route.
//
// This file intentionally adds NO team UI of its own: the docked shell owns
// the panel header (title / expand / close) and the full-screen route owns
// the chrome-less "← Back to chat" bar, so the content is exactly the tab.
// Registration into src/components/panel-shell/registry.tsx is the panel
// registration lane's change (same shape as LibraryPanelContent there).

import { useCallback, useEffect } from 'react'
import { useQuery } from '@tanstack/react-query'
import { fetchWorkspace, workspacesQueryKeys } from '@/lib/api'
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
  const { data: workspace } = useQuery({
    queryKey: workspacesQueryKeys.detail(workspaceId ?? ''),
    queryFn: () => fetchWorkspace(workspaceId as string),
    enabled: !!workspaceId,
    staleTime: 30_000,
  })

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

  if (!suppliedContext?.workspaceId || !workspace) return null
  if (shellProps?.presentation === 'docked' && activePanel?.id !== 'team') return null

  return (
    <div
      data-testid={
        shellProps?.presentation === 'fullscreen' ? 'team-panel-fullscreen' : 'team-panel-docked'
      }
      aria-label="Team panel"
      className="relative h-full min-h-0 w-full min-w-0 overflow-hidden bg-[var(--color-surface-0)]"
    >
      <WorkspaceContextProvider workspace={workspace}>
        <WorkspaceTeamTab workspaceId={suppliedContext.workspaceId} />
      </WorkspaceContextProvider>
    </div>
  )
}

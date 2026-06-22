import { createContext, useContext, useEffect } from 'react'
import { Outlet, useNavigate, useLocation } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { fetchWorkspaces, workspacesQueryKeys } from '@/lib/api'
import type { Workspace } from '@/lib/api'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { WorkspaceTabBar, resolveActiveSegment, WORKSPACE_TABS } from './WorkspaceTabBar'

// React context carrying the resolved workspace to every tab. Tabs read the
// workspace from here rather than re-fetching, so the container is the single
// source of truth for workspace identity + the active-workspace binding.
const WorkspaceContext = createContext<Workspace | null>(null)

/** Hook for tab components to read the resolved workspace. */
export function useActiveWorkspace(): Workspace {
  const ws = useContext(WorkspaceContext)
  if (!ws) {
    throw new Error('useActiveWorkspace must be used within a WorkspaceTabContainer')
  }
  return ws
}

interface WorkspaceTabContainerProps {
  workspaceId: string
}

/**
 * The workspace detail shell — a tabbed container. Resolves the workspace
 * (active or archived), binds it as the active workspace (so chat/sessions/
 * tasks pick up the context), renders the sticky tab bar, and provides the
 * resolved workspace to the active tab via React context.
 *
 * Tab content (Chat/Board/List/Graph/Calendar/Team/Settings) lives in the
 * sub-route files under routes/_app/workspaces.$workspaceId.*.
 */
export function WorkspaceTabContainer({ workspaceId }: WorkspaceTabContainerProps) {
  const navigate = useNavigate()
  const { activeWorkspaceId, setActiveWorkspaceId, setActiveMilestoneId } = useWorkspacesStore()

  const {
    data: workspaces = [],
    isError: workspacesError,
    isLoading: workspacesLoading,
  } = useQuery({
    queryKey: workspacesQueryKeys.list({ status: 'active' }),
    queryFn: () => fetchWorkspaces({ status: 'active' }),
    staleTime: 30_000,
  })

  // Resolve 'inbox' alias to the real default workspace id.
  useEffect(() => {
    if (workspaceId !== 'inbox') return
    if (workspaces.length === 0) return
    const def = workspaces.find((w) => w.is_default)
    if (def) {
      void navigate({
        to: '/workspaces/$workspaceId/chat',
        params: { workspaceId: def.id },
        replace: true,
      })
    }
  }, [workspaceId, workspaces, navigate])

  // Bind the active workspace from the route — the single source of truth the
  // chat turn, session filter, and task views read (M4 Gap 2).
  useEffect(() => {
    if (workspaceId && workspaceId !== 'inbox' && activeWorkspaceId !== workspaceId) {
      setActiveWorkspaceId(workspaceId)
    }
  }, [workspaceId, activeWorkspaceId, setActiveWorkspaceId])

  // Reset milestone filter whenever the active workspace changes.
  useEffect(() => {
    setActiveMilestoneId(null)
  }, [workspaceId, setActiveMilestoneId])

  // Direct-URL access to an archived workspace — fall back to the archived list.
  const { data: archivedWorkspaces = [], isLoading: archivedLoading } = useQuery({
    queryKey: workspacesQueryKeys.list({ status: 'archived' }),
    queryFn: () => fetchWorkspaces({ status: 'archived' }),
    staleTime: 60_000,
    enabled: workspaces.length > 0 && !workspaces.find((w) => w.id === workspaceId),
  })

  const workspace =
    workspaces.find((w) => w.id === workspaceId) ??
    archivedWorkspaces.find((w) => w.id === workspaceId)

  // 'inbox' alias is redirecting — suppress render while useEffect navigates.
  if (workspaceId === 'inbox') return null

  if (workspacesError) {
    return (
      <div className="flex items-center justify-center h-full p-8 text-[var(--color-muted)] text-sm">
        Failed to load workspace. Check your connection and try again.
      </div>
    )
  }

  if (workspacesLoading) return <WorkspaceShellSkeleton />

  if (!workspace) {
    if (archivedLoading) return <WorkspaceShellSkeleton />
    return <WorkspaceNotFoundState />
  }

  return (
    <WorkspaceContext.Provider value={workspace}>
      <div className="absolute inset-0 flex flex-col overflow-hidden">
        <WorkspaceBreadcrumbBar workspace={workspace} />
        <WorkspaceTabBar workspaceId={workspace.id} />
        <div className="flex-1 min-h-0 relative">
          <Outlet />
        </div>
      </div>
    </WorkspaceContext.Provider>
  )
}

/**
 * WorkspaceNotFoundState — shown when a workspace ID doesn't resolve.
 * Provides a "Back to my workspace" button that navigates to the default
 * workspace (or the workspaces list if none is found). Fix #10.
 */
function WorkspaceNotFoundState() {
  const navigate = useNavigate()
  const { data: workspaces = [] } = useQuery({
    queryKey: workspacesQueryKeys.list({ status: 'active' }),
    queryFn: () => fetchWorkspaces({ status: 'active' }),
    staleTime: 30_000,
  })

  function handleBackToWorkspace() {
    const defaultWs = workspaces.find((w) => w.is_default) ?? workspaces[0]
    if (defaultWs) {
      void navigate({ to: '/workspaces/$workspaceId/chat', params: { workspaceId: defaultWs.id } })
    } else {
      void navigate({ to: '/' })
    }
  }

  return (
    <div
      className="flex flex-col items-center justify-center h-full gap-4 p-8 text-center"
      data-testid="workspace-not-found"
    >
      <p className="text-[var(--color-muted)] text-sm">Workspace not found.</p>
      <p className="text-[var(--color-muted)] text-xs max-w-xs">
        This workspace may have been deleted or the link may be outdated.
      </p>
      <button
        type="button"
        onClick={handleBackToWorkspace}
        data-testid="workspace-not-found-back-btn"
        className="inline-flex items-center gap-2 px-4 py-2 rounded-md bg-[var(--color-accent)] text-[var(--color-primary)] font-semibold text-sm hover:opacity-90 transition-opacity"
      >
        Back to my workspace
      </button>
    </div>
  )
}

/**
 * WorkspaceBreadcrumbBar — slim title row above the tab bar showing
 * "WorkspaceName › ActiveTab" so users always know which workspace
 * they're in when the sidebar is closed (fix #8).
 */
function WorkspaceBreadcrumbBar({ workspace }: { workspace: Workspace }) {
  const location = useLocation()
  const activeSegment = resolveActiveSegment(location.pathname, workspace.id)
  const activeTab = WORKSPACE_TABS.find((t) => t.segment === activeSegment)

  return (
    <div
      className="flex items-center gap-1.5 px-4 py-1 border-b border-[var(--color-border)]/50 bg-[var(--color-surface-1)]/80 backdrop-blur-sm"
      data-testid="workspace-breadcrumb"
      aria-label="Workspace breadcrumb"
    >
      <span className="text-xs font-semibold text-[var(--color-secondary)] truncate max-w-[200px]">
        {workspace.name}
      </span>
      {activeTab && (
        <>
          <span className="text-xs text-[var(--color-muted)]" aria-hidden="true">›</span>
          <span className="text-xs text-[var(--color-muted)]">
            {activeTab.label}
          </span>
        </>
      )}
    </div>
  )
}

function WorkspaceShellSkeleton() {
  return (
    <div className="absolute inset-0 flex flex-col">
      <div className="flex gap-2 px-3 border-b border-[var(--color-border)] bg-[var(--color-surface-1)]">
        {[1, 2, 3, 4, 5, 6, 7].map((i) => (
          <div key={i} className="h-11 w-20 my-1 rounded bg-[var(--color-surface-2)] animate-pulse" />
        ))}
      </div>
      <div className="flex gap-3 p-4 flex-1">
        {[1, 2, 3, 4, 5].map((i) => (
          <div
            key={i}
            className="flex-1 min-w-[180px] h-48 rounded-xl border border-[var(--color-border)] animate-pulse bg-[var(--color-surface-1)]"
          />
        ))}
      </div>
    </div>
  )
}

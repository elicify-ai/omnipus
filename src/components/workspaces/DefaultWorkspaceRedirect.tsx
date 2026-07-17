import { useEffect } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { fetchWorkspaces, workspacesQueryKeys } from '@/lib/api'
import type { WorkspaceTab } from './WorkspaceTabBar'

interface DefaultWorkspaceRedirectProps {
  /** Which workspace tab to land on (default 'chat'). */
  tab?: WorkspaceTab['segment']
}

/**
 * Resolves the default workspace and redirects to one of its tabs. Used by the
 * folded-away top-level routes (global chat "/", /tasks,
 * /automations) so old deep links land in the right place inside a workspace.
 *
 * Resolution order: the is_default workspace → the first workspace → "/" stays
 * (no workspaces yet, an error state). A spinner shows while the list loads.
 */
export function DefaultWorkspaceRedirect({ tab = 'chat' }: DefaultWorkspaceRedirectProps) {
  const navigate = useNavigate()

  const { data: workspaces, isError, isLoading } = useQuery({
    queryKey: workspacesQueryKeys.list({ status: 'active' }),
    queryFn: () => fetchWorkspaces({ status: 'active' }),
    staleTime: 30_000,
  })

  useEffect(() => {
    if (isLoading) return
    if (isError) {
      console.warn('[workspace redirect] failed to load workspaces')
      return
    }
    const target = workspaces?.find((w) => w.is_default) ?? workspaces?.[0]
    if (target) {
      void navigate({
        to: `/workspaces/$workspaceId/${tab}`,
        params: { workspaceId: target.id },
        replace: true,
      })
    }
  }, [workspaces, isLoading, isError, navigate, tab])

  if (isError) {
    return (
      <div className="flex items-center justify-center h-full min-h-[200px] p-8 text-sm text-[var(--color-muted)]">
        Could not load workspaces. Check your connection and try again.
      </div>
    )
  }

  // Loaded, but there are no workspaces to redirect into — don't spin forever.
  if (!isLoading && (workspaces?.length ?? 0) === 0) {
    return (
      <div className="flex items-center justify-center h-full min-h-[200px] p-8 text-center text-sm text-[var(--color-muted)]">
        No workspaces yet. Create one to get started.
      </div>
    )
  }

  return (
    <div className="flex items-center justify-center h-full min-h-[200px]">
      <div className="w-6 h-6 rounded-full border-2 border-[var(--color-accent)] border-t-transparent animate-spin" />
    </div>
  )
}

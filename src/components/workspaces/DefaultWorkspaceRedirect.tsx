import { useEffect } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { ArrowClockwise, WarningCircle } from '@phosphor-icons/react'
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
 *
 * The workspaces query key (workspacesQueryKeys.list({status:'active'})) is
 * SHARED with Sidebar.tsx's own 30s poll — both observe the same cache entry.
 * A failed background refetch (e.g. the poll 401ing because a second login
 * elsewhere invalidated this session) sets that shared entry's status to
 * "error" for every observer, including this one, even if it had previously
 * loaded successfully. Unlike Sidebar's poll, this component has no
 * refetchInterval of its own and mounts fresh only when the user lands on
 * "/" — so without an explicit recovery affordance, a user who hits this
 * screen while the shared cache is errored has no way to get past it short
 * of reloading the page. The isError branch below therefore wires a REAL
 * retry (refetch()) rather than a dead-end message.
 */
export function DefaultWorkspaceRedirect({ tab = 'chat' }: DefaultWorkspaceRedirectProps) {
  const navigate = useNavigate()

  const { data: workspaces, isError, isLoading, isFetching, refetch } = useQuery({
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
      <div className="flex flex-col items-center justify-center gap-3 h-full min-h-[200px] p-8 text-center">
        <WarningCircle size={20} weight="bold" className="text-[var(--color-error)]" />
        <p className="text-sm text-[var(--color-muted)]">
          Could not load workspaces. Check your connection, then retry.
        </p>
        <button tabIndex={0}
          type="button"
          data-testid="workspace-redirect-retry"
          onClick={() => void refetch()}
          disabled={isFetching}
          aria-label="Retry loading workspaces"
          className="inline-flex items-center gap-1.5 rounded-md border px-3 py-1.5 text-xs font-medium transition-colors disabled:opacity-50"
          style={{ borderColor: 'var(--color-accent)', color: 'var(--color-accent)' }}
        >
          <ArrowClockwise size={14} className={isFetching ? 'animate-spin' : undefined} />
          {isFetching ? 'Retrying…' : 'Retry'}
        </button>
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

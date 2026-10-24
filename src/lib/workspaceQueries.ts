import { fetchWorkspaces, workspacesQueryKeys } from '@/lib/api'

/**
 * The ONE query definition for the active workspace list. Every producer of
 * this key (sidebar, Omnipus chat, saved-chat route) uses it, so whichever
 * component starts the request, cancelling the query aborts the real request.
 */
export const activeWorkspacesQueryOptions = {
  queryKey: workspacesQueryKeys.list({ status: 'active' }),
  queryFn: ({ signal }: { signal: AbortSignal }) => fetchWorkspaces({ status: 'active', signal }),
}

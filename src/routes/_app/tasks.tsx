import { useEffect } from 'react'
import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { fetchWorkspaces, workspacesQueryKeys } from '@/lib/api'

// FR-L2-016: /tasks redirects to the Inbox workspace (is_default: true) at
// /workspaces/$workspaceId. Shows a brief spinner while the workspaces list
// loads. Falls back to / if the fetch fails.

function TasksRedirect() {
  const navigate = useNavigate()

  const { data: workspaces, isError, isLoading } = useQuery({
    queryKey: workspacesQueryKeys.list({ status: 'active' }),
    queryFn: () => fetchWorkspaces({ status: 'active' }),
    staleTime: 30_000,
  })

  useEffect(() => {
    if (isError) {
      console.warn('[tasks redirect] failed to load workspaces, falling back to /')
      void navigate({ to: '/', replace: true })
      return
    }
    if (!isLoading && workspaces) {
      const inbox = workspaces.find((p) => p.is_default)
      if (inbox) {
        void navigate({ to: '/workspaces/$workspaceId', params: { workspaceId: inbox.id }, replace: true })
      } else if (workspaces.length > 0) {
        // No inbox workspace found (backend may not have created it yet) — use first workspace
        void navigate({ to: '/workspaces/$workspaceId', params: { workspaceId: workspaces[0].id }, replace: true })
      } else {
        // No workspaces at all — go to root
        void navigate({ to: '/', replace: true })
      }
    }
  }, [workspaces, isLoading, isError, navigate])

  return (
    <div className="flex items-center justify-center h-full min-h-[200px]">
      <div className="w-6 h-6 rounded-full border-2 border-[var(--color-accent)] border-t-transparent animate-spin" />
    </div>
  )
}

export const Route = createFileRoute('/_app/tasks')({
  component: TasksRedirect,
})

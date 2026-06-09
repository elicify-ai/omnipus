import { useEffect } from 'react'
import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { fetchProjects, projectsQueryKeys } from '@/lib/api'

// FR-L2-016: /tasks redirects to the Inbox project (is_default: true).
// Shows a brief spinner while projects list loads.
// Falls back to / if the fetch fails.

function TasksRedirect() {
  const navigate = useNavigate()

  const { data: projects, isError, isLoading } = useQuery({
    queryKey: projectsQueryKeys.list({ status: 'active' }),
    queryFn: () => fetchProjects({ status: 'active' }),
    staleTime: 30_000,
  })

  useEffect(() => {
    if (isError) {
      console.warn('[tasks redirect] failed to load projects, falling back to /')
      void navigate({ to: '/', replace: true })
      return
    }
    if (!isLoading && projects) {
      const inbox = projects.find((p) => p.is_default)
      if (inbox) {
        void navigate({ to: '/projects/$projectId', params: { projectId: inbox.id }, replace: true })
      } else if (projects.length > 0) {
        // No inbox project found (backend may not have created it yet) — use first project
        void navigate({ to: '/projects/$projectId', params: { projectId: projects[0].id }, replace: true })
      } else {
        // No projects at all — go to root
        void navigate({ to: '/', replace: true })
      }
    }
  }, [projects, isLoading, isError, navigate])

  return (
    <div className="flex items-center justify-center h-full min-h-[200px]">
      <div className="w-6 h-6 rounded-full border-2 border-[var(--color-accent)] border-t-transparent animate-spin" />
    </div>
  )
}

export const Route = createFileRoute('/_app/tasks')({
  component: TasksRedirect,
})

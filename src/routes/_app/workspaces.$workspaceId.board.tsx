import { useEffect } from 'react'
import { createFileRoute, useNavigate } from '@tanstack/react-router'

// Tasks tab — RETIRED AS A PAGE (wave 3, SP-6/SP-38). The strip entry is a
// panel toggle and the full-screen surface is the shared chrome-less
// "Back to chat" route; this route is kept — deliberately — as a redirect
// stub (the workspaces.$workspaceId.media.tsx precedent): a bookmarked or
// deep-linked /workspaces/{id}/board URL must not 404 or dead-end. Landing
// here retargets to the deep-link form of the same destination
// (side-panel-shell-spec.md §8.2): `/workspaces/{id}/chat?panel=tasks`
// (replace). The chat route's deep-link restore (workspaces.$workspaceId.
// chat.tsx) owns the actual panel open — the redirect itself makes NO store
// call, so the panel state has exactly one writer: the URL contract.
function WorkspaceBoardRedirect() {
  const { workspaceId } = Route.useParams()
  const navigate = useNavigate()

  useEffect(() => {
    void navigate({
      to: '/workspaces/$workspaceId/chat',
      params: { workspaceId },
      search: { panel: 'tasks' },
      replace: true,
    })
  }, [workspaceId, navigate])

  return (
    <div className="flex items-center justify-center h-full min-h-[200px]">
      <div className="w-6 h-6 rounded-full border-2 border-[var(--color-accent)] border-t-transparent animate-spin" />
    </div>
  )
}

export const Route = createFileRoute('/_app/workspaces/$workspaceId/board')({
  component: WorkspaceBoardRedirect,
})

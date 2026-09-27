import { useEffect } from 'react'
import { createFileRoute, useNavigate } from '@tanstack/react-router'

// Media tab — RETIRED (superseded by the Library, library-spec.md). The old
// UUID-blob manifest surface (WorkspaceMediaTab.tsx, GET
// /workspaces/…/media) has been deleted; that endpoint and manifest still
// exist server-side here but now back only the chat-attachment picker
// (ComposerMediaLibrary.tsx), not a standalone tab.
//
// This route is kept — deliberately — as a redirect stub, mirroring the
// /tasks and /automations precedent (CLAUDE.md "Retired surfaces"): a
// bookmarked/deep-linked /workspaces/{id}/media URL must not 404 or
// dead-end. Landing here — from a raw URL hit — retargets to the deep-link
// form of the same destination (side-panel-shell-spec.md §8.2):
// `/workspaces/{id}/chat?panel=library` (replace). The chat route's
// deep-link restore (workspaces.$workspaceId.chat.tsx) owns the actual
// panel open — the redirect itself makes NO store call, so the panel state
// has exactly one writer: the URL contract. (The tab-strip "Library" entry
// no longer routes here at all — it is a toggle button over the store, see
// WorkspaceTabBar.tsx.)
function WorkspaceMediaRedirect() {
  const { workspaceId } = Route.useParams()
  const navigate = useNavigate()

  useEffect(() => {
    void navigate({
      to: '/workspaces/$workspaceId/chat',
      params: { workspaceId },
      search: { panel: 'library' },
      replace: true,
    })
  }, [workspaceId, navigate])

  return (
    <div className="flex items-center justify-center h-full min-h-[200px]">
      <div className="w-6 h-6 rounded-full border-2 border-[var(--color-accent)] border-t-transparent animate-spin" />
    </div>
  )
}

export const Route = createFileRoute('/_app/workspaces/$workspaceId/media')({
  component: WorkspaceMediaRedirect,
})

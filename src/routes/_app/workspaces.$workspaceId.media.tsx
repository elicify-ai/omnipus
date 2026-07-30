import { useEffect } from 'react'
import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { useUiStore } from '@/store/ui'

// Media tab — RETIRED (superseded by the Library, library-spec.md). The old
// UUID-blob manifest surface (WorkspaceMediaTab.tsx, GET
// /workspaces/{id}/media) has been deleted; that endpoint and manifest still
// exist server-side but now back only the chat-attachment picker
// (ComposerMediaLibrary.tsx), not a standalone tab.
//
// This route is kept — deliberately — as a redirect stub, mirroring the
// /tasks and /automations precedent (CLAUDE.md "Retired surfaces"): a
// bookmarked/deep-linked /workspaces/{id}/media URL must not 404 or
// dead-end. WorkspaceTabBar.tsx's tab strip still links here too (segment
// 'media', now labelled "Library" — see that file's doc comment for why the
// route/Link wiring was kept instead of special-casing the tab into a plain
// button). Landing here — from either the tab click or a raw URL hit — opens
// the Library panel scoped to this workspace (the same
// `openLibraryPanel(workspaceId)` call ChatControls.tsx's "Open library"
// button makes) and immediately redirects back to the workspace's Chat tab,
// so the URL never sits on a page with no content of its own.
function WorkspaceMediaRedirect() {
  const { workspaceId } = Route.useParams()
  const navigate = useNavigate()

  useEffect(() => {
    useUiStore.getState().openLibraryPanel(workspaceId)
    void navigate({
      to: '/workspaces/$workspaceId/chat',
      params: { workspaceId },
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

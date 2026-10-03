import { createFileRoute } from '@tanstack/react-router'
import { WorkspaceChatTab } from '@/components/workspaces/WorkspaceChatTab'
import { WAVE_2_PANEL_IDS } from '@/components/panel-shell/types'
import { usePanelDeepLink } from '@/components/panel-shell/usePanelDeepLink'

// Chat tab — the workspace's default front view (chat-first). Agent picker is
// scoped to the workspace team; sessions are filtered by this workspace_id.
//
// §8.2 — this is the ONLY route whose search schema declares `panel` (plus
// `agent`, meaningful only with panel=mail, SP-23). The hand-rolled
// validateSearch (no zod needed for two optional strings) keeps a `panel`
// value ONLY when it names a REGISTERED panel id (MAJ-012; wave 2 = library,
// browser, mail per §10/FR-014): an unknown or unregistered-but-future id
// (`bogus`, `tasks`) is dropped, and `agent` is kept as a declared key
// regardless. The router MERGES this return onto the raw search
// (`{...raw, ...validated}`), so a dropped key survives unless it is returned
// as `undefined`. That still does not rewrite the address bar — the visible
// replace is usePanelDeepLink's job.
function validateSearch(search: Record<string, unknown>): { panel?: string; agent?: string } {
  const result: Record<string, string | undefined> = {}
  for (const key of Object.keys(search)) result[key] = undefined
  if (typeof search.panel === 'string' && (WAVE_2_PANEL_IDS as readonly string[]).includes(search.panel)) {
    result.panel = search.panel
  }
  if (typeof search.agent === 'string') {
    result.agent = search.agent
  }
  return result
}

function WorkspaceChatRoute() {
  const { workspaceId } = Route.useParams()
  const { panel } = Route.useSearch()
  usePanelDeepLink(workspaceId, panel)
  return <WorkspaceChatTab workspaceId={workspaceId} />
}

export const Route = createFileRoute('/_app/workspaces/$workspaceId/chat')({
  component: WorkspaceChatRoute,
  validateSearch,
})

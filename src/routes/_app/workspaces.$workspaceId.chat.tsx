import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { useEffect, useRef } from 'react'
import { WorkspaceChatTab } from '@/components/workspaces/WorkspaceChatTab'
import { getPanelDefinition } from '@/components/panel-shell/registry'
import { usePanelDeepLink } from '@/components/panel-shell/usePanelDeepLink'
import type { PanelId } from '@/components/panel-shell/types'
import { useSessionStore } from '@/store/session'

// Chat tab — the workspace's default front view (chat-first). Agent picker is
// scoped to the workspace team; sessions are filtered by this workspace_id.
//
// §8.2 — this is the ONLY route whose search schema declares `panel` (plus
// `agent`, meaningful only with panel=mail, SP-23, and `open`, the linked
// chat's session id, #1437). The hand-rolled validateSearch (no zod needed
// for these optional strings) keeps a `panel` value ONLY when it names a
// REGISTERED panel id (the registry is the single source per the shell
// slice). An unknown or unregistered id is dropped, and `agent`/`open` are
// kept as declared keys regardless. The router MERGES this return onto the
// raw search (`{...raw, ...validated}`), so a dropped key survives unless it
// is returned as `undefined`. That still does not rewrite the address bar —
// the visible replace is usePanelDeepLink's job.
function validateSearch(search: Record<string, unknown>): { panel?: string; agent?: string; open?: string } {
  const result: Record<string, string | undefined> = {}
  for (const key of Object.keys(search)) result[key] = undefined
  if (
    typeof search.panel === 'string' &&
    getPanelDefinition(search.panel as PanelId) !== undefined
  ) {
    result.panel = search.panel
  }
  if (typeof search.agent === 'string') {
    result.agent = search.agent
  }
  // #1437: the linked chat's session id — see WorkspaceTabContainer's entry
  // effect (it drives workspace entry) and the sync effect below (it keeps
  // the address naming the chat actually open).
  if (typeof search.open === 'string') {
    result.open = search.open
  }
  return result
}

function WorkspaceChatRoute() {
  const { workspaceId } = Route.useParams()
  const { panel, open } = Route.useSearch()
  usePanelDeepLink(workspaceId, panel)
  useSyncOpenSearchKey(workspaceId, open)
  return <WorkspaceChatTab workspaceId={workspaceId} />
}

// #1437 — keep the `open` key truthful while it is present. The key enters
// the address through the /sessions/{id} deep-link redirect and then follows
// the chat actually open: a session switch in the sidebar rewrites it, and
// New chat (no active session) drops it, so a reload always restores the
// chat the user is looking at. Strictly bounded to addresses that already
// carry the key: ordinary chat addresses never gain it, so every flow that
// does not start from a session link keeps its exact current address
// behaviour. Two guards keep the sync out of the way:
// - the mount pass records a baseline and writes nothing — a store a mount
//   STARTS with is not this mount's change to project (the same pattern
//   usePanelDeepLink's projection uses);
// - while the workspace entry is resolving, the adoption is still landing —
//   rewriting now could drop the key before it drove the entry.
function useSyncOpenSearchKey(workspaceId: string, open: string | undefined): void {
  const navigate = useNavigate()
  const activeSessionId = useSessionStore((s) => s.activeSessionId)
  const resolving = useSessionStore((s) => s.resolvingSessionForWorkspace[workspaceId] === true)
  const mountedRef = useRef(false)
  useEffect(() => {
    if (!mountedRef.current) {
      mountedRef.current = true
      return
    }
    if (open === undefined) return
    if (resolving) return
    const live =
      activeSessionId && activeSessionId !== '__pending' ? activeSessionId : undefined
    if (live === open) return
    navigate({
      search: ((prev: Record<string, unknown>) => ({ ...prev, open: live })) as never,
      replace: true,
    })
  }, [activeSessionId, open, resolving, navigate])
}

export const Route = createFileRoute('/_app/workspaces/$workspaceId/chat')({
  component: WorkspaceChatRoute,
  validateSearch,
})

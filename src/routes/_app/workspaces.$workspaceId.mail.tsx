import { useEffect } from 'react'
import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { useUiStore } from '@/store/ui'
import { leaveGateThen } from '@/components/panel-shell/leaveGate'
import { writeMailPanelIntent } from '@/components/workspaces/mail/mailPanelIntent'

// Mail tab — deep-linkable entry that opens the Mail side panel (SP-8: ONE
// shared panel surface; the panel itself is shell CONTENT since wave 2 —
// side-panel-shell-spec.md §10 registers Mail's PanelDefinition in the
// production registry, and the workspace tab-strip entry is a registered
// panel toggle, not a Link here). This route survives as the EXPAND target
// and the bookmarked-URL stub: it consumes the §17 draft-link params into
// the panel's per-workspace intent (mailPanelIntent.ts — messageRef is
// consume-once), opens the panel through the CRIT-001 leave gate (the
// OUTGOING panel's guard — a dirty Library asks before it is replaced), then
// retargets to the deep-link form /workspaces/{id}/chat?panel=mail (replace)
// — §8.2's media-stub pattern — so the URL never dead-ends on a page with no
// content of its own and the registered panel param survives a reload.
interface MailRouteSearch {
  mailbox?: string
  folder?: string
  message?: string
}

function WorkspaceMailRedirect() {
  const { workspaceId } = Route.useParams()
  const search = Route.useSearch() as MailRouteSearch
  const navigate = useNavigate()

  useEffect(() => {
    writeMailPanelIntent(workspaceId, {
      agentId: search.mailbox ?? null,
      folder: search.folder ?? 'inbox',
      messageRef: search.message ?? null,
    })
    leaveGateThen(useUiStore.getState().activePanel?.id ?? null, () => {
      useUiStore.getState().openPanel('mail', { workspaceId })
    })
    void navigate({
      to: '/workspaces/$workspaceId/chat',
      params: { workspaceId },
      search: { panel: 'mail' },
      replace: true,
    })
  }, [workspaceId, search.mailbox, search.folder, search.message, navigate])

  return (
    <div className="flex items-center justify-center h-full min-h-[200px]">
      <div className="w-6 h-6 rounded-full border-2 border-[var(--color-accent)] border-t-transparent animate-spin" />
    </div>
  )
}

export const Route = createFileRoute('/_app/workspaces/$workspaceId/mail')({
  validateSearch: (search: Record<string, unknown>): MailRouteSearch => ({
    mailbox: typeof search.mailbox === 'string' ? search.mailbox : undefined,
    folder: typeof search.folder === 'string' ? search.folder : undefined,
    message: typeof search.message === 'string' ? search.message : undefined,
  }),
  component: WorkspaceMailRedirect,
})

import { useEffect } from 'react'
import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { useUiStore } from '@/store/ui'
import { leaveGateThen } from '@/components/panel-shell/leaveGate'
import { writeMailPanelIntent } from '@/components/workspaces/mail/mailPanelIntent'

// Mail tab — deep-linkable entry that opens the Mail side panel (SP-8: one
// shared panel surface; the panel itself is docked beside the chat). The
// tab-strip entry (WorkspaceTabBar WORKSPACE_TABS) is a Link here, exactly
// like the Library tab's Link to the media route stub. Opening goes through
// the CRIT-001 leave gate (leaveGateThen — a dirty outgoing Library panel
// prompts before the replace), then the route redirects to the workspace's
// Chat tab so the URL never dead-ends on a page with no content of its own
// (same shape as the media/Library redirect stub).
//
// Deep links (email-mail-view-spec.md §17): the create_email_draft result's
// chat_link is /#/workspaces/{ws}/mail?mailbox={agent}&folder=drafts&message=
// {ref}. This route consumes those params into the panel's per-workspace
// intent (mailPanelIntent.ts — messageRef is consume-once), so the panel
// opens directly on that draft.
//
// NOTE: the chat route's ?panel= search contract is the shell squad's
// pending wave-1 work (-workspaces.$workspaceId.chat.panelSearch.test.ts is
// their RED). Until it lands, this route is the URL entry that opens Mail;
// when their search-restore lands, the two converge (both write the same
// activePanel value).
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
    leaveGateThen(() => {
      useUiStore.getState().openPanel('mail', { workspaceId })
    })
    void navigate({
      to: '/workspaces/$workspaceId/chat',
      params: { workspaceId },
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

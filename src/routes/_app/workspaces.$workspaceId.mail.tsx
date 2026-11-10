import { useEffect } from 'react'
import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { useUiStore } from '@/store/ui'
import { leaveGateThen } from '@/components/panel-shell/leaveGate'

// Retired full-page Mail route (superseded by R10/R11: full screen is now a
// SHARED shell feature, not a Mail-owned page). This compatibility route
// never renders Mail — every address, old bookmarks included, is carried
// straight into the shared side panel via `openPanel('mail', …)` and the
// browser lands on chat, exactly like a §17 chat_link.
interface MailRouteSearch {
  mailbox?: string
  folder?: string
  message?: string
}

function WorkspaceMailPage() {
  const { workspaceId } = Route.useParams()
  const search = Route.useSearch() as MailRouteSearch
  const navigate = useNavigate()

  useEffect(() => {
    leaveGateThen(useUiStore.getState().activePanel?.id ?? null, () => {
      useUiStore.getState().openPanel('mail', {
        workspaceId,
        mailboxId: search.mailbox ?? null,
        folder: (search.folder as 'inbox' | 'sent' | 'drafts' | undefined) ?? null,
        messageRef: search.message ?? null,
      })
      void navigate({
        to: '/workspaces/$workspaceId/chat',
        params: { workspaceId },
        search: { panel: 'mail' },
        replace: true,
      })
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
  component: WorkspaceMailPage,
})

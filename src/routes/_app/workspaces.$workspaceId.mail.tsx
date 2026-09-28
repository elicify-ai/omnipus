import { useEffect } from 'react'
import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { MailPanel } from '@/components/workspaces/mail/MailPanel'
import { useUiStore } from '@/store/ui'
import { leaveGateThen } from '@/components/panel-shell/leaveGate'
import { writeMailPanelIntent } from '@/components/workspaces/mail/mailPanelIntent'

// Full-page Mail. Expand includes view=full and renders the same MailPanel
// core in the split layout. Existing §17 chat links omit that marker and
// retain their panel-in-chat behavior through WorkspaceMailPanelDeepLink.
interface MailRouteSearch {
  mailbox?: string
  folder?: string
  message?: string
  view?: 'full'
}

function WorkspaceMailPage() {
  const { workspaceId } = Route.useParams()
  const search = Route.useSearch() as MailRouteSearch

  if (search.view !== 'full') {
    return <WorkspaceMailPanelDeepLink workspaceId={workspaceId} search={search} />
  }

  return (
    <MailPanel
      workspaceId={workspaceId}
      mailboxId={search.mailbox}
      initialFolder={search.folder}
      initialMessageRef={search.message}
      layout="split"
    />
  )
}

function WorkspaceMailPanelDeepLink({ workspaceId, search }: {
  workspaceId: string
  search: MailRouteSearch
}) {
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
    view: search.view === 'full' ? 'full' : undefined,
  }),
  component: WorkspaceMailPage,
})

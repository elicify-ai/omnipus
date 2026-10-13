/**
 * RED regression test — f5f6-round2 Item 1: after a compose/edit-draft/send
 * cycle in workspace A, a round trip through workspace B (no mailbox) and
 * workspace C (mailbox configured but unreachable) and back to A, the
 * Drafts folder must still show the real (still-present) drafts — never
 * "No messages" (MailMessageList.tsx's messages.length === 0 branch).
 *
 * Exercises MailPanel directly (not the whole panel-shell) because F3
 * (MailPanel.tsx's own doc comment) establishes that a workspace switch
 * re-adopts MailPanel WITHOUT remounting it — usePanelDeepLink /
 * WorkspaceTabContainer.resolveWorkspaceSwitch just feed it a new
 * `workspaceId` prop. RTL's rerender() reuses the same component instance
 * the same way, so this is a faithful, controllable stand-in for the real
 * workspace-switch path without needing three live IMAP servers.
 *
 * The QueryClient here is configured with the SAME defaultOptions as the
 * app's real singleton (src/lib/queryClient.ts::queryClient) — staleTime
 * 30_000 — because that default (not react-query's own staleTime:0) governs
 * whether a query considers its cache fresh enough to skip a refetch on
 * re-observation, which is exactly the mechanism under test.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

const {
  fetchAgents,
  fetchMailboxes,
  fetchMailFolders,
  fetchMailMessages,
  fetchMailMessage,
  fetchMailSummary,
  markMailSeen,
  saveMailDraft,
  sendMailDraft,
  sendMailMessage,
} = vi.hoisted(() => ({
  fetchAgents: vi.fn(),
  fetchMailboxes: vi.fn(),
  fetchMailFolders: vi.fn(),
  fetchMailMessages: vi.fn(),
  fetchMailMessage: vi.fn(),
  fetchMailSummary: vi.fn(),
  markMailSeen: vi.fn(),
  saveMailDraft: vi.fn(),
  sendMailDraft: vi.fn(),
  sendMailMessage: vi.fn(),
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, fetchAgents, fetchMailboxes }
})

vi.mock('@/lib/api/mail', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api/mail')>()
  return {
    ...actual,
    fetchMailFolders,
    fetchMailMessages,
    fetchMailMessage,
    fetchMailSummary,
    markMailSeen,
    saveMailDraft,
    sendMailDraft,
    sendMailMessage,
  }
})

import { useUiStore } from '@/store/ui'
import { MailPanel } from './MailPanel'

const FOLDERS_OK = {
  folders: [
    { slug: 'inbox', display_name: 'INBOX', total: 1, unread_count: 0 },
    { slug: 'sent', display_name: 'Sent', total: 1, unread_count: null },
    { slug: 'drafts', display_name: 'Drafts', total: 3, unread_count: null },
  ],
}

// Subject names are word-based (not "Draft 1"/"Draft 2") on purpose: the
// list row's accessible name concatenates the subject and timestamp spans
// with no separating whitespace text node (confirmed via
// dom-accessibility-api: "Draft 1" immediately followed by "10:00" computes
// to "Draft 110:00...", not "Draft 1 10:00..."), so a numeric subject would
// make `\b`-anchored name matching unreliable for reasons unrelated to the
// regression under test.
const DRAFT_SUBJECTS = ['Draft Alpha', 'Draft Bravo', 'Draft Charlie']

function draftSummary(n: number) {
  return {
    message_id: `<draft-${n}@example.test>`,
    uid: n,
    uidvalidity: 1,
    folder: 'drafts' as const,
    subject: DRAFT_SUBJECTS[n - 1],
    from: 'mailbox@test.local',
    from_name: null,
    to: ['ada@example.test'],
    cc: [],
    date: '2026-09-29T10:00:00Z',
    seen: true,
    is_draft: true,
    is_omnipus_draft: true,
    read_by_agent: false,
  }
}

const DRAFT_MESSAGES = {
  messages: [draftSummary(1), draftSummary(2), draftSummary(3)],
  truncated: false,
  next_before_uid: null,
}

function draftDetail(n: number) {
  return {
    ...draftSummary(n),
    reply_to: null,
    in_reply_to: null,
    references: null,
    body_text: 'Please review.',
    has_html: false,
    bcc: [],
    attachments: [],
    body_markdown: 'Please review.',
    markdown_lossy: false,
    draft_cleanup_warning: null,
  }
}

function renderPanel(client: QueryClient, workspaceId: string) {
  return render(
    <QueryClientProvider client={client}>
      <MailPanel workspaceId={workspaceId} />
    </QueryClientProvider>,
  )
}

describe('MailPanel — Drafts survives a workspace round-trip (f5f6-round2 Item 1)', () => {
  beforeEach(() => {
    fetchAgents.mockReset()
    fetchMailboxes.mockReset()
    fetchMailFolders.mockReset()
    fetchMailMessages.mockReset()
    fetchMailMessage.mockReset()
    fetchMailSummary.mockReset()
    markMailSeen.mockReset()
    saveMailDraft.mockReset()
    sendMailDraft.mockReset()
    sendMailMessage.mockReset()
    sessionStorage.clear()
    useUiStore.setState({ toasts: [] })

    fetchAgents.mockResolvedValue([{ figure: 'Omnipus', role: 'general', id: 'mia', name: 'Mia' }])
    // ws-A and ws-C each have their own configured mailbox for 'mia'; ws-B
    // has none (US-3 AS-3's "no mailbox" empty state).
    fetchMailboxes.mockResolvedValue([
      { agent_id: 'mia', workspace_id: 'ws-A', enabled: true, configured: true, username: 'mia@a.test' },
      { agent_id: 'mia', workspace_id: 'ws-C', enabled: true, configured: true, username: 'mia@c.test' },
    ])
    fetchMailFolders.mockImplementation((workspaceId: string) => {
      if (workspaceId === 'ws-C') {
        return Promise.reject(Object.assign(new Error('down'), { code: 'connect_refused' }))
      }
      return Promise.resolve(FOLDERS_OK)
    })
    fetchMailMessages.mockResolvedValue(DRAFT_MESSAGES)
    fetchMailMessage.mockImplementation((_ws: string, _agent: string, _folder: string, ref: string) => {
      const uid = Number(ref.split(':')[2])
      return Promise.resolve(draftDetail(uid))
    })
    fetchMailSummary.mockResolvedValue({ items: [] })
    markMailSeen.mockResolvedValue(undefined)
    saveMailDraft.mockImplementation((_ws: string, _agent: string, _ref: string, body: { body_markdown: string }) =>
      Promise.resolve({ ...draftDetail(1), body_markdown: body.body_markdown }),
    )
    sendMailDraft.mockResolvedValue({ message_id: '<draft-1@example.test>', sent_saved: true, save_warning: null, draft_cleanup_warning: null })
    sendMailMessage.mockResolvedValue({ message_id: '<new@example.test>', sent_saved: true, save_warning: null, draft_cleanup_warning: null })
  })

  afterEach(() => {
    cleanup()
    useUiStore.setState({ toasts: [] })
  })

  it('re-opening Drafts after A -> B -> C -> A round trip still shows the real drafts', async () => {
    // Mirrors the app's real singleton defaults (src/lib/queryClient.ts) —
    // staleTime 30_000 — rather than react-query's own staleTime:0, since
    // that setting is part of the mechanism under test.
    const client = new QueryClient({
      defaultOptions: { queries: { staleTime: 30_000, retry: false } },
    })
    const { rerender } = renderPanel(client, 'ws-A')

    const LONG = { timeout: 5000 }

    // Land on Drafts and open one (mirrors "open a Drafts item").
    const draftsTab = await screen.findByRole('tab', { name: 'Drafts' }, LONG)
    fireEvent.click(draftsTab)
    const draftButton = await screen.findByRole('button', { name: /Draft Alpha/ }, LONG)
    fireEvent.click(draftButton)

    // Edit -> Save -> Send (the mutating cycle preceding the switch).
    fireEvent.click(await screen.findByRole('button', { name: 'Edit' }, LONG))
    const body = await screen.findByRole('textbox', { name: 'Message' }, LONG)
    fireEvent.change(body, { target: { value: 'Please review. Updated.' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(saveMailDraft).toHaveBeenCalled(), LONG)
    const sendButton = await screen.findByRole('button', { name: /^Send$/ }, LONG)
    fireEvent.click(sendButton)
    await waitFor(() => expect(sendMailDraft).toHaveBeenCalled(), LONG)
    await waitFor(() => expect(useUiStore.getState().toasts).toEqual(expect.arrayContaining([
      expect.objectContaining({ message: 'Draft sent', variant: 'success' }),
    ])), LONG)

    // Navigate away: workspace B (no mailbox) then workspace C (unreachable
    // mailbox) — same MailPanel instance, new workspaceId prop each time
    // (F3: re-adoption never remounts).
    rerender(
      <QueryClientProvider client={client}>
        <MailPanel workspaceId="ws-B" />
      </QueryClientProvider>,
    )
    await screen.findByTestId('mail-choose-mailbox', undefined, LONG)

    rerender(
      <QueryClientProvider client={client}>
        <MailPanel workspaceId="ws-C" />
      </QueryClientProvider>,
    )
    await screen.findByTestId('mail-folders-error', undefined, LONG)

    // Back to A.
    rerender(
      <QueryClientProvider client={client}>
        <MailPanel workspaceId="ws-A" />
      </QueryClientProvider>,
    )

    await screen.findByRole('tab', { name: 'Drafts' }, LONG)
    // The regression: Drafts renders "No messages" even though the mock
    // ALWAYS resolves the same 3-message list — proving (if it reproduces)
    // that this is a client-side display/cache bug, not a genuinely empty
    // backend response.
    await waitFor(() => {
      expect(screen.queryByText('No messages')).not.toBeInTheDocument()
    }, LONG)
    expect(await screen.findByText(/Draft Bravo/, undefined, LONG)).toBeInTheDocument()
  })
})

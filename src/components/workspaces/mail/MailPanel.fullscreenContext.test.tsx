/**
 * RED contract — fullscreen `#/panel/mail` route carries Mail's context
 * (workspaceId, mailboxId, folder, messageRef) into Mail's CONTENT in BOTH
 * presentations. Without this, the shell-owned chrome-less full-screen
 * route opens, but the Mail content inside it shows the "Choose a mailbox"
 * placeholder + an empty list — same posture as a docked panel opened
 * without any panel context — so the URL's mailbox/folder/message are
 * silently lost.
 *
 * This integration suite drives the actual MailPanel through MailPanelContent
 * (mailPanelDefinition.content) with presentation='fullscreen' and the full
 * context — the exact seam the shell route invokes from
 * `_fullscreen.panel.$panelId.tsx`. The same test is also driven with
 * presentation='docked' so the contract holds in BOTH presentations.
 */
import { Suspense } from 'react'
import {
  cleanup,
  render,
  screen,
  waitFor,
} from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  afterEach,
  beforeEach,
  describe,
  expect,
  it,
  vi,
} from 'vitest'
import type {
  PanelContentProps,
  WorkspacePanelContext,
} from '@/components/panel-shell/types'

const {
  fetchAgents,
  fetchMailboxes,
  fetchMailFolders,
  fetchMailMessages,
  fetchMailMessage,
  fetchMailSummary,
} = vi.hoisted(() => ({
  fetchAgents: vi.fn(),
  fetchMailboxes: vi.fn(),
  fetchMailFolders: vi.fn(),
  fetchMailMessages: vi.fn(),
  fetchMailMessage: vi.fn(),
  fetchMailSummary: vi.fn(),
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents,
    fetchMailboxes,
  }
})

vi.mock('@/lib/api/mail', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api/mail')>()
  return {
    ...actual,
    fetchMailFolders,
    fetchMailMessages,
    fetchMailMessage,
    fetchMailSummary,
  }
})

import { mailPanelDefinition } from './mailPanelDefinition'

const MESSAGE_REF = 'uid:777:42'
const MESSAGE_BODY = 'Quarterly notes follow.'
const FULLSCREEN_CONTEXT: WorkspacePanelContext = {
  workspaceId: 'ws-1',
  mailboxId: 'mia',
  folder: 'inbox',
  messageRef: MESSAGE_REF,
}
const MESSAGE_DETAIL = {
  message_id: '<quarterly@example.test>',
  uid: 42,
  uidvalidity: 777,
  folder: 'inbox',
  subject: 'Quarterly',
  from: 'ada@example.test',
  from_name: 'Ada',
  to: ['mia@example.test'],
  cc: [],
  date: '2026-09-28T10:00:00Z',
  seen: true,
  is_draft: false,
  is_omnipus_draft: false,
  read_by_agent: false,
  body_markdown: MESSAGE_BODY,
  body_text: MESSAGE_BODY,
  has_html: false,
  attachments: [],
}

function renderMail(
  context: WorkspacePanelContext,
  presentation: PanelContentProps['presentation'],
) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const Content = mailPanelDefinition.content
  return render(
    <QueryClientProvider client={client}>
      <Suspense fallback={<div>Loading Mail…</div>}>
        <Content
          context={context}
          presentation={presentation}
          close={() => undefined}
          expand={() => undefined}
          registerExpandContext={() => undefined}
          onWidthSettle={() => undefined}
        />
      </Suspense>
    </QueryClientProvider>,
  )
}

describe('Mail fullscreen carries context into Mail content (R10/R11)', () => {
  beforeEach(() => {
    fetchAgents.mockReset().mockResolvedValue([{ figure: 'Omnipus', role: 'general', id: 'mia', name: 'Mia' }])
    fetchMailboxes.mockReset().mockResolvedValue([
      {
        agent_id: 'mia',
        workspace_id: 'ws-1',
        enabled: true,
        configured: true,
        username: 'mia@test.local',
      },
    ])
    fetchMailFolders.mockReset().mockResolvedValue({
      folders: [
        { slug: 'inbox', display_name: 'Inbox', total: 1, unread_count: 0 },
      ],
    })
    fetchMailMessages.mockReset().mockResolvedValue({
      messages: [
        {
          message_id: '<quarterly@example.test>',
          uid: 42,
          uidvalidity: 777,
          folder: 'inbox',
          subject: 'Quarterly',
          from: 'ada@example.test',
          seen: true,
          read_by_agent: false,
        },
      ],
      truncated: false,
      next_before_uid: null,
    })
    fetchMailMessage.mockReset().mockResolvedValue(MESSAGE_DETAIL)
    fetchMailSummary.mockReset().mockResolvedValue({ items: [] })
  })

  afterEach(() => cleanup())

  it('selects the URL-named mailbox, opens the URL-named message and renders its body — fullscreen', async () => {
    renderMail(FULLSCREEN_CONTEXT, 'fullscreen')

    // Wait for the lazy-loaded MailPanel to mount and resolve its layout
    // (the cold chunk path takes 3.7-11s on first import — matches the
    // precedent in MailPanel.unsavedLeave.test.tsx).
    await waitFor(
      () => {
        const selector = screen.getByRole('combobox', { name: 'Mailbox' })
        expect(selector).toHaveTextContent('Mia')
        expect(selector).not.toHaveTextContent(/choose a mailbox/i)
      },
      { timeout: 30000 },
    )

    await waitFor(
      () => {
        expect(fetchMailMessage).toHaveBeenCalledWith(
          'ws-1',
          'mia',
          'inbox',
          MESSAGE_REF,
          expect.objectContaining({ retry: false }),
        )
      },
      { timeout: 30000 },
    )

    expect(await screen.findByText(MESSAGE_BODY, {}, { timeout: 30000 })).toBeInTheDocument()
  })

  it('selects the URL-named mailbox, opens the URL-named message and renders its body — docked', async () => {
    renderMail(FULLSCREEN_CONTEXT, 'docked')

    await waitFor(
      () => {
        const selector = screen.getByRole('combobox', { name: 'Mailbox' })
        expect(selector).toHaveTextContent('Mia')
        expect(selector).not.toHaveTextContent(/choose a mailbox/i)
      },
      { timeout: 30000 },
    )

    await waitFor(
      () => {
        expect(fetchMailMessage).toHaveBeenCalledWith(
          'ws-1',
          'mia',
          'inbox',
          MESSAGE_REF,
          expect.objectContaining({ retry: false }),
        )
      },
      { timeout: 30000 },
    )

    expect(await screen.findByText(MESSAGE_BODY, {}, { timeout: 30000 })).toBeInTheDocument()
  })
})

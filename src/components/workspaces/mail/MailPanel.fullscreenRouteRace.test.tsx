// Exercise the shell's search -> panel -> registered context -> search path
// with the real MailPanel and a mailbox response that settles after mount.
import { Suspense, useCallback, useState } from 'react'
import { act, cleanup, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { PanelContentProps } from '@/components/panel-shell/types'

const { fetchAgents, fetchMailboxes, fetchMailFolders, fetchMailMessages, fetchMailMessage, fetchMailSummary } = vi.hoisted(() => ({
  fetchAgents: vi.fn(),
  fetchMailboxes: vi.fn(),
  fetchMailFolders: vi.fn(),
  fetchMailMessages: vi.fn(),
  fetchMailMessage: vi.fn(),
  fetchMailSummary: vi.fn(),
}))

vi.mock('@/lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api')>()),
  fetchAgents,
  fetchMailboxes,
}))
vi.mock('@/lib/api/mail', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api/mail')>()),
  fetchMailFolders,
  fetchMailMessages,
  fetchMailMessage,
  fetchMailSummary,
}))

import { mailPanelDefinition } from './mailPanelDefinition'

const URL_SEARCH = Object.fromEntries(new URLSearchParams(
  '#/panel/mail?workspace=ws-1&mailbox=mia&folder=inbox&message=uid%3A1%3A2'.split('?')[1],
))
const MAILBOXES = [
  // The wrong workspace comes first, so choosing without filtering cannot pass.
  { agent_id: 'mia', workspace_id: 'ws-other', enabled: true, configured: true, username: 'other@test.local' },
  { agent_id: 'mia', workspace_id: 'ws-1', enabled: true, configured: true, username: 'mia@test.local' },
]
const MESSAGE_BODY = 'The selected inbox message.'
const MESSAGE_DETAIL = {
  message_id: '<selected@example.test>', uid: 2, uidvalidity: 1, folder: 'inbox',
  subject: 'Selected', from: 'ada@example.test', from_name: 'Ada',
  to: ['mia@test.local'], cc: [], date: '2026-09-29T10:00:00Z',
  seen: true, is_draft: false, is_omnipus_draft: false, read_by_agent: false,
  body_markdown: MESSAGE_BODY, body_text: MESSAGE_BODY, has_html: false, attachments: [],
}

function ShellSearchHarness() {
  const [search, setSearch] = useState(URL_SEARCH)
  const context = mailPanelDefinition.fullScreen.fromSearch(search)
  const registerExpandContext = useCallback<PanelContentProps['registerExpandContext']>((getter) => {
    if (getter) setSearch(mailPanelDefinition.fullScreen.toSearch(getter()))
  }, [])
  if (context === null) throw new Error('Mail fullscreen search unexpectedly rejected')
  const Content = mailPanelDefinition.content
  return (
    <>
      <output data-testid="fullscreen-search">{JSON.stringify(search)}</output>
      <Suspense fallback={<div>Loading Mail…</div>}>
        <Content
          context={context}
          presentation="fullscreen"
          close={() => undefined}
          expand={() => undefined}
          registerExpandContext={registerExpandContext}
          onWidthSettle={() => undefined}
        />
      </Suspense>
    </>
  )
}

describe('Mail fullscreen search preserves the requested mailbox during startup', () => {
  let resolveMailboxRequest: () => void

  beforeEach(() => {
    sessionStorage.clear()
    fetchMailboxes.mockReset().mockImplementation(() => new Promise((resolve) => {
      resolveMailboxRequest = () => resolve(MAILBOXES)
    }))
    fetchAgents.mockReset().mockResolvedValue([{ id: 'mia', name: 'Mia' }])
    fetchMailFolders.mockReset().mockResolvedValue({
      folders: [{ slug: 'inbox', display_name: 'Inbox', total: 1, unread_count: 0 }],
    })
    fetchMailMessages.mockReset().mockResolvedValue({
      messages: [{ message_id: '<selected@example.test>', uid: 2, uidvalidity: 1,
        folder: 'inbox', subject: 'Selected', from: 'ada@example.test', seen: true,
        read_by_agent: false }],
      truncated: false, next_before_uid: null,
    })
    fetchMailMessage.mockReset().mockResolvedValue(MESSAGE_DETAIL)
    fetchMailSummary.mockReset().mockResolvedValue({ items: [] })
  })
  afterEach(() => cleanup())

  it('keeps Mia and uid:1:2 in the URL, selects the workspace mailbox, and shows list beside message', async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(<QueryClientProvider client={client}><ShellSearchHarness /></QueryClientProvider>)
    // The real MailPanel is lazy-loaded and the cold module graph takes seconds.
    await waitFor(() => expect(fetchMailboxes).toHaveBeenCalledTimes(1), { timeout: 30000 })
    const search = screen.getByTestId('fullscreen-search')
    expect(JSON.parse(search.textContent ?? '')).toEqual(URL_SEARCH)

    await act(async () => { resolveMailboxRequest() })
    await waitFor(() => expect(screen.getByRole('combobox', { name: 'Mailbox' })).toHaveTextContent('Mia · mia@test.local'))
    expect(screen.getByRole('combobox', { name: 'Mailbox' })).not.toHaveTextContent('other@test.local')
    expect(screen.getByTestId('mail-list-preview-layout')).toHaveAttribute('data-layout', 'split')
    expect(screen.getByTestId('mail-list-region')).toBeInTheDocument()
    expect(screen.getByTestId('mail-reading-zone')).toBeInTheDocument()
    expect(fetchMailMessage).toHaveBeenCalledWith('ws-1', 'mia', 'inbox', 'uid:1:2', { retry: false })
    expect(await screen.findByText(MESSAGE_BODY)).toBeInTheDocument()
    expect(JSON.parse(search.textContent ?? '')).toEqual(URL_SEARCH)
  })
})

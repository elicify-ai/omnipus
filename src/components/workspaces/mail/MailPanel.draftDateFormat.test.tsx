import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { MailMessage } from '@/lib/api/generated/openapi-types'
import { MailPanel } from './MailPanel'

// F8: the draft header must omit unavailable dates just as the message-list row does.
const { fetchAgents, fetchMailboxes, fetchMailFolders, fetchMailMessages, fetchMailMessage, fetchMailSummary } = vi.hoisted(() => ({
  fetchAgents: vi.fn(), fetchMailboxes: vi.fn(), fetchMailFolders: vi.fn(),
  fetchMailMessages: vi.fn(), fetchMailMessage: vi.fn(), fetchMailSummary: vi.fn(),
}))
vi.mock('@/lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api')>()), fetchAgents, fetchMailboxes,
}))
vi.mock('@/lib/api/mail', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api/mail')>()),
  fetchMailFolders, fetchMailMessages, fetchMailMessage, fetchMailSummary,
}))

const draft: MailMessage = {
  message_id: '<draft@example.test>', uid: 4, uidvalidity: 77, folder: 'drafts',
  subject: 'Review request', from: 'mia@example.test', from_name: 'Mia',
  to: ['alice@example.test'], cc: [], bcc: null,
  date: '2026-01-29T12:00:00Z', seen: true, is_draft: true, is_omnipus_draft: true,
  read_by_agent: false, reply_to: null, in_reply_to: null, references: null,
  body_text: 'Agent draft', has_html: false, body_markdown: 'Agent draft', markdown_lossy: false,
  attachments: [],
}

function renderDraft(date: string) {
  const message = { ...draft, date }
  fetchMailMessages.mockResolvedValue({ messages: [message], truncated: false, next_before_uid: null })
  fetchMailMessage.mockResolvedValue(message)
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<QueryClientProvider client={client}><MailPanel workspaceId="ws-1" initialFolder="drafts" /></QueryClientProvider>)
}

async function openDraft() {
  fireEvent.click(await screen.findByRole('button', { name: /Review request/ }, { timeout: 30000 }))
  const readingZone = screen.getByTestId('mail-reading-zone')
  await within(readingZone).findByRole('button', { name: 'Edit' })
  return readingZone
}

function draftHeader(readingZone: HTMLElement) {
  return within(readingZone).getByText(/^From mia@example\.test/)
}

describe('MailPanel — unavailable dates in the opened draft header (F8)', () => {
  beforeEach(() => {
    sessionStorage.clear()
    fetchAgents.mockReset().mockResolvedValue([{ id: 'mia', name: 'Mia' }])
    fetchMailboxes.mockReset().mockResolvedValue([{ agent_id: 'mia', workspace_id: 'ws-1', enabled: true, configured: true, username: 'mia@example.test' }])
    fetchMailFolders.mockReset().mockResolvedValue({ folders: [{ slug: 'drafts', display_name: 'Drafts', total: 1, unread_count: null }] })
    fetchMailMessages.mockReset()
    fetchMailMessage.mockReset()
    fetchMailSummary.mockReset().mockResolvedValue({ items: [] })
  })
  afterEach(() => cleanup())

  it('keeps a genuine draft date visible in the opened header', async () => {
    renderDraft('2026-01-29T12:00:00Z')
    const readingZone = await openDraft()
    expect(draftHeader(readingZone).textContent?.trim()).toBe('From mia@example.test · 29 Jan 2026')

    fireEvent.click(within(readingZone).getByRole('button', { name: 'Edit' }))
    await within(readingZone).findByRole('button', { name: 'Save' })
    expect(draftHeader(readingZone).textContent?.trim()).toBe('From mia@example.test · 29 Jan 2026')
  })

  it.each([
    ['server zero date', '0001-01-01T00:00:00Z'],
    ['malformed date', 'not-a-date'],
    ['missing date', ''],
  ])('omits a %s and its separator in both draft preview and editor headers', async (_case, date) => {
    renderDraft(date)
    const readingZone = await openDraft()
    expect(draftHeader(readingZone).textContent?.trim()).toBe('From mia@example.test')

    fireEvent.click(within(readingZone).getByRole('button', { name: 'Edit' }))
    await within(readingZone).findByRole('button', { name: 'Save' })
    expect(draftHeader(readingZone).textContent?.trim()).toBe('From mia@example.test')
  })
})

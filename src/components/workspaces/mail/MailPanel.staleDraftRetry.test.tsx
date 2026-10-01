// Delta-4 finding 2 / MC-33: a reading-pane Retry is human initiated across
// UID lookup, Message-ID recovery and the final draft-list refresh. Automatic
// recovery must not bypass watcher backoff. Mock only the HTTP boundary.
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Mailbox, MailFolderList, MailMessagePage, MailMessageSummary } from '@/lib/api/generated/openapi-types'
import { MailPanel } from './MailPanel'
import { clearMailPanelIntent } from './mailPanelIntent'

const mailbox: Mailbox = {
  agent_id: 'mia', workspace_id: 'ws-retry', enabled: true, configured: true, username: 'mia@test.local',
}
const folders: MailFolderList = { folders: [
  { slug: 'drafts', display_name: 'Drafts', total: 1, unread_count: null },
] }
const staleRow: MailMessageSummary = {
  message_id: '<stale-draft@test.local>', uid: 1, uidvalidity: 3,
  folder: 'drafts', subject: 'Missing draft', from: 'mia@test.local', from_name: null,
  to: [], cc: [], date: '2026-09-30T00:00:00Z', seen: false,
  is_draft: true, is_omnipus_draft: true, read_by_agent: false,
}
const stalePage: MailMessagePage = { messages: [staleRow], truncated: false, next_before_uid: null }
const emptyPage: MailMessagePage = { messages: [], truncated: false, next_before_uid: null }
const messagesPath = '/api/v1/workspaces/ws-retry/mail/mia/folders/drafts/messages'
// Literal URLs derived from the fixture identifiers, not the serializer under test.
const uidPath = `${messagesPath}/uid%3A3%3A1`
const midPath = `${messagesPath}/mid%3A%3Cstale-draft%40test.local%3E`
const changedMessage = 'This draft was changed or deleted elsewhere. The list has been refreshed.'
let requests: string[]
let persistedBackoff: boolean
let backoffOnMidLookup: boolean
let failRefresh: boolean

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'content-type': 'application/json' } })
}

function backoffResponse() {
  return json({ error: 'Mailbox is backing off', code: 'backoff' }, 503)
}

function renderDraft() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<QueryClientProvider client={client}><MailPanel workspaceId="ws-retry" mailboxId="mia" initialFolder="drafts" /></QueryClientProvider>)
}

function readRequests() {
  return requests.filter((url) => url.startsWith(messagesPath))
}

async function openDuringBackoff() {
  renderDraft()
  const row = await screen.findByRole('button', { name: /Missing draft/ })
  // The visible row was fetched while healthy. The watcher subsequently
  // records backoff; it stays active even though the mail server has recovered.
  persistedBackoff = true
  fireEvent.click(row)
  const readingPane = within(screen.getByTestId('mail-reading-zone'))
  expect(await readingPane.findByText('backoff')).toBeInTheDocument()
  expect(readRequests()).toEqual([messagesPath, uidPath])
  return readingPane
}

beforeEach(() => {
  requests = []
  persistedBackoff = false
  backoffOnMidLookup = false
  failRefresh = false
  clearMailPanelIntent('ws-retry')
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const url = new URL(String(input), 'http://omnipus.test')
    const path = url.pathname
    requests.push(`${path}${url.search}`)
    if (path === '/api/v1/agents') return json([])
    if (path === '/api/v1/mailboxes') return json({ mailboxes: [mailbox] })
    if (path === '/api/v1/workspaces/ws-retry/mail/summary') return json({ items: [] })
    if (path === '/api/v1/workspaces/ws-retry/mail/mia/folders') return json(folders)
    if (path === messagesPath) {
      if (persistedBackoff && url.searchParams.get('retry') !== 'true') return backoffResponse()
      if (failRefresh) return json({ error: 'Draft list unavailable', code: 'server_error' }, 502)
      return json(persistedBackoff ? emptyPage : stalePage)
    }
    if (path === uidPath || path === midPath) {
      if (persistedBackoff && url.searchParams.get('retry') !== 'true') return backoffResponse()
      if (path === midPath && backoffOnMidLookup) persistedBackoff = true
      return json({ error: 'Draft not found', code: 'not_found' }, 404)
    }
    throw new Error(`Unexpected request: ${path}${url.search}`)
  })
})

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  clearMailPanelIntent('ws-retry')
})

describe('MailPanel — human Retry through stale-draft recovery', () => {
  it('marks the final list refresh human and explains the missing draft instead of backoff', async () => {
    const readingPane = await openDuringBackoff()

    fireEvent.click(readingPane.getByRole('button', { name: 'Retry' }))

    await waitFor(() => expect(readRequests()).toEqual([
      messagesPath, uidPath,
      `${uidPath}?retry=true`, `${midPath}?retry=true`, `${messagesPath}?retry=true`,
    ]))
    expect(await readingPane.findByText(changedMessage)).toBeInTheDocument()
    expect(await screen.findByText('No messages')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Missing draft/ })).not.toBeInTheDocument()
    expect(readingPane.queryByText('backoff')).not.toBeInTheDocument()
  })

  it('does not bypass backoff when recovery was automatic', async () => {
    backoffOnMidLookup = true
    renderDraft()

    fireEvent.click(await screen.findByRole('button', { name: /Missing draft/ }))

    const readingPane = within(screen.getByTestId('mail-reading-zone'))
    expect(await readingPane.findByText('backoff')).toBeInTheDocument()
    expect(readRequests()).toEqual([messagesPath, uidPath, midPath, messagesPath])
    expect(screen.getByRole('button', { name: /Missing draft/ })).toBeInTheDocument()
    expect(readingPane.queryByText(changedMessage)).not.toBeInTheDocument()
    expect(screen.queryByText('No messages')).not.toBeInTheDocument()
  })

  it('keeps a genuine refresh error visible after human Retry without claiming the list refreshed', async () => {
    const readingPane = await openDuringBackoff()
    failRefresh = true

    fireEvent.click(readingPane.getByRole('button', { name: 'Retry' }))

    await waitFor(() => expect(readRequests()).toEqual([
      messagesPath, uidPath,
      `${uidPath}?retry=true`, `${midPath}?retry=true`, `${messagesPath}?retry=true`,
    ]))
    expect(await readingPane.findByText('server_error')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Missing draft/ })).toBeInTheDocument()
    expect(readingPane.queryByText(changedMessage)).not.toBeInTheDocument()
    expect(readingPane.queryByText('backoff')).not.toBeInTheDocument()
    expect(screen.queryByText('No messages')).not.toBeInTheDocument()
  })
})

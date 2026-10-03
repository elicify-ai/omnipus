// Delta-4 finding 2 / MC-33: a reading-pane Retry is human initiated across
// UID lookup, Message-ID recovery and the final draft-list refresh. Automatic
// recovery must not bypass watcher backoff. Mock only the HTTP boundary.
//
// Superseded oracles re-pinned (W3 panel spec §11 S-11 + §8.8's re-pin list,
// §2.4/§3.2 cache-first list reads; landed in a3a678b34, 2026-10-02): the
// recovery explanation is S-11's "This message changed or was deleted. Refresh
// the list." (the drafts-era string is gone); the failure surface names its
// class as "Error class: <class>" (S-10); the open list read carries
// mode=cache_first and an explicit limit=25. Under S-11 the list updates only
// when the user clicks the explanation's "Refresh list" control, so the
// empty-list outcome is asserted after that click.
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
const messageChangedNotice = 'This message changed or was deleted. Refresh the list.'
let requests: string[]
let persistedBackoff: boolean
let backoffOnMidLookup: boolean
let failRefresh: boolean
// The scenario's premise: the draft really was deleted elsewhere. The watcher's
// backoff is a separate, transient belief, so the fixture tracks the draft's
// absence independently (a human-retry mid lookup that 404s proves it gone) —
// the old fixture conflated "backing off" with "draft gone".
let draftGone: boolean

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
  // S-10 surface: the class rides the caption line, "Error class: backoff".
  expect(await readingPane.findByText('Error class: backoff')).toBeInTheDocument()
  // The open event's cache-first read carries mode=cache_first and the
  // explicit limit=25 (W3 spec §2.4/§3.2); the detail read stays a bare path.
  expect(readRequests()).toEqual([`${messagesPath}?limit=25&mode=cache_first`, uidPath])
  return readingPane
}

beforeEach(() => {
  requests = []
  persistedBackoff = false
  backoffOnMidLookup = false
  failRefresh = false
  draftGone = false
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
      return json(draftGone ? emptyPage : stalePage)
    }
    if (path === uidPath || path === midPath) {
      if (persistedBackoff && url.searchParams.get('retry') !== 'true') return backoffResponse()
      if (path === midPath && backoffOnMidLookup) persistedBackoff = true
      if (path === midPath && url.searchParams.get('retry') === 'true') draftGone = true
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
      `${messagesPath}?limit=25&mode=cache_first`, uidPath,
      `${uidPath}?retry=true`, `${midPath}?retry=true`, `${messagesPath}?retry=true`,
    ]))
    // S-11: the pane explains the missing draft and hands the list update to
    // the user's own "Refresh list" control — it never mutates the list
    // behind the explanation.
    expect(await readingPane.findByText(messageChangedNotice)).toBeInTheDocument()
    // The watcher's backoff window expires; the server has recovered (the
    // scenario's premise). The user's refresh then shows the folder's truth.
    persistedBackoff = false
    fireEvent.click(readingPane.getByRole('button', { name: 'Refresh list' }))
    expect(await screen.findByText('No messages')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Missing draft/ })).not.toBeInTheDocument()
    expect(readingPane.queryByText('Error class: backoff')).not.toBeInTheDocument()
  })

  it('does not bypass backoff when recovery was automatic', async () => {
    backoffOnMidLookup = true
    renderDraft()

    fireEvent.click(await screen.findByRole('button', { name: /Missing draft/ }))

    const readingPane = within(screen.getByTestId('mail-reading-zone'))
    expect(await readingPane.findByText('Error class: backoff')).toBeInTheDocument()
    // The automatic recovery refresh dials the bare path: no retry=true, so
    // watcher backoff still answers it (that is the point of this test).
    expect(readRequests()).toEqual([`${messagesPath}?limit=25&mode=cache_first`, uidPath, midPath, messagesPath])
    expect(screen.getByRole('button', { name: /Missing draft/ })).toBeInTheDocument()
    expect(readingPane.queryByText(messageChangedNotice)).not.toBeInTheDocument()
    expect(screen.queryByText('No messages')).not.toBeInTheDocument()
  })

  it('keeps a genuine refresh error visible after human Retry without claiming the list refreshed', async () => {
    const readingPane = await openDuringBackoff()
    failRefresh = true

    fireEvent.click(readingPane.getByRole('button', { name: 'Retry' }))

    await waitFor(() => expect(readRequests()).toEqual([
      `${messagesPath}?limit=25&mode=cache_first`, uidPath,
      `${uidPath}?retry=true`, `${midPath}?retry=true`, `${messagesPath}?retry=true`,
    ]))
    expect(await readingPane.findByText('Error class: server_error')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Missing draft/ })).toBeInTheDocument()
    expect(readingPane.queryByText(messageChangedNotice)).not.toBeInTheDocument()
    expect(readingPane.queryByText('Error class: backoff')).not.toBeInTheDocument()
    expect(screen.queryByText('No messages')).not.toBeInTheDocument()
  })
})

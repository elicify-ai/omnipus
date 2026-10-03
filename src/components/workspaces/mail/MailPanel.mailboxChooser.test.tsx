// Delta-4 finding 1 / SP-23: a bare cross-workspace link requests the
// chooser, not a remembered mailbox. A subsequent picker gesture must
// activate the chosen mailbox, even when the saved intent already names it.
// Keep the Select, query hooks, storage and HTTP clients real; mock fetch only.
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Mailbox, MailFolderList, MailMessagePage } from '@/lib/api/generated/openapi-types'
import { MailPanel, type MailPanelProps } from './MailPanel'
import { clearMailPanelIntent, writeMailPanelIntent } from './mailPanelIntent'

const mailboxes: Mailbox[] = [
  { agent_id: 'mia', workspace_id: 'ws-target', enabled: true, configured: true, username: 'mia@target.test' },
  { agent_id: 'cleo', workspace_id: 'ws-target', enabled: true, configured: true, username: 'cleo@target.test' },
  { agent_id: 'cleo', workspace_id: 'ws-other', enabled: true, configured: true, username: 'cleo@other.test' },
]
const folders: MailFolderList = { folders: [
  { slug: 'inbox', display_name: 'INBOX', total: 0, unread_count: 0 },
  { slug: 'sent', display_name: 'Sent', total: 0, unread_count: null },
  { slug: 'drafts', display_name: 'Drafts', total: 0, unread_count: null },
] }
const emptyPage: MailMessagePage = { messages: [], truncated: false, next_before_uid: null }
const originalScrollIntoView = Element.prototype.scrollIntoView
let requests: string[]

function json(body: unknown) {
  return new Response(JSON.stringify(body), { status: 200, headers: { 'content-type': 'application/json' } })
}

function renderPanel(props: MailPanelProps = { workspaceId: 'ws-target', mailboxId: null }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const view = render(<QueryClientProvider client={client}><MailPanel {...props} /></QueryClientProvider>)
  return {
    ...view,
    rerenderPanel: (next: MailPanelProps) => view.rerender(
      <QueryClientProvider client={client}><MailPanel {...next} /></QueryClientProvider>,
    ),
  }
}

function dialingRequests() {
  return requests.filter((path) => path.includes('/mail/') && !path.endsWith('/summary'))
}

async function selectCleo() {
  fireEvent.click(screen.getByRole('combobox', { name: 'Mailbox' }))
  fireEvent.click(await screen.findByRole('option', { name: 'cleo · cleo@target.test' }))
  // Waiting for the rendered empty list also proves the folders request
  // succeeded and the dependent messages query completed.
  expect(await screen.findByText('No messages')).toBeInTheDocument()
  expect(screen.getByText('INBOX')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Compose' })).toBeEnabled()
  expect(screen.getByRole('combobox', { name: 'Mailbox' }).textContent).toBe('cleo · cleo@target.test')
}

beforeEach(() => {
  requests = []
  clearMailPanelIntent('ws-target')
  clearMailPanelIntent('ws-other')
  Element.prototype.scrollIntoView = vi.fn()
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = new URL(String(input), 'http://omnipus.test').pathname
    requests.push(path)
    if (path === '/api/v1/agents') return json([])
    if (path === '/api/v1/mailboxes') return json({ mailboxes })
    if (path.endsWith('/mail/summary')) return json({ items: [] })
    if (path.endsWith('/folders')) return json(folders)
    if (path.endsWith('/messages')) return json(emptyPage)
    throw new Error(`Unexpected request: ${path}`)
  })
})

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  Element.prototype.scrollIntoView = originalScrollIntoView
  clearMailPanelIntent('ws-target')
  clearMailPanelIntent('ws-other')
})

describe('MailPanel — bare-link mailbox chooser accepts a human selection', () => {
  it.each([null, 'mia', 'cleo'])('ignores stored mailbox %s on landing but opens the explicitly chosen mailbox', async (storedAgentId) => {
    writeMailPanelIntent('ws-target', { agentId: storedAgentId, folder: 'inbox', messageRef: null })
    const onLocationChange = vi.fn()
    renderPanel({ workspaceId: 'ws-target', mailboxId: null, onLocationChange })
    await waitFor(() => expect(onLocationChange).toHaveBeenLastCalledWith({ mailboxId: null, folder: 'inbox', messageRef: null }))
    expect(screen.getByRole('combobox', { name: 'Mailbox' }).textContent).toBe('Choose a mailbox')
    expect(screen.getByRole('button', { name: 'Compose' })).toBeDisabled()
    expect(dialingRequests()).toEqual([])

    await selectCleo()

    expect(dialingRequests()).toEqual([
      '/api/v1/workspaces/ws-target/mail/cleo/folders',
      '/api/v1/workspaces/ws-target/mail/cleo/folders/inbox/messages',
    ])
    await waitFor(() => expect(onLocationChange).toHaveBeenLastCalledWith({ mailboxId: 'cleo', folder: 'inbox', messageRef: null }))
  })

  it('requires a new choice when a bare link remounts after an explicit selection', async () => {
    const view = renderPanel()
    await selectCleo()
    view.unmount()
    const previousRequests = dialingRequests()
    const onLocationChange = vi.fn()

    renderPanel({ workspaceId: 'ws-target', mailboxId: null, onLocationChange })
    await waitFor(() => expect(onLocationChange).toHaveBeenLastCalledWith({ mailboxId: null, folder: 'inbox', messageRef: null }))

    expect(screen.getByRole('combobox', { name: 'Mailbox' }).textContent).toBe('Choose a mailbox')
    expect(screen.getByRole('button', { name: 'Compose' })).toBeDisabled()
    expect(dialingRequests()).toEqual(previousRequests)
  })

  it('does not carry a picker acknowledgement across a cross-workspace bare-link round trip', async () => {
    const view = renderPanel()
    await selectCleo()
    const previousRequests = dialingRequests()
    const onLocationChange = vi.fn()

    // The same agent exists in both workspaces: an unscoped boolean would
    // enable the new workspace query before its reset effect can run.
    view.rerenderPanel({ workspaceId: 'ws-other', mailboxId: null, onLocationChange })
    await waitFor(() => expect(onLocationChange).toHaveBeenLastCalledWith({ mailboxId: null, folder: 'inbox', messageRef: null }))
    expect(screen.getByRole('button', { name: 'Compose' })).toBeDisabled()
    expect(dialingRequests()).toEqual(previousRequests)

    // MailPanelContent supplies a fresh callback when its context changes.
    const onReturnLocationChange = vi.fn()
    view.rerenderPanel({ workspaceId: 'ws-target', mailboxId: null, onLocationChange: onReturnLocationChange })
    await waitFor(() => expect(onReturnLocationChange).toHaveBeenLastCalledWith({ mailboxId: null, folder: 'inbox', messageRef: null }))
    expect(screen.getByRole('combobox', { name: 'Mailbox' }).textContent).toBe('Choose a mailbox')
    expect(screen.getByRole('button', { name: 'Compose' })).toBeDisabled()
    expect(dialingRequests()).toEqual(previousRequests)
  })
})

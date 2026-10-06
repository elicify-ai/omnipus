// Independent RED supplement: the commissioned two-mailbox selector requirement
// and docs/mail.md::Set up and open Mail. Explicit URL-named A is INITIAL state,
// not a permanent override of a human's later choice of eligible mailbox B.
// REAL: MailPanel, catalogued Radix Select, queries/generated HTTP validation,
// intent/store, auth, production registration and fullscreen route/context.
// PROCESS edges only: HTTP, BroadcastChannel and absent native DOM scrolling.
// jsdom runtime evidence is not native-browser or live-account UAT.
// GREEN and production mutation proof are deferred to an independent CHECK.
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, createRouter, RouterProvider } from '@tanstack/react-router'
import type {
  Mailbox, MailFolderList, MailMessage, MailMessagePage, MailMessageSummary,
  MailSummaryList,
} from '@/lib/api/generated/openapi-types'
import { routeTree } from '@/routeTree.gen'
import { usePanelShellStore } from '@/components/panel-shell/panelShellStore'
import { PanelChannelEdge, signedInState } from '../../../../tests/fixtures/pe1-panel-process-edges'
import { readMailPanelIntent, writeMailPanelIntent } from './mailPanelIntent'

const WORKSPACE = 'ws-1'
const FOREIGN_WORKSPACE = 'ws-other'
const POPOUT = 'synthetic-mailbox-switch'
const A = 'mia'
const B = 'cleo'
const LABEL_A = 'mia · a@example.test'
const LABEL_B = 'cleo · b@example.test'
const REF_A = 'uid:777:42'
const REF_B = 'uid:888:84'
const SUBJECT_A = 'Mailbox A message'
const SUBJECT_B = 'Mailbox B message'
const BODY_A = 'Synthetic message body owned by mailbox A.'
const BODY_B = 'Synthetic message body owned by mailbox B.'

const mailboxes: Mailbox[] = [
  // The foreign copy comes first so an unscoped roster cannot accidentally pass.
  { agent_id: A, workspace_id: FOREIGN_WORKSPACE, enabled: true, configured: true, username: 'foreign@example.test' },
  { agent_id: A, workspace_id: WORKSPACE, enabled: true, configured: true, username: 'a@example.test' },
  { agent_id: B, workspace_id: WORKSPACE, enabled: true, configured: true, username: 'b@example.test' },
  { agent_id: 'disabled', workspace_id: WORKSPACE, enabled: false, configured: true, username: 'disabled@example.test' },
  { agent_id: 'unconfigured', workspace_id: WORKSPACE, enabled: true, configured: false, username: 'unconfigured@example.test' },
]
const foldersA: MailFolderList = { folders: [
  { slug: 'inbox', display_name: 'A Inbox', total: 1, unread_count: 0, availability: 'present' },
] }
const foldersB: MailFolderList = { folders: [
  { slug: 'inbox', display_name: 'B Inbox', total: 1, unread_count: 0, availability: 'present' },
] }
const rowA: MailMessageSummary = {
  message_id: '<mailbox-a@example.test>', uid: 42, uidvalidity: 777,
  folder: 'inbox', subject: SUBJECT_A, from: 'sender-a@example.test', from_name: 'Sender A',
  to: ['a@example.test'], cc: [], date: '2026-10-05T10:00:00Z',
  seen: true, is_draft: false, is_omnipus_draft: false, read_by_agent: false,
}
const rowB: MailMessageSummary = {
  ...rowA, message_id: '<mailbox-b@example.test>', uid: 84, uidvalidity: 888,
  subject: SUBJECT_B, from: 'sender-b@example.test', from_name: 'Sender B', to: ['b@example.test'],
}
const detailA: MailMessage = {
  ...rowA, reply_to: null, in_reply_to: null, references: null, bcc: null,
  body_text: BODY_A, body_markdown: BODY_A, markdown_lossy: false,
  has_html: false, attachments: [],
}
const detailB: MailMessage = { ...detailA, ...rowB, body_text: BODY_B, body_markdown: BODY_B }
const pageA: MailMessagePage = {
  messages: [rowA], truncated: false, next_before_uid: null,
  has_more: false, next_cursor: null, view_limit_reached: false,
}
const pageB: MailMessagePage = { ...pageA, messages: [rowB] }
const summary: MailSummaryList = { items: [] }

// not-wire-format: observations of browser PROCESS-edge lifecycle bytes.
class ObservedChannel extends PanelChannelEdge {
  static sent: { channel: string; data: unknown }[] = []
  override postMessage(data: unknown) {
    ObservedChannel.sent.push({ channel: this.name, data: structuredClone(data) })
    super.postMessage(data)
  }
}

const requests: string[] = []
const unsupportedHttp: string[] = []
const clients: QueryClient[] = []
const originalScrollIntoView = Element.prototype.scrollIntoView

function json(body: unknown): Response {
  return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })
}

const httpEdge = vi.fn<typeof fetch>(async (input, init) => {
  const raw = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url
  const url = new URL(raw, window.location.href)
  const method = init?.method ?? (input instanceof Request ? input.method : 'GET')
  const reject = (): never => {
    const reason = `Unsupported synthetic HTTP request: ${method} ${url.href}`
    unsupportedHttp.push(reason)
    throw new Error(reason)
  }
  // Do not authenticate or connect to any real app, account, mailbox or provider.
  if (url.origin !== window.location.origin || method !== 'GET' || init?.credentials !== 'include') return reject()
  requests.push(url.pathname + url.search)
  if (url.pathname === '/api/v1/state') return json(signedInState)
  if (url.pathname === '/api/v1/agents') return json([])
  if (url.pathname === '/api/v1/mailboxes') return json({ mailboxes })
  const base = `/api/v1/workspaces/${WORKSPACE}/mail`
  if (url.pathname === `${base}/summary`) return json(summary)
  for (const [agent, folders, page, ref, detail] of [
    [A, foldersA, pageA, REF_A, detailA], [B, foldersB, pageB, REF_B, detailB],
  ] as const) {
    const folderPath = `${base}/${agent}/folders`
    const listPath = `${folderPath}/inbox/messages`
    if (url.pathname === folderPath) return json(folders)
    if (url.pathname === listPath) return json(page)
    if (decodeURIComponent(url.pathname) === `${listPath}/${ref}`) return json(detail)
  }
  return reject()
})

function dialingPaths() {
  return requests.map((path) => path.split('?')[0])
    .filter((path) => path.includes('/mail/') && !path.endsWith('/summary'))
}

function latestLiveFrame() {
  return ObservedChannel.sent
    .filter((entry) => entry.channel === 'omnipus-panel-popout-lifecycle')
    .map((entry) => entry.data)
    .filter((data) => typeof data === 'object' && data !== null && 'type' in data && data.type === 'context-changed')
    .at(-1)
}

function expectedFrame(mailboxId: string | null, messageRef: string | null = null) {
  return {
    type: 'context-changed', panelId: 'mail', popoutId: POPOUT,
    context: { workspaceId: WORKSPACE, mailboxId, folder: 'inbox', messageRef },
  }
}

beforeAll(async () => {
  await import('./MailPanel') // Real lazy content, resolved before interaction waits.
})
beforeEach(() => {
  requests.length = 0
  unsupportedHttp.length = 0
  ObservedChannel.sent = []
  sessionStorage.clear()
  vi.clearAllMocks()
  vi.stubGlobal('fetch', httpEdge)
  vi.stubGlobal('BroadcastChannel', ObservedChannel)
  vi.stubGlobal('scrollTo', vi.fn()) // Accepted Window edge: jsdom has no native scrolling.
  Element.prototype.scrollIntoView = vi.fn() // Accepted existing selector-test DOM adapter.
  usePanelShellStore.setState({ activePanel: null, panelWidth: null, guardPending: false, historyPushed: false, toasts: [] })
  window.history.replaceState({}, '', '/#/workspaces/ws-1/chat')
})
afterEach(() => {
  cleanup()
  for (const client of clients.splice(0)) client.clear()
  usePanelShellStore.getState().closePanel()
  PanelChannelEdge.reset()
  Element.prototype.scrollIntoView = originalScrollIntoView
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  expect(unsupportedHttp, 'Strict HTTP fixture must expose unmodelled requests, never hide a setup error').toEqual([])
  expect(dialingPaths().some((path) => path.includes(`/workspaces/${FOREIGN_WORKSPACE}/`)),
    'Mailbox reads must remain in the selected workspace',
  ).toBe(false)
})

async function openRoute(mailboxId: string | null, messageRef: string | null = null) {
  const search = new URLSearchParams({
    workspace: WORKSPACE, mailbox: mailboxId ?? '', folder: 'inbox', popout: POPOUT,
    ...(messageRef === null ? {} : { message: messageRef }),
  })
  const history = createMemoryHistory({ initialEntries: [`/panel/mail?${search}`] })
  const router = createRouter({ routeTree, history })
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  clients.push(client)
  render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>)
  expect(await screen.findByTestId('mail-panel')).toBeInTheDocument()
  expect(screen.getByTestId('fullscreen-panel')).toBeInTheDocument()
  expect(router.state.matches.map((match) => match.routeId)).toContain('/_fullscreen/panel/$panelId')
  await waitFor(() => expect(requests).toContain('/api/v1/mailboxes'))
  await waitFor(() => expect(latestLiveFrame()).toEqual(expectedFrame(mailboxId, messageRef)))
  return router
}

async function assertInitialA() {
  const router = await openRoute(A, REF_A)
  await waitFor(() => expect(screen.getByRole('combobox', { name: 'Mailbox' }).textContent).toBe(LABEL_A))
  expect(await screen.findByRole('tab', { name: 'A Inbox' })).toHaveAttribute('aria-selected', 'true')
  expect(await screen.findByText(BODY_A)).toBeInTheDocument()
  expect(within(screen.getByTestId('mail-message-list')).getByRole('button', { name: new RegExp(SUBJECT_A) })).toHaveAttribute('aria-current', 'true')
  expect(dialingPaths()).toContain(`/api/v1/workspaces/${WORKSPACE}/mail/${A}/folders`)
  expect(dialingPaths()).toContain(`/api/v1/workspaces/${WORKSPACE}/mail/${A}/folders/inbox/messages`)
  expect(dialingPaths()).toContain(`/api/v1/workspaces/${WORKSPACE}/mail/${A}/folders/inbox/messages/${encodeURIComponent(REF_A)}`)
  expect(dialingPaths().some((path) => path.includes(`/mail/${B}/`))).toBe(false)
  expect(router.state.location.search).toEqual({ workspace: WORKSPACE, mailbox: A, folder: 'inbox', message: REF_A, popout: POPOUT })
  return router
}

async function chooseB() {
  fireEvent.click(screen.getByRole('combobox', { name: 'Mailbox' }))
  const options = await screen.findAllByRole('option')
  expect(options.map((option) => option.textContent), 'Only enabled, configured same-workspace mailboxes are eligible').toEqual([LABEL_A, LABEL_B])
  fireEvent.click(screen.getByRole('option', { name: LABEL_B }))
  await waitFor(() => expect(screen.getByRole('combobox', { name: 'Mailbox' })).toHaveAttribute('aria-expanded', 'false'))
  // This visible effect proves the actual gesture reached product state handling,
  // even if the mailbox itself incorrectly remains A. No callback is invoked here.
  await waitFor(() => expect(screen.queryByText(BODY_A)).not.toBeInTheDocument())
  await act(async () => {})
}

function recordSwitch(router: Awaited<ReturnType<typeof openRoute>>, since: number) {
  console.info('[mailbox-switch-observation]', JSON.stringify({
    selectedMailbox: screen.getByRole('combobox', { name: 'Mailbox' }).textContent,
    requestPathsAfterGesture: dialingPaths().slice(since),
    liveFrame: latestLiveFrame(), routeSearch: router.state.location.search,
    persistedIntent: readMailPanelIntent(WORKSPACE),
  }))
}

async function assertVisibleB() {
  await waitFor(() => expect(screen.getByRole('combobox', { name: 'Mailbox' }).textContent,
    'A human choice of B must replace explicit URL mailbox A, not write unused intent',
  ).toBe(LABEL_B))
  expect(await screen.findByRole('tab', { name: 'B Inbox' })).toHaveAttribute('aria-selected', 'true')
  const list = within(screen.getByTestId('mail-message-list'))
  expect(await list.findByRole('button', { name: new RegExp(SUBJECT_B) })).toBeInTheDocument()
  expect(list.queryByRole('button', { name: new RegExp(SUBJECT_A) })).not.toBeInTheDocument()
  expect(screen.getByText('Select a message to read')).toBeInTheDocument()
  expect(screen.queryByText(BODY_A)).not.toBeInTheDocument()
}

function assertBRequests(paths: string[]) {
  expect(paths, 'Selector B must cause the real B folders request').toContain(`/api/v1/workspaces/${WORKSPACE}/mail/${B}/folders`)
  expect(paths, 'Selector B must cause the real B message-list request').toContain(`/api/v1/workspaces/${WORKSPACE}/mail/${B}/folders/inbox/messages`)
  expect(paths.some((path) => path.includes(`/mail/${A}/`)), 'A must not be dialed in place of the human-selected B').toBe(false)
}

async function assertLiveB(router: Awaited<ReturnType<typeof openRoute>>) {
  await waitFor(() => expect(latestLiveFrame(), 'Live context must identify human-selected B and clear the prior A message').toEqual(expectedFrame(B)))
  expect(router.state.location.search).toEqual({ workspace: WORKSPACE, mailbox: B, folder: 'inbox', message: '', popout: POPOUT })
  expect(readMailPanelIntent(WORKSPACE)).toEqual({ agentId: B, folder: 'inbox', messageRef: null })
}

describe('Real Mailbox selector can replace explicit URL-named mailbox A with B', () => {
  it('initial explicit-A positive control loads and reports the exact A mailbox/message', async () => {
    await assertInitialA()
  })

  it('bare-link negative control connects to no mailbox before choice; the actual B gesture then loads and reports B', async () => {
    writeMailPanelIntent(WORKSPACE, { agentId: A, folder: 'inbox', messageRef: null })
    const router = await openRoute(null)
    expect(screen.getByRole('combobox', { name: 'Mailbox' }).textContent).toBe('Choose a mailbox')
    expect(screen.getByRole('button', { name: 'Compose' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Refresh' })).toBeDisabled()
    expect(dialingPaths(), 'A bare explicit-choose link must not connect even with saved A intent').toEqual([])

    await chooseB()
    await assertVisibleB()
    assertBRequests(dialingPaths())
    await assertLiveB(router)
    fireEvent.click(within(screen.getByTestId('mail-message-list')).getByRole('button', { name: new RegExp(SUBJECT_B) }))
    expect(await screen.findByText(BODY_B)).toBeInTheDocument()
    expect(dialingPaths()).toContain(`/api/v1/workspaces/${WORKSPACE}/mail/${B}/folders/inbox/messages/${encodeURIComponent(REF_B)}`)
    await waitFor(() => expect(latestLiveFrame()).toEqual(expectedFrame(B, REF_B)))
  })

  it.each(['visible selection', 'folder/message request identity', 'reported live route/context'] as const)(
    'after explicit A, the real selector gesture must change %s to B', async (oracle) => {
      const router = await assertInitialA()
      const since = dialingPaths().length
      await chooseB()
      recordSwitch(router, since)
      if (oracle === 'visible selection') await assertVisibleB()
      if (oracle === 'folder/message request identity') {
        await waitFor(() => assertBRequests(dialingPaths().slice(since)))
      }
      if (oracle === 'reported live route/context') await assertLiveB(router)
    },
  )
})

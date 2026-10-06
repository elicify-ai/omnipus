// RED oracle: PANEL-MAIL-LIVE-BACK-RED-2217 acceptance, CS2 investigation;
// side-panel-shell-spec.md FR-018 / §8.3. Back must return the child's LAST
// visible mailbox/folder/message, not its open-time selection. Expectations
// below were derived from that brief, not from observed production output.
// REAL: source Expand, registry, MailPanelContent + MailPanel, generated HTTP
// validation, router/auth, registration/Back, presence and opener re-docking.
// Controlled PROCESS edges only: HTTP, Window, BroadcastChannel, geometry.
// This is a jsdom integration reproduction, NOT native multi-tab UAT.
// GREEN and production mutation proof are deferred to an independent CHECK.
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, render, waitFor, within } from '@testing-library/react'
import { createMemoryHistory, createRouter, RouterProvider } from '@tanstack/react-router'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import type {
  Mailbox, MailFolderList, MailMessage, MailMessagePage, MailMessageSummary,
  MailSummaryList,
} from '@/lib/api/generated/openapi-types'
import { PanelChannelEdge, popupEdge, signedInState } from '../../../../tests/fixtures/pe1-panel-process-edges'
import { routeTree } from '@/routeTree.gen'
import { getPanelTabHandleRegistry, getPanelTabPresence } from '@/lib/panelTabPresence'
import { panels } from '@/components/panel-shell/registry'
import { SidePanelShell } from '@/components/panel-shell/SidePanelShell'
import { PanelTabPresenceBridge } from '@/components/panel-shell/PanelTabPresenceBridge'
import { usePanelShellStore } from '@/components/panel-shell/panelShellStore'

const WORKSPACE = 'ws-1'
const OTHER_WORKSPACE = 'ws-other'
const MAILBOX = 'mia'
// Synthetic folder-scoped references for the brief's message A and message B.
const REF_A = 'uid:777:42'
const REF_B = 'uid:777:43'
const CONTEXT_A = { workspaceId: WORKSPACE, mailboxId: MAILBOX, folder: 'inbox', messageRef: REF_A } as const
const CONTEXT_B = { workspaceId: WORKSPACE, mailboxId: MAILBOX, folder: 'sent', messageRef: REF_B } as const
const BODY_A = 'Synthetic Inbox message A body.'
const BODY_B = 'Synthetic Sent message B body.'

const mailboxes: Mailbox[] = [
  // Same agent in another workspace comes FIRST: an unscoped mailbox choice
  // must not accidentally satisfy the workspace-isolation control.
  { agent_id: MAILBOX, workspace_id: OTHER_WORKSPACE, enabled: true, configured: true, username: 'other@example.test' },
  { agent_id: MAILBOX, workspace_id: WORKSPACE, enabled: true, configured: true, username: 'owner@example.test' },
]
const folders: MailFolderList = { folders: [
  { slug: 'inbox', display_name: 'Inbox', total: 1, unread_count: 0, availability: 'present' },
  { slug: 'sent', display_name: 'Sent', total: 1, unread_count: 0, availability: 'present' },
] }
const rowA: MailMessageSummary = {
  message_id: '<synthetic-a@example.test>', uid: 42, uidvalidity: 777,
  folder: 'inbox', subject: 'Message A', from: 'sender@example.test', from_name: 'Sender',
  to: ['owner@example.test'], cc: [], date: '2026-10-05T10:00:00Z',
  seen: true, is_draft: false, is_omnipus_draft: false, read_by_agent: false,
}
const rowB: MailMessageSummary = {
  ...rowA, message_id: '<synthetic-b@example.test>', uid: 43,
  folder: 'sent', subject: 'Message B', from: 'owner@example.test',
  to: ['recipient@example.test'],
}
const detailA: MailMessage = {
  ...rowA, reply_to: null, in_reply_to: null, references: null, bcc: null,
  body_text: BODY_A, body_markdown: BODY_A, markdown_lossy: false,
  has_html: false, attachments: [],
}
const detailB: MailMessage = { ...detailA, ...rowB, body_text: BODY_B, body_markdown: BODY_B }
const summary: MailSummaryList = { items: [] }

// not-wire-format: observation of browser PROCESS-edge bytes, not a gateway type.
class ObservedChannel extends PanelChannelEdge {
  static sent: { channel: string; data: unknown }[] = []
  override postMessage(data: unknown) {
    ObservedChannel.sent.push({ channel: this.name, data: structuredClone(data) })
    super.postMessage(data)
  }
}

class RowResizeObserver {
  constructor(private readonly callback: ResizeObserverCallback) {}
  observe() {
    // A desktop row, not a width-budget test: keep the real source Expand visible.
    this.callback(
      [{ contentRect: { width: 1280 } as DOMRectReadOnly } as ResizeObserverEntry],
      this as unknown as ResizeObserver,
    )
  }
  unobserve() {}
  disconnect() {}
}

const unsupportedHttp: string[] = []
const mailRequests: string[] = []
function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })
}
const httpEdge = vi.fn<typeof fetch>(async (input, init) => {
  const raw = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url
  const url = new URL(raw, window.location.href)
  const method = init?.method ?? (input instanceof Request ? input.method : 'GET')
  const reject = (): never => {
    const reason = `Unsupported synthetic HTTP request: ${method} ${url.pathname}`
    unsupportedHttp.push(reason)
    throw new Error(reason)
  }
  // Never reach a real app, mailbox or provider. Every unsupported request is
  // fatal to the fixture even if an application error boundary handles it.
  if (url.origin !== window.location.origin || method !== 'GET' || init?.credentials !== 'include') return reject()
  if (url.pathname === '/api/v1/state') return jsonResponse(signedInState)
  if (url.pathname === '/api/v1/agents') return jsonResponse([])
  if (url.pathname === '/api/v1/mailboxes') return jsonResponse({ mailboxes })
  const base = `/api/v1/workspaces/${WORKSPACE}/mail`
  if (url.pathname.startsWith('/api/v1/workspaces/')) mailRequests.push(url.pathname)
  if (url.pathname === `${base}/summary`) return jsonResponse(summary)
  if (url.pathname === `${base}/${MAILBOX}/folders`) return jsonResponse(folders)
  for (const [folder, row, ref, detail] of [
    ['inbox', rowA, REF_A, detailA], ['sent', rowB, REF_B, detailB],
  ] as const) {
    const listPath = `${base}/${MAILBOX}/folders/${folder}/messages`
    if (url.pathname === listPath) {
      const page: MailMessagePage = {
        messages: [row], truncated: false, next_before_uid: null,
        has_more: false, next_cursor: null, view_limit_reached: false,
      }
      return jsonResponse(page)
    }
    if (decodeURIComponent(url.pathname) === `${listPath}/${ref}`) return jsonResponse(detail)
  }
  return reject()
})

const clients: QueryClient[] = []
function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  clients.push(client)
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>
}

beforeAll(async () => {
  // Resolve the real lazy chunk before bounded interaction waits; do not mock it.
  await import('./MailPanel')
})
beforeEach(() => {
  vi.clearAllMocks()
  unsupportedHttp.length = 0
  mailRequests.length = 0
  ObservedChannel.sent = []
  sessionStorage.clear()
  vi.stubGlobal('fetch', httpEdge)
  vi.stubGlobal('BroadcastChannel', ObservedChannel)
  vi.stubGlobal('ResizeObserver', RowResizeObserver)
  vi.stubGlobal('scrollTo', vi.fn()) // Native scrolling is absent from jsdom's Window edge.
  usePanelShellStore.setState({ activePanel: null, panelWidth: null, guardPending: false, historyPushed: false, toasts: [] })
  window.history.replaceState({}, '', '/#/workspaces/ws-1/chat')
})
afterEach(() => {
  cleanup() // Real owner/child cleanups run before resetting the browser edges.
  for (const client of clients.splice(0)) client.clear()
  usePanelShellStore.getState().closePanel()
  PanelChannelEdge.reset()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  expect(unsupportedHttp, 'The HTTP instrument must refuse every unmodelled request, not hide it').toEqual([])
})

async function assertSelection(container: HTMLElement, selection: 'A' | 'B') {
  const view = within(container)
  const folder = selection === 'A' ? 'Inbox' : 'Sent'
  const subject = selection === 'A' ? 'Message A' : 'Message B'
  const body = selection === 'A' ? BODY_A : BODY_B
  expect(await view.findByTestId('mail-panel')).toBeInTheDocument()
  await waitFor(() => expect(view.getByRole('combobox', { name: 'Mailbox' })).toHaveTextContent('mia · owner@example.test'))
  expect(view.getByRole('combobox', { name: 'Mailbox' })).not.toHaveTextContent('other@example.test')
  await waitFor(() => expect(view.getByRole('tab', { name: folder })).toHaveAttribute('aria-selected', 'true'))
  const list = within(view.getByTestId('mail-message-list'))
  await waitFor(() => expect(list.getByRole('button', { name: new RegExp(subject) })).toHaveAttribute('aria-current', 'true'))
  expect(await view.findByText(body)).toBeInTheDocument()
  expect(view.queryByText('Something went wrong')).not.toBeInTheDocument()
}

async function sourceAndMailChild(expandSelection: 'A' | 'B' = 'A') {
  const expected = expandSelection === 'A' ? CONTEXT_A : CONTEXT_B
  const child = popupEdge()
  const open = vi.spyOn(window, 'open').mockReturnValue(child as unknown as Window)
  const source = render(<>
    <PanelTabPresenceBridge />
    <SidePanelShell panels={panels} username="synthetic-owner" chat={
      <textarea data-testid="chat-input" aria-label="Source draft" defaultValue="Synthetic source draft survives" />
    } />
  </>, { wrapper })
  act(() => usePanelShellStore.getState().openPanel('mail', CONTEXT_A))
  await assertSelection(source.container, 'A')
  if (expandSelection === 'B') await chooseSentB(source.container)
  const draft = within(source.container).getByRole('textbox', { name: 'Source draft' })
  fireEvent.click(within(source.container).getByRole('button', { name: 'Expand Mail panel' }))
  await waitFor(() => expect(child.location.replace).toHaveBeenCalledTimes(1))
  expect(open, 'Only the real source Expand may create this child').toHaveBeenCalledExactlyOnceWith('about:blank', '_blank')
  const destination = child.location.replace.mock.calls[0]?.[0]
  if (!destination) throw new Error('BLOCKED: source Expand produced no Mail child URL — PANEL-MAIL-LIVE-BACK-RED-2217')
  const url = new URL(destination, window.location.href)
  const search = new URLSearchParams(url.hash.split('?')[1])
  const popout = search.get('popout')
  if (!popout) throw new Error('BLOCKED: source ownership identifier missing — PANEL-MAIL-LIVE-BACK-RED-2217')
  expect(Object.fromEntries(search)).toEqual({
    workspace: WORKSPACE, mailbox: MAILBOX, folder: expected.folder, message: expected.messageRef, popout,
  })
  expect(url.hash.split('?')[0]).toBe('#/panel/mail')
  expect(child.opener).toBeNull()
  expect(usePanelShellStore.getState().activePanel).toBeNull()
  expect(getPanelTabHandleRegistry().get(`mail:${WORKSPACE}`)).toBe(child)

  // Use the URL actually emitted by Expand. No injected child context,
  // replacement registry, registration mock or synthetic content callback.
  const history = createMemoryHistory({ initialEntries: [url.hash.slice(1)] })
  const router = createRouter({ routeTree, history })
  const mounted = render(<RouterProvider router={router} />, { wrapper })
  await assertSelection(mounted.container, expandSelection)
  expect(router.state.matches.map((match) => match.routeId)).toContain('/_fullscreen/panel/$panelId')
  expect(within(mounted.container).getByTestId('fullscreen-panel')).toBeInTheDocument()
  expect(within(mounted.container).queryByTestId('app-shell')).not.toBeInTheDocument()
  expect(within(mounted.container).getByTestId('mail-list-preview-layout')).toHaveAttribute('data-layout', 'split')
  await waitFor(() => expect(getPanelTabPresence()).toEqual([{ panelId: 'mail', workspaceId: WORKSPACE }]))
  return { source, child, open, popout, router, mounted, draft }
}

type Session = Awaited<ReturnType<typeof sourceAndMailChild>>
async function chooseSentB(container: HTMLElement) {
  const view = within(container)
  fireEvent.click(view.getByRole('tab', { name: 'Sent' }))
  const list = within(await view.findByTestId('mail-message-list'))
  const message = await list.findByRole('button', { name: /Message B/ })
  fireEvent.click(message)
  await assertSelection(container, 'B')
  // Exact HTTP path proves the real Mail handlers loaded B, not a test callback.
  expect(mailRequests).toContain(`/api/v1/workspaces/${WORKSPACE}/mail/${MAILBOX}/folders/sent/messages/${encodeURIComponent(REF_B)}`)
}

function lifecycleMessages(type: 'context-changed' | 'popout-closed') {
  return ObservedChannel.sent
    .filter((entry) => entry.channel === 'omnipus-panel-popout-lifecycle')
    .map((entry) => entry.data)
    .filter((data) => typeof data === 'object' && data !== null && 'type' in data && data.type === type)
}

async function backFromChild(session: Session) {
  // jsdom cannot close a native tab. This Window-edge replacement changes only
  // the real closed flag/owned handle; the real Back handler produces the message.
  vi.stubGlobal('closed', false)
  const close = vi.spyOn(window, 'close').mockImplementation(() => {
    session.child.close()
    vi.stubGlobal('closed', session.child.closed)
  })
  fireEvent.click(within(session.mounted.container).getByRole('button', { name: 'Back to chat' }))
  await waitFor(() => expect(close).toHaveBeenCalledTimes(1))
  expect(session.child.closed).toBe(true)
  session.mounted.unmount()
  await act(async () => {}) // Deliver production lifecycle messages to the owner.
}

describe('Mail actual child Back returns its last visible selection (CS2)', () => {
  it('unchanged-A positive control: real Expand and child Back restore Inbox/message A', async () => {
    const session = await sourceAndMailChild()
    await backFromChild(session)
    expect(lifecycleMessages('popout-closed')).toEqual([{
      type: 'popout-closed', panelId: 'mail', popoutId: session.popout, context: CONTEXT_A,
    }])
    await waitFor(() => expect(usePanelShellStore.getState().activePanel).toEqual({ id: 'mail', context: CONTEXT_A }))
    await assertSelection(session.source.container, 'A')
    expect([...getPanelTabHandleRegistry().keys()]).toEqual([])
    expect(session.draft).toHaveValue('Synthetic source draft survives')
    expect(session.open).toHaveBeenCalledTimes(1)
  })

  it('CS2: Back after real Sent/message B clicks announces B, never stale Inbox/message A', async () => {
    const session = await sourceAndMailChild()
    await chooseSentB(session.mounted.container)
    await backFromChild(session)
    expect(lifecycleMessages('popout-closed'),
      'CS2: actual child Back must carry last-visible Sent/message B, not the frozen open-time Inbox/message A',
    ).toEqual([{
      type: 'popout-closed', panelId: 'mail', popoutId: session.popout, context: CONTEXT_B,
    }])
    await waitFor(() => expect(usePanelShellStore.getState().activePanel,
      'CS2: original source must re-dock Sent/message B',
    ).toEqual({ id: 'mail', context: CONTEXT_B }))
    await assertSelection(session.source.container, 'B')
    expect(session.draft).toHaveValue('Synthetic source draft survives')
    expect([...getPanelTabHandleRegistry().keys()]).toEqual([])
    expect(session.open).toHaveBeenCalledTimes(1)
  })

  it('CS2: Back after real Sent/message B clicks re-docks the original source at Sent/message B', async () => {
    const session = await sourceAndMailChild()
    await chooseSentB(session.mounted.container)
    await backFromChild(session)
    expect(usePanelShellStore.getState().activePanel,
      'CS2: actual opener re-docking must use last-visible Sent/message B, not stale Inbox/message A',
    ).toEqual({ id: 'mail', context: CONTEXT_B })
    await assertSelection(session.source.container, 'B')
    expect([...getPanelTabHandleRegistry().keys()]).toEqual([])
    expect(session.draft).toHaveValue('Synthetic source draft survives')
    expect(session.open).toHaveBeenCalledTimes(1)
  })

  it('live-getter instrument control: source Sent/B clicks before Expand open and restore B', async () => {
    // A deliberately frozen Mail getter would emit A in the generated Expand
    // URL and fail sourceAndMailChild('B'). No callback is supplied by the test.
    // Production mutation execution belongs to a fresh CHECK instance.
    const session = await sourceAndMailChild('B')
    await backFromChild(session)
    expect(lifecycleMessages('popout-closed')).toEqual([{
      type: 'popout-closed', panelId: 'mail', popoutId: session.popout, context: CONTEXT_B,
    }])
    expect(usePanelShellStore.getState().activePanel).toEqual({ id: 'mail', context: CONTEXT_B })
    await assertSelection(session.source.container, 'B')
    expect([...getPanelTabHandleRegistry().keys()]).toEqual([])
    expect(session.open).toHaveBeenCalledTimes(1)
  })

  it('identity/isolation positive control: real Sent/B retains the original child and correct workspace', async () => {
    const session = await sourceAndMailChild()
    await chooseSentB(session.mounted.container)
    expect(session.router.state.location.pathname).toBe('/panel/mail')
    expect(session.router.state.location.search).toMatchObject({ workspace: WORKSPACE, popout: session.popout })
    expect(getPanelTabHandleRegistry().get(`mail:${WORKSPACE}`)).toBe(session.child)
    expect(getPanelTabHandleRegistry().has(`mail:${OTHER_WORKSPACE}`)).toBe(false)
    expect(getPanelTabPresence()).toEqual([{ panelId: 'mail', workspaceId: WORKSPACE }])
    expect(usePanelShellStore.getState().activePanel).toBeNull()
    expect(session.child.closed).toBe(false)
    expect(session.child.location.replace).toHaveBeenCalledTimes(1)
    expect(session.open).toHaveBeenCalledTimes(1)
    const contextMessages = lifecycleMessages('context-changed')
    expect(contextMessages.length, 'The observation channel must see the real child producer').toBeGreaterThan(0)
    for (const frame of contextMessages) {
      expect(frame).toMatchObject({ type: 'context-changed', panelId: 'mail', popoutId: session.popout, context: { workspaceId: WORKSPACE } })
    }
    expect(mailRequests.some((path) => path.includes(`/workspaces/${OTHER_WORKSPACE}/`)),
      'The other workspace mailbox must never be dialed',
    ).toBe(false)
    expect(session.draft).toHaveValue('Synthetic source draft survives')
  })
})

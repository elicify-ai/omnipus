// Private PE1 unit fixture: generated API replies and browser PROCESS edges only.
// Never replaces the shell, registry, content, auth guard, codecs or opener.
import { vi } from 'vitest'
import type {
  AppState, Workspace, WorkspaceDelegation, LibraryEntry, LibraryWorkspaceNode,
  LibraryContentResponse, KnowledgeBaseInfo, Mailbox, MailFolderList,
  MailMessagePage, MailMessage, MailSummaryList, operations,
} from '../../src/lib/api/generated/openapi-types'
import type { BrowserAttachFrame, BrowserStatusFrame } from '../../src/lib/api/generated/asyncapi-types'
import { TaskOccurrenceSet as TaskOccurrenceSetSchema } from '../../src/lib/api/generated/schemas'

export const signedInState: AppState = {
  onboarding_complete: true,
  identity: { mode: 'local', edition: 'core', signed_in: true, blocked_reason: 'none' },
}

export function workspaceReply(id: string): Workspace {
  return {
    revision: '2'.repeat(64), id, name: 'PE1 workspace', status: 'active',
    pinned: false, pin_order: 0, task_count: 0, core_team: [],
    created_at: '2026-06-20T00:00:00Z', updated_at: '2026-06-20T00:00:00Z',
  }
}

const libraryNodes: LibraryWorkspaceNode[] = [
  { id: 'ws-1', name: 'PE1 workspace', entry_count: 1 },
  { id: 'workspace-current', name: 'PE1 current workspace', entry_count: 1 },
]
const libraryEntries: LibraryEntry[] = [{
  name: 'Current.txt', path: 'Current.txt', is_dir: false, is_hidden: false,
  size: 21, modified_at: '2026-07-28T10:15:00Z', is_text_editable: true, mime: 'text/plain',
}, {
  name: 'Current.md', path: 'Projects/Current.md', is_dir: false, is_hidden: false,
  size: 21, modified_at: '2026-07-28T10:15:00Z', is_text_editable: true, mime: 'text/plain',
}]
const libraryContent: LibraryContentResponse = {
  path: 'Current.txt', content: 'PE1 Library selection', size: 21,
  is_text: true, too_large: false, mime: 'text/plain',
}
const mailboxes: Mailbox[] = ['ws-1', 'workspace-current'].flatMap((workspace_id) =>
  ['mia', 'agent-current'].map((agent_id) => ({
    agent_id, workspace_id, enabled: true, configured: true, username: `${agent_id}@example.test`,
  })),
)
const folders: MailFolderList = { folders: [
  { slug: 'inbox', display_name: 'Inbox', total: 1, unread_count: 0 },
] }
const messages: MailMessagePage = {
  messages: [{
    message_id: '<pe1@example.test>', uid: 42, uidvalidity: 777, folder: 'inbox',
    subject: 'PE1 selected message', from: 'ada@example.test', from_name: 'Ada',
    to: ['mia@example.test'], cc: [], date: '2026-09-28T10:00:00Z', seen: true,
    is_draft: false, is_omnipus_draft: false, read_by_agent: false,
  }], truncated: false, next_before_uid: null,
}
const message: MailMessage = {
  ...messages.messages[0], message_id: '<pe1@example.test>', uid: 42, uidvalidity: 777,
  folder: 'inbox', subject: 'PE1 selected message', from: 'ada@example.test', from_name: 'Ada',
  to: ['mia@example.test'], cc: [], date: '2026-09-28T10:00:00Z', seen: true,
  is_draft: false, is_omnipus_draft: false, read_by_agent: false,
  body_markdown: 'PE1 Mail selection', body_text: 'PE1 Mail selection', has_html: false, attachments: [],
  reply_to: null, in_reply_to: null, references: null, bcc: null, markdown_lossy: false,
}

export const apiEdges = {
  fetchAppState: vi.fn<typeof import('../../src/lib/api').fetchAppState>(async () => signedInState),
  fetchTasks: vi.fn<typeof import('../../src/lib/api').fetchTasks>(async () => []),
  fetchPlans: vi.fn<typeof import('../../src/lib/api').fetchPlans>(async () => []),
  fetchAgents: vi.fn<typeof import('../../src/lib/api').fetchAgents>(async () => []),
  fetchWorkspace: vi.fn<typeof import('../../src/lib/api').fetchWorkspace>(async (id) => workspaceReply(id)),
  fetchWorkspaceDelegation: vi.fn<typeof import('../../src/lib/api').fetchWorkspaceDelegation>(async (id): Promise<WorkspaceDelegation> => ({
    revision: '2'.repeat(64), workspace_id: id, team: [], edges: [], default_depth: 3,
  })),
  fetchLibraryWorkspaces: vi.fn<typeof import('../../src/lib/api').fetchLibraryWorkspaces>(async () => libraryNodes),
  fetchLibraryEntries: vi.fn<typeof import('../../src/lib/api').fetchLibraryEntries>(async () => libraryEntries),
  fetchLibraryContent: vi.fn<typeof import('../../src/lib/api').fetchLibraryContent>(async () => libraryContent),
  fetchLibraryContentVersioned: vi.fn<typeof import('../../src/lib/api').fetchLibraryContentVersioned>(async () => ({ data: libraryContent, version: 'pe1-version' })),
  fetchKnowledgeBaseInfo: vi.fn<typeof import('../../src/lib/api').fetchKnowledgeBaseInfo>(async (id, path = ''): Promise<KnowledgeBaseInfo> => ({
    workspace_id: id, root_path: path, is_knowledge_base: false, marker: 'none',
  })),
  fetchMailboxes: vi.fn<typeof import('../../src/lib/api').fetchMailboxes>(async () => mailboxes),
}
export const mailApiEdges = {
  fetchMailFolders: vi.fn<typeof import('../../src/lib/api/mail').fetchMailFolders>(async () => folders),
  fetchMailMessages: vi.fn<typeof import('../../src/lib/api/mail').fetchMailMessages>(async () => messages),
  fetchMailMessage: vi.fn<typeof import('../../src/lib/api/mail').fetchMailMessage>(async () => message),
  fetchMailSummary: vi.fn<typeof import('../../src/lib/api/mail').fetchMailSummary>(async (): Promise<MailSummaryList> => ({ items: [] })),
}

// not-wire-format: in-process stand-in for browser BroadcastChannel delivery.
// Production listeners/validation/presence/lifecycle remain real. Delivering to
// OTHER live endpoints models the browser edge; there is no pre-seeded identity.
export class PanelChannelEdge {
  static endpoints = new Set<PanelChannelEdge>()
  private listeners = new Set<(event: MessageEvent) => void>()
  private closed = false
  onmessage: ((event: MessageEvent) => void) | null = null
  constructor(readonly name: string) { PanelChannelEdge.endpoints.add(this) }
  addEventListener(type: string, listener: (event: MessageEvent) => void) {
    if (type === 'message') this.listeners.add(listener)
  }
  removeEventListener(type: string, listener: (event: MessageEvent) => void) {
    if (type === 'message') this.listeners.delete(listener)
  }
  postMessage(data: unknown) {
    if (this.closed) throw new DOMException('Channel is closed', 'InvalidStateError')
    for (const endpoint of PanelChannelEdge.endpoints) {
      if (endpoint === this || endpoint.name !== this.name) continue
      queueMicrotask(() => {
        if (endpoint.closed) return
        const event = new MessageEvent('message', { data })
        endpoint.onmessage?.(event)
        for (const listener of endpoint.listeners) listener(event)
      })
    }
  }
  close() { this.closed = true; PanelChannelEdge.endpoints.delete(this) }
  static reset() { for (const endpoint of [...this.endpoints]) endpoint.close() }
}

// not-wire-format: WebSocket process edge; real BrowserLiveWsConnection sends
// its GENERATED attach frame. No Browser component or signaling logic is mocked.
export class BrowserSocketEdge {
  static OPEN = 1
  static CLOSED = 3
  static sockets: BrowserSocketEdge[] = []
  readonly sent: unknown[] = []
  readyState = 0
  onopen: ((event: Event) => void) | null = null
  onmessage: ((event: MessageEvent) => void) | null = null
  onclose: ((event: CloseEvent) => void) | null = null
  onerror: ((event: Event) => void) | null = null
  constructor(readonly url: string) {
    BrowserSocketEdge.sockets.push(this)
    queueMicrotask(() => {
      if (this.readyState === BrowserSocketEdge.CLOSED) return
      this.readyState = BrowserSocketEdge.OPEN
      this.onopen?.(new Event('open'))
    })
  }
  send(bytes: string) {
    const frame: unknown = JSON.parse(bytes)
    this.sent.push(frame)
    if ((frame as BrowserAttachFrame).type === 'browser_attach') {
      const reply: BrowserStatusFrame = { type: 'browser_status', state: 'attached', session_id: (frame as BrowserAttachFrame).session_id }
      queueMicrotask(() => {
        if (this.readyState === BrowserSocketEdge.OPEN) this.onmessage?.(new MessageEvent('message', { data: JSON.stringify(reply) }))
      })
    }
  }
  close() { this.readyState = BrowserSocketEdge.CLOSED }
  static reset() { for (const socket of this.sockets) socket.close(); this.sockets = [] }
}

// not-wire-format: Window process edge. The actual production opener must fill
// the destination; the fixture NEVER provides a preset valid target URL.
export function popupEdge() {
  const child = {
    closed: false, opener: window as Window | null,
    location: { replace: vi.fn<(href: string) => void>() },
    focus: vi.fn<() => void>(), close: vi.fn<() => void>(),
  }
  child.close.mockImplementation(() => { child.closed = true })
  return child
}

// not-wire-format: HTTP process-edge integrity state and request receipts.
// useOccurrences deliberately fetches outside lib/api. Keep that real client,
// generated validation and Calendar error handling intact. This fixture's
// empty task list implies the contract's EMPTY ARRAY, never null or a hook mock.
const unsupportedFetches: Error[] = []
let previousFetch: typeof fetch | null = null
export const occurrenceHttpEdge = {
  fetch: vi.fn<typeof fetch>(async (input, init) => {
    const rawUrl = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url
    const request = typeof Request === 'function' && input instanceof Request ? input : undefined
    const method = init?.method ?? request?.method ?? 'GET'
    const credentials = init?.credentials ?? request?.credentials
    const reject = (reason: string): never => {
      const error = new Error(`PE1 HTTP fixture rejected ${method} ${rawUrl}: ${reason}`)
      unsupportedFetches.push(error)
      console.error(error)
      throw error
    }
    const passthrough = () => {
      if (!previousFetch) return reject('fixture was not initialized')
      console.info('[pe1-http-passthrough]', JSON.stringify({ url: rawUrl, method, behavior: 'original fetch, no response replacement or error catch' }))
      return previousFetch(input, init)
    }
    let url: URL
    try {
      url = new URL(rawUrl, window.location.href)
    } catch {
      return passthrough()
    }
    const endpoint = new URL(`${import.meta.env.VITE_API_URL ?? ''}/api/v1/tasks/occurrences`, window.location.href)
    if (url.origin !== endpoint.origin || url.pathname !== endpoint.pathname) return passthrough()
    if (method !== 'GET' || credentials !== 'include' || init?.body != null) {
      return reject('unsupported occurrence transport')
    }
    if ([...url.searchParams.keys()].sort().join(',') !== 'from_ms,to_ms,tz,workspace_id') return reject('wrong occurrence query keys')
    const query: operations['listTaskOccurrences']['parameters']['query'] = {
      workspace_id: url.searchParams.get('workspace_id') ?? '',
      from_ms: Number(url.searchParams.get('from_ms')),
      to_ms: Number(url.searchParams.get('to_ms')),
      tz: url.searchParams.get('tz') ?? '',
    }
    if (!['ws-1', 'workspace-current'].includes(query.workspace_id) || !/^-?\d+$/.test(url.searchParams.get('from_ms') ?? '') || !/^-?\d+$/.test(url.searchParams.get('to_ms') ?? '') || !Number.isSafeInteger(query.from_ms) || !Number.isSafeInteger(query.to_ms) || query.from_ms >= query.to_ms || !query.tz) {
      return reject('invalid occurrence query for this fixture')
    }
    try {
      new Intl.DateTimeFormat('en-US', { timeZone: query.tz })
    } catch {
      return reject('invalid occurrence time zone')
    }
    const body: operations['listTaskOccurrences']['responses'][200]['content']['application/json'] = []
    TaskOccurrenceSetSchema.array().parse(body)
    const response = new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })
    console.info('[pe1-occurrence-http-edge]', JSON.stringify({
      request: { url: rawUrl, method, credentials, query },
      response: { status: response.status, contentType: response.headers.get('Content-Type'), body },
      contract: 'listTaskOccurrences:TaskOccurrenceSet[]',
    }))
    return response
  }),
  reset(originalFetch: typeof fetch) {
    previousFetch = originalFetch
    unsupportedFetches.length = 0
    this.fetch.mockClear()
  },
  verifyNoUnsupportedFetches() {
    if (unsupportedFetches.length > 0) {
      throw new Error(`PE1 HTTP fixture saw unsupported fetches: ${unsupportedFetches.map((error) => error.message).join('; ')}`)
    }
  },
}

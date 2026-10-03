// Q33 / #1081: ADR-066 MAJ-CW-009 — REST/replay retain the classified
// diagnostic and original identity. One real toolVisibility predicate applies
// to live, REST and replay; Verbose hides at rendering, never at ingestion.
//
// Unlike ChatScreen.context-notice-q33.test.tsx, this file does NOT mock
// fetchSessionMessages or seed already-decoded display messages. It exercises
// the real HTTP client, generated validators, REST adapter, replay reducer,
// chat store, ChatScreen and AssistantUI runtime. Only fetch (the network
// boundary) and jsdom's absent layout observer are substituted.
// GREEN verification and production mutation probes are deferred to CHECK.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, render, screen, waitFor } from '@testing-library/react'
import { AssistantRuntimeProvider } from '@assistant-ui/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from '@tanstack/react-router'
import { fetchSessionMessages } from '@/lib/api/sessions'
import { getApiSchemaErrorCount, resetApiSchemaErrorCount } from '@/lib/api/http'
import type { ContextWindowNotice, Message as WireMessage } from '@/lib/api/generated/openapi-types'
import type { ContextWindowNoticeFrame, ReplayMessageFrame } from '@/lib/api/generated/asyncapi-types'
import { ContextWindowNoticeFrame as ContextWindowNoticeFrameSchema } from '@/lib/api/generated/schemas'
import * as toolVisibility from '@/lib/toolVisibility'
import { useOmnipusRuntime } from '@/lib/omnipus-runtime'
import { useChatStore } from '@/store/chat'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { ChatScreen } from './ChatScreen'

const SID = 'q33-history-reload-session'
const ENTRY_ID = 'q33-original-history-notice'
const TURN_ID = 'q33-original-history-turn'
const AUTHOR = 'jim' // Deliberately different from the active/default agent, Mia.
const BEFORE_NOTICE = '2026-09-30T12:34:55Z'
const NOTICE_TIME = '2026-09-30T12:34:56.123456789Z'
const ANSWER = 'The history-loaded answer is still readable.'
const NOTICE = 'A changed context diagnostic sentence still has the same classification.'
const KINDS = ['provider_retry', 'mid_turn'] as const
const initialChat = useChatStore.getState()
const initialSession = useSessionStore.getState()
const initialConnection = useConnectionStore.getState()
const queryClients: QueryClient[] = []
let history: WireMessage[] = []
const fetchNetwork = vi.fn<typeof fetch>()

function noticePayload(kind: ContextWindowNotice['kind']): ContextWindowNotice {
  return { kind, message: NOTICE }
}

function storedNotice(kind: ContextWindowNotice['kind']): WireMessage {
  return {
    id: ENTRY_ID,
    type: 'context_window_notice',
    role: 'system',
    content: NOTICE,
    timestamp: NOTICE_TIME,
    agent_id: AUTHOR,
    turn_id: TURN_ID,
    context_window_notice: noticePayload(kind),
  }
}

function ordinaryHistory(): WireMessage[] {
  return [
    { id: 'q33-history-answer', type: 'message', role: 'assistant', content: ANSWER, timestamp: BEFORE_NOTICE, agent_id: 'mia' },
    // A sentence-based filter must not hide this ordinary same-text record.
    { id: 'q33-history-ordinary-copy', type: 'message', role: 'system', content: NOTICE, timestamp: BEFORE_NOTICE, agent_id: 'mia' },
  ]
}

function noticeFrame(kind: ContextWindowNotice['kind']): ContextWindowNoticeFrame {
  // Live and replay share this generated carrier, including original identity.
  return ContextWindowNoticeFrameSchema.parse({
    type: 'context_window_notice', session_id: SID, entry_id: ENTRY_ID,
    turn_id: TURN_ID, timestamp: NOTICE_TIME, agent_id: AUTHOR,
    notice: noticePayload(kind),
  })
}

function realNoticePredicate() {
  // Namespace lookup lets the test collect before this spec-required export
  // exists. Never install a fallback/dummy predicate that could manufacture green.
  const predicate: unknown = Reflect.get(toolVisibility, 'shouldRenderContextWindowNoticeInThread')
  if (typeof predicate !== 'function') {
    throw new Error('BLOCKED: shouldRenderContextWindowNoticeInThread not implemented — required by ADR-066 MAJ-CW-009 and the agreed Q33 public API')
  }
  return predicate
}

function ThreadRuntime() {
  const runtime = useOmnipusRuntime()
  return <AssistantRuntimeProvider runtime={runtime}><ChatScreen /></AssistantRuntimeProvider>
}

async function renderHistoryThread() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: Infinity } },
  })
  queryClients.push(client)
  const root = createRootRoute({
    component: () => <QueryClientProvider client={client}><ThreadRuntime /></QueryClientProvider>,
  })
  const router = createRouter({ routeTree: root, history: createMemoryHistory({ initialEntries: ['/'] }) })
  await act(async () => {
    await router.load()
    render(<RouterProvider router={router} />)
  })
  // Positive instrument control: a blank/crashed thread cannot prove hiding.
  expect(await screen.findByText(ANSWER)).toBeVisible()
}

async function expectRetainedVerboseToggle(kind: ContextWindowNotice['kind']) {
  expect.soft(screen.queryAllByText(NOTICE, { exact: true }), 'Verbose off leaves only the ordinary same-text message').toHaveLength(1)
  await act(async () => useChatPreferencesStore.getState().setVerboseChatEnabled(true))
  expect.soft(screen.queryAllByText(NOTICE, { exact: true }), 'Verbose on reveals the retained diagnostic without a reload').toHaveLength(2)
  await act(async () => useChatPreferencesStore.getState().setVerboseChatEnabled(false))
  expect.soft(screen.queryAllByText(NOTICE, { exact: true }), 'switching off hides only the diagnostic again').toHaveLength(1)
  await act(async () => useChatPreferencesStore.getState().setVerboseChatEnabled(true))
  expect.soft(screen.queryAllByText(NOTICE, { exact: true }), 'switching on again requires no refetch or new frame').toHaveLength(2)

  const retained = useChatStore.getState().messages.filter((entry) => entry.id === ENTRY_ID)
  expect.soft(retained, 'the original notice is retained exactly once, including while hidden').toHaveLength(1)
  expect.soft(retained[0], 'retain full classification, payload and original identity').toMatchObject({
    id: ENTRY_ID, type: 'context_window_notice', timestamp: NOTICE_TIME,
    agentId: AUTHOR, turnId: TURN_ID, contextWindowNotice: noticePayload(kind),
  })
  const retainedNotice: unknown = retained[0] ? Reflect.get(retained[0], 'contextWindowNotice') : undefined
  expect.soft(retainedNotice, 'the retained notice payload is the exact closed kind/message object').toEqual(noticePayload(kind))
  expect(screen.getByText(ANSWER)).toBeVisible()
}

beforeEach(() => {
  history = []
  fetchNetwork.mockReset()
  fetchNetwork.mockImplementation(async (input, init) => {
    const url = new URL(typeof input === 'string' ? input : input instanceof URL ? input.href : input.url, 'http://localhost')
    if ((init?.method ?? 'GET') !== 'GET') throw new Error(`Unexpected non-GET request: ${url.pathname}`)
    if (url.pathname === `/api/v1/sessions/${SID}/messages`) {
      return new Response(JSON.stringify(history), { status: 200, headers: { 'Content-Type': 'application/json' } })
    }
    // Unrelated list queries use the real client too, with empty backend lists.
    if (['/api/v1/agents', '/api/v1/providers', '/api/v1/skills', '/api/v1/commands'].includes(url.pathname)) {
      return new Response('[]', { status: 200, headers: { 'Content-Type': 'application/json' } })
    }
    throw new Error(`Unconfigured network request: ${url.pathname}`)
  })
  vi.stubGlobal('fetch', fetchNetwork)
  // Use the real PlainMessageList degradation path; jsdom has no layout engine.
  vi.stubGlobal('ResizeObserver', undefined)
  useChatStore.setState(initialChat, true)
  useSessionStore.setState({ ...initialSession, activeSessionId: SID, activeAgentId: 'mia' }, true)
  useConnectionStore.setState({ ...initialConnection, isConnected: false, connection: null }, true)
  useChatPreferencesStore.getState().setVerboseChatEnabled(false)
  resetApiSchemaErrorCount()
})

afterEach(() => {
  cleanup()
  for (const client of queryClients.splice(0)) client.clear()
  useChatStore.setState(initialChat, true)
  useSessionStore.setState(initialSession, true)
  useConnectionStore.setState(initialConnection, true)
  useChatPreferencesStore.getState().setVerboseChatEnabled(false)
  resetApiSchemaErrorCount()
  vi.unstubAllGlobals()
})

describe('Q33: real REST reload and classified replay', () => {
  it.each(KINDS)('real history GET decoding retains %s classification, payload and identity', async (kind) => {
    history = [...ordinaryHistory(), storedNotice(kind)]
    const decoded = await fetchSessionMessages(SID)
    expect(fetchNetwork).toHaveBeenCalledExactlyOnceWith(`/api/v1/sessions/${SID}/messages`, {
      credentials: 'include', headers: { 'Content-Type': 'application/json' },
    })
    expect(getApiSchemaErrorCount(), 'the contract-valid notice must not become a validation-error placeholder').toBe(0)
    expect(decoded.map((entry) => entry.id), 'the diagnostic survives the real per-entry REST decoder').toEqual([
      'q33-history-answer', 'q33-history-ordinary-copy', ENTRY_ID,
    ])
    expect(decoded[2], 'REST mapping must preserve the classified carrier, not reduce it to an ordinary system sentence').toMatchObject({
      id: ENTRY_ID, role: 'system', type: 'context_window_notice', content: NOTICE,
      timestamp: NOTICE_TIME, agentId: AUTHOR, turnId: TURN_ID, contextWindowNotice: noticePayload(kind),
    })
    expect(decoded[2], 'the decoded payload is the exact closed kind/message object').toHaveProperty('contextWindowNotice', noticePayload(kind))
  })

  it.each(KINDS)('REST fallback uses the shared Verbose behavior for retained %s history without refetching', async (kind) => {
    history = [...ordinaryHistory(), storedNotice(kind)]
    // The store is genuinely empty and replay is neither active nor completed:
    // ChatScreen must fetch, decode and hydrate this history itself.
    expect(useChatStore.getState().messages).toEqual([])
    await renderHistoryThread()
    await expectRetainedVerboseToggle(kind)
    const historyCalls = fetchNetwork.mock.calls.filter(([input]) => input === `/api/v1/sessions/${SID}/messages`)
    expect(historyCalls, 'preference changes must not reload dropped diagnostic data').toHaveLength(1)
    expect(getApiSchemaErrorCount()).toBe(0)
  })

  it.each(KINDS)('replayed %s notice survives Verbose off, advances the cursor and deduplicates its original entry', async (kind) => {
    await act(async () => {
      useChatStore.getState().setReplaying(true)
      for (const entry of ordinaryHistory()) {
        const frame: ReplayMessageFrame = {
          type: 'replay_message', session_id: SID, id: entry.id,
          role: entry.role ?? 'system', content: entry.content ?? '',
          timestamp: entry.timestamp, agent_id: entry.agent_id,
        }
        useChatStore.getState().handleFrame(frame)
      }
      // Deliver the actual classified replay carrier twice, as on reconnect.
      // No already-normalized notice is inserted through setMessages.
      useChatStore.getState().handleFrame(noticeFrame(kind))
      useChatStore.getState().handleFrame(noticeFrame(kind))
      useChatStore.getState().setReplaying(false)
    })
    await waitFor(() => expect(useChatStore.getState().isReplaying).toBe(false))
    expect.soft(useChatStore.getState().sessionsById[SID]?.lastReceivedEventTime,
      'a hidden replay diagnostic still advances the original timestamp cursor').toBe(NOTICE_TIME)
    await renderHistoryThread()
    await expectRetainedVerboseToggle(kind)
    expect(getApiSchemaErrorCount()).toBe(0)
  })

  it.each(KINDS)('one real predicate classifies live, REST and replay %s carriers by type rather than wording', (kind) => {
    const predicate = realNoticePredicate()
    const live = noticeFrame(kind)
    const carriers = [
      { source: 'live', entry: live },
      { source: 'REST', entry: storedNotice(kind) },
      { source: 'replay', entry: { ...live } },
    ]
    for (const { source, entry } of carriers) {
      expect(predicate(entry.type, false), `${source}: classified diagnostic hidden by default`).toBe(false)
      expect(predicate(entry.type, true), `${source}: same retained diagnostic revealed by Verbose`).toBe(true)
      expect(predicate(entry.type, false), `${source}: toggling changes visibility only`).toBe(false)
    }
    for (const ordinary of ordinaryHistory()) {
      expect(predicate(ordinary.type, false), 'ordinary messages, including identical text, remain visible').toBe(true)
      expect(predicate(ordinary.type, true), 'Verbose does not change ordinary-message visibility').toBe(true)
    }
  })
})

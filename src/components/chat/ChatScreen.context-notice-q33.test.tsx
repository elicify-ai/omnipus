// Q33 (#1081), founder Q4 / the relayed ADR-066 carrier amendment:
// context_window_notice is hidden in normal chat and visible in Verbose chat.
// Classify by type, never by the sentence. The carrier is still awaiting its
// contract commit/review; these are input fixtures, not parallel wire types.
//
// Real: ChatScreen, chat/frame reducers, preferences, AssistantUI runtime,
// router, and query provider. Fake: backend network responses only.
// The stored-message cases verify the common renderer, not REST/replay decoding.
// The live cases additionally exercise handleFrame with the named WS envelope.
import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, render, screen } from '@testing-library/react'
import { AssistantRuntimeProvider } from '@assistant-ui/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from '@tanstack/react-router'
import { useOmnipusRuntime } from '@/lib/omnipus-runtime'
import { useChatStore } from '@/store/chat'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { ChatScreen } from './ChatScreen'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([]),
    fetchSessionMessages: vi.fn().mockResolvedValue([]),
    fetchProviders: vi.fn().mockResolvedValue([]),
    fetchCommands: vi.fn().mockResolvedValue([]),
    fetchSkills: vi.fn().mockResolvedValue([]),
  }
})

const SID = 'q33-context-notice-session'
const TIMESTAMP = '2026-09-30T12:00:00Z'
const NORMAL_ANSWER = 'The task continued successfully.'
const NOTICE = 'Context window exceeded. Compressing history and retrying...'
// A changed diagnostic sentence must have the same classification behaviour.
const OTHER_COPY = 'The context budget was reduced before the provider retry.'
const initialChat = useChatStore.getState()
const initialSession = useSessionStore.getState()
const initialConnection = useConnectionStore.getState()
const queryClients: QueryClient[] = []

function ThreadRuntime() {
  const runtime = useOmnipusRuntime()
  return (
    <AssistantRuntimeProvider runtime={runtime}>
      <ChatScreen />
    </AssistantRuntimeProvider>
  )
}

async function renderThread() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: Infinity } },
  })
  queryClients.push(queryClient)
  const root = createRootRoute({
    component: () => (
      <QueryClientProvider client={queryClient}>
        <ThreadRuntime />
      </QueryClientProvider>
    ),
  })
  const router = createRouter({
    routeTree: root,
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  await act(async () => {
    await router.load()
    render(<RouterProvider router={router} />)
  })
  // Positive instrument control: an empty/failed thread cannot pass absence.
  expect(await screen.findByText(NORMAL_ANSWER)).toBeVisible()
}

function seedThread(message: string, includeNotice: boolean) {
  // Decode literal input data because the architect's new classification is
  // not in the generated/current display types yet. No cast, handwritten
  // wire interface, or mocked renderer is used to make that gap disappear.
  const messages = [
    {
      id: 'q33-answer', session_id: SID, role: 'assistant',
      content: NORMAL_ANSWER, timestamp: TIMESTAMP, status: 'done',
    },
    {
      id: 'q33-ordinary-copy', session_id: SID, role: 'system',
      content: message, timestamp: TIMESTAMP, status: 'done',
    },
    ...(includeNotice ? [{
      id: 'q33-context-notice', session_id: SID, role: 'system',
      type: 'context_window_notice', timestamp: TIMESTAMP, status: 'done',
      content: message,
      notice: { kind: 'provider_retry', message },
    }] : []),
  ]
  useChatStore.getState().setMessages(JSON.parse(JSON.stringify(messages)))
  useChatStore.setState({ replayCompletedForSession: SID })
  expect(useChatStore.getState().messages.map((entry) => entry.id)).toEqual(
    includeNotice
      ? ['q33-answer', 'q33-ordinary-copy', 'q33-context-notice']
      : ['q33-answer', 'q33-ordinary-copy'],
  )
}

beforeEach(() => {
  useChatStore.setState(initialChat, true)
  useSessionStore.setState({ ...initialSession, activeSessionId: SID, activeAgentId: 'mia' }, true)
  useConnectionStore.setState({ ...initialConnection, isConnected: true, connection: null }, true)
  useChatPreferencesStore.getState().setVerboseChatEnabled(false)
  // jsdom has no layout engine: use the production PlainMessageList fallback,
  // which still renders the real historical/system rows and visibility gates.
  vi.stubGlobal('ResizeObserver', undefined)
})

afterEach(() => {
  cleanup()
  for (const client of queryClients.splice(0)) client.clear()
  useChatStore.setState(initialChat, true)
  useSessionStore.setState(initialSession, true)
  useConnectionStore.setState(initialConnection, true)
  useChatPreferencesStore.getState().setVerboseChatEnabled(false)
  vi.unstubAllGlobals()
})

describe('Q33: classified context notices in the real chat thread', () => {
  it.each([NOTICE, OTHER_COPY])('hides the classified notice with Verbose off, not ordinary identical text: %s', async (message) => {
    seedThread(message, true)
    await renderThread()

    // Exactly the unclassified system message remains. A text-matching filter
    // would hide both; no filter would leave both visible.
    expect(screen.queryAllByText(message, { exact: true })).toHaveLength(1)
  })

  it.each([NOTICE, OTHER_COPY])('responds to the real Verbose preference without losing the classified notice: %s', async (message) => {
    seedThread(message, true)
    useChatPreferencesStore.getState().setVerboseChatEnabled(true)
    await renderThread()
    expect(screen.getAllByText(message, { exact: true })).toHaveLength(2)

    await act(async () => useChatPreferencesStore.getState().setVerboseChatEnabled(false))
    expect.soft(screen.queryAllByText(message, { exact: true })).toHaveLength(1)

    await act(async () => useChatPreferencesStore.getState().setVerboseChatEnabled(true))
    expect(screen.getAllByText(message, { exact: true })).toHaveLength(2)
    expect(useChatStore.getState().messages.map((entry) => entry.id)).toEqual([
      'q33-answer', 'q33-ordinary-copy', 'q33-context-notice',
    ])
  })

  it.each([NOTICE, OTHER_COPY])('delivers a live context_window_notice through the real reducer when Verbose is on: %s', async (message) => {
    seedThread(message, false)
    useChatPreferencesStore.getState().setVerboseChatEnabled(true)
    await renderThread()
    expect(screen.getAllByText(message, { exact: true })).toHaveLength(1)

    // Exact architect-named envelope. JSON decoding represents the incoming
    // frame without inventing a type before contract generation has landed.
    const frame = JSON.parse(JSON.stringify({
      type: 'context_window_notice', session_id: SID,
      turn_id: 'q33-turn', agent_id: 'mia', entry_id: 'q33-live-notice',
      timestamp: TIMESTAMP, notice: { kind: 'provider_retry', message },
    }))
    await act(async () => useChatStore.getState().handleFrame(frame))
    expect(screen.getAllByText(message, { exact: true })).toHaveLength(2)

    await act(async () => useChatPreferencesStore.getState().setVerboseChatEnabled(false))
    expect(screen.queryAllByText(message, { exact: true })).toHaveLength(1)
    expect(screen.getByText(NORMAL_ANSWER)).toBeVisible()
  })
})

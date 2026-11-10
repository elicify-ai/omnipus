import type { ReactNode } from 'react'
import { act, cleanup, render, within } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { WsConnection } from '@/lib/ws'
import { queryClient } from '@/lib/queryClient'
import { convertMessage } from '@/lib/omnipus-runtime'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { ChatScreen } from './ChatScreen'
import { codeToDisplay } from '@/lib/llm-error'

// #1671: a stopped (cancelled) assistant turn is incomplete with reason
// cancelled — it shows "(interrupted)" AND its row reports
// data-status="incomplete", never "complete". Oracle: the product's own
// status contract — src/lib/omnipus-runtime.ts::buildMessageStatus maps a
// cancelled turn to { type: 'incomplete', reason: 'cancelled' };
// src/lib/truncation.ts::getMessageStatusSuffix treats status 'interrupted'
// OR truncationReason 'cancelled' as the same cancelled outcome. A settled
// reply is data-status="complete" (tests/e2e/bug-regression.spec.ts
// SETTLED_REPLY_SELECTOR). Covers the live Stop path and the replayed
// (reloaded) cancelled turn. Real stores, frame routing and message rows;
// AssistantUI's external adapter and remote services are test stand-ins
// (same harness as ChatScreen.interruption-history.test.tsx).
vi.mock('@assistant-ui/react', async () => (await import('@/test/assistantUiMock')).createAssistantUiMock())
vi.mock('@tanstack/react-router', () => ({
  useRouter: () => ({ navigate: vi.fn() }), useNavigate: () => vi.fn(),
  useSearch: () => ({}), Link: ({ children }: { children: ReactNode }) => children,
}))
import { emptyHistoryPage, type HistoryReadPort } from '../../../tests/chat-history-paging/legacy-fixtures'

vi.mock('@/lib/api', async (original) => ({
  ...await original<typeof import('@/lib/api')>(),
  fetchAgents: vi.fn().mockResolvedValue([]), fetchSessionMessages: vi.fn<HistoryReadPort>(async (sessionId) => emptyHistoryPage(sessionId)),
  fetchAboutInfo: vi.fn().mockResolvedValue({ preview_port: 5001 }), fetchProviders: vi.fn().mockResolvedValue([]),
  fetchCommands: vi.fn().mockResolvedValue([]), fetchSkills: vi.fn().mockResolvedValue([]),
}))
vi.mock('./composer/ModelPicker', () => ({ ModelPicker: () => null }))
vi.mock('./composer/TokenCounter', () => ({ TokenCounter: () => null }))
vi.mock('@/lib/memory-observer', () => ({
  startMemoryObserver: () => ({ dispose: vi.fn(), getCurrentSnapshot: vi.fn() }),
  addMemoryObserver: () => () => {}, getCurrentSnapshot: () => ({ usedJSHeapSizeBytes: null, level: 'ok', supported: false }),
}))
const SID = 'cancelled-turn-status'
const sender = { send: vi.fn<WsConnection['send']>().mockReturnValue(true) }

beforeEach(() => {
  vi.stubGlobal('ResizeObserver', undefined)
  queryClient.clear()
  act(() => {
    useSessionStore.setState(useSessionStore.getInitialState(), true)
    useChatStore.setState(useChatStore.getInitialState(), true)
    useConnectionStore.setState(useConnectionStore.getInitialState(), true)
    useConnectionStore.getState().setConnection(sender as unknown as WsConnection)
    useConnectionStore.getState().setConnected(true)
    useSessionStore.getState().setActiveSession(SID, 'jim')
  })
})
afterEach(() => { cleanup(); queryClient.clear(); vi.unstubAllGlobals() })

function row(id: string) {
  const rows = document.querySelectorAll(`[data-testid="assistant-message"][data-message-id="${id}"]`)
  expect(rows).toHaveLength(1)
  return rows[0] as HTMLElement
}

function runtimeStatus(id: string) {
  const state = useChatStore.getState()
  const msg = state.messagesById[id]
  return convertMessage(msg, {}, [], {}, false).status
}

function assistantIdWithContent(content: string) {
  return useChatStore.getState().messages.find((m) => m.role === 'assistant' && m.content === content)!.id
}

// Live Stop with a partial reply, then the server's cancel ack + done
// (carrying the cancelled truncation stats the gateway stamps).
function stoppedWithPartial() {
  useChatStore.getState().sendMessage('Begin work', { clientMessageId: 'user-partial' })
  useChatStore.getState().handleFrame({ type: 'token', session_id: SID, turn_id: 'partial-turn', message_id: 'partial-reply', content: 'Partial answer' })
  const id = assistantIdWithContent('Partial answer')
  useChatStore.getState().cancelStream()
  useChatStore.getState().handleFrame({ type: 'error', session_id: SID, message: codeToDisplay.turn_canceled, payload: { llm_error: { code: 'turn_canceled', message: codeToDisplay.turn_canceled, retryable: true } } })
  useChatStore.getState().handleFrame({ type: 'done', session_id: SID, turn_id: 'partial-turn', stats: { tokens: 1, cost: 0, truncated: true, truncation_reason: 'cancelled' } })
  return id
}

// Live Stop while the provider is still silent (Bug-3-a: the mock holds
// tab B's reply open, no token ever arrives), then the server's done.
function stoppedBeforeFirstToken() {
  useChatStore.getState().sendMessage('Hold this one', { clientMessageId: 'user-held' })
  useChatStore.getState().cancelStream()
  useChatStore.getState().handleFrame({ type: 'done', session_id: SID, turn_id: 'held-turn', stats: { tokens: 0, cost: 0, truncated: true, truncation_reason: 'cancelled' } })
  const ids = useChatStore.getState().messages.filter((m) => m.role === 'assistant').map((m) => m.id)
  expect(ids, 'exactly one assistant row for the stopped turn').toHaveLength(1)
  return ids[0]
}

// Reloaded session: the gateway replays the cancelled entry with the ADR-087
// truncation markers (truncated + truncation_reason 'cancelled').
function replayedCancelled() {
  useChatStore.getState().handleFrame({ type: 'replay_message', session_id: SID, id: 'old-reply', role: 'assistant', content: 'Original partial reply', turn_id: 'old-turn', truncated: true, truncation_reason: 'cancelled' })
  useChatStore.getState().handleFrame({ type: 'catch_up_complete', session_id: SID, seq: 1, boot_id: 'cancelled-status-boot', mode: 'snapshot' })
  return 'old-reply'
}

function settledReply() {
  useChatStore.getState().sendMessage('Are you alive?', { clientMessageId: 'user-fresh' })
  useChatStore.getState().handleFrame({ type: 'token', session_id: SID, turn_id: 'fresh-turn', message_id: 'fresh-reply', content: 'ALIVE' })
  useChatStore.getState().handleFrame({ type: 'done', session_id: SID, turn_id: 'fresh-turn', stats: { tokens: 1, cost: 0 } })
  return assistantIdWithContent('ALIVE')
}

describe('#1671 — a stopped assistant turn is incomplete (cancelled), not complete', () => {
  it.each([
    ['live Stop after a partial reply', stoppedWithPartial],
    ['live Stop before the first token', stoppedBeforeFirstToken],
    ['replayed cancelled turn after reload', replayedCancelled],
  ] as const)('%s: row is data-status="incomplete" with exactly one "(interrupted)"', async (_label, scenario) => {
    let id = ''
    act(() => { id = scenario() })
    expect(runtimeStatus(id), 'runtime status of the cancelled turn').toStrictEqual({ type: 'incomplete', reason: 'cancelled' })
    await act(async () => { render(<QueryClientProvider client={queryClient}><ChatScreen /></QueryClientProvider>) })
    const stopped = row(id)
    expect(stopped).toHaveAttribute('data-status', 'incomplete')
    expect(within(stopped).getAllByText('(interrupted)', { exact: true })).toHaveLength(1)
  })

  it('CONTROL: a settled reply stays data-status="complete" with no suffix', async () => {
    let id = ''
    act(() => { id = settledReply() })
    expect(runtimeStatus(id)).toStrictEqual({ type: 'complete', reason: 'stop' })
    await act(async () => { render(<QueryClientProvider client={queryClient}><ChatScreen /></QueryClientProvider>) })
    const fresh = row(id)
    expect(fresh).toHaveAttribute('data-status', 'complete')
    expect(within(fresh).queryByText('(interrupted)', { exact: true })).not.toBeInTheDocument()
  })
})

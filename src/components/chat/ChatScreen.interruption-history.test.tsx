import type { ReactNode } from 'react'
import { act, cleanup, render, screen, within } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { WsConnection } from '@/lib/ws'
import { queryClient } from '@/lib/queryClient'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { ChatScreen } from './ChatScreen'

// U6-R1: historical interruption belongs only to that message, never a
// conversation footer. Real stores, sends/cancel/frame routing and message
// rows (including actual markdown). AssistantUI's external adapter and
// incidental remote services/composer pickers are test stand-ins only.
vi.mock('@assistant-ui/react', async () => (await import('@/test/assistantUiMock')).createAssistantUiMock())
vi.mock('@tanstack/react-router', () => ({
  useRouter: () => ({ navigate: vi.fn() }), useNavigate: () => vi.fn(),
  useSearch: () => ({}), Link: ({ children }: { children: ReactNode }) => children,
}))
vi.mock('@/lib/api', async (original) => ({
  ...await original<typeof import('@/lib/api')>(),
  fetchAgents: vi.fn().mockResolvedValue([]), fetchSessionMessages: vi.fn().mockResolvedValue([]),
  fetchAboutInfo: vi.fn().mockResolvedValue({ preview_port: 5001 }), fetchProviders: vi.fn().mockResolvedValue([]),
  fetchCommands: vi.fn().mockResolvedValue([]), fetchSkills: vi.fn().mockResolvedValue([]),
}))
vi.mock('./composer/AgentPicker', () => ({ AgentPicker: () => null }))
vi.mock('./composer/ModelPicker', () => ({ ModelPicker: () => null }))
vi.mock('./composer/TokenCounter', () => ({ TokenCounter: () => null }))
vi.mock('@/lib/memory-observer', () => ({
  startMemoryObserver: () => ({ dispose: vi.fn(), getCurrentSnapshot: vi.fn() }),
  addMemoryObserver: () => () => {}, getCurrentSnapshot: () => ({ usedJSHeapSizeBytes: null, level: 'ok', supported: false }),
}))
const SID = 'uat-interrupted-history'
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

function oldReply() {
  useChatStore.getState().sendMessage('Begin work', { clientMessageId: 'user-old' })
  useChatStore.getState().handleFrame({ type: 'token', session_id: SID, turn_id: 'old-turn', message_id: 'old-reply', content: 'Original partial reply' })
  const id = useChatStore.getState().messages.find((message) => message.role === 'assistant' && message.content === 'Original partial reply')!.id
  useChatStore.getState().cancelStream()
  useChatStore.getState().handleFrame({ type: 'error', session_id: SID, message: 'This turn was stopped before it finished.', payload: { llm_error: { code: 'turn_canceled', message: 'This turn was stopped before it finished.', retryable: true } } })
  useChatStore.getState().handleFrame({ type: 'done', session_id: SID, turn_id: 'old-turn', stats: { tokens: 1, cost: 0 } })
  return id
}
function freshReply() {
  useChatStore.getState().sendMessage('Are you alive?', { clientMessageId: 'user-fresh' })
  useChatStore.getState().handleFrame({ type: 'token', session_id: SID, turn_id: 'fresh-turn', message_id: 'fresh-reply', content: 'ALIVE' })
  useChatStore.getState().handleFrame({ type: 'done', session_id: SID, turn_id: 'fresh-turn', stats: { tokens: 1, cost: 0 } })
  return useChatStore.getState().messages.find((message) => message.role === 'assistant' && message.content === 'ALIVE')!.id
}

describe('U6 — interrupted history never leaves a loose legend under a fresh reply', () => {
  it.each(['live', 'replay'] as const)('%s retains exactly one suffix on the old partial message and none on the successful ALIVE reply', async (mode) => {
    let oldId = 'old-reply'
    let freshId = 'fresh-reply'
    act(() => {
      if (mode === 'live') { oldId = oldReply(); freshId = freshReply() }
      else {
        useChatStore.getState().handleFrame({ type: 'replay_message', session_id: SID, id: 'old-reply', role: 'assistant', content: 'Original partial reply', turn_id: 'old-turn', truncated: true, truncation_reason: 'cancelled' })
        useChatStore.getState().handleFrame({ type: 'replay_message', session_id: SID, id: 'fresh-reply', role: 'assistant', content: 'ALIVE', turn_id: 'fresh-turn' })
        useChatStore.getState().handleFrame({ type: 'catch_up_complete', session_id: SID, seq: 0, boot_id: 'uat-history-boot', mode: 'snapshot' })
      }
    })
    await act(async () => { render(<QueryClientProvider client={queryClient}><ChatScreen /></QueryClientProvider>) })
    const old = row(oldId)
    const fresh = row(freshId)
    expect(within(old).getByText('Original partial reply')).toBeInTheDocument()
    expect(within(old).getAllByText('(interrupted)', { exact: true })).toHaveLength(1)
    expect(within(fresh).getByText('ALIVE')).toBeInTheDocument()
    expect(within(fresh).queryByText('(interrupted)', { exact: true })).not.toBeInTheDocument()
    if (mode === 'live') expect(useChatStore.getState().messagesById[oldId].status).toBe('interrupted')
    else expect(useChatStore.getState().messagesById[oldId].truncationReason).toBe('cancelled')
    expect(useChatStore.getState().messagesById[freshId].status).toBe('done')
    expect(useChatStore.getState().messagesById[freshId].truncated).not.toBe(true)
    expect(screen.getAllByText('(interrupted)', { exact: true })).toHaveLength(1)
    expect(screen.queryByTestId('interrupted-marker')).not.toBeInTheDocument()
  })

  it('a successful reply with no interrupted history has no suffix or conversation-level marker', async () => {
    let freshId = ''
    act(() => { freshId = freshReply() })
    await act(async () => { render(<QueryClientProvider client={queryClient}><ChatScreen /></QueryClientProvider>) })
    expect(within(row(freshId)).getByText('ALIVE')).toBeInTheDocument()
    expect(screen.queryByText('(interrupted)', { exact: true })).not.toBeInTheDocument()
    expect(screen.queryByText('(cut off at the output limit)', { exact: true })).not.toBeInTheDocument()
    expect(screen.queryByTestId('interrupted-marker')).not.toBeInTheDocument()
  })
})

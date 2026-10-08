// When no main or welcome chat can be identified, the message box must say so
// and refuse to send. Retry asks workspace entry to choose again.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import * as React from 'react'
import { act } from 'react'
import { useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import type { WsConnection } from '@/lib/ws'
import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
if (typeof globalThis.ResizeObserver === 'undefined') {
  vi.stubGlobal('ResizeObserver', ResizeObserverStub)
}
if (typeof Element !== 'undefined' && !Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = function () {}
}

const runWorkspaceEntry = vi.hoisted(() => vi.fn().mockResolvedValue(undefined))

vi.mock('@/store/session/workspaceEntryFlow', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/store/session/workspaceEntryFlow')>()
  return { ...actual, runWorkspaceEntry }
})

import { OmnipusComposer } from './ChatScreen'

vi.mock('@assistant-ui/react', async () => (await import('@/test/assistantUiMock')).createAssistantUiMock())

vi.mock('@tanstack/react-query', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-query')>()
  return {
    ...actual,
    useQuery: () => ({ data: [], isError: false, refetch: vi.fn() }),
    useMutation: () => ({ mutate: vi.fn(), isPending: false }),
    useQueryClient: () => ({ invalidateQueries: vi.fn(), removeQueries: vi.fn() }),
  }
})

vi.mock('@tanstack/react-router', () => ({
  useRouter: () => ({ navigate: vi.fn() }),
  useSearch: () => ({}),
  Link: ({ children }: { children: React.ReactNode }) => children,
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([]),
    fetchSessionMessages: vi.fn().mockResolvedValue([]),
    fetchCommands: vi.fn().mockResolvedValue([]),
    fetchSkills: vi.fn().mockResolvedValue([]),
    fetchProviders: vi.fn().mockResolvedValue([]),
    uploadFiles: vi.fn(),
  }
})

vi.mock('@/assets/logo/omnipus-avatar.svg?url', () => ({ default: 'omnipus-avatar.svg' }))
vi.mock('./RateLimitIndicator', () => ({ RateLimitIndicator: () => null }))
vi.mock('./markdown-text', () => ({ MarkdownText: () => null }))
vi.mock('./tools/GenericToolCall', () => ({ GenericToolCall: () => null }))
vi.mock('@/components/shared/IconRenderer', () => ({ IconRenderer: () => null }))
vi.mock('./composer/ModelPicker', () => ({ ModelPicker: () => null }))
vi.mock('./composer/TokenCounter', () => ({ TokenCounter: () => null }))

const unavailable = {
  status: 'unavailable' as const,
  sessionId: null,
  sendEnabled: false as const,
  acknowledged: false as const,
  retry: true as const,
}

function resetStores() {
  act(() => {
    useChatStore.setState({
      messages: [],
      isStreaming: false,
      isReplaying: false,
      toolCalls: {},
      sessionTokens: 0,
      sessionCost: 0,
      outboundQueue: [],
      pendingDrainQueue: [],
    })
    useConnectionStore.setState({
      connection: { send: vi.fn().mockReturnValue(true) } as unknown as WsConnection,
      isConnected: true,
      connectionError: null,
      reconnectPhase: null,
      reconnectAttempt: 0,
    })
    useSessionStore.setState({
      activeSessionId: null,
      activeAgentId: null,
      activeAgentType: null,
      workspaceEntry: null,
      resolvingSessionForWorkspace: {},
    })
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-product' })
  })
}

beforeEach(() => {
  runWorkspaceEntry.mockClear()
  resetStores()
})

describe('unavailable workspace chat', () => {
  it('shows the notice where the person types, disables send, and retries entry', () => {
    act(() => {
      useSessionStore.setState({ workspaceEntry: unavailable })
    })
    render(<OmnipusComposer />)

    const notice = screen.getByText('This chat is unavailable right now')
    expect(notice.closest('[role="status"]')).toBeTruthy()
    expect(screen.getByTestId('composer-input')).toBeDisabled()
    expect(screen.getByTestId('chat-send')).toBeDisabled()

    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(runWorkspaceEntry).toHaveBeenCalledWith('ws-product')
  })

  it('failed restore keeps the old chat visible but disables composer and retries the target workspace', () => {
    act(() => useSessionStore.setState({
      activeSessionId: 'retained-source-chat',
      activeAgentId: 'mia',
      workspaceEntry: {
        status: 'failed-attempt', committed: { sessionId: 'retained-source-chat', agentId: 'mia' },
        attempted: { sessionId: 'target-chat', sendEnabled: false },
        acknowledged: false, retry: true, fellBack: false,
      },
    }))
    render(<OmnipusComposer />)
    expect(screen.getByText('Could not restore your last conversation. Retry to try again.')).toBeInTheDocument()
    expect(screen.getByTestId('composer-input')).toBeDisabled()
    expect(screen.getByTestId('chat-send')).toBeDisabled()
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(runWorkspaceEntry).toHaveBeenCalledTimes(1)
    expect(runWorkspaceEntry).toHaveBeenCalledWith('ws-product')
    expect(useSessionStore.getState().activeSessionId).toBe('retained-source-chat')
  })

  it('disables the composer while validating the target workspace without changing the retained chat', () => {
    act(() => useSessionStore.setState({
      activeSessionId: 'retained-source-chat', activeAgentId: 'mia',
      resolvingSessionForWorkspace: { 'ws-product': true },
    }))
    render(<OmnipusComposer />)
    expect(screen.getByTestId('composer-input')).toBeDisabled()
    expect(screen.getByTestId('chat-send')).toBeDisabled()
    expect(useSessionStore.getState().activeSessionId).toBe('retained-source-chat')
  })

  it('says nothing and leaves send enabled when a chat is available', () => {
    render(<OmnipusComposer />)
    expect(screen.queryByText('This chat is unavailable right now')).toBeNull()
    expect(screen.getByTestId('composer-input')).not.toBeDisabled()
    expect(screen.getByTestId('chat-send')).not.toBeDisabled()
  })
})

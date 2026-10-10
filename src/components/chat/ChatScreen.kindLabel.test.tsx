/**
 * BDD-07.1 above the feed. The label is a status element rendered before the
 * message text. ChatScreen reads the active workspace descriptor
 * (sessionByWorkspace[activeWorkspaceId], when its id is the active session)
 * and the seam. Paths (live or replay) do not change the words.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, act } from '@testing-library/react'
import * as React from 'react'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { useWorkspacesStore } from '@/store/workspacesStore'

const seam = vi.hoisted(() => ({
  mains: new Set<string>(),
}))

vi.mock('@/lib/nav/sessionCoreSeam', () => ({
  mainSessionIdOfMember: () => undefined,
  isMainSession: (session: unknown) => {
    const id = session && typeof session === 'object' && 'id' in session
      ? String((session as { id: unknown }).id)
      : ''
    return seam.mains.has(id)
  },
  sessionAttention: () => 'unknown',
  attachAckFields: () => ({}),
  attentionBoundOfFrame: () => undefined,
}))

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
    fetchAboutInfo: vi.fn().mockResolvedValue({}),
    createSession: vi.fn(),
    uploadFiles: vi.fn(),
    fetchProviders: vi.fn().mockResolvedValue([]),
    isApiError: vi.fn().mockReturnValue(false),
    fetchCommands: vi.fn().mockResolvedValue([]),
    fetchSkills: vi.fn().mockResolvedValue([]),
  }
})

vi.mock('./historical-markdown', () => ({
  HistoricalMessageMarkdown: ({ content }: { content: string }) =>
    React.createElement('div', null, content),
}))
vi.mock('@/assets/logo/omnipus-avatar.svg?url', () => ({ default: 'omnipus-avatar.svg' }))
vi.mock('./RateLimitIndicator', () => ({ RateLimitIndicator: () => null }))
vi.mock('./tools/GenericToolCall', () => ({ GenericToolCall: () => null }))
vi.mock('./markdown-text', () => ({ MarkdownText: () => null }))
vi.mock('./composer/ModelPicker', () => ({ ModelPicker: () => null }))
vi.mock('./composer/TokenCounter', () => ({ TokenCounter: () => null }))
vi.mock('@/lib/memory-observer', () => ({
  startMemoryObserver: () => ({ dispose: vi.fn(), getCurrentSnapshot: vi.fn() }),
  addMemoryObserver: () => () => {},
  getCurrentSnapshot: () => ({ usedJSHeapSizeBytes: null, level: 'ok', supported: false }),
}))

import { ChatScreen } from './ChatScreen'

const WS = 'ws-1'

function seed(sessionId: string, descriptorType: 'chat' | 'task' | 'delegate', title: string): void {
  const messages = [{
    id: 'msg-user',
    role: 'user' as const,
    content: 'hello from the feed',
    timestamp: '2026-03-29T10:00:00Z',
    status: 'done' as const,
  }]
  useChatStore.setState({
    messages,
    isStreaming: false,
    isReplaying: false,
  })
  useWorkspacesStore.setState({ activeWorkspaceId: WS })
  useSessionStore.setState({
    activeSessionId: sessionId,
    activeAgentId: 'mia',
    sessionByWorkspace: {
      [WS]: { id: sessionId, type: descriptorType, title, agentId: 'mia' },
    },
  })
}

async function renderScreen(): Promise<void> {
  await act(async () => {
    render(<ChatScreen />)
  })
}

function expectLabelAboveFeed(name: string): void {
  const label = screen.getByRole('status', { name })
  const message = screen.getByText('hello from the feed')
  expect(label.compareDocumentPosition(message) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
}

beforeEach(() => {
  seam.mains.clear()
  useConnectionStore.setState({
    isConnected: true,
    liteMode: false,
    reconnectPhase: null,
    reconnectAttempt: 0,
    connectionError: null,
    connection: null,
  })
})

describe('ChatScreen kind label (BDD-07.1)', () => {
  it('shows "Main chat" above the feed when the seam says this session is the main', async () => {
    seam.mains.add('sid-main')
    seed('sid-main', 'chat', 'Launch notes')
    await renderScreen()
    expectLabelAboveFeed('Main chat')
  })

  it('shows "Extra chat — Launch notes" above the feed for an extra', async () => {
    seed('sid-extra', 'chat', 'Launch notes')
    await renderScreen()
    expectLabelAboveFeed('Extra chat — Launch notes')
  })

  it('shows "Task run" above the feed for a task', async () => {
    seed('sid-task', 'task', 'Deploy')
    await renderScreen()
    expectLabelAboveFeed('Task run')
  })

  it('shows "Helper" above the feed for a subordinate session', async () => {
    seed('sid-helper', 'delegate', 'Scan')
    await renderScreen()
    expectLabelAboveFeed('Helper')
  })

  it('keeps "Main chat" above the feed while history is replaying', async () => {
    seam.mains.add('sid-main')
    seed('sid-main', 'chat', 'Launch notes')
    useChatStore.setState({ isReplaying: true })
    await renderScreen()
    expectLabelAboveFeed('Main chat')
  })
})

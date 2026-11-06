/**
 * BDD-07.1 — the chat's kind label, in the header line above the feed — as
 * AMENDED for mains (founder decision 2026-10-11, amending FR-008 "Main chat
 * has no title suffix"): the line for a main session reads "<Agent name> -
 * Main Session", ALWAYS derived from the agent's CURRENT display name (S2-05
 * ruling, chief-confirmed 2026-10-12: the stored session.title is ignored for
 * a main, so no attach-time copy survives a rename) and following an agent
 * rename without a reload. Paths (live or replay) do not change the words.
 * Extra chats, tasks, and helpers keep their stored titles, unchanged.
 *
 * Where the rule lives now: #1449 D13 (founder 2026-10-28: no header bars, no
 * borders) moved the label OUT of ChatScreen. FeedKindLabel renders in
 * ChatHeaderSlot, the right end of the workspace header line — the row above
 * the chat region in WorkspaceTabContainer (the slot's place in that row is
 * pinned by WorkspaceTabContainer.test.tsx). The label keeps its status role,
 * its accessible name and its feed-kind-label test hook, so the SAME words and
 * rules are asserted here, on the header slot rendered together with the
 * screen it heads (ChatPage below). FeedKindLabel reads the active workspace
 * descriptor (sessionByWorkspace[activeWorkspaceId], when its id is the active
 * session), the agents roster and the seam.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, act, within } from '@testing-library/react'
import * as React from 'react'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { useWorkspacesStore } from '@/store/workspacesStore'
import type { Agent } from '@/lib/api'

const WS = vi.hoisted(() => 'ws-1')

const seam = vi.hoisted(() => ({
  mains: new Set<string>(),
}))

vi.mock('@/lib/nav/sessionCoreSeam', async (importOriginal) => ({
  // Real seam for everything the stubs don't override. (WU-11 GREEN removed the
  // Admin-main seam reader with the retired Admin row — FR-008.)
  ...await importOriginal<typeof import('@/lib/nav/sessionCoreSeam')>(),
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

// Key-aware agents data for the header: the shared ['agents'] payload is
// mutable per test (rename test flips it) while every OTHER query keeps the
// exact `data: []` shape this harness always served.
const agentsQuery = vi.hoisted(() => ({ data: [] as Agent[] }))

vi.mock('@tanstack/react-query', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-query')>()
  return {
    ...actual,
    useQuery: vi.fn((options: { queryKey: unknown[] }) => {
      const key = JSON.stringify(options.queryKey)
      if (key === '["agents"]') {
        return { data: agentsQuery.data, isPending: false, isError: false, refetch: vi.fn() }
      }
      return { data: queryDataWithEmptyHistory(options.queryKey), isError: false, refetch: vi.fn() }
    }),
    useMutation: () => ({ mutate: vi.fn(), isPending: false }),
    useQueryClient: () => ({ invalidateQueries: vi.fn(), removeQueries: vi.fn() }),
  }
})

vi.mock('@tanstack/react-router', () => ({
  useRouter: () => ({ navigate: vi.fn() }),
  useSearch: () => ({}),
  // The header slot shows the chat only on the workspace's session-chat route.
  useLocation: () => ({ pathname: `/workspaces/${WS}/chat`, search: {} }),
  useNavigate: () => vi.fn(),
  Link: ({ children }: { children: React.ReactNode }) => children,
}))

import { emptyHistoryPage, queryDataWithEmptyHistory, type HistoryReadPort } from '../../../tests/chat-history-paging/legacy-fixtures'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([]),
    fetchSessionMessages: vi.fn<HistoryReadPort>(async (sessionId) => emptyHistoryPage(sessionId)),
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
import { ChatHeaderSlot } from './ChatHeaderSlot'

function agent(id: string, name: string): Agent {
  return {
    id,
    name,
    type: 'core',
    locked: true,
    figure: {} as Agent['figure'],
    role: {} as Agent['role'],
    revision: {} as Agent['revision'],
  } as Agent
}

function seed(sessionId: string, descriptorType: 'chat' | 'task' | 'delegate' | 'main', title: string | null, agentId: string | null = 'mia'): void {
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
    activeAgentId: agentId ?? 'mia',
    sessionByWorkspace: {
      [WS]: { id: sessionId, type: descriptorType, title, agentId },
    },
  })
}

/**
 * The chat page as the app composes it (WorkspaceTabContainer): the workspace
 * header line first — its right-end slot carries the chat's kind label — then
 * the chat screen below it.
 */
function ChatPage() {
  return (
    <>
      <header role="banner" data-testid="workspace-top-bar">
        <ChatHeaderSlot workspaceId={WS} />
      </header>
      <ChatScreen />
    </>
  )
}

async function renderChatPage(): Promise<ReturnType<typeof render>> {
  let view!: ReturnType<typeof render>
  await act(async () => {
    view = render(<ChatPage />)
  })
  return view
}

function expectLabelInHeaderAboveFeed(name: string): void {
  // The label lives in the header line's right-end slot ...
  const label = within(screen.getByTestId('chat-header-slot')).getByRole('status', { name })
  // ... and the words on screen are the words named for assistive technology.
  expect(label.textContent).toBe(name)
  // ... it is the ONLY kind label on the page (D13: the bar the screen used to
  // render above its feed is gone, and must not come back beside the slot) ...
  expect(screen.getAllByTestId('feed-kind-label')).toHaveLength(1)
  // ... and the header line is above the feed.
  const message = screen.getByText('hello from the feed')
  expect(label.compareDocumentPosition(message) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
}

beforeEach(() => {
  seam.mains.clear()
  agentsQuery.data = []
  useConnectionStore.setState({
    isConnected: true,
    liteMode: false,
    reconnectPhase: null,
    reconnectAttempt: 0,
    connectionError: null,
    connection: null,
  })
})

describe('Chat kind label in the header line (BDD-07.1; mains as amended 2026-10-11)', () => {
  it('shows "Jim - Main Session" in the header line above the feed for an untitled main (FR-008 amended, founder decision 2026-10-11)', async () => {
    seam.mains.add('sid-main')
    agentsQuery.data = [agent('jim', 'Jim')]
    seed('sid-main', 'main', null, 'jim')
    await renderChatPage()
    expectLabelInHeaderAboveFeed('Jim - Main Session')
  })

  it('ignores a stored title on a main and derives from the live agent name (S2-05 ruling, chief-confirmed 2026-10-12)', async () => {
    seam.mains.add('sid-main')
    agentsQuery.data = [agent('jim', 'Jim')]
    // S2-05 (combined-review X-architect X-3; see ARCHITECT-RULINGS.md for
    // the combined-review ruling set; chief-confirmed 2026-10-12, team-lead
    // dispatch): for a MAIN the SPA ignores the stored session.title — it is
    // a copy taken at attach time, persisted in the browser — and ALWAYS
    // derives "<Agent> - Main Session" from the live agent list, so no
    // stored copy can survive an agent rename. The stored value here is
    // deliberately different from the derived one, keeping the oracle's
    // distinguishing power: the derived label must show and the stored one
    // must NOT. (Supersedes ff32fd420's server-title-wins oracle, S5-12/Q4.)
    // Extra chats, tasks and helpers keep their stored titles (covered below).
    seed('sid-main', 'main', 'Jim - Main Session (server-stamped)', 'jim')
    await renderChatPage()
    expectLabelInHeaderAboveFeed('Jim - Main Session')
    expect(screen.queryByText('Jim - Main Session (server-stamped)')).not.toBeInTheDocument()
  })

  it('follows an agent rename: the header re-derives from the CURRENT name without a reload', async () => {
    seam.mains.add('sid-main')
    agentsQuery.data = [agent('jim', 'Jim')]
    seed('sid-main', 'main', null, 'jim')
    const view = await renderChatPage()
    expectLabelInHeaderAboveFeed('Jim - Main Session')

    // The rename lands in the shared agents data (['agents'] cache) and the
    // header must re-derive on the next render — never a cached label.
    agentsQuery.data = [agent('jim', 'Jim Prime')]
    await act(async () => {
      view.rerender(<ChatPage />)
    })
    expectLabelInHeaderAboveFeed('Jim Prime - Main Session')
  })

  it('shows "Extra chat — Launch notes" in the header line above the feed for an extra (unchanged)', async () => {
    seed('sid-extra', 'chat', 'Launch notes')
    await renderChatPage()
    expectLabelInHeaderAboveFeed('Extra chat — Launch notes')
  })

  it('shows "Task run" in the header line above the feed for a task (unchanged)', async () => {
    seed('sid-task', 'task', 'Deploy')
    await renderChatPage()
    expectLabelInHeaderAboveFeed('Task run')
  })

  it('shows "Helper" in the header line above the feed for a subordinate session (unchanged)', async () => {
    seed('sid-helper', 'delegate', 'Scan')
    await renderChatPage()
    expectLabelInHeaderAboveFeed('Helper')
  })

  it('keeps the main’s title in the header line above the feed while history is replaying', async () => {
    seam.mains.add('sid-main')
    agentsQuery.data = [agent('jim', 'Jim')]
    seed('sid-main', 'main', null, 'jim')
    useChatStore.setState({ isReplaying: true })
    await renderChatPage()
    expectLabelInHeaderAboveFeed('Jim - Main Session')
  })
})

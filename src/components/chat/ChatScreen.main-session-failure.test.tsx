// Train 1 UAT Row 4: the dispatch, not the implementation, supplies these oracles.
// Real screen/composer, entry flow/decision, stores and queries. Only API network
// calls and the third-party AssistantUI/router environment are stand-ins.
import type { ReactNode } from 'react'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Workspace, WorkspaceMemberConfig } from '@/lib/api/generated/openapi-types'
import type { WsConnection } from '@/lib/ws'
import { queryClient } from '@/lib/queryClient'
import { useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { runWorkspaceEntry } from '@/store/session/workspaceEntryFlow'
import { useUiStore } from '@/store/ui'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { JIM, MIA, workspace } from '../../../tests/omnipus-l1-ui-red/fixtures'
import { ChatScreen } from './ChatScreen'

vi.mock('@assistant-ui/react', async () => (await import('@/test/assistantUiMock')).createAssistantUiMock())
vi.mock('@tanstack/react-router', () => ({
  useRouter: () => ({ navigate: vi.fn() }),
  useNavigate: () => vi.fn(),
  useSearch: () => ({}),
  Link: ({ children }: { children: ReactNode }) => children,
}))
vi.mock('@/lib/api', async (original) => ({
  ...await original<typeof import('@/lib/api')>(),
  fetchWorkspace: vi.fn(),
  fetchWorkspaces: vi.fn().mockResolvedValue([]),
  fetchSessions: vi.fn().mockResolvedValue([]),
  fetchAgents: vi.fn().mockResolvedValue([]),
  fetchSessionMessages: vi.fn().mockResolvedValue([]),
  fetchCommands: vi.fn().mockResolvedValue([]),
  fetchSkills: vi.fn().mockResolvedValue([]),
  fetchProviders: vi.fn().mockResolvedValue([]),
}))
import { fetchAgents, fetchSessions, fetchWorkspace } from '@/lib/api'

const WS = 'row4-fault-workspace'
const MAIN = 'opaque-server-recovered-main'
const READY = 'Your agent is ready. Start a conversation below.'
// Exact founder-specified text for the tester's ENOTDIR fault; never synthesized by the UI.
const FOLDER_ERROR = 'The main chat could not be prepared because session storage has a file where a folder is required. Ask the server operator to fix it, then retry.'
const sender = { send: vi.fn<WsConnection['send']>().mockReturnValue(true) }

function team(member: WorkspaceMemberConfig, id = WS): Workspace {
  return workspace(id, {
    core_team: ['mia', 'jim'],
    member_configs: {
      mia: member,
      jim: { main_session_id: 'opaque-healthy-sibling-main' },
    },
  })
}

beforeEach(() => {
  vi.stubGlobal('ResizeObserver', undefined)
  localStorage.clear()
  queryClient.clear()
  vi.mocked(fetchAgents).mockReset().mockResolvedValue([MIA, JIM])
  vi.mocked(fetchSessions).mockReset().mockResolvedValue([])
  vi.mocked(fetchWorkspace).mockReset().mockResolvedValue(team({ main_session_error: FOLDER_ERROR }))
  sender.send.mockClear()
  act(() => {
    useSessionStore.setState(useSessionStore.getInitialState(), true)
    useChatStore.setState(useChatStore.getInitialState(), true)
    useConnectionStore.setState(useConnectionStore.getInitialState(), true)
    useWorkspacesStore.setState(useWorkspacesStore.getInitialState(), true)
    useUiStore.setState({ toasts: [] })
    useWorkspacesStore.setState({ activeWorkspaceId: WS })
    // Reproduce the stale selection that made the failed main advertise readiness.
    useSessionStore.setState({ activeAgentId: 'mia' })
    useConnectionStore.getState().setConnection(sender as unknown as WsConnection)
    useConnectionStore.getState().setConnected(true)
  })
})

afterEach(() => {
  cleanup()
  queryClient.clear()
  vi.unstubAllGlobals()
})

function showChat() {
  return render(<QueryClientProvider client={queryClient}><ChatScreen /></QueryClientProvider>)
}

async function enter(id = WS) {
  await act(async () => { await runWorkspaceEntry(id) })
}

function finishReplay() {
  act(() => {
    useChatStore.getState().handleFrame({
      type: 'catch_up_complete', session_id: MAIN, seq: 0, boot_id: 'row4-boot', mode: 'snapshot',
    })
  })
}

describe('Train 1 Row 4 — honest failed member main', () => {
  it('shows the server storage cause and Retry, never readiness or a sibling main, and disables sending', async () => {
    await enter()
    showChat()

    expect(screen.queryByText(READY, { exact: true })).not.toBeInTheDocument()
    expect(screen.getByText(FOLDER_ERROR, { exact: true })).toBeVisible()
    expect(screen.getByRole('alert')).toHaveTextContent(FOLDER_ERROR)
    expect(screen.getByRole('button', { name: 'Retry' })).toBeEnabled()
    expect(screen.getByTestId('composer-input')).toBeDisabled()
    expect(screen.getByTestId('chat-send')).toBeDisabled()
    expect(useSessionStore.getState().activeSessionId).toBeNull()
    expect(sender.send.mock.calls).toEqual([])
    expect(vi.mocked(fetchWorkspace).mock.calls).toEqual([[WS]])
  })

  it('preserves a different server-provided cause verbatim instead of hard-coding the folder conflict', async () => {
    const message = 'The main chat could not be prepared because session storage is unavailable. Ask the server operator to check it, then retry.'
    vi.mocked(fetchWorkspace).mockResolvedValue(team({ main_session_error: message }))
    await enter()
    showChat()

    expect(screen.getByText(message, { exact: true })).toBeVisible()
    expect(screen.queryByText(FOLDER_ERROR, { exact: true })).not.toBeInTheDocument()
    expect(screen.getByTestId('composer-input')).toBeDisabled()
    expect(sender.send.mock.calls).toEqual([])
  })

  it('Retry refetches the failed workspace once and opens only its newly returned main', async () => {
    await enter()
    showChat()
    expect(screen.getByText(FOLDER_ERROR, { exact: true })).toBeVisible()
    let release!: (value: Workspace) => void
    vi.mocked(fetchWorkspace).mockReturnValue(new Promise((resolve) => { release = resolve }))

    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    try {
      expect(vi.mocked(fetchWorkspace).mock.calls).toEqual([[WS], [WS]])
      expect(screen.queryByText(READY, { exact: true })).not.toBeInTheDocument()
      expect(screen.getByTestId('composer-input')).toBeDisabled()
      expect(screen.getByTestId('chat-send')).toBeDisabled()
      expect(sender.send.mock.calls).toEqual([])
    } finally {
      await act(async () => { release(team({ main_session_id: MAIN })) })
    }
    await waitFor(() => expect(useSessionStore.getState().activeSessionId).toBe(MAIN))
    finishReplay()

    expect(vi.mocked(fetchWorkspace).mock.calls).toEqual([[WS], [WS]])
    expect(sender.send.mock.calls).toEqual([[{ type: 'attach_session', session_id: MAIN }]])
    expect(useSessionStore.getState().activeAgentId).toBe('mia')
    expect(screen.queryByText(FOLDER_ERROR, { exact: true })).not.toBeInTheDocument()
    expect(screen.queryByTestId('unavailable-chat-notice')).not.toBeInTheDocument()
    expect(screen.getByText(READY, { exact: true })).toBeVisible()
    expect(screen.getByTestId('composer-input')).toBeEnabled()
    expect(screen.getByTestId('chat-send')).toBeEnabled()
  })

  it('keeps the normal welcome unchanged for a healthy first main and ignores another member error', async () => {
    vi.mocked(fetchWorkspace).mockResolvedValue({
      ...team({ main_session_id: MAIN }),
      member_configs: { mia: { main_session_id: MAIN }, jim: { main_session_error: FOLDER_ERROR } },
    })
    await enter()
    finishReplay()
    showChat()

    expect(screen.getByText(READY, { exact: true })).toBeVisible()
    expect(screen.queryByText(FOLDER_ERROR, { exact: true })).not.toBeInTheDocument()
    expect(screen.queryByTestId('unavailable-chat-notice')).not.toBeInTheDocument()
    expect(screen.getByTestId('composer-input')).toBeEnabled()
    expect(screen.getByTestId('chat-send')).toBeEnabled()
    expect(sender.send.mock.calls).toEqual([[{ type: 'attach_session', session_id: MAIN }]])
  })

  it('retains the generic Retry notice without a server cause but does not call the unresolved main ready', async () => {
    vi.mocked(fetchWorkspace).mockResolvedValue(team({}))
    await enter()
    showChat()

    expect(screen.queryByText(READY, { exact: true })).not.toBeInTheDocument()
    expect(screen.getByText('This chat is unavailable right now', { exact: true })).toBeVisible()
    expect(screen.getByRole('button', { name: 'Retry' })).toBeEnabled()
    expect(screen.getByTestId('composer-input')).toBeDisabled()
    expect(screen.getByTestId('chat-send')).toBeDisabled()
    expect(sender.send.mock.calls).toEqual([])
  })

  it('does not show the ready welcome or enable the composer while the main is still being resolved', async () => {
    let release!: (value: Workspace) => void
    vi.mocked(fetchWorkspace).mockReturnValue(new Promise((resolve) => { release = resolve }))
    const pending = runWorkspaceEntry(WS)
    try {
      showChat()
      expect(screen.queryByText(READY, { exact: true })).not.toBeInTheDocument()
      expect(screen.getByTestId('composer-input')).toBeDisabled()
      expect(screen.getByTestId('chat-send')).toBeDisabled()
      expect(sender.send.mock.calls).toEqual([])
    } finally {
      await act(async () => {
        release(team({ main_session_id: MAIN }))
        await pending
      })
    }
  })

  it('does not advertise readiness after a failed restore with a selected agent', () => {
    act(() => useSessionStore.setState({
      workspaceEntry: {
        status: 'failed-attempt', committed: null,
        attempted: { sessionId: 'remembered-unavailable-chat', sendEnabled: false },
        acknowledged: false, retry: true, fellBack: false,
      },
    }))
    showChat()

    expect(screen.queryByText(READY, { exact: true })).not.toBeInTheDocument()
    expect(screen.getByText('Could not restore your last conversation. Retry to try again.', { exact: true })).toBeVisible()
    expect(screen.getByTestId('composer-input')).toBeDisabled()
    expect(screen.getByTestId('chat-send')).toBeDisabled()
  })

  it('does not carry the same member failure into a different healthy workspace', async () => {
    await enter()
    showChat()
    const healthyWorkspace = 'row4-other-workspace'
    vi.mocked(fetchWorkspace).mockResolvedValue(team({ main_session_id: MAIN }, healthyWorkspace))
    act(() => useWorkspacesStore.setState({ activeWorkspaceId: healthyWorkspace }))
    await enter(healthyWorkspace)
    finishReplay()

    expect(vi.mocked(fetchWorkspace).mock.calls).toEqual([[WS], [healthyWorkspace]])
    expect(screen.queryByText(FOLDER_ERROR, { exact: true })).not.toBeInTheDocument()
    expect(screen.getByText(READY, { exact: true })).toBeVisible()
    expect(screen.getByTestId('composer-input')).toBeEnabled()
    expect(sender.send.mock.calls).toEqual([[{ type: 'attach_session', session_id: MAIN }]])
  })
})

import { describe, it, expect, vi, beforeEach, beforeAll } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { act } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { SearchModal } from './SearchModal'
import { useUiStore } from '@/store/ui'
import { useSessionStore } from '@/store/session'
import type { Agent, Session, Workspace } from '@/lib/api'

// jsdom doesn't implement scrollIntoView; SearchModal calls it to keep the
// keyboard-highlighted row in view as the user arrows through results
// (project convention — see src/components/ui/model-selector.test.tsx).
beforeAll(() => {
  if (!Element.prototype.scrollIntoView) {
    Element.prototype.scrollIntoView = () => {}
  }
})

// selectSession is a plain callback returned by useSelectSession — the hook
// itself pulls in useNavigate/useSessionStore/useChatStore/useWorkspacesStore
// wiring that's irrelevant to SearchModal's own behaviour. Mock the hook so
// keyboard-selection tests can assert "the right session was handed off"
// without dragging in router/store plumbing that belongs to useSelectSession's
// own test suite.
const mockSelectSession = vi.fn()
vi.mock('@/components/chat/useSelectSession', () => ({
  useSelectSession: () => mockSelectSession,
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn(),
    fetchSessions: vi.fn(),
    fetchWorkspaces: vi.fn(),
    renameSession: vi.fn(),
    deleteSession: vi.fn(),
  }
})

import { fetchAgents, fetchSessions, fetchWorkspaces, renameSession, deleteSession } from '@/lib/api'

function makeSession(overrides: Partial<Session> = {}): Session {
  return {
    id: 's-1',
    agent_id: 'agent-1',
    active_agent_id: 'agent-1',
    title: 'Session One',
    type: 'chat',
    created_at: '2026-07-16T09:00:00Z',
    updated_at: '2026-07-16T11:00:00Z',
    message_count: 3,
    workspace_id: 'ws-1',
    ...overrides,
  }
}

function makeWorkspace(overrides: Partial<Workspace> = {}): Workspace {
  return {
    id: 'ws-1',
    name: 'Alpha Workspace',
    status: 'active',
    pinned: false,
    pin_order: 0,
    task_count: 0,
    created_at: '2025-01-01T00:00:00Z',
    updated_at: '2025-01-01T00:00:00Z',
    ...overrides,
  }
}

function makeAgent(overrides: Partial<Agent> = {}): Agent {
  return {
    id: 'agent-1',
    name: 'Mia',
    type: 'core',
    locked: false,
    status: 'active',
    model: 'anthropic/claude-3.5-haiku',
    description: 'Assistant',
    soul: '',
    timeout_seconds: 60,
    max_tool_iterations: 20,
    steering_mode: 'one-at-a-time' as const,
    ...overrides,
  }
}

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

function renderModal() {
  const client = makeClient()
  const utils = render(
    <QueryClientProvider client={client}>
      <SearchModal />
    </QueryClientProvider>,
  )
  return { ...utils, client }
}

beforeEach(() => {
  vi.mocked(fetchAgents).mockReset().mockResolvedValue([makeAgent()])
  vi.mocked(fetchWorkspaces).mockReset().mockResolvedValue([makeWorkspace()])
  vi.mocked(fetchSessions).mockReset().mockResolvedValue([makeSession()])
  vi.mocked(renameSession).mockReset().mockImplementation(async (id, title) => makeSession({ id, title }))
  vi.mocked(deleteSession).mockReset().mockResolvedValue({ success: true })
  mockSelectSession.mockClear()
  act(() => {
    useUiStore.setState({ searchModalOpen: true, searchModalWorkspaceFilter: null })
    useSessionStore.setState({
      activeSessionId: null,
      activeAgentId: null,
      activeAgentType: null,
      attachedSessionType: null,
      attachedTaskTitle: null,
    })
  })
})

describe('SearchModal — delete flow', () => {
  it('requires explicit confirmation before deleting; no delete on a bare trash click', async () => {
    const user = userEvent.setup()
    renderModal()

    await waitFor(() => expect(screen.getByText('Session One')).toBeInTheDocument())

    await user.click(screen.getByRole('button', { name: 'Delete Session One' }))
    expect(screen.getByText('Delete "Session One"?')).toBeInTheDocument()
    expect(deleteSession).not.toHaveBeenCalled()

    // Cancel backs out without deleting.
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(screen.queryByText('Delete "Session One"?')).not.toBeInTheDocument()
    expect(deleteSession).not.toHaveBeenCalled()

    // Confirm now performs the deletion.
    await user.click(screen.getByRole('button', { name: 'Delete Session One' }))
    await user.click(screen.getByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(deleteSession).toHaveBeenCalledWith('s-1'))
  })

  it('clears the active session when the deleted session is the active one', async () => {
    const user = userEvent.setup()
    act(() => {
      useSessionStore.setState({ activeSessionId: 's-1', activeAgentId: 'agent-1', activeAgentType: 'core' })
    })
    renderModal()

    await waitFor(() => expect(screen.getByText('Session One')).toBeInTheDocument())

    await user.click(screen.getByRole('button', { name: 'Delete Session One' }))
    await user.click(screen.getByRole('button', { name: 'Delete' }))

    await waitFor(() => expect(deleteSession).toHaveBeenCalledWith('s-1'))
    await waitFor(() => expect(useSessionStore.getState().activeSessionId).toBeNull())
  })

  it('does NOT clear the active session when a different session is deleted', async () => {
    const user = userEvent.setup()
    vi.mocked(fetchSessions).mockResolvedValue([
      makeSession({ id: 's-1', title: 'Session One', updated_at: '2026-07-16T11:00:00Z' }),
      makeSession({ id: 's-2', title: 'Session Two', updated_at: '2026-07-16T10:00:00Z' }),
    ])
    act(() => {
      useSessionStore.setState({ activeSessionId: 's-2', activeAgentId: 'agent-1', activeAgentType: 'core' })
    })
    renderModal()

    await waitFor(() => expect(screen.getByText('Session One')).toBeInTheDocument())

    await user.click(screen.getByRole('button', { name: 'Delete Session One' }))
    await user.click(screen.getByRole('button', { name: 'Delete' }))

    await waitFor(() => expect(deleteSession).toHaveBeenCalledWith('s-1'))
    expect(useSessionStore.getState().activeSessionId).toBe('s-2')
  })

  it('disables the delete control for a protected (heartbeat) session and never opens the confirm step', async () => {
    const user = userEvent.setup()
    vi.mocked(fetchSessions).mockResolvedValue([
      makeSession({ id: 's-1', title: 'Session One', protected: true }),
    ])
    renderModal()

    await waitFor(() => expect(screen.getByText('Session One')).toBeInTheDocument())

    const deleteBtn = screen.getByRole('button', { name: 'Delete Session One' })
    expect(deleteBtn).toBeDisabled()
    expect(deleteBtn).toHaveAttribute('title', 'Protected (heartbeat)')

    // A disabled button ignores clicks — the destructive confirm step must
    // never appear, and no delete request must be made.
    await user.click(deleteBtn)
    expect(screen.queryByText('Delete "Session One"?')).not.toBeInTheDocument()
    expect(deleteSession).not.toHaveBeenCalled()
  })
})

describe('SearchModal — rename flow', () => {
  it('commits a rename on Enter', async () => {
    const user = userEvent.setup()
    renderModal()

    await waitFor(() => expect(screen.getByText('Session One')).toBeInTheDocument())

    await user.click(screen.getByRole('button', { name: 'Rename Session One' }))
    const input = screen.getByDisplayValue('Session One')
    await user.clear(input)
    await user.type(input, 'Renamed Session{Enter}')

    await waitFor(() => expect(renameSession).toHaveBeenCalledWith('s-1', 'Renamed Session'))
  })

  it('cancels on Escape without renaming', async () => {
    const user = userEvent.setup()
    renderModal()

    await waitFor(() => expect(screen.getByText('Session One')).toBeInTheDocument())

    await user.click(screen.getByRole('button', { name: 'Rename Session One' }))
    const input = screen.getByDisplayValue('Session One')
    await user.clear(input)
    await user.type(input, 'Should not stick{Escape}')

    expect(renameSession).not.toHaveBeenCalled()
    await waitFor(() => expect(screen.queryByDisplayValue('Should not stick')).not.toBeInTheDocument())
    expect(screen.getByText('Session One')).toBeInTheDocument()
  })

  it('restores focus to the row rename button after Escape-cancel', async () => {
    const user = userEvent.setup()
    renderModal()

    await waitFor(() => expect(screen.getByText('Session One')).toBeInTheDocument())

    const renameBtn = screen.getByRole('button', { name: 'Rename Session One' })
    await user.click(renameBtn)
    const input = screen.getByDisplayValue('Session One')
    await user.type(input, '{Escape}')

    await waitFor(() => expect(screen.getByRole('button', { name: 'Rename Session One' })).toHaveFocus())
  })

  it('restores focus to the row rename button after a successful commit', async () => {
    const user = userEvent.setup()
    renderModal()

    await waitFor(() => expect(screen.getByText('Session One')).toBeInTheDocument())

    await user.click(screen.getByRole('button', { name: 'Rename Session One' }))
    const input = screen.getByDisplayValue('Session One')
    await user.type(input, ' 2{Enter}')

    // renameSession is mocked and doesn't feed back into the fetchSessions
    // mock, so the row's displayed title (prop-driven) reverts to "Session
    // One" once the post-mutation invalidateQueries refetch resolves — the
    // rename button's accessible name reverts with it. What this test
    // verifies is that focus lands back on THE ROW'S rename button (not
    // dropped to <body>) the moment the inline editor unmounts on commit.
    await waitFor(() => expect(renameSession).toHaveBeenCalledWith('s-1', 'Session One 2'))
    await waitFor(() => expect(screen.getByRole('button', { name: 'Rename Session One' })).toHaveFocus())
  })
})

describe('SearchModal — editing-state contract (Escape-to-close suppression)', () => {
  it('Escape closes the modal when no row is being renamed', async () => {
    const user = userEvent.setup()
    renderModal()

    await waitFor(() => expect(screen.getByText('Session One')).toBeInTheDocument())

    await user.keyboard('{Escape}')

    await waitFor(() => expect(useUiStore.getState().searchModalOpen).toBe(false))
  })

  it('unmounting a mid-rename row (group collapse) does not wedge Escape-to-close (wedge regression)', async () => {
    const user = userEvent.setup()
    renderModal()

    await waitFor(() => expect(screen.getByText('Session One')).toBeInTheDocument())

    await user.click(screen.getByRole('button', { name: 'Rename Session One' }))
    expect(screen.getByDisplayValue('Session One')).toBeInTheDocument()

    // Collapse the workspace group the renaming row lives in — SessionRow
    // (still mid-rename) unmounts without ever calling setEditing(false).
    await user.click(screen.getByRole('button', { name: 'Alpha Workspace' }))
    await waitFor(() => expect(screen.queryByDisplayValue('Session One')).not.toBeInTheDocument())

    // Pre-fix, the ref never gets reset to `false` on unmount, so Escape
    // stays permanently suppressed for the rest of the modal's life.
    await user.keyboard('{Escape}')
    await waitFor(() => expect(useUiStore.getState().searchModalOpen).toBe(false))
  })

  it('a parent re-render mid-rename does not silently re-enable Escape-to-close (re-render regression)', async () => {
    const user = userEvent.setup()
    vi.mocked(fetchSessions).mockResolvedValue([
      makeSession({ id: 's-1', title: 'Session One', updated_at: '2026-07-16T11:00:00Z', workspace_id: 'ws-1' }),
      makeSession({ id: 's-2', title: 'Session Two', updated_at: '2026-07-16T10:00:00Z', workspace_id: 'ws-1' }),
    ])
    renderModal()

    await waitFor(() => expect(screen.getByText('Session One')).toBeInTheDocument())
    await waitFor(() => expect(screen.getByText('Session Two')).toBeInTheDocument())

    await user.click(screen.getByRole('button', { name: 'Rename Session One' }))
    const input = screen.getByDisplayValue('Session One')
    await user.type(input, ' extra')

    // Force a SearchModal re-render for a reason unrelated to either row's
    // own editing state — e.g. a background refetch of `sessions`,
    // `workspaces`, or `agents` re-renders the whole tree the same way.
    // Setting the workspace filter to the workspace both sessions already
    // belong to changes zero visible results (nothing is filtered out) but
    // does change the `wsFilter` value SearchModal selects from the store,
    // forcing a genuine re-render of SearchModal and — since SessionRow is
    // not memoized — fresh `onEditingChange` props on every row, all without
    // moving focus off the rename input (a real click would).
    // Note: `sessions` (and thus `groups`/`flatSessions`) are TanStack Query
    // data, which uses structural sharing — resolving a fresh-but-equal
    // array back to the SAME reference and skipping the re-render entirely,
    // so mutating query data directly does not reliably reproduce this bug.
    act(() => {
      useUiStore.setState({ searchModalWorkspaceFilter: 'ws-1' })
    })

    await user.keyboard('{Escape}')

    // Escape must cancel the in-progress rename, not close the whole modal
    // and discard it.
    await waitFor(() => expect(screen.queryByDisplayValue('Session One extra')).not.toBeInTheDocument())
    expect(useUiStore.getState().searchModalOpen).toBe(true)
    expect(screen.getByText('Session One')).toBeInTheDocument()
    expect(screen.getByText('Session Two')).toBeInTheDocument()
    expect(renameSession).not.toHaveBeenCalled()
  })
})

describe('SearchModal — keyboard selection', () => {
  it('moves the highlight with ArrowDown/ArrowUp and Enter attaches the highlighted session', async () => {
    const user = userEvent.setup()
    vi.mocked(fetchSessions).mockResolvedValue([
      makeSession({ id: 's-1', title: 'Session One', updated_at: '2026-07-16T11:00:00Z' }),
      makeSession({ id: 's-2', title: 'Session Two', updated_at: '2026-07-16T10:00:00Z' }),
    ])
    renderModal()

    await waitFor(() => expect(screen.getByText('Session One')).toBeInTheDocument())
    await waitFor(() => expect(screen.getByText('Session Two')).toBeInTheDocument())

    // getByLabelText matches both the search <input aria-label="Search
    // sessions"> and the dialog's role="dialog" container (Radix labels the
    // content via aria-labelledby pointing at the same-text DialogTitle) —
    // scope to the textbox role to disambiguate.
    const input = screen.getByRole('textbox', { name: 'Search sessions' })
    await user.click(input)

    // Default highlight is index 0 (Session One, most recently active).
    // ArrowDown moves to Session Two.
    await user.keyboard('{ArrowDown}')
    await user.keyboard('{Enter}')
    await waitFor(() => expect(mockSelectSession).toHaveBeenCalledTimes(1))
    expect(mockSelectSession).toHaveBeenCalledWith(expect.objectContaining({ id: 's-2' }))

    mockSelectSession.mockClear()
    // ArrowUp moves the highlight back to Session One.
    await user.keyboard('{ArrowUp}')
    await user.keyboard('{Enter}')
    await waitFor(() => expect(mockSelectSession).toHaveBeenCalledTimes(1))
    expect(mockSelectSession).toHaveBeenCalledWith(expect.objectContaining({ id: 's-1' }))
  })
})

describe('SearchModal — focus-reveal affordance', () => {
  it('rename/delete buttons carry the group-focus-within + focus-visible reveal classes', async () => {
    renderModal()
    await waitFor(() => expect(screen.getByText('Session One')).toBeInTheDocument())

    const renameBtn = screen.getByRole('button', { name: 'Rename Session One' })
    const deleteBtn = screen.getByRole('button', { name: 'Delete Session One' })
    for (const btn of [renameBtn, deleteBtn]) {
      expect(btn.className).toContain('group-focus-within:opacity-100')
      expect(btn.className).toContain('focus-visible:opacity-100')
    }
  })
})

describe('SearchModal — error states', () => {
  it('shows the error state (not "Unfiled" grouping) when the workspaces fetch fails', async () => {
    vi.mocked(fetchWorkspaces).mockReset().mockRejectedValue(new Error('workspaces down'))
    renderModal()

    await waitFor(() =>
      expect(screen.getByText('Could not load sessions — try again')).toBeInTheDocument(),
    )
    expect(screen.queryByText('Unfiled')).not.toBeInTheDocument()
    // The known-bad denominator must not be used to render a (misleading) list.
    expect(screen.queryByText('Session One')).not.toBeInTheDocument()
  })

  it('uses a neutral "Unknown" label (never "[removed]") when the agents fetch fails', async () => {
    vi.mocked(fetchAgents).mockReset().mockRejectedValue(new Error('agents down'))
    vi.mocked(fetchSessions).mockResolvedValue([
      makeSession({ id: 's-x', agent_id: 'agent-x', active_agent_id: 'agent-x', title: 'Session X', updated_at: '2026-07-16T11:00:00Z' }),
      makeSession({ id: 's-y', agent_id: 'agent-y', active_agent_id: 'agent-y', title: 'Session Y', updated_at: '2026-07-16T10:00:00Z' }),
    ])
    renderModal()

    await waitFor(() => expect(screen.getByText('Session X')).toBeInTheDocument())
    expect(screen.queryByText('[removed]')).not.toBeInTheDocument()
    expect(screen.getAllByText('Unknown').length).toBeGreaterThan(0)
  })
})

import { describe, it, expect, vi, beforeEach, beforeAll } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { act } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { SearchModal } from './SearchModal'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { useUiStore } from '@/store/ui'
import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'
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

// Session-search enhancement: SearchModal now also calls useNavigate()
// directly (the workspace-switch arrow), separately from useSelectSession's
// own router usage above. Mirrors useSelectSession.test.tsx's own mock
// pattern (importOriginal + override just useNavigate) rather than
// Sidebar.test.tsx's full hand-rolled replacement — this file doesn't render
// <Link>, so there's nothing else to stub.
const mockNavigate = vi.fn()
vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    useNavigate: () => mockNavigate,
  }
})

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
    needs_model: false,
    status: 'active',
    model: 'anthropic/claude-3.5-haiku',
    description: 'Assistant',
    soul: '',
    timeout_seconds: 60,
    max_tool_iterations: 20,
    // ADR-052 FR-039: memory_enabled is required on the wire Agent type.
    memory_enabled: true,
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

// Captured once, before any test can override it (see the workspace-switch
// "does not collapse" test below, which temporarily swaps closeSearchModal
// for a spy) — restored in beforeEach so that override never leaks into a
// later test.
const realCloseSearchModal = useUiStore.getState().closeSearchModal

beforeEach(() => {
  vi.mocked(fetchAgents).mockReset().mockResolvedValue([makeAgent()])
  vi.mocked(fetchWorkspaces).mockReset().mockResolvedValue([makeWorkspace()])
  vi.mocked(fetchSessions).mockReset().mockResolvedValue([makeSession()])
  vi.mocked(renameSession).mockReset().mockImplementation(async (id, title) => makeSession({ id, title }))
  vi.mocked(deleteSession).mockReset().mockResolvedValue({ success: true })
  mockSelectSession.mockClear()
  mockNavigate.mockClear()
  act(() => {
    // searchModalMode explicitly reset to 'sessions' here — the two-modes
    // describe block below flips it to 'workspaces' for its own tests, and
    // without this reset that would leak into whichever test runs next.
    useUiStore.setState({ searchModalOpen: true, searchModalWorkspaceFilter: null, searchModalMode: 'sessions', closeSearchModal: realCloseSearchModal })
    useSessionStore.setState({
      activeSessionId: null,
      activeAgentId: null,
      activeAgentType: null,
      attachedSessionType: null,
      attachedTaskTitle: null,
    })
    useWorkspacesStore.setState({ activeWorkspaceId: null })
  })
})

// ADR-052 FR-036: verifier-role sessions must stay hidden from the search
// modal's session list by default. GET /sessions defaults to excluding
// type "verifier" unless include_verifier=true is passed.
//
// ADR-057 W22b [grill2 M2-9, spec "SPA tests that MUST be deliberately
// inverted, never deleted"]: this test's OLD assertion was
// `toHaveBeenCalledWith()` — literally "no arguments at all". That shape is
// gone the moment FR-094/FR-104 land: SearchModal now fetches `flat: true`
// (US-19) so a text search can find a match inside a delegated CHILD
// session too, not just a root — the default roots-only page would
// silently exclude every subordinate from the search space. DELIBERATELY
// INVERTED (not deleted) to assert the new call shape, while the ORIGINAL
// property this test protected — `include_verifier` is never requested by
// this surface — is restated as its own explicit assertion so that
// regression stays covered under the new call shape too.
describe('SearchModal — ADR-052 FR-036 / ADR-057 US-19/FR-094/FR-104: session query call shape', () => {
  it('calls fetchSessions with flat:true (search must see delegated children) and never requests include_verifier', async () => {
    renderModal()
    await waitFor(() => {
      expect(vi.mocked(fetchSessions)).toHaveBeenCalledWith(undefined, undefined, { flat: true })
    })
    // Positive lower bound (Rule 4): the mock really was invoked, not merely
    // never called — and the FR-036 property survives under the new shape:
    // no call ever asks for include_verifier.
    const calls = vi.mocked(fetchSessions).mock.calls
    expect(calls.length).toBeGreaterThan(0)
    for (const call of calls) {
      const opts = call[2] as { includeVerifier?: boolean } | undefined
      expect(opts?.includeVerifier).not.toBe(true)
    }
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

// Session-search enhancement (user-approved): agent sub-headers now always
// render, even for a single-agent workspace group — previously gated behind
// `group.agentGroups.length > 1` (SearchModal.tsx).
describe('SearchModal — agent header visibility (always shown)', () => {
  it('shows the agent sub-header even for a single-agent workspace group', async () => {
    // Default fixture: one workspace ("Alpha Workspace"), one agent ("Mia"),
    // one session — exactly the case the old `length > 1` gate used to hide.
    renderModal()
    await waitFor(() => expect(screen.getByText('Session One')).toBeInTheDocument())

    expect(screen.getByRole('button', { name: /Mia/ })).toBeInTheDocument()
  })
})

// Session-search enhancement (user-approved): a workspace-switch arrow sits
// at the right end of each real WorkspaceHeader row.
describe('SearchModal — workspace-switch arrow', () => {
  it('renders on a real workspace group but not on the Unfiled pseudo-group', async () => {
    vi.mocked(fetchSessions).mockResolvedValue([
      makeSession({ id: 's-1', title: 'Session One', workspace_id: 'ws-1' }),
      makeSession({ id: 's-2', title: 'Session Two', workspace_id: undefined }),
    ])
    renderModal()

    await waitFor(() => expect(screen.getByText('Session One')).toBeInTheDocument())
    await waitFor(() => expect(screen.getByText('Session Two')).toBeInTheDocument())
    expect(screen.getByRole('button', { name: 'Unfiled' })).toBeInTheDocument()

    const arrows = screen.getAllByTestId('workspace-switch-arrow')
    expect(arrows).toHaveLength(1)
    expect(arrows[0]).toHaveAttribute('aria-label', 'Switch to workspace Alpha Workspace')
    expect(screen.queryByLabelText('Switch to workspace Unfiled')).not.toBeInTheDocument()
  })

  it('clicking it switches the active workspace, starts a fresh session, navigates, and closes the modal', async () => {
    const user = userEvent.setup()
    act(() => {
      useSessionStore.setState({ activeSessionId: 's-1', activeAgentId: 'agent-1', activeAgentType: 'core' })
    })
    renderModal()
    await waitFor(() => expect(screen.getByText('Session One')).toBeInTheDocument())

    await user.click(screen.getByTestId('workspace-switch-arrow'))

    // setActiveWorkspaceId
    expect(useWorkspacesStore.getState().activeWorkspaceId).toBe('ws-1')
    // startNewSession — clears the previously-active session (exactly what
    // Sidebar's "New chat" workspace-row action does; session.ts's
    // startNewSession implementation).
    expect(useSessionStore.getState().activeSessionId).toBeNull()
    expect(useSessionStore.getState().attachedSessionType).toBeNull()
    // navigate
    expect(mockNavigate).toHaveBeenCalledWith({ to: '/workspaces/$workspaceId/chat', params: { workspaceId: 'ws-1' } })
    // closes the modal
    await waitFor(() => expect(useUiStore.getState().searchModalOpen).toBe(false))
    // Order pin (load-bearing): setActiveWorkspaceId must run BEFORE
    // startNewSession — startNewSession clears sessionByWorkspace for the
    // CURRENT workspace, so only the correct order leaves the TARGET
    // workspace's slot explicitly null (fresh composer, no silent
    // re-attach). If the two calls were swapped, this key would be
    // undefined, not null.
    expect(useSessionStore.getState().sessionByWorkspace['ws-1']).toBeNull()
  })

  it('is a no-op switch on the ALREADY-active workspace — navigates and closes but does not detach the live session', async () => {
    const user = userEvent.setup()
    act(() => {
      useWorkspacesStore.setState({ activeWorkspaceId: 'ws-1' })
      useSessionStore.setState({ activeSessionId: 's-1', activeAgentId: 'agent-1', activeAgentType: 'core' })
    })
    renderModal()
    await waitFor(() => expect(screen.getByText('Session One')).toBeInTheDocument())

    await user.click(screen.getByTestId('workspace-switch-arrow'))

    // The arrow says "Switch", not "New chat": the live session survives.
    expect(useSessionStore.getState().activeSessionId).toBe('s-1')
    expect(mockNavigate).toHaveBeenCalledWith({ to: '/workspaces/$workspaceId/chat', params: { workspaceId: 'ws-1' } })
    await waitFor(() => expect(useUiStore.getState().searchModalOpen).toBe(false))
  })

  it('does NOT collapse the group — it is a sibling control, not the toggle', async () => {
    const user = userEvent.setup()
    // Swap closeSearchModal for a spy that does not actually flip
    // searchModalOpen, so the panel stays mounted after the click and the
    // group's expand/collapse state remains observable (clicking the arrow
    // closes the modal per spec, which would otherwise unmount everything
    // and make "still expanded" unobservable). Restored in beforeEach.
    const closeSpy = vi.fn()
    act(() => { useUiStore.setState({ closeSearchModal: closeSpy }) })
    renderModal()
    await waitFor(() => expect(screen.getByText('Session One')).toBeInTheDocument())

    const toggle = screen.getByRole('button', { name: 'Alpha Workspace' })
    expect(toggle).toHaveAttribute('aria-expanded', 'true')

    await user.click(screen.getByTestId('workspace-switch-arrow'))

    expect(closeSpy).toHaveBeenCalledTimes(1)
    // The group is still expanded and its session row still rendered. What
    // this guards: the arrow must stay a SIBLING of the collapse-toggle
    // button — nesting it inside the toggle would fire onToggle on every
    // switch click (and be invalid HTML).
    expect(toggle).toHaveAttribute('aria-expanded', 'true')
    expect(screen.getByText('Session One')).toBeInTheDocument()
  })

  it('is keyboard reachable (a real <button>) and Enter activates it', async () => {
    const user = userEvent.setup()
    renderModal()
    await waitFor(() => expect(screen.getByText('Session One')).toBeInTheDocument())

    const arrow = screen.getByTestId('workspace-switch-arrow')
    expect(arrow.tagName).toBe('BUTTON')
    arrow.focus()
    expect(arrow).toHaveFocus()

    await user.keyboard('{Enter}')

    expect(mockNavigate).toHaveBeenCalledWith({ to: '/workspaces/$workspaceId/chat', params: { workspaceId: 'ws-1' } })
  })
})

// Session-search enhancement (user-approved): the search panel opts out of
// the shared dialog.tsx dim (DialogContent's overlayClassName) so the rest
// of the screen stays 100% visible while search is open.
describe('SearchModal — zero backdrop (no dim overlay)', () => {
  it('renders a transparent overlay — no dim behind the panel', async () => {
    renderModal()
    await waitFor(() => expect(screen.getByText('Session One')).toBeInTheDocument())

    const overlay = screen.getByTestId('dialog-overlay')
    expect(overlay.className).not.toContain('bg-[var(--color-primary)]/80')
    expect(overlay.className).toContain('bg-transparent')
  })

  it('every OTHER dialog keeps the default 80% dim (overlayClassName plumbing must not leak)', () => {
    // Renders the shared DialogContent directly with no overlayClassName —
    // the other half of the contract: a regression that defaulted all
    // overlays to transparent would ship silently without this.
    render(
      <Dialog open>
        <DialogContent aria-describedby={undefined}>
          <DialogTitle>plain dialog</DialogTitle>
        </DialogContent>
      </Dialog>,
    )
    const overlay = screen.getByTestId('dialog-overlay')
    expect(overlay.className).toContain('bg-[var(--color-primary)]/80')
    expect(overlay.className).not.toContain('bg-transparent')
  })
})

// Session-search TWO MODES (user-required behavior fix): /workspace opens
// the SAME SearchModal instance in workspace-switch mode — ALL workspaces
// listed (including zero-session ones), session groups collapsed by
// default, ArrowUp/Down walks workspace HEADERS (not sessions), Enter
// switches to the highlighted workspace, typing filters by workspace NAME,
// and the Unfiled pseudo-group is excluded (it isn't switchable). Every
// describe block above this one exercises the DEFAULT 'sessions' mode and
// is left untouched — this block only covers what's different in
// 'workspaces' mode.
describe('SearchModal — workspaces mode (workspace switcher)', () => {
  beforeEach(() => {
    act(() => { useUiStore.setState({ searchModalMode: 'workspaces' }) })
  })

  it('lists ALL workspaces including one with zero sessions — each real workspace gets a switch arrow', async () => {
    vi.mocked(fetchWorkspaces).mockResolvedValue([
      makeWorkspace({ id: 'ws-1', name: 'Alpha Workspace' }),
      makeWorkspace({ id: 'ws-2', name: 'Beta Workspace' }),
    ])
    vi.mocked(fetchSessions).mockResolvedValue([
      makeSession({ id: 's-1', title: 'Session One', workspace_id: 'ws-1' }),
    ])
    renderModal()

    await waitFor(() => expect(screen.getByRole('button', { name: 'Alpha Workspace' })).toBeInTheDocument())
    // Beta has zero sessions but must still be listed — sourced from the
    // full fetchWorkspaces result, not derived from session groups.
    expect(screen.getByRole('button', { name: 'Beta Workspace' })).toBeInTheDocument()

    const arrows = screen.getAllByTestId('workspace-switch-arrow')
    expect(arrows).toHaveLength(2)
    expect(screen.getByLabelText('Switch to workspace Beta Workspace')).toBeInTheDocument()
  })

  it('the zero-session workspace is Enter-switchable via ArrowDown + Enter', async () => {
    const user = userEvent.setup()
    vi.mocked(fetchWorkspaces).mockResolvedValue([
      makeWorkspace({ id: 'ws-1', name: 'Alpha Workspace' }),
      makeWorkspace({ id: 'ws-2', name: 'Beta Workspace' }),
    ])
    vi.mocked(fetchSessions).mockResolvedValue([
      makeSession({ id: 's-1', title: 'Session One', workspace_id: 'ws-1' }),
    ])
    renderModal()
    await waitFor(() => expect(screen.getByRole('button', { name: 'Beta Workspace' })).toBeInTheDocument())

    const input = screen.getByRole('textbox', { name: 'Filter workspaces' })
    await user.click(input)
    // Default highlight is index 0 (Alpha, alphabetically first) — ArrowDown
    // moves to Beta (index 1, the zero-session workspace).
    await user.keyboard('{ArrowDown}')
    await user.keyboard('{Enter}')

    expect(useWorkspacesStore.getState().activeWorkspaceId).toBe('ws-2')
    expect(mockNavigate).toHaveBeenCalledWith({ to: '/workspaces/$workspaceId/chat', params: { workspaceId: 'ws-2' } })
    await waitFor(() => expect(useUiStore.getState().searchModalOpen).toBe(false))
  })

  it('the zero-session workspace\'s switch arrow is clickable directly (not gated on having sessions)', async () => {
    const user = userEvent.setup()
    vi.mocked(fetchWorkspaces).mockResolvedValue([makeWorkspace({ id: 'ws-2', name: 'Beta Workspace' })])
    vi.mocked(fetchSessions).mockResolvedValue([])
    renderModal()
    await waitFor(() => expect(screen.getByRole('button', { name: 'Beta Workspace' })).toBeInTheDocument())

    await user.click(screen.getByTestId('workspace-switch-arrow'))

    expect(useWorkspacesStore.getState().activeWorkspaceId).toBe('ws-2')
    await waitFor(() => expect(useUiStore.getState().searchModalOpen).toBe(false))
  })

  it('groups start COLLAPSED by default (unlike sessions mode, which starts expanded)', async () => {
    vi.mocked(fetchWorkspaces).mockResolvedValue([makeWorkspace({ id: 'ws-1', name: 'Alpha Workspace' })])
    vi.mocked(fetchSessions).mockResolvedValue([makeSession({ id: 's-1', title: 'Session One', workspace_id: 'ws-1' })])
    renderModal()

    await waitFor(() => expect(screen.getByRole('button', { name: 'Alpha Workspace' })).toBeInTheDocument())
    expect(screen.getByRole('button', { name: 'Alpha Workspace' })).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryByText('Session One')).not.toBeInTheDocument()
  })

  it('manually expanding a collapsed group still reveals its sessions (peek)', async () => {
    const user = userEvent.setup()
    vi.mocked(fetchWorkspaces).mockResolvedValue([makeWorkspace({ id: 'ws-1', name: 'Alpha Workspace' })])
    vi.mocked(fetchSessions).mockResolvedValue([makeSession({ id: 's-1', title: 'Session One', workspace_id: 'ws-1' })])
    renderModal()

    await waitFor(() => expect(screen.getByRole('button', { name: 'Alpha Workspace' })).toBeInTheDocument())
    await user.click(screen.getByRole('button', { name: 'Alpha Workspace' }))

    expect(screen.getByRole('button', { name: 'Alpha Workspace' })).toHaveAttribute('aria-expanded', 'true')
    await waitFor(() => expect(screen.getByText('Session One')).toBeInTheDocument())
  })

  it('ArrowDown/ArrowUp walk WORKSPACE HEADERS (not sessions) — the highlight moves between headers', async () => {
    const user = userEvent.setup()
    vi.mocked(fetchWorkspaces).mockResolvedValue([
      makeWorkspace({ id: 'ws-1', name: 'Alpha Workspace' }),
      makeWorkspace({ id: 'ws-2', name: 'Beta Workspace' }),
    ])
    vi.mocked(fetchSessions).mockResolvedValue([
      makeSession({ id: 's-1', title: 'Session One', workspace_id: 'ws-1' }),
    ])
    renderModal()
    await waitFor(() => expect(screen.getByRole('button', { name: 'Beta Workspace' })).toBeInTheDocument())

    const alphaHeader = screen.getByRole('button', { name: 'Alpha Workspace' })
    const betaHeader = screen.getByRole('button', { name: 'Beta Workspace' })

    // Default highlight is index 0 (Alpha) — the "↵" hint sits inside its
    // own header row, not any session row (sessions mode's SessionRow
    // highlight is inert here — mode==='sessions' is false).
    expect(within(alphaHeader).queryByText('↵')).toBeInTheDocument()
    expect(within(betaHeader).queryByText('↵')).not.toBeInTheDocument()

    const input = screen.getByRole('textbox', { name: 'Filter workspaces' })
    await user.click(input)
    await user.keyboard('{ArrowDown}')

    await waitFor(() => expect(within(betaHeader).queryByText('↵')).toBeInTheDocument())
    expect(within(alphaHeader).queryByText('↵')).not.toBeInTheDocument()

    await user.keyboard('{ArrowUp}')

    await waitFor(() => expect(within(alphaHeader).queryByText('↵')).toBeInTheDocument())
    expect(within(betaHeader).queryByText('↵')).not.toBeInTheDocument()
  })

  it('typing filters WORKSPACES by name (not by session title)', async () => {
    const user = userEvent.setup()
    vi.mocked(fetchWorkspaces).mockResolvedValue([
      makeWorkspace({ id: 'ws-1', name: 'Alpha Workspace' }),
      makeWorkspace({ id: 'ws-2', name: 'Beta Workspace' }),
    ])
    vi.mocked(fetchSessions).mockResolvedValue([])
    renderModal()
    await waitFor(() => expect(screen.getByRole('button', { name: 'Beta Workspace' })).toBeInTheDocument())

    const input = screen.getByRole('textbox', { name: 'Filter workspaces' })
    await user.type(input, 'bet')

    await waitFor(() => expect(screen.queryByRole('button', { name: 'Alpha Workspace' })).not.toBeInTheDocument())
    expect(screen.getByRole('button', { name: 'Beta Workspace' })).toBeInTheDocument()
  })

  it('excludes the Unfiled pseudo-group — it is not a real workspace and cannot be switched to', async () => {
    vi.mocked(fetchWorkspaces).mockResolvedValue([makeWorkspace({ id: 'ws-1', name: 'Alpha Workspace' })])
    vi.mocked(fetchSessions).mockResolvedValue([
      makeSession({ id: 's-1', title: 'Session One', workspace_id: 'ws-1' }),
      makeSession({ id: 's-2', title: 'Session Two', workspace_id: undefined }),
    ])
    renderModal()

    await waitFor(() => expect(screen.getByRole('button', { name: 'Alpha Workspace' })).toBeInTheDocument())
    expect(screen.queryByRole('button', { name: 'Unfiled' })).not.toBeInTheDocument()
  })

  it('adapts the title and input placeholder for workspace-switch mode', async () => {
    vi.mocked(fetchWorkspaces).mockResolvedValue([makeWorkspace({ id: 'ws-1', name: 'Alpha Workspace' })])
    renderModal()

    await waitFor(() => expect(screen.getByText('Switch workspace')).toBeInTheDocument())
    expect(screen.getByPlaceholderText('Filter workspaces by name...')).toBeInTheDocument()
    expect(screen.queryByText('Search sessions')).not.toBeInTheDocument()
  })
})

// ADR-057 US-19/FR-094/BDD-105 (operator decision 1 — nested under parent,
// not the `verifier` hidden-with-a-flag precedent; test #102
// TestSearchModalTree_NestedAndVirtualized): a search hit inside a
// delegated CHILD session must surface its parent for context, and a large
// fan-out must not mount every row.
describe('SearchModal — ADR-057 US-19/FR-094/BDD-105: nests a child-only match under its parent and virtualizes a large fan-out', () => {
  function makeChild(i: number, parentId: string, title = `Delegated task ${i}`): Session {
    return {
      id: `child-${i}`,
      agent_id: 'agent-1',
      active_agent_id: 'agent-1',
      title,
      type: 'delegate',
      created_at: '2026-07-16T09:00:00Z',
      updated_at: '2026-07-16T09:00:00Z',
      message_count: 1,
      workspace_id: 'ws-1',
      parent_session_id: parentId,
    }
  }

  it('a child-only match reveals its non-matching parent for context, nested underneath it', async () => {
    // Below VIRTUALIZE_ROW_THRESHOLD — takes the PLAIN render path, which
    // every other test in this file already relies on. Isolating the
    // NESTING assertion from the virtualization one (below) keeps this test
    // deterministic: it verifies BDD-105's core claim without depending on
    // @tanstack/virtual-core's container-size measurement, which reads
    // `element.offsetWidth`/`offsetHeight` — properties jsdom (no layout
    // engine) cannot make behave like a real browser regardless of mocking.
    const parent = makeSession({ id: 'parent-1', title: 'Parent Chat' })
    const children = Array.from({ length: 5 }, (_, i) =>
      i === 2 ? makeChild(i, parent.id, 'Special Delegated Match') : makeChild(i, parent.id),
    )
    // Positive lower bound (Rule 4): the fixture genuinely carries several
    // children with exactly one match before asserting anything about
    // nesting — a test that passed on an empty or all-matching fixture
    // would prove nothing.
    expect(children).toHaveLength(5)
    expect(children.filter((c) => c.title === 'Special Delegated Match')).toHaveLength(1)

    vi.mocked(fetchSessions).mockResolvedValue([parent, ...children])
    const user = userEvent.setup()
    renderModal()
    await waitFor(() => expect(screen.getByText('Parent Chat')).toBeInTheDocument())

    const input = screen.getByRole('textbox', { name: 'Search sessions' })
    await user.type(input, 'special')

    // The matching child is revealed, nested under its parent — the parent
    // is shown for context even though it did not itself match — and its
    // non-matching siblings are NOT force-revealed as separate top-level
    // rows (they only appear because the whole node got expanded).
    await waitFor(() => expect(screen.getByText('Special Delegated Match')).toBeInTheDocument())
    expect(screen.getByText('Parent Chat')).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: 'Collapse Parent Chat delegated sessions' }),
    ).toHaveAttribute('aria-expanded', 'true')
  })

  it('a fan-out past the row threshold mounts inside a virtualized viewport whose total scroll height reflects every descendant', async () => {
    // @tanstack/virtual-core computes `getTotalSize() = count * estimateSize`
    // from its full item count regardless of how many it actually mounts —
    // this is a DETERMINISTIC, jsdom-safe way to prove the virtualizer
    // received the correct full row count (1 parent + 30 children = 31,
    // i.e. every descendant is logically present, none silently dropped)
    // without depending on jsdom's absent layout engine to produce a
    // specific mounted-DOM-node count. `ChatScreen.virtualization.test.tsx`
    // documents the same underlying limitation ("We allow 0 as an
    // acceptable outcome in jsdom (no layout engine)") for exactly this
    // reason — a strict ">0 mounted rows" assertion is not reliable here,
    // so this test asserts the height-derived row count instead, which is.
    const parent = makeSession({ id: 'parent-1', title: 'Parent Chat', child_count: 30 })
    const children = Array.from({ length: 30 }, (_, i) => makeChild(i, parent.id))
    // Positive lower bound (Rule 4): the fixture really carries 30 children
    // before asserting anything about bounded/virtualized rendering.
    expect(children).toHaveLength(30)

    vi.mocked(fetchSessions).mockResolvedValue([parent, ...children])

    // SessionTree.tsx only takes the virtualized branch when `ResizeObserver`
    // is defined (absent by default in this file's jsdom environment — the
    // same feature-detect ChatScreen.tsx's VirtualizedMessageList uses, so
    // every other test in this file, which does not stub it, keeps getting
    // the plain fully-rendered list with zero behavior change). Only the
    // STUB matters for this assertion — `getTotalSize()` is derived purely
    // from item count × estimateSize, independent of the container's
    // measured viewport size, so no offsetWidth/offsetHeight patching is
    // needed for this particular check (unlike a mounted-row-count check).
    class StubResizeObserver {
      observe() {}
      unobserve() {}
      disconnect() {}
    }
    vi.stubGlobal('ResizeObserver', StubResizeObserver)

    try {
      const user = userEvent.setup()
      renderModal()
      await waitFor(() => expect(screen.getByText('Parent Chat')).toBeInTheDocument())

      // Manually expand (no search text needed) — the group now has 31 rows
      // (1 root + 30 children), crossing VIRTUALIZE_ROW_THRESHOLD (20).
      const expandBtn = screen.getByRole('button', { name: 'Expand Parent Chat delegated sessions' })
      await user.click(expandBtn)

      const scrollEl = await screen.findByTestId('search-session-list-virtual-scroll')
      const spacer = await waitFor(() => {
        const el = scrollEl.querySelector<HTMLElement>('[data-testid="session-tree-virtual-spacer"]')
        expect(el).not.toBeNull()
        return el!
      })

      // FR-094: the group rendered inside a capped-height virtualized
      // viewport (not the plain unbounded list every other fixture in this
      // file takes), and the virtualizer's total scrollable height is
      // exactly 31 rows' worth — proof the DOM never has to hold the whole
      // fan-out at once the way the unvirtualized `groups.map(...)` used to
      // (SearchModal.tsx:363/:687 per the ADR-057 spec's own citation),
      // while still accounting for every one of the 30 children (none
      // dropped or truncated to fit a page).
      expect(spacer.style.height).toBe('1736px') // 31 rows * 56px estimateRowHeight
      // Best-effort, jsdom-tolerant bound on the mounted DOM node count
      // (never MORE than the full result count, whatever the environment's
      // layout measurement yields it to be — 0 is an accepted outcome here,
      // same caveat as ChatScreen.virtualization.test.tsx).
      const mountedRows = scrollEl.querySelectorAll('[data-index]')
      expect(mountedRows.length).toBeLessThan(31)
    } finally {
      vi.unstubAllGlobals()
    }
  })

  it('with no active search, the fan-out stays collapsed behind an expand affordance (manual browse, no query typed)', async () => {
    const parent = makeSession({ id: 'parent-1', title: 'Parent Chat', child_count: 30 })
    const children = Array.from({ length: 30 }, (_, i) => makeChild(i, parent.id))
    expect(children).toHaveLength(30)

    vi.mocked(fetchSessions).mockResolvedValue([parent, ...children])
    renderModal()

    await waitFor(() => expect(screen.getByText('Parent Chat')).toBeInTheDocument())

    // Not one of the 30 children renders until the user expands the parent.
    expect(screen.queryByText('Delegated task 0')).not.toBeInTheDocument()
    expect(screen.queryByText('Delegated task 29')).not.toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: 'Expand Parent Chat delegated sessions' }),
    ).toHaveAttribute('aria-expanded', 'false')
  })
})

/**
 * FeedKindLabel — the child-chat breadcrumb back to the parent (founder
 * decision 2026-10-20, "Breadcrumb"): a helper or task run reads
 * "<parent title> › Helper"; the parent part opens the parent chat through
 * the shared session-selection path. When the parent cannot be resolved the
 * plain kind label shows with no link — never a fabricated name.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider, useQuery } from '@tanstack/react-query'
import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { fetchAgents, fetchSessions, fetchWorkspaces } from '@/lib/api'
import type { Agent, Session } from '@/lib/api'

const selectSession = vi.hoisted(() => vi.fn())
vi.mock('./useSelectSession', () => ({ useSelectSession: () => selectSession }))
vi.mock('@/lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api')>()),
  fetchAgents: vi.fn(),
  fetchSessions: vi.fn(),
  fetchWorkspaces: vi.fn(),
}))

import { FeedKindLabel } from './FeedKindLabel'

const WORKSPACE_ID = 'ws_crumb_test'
const CHILD_ID = 'sid-child'
const PARENT_ID = 'sid-parent'

const mia = { id: 'mia', name: 'Mia', type: 'core', locked: true, figure: {}, role: {}, revision: {} } as unknown as Agent

function session(overrides: Partial<Session>): Session {
  return { id: 'x', type: 'chat', title: '', status: 'active', agent_id: 'mia', ...overrides } as Session
}

function setChildDescriptor(type: 'delegate' | 'task' = 'delegate'): void {
  useWorkspacesStore.setState({ activeWorkspaceId: WORKSPACE_ID })
  useSessionStore.setState({
    activeSessionId: CHILD_ID,
    attachedSessionType: null,
    attachedTaskTitle: null,
    sessionByWorkspace: { [WORKSPACE_ID]: { id: CHILD_ID, type, title: 'child', agentId: 'mia' } },
  })
}

function renderLabel() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(<QueryClientProvider client={qc}><FeedKindLabel /></QueryClientProvider>)
}

function label(): HTMLElement | null {
  return document.querySelector('[data-testid="feed-kind-label"]')
}

beforeEach(() => {
  selectSession.mockReset()
  vi.mocked(fetchAgents).mockReset().mockResolvedValue([mia])
  vi.mocked(fetchWorkspaces).mockReset().mockResolvedValue([])
  vi.mocked(fetchSessions).mockReset()
  setChildDescriptor()
})

describe('FeedKindLabel — child chat breadcrumb', () => {
  it('a helper whose parent is a main reads "Mia - Main Session › Helper" and the parent part opens the parent', async () => {
    const parent = session({ id: PARENT_ID, type: 'main', title: 'stale stored title' })
    vi.mocked(fetchSessions).mockResolvedValue([
      parent,
      session({ id: CHILD_ID, type: 'delegate', title: 'child', parent_session_id: PARENT_ID }),
    ])
    renderLabel()
    await waitFor(() => {
      expect(label()?.textContent).toBe('Mia - Main Session › Helper')
    })
    await userEvent.click(screen.getByRole('button', { name: 'Mia - Main Session' }))
    expect(selectSession).toHaveBeenCalledTimes(1)
    expect(selectSession).toHaveBeenCalledWith(parent)
  })

  it('a helper whose parent cannot be resolved shows plain "Helper" with no link', async () => {
    vi.mocked(fetchSessions).mockResolvedValue([
      session({ id: CHILD_ID, type: 'delegate', title: 'child', parent_session_id: 'deleted-parent' }),
    ])
    renderLabel()
    await waitFor(() => {
      expect(vi.mocked(fetchSessions)).toHaveBeenCalled()
    })
    await waitFor(() => {
      expect(label()?.textContent).toBe('Helper')
    })
    expect(screen.queryByRole('button')).toBeNull()
    expect(screen.queryByRole('link')).toBeNull()
  })

  it('a helper with no parent_session_id shows plain "Helper" with no link', async () => {
    vi.mocked(fetchSessions).mockResolvedValue([session({ id: CHILD_ID, type: 'delegate', title: 'child' })])
    renderLabel()
    await waitFor(() => {
      expect(vi.mocked(fetchSessions)).toHaveBeenCalled()
    })
    expect(label()?.textContent).toBe('Helper')
    expect(screen.queryByRole('button')).toBeNull()
  })

  it('a failed sessions fetch leaves plain "Helper" with no link', async () => {
    vi.mocked(fetchSessions).mockRejectedValue(new Error('sessions down'))
    renderLabel()
    await waitFor(() => {
      expect(vi.mocked(fetchSessions)).toHaveBeenCalled()
    })
    expect(label()?.textContent).toBe('Helper')
    expect(screen.queryByRole('button')).toBeNull()
  })

  it('a task run whose parent is an extra chat reads "<title> › Task run"', async () => {
    setChildDescriptor('task')
    vi.mocked(fetchSessions).mockResolvedValue([
      session({ id: PARENT_ID, type: 'chat', title: 'Launch notes' }),
      session({ id: CHILD_ID, type: 'task', title: 'child', parent_session_id: PARENT_ID }),
    ])
    renderLabel()
    await waitFor(() => {
      expect(label()?.textContent).toBe('Launch notes › Task run')
    })
  })

  it('a main parent whose agent roster is still loading stays plain "Helper" (no fabricated name)', async () => {
    vi.mocked(fetchAgents).mockReturnValue(new Promise(() => {}))
    const resolvedSessions = [
      session({ id: PARENT_ID, type: 'main' }),
      session({ id: CHILD_ID, type: 'delegate', parent_session_id: PARENT_ID }),
    ]
    vi.mocked(fetchSessions).mockResolvedValue(resolvedSessions)

    // Observe the same real queries without fetching: a request starting is
    // not proof that the parent data has reached a committed React render.
    // Render the real label beneath the observer so that the witness commits
    // alongside a label render that can read the resolved sessions.
    function LabelWithQueryState() {
      const sessionsQuery = useQuery({
        queryKey: ['sessions', 'flat'],
        queryFn: () => fetchSessions(undefined, undefined, { flat: true }),
        enabled: false,
      })
      const agentsQuery = useQuery({ queryKey: ['agents'], queryFn: fetchAgents, enabled: false })
      return (
        <>
          <FeedKindLabel />
          <output data-testid="breadcrumb-query-state">
            {JSON.stringify({
              sessions: sessionsQuery.data,
              sessionsStatus: sessionsQuery.status,
              rosterStatus: agentsQuery.status,
              rosterFetchStatus: agentsQuery.fetchStatus,
            })}
          </output>
        </>
      )
    }

    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(<QueryClientProvider client={qc}><LabelWithQueryState /></QueryClientProvider>)
    await waitFor(() => {
      expect(vi.mocked(fetchSessions)).toHaveBeenCalled()
      expect(vi.mocked(fetchSessions)).toHaveBeenCalledWith(undefined, undefined, { flat: true })
      expect(screen.getByTestId('breadcrumb-query-state').textContent).toBe(JSON.stringify({
        sessions: resolvedSessions,
        sessionsStatus: 'success',
        rosterStatus: 'pending',
        rosterFetchStatus: 'fetching',
      }))
    })
    expect(label()?.textContent).toBe('Helper')
    expect(screen.queryByRole('button')).toBeNull()
    expect(screen.queryByRole('link')).toBeNull()
  })
})

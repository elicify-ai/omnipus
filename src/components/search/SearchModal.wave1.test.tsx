import { describe, it, expect, vi, beforeEach, beforeAll, afterEach } from 'vitest'
import { render, screen, within, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { act } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { SearchModal } from './SearchModal'
import { useUiStore } from '@/store/ui'
import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'
import type { Agent, Session, Workspace } from '@/lib/api'

// Wave 1 Sessions oracles: FR-025/026/030/031/033/034/036 and ARCH-DECISIONS 4.
// execution and background_command_count are not on the SPA Session type yet.
// They are carried on the fixture object; the modal must read them.

type WaveSession = Session & {
  execution?: 'queued' | 'running'
  background_command_count?: number
}

beforeAll(() => {
  if (!Element.prototype.scrollIntoView) Element.prototype.scrollIntoView = () => {}
})

const mockSelectSession = vi.fn()
vi.mock('@/components/chat/useSelectSession', () => ({
  useSelectSession: () => mockSelectSession,
}))

const mockNavigate = vi.fn()
vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return { ...actual, useNavigate: () => mockNavigate }
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

import { fetchAgents, fetchSessions, fetchWorkspaces, readSessionFetchCoverage } from '@/lib/api'
import type { Session as WireSession, SessionPage } from '@/lib/api/generated/openapi-types'

afterEach(() => {
  vi.unstubAllGlobals()
})

function sessionRow(id: string): HTMLElement {
  const row = document.getElementById(`search-result-${id}`)
  expect(row, `original session row ${id}`).not.toBeNull()
  return row as HTMLElement
}

function expectNestedAfter(childId: string, parentId: string) {
  const parent = sessionRow(parentId)
  const child = sessionRow(childId)
  // FR-030: a helper must be indented below its real parent, not a root
  // merely painted adjacent to another matching title.
  expect(parent.compareDocumentPosition(child) & Node.DOCUMENT_POSITION_FOLLOWING).not.toBe(0)
  const indent = child.parentElement?.parentElement
  expect(indent?.className, `nested helper ${childId}`).toContain('pl-')
  expect(parent.parentElement?.parentElement?.className, `root parent ${parentId}`).not.toContain('pl-')
}

function wireSession(overrides: Partial<WireSession>): WireSession {
  return {
    id: 'wire-control', agent_id: 'agent-1', title: 'Control chat', type: 'chat',
    status: 'active', channel: 'webchat', partitions: [], workspace_id: 'ws-1',
    created_at: '2026-07-16T09:00:00Z', updated_at: '2026-07-16T11:00:00Z',
    stats: { tokens_in: 0, tokens_out: 0, tokens_total: 0, cost: 0, tool_calls: 0, message_count: 0 },
    ...overrides,
  }
}

function makeSession(overrides: Partial<WaveSession> = {}): WaveSession {
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

function makeWorkspace(): Workspace {
  return {
    id: 'ws-1',
    name: 'Alpha Workspace',
    status: 'active',
    pinned: false,
    pin_order: 0,
    task_count: 0,
    created_at: '2025-01-01T00:00:00Z',
    updated_at: '2025-01-01T00:00:00Z',
    revision: '0'.repeat(64),
  }
}

function makeAgent(): Agent {
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
    max_tool_iterations_source: 'global',
    max_tool_iterations_override_ignored: false,
    memory_enabled: true,
    figure: 'Omnipus',
    role: 'general',
    revision: '0'.repeat(64),
  }
}

function renderModal() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <SearchModal />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.mocked(fetchAgents).mockReset().mockResolvedValue([makeAgent()])
  vi.mocked(fetchWorkspaces).mockReset().mockResolvedValue([makeWorkspace()])
  vi.mocked(fetchSessions).mockReset().mockResolvedValue([makeSession()])
  mockSelectSession.mockClear()
  mockNavigate.mockClear()
  act(() => {
    useUiStore.setState({ searchModalOpen: true, searchModalWorkspaceFilter: null, searchModalMode: 'sessions' })
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

describe('Sessions view — title, status, kind', () => {
  it('is titled Sessions', async () => {
    renderModal()
    expect(await screen.findByRole('heading', { name: 'Sessions' })).toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'Search sessions' })).not.toBeInTheDocument()
  })

  it('maps lifecycle_state to the six status words and does not invent Working from status active', async () => {
    vi.mocked(fetchSessions).mockResolvedValue([
      makeSession({ id: 'work', title: 'Doing', lifecycle_state: 'working', status: 'active' }),
      makeSession({ id: 'wait', title: 'Asking', lifecycle_state: 'waiting_for_answer' }),
      makeSession({ id: 'done', title: 'Finished', lifecycle_state: 'done' }),
      makeSession({ id: 'fail', title: 'Broke', lifecycle_state: 'failed' }),
      makeSession({ id: 'stop', title: 'Halted', lifecycle_state: 'stopped', stop_note: { at: '2026-10-08T00:00:00Z', by: 'human:1', cause: 'stop', seq: 1 } }),
      makeSession({ id: 'int', title: 'Cut', lifecycle_state: 'interrupted' }),
      makeSession({ id: 'none', title: 'No record', status: 'active' }),
      makeSession({ id: 'arch', title: 'Archived chat', status: 'archived' }),
    ])
    renderModal()
    const row = async (title: string) => {
      const titleNode = await screen.findByText(title)
      const container = titleNode.closest('[id^="search-result-"]')
      expect(container, title).not.toBeNull()
      return within(container as HTMLElement)
    }
    expect((await row('Doing')).getByText('Working')).toBeInTheDocument()
    expect(await (await row('Asking')).findByText(/^Waiting/)).toBeInTheDocument()
    expect(await (await row('Finished')).findByText('Done')).toBeInTheDocument()
    expect(await (await row('Broke')).findByText('Failed')).toBeInTheDocument()
    expect(await (await row('Halted')).findByText(/Stopped/)).toBeInTheDocument()
    expect(await (await row('Cut')).findByText('Interrupted')).toBeInTheDocument()
    const none = await row('No record')
    expect(none.getByText(/unavailable/i)).toBeInTheDocument()
    expect(none.queryByText('Working')).not.toBeInTheDocument()
    expect(await (await row('Archived chat')).findByText('Done')).toBeInTheDocument()
  })

  it('shows real kind labels, including Channel and Heartbeat, and helper for delegate', async () => {
    vi.mocked(fetchSessions).mockResolvedValue([
      makeSession({ id: 'c', title: 'A chat', type: 'chat' }),
      makeSession({ id: 't', title: 'A task', type: 'task' }),
      makeSession({ id: 'h', title: 'A helper', type: 'delegate', parent_session_id: 'c' }),
      makeSession({ id: 's', title: 'A schedule', type: 'scheduled' }),
      makeSession({ id: 'ch', title: 'A channel', type: 'channel' }),
      makeSession({ id: 'hb', title: 'A beat', type: 'heartbeat' }),
    ])
    renderModal()
    expect(await screen.findByText('A chat')).toBeInTheDocument()
    expect(screen.getByText('Helper')).toBeInTheDocument()
    expect(screen.getByText('Task')).toBeInTheDocument()
    expect(screen.getByText(/Scheduled/)).toBeInTheDocument()
    expect(screen.getByText('Channel')).toBeInTheDocument()
    expect(screen.getByText('Heartbeat')).toBeInTheDocument()
    expect(screen.queryByText(/^HB$/)).not.toBeInTheDocument()
  })
})

describe('Sessions view — Running filter and queued', () => {
  it('Running is execution running only, and a queued row is labelled Queued', async () => {
    const user = userEvent.setup()
    vi.mocked(fetchSessions).mockResolvedValue([
      makeSession({ id: 'run', title: 'Actually running', lifecycle_state: 'working', execution: 'running' }),
      makeSession({ id: 'q', title: 'Only queued', lifecycle_state: 'working', execution: 'queued' }),
      makeSession({ id: 'idle', title: 'Sitting', lifecycle_state: 'done' }),
    ])
    renderModal()
    expect(await screen.findByText('Only queued')).toBeInTheDocument()
    expect(screen.getByText('Queued')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Running' }))
    expect(screen.getByText('Actually running')).toBeInTheDocument()
    expect(screen.queryByText('Only queued')).not.toBeInTheDocument()
    expect(screen.queryByText('Sitting')).not.toBeInTheDocument()
  })
})

describe('Sessions view — hierarchy, fold, shell count, tokens', () => {
  it('nests a helper under its parent even when only the helper title matches', async () => {
    const user = userEvent.setup()
    vi.mocked(fetchSessions).mockResolvedValue([
      makeSession({ id: 'parent', title: 'Launch notes' }),
      makeSession({ id: 'child', title: 'unique-helper-needle', type: 'delegate', parent_session_id: 'parent', execution: 'running' }),
      makeSession({ id: 'control', title: 'Unrelated control' }),
    ])
    renderModal()
    const box = await screen.findByRole('textbox')
    expect(await screen.findByText('Unrelated control')).toBeInTheDocument()
    await user.type(box, 'unique-helper-needle')
    await waitFor(() => expect(screen.queryByText('Unrelated control')).not.toBeInTheDocument())
    expect(await screen.findByText('unique-helper-needle')).toBeInTheDocument()
    expect(screen.getByText('Launch notes')).toBeInTheDocument()
    expectNestedAfter('child', 'parent')
    await user.click(screen.getByRole('button', { name: 'Running' }))
    expect(screen.getByText('Launch notes')).toBeInTheDocument()
    expectNestedAfter('child', 'parent')
    expect(screen.queryByText(/parent chat unavailable/i)).not.toBeInTheDocument()
  })

  it('shows a missing parent as parent chat unavailable and still offers Open', async () => {
    vi.mocked(fetchSessions).mockResolvedValue([
      makeSession({ id: 'orphan', title: 'Lost helper', type: 'delegate', parent_session_id: 'gone-parent' }),
    ])
    renderModal()
    expect(await screen.findByText(/parent chat unavailable/i)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /open/i })).toBeInTheDocument()
    expect(screen.getByText('Lost helper')).toBeInTheDocument()
    await userEvent.click(within(sessionRow('orphan')).getByRole('button', { name: 'Open Lost helper' }))
    expect(mockSelectSession).toHaveBeenCalledTimes(1)
    expect(mockSelectSession).toHaveBeenCalledWith(expect.objectContaining({ id: 'orphan', parent_session_id: 'gone-parent' }))
  })

  it('does not call a helper parent unavailable when the session fetch is partial', async () => {
    // SPEC FR-032 / DEP-ACT: an incomplete enumeration is partial or unknown,
    // never a confirmed missing parent. "parent chat unavailable" is only for
    // a complete fetch whose parent is really absent (BDD-08.2). FE-3 attaches
    // partialErrors and incomplete onto the Session[] that fetchSessions
    // returns (origin/work/nav-wave1-fe3 src/lib/api/sessions.ts
    // SessionListResult). The helper stays Openable.
    const rows = [
      makeSession({ id: 'orphan', title: 'Lost helper', type: 'delegate', parent_session_id: 'gone-parent' }),
    ]
    const partial = rows as typeof rows & { partialErrors: string[]; incomplete: boolean }
    partial.partialErrors = ['agent-store-failed']
    partial.incomplete = true
    vi.mocked(fetchSessions).mockResolvedValue(partial)
    renderModal()
    expect(await screen.findByText('Lost helper')).toBeInTheDocument()
    expect(screen.queryByText(/parent chat unavailable/i)).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: /open/i })).toBeInTheDocument()
  })

  it('propagates partial HTTP pages through real fetchSessions and repairs hierarchy on Retry', async () => {
    const actual = await vi.importActual<typeof import('@/lib/api')>('@/lib/api')
    // ARCH decision 4 / C2: current-boot running and queued are distinct;
    // a prior-boot interrupted root omits execution until actually re-adopted.
    const orphan = wireSession({
      id: 'orphan', title: 'Partly loaded helper', type: 'delegate', parent_session_id: 'unloaded-parent',
      lifecycle_state: 'working', execution: 'running', background_command_count: 2,
    })
    const control = wireSession({
      id: 'control', title: 'Other page', lifecycle_state: 'working', execution: 'queued', background_command_count: 0,
    })
    const interrupted = wireSession({
      id: 'restart-root', title: 'Restarted conversation', status: 'interrupted', lifecycle_state: 'interrupted',
    })
    const parent = wireSession({
      id: 'unloaded-parent', title: 'Recovered parent', status: 'interrupted', lifecycle_state: 'interrupted',
    })
    const reAdopted = wireSession({
      ...interrupted, status: 'active', lifecycle_state: 'working', execution: 'running',
    })
    let recovered = false
    const requests: string[] = []
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = new URL(String(input), 'http://localhost')
      expect(url.pathname).toBe('/api/v1/sessions')
      expect(url.searchParams.get('flat')).toBe('true')
      requests.push(url.searchParams.get('offset') ?? 'first')
      const page: SessionPage = recovered
        ? { sessions: [parent, orphan, control, reAdopted] }
        : url.searchParams.has('offset')
          ? { sessions: [control, interrupted], partial_errors: ['agent-store-failed'] }
          : { sessions: [orphan], next_cursor: '1' }
      return new Response(JSON.stringify(page), { status: 200, headers: { 'Content-Type': 'application/json' } })
    }))
    // Do not manufacture coverage flags: the real pagination aggregator must
    // derive them from the server envelope (ARCH decision 4 / FR-032).
    vi.mocked(fetchSessions).mockImplementation(actual.fetchSessions)
    const result = await fetchSessions(undefined, undefined, { flat: true })
    expect(result.map((row) => row.id)).toEqual(['orphan', 'control', 'restart-root'])
    expect(readSessionFetchCoverage(result)).toEqual({ partialErrors: ['agent-store-failed'], incomplete: true })
    expect(requests).toEqual(['first', '1'])
    // The real generated validator and rawToSession adapter must preserve
    // known zero versus an omitted/unknown process count, not just the UI text.
    expect(result.map(({ id, lifecycle_state, execution, background_command_count }) => ({
      id, lifecycle_state, execution, background_command_count,
    }))).toEqual([
      { id: 'orphan', lifecycle_state: 'working', execution: 'running', background_command_count: 2 },
      { id: 'control', lifecycle_state: 'working', execution: 'queued', background_command_count: 0 },
      { id: 'restart-root', lifecycle_state: 'interrupted', execution: undefined, background_command_count: undefined },
    ])
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    render(<QueryClientProvider client={client}><SearchModal /></QueryClientProvider>)
    expect(await screen.findByText('Partly loaded helper')).toBeInTheDocument()
    expect(screen.getByText('Session list is incomplete (1 source error). Missing parents are not marked unavailable.')).toBeInTheDocument()
    expect(screen.getByTestId('session-unplaced')).toHaveTextContent(/partial list/i)
    expect(screen.queryByText(/^parent chat unavailable$/i)).not.toBeInTheDocument()
    expect(within(sessionRow('orphan')).getByRole('button', { name: 'Open Partly loaded helper' })).toBeInTheDocument()
    expect(screen.getByTestId('session-status-orphan').textContent).toBe('Working')
    expect(screen.getByTestId('session-status-control').textContent).toBe('Queued')
    expect(screen.getByTestId('session-status-restart-root').textContent).toBe('Interrupted')
    expect(screen.getByTestId('session-background-orphan').textContent).toBe('2 background commands running')
    expect(within(sessionRow('control')).queryByText(/background commands/)).not.toBeInTheDocument()
    expect(within(sessionRow('restart-root')).queryByText(/background commands/)).not.toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Running' }))
    expect(screen.getAllByTestId('session-row').map((row) => row.dataset.sessionId)).toEqual(['orphan'])
    expect(screen.queryByText('Other page')).not.toBeInTheDocument()
    expect(screen.queryByText('Restarted conversation')).not.toBeInTheDocument()

    recovered = true
    await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(await screen.findByText('Recovered parent')).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByText(/Session list is incomplete/)).not.toBeInTheDocument())
    expectNestedAfter('orphan', 'unloaded-parent')
    expect(screen.queryByTestId('session-unplaced')).not.toBeInTheDocument()
    expect(screen.getAllByTestId('session-row').map((row) => row.dataset.sessionId).sort()).toEqual([
      'orphan', 'restart-root', 'unloaded-parent',
    ])
    // A nonmatching Interrupted parent stays as context for the running
    // helper. The same re-adopted root now matches Running and loses the old label.
    expect(screen.getByTestId('session-status-unloaded-parent').textContent).toBe('Interrupted')
    expect(screen.getByTestId('session-status-orphan').textContent).toBe('Working')
    expect(screen.getByTestId('session-status-restart-root').textContent).toBe('Working')
    expect(within(sessionRow('restart-root')).queryByText('Interrupted')).not.toBeInTheDocument()
    expect(screen.queryByText('Other page')).not.toBeInTheDocument()
    expect(screen.getByTestId('session-background-orphan').textContent).toBe('2 background commands running')
    expect(within(sessionRow('unloaded-parent')).queryByText(/background commands/)).not.toBeInTheDocument()
    const confirmed = client.getQueryData<Session[]>(['sessions', 'flat'])
    expect(confirmed?.map(({ id, execution, background_command_count }) => ({
      id, execution, background_command_count,
    }))).toEqual([
      { id: 'unloaded-parent', execution: undefined, background_command_count: undefined },
      { id: 'orphan', execution: 'running', background_command_count: 2 },
      { id: 'control', execution: 'queued', background_command_count: 0 },
      { id: 'restart-root', execution: 'running', background_command_count: undefined },
    ])
    expect(readSessionFetchCoverage(confirmed)).toEqual({ partialErrors: [], incomplete: false })
    expect(requests).toEqual(['first', '1', 'first', '1', 'first'])
    await userEvent.click(screen.getByRole('button', { name: 'All' }))
    expect(screen.getByTestId('session-status-control').textContent).toBe('Queued')
    expect(within(sessionRow('control')).queryByText(/background commands/)).not.toBeInTheDocument()
  })

  it('folds nine consecutive identical helpers and keeps each original Open', async () => {
    const user = userEvent.setup()
    const parent = makeSession({ id: 'p', title: 'Parent chat' })
    const helpers = Array.from({ length: 9 }, (_, i) =>
      makeSession({
        id: `h${i}`,
        title: 'Same helper',
        type: 'delegate',
        parent_session_id: 'p',
        lifecycle_state: i === 0 ? 'failed' : 'done',
      }),
    )
    const other = makeSession({ id: 'q', title: 'Other parent' })
    const foreign = makeSession({ id: 'qh', title: 'Same helper', type: 'delegate', parent_session_id: 'q' })
    vi.mocked(fetchSessions).mockResolvedValue([parent, ...helpers, other, foreign])
    renderModal()
    const summary = await screen.findByRole('button', { name: /9 similar helper runs/i })
    expect(screen.getAllByText('Same helper').length).toBe(1)
    await user.click(summary)
    expect(screen.getAllByRole('button', { name: /^Open / }).length).toBeGreaterThanOrEqual(9)
    expect(screen.getByText('Failed')).toBeInTheDocument()
    expect(screen.getAllByRole('button', { name: 'Open Same helper' })).toHaveLength(10)
    for (const helper of helpers) {
      const row = within(sessionRow(helper.id))
      expect(row.getByTestId(`session-status-${helper.id}`)).toHaveTextContent(helper.id === 'h0' ? 'Failed' : 'Done')
      await user.click(row.getByRole('button', { name: 'Open Same helper' }))
      expect(mockSelectSession).toHaveBeenLastCalledWith(expect.objectContaining({
        id: helper.id, parent_session_id: 'p', lifecycle_state: helper.lifecycle_state,
      }))
    }
    await user.click(within(sessionRow('qh')).getByRole('button', { name: 'Open Same helper' }))
    expect(mockSelectSession).toHaveBeenLastCalledWith(expect.objectContaining({ id: 'qh', parent_session_id: 'q' }))
    expect(mockSelectSession).toHaveBeenCalledTimes(10)
  })

  it('puts the background-command sentence on the owning row only', async () => {
    vi.mocked(fetchSessions).mockResolvedValue([
      makeSession({ id: 'origin', title: 'Origin chat', background_command_count: 2 }),
      makeSession({ id: 'child', title: 'Child helper', type: 'delegate', parent_session_id: 'origin' }),
      makeSession({ id: 'zero', title: 'Quiet chat', background_command_count: 0 }),
      makeSession({ id: 'unknown', title: 'Unknown commands' }),
    ])
    renderModal()
    const sentence = await screen.findByText('2 background commands running')
    expect(sentence.closest('#search-result-origin, [data-session-id="origin"]')).not.toBeNull()
    expect(screen.queryByText('0 background commands running')).not.toBeInTheDocument()
    expect(within(document.getElementById('search-result-unknown')!).queryByText(/background commands/i)).not.toBeInTheDocument()
  })

  it('shows a stored token count of zero', async () => {
    vi.mocked(fetchSessions).mockResolvedValue([makeSession({ id: 'z', title: 'Zero tokens', total_tokens: 0 })])
    renderModal()
    await screen.findByText('Zero tokens')
    const row = document.getElementById('search-result-z')
    expect(row).not.toBeNull()
    expect(row!.textContent).toMatch(/(^|\D)0(\D|$)/)
  })

  it.each(['removal', 'same-length replacement'] as const)('clears a highlighted session after %s until explicit new navigation', async (change) => {
    const alpha = makeSession({ id: 'alpha', title: 'Alpha chat' })
    const beta = makeSession({ id: 'beta', title: 'Beta chat' })
    vi.mocked(fetchSessions).mockResolvedValue([alpha, beta])
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(<QueryClientProvider client={client}><SearchModal /></QueryClientProvider>)
    await screen.findByText('Beta chat')
    const box = screen.getByRole('textbox', { name: 'Search sessions' })
    box.focus()
    await userEvent.keyboard('{ArrowDown}')
    expect(sessionRow('beta').textContent).toContain('↵')
    act(() => client.setQueryData(['sessions', 'flat'], change === 'removal'
      ? [alpha]
      : [alpha, makeSession({ id: 'gamma', title: 'Gamma chat' })]))
    await waitFor(() => expect(screen.queryByText('Beta chat')).not.toBeInTheDocument())
    expect(box).toHaveFocus()
    expect(screen.getByTestId('sessions-activation-live').textContent?.trim()).toBe('Highlighted session is unavailable.')
    await userEvent.keyboard('{Enter}')
    expect(mockSelectSession).not.toHaveBeenCalled()
    expect(sessionRow('alpha').textContent).not.toContain('↵')
    await userEvent.keyboard('{ArrowDown}{Enter}')
    expect(mockSelectSession).toHaveBeenCalledTimes(1)
    expect(mockSelectSession).toHaveBeenCalledWith(expect.objectContaining({ id: 'alpha' }))
  })

  it('clears a highlighted helper when its fold collapses, without substituting a sibling or summary', async () => {
    const user = userEvent.setup()
    vi.mocked(fetchSessions).mockResolvedValue([
      makeSession({ id: 'parent', title: 'Parent chat' }),
      ...Array.from({ length: 9 }, (_, i) => makeSession({ id: `h${i}`, title: 'Same helper', type: 'delegate', parent_session_id: 'parent' })),
    ])
    renderModal()
    const summary = await screen.findByRole('button', { name: /9 similar helper runs/i })
    await user.click(summary)
    const box = screen.getByRole('textbox', { name: 'Search sessions' })
    box.focus()
    // Parent -> expand-only summary -> original first helper.
    await user.keyboard('{ArrowDown}{ArrowDown}')
    expect(sessionRow('h0').textContent).toContain('↵')
    await user.click(summary)
    expect(screen.queryByText('Same helper')).not.toBeInTheDocument()
    expect(box).toHaveFocus()
    expect(screen.getByTestId('sessions-activation-live').textContent?.trim()).toBe('Highlighted session is unavailable.')
    await user.keyboard('{Enter}')
    expect(mockSelectSession).not.toHaveBeenCalled()
    expect(summary).toHaveAttribute('aria-expanded', 'false')
    await user.keyboard('{ArrowDown}{Enter}')
    expect(mockSelectSession).toHaveBeenCalledTimes(1)
    expect(mockSelectSession).toHaveBeenCalledWith(expect.objectContaining({ id: 'parent' }))
  })

  it('activates the highlighted session after a reorder, not the row that slid into that index', async () => {
    const stamp = '2026-07-16T11:00:00Z'
    const alpha = makeSession({ id: 'alpha', title: 'Alpha chat', updated_at: stamp })
    const beta = makeSession({ id: 'beta', title: 'Beta chat', updated_at: stamp })
    vi.mocked(fetchSessions).mockResolvedValue([alpha, beta])
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    render(
      <QueryClientProvider client={client}>
        <SearchModal />
      </QueryClientProvider>,
    )
    await screen.findByText('Beta chat')
    screen.getByRole('textbox', { name: 'Search sessions' }).focus()
    await userEvent.keyboard('{ArrowDown}')
    expect(document.getElementById('search-result-beta')?.textContent).toContain('↵')
    act(() => {
      client.setQueryData(['sessions', 'flat'], [beta, alpha])
    })
    await userEvent.keyboard('{Enter}')
    expect(mockSelectSession).toHaveBeenCalledWith(expect.objectContaining({ id: 'beta' }))
  })
})

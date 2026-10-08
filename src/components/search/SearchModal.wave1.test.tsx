import { describe, it, expect, vi, beforeEach, beforeAll } from 'vitest'
import { render, screen, within } from '@testing-library/react'
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

import { fetchAgents, fetchSessions, fetchWorkspaces } from '@/lib/api'

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
      makeSession({ id: 'child', title: 'unique-helper-needle', type: 'delegate', parent_session_id: 'parent' }),
    ])
    renderModal()
    const box = await screen.findByRole('textbox')
    await user.type(box, 'unique-helper-needle')
    expect(await screen.findByText('unique-helper-needle')).toBeInTheDocument()
    expect(screen.getByText('Launch notes')).toBeInTheDocument()
  })

  it('shows a missing parent as parent chat unavailable and still offers Open', async () => {
    vi.mocked(fetchSessions).mockResolvedValue([
      makeSession({ id: 'orphan', title: 'Lost helper', type: 'delegate', parent_session_id: 'gone-parent' }),
    ])
    renderModal()
    expect(await screen.findByText(/parent chat unavailable/i)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /open/i })).toBeInTheDocument()
    expect(screen.getByText('Lost helper')).toBeInTheDocument()
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

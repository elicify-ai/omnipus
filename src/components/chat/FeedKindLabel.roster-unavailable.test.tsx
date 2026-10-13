// FeedKindLabel roster-unavailable tests (combined-review R2-S4-03).
//
// When the shared ['agents'] roster FAILS, the above-feed line falls back to
// the agent id ("jim - Main Session") — the ruling's accepted stand-in — but
// until now nothing marked that fallback, so a rendered id read as an actual
// agent name. The strip now carries the same honest signal the search modal
// shows for the identical trigger ("agent names unavailable"), and ONLY in
// the real fallback case:
//   - a merely-loading roster keeps the line absent (existing behaviour);
//   - a failed REFETCH whose retained data still names the agent keeps the
//     plain name — nothing fell back, so there is nothing to disclaim.
//
// Harness copied from FeedKindLabel.main-title.test.tsx.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { fetchAgents } from '@/lib/api'
import type { Agent } from '@/lib/api'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn(),
  }
})

import { FeedKindLabel } from './FeedKindLabel'

const WORKSPACE_ID = 'ws_roster_unavailable_test'

function agentFixture(overrides: Partial<Agent>): Agent {
  return {
    id: 'jim',
    name: 'Jim',
    type: 'core',
    locked: true,
    figure: {} as Agent['figure'],
    role: {} as Agent['role'],
    revision: {} as Agent['revision'],
    ...overrides,
  } as Agent
}

function setDescriptor(): void {
  useWorkspacesStore.setState({ activeWorkspaceId: WORKSPACE_ID })
  useSessionStore.setState({
    activeSessionId: 'sid-main',
    attachedSessionType: null,
    attachedTaskTitle: null,
    sessionByWorkspace: {
      [WORKSPACE_ID]: {
        id: 'sid-main',
        type: 'main',
        title: 'Jim - Main Session',
        agentId: 'jim',
      },
    },
  })
}

function renderLabel() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const view = render(
    <QueryClientProvider client={qc}>
      <FeedKindLabel />
    </QueryClientProvider>,
  )
  return { qc, view }
}

function label(): HTMLElement | null {
  return document.querySelector('[data-testid="feed-kind-label"]')
}

beforeEach(() => {
  vi.mocked(fetchAgents).mockReset()
  setDescriptor()
})

describe('FeedKindLabel — an id-fallback line is marked as such (R2-S4-03)', () => {
  it('when the roster has FAILED and the line shows the agent id, the strip says names are unavailable', async () => {
    vi.mocked(fetchAgents).mockRejectedValue(new Error('roster down'))
    renderLabel()
    await waitFor(() => {
      expect(label()?.textContent).toContain('jim - Main Session')
    })
    expect(label()?.textContent).toContain('agent names unavailable')
  })

  it('while the roster is merely LOADING the line is absent — no signal, existing behaviour', () => {
    vi.mocked(fetchAgents).mockReturnValue(new Promise(() => {})) // never settles
    renderLabel()
    expect(label()).toBeNull()
  })

  it('a failed REFETCH with retained data that still names the agent keeps the plain name', async () => {
    vi.mocked(fetchAgents)
      .mockResolvedValueOnce([agentFixture({})])
      .mockRejectedValueOnce(new Error('roster down'))
    const { qc } = renderLabel()
    await waitFor(() => {
      expect(label()?.textContent).toBe('Jim - Main Session')
    })
    // Refetch fails; React Query retains the last successful roster.
    await qc.refetchQueries({ queryKey: ['agents'] })
    await waitFor(() => {
      expect(qc.getQueryState(['agents'])?.status).toBe('error')
    })
    expect(label()?.textContent).toBe('Jim - Main Session')
    expect(label()?.textContent).not.toContain('unavailable')
  })

  it('when the roster loaded fine the line stays plain', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([agentFixture({})])
    renderLabel()
    await waitFor(() => {
      expect(label()?.textContent).toBe('Jim - Main Session')
    })
    expect(label()?.textContent).not.toContain('unavailable')
  })
})

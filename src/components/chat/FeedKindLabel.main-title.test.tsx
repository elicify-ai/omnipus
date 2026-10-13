/**
 * FeedKindLabel — the chat header's main-session title line (FR-008 amended).
 *
 * Honest-unavailable amendment (combined-review S4-04 + S2-05 SPA half,
 * 2026-10-12): the header NEVER shows a fabricated name. While the shared
 * ['agents'] query is still loading there is no title line at all (the
 * component's existing "nothing to say" presentation); when the query has
 * FAILED the line falls back to the agent ID — exactly the server's own
 * empty-name fallback (pkg/gateway/session_title.go::withDisplayTitle) — and
 * a title SNAPSHOT persisted in the browser (the workspace descriptor copy
 * taken at attach time) is never shown for a main, so a rename that happened
 * elsewhere still re-derives from the live roster.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
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

const WORKSPACE_ID = 'ws_main_title_test'

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

function setDescriptor(overrides: { type?: 'main' | 'chat'; title?: string | null; agentId?: string | null; id?: string }): void {
  useWorkspacesStore.setState({ activeWorkspaceId: WORKSPACE_ID })
  useSessionStore.setState({
    activeSessionId: overrides.id ?? 'sid-main',
    attachedSessionType: null,
    attachedTaskTitle: null,
    sessionByWorkspace: {
      [WORKSPACE_ID]: {
        id: overrides.id ?? 'sid-main',
        type: overrides.type ?? 'main',
        title: overrides.title ?? 'Jim - Main Session',
        // An explicitly-passed null must stay null (?? would coerce it).
        agentId: overrides.agentId !== undefined ? overrides.agentId : 'jim',
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
  setDescriptor({})
})

describe('FeedKindLabel — main-session header title (S4-04/S2-05 honest states)', () => {
  it('while the agents list is still loading there is NO title line — never a fabricated name', async () => {
    vi.mocked(fetchAgents).mockReturnValue(new Promise(() => {})) // never settles
    renderLabel()
    // The status line is absent while the roster is in flight.
    expect(label()).toBeNull()
    expect(screen.queryByText(/Unknown agent/)).not.toBeInTheDocument()
  })

  it('when the agents list has FAILED the line falls back to the AGENT ID — the server’s own empty-name fallback', async () => {
    vi.mocked(fetchAgents).mockRejectedValue(new Error('roster down'))
    renderLabel()
    // R3-QA-03: strict equality again — the WHOLE line is the id-derived
    // title, an em dash, and the unavailability marker, in that order. Two
    // separate substring checks would also pass a line that interleaved
    // other text between them.
    await waitFor(() => {
      expect(label()?.textContent).toBe('jim - Main Session — agent names unavailable')
    })
    expect(screen.queryByText(/Unknown agent/)).not.toBeInTheDocument()
  })

  it('when the agents list has loaded the line derives from the CURRENT display name', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([agentFixture({})])
    renderLabel()
    await waitFor(() => {
      expect(label()?.textContent).toBe('Jim - Main Session')
    })
  })

  it('a rename in the roster wins over the STALE persisted descriptor snapshot (S2-05: no title cached across a rename)', async () => {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    // The descriptor's title is the attach-time snapshot ("Jim - Main
    // Session") persisted in the browser; the agent was since renamed. The
    // snapshot must never win for a main.
    setDescriptor({ title: 'Jim - Main Session' })
    vi.mocked(fetchAgents).mockResolvedValue([agentFixture({ name: 'Jim Prime' })])
    const view = render(
      <QueryClientProvider client={qc}>
        <FeedKindLabel />
      </QueryClientProvider>,
    )
    await waitFor(() => {
      expect(view.container.querySelector('[data-testid="feed-kind-label"]')?.textContent).toBe('Jim Prime - Main Session')
    })
    expect(view.container.textContent).not.toContain('Jim - Main Session')
  })

  it('a FAILED roster with no agent id on the descriptor leaves the line absent (nothing honest to show)', async () => {
    setDescriptor({ agentId: null })
    vi.mocked(fetchAgents).mockRejectedValue(new Error('roster down'))
    const { qc } = renderLabel()
    // The absence is final (the query settled in error), not merely "in flight".
    await waitFor(() => {
      expect(qc.getQueryState(['agents'])?.status).toBe('error')
    })
    expect(label()).toBeNull()
    expect(screen.queryByText(/Unknown agent/)).not.toBeInTheDocument()
  })

  it('a NON-main descriptor with a title keeps its snapshot title (unchanged behaviour)', async () => {
    setDescriptor({ type: 'chat', title: 'Launch notes', agentId: null })
    vi.mocked(fetchAgents).mockRejectedValue(new Error('roster down'))
    renderLabel()
    await waitFor(() => {
      expect(label()?.textContent).toBe('Extra chat — Launch notes')
    })
  })
})

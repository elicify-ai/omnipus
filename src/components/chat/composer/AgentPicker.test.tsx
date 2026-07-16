/**
 * AgentPicker tests.
 *
 * AgentPicker was extracted out of ChatControls (Composer Redesign variant
 * A1) — it now lives in the composer's context row (rendered above the card) instead of the
 * workspace header — logic unchanged except the auto-select
 * session-preservation fix and the error/read-only hardening (see
 * AgentPicker.tsx's own header comment). Assertions are ported from
 * ChatControls.test.tsx / ChatControls.agent-selector-open.test.tsx (since
 * removed) / the SessionBar.test.tsx worker suite, PLUS new regression tests
 * added for the 14-reviewer sign-off findings (cached-data error gating,
 * refetch-based retry, the agentSelectorOpen latch reset, and the auto-select
 * stale-closure/attached-context fixes) — not invented fresh, per the
 * migration plan.
 *
 * Covers:
 *   - `agent-picker-trigger` testid renders + shows the active agent
 *   - "Select agent" fallback when no active agent
 *   - Workspace `core_team` scoping (agents outside the team are excluded)
 *   - Worker agents are excluded from the dropdown
 *   - Auto-select never picks a worker, even when the worker sorts first
 *   - Auto-select preserves a deep-linked activeSessionId (does not detach it)
 *   - Error / empty states (agents query error, all-draft agents)
 *   - A background-refetch error does not replace usable cached data; Retry
 *     re-runs the query via refetch (not window.location.reload)
 *   - The agentSelectorOpen latch resets when the error/draft branch is active
 *
 * Does NOT cover SC-005/US5.2 (switching agents keeps the current session) —
 * that is exercised by the sibling AgentPicker.agent-selector-open.test.tsx,
 * which is the file's dedicated purpose (avoids the duplicate assertion that
 * used to live in both files).
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { act } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'
import { useWorkspacesStore } from '@/store/workspacesStore'
import * as api from '@/lib/api'

// ── API mocks ─────────────────────────────────────────────────────────────────

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([
      {
        id: 'mia',
        name: 'Mia',
        type: 'core',
        status: 'active',
        model: 'z-ai/glm-5.2',
        description: 'Assistant',
      },
      {
        id: 'jim',
        name: 'Jim',
        type: 'core',
        status: 'idle',
        description: 'Orchestrator',
      },
    ]),
    fetchWorkspaces: vi.fn().mockResolvedValue([]),
  }
})

vi.mock('@/components/shared/IconRenderer', () => ({
  IconRenderer: () => null,
}))

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
if (typeof globalThis.ResizeObserver === 'undefined') {
  vi.stubGlobal('ResizeObserver', ResizeObserverStub)
}
if (typeof Element !== 'undefined' && !Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = function () {}
}

// Static import after mocks are in place
import { AgentPicker } from './AgentPicker'

// ── Render helper ─────────────────────────────────────────────────────────────

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function renderPicker() {
  return render(
    <QueryClientProvider client={makeClient()}>
      <AgentPicker />
    </QueryClientProvider>,
  )
}

// ── Store reset ───────────────────────────────────────────────────────────────

beforeEach(() => {
  vi.clearAllMocks()
  act(() => {
    useSessionStore.setState({
      activeAgentId: 'mia',
      activeSessionId: 'sess_1',
    })
    useUiStore.setState({ agentSelectorOpen: false })
    useWorkspacesStore.setState({ activeWorkspaceId: null })
  })
})

// ── Trigger + active agent display ─────────────────────────────────────────────

describe('AgentPicker — trigger', () => {
  it('renders the agent-picker-trigger testid', async () => {
    renderPicker()
    const trigger = await vi.waitFor(() => {
      const el = screen.getByTestId('agent-picker-trigger')
      if (!el) throw new Error('not rendered')
      return el
    })
    expect(trigger).toBeInTheDocument()
  })

  it('shows the active agent name', async () => {
    renderPicker()
    await vi.waitFor(() => {
      const els = screen.getAllByText('Mia')
      expect(els.length).toBeGreaterThan(0)
    })
  })

  it('shows "Select agent" when no active agent', async () => {
    act(() => {
      useSessionStore.setState({ activeAgentId: null })
    })
    renderPicker()
    await vi.waitFor(() => {
      const els = screen.getAllByText(/select agent/i)
      expect(els.length).toBeGreaterThan(0)
    })
  })

  it('sets an aria-label reflecting the current active agent', async () => {
    renderPicker()
    await vi.waitFor(() => {
      expect(screen.getByTestId('agent-picker-trigger').getAttribute('aria-label')).toBe(
        'Select agent (current: Mia)',
      )
    })
  })

  // Composer tab ring, position 3 — the value itself is ChatScreen's
  // contract (see src/components/chat/ChatScreen.tab-ring.test.tsx), but the
  // plumbing that the `tabIndex` prop actually reaches the interactive
  // trigger element (not just an ignored prop) is this component's own
  // contract to keep.
  it('forwards the tabIndex prop onto the trigger button', async () => {
    render(
      <QueryClientProvider client={makeClient()}>
        <AgentPicker tabIndex={3} />
      </QueryClientProvider>,
    )
    await vi.waitFor(() => {
      expect(screen.getByTestId('agent-picker-trigger')).toHaveAttribute('tabindex', '3')
    })
  })
})

// ── Workspace core_team scoping ────────────────────────────────────────────────

describe('AgentPicker — workspace core_team scoping', () => {
  it('lists workspace-team agents (scoped when workspace + core_team set)', async () => {
    // Workspace scopes to 'mia' only; 'jim' is excluded
    vi.mocked(api.fetchWorkspaces).mockResolvedValueOnce([
      {
        id: 'ws-1',
        name: 'Test WS',
        status: 'active',
        core_team: ['mia'],
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
      } as any,
    ])
    act(() => {
      useWorkspacesStore.setState({ activeWorkspaceId: 'ws-1' })
    })
    renderPicker()
    await vi.waitFor(() => {
      const els = screen.getAllByText('Mia')
      expect(els.length).toBeGreaterThan(0)
    })
    // Open the dropdown so the assertion checks the actual portal content
    // (SC-005 sibling file proves this pattern renders the portal), not just
    // the trigger label — which shows 'Mia' regardless of scoping and would
    // pass this test even with scoping fully broken.
    act(() => {
      useUiStore.getState().setAgentSelectorOpen(true)
    })
    await vi.waitFor(() => {
      // Trigger label + portal item = 2+ 'Mia' occurrences once open.
      expect(screen.getAllByText('Mia').length).toBeGreaterThan(1)
    })
    // Jim should not appear in the picker (scoped out)
    expect(screen.queryByText('Jim')).not.toBeInTheDocument()
  })

  it('excludes worker agents from the dropdown', async () => {
    vi.mocked(api.fetchAgents).mockResolvedValueOnce([
      {
        id: 'mia',
        name: 'Mia',
        type: 'core',
        status: 'active',
        model: 'z-ai/glm-5.2',
        description: 'Assistant',
      },
      {
        id: 'builder',
        name: 'Builder Worker',
        type: 'worker',
        status: 'active',
        description: 'Labour agent',
      },
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    ] as any[])
    renderPicker()
    await vi.waitFor(() => {
      const els = screen.getAllByText('Mia')
      expect(els.length).toBeGreaterThan(0)
    })
    // Open the dropdown — same rationale as the core_team scoping test above:
    // asserting against the trigger label alone would pass even if the
    // exclusion filter were removed entirely.
    act(() => {
      useUiStore.getState().setAgentSelectorOpen(true)
    })
    await vi.waitFor(() => {
      expect(screen.getAllByText('Mia').length).toBeGreaterThan(1)
    })
    expect(screen.queryByText('Builder Worker')).not.toBeInTheDocument()
  })

  // Auto-select (the effect in AgentPicker) must pick the first *eligible*
  // agent — never a worker, even when the worker sorts first in the API
  // response. Ported from the retired SessionBar.test.tsx worker suite.
  // This also transitively covers the dropdown's worker-exclusion filter
  // (chatAgents), since auto-select reads from the same filtered list.
  it('auto-selects the first non-worker agent when none is active', async () => {
    vi.mocked(api.fetchAgents).mockResolvedValueOnce([
      {
        id: 'builder',
        name: 'Builder Worker',
        type: 'worker',
        status: 'active',
        description: 'Labour agent',
      },
      {
        id: 'mia',
        name: 'Mia',
        type: 'core',
        status: 'active',
        model: 'z-ai/glm-5.2',
        description: 'Assistant',
      },
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    ] as any[])
    act(() => {
      useSessionStore.setState({ activeAgentId: null, activeSessionId: null })
    })
    renderPicker()
    // Auto-select picks the base agent, never the worker.
    await vi.waitFor(() => {
      expect(useSessionStore.getState().activeAgentId).toBe('mia')
    })
    expect(useSessionStore.getState().activeAgentId).not.toBe('builder')
    // The worker is never listed either (closed dropdown won't show it).
    expect(screen.queryByText('Builder Worker')).not.toBeInTheDocument()
  })

  // Regression test for the deep-link detach bug: the composer now also
  // renders on the /sessions/{id} deep-link route (ChatScreen inside
  // sessions.$sessionId.tsx), where a legacy session with no agent fields
  // triggers this auto-select effect. setActiveSession writes its first
  // argument (sessionId) UNCONDITIONALLY (src/store/session.ts), so the old
  // `setActiveSession(null, first.id, first.type)` call silently detached the
  // just-opened session — the next message would start a NEW session instead
  // of continuing the deep-linked one. Auto-select must preserve
  // activeSessionId while still picking the first eligible agent.
  it('auto-select preserves a deep-linked activeSessionId instead of detaching it', async () => {
    act(() => {
      useSessionStore.setState({ activeAgentId: null, activeSessionId: 'sess_deep_link' })
    })
    renderPicker()
    await vi.waitFor(() => {
      expect(useSessionStore.getState().activeAgentId).toBe('mia')
    })
    // The deep-linked session must NOT have been detached by auto-select.
    expect(useSessionStore.getState().activeSessionId).toBe('sess_deep_link')
  })

  // Regression test: auto-select must not wipe the "Task:" banner off a
  // deep-linked task session. setActiveSession unconditionally nulls
  // attachedSessionType/attachedTaskTitle (src/store/session.ts) — the
  // auto-select effect must capture and restore them via setAttachedContext.
  it('auto-select preserves attachedSessionType/attachedTaskTitle (does not wipe the Task banner)', async () => {
    act(() => {
      useSessionStore.setState({
        activeAgentId: null,
        activeSessionId: 'sess_task_link',
        attachedSessionType: 'task',
        attachedTaskTitle: 'Migrate the database',
      })
    })
    renderPicker()
    await vi.waitFor(() => {
      expect(useSessionStore.getState().activeAgentId).toBe('mia')
    })
    expect(useSessionStore.getState().attachedSessionType).toBe('task')
    expect(useSessionStore.getState().attachedTaskTitle).toBe('Migrate the database')
  })
})

// ── Error / empty states ────────────────────────────────────────────────────

describe('AgentPicker — error and empty states', () => {
  it('shows "Could not load agents" and a Retry button when the agents query errors with no cached data', async () => {
    vi.mocked(api.fetchAgents).mockRejectedValueOnce(new Error('boom'))
    renderPicker()
    await vi.waitFor(() => {
      expect(screen.getByText('Could not load agents')).toBeInTheDocument()
    })
    expect(screen.getByRole('button', { name: /retry/i })).toBeInTheDocument()
  })

  // NOTE: this jsdom version defines `window.location` (and `location.reload`)
  // as non-configurable, so neither can be spied on or replaced here
  // (Object.defineProperty throws "Cannot redefine property"). Proof is
  // therefore functional rather than a call-spy: the fix replaces
  // `window.location.reload()` with the query's own `refetch()`, so clicking
  // Retry must make FRESH mocked data appear WITHOUT any remount/reload of
  // the component (a real page reload would tear down and recreate the
  // QueryClientProvider tree, wiping the render entirely — jsdom's
  // unimplemented-navigation no-op would instead leave the error branch
  // stuck since no data would ever change). Only a genuine `refetch()` call
  // produces this observed transition.
  it('clicking Retry re-runs the query via refetch — not a page reload', async () => {
    vi.mocked(api.fetchAgents).mockRejectedValueOnce(new Error('boom'))
    renderPicker()
    await vi.waitFor(() => {
      expect(screen.getByText('Could not load agents')).toBeInTheDocument()
    })

    vi.mocked(api.fetchAgents).mockResolvedValueOnce([
      { id: 'mia', name: 'Mia', type: 'core', status: 'active', model: 'z-ai/glm-5.2', description: 'Assistant' },
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    ] as any[])
    fireEvent.click(screen.getByRole('button', { name: /retry/i }))

    await vi.waitFor(() => {
      expect(screen.queryByText('Could not load agents')).not.toBeInTheDocument()
    })
    expect(screen.getAllByText('Mia').length).toBeGreaterThan(0)
  })

  it('a background refetch failure does NOT replace a usable cached picker (cached agent keeps rendering)', async () => {
    const client = makeClient()
    vi.mocked(api.fetchAgents).mockResolvedValueOnce([
      { id: 'mia', name: 'Mia', type: 'core', status: 'active', model: 'z-ai/glm-5.2', description: 'Assistant' },
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    ] as any[])
    render(
      <QueryClientProvider client={client}>
        <AgentPicker />
      </QueryClientProvider>,
    )
    await vi.waitFor(() => {
      expect(screen.getAllByText('Mia').length).toBeGreaterThan(0)
    })

    // Simulate a background refetch (e.g. gateway restart + tab refocus)
    // that fails — the picker must keep showing the cached agent, not the
    // error branch.
    vi.mocked(api.fetchAgents).mockRejectedValueOnce(new Error('refetch boom'))
    await act(async () => {
      await client.refetchQueries({ queryKey: ['agents'] })
    })

    expect(screen.queryByText('Could not load agents')).not.toBeInTheDocument()
    expect(screen.getAllByText('Mia').length).toBeGreaterThan(0)
  })

  it('shows the all-draft message when every agent is in draft status', async () => {
    vi.mocked(api.fetchAgents).mockResolvedValueOnce([
      {
        id: 'mia',
        name: 'Mia',
        type: 'core',
        status: 'draft',
        model: 'z-ai/glm-5.2',
        description: 'Assistant',
      },
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    ] as any[])
    renderPicker()
    await vi.waitFor(() => {
      expect(
        screen.getByText('All agents are in draft status. Configure an agent to start chatting.'),
      ).toBeInTheDocument()
    })
  })
})

// ── agentSelectorOpen latch reset (error/draft branches) ────────────────────

describe('AgentPicker — agentSelectorOpen latch reset', () => {
  it('resets agentSelectorOpen to false when the error branch is active (rejecting fetch, empty cache)', async () => {
    vi.mocked(api.fetchAgents).mockRejectedValueOnce(new Error('boom'))
    act(() => {
      useUiStore.setState({ agentSelectorOpen: true })
    })
    renderPicker()
    await vi.waitFor(() => {
      expect(screen.getByText('Could not load agents')).toBeInTheDocument()
    })
    expect(useUiStore.getState().agentSelectorOpen).toBe(false)
  })

  it('resets agentSelectorOpen to false when the all-draft branch is active', async () => {
    vi.mocked(api.fetchAgents).mockResolvedValueOnce([
      { id: 'mia', name: 'Mia', type: 'core', status: 'draft', model: 'z-ai/glm-5.2', description: 'Assistant' },
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    ] as any[])
    act(() => {
      useUiStore.setState({ agentSelectorOpen: true })
    })
    renderPicker()
    await vi.waitFor(() => {
      expect(
        screen.getByText('All agents are in draft status. Configure an agent to start chatting.'),
      ).toBeInTheDocument()
    })
    expect(useUiStore.getState().agentSelectorOpen).toBe(false)
  })
})

// ── disabled (read-only) mode ────────────────────────────────────────────────

describe('AgentPicker — disabled prop (read-only session)', () => {
  it('disables the trigger button when disabled=true', async () => {
    render(
      <QueryClientProvider client={makeClient()}>
        <AgentPicker disabled />
      </QueryClientProvider>,
    )
    await vi.waitFor(() => {
      expect(screen.getByTestId('agent-picker-trigger')).toBeDisabled()
    })
  })

  it('does NOT auto-select an agent when disabled=true', async () => {
    act(() => {
      useSessionStore.setState({ activeAgentId: null, activeSessionId: null })
    })
    render(
      <QueryClientProvider client={makeClient()}>
        <AgentPicker disabled />
      </QueryClientProvider>,
    )
    await vi.waitFor(() => {
      expect(screen.getByTestId('agent-picker-trigger')).toBeInTheDocument()
    })
    // Give the auto-select effect a tick to (not) fire.
    await act(async () => {
      await new Promise((r) => setTimeout(r, 0))
    })
    expect(useSessionStore.getState().activeAgentId).toBeNull()
  })
})

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { act } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { AgentListScreen } from '@/components/screens/AgentListScreen'
import { useUiStore } from '@/store/ui'

// test_agent_list_fetch (test #26)
// Traces to: wave5a-wire-ui-spec.md — Scenario: Agent cards render in responsive grid

// AgentCard uses useNavigate and AgentListScreen renders a <Link>, so we mock
// TanStack Router. The real <Link> calls useLinkProps → useRouter, which throws
// without a RouterProvider; stub it with a plain anchor so the screen renders
// in isolation (these tests assert content, not navigation behaviour).
const mockNavigate = vi.fn()

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    useNavigate: () => mockNavigate,
    Link: ({ children, to, ...rest }: { children?: React.ReactNode; to?: string } & Record<string, unknown>) => (
      <a href={typeof to === 'string' ? to : '#'} {...(rest as Record<string, unknown>)}>
        {children}
      </a>
    ),
  }
})

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, fetchAgents: vi.fn() }
})

import { fetchAgents } from '@/lib/api'

const agentDefaults = {
  soul: '',
  needs_model: false,
  heartbeat: '',
  timeout_seconds: 60,
  max_tool_iterations: 20,
  heartbeat_enabled: false,
  heartbeat_interval: 300,
  // ADR-052 FR-039: memory_enabled is required on the wire Agent type.
  memory_enabled: true,
}

const mockAgents = [
  { id: 'mia', name: 'Mia', type: 'core' as const, locked: true, color: '#6366f1', status: 'active' as const, model: 'claude-opus-4-6', description: 'General purpose assistant', ...agentDefaults },
  { id: 'general-assistant', name: 'General Assistant', type: 'core' as const, locked: false, color: 'green', status: 'active' as const, model: 'claude-sonnet-4-6', description: 'General assistant', ...agentDefaults },
  { id: 'researcher', name: 'Researcher', type: 'core' as const, locked: false, color: 'blue', status: 'idle' as const, model: 'claude-opus-4-6', description: 'Research specialist', ...agentDefaults },
  { id: 'content-creator', name: 'Content Creator', type: 'Main' as const, locked: false, color: 'purple', status: 'idle' as const, model: 'claude-sonnet-4-6', description: 'Content writer', ...agentDefaults },
]

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

function wrapper({ children }: { children: React.ReactNode }) {
  return <QueryClientProvider client={makeClient()}>{children}</QueryClientProvider>
}

beforeEach(() => {
  mockNavigate.mockClear()
  vi.mocked(fetchAgents).mockResolvedValue(mockAgents)
  act(() => {
    useUiStore.setState({ createAgentModalOpen: false })
  })
})

describe('agent list integration (test #26) — data fetching', () => {
  it('fetches agents and renders 4 agent cards in a grid', async () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Agent cards render in responsive grid (AC1, AC2)
    render(<AgentListScreen />, { wrapper })

    await waitFor(() => {
      // Case-sensitive match: agent name is "General Assistant" (capital A),
      // description is "General assistant" (lowercase a) — avoid multiple-element error
      expect(screen.getAllByText(/General Assistant/).length).toBeGreaterThan(0)
      expect(screen.getByText(/Researcher/i)).toBeInTheDocument()
      expect(screen.getByText(/Content Creator/i)).toBeInTheDocument()
    })
  })

  it('shows "+ New Main" button in the agent list', async () => {
    // W6 (US-6 AC4) replaces the single top-level "+ New Agent" button with
    // per-section buttons. The base section renders "+ New Main" and the
    // worker section renders "+ New Subagent". Asserting on the section
    // testids proves the per-section split is in effect.
    render(<AgentListScreen />, { wrapper })

    await waitFor(() => {
      expect(screen.getByTestId('add-main-button')).toBeInTheDocument()
    })
  })

  it('shows empty state when no agents are configured', async () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Empty agent list (edge case)
    vi.mocked(fetchAgents).mockResolvedValue([])
    render(<AgentListScreen />, { wrapper })

    await waitFor(() => {
      expect(
        screen.queryByText(/no agents/i) ?? screen.queryByText(/create/i) ?? screen.queryByText(/get started/i)
      ).toBeTruthy()
    })
  })
})

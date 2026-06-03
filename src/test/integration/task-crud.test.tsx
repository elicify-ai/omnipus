import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { CommandCenterScreen } from '@/components/screens/CommandCenterScreen'

// test_task_crud (test #28)
// Traces to: wave5a-wire-ui-spec.md — Scenario: Task list renders with status grouping
//             wave5a-wire-ui-spec.md — Scenario: Status bar shows system overview

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchGatewayStatus: vi.fn(),
    fetchTasks: vi.fn(),
    createTask: vi.fn(),
    fetchAgents: vi.fn(),
    fetchActivity: vi.fn(),
  }
})

import { fetchGatewayStatus, fetchTasks, fetchAgents, fetchActivity } from '@/lib/api'

const mockStatus = {
  online: true,
  agent_count: 3,
  channel_count: 5,
  daily_cost: 4.82,
  version: '0.1.0',
}

const mockTasks = [
  {
    id: 't1',
    title: 'Refactor auth',
    prompt: 'Refactor the auth module',
    agent_id: 'general-assistant',
    agent_name: 'General Assistant',
    status: 'running' as const,
    priority: 2,
    trigger_type: 'manual' as const,
    created_at: '2026-03-29T09:00:00Z',
    started_at: '2026-03-29T10:00:00Z',
  },
  {
    id: 't2',
    title: 'Write docs',
    prompt: 'Write documentation for the API',
    agent_id: 'researcher',
    agent_name: 'Researcher',
    status: 'queued' as const,
    priority: 3,
    trigger_type: 'manual' as const,
    created_at: '2026-03-29T08:00:00Z',
  },
  {
    id: 't3',
    title: 'Deploy',
    prompt: 'Deploy to production',
    agent_id: 'general-assistant',
    agent_name: 'General Assistant',
    status: 'completed' as const,
    priority: 1,
    trigger_type: 'manual' as const,
    result: 'Deployed successfully',
    created_at: '2026-03-28T08:00:00Z',
    completed_at: '2026-03-29T08:00:00Z',
  },
]

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function wrapper({ children }: { children: React.ReactNode }) {
  return <QueryClientProvider client={makeClient()}>{children}</QueryClientProvider>
}

beforeEach(() => {
  vi.mocked(fetchGatewayStatus).mockResolvedValue(mockStatus)
  vi.mocked(fetchTasks).mockResolvedValue(mockTasks)
  vi.mocked(fetchAgents).mockResolvedValue([])
  vi.mocked(fetchActivity).mockResolvedValue([])
})

describe('command center integration (test #28)', () => {
  it('displays gateway online status from API', async () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Status bar shows system overview
    render(<CommandCenterScreen />, { wrapper })

    await waitFor(() => {
      expect(screen.getByText(/gateway online/i)).toBeInTheDocument()
    })
  })

  it('displays agent count and channel count from API', async () => {
    // Traces to: wave5a-wire-ui-spec.md — US-13 AC1: agent count, channel count
    render(<CommandCenterScreen />, { wrapper })

    await waitFor(() => {
      expect(screen.getByText(/3 agents/i)).toBeInTheDocument()
      expect(screen.getByText(/5 channels/i)).toBeInTheDocument()
    })
  })

  it('renders task list with tasks from API', async () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Task list renders with status grouping
    render(<CommandCenterScreen />, { wrapper })

    await waitFor(() => {
      expect(screen.getByText(/Refactor auth/i)).toBeInTheDocument()
      expect(screen.getByText(/Write docs/i)).toBeInTheDocument()
    })
  })

  it('shows "Gateway offline" when API returns online: false', async () => {
    // Dataset: Status Bar row 2 — gateway offline
    vi.mocked(fetchGatewayStatus).mockResolvedValue({ ...mockStatus, online: false })
    render(<CommandCenterScreen />, { wrapper })

    await waitFor(() => {
      expect(screen.getByText(/gateway offline/i)).toBeInTheDocument()
    })
  })
})

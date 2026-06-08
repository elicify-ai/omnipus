import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { act } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { NewProjectSlideOver } from './NewProjectSlideOver'
import { useUiStore } from '@/store/ui'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn(),
    createProject: vi.fn(),
  }
})

import { fetchAgents } from '@/lib/api'

// Traces to: project-task-management-level1-spec.md — core_team graceful degradation (F7-02 follow-on)
// When the agents fetch fails, the slide-over must degrade gracefully and surface
// a "Could not load agents" message with a manual agent-ID entry fallback, rather
// than crashing or silently dropping the core-team selector.

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

function renderSlideOver() {
  return render(
    <QueryClientProvider client={makeClient()}>
      <NewProjectSlideOver open onOpenChange={vi.fn()} />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  act(() => {
    useUiStore.setState({ toasts: [] })
  })
})

describe('NewProjectSlideOver — core_team graceful degradation', () => {
  it('shows error message when agent fetch fails', async () => {
    // Given: the backend agent list endpoint is unavailable
    vi.mocked(fetchAgents).mockRejectedValue(new Error('network down'))

    // When: the slide-over renders and the agents query settles into an error state
    renderSlideOver()

    // Then: a graceful "Could not load agents" message is shown so the operator can
    // still enter core-team agent IDs manually.
    expect(
      await screen.findByText(/Could not load agents/i),
    ).toBeInTheDocument()
  })
})

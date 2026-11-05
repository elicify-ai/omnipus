// AgentProfile.closedNoPerformanceFetch.test.tsx — the shell-hosted editor
// must not read the admin-only GET /api/v1/performance while it is closed.
//
// Why: AppShell mounts the ONE store-driven AgentProfile on every page
// (cc637a733, "single editor host"). The profile read the global
// tool-iteration limit unconditionally, so every page load fetched
// /api/v1/performance — an adminWrap route that answers 503 under
// dev_mode_bypass (pkg/gateway/middleware/bypass_gate.go::RequireNotBypass).
// Release run 39029558027 recorded exactly that: four 503s (the first try
// plus the query retries) as console errors in subagent.spec.ts (d).
//
// Oracle: a closed profile makes ZERO performance requests; opening it
// (store-driven, exactly as the sidebar/Team tab/Agents screen do) makes
// exactly one.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, act, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { AgentProfile } from './AgentProfile'
import type { Agent, PerformanceSettings } from '@/lib/api'

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
if (typeof Element !== 'undefined' && !Element.prototype.hasPointerCapture) {
  Element.prototype.hasPointerCapture = () => false
}

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return { ...actual, useNavigate: () => vi.fn(), useParams: () => ({}) }
})

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgent: vi.fn(),
    fetchWorkspace: vi.fn(),
    fetchSkills: vi.fn(),
    fetchProviders: vi.fn(),
    fetchActivity: vi.fn(),
    fetchExecutorPreview: vi.fn(),
    fetchPerformanceSettings: vi.fn(),
  }
})

import { fetchAgent, fetchSkills, fetchProviders, fetchActivity, fetchExecutorPreview, fetchPerformanceSettings } from '@/lib/api'
import { useUiStore } from '@/store/ui'

const agent: Agent = {
  revision: '0'.repeat(64),
  editable_fields: [{ name: 'max_tool_iterations', editable: true }],
  id: 'triage',
  name: 'Triage',
  type: 'Main',
  locked: false,
  needs_model: false,
  status: 'active',
  model: 'claude-sonnet-4-6',
  description: 'Sorts things',
  soul: '',
  max_tool_iterations: 200,
  max_tool_iterations_source: 'global',
  max_tool_iterations_override_ignored: false,
  memory_enabled: true,
  figure: 'Omnipus',
  role: 'general',
}

function renderShellHostedProfile() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <AgentProfile />
    </QueryClientProvider>,
  )
}

// Let every effect run and every query that is going to start, start: a
// mounted, enabled query calls its queryFn on mount, so after this a zero
// call count is a real absence, not a not-yet.
async function settle() {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 50))
  })
}

beforeEach(() => {
  vi.mocked(fetchAgent).mockReset().mockResolvedValue(agent)
  vi.mocked(fetchSkills).mockReset().mockResolvedValue([])
  vi.mocked(fetchProviders).mockReset().mockResolvedValue([])
  vi.mocked(fetchActivity).mockReset().mockResolvedValue({ events: [] })
  vi.mocked(fetchExecutorPreview).mockReset().mockReturnValue(new Promise(() => {}))
  vi.mocked(fetchPerformanceSettings)
    .mockReset()
    .mockResolvedValue({ max_tool_iterations: 200, max_tool_iterations_saved_state: 'ok' } as PerformanceSettings)
  useUiStore.setState({ editAgentId: null, editAgentWorkspaceId: null, toasts: [] })
})

describe('AgentProfile hosted by the shell — no admin read while closed', () => {
  it('a closed profile issues no GET /api/v1/performance', async () => {
    renderShellHostedProfile()
    await settle()
    // Instrument check: the closed profile really mounted and ran its
    // unconditional queries, so the absence below is meaningful.
    expect(vi.mocked(fetchProviders)).toHaveBeenCalled()
    expect(vi.mocked(fetchPerformanceSettings)).not.toHaveBeenCalled()
  })

  it('opening the profile reads the global limit exactly once', async () => {
    renderShellHostedProfile()
    await settle()
    act(() => {
      useUiStore.getState().openEditAgentSlideOver(agent.id)
    })
    await waitFor(() => expect(vi.mocked(fetchPerformanceSettings)).toHaveBeenCalledTimes(1))
    await settle()
    expect(vi.mocked(fetchPerformanceSettings)).toHaveBeenCalledTimes(1)
  })
})

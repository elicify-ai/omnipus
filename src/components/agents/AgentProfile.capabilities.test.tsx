import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { AgentProfile } from './AgentProfile'
import type { Agent } from '@/lib/api'
import { useUiStore } from '@/store/ui'

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
if (typeof globalThis.ResizeObserver === 'undefined') vi.stubGlobal('ResizeObserver', ResizeObserverStub)
if (typeof Element !== 'undefined' && !Element.prototype.scrollIntoView) Element.prototype.scrollIntoView = function () {}
if (typeof Element !== 'undefined' && !Element.prototype.hasPointerCapture) Element.prototype.hasPointerCapture = () => false

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
    updateAgent: vi.fn(),
    updateWorkspace: vi.fn(),
    fetchRegistryTools: vi.fn(),
    fetchSkills: vi.fn(),
    fetchProviders: vi.fn(),
    testAgentRunner: vi.fn(),
    fetchAgentTools: vi.fn(),
    fetchGlobalToolPolicies: vi.fn(),
  }
})

import {
  fetchAgent,
  fetchAgentTools,
  fetchGlobalToolPolicies,
  fetchProviders,
  fetchRegistryTools,
  fetchSkills,
} from '@/lib/api'

const lockedAgent: Agent = {
  revision: '0'.repeat(64),
  id: 'mia',
  name: 'Mia',
  type: 'Main',
  locked: true,
  needs_model: false,
  status: 'active',
  model: 'claude-sonnet-4-6',
  description: 'General purpose assistant',
  soul: '',
  timeout_seconds: 60,
  max_tool_iterations: 20,
  memory_enabled: true,
  editable_fields: [],
}

function renderProfile() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<QueryClientProvider client={client}><AgentProfile agentId="mia" /></QueryClientProvider>)
}

function switchTab(testId: string) {
  const trigger = screen.getByTestId(testId)
  trigger.focus()
  fireEvent.keyDown(trigger, { key: 'Enter' })
  fireEvent.click(trigger)
}

beforeEach(() => {
  vi.mocked(fetchAgent).mockReset()
  vi.mocked(fetchRegistryTools).mockReset().mockResolvedValue([])
  vi.mocked(fetchAgentTools).mockReset().mockResolvedValue({
    revision: '0'.repeat(64),
    override_names: [],
    config: { builtin: { policies: {} }, mcp: { servers: [] } },
    tools: [],
  })
  vi.mocked(fetchGlobalToolPolicies).mockReset().mockResolvedValue({ policies: {} })
  vi.mocked(fetchSkills).mockReset().mockResolvedValue([])
  vi.mocked(fetchProviders).mockReset().mockResolvedValue([])
  useUiStore.setState({ editAgentId: 'mia', toasts: [] })
})

describe('AgentProfile — ADR090 tool policy descriptor', () => {
  it.each([
    { label: 'allows the declared capability', fields: [{ name: 'tool_policy_changes', editable: true }], allowed: true },
    { label: 'honors an explicit refusal', fields: [{ name: 'tool_policy_changes', editable: false }], allowed: false },
    { label: 'does not infer missing capability', fields: [], allowed: false },
    { label: 'does not substitute the storage field name', fields: [{ name: 'tools_cfg', editable: true }], allowed: false },
  ])('$label', async ({ fields, allowed }) => {
    vi.mocked(fetchRegistryTools).mockResolvedValue([])
    vi.mocked(fetchAgent).mockResolvedValue({ ...lockedAgent, editable_fields: fields })
    renderProfile()
    await screen.findByText('Mia')
    switchTab('tab-tools')
    for (const name of ['Cautious', 'Balanced', 'Full access']) {
      const button = await screen.findByRole('button', { name })
      expect(button).toHaveProperty('disabled', !allowed)
    }
    const warning = screen.queryByText('Tool policies are read-only for this agent: the backend marks this capability as fixed.')
    if (allowed) expect(warning).toBeNull()
    else expect(warning).toBeInTheDocument()
  })
})

import { beforeEach, describe, expect, it, vi } from 'vitest'
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { AgentProfile } from './AgentProfile'
import type { Agent, RegistryTool } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { useSkillPermissionNetwork } from '../../../tests/helpers/skill-permission-network'

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
    fetchGodMode: vi.fn(),
  }
})

import {
  fetchAgent,
  fetchAgentTools,
  fetchGodMode,
  fetchProviders,
  fetchRegistryTools,
  fetchSkills,
} from '@/lib/api'

// The global value the tab's badges are floored by: read_file Allow, so all
// three of its badges are open to an agent that may edit. The real client reads
// it from GET /security/permissions (C-22 row 6 retires GET
// /security/tool-policies); only the fetch edge is doubled.
const permissions = useSkillPermissionNetwork(() => ({
  policies: { read_file: 'allow' },
  skill_policies: {}, visibility: {}, skill_visibility: {}, skill_trust: 'ask',
}))

const readFile: RegistryTool = {
  name: 'read_file',
  scope: 'general',
  category: 'filesystem',
  description: 'Read file contents',
  source: 'builtin',
  visibility: 'full',
  visibility_locked: false,
  permission_fixed: false,
}

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
  max_tool_iterations: 20,
  max_tool_iterations_source: 'global',
  max_tool_iterations_override_ignored: false,
  memory_enabled: true,
  figure: 'Omnipus',
  role: 'general',
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
    skills: [],
  })
  vi.mocked(fetchGodMode).mockReset().mockResolvedValue({ enabled: false, persisted: false, available: true, supported: true })
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
    vi.mocked(fetchRegistryTools).mockResolvedValue([readFile])
    vi.mocked(fetchAgent).mockResolvedValue({ ...lockedAgent, editable_fields: fields })
    renderProfile()
    await screen.findByText('Mia')
    switchTab('tab-tools')
    // The control this case used to probe (the Cautious / Balanced / Full
    // access buttons) is retired with the role presets (US-3 AS1, T-Q6 A;
    // CHECK round 4, candidate 9). The descriptor oracle is unchanged: the
    // backend's tool_policy_changes capability decides whether the tab's
    // controls are open. The probe is now one tool row's three badges.
    const grid = await screen.findByTestId('category-grid')
    fireEvent.click(within(grid).getByRole('button', { name: /files/i }))
    const row = await screen.findByTestId('tool-row-read_file')
    // Both reads have landed before the disabled state is judged, so a
    // refusal is the descriptor's and not a tab that has not loaded yet.
    await waitFor(() => expect(fetchAgentTools).toHaveBeenCalled())
    await waitFor(() => expect(permissions().seen.some(({ method }) => method === 'GET')).toBe(true))
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)) })
    for (const name of ['Allow', 'Ask', 'Deny']) {
      const badge = within(row).getByRole('button', { name })
      await waitFor(() => expect(badge).toHaveProperty('disabled', !allowed))
    }
    const warning = screen.queryByText('Tool policies are read-only for this agent: the backend marks this capability as fixed.')
    if (allowed) expect(warning).toBeNull()
    else expect(warning).toBeInTheDocument()
  })
})

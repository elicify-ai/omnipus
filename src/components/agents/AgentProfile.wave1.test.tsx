// BDD-06.2. A built-in agent's figure, role and colour are visible and locked.
// The legacy icon slug stays on screen. There is no upload control.
// Other fields the server already marks editable (model) stay editable.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { AgentProfile } from './AgentProfile'
import type { Agent } from '@/lib/api'

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
    updateAgent: vi.fn(),
    updateWorkspace: vi.fn(),
    deleteAgent: vi.fn(),
    fetchSkills: vi.fn(),
    fetchProviders: vi.fn(),
    testAgentRunner: vi.fn(),
  }
})

import { fetchAgent, fetchSkills, fetchProviders } from '@/lib/api'

const locked = (name: string) => ({ name, editable: false, reason: 'Built-in identity is fixed.' })
const open = (name: string) => ({ name, editable: true })

const mia = {
  revision: '0'.repeat(64),
  id: 'mia',
  name: 'Mia',
  type: 'core',
  locked: true,
  needs_model: false,
  status: 'active',
  model: 'claude-opus-4-6',
  description: 'Core agent',
  soul: '',
  timeout_seconds: 60,
  max_tool_iterations: 20,
  max_tool_iterations_source: 'global',
  max_tool_iterations_override_ignored: false,
  memory_enabled: true,
  color: '#3B82F6',
  icon: 'lightbulb',
  figure: 'Omnipus',
  role: 'general',
  editable_fields: [
    locked('name'),
    locked('description'),
    locked('color'),
    locked('icon'),
    locked('figure'),
    locked('role'),
    locked('soul'),
    locked('executor'),
    open('model'),
    open('provider'),
  ],
} as Agent

beforeEach(() => {
  vi.mocked(fetchAgent).mockReset().mockResolvedValue(mia)
  vi.mocked(fetchSkills).mockReset().mockResolvedValue([])
  vi.mocked(fetchProviders).mockReset().mockResolvedValue([])
})

describe('built-in identity stays locked', () => {
  it('shows the figure, role and colour as locked choices and keeps the icon slug', async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={client}>
        <AgentProfile agentId="mia" />
      </QueryClientProvider>,
    )
    await screen.findByText('Mia')
    const figure = await screen.findByRole('button', { name: 'Omnipus' })
    expect(figure).toBeDisabled()
    const role = screen.getByRole('button', { name: 'General assistant' })
    expect(role).toBeDisabled()
    const colour = screen.getByRole('button', { name: 'Azure' })
    expect(colour).toBeDisabled()
    expect(screen.getByText('lightbulb')).toBeInTheDocument()
    expect(document.querySelector('input[type="file"]')).toBeNull()
  })
})

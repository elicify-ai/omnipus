// BDD-06.2. A built-in agent's figure, role and colour are visible and locked.
// The legacy icon slug stays on screen. There is no upload control.
// Other fields the server already marks editable (model) stay editable.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { AgentProfile } from './AgentProfile'
import type { Agent } from '@/lib/api'
import { AZURE } from '@/test/agentPalette'

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
  color: AZURE,
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

// Tailwind `sm` breakpoint. The desktop tab strip is `hidden sm:block`;
// the phone accordion is `block sm:hidden`.
const SM_PX = 640

function visibleIdentityLayout(): HTMLElement {
  // The profile slide-over portals out of the render container, so the
  // layout lives on document.body. Desktop is `hidden sm:block`; phone is
  // `block sm:hidden`. Tailwind `sm` is 640px.
  const root = document.body
  const desktop = root.querySelector('.hidden.sm\\:block')
  const phone = root.querySelector('.block.sm\\:hidden')
  const node = window.innerWidth >= SM_PX ? desktop : phone
  if (!(node instanceof HTMLElement)) {
    const tab = Boolean(root.querySelector('[data-testid="tab-basics"]'))
    const accordion = Boolean(root.querySelector('[data-testid="accordion-basics"]'))
    throw new Error(`visible identity layout is not mounted; tab=${tab} accordion=${accordion}`)
  }
  return node
}

describe('built-in identity stays locked', () => {
  it('shows the figure, role and colour as locked choices and keeps the icon slug', async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={client}>
        <AgentProfile agentId="mia" />
      </QueryClientProvider>,
    )
    await screen.findByText('Mia')
    // Desktop tabs (`hidden sm:block`) and the phone accordion (`block sm:hidden`)
    // both mount the same form. jsdom does not apply Tailwind, so both copies
    // are in the accessibility tree. Scope to the layout that is visible at
    // the current viewport (Tailwind `sm` is 640px) instead of getByRole,
    // which throws when it sees two Omnipus buttons. The assertions are the
    // same: that copy's figure, role and colour are disabled.
    const layout = visibleIdentityLayout()
    const figure = await within(layout).findByRole('button', { name: 'Omnipus' })
    expect(figure).toBeDisabled()
    const role = within(layout).getByRole('button', { name: 'General assistant' })
    expect(role).toBeDisabled()
    const colour = within(layout).getByRole('button', { name: 'Azure' })
    expect(colour).toBeDisabled()
    expect(within(layout).getByText('lightbulb')).toBeInTheDocument()
    expect(document.querySelector('input[type="file"]')).toBeNull()
  })
})

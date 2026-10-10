// BDD-06.2. A built-in agent's figure, role and colour are visible and locked.
// W1-6 / FR-020 replaces the legacy icon picker with figure/role/colour.
// The stored legacy slug is still shown once above both identity layouts.
// Other fields the server already marks editable (model) stay editable.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, within, fireEvent, waitFor, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { AgentProfile } from './AgentProfile'
import type { Agent, AgentUpdateRequest } from '@/lib/api'
import { AgentColor } from '@/lib/api/generated/schemas'

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

import { fetchAgent, fetchSkills, fetchProviders, updateAgent } from '@/lib/api'

const locked = (name: string) => ({ name, editable: false, reason: 'Built-in identity is fixed.' })
const open = (name: string) => ({ name, editable: true })

// The colour field has to be written inside a named function. A module-level
// object, and an anonymous callback, both leave the palette read unsupported.
function lockedMia(): Agent {
  return {
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
    color: AgentColor.options[0],
    figure: 'Omnipus',
    role: 'general',
    editable_fields: [
      locked('name'),
      locked('description'),
      locked('color'),
      locked('figure'),
      locked('role'),
      locked('soul'),
      locked('executor'),
      open('model'),
      open('provider'),
    ],
  } as Agent
}

beforeEach(() => {
  vi.mocked(fetchAgent).mockReset().mockResolvedValue(lockedMia())
  vi.mocked(fetchSkills).mockReset().mockResolvedValue([])
  vi.mocked(fetchProviders).mockReset().mockResolvedValue([])
  vi.mocked(updateAgent).mockReset().mockResolvedValue(lockedMia())
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

function renderProfile(agentId: string) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(<QueryClientProvider client={client}><AgentProfile agentId={agentId} /></QueryClientProvider>)
}

function headerMark(name: string): HTMLElement {
  const header = screen.getByRole('heading', { name }).parentElement
  if (!header) throw new Error('Profile heading has no header container')
  return within(header).getByTestId('agent-icon')
}

// W1-6 / FR-020 / BDD-06.1: the edit header previews draft figure, role,
// and ink immediately; previewing is not evidence that persistence succeeded.
describe('edit header live identity preview', () => {
  it('renders the saved built-in figure, role and colour in the header', async () => {
    renderProfile('mia')
    await screen.findByRole('heading', { name: 'Mia' })
    await waitFor(() => expect(headerMark('Mia')).toHaveAttribute('data-figure', 'Omnipus'))
    expect(headerMark('Mia').querySelector('[data-role]')).toHaveAttribute('data-role', 'general')
    expect(headerMark('Mia')).toHaveStyle({ color: '#3B82F6' })
    expect(updateAgent).not.toHaveBeenCalled()
  })

  it('previews the selected figure, role and colour before the save completes', async () => {
    const custom: Agent = {
      ...lockedMia(), id: 'custom', name: 'Custom Mia', type: 'Main', locked: false,
      editable_fields: [open('figure'), open('role'), open('color')],
    }
    vi.mocked(fetchAgent).mockResolvedValue(custom)
    // Process-edge mock: the server never confirms this draft during the test.
    vi.mocked(updateAgent).mockImplementation(() => new Promise<Agent>(() => {}))
    renderProfile('custom')
    await screen.findByRole('heading', { name: 'Custom Mia' })
    const layout = visibleIdentityLayout()
    fireEvent.click(within(layout).getByRole('button', { name: 'Woman' }))
    // The role is ONE searchable dropdown (founder 2026-10-10): open it and
    // pick the Writer option; the header mark must preview it before any save.
    fireEvent.click(within(layout).getByRole('combobox', { name: 'Role badge' }))
    fireEvent.click(await screen.findByRole('option', { name: 'Writer' }))
    fireEvent.click(within(layout).getByRole('button', { name: 'Violet' }))
    const preview = headerMark('Custom Mia')
    expect(preview).toHaveAttribute('data-figure', 'Woman')
    expect(preview.querySelector('[data-role]')).toHaveAttribute('data-role', 'writer')
    expect(preview).toHaveStyle({ color: '#A78BFA' })
    expect(within(layout).getByRole('button', { name: 'Woman' })).toHaveAttribute('aria-pressed', 'true')
    expect(within(layout).getByRole('combobox', { name: 'Role badge' })).toHaveTextContent('Writer')
    expect(within(layout).getByRole('button', { name: 'Violet' })).toHaveAttribute('aria-pressed', 'true')
    expect(updateAgent).not.toHaveBeenCalled()
  })
})

describe('successful custom identity edit', () => {
  it('autosaves the exact identity patch at the reviewed revision and reloads the confirmed identity (the stored legacy icon is never sent and never displayed)', async () => {
    // FR-020 / ARCH decision 1.1: wire figure/role/palette values, not art
    // keys or display names. The stored legacy icon is not part of this edit.
    const reviewedRevision = 'a'.repeat(64)
    let stored: Agent = {
      ...lockedMia(), id: 'custom-save', name: 'Saved identity', type: 'Main', locked: false,
      revision: reviewedRevision, updated_at: '2026-10-09T09:00:00Z',
      editable_fields: [open('figure'), open('role'), open('color')],
    }
    vi.mocked(fetchAgent).mockImplementation(async () => structuredClone(stored))
    // Process-edge fake: persist only the values actually supplied in the
    // request. Returning a preselected identity would hide a dropped field.
    vi.mocked(updateAgent).mockImplementation(async (_id, patch) => {
      const identityPatch: Pick<AgentUpdateRequest, 'figure' | 'role' | 'color'> = patch
      stored = {
        ...stored, ...identityPatch,
        revision: 'b'.repeat(64), updated_at: '2026-10-09T09:00:01Z',
      }
      return structuredClone(stored)
    })
    const profile = renderProfile('custom-save')
    await screen.findByRole('heading', { name: 'Saved identity' })
    const layout = visibleIdentityLayout()
    expect(within(layout).getByRole('button', { name: 'Omnipus' })).toHaveAttribute('aria-pressed', 'true')
    expect(updateAgent).not.toHaveBeenCalled()
    try {
      vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
      fireEvent.click(within(layout).getByRole('button', { name: 'Woman' }))
      // The role is ONE searchable dropdown (founder 2026-10-10).
      fireEvent.click(within(layout).getByRole('combobox', { name: 'Role badge' }))
      fireEvent.click(screen.getByRole('option', { name: 'Writer' }))
      fireEvent.click(within(layout).getByRole('button', { name: 'Violet' }))
      // Run the profile's real 1500ms debounce, not saveNow or a mocked hook.
      await act(async () => { await vi.advanceTimersByTimeAsync(1500) })
      expect(updateAgent).toHaveBeenCalledTimes(1)
      expect(updateAgent).toHaveBeenCalledWith('custom-save', {
        revision: reviewedRevision, figure: 'Woman', role: 'writer', color: '#A78BFA',
      })
      // The deleted legacy field must not ride onto the wire with the patch.
      expect(vi.mocked(updateAgent).mock.calls[0][1]).not.toHaveProperty('icon')
      expect(screen.getByText('Saved just now')).toBeInTheDocument()
      expect(stored.revision).toBe('b'.repeat(64))
      // FOUNDER DECISION 2026-10-10 + core contract: Agent.icon no longer exists,
      // so there is no stored legacy slug left to preserve (the no-icon wire check above stays).
      profile.unmount()
      vi.useRealTimers()

      // A new provider creates a fresh query cache. The identity must come
      // from a new server read, not the previous header's draft or cache.
      vi.mocked(fetchAgent).mockClear()
      renderProfile('custom-save')
      await screen.findByRole('heading', { name: 'Saved identity' })
      await waitFor(() => expect(headerMark('Saved identity')).toHaveAttribute('data-figure', 'Woman'))
      expect(fetchAgent).toHaveBeenCalledTimes(1)
      expect(fetchAgent).toHaveBeenCalledWith('custom-save')
      expect(headerMark('Saved identity').querySelector('[data-role]')).toHaveAttribute('data-role', 'writer')
      expect(headerMark('Saved identity')).toHaveStyle({ color: '#A78BFA' })
      const reloadedLayout = visibleIdentityLayout()
      expect(within(reloadedLayout).getByRole('button', { name: 'Woman' })).toHaveAttribute('aria-pressed', 'true')
      expect(within(reloadedLayout).getByRole('combobox', { name: 'Role badge' })).toHaveTextContent('Writer')
      expect(within(reloadedLayout).getByRole('button', { name: 'Violet' })).toHaveAttribute('aria-pressed', 'true')
      // FOUNDER DECISION 2026-10-10 (avatar consistency): the stored legacy
      // slug is no longer displayed anywhere; nothing about Agent.icon is UI.
      vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
      await act(async () => { await vi.advanceTimersByTimeAsync(1500) })
      expect(updateAgent).toHaveBeenCalledTimes(1)
    } finally {
      vi.useRealTimers()
    }
  })
})

describe('built-in identity stays locked', () => {
  it('shows selected figure, role and colour as locked choices, and never an icon control', async () => {
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
    // The role is ONE searchable dropdown (founder 2026-10-10), disabled for
    // a built-in and showing the stored role's label.
    const role = within(layout).getByRole('combobox', { name: 'Role badge' })
    expect(role).toBeDisabled()
    expect(role).toHaveTextContent('General assistant')
    const colour = within(layout).getByRole('button', { name: 'Azure' })
    expect(colour).toBeDisabled()
    expect(figure).toHaveAttribute('aria-pressed', 'true')
    expect(colour).toHaveAttribute('aria-pressed', 'true')
    // FOUNDER DECISION 2026-10-10 (avatar consistency): the stored legacy
    // slug ("lightbulb") is no longer displayed anywhere — the old
    // "kept the stored icon slug" assertion asserted deleted UI and is gone.
    expect(within(layout).queryByLabelText('Icon')).toBeNull()
    expect(document.querySelector('input[type="file"]')).toBeNull()
  })
})

// The 5th figure, Monogram, in the slide-over header. The header mark draws the
// agent's name initial. ARCH-RULING-monogram AC-15 / D3.
describe('Monogram identity header', () => {
  it('shows the Monogram initial beside the title (AC-15)', async () => {
    const monogram: Agent = {
      ...lockedMia(),
      id: 'monogram-agent',
      name: 'Daniel',
      // Monogram joins AgentFigure when the contract regenerates (AC-17); the
      // cast is only here because the generated union still predates it.
      figure: 'Monogram' as unknown as Agent['figure'],
    }
    vi.mocked(fetchAgent).mockResolvedValue(monogram)
    renderProfile('monogram-agent')
    await screen.findByRole('heading', { name: 'Daniel' })
    const mark = headerMark('Daniel')
    await waitFor(() => expect(mark).toHaveAttribute('data-figure', 'Monogram'))
    const letter = mark.querySelector('[data-initial]')
    expect(letter, 'header letter node').not.toBeNull()
    // Attribute AND painted text (F-1): a wrong painted glyph must fail RED.
    expect(letter!.tagName.toLowerCase(), 'header letter is an SVG <text>').toBe('text')
    expect(letter!.getAttribute('data-initial')).toBe('D')
    expect(letter!.textContent, 'painted header initial').toBe('D')
  })
})

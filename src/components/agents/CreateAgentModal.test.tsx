import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { act } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { CreateAgentModal } from './CreateAgentModal'
import { useUiStore } from '@/store/ui'
import type { Skill } from '@/lib/api'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchProviders: vi.fn().mockResolvedValue([]),
    fetchRegistryTools: vi.fn().mockResolvedValue([]),
    fetchSkills: vi.fn().mockResolvedValue([]),
    createAgent: vi.fn(),
  }
})

import { fetchSkills } from '@/lib/api'

// test_create_agent_modal (test #14)
// Traces to: wave5a-wire-ui-spec.md — Scenario: Creating a custom agent
//             wave5a-wire-ui-spec.md — Scenario: Create agent form validation

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

function renderModal(props: { open?: boolean; onClose?: () => void; onCreate?: (data: any) => Promise<void> } = {}) {
  return render(
    <QueryClientProvider client={makeClient()}>
      <CreateAgentModal {...props} />
    </QueryClientProvider>
  )
}

beforeEach(() => {
  act(() => {
    useUiStore.setState({
      createAgentModalOpen: false,
      sessionPanelOpen: false,
      toasts: [],
    })
  })
})

describe('CreateAgentModal — rendering (test #14)', () => {
  it('shows Create Agent dialog when open via prop', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Creating a custom agent (AC1)
    renderModal({ open: true, onClose: vi.fn() })
    // Use heading role to distinguish the dialog title h2 from the "Create Agent" submit button
    expect(screen.getByRole('heading', { name: /create agent/i })).toBeInTheDocument()
  })

  it('shows Create Agent dialog when createAgentModalOpen is true in store', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Creating a custom agent (AC1)
    act(() => { useUiStore.setState({ createAgentModalOpen: true }) })
    renderModal()
    // Use heading role to distinguish the dialog title h2 from the "Create Agent" submit button
    expect(screen.getByRole('heading', { name: /create agent/i })).toBeInTheDocument()
  })

  it('does not render dialog content when closed', () => {
    // Traces to: wave5a-wire-ui-spec.md — AC2: modal hidden by default
    renderModal({ open: false, onClose: vi.fn() })
    expect(screen.queryByText('Create Agent')).toBeNull()
  })

  it('renders name input, description textarea, and model select when open', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Create agent form fields
    renderModal({ open: true, onClose: vi.fn() })
    expect(screen.getByLabelText(/name/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/description/i)).toBeInTheDocument()
    // ModelSelector renders as an <input> with placeholder when no models are available.
    // "Use provider default" is the placeholder attribute, not a text node — use getByPlaceholderText.
    expect(screen.getByPlaceholderText(/use provider default/i)).toBeInTheDocument()
  })
})

describe('CreateAgentModal — form validation (test #14)', () => {
  it('shows "Name is required" error when Create Agent is clicked without a name', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Create agent form validation (AC1)
    renderModal({ open: true, onClose: vi.fn() })
    fireEvent.click(screen.getByRole('button', { name: /create agent/i }))
    expect(screen.getByText(/name is required/i)).toBeInTheDocument()
  })

  it('clears name error once user types a valid name', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Create agent form validation (AC2)
    renderModal({ open: true, onClose: vi.fn() })
    fireEvent.click(screen.getByRole('button', { name: /create agent/i }))
    expect(screen.getByText(/name is required/i)).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText(/name/i), { target: { value: 'Research Bot' } })
    expect(screen.queryByText(/name is required/i)).toBeNull()
  })
})

describe('CreateAgentModal — actions (test #14)', () => {
  it('calls onCreate with agent data when form is valid and submitted', async () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Creating a custom agent (AC3)
    const onCreate = vi.fn().mockResolvedValue(undefined)
    const onClose = vi.fn()
    renderModal({ open: true, onClose, onCreate })

    fireEvent.change(screen.getByLabelText(/name/i), { target: { value: 'Research Bot' } })
    fireEvent.click(screen.getByRole('button', { name: /create agent/i }))

    await vi.waitFor(() => {
      expect(onCreate).toHaveBeenCalledWith(
        expect.objectContaining({ name: 'Research Bot' })
      )
    })
  })

  it('closes the modal when Cancel is clicked', () => {
    // Traces to: wave5a-wire-ui-spec.md — AC4: cancel closes modal
    const onClose = vi.fn()
    renderModal({ open: true, onClose })
    fireEvent.click(screen.getByRole('button', { name: /cancel/i }))
    expect(onClose).toHaveBeenCalledOnce()
  })
})

// US-E6: per-agent skill assignment tests for Create modal
// Traces to: nontech-ux-hardening-spec.md §6.5, F-06
describe('CreateAgentModal — Skills picker (US-E6)', () => {
  it('new agent submits without skills when none are selected (AC1 — default none)', async () => {
    // A new agent must default to no skills granted.
    vi.mocked(fetchSkills).mockResolvedValue([])
    const onCreate = vi.fn().mockResolvedValue(undefined)
    renderModal({ open: true, onClose: vi.fn(), onCreate })

    fireEvent.change(screen.getByLabelText(/name/i), { target: { value: 'Skill Test Agent' } })
    fireEvent.click(screen.getByRole('button', { name: /create agent/i }))

    await vi.waitFor(() => {
      expect(onCreate).toHaveBeenCalledWith(
        expect.objectContaining({ name: 'Skill Test Agent' })
      )
    })
    // skills must be absent (undefined) when none are selected
    const call = onCreate.mock.calls[0][0]
    expect(call.skills).toBeUndefined()
  })

  it('shows skills in Advanced section when installed skills are present', async () => {
    const mockSkills: Skill[] = [
      { id: 'web-research', name: 'Web Research', version: '1.0.0', verified: true, status: 'active' },
      { id: 'code-review', name: 'Code Review', version: '1.0.0', verified: false, status: 'active' },
    ]
    vi.mocked(fetchSkills).mockResolvedValue(mockSkills)
    renderModal({ open: true, onClose: vi.fn() })

    // Open Advanced section
    const advancedButton = await screen.findByRole('button', { name: /advanced/i })
    fireEvent.click(advancedButton)

    // Skills checkboxes should be visible
    expect(await screen.findByTestId('create-skill-checkbox-web-research')).toBeInTheDocument()
    expect(await screen.findByTestId('create-skill-checkbox-code-review')).toBeInTheDocument()
  })

  it('includes selected skills in the onCreate payload', async () => {
    const mockSkills: Skill[] = [
      { id: 'web-research', name: 'Web Research', version: '1.0.0', verified: true, status: 'active' },
    ]
    vi.mocked(fetchSkills).mockResolvedValue(mockSkills)
    const onCreate = vi.fn().mockResolvedValue(undefined)
    renderModal({ open: true, onClose: vi.fn(), onCreate })

    // Open Advanced section and select the skill
    const advancedButton = await screen.findByRole('button', { name: /advanced/i })
    fireEvent.click(advancedButton)
    const checkbox = await screen.findByTestId('create-skill-checkbox-web-research')
    fireEvent.click(checkbox)

    fireEvent.change(screen.getByLabelText(/name/i), { target: { value: 'Research Agent' } })
    fireEvent.click(screen.getByRole('button', { name: /create agent/i }))

    await vi.waitFor(() => {
      expect(onCreate).toHaveBeenCalledWith(
        expect.objectContaining({
          name: 'Research Agent',
          skills: ['web-research'],
        })
      )
    })
  })

  it('does not include skills in payload for a different agent that had none selected (AC2)', async () => {
    // This test verifies that the selectedSkills state is reset between modal opens,
    // ensuring agent A's selected skills do not bleed into a new agent B creation.
    const mockSkills: Skill[] = [
      { id: 'web-research', name: 'Web Research', version: '1.0.0', verified: true, status: 'active' },
    ]
    vi.mocked(fetchSkills).mockResolvedValue(mockSkills)
    const onCreate = vi.fn().mockResolvedValue(undefined)
    const onClose = vi.fn()

    const { rerender } = renderModal({ open: true, onClose, onCreate })

    // Open Advanced and select a skill for "agent A" creation
    const advancedButton = await screen.findByRole('button', { name: /advanced/i })
    fireEvent.click(advancedButton)
    const checkbox = await screen.findByTestId('create-skill-checkbox-web-research')
    fireEvent.click(checkbox)

    // Close and reopen the modal (simulating creating agent B)
    rerender(
      <QueryClientProvider client={makeClient()}>
        <CreateAgentModal open={false} onClose={onClose} onCreate={onCreate} />
      </QueryClientProvider>
    )
    rerender(
      <QueryClientProvider client={makeClient()}>
        <CreateAgentModal open={true} onClose={onClose} onCreate={onCreate} />
      </QueryClientProvider>
    )

    // Submit without selecting any skills for the new agent
    fireEvent.change(screen.getByLabelText(/name/i), { target: { value: 'Agent B' } })
    fireEvent.click(screen.getByRole('button', { name: /create agent/i }))

    await vi.waitFor(() => {
      expect(onCreate).toHaveBeenCalledWith(
        expect.objectContaining({ name: 'Agent B' })
      )
    })
    // Agent B should have no skills — state was reset on modal reopen
    const latestCall = onCreate.mock.calls[onCreate.mock.calls.length - 1][0]
    expect(latestCall.skills).toBeUndefined()
  })
})

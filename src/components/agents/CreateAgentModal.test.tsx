import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { act } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { CreateAgentModal } from './CreateAgentModal'
import { useUiStore } from '@/store/ui'

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

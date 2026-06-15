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

function renderModal(props: { open?: boolean; onClose?: () => void; onCreate?: (data: any) => Promise<void>; initialType?: 'custom' | 'worker' } = {}) {
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
    // Prop-driven open pins the type to 'custom' regardless of the store, so
    // the title is "New custom agent". Identify via the data-testid that the
    // shared getCreateAgentFormCopy() helper emits.
    expect(screen.getByTestId('create-custom-modal-title')).toBeInTheDocument()
  })

  it('shows Create Agent dialog when createAgentModalOpen is true in store', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Creating a custom agent (AC1)
    act(() => { useUiStore.setState({ createAgentModalOpen: true, createAgentModalType: 'custom' }) })
    renderModal()
    // Default tier (custom) → "New custom agent" title.
    expect(screen.getByTestId('create-custom-modal-title')).toBeInTheDocument()
  })

  it('shows the worker title when createAgentModalType is worker', () => {
    // Tier-preset: 'worker' set on the store flips the title + description +
    // form shape. The "New worker" button on the Agents screen sets this
    // before opening the modal.
    act(() => { useUiStore.setState({ createAgentModalOpen: true, createAgentModalType: 'worker' }) })
    renderModal()
    expect(screen.getByTestId('create-worker-modal-title')).toBeInTheDocument()
    expect(screen.getByText(/configure a delegation-only labour agent/i)).toBeInTheDocument()
  })

  it('shows the worker form shape when the initialType prop is "worker" (prop-only path)', () => {
    // Prop-only path (open + initialType) lets a parent render the modal in
    // worker mode without touching the Zustand store. The modal must still
    // resolve to the worker form shape — title, task-prompt field, and the
    // "Create worker" submit label.
    renderModal({ open: true, onClose: vi.fn(), initialType: 'worker' })
    expect(screen.getByTestId('create-worker-modal-title')).toBeInTheDocument()
    expect(screen.getByTestId('create-worker-task-prompt')).toBeInTheDocument()
    expect(screen.getByTestId('create-agent-submit')).toHaveTextContent(/create worker/i)
  })

  it('initialType=custom is the prop-only default and renders the custom form', () => {
    // When the prop path is taken without initialType (or with the explicit
    // 'custom' value), the modal falls back to the custom form shape. The
    // title testid differentiates the two tiers without an OR over strings.
    renderModal({ open: true, onClose: vi.fn() })
    expect(screen.getByTestId('create-custom-modal-title')).toBeInTheDocument()
    // The worker-only task-prompt field must NOT be in the DOM for custom.
    expect(screen.queryByTestId('create-worker-task-prompt')).toBeNull()
  })

  it('initialType=worker wins over the store value when the prop path is taken', () => {
    // Defensive: even if the store accidentally says 'custom', the
    // initialType prop must still drive the modal. The prop is the source
    // of truth on the prop-only path.
    act(() => { useUiStore.setState({ createAgentModalOpen: true, createAgentModalType: 'custom' }) })
    renderModal({ open: true, onClose: vi.fn(), initialType: 'worker' })
    expect(screen.getByTestId('create-worker-modal-title')).toBeInTheDocument()
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

// Spec-4 FR-4.1 — Executor selector wired into the create flow.
describe('CreateAgentModal — Executor (Spec-4)', () => {
  it('omits executor when left on the native default', async () => {
    const onCreate = vi.fn().mockResolvedValue(undefined)
    renderModal({ open: true, onClose: vi.fn(), onCreate })

    fireEvent.change(screen.getByLabelText(/name/i), { target: { value: 'Native Agent' } })
    fireEvent.click(screen.getByRole('button', { name: /create agent/i }))

    await vi.waitFor(() => {
      expect(onCreate).toHaveBeenCalledWith(expect.objectContaining({ name: 'Native Agent' }))
    })
    const call = onCreate.mock.calls.at(-1)![0]
    expect(call.executor).toBeUndefined()
  })

  it('includes an external-cli executor in the create payload', async () => {
    const onCreate = vi.fn().mockResolvedValue(undefined)
    renderModal({ open: true, onClose: vi.fn(), onCreate })

    // Open Advanced to reveal the executor selector.
    fireEvent.click(await screen.findByRole('button', { name: /advanced/i }))
    const kind = await screen.findByTestId('executor-kind-select')
    fireEvent.change(kind, { target: { value: 'external-cli' } })
    fireEvent.change(await screen.findByTestId('executor-cli-select'), {
      target: { value: 'opencode' },
    })

    fireEvent.change(screen.getByLabelText(/name/i), { target: { value: 'CLI Agent' } })
    fireEvent.click(screen.getByRole('button', { name: /create agent/i }))

    await vi.waitFor(() => {
      expect(onCreate).toHaveBeenCalledWith(
        expect.objectContaining({
          name: 'CLI Agent',
          executor: { kind: 'external-cli', cli: 'opencode' },
        }),
      )
    })
  })

  it('does not render the connection test button in the create flow (no agentId)', async () => {
    renderModal({ open: true, onClose: vi.fn() })
    fireEvent.click(await screen.findByRole('button', { name: /advanced/i }))
    const kind = await screen.findByTestId('executor-kind-select')
    fireEvent.change(kind, { target: { value: 'external-cli' } })
    // CLI select shows, but no runner test button (no persisted agent yet).
    expect(await screen.findByTestId('executor-cli-select')).toBeInTheDocument()
    expect(screen.queryByTestId('runner-test-button')).toBeNull()
  })
})

// ── Tier-preset: worker modal (v0.3 worker roster split) ────────────────────
// Traces to: per-section "New worker" button → CreateAgentModal in worker
// mode. The form shape, payload type, and executor-required validation are
// the worker's defining contract.
describe('CreateAgentModal — worker tier preset (v0.3)', () => {
  it('shows the worker task-prompt field when opened in worker mode', () => {
    act(() => { useUiStore.setState({ createAgentModalOpen: true, createAgentModalType: 'worker' }) })
    renderModal()
    // The task prompt textarea is the worker-specific affordance.
    expect(screen.getByTestId('create-worker-task-prompt')).toBeInTheDocument()
    // The submit button is relabelled.
    expect(screen.getByTestId('create-agent-submit')).toHaveTextContent(/create worker/i)
  })

  it('hides the worker task-prompt field when opened in custom (default) mode', () => {
    act(() => { useUiStore.setState({ createAgentModalOpen: true, createAgentModalType: 'custom' }) })
    renderModal()
    // The task prompt is a worker-only field. Base agents seed soul later
    // via the profile edit flow.
    expect(screen.queryByTestId('create-worker-task-prompt')).toBeNull()
    expect(screen.getByTestId('create-agent-submit')).toHaveTextContent(/create agent/i)
  })

  it('blocks submission when the worker has no executor', async () => {
    act(() => { useUiStore.setState({ createAgentModalOpen: true, createAgentModalType: 'worker' }) })
    const onCreate = vi.fn().mockResolvedValue(undefined)
    renderModal({ onCreate })

    fireEvent.change(screen.getByLabelText(/name/i), { target: { value: 'Orphan Worker' } })
    // Open advanced to reach the submit button (it's always reachable, but
    // the executor is hidden inside Advanced by default).
    fireEvent.click(await screen.findByRole('button', { name: /advanced/i }))
    fireEvent.click(screen.getByTestId('create-agent-submit'))

    await vi.waitFor(() => {
      expect(onCreate).not.toHaveBeenCalled()
    })
    // The executor-required error surfaces inline.
    expect(await screen.findByTestId('executor-error')).toHaveTextContent(/worker requires an executor/i)
  })

  it('sends type=worker and the executor in the create payload when valid', async () => {
    act(() => { useUiStore.setState({ createAgentModalOpen: true, createAgentModalType: 'worker' }) })
    const onCreate = vi.fn().mockResolvedValue(undefined)
    renderModal({ onCreate })

    fireEvent.change(screen.getByLabelText(/name/i), { target: { value: 'Scout Worker' } })
    // Fill the optional task prompt — should be sent as `soul` on the wire.
    fireEvent.change(screen.getByTestId('create-worker-task-prompt'), {
      target: { value: '# Task prompt\n\nGather sources for the calling agent.' },
    })
    // Pick an external-cli executor in Advanced.
    fireEvent.click(await screen.findByRole('button', { name: /advanced/i }))
    fireEvent.change(await screen.findByTestId('executor-kind-select'), {
      target: { value: 'external-cli' },
    })
    fireEvent.change(await screen.findByTestId('executor-cli-select'), {
      target: { value: 'opencode' },
    })
    fireEvent.click(screen.getByTestId('create-agent-submit'))

    await vi.waitFor(() => {
      expect(onCreate).toHaveBeenCalledWith(
        expect.objectContaining({
          type: 'worker',
          name: 'Scout Worker',
          soul: expect.stringContaining('Gather sources for the calling agent.'),
          executor: { kind: 'external-cli', cli: 'opencode' },
        }),
      )
    })
  })

  it('sends type=custom in the create payload when opened in custom mode', async () => {
    act(() => { useUiStore.setState({ createAgentModalOpen: true, createAgentModalType: 'custom' }) })
    const onCreate = vi.fn().mockResolvedValue(undefined)
    renderModal({ onCreate })

    fireEvent.change(screen.getByLabelText(/name/i), { target: { value: 'Base Agent' } })
    fireEvent.click(screen.getByTestId('create-agent-submit'))

    await vi.waitFor(() => {
      expect(onCreate).toHaveBeenCalledWith(
        expect.objectContaining({ type: 'custom', name: 'Base Agent' }),
      )
    })
    // The custom payload must NOT carry soul (the base agent seeds it later
    // via the profile edit flow) and must NOT carry a native executor.
    const call = onCreate.mock.calls.at(-1)![0]
    expect(call.soul).toBeUndefined()
    expect(call.executor).toBeUndefined()
  })

  it('omits soul in the worker payload when the task prompt is left empty', async () => {
    act(() => { useUiStore.setState({ createAgentModalOpen: true, createAgentModalType: 'worker' }) })
    const onCreate = vi.fn().mockResolvedValue(undefined)
    renderModal({ onCreate })

    fireEvent.change(screen.getByLabelText(/name/i), { target: { value: 'Quiet Worker' } })
    fireEvent.click(await screen.findByRole('button', { name: /advanced/i }))
    fireEvent.change(await screen.findByTestId('executor-kind-select'), {
      target: { value: 'external-cli' },
    })
    fireEvent.change(await screen.findByTestId('executor-cli-select'), {
      target: { value: 'claude-code' },
    })
    // Leave the task prompt empty.
    fireEvent.click(screen.getByTestId('create-agent-submit'))

    await vi.waitFor(() => {
      expect(onCreate).toHaveBeenCalledWith(
        expect.objectContaining({ type: 'worker', name: 'Quiet Worker' }),
      )
    })
    const call = onCreate.mock.calls.at(-1)![0]
    expect(call.soul).toBeUndefined()
  })
})

// ── getCreateAgentFormCopy (shared tier copy) ─────────────────────────────────
describe('getCreateAgentFormCopy — tier-branched copy', () => {
  it('returns the worker copy when type=worker', async () => {
    const { getCreateAgentFormCopy } = await import('./AgentFormFields')
    const copy = getCreateAgentFormCopy('worker')
    expect(copy.title).toBe('New sub-agent worker')
    expect(copy.submitLabel).toBe('Create worker')
    expect(copy.testId).toBe('create-worker-modal-title')
  })

  it('returns the custom copy when type=custom', async () => {
    const { getCreateAgentFormCopy } = await import('./AgentFormFields')
    const copy = getCreateAgentFormCopy('custom')
    expect(copy.title).toBe('New custom agent')
    expect(copy.submitLabel).toBe('Create agent')
    expect(copy.testId).toBe('create-custom-modal-title')
  })
})


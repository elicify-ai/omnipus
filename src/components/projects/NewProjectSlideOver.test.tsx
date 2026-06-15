import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { act } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { NewProjectSlideOver } from './NewProjectSlideOver'
import { useUiStore } from '@/store/ui'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn(),
    createWorkspace: vi.fn(),
  }
})

import { fetchAgents, createWorkspace } from '@/lib/api'

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

// Traces to: project-task-management-level1-spec.md — TC-003 NewProjectSlideOver create flow
describe('NewProjectSlideOver — create flow', () => {
  beforeEach(() => {
    // Most create-flow tests use a successful agents fetch so the Select renders.
    vi.mocked(fetchAgents).mockResolvedValue([])
    vi.mocked(createWorkspace).mockReset()
  })

  it('shows validation error when name is empty', async () => {
    // BDD: Given the slide-over is open with an empty Name field,
    // When the user submits the form,
    // Then a "Name is required" validation error is displayed inline.
    // Traces to: project-task-management-level1-spec.md — TC-003 empty-name validation
    renderSlideOver()

    // Name field starts empty — submit immediately.
    fireEvent.click(screen.getByRole('button', { name: /create workspace/i }))

    // Inline error must appear without any API call.
    expect(await screen.findByText('Name is required')).toBeInTheDocument()
    expect(vi.mocked(createWorkspace)).not.toHaveBeenCalled()
  })

  it('shows validation error when name is over 200 chars', async () => {
    // BDD: Given the slide-over is open,
    // When the user types a name longer than 200 characters and submits,
    // Then a "Name must be 200 characters or fewer" error is displayed.
    // Traces to: project-task-management-level1-spec.md — TC-003 name-length validation
    renderSlideOver()

    const nameInput = screen.getByLabelText(/name/i)

    // Type a 201-character name (maxLength on the input is 200, but the Input component
    // does not enforce maxLength in JSDOM — the validate() function does the check).
    // We fire a change event directly to bypass the DOM maxLength attribute.
    fireEvent.change(nameInput, { target: { value: 'a'.repeat(201) } })
    fireEvent.click(screen.getByRole('button', { name: /create workspace/i }))

    expect(await screen.findByText(/Name must be 200 characters or fewer/i)).toBeInTheDocument()
    expect(vi.mocked(createWorkspace)).not.toHaveBeenCalled()
  })

  it('calls createWorkspace with correct data and shows success toast on submit', async () => {
    // BDD: Given the slide-over is open with valid form data,
    // When the user fills in Name and Description and clicks "Create workspace",
    // Then createWorkspace is called with the trimmed form values,
    // And a success toast with message "Workspace created" is shown,
    // And the slide-over closes (onOpenChange(false) is called).
    // Traces to: project-task-management-level1-spec.md — TC-003 happy path
    const onOpenChange = vi.fn()
    const createdWorkspace = {
      id: 'new-proj',
      name: 'My New Project',
      status: 'active',
      pinned: false,
      pin_order: 0,
      task_count: 0,
      created_at: '2026-06-01T00:00:00Z',
      updated_at: '2026-06-01T00:00:00Z',
    }
    vi.mocked(createWorkspace).mockResolvedValueOnce(createdWorkspace as never)

    render(
      <QueryClientProvider client={makeClient()}>
        <NewProjectSlideOver open onOpenChange={onOpenChange} />
      </QueryClientProvider>,
    )

    // Fill in the Name field.
    fireEvent.change(screen.getByLabelText(/name/i), { target: { value: 'My New Project' } })
    // Fill in the Description field.
    fireEvent.change(screen.getByLabelText(/description/i), { target: { value: 'A great project' } })

    // Submit.
    fireEvent.click(screen.getByRole('button', { name: /create workspace/i }))

    // Verify createWorkspace was called with trimmed values.
    await waitFor(() => expect(vi.mocked(createWorkspace)).toHaveBeenCalledOnce())
    const callArg = vi.mocked(createWorkspace).mock.calls[0][0]
    expect(callArg.name).toBe('My New Project')
    expect(callArg.description).toBe('A great project')

    // Verify success toast was added to the real Zustand store.
    await waitFor(() => {
      const toasts = useUiStore.getState().toasts
      expect(toasts.some((t) => t.message === 'Workspace created' && t.variant === 'success')).toBe(true)
    })

    // Verify the slide-over closes.
    await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false))
  })

  it('shows error toast when createWorkspace rejects', async () => {
    // BDD: Given the slide-over is open with a valid name,
    // When createWorkspace rejects with an error,
    // Then an error toast containing "Failed to create workspace" is shown,
    // And the slide-over remains open.
    // Traces to: project-task-management-level1-spec.md — TC-003 error path
    const onOpenChange = vi.fn()
    vi.mocked(createWorkspace).mockRejectedValueOnce(new Error('server unavailable'))

    render(
      <QueryClientProvider client={makeClient()}>
        <NewProjectSlideOver open onOpenChange={onOpenChange} />
      </QueryClientProvider>,
    )

    fireEvent.change(screen.getByLabelText(/name/i), { target: { value: 'Doomed Workspace' } })
    fireEvent.click(screen.getByRole('button', { name: /create workspace/i }))

    await waitFor(() => expect(vi.mocked(createWorkspace)).toHaveBeenCalledOnce())

    // Verify error toast was added.
    await waitFor(() => {
      const toasts = useUiStore.getState().toasts
      expect(
        toasts.some((t) => t.message.includes('Failed to create workspace') && t.variant === 'error'),
      ).toBe(true)
    })

    // Slide-over must NOT close on error.
    expect(onOpenChange).not.toHaveBeenCalledWith(false)
  })

  it('excludes workers from the core-team agent picker; lists base agents', async () => {
    // BDD: Given the agents list contains a worker (type:'worker'),
    // When the user opens the core-team "Add agent" picker,
    // Then the worker is NOT offered as a selectable option,
    // And base agents ARE offered.
    // The concept treats workers as non-roster, delegation-only labour, so they
    // must never seed a workspace core team (which seeds the assignee pickers).
    vi.mocked(fetchAgents).mockResolvedValue([
      { id: 'mia', name: 'Mia', type: 'core', default: false },
      { id: 'jim', name: 'Jim', type: 'core', default: false },
      { id: 'builder', name: 'Builder Worker', type: 'worker', default: false },
    ] as never)
    // jsdom lacks scrollIntoView; Radix Select calls it when opening the listbox.
    Element.prototype.scrollIntoView = vi.fn()

    renderSlideOver()

    // Wait for the populated picker to render (placeholder appears once agents load).
    const trigger = await screen.findByText('Add agent to core team')
    fireEvent.click(trigger)

    await waitFor(() => {
      const options = Array.from(document.querySelectorAll('[role="option"]')).map(
        (el) => el.textContent ?? '',
      )
      expect(options.some((t) => t.includes('Mia'))).toBe(true)
      expect(options.some((t) => t.includes('Jim'))).toBe(true)
      expect(options.some((t) => t.includes('Builder Worker'))).toBe(false)
    })

    delete (Element.prototype as { scrollIntoView?: () => void }).scrollIntoView
  })

  it('Cancel button closes the slide-over', async () => {
    // BDD: Given the slide-over is open,
    // When the user clicks the Cancel button,
    // Then onOpenChange(false) is called,
    // And no API call is made.
    // Traces to: project-task-management-level1-spec.md — TC-003 cancel behaviour
    const user = userEvent.setup()
    const onOpenChange = vi.fn()

    render(
      <QueryClientProvider client={makeClient()}>
        <NewProjectSlideOver open onOpenChange={onOpenChange} />
      </QueryClientProvider>,
    )

    await user.click(screen.getByRole('button', { name: /cancel/i }))

    expect(onOpenChange).toHaveBeenCalledWith(false)
    expect(vi.mocked(createWorkspace)).not.toHaveBeenCalled()
  })
})

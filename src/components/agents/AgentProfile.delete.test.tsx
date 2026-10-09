import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { AgentProfile } from './AgentProfile'
import type { Agent } from '@/lib/api'
import { ConfigurationSaveError } from '@/lib/api/configuration'
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
    deleteAgent: vi.fn(),
    fetchSkills: vi.fn(),
    fetchProviders: vi.fn(),
    testAgentRunner: vi.fn(),
  }
})

import { deleteAgent, fetchAgent, fetchProviders, fetchSkills, updateAgent } from '@/lib/api'

const editableAgent: Agent = {
  revision: '0'.repeat(64),
  id: 'general-assistant',
  name: 'General Assistant',
  type: 'Main',
  locked: false,
  figure: 'Robot',
  role: 'general',
  needs_model: false,
  status: 'active',
  model: 'claude-sonnet-4-6',
  description: 'General purpose assistant',
  soul: '',
  timeout_seconds: 60,
  max_tool_iterations: 20,
  max_tool_iterations_source: 'global',
  max_tool_iterations_override_ignored: false,
  memory_enabled: true,
  editable_fields: [],
}

const retainedAgent: Agent = {
  ...editableAgent,
  id: 'mia',
  name: 'Mia',
  locked: true,
}

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function renderProfile(client = makeClient()) {
  return render(
    <QueryClientProvider client={client}>
      <AgentProfile agentId={editableAgent.id} />
    </QueryClientProvider>,
  )
}

function actionButton(dialog: HTMLElement): HTMLElement {
  const confirm = dialog.querySelector('[data-confirm-dialog-action]') as HTMLElement | null
  expect(confirm).not.toBeNull()
  return confirm!
}

/** FR-037 (founder 2026-10-07 Q6): the destructive delete is confirmed TWICE.
 *  Step 1 is the cascade warning; step 2 is the final confirmation. */
async function confirmDeletion() {
  await screen.findByText(editableAgent.name)
  fireEvent.click(screen.getByTestId('delete-agent-button'))
  // Step 1 — the warning confirmation.
  fireEvent.click(actionButton(await screen.findByRole('alertdialog')))
  // Step 2 — the same catalogued dialog advances to the final confirmation.
  await screen.findByText(/Permanently delete/)
  fireEvent.click(actionButton(screen.getByRole('alertdialog')))
}

beforeEach(() => {
  vi.mocked(fetchAgent).mockReset().mockResolvedValue(editableAgent)
  vi.mocked(fetchSkills).mockReset().mockResolvedValue([])
  vi.mocked(fetchProviders).mockReset().mockResolvedValue([])
  vi.mocked(updateAgent).mockReset().mockResolvedValue(editableAgent)
  vi.mocked(deleteAgent).mockReset()
  useUiStore.setState({ editAgentId: editableAgent.id, toasts: [] })
})

describe('AgentProfile deletion outcomes', () => {
  it('submits the revision the operator reviewed', async () => {
    vi.mocked(deleteAgent).mockResolvedValue({
      revision: '1'.repeat(64),
      persistence_status: 'complete',
      activation_status: 'active',
      changed_fields: ['agents.general-assistant'],
    })
    renderProfile()
    await confirmDeletion()
    await waitFor(() => expect(deleteAgent).toHaveBeenCalledWith(editableAgent.id, editableAgent.revision))
  })

  it('requires a second confirmation: dismissing the first step fires nothing (BDD-11.3)', async () => {
    vi.mocked(deleteAgent).mockResolvedValue({
      revision: '1'.repeat(64),
      persistence_status: 'complete',
      activation_status: 'active',
      changed_fields: [],
    })
    renderProfile()
    await screen.findByText(editableAgent.name)
    fireEvent.click(screen.getByTestId('delete-agent-button'))
    const dialog = await screen.findByRole('alertdialog')
    fireEvent.click(dialog.querySelector('[data-confirm-dialog-cancel]') as HTMLElement)
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
    expect(deleteAgent).not.toHaveBeenCalled()
  })

  it('does not delete after only the first confirmation is given (BDD-11.3)', async () => {
    vi.mocked(deleteAgent).mockResolvedValue({
      revision: '1'.repeat(64),
      persistence_status: 'complete',
      activation_status: 'active',
      changed_fields: [],
    })
    renderProfile()
    await screen.findByText(editableAgent.name)
    fireEvent.click(screen.getByTestId('delete-agent-button'))
    // Give only the first confirmation; the dialog stays open on the final step.
    fireEvent.click(actionButton(await screen.findByRole('alertdialog')))
    await screen.findByText(/Permanently delete/)
    expect(deleteAgent).not.toHaveBeenCalled()
    // Dismiss the final step: still zero destruction.
    fireEvent.click(screen.getByRole('alertdialog').querySelector('[data-confirm-dialog-cancel]') as HTMLElement)
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
    expect(deleteAgent).not.toHaveBeenCalled()
  })

  it('recovers from a persisted but inactive deletion without claiming success', async () => {
    const client = makeClient()
    client.setQueryData(['agents'], [editableAgent, retainedAgent])
    vi.mocked(deleteAgent).mockRejectedValue(new ConfigurationSaveError({
      revision: '1'.repeat(64),
      persistence_status: 'complete',
      activation_status: 'failed',
      changed_fields: ['agents.general-assistant'],
      message: 'reload failed',
    }))

    renderProfile(client)
    await confirmDeletion()

    await waitFor(() => expect(useUiStore.getState().editAgentId).toBeNull())
    expect(client.getQueryData<Agent[]>(['agents'])).toEqual([retainedAgent])
    const deletionToasts = useUiStore.getState().toasts
    expect(deletionToasts).toEqual([expect.objectContaining({
      variant: 'error',
      message: 'Delete incomplete: Changes were saved but are not active. Reload before making further changes.',
    })])
    expect(deletionToasts.some((toast) => toast.variant === 'success')).toBe(false)
    expect(client.getQueryState(['agents'])?.isInvalidated).toBe(true)
    await waitFor(() => expect(fetchAgent).toHaveBeenCalledTimes(2))
  })

  it('keeps the editor open when deletion was not persisted and requires recovery', async () => {
    vi.mocked(deleteAgent).mockRejectedValue(new ConfigurationSaveError({
      revision: editableAgent.revision,
      persistence_status: 'none',
      activation_status: 'not_attempted',
      changed_fields: [],
    }))

    renderProfile()
    await confirmDeletion()

    await waitFor(() => expect(deleteAgent).toHaveBeenCalledTimes(1))
    expect(useUiStore.getState().editAgentId).toBe(editableAgent.id)
    expect(useUiStore.getState().toasts).toEqual([expect.objectContaining({
      variant: 'error',
      message: 'Delete failed: Changes were not saved. Reload before trying again.',
    })])
  })

  it('a partly-deleted result stays visible and the same Delete retries with the current revision (BDD-11.2/11.5)', async () => {
    const client = makeClient()
    client.setQueryData(['agents'], [editableAgent, retainedAgent])
    // A partial cleanup leaves the record in place, but its revision advances
    // (some owned data is gone). The refresh below models the same record a
    // restart would reload — same Delete, current revision.
    const refreshed = { ...editableAgent, revision: '2'.repeat(64) }
    vi.mocked(fetchAgent)
      .mockResolvedValueOnce(editableAgent)
      .mockResolvedValue(refreshed)
    vi.mocked(deleteAgent)
      .mockRejectedValueOnce(new ConfigurationSaveError({
        revision: '1'.repeat(64),
        persistence_status: 'partial',
        activation_status: 'not_attempted',
        changed_fields: ['sessions'],
        message: 'owned session cleanup failed',
      }))
      .mockResolvedValueOnce({
        revision: '3'.repeat(64),
        persistence_status: 'complete',
        activation_status: 'active',
        changed_fields: [],
      })

    renderProfile(client)
    await confirmDeletion()

    // Not a clean success: the row stays visible and the editor stays open.
    await waitFor(() => expect(deleteAgent).toHaveBeenCalledTimes(1))
    expect(useUiStore.getState().toasts.some((toast) => toast.variant === 'success')).toBe(false)
    expect(useUiStore.getState().toasts).toEqual([expect.objectContaining({ variant: 'warning' })])
    expect(client.getQueryData<Agent[]>(['agents'])).toEqual([editableAgent, retainedAgent])
    expect(useUiStore.getState().editAgentId).toBe(editableAgent.id)

    // The partial refreshed the record; the SAME Delete retries with the new
    // revision (the post-restart path reloads exactly this record).
    await waitFor(() => expect(client.getQueryData<Agent>(['agent', editableAgent.id])?.revision).toBe(refreshed.revision))
    await confirmDeletion()
    await waitFor(() => expect(deleteAgent).toHaveBeenCalledTimes(2))
    expect(vi.mocked(deleteAgent).mock.calls[1]).toEqual([editableAgent.id, refreshed.revision])
  })
})

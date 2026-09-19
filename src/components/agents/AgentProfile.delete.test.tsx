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

async function confirmDeletion() {
  await screen.findByText(editableAgent.name)
  fireEvent.click(screen.getByTestId('delete-agent-button'))
  const dialog = await screen.findByRole('alertdialog')
  const confirm = dialog.querySelector('button.bg-\\[var\\(--color-error\\)\\]') as HTMLElement | null
  expect(confirm).not.toBeNull()
  fireEvent.click(confirm!)
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
})

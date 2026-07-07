/**
 * WorkspaceHeader.test.tsx — the workspace detail page header (name, repo
 * link, task count, milestone progress bars).
 *
 * Covers:
 *   - renders the workspace name
 *   - hides the milestone progress section when there are no milestones
 *     (empty → nothing rendered)
 *   - renders a milestone progress bar per milestone when the fetch succeeds
 *   - a failed milestones fetch shows a distinct error+retry state, NOT the
 *     same empty render as "genuinely no milestones" — retry re-triggers
 *     the fetch (isError-blindness regression, Wave 2).
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { WorkspaceHeader } from './WorkspaceHeader'
import { ApiError } from '@/lib/api'
import type { Workspace, Milestone } from '@/lib/api'

const addToast = vi.fn()

vi.mock('@/store/ui', () => ({
  useUiStore: (selector: (s: { addToast: typeof addToast }) => unknown) =>
    selector({ addToast }),
}))

const fetchMilestonesMock = vi.fn()
const updateWorkspaceMock = vi.fn()

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchMilestones: (id: string) => fetchMilestonesMock(id),
    updateWorkspace: (id: string, patch: Record<string, unknown>) => updateWorkspaceMock(id, patch),
    milestonesQueryKeys: {
      ...actual.milestonesQueryKeys,
      list: (id: string) => ['milestones', id],
    },
  }
})

function workspace(over: Partial<Workspace> = {}): Workspace {
  return {
    id: 'ws-1',
    name: 'Test Workspace',
    status: 'active',
    pinned: false,
    pin_order: 0,
    task_count: 0,
    created_at: '2026-06-20T10:00:00Z',
    updated_at: '2026-06-20T10:00:00Z',
    ...over,
  } as Workspace
}

function milestone(over: Partial<Milestone> = {}): Milestone {
  return {
    id: 'm1',
    workspace_id: 'ws-1',
    name: 'v1.0 Launch',
    created_at: '2026-06-20T10:00:00Z',
    updated_at: '2026-06-20T10:00:00Z',
    progress: 0.5,
    ...over,
  } as Milestone
}

function renderHeader(props: Partial<React.ComponentProps<typeof WorkspaceHeader>> = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <WorkspaceHeader workspace={workspace()} {...props} />
    </QueryClientProvider>,
  )
}

describe('WorkspaceHeader', () => {
  beforeEach(() => {
    fetchMilestonesMock.mockReset()
    updateWorkspaceMock.mockReset()
    addToast.mockReset()
  })

  it('renders the workspace name', async () => {
    fetchMilestonesMock.mockResolvedValue([])
    renderHeader()
    expect(screen.getByText('Test Workspace')).toBeInTheDocument()
  })

  it('renders nothing in the milestone section when there are no milestones (empty)', async () => {
    fetchMilestonesMock.mockResolvedValue([])
    renderHeader()
    await waitFor(() => expect(fetchMilestonesMock).toHaveBeenCalledWith('ws-1'))
    expect(screen.queryByText(/Couldn.t load milestones/i)).not.toBeInTheDocument()
    expect(screen.queryByText('v1.0 Launch')).not.toBeInTheDocument()
  })

  it('renders a progress bar per milestone when the fetch succeeds', async () => {
    fetchMilestonesMock.mockResolvedValue([
      milestone({ id: 'm1', name: 'v1.0 Launch' }),
      milestone({ id: 'm2', name: 'v2.0 Launch' }),
    ])
    renderHeader()
    await screen.findByText('v1.0 Launch')
    expect(screen.getByText('v2.0 Launch')).toBeInTheDocument()
  })

  // Regression: a failed fetch must not collapse to the same "nothing
  // rendered" state as a workspace that genuinely has no milestones — that
  // would silently hide real milestone progress. It must show a distinct
  // error state with a way to retry.
  it('shows a distinct error state (not empty) when the milestones fetch fails, and retry re-fetches', async () => {
    fetchMilestonesMock.mockRejectedValue(new Error('network error'))
    renderHeader()

    await waitFor(() => expect(fetchMilestonesMock).toHaveBeenCalledWith('ws-1'))
    expect(await screen.findByText(/Couldn.t load milestones/i)).toBeInTheDocument()

    fetchMilestonesMock.mockResolvedValueOnce([milestone({ id: 'm1', name: 'Recovered Milestone' })])
    fireEvent.click(screen.getByRole('button', { name: /Retry/i }))

    await screen.findByText('Recovered Milestone')
    expect(fetchMilestonesMock).toHaveBeenCalledTimes(2)
  })

  // Traces to: wave2 findings-fix cycle — updateMutation's onError
  // (WorkspaceHeader.tsx ~L37-40) was dedup'd from a raw isApiError ternary
  // into getErrorMessage(). addToast was mocked in this file already but
  // never actually triggered/asserted for this rename-failure path.
  it('shows an error toast with the ApiError userMessage when the rename mutation rejects, and stays in edit mode', async () => {
    fetchMilestonesMock.mockResolvedValue([])
    updateWorkspaceMock.mockRejectedValue(
      new ApiError(409, 'This conflicts with the current state. Please refresh and try again.', {
        body: '{"error":"workspace name conflict"}',
      }),
    )
    renderHeader()
    await waitFor(() => expect(fetchMilestonesMock).toHaveBeenCalledWith('ws-1'))

    fireEvent.click(screen.getByRole('button', { name: /edit workspace name/i }))
    const input = screen.getByRole('textbox')
    fireEvent.change(input, { target: { value: 'Renamed Workspace' } })
    fireEvent.click(screen.getByRole('button', { name: /^save$/i }))

    await waitFor(() =>
      expect(updateWorkspaceMock).toHaveBeenCalledWith('ws-1', { name: 'Renamed Workspace' }),
    )
    await waitFor(() =>
      expect(addToast).toHaveBeenCalledWith({
        message: 'This conflicts with the current state. Please refresh and try again.',
        variant: 'error',
      }),
    )
    // onError never calls setEditingName(false) (only onSuccess does) — the
    // input must remain visible, proving the failure path doesn't silently
    // behave like success.
    expect(screen.getByRole('textbox')).toBeInTheDocument()
    // The raw server body is debug-only and must never leak into the toast.
    expect(addToast).not.toHaveBeenCalledWith(
      expect.objectContaining({ message: expect.stringMatching(/workspace name conflict/i) }),
    )
  })
})

/**
 * WorkspaceHeader.test.tsx — the workspace detail page header (name, repo
 * link, task count).
 *
 * Milestone progress bars were removed with the milestone feature (ADR-049
 * — replaced by Plans/tags, owned by the workspace Tasks tab, not the
 * header).
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { WorkspaceHeader } from './WorkspaceHeader'
import { ApiError } from '@/lib/api'
import type { Workspace } from '@/lib/api'

const addToast = vi.fn()

vi.mock('@/store/ui', () => ({
  useUiStore: (selector: (s: { addToast: typeof addToast }) => unknown) =>
    selector({ addToast }),
}))

const updateWorkspaceMock = vi.fn()

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    updateWorkspace: (id: string, patch: Record<string, unknown>) => updateWorkspaceMock(id, patch),
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
    updateWorkspaceMock.mockReset()
    addToast.mockReset()
  })

  it('renders the workspace name', () => {
    renderHeader()
    expect(screen.getByText('Test Workspace')).toBeInTheDocument()
  })

  it('renders no milestone UI anywhere (ADR-049 removal)', () => {
    renderHeader()
    expect(screen.queryByText(/milestone/i)).not.toBeInTheDocument()
  })

  // Traces to: wave2 findings-fix cycle — updateMutation's onError
  // (WorkspaceHeader.tsx) was dedup'd from a raw isApiError ternary into
  // getErrorMessage(). addToast was mocked in this file already but never
  // actually triggered/asserted for this rename-failure path.
  it('shows an error toast with the ApiError userMessage when the rename mutation rejects, and stays in edit mode', async () => {
    updateWorkspaceMock.mockRejectedValue(
      new ApiError(409, 'This conflicts with the current state. Please refresh and try again.', {
        body: '{"error":"workspace name conflict"}',
      }),
    )
    renderHeader()

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

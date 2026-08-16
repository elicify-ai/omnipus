import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

const mockNavigate = vi.fn()
vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => mockNavigate,
}))

const mockFetchWorkspaces = vi.fn()
vi.mock('@/lib/api', () => ({
  fetchWorkspaces: (...args: unknown[]) => mockFetchWorkspaces(...args),
  workspacesQueryKeys: { list: (params?: unknown) => ['workspaces', params] },
}))

import { DefaultWorkspaceRedirect } from './DefaultWorkspaceRedirect'

function renderRedirect(tab?: 'chat' | 'board' | 'calendar') {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <DefaultWorkspaceRedirect tab={tab} />
    </QueryClientProvider>,
  )
}

const DEFAULT_WS = {
  id: 'ws-default',
  name: 'My Workspace',
  is_default: true,
  status: 'active',
  pinned: false,
  pin_order: 0,
  task_count: 0,
}

describe('DefaultWorkspaceRedirect — folded-route redirect map', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockNavigate.mockReset()
    mockFetchWorkspaces.mockReset()
    mockFetchWorkspaces.mockResolvedValue([DEFAULT_WS])
  })

  it('defaults to the Chat tab (global "/" front door)', async () => {
    renderRedirect()
    await waitFor(() => {
      expect(mockNavigate).toHaveBeenCalledWith({
        to: '/workspaces/$workspaceId/chat',
        params: { workspaceId: 'ws-default' },
        replace: true,
      })
    })
  })

  it('redirects /tasks to the Board tab', async () => {
    renderRedirect('board')
    await waitFor(() => {
      expect(mockNavigate).toHaveBeenCalledWith({
        to: '/workspaces/$workspaceId/board',
        params: { workspaceId: 'ws-default' },
        replace: true,
      })
    })
  })

  it('redirects /automations to the Calendar tab', async () => {
    renderRedirect('calendar')
    await waitFor(() => {
      expect(mockNavigate).toHaveBeenCalledWith({
        to: '/workspaces/$workspaceId/calendar',
        params: { workspaceId: 'ws-default' },
        replace: true,
      })
    })
  })
})

// ---------------------------------------------------------------------------
// Landing-race bugfix: before this fix, a failed workspaces fetch left the
// user on a static "Could not load workspaces" message with no working
// control — just a console.warn and a dead end (no refetchInterval of its
// own, unlike Sidebar.tsx's 30s poll on the SAME shared query key). The
// isError branch now wires a real Retry button to the query's refetch().
// ---------------------------------------------------------------------------
describe('DefaultWorkspaceRedirect — error state has a working retry (landing-race bugfix)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockNavigate.mockReset()
    mockFetchWorkspaces.mockReset()
  })

  it('shows a visible, working Retry control when the workspaces request fails — not a silent dead end', async () => {
    mockFetchWorkspaces.mockRejectedValueOnce(new Error('network down'))
    renderRedirect()

    const retryButton = await screen.findByTestId('workspace-redirect-retry')
    expect(retryButton).toBeInTheDocument()
    expect(retryButton).toHaveTextContent(/retry/i)
    expect(screen.getByText(/could not load workspaces/i)).toBeInTheDocument()
    // Confirms the "dead end" half of the bug report too: on the initial
    // failure, with nobody having clicked anything yet, there is genuinely no
    // navigation — the fix is the button, not an accidental auto-recovery.
    expect(mockNavigate).not.toHaveBeenCalled()
  })

  it('clicking Retry re-fetches and, once it succeeds, redirects — recovers instead of staying stuck', async () => {
    mockFetchWorkspaces.mockRejectedValueOnce(new Error('network down'))
    mockFetchWorkspaces.mockResolvedValueOnce([DEFAULT_WS])
    renderRedirect()

    const retryButton = await screen.findByTestId('workspace-redirect-retry')
    fireEvent.click(retryButton)

    await waitFor(() => {
      expect(mockNavigate).toHaveBeenCalledWith({
        to: '/workspaces/$workspaceId/chat',
        params: { workspaceId: 'ws-default' },
        replace: true,
      })
    })
    // Once for the initial (failing) mount, once for the manual retry.
    expect(mockFetchWorkspaces).toHaveBeenCalledTimes(2)
  })
})

// ---------------------------------------------------------------------------
// "A user with genuinely no workspaces lands somewhere defined" — confirms
// the existing empty state is reached DELIBERATELY (isLoading false, isError
// false, zero-length list) and not by falling through the error branch or
// spinning forever.
// ---------------------------------------------------------------------------
describe('DefaultWorkspaceRedirect — no workspaces (defined empty state, not a fall-through)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockNavigate.mockReset()
    mockFetchWorkspaces.mockReset()
  })

  it('shows "No workspaces yet" and never navigates when the list is empty', async () => {
    mockFetchWorkspaces.mockResolvedValue([])
    renderRedirect()

    expect(await screen.findByText(/no workspaces yet/i)).toBeInTheDocument()
    // Not the error branch — this is the deliberate empty state.
    expect(screen.queryByTestId('workspace-redirect-retry')).toBeNull()
    // Give any pending effect a tick, then confirm navigate is never called —
    // this is a defined terminal state, not a race that resolves later.
    await new Promise((resolve) => setTimeout(resolve, 50))
    expect(mockNavigate).not.toHaveBeenCalled()
  })
})

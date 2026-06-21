import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, waitFor } from '@testing-library/react'
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

  it('redirects /tasks + /command-center to the Board tab', async () => {
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

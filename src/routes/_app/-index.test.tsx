// Unit tests for the "/" route component — the global front-door redirect.
//
// IA reframe (Sprint 4): "/" is no longer a global chat screen. You are always
// inside a workspace, so "/" redirects into the DEFAULT workspace's Chat tab
// (/workspaces/<default>/chat). These tests pin that redirect contract:
//   - the is_default workspace wins,
//   - the first workspace is the fallback when none is marked default,
//   - no navigation happens while the workspaces list is still loading.
//
// Design note: the DefaultWorkspaceRedirect component is rendered through the
// route's component. fetchWorkspaces is mocked so we control the resolved set,
// and TanStack Router's useNavigate is mocked to capture the redirect.

import React from 'react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

const mockNavigate = vi.fn()

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    createFileRoute: () => (opts: { component: React.ComponentType }) => ({
      ...opts,
    }),
    useNavigate: () => mockNavigate,
  }
})

const mockFetchWorkspaces = vi.fn()
vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchWorkspaces: (...args: unknown[]) => mockFetchWorkspaces(...args),
  }
})

let IndexComponent: React.ComponentType | null = null

function renderIndex() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const Comp = IndexComponent
  if (!Comp) throw new Error('index route component not loaded')
  return render(
    <QueryClientProvider client={client}>
      <Comp />
    </QueryClientProvider>,
  )
}

describe('"/" route — default workspace redirect (IA reframe)', () => {
  beforeEach(async () => {
    vi.clearAllMocks()
    mockNavigate.mockReset()
    mockFetchWorkspaces.mockReset()

    if (!IndexComponent) {
      const mod = await import('./index')
      IndexComponent = (mod.Route as unknown as { component: React.ComponentType }).component
    }
  })

  it('redirects to the default workspace Chat tab', async () => {
    mockFetchWorkspaces.mockResolvedValue([
      { id: 'ws-other', name: 'Other', is_default: false, task_count: 0, status: 'active', pinned: false, pin_order: 0 },
      { id: 'ws-default', name: 'My Workspace', is_default: true, task_count: 0, status: 'active', pinned: false, pin_order: 0 },
    ])

    renderIndex()

    await waitFor(() => {
      expect(mockNavigate).toHaveBeenCalledWith({
        to: '/workspaces/$workspaceId/chat',
        params: { workspaceId: 'ws-default' },
        replace: true,
      })
    })
  })

  it('falls back to the first workspace when none is marked default', async () => {
    mockFetchWorkspaces.mockResolvedValue([
      { id: 'ws-first', name: 'First', is_default: false, task_count: 0, status: 'active', pinned: false, pin_order: 0 },
      { id: 'ws-second', name: 'Second', is_default: false, task_count: 0, status: 'active', pinned: false, pin_order: 0 },
    ])

    renderIndex()

    await waitFor(() => {
      expect(mockNavigate).toHaveBeenCalledWith({
        to: '/workspaces/$workspaceId/chat',
        params: { workspaceId: 'ws-first' },
        replace: true,
      })
    })
  })

  it('does not navigate while the workspaces list is still loading', async () => {
    // Never resolves → query stays in loading state.
    mockFetchWorkspaces.mockReturnValue(new Promise(() => {}))

    renderIndex()

    await new Promise((r) => setTimeout(r, 30))
    expect(mockNavigate).not.toHaveBeenCalled()
  })
})

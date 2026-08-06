// Unit tests for the retired /workspaces/$workspaceId/media route — now a
// redirect stub (library-spec.md; see workspaces.$workspaceId.media.tsx's doc
// comment). Mirrors -sessions.$sessionId.test.tsx's mocking approach:
// createFileRoute is stubbed to expose a fixed useParams, and TanStack
// Router's useNavigate is mocked so the redirect call can be asserted
// directly. The Library panel state lives in the REAL Zustand ui store (not
// mocked) — same approach ChatControls.test.tsx uses for the sibling "Open
// library" button, since openLibraryPanel is a plain synchronous state
// setter, not worth mocking.
//
// Covers the two things this route exists to guarantee: (1) landing here
// (tab click or a bare bookmarked URL) opens the Library panel scoped to
// THIS workspace, and (2) it redirects back to the workspace's Chat tab
// (replace: true) so the URL never dead-ends on a page with no content.

import React from 'react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, waitFor } from '@testing-library/react'

const mockNavigate = vi.fn()
let mockWorkspaceId = 'ws-1'

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    createFileRoute: (_path: string) => (opts: { component: React.ComponentType }) => ({
      ...opts,
      useParams: () => ({ workspaceId: mockWorkspaceId }),
    }),
    useNavigate: () => mockNavigate,
  }
})

import { useUiStore } from '@/store/ui'

let WorkspaceMediaRedirect: React.ComponentType | null = null

beforeEach(async () => {
  mockNavigate.mockClear()
  mockWorkspaceId = 'ws-1'
  useUiStore.setState({ libraryPanel: null })

  if (!WorkspaceMediaRedirect) {
    const mod = await import('./workspaces.$workspaceId.media')
    WorkspaceMediaRedirect = (mod.Route as unknown as { component: React.ComponentType }).component
  }
})

describe('/workspaces/$workspaceId/media redirect stub', () => {
  it('opens the Library panel scoped to this workspace on mount', async () => {
    const Route = WorkspaceMediaRedirect
    if (!Route) throw new Error('route component not loaded')
    render(<Route />)

    await waitFor(() => {
      expect(useUiStore.getState().libraryPanel).toEqual({ workspaceId: 'ws-1' })
    })
  })

  it('redirects to the workspace Chat tab, replacing history so /media never sits in the URL', async () => {
    const Route = WorkspaceMediaRedirect
    if (!Route) throw new Error('route component not loaded')
    render(<Route />)

    await waitFor(() => {
      expect(mockNavigate).toHaveBeenCalledWith({
        to: '/workspaces/$workspaceId/chat',
        params: { workspaceId: 'ws-1' },
        replace: true,
      })
    })
  })

  it('scopes the panel and redirect to whichever workspace the route was reached for, not a hardcoded id', async () => {
    mockWorkspaceId = 'ws-42'
    const Route = WorkspaceMediaRedirect
    if (!Route) throw new Error('route component not loaded')
    render(<Route />)

    await waitFor(() => {
      expect(useUiStore.getState().libraryPanel).toEqual({ workspaceId: 'ws-42' })
      expect(mockNavigate).toHaveBeenCalledWith({
        to: '/workspaces/$workspaceId/chat',
        params: { workspaceId: 'ws-42' },
        replace: true,
      })
    })
  })
})

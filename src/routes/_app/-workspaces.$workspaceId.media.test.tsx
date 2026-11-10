// Unit tests for the retired /workspaces/$workspaceId/media route — now a
// redirect stub (library-spec.md; see workspaces.$workspaceId.media.tsx's doc
// comment). Mirrors -sessions.$sessionId.test.tsx's mocking approach:
// createFileRoute is stubbed to expose a fixed useParams, and TanStack
// Router's useNavigate is mocked so the redirect call can be asserted
// directly. The ui store is the REAL Zustand store — asserted only to prove
// the redirect makes NO panel store call anymore.
//
// WAVE 1 REWORK (side-panel-shell-spec.md §8.2, intentional change — the
// companion -workspaces.$workspaceId.media.panel.test.tsx pins the same new
// contract): the redirect retargets to the DEEP-LINK form
// `/workspaces/{id}/chat?panel=library` (replace) instead of making a store
// call + bare redirect. The panel state has exactly one writer — the URL
// contract — and the chat route's deep-link restore owns the actual open.
//
// Covers the two things this route exists to guarantee: (1) landing here
// (a bare bookmarked URL) keeps working by retargeting to the deep-link
// chat URL, and (2) the redirect replaces history so /media never sits in
// the URL.

import React from 'react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, waitFor } from '@testing-library/react'

const mockNavigate = vi.fn()
let mockWorkspaceId = 'ws-1'

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    createFileRoute: () => (opts: { component: React.ComponentType }) => ({
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
  useUiStore.setState({ activePanel: null })

  if (!WorkspaceMediaRedirect) {
    const mod = await import('./workspaces.$workspaceId.media')
    WorkspaceMediaRedirect = (mod.Route as unknown as { component: React.ComponentType }).component
  }
})

describe('/workspaces/$workspaceId/media redirect stub', () => {
  it('retargets to the deep-link chat URL with ?panel=library — and makes NO panel store call (§8.2)', async () => {
    const Route = WorkspaceMediaRedirect
    if (!Route) throw new Error('route component not loaded')
    render(<Route />)

    await waitFor(() => {
      expect(mockNavigate).toHaveBeenCalledWith({
        to: '/workspaces/$workspaceId/chat',
        params: { workspaceId: 'ws-1' },
        search: { panel: 'library' },
        replace: true,
      })
    })
    // The store has exactly one writer — the chat route's deep-link
    // adoption. The redirect stub itself never touches panel state.
    expect(useUiStore.getState().activePanel).toBeNull()
  })

  it('redirects to the workspace Chat tab, replacing history so /media never sits in the URL', async () => {
    const Route = WorkspaceMediaRedirect
    if (!Route) throw new Error('route component not loaded')
    render(<Route />)

    await waitFor(() => {
      expect(mockNavigate).toHaveBeenCalledWith(
        expect.objectContaining({ replace: true, to: '/workspaces/$workspaceId/chat' }),
      )
    })
  })

  it('scopes the retarget to whichever workspace the route was reached for, not a hardcoded id', async () => {
    mockWorkspaceId = 'ws-42'
    const Route = WorkspaceMediaRedirect
    if (!Route) throw new Error('route component not loaded')
    render(<Route />)

    await waitFor(() => {
      expect(mockNavigate).toHaveBeenCalledWith({
        to: '/workspaces/$workspaceId/chat',
        params: { workspaceId: 'ws-42' },
        search: { panel: 'library' },
        replace: true,
      })
    })
  })
})

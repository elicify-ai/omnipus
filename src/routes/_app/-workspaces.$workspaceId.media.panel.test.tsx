// workspaces.$workspaceId.media.panel.test.tsx — RED pack for
// side-panel-shell-spec.md §8.2's media-redirect retarget (wave 1):
//
//   "The media redirect stub
//   (src/routes/_app/workspaces.$workspaceId.media.tsx::WorkspaceMediaRedirect)
//   retargets to the deep-link form: navigate to
//   `/workspaces/{id}/chat?panel=library` (replace) instead of store-call +
//   bare redirect — bookmarked `media` links keep working."
//
// Oracle: §8.2, verbatim above, plus §3.1's impact row for this exact route
// ("Redirect target becomes the deep-link chat URL with `?panel=library`
// instead of store-call + redirect").
//
// Mirrors -workspaces.$workspaceId.media.test.tsx's harness exactly
// (createFileRoute stubbed for a fixed useParams; useNavigate mocked so the
// call can be asserted directly) — this file is the WAVE-1 companion, not a
// replacement: the existing file pins today's (store-call + bare redirect)
// behavior, which this feature intentionally changes.
//
// RED evidence (2026-09-27, read
// src/routes/_app/workspaces.$workspaceId.media.tsx in full): the route
// still calls `useUiStore.getState().openLibraryPanel(workspaceId)` directly
// and navigates with NO `search` at all
// (`{ to: '/workspaces/$workspaceId/chat', params: { workspaceId },
// replace: true }`) — every assertion below expecting a `panel: 'library'`
// search param fails because the actual call has no `search` key.

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

let WorkspaceMediaRedirect: React.ComponentType | null = null

beforeEach(async () => {
  mockNavigate.mockClear()
  mockWorkspaceId = 'ws-1'

  if (!WorkspaceMediaRedirect) {
    const mod = await import('./workspaces.$workspaceId.media')
    WorkspaceMediaRedirect = (mod.Route as unknown as { component: React.ComponentType }).component
  }
})

describe('/workspaces/$workspaceId/media redirect — wave-1 deep-link retarget (§8.2)', () => {
  it('navigates to the chat route with ?panel=library (replace) instead of a bare redirect', async () => {
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
  })

  it('carries the panel param for whichever workspace the route was reached for, not a hardcoded id', async () => {
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

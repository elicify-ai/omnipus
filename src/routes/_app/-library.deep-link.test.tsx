// -library.deep-link.test.tsx — D-97 (leftover, 2026-09-14 fix round).
//
// OP2's observation: a deep link to /#/library?workspace=…&path=… rendered
// the base preview and then, ~30 s later WITH NO INTERACTION, the SPA
// navigated itself to #/workspaces/…/chat.
//
// Unlike -library.test.tsx (which stubs createFileRoute), this mounts the
// REAL routeTree in a REAL router (memory history) so every navigation
// effect in the app is live: _app.tsx's beforeLoad auth guard, AppShell,
// Sidebar (including its 30 s workspaces poll — the ~30 s timing in the
// report is exactly a poll/refetch landing), and the real LibraryExplorer.
// The test opens the deep link COLD, lets everything settle, advances time
// past the poll interval so late-arriving data lands exactly as reported,
// and asserts the router is STILL on /library with its address intact.
//
// A second case pins the sibling form the fix round asked about,
// /workspaces/<id>/library: that route does not exist in the route tree, so
// the assertion is that it renders the router's not-found state and STAYS
// there — it must not silently bounce into a workspace chat (which would be
// indistinguishable, from the user's seat, from the reported defect).

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createRouter, createMemoryHistory, RouterProvider } from '@tanstack/react-router'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    // _app.tsx beforeLoad:
    fetchAppState: vi.fn(async () => ({ onboarding_complete: true })),
    validateToken: vi.fn(async () => ({})),
    // AppShell + Sidebar:
    fetchNotifications: vi.fn(async () => ({ notifications: [], unread_count: 0 })),
    fetchTasks: vi.fn(async () => []),
    fetchAgents: vi.fn(async () => []),
    fetchVersion: vi.fn(async () => ({ version: 'test', build_sha: 'test' })),
    fetchWorkspaces: vi.fn(async () => [
      { id: 'ws-1', name: 'UAT Build', status: 'active', is_default: true },
    ]),
    fetchSessions: vi.fn(async () => []),
    // LibraryExplorer (real component, real queries):
    fetchLibraryWorkspaces: vi.fn(async () => [{ id: 'ws-1', name: 'UAT Build' }]),
    fetchLibraryEntries: vi.fn(async () => []),
    fetchLibraryContent: vi.fn(async () => ({
      path: 'UAT Vault/Projects.base',
      content: 'views:\n  - id: all\n',
      size: 24,
      is_text: true,
      too_large: false,
    })),
    fetchKnowledgeBaseInfo: vi.fn(async () => ({
      workspace_id: 'ws-1',
      root_path: 'UAT Vault',
      is_knowledge_base: false,
      marker: 'none',
    })),
    fetchKnowledgeOutline: vi.fn(async () => ({ path: '', is_knowledge_base: false, headings: [] })),
    fetchKnowledgeGraph: vi.fn(async () => ({
      collection_id: 'kb_1',
      kind: 'backlinks',
      nodes: [],
      edges: [],
      skipped: [],
      truncated: false,
    })),
    searchVault: vi.fn(async () => ({
      collection_id: 'kb_1',
      complete: true,
      notes: [],
      records: [],
      views: [],
    })),
    searchFiles: vi.fn(async () => ({
      hits: [],
      truncated: false,
      limits_applied: {
        files: 50000,
        bytes: 268435456,
        matches: 1000,
        matches_per_file: 50,
        depth: 32,
        deadline_ms: 3000,
        output_bytes: 1048576,
      },
      stats: {
        files_visited: 0,
        bytes_scanned: 0,
        files_skipped_problems: 0,
        files_pruned_ignored: 0,
        files_skipped_per_file_cap: 0,
        hits_capped_per_file: 0,
      },
    })),
    libraryDownloadUrl: vi.fn(
      (wsId: string, path: string) => `/api/v1/library/${wsId}/download?path=${path}`,
    ),
    mintLibraryPreviewToken: vi.fn(),
  }
})

import { routeTree } from '@/routeTree.gen'

// hasStoredSession is the gate before any server round trip in beforeLoad —
// seed it the way a real login would have. jsdom has no matchMedia
// (AppShell/Sidebar/Sidebar's pin breakpoint use it) — same per-file stub
// AppShell.test.tsx uses.
const originalMatchMedia = window.matchMedia
beforeEach(() => {
  localStorage.setItem('omnipus_auth_username', 'uat-tester')
  Object.defineProperty(window, 'matchMedia', {
    configurable: true,
    writable: true,
    value: (query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    }),
  })
})

afterEach(() => {
  localStorage.removeItem('omnipus_auth_username')
  vi.useRealTimers()
  Object.defineProperty(window, 'matchMedia', {
    configurable: true,
    writable: true,
    value: originalMatchMedia,
  })
})

function makeClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
}

describe('D-97 — Library deep link stays put', () => {
  it('a cold /library?workspace=&path= deep link is still on /library after the 30 s poll window', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const client = makeClient()
    const router = createRouter({
      routeTree,
      history: createMemoryHistory({
        initialEntries: ['/library?workspace=ws-1&path=UAT%20Vault%2FProjects.base'],
      }),
    })

    render(
      <QueryClientProvider client={client}>
        <RouterProvider router={router} />
      </QueryClientProvider>,
    )

    // The real explorer rendering is proof the deep link ARRIVED (guard
    // passed, route matched, address threaded through).
    await waitFor(
      () => expect(screen.getByTestId('library-explorer')).toBeInTheDocument(),
      { timeout: 5000 },
    )
    expect(router.state.location.pathname).toBe('/library')

    // The reported window: ~30 s of NOTHING, while every poll/refetch in
    // the app lands (Sidebar's 30 s workspaces poll above all — the only
    // thing in the app that fires on that schedule with no interaction).
    await act(async () => {
      await vi.advanceTimersByTimeAsync(35_000)
    })
    // Belt and braces: a fresh refetch of the SHARED workspaces key, the
    // exact late-data arrival a poll produces.
    await act(async () => {
      await client.refetchQueries({ queryKey: ['workspaces'] })
    })

    expect(router.state.location.pathname).toBe('/library')
    expect(router.state.location.search).toMatchObject({
      workspace: 'ws-1',
      path: 'UAT Vault/Projects.base',
    })
    expect(screen.getByTestId('library-explorer')).toBeInTheDocument()

    client.clear()
  })

  it('a /workspaces/<id>/library deep link (no such route) stays on its not-found state — no silent chat bounce', async () => {
    const client = makeClient()
    const router = createRouter({
      routeTree,
      history: createMemoryHistory({ initialEntries: ['/workspaces/ws-1/library'] }),
    })

    render(
      <QueryClientProvider client={client}>
        <RouterProvider router={router} />
      </QueryClientProvider>,
    )

    // Let the guard + any redirects settle (real timers here — no interval
    // to advance, just the beforeLoad promises).
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
    await waitFor(() => expect(router.state.status).toBe('idle'), { timeout: 5000 })

    expect(router.state.location.pathname).toBe('/workspaces/ws-1/library')
    // Not-found is rendered INLINE (TanStack default) — the router must not
    // have replaced the location with a workspace chat route.
    expect(router.state.location.pathname).not.toMatch(/\/chat$/)

    client.clear()
  })
})

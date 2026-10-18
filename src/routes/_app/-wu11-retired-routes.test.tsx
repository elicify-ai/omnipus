/**
 * -wu11-retired-routes.test.tsx — WU-11 RED: the retired routes are GONE.
 *
 * Oracles (omnipus-system-agent-spec.md — docs/internal/specs/omnipus-system-agent-spec.md):
 *   - FR-108: "The Agents screen and routes, the create wizard/modal and
 *     /admin/chat MUST be removed without forwarding; REST create-agent MUST
 *     remain, documented as a non-UI API."
 *   - BDD-12.7 Old links (US-12 AS-5, Error Path, Outline): Given /agents,
 *     /agents/mia, /admin/chat — "Then not-found, no redirect".
 *   - BDD-12.8 No create form (US-12 AS-5): "Then no manual create action
 *     exists; Add agent opens Omnipus."
 *   - §15 WU-11 row: SPA removals land in WU-11 (landing 1).
 *
 * SC-007 (team-lead steer on the frontend impact map): not-found alone is not
 * enough — /agents/mia and /admin/chat REDIRECT today, so each case asserts
 * the root NotFoundPage renders AND the URL is unchanged. A redirect that
 * lands on a not-found-looking page must still fail these tests.
 *
 * The route tree is REAL (routeTree.gen), the router is REAL (memory history),
 * the shell and screens are REAL. Only the network edge is controlled: the
 * @/lib/api fetchers the shell reads are mocked; every query runs with
 * retry:false so an unmocked read cannot hang the assertion.
 *
 * Expected state: RED on today's code — /agents renders AgentListScreen,
 * /agents/mia redirects to /agents (opening the edit slide-over), /admin/chat
 * redirects into Admin's default-workspace main, and the sidebar still carries
 * the "Agents" asset link (Sidebar.tsx::ASSET_ITEMS).
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, createRouter, RouterProvider } from '@tanstack/react-router'
import type { ReactNode } from 'react'

// ── Network edge ─────────────────────────────────────────────────────────────
// Fresh-install state: one workspace, no agents, no sessions — EXCEPT the
// /admin/chat case, which needs Admin RESOLVABLE (agent loaded + default
// workspace carrying admin_main_session_id + the session in the list) so the
// stub has every reason to redirect. A redirect under ideal conditions is the
// strongest possible violation of "no forwarding" (FR-108).
const { apiEdges } = vi.hoisted(() => ({
  apiEdges: {
    fetchAppState: vi.fn(),
    fetchAgents: vi.fn(),
    fetchSessions: vi.fn(),
    fetchWorkspaces: vi.fn(),
    fetchWorkspace: vi.fn(),
    fetchWorkspaceDelegation: vi.fn(),
    fetchTasks: vi.fn(),
    fetchPlans: vi.fn(),
  },
}))

vi.mock('@/lib/api', async (importOriginal) => ({
  ...(await importOriginal<Record<string, unknown>>()),
  ...apiEdges,
}))

import { routeTree } from '@/routeTree.gen'
import type { Agent, Session, Workspace } from '@/lib/api'

function makeWorkspace(overrides: Partial<Workspace> = {}): Workspace {
  return {
    id: 'ws-default',
    name: 'Default',
    status: 'active',
    pinned: false,
    pin_order: 0,
    task_count: 0,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    revision: '0'.repeat(64),
    is_default: true,
    ...overrides,
  }
}

function makeAgent(overrides: Partial<Agent> = {}): Agent {
  return {
    id: 'mia',
    name: 'Mia',
    type: 'core',
    status: 'idle',
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    ...overrides,
  } as Agent
}

function makeSession(overrides: Partial<Session> = {}): Session {
  return {
    id: 'main-mia-1',
    agent_id: 'mia',
    title: 'Mia',
    type: 'main',
    workspace_id: 'ws-default',
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    message_count: 0,
    ...overrides,
  }
}

const SIGNED_IN = {
  onboarding_complete: true,
  identity: { mode: 'local', edition: 'core', signed_in: true, blocked_reason: 'none' },
}

function mockFreshInstall(adminResolvable = false) {
  apiEdges.fetchAppState.mockResolvedValue(SIGNED_IN)
  apiEdges.fetchAgents.mockResolvedValue(
    adminResolvable ? [makeAgent({ id: 'admin', name: 'Admin' })] : [],
  )
  apiEdges.fetchSessions.mockResolvedValue(
    adminResolvable
      ? [makeSession({ id: 'main-admin-1', agent_id: 'admin', title: 'Admin' })]
      : [],
  )
  apiEdges.fetchWorkspaces.mockResolvedValue(
    adminResolvable
      ? [makeWorkspace({ admin_main_session_id: 'main-admin-1' })]
      : [makeWorkspace()],
  )
  apiEdges.fetchWorkspace.mockImplementation(async (id: string) => makeWorkspace({ id }))
  apiEdges.fetchWorkspaceDelegation.mockResolvedValue({
    revision: '0'.repeat(64),
    workspace_id: 'ws-default',
    team: [],
    edges: [],
    default_depth: 3,
  })
  apiEdges.fetchTasks.mockResolvedValue([])
  apiEdges.fetchPlans.mockResolvedValue([])
}

// ── Harness ──────────────────────────────────────────────────────────────────
async function mountAt(path: string) {
  const history = createMemoryHistory({ initialEntries: [path] })
  const router = createRouter({ routeTree, history })
  const clients: QueryClient[] = []
  const wrapper = ({ children }: { children: ReactNode }) => {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    })
    clients.push(client)
    return <QueryClientProvider client={client}>{children}</QueryClientProvider>
  }
  render(<RouterProvider router={router} />, { wrapper })
  return {
    /** The live memory-history path — the no-redirect oracle (SC-007).
     * Read it only AFTER a content assertion has settled the tree: the async
     * beforeLoad and any redirect effects have then flushed. */
    currentPath: () => history.location.pathname,
  }
}

/**
 * Wait until the route's content has actually settled. agents.index.tsx
 * lazy-loads AgentListScreen behind RouteFallback, so an assertion fired
 * immediately would read the skeleton and pass vacuously. The marker is the
 * DISJUNCTION the oracle itself allows: either the branded 404 (the FR-108
 * end state) or the retired screen's own content (today's state).
 */
async function awaitSettled(markers: RegExp[]) {
  await waitFor(
    () => {
      const settled = markers.some((m) => screen.queryByText(m) !== null)
      expect(settled, 'route content never settled (neither not-found nor the retired screen)').toBe(true)
    },
    { timeout: 10_000 },
  )
}

beforeEach(() => {
  // jsdom has no matchMedia; the sidebar's motion/pointer queries need it
  // (same stub the sidebar and Team harnesses use).
  if (!window.matchMedia) {
    Object.defineProperty(window, 'matchMedia', {
      writable: true,
      value: vi.fn().mockImplementation((query: string) => ({
        matches: false,
        media: query,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
      })),
    })
  }
  window.history.replaceState({}, '', '/')
})

afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

describe('WU-11 RED — retired routes are removed without forwarding (FR-108, BDD-12.7, BDD-12.8)', () => {
  it('/agents renders the root not-found page and keeps the URL (no redirect, no Agents screen)', async () => {
    mockFreshInstall()
    const view = await mountAt('/agents')
    // Settle: today the lazy AgentListScreen loads ("New agent"); post-GREEN
    // the branded 404 renders instead.
    await awaitSettled([/drifted into the deep/, /New agent/])

    // BDD-12.8: no manual create action exists — AgentListScreen's "+ New
    // agent" header action (AgentListScreen.tsx) must not render. This is the
    // loudest RED today: the action IS on the settled screen.
    expect(screen.queryByText('New agent')).toBeNull()
    expect(screen.queryByText('Create agent')).toBeNull()

    // BDD-12.7: the branded 404 (src/routes/__root.tsx::NotFoundPage —
    // "This page drifted into the deep.") is the ONLY thing /agents may show.
    expect(screen.queryByText(/drifted into the deep/)).toBeInTheDocument()

    // SC-007: no redirect — the URL stays exactly where the old link points.
    expect(view.currentPath()).toBe('/agents')
  })

  it('/agents/mia renders the root not-found page and keeps the URL (deep link must not open the editor or redirect)', async () => {
    mockFreshInstall()
    const view = await mountAt('/agents/mia')
    await awaitSettled([/drifted into the deep/, /New agent/])

    // FR-108: the route is removed — today agents.$agentId.tsx opens the edit
    // slide-over and navigate({ to: '/agents', replace: true }); both are
    // violations. Settling lands on the redirected list screen today, so the
    // 404 assertion below is the RED.
    expect(screen.queryByText('New agent')).toBeNull()
    expect(screen.queryByText(/drifted into the deep/)).toBeInTheDocument()
    expect(view.currentPath()).toBe('/agents/mia')
  })

  it('/admin/chat renders the root not-found page and keeps the URL even when Admin resolves (no forwarding)', async () => {
    // Resolvable Admin: the strongest form of the oracle. Today the redirect
    // stub (admin.chat.tsx::AdminChatRedirect) enters Admin's default-workspace
    // main; FR-108/BDD-12.7 require the route gone WITHOUT forwarding. The
    // settle markers are every screen the stub can show today, plus the 404.
    mockFreshInstall(true)
    const view = await mountAt('/admin/chat')
    await awaitSettled([
      /drifted into the deep/,
      /Restoring your conversation/,
      /Main chat unavailable/,
      /Default/,
    ])

    expect(screen.queryByText('Restoring your conversation…')).toBeNull()
    expect(screen.queryByLabelText('Main chat unavailable')).toBeNull()
    expect(screen.queryByText(/drifted into the deep/)).toBeInTheDocument()
    expect(view.currentPath()).toBe('/admin/chat')
  })

  it('the sidebar carries no "Agents" asset link (BDD-12.8: no Agents management entry exists)', async () => {
    mockFreshInstall()
    const view = await mountAt('/workspaces/ws-default/chat')

    // The app shell must render (a matched workspace route) — this proves the
    // absence below is the sidebar's real content, not a 404 page.
    const burger = await screen.findByRole('button', { name: 'Show sidebar' }, { timeout: 10_000 })
    // The fixed nav is collapsed by default in jsdom; expand it so the
    // absence assertion below reads the REAL nav contents, not a collapsed one.
    const user = userEvent.setup()
    if (burger.getAttribute('aria-expanded') !== 'true') await user.click(burger)

    // Sidebar.tsx::ASSET_ITEMS ships { to: '/agents', label: 'Agents' } today.
    expect(screen.queryByRole('link', { name: 'Agents' })).toBeNull()
    expect(view.currentPath()).toBe('/workspaces/ws-default/chat')
  })
})

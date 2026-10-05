// wave3FullScreen.test.tsx — wave-3 join pack for side-panel-shell-spec.md
// Wave 3, SP-38 (amending FR-008/US-6 for Tasks, Team and Calendar only):
//
//   "Those three panels' full-screen presentation is the SAME chrome-less,
//   in-app 'Back to chat' shell route already specified for
//   Library/Browser/Mail's full-screen work — NOT the 'Expand opens a new
//   browser tab' route FR-008/US-6 describe. There is no separate full-page
//   route for Tasks/Team/Calendar."  (§ amendment header, item 1; FR-008
//   amended: "Tasks, Team and Calendar do NOT use this new-tab route")
//
// Oracle sources: spec §10 Wave 3 table, §13 SP-38, FR-008 as amended, the
// § amendment header, wireframe §3/§4/§5 (chrome-less "← Back to chat").
//
// JOIN STATUS (2026-10-05, work/side-panel-wave3-join-20261005) — delivered
// seam renegotiations (authorised by the 8773803cf pack's header), spec
// expectations unchanged:
//
//   1. ROUTE PATH: the shared chrome-less route's URL is `/panel/$panelId`
//      (the `_fullscreen` layout is pathless — TanStack FileRouter), not the
//      `/fullscreen/panel/...` path the RED pack guessed pre-implementation.
//      The workspace-scoped panels' codec (registry.tsx
//      workspaceFullScreenCodec) REQUIRES `?workspace=<id>` on this route —
//      a bare /panel/<id> link is an invalid link and shows the recovery
//      page — so the pack now drives a workspace-scoped URL and asserts the
//      InvalidPanel fallback stays absent (which proves the codec, not just
//      the route).
//   2. EXPAND MECHANISM: `expand: 'route'` (the registry field FR-008's
//      amendment introduces) is delivered in the SHELL, upstream of the old
//      popout flow — SidePanelShell.handleExpand branches on it and hands
//      the panel to the router-owned handler (AppShell navigates THIS tab to
//      /panel/$panelId), so the negative "never window.open" is asserted at
//      the real user seam: clicking the docked panel's Expand control.
//
// RED evidence this pack carries forward: on base fe0b68fb0 the registry
// held only library/browser (no tasks/team/calendar definitions) — the
// registry tests below failed with the gap named. In the JOIN they pass
// against the delivered registry; the full-screen route and expand tests
// pin SP-38's delivered behaviour.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, waitFor, screen, fireEvent, act } from '@testing-library/react'
import { createMemoryHistory, createRouter, RouterProvider } from '@tanstack/react-router'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'

// SidePanelShell tracks the panel's own width with ResizeObserver, which
// jsdom lacks (same stub the shell's own resilience pack uses).
class RowResizeObserver {
  constructor(private readonly callback: ResizeObserverCallback) {}
  observe() {
    this.callback(
      [{ contentRect: { width: 1280 } as DOMRectReadOnly } as ResizeObserverEntry],
      this as unknown as ResizeObserver,
    )
  }
  unobserve() {}
  disconnect() {}
}
vi.stubGlobal('ResizeObserver', RowResizeObserver)

// The shared chrome-less layout is authenticated; the route-level gate is not
// what SP-38 tests — stub it so the router can mount the panel route.
vi.mock('@/routes/-authenticatedBeforeLoad', () => ({
  authenticatedBeforeLoad: vi.fn(async () => {}),
}))

// Keep the screen data layer inert; the route assertions are chrome-level and
// must not depend on live queries. (The real registry stays REAL — it is the
// thing under test.)
vi.mock('@/lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api')>()),
  fetchTasks: vi.fn(async () => []),
  fetchPlans: vi.fn(async () => []),
  fetchAgents: vi.fn(async () => []),
  fetchWorkspaceDelegation: vi.fn(async () => null),
}))

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={makeClient()}>{children}</QueryClientProvider>
}

import { panels, getPanelDefinition } from './registry'
import { SidePanelShell } from './SidePanelShell'
import { usePanelShellStore } from './panelShellStore'
import { routeTree } from '@/routeTree.gen'
import type { WorkspacePanelId } from './types'

describe.each([
  { id: 'tasks', title: 'Tasks' },
  { id: 'team', title: 'Team' },
  { id: 'calendar', title: 'Calendar' },
] as const satisfies readonly { id: WorkspacePanelId; title: string }[])(
  'wave-3 registry — $title is a registered panel (SP-6)',
  ({ id, title }) => {
    it(`the production registry defines the ${id} panel with title "${title}"`, () => {
      const definition = getPanelDefinition(id)
      expect(definition).toBeDefined()
      expect(definition?.title).toBe(title)
      expect(definition?.content).toBeDefined()
    })

    it(`the ${id} panel participates in the same registry array as library/browser`, () => {
      // One registry, one shell: SP-6 makes these panels, not pages.
      expect(panels.some((panel) => panel.id === id)).toBe(true)
    })
  },
)

describe.each([
  { id: 'tasks', title: 'Tasks' },
  { id: 'team', title: 'Team' },
  { id: 'calendar', title: 'Calendar' },
] as const satisfies readonly { id: WorkspacePanelId; title: string }[])(
  'wave-3 full screen — $title uses the chrome-less "Back to chat" route (SP-38)',
  ({ id }) => {
    it(`navigating to /panel/${id}?workspace=… renders the chrome-less panel shell with "Back to chat"`, async () => {
      const history = createMemoryHistory({
        initialEntries: [`/panel/${id}?workspace=ws-1`],
      })
      const router = createRouter({ routeTree, history })
      const mounted = render(<RouterProvider router={router} />, { wrapper })
      await waitFor(() => expect(router.state.status).toBe('idle'), { timeout: 5_000 })

      // The shared chrome-less shell renders (SP-38: "the SAME chrome-less
      // 'Back to chat' shell route").
      expect(mounted.getByTestId('fullscreen-panel')).toBeInTheDocument()
      expect(
        mounted.getByRole('button', { name: /back to chat/i }),
      ).toBeInTheDocument()
      // Chrome-less: the app chrome (workspace tab strip) is NOT mounted here.
      expect(mounted.queryByTestId('workspace-tab-strip')).not.toBeInTheDocument()
      // Not the InvalidPanel fallback — the workspace-scoped codec validated
      // the link (a bare /panel/<id> without ?workspace= is the invalid one,
      // by the registry's own single-source codec).
      expect(mounted.queryByText(/can't open this panel/i)).not.toBeInTheDocument()
      // The panel's own content is mounted inside the chrome-less host (not
      // a blank frame): the registry's content component for this panel ran.
      await waitFor(
        () => expect(mounted.queryByTestId('fullscreen-panel')?.textContent?.length ?? 0).toBeGreaterThan(0),
      )
    })
  },
)

describe.each(['tasks', 'team', 'calendar'] as const satisfies readonly WorkspacePanelId[])(
  'wave-3 full screen — expanding %s never opens a new browser tab (SP-38 negative)',
  (id) => {
    let openSpy: ReturnType<typeof vi.spyOn>

    beforeEach(() => {
      usePanelShellStore.setState({
        activePanel: null,
        panelWidth: null,
        guardPending: false,
        historyPushed: false,
      })
      openSpy = vi.spyOn(window, 'open').mockReturnValue(null)
    })

    afterEach(() => {
      openSpy.mockRestore()
      vi.restoreAllMocks()
      usePanelShellStore.getState().closePanel()
    })

    it(`clicking the docked ${id} panel's Expand navigates this tab's route — window.open never runs (FR-008 as amended)`, () => {
      // Anti-vacuous guards: the negative is only meaningful because (a) the
      // panel exists and (b) its registry entry is the SP-38 'route' expand —
      // the exact field FR-008's amendment introduces to divert these three
      // panels away from the new-tab popout flow.
      const definition = getPanelDefinition(id)
      expect(definition).toBeDefined()
      expect(definition?.fullScreen?.expand).toBe('route')

      // The real shell around the real registry: the same seam a user's
      // Expand click drives.
      const onExpandRoute = vi.fn()
      render(
        <SidePanelShell
          panels={panels}
          username="dana"
          chat={<textarea data-testid="chat-input" defaultValue="chat" />}
          onExpandRoute={onExpandRoute}
        />,
      )
      act(() => {
        usePanelShellStore.getState().openPanel(id, { workspaceId: 'ws-1' })
      })
      expect(usePanelShellStore.getState().activePanel?.id).toBe(id)

      fireEvent.click(screen.getByRole('button', { name: `Expand ${definition?.title} panel` }))

      expect(openSpy).not.toHaveBeenCalled()
      expect(onExpandRoute).toHaveBeenCalledTimes(1)
      expect(onExpandRoute.mock.calls[0]?.[0]?.id).toBe(id)
      expect(onExpandRoute.mock.calls[0]?.[1]).toEqual({ workspaceId: 'ws-1' })
    })
  },
)

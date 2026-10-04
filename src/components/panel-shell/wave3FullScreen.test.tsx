// wave3FullScreen.test.tsx — RED pack for side-panel-shell-spec.md Wave 3,
// SP-38 (amending FR-008/US-6 for Tasks, Team and Calendar only):
//
//   "Those three panels' full-screen presentation is the SAME chrome-less,
//   in-app 'Back to chat' shell route already specified for
//   Library/Browser/Mail's full-screen work — NOT the 'Expand opens a new
//   browser tab' route FR-008/US-6 describe. There is no separate full-page
//   route for Tasks/Team/Calendar."  (§ amendment header, item 1; FR-008
//   amended: "Tasks, Team and Calendar do NOT use this new-tab route")
//
// Wave 3 also registers Tasks/Team/Calendar as panels at all (SP-6, §10 Wave
// 3) — the registry below still holds only library/browser.
//
// Oracle sources: spec §10 Wave 3 table, §13 SP-38, FR-008 as amended, the
// § amendment header, wireframe §3/§4/§5 (chrome-less "← Back to chat").
//
// RED evidence (2026-10-04, read src/components/panel-shell/registry.tsx and
// usePanelShell.ts::expandActivePanel): `panels` holds only library/browser —
// getPanelDefinition('tasks'|'team'|'calendar') is undefined today, so the
// fullscreen route renders its InvalidPanel fallback for those ids, and
// expandActivePanel's only full-screen mechanism is `window.open` (the exact
// new-tab route SP-38 forbids for the three). Each test below fails against
// this code, and each is guarded so it can never pass vacuously once the
// panels are registered.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, waitFor } from '@testing-library/react'
import { renderHook } from '@testing-library/react'
import { createMemoryHistory, createRouter, RouterProvider } from '@tanstack/react-router'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={makeClient()}>{children}</QueryClientProvider>
}

// Keep the expand path deterministic: the real presence module owns
// BroadcastChannel/window.open coordination. This mirrors the established
// mock in usePanelShell.fullScreenUrl.test.tsx — the open callback still
// runs, so a GREEN implementation that keeps the new-tab expand for these
// panels is still caught by the window.open assertion below.
vi.mock('@/lib/panelTabPresence', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/panelTabPresence')>()),
  resolveRegisteredPanelOpen: ({ open }: { open: () => Window | null }) => {
    open()
    return { kind: 'opened' as const }
  },
}))

import { panels, getPanelDefinition } from './registry'
import { usePanelShell } from './usePanelShell'
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
    it(`navigating to /fullscreen/panel/${id} renders the chrome-less panel shell with "Back to chat"`, async () => {
      const history = createMemoryHistory({ initialEntries: [`/fullscreen/panel/${id}`] })
      const router = createRouter({ routeTree, history })
      const mounted = render(<RouterProvider router={router} />, { wrapper })
      await waitFor(() => expect(router.state.status).toBe('idle'), { timeout: 5_000 })

      // The shared chrome-less shell renders (SP-38: "the SAME chrome-less
      // 'Back to chat' shell route").
      expect(mounted.getByTestId('fullscreen-panel')).toBeInTheDocument()
      expect(
        mounted.getByRole('button', { name: /back to chat/i }),
      ).toBeInTheDocument()
      // Chrome-less: the app shell (header/nav chrome) is NOT mounted here.
      expect(mounted.queryByTestId('app-shell')).not.toBeInTheDocument()
      // Not the InvalidPanel fallback (which is what this route shows today,
      // because the panel is not registered).
      expect(mounted.queryByText(/can.t open this panel/i)).not.toBeInTheDocument()
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

    it(`expanding the docked ${id} panel does not window.open — it stays in-app (FR-008 as amended)`, async () => {
      // Anti-vacuous guard: the negative below is only meaningful once the
      // panel exists to expand. (Today this line fails first, with the
      // registry gap named — never a silent pass.)
      expect(getPanelDefinition(id)).toBeDefined()

      const { result } = renderHook(() => usePanelShell(panels, 'dana'))
      usePanelShellStore.getState().openPanel(id, { workspaceId: 'ws-1' })
      expect(usePanelShellStore.getState().activePanel?.id).toBe(id)

      await result.current.requestExpand()

      expect(openSpy).not.toHaveBeenCalled()
    })
  },
)

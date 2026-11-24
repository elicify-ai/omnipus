/**
 * saveBar.leaveGuard.test.tsx — Settings redesign #1519, RED.
 *
 * Test Implementation Order row 20 (FR-016/FR-019, leave prompt): with an
 * unsaved draft present, EVERY way out inside the app asks first — switching
 * sections, opening another screen, closing the browser tab. Inside the app
 * the guard shows the app's own confirm ("Leave without saving N change(s)?"
 * with Stay and Leave); leaving the page uses the browser's own prompt,
 * registered as a `beforeunload` listener. Without a draft nothing asks.
 *
 * Same BLOCKED-import pattern as saveBar.outcomes.test.tsx: the SaveBar
 * does not exist yet, so each test fails loudly with a BLOCKED message
 * naming its FR; after GREEN the real component loads and the assertions
 * run.
 *
 * The in-app case renders the REAL app — the real routeTree in a real
 * router (memory history), with the matched route tree (the app shell and
 * its sidebar) rendered by <Matches> and the SaveBar mounted INSIDE the
 * router context beside it — so the exit the oracle drives is the app's
 * own sidebar row (the way a person leaves Settings), and whatever way
 * GREEN arms the guard (router blocker, shell hook, save store) can see
 * the draft this mount creates. The same real-router pattern as
 * settingsRoute.legacyTabs.test.tsx.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
// Type-only: the runtime module loads through loadSaveBar's BLOCKED RED
// importActual; the erased type gives the field fixture the real contract.
import type { SaveBarField } from './useSaveBar'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  createRouter,
  createMemoryHistory,
  RouterContextProvider,
  Matches,
} from '@tanstack/react-router'
import { routeTree } from '@/routeTree.gen'

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
    // SettingsScreen section reads that render alongside the headings:
    fetchKnowledgeBaseInfo: vi.fn(async () => ({
      workspace_id: 'ws-1',
      root_path: '',
      is_knowledge_base: false,
      marker: 'none',
    })),
  }
})

async function loadSaveBar(): Promise<typeof import('@/components/settings/SaveBar')['SaveBar']> {
  try {
    const mod = await vi.importActual<typeof import('@/components/settings/SaveBar')>('@/components/settings/SaveBar')
    return mod.SaveBar ?? mod.default
  } catch {
    throw new Error(
      'BLOCKED RED (FR-016/FR-019): the Settings SaveBar component does not exist yet — ' +
        'required so every way out with an unsaved draft asks first',
    )
  }
}

const gatewayLogLevelField: SaveBarField = {
  key: 'gateway.log_level',
  label: 'Log level',
  value: 'info',
  kind: 'select',
  options: ['info', 'debug'],
}

/**
 * Opens the REAL app at Settings → Gateway with the SaveBar mounted inside
 * the router context, arms a draft by changing the field, and opens the
 * sidebar (it ships collapsed). Returns nothing — the caller drives the exit.
 */
async function openRealAppWithDraft() {
  const SaveBar = await loadSaveBar()
  window.localStorage.setItem('omnipus_auth_username', 'leaveguard-tester')
  // jsdom has no matchMedia — the same per-file stub
  // settingsRoute.legacyTabs.test.tsx uses (AppShell pins breakpoints).
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
  const router = createRouter({
    routeTree,
    history: createMemoryHistory({ initialEntries: ['/settings?tab=gateway'] }),
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <RouterContextProvider router={router}>
        {/* The REAL matched route tree: the app shell with its sidebar. */}
        <Matches />
        {/* The Save bar under test, inside the router context so any
            guard mechanism GREEN chooses can see this draft. */}
        <SaveBar
          section="gateway"
          fields={[gatewayLogLevelField]}
          onSave={vi.fn(async () => ({ ok: true, status: 200, json: async () => ({}) }))}
          onDiscard={vi.fn()}
        />
      </RouterContextProvider>
    </QueryClientProvider>,
  )
  await userEvent.selectOptions(screen.getByLabelText('Log level'), 'debug')
  // The sidebar ships collapsed; open it the way the person does.
  const showButtons = screen.getAllByRole('button', { name: 'Show sidebar' })
  await userEvent.click(showButtons[0])
  const group = await screen.findByRole('group', { name: 'Workspaces' }, { timeout: 5000 })
  return { router, workspaceRow: within(group).getByText('UAT Build') }
}

beforeEach(() => {
  vi.restoreAllMocks()
})

describe('FR-016/FR-019 — every way out with an unsaved draft asks first', () => {
  it('an in-app navigation away asks before leaving, and Stay keeps everything (no silent drop)', async () => {
    const { router, workspaceRow } = await openRealAppWithDraft()
    // The app shell's own way out: the sidebar's workspace row (the person
    // leaves Settings for their workspace chat).
    await userEvent.click(workspaceRow)
    // FR-014's question, with its Stay and Leave answers.
    expect(
      await screen.findByText(/leave without saving 1 change/i, {}, { timeout: 3000 }),
      'RED/BLOCKED (FR-014): the app must ask "Leave without saving 1 change(s)?" before the sidebar navigation lands',
    ).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Stay' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Leave' })).toBeInTheDocument()
    // Stay keeps the section, its changes, the address and the history
    // unchanged (FR-014) — the navigation must not have landed.
    await userEvent.click(screen.getByRole('button', { name: 'Stay' }))
    expect(screen.queryByText(/leave without saving/i)).not.toBeInTheDocument()
    // The message rides expect()'s own second argument — the matchers take
    // one argument, and the two-argument form fails `npm run typecheck`
    // against the frontend branch (TS2554, item 11 of the dispatch).
    expect(
      router.state.location.pathname,
      'Stay must keep the address (FR-014) — no silent drop',
    ).toBe('/settings')
    expect(screen.getByLabelText('Log level')).toHaveValue('debug')
  })

  it('closing or reloading the tab registers beforeunload (the browser asks)', async () => {
    const listener = vi.fn()
    window.addEventListener('beforeunload', listener)
    await renderWithDraft()
    // The Save bar must have registered its own beforeunload guard while a
    // draft exists.
    expect(listener).not.toHaveBeenCalled()
    const event = new Event('beforeunload', { cancelable: true })
    window.dispatchEvent(event)
    // The registered guard prevents the default (the browser then asks).
    expect(event.defaultPrevented).toBe(true)
    window.removeEventListener('beforeunload', listener)
  })

  it('with no draft nothing asks — navigating and unloading are free', async () => {
    const SaveBar = await loadSaveBar()
    render(
      <SaveBar
        section="gateway"
        fields={[gatewayLogLevelField]}
        onSave={vi.fn()}
        onDiscard={vi.fn()}
      />,
    )
    const event = new Event('beforeunload', { cancelable: true })
    window.dispatchEvent(event)
    expect(event.defaultPrevented).toBe(false)
    expect(screen.queryByText(/unsaved changes/i)).not.toBeInTheDocument()
  })
})

async function renderWithDraft() {
  const SaveBar = await loadSaveBar()
  render(
    <SaveBar
      section="gateway"
      fields={[gatewayLogLevelField]}
      onSave={vi.fn(async () => ({ ok: true, status: 200, json: async () => ({}) }))}
      onDiscard={vi.fn()}
    />,
  )
  await userEvent.selectOptions(screen.getByLabelText('Log level'), 'debug')
}

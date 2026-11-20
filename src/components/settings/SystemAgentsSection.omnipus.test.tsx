// WU-10 RED: spec FR-106/109/112, BDD-12.6/13/14, §6.3/6.8; ADR OSA-10 U9 (FQ-9 A).
// The new module path is named by spec §9.2 test 29. No section/editor/control mocks.
import type { ComponentType } from 'react'
import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createRouter, createMemoryHistory, RouterProvider } from '@tanstack/react-router'
import { routeTree } from '@/routeTree.gen'
import { AgentProfile } from '@/components/agents/AgentProfile'
import { useUiStore } from '@/store/ui'
import { ApiError } from '@/lib/api-error'
import { WsConnection } from '@/lib/ws'
import { useConnectionStore } from '@/store/connection'
import { JUDGE, MIA, OMNIPUS, PLAN_SUPERVISOR, REVISION } from '../../../tests/omnipus-l1-ui-red/fixtures'

vi.mock('@tanstack/react-router', async (original) => ({
  ...await original<typeof import('@tanstack/react-router')>(), useNavigate: () => vi.fn(),
}))
vi.mock('@/lib/api', async (original) => ({
  ...await original<typeof import('@/lib/api')>(),
  // The real route tree: _app.tsx beforeLoad + AppShell + Sidebar reads.
  fetchAppState: vi.fn(), validateToken: vi.fn(async () => ({})),
  fetchNotifications: vi.fn(async () => ({ notifications: [], unread_count: 0 })),
  fetchTasks: vi.fn(async () => []), fetchAgents: vi.fn(), fetchAgent: vi.fn(),
  updateAgent: vi.fn(), fetchProviders: vi.fn(),
  fetchVersion: vi.fn(async () => ({ version: 'test', build_sha: 'test' })),
  fetchWorkspaces: vi.fn(async () => [{ id: 'ws-1', name: 'UAT Build', status: 'active', is_default: true }]),
  fetchSessions: vi.fn(async () => []),
  fetchKnowledgeBaseInfo: vi.fn(async () => ({ workspace_id: 'ws-1', root_path: '', is_knowledge_base: false, marker: 'none' })),
  // SettingsScreen + SystemAgentsSection reads:
  fetchActivity: vi.fn(), fetchSkills: vi.fn(), fetchAboutInfo: vi.fn(), fetchPerformanceSettings: vi.fn(),
}))
import { fetchAgents, fetchAgent, updateAgent, fetchProviders, fetchActivity, fetchSkills, fetchAboutInfo, fetchPerformanceSettings, fetchAppState } from '@/lib/api'

// Module namespace is test-only, not a wire format. Missing implementation is a loud
// per-case failure, not a skipped test or an import/collection error.
const sectionModules = import.meta.glob<{ SystemAgentsSection: ComponentType }>('./SystemAgentsSection.tsx')
async function loadSection() {
  const load = sectionModules['./SystemAgentsSection.tsx']
  if (!load) expect.fail('BLOCKED: SystemAgentsSection.tsx not implemented — required by spec FR-106 / BDD-12.6 / §9.2 test 29')
  const module = await load()
  expect(module, 'spec §6.8 shared Settings section must expose SystemAgentsSection').toHaveProperty('SystemAgentsSection')
  return module.SystemAgentsSection
}

const SYSTEM_AGENTS = [JUDGE, PLAN_SUPERVISOR, { ...OMNIPUS, editable_fields: [
  { name: 'model', editable: true }, { name: 'provider', editable: true },
] }]
let client: QueryClient
let socket: WsConnection
let sends = vi.fn<WsConnection['send']>()

beforeEach(() => {
  vi.clearAllMocks()
  client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  useUiStore.setState({ editAgentId: null, editAgentWorkspaceId: null, toasts: [] })
  // The real _app route guard (used only by the navigation cases) needs an
  // onboarded app state.
  vi.mocked(fetchAppState).mockResolvedValue({ onboarding_complete: true } as never)
  vi.mocked(fetchAgents).mockResolvedValue([MIA, ...SYSTEM_AGENTS])
  vi.mocked(fetchAgent).mockImplementation(async (id) => SYSTEM_AGENTS.find((a) => a.id === id) ?? MIA)
  vi.mocked(updateAgent).mockImplementation(async (id, patch) => ({
    ...(SYSTEM_AGENTS.find((a) => a.id === id) ?? MIA),
    model: patch.model ?? '', provider: patch.provider, soul: patch.soul ?? '', revision: REVISION,
  }))
  vi.mocked(fetchProviders).mockResolvedValue([]) // BDD-12.6: no working provider
  vi.mocked(fetchActivity).mockResolvedValue({ events: [] })
  vi.mocked(fetchSkills).mockResolvedValue([])
  vi.mocked(fetchAboutInfo).mockResolvedValue({ version: '0.1.1', go_version: 'go1.26.9', os: 'darwin', arch: 'amd64', uptime_seconds: 0 })
  vi.mocked(fetchPerformanceSettings).mockResolvedValue({})
  socket = new WsConnection({ onConnected: () => {}, onDisconnected: () => {}, onError: () => {} })
  sends = vi.fn<WsConnection['send']>().mockReturnValue(true)
  vi.spyOn(socket, 'send').mockImplementation(sends)
  useConnectionStore.setState({ connection: socket, isConnected: true })
})
afterEach(() => {
  client.clear()
  socket.disconnect()
  useConnectionStore.setState({ connection: null, isConnected: false })
  vi.restoreAllMocks()
})
async function renderSection(withEditor = false) {
  const Section = await loadSection()
  render(<QueryClientProvider client={client}><Section />{withEditor && <AgentProfile />}</QueryClientProvider>)
  return Section
}
async function openOmnipusEditor() {
  await userEvent.setup().click(await screen.findByRole('button', { name: /edit omnipus/i }))
  expect(useUiStore.getState().editAgentId).toBe('omnipus')
  return screen.findByRole('dialog', { name: /omnipus/i })
}
async function editModel() {
  const user = userEvent.setup()
  const manual = screen.queryByRole('button', { name: /^enter manually$/i })
  if (manual) await user.click(manual) // Existing catalogued empty-catalog escape.
  const input = screen.getByRole('textbox', { name: /model slug/i })
  expect(input).toBeEnabled()
  await user.clear(input)
  await user.type(input, 'replacement-model')
  return input
}

describe('WU-10 System agents through the shared editor', () => {
  it('FR-106 lists exactly the three system roles, not ordinary Mia', async () => {
    await renderSection()
    for (const name of ['Judge', 'Plan Supervisor', 'Omnipus']) {
      expect(await screen.findByText(name, { exact: true })).toBeVisible()
      expect(screen.getByRole('button', { name: `Edit ${name}` })).toBeEnabled()
    }
    expect(screen.queryByText('Mia', { exact: true })).not.toBeInTheDocument()
  })
  it.each([['judge', 'Judge'], ['plansupervisor', 'Plan Supervisor'], ['omnipus', 'Omnipus']])(
    'FR-106 Edit %s opens the existing shared editor, not an agent chat', async (id, name) => {
      await renderSection(true)
      await userEvent.setup().click(await screen.findByRole('button', { name: `Edit ${name}` }))
      expect(useUiStore.getState().editAgentId).toBe(id)
      expect(await screen.findByRole('dialog', { name: new RegExp(name, 'i') })).toBeVisible()
      expect(sends.mock.calls).toEqual([])
    },
  )
  it.each([['judge', 'Judge'], ['plansupervisor', 'Plan Supervisor']])(
    'FR-106 preserves editable instructions for %s in the shared editor', async (id, name) => {
      await renderSection(true)
      const user = userEvent.setup()
      await user.click(await screen.findByRole('button', { name: `Edit ${name}` }))
      await screen.findByRole('dialog', { name: new RegExp(name, 'i') })
      await user.click(screen.getByRole('tab', { name: /^personality$/i }))
      const instructions = screen.getByTestId('agent-soul')
      expect(instructions).toBeEnabled()
      expect(instructions).not.toHaveAttribute('readonly')
      await user.clear(instructions)
      // One real paste exercises the long-form edit without timing every keystroke.
      await user.paste('Evaluate the supplied requirements and report the evidence.')
      await waitFor(() => expect(updateAgent).toHaveBeenCalledWith(id, expect.objectContaining({
        soul: 'Evaluate the supplied requirements and report the evidence.', revision: REVISION,
      })))
      expect(sends.mock.calls).toEqual([])
    },
  )
  it('BDD-12.6 changes and saves the Omnipus model with no working provider and no model turn', async () => {
    await renderSection(true)
    await openOmnipusEditor()
    await editModel()
    await waitFor(() => expect(updateAgent).toHaveBeenCalledWith('omnipus', expect.objectContaining({
      model: 'replacement-model', revision: REVISION,
    })))
    expect(sends.mock.calls).toEqual([])
  })
  it('BDD-12.13 first load error shows Retry, not an empty system section', async () => {
    vi.mocked(fetchAgents).mockRejectedValue(new Error('System roster offline'))
    await renderSection()
    expect(await screen.findByRole('button', { name: /^retry$/i })).toBeEnabled()
    expect(screen.getByText(/failed|could not|unavailable/i)).toBeVisible()
    expect(screen.queryByRole('button', { name: /edit omnipus/i })).not.toBeInTheDocument()
  })
  it('FR-112 Retry restores system roles after a failed first load', async () => {
    vi.mocked(fetchAgents).mockRejectedValueOnce(new Error('System roster offline')).mockResolvedValue(SYSTEM_AGENTS)
    await renderSection()
    await userEvent.setup().click(await screen.findByRole('button', { name: /^retry$/i }))
    expect(await screen.findByRole('button', { name: /edit omnipus/i })).toBeEnabled()
    expect(screen.queryByText(/System roster offline/)).not.toBeInTheDocument()
  })
  it('FR-112 a refresh error keeps cached roles with a stale hint', async () => {
    await renderSection()
    await screen.findByRole('button', { name: /edit omnipus/i })
    vi.mocked(fetchAgents).mockRejectedValue(new Error('System roster refresh offline'))
    await act(async () => { await client.invalidateQueries({ queryKey: ['agents'] }) })
    // Check both original postconditions after the deferred observer render.
    await waitFor(() => {
      expect(screen.getByRole('button', { name: /edit omnipus/i })).toBeVisible()
      expect(screen.getByText(/may be out of date|last known|stale/i)).toBeVisible()
    })
  })
  it('BDD-12.13 an uncertain model save says not confirmed and offers Retry, never Saved', async () => {
    vi.mocked(updateAgent).mockRejectedValue(new ApiError(0, 'Response lost after submit'))
    await renderSection(true)
    await openOmnipusEditor()
    await editModel()
    await waitFor(() => expect(updateAgent).toHaveBeenCalledWith('omnipus', expect.objectContaining({ model: 'replacement-model' })))
    expect(await screen.findByText(/not confirmed/i)).toBeVisible()
    expect(screen.getByRole('button', { name: /^retry$/i })).toBeEnabled()
    expect(screen.queryByText(/^saved$/i)).not.toBeInTheDocument()
  })
  it('FR-112 loading announces the pending state, not a fabricated empty roster', async () => {
    vi.mocked(fetchAgents).mockReturnValue(new Promise(() => {}))
    await renderSection()
    expect(screen.getByRole('status')).toHaveTextContent(/loading/i)
    expect(screen.queryByText(/no system agents/i)).not.toBeInTheDocument()
  })
})

describe('WU-10 Settings navigation is reachable, not just a standalone section', () => {
  // Settings redesign #1519 reconciliation: the old tab bar and the
  // `initialTab` prop are replaced by FR-001/FR-002 — 11 sections listed in
  // three groups as links, the open one named in the address as `?tab=<id>`
  // (D-3 row 2: `system-agents`) with `aria-current` on the open link. The
  // cases run the REAL routeTree in a REAL router (the same harness as
  // settingsRoute.legacyTabs.test.tsx), so the address rule is live.
  async function openSettings(search: string) {
    // AppShell pins breakpoints (matchMedia) and the _app guard wants a
    // signed-in username — scoped here so the standalone editor cases below
    // keep their plain environment.
    window.localStorage.setItem('omnipus_auth_username', 'system-agents-tester')
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
      history: createMemoryHistory({ initialEntries: [`/settings${search}`] }),
    })
    render(
      <QueryClientProvider client={client}>
        <RouterProvider router={router} />
      </QueryClientProvider>,
    )
  }

  it('FR-106 exposes System agents in the section list and opens it from ?tab=system-agents', async () => {
    await openSettings('?tab=system-agents')
    // FR-001: the section list holds System agents as a link; FR-002/D-3
    // row 2: the id names the open section, marked aria-current.
    const link = await screen.findByRole('link', { name: /^system agents$/i })
    expect(link).toHaveAttribute('aria-current', 'page')
    // The section really opened: the system roster is reachable.
    expect(await screen.findByRole('button', { name: /edit omnipus/i })).toBeEnabled()
  })

  it('BDD-12.14 enters System agents by keyboard from the section list', async () => {
    await openSettings('') // no section named — Providers opens (FR-001)
    const link = await screen.findByRole('link', { name: /^system agents$/i })
    expect(link).not.toHaveAttribute('aria-current', 'page')
    link.focus()
    await userEvent.setup().keyboard('{Enter}')
    // The Enter activation lands on System agents: the link becomes the
    // current one and the roster renders.
    await waitFor(() => expect(link).toHaveAttribute('aria-current', 'page'))
    expect(await screen.findByRole('button', { name: /edit omnipus/i })).toBeEnabled()
  })
})

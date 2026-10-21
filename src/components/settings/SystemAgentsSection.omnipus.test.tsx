// WU-10 RED: spec FR-106/109/112, BDD-12.6/13/14, §6.3/6.8; ADR OSA-10 U9 (FQ-9 A).
// The new module path is named by spec §9.2 test 29. No section/editor/control mocks.
import type { ComponentType } from 'react'
import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { SettingsScreen } from '@/components/screens/SettingsScreen'
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
  fetchAgents: vi.fn(), fetchAgent: vi.fn(), updateAgent: vi.fn(), fetchProviders: vi.fn(),
  fetchActivity: vi.fn(), fetchSkills: vi.fn(), fetchAboutInfo: vi.fn(), fetchPerformanceSettings: vi.fn(),
}))
import { fetchAgents, fetchAgent, updateAgent, fetchProviders, fetchActivity, fetchSkills, fetchAboutInfo, fetchPerformanceSettings } from '@/lib/api'

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

describe('WU-10 Settings navigation is reachable, not just a standalone section', () => {
  it('FR-106 exposes the System agents tab in the real Settings screen', () => {
    render(<QueryClientProvider client={client}><SettingsScreen initialTab="chat" /></QueryClientProvider>)
    expect(screen.getByRole('tab', { name: /^system agents$/i })).toBeVisible()
  })
  it('BDD-12.14 enters System agents by keyboard from the real Settings screen', async () => {
    render(<QueryClientProvider client={client}><SettingsScreen initialTab="chat" /></QueryClientProvider>)
    const tab = screen.getByRole('tab', { name: /^system agents$/i })
    tab.focus()
    await userEvent.setup().keyboard('{Enter}')
    expect(tab).toHaveAttribute('aria-selected', 'true')
    expect(await screen.findByRole('button', { name: /edit omnipus/i })).toBeEnabled()
  })
})

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

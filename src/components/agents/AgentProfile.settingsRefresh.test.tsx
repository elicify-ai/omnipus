/** T11 §4 / FR-8: Runtime projects CURRENT Settings values, not an agent
 * timestamp snapshot. The Settings HTTP client, query cache and Profile are
 * real. Only transport/API reads and router context are fixtures. Settings
 * click/Save interaction is covered in ThirdPartyAgentsSection.test.tsx.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ReactNode } from 'react'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { Agent, ThirdPartyAgentSettings, ThirdPartyAgentSettingsView } from '@/lib/api/generated/openapi-types'
import { AgentProfile } from './AgentProfile'
import { useUiStore } from '@/store/ui'

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return { ...actual, useNavigate: () => vi.fn(), useParams: () => ({}),
    Link: ({ children, to }: { children?: ReactNode; to?: string }) => <a href={to}>{children}</a>,
  }
})
vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual,
    fetchAgent: vi.fn(), fetchSkills: vi.fn(), fetchProviders: vi.fn(), fetchActivity: vi.fn(),
    fetchCliDetect: vi.fn(), fetchExecutorPreview: vi.fn(), updateAgent: vi.fn(),
  }
})
import {
  fetchAgent, fetchSkills, fetchProviders, fetchActivity, fetchCliDetect,
  fetchExecutorPreview, updateAgent, updateThirdPartyAgentSettings,
} from '@/lib/api'

// §4 specifies this no-saved-path warning; no Q48 path-bearing refusal copy.
const NO_PATH_WARNING = 'This agent cannot run: no program path is set for Codex. A person can set it in Settings → Third-party agents → Codex.'
const UPDATED_AT = '2026-11-09T09:00:00Z'
const OLD_PROGRAM = '/opt/test-clis/codex-previous'
const NEW_PROGRAM = '/opt/test-clis/codex-current'
const OLD_PARAMS = '--model previous-person-model'
const NEW_PARAMS = '--model current-person-model'

function worker(path: string, parameters: string): Agent {
  return {
    revision: '0'.repeat(64), id: 'settings-worker', name: 'Settings Worker', type: 'subagent_3p',
    updated_at: UPDATED_AT, editable_fields: [{ name: 'executor', editable: true }, { name: 'name', editable: true }],
    locked: false, needs_model: false, status: 'active', description: '', soul: '',
    max_tool_iterations: 20, max_tool_iterations_source: 'global', max_tool_iterations_override_ignored: false,
    executor: { kind: 'external-cli', cli: 'codex', effective_cli_path: path || undefined,
      effective_preset_args: parameters, program_warning: path ? undefined : NO_PATH_WARNING },
    memory_enabled: true, figure: 'Omnipus', role: 'general',
  }
}
function settings(path: string, parameters: string): ThirdPartyAgentSettings {
  return { clis: {
    'claude-code': { path: '', preset_args: '' }, codex: { path, preset_args: parameters },
    opencode: { path: '', preset_args: '' },
  } }
}
function savedView(body: ThirdPartyAgentSettings): ThirdPartyAgentSettingsView {
  const detected = { installed: false, path: null, link_path: null, source: null }
  return { clis: {
    'claude-code': { path: '', preset_args: '', detected },
    codex: { path: body.clis.codex?.path ?? '', preset_args: body.clis.codex?.preset_args ?? '', detected },
    opencode: { path: '', preset_args: '', detected },
  } }
}
let client: QueryClient
beforeEach(() => {
  vi.mocked(fetchAgent).mockReset()
  vi.mocked(fetchSkills).mockReset().mockResolvedValue([])
  vi.mocked(fetchProviders).mockReset().mockResolvedValue([])
  vi.mocked(fetchActivity).mockReset().mockResolvedValue({ events: [] })
  vi.mocked(fetchCliDetect).mockReset().mockResolvedValue({
    claude: { installed: false, path: null, source: null },
    codex: { installed: false, path: null, source: null },
    opencode: { installed: false, path: null, source: null },
  })
  vi.mocked(fetchExecutorPreview).mockReset().mockResolvedValue({
    binary: 'codex', argv: [], command_line: 'codex', prompt_delivery: 'stdin', dropped_args: [],
  })
  vi.mocked(updateAgent).mockReset()
  client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 30_000 } } })
  useUiStore.setState({ toasts: [], editAgentWorkspaceId: null })
  document.cookie = 'csrf=t11-r6-person-session; path=/'
})
afterEach(() => {
  cleanup()
  client.clear()
  vi.unstubAllGlobals()
  document.cookie = 'csrf=; path=/; max-age=0'
  useUiStore.setState({ toasts: [] })
})

function assertWarning(path: string) {
  if (path) expect(screen.queryByTestId('profile-program-warning')).not.toBeInTheDocument()
  else expect(screen.getByTestId('profile-program-warning').textContent).toBe(NO_PATH_WARNING)
}

describe('Runtime after Settings save with unchanged agent updated_at (T11 §4; FR-8)', () => {
  it.each([
    { name: 'new program', before: OLD_PROGRAM, after: NEW_PROGRAM, field: 'program' },
    { name: 'new standard parameters', before: OLD_PROGRAM, after: OLD_PROGRAM, field: 'parameters' },
    { name: 'warning removed after repair', before: '', after: NEW_PROGRAM, field: 'warning' },
    { name: 'warning added after clearing', before: OLD_PROGRAM, after: '', field: 'warning' },
  ])('shows $name from the refetched effective executor', async ({ before, after, field }) => {
    let serverAgent = worker(before, OLD_PARAMS)
    vi.mocked(fetchAgent).mockImplementation(async () => serverAgent)
    const saves: ThirdPartyAgentSettings[] = []
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = new URL(input instanceof Request ? input.url : String(input), window.location.origin).pathname
      if (path !== '/api/v1/settings/third-party-agents' || init?.method !== 'PUT') {
        throw new Error(`Unexpected HTTP boundary call: ${init?.method ?? 'GET'} ${path}`)
      }
      const body = JSON.parse(String(init.body)) as ThirdPartyAgentSettings
      saves.push(body)
      const saved = savedView(body)
      serverAgent = worker(saved.clis.codex.path, saved.clis.codex.preset_args)
      return new Response(JSON.stringify(saved), { status: 200, headers: { 'Content-Type': 'application/json' } })
    }))
    render(<QueryClientProvider client={client}><AgentProfile agentId={serverAgent.id} /></QueryClientProvider>)
    await screen.findByText(serverAgent.name)
    const runtime = screen.getByTestId('tab-runtime')
    act(() => { runtime.focus() })
    fireEvent.keyDown(runtime, { key: 'Enter' })
    fireEvent.click(runtime)
    // Positive control: the first server view really hydrated the Runtime tab.
    await waitFor(() => expect(screen.getByTestId('profile-program-path').textContent).toBe(before || 'Not set'))
    expect(screen.getByTestId('profile-preset-args').textContent).toBe(OLD_PARAMS)
    assertWarning(before)
    // Settle the real, debounced sibling before driving the Settings change.
    // Wait for an observed state, not a sleep or a suppressed act warning.
    await waitFor(() => expect(screen.getByTestId('profile-command-preview')).toHaveAttribute('data-preview-status', 'ready'))

    const body = settings(after, NEW_PARAMS)
    const saved = await updateThirdPartyAgentSettings(body)
    expect(saves).toEqual([body])
    expect(saved.clis.codex.path).toBe(after)
    expect(saved.clis.codex.preset_args).toBe(NEW_PARAMS)
    expect(serverAgent.updated_at).toBe(UPDATED_AT)
    await act(async () => { await client.refetchQueries({ queryKey: ['agent', serverAgent.id], exact: true }) })
    expect(fetchAgent).toHaveBeenCalledTimes(2)
    const cached = client.getQueryData<Agent>(['agent', serverAgent.id])
    expect(cached?.updated_at).toBe(UPDATED_AT)
    expect(cached?.executor).toEqual(worker(after, NEW_PARAMS).executor)

    // Check the row's changed field first, so each RED names its own defect.
    await waitFor(() => {
      if (field === 'warning') assertWarning(after)
      if (field === 'parameters') expect(screen.getByTestId('profile-preset-args').textContent).toBe(NEW_PARAMS)
      expect(screen.getByTestId('profile-program-path').textContent).toBe(after || 'Not set')
      expect(screen.getByTestId('profile-preset-args').textContent).toBe(NEW_PARAMS)
      assertWarning(after)
    })
    expect(screen.getByTestId('profile-cli-path').querySelectorAll('input, textarea, [contenteditable]')).toHaveLength(0)
    expect(updateAgent).not.toHaveBeenCalled()
  })
})

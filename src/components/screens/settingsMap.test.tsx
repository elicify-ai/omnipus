/**
 * settingsMap.test.tsx — Settings redesign #1519, RED.
 *
 * Test Implementation Order row 27 (spec order; brief row 28: Every
 * inventory control is where the map says, Dataset D-9, FR-051, FR-053,
 * FR-055, FR-058, FR-060, FR-061, S-Q3/S-Q12, FR-108 rider).
 *
 * Oracles (spec, never code):
 *   - FR-001: 11 sections in three groups — AI & agents (Providers, Memory &
 *     recaps, System agents, Third-party agents, Limits); Security
 *     (Protection, Sandbox & network, Keys & secrets); System (Gateway, Data
 *     & retention, About).
 *   - D-9 rows 1-2 / FR-058 / S-Q12 A: Limits holds the three groups
 *     "Context and tool results", "Model overrides" (collapsed),
 *     "Agents and goals".
 *   - D-9 row 5 / FR-053: Providers holds a "Voice input" group with Active
 *     transcriber.
 *   - D-9 rows 4+6 / FR-060: Web search remains under Providers for now;
 *     the skill-trust/tool grid is temporarily unavailable, with no Settings
 *     entry or promised replacement location. This supersedes row 6 under
 *     Q1 A in /workspace/omnipus-coordination/decisions/2026-11-23-founder-q1-q2.md
 *     and team-lead ruling 2026-11-23 implementing Q1 A (part 32).
 *   - D-9 row 8 / FR-051: God Mode is a button in Protection (and Gateway
 *     has no God Mode control).
 *   - D-9 row 12 / S-Q3: the tool-loading switch stays in Limits → Agents
 *     and goals.
 *   - D-9 rows 10-11 / FR-055 / FR-061: no Backup/Restore; no Devices even
 *     when the pairing flag is on.
 *   - FR-108 rider: the Danger zone row says "Clear all chat history".
 *
 * Harness: the REAL routeTree in a REAL router (row-18 pattern). Reads that
 * an assertion does not need are left unmocked — they fail into the
 * section's error state instead of crashing. RED today: the current screen
 * is 13 flat tabs — the grouped list, the Limits groups, the Protection
 * notice, the God Mode placement and the new Data & retention wording do
 * not exist, so the assertions fail naming FR/D-9.
 */

import { describe, it, expect, vi, beforeAll, beforeEach } from 'vitest'
import { cleanup, render, screen, fireEvent, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createRouter, createMemoryHistory, RouterProvider } from '@tanstack/react-router'
import { routeTree } from '@/routeTree.gen'
import { rotateGatewayToken } from '@/lib/api'
import userEvent from '@testing-library/user-event'
import { appG1, jsonG1, transportG1 } from '../../../tests/helpers/settings-gate-g1'
import { useUiStore } from '@/store/ui'
import { makeAgent } from '@/test/factories'
import type { Workspace } from '@/lib/api/generated/openapi-types'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    // Shell reads (row-18 harness set).
    fetchAppState: vi.fn(async () => ({ onboarding_complete: true })),
    validateToken: vi.fn(async () => ({})),
    fetchNotifications: vi.fn(async () => ({ notifications: [], unread_count: 0 })),
    fetchTasks: vi.fn(async () => []),
    fetchAgents: vi.fn(async () => []),
    fetchVersion: vi.fn(async () => ({ version: '1.2.3', build_sha: 'abc1234' })),
    fetchWorkspaces: vi.fn(async () => [
      { id: 'ws-1', name: 'UAT Build', status: 'active', is_default: true },
    ]),
    fetchSessions: vi.fn(async () => []),
    fetchKnowledgeBaseInfo: vi.fn(async () => ({
      workspace_id: 'ws-1',
      root_path: '',
      is_knowledge_base: false,
      marker: 'none',
    })),
    fetchAboutInfo: vi.fn(async () => ({
      version: '1.2.3',
      go_version: 'go1.24',
      os: 'linux',
      arch: 'amd64',
      uptime_seconds: 42,
      device_pairing_enabled: true, // FR-055: hidden even when the flag is on
    })),
    // Reads that must SUCCEED for an asserted control to render.
    fetchGodMode: vi.fn(async () => ({
      enabled: false,
      persisted: false,
      available: true,
      supported: true,
    })),
    fetchConfig: vi.fn(async () => ({ gateway: {}, data: { session_retention_days: 90 } })),
    fetchStorageStats: vi.fn(async () => ({
      workspace_size_bytes: 10,
      session_count: 3,
      memory_entry_count: 5,
      warnings: [],
    })),
    // The section's future retention read (FR-041 shape).
    fetchRetentionSettings: vi.fn(async () => ({
      session_days: 90,
      memory_retros_days: 180,
      memory_retros_forever: false,
      keep_everything_forever: false,
    })),
    // Limits reads (FR-058 groups render from these at GREEN).
    getContextSettings: vi.fn(async () => ({
      mcp_result_cap: 20,
      builtin_success_cap: 5,
      model_overrides: [],
    })),
    fetchPerformanceSettings: vi.fn(async () => ({
      max_parallel_agents: 4,
      effective_max_parallel_agents: 4,
      max_parallel_agents_configured: true,
      tools_on_demand: false,
      goal_max_rounds: 20,
      goal_reminder_quiet_seconds: 60,
      goal_stalled_checkin_enabled: true,
      goal_stalled_checkin_minutes: 10,
      max_tool_iterations: 50,
    })),
    // The read behind the Providers tab's "Voice input and web search" group
    // (D-9 row 5): without it the section stays in its loading/error state
    // and no Voice input row renders (the harness gap this test died of —
    // its Voice input lookup timed out, not a spec oracle).
    fetchIntegrationProviders: vi.fn(async () => ({
      search: [
        { id: 'brave', kind: 'search', display_name: 'Brave', configured: true, requires_key: true, usable: true, active: true },
      ],
      voice: [
        { id: 'elevenlabs', kind: 'voice', display_name: 'ElevenLabs', configured: true, requires_key: true, usable: true, active: true },
      ],
      active_search: 'brave',
      active_voice: 'elevenlabs',
      default_search: 'brave',
      fallback_search: null,
    })),
    // FR-057: the rotate call must be observable — Cancel keeps the token,
    // so the mock records and the test asserts it was never called.
    rotateGatewayToken: vi.fn(async () => ({ token: 'rotated-token' })),
    // Section reads the FR-073 sweep's witnesses depend on (R2-06): without
    // them the tab renders no section content at all and the sweep would
    // pass on an empty screen. Shapes follow the generated contract types.
    fetchMemorySettings: vi.fn(async () => ({})), // formFromSettings applies defaults
    fetchProviders: vi.fn(async () => []),
    getThirdPartyAgentSettings: vi.fn(async () => ({
      clis: {
        codex: { path: '', preset_args: '', detected: { installed: false } },
        'claude-code': { path: '', preset_args: '', detected: { installed: false } },
        opencode: { path: '', preset_args: '', detected: { installed: false } },
      },
    })),
    fetchSandboxStatus: vi.fn(async () => ({
      backend: 'landlock',
      available: true,
      kernel_level: true,
      policy_applied: true,
      seccomp_enabled: true,
      bind_ports_count: 0,
    })),
    fetchSandboxConfig: vi.fn(async () => ({
      mode: 'permissive',
      allowed_paths: [],
      ssrf: { allow_internal: [] },
      requires_restart: false,
    })),
    fetchCredentials: vi.fn(async () => []),
  }
})

async function openSettings(search: string) {
  const router = createRouter({
    routeTree,
    history: createMemoryHistory({ initialEntries: [`/settings${search}`] }),
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
}

// The registry lazy-loads this REAL panel. Cold transpilation is not the
// overlay behavior: load its actual chunk before the interaction, following
// the existing panel-shell real-route tests, rather than widening a timeout.
beforeAll(async () => { await import('@/components/workspaces/team/TeamPanel') })

beforeEach(() => {
  window.localStorage.setItem('omnipus_auth_username', 'map-tester')
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

describe('FR-001 / D-9 — the map: 11 sections in three groups', () => {
  it('the section list holds the three groups with their 11 sections and no Chat/Devices/Models tabs', async () => {
    await openSettings('')
    expect(await screen.findByText('AI & agents')).toBeInTheDocument()
    expect(screen.getByText('Security')).toBeInTheDocument()
    expect(screen.getByText('System')).toBeInTheDocument()
    for (const section of [
      'Providers',
      'Memory & recaps',
      'System agents',
      'Third-party agents',
      'Limits',
      'Protection',
      'Sandbox & network',
      'Keys & secrets',
      'Gateway',
      'Data & retention',
      'About',
    ]) {
      expect(screen.getByRole('link', { name: section }),
        `BLOCKED RED (FR-001/D-9): section ${section} must be in the grouped list`).toBeInTheDocument()
    }
    // Moved/hidden sections are absent from the list (FR-054, FR-055, D-3).
    expect(screen.queryByRole('link', { name: 'Chat' })).not.toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'Devices' })).not.toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'Models' })).not.toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'Performance' })).not.toBeInTheDocument()
  })
})

describe('D-9 rows 1-2, row 12 — Limits holds the three groups', () => {
  it('Context and tool results, Model overrides (collapsed) and Agents and goals', async () => {
    await openSettings('?tab=limits')
    expect(await screen.findByRole('heading', { name: 'Limits', level: 2 })).toBeInTheDocument()
    // FR-072 / S-Q12 A: every grouped list starts collapsed.
    for (const group of ['Context and tool results', 'Model overrides', 'Agents and goals']) {
      expect(
        screen.getByRole('button', { name: new RegExp(group, 'i') }),
        `BLOCKED RED (FR-072): the "${group}" group must render its collapsed toggle`,
      ).toHaveAttribute('aria-expanded', 'false')
    }
    // D-9 row 12: the tool-loading switch stays in Limits → Agents and goals.
    // FR-072: a collapsed group renders no body, so the row is asserted after
    // expanding the group. The row's name is the wireframe's ("Tool loading",
    // one row description) — the old per-state copy "Load tools on demand" is
    // retired (see PerformanceSection.test.tsx, "Tool loading" block).
    const agentsToggle = screen.getByRole('button', { name: /Agents and goals/i })
    fireEvent.click(agentsToggle)
    const toggle = await screen.findByLabelText(/tool loading/i)
    expect(toggle).toBeInTheDocument()
    expect(screen.getByText(/Load tools only when an agent needs them/)).toBeInTheDocument()
    // D-9 rows 3, 13: no per-control save buttons — the Save bar owns saving.
    expect(screen.queryAllByRole('button', { name: /^save$/i })).toHaveLength(0)
  })
})

describe('D-9 rows 5-6, row 8 — Providers voice, moved-out skills, God Mode', () => {
  it('Providers holds a Voice input group with Active transcriber (D-9 row 5)', async () => {
    await openSettings('?tab=providers')
    expect(await screen.findByText('Voice input')).toBeInTheDocument()
    expect(screen.getByText('Active transcriber')).toBeInTheDocument()
    // TEAM-LEAD ACCEPTED STATE (2026-11-20, part-9 brief): FR-060 stays as
    // written, but web search remains under Providers until Skills & Tools
    // carries it — the same "Removed later" disposition as D-9 row 12's
    // tool-loading switch — with the doc lines (DOC-007/DOC-011) saying so.
    // The old oracle asserted absence; the accepted present state is pinned
    // here (the tab's "Web Search" section heading) so the move-out (FR-060)
    // flips this test when it lands.
    expect(screen.getByText('Web Search')).toBeInTheDocument()
  })

  it('Q1 A / D-9 row 6: Protection names the temporary tool-permission and skill-trust gap without a Skills link', async () => {
    await openSettings('?tab=protection')
    // Retain the working-section witness and the D-9 row 8 / FR-051 oracle.
    expect(
      await screen.findByRole('button', { name: /Turn (on|off) God Mode/i }),
    ).toBeInTheDocument()
    const notice = await screen.findByTestId('protection-moved-permissions')
    expect(notice, 'instrument: inspect the real Protection callout, not an unloaded screen').toBeVisible()
    expect.soft(notice, 'Q1 A: the callout names the unavailable tool permissions').toHaveTextContent(/tool(?: and skill)? permissions|tool permission grid/i)
    expect.soft(notice, 'Q1 A: the callout names the unavailable skill trust').toHaveTextContent(/skill trust/i)
    expect.soft(notice, 'Q1 A: describe the temporary gap').toHaveTextContent(/temporarily unavailable/i)
    expect.soft(notice, 'Q1 A: name the update that ends the gap').toHaveTextContent(/until the Skills & Tools update lands/i)
    expect.soft(notice, 'Q1 A: do not claim the controls have already moved').not.toHaveTextContent(/moved|set on that page now/i)
    const destinations = within(notice).queryAllByRole('link').map((link) => new URL(link.getAttribute('href')!, window.location.origin).pathname)
    expect.soft(destinations, 'Q1 A: there is no temporary link to the Skills page').not.toEqual(expect.arrayContaining([expect.stringMatching(/^\/skills(?:\/|$)/)]))
  })

  it('Gateway has no God Mode control (FR-051)', async () => {
    await openSettings('?tab=gateway')
    // FR-051 (spec:1968) means God Mode absent FROM a working Gateway
    // section — not a section that failed to render passing as compliant.
    // The old oracle swallowed the heading lookup's rejection
    // (.catch(() => {})), so Gateway rendering nothing at all still passed.
    // The section and one of its real controls must both be present before
    // the absence assertion can mean anything.
    expect(await screen.findByRole('heading', { name: 'Gateway', level: 2 })).toBeInTheDocument()
    expect(await screen.findByLabelText('Log level')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /God Mode/i })).not.toBeInTheDocument()
  })
})

describe('D-9 rows 10-11 + FR-108 rider — removals, hidden devices, wording', () => {
  it('Data & retention has no Backup/Restore and says "Clear all chat history" (FR-061, FR-108)', async () => {
    await openSettings('?tab=data')
    expect(await screen.findByText(/Clear all chat history/)).toBeInTheDocument()
    expect(screen.queryByText(/Backup/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/Restore/i)).not.toBeInTheDocument()
  })

  it('the About build row shows the build sha (rider #1560)', async () => {
    await openSettings('?tab=about')
    expect(await screen.findByText('abc1234')).toBeInTheDocument()
  })
})

describe('FR-057 — rotating the gateway token asks first', () => {
  it('the confirm names the sign-out consequence; Cancel keeps the token', async () => {
    await openSettings('?tab=gateway')
    expect(await screen.findByRole('heading', { name: 'Gateway', level: 2 })).toBeInTheDocument()
    const rotate = await screen.findByRole('button', { name: 'Rotate gateway token' })
    fireEvent.click(rotate)
    // FR-057: the confirm must SAY what happens — every app and program
    // using the token is signed out (must sign in again with the new one).
    const confirm = await screen.findByRole('alertdialog', { name: /rotate the gateway token/i })
    expect(confirm, 'RED (FR-057): the confirm must name the signed-out consequence').toHaveTextContent(
      /every app and program/i,
    )
    expect(confirm, 'RED (FR-057): the confirm must say the sign-in consequence').toHaveTextContent(/sign/i)
    // Cancel keeps the token: the dialog closes and NO rotate call fires.
    fireEvent.click(within(confirm).getByRole('button', { name: /cancel/i }))
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(rotateGatewayToken, 'FR-057: Cancel keeps the token — no rotate call').not.toHaveBeenCalled()
  })
})

// PTA-03 must not inherit this file's older API-function stubs: forward each
// mocked export to the actual implementation for this one test, then restore
// it. Only transportG1's fetch replacement supplies data to the guard.
async function withRealG1API(run: () => Promise<void>) {
  const api = await import('@/lib/api')
  const actual = await vi.importActual<typeof api>('@/lib/api')
  const restore: (() => void)[] = []
  for (const [name, fn] of Object.entries(api)) {
    if (!vi.isMockFunction(fn)) continue
    const previous = fn.getMockImplementation()
    fn.mockImplementation(Reflect.get(actual, name))
    restore.push(() => { if (previous) fn.mockImplementation(previous); else fn.mockReset() })
  }
  try { await run() } finally {
    cleanup()
    for (const reset of restore) reset()
    vi.unstubAllGlobals()
    useUiStore.getState().closeEditAgentSlideOver()
    useUiStore.getState().closePanel()
    for (const toast of useUiStore.getState().toasts) useUiStore.getState().removeToast(toast.id)
  }
}

it('PTA-03 FR-109 — following the real worker Runtime link removes the shared editor and overlays from Settings', async () => {
  await withRealG1API(async () => {
    const t = transportG1()
    // Explicit desktop viewport: AgentProfile exposes Runtime as tabs above
    // 40rem and as an accordion below it; do not query an aria-hidden tab.
    vi.stubGlobal('matchMedia', (query: string) => ({ matches: query === '(min-width: 40rem)', media: query, onchange: null, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false }))
    vi.stubGlobal('scrollTo', () => {})
    // This path mounts Chat to reach its real Team panel, but sends no chat
    // frame. Keep the unrelated socket at the transport edge unconnected;
    // any unexpected send/parse request is loud, not silently accepted.
    vi.stubGlobal('WebSocket', class {
      static CONNECTING = 0; static OPEN = 1; static CLOSING = 2; static CLOSED = 3
      readyState = 0
      send() { throw new Error('PTA-03 must not send a chat frame') }
      close() { this.readyState = 3 }
    })
    vi.stubGlobal('Worker', class {
      postMessage() { throw new Error('PTA-03 must not parse a chat frame') }
      terminate() {}
    })
    const worker = makeAgent({
      id: 'g1-overlay-worker', name: 'G1 overlay worker', type: 'subagent_3p',
      executor: { kind: 'external-cli', cli: 'codex', effective_cli_path: '/opt/bin/Codex', effective_preset_args: '--profile Work' },
    })
    const workspace: Workspace = {
      id: 'ws-g1', name: 'G1 workspace', revision: '0'.repeat(64),
      status: 'active', pinned: false, pin_order: 0, task_count: 0,
      created_at: '2026-11-22T00:00:00Z', updated_at: '2026-11-22T00:00:00Z',
      core_team: [worker.id], is_default: true,
    }
    t.on('GET', '/agents', () => jsonG1([worker]))
    t.on('GET', `/agents/${worker.id}`, () => jsonG1(worker))
    t.on('GET', '/workspaces', () => jsonG1([workspace]))
    t.on('GET', `/workspaces/${workspace.id}`, () => jsonG1(workspace))
    t.on('GET', '/activity', () => jsonG1({ events: [] }))
    t.on('GET', '/skills', () => jsonG1([]))
    t.on('GET', '/sessions', () => jsonG1({ sessions: [] }))
    t.on('GET', `/workspaces/${workspace.id}/delegation`, () => jsonG1({ revision: workspace.revision, workspace_id: workspace.id, edges: [], team: [worker.id], default_depth: 3 }))
    // Preview computes argv but stores nothing. It may run while Runtime is
    // open, so serve its real contract rather than racing its 400ms debounce.
    t.on('POST', '/agents/executor-preview', () => jsonG1({ binary: '/opt/bin/Codex', argv: ['--profile', 'Work'], command_line: '/opt/bin/Codex --profile Work', prompt_delivery: 'stdin', dropped_args: [] }))
    // This is a genuine external worker, not a native System agent relabelled
    // to expose a link. The same reachable editor path is used by the existing
    // FR-109 browser guard; native editors currently contain no section link.
    const { router } = await appG1(`/workspaces/${workspace.id}/team`)
    await waitFor(() => {
      expect(router.state.location.pathname, 'PTA-03 instrument: the real Team bookmark redirects to workspace chat').toBe(`/workspaces/${workspace.id}/chat`)
      expect(useUiStore.getState().activePanel, `PTA-03 instrument: the Team link must open its panel; reads ${JSON.stringify(t.calls.map(({ method, path }) => ({ method, path })))}`).toMatchObject({ id: 'team', context: { workspaceId: workspace.id } })
    })
    const teamPanel = await screen.findByTestId('side-panel')
    await userEvent.click(await within(teamPanel).findByRole('button', { name: `Actions for ${worker.name}` }))
    await userEvent.click(await screen.findByRole('button', { name: `Edit ${worker.name}` }))
    await userEvent.click(await screen.findByRole('tab', { name: 'Runtime' }))
    const link = await screen.findByRole('link', { name: 'Change in Settings' })
    expect(screen.getAllByRole('dialog'), 'PTA-03 instrument: the real editor must be open before navigation').toHaveLength(1)
    expect(screen.getByTestId('sheet-overlay'), 'PTA-03 instrument: an overlay must exist before the link is followed').toBeInTheDocument()
    await userEvent.click(link)
    expect(await screen.findByRole('heading', { name: 'Third-party agents', level: 2 })).toBeVisible()
    expect(router.state.location.pathname).toBe('/settings')
    expect(router.state.location.search).toMatchObject({ tab: 'third-party-agents' })
    await waitFor(() => {
      expect(screen.queryAllByRole('dialog')).toHaveLength(0)
      expect(screen.queryAllByRole('alertdialog')).toHaveLength(0)
      expect(document.querySelectorAll('[data-testid="sheet-overlay"], [data-testid="dialog-overlay"], [data-radix-popper-content-wrapper]'), 'PTA-03: no modal overlay or floating editor wrapper may survive the handoff').toHaveLength(0)
    })
    const path = await screen.findByRole('textbox', { name: 'Codex program path' })
    await userEvent.clear(path)
    await userEvent.type(path, '/opt/bin/g1-clicked')
    expect(path).toHaveValue('/opt/bin/g1-clicked')
    await userEvent.click(screen.getByRole('button', { name: 'Discard' }))
    expect(path).toHaveValue('/opt/bin/Codex')
    expect(t.writes().filter(({ path }) => path !== '/agents/executor-preview'), 'PTA-03: following the link, editing and Discard may not persist anything').toEqual([])
  })
})

describe('FR-073 — on-screen text sweep (no tags, no issue numbers, no support line)', () => {
  const TABS = [
    'providers', 'memory', 'system-agents', 'third-party-agents', 'limits',
    'protection', 'sandbox', 'keys', 'gateway', 'data', 'about',
  ] as const
  // R2-06: the sweep must be PROVEN to have swept real section content — a
  // pass over an empty or unloaded section proves nothing (it is how
  // forbidden text in a rendered Gateway label once survived: the captured
  // text never contained it). Each tab pins a witness — the section's own
  // heading (the shell's h2, SettingsScreen.tsx) and one real control of
  // that section — asserted before any text is captured.
  const WITNESS: Record<(typeof TABS)[number], { heading: string; control: () => Promise<HTMLElement> }> = {
    providers: { heading: 'Providers', control: () => screen.findByText('Active transcriber') },
    memory: { heading: 'Memory & recaps', control: () => screen.findByRole('switch', { name: 'Auto recap' }) },
    'system-agents': {
      heading: 'System agents',
      control: () => screen.findAllByRole('button', { name: /^Edit/ }).then((rows) => rows[0]),
    },
    'third-party-agents': {
      heading: 'Third-party agents',
      control: () => screen.findAllByRole('textbox', { name: /program path/i }).then((rows) => rows[0]),
    },
    limits: { heading: 'Limits', control: () => screen.findByRole('button', { name: /Agents and goals/i }) },
    protection: { heading: 'Protection', control: () => screen.findByRole('button', { name: /Turn (on|off) God Mode/i }) },
    sandbox: { heading: 'Sandbox & network', control: () => screen.findByRole('textbox', { name: /new allowed path/i }) },
    keys: { heading: 'Keys & secrets', control: () => screen.findByRole('button', { name: /Add key/ }) },
    gateway: { heading: 'Gateway', control: () => screen.findByLabelText(/Log level/) },
    data: { heading: 'Data & retention', control: () => screen.findByText(/Clear all chat history/) },
    about: { heading: 'About', control: () => screen.findByText('abc1234') },
  }
  // The spec's banned classes: an issue number (#936-style), a requirement
  // tag ("R30, S-Q8 and similar" — the spec's own tag vocabulary), or the
  // support line. A hit is a violation; the sweep is the guard.
  const BANNED: Array<[RegExp, string]> = [
    [/#\d{3,}/, 'an issue number'],
    [/\bR\d+\b/, 'a requirement tag (R…)'],
    [/\bS-Q\d+\b/, 'a requirement tag (S-Q…)'],
    [/\bD-\d+\b/, 'a dataset tag (D-…)'],
    [/\bC-\d+\b/, 'a rider tag (C-…)'],
    [/\bFR-\d+\b/, 'a requirement tag (FR-…)'],
    [/\bUS-\d+\b/, 'a user-story tag (US-…)'],
    [/\bMAJ-\d+\b/, 'a review tag (MAJ-…)'],
    [/\bCRIT-\d+\b/, 'a review tag (CRIT-…)'],
    [/\bAS\d+\b/, 'an acceptance-scenario tag (AS…)'],
    [/contact support/i, 'the support line'],
  ]

  for (const tab of TABS) {
    it(`${tab} carries no issue number, requirement tag or support line`, async () => {
      const { heading, control } = WITNESS[tab]
      await openSettings(`?tab=${tab}`)
      // Witness (R2-06): the section and one of its real controls must be
      // loaded before anything is swept — an unloaded section must fail the
      // witness, not pass the sweep.
      expect(
        await screen.findByRole('heading', { name: heading, level: 2 }),
        `BLOCKED (FR-073 witness): ${tab} did not render its section — the sweep would cover nothing`,
      ).toBeInTheDocument()
      expect(
        await control(),
        `BLOCKED (FR-073 witness): ${tab} rendered none of its section's controls — the sweep would cover an unloaded section`,
      ).toBeInTheDocument()
      // Every visible section must settle out of its loading state —
      // sweeping mid-load would sweep less text than a person sees.
      await waitFor(() => {
        expect(screen.queryByText(/^Loading /i)).not.toBeInTheDocument()
      }, { timeout: 5_000 })
      // FR-072: grouped lists start collapsed and a collapsed group renders
      // no body — expand every disclosure so the sweep covers group content
      // too. Pickers (aria-haspopup) open lists, not groups; leave them.
      for (const el of Array.from(document.querySelectorAll<HTMLButtonElement>('button[aria-expanded="false"]'))) {
        if (!el.hasAttribute('aria-haspopup')) fireEvent.click(el)
      }
      // The captured text must be real, non-empty section text. jsdom has no
      // innerText — textContent is the source the sweep can actually read
      // (PerformanceSection.test.tsx documents the same constraint), so an
      // empty capture fails loudly instead of sweeping nothing.
      const text = document.body.textContent ?? ''
      expect(text.trim().length, `BLOCKED (FR-073 witness): ${tab} captured no text to sweep`).toBeGreaterThan(0)
      expect(text, `BLOCKED (FR-073 witness): ${tab} captured text without its own section heading`).toContain(heading)
      for (const [pattern, what] of BANNED) {
        const hit = text.match(pattern)
        expect(hit, `RED (FR-073): ${tab} on-screen text contains ${what}`).toBeNull()
      }
    })
  }
})

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
 *   - D-9 rows 4+6 / FR-060: no Web search, no skill-trust/tool grid;
 *     Protection announces they live in Skills & Tools.
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

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createRouter, createMemoryHistory, RouterProvider } from '@tanstack/react-router'
import { routeTree } from '@/routeTree.gen'
import { rotateGatewayToken } from '@/lib/api'

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

  it('Protection announces tool permissions and skill trust live in Skills & Tools (D-9 row 6)', async () => {
    await openSettings('?tab=protection')
    expect(
      await screen.findByText(/Skills & Tools/),
      'BLOCKED RED (FR-060/D-9 row 6): Protection must link skill trust and tool permissions to Skills & Tools',
    ).toBeInTheDocument()
    // The God Mode control is a button in Protection (D-9 row 8, FR-051).
    expect(
      await screen.findByRole('button', { name: /Turn (on|off) God Mode/i }),
    ).toBeInTheDocument()
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

describe('FR-073 — on-screen text sweep (no tags, no issue numbers, no support line)', () => {
  const TABS = [
    'providers', 'memory', 'system-agents', 'third-party-agents', 'limits',
    'protection', 'sandbox', 'keys', 'gateway', 'data', 'about',
  ] as const
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
      await openSettings(`?tab=${tab}`)
      // Every visible section must settle out of its loading state first —
      // sweeping mid-load would sweep less text than a person sees.
      await waitFor(() => {
        expect(screen.queryByText(/^Loading /i)).not.toBeInTheDocument()
      }, { timeout: 5_000 })
      const text = document.body.innerText ?? ''
      for (const [pattern, what] of BANNED) {
        const hit = text.match(pattern)
        expect(hit, `RED (FR-073): ${tab} on-screen text contains ${what}`).toBeNull()
      }
    })
  }
})

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
import { render, screen, cleanup } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createRouter, createMemoryHistory, RouterProvider } from '@tanstack/react-router'
import { routeTree } from '@/routeTree.gen'

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
    const overridesToggle = screen.getByRole('button', { name: /Model overrides/i })
    expect(overridesToggle).toHaveAttribute('aria-expanded', 'false') // S-Q12 A: collapsed
    expect(screen.getByText('Context and tool results')).toBeInTheDocument()
    expect(screen.getByText('Agents and goals')).toBeInTheDocument()
    // D-9 row 12: the tool-loading switch stays in Limits → Agents and goals.
    expect(screen.getByRole('switch', { name: /Load tools on demand/i })).toBeInTheDocument()
    // D-9 rows 3, 13: no per-control save buttons — the Save bar owns saving.
    expect(screen.queryAllByRole('button', { name: /^save$/i })).toHaveLength(0)
  })
})

describe('D-9 rows 5-6, row 8 — Providers voice, moved-out skills, God Mode', () => {
  it('Providers holds a Voice input group with Active transcriber (D-9 row 5)', async () => {
    await openSettings('?tab=providers')
    expect(await screen.findByText('Voice input')).toBeInTheDocument()
    expect(screen.getByText('Active transcriber')).toBeInTheDocument()
    // D-9 row 4: web search is absent (moved to Skills & Tools).
    expect(screen.queryByText(/Web search/i)).not.toBeInTheDocument()
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
    await screen.findByRole('heading', { name: 'Gateway', level: 2 }).catch(() => {})
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

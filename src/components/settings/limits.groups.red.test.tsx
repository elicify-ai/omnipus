/**
 * limits.groups.red.test.tsx — Settings redesign #1519, RED
 * (FR-058, FR-072, FR-066; BDD "Limits holds three groups",
 * "Grouped lists start collapsed", "A section that cannot load").
 *
 * Oracles (spec, never code):
 *   - FR-058: Limits holds three collapsible groups — Context and tool
 *     results, Model overrides, Agents and goals.
 *   - FR-072: every grouped list starts collapsed (aria-expanded="false"),
 *     including Model overrides.
 *   - FR-066: when only the context read fails, the groups that need it
 *     (Context and tool results, Model overrides) show their error on open —
 *     the group opens itself with its error instead of hiding it behind the
 *     collapsed header — while Agents and goals still works.
 *
 * RED on landing: LimitsSection does not exist (the old route shows the
 * separate Models/Performance tabs), so the loader fails loudly as BLOCKED,
 * naming the FR it is required by.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

// Full wire shapes (contracts' GET /settings/context and GET /performance):
// the hooks parse these into their forms, so partial fixtures would shift
// failures away from the FR this test proves.
const CONTEXT_OK = {
  mcp_result_cap: 20000,
  builtin_success_cap: 8000,
  builtin_failure_cap: 4000,
  tool_result_share_fraction: 0.4,
  ingest_bound_bytes: 1048576,
  default_context_window: null,
  model_overrides: [],
}

const PERF_OK = {
  max_parallel_agents: 3,
  effective_max_parallel_agents: 3,
  max_parallel_agents_configured: true,
  tools_on_demand: true,
  goal_max_rounds: 25,
  goal_reminder_quiet_seconds: 300,
  goal_stalled_checkin_enabled: false,
  goal_stalled_checkin_minutes: 30,
  max_tool_iterations: 40,
  max_tool_iterations_saved_state: 'ok' as const,
  max_tool_iterations_saved_raw: 40,
  max_tool_iterations_lowered_agents: [],
  pending_apply: { stage: 'refresh' as const, changed_fields: [] },
}

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    getContextSettings: vi.fn(),
    putContextSettings: vi.fn(async () => CONTEXT_OK),
    fetchPerformanceSettings: vi.fn(),
    updatePerformanceSettings: vi.fn(async () => PERF_OK),
    fetchMaxToolIterationsLoweringPreview: vi.fn(async () => ({ agents: [] })),
  }
})
vi.mock('@/store/ui', () => ({ useUiStore: () => ({ addToast: vi.fn() }) }))

import * as api from '@/lib/api'

/** The LimitsSection is new in this package; missing it is a loud BLOCKED. */
async function loadLimits(): Promise<(p: { prefillOverride?: unknown }) => React.ReactElement> {
  try {
    const mod: any = await vi.importActual('./LimitsSection')
    return mod.LimitsSection ?? mod.default
  } catch {
    throw new Error(
      'BLOCKED RED (FR-058/FR-066/FR-072): the Limits section (LimitsSection) does not exist yet — ' +
        'required by FR-058 (three groups), FR-072 (collapsed by default) and FR-066 (a failed read shows its error)',
    )
  }
}

function renderLimits(ui: React.ReactElement) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>)
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.getContextSettings).mockResolvedValue(CONTEXT_OK as never)
  vi.mocked(api.fetchPerformanceSettings).mockResolvedValue(PERF_OK as never)
})

const GROUP_NAMES = ['Context and tool results', 'Model overrides', 'Agents and goals']

describe('FR-058/FR-072 — Limits holds three collapsed groups', () => {
  it('shows exactly the three groups, each starting collapsed', async () => {
    const LimitsSection = await loadLimits()
    renderLimits(<LimitsSection prefillOverride={undefined} />)
    for (const name of GROUP_NAMES) {
      const trigger = await screen.findByRole('button', { name, timeout: 5000 })
      expect(
        trigger,
        `FR-058 RED: the group "${name}" must exist in Limits`,
      ).toHaveAttribute('aria-expanded', 'false')
    }
  })
})

describe('FR-066 — a Limits group whose read failed opens itself with its error', () => {
  it('a failing context read shows the error on open for the two context groups; Agents and goals still works', async () => {
    vi.mocked(api.getContextSettings).mockRejectedValue(new Error('context settings unreadable'))
    const LimitsSection = await loadLimits()
    renderLimits(<LimitsSection prefillOverride={undefined} />)

    // The error is VISIBLE without expanding anything: the group opens
    // itself with its error (FR-066), unlike the collapsed-by-default
    // healthy state of FR-072.
    await waitFor(
      () => expect(screen.getAllByText(/context settings unreadable/).length).toBeGreaterThanOrEqual(1),
      { timeout: 5000 },
    )
    // Both groups that need the failed read name the cause; Agents and goals
    // (the performance read succeeded) still renders its fields.
    expect(
      screen.getAllByText(/context settings unreadable/).length,
      'FR-066 RED: Context and tool results AND Model overrides show the error (Read dependencies)',
    ).toBeGreaterThanOrEqual(2)
    // Agents and goals does not depend on the failed read: the person can
    // still open it and work its fields (a collapsed group mounts nothing,
    // so "still works" is proven by opening it).
    fireEvent.click(screen.getByRole('button', { name: 'Agents and goals' }))
    expect(
      await screen.findByLabelText(/tries per goal/i, {}, { timeout: 5000 }),
      'FR-066: Agents and goals still works when only the context read failed',
    ).toBeInTheDocument()
  })
})

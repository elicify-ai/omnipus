/**
 * sectionGroupErrors.retry.test.tsx — Settings redesign #1519, part 8, RED.
 *
 * FR-066 Retry (part-8 item 2): a group whose read failed shows its error
 * Callout offering Retry; Retry sends the failed read AGAIN; while the read
 * still fails the error stays; when the read then succeeds, the group shows
 * its values.
 *
 * Oracles (spec, never code):
 *   - FR-066: the groups that need the failed read show "an error Callout
 *     naming what failed, quoting the server's cause, giving one thing to do
 *     and offering Retry"; Retry re-reads only the failed read and never
 *     replaces drafts elsewhere.
 *   - BDD "A section that cannot load" names the server causes this test
 *     quotes: "performance settings unreadable".
 *   - Read dependencies table: Limits' "Agents and goals" group needs
 *     GET /performance; the other two groups need GET /settings/context — so
 *     with only the performance read failing, exactly one group errors and
 *     the context groups keep rendering.
 *   - 'Tries per goal' is the spec's label for planning.goal_max_rounds.
 *
 * Harness: the REAL routeTree in a REAL router (row-28b pattern), '@/lib/api'
 * mocked at the boundary. RED today on landing: there is no Limits section
 * with groups (?tab=limits still renders the old tabs), so the assertions
 * fail naming FR-066. On work/settings-1519-fe the expected failure is the
 * Retry control / re-read assertions (no Retry there yet).
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createRouter, createMemoryHistory, RouterProvider } from '@tanstack/react-router'
import userEvent from '@testing-library/user-event'
import { routeTree } from '@/routeTree.gen'

const apiState = { perfFails: true }

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAppState: vi.fn(async () => ({ onboarding_complete: true })),
    validateToken: vi.fn(async () => ({})),
    fetchNotifications: vi.fn(async () => ({ notifications: [], unread_count: 0 })),
    fetchTasks: vi.fn(async () => []),
    fetchAgents: vi.fn(async () => []),
    fetchVersion: vi.fn(async () => ({ version: 'test', build_sha: 'test' })),
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
    // The context read stays healthy: only the Agents-and-goals group may
    // error (Read dependencies table).
    getContextSettings: vi.fn(async () => ({
      mcp_result_cap: 20,
      builtin_success_cap: 5,
      model_overrides: [],
    })),
    // The performance read fails on demand (BDD seam, the spec's cause text).
    fetchPerformanceSettings: vi.fn(async () => {
      if (apiState.perfFails) {
        throw Object.assign(new Error('performance settings unreadable'), { status: 500 })
      }
      return {
        max_parallel_agents: 4,
        effective_max_parallel_agents: 4,
        max_parallel_agents_configured: true,
        tools_on_demand: false,
        goal_max_rounds: 6,
        goal_reminder_quiet_seconds: 60,
        goal_stalled_checkin_enabled: true,
        goal_stalled_checkin_minutes: 10,
        max_tool_iterations: 50,
      }
    }),
    fetchRetentionSettings: vi.fn(async () => ({
      session_days: 90,
      memory_retros_days: 180,
      memory_retros_forever: false,
      keep_everything_forever: false,
    })),
    fetchStorageStats: vi.fn(async () => ({
      workspace_size_bytes: 10,
      session_count: 3,
      memory_entry_count: 5,
      warnings: [],
    })),
    fetchConfig: vi.fn(async () => ({ gateway: {}, data: { session_retention_days: 90 } })),
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
  window.localStorage.setItem('omnipus_auth_username', 'group-retry-tester')
  apiState.perfFails = true
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

describe('FR-066 — Retry re-reads a failed group and renders it on success', () => {
  it('the failed group shows the error with Retry; Retry sends the read again and, on success, the group shows its values', async () => {
    const { fetchPerformanceSettings } = await import('@/lib/api')
    await openSettings('?tab=limits')
    expect(await screen.findByRole('heading', { name: 'Limits', level: 2 })).toBeInTheDocument()

    // Only the group that needs the failed read errors (Read dependencies),
    // quoting the server's cause and offering Retry.
    const causes = screen.getAllByText(/performance settings unreadable/i)
    expect(causes.length).toBeGreaterThanOrEqual(1)
    expect(screen.queryByText(/context settings unreadable/i)).not.toBeInTheDocument()
    const retries = screen.getAllByRole('button', { name: /retry/i })
    expect(retries.length).toBeGreaterThanOrEqual(1)

    // Retry sends the read again — the read is re-issued, not merely shown.
    const callsAfterOpen = vi.mocked(fetchPerformanceSettings).mock.calls.length
    await userEvent.click(retries[0])
    expect(vi.mocked(fetchPerformanceSettings).mock.calls.length).toBeGreaterThan(callsAfterOpen)

    // The read still fails: the error stays.
    expect(screen.getAllByText(/performance settings unreadable/i).length).toBeGreaterThanOrEqual(1)

    // The read then succeeds: Retry's re-read renders the group's values.
    apiState.perfFails = false
    const retries2 = screen.getAllByRole('button', { name: /retry/i })
    await userEvent.click(retries2[0])
    expect(screen.queryByText(/performance settings unreadable/i)).not.toBeInTheDocument()
    const tries = await screen.findByLabelText('Tries per goal')
    expect(tries).toHaveValue(6)
  })
})

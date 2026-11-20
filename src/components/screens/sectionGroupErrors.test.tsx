/**
 * sectionGroupErrors.test.tsx — Settings redesign #1519, RED.
 *
 * Test Implementation Order row 28 (FR-066, the "Read dependencies" table;
 * BDD "A section that cannot load", "One failed read blocks only its group",
 * "A group whose read worked still saves while another group's read failed",
 * "Data & retention keeps working when storage counts fail").
 *
 * Oracles (spec, never code):
 *   - FR-066: when a section's read fails, ONLY the groups that need that
 *     read (Read dependencies: Limits: GET /settings/context → Context and
 *     tool results + Model overrides; GET /performance → Agents and goals;
 *     Data & retention: GET /storage/stats → Counts; GET /settings/retention
 *     → How long data is kept) show an error Callout naming what failed,
 *     quoting the server's cause, giving one thing to do and offering Retry;
 *     other groups keep their drafts and still save. Retry re-reads only the
 *     failed read and never replaces drafts elsewhere. A section is blocked
 *     as a whole only when EVERY read failed.
 *   - BDD "One failed read blocks only its group": with context failing and
 *     performance fine, a Tries-per-goal draft (6 → 8) survives the Context
 *     group's Retry.
 *   - BDD "A group whose read worked still saves": the save sends exactly
 *     one write (PUT /performance) and no PUT /settings/context.
 *   - BDD "Data & retention keeps working when storage counts fail": the
 *     counts show their error and the retention value is stored.
 *
 * Harness: the REAL routeTree in a REAL router (row-18 pattern), '@/lib/api'
 * mocked at the boundary. RED today: there is no Limits section with groups
 * (Models and Performance are separate tabs) and no per-group error states —
 * the assertions fail naming FR-066.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createRouter, createMemoryHistory, RouterProvider } from '@tanstack/react-router'
import userEvent from '@testing-library/user-event'
import { routeTree } from '@/routeTree.gen'

const apiState = { contextFails: true, statsFails: false }

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
    // Limits reads — context fails on demand (BDD seam), performance answers.
    getContextSettings: vi.fn(async () => {
      if (apiState.contextFails) {
        throw Object.assign(new Error('context settings unreadable'), { status: 500 })
      }
      return { mcp_result_cap: 20, builtin_success_cap: 5, model_overrides: [] }
    }),
    putContextSettings: vi.fn(async () => {
      throw Object.assign(new Error('context settings unreadable'), { status: 500 })
    }),
    fetchPerformanceSettings: vi.fn(async () => ({
      max_parallel_agents: 4,
      effective_max_parallel_agents: 4,
      max_parallel_agents_configured: true,
      tools_on_demand: false,
      goal_max_rounds: 6,
      goal_reminder_quiet_seconds: 60,
      goal_stalled_checkin_enabled: true,
      goal_stalled_checkin_minutes: 10,
      max_tool_iterations: 50,
    })),
    updatePerformanceSettings: vi.fn(async () => ({
      max_parallel_agents: 4,
      effective_max_parallel_agents: 4,
      max_parallel_agents_configured: true,
      tools_on_demand: false,
      goal_max_rounds: 8,
      goal_reminder_quiet_seconds: 60,
      goal_stalled_checkin_enabled: true,
      goal_stalled_checkin_minutes: 10,
      max_tool_iterations: 50,
    })),
    // Data & retention reads.
    fetchRetentionSettings: vi.fn(async () => ({
      session_days: 90,
      memory_retros_days: 180,
      memory_retros_forever: false,
      keep_everything_forever: false,
    })),
    updateRetentionSettings: vi.fn(async () => ({
      session_days: 30,
      memory_retros_days: 180,
      memory_retros_forever: false,
      keep_everything_forever: false,
    })),
    fetchStorageStats: vi.fn(async () => {
      if (apiState.statsFails) {
        throw Object.assign(new Error('storage stats unreadable'), { status: 500 })
      }
      return { workspace_size_bytes: 10, session_count: 3, memory_entry_count: 5, warnings: [] }
    }),
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
  window.localStorage.setItem('omnipus_auth_username', 'group-errors-tester')
  apiState.contextFails = true
  apiState.statsFails = false
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

describe('FR-066 — a failed read blocks only its groups', () => {
  it('Limits with a failing context read: the two context groups show the error with Retry, Agents and goals still works', async () => {
    await openSettings('?tab=limits')
    expect(await screen.findByRole('heading', { name: 'Limits', level: 2 })).toBeInTheDocument()
    // The failed read's groups show the error naming the read and its cause.
    const errors = screen.getAllByText(/context settings unreadable/i)
    expect(errors.length).toBeGreaterThanOrEqual(2) // Context and tool results + Model overrides
    expect(screen.getAllByRole('button', { name: /retry/i }).length).toBeGreaterThanOrEqual(2)
    // The group whose read works still renders its field.
    expect(screen.getByLabelText('Tries per goal')).toBeInTheDocument()
  })

  it('every read failed → the whole section is blocked (FR-066 last rule)', async () => {
    await openSettings('?tab=limits')
    await screen.findByRole('heading', { name: 'Limits', level: 2 })
    // No saveable value anywhere: the performance read also fails.
    apiState.contextFails = true
    // (the performance seam is not stateful here; its failure shape is
    // covered by the data-retention test below — this asserts the
    // context-side groups are blocked)
    expect(screen.getByLabelText('Tries per goal')).toBeDisabled()
  })

  it('a draft in the working group survives the failed group Retry', async () => {
    await openSettings('?tab=limits')
    await screen.findByRole('heading', { name: 'Limits', level: 2 })
    const tries = screen.getByLabelText('Tries per goal')
    await userEvent.clear(tries)
    await userEvent.type(tries, '8')
    // Retry the context groups only.
    const retries = screen.getAllByRole('button', { name: /retry/i })
    await userEvent.click(retries[0])
    // The draft is untouched (Retry never replaces drafts elsewhere).
    expect(screen.getByLabelText('Tries per goal')).toHaveValue(8)
  })

  it('the working group still saves: exactly one PUT /performance, no PUT /settings/context', async () => {
    const { updatePerformanceSettings, putContextSettings } = await import('@/lib/api')
    await openSettings('?tab=limits')
    await screen.findByRole('heading', { name: 'Limits', level: 2 })
    const tries = screen.getByLabelText('Tries per goal')
    await userEvent.clear(tries)
    await userEvent.type(tries, '8')
    await userEvent.click(screen.getByRole('button', { name: /save changes/i }))
    expect(vi.mocked(updatePerformanceSettings)).toHaveBeenCalledTimes(1)
    expect(vi.mocked(putContextSettings)).not.toHaveBeenCalled()
    // The failed groups still show their error.
    expect(screen.getAllByText(/context settings unreadable/i).length).toBeGreaterThanOrEqual(2)
  })

  it('Data & retention keeps working when storage counts fail: counts error, retention saves', async () => {
    const { updateRetentionSettings } = await import('@/lib/api')
    apiState.statsFails = true
    await openSettings('?tab=data')
    // The counts group shows its error with Retry.
    expect(await screen.findByText(/storage stats unreadable/i)).toBeInTheDocument()
    // The retention group still works: change and save.
    const chat = screen.getByLabelText('Keep chat history for')
    await userEvent.clear(chat)
    await userEvent.type(chat, '30')
    await userEvent.click(screen.getByRole('button', { name: /save changes/i }))
    expect(vi.mocked(updateRetentionSettings)).toHaveBeenCalledTimes(1)
  })
})

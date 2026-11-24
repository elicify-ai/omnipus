/**
 * sectionGroupErrors.test.tsx — Settings redesign #1519.
 *
 * Test Implementation Order row 28 (FR-066, the "Read dependencies" table;
 * BDD "A section that cannot load", "One failed read blocks only its group"
 * (spec ~line 1572), "A group whose read worked still saves while another
 * group's read failed" (spec ~line 1579), "Data & retention keeps working
 * when storage counts fail").
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
 *     group's Retry and the bar still says "1 unsaved change".
 *   - BDD "A group whose read worked still saves": the save sends exactly
 *     one write (PUT /performance carrying goal_max_rounds: 8, FR-015's
 *     changed-fields-only body) and no PUT /settings/context. The draft is
 *     a Password-marked row (FR-025: the six Agents-and-goals rows), so the
 *     save goes through the real prompt chain (FR-026: the prompt precedes
 *     the write) — the harness answers the real ReAuthDialog.
 *   - BDD "Data & retention keeps working when storage counts fail": the
 *     counts show their error and the retention value is stored.
 *
 * Harness (reconciled part 13; the RED shapes were harness defects, the
 * oracles above are unchanged):
 *   - the REAL routeTree in a REAL router (row-18 pattern), '@/lib/api'
 *     mocked at the network edge; the page-level reads (about,
 *     pending-restart) are mocked healthy so the only Retry buttons on the
 *     page are the failed groups' (the sectionGroupErrors.retry pattern);
 *   - FR-072 / BDD "Limits holds three groups", "Grouped lists start
 *     collapsed": every group starts collapsed and a collapsed group mounts
 *     nothing, so a healthy group's field is reached by expanding it — the
 *     error Callouts of a failed group need no click (the group opens
 *     itself with its error);
 *   - reads resolve asynchronously, so callouts and fields are awaited
 *     (the findBy / findAll queries), never read synchronously after the
 *     heading.
 *
 * RED history: the part-2 RED run (commit 58f7e5c8b2) failed naming FR-066
 * on the pre-redesign tree; the failing run of the un-reconciled file
 * against the finished frontend is saved at
 * /workspace/logs/settings-1519/int/sectionGroupErrors.log (4 of 5 red).
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createRouter, createMemoryHistory, RouterProvider } from '@tanstack/react-router'
import userEvent from '@testing-library/user-event'
import { routeTree } from '@/routeTree.gen'

const apiState = { contextFails: true, statsFails: false, perfFails: false }

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
    fetchAboutInfo: vi.fn(async () => ({ version: 'test', build_sha: 'test' })),
    fetchPendingRestart: vi.fn(async () => []),
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
    // Limits reads — context fails on demand (BDD seam), performance is
    // stateful so "every read failed" can actually be constructed.
    getContextSettings: vi.fn(async () => {
      if (apiState.contextFails) {
        throw Object.assign(new Error('context settings unreadable'), { status: 500 })
      }
      return { mcp_result_cap: 20, builtin_success_cap: 5, model_overrides: [] }
    }),
    putContextSettings: vi.fn(async () => {
      throw Object.assign(new Error('context settings unreadable'), { status: 500 })
    }),
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
    // The save's real prompt chain (FR-025/FR-026): the shell's
    // ReAuthDialog POSTs /auth/reauth once and hands the consent token to
    // the write. The token is a fixed harness value so the write's
    // arguments are assertable.
    reAuth: vi.fn(async () => ({ token: 'tok-section-group-errors' })),
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
  apiState.perfFails = false
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
    // The failed read's groups show the error naming the read and its cause;
    // the Callouts appear when the read rejects, so they are awaited.
    const errors = await screen.findAllByText(/context settings unreadable/i)
    expect(errors.length).toBeGreaterThanOrEqual(2) // Context and tool results + Model overrides
    expect((await screen.findAllByRole('button', { name: /retry/i })).length).toBeGreaterThanOrEqual(2)
    // FR-072: the working group starts collapsed and mounts nothing — open
    // it (BDD "Grouped lists start collapsed") and it still renders its
    // field with the read's value.
    await userEvent.click(screen.getByRole('button', { name: 'Agents and goals' }))
    expect(await screen.findByLabelText('Tries per goal')).toHaveValue(6)
  })

  it('every read failed → the whole section is blocked (FR-066 last rule)', async () => {
    // Both Limits reads fail (Read dependencies: context → Context and tool
    // results + Model overrides; performance → Agents and goals). The flag
    // is set before the render so the failing shape is deterministic.
    apiState.perfFails = true
    await openSettings('?tab=limits')
    await screen.findByRole('heading', { name: 'Limits', level: 2 })
    // Each group shows the error Callout quoting its cause (BDD "A section
    // that cannot load"): the two context groups the context read's cause,
    // Agents and goals the performance read's cause, each with its Retry.
    expect((await screen.findAllByText(/context settings unreadable/i)).length).toBeGreaterThanOrEqual(2)
    expect((await screen.findAllByText(/performance settings unreadable/i)).length).toBeGreaterThanOrEqual(1)
    expect((await screen.findAllByRole('button', { name: /retry/i })).length).toBeGreaterThanOrEqual(3)
    // "No value in Limits can be saved" (BDD "A section that cannot load"):
    // a blocked group shows the error Callout in place of its values, so no
    // field is presented anywhere. (Replaces the part-2 disabled-field
    // expectation — FR-066's blocked shape is the Callout, not a rendered
    // input; FR-066 last rule + BDD "A section that cannot load".)
    expect(screen.queryByLabelText('Tries per goal')).not.toBeInTheDocument()
  })

  it('a draft in the working group survives the failed group Retry', async () => {
    const { getContextSettings, fetchPerformanceSettings } = await import('@/lib/api')
    await openSettings('?tab=limits')
    await screen.findByRole('heading', { name: 'Limits', level: 2 })
    // FR-072: the working group starts collapsed — open it to make its draft.
    await userEvent.click(screen.getByRole('button', { name: 'Agents and goals' }))
    const tries = await screen.findByLabelText('Tries per goal')
    await userEvent.clear(tries)
    await userEvent.type(tries, '8')
    expect(tries).toHaveValue(8)
    // BDD "One failed read blocks only its group": the bar counts the draft.
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    // Retry the failed context read only (FR-066: Retry re-reads only the
    // failed read and never replaces drafts elsewhere). The page-level reads
    // are mocked healthy, so the Retry buttons here belong to the two
    // context groups.
    const contextReads = vi.mocked(getContextSettings).mock.calls.length
    const perfReads = vi.mocked(fetchPerformanceSettings).mock.calls.length
    const retries = await screen.findAllByRole('button', { name: /retry/i })
    expect(retries.length).toBeGreaterThanOrEqual(2)
    await userEvent.click(retries[0])
    // The failed read is re-issued; the healthy one is not.
    expect(vi.mocked(getContextSettings).mock.calls.length).toBeGreaterThan(contextReads)
    expect(vi.mocked(fetchPerformanceSettings).mock.calls.length).toBe(perfReads)
    // The draft is untouched and still counted (spec: "Tries per goal still
    // shows the unsaved 8 with the bar saying '1 unsaved change'").
    expect(screen.getByLabelText('Tries per goal')).toHaveValue(8)
    expect(screen.getByText(/1 unsaved change/)).toBeInTheDocument()
  })

  it('Retry with the read now healthy refills the group with the read value (FR-066 BDD "A section that cannot load" recovery)', async () => {
    const { getContextSettings, fetchPerformanceSettings } = await import('@/lib/api')
    await openSettings('?tab=limits')
    await screen.findByRole('heading', { name: 'Limits', level: 2 })
    // The failed shape first: both context groups show their error Callouts.
    expect((await screen.findAllByText(/context settings unreadable/i)).length).toBeGreaterThanOrEqual(2)
    // The gateway answers the Retry: the failed read returns healthy values
    // (the BDD recovery of FR-066 — the callout's "one thing to do" works).
    apiState.contextFails = false
    const contextReads = vi.mocked(getContextSettings).mock.calls.length
    const perfReads = vi.mocked(fetchPerformanceSettings).mock.calls.length
    const retries = await screen.findAllByRole('button', { name: /retry/i })
    await userEvent.click(retries[0])
    // Retry re-reads ONLY the failed read (FR-066); the healthy one is not re-issued.
    await waitFor(() => expect(vi.mocked(getContextSettings).mock.calls.length).toBeGreaterThan(contextReads))
    expect(vi.mocked(fetchPerformanceSettings).mock.calls.length).toBe(perfReads)
    // The group FILLS with the read value: the error Callout is replaced by
    // the section's fields carrying what the read answered (the group the
    // failure left open stays open — the refill lands in place).
    const cap = await screen.findByLabelText('MCP tool result cap')
    await waitFor(() => expect(cap).toHaveValue('20'))
    expect(screen.queryByText(/context settings unreadable/i)).not.toBeInTheDocument()
    // The read's other fields filled too, and no draft existed to lose (a
    // fresh page): the values are exactly the read's.
    expect(screen.getByLabelText('Built-in tool result cap')).toHaveValue('5')
  })

  it('the working group still saves: exactly one PUT /performance, no PUT /settings/context', async () => {
    const { updatePerformanceSettings, putContextSettings } = await import('@/lib/api')
    await openSettings('?tab=limits')
    await screen.findByRole('heading', { name: 'Limits', level: 2 })
    // FR-072: open the collapsed working group to make its draft.
    await userEvent.click(screen.getByRole('button', { name: 'Agents and goals' }))
    const tries = await screen.findByLabelText('Tries per goal')
    await userEvent.clear(tries)
    await userEvent.type(tries, '8')
    await userEvent.click(await screen.findByRole('button', { name: /save changes/i }))
    // The draft is a Password-marked row (FR-025: the six Agents-and-goals
    // rows), so the save first asks the shell's real prompt (FR-026: one
    // prompt before any write) and the write rides its consent token.
    const password = await screen.findByPlaceholderText('Your password')
    await userEvent.type(password, 'correct horse battery staple')
    await userEvent.click(screen.getByRole('button', { name: 'Confirm' }))
    // BDD "A group whose read worked still saves while another group's read
    // failed": exactly one write, to the healthy group's endpoint, carrying
    // only the changed field (FR-015) and the prompt's consent token.
    await waitFor(() => {
      expect(vi.mocked(updatePerformanceSettings)).toHaveBeenCalledTimes(1)
      expect(vi.mocked(updatePerformanceSettings)).toHaveBeenCalledWith(
        { goal_max_rounds: 8 },
        'tok-section-group-errors',
      )
      expect(vi.mocked(putContextSettings)).not.toHaveBeenCalled()
    })
    // The failed groups still show their error.
    expect((await screen.findAllByText(/context settings unreadable/i)).length).toBeGreaterThanOrEqual(2)
  })

  it('Data & retention keeps working when storage counts fail: counts error, retention saves', async () => {
    // updateRetentionSettings exists only inside this file's vi.mock until
    // the generated client lands (GREEN); the mock's handle is read loosely.
    const mod = (await import('@/lib/api')) as unknown as {
      updateRetentionSettings: ReturnType<typeof vi.fn>
    }
    apiState.statsFails = true
    await openSettings('?tab=data')
    // The counts group shows its error with Retry.
    expect(await screen.findByText(/storage stats unreadable/i)).toBeInTheDocument()
    // The retention group still works: change and save.
    const chat = screen.getByLabelText('Keep chat history for')
    await userEvent.clear(chat)
    await userEvent.type(chat, '30')
    await userEvent.click(screen.getByRole('button', { name: /save changes/i }))
    expect(mod.updateRetentionSettings).toHaveBeenCalledTimes(1)
  })
})

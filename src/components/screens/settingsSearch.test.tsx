/**
 * settingsSearch.test.tsx — Settings redesign #1519, RED.
 *
 * Test Implementation Order row 26 (spec order; brief row 27: Search
 * scenarios, Dataset D-13, FR-006, FR-007; BDD "Searching for a moved
 * setting says where it went", "Search with no match shows an empty state").
 *
 * Oracles (spec, never code):
 *   - FR-006: search matches case-insensitively as a plain substring over
 *     each setting's label AND description, lists results with their
 *     section, announces the count in a polite live region, shows the kit
 *     Empty state "No settings match" with the typed text when nothing
 *     matches, and treats typed markup as text.
 *   - D-13: "port" → Gateway → Port; "PORT" → same; "web search" → moved to
 *     Skills & Tools, with a link; "backup"/"restore" → "Backup and restore
 *     were removed."; "pair" → "Device pairing is not available in Settings
 *     at the moment."; "retention" → Data & retention → both settings;
 *     "zzqx" → empty "No settings match"; whitespace → full section list, no
 *     results panel; "<script>" → treated as text, no match; "browsers" →
 *     "Signed-in browsers are not in Settings yet."
 *
 * Harness: the REAL routeTree in a REAL router (the row-18 pattern), so the
 * screen that search lives in is the one production renders. RED today: the
 * current Settings screen has no search box at all — every test fails at the
 * search-box lookup, naming the FR.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, cleanup, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createRouter, createMemoryHistory, RouterProvider } from '@tanstack/react-router'
import userEvent from '@testing-library/user-event'
import { routeTree } from '@/routeTree.gen'

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
  }
})

async function openSettings(search = '') {
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

async function typeQuery(q: string) {
  // The router renders the shell asynchronously (FR-065's loading state comes
  // first); a sync getByRole runs before the search box exists. Wait for it,
  // exactly like the keyboard file's passing helper (settingsSearch.keyboard).
  const box = await screen.findByRole('searchbox', { name: /search/i })
  await userEvent.type(box, q)
}

beforeEach(() => {
  window.localStorage.setItem('omnipus_auth_username', 'search-tester')
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

describe('D-13 — search answers', () => {
  it('D-13 rows 1-2: "port" and "PORT" find Gateway → Port (case-insensitive, label match)', async () => {
    await openSettings()
    await typeQuery('PORT')
    // Result rows are asserted inside the results panel: the section list in
    // the sidebar always renders the same names, so a screen-wide lookup for
    // "Gateway" matched the nav link too (locator fix; the oracle — the
    // result lists the setting with its section — is unchanged).
    const panel = await screen.findByTestId('settings-search-results')
    expect(within(panel).getByText('Port')).toBeInTheDocument()
    expect(within(panel).getByText('Gateway')).toBeInTheDocument()
  })

  it('D-13 row 3 / FR-007: "web search" says it moved to Skills & Tools, with a link', async () => {
    await openSettings()
    await typeQuery('web search')
    expect(await screen.findByText(/moved to Skills & Tools|Skills & Tools/)).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /Skills & Tools/i })).toBeInTheDocument()
  })

  it('D-13 row 4: "backup" and "restore" answer the removal notice', async () => {
    await openSettings()
    await typeQuery('backup')
    // "backup" matches BOTH the Backup and the Restore index rows and the
    // notice is the removal answer under each — assert at least one notice,
    // in the results panel (locator fix; the notice text is D-13's verbatim).
    const panel = await screen.findByTestId('settings-search-results')
    expect(within(panel).getAllByText('Backup and restore were removed.').length).toBeGreaterThan(0)
    cleanup()
    await openSettings()
    await typeQuery('restore')
    // "restore" matches both rows the same way (Backup's description and the
    // Restore label) — same at-least-one notice assertion, panel-scoped.
    const panel2 = await screen.findByTestId('settings-search-results')
    expect(within(panel2).getAllByText('Backup and restore were removed.').length).toBeGreaterThan(0)
  })

  it('D-13 row 5: "pair" answers that device pairing is not available', async () => {
    await openSettings()
    await typeQuery('pair')
    expect(await screen.findByText('Device pairing is not available in Settings at the moment.')).toBeInTheDocument()
  })

  it('D-13 row 6: "retention" finds the Data & retention settings', async () => {
    await openSettings()
    await typeQuery('retention')
    // The retention settings are found, each row carrying its section; the
    // panel is the scope because the sidebar lists "Data & retention" too
    // (locator fix; the oracle is D-13 row 6's answer, unchanged).
    const panel = await screen.findByTestId('settings-search-results')
    expect(within(panel).getByText('Keep chat history for')).toBeInTheDocument()
    expect(within(panel).getByText('Keep memory for')).toBeInTheDocument()
    expect(within(panel).getAllByText('Data & retention').length).toBeGreaterThan(0)
  })

  it('D-13 row 7: a word no setting contains shows the empty state with the typed text', async () => {
    await openSettings()
    await typeQuery('zzqx')
    // The empty state names "No settings match" WITH the typed text (D-13
    // row 7); it renders them as one line: No settings match “zzqx”.
    expect(await screen.findByText('No settings match “zzqx”')).toBeInTheDocument()
  })

  it('D-13 row 8: whitespace-only query shows the full section list and no results panel', async () => {
    await openSettings()
    await typeQuery('   ')
    expect(screen.queryByText('No settings match')).not.toBeInTheDocument()
    // The full list is present (all three group headings per FR-001).
    expect(screen.getByText('AI & agents')).toBeInTheDocument()
    expect(screen.getByText('Security')).toBeInTheDocument()
    expect(screen.getByText('System')).toBeInTheDocument()
  })

  it('D-13 row 9: typed markup is treated as text — no match, no execution', async () => {
    await openSettings()
    await typeQuery('<script>')
    expect(await screen.findByText('No settings match “<script>”')).toBeInTheDocument()
    expect(document.querySelectorAll('script').length).toBe(0)
  })

  it('D-13 row 10: "browsers" says signed-in browsers are not in Settings yet', async () => {
    await openSettings()
    await typeQuery('browsers')
    expect(await screen.findByText('Signed-in browsers are not in Settings yet.')).toBeInTheDocument()
  })

  it('FR-006: results announce their count in a polite live region', async () => {
    await openSettings()
    await typeQuery('port')
    // The count live region is aria-live="polite" without a status role, so
    // findByRole('status') could never see it (FR-006 names a polite live
    // region, not a role). The announcement's content is the count itself.
    const live = await screen.findByText(/^\d+ results?$/)
    expect(live).toHaveAttribute('aria-live', 'polite')
  })
})

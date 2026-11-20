/**
 * settingsSearch.keyboard.red.test.tsx — Settings redesign #1519, RED.
 *
 * Two things the matching tests (settingsSearch.test.tsx, D-13) do not
 * cover, driven on the REAL route tree:
 *
 * 1. The keyboard moves through search: ArrowDown from the search box
 *    focuses the first result and moves through the results, Escape in a
 *    result returns to the search box, and Enter opens the section the
 *    result names. Oracles: the section list and its results are links
 *    (the wireframe's keyboard notes: "the section list is a list of links
 *    (Tab, Enter)"); FR-006 requires the results to "list results with
 *    their section", so the section a result names is shown on the result
 *    itself; FR-002 names the open section in the address, one history
 *    entry per switch. The query "retention" is D-13 row 6: Data &
 *    retention, BOTH settings — two results, so "moves through results" is
 *    observable without depending on an implementation's ranking.
 *
 * 2. FR-014's way-out table with 1 unsaved change (the BDD "Leaving with
 *    unsaved changes asks first, and Stay keeps them"):
 *      - "a search result | the leave confirm; Stay returns focus to the
 *        search field" — and Stay keeps the section, its changes, the
 *        address and the history unchanged (FR-014), including the typed
 *        search;
 *      - "another section in the list | the leave confirm" — Stay keeps the
 *        section and its change and returns focus to the link used (the
 *        focus-return rule the way-out rows state; FR-069's return-to-the-
 *        opener pattern is the same rule for dialogs).
 *    The question is the app's own: "Leave without saving N change(s)?"
 *    with Stay and Leave.
 *
 * The draft is armed the deterministic way the spec itself gives: the
 * FR-004 pre-fill link opens Limits with a filled, unsaved row ("1 unsaved
 * change" in the Save bar) — that Save-bar count IS the draft the guard
 * must ask about.
 *
 * Plan (elicify-test-writing step 1):
 *   specification source : FR-002, FR-004, FR-006, FR-014 (+ way-out
 *     table), FR-069, D-13 row 6, BDD "Leaving with unsaved changes asks
 *     first, and Stay keeps them", the wireframe's keyboard note. Expected
 *     values come from these — never from the implementation.
 *   unit boundary        : real route tree, real screen, real sections;
 *     '@/lib/api' mocked at the API seam only (the reads the shell and the
 *     open sections make).
 *   known gaps (deliberate, named): ArrowUp/Home/End variants (not in the
 *     dispatch), the Leave branch's drop outcome (saveBar.outcomes.test),
 *     the beforeunload arm (saveBar.leaveGuard.test), moved-item result
 *     links (settingsSearch.test, D-13 rows 3-5/10).
 *
 * RED today: the current screen has no search box, no results list, no
 * settings-nav list links and no leave confirm — every case fails at its
 * first missing piece, naming the requirement.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, within, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createRouter, createMemoryHistory, RouterProvider } from '@tanstack/react-router'
import { routeTree } from '@/routeTree.gen'
import { fetchAboutInfo, getContextSettings, fetchPerformanceSettings } from '@/lib/api'
import type { ContextSettings } from '@/lib/api'
import { ContextSettings as ContextSettingsSchema } from '@/lib/api/generated/schemas'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    // _app.tsx beforeLoad + AppShell + Sidebar:
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
    // The shell's own reads; each case pins what it needs.
    fetchAboutInfo: vi.fn(),
    // FR-004's Limits read (the draft-arming cases need it to answer).
    getContextSettings: vi.fn(),
    fetchPerformanceSettings: vi.fn(),
    // Section reads the shell mounts alongside (values are not the oracle).
    fetchGodMode: vi.fn(async () => ({ enabled: false, persisted: false, available: true, supported: true })),
    fetchConfig: vi.fn(async () => ({
      gateway: { bind_address: '127.0.0.1', port: 8080 },
      security: { rate_limits: {} },
      data: { session_retention_days: 30 },
    })),
  }
})

/** A fresh install's context settings — the generated schema's defaults. */
const CONTEXT_DEFAULTS: ContextSettings = ContextSettingsSchema.parse({
  mcp_result_cap: 62500,
  builtin_success_cap: 64000,
  builtin_failure_cap: 10000,
  tool_result_share_fraction: 0.5,
  ingest_bound_bytes: 8000000,
  model_overrides: [],
})

let history: ReturnType<typeof createMemoryHistory>

async function openSettings(search = '') {
  // AppShell pins breakpoints (matchMedia) and the _app route guard wants a
  // signed-in username — the same provisioning the copied harness uses.
  window.localStorage.setItem('omnipus_auth_username', 'search-keyboard-tester')
  // jsdom has no matchMedia and no scrollIntoView — environment stubs, the
  // same class as the harness this file copies.
  Element.prototype.scrollIntoView = Element.prototype.scrollIntoView ?? (() => {})
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
  history = createMemoryHistory({ initialEntries: [`/settings${search}`] })
  const router = createRouter({ routeTree, history })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
}

/** The search box, required to exist (FR-006) — the loud RED gate. Awaited:
 * the real router mounts the screen after the first render tick. */
async function searchBox(): Promise<HTMLElement> {
  try {
    return await screen.findByRole('searchbox', { name: /search/i }, { timeout: 3000 })
  } catch {
    expect.fail(
      'BLOCKED RED (FR-006): the Settings search box does not exist yet — the keyboard moves through search results and the FR-014 search-result way out have nothing to drive',
    )
  }
}

/** The result links of an active search, in list order. */
async function resultLinks() {
  const results = await screen.findByTestId(
    'settings-search-results',
    {},
    { timeout: 3000 },
  )
  const links = within(results).getAllByRole('link')
  expect(links.length, 'the search must list its matching settings as results').toBeGreaterThan(0)
  return links
}

/** Arms FR-004's prefill draft and proves the Given: 1 unsaved change. */
async function openLimitsWithDraft() {
  vi.mocked(getContextSettings).mockResolvedValue(CONTEXT_DEFAULTS)
  vi.mocked(fetchPerformanceSettings).mockResolvedValue({} as never)
  // The BDD's own pre-fill address: "?tab=models&provider=&model=" (FR-004).
  await openSettings('?tab=models&provider=openrouter&model=acme/m1')
  expect(
    await screen.findByRole('heading', { name: 'Limits', level: 2 }),
    'BLOCKED RED (FR-004): the pre-fill link must open Limits — it is how this scenario arms its Given, "a section with 1 unsaved change"',
  ).toBeInTheDocument()
  const saveBar = await screen.findByTestId('settings-save-bar')
  await waitFor(
    () => expect(saveBar).toHaveTextContent('1 unsaved change'),
    { timeout: 3000 },
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  // The gateway answers; the offline Callout is not this file's oracle.
  vi.mocked(fetchAboutInfo).mockResolvedValue({
    version: '0.1.1',
    go_version: 'go1.24',
    os: 'linux',
    arch: 'amd64',
    uptime_seconds: 100,
  })
})

describe('keyboard moves through search (FR-006 results; the section list and its results are links)', () => {
  it('ArrowDown moves from the box through the results, Escape returns to the box, Enter opens the section the result names', async () => {
    await openSettings('?tab=gateway')
    const box = await searchBox()
    // D-13 row 6: "retention" → Data & retention → both settings.
    await userEvent.type(box, 'retention')
    const links = await resultLinks()
    expect(
      links.length,
      'D-13 row 6: "retention" answers with BOTH retention settings — two results, so the moves below are observable',
    ).toBeGreaterThanOrEqual(2)
    // ArrowDown: the box hands focus to the first result.
    await userEvent.keyboard('{ArrowDown}')
    expect(document.activeElement, 'RED (keyboard): ArrowDown from the search box must focus the first result').toBe(links[0])
    // ArrowDown again: through the results.
    await userEvent.keyboard('{ArrowDown}')
    expect(document.activeElement, 'RED (keyboard): ArrowDown must move through the results').toBe(links[1])
    // Escape in a result: back to the search box.
    await userEvent.keyboard('{Escape}')
    expect(document.activeElement, 'RED (keyboard): Escape in a result must return focus to the search box').toBe(box)
    // Enter on a result opens the section the result names (FR-006 shows the
    // section on the result; FR-002 names it in the address).
    await userEvent.keyboard('{ArrowDown}')
    expect(document.activeElement).toBe(links[0])
    const href = links[0].getAttribute('href') ?? ''
    const targetTab = new URLSearchParams(href.split('?')[1] ?? '').get('tab')
    expect(targetTab, 'the result must point at its section (FR-006 lists results with their section)').not.toBeNull()
    const sectionShown = links[0].querySelectorAll('span')[1]?.textContent ?? ''
    expect(sectionShown, 'the result must show the section it belongs to (FR-006)').not.toBe('')
    await userEvent.keyboard('{Enter}')
    expect(
      await screen.findByRole('heading', { level: 2, name: sectionShown }),
      'RED (keyboard): Enter on a result must open the section the result names',
    ).toBeInTheDocument()
    expect(history.location.search, 'FR-002: the opened section is named in the address').toContain(`tab=${targetTab}`)
    expect(history.length, 'FR-002: the switch adds one history entry').toBe(2)
  })
})

describe('FR-014 — the way-out table with 1 unsaved change (BDD: leaving asks first, Stay keeps)', () => {
  it('a search result asks the app question; Stay keeps the change, the typed search, the address and the history, and returns focus to the search field', async () => {
    await openLimitsWithDraft()
    const box = await searchBox()
    await userEvent.type(box, 'retention')
    const links = await resultLinks()
    await userEvent.keyboard('{ArrowDown}')
    expect(document.activeElement).toBe(links[0])
    await userEvent.keyboard('{Enter}')
    // The app's own question (FR-014: "Leave without saving N change(s)?"
    // with Stay and Leave).
    expect(
      await screen.findByRole('alertdialog', { name: 'Leave without saving 1 change?' }),
      'RED/BLOCKED (FR-014): leaving through a search result with 1 unsaved change must ask "Leave without saving 1 change?" first',
    ).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Stay' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Leave' })).toBeInTheDocument()
    // Stay: nothing moves (FR-014: the section, its changes, the address and
    // the history stay unchanged) and focus returns to the search field
    // (the way-out table row for a search result).
    await userEvent.click(screen.getByRole('button', { name: 'Stay' }))
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(screen.getByTestId('settings-save-bar')).toHaveTextContent('1 unsaved change')
    expect(screen.getByTestId('context-override-provider')).toHaveValue('openrouter')
    expect(screen.getByTestId('context-override-model')).toHaveValue('acme/m1')
    // The typed search is still there, still filtering (Stay must not clear
    // what the person typed — the navigation never landed).
    expect(screen.getByTestId('settings-search-results')).toBeInTheDocument()
    expect(box).toHaveValue('retention')
    const address = new URLSearchParams(history.location.search)
    expect(address.get('tab'), 'FR-014: Stay keeps the address (no navigation landed)').toBe('models')
    expect(address.get('provider'), 'FR-014: Stay keeps the address').toBe('openrouter')
    expect(address.get('model'), 'FR-014: Stay keeps the address').toBe('acme/m1')
    expect(history.length, 'FR-014: Stay keeps the history unchanged').toBe(1)
    await waitFor(
      () => expect(document.activeElement, 'FR-014 way-out table: Stay returns focus to the search field').toBe(box),
      { timeout: 3000 },
    )
  })

  it('another section in the list asks first; Stay keeps the section and its change and returns focus to the link used', async () => {
    await openLimitsWithDraft()
    expect(
      screen.queryByTestId('settings-nav-gateway'),
      'BLOCKED RED (FR-001): the section-list links do not exist yet — FR-014 way-out "another section in the list" has nothing to drive',
    ).not.toBeNull()
    const gatewayLink = screen.getByTestId('settings-nav-gateway')
    await userEvent.click(gatewayLink)
    expect(
      await screen.findByRole('alertdialog', { name: 'Leave without saving 1 change?' }),
      'RED/BLOCKED (FR-014): clicking another section in the list with 1 unsaved change must ask first (BDD: "Leaving with unsaved changes asks first")',
    ).toBeInTheDocument()
    // Stay: Limits stays open with its change, the address and the history
    // unchanged, and focus returns to the link that was used.
    await userEvent.click(screen.getByRole('button', { name: 'Stay' }))
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Limits', level: 2 })).toBeInTheDocument()
    expect(screen.getByTestId('settings-save-bar')).toHaveTextContent('1 unsaved change')
    expect(screen.getByTestId('context-override-provider')).toHaveValue('openrouter')
    const address = new URLSearchParams(history.location.search)
    expect(address.get('tab'), 'FR-014: Stay keeps the address naming Limits').toBe('models')
    expect(history.length, 'FR-014: Stay keeps the history unchanged').toBe(1)
    await waitFor(
      () => expect(document.activeElement, 'FR-014: Stay returns focus to the link used').toBe(gatewayLink),
      { timeout: 3000 },
    )
  })
})

// SettingsScreen.test.tsx — the Settings shell, Settings redesign #1519.
//
// The old shell contract — the `initialTab` prop and the role="tab" bar — is
// replaced by the approved spec's address-based sections (Settings redesign
// #1519, spec docs/internal/specs/settings-redesign-spec.md, Status:
// Approved). The cases below run the REAL routeTree in a REAL router (memory
// history) — the settingsRoute.legacyTabs.test.tsx harness — so the `?tab=`
// address rule of FR-002 and the section list of FR-001 are live, not props.
//
// Plan (elicify-test-writing step 1):
//   behaviour under test : the shell's address/section list contract
//     (FR-001, FR-002, D-3), the pre-fill link (FR-004, D-3 row 14), the God
//     Mode focus link (FR-005, D-3 row 12) and the gateway-offline Callout
//     (FR-067); hidden controls stay hidden (FR-055).
//   specification source : settings-redesign-spec.md — FR-001 to FR-005,
//     FR-055, FR-067, Datasets D-3 rows 1/2/12/14, the BDD scenarios "Person
//     opens Settings…", "Reload keeps the open section", "Browser Back moves
//     to the previous section", "The 'No context length' link pre-fills…",
//     "The God Mode pill lands on the God Mode button", and the UI table's
//     Settings-shell row. Expected values come from these, never from the
//     implementation.
//   unit boundary        : the real route tree, the real screen shell and the
//     real section components render; `@/lib/api` is mocked at the API seam
//     only (what the shell, AppShell and Sidebar read). Section reads left
//     unmocked hit the test fetch guard and render their error states — the
//     oracles below are the shell's own render (headings, list links,
//     address, Callout), not section data.
//   replaced assertions  : every old case in this file cited its old oracle;
//     each replacement cites the FR that replaces it (per-case comments and
//     the commit message). Old case 4's "Models tab" has no shell replacement:
//     `?tab=models` → Limits is D-3 row 3, covered by
//     settingsRoute.legacyTabs.test.tsx.
//
// Harness notes: every describe in this file renders the real route tree, so
// the part-5 state-leakage rule ("route describes must run last in the file")
// is satisfied trivially — nothing plain-rendered precedes them. jsdom
// provisioning (signed-in username, matchMedia) is scoped to the openSettings
// helper, the same as the harness this file copies.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
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
    // The shell's own reads; each case pins the behaviour it needs.
    fetchAboutInfo: vi.fn(),
    // FR-004's Limits read (only case 4 needs it to answer).
    getContextSettings: vi.fn(),
    fetchPerformanceSettings: vi.fn(),
    // FR-005: the God Mode button is disabled while its status read is in
    // flight and cannot take focus; the read must answer for the focus link
    // to land (the status itself is not the oracle — either enabled state
    // shows a "Turn on/off God Mode…" button). The Protection view also
    // early-returns on a failed ['config'] read before God Mode renders, so
    // that read must answer too (its values are not the oracle).
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
  window.localStorage.setItem('omnipus_auth_username', 'settings-screen-tester')
  // jsdom has no scrollIntoView — the God Mode focus link scrolls its button
  // into view before focusing (FR-005); the focus, not the scroll, is the
  // oracle. Environment provisioning, the same class as the matchMedia stub.
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

beforeEach(() => {
  vi.clearAllMocks()
  // The shell gates the offline Callout on the about read's error: by default
  // the gateway answers; cases 1 and 7 pin the other two arms.
  vi.mocked(fetchAboutInfo).mockResolvedValue({
    version: '0.1.1',
    go_version: 'go1.24',
    os: 'linux',
    arch: 'amd64',
    uptime_seconds: 100,
  })
})

describe('FR-001 — 11 sections in three groups; no section named opens Providers', () => {
  it('D-3 row 1: the list shows the three groups and the 11 sections, Providers open; no old tab, no Sign-in', async () => {
    // The about read is still in flight: the UI table's Settings-shell row
    // says the list shows at once (and FR-067's Callout is error-gated — the
    // old "no error while loading" assertion, now resting on FR-067 itself).
    vi.mocked(fetchAboutInfo).mockReturnValue(new Promise(() => {}))
    await openSettings('')
    // Providers is open (D-3 row 1) and marked current in the list.
    expect(
      await screen.findByRole('heading', { name: 'Providers', level: 2 }),
      'BLOCKED RED (FR-001/D-3 row 1): with no section named, Providers must be open',
    ).toBeInTheDocument()
    expect(screen.getByTestId('settings-nav-providers')).toHaveAttribute('aria-current', 'page')
    // FR-001: three groups, eleven sections in this order.
    for (const group of ['AI & agents', 'Security', 'System']) {
      expect(screen.getByText(group), `group ${group} must be listed`).toBeInTheDocument()
    }
    const expectedIds = [
      'providers', 'memory', 'system-agents', 'third-party-agents', 'limits',
      'protection', 'sandbox', 'keys', 'gateway', 'data', 'about',
    ] as const
    for (const id of expectedIds) {
      expect(screen.getByTestId(`settings-nav-${id}`), `section link ${id} must exist`).toBeInTheDocument()
    }
    // FR-001: the old tabs and Sign-in MUST NOT appear.
    for (const retired of ['models', 'performance', 'integrations', 'security', 'chat', 'devices']) {
      expect(screen.queryByTestId(`settings-nav-${retired}`), `retired tab ${retired} must not appear`).not.toBeInTheDocument()
    }
    expect(screen.queryByText('Sign-in')).not.toBeInTheDocument()
  })
})

describe('FR-002 — the address names the open section', () => {
  it('D-3 row 2: ?tab=sandbox reopens Sandbox & network, marked current in the list (BDD: reload keeps the open section)', async () => {
    await openSettings('?tab=sandbox')
    expect(
      await screen.findByRole('heading', { name: 'Sandbox & network', level: 2 }),
      'BLOCKED RED (FR-002/D-3 row 2): tab=sandbox must reopen Sandbox & network',
    ).toBeInTheDocument()
    expect(screen.getByTestId('settings-nav-sandbox')).toHaveAttribute('aria-current', 'page')
    // The address names the section (FR-002).
    expect(history.location.search, 'BLOCKED RED (FR-002): the address must carry tab=sandbox').toContain('tab=sandbox')
  })

  it('BDD: a section switch adds one history entry, Back returns to the previous section, Forward returns again', async () => {
    await openSettings('?tab=gateway')
    expect(await screen.findByRole('heading', { name: 'Gateway', level: 2 })).toBeInTheDocument()
    // With no unsaved changes the switch must not ask (FR-014 gates on drafts).
    await userEvent.setup().click(screen.getByTestId('settings-nav-limits'))
    expect(await screen.findByRole('heading', { name: 'Limits', level: 2 })).toBeInTheDocument()
    expect(history.location.search, 'BLOCKED RED (FR-002): the address must name the new section').toContain('tab=limits')
    expect(history.length, 'BLOCKED RED (FR-002): each section switch adds ONE history entry').toBe(2)
    history.back()
    expect(await screen.findByRole('heading', { name: 'Gateway', level: 2 })).toBeInTheDocument()
    history.forward()
    expect(await screen.findByRole('heading', { name: 'Limits', level: 2 })).toBeInTheDocument()
  })
})

describe('FR-004 — the pre-fill link opens Limits with a filled, unsaved row', () => {
  it('D-3 row 14: ?provider=&model= with no tab opens Limits, Model overrides expanded, one new unsaved row', async () => {
    vi.mocked(getContextSettings).mockResolvedValue(CONTEXT_DEFAULTS)
    vi.mocked(fetchPerformanceSettings).mockResolvedValue({} as never)
    await openSettings('?provider=openrouter&model=acme/m1')
    expect(
      await screen.findByRole('heading', { name: 'Limits', level: 2 }),
      'BLOCKED RED (FR-004/D-3 row 14): the pre-fill link must open Limits',
    ).toBeInTheDocument()
    // Model overrides is expanded for the pre-fill (BDD: "the Model overrides
    // group is expanded"; FR-072's start-collapsed rule is for the plain visit).
    // aria-expanded lives on the ListGroup's DisclosureRow trigger inside the
    // group's section element.
    const group = screen.getByTestId('limits-group-overrides')
    const trigger = group.querySelector('[aria-expanded]')
    expect(trigger, 'the Model overrides group must render its disclosure trigger').not.toBeNull()
    expect(trigger).toHaveAttribute('aria-expanded', 'true')
    // One new unsaved row filled with the provider and model, empty context
    // length (the row appears once the section's read hydrates the form).
    expect(await screen.findByTestId('context-override-provider')).toHaveValue('openrouter')
    expect(screen.getByTestId('context-override-model')).toHaveValue('acme/m1')
    expect(screen.getByTestId('context-override-window')).toHaveValue('')
    // The row is a draft: nothing is stored until Save changes (FR-010).
    expect(screen.getByTestId('settings-save-bar')).toHaveTextContent('1 unsaved change')
  })
})

describe('FR-005 — the God Mode focus link lands on the God Mode button', () => {
  it('D-3 row 12: ?tab=gateway&focus=god-mode opens Protection with the button focused', async () => {
    await openSettings('?tab=gateway&focus=god-mode')
    expect(
      await screen.findByRole('heading', { name: 'Protection', level: 2 }),
      'BLOCKED RED (FR-005/D-3 row 12): focus=god-mode with any tab must open Protection',
    ).toBeInTheDocument()
    // The God Mode button has keyboard focus (FR-005; the button itself is
    // FR-051's "Turn on/off God Mode…"). It renders once its status read
    // settles — wait for it, then assert the focus.
    const button = await screen.findByTestId('god-mode-toggle')
    await waitFor(() => expect(document.activeElement).toBe(button))
    expect(button).toHaveTextContent(/Turn (on|off) God Mode…/)
  })

  it('FR-005: after focus moved, the address no longer carries focus and no history entry was added', async () => {
    await openSettings('?tab=gateway&focus=god-mode')
    await screen.findByRole('heading', { name: 'Protection', level: 2 })
    const button = await screen.findByTestId('god-mode-toggle')
    await waitFor(() => expect(document.activeElement).toBe(button))
    await waitFor(() => {
      expect(history.location.search, 'BLOCKED RED (FR-005): focus must leave the address').not.toContain('focus=god-mode')
    })
    // Removed without a new history entry (FR-005) — the replace consumed it.
    expect(history.length, 'FR-005: consuming focus must not add a history entry').toBe(1)
  })
})

describe('FR-067 — when the gateway does not answer', () => {
  it('one error Callout shows between the page title and the sections, saying values may be out of date, with Retry', async () => {
    vi.mocked(fetchAboutInfo).mockRejectedValue(new Error('connection refused'))
    await openSettings('?tab=gateway')
    const callout = await screen.findByTestId('settings-gateway-offline')
    expect(callout.getAttribute('class') ?? '', 'FR-067: the Callout is an error').toMatch(/error/)
    expect(callout).toHaveTextContent(/values may be out of date/i)
    // One thing to do: Retry.
    const retry = callout.querySelector('button')
    expect(retry, 'BLOCKED RED (FR-067): the Callout must offer Retry').not.toBeNull()
    expect(retry).toHaveTextContent(/retry/i)
    // Between the page title and the sections: after the header, before the
    // section content. The ScreenHeader is the banner holding the "Settings"
    // title (the app shell's own header precedes it and is not the page
    // title).
    const header = screen.getByRole('heading', { name: 'Settings' }).closest('header')
    const content = document.getElementById('settings-content')
    expect(header, 'the page title header must render').not.toBeNull()
    expect(content, 'the section content must render').not.toBeNull()
    expect(
      header && header.compareDocumentPosition(callout) & Node.DOCUMENT_POSITION_FOLLOWING,
      'FR-067: the Callout must sit after the page title',
    ).toBeTruthy()
    expect(
      callout.compareDocumentPosition(content as Node) & Node.DOCUMENT_POSITION_FOLLOWING,
      'FR-067: the Callout must sit before the sections',
    ).toBeTruthy()
  })

  it('the gateway answers: no offline Callout; with pairing switched on, still no device-pairing or Sign-in control (FR-055)', async () => {
    // The spec's edge case: device pairing switched on in the config file is
    // still hidden (FR-055, #1916; D-9 row 11). The old shell showed the
    // Devices tab whenever the flag was on — that is the replaced behaviour.
    vi.mocked(fetchAboutInfo).mockResolvedValue({
      version: '0.1.1',
      go_version: 'go1.24',
      os: 'linux',
      arch: 'amd64',
      uptime_seconds: 100,
      device_pairing_enabled: true,
    })
    await openSettings('?tab=providers')
    await screen.findByRole('heading', { name: 'Providers', level: 2 })
    await waitFor(() => {
      expect(screen.queryByTestId('settings-gateway-offline')).not.toBeInTheDocument()
    })
    // FR-055: no device pairing control and no Sign-in section anywhere.
    expect(screen.queryByText(/device pairing/i)).not.toBeInTheDocument()
    expect(screen.queryByRole('tab', { name: /devices/i })).not.toBeInTheDocument()
    expect(screen.queryByText('Sign-in')).not.toBeInTheDocument()
  })
})

// Every describe above renders the real route tree, so the part-5
// state-leakage rule ("route describes must run last in the file") is
// satisfied by construction — nothing plain-rendered follows or precedes
// them, and setup.ts unmounts after each case.

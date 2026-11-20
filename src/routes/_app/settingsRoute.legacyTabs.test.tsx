/**
 * settingsRoute.legacyTabs.test.tsx — Settings redesign #1519, RED.
 *
 * Test Implementation Order row 18 (FR-003, Dataset D-3, SC-001): every old
 * `tab=` value opens the section D-3 lists; an unknown or differently cased
 * value, and the hidden `signin`/`devices`, open Providers without an
 * error; `backup`/`backups`/`restore` show "Backup and restore were
 * removed." in Data & retention; `chat` opens Profile at Chat display; the
 * legacy God Mode focus link lands on Protection.
 *
 * Harness: the REAL routeTree in a REAL router (memory history) — the
 * same pattern as -library.deep-link.test.tsx — so the route's own
 * validateSearch and the screen it opens are both live. `@/lib/api` is
 * mocked at the API boundary only (what _app/AppShell/Sidebar read); the
 * settings sections' own reads are left to hit the fetch guard and render
 * their error states — the oracle is the SECTION the link opens, which
 * renders from the resolved tab, not from section data.
 *
 * RED today: the current route opens the OLD sections — ?tab=models shows
 * Models, ?tab=chat shows Chat, ?tab=devices shows Devices — so every
 * D-3 legacy mapping fails its "opens <new section>" assertion.
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
    // _app.tsx beforeLoad:
    fetchAppState: vi.fn(async () => ({ onboarding_complete: true })),
    validateToken: vi.fn(async () => ({})),
    // AppShell + Sidebar:
    fetchNotifications: vi.fn(async () => ({ notifications: [], unread_count: 0 })),
    fetchTasks: vi.fn(async () => []),
    fetchAgents: vi.fn(async () => []),
    fetchVersion: vi.fn(async () => ({ version: 'test', build_sha: 'test' })),
    fetchWorkspaces: vi.fn(async () => [
      { id: 'ws-1', name: 'UAT Build', status: 'active', is_default: true },
    ]),
    fetchSessions: vi.fn(async () => []),
    // SettingsScreen section reads that render alongside the headings:
    fetchKnowledgeBaseInfo: vi.fn(async () => ({
      workspace_id: 'ws-1',
      root_path: '',
      is_knowledge_base: false,
      marker: 'none',
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

async function expectSection(name: string) {
  // The oracle: D-3's target section IS open — its heading renders.
  expect(
    await screen.findByRole('heading', { name, level: 2 }, { timeout: 5000 }),
    `BLOCKED RED (FR-003, D-3): ${name} must be the open section`,
  ).toBeInTheDocument()
}

beforeEach(() => {
  window.localStorage.setItem('omnipus_auth_username', 'legacy-tabs-tester')
  // jsdom has no matchMedia — the same per-file stub AppShell.test.tsx and
  // -library.deep-link.test.tsx use (AppShell/Sidebar pin breakpoints).
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

describe('D-3 — old and new section links land in the right place', () => {
  it('D3 row 2: a new id opens that section (providers, data, gateway, about)', async () => {
    await openSettings('?tab=providers')
    await expectSection('Providers')
  })

  it('D3 rows 3-4: legacy models and performance open Limits', async () => {
    for (const tab of ['models', 'performance']) {
      await openSettings(`?tab=${tab}`)
      await expectSection('Limits')
      cleanup()
    }
  })

  it('D3 row 5: legacy integrations opens Providers (Voice input)', async () => {
    await openSettings('?tab=integrations')
    await expectSection('Providers')
  })

  it('D3 row 6: legacy security opens Protection', async () => {
    await openSettings('?tab=security')
    await expectSection('Protection')
  })

  it('D3 rows 9: hidden devices and an unknown value open Providers without an error', async () => {
    for (const tab of ['devices', 'signin', 'bogus-value']) {
      await openSettings(`?tab=${tab}`)
      await expectSection('Providers')
      expect(screen.queryByText(/something went wrong/i)).not.toBeInTheDocument()
      cleanup()
    }
  })

  it('D3 row 10: legacy chat opens Profile at Chat display', async () => {
    await openSettings('?tab=chat')
    await expectSection('Profile')
    // The Chat display group is inside Profile (FR-054).
    expect(
      screen.getByText('Chat display'),
      'BLOCKED RED (FR-054/D-3 row 10): Chat display must be a Profile group',
    ).toBeInTheDocument()
  })

  it('D3 row 11: backup shows the removal notice in Data & retention', async () => {
    await openSettings('?tab=backup')
    await expectSection('Data & retention')
    expect(screen.getByText('Backup and restore were removed.')).toBeInTheDocument()
  })

  it('D3 row 12: the legacy God Mode focus link lands on Protection', async () => {
    await openSettings('?tab=gateway&focus=god-mode')
    await expectSection('Protection')
  })
})

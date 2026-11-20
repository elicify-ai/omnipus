/**
 * DataSection.backup-removal-1919.test.tsx — RED tests for issue #1919
 * (founder decision P-Q3b B, 2026-11-19): Backup and Restore is removed
 * from Settings, Data.
 *
 * The NEW truth pinned here:
 *   1. The Data section shows NO backup UI in any edition — no "Backup &
 *      Restore" section, no Create backup / Restore buttons, and the
 *      heading is exactly "Data" even in local mode (the mode that used to
 *      show "Data & Backup").
 *   2. The section makes NO request to any backup endpoint
 *      (/api/v1/backups, /api/v1/backup, /api/v1/restore).
 *   3. CONTROLS (pass today, must keep passing): the neighbouring Data
 *      controls — storage stat boxes, session retention, danger zone —
 *      still render, and platform mode is unchanged.
 *
 * Oracle note (test-writing skill: oracle independence): every expected
 * value here comes from issue #1919's brief, not from the current
 * implementation. The real '@/lib/api' module is loaded — createBackup /
 * fetchBackups / restoreBackup are deliberately NOT mocked — so the fetch
 * edge sees every request the section actually makes. Only the surviving
 * read APIs are fixture-mocked (their zod-validated wire shapes are not
 * what this test exercises), and global fetch is stubbed at the process
 * edge: it records every URL and rejects relative inputs with the exact
 * TypeError src/test/setup.ts's guard produces, so the application's catch
 * behaviour is unchanged. After the developer removes the feature this
 * file compiles unchanged: it imports only APIs that survive the removal.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchConfig: vi.fn(),
    updateConfig: vi.fn(),
    fetchStorageStats: vi.fn(),
    fetchAppState: vi.fn(),
    clearAllSessions: vi.fn(),
    reAuth: vi.fn(),
  }
})

const mockAddToast = vi.fn()
vi.mock('@/store/ui', () => ({
  useUiStore: vi.fn(() => ({ addToast: mockAddToast })),
}))

import { fetchConfig, updateConfig, fetchStorageStats, fetchAppState } from '@/lib/api'
import { DataSection } from './DataSection'
import type { Config, StorageStats, AppState } from '@/lib/api'

// ── Fetch edge: the network instrument ───────────────────────────────────────

const fetchCalls: string[] = []
const fetchSpy = vi.fn(async (input: RequestInfo | URL) => {
  const url =
    typeof input === 'string' ? input : input instanceof URL ? input.toString() : input.url
  fetchCalls.push(url)
  // Mirror src/test/setup.ts's relative-URL guard: reject with the exact
  // same error type/message the app's request() catch path handles.
  return Promise.reject(new TypeError(`Failed to parse URL from ${url}`))
})

const backupEndpointPattern = /\/api\/v1\/(backups?|restore)(\?|$)/

function backupFetchCalls(): string[] {
  return fetchCalls.filter((u) => backupEndpointPattern.test(u))
}

// Settles every query the section starts — symmetric wait, no sleeps: today
// it also waits out the (removed-feature) backups query, after removal it
// settles as soon as the three legitimate reads finish.
async function renderSettled() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const utils = render(
    <QueryClientProvider client={client}>
      <DataSection />
    </QueryClientProvider>,
  )
  await waitFor(() => {
    expect(client.isFetching()).toBe(0)
  })
  return { ...utils, client }
}

// ── Fixtures ─────────────────────────────────────────────────────────────────

const baseConfig: Config = {
  gateway: { bind_address: '0.0.0.0', port: 5000 },
  security: { rate_limits: {} },
  data: { session_retention_days: 90 },
}

const baseStats: StorageStats = {
  workspace_size_bytes: 1024,
  session_count: 3,
  memory_entry_count: 10,
}

const localAppState = { onboarding_complete: true, identity: { mode: 'local' } } as AppState
const platformAppState = { onboarding_complete: true } as AppState

beforeEach(() => {
  vi.clearAllMocks()
  fetchCalls.length = 0
  vi.stubGlobal('fetch', fetchSpy)
  vi.mocked(fetchConfig).mockResolvedValue(baseConfig)
  vi.mocked(updateConfig).mockResolvedValue(baseConfig)
  vi.mocked(fetchStorageStats).mockResolvedValue(baseStats)
  vi.mocked(fetchAppState).mockResolvedValue(platformAppState)
})

afterEach(() => {
  vi.unstubAllGlobals()
})

// ── RED: the backup UI is gone (issue #1919) ─────────────────────────────────

describe('DataSection — issue #1919: Backup and Restore is removed', () => {
  it('local mode: no backup UI remains — no Backup & Restore section, no Create backup or Restore buttons, heading exactly "Data"', async () => {
    vi.mocked(fetchAppState).mockResolvedValue(localAppState)
    await renderSettled()

    // Heading must be exactly "Data" — the local-mode "Data & Backup"
    // variant is part of the removed feature.
    expect(screen.queryByText('Data')).toBeInTheDocument()
    expect(screen.queryByText('Data & Backup')).not.toBeInTheDocument()
    // No backup-flavoured text anywhere: section title, list states, error
    // states, dialog titles all used the word.
    expect(screen.queryByText(/backup/i)).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /create backup/i })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^restore$/i })).not.toBeInTheDocument()
    // The restore confirmation dialog must not exist either.
    expect(screen.queryByText('Restore backup?')).not.toBeInTheDocument()
  })

  it('local mode: the section makes no request to any backup endpoint', async () => {
    vi.mocked(fetchAppState).mockResolvedValue(localAppState)
    await renderSettled()

    expect(backupFetchCalls()).toEqual([])
  })

  // ── CONTROLS: pass today, must keep passing ────────────────────────────────

  it('CONTROL local mode: the neighbouring Data controls still render — storage stats, session retention, danger zone', async () => {
    vi.mocked(fetchAppState).mockResolvedValue(localAppState)
    await renderSettled()

    // Storage stat boxes with the fixture values (formatBytes(1024) = "1.0 KB").
    expect(screen.getByText('Workspace')).toBeInTheDocument()
    expect(screen.getByText('1.0 KB')).toBeInTheDocument()
    expect(screen.getByText('Sessions')).toBeInTheDocument()
    expect(screen.getByText('3')).toBeInTheDocument()
    expect(screen.getByText('Memory entries')).toBeInTheDocument()
    expect(screen.getByText('10')).toBeInTheDocument()
    // Session retention: header + hydrated persisted value.
    expect(screen.getByText('Session Retention')).toBeInTheDocument()
    expect(screen.getByDisplayValue('90')).toBeInTheDocument()
    // Danger zone.
    expect(screen.getByText('Danger Zone')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /clear sessions/i })).toBeInTheDocument()
  })

  it('CONTROL platform mode: Data section unchanged — no backup UI and no backup request (hidden there today, stays hidden after removal)', async () => {
    vi.mocked(fetchAppState).mockResolvedValue(platformAppState)
    await renderSettled()

    expect(screen.queryByText('Data')).toBeInTheDocument()
    expect(screen.queryByText(/backup/i)).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /create backup/i })).not.toBeInTheDocument()
    expect(backupFetchCalls()).toEqual([])
  })
})

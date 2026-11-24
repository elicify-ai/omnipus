/**
 * DataSection.backup-removal-1919.test.tsx — issue #1919 (founder decision
 * P-Q3b B, 2026-11-19): Backup and Restore is removed; Settings redesign
 * #1519 carries it forward as FR-061 ("No backup or restore control MUST
 * appear; old links show the FR-003 notice") and D-3 row 11 (`backup`,
 * `backups`, `restore` land in Data & retention with the notice "Backup and
 * restore were removed.").
 *
 * The truth pinned here:
 *   1. The Data & retention section shows NO backup UI in any edition — no
 *      "Backup & Restore" section, no Create backup / Restore buttons, no
 *      backup-flavoured text. The local-mode "Data & Backup" heading variant
 *      is part of the removed feature; the section is Data & retention now
 *      (the shell renders its heading; the section body has no backup text).
 *   2. The section makes NO request to any backup endpoint
 *      (/api/v1/backups, /api/v1/backup, /api/v1/restore).
 *   3. FR-003/D-3 row 11: with the backup link flag set (the route resolves
 *      `?tab=backup` to this section with `backupRemoved`), the notice
 *      "Backup and restore were removed." shows. Without the flag, no notice.
 *   4. CONTROLS (pass today, must keep passing): the neighbouring Data &
 *      retention controls — storage stat boxes, the retention fields, the
 *      Danger zone's "Clear all chat history" (FR-108) — still render.
 *
 * Oracle note (test-writing skill: oracle independence): every expected
 * value comes from the #1919 brief and the spec (FR-003, FR-061, FR-108,
 * D-3 row 11), not from the implementation. The real '@/lib/api' module is
 * loaded — createBackup / fetchBackups / restoreBackup are deliberately NOT
 * mocked — and global fetch is stubbed at the process edge: it records every
 * URL and rejects relative inputs with the exact TypeError src/test/setup.ts
 * 's guard produces, so the application's catch behaviour is unchanged.
 *
 * Harness: the redesign's real section body — DataRetentionSection, which
 * embeds the old DataSection (storage counts + Danger zone). Loaded through
 * import.meta.glob so the file compiles on the pre-redesign tree and fails
 * loudly as BLOCKED RED there, never as an import error.
 */

import type { ComponentType } from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchStorageStats: vi.fn(),
    fetchAppState: vi.fn(),
    clearAllSessions: vi.fn(),
    // The retention read of the Data & retention section (FR-041 shape).
    fetchRetentionSettings: vi.fn(async () => ({
      session_days: 90,
      memory_retros_days: 180,
      memory_retros_forever: false,
      keep_everything_forever: false,
    })),
    updateRetentionSettings: vi.fn(),
    fetchConfig: vi.fn(),
  }
})

const mockAddToast = vi.fn()
vi.mock('@/store/ui', () => ({
  useUiStore: vi.fn(() => ({ addToast: mockAddToast })),
}))

import { fetchStorageStats, fetchAppState } from '@/lib/api'

// The Data & retention section body exists only on the redesign tree; a
// missing module is a loud per-case failure, not a skipped test or an
// import/collection error.
// ComponentType<P> defaults to P = {} — the bare type rejects the
// backupRemoved prop the section really takes (DataRetentionSection.tsx).
const sectionModules = import.meta.glob<{ DataRetentionSection: ComponentType<{ backupRemoved?: boolean }> }>('./DataRetentionSection.tsx')
async function loadDataRetentionSection() {
  const load = sectionModules['./DataRetentionSection.tsx']
  if (!load) {
    expect.fail(
      'BLOCKED RED (FR-061/FR-003/D-3 row 11/D-9 row 9): DataRetentionSection.tsx not implemented — the Data & retention section body that must show no backup UI and the removal notice',
    )
  }
  const module = await load()
  expect(module, 'the Data & retention section body must export DataRetentionSection').toHaveProperty('DataRetentionSection')
  return module.DataRetentionSection
}

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

async function renderSettled({ backupRemoved = false }: { backupRemoved?: boolean } = {}) {
  const Section = await loadDataRetentionSection()
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const utils = render(
    <QueryClientProvider client={client}>
      <Section backupRemoved={backupRemoved} />
    </QueryClientProvider>,
  )
  await waitFor(() => {
    expect(client.isFetching()).toBe(0)
  })
  return { ...utils, client }
}

// ── Fixtures ─────────────────────────────────────────────────────────────────

const baseStats = {
  workspace_size_bytes: 1024,
  session_count: 3,
  memory_entry_count: 10,
}

const localAppState = { onboarding_complete: true, identity: { mode: 'local' } }
const platformAppState = { onboarding_complete: true }

beforeEach(() => {
  vi.clearAllMocks()
  fetchCalls.length = 0
  vi.stubGlobal('fetch', fetchSpy)
  vi.mocked(fetchStorageStats).mockResolvedValue(baseStats)
  vi.mocked(fetchAppState).mockResolvedValue(platformAppState as never)
})

afterEach(() => {
  vi.unstubAllGlobals()
})

// ── The backup UI is gone (#1919 → FR-061) ───────────────────────────────────

describe('Data & retention — issue #1919 / FR-061: Backup and Restore is removed', () => {
  it('local mode: no backup UI remains — no Backup & Restore section, no Create backup or Restore buttons, no backup text', async () => {
    vi.mocked(fetchAppState).mockResolvedValue(localAppState as never)
    await renderSettled()

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
    vi.mocked(fetchAppState).mockResolvedValue(localAppState as never)
    await renderSettled()

    expect(backupFetchCalls()).toEqual([])
  })

  it('D-3 row 11: with the backup link flag, the notice "Backup and restore were removed." shows (FR-003/FR-061)', async () => {
    await renderSettled({ backupRemoved: true })
    expect(
      await screen.findByText('Backup and restore were removed.'),
      'BLOCKED RED (FR-003/D-3 row 11): an old backup link must show the removal notice in Data & retention',
    ).toBeInTheDocument()
  })

  it('without the flag no removal notice shows (the notice is the old-link answer, not the section default)', async () => {
    await renderSettled()
    expect(screen.queryByText('Backup and restore were removed.')).not.toBeInTheDocument()
  })

  // ── CONTROLS: pass today, must keep passing ────────────────────────────────

  it('CONTROL: the neighbouring Data & retention controls still render — storage stats, retention fields, the FR-108 danger zone', async () => {
    await renderSettled()

    // Storage stat boxes with the fixture values (formatBytes(1024) = "1.0 KB").
    expect(screen.getByText('Workspace')).toBeInTheDocument()
    expect(screen.getByText('1.0 KB')).toBeInTheDocument()
    expect(screen.getByText('Sessions')).toBeInTheDocument()
    expect(screen.getByText('3')).toBeInTheDocument()
    expect(screen.getByText('Memory entries')).toBeInTheDocument()
    expect(screen.getByText('10')).toBeInTheDocument()
    // The retention fields live in this section now (D-9 row 9: kept/moved here).
    // The days field and the memory-forever switch are asserted by their
    // distinguishing queries — the exact label oracles live in
    // dataRetention.section.test.tsx.
    expect(screen.getByLabelText(/^keep chat history for/i)).toBeInTheDocument()
    // The memory-forever switch (the spec's exact label oracle, "Keep
    // forever", lives in dataRetention.section.test.tsx; this CONTROL only
    // requires the switch to render).
    expect(screen.getByRole('switch', { name: /keep.*forever/i })).toBeInTheDocument()
    // Danger zone with the FR-108 wording.
    expect(screen.getByRole('button', { name: /clear all chat history/i })).toBeInTheDocument()
  })

  it('CONTROL: with the backup flag set the controls still render alongside the notice', async () => {
    await renderSettled({ backupRemoved: true })

    expect(await screen.findByText('Backup and restore were removed.')).toBeInTheDocument()
    expect(screen.getByText('Workspace')).toBeInTheDocument()
    expect(screen.getByLabelText(/keep chat history for/i)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /clear all chat history/i })).toBeInTheDocument()
  })
})

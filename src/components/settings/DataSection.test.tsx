/**
 * DataSection.test.tsx — targeted coverage for a data-loss-adjacent settings
 * panel (backup / restore / clear-all-sessions) that had ZERO test coverage
 * before this file. `SettingsScreen.test.tsx` explicitly stubs DataSection
 * out (`vi.mock('@/components/settings/DataSection', () => ({ DataSection:
 * () => null }))`), so there was no incidental coverage either.
 *
 * Wave 2 converted this component's three mutation `onError` handlers from a
 * hand-rolled ternary to the shared `getErrorMessage()` helper
 * (`src/lib/api-error.ts`). These tests pin that conversion: a rejected
 * mutation must surface `ApiError.userMessage` (or `Error.message` as the
 * fallback) via `addToast`, not a crash, not a generic "isError" flag, and
 * not the status-prefixed legacy `Error.message` string.
 *
 * Traces to: Wave 2 findings-fix (task #149, gap 1) — pr-test-analyzer,
 * hotfix/v0.1.1.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

// ── Module mocks ──────────────────────────────────────────────────────────────

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchConfig: vi.fn(),
    updateConfig: vi.fn(),
    fetchStorageStats: vi.fn(),
    createBackup: vi.fn(),
    fetchBackups: vi.fn(),
    restoreBackup: vi.fn(),
    clearAllSessions: vi.fn(),
  }
})

const mockAddToast = vi.fn()
vi.mock('@/store/ui', () => ({
  useUiStore: vi.fn(() => ({ addToast: mockAddToast })),
}))

import {
  fetchConfig,
  updateConfig,
  fetchStorageStats,
  createBackup,
  fetchBackups,
  restoreBackup,
  clearAllSessions,
  ApiError,
} from '@/lib/api'
import { DataSection } from './DataSection'
import type { Config, StorageStats, BackupEntry } from '@/lib/api'

// ── Helpers ───────────────────────────────────────────────────────────────────

function makeClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
}

function renderSection() {
  const client = makeClient()
  const utils = render(
    <QueryClientProvider client={client}>
      <DataSection />
    </QueryClientProvider>,
  )
  return { ...utils, client }
}

const baseConfig: Config = {
  gateway: { bind_address: '0.0.0.0', port: 5000 },
  security: { policy_mode: 'allow', exec_approval: 'auto', rate_limits: {} },
  data: { session_retention_days: 90 },
}

const baseStats: StorageStats = {
  workspace_size_bytes: 1024,
  session_count: 3,
  memory_entry_count: 10,
}

const oneBackup: BackupEntry[] = [
  { filename: 'omnipus-backup-2026-05-16T10-00-00Z.tar.gz', size_bytes: 2048, created_at: '2026-05-16T10:00:00Z' },
]

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(fetchConfig).mockResolvedValue(baseConfig)
  vi.mocked(updateConfig).mockResolvedValue(baseConfig)
  vi.mocked(fetchStorageStats).mockResolvedValue(baseStats)
  vi.mocked(fetchBackups).mockResolvedValue([])
})

// ── describe: rejected mutations render getErrorMessage() text ──────────────

describe('DataSection — rejected mutations render getErrorMessage() text', () => {
  it('backup creation failure shows ApiError.userMessage via addToast (not a crash, not just isError)', async () => {
    vi.mocked(createBackup).mockRejectedValue(new ApiError(500, undefined))
    renderSection()

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /create backup/i })).toBeInTheDocument()
    })
    fireEvent.click(screen.getByRole('button', { name: /create backup/i }))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        message: 'The server is unavailable. Please try again in a moment.',
        variant: 'error',
      })
    })
    // The component itself must not have crashed — the section is still
    // rendered and interactive.
    expect(screen.getByRole('button', { name: /create backup/i })).toBeInTheDocument()
  })

  it('restore failure shows a DIFFERENT ApiError.userMessage than the backup-failure test (proves the message is read from the error, not hardcoded)', async () => {
    vi.mocked(fetchBackups).mockResolvedValue(oneBackup)
    vi.mocked(restoreBackup).mockRejectedValue(new ApiError(409, undefined))
    renderSection()

    await waitFor(() => {
      expect(screen.getByText(oneBackup[0].filename)).toBeInTheDocument()
    })
    // Open the restore confirmation dialog for the single backup row.
    fireEvent.click(screen.getByRole('button', { name: /^restore$/i }))

    const dialog = await screen.findByRole('dialog')
    fireEvent.click(within(dialog).getByRole('button', { name: /^restore$/i }))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        message: 'This conflicts with the current state. Please refresh and try again.',
        variant: 'error',
      })
    })
  })

  it('clear-all-sessions failure (data-loss-adjacent) shows ApiError.userMessage, not the isError flag alone', async () => {
    vi.mocked(clearAllSessions).mockRejectedValue(new ApiError(403, undefined))
    renderSection()

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /clear sessions/i })).toBeInTheDocument()
    })
    fireEvent.click(screen.getByRole('button', { name: /clear sessions/i }))

    const dialog = await screen.findByRole('dialog')
    fireEvent.click(within(dialog).getByRole('button', { name: /^clear all$/i }))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        message: "You don't have permission to perform this action.",
        variant: 'error',
      })
    })
  })

  it('falls back to Error.message when the rejection is a plain Error (not an ApiError) — getErrorMessage() priority chain', async () => {
    vi.mocked(createBackup).mockRejectedValue(new Error('disk quota exceeded'))
    renderSection()

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /create backup/i })).toBeInTheDocument()
    })
    fireEvent.click(screen.getByRole('button', { name: /create backup/i }))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        message: 'disk quota exceeded',
        variant: 'error',
      })
    })
  })
})

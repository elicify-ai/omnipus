/**
 * DataSection.test.tsx — targeted coverage for a data-loss-adjacent settings
 * panel (clear-all-sessions, session retention) that had ZERO test coverage
 * before this file. `SettingsScreen.test.tsx` explicitly stubs DataSection
 * out (`vi.mock('@/components/settings/DataSection', () => ({ DataSection:
 * () => null }))`), so there was no incidental coverage either.
 *
 * Wave 2 converted this component's mutation `onError` handlers from a
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
    fetchAppState: vi.fn(),
    createBackup: vi.fn(),
    fetchBackups: vi.fn(),
    restoreBackup: vi.fn(),
    clearAllSessions: vi.fn(),
    reAuth: vi.fn(),
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
  fetchAppState,
  createBackup,
  fetchBackups,
  restoreBackup,
  clearAllSessions,
  reAuth,
  ApiError,
} from '@/lib/api'
import { DataSection } from './DataSection'
import type { Config, StorageStats, AppState } from '@/lib/api'
import type { BackupEntry } from '@/lib/api/generated/openapi-types'

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
  security: { policy_mode: 'allow', rate_limits: {} },
  data: { session_retention_days: 90 },
}

const baseStats: StorageStats = {
  workspace_size_bytes: 1024,
  session_count: 3,
  memory_entry_count: 10,
}

// WP4 (ADR-0010): platform mode is the DEFAULT test fixture for AppState —
// no `identity` field at all, which is exactly what this branch sees today
// (the field lands with a parallel lane, WP1). DataSection must fail CLOSED
// (hide the backup section) in that shape, same as an absent/unknown
// identity would on a real hosted/desktop build.
const platformAppState = { onboarding_complete: true } as AppState
const localAppState = { onboarding_complete: true, identity: { mode: 'local' } } as AppState

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(fetchConfig).mockResolvedValue(baseConfig)
  vi.mocked(updateConfig).mockResolvedValue(baseConfig)
  vi.mocked(fetchStorageStats).mockResolvedValue(baseStats)
  vi.mocked(fetchAppState).mockResolvedValue(platformAppState)
  vi.mocked(fetchBackups).mockResolvedValue([])
})

// ── describe: rejected mutations render getErrorMessage() text ──────────────

describe('DataSection — rejected mutations render getErrorMessage() text', () => {
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
    vi.mocked(clearAllSessions).mockRejectedValue(new Error('disk quota exceeded'))
    renderSection()

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /clear sessions/i })).toBeInTheDocument()
    })
    fireEvent.click(screen.getByRole('button', { name: /clear sessions/i }))

    const dialog = await screen.findByRole('dialog')
    fireEvent.click(within(dialog).getByRole('button', { name: /^clear all$/i }))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        message: 'disk quota exceeded',
        variant: 'error',
      })
    })
  })
})

// D3 (UAT v0.1.1 defects) — hydration must never trigger a spurious PUT.
//
// Root cause: `retentionDays` starts at the hardcoded useState default
// '90'. Before this fix, useAutoSave's `disabled` option here was
// `!config` — but `config` turns truthy in the SAME commit the hydration
// effect is SCHEDULED, one render before the effect's own `setState` call
// actually lands. So `disabled` flipped false one render too early,
// useAutoSave captured the hardcoded '90' default as its baseline, and the
// LATER commit where the real persisted value hydrates looked like a
// genuine edit — firing a spurious `updateConfig` that echoes the fetched
// value straight back.
describe('DataSection — D3: hydration must not trigger a spurious PUT', () => {
  it('loading a retention value that differs from the hardcoded "90" default never calls updateConfig, even after the debounce window elapses (REVERT-PROOF: fails without the retentionHydrated gate)', async () => {
    vi.mocked(fetchConfig).mockResolvedValue({
      ...baseConfig,
      data: { session_retention_days: 45 },
    })
    renderSection()

    await waitFor(() => {
      expect(screen.getByDisplayValue('45')).toBeInTheDocument()
    })

    // PASSIVE idle wait — no interaction at all — comfortably past the
    // 500ms default debounce.
    await new Promise((resolve) => setTimeout(resolve, 900))
    expect(updateConfig).not.toHaveBeenCalled()
  })
})

// WP4 (ADR-0010): local backup, off in platform mode. These tests pin the
// SPA half of the switch — the section must be entirely absent (not
// disabled, not a 404 error state) in platform mode, and fully functional
// (create + list + restore) in local mode.
describe('DataSection — WP4: backup/restore gated by AppState identity.mode', () => {
  it('platform mode: no Backup & Restore section, no backup network call', async () => {
    vi.mocked(fetchAppState).mockResolvedValue(platformAppState)
    renderSection()

    await waitFor(() => {
      expect(screen.getByText('Data')).toBeInTheDocument()
    })
    expect(screen.queryByText('Backup & Restore')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /create backup/i })).not.toBeInTheDocument()
    expect(fetchBackups).not.toHaveBeenCalled()
  })

  it('absent identity field (pre-WP1, today\'s actual AppState shape) also hides the section — fails closed, not open', async () => {
    vi.mocked(fetchAppState).mockResolvedValue({ onboarding_complete: true } as AppState)
    renderSection()

    await waitFor(() => {
      expect(screen.getByText('Data')).toBeInTheDocument()
    })
    expect(screen.queryByText('Backup & Restore')).not.toBeInTheDocument()
  })

  it('local mode: shows Backup & Restore, lists existing archives, and creates a new one', async () => {
    vi.mocked(fetchAppState).mockResolvedValue(localAppState)
    const backup: BackupEntry = {
      filename: 'backup-20260101T000000Z.tar.gz',
      size_bytes: 2048,
      created_at: '2026-01-01T00:00:00Z',
    }
    vi.mocked(fetchBackups).mockResolvedValue([backup])
    vi.mocked(createBackup).mockResolvedValue({
      path: '/home/user/.omnipus/backups/backup-20260102T000000Z.tar.gz',
      size_bytes: 4096,
      created_at: '2026-01-02T00:00:00Z',
    })
    renderSection()

    await waitFor(() => {
      expect(screen.getByText('Data & Backup')).toBeInTheDocument()
    })
    await waitFor(() => {
      expect(screen.getByText(backup.filename)).toBeInTheDocument()
    })

    fireEvent.click(screen.getByRole('button', { name: /create backup/i }))
    await waitFor(() => {
      expect(createBackup).toHaveBeenCalled()
    })
    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        message: expect.stringContaining('Backup created:'),
        variant: 'success',
      })
    })
  })

  // Restore overwrites the whole vault, so after the plain confirmation it
  // takes the step-up gate (WP3): local mode is password mode, and the gate
  // is dialog-first — ReAuthDialog opens BEFORE any request, and the one
  // POST carries the minted consent token.
  it('local mode: restoring a listed backup calls restoreBackup with its filename after confirmation and re-auth', async () => {
    vi.mocked(fetchAppState).mockResolvedValue(localAppState)
    const backup: BackupEntry = {
      filename: 'backup-20260101T000000Z.tar.gz',
      size_bytes: 2048,
      created_at: '2026-01-01T00:00:00Z',
    }
    vi.mocked(fetchBackups).mockResolvedValue([backup])
    vi.mocked(restoreBackup).mockResolvedValue(undefined)
    vi.mocked(reAuth).mockResolvedValue({ verified: true, token: 'reauth_tok', expires_in: 300 } as never)
    renderSection()

    await waitFor(() => {
      expect(screen.getByText(backup.filename)).toBeInTheDocument()
    })
    fireEvent.click(screen.getByRole('button', { name: /^restore$/i }))

    const dialog = await screen.findByRole('dialog')
    fireEvent.click(within(dialog).getByRole('button', { name: /^restore$/i }))

    // Password prompt first; nothing has been sent yet.
    await waitFor(() => {
      expect(screen.getByTestId('reauth-password-input')).toBeInTheDocument()
    })
    expect(restoreBackup).not.toHaveBeenCalled()

    fireEvent.change(screen.getByTestId('reauth-password-input'), { target: { value: 'mypassword' } })
    fireEvent.click(screen.getByTestId('reauth-confirm'))

    await waitFor(() => {
      expect(reAuth).toHaveBeenCalledWith('mypassword')
      expect(restoreBackup).toHaveBeenCalledTimes(1)
      expect(restoreBackup).toHaveBeenCalledWith(backup.filename, 'reauth_tok')
    })
    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        message: 'Restore complete. Restart gateway to apply.',
        variant: 'success',
      })
    })
  })
})

/**
 * ExecProxyStatusCard.test.tsx — targeted coverage for a security-relevant
 * settings control (the bash-tool SSRF proxy enable/disable toggle) that had
 * ZERO test coverage before this file.
 *
 * Wave 2 converted this component's mutation `onError` handler from a
 * hand-rolled ternary to the shared `getErrorMessage()` helper
 * (`src/lib/api-error.ts`). These tests pin that conversion: a rejected
 * `updateConfig` save must surface `ApiError.userMessage` (or `Error.message`
 * as the fallback) via `addToast`, prefixed by the component's own
 * "Failed to save proxy setting:" text — not a crash, not a generic
 * "isError" flag, and not the status-prefixed legacy `Error.message` string.
 *
 * Traces to: Wave 2 findings-fix (task #149, gap 1) — pr-test-analyzer,
 * hotfix/v0.1.1.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

// ── Module mocks ──────────────────────────────────────────────────────────────

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchExecProxyStatus: vi.fn(),
    updateConfig: vi.fn(),
  }
})

const mockAddToast = vi.fn()
vi.mock('@/store/ui', () => ({
  useUiStore: vi.fn(() => ({ addToast: mockAddToast })),
}))

import { fetchExecProxyStatus, updateConfig, ApiError } from '@/lib/api'
import { ExecProxyStatusCard } from './ExecProxyStatusCard'
import type { ExecProxyStatus } from '@/lib/api'

// ── Helpers ───────────────────────────────────────────────────────────────────

function makeClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
}

function renderCard() {
  return render(
    <QueryClientProvider client={makeClient()}>
      <ExecProxyStatusCard />
    </QueryClientProvider>,
  )
}

const baseStatus: ExecProxyStatus = { enabled: false, running: false }

/** Toggle the switch and confirm the resulting RestartConfirmDialog. */
async function toggleAndConfirm() {
  // The switch renders immediately (before fetchExecProxyStatus resolves) but
  // stays disabled={isLoading || isSaving} until the query settles — wait for
  // it to become enabled, not just present, or the click below is a no-op.
  await waitFor(() => {
    expect(screen.getByRole('switch')).not.toBeDisabled()
  })
  fireEvent.click(screen.getByRole('switch'))

  await waitFor(() => {
    expect(screen.getByRole('button', { name: /save & restart later/i })).toBeInTheDocument()
  })
  fireEvent.click(screen.getByRole('button', { name: /save & restart later/i }))
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(fetchExecProxyStatus).mockResolvedValue(baseStatus)
})

// ── describe: rejected save renders getErrorMessage() text ──────────────────

describe('ExecProxyStatusCard — rejected save shows getErrorMessage() text', () => {
  it('shows ApiError.userMessage prefixed by "Failed to save proxy setting:" (not a crash, not just isError)', async () => {
    vi.mocked(updateConfig).mockRejectedValue(new ApiError(500, undefined))
    renderCard()

    await toggleAndConfirm()

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        message: 'Failed to save proxy setting: The server is unavailable. Please try again in a moment.',
        variant: 'error',
      })
    })
    // The optimistic toggle intent is cleared on error — the switch reverts
    // to the last-known server value (false) rather than lying about success.
    await waitFor(() => {
      expect(screen.getByRole('switch')).not.toBeChecked()
    })
  })

  it('shows a DIFFERENT ApiError.userMessage for a 403 rejection (proves the message is read from the error, not hardcoded)', async () => {
    vi.mocked(updateConfig).mockRejectedValue(new ApiError(403, undefined))
    renderCard()

    await toggleAndConfirm()

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        message: "Failed to save proxy setting: You don't have permission to perform this action.",
        variant: 'error',
      })
    })
  })

  it('falls back to Error.message when the rejection is a plain Error (not an ApiError) — getErrorMessage() priority chain', async () => {
    vi.mocked(updateConfig).mockRejectedValue(new Error('proxy bind failed: address already in use'))
    renderCard()

    await toggleAndConfirm()

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        message: 'Failed to save proxy setting: proxy bind failed: address already in use',
        variant: 'error',
      })
    })
  })
})

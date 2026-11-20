/**
 * DataSection.test.tsx — the Danger-zone row of Settings → Data & retention.
 *
 * Settings redesign #1519 changed two things this file covered:
 *   1. FR-108 (#1265, wording only — Q5 A): the Danger-zone row and its
 *      confirm say "Clear all chat history". The action, its confirm step
 *      and who may see it stay as they are — so the Wave-2 error-surfacing
 *      cases keep their oracles (getErrorMessage() text via addToast) and
 *      only the button name follows FR-108.
 *   2. FR-010/FR-043: session retention no longer lives in this section and
 *      no longer autosaves through `updateConfig` — retention moved to the
 *      Data & retention section's one write path (PUT /settings/retention,
 *      covered by dataRetention.section.test.tsx). The old D3 describe
 *      ("hydration must not trigger a spurious useAutoSave PUT") tested the
 *      retired auto-save model; FR-010 replaces it ("this removes
 *      useAutoSave from Settings", #346 — the spec's regression table row
 *      "useAutoSave unmount flush swallows save errors → Fixed by removal →
 *      FR-010"), and its "nothing is stored until Save changes" concern is
 *      carried by dataRetention.section.test.tsx's FR-046 and FR-040 cases,
 *      which send nothing until Save changes is pressed.
 *
 * Traces to: FR-108; Wave 2 findings-fix (task #149, gap 1) for the
 * getErrorMessage() oracles, whose behaviour FR-108 keeps unchanged.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

// ── Module mocks ──────────────────────────────────────────────────────────────

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchStorageStats: vi.fn(),
    clearAllSessions: vi.fn(),
  }
})

const mockAddToast = vi.fn()
vi.mock('@/store/ui', () => ({
  useUiStore: vi.fn(() => ({ addToast: mockAddToast })),
}))

import { fetchStorageStats, clearAllSessions, ApiError } from '@/lib/api'
import { DataSection } from './DataSection'
import type { StorageStats } from '@/lib/api'

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

const baseStats: StorageStats = {
  workspace_size_bytes: 1024,
  session_count: 3,
  memory_entry_count: 10,
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(fetchStorageStats).mockResolvedValue(baseStats)
})

// ── FR-108 wording + Wave-2 error surfacing (behaviour unchanged) ────────────

describe('DataSection — the Danger-zone row says "Clear all chat history" (FR-108) and its failures surface getErrorMessage()', () => {
  it('clear-all failure (data-loss-adjacent) shows ApiError.userMessage, not the isError flag alone', async () => {
    vi.mocked(clearAllSessions).mockRejectedValue(new ApiError(403, undefined))
    renderSection()

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /clear all chat history/i })).toBeInTheDocument()
    })
    fireEvent.click(screen.getByRole('button', { name: /clear all chat history/i }))

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
      expect(screen.getByRole('button', { name: /clear all chat history/i })).toBeInTheDocument()
    })
    fireEvent.click(screen.getByRole('button', { name: /clear all chat history/i }))

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

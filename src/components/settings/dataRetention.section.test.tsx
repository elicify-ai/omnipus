/**
 * dataRetention.section.test.tsx — Settings redesign #1519, RED.
 *
 * Test Implementation Order row 26 (spec order 25: Retention UI scenarios,
 * US-5; FR-040, FR-046, FR-048, FR-049, FR-052; BDD "Fresh-install
 * defaults", "Saving both day counts", "With Keep forever on the day field
 * is off and not sent", "A hand-edited value outside the range is shown and
 * blocks Save", "The legacy keep-everything switch is shown, not hidden",
 * "Turning Keep forever off again resumes deletion" (description wording),
 * "Memory & recaps holds only a link to retention").
 *
 * Oracles (spec, never code):
 *   - S-Q13 A defaults: 90 / 180 / Keep forever off.
 *   - Saving 30 + 365 sends ONE update with both values; they show after a
 *     reload (the section re-reads).
 *   - FR-040: while Keep forever is on, "Keep memory for" is disabled, not
 *     validated and NOT SENT — the body carries memory_retros_forever only.
 *   - D-1 row 6 / FR-042: 366 → "Enter a whole number from 1 to 365." and
 *     Save unavailable. D-1 row 10 / FR-046: a stored 500 shows as stored
 *     with the same range error and Save unavailable until fixed.
 *   - FR-048: keep_everything_forever (legacy config-file switch) shows an
 *     info notice: both kept forever because of a setting in the
 *     configuration file; the day counts apply once it is removed; fields
 *     stay editable.
 *   - FR-049: the switch's description says older reflections are deleted at
 *     the next nightly clean-up when it is turned off.
 *   - FR-052: Memory & recaps shows no retention field, only a row "How
 *     long memory is kept" that opens Data & retention.
 *
 * Harness: the REAL DataSection (and MemorySection) with '@/lib/api' mocked
 * at the API boundary — the mock adds the section's future wrappers
 * `fetchRetentionSettings` / `updateRetentionSettings` (the generated client
 * does not carry them yet; regeneration is a GREEN-side step, contracts are
 * committed). "Uses the generated response validator" (order row note): the
 * REAL component consumes the generated client at GREEN; the mock stands in
 * for it here at the boundary. RED today: the section reads no
 * /settings/retention and renders no retention fields — assertions fail for
 * the spec's reason.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import userEvent from '@testing-library/user-event'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    // The section's CURRENT reads (DataSection today): the config view needs
    // data.session_retention_days; stats need workspace_size_bytes.
    fetchConfig: vi.fn(async () => ({ gateway: {}, data: { session_retention_days: 90 } })),
    fetchStorageStats: vi.fn(async () => ({
      workspace_size_bytes: 10,
      session_count: 3,
      memory_entry_count: 5,
      warnings: [],
    })),
    fetchMemorySettings: vi.fn(async () => ({
      auto_recap_enabled: true,
      bootstrap_recap_enabled: true,
      bootstrap_recap_max_per_minute: 5,
      idle_timeout_minutes: 30,
      recap_model: '',
      recap_fallback_models: [],
      recap_provider: '',
    })),
    // The section's future retention wrappers (FR-041/FR-043 shapes).
    fetchRetentionSettings: vi.fn(async () => ({
      session_days: 90,
      memory_retros_days: 180,
      memory_retros_forever: false,
      keep_everything_forever: false,
    })),
    updateRetentionSettings: vi.fn(async (_body: unknown) => ({
      session_days: 90,
      memory_retros_days: 180,
      memory_retros_forever: false,
      keep_everything_forever: false,
    })),
  }
})

import * as api from '@/lib/api'
import { DataSection } from './DataSection'
import { MemorySection } from './MemorySection'

function renderSection(ui: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(<QueryClientProvider client={qc}>{ui}</QueryClientProvider>)
}

function retentionOf(): Record<string, unknown> {
  return vi.mocked(api.updateRetentionSettings).mock.calls.at(-1)?.[0] as Record<string, unknown>
}

beforeEach(() => {
  vi.clearAllMocks()
})

describe('US-5 — Data & retention shows and saves the retention values', () => {
  it('a fresh install shows 90 / 180 with Keep forever off (S-Q13 A)', async () => {
    renderSection(<DataSection />)
    expect(await screen.findByLabelText('Keep chat history for')).toHaveValue(90)
    expect(screen.getByLabelText('Keep memory for')).toHaveValue(180)
    expect(screen.getByRole('switch', { name: /keep forever/i })).not.toBeChecked()
  })

  it('saving 30 and 365 sends one update carrying both values (one write path, FR-043)', async () => {
    renderSection(<DataSection />)
    const chat = await screen.findByLabelText('Keep chat history for')
    await userEvent.clear(chat)
    await userEvent.type(chat, '30')
    const memory = screen.getByLabelText('Keep memory for')
    await userEvent.clear(memory)
    await userEvent.type(memory, '365')
    await userEvent.click(screen.getByRole('button', { name: /save changes/i }))
    expect(api.updateRetentionSettings).toHaveBeenCalledTimes(1)
    expect(retentionOf()).toEqual({ session_days: 30, memory_retros_days: 365 })
  })

  it('366 is blocked with the exact range text and Save stays unavailable (D-1 row 6)', async () => {
    renderSection(<DataSection />)
    const chat = await screen.findByLabelText('Keep chat history for')
    await userEvent.clear(chat)
    await userEvent.type(chat, '366')
    expect(await screen.findByText('Enter a whole number from 1 to 365.')).toBeInTheDocument()
    const save = screen.getByRole('button', { name: /save changes/i })
    expect(save).toHaveAttribute('aria-disabled', 'true')
    expect(api.updateRetentionSettings).not.toHaveBeenCalled()
  })

  it('a hand-edited stored 500 shows as stored with the range error and blocks Save until fixed (D-1 row 10, FR-046)', async () => {
    vi.mocked(api.fetchRetentionSettings).mockResolvedValueOnce({
      session_days: 500,
      memory_retros_days: 180,
      memory_retros_forever: false,
      keep_everything_forever: false,
    })
    renderSection(<DataSection />)
    const chat = await screen.findByLabelText('Keep chat history for')
    expect(chat).toHaveValue(500) // shown as stored — the read schema has no range
    expect(await screen.findByText('Enter a whole number from 1 to 365.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /save changes/i })).toHaveAttribute('aria-disabled', 'true')
    await userEvent.clear(chat)
    await userEvent.type(chat, '365')
    await userEvent.click(screen.getByRole('button', { name: /save changes/i }))
    expect(retentionOf()).toEqual(expect.objectContaining({ session_days: 365 }))
  })

  it('with Keep forever on the day field is disabled and the body omits memory_retros_days (FR-040)', async () => {
    renderSection(<DataSection />)
    const memory = await screen.findByLabelText('Keep memory for')
    await userEvent.clear(memory)
    await userEvent.type(memory, '999') // invalid on purpose: never validated while forever is on
    await userEvent.click(screen.getByRole('switch', { name: /keep forever/i }))
    expect(memory).toBeDisabled()
    await userEvent.click(screen.getByRole('button', { name: /save changes/i }))
    expect(api.updateRetentionSettings).toHaveBeenCalledTimes(1)
    expect(retentionOf()).toEqual({ memory_retros_forever: true })
  })

  it('the switch description says older reflections are deleted at the next nightly clean-up when turned off (FR-049)', async () => {
    renderSection(<DataSection />)
    expect(await screen.findByText(/nightly/i)).toBeInTheDocument()
    expect(screen.getByText(/older reflections are deleted/i)).toBeInTheDocument()
  })

  it('the legacy keep-everything switch shows the info notice and keeps the day fields editable (FR-048)', async () => {
    vi.mocked(api.fetchRetentionSettings).mockResolvedValueOnce({
      session_days: 90,
      memory_retros_days: 180,
      memory_retros_forever: false,
      keep_everything_forever: true,
    })
    renderSection(<DataSection />)
    expect(
      await screen.findByText(/kept forever because of a setting in the configuration file/i),
    ).toBeInTheDocument()
    expect(screen.getByText(/day counts below apply once it is removed|apply once it is removed/i)).toBeInTheDocument()
    expect(screen.getByLabelText('Keep chat history for')).toBeEnabled()
    expect(screen.getByLabelText('Keep memory for')).toBeEnabled()
  })
})

describe('FR-052 — Memory & recaps holds only a link to retention', () => {
  it('shows no day-count field and a "How long memory is kept" row', async () => {
    renderSection(<MemorySection />)
    expect(await screen.findByText('How long memory is kept')).toBeInTheDocument()
    expect(screen.queryByLabelText('Keep chat history for')).not.toBeInTheDocument()
    expect(screen.queryByLabelText('Keep memory for')).not.toBeInTheDocument()
    expect(screen.queryByLabelText(/keep forever/i)).not.toBeInTheDocument()
  })
})

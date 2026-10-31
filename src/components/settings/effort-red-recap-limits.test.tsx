// effort-red-recap-limits.test.tsx — RED tests for the Memory screen's
// "Recap limits" area (T-L4, S-82) and the recap-slot autosave with unsaved
// limit drafts (T-L7, S-93).
//
// Spec: /workspace/omnipus-coordination/designs-20261029/effort-levels-spec-r10.md
//
// RED reason: MemorySection renders no recap-limit inputs and no "Recap limits"
// area today — every label query fails until GREEN adds the accordion with ten
// fields, the two counters and the slot autosave. Mock/setup pattern follows
// MemorySection.test.tsx (the section's own suite).
//
// The ten fields, defaults, ranges and screen labels are the spec's L-table
// (FD-20): L1-L6, L8-L10, L12 — L7 removed by FD-23, L11 is the existing idle
// timeout, L13 deleted. FormError texts are the screen's own fixed range
// sentences ("{label} must be between {min} and {max}", S-82).

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { act } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemorySection } from './MemorySection'
import { fetchMemorySettings, updateMemorySettings, fetchProviders } from '@/lib/api'
import type { MemorySettings, Provider } from '@/lib/api'

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
if (typeof globalThis.ResizeObserver === 'undefined') {
  vi.stubGlobal('ResizeObserver', ResizeObserverStub)
}
if (typeof Element !== 'undefined' && !Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = function () {}
}

// ── Mocks ─────────────────────────────────────────────────────────────────────

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchMemorySettings: vi.fn(),
    updateMemorySettings: vi.fn(),
    fetchProviders: vi.fn(),
  }
})

vi.mock('@/store/ui', () => ({
  useUiStore: vi.fn(() => ({ addToast: vi.fn() })),
}))

// ── Fixtures ──────────────────────────────────────────────────────────────────

const DEFAULT_SETTINGS = {
  auto_recap_enabled: false,
  idle_timeout_minutes: 30,
  bootstrap_recap_enabled: false,
  bootstrap_recap_max_per_minute: 5,
  recap_model: '',
  recap_fallback_models: [] as Array<{ model: string; provider?: string }>,
  session_days: 90,
  memory_retros_days: 180,
}

// The ten recap-limit keys with their shipped defaults (spec L-table, FD-20).
const DEFAULT_LIMITS = {
  recap_max_output_tokens: 2048, // L1
  recap_input_tokens: 2000, // L2
  recap_summary_words: 150, // L3
  recap_list_items: 5, // L4
  recap_fallback_context_chars: 1500, // L5
  recap_timeout_seconds: 60, // L6
  recap_stream_retries: 2, // L8
  recap_retry_pause_ms: 500, // L9
  recap_chars_per_token: 4, // L10
  bootstrap_recap_min_spacing_seconds: 1, // L12
}

// [label prefix, key, min, max] — screen labels per the L-table; the unit
// suffix ("(tokens)" …) may follow the label, so anchors are prefix-based.
const LIMIT_FIELDS: Array<[RegExp, string, string, string]> = [
  [/^Answer length limit/, 'recap_max_output_tokens', '512', '16384'],
  [/^Conversation sent for the recap/, 'recap_input_tokens', '500', '32000'],
  [/^Summary length/, 'recap_summary_words', '50', '500'],
  [/^Items per list/, 'recap_list_items', '1', '10'],
  [/^Kept text if the recap fails/, 'recap_fallback_context_chars', '200', '8000'],
  [/^Recap time limit/, 'recap_timeout_seconds', '15', '300'],
  [/^Retries after a dropped connection/, 'recap_stream_retries', '0', '5'],
  [/^Pause before a retry/, 'recap_retry_pause_ms', '100', '5000'],
  [/^Characters per token/, 'recap_chars_per_token', '2', '8'],
  [/^Minimum gap between catch-up recaps/, 'bootstrap_recap_min_spacing_seconds', '1', '60'],
]

const MOCK_PROVIDERS: Provider[] = [
  {
    id: 'openrouter',
    name: 'openrouter',
    display_name: 'OpenRouter',
    status: 'connected',
    models: [
      'google/gemini-2.5-flash',
      'anthropic/claude-3.5-haiku',
      'openai/gpt-6.1-sol',
      'deepseek/deepseek-v4.1-flash',
    ],
  } as Provider,
]

function settings(overrides: Partial<typeof DEFAULT_LIMITS & typeof DEFAULT_SETTINGS> = {}): MemorySettings {
  return { ...DEFAULT_SETTINGS, ...DEFAULT_LIMITS, ...overrides } as unknown as MemorySettings
}

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function renderSection() {
  return render(
    <QueryClientProvider client={makeClient()}>
      <MemorySection />
    </QueryClientProvider>,
  )
}

const user = userEvent.setup()

/** Loads the section, then opens the collapsed "Recap limits" area. */
async function openRecapLimits() {
  renderSection()
  await screen.findByText('Memory & Recap')
  await user.click(screen.getByRole('button', { name: /recap limits/i }))
}

const saveButton = () => screen.getByRole('button', { name: /save memory settings/i })

// ── T-L4: the "Recap limits" area (S-82) ──────────────────────────────────────

describe('MemorySection — T-L4 "Recap limits" area', () => {
  beforeEach(() => {
    vi.mocked(fetchMemorySettings).mockResolvedValue(settings())
    vi.mocked(updateMemorySettings).mockResolvedValue(settings())
    vi.mocked(fetchProviders).mockResolvedValue(MOCK_PROVIDERS)
  })

  // Traces to: effort-levels-spec-r10.md S-82 (collapsed by default with the
  // summary "all at defaults"; opening shows all ten fields in the two groups).
  it('S-82: collapsed by default with "all at defaults"; opening shows ten fields in two groups', async () => {
    renderSection()
    await screen.findByText('Memory & Recap')
    expect(screen.getByText('all at defaults')).toBeInTheDocument()
    expect(screen.queryByLabelText(/^Summary length/)).toBeNull() // collapsed: fields hidden

    await user.click(screen.getByRole('button', { name: /recap limits/i }))
    expect(screen.getByText('What the recap reads and writes')).toBeInTheDocument()
    expect(screen.getByText('Timing and retries')).toBeInTheDocument()
    for (const [label] of LIMIT_FIELDS) {
      expect(screen.getByLabelText(label)).toBeInTheDocument()
    }
  })

  // Traces to: effort-levels-spec-r10.md S-82 / S-81 (the screen checks the
  // same ranges before sending: every input carries the spec min/max).
  it('S-82: every recap-limit input carries the spec range as min and max', async () => {
    await openRecapLimits()
    for (const [label, , min, max] of LIMIT_FIELDS) {
      const input = screen.getByLabelText(label)
      expect(input).toHaveAttribute('min', min)
      expect(input).toHaveAttribute('max', max)
    }
  })

  // Traces to: effort-levels-spec-r10.md S-82 (600 shows the screen's FormError
  // "Summary length must be between 50 and 500", blocks Save and takes focus).
  it('S-82: an out-of-range Summary length shows the exact FormError, blocks Save and holds focus', async () => {
    await openRecapLimits()
    const input = screen.getByLabelText(/^Summary length/)
    await user.clear(input)
    await user.type(input, '600')
    expect(screen.getByText('Summary length must be between 50 and 500')).toBeInTheDocument()
    expect(saveButton()).toBeDisabled()
    expect(input).toHaveFocus()
  })

  // Traces to: effort-levels-spec-r10.md S-82 (200 accepted with summary
  // "1 changed"; Reset puts all ten back without saving; Save stores defaults).
  it('S-82: 200 is accepted ("1 changed"); Reset restores the defaults without saving; Save stores them', async () => {
    await openRecapLimits()
    const input = screen.getByLabelText(/^Summary length/)
    await user.clear(input)
    await user.type(input, '600')
    expect(screen.getByText('Summary length must be between 50 and 500')).toBeInTheDocument()

    await user.clear(input)
    await user.type(input, '200')
    expect(screen.queryByText('Summary length must be between 50 and 500')).toBeNull()
    expect(screen.getByText('1 changed')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: /reset/i }))
    expect(input).toHaveValue(150)
    expect(screen.getByText('all at defaults')).toBeInTheDocument()
    expect(vi.mocked(updateMemorySettings)).not.toHaveBeenCalled()

    await act(async () => {
      fireEvent.click(saveButton())
    })
    await waitFor(() => expect(vi.mocked(updateMemorySettings)).toHaveBeenCalled())
    expect(vi.mocked(updateMemorySettings)).toHaveBeenCalledWith(
      expect.objectContaining(DEFAULT_LIMITS),
    )
  })

  // Traces to: effort-levels-spec-r10.md S-82 And (r8, R6-MIN-001): the
  // defaults summary stays a defaults comparison while SaveStatus shows the
  // save state — saved 200 reads "1 changed" clean; typing the default 150 over
  // it reads "all at defaults" dirty. The two counters never merge.
  it('S-82: a saved non-default reads "1 changed" clean; typing the default over it reads "all at defaults" dirty', async () => {
    vi.mocked(fetchMemorySettings).mockResolvedValue(settings({ recap_summary_words: 200 }))
    renderSection()
    await screen.findByText('Memory & Recap')
    expect(screen.getByText('1 changed')).toBeInTheDocument()
    expect(screen.queryByText(/^Saved$/)).toBeNull() // clean: nothing unsaved presented

    await user.click(screen.getByRole('button', { name: /recap limits/i }))
    const input = screen.getByLabelText(/^Summary length/)
    expect(input).toHaveValue(200)
    await user.clear(input)
    await user.type(input, '150')
    expect(screen.getByText('all at defaults')).toBeInTheDocument()
    expect(saveButton()).toBeEnabled() // dirty: the draft can be saved
  })
})

// ── T-L7: recap-slot autosave with unsaved limit drafts (S-93) ────────────────

describe('MemorySection — T-L7 recap-slot autosave with unsaved limit drafts', () => {
  beforeEach(() => {
    vi.mocked(fetchMemorySettings).mockResolvedValue(settings({ recap_model: 'openai/gpt-6.1-sol' }))
    vi.mocked(updateMemorySettings).mockResolvedValue(
      settings({ recap_model: 'openai/gpt-6.1-sol' }),
    )
    vi.mocked(fetchProviders).mockResolvedValue(MOCK_PROVIDERS)
  })

  /** The S-93 Given: drafts 80 (valid) and 100 (invalid) typed, nothing saved. */
  async function typeDrafts() {
    await openRecapLimits()
    const summary = screen.getByLabelText(/^Summary length/)
    await user.clear(summary)
    await user.type(summary, '80')
    const sent = screen.getByLabelText(/^Conversation sent for the recap/)
    await user.clear(sent)
    await user.type(sent, '100')
  }

  // Traces to: effort-levels-spec-r10.md S-93 Given (draft-on-edit: no request
  // while typing; r9, R7-MIN-001: the defaults summary counts both drafts —
  // "2 changed"; invalid drafts counted; Save blocked by the invalid 100).
  it('S-93: drafting limits sends no request, reads "2 changed" and keeps Save blocked by the invalid draft', async () => {
    await typeDrafts()
    expect(vi.mocked(updateMemorySettings)).not.toHaveBeenCalled()
    expect(screen.getByText('2 changed')).toBeInTheDocument()
    expect(
      screen.getByText('Conversation sent for the recap must be between 500 and 32000'),
    ).toBeInTheDocument()
    expect(saveButton()).toBeDisabled()
  })

  // Traces to: effort-levels-spec-r10.md S-93 main row: switching the recap
  // model autosaves EXACTLY ONE slot-only request (recap_model, recap_provider,
  // recap_reasoning_effort — merge semantics, no limit keys); the drafts, the
  // FormError, the "2 changed" summary, the dirty state and the blocked Save
  // survive it; the mapping note shows.
  it('S-93: switching the recap model autosaves a slot-only body and keeps the drafts, counters and blocked Save', async () => {
    await typeDrafts()
    expect(screen.getByText('2 changed')).toBeInTheDocument()

    fireEvent.click(screen.getByTestId('recap-model-trigger'))
    fireEvent.click(await screen.findByText('deepseek/deepseek-v4.1-flash'))

    await waitFor(() => expect(vi.mocked(updateMemorySettings)).toHaveBeenCalledTimes(1))
    const body = vi.mocked(updateMemorySettings).mock.calls[0][0] as Record<string, unknown>
    expect(body).toEqual({
      recap_model: 'deepseek/deepseek-v4.1-flash',
      recap_provider: 'openrouter',
      recap_reasoning_effort: 'low',
    })

    // Nothing was disturbed by the autosave.
    expect(screen.getByLabelText(/^Summary length/)).toHaveValue(80)
    expect(screen.getByLabelText(/^Conversation sent for the recap/)).toHaveValue(100)
    expect(
      screen.getByText('Conversation sent for the recap must be between 500 and 32000'),
    ).toBeInTheDocument()
    expect(screen.getByText('2 changed')).toBeInTheDocument()
    expect(saveButton()).toBeDisabled()
    expect(screen.getByText(/Effort set to Low\. deepseek\/deepseek-v4\.1-flash doesn't offer Medium\./)).toBeInTheDocument()
  })

  // Traces to: effort-levels-spec-r10.md S-93 And (r7, R5-MAJ-002; counters
  // named r8, R6-MIN-001): a deferred explicit Save never overwrites later
  // edits — held response, draft 250 survives the 200 baseline, dirty and
  // "1 changed" move independently, the second Save persists 250 and cleans;
  // and (r6 one-writer rule) slot pickers are disabled while Save is pending.
  it('S-93: a deferred Save keeps the later 250 edit; counters move independently; the second Save persists 250', async () => {
    await openRecapLimits()
    let resolveFirst: ((value: MemorySettings) => void) | undefined
    vi.mocked(updateMemorySettings).mockImplementation(
      () =>
        new Promise<MemorySettings>((resolve) => {
          resolveFirst = resolve
        }),
    )

    const input = screen.getByLabelText(/^Summary length/)
    await user.clear(input)
    await user.type(input, '200')
    await act(async () => {
      fireEvent.click(saveButton())
    })
    expect(vi.mocked(updateMemorySettings)).toHaveBeenCalledTimes(1)
    expect(screen.getByText('Saving…')).toBeInTheDocument()
    expect(screen.getByTestId('recap-model-trigger')).toBeDisabled() // one writer while Save pending

    await user.clear(input) // drafts stay editable while the request is in flight
    await user.type(input, '250')
    expect(input).toHaveValue(250)

    await act(async () => {
      resolveFirst?.(settings({ recap_summary_words: 200 }))
    })
    await waitFor(() => expect(screen.queryByText('Saving…')).toBeNull())
    // Baseline became 200; the draft stays 250 — nothing presented as saved,
    // the save state is dirty, the defaults summary reads "1 changed".
    expect(input).toHaveValue(250)
    expect(screen.getByText('1 changed')).toBeInTheDocument()
    expect(screen.queryByText(/^Saved$/)).toBeNull()
    expect(saveButton()).toBeEnabled()

    vi.mocked(updateMemorySettings).mockResolvedValue(settings({ recap_summary_words: 250 }))
    await act(async () => {
      fireEvent.click(saveButton())
    })
    await waitFor(() => expect(vi.mocked(updateMemorySettings)).toHaveBeenCalledTimes(2))
    expect(vi.mocked(updateMemorySettings)).toHaveBeenLastCalledWith(
      expect.objectContaining({ recap_summary_words: 250 }),
    )
    expect(input).toHaveValue(250)
    expect(screen.getByText('Saved')).toBeInTheDocument() // clean now
    expect(screen.getByText('1 changed')).toBeInTheDocument() // defaults summary unaffected
  })

  // Traces to: effort-levels-spec-r10.md S-93 And (R5-MAJ-002): a failed Save
  // keeps the submitted draft with the existing save-error message; nothing is
  // restored from the server.
  it('S-93: a failed Save keeps the draft and shows the save error', async () => {
    await openRecapLimits()
    vi.mocked(updateMemorySettings).mockRejectedValue(new Error('network down'))
    const input = screen.getByLabelText(/^Summary length/)
    await user.clear(input)
    await user.type(input, '80')
    await act(async () => {
      fireEvent.click(saveButton())
    })
    await waitFor(() => expect(screen.getByRole('alert')).toBeInTheDocument())
    expect(screen.getByText(/save failed/i)).toBeInTheDocument()
    expect(input).toHaveValue(80)
    expect(screen.getByText('1 changed')).toBeInTheDocument()
  })
})

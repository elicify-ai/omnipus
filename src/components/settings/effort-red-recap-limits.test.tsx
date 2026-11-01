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
import { render, screen, waitFor, fireEvent, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { act } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemorySection } from './MemorySection'
import { fetchMemorySettings, updateMemorySettings, fetchProviders, fetchProvidersCatalog } from '@/lib/api'
import type { MemorySettings, Provider, ProvidersCatalog } from '@/lib/api'

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
    fetchProviders: vi.fn(), fetchProvidersCatalog: vi.fn(),
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
  recap_provider: 'openrouter',
  recap_reasoning_effort: 'off',
  recap_fallback_models: [] as Array<{ model: string; provider?: string; reasoning_effort?: string }>,
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

const RECAP_CATALOG = {
 schema_version:'2.0.0',version:'v2026.10.31',updated_at:'2026-10-31T00:00:00Z',source:'test',served_from:'embedded',stale:false,
 default_resize_limits:{long_edge_px:2048,max_bytes:10485760},
 providers:[{id:'openrouter',name:'OpenRouter',company:'OpenRouter',api:'https://openrouter.ai/api/v1',protocol:'openai-compatible',protocols:[{protocol:'openai-compatible',api:'https://openrouter.ai/api/v1'}],tier:'popular',auth_methods:['api_key'],aliases:[],locality:'cloud',models:[
  ['openai/gpt-6.1-sol',['low','medium','high']],
  ['deepseek/deepseek-v4.1-flash',['none','low','high','max']],
  ['google/gemini-2.5-flash',['low','medium','high']],
  ['anthropic/claude-3.5-haiku',['low','medium','high']],
 ].map(([id,levels])=>({id,name:id,context_window:128000,max_output_tokens:8192,input_modalities:['text'],tool_call:true,status:'active',reasoning:{levels,switch:false}}))}]
} as unknown as ProvidersCatalog

beforeEach(() => {
  // S-93 call counts and queued one-off responses belong to this test only.
  // Reset before the nested setups establish each case's API behavior.
  vi.resetAllMocks()
  vi.mocked(fetchProvidersCatalog).mockResolvedValue(RECAP_CATALOG)
})

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

// ── C2-02 / S-60: the real Memory screen, not an explicit picker prop ────────

describe('MemorySection — S-60 recap presentation', () => {
  beforeEach(() => {
    vi.mocked(fetchProviders).mockResolvedValue(MOCK_PROVIDERS)
  })

  // S-60 fixes this entire sentence, including its straight apostrophe, and
  // requires exactly one copy even when more than one recap slot is present.
  it('S-60: the exact Off recommendation appears once for model plus fallback', async () => {
    const given = settings({
      recap_model: 'deepseek/deepseek-v4.1-flash',
      recap_fallback_models: [{ provider: 'openrouter', model: 'openai/gpt-6.1-sol' }],
    })
    vi.mocked(fetchMemorySettings).mockResolvedValue(given)
    renderSection()
    await screen.findByText('Memory & Recap')
    expect(screen.getByTestId('recap-model-trigger')).toHaveTextContent('deepseek/deepseek-v4.1-flash')
    expect(screen.getByTestId('recap-fallback-row-openai/gpt-6.1-sol')).toBeInTheDocument()
    expect(screen.queryAllByText(
      "We recommend Off for the recap: it keeps recaps cheap, and a recap doesn't need reasoning.",
      { exact: true },
    )).toHaveLength(1)
    expect(vi.mocked(updateMemorySettings)).not.toHaveBeenCalled()
  })

  it.each(['absent', 'explicit Off'] as const)(
    'S-60: %s effort starts Off per slot; model/chip, fallback/chip, recommendation, collapsed limits follow in order',
    async (storage) => {
      const given = settings({
        recap_model: 'deepseek/deepseek-v4.1-flash',
        recap_fallback_models: [{
          provider: 'openrouter', model: 'openai/gpt-6.1-sol',
          ...(storage === 'explicit Off' ? { reasoning_effort: 'off' } : {}),
        }],
      })
      if (storage === 'absent') Reflect.deleteProperty(given, 'recap_reasoning_effort')
      vi.mocked(fetchMemorySettings).mockResolvedValue(given)
      renderSection()
      await screen.findByText('Memory & Recap')
      const model = screen.getByTestId('recap-model-trigger')
      const fallback = screen.getByTestId('recap-fallback-row-openai/gpt-6.1-sol')
      const chips = screen.queryAllByRole('button', { name: /^Effort/ })
      expect(chips).toHaveLength(2)
      for (const chip of chips) expect(chip).toHaveAccessibleName(/^Effort.*: Off$/)
      const fallbackChip = within(fallback).getByRole('button', { name: /^Effort.*: Off$/ })
      expect(fallbackChip).toBe(chips[1])
      const recommendation = screen.getByText(
        "We recommend Off for the recap: it keeps recaps cheap, and a recap doesn't need reasoning.",
        { exact: true },
      )
      const limits = screen.getByRole('button', { name: /recap limits/i })
      const ordered = [model, chips[0], fallback, recommendation, limits]
      for (let i = 1; i < ordered.length; i++) {
        expect(ordered[i - 1].compareDocumentPosition(ordered[i]) & Node.DOCUMENT_POSITION_FOLLOWING,
          `S-60 group order: item ${i - 1} must precede item ${i}`,
        ).toBe(Node.DOCUMENT_POSITION_FOLLOWING)
      }
      expect(limits).toHaveAttribute('aria-expanded', 'false')
      for (const [label] of LIMIT_FIELDS) {
        expect(screen.queryByRole('spinbutton', { name: label })).toBeNull()
      }
      expect(vi.mocked(updateMemorySettings)).not.toHaveBeenCalled()
    },
  )
})

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
    vi.mocked(fetchMemorySettings).mockResolvedValue(settings({ recap_model: 'openai/gpt-6.1-sol',recap_provider:'openrouter',recap_reasoning_effort:'medium' }))
    vi.mocked(updateMemorySettings).mockImplementation(async body=>settings({recap_model:'openai/gpt-6.1-sol',recap_provider:'openrouter',recap_reasoning_effort:'medium',...body}))
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
  it('S-93: held fallback autosave serializes the next write as [A-prime, B-prime], never stale [A, B-prime]',async()=>{
    const original=[{provider:'openrouter',model:'openai/gpt-6.1-sol',reasoning_effort:'medium'},{provider:'openrouter',model:'google/gemini-2.5-flash',reasoning_effort:'high'}]
    const afterA=[{provider:'openrouter',model:'deepseek/deepseek-v4.1-flash',reasoning_effort:'low'},original[1]]
    const final=[afterA[0],{provider:'openrouter',model:'anthropic/claude-3.5-haiku',reasoning_effort:'high'}]
    vi.mocked(fetchMemorySettings).mockResolvedValue(settings({recap_fallback_models:original}))
    let releaseFirst!: (value:MemorySettings)=>void
    vi.mocked(updateMemorySettings).mockImplementationOnce(()=>new Promise(resolve=>{releaseFirst=resolve}))
      .mockResolvedValueOnce(settings({recap_fallback_models:final}))
    const view=renderSection()
    await screen.findByText('Memory & Recap')
    const rowA=screen.getByTestId('recap-fallback-row-openai/gpt-6.1-sol')
    await user.click(within(rowA).getByRole('combobox'))
    await user.click(await screen.findByText('deepseek/deepseek-v4.1-flash',{exact:true}))
    await waitFor(()=>expect(vi.mocked(updateMemorySettings)).toHaveBeenCalledTimes(1))
    expect(vi.mocked(updateMemorySettings).mock.calls[0][0]).toEqual({recap_fallback_models:afterA})
    const pickerB=within(screen.getByTestId('recap-fallback-row-google/gemini-2.5-flash')).getByRole('combobox')
    expect(pickerB).toBeDisabled()
    await user.click(pickerB)
    expect(vi.mocked(updateMemorySettings)).toHaveBeenCalledTimes(1)
    expect(saveButton()).toBeDisabled()
    await act(async()=>{releaseFirst(settings({recap_fallback_models:afterA}))})
    await waitFor(()=>expect(pickerB).toBeEnabled())
    await user.click(pickerB)
    await user.click(await screen.findByText('anthropic/claude-3.5-haiku',{exact:true}))
    await waitFor(()=>expect(vi.mocked(updateMemorySettings)).toHaveBeenCalledTimes(2))
    expect(vi.mocked(updateMemorySettings).mock.calls[1][0]).toEqual({recap_fallback_models:final})
    expect(vi.mocked(updateMemorySettings).mock.calls.map(([body])=>body)).not.toContainEqual({recap_fallback_models:[original[0],final[1]]})
    view.unmount()
    vi.mocked(fetchMemorySettings).mockResolvedValue(settings({recap_fallback_models:final}))
    renderSection()
    await screen.findByTestId('recap-fallback-row-deepseek/deepseek-v4.1-flash')
    expect(screen.getByTestId('recap-fallback-row-anthropic/claude-3.5-haiku')).toBeInTheDocument()
  })

})

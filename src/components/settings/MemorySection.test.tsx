import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { act } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemorySection } from './MemorySection'
import { fetchMemorySettings, updateMemorySettings, fetchProviders } from '@/lib/api'
import type { MemorySettings, Provider } from '@/lib/api'

// ResizeObserver is required by cmdk (used inside ModelSelector popovers);
// jsdom does not implement it. Polyfill with a noop so popover-open tests work.
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

// ── Helpers ───────────────────────────────────────────────────────────────────

// Settings redesign #1519, FR-045: MemorySettings drops `session_days` and
// `memory_retros_days` (retention moved to Data & retention's one write
// path), and `PUT /settings/memory` carrying either must answer 400 naming
// it — so the section's fixture and save payloads must never carry them.
const DEFAULT_SETTINGS = {
  auto_recap_enabled: false,
  idle_timeout_minutes: 30,
  bootstrap_recap_enabled: false,
  bootstrap_recap_max_per_minute: 5,
  recap_model: '',
  recap_fallback_models: [] as Array<{ model: string; provider?: string }>,
}

const MOCK_PROVIDERS: Provider[] = [
  {
    id: 'openrouter',
    name: 'openrouter',
    display_name: 'OpenRouter',
    status: 'connected',
    models: ['google/gemini-2.5-flash', 'anthropic/claude-3.5-haiku'],
  } as Provider,
]

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

// ── Tests ─────────────────────────────────────────────────────────────────────

// F1 reconciliation adds exact no-write/count checks. Calls from a previous
// test must not become evidence of an autosave in the next test.
beforeEach(() => { vi.clearAllMocks() })

describe('MemorySection', () => {
  beforeEach(() => {
    vi.mocked(fetchMemorySettings).mockResolvedValue({ ...DEFAULT_SETTINGS })
    vi.mocked(updateMemorySettings).mockResolvedValue({ ...DEFAULT_SETTINGS })
    vi.mocked(fetchProviders).mockResolvedValue(MOCK_PROVIDERS)
  })

  it('renders a loading skeleton while fetching', () => {
    vi.mocked(fetchMemorySettings).mockReturnValue(new Promise(() => {}))
    renderSection()
    // Skeleton is rendered via animate-pulse — check the section heading is absent
    expect(screen.queryByText('Memory & Recap')).toBeNull()
  })

  it('renders all settings fields once data loads', async () => {
    renderSection()

    // Section heading
    await screen.findByText('Memory & Recap')

    // Toggle fields (rendered as buttons with role=switch)
    expect(screen.getByRole('switch', { name: /auto recap/i })).toBeInTheDocument()
    expect(screen.getByRole('switch', { name: /bootstrap recap/i })).toBeInTheDocument()

    // Summarization model section
    expect(screen.getByText('Summarization model')).toBeInTheDocument()
    expect(screen.getByText(/recap runs a background summarization call/i)).toBeInTheDocument()

    // FR-052 (Settings redesign #1519): Memory & recaps shows NO retention
    // field — the day counts moved to Data & retention; only the "How long
    // memory is kept" row remains, with its link there.
    expect(screen.queryByLabelText('Session retention')).toBeNull()
    expect(screen.queryByLabelText('Memory retrospective retention')).toBeNull()
    expect(screen.queryByLabelText('Keep chat history for')).toBeNull()
    expect(screen.queryByLabelText('Keep memory for')).toBeNull()
    expect(screen.queryByRole('switch', { name: /keep.*forever/i })).toBeNull()
    expect(screen.getByText('How long memory is kept')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /open data & retention/i })).toHaveAttribute(
      'href',
      expect.stringContaining('tab=data'),
    )

    // Removed fields must NOT be present
    expect(screen.queryByText(/recap model allow-list/i)).toBeNull()
    expect(screen.queryByLabelText('Bootstrap recap daily budget')).toBeNull()
  })

  it('does NOT render the allow-list editor or USD budget field', async () => {
    renderSection()
    await screen.findByText('Memory & Recap')

    expect(screen.queryByText(/recap model allow-list/i)).toBeNull()
    expect(screen.queryByLabelText(/daily budget/i)).toBeNull()
    expect(screen.queryByText(/usd \/ day/i)).toBeNull()
  })

  it('shows idle-timeout field only when auto_recap_enabled is true', async () => {
    renderSection()
    await screen.findByText('Memory & Recap')

    // Initially hidden
    expect(screen.queryByLabelText('Idle timeout')).toBeNull()

    // Toggle auto recap on
    const autoRecapToggle = screen.getByRole('switch', { name: /auto recap/i })
    fireEvent.click(autoRecapToggle)

    expect(screen.getByLabelText('Idle timeout')).toBeInTheDocument()
  })

  it('shows bootstrap rate-limit field only when bootstrap_recap_enabled is true', async () => {
    renderSection()
    await screen.findByText('Memory & Recap')

    // Initially hidden
    expect(screen.queryByLabelText('Bootstrap recap rate limit')).toBeNull()

    // Toggle bootstrap recap on
    const bootstrapToggle = screen.getByRole('switch', { name: /bootstrap recap/i })
    fireEvent.click(bootstrapToggle)

    expect(screen.getByLabelText('Bootstrap recap rate limit')).toBeInTheDocument()

    // USD budget must never appear regardless of toggle state
    expect(screen.queryByLabelText('Bootstrap recap daily budget')).toBeNull()
  })

  it('calls updateMemorySettings with the correct payload when Save is clicked', async () => {
    vi.mocked(updateMemorySettings).mockResolvedValue({
      ...DEFAULT_SETTINGS,
      auto_recap_enabled: true,
    })

    renderSection()
    await screen.findByText('Memory & Recap')

    // Turn on auto recap
    const autoRecapToggle = screen.getByRole('switch', { name: /auto recap/i })
    fireEvent.click(autoRecapToggle)

    // Click save
    const saveBtn = screen.getByRole('button', { name: /save changes/i })
    await act(async () => { fireEvent.click(saveBtn) })

    await waitFor(() => {
      expect(vi.mocked(updateMemorySettings)).toHaveBeenCalledWith(
        expect.objectContaining({ auto_recap_enabled: true }),
      )
    })
  })

  it('F1 / FR-015: leaves unchanged recap slots out when another Memory value is saved', async () => {
    const saved: Record<string, unknown> = {}
    // Seed with a pre-configured recap_model and one fallback. A partial PUT
    // returns the merged document, including the unchanged saved slots.
    const initial = {
      ...DEFAULT_SETTINGS,
      recap_model: 'google/gemini-2.5-flash',
      recap_fallback_models: [{ model: 'anthropic/claude-3.5-haiku', provider: 'openrouter' }],
    }
    vi.mocked(updateMemorySettings).mockImplementation(async (body) => {
      Object.assign(saved, body)
      return { ...initial, ...body }
    })
    vi.mocked(fetchMemorySettings).mockResolvedValue(initial)

    renderSection()
    await screen.findByText('Memory & Recap')

    expect(screen.getByTestId('recap-model-trigger')).toHaveTextContent('google/gemini-2.5-flash')
    expect(screen.getByTestId('recap-fallback-row-anthropic/claude-3.5-haiku')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('switch', { name: /auto recap/i }))
    expect(vi.mocked(updateMemorySettings)).not.toHaveBeenCalled()
    const saveBtn = screen.getByRole('button', { name: /save changes/i })
    await act(async () => { fireEvent.click(saveBtn) })

    await waitFor(() => expect(vi.mocked(updateMemorySettings)).toHaveBeenCalledTimes(1))
    // F1 / FR-015: saved slots remain visible but are not echoed into a
    // different field's partial update. The picker tests below cover edits.
    expect(saved).toEqual({ auto_recap_enabled: true })
    expect(screen.getByTestId('recap-model-trigger')).toHaveTextContent('google/gemini-2.5-flash')
    expect(screen.getByTestId('recap-fallback-row-anthropic/claude-3.5-haiku')).toBeInTheDocument()

    // Removed fields must NOT appear in the payload
    expect(saved).not.toHaveProperty('bootstrap_recap_daily_budget_usd')
    expect(saved).not.toHaveProperty('recap_model_allow_list')
  })

  it('omits recap_model from payload when empty', async () => {
    const saved: Record<string, unknown> = {}
    vi.mocked(updateMemorySettings).mockImplementation(async (body) => {
      Object.assign(saved, body)
      return { ...DEFAULT_SETTINGS, ...body }
    })

    renderSection()
    await screen.findByText('Memory & Recap')

    // F1 / FR-011/015: a clean form has no Save; make a real scalar draft.
    fireEvent.click(screen.getByRole('switch', { name: /auto recap/i }))
    const saveBtn = screen.getByRole('button', { name: /save changes/i })
    await act(async () => { fireEvent.click(saveBtn) })

    await waitFor(() => expect(vi.mocked(updateMemorySettings)).toHaveBeenCalled())

    // recap_model should be omitted (undefined) when empty
    expect(saved['recap_model']).toBeUndefined()
  })

  it('F1 / FR-015: persists each changed core field in the partial save payload (round-trip)', async () => {
    const saved: Record<string, unknown> = {}
    vi.mocked(updateMemorySettings).mockImplementation(async (body) => {
      Object.assign(saved, body)
      return { ...DEFAULT_SETTINGS, ...body }
    })

    renderSection()
    await screen.findByText('Memory & Recap')

    fireEvent.click(screen.getByRole('switch', { name: /auto recap/i }))
    fireEvent.change(screen.getByLabelText('Idle timeout'), { target: { value: '45' } })
    fireEvent.click(screen.getByRole('switch', { name: /bootstrap recap/i }))
    fireEvent.change(screen.getByLabelText('Bootstrap recap rate limit'), { target: { value: '10' } })
    expect(vi.mocked(updateMemorySettings)).not.toHaveBeenCalled()
    const saveBtn = screen.getByRole('button', { name: /save changes/i })
    await act(async () => { fireEvent.click(saveBtn) })

    await waitFor(() => expect(vi.mocked(updateMemorySettings)).toHaveBeenCalledTimes(1))
    expect(saved).toEqual({
      auto_recap_enabled: true,
      idle_timeout_minutes: 45,
      bootstrap_recap_enabled: true,
      bootstrap_recap_max_per_minute: 10,
    })

    // FR-045: the retired retention fields must not ride along — PUT
    // /settings/memory carrying either must be refused with 400, so the
    // section never sends them (their home is Data & retention, FR-043).
    expect(saved).not.toHaveProperty('session_days')
    expect(saved).not.toHaveProperty('memory_retros_days')

    // Removed fields must NOT appear
    expect(saved).not.toHaveProperty('bootstrap_recap_daily_budget_usd')
    expect(saved).not.toHaveProperty('recap_model_allow_list')
  })

  it('renders a fallback-model row from fetched settings', async () => {
    vi.mocked(fetchMemorySettings).mockResolvedValue({
      ...DEFAULT_SETTINGS,
      recap_fallback_models: [{ model: 'anthropic/claude-3.5-haiku', provider: 'openrouter' }],
    })

    renderSection()
    await screen.findByText('Memory & Recap')

    // The fallback row should be visible
    expect(screen.getByTestId('recap-fallback-row-anthropic/claude-3.5-haiku')).toBeInTheDocument()
    expect(screen.getByTestId('recap-fallback-model-anthropic/claude-3.5-haiku')).toHaveTextContent('anthropic/claude-3.5-haiku')
  })

  it('removes a fallback model when X is clicked', async () => {
    vi.mocked(fetchMemorySettings).mockResolvedValue({
      ...DEFAULT_SETTINGS,
      recap_fallback_models: [{ model: 'anthropic/claude-3.5-haiku', provider: 'openrouter' }],
    })

    renderSection()
    await screen.findByText('Memory & Recap')

    const removeBtn = screen.getByTestId('recap-fallback-remove-anthropic/claude-3.5-haiku')
    fireEvent.click(removeBtn)

    await waitFor(() => {
      expect(screen.queryByTestId('recap-fallback-row-anthropic/claude-3.5-haiku')).toBeNull()
    })
  })

  it('selecting a primary model via ModelSelector sets recap_model and Save includes it in the PUT body', async () => {
    const saved: Record<string, unknown> = {}
    vi.mocked(updateMemorySettings).mockImplementation(async (body) => {
      Object.assign(saved, body)
      return body
    })

    renderSection()
    await screen.findByText('Memory & Recap')

    // Open the primary model selector popover
    const trigger = screen.getByTestId('recap-model-trigger')
    fireEvent.click(trigger)

    // The popover renders CommandItems as [role=option] or text nodes.
    // Find the model by its visible text content inside the open Command list.
    const modelItem = await screen.findByText('google/gemini-2.5-flash')
    fireEvent.click(modelItem)

    // The popover should close and the trigger should now display the selection.
    // Save and confirm the PUT body carries recap_model.
    const saveBtn = screen.getByRole('button', { name: /save changes/i })
    await act(async () => { fireEvent.click(saveBtn) })

    await waitFor(() => expect(vi.mocked(updateMemorySettings)).toHaveBeenCalled())

    expect(saved['recap_model']).toBe('google/gemini-2.5-flash')
  })

  it('adding a fallback via the add-selector appends a new row, and Save includes it in recap_fallback_models', async () => {
    const saved: Record<string, unknown> = {}
    vi.mocked(updateMemorySettings).mockImplementation(async (body) => {
      Object.assign(saved, body)
      return body
    })

    renderSection()
    await screen.findByText('Memory & Recap')

    // Open the "add fallback" ModelSelector popover
    const addTrigger = screen.getByTestId('recap-fallback-add-trigger')
    fireEvent.click(addTrigger)

    // Select a model from the open popover
    const modelItem = await screen.findByText('anthropic/claude-3.5-haiku')
    fireEvent.click(modelItem)

    // A new fallback row must appear
    await waitFor(() => {
      expect(screen.getByTestId('recap-fallback-row-anthropic/claude-3.5-haiku')).toBeInTheDocument()
    })

    // Save and confirm the fallback appears in the PUT body
    const saveBtn = screen.getByRole('button', { name: /save changes/i })
    await act(async () => { fireEvent.click(saveBtn) })

    await waitFor(() => expect(vi.mocked(updateMemorySettings)).toHaveBeenCalled())

    const fallbacks = saved['recap_fallback_models'] as Array<{ model: string; provider?: string }>
    expect(Array.isArray(fallbacks)).toBe(true)
    expect(fallbacks).toHaveLength(1)
    expect(fallbacks[0]).toMatchObject({ model: 'anthropic/claude-3.5-haiku' })
  })

  it('shows an error message when fetchMemorySettings fails', async () => {
    vi.mocked(fetchMemorySettings).mockRejectedValue(new Error('Network error'))

    renderSection()

    // F12 / FR-066: the shared error Callout names the failed read.
    await screen.findByText(/memory settings could not be loaded/i)
  })

  it('populates fields from the fetched settings', async () => {
    vi.mocked(fetchMemorySettings).mockResolvedValue({
      auto_recap_enabled: true,
      idle_timeout_minutes: 45,
      bootstrap_recap_enabled: false,
      bootstrap_recap_max_per_minute: 10,
      recap_model: '',
      recap_fallback_models: [],
    })

    renderSection()
    await screen.findByText('Memory & Recap')

    // auto_recap_enabled=true means the toggle is checked
    const autoRecapToggle = screen.getByRole('switch', { name: /auto recap/i })
    expect(autoRecapToggle).toHaveAttribute('aria-checked', 'true')

    // idle_timeout_minutes visible
    const idleInput = screen.getByLabelText('Idle timeout')
    expect(idleInput).toHaveValue(45)

    // FR-045/FR-052: no retention fields hydrate here — their values live in
    // Data & retention now.
    expect(screen.queryByLabelText('Session retention')).toBeNull()
    expect(screen.queryByLabelText('Memory retrospective retention')).toBeNull()
  })
})

// ── H6 R2 (review round 1) — duplicate-provider fixtures ──────────────────────
//
// The same slug served by TWO usable providers (the founder-instance shape:
// a local proxy row plus a subscription row). Fixture order puts grok-local
// FIRST, so first-wins resolution would name grok-local for any pick made
// under openai-chatgpt.

const DUAL_PROVIDERS: Provider[] = [
  {
    id: 'grok-local',
    name: 'grok-local',
    display_name: 'grok-local',
    status: 'connected',
    models: ['gpt-6.1-sol', 'grok-4'],
  } as Provider,
  {
    id: 'openai-chatgpt',
    name: 'openai-chatgpt',
    display_name: 'ChatGPT (subscription)',
    status: 'signed_in',
    models: ['gpt-6.1-sol', 'gpt-5.6'],
  } as Provider,
]

/** The two grouped rows for one slug, in fixture (render) order. */
const optionRowsFor = (model: string) =>
  screen.getAllByRole('option').filter((el) => (el.textContent ?? '').includes(model))

describe('MemorySection — H6 R2 duplicate-provider picks', () => {
  beforeEach(() => {
    vi.mocked(fetchMemorySettings).mockResolvedValue({ ...DEFAULT_SETTINGS })
    vi.mocked(updateMemorySettings).mockResolvedValue({ ...DEFAULT_SETTINGS })
  })

  it('picking the recap PRIMARY model under the SECOND provider keeps THAT provider\'s row chosen on reopen', async () => {
    vi.mocked(fetchProviders).mockResolvedValue(DUAL_PROVIDERS)
    vi.mocked(updateMemorySettings).mockClear()
    let acknowledge!: (settings: MemorySettings) => void
    vi.mocked(updateMemorySettings).mockImplementation(() => new Promise<MemorySettings>((resolve) => {
      acknowledge = resolve
    }))
    renderSection()
    await screen.findByText('Memory & Recap')

    // Pick gpt-6.1-sol under openai-chatgpt (the second row).
    fireEvent.click(screen.getByTestId('recap-model-trigger'))
    const rows = await vi.waitFor(() => optionRowsFor('gpt-6.1-sol'))
    expect(rows).toHaveLength(2)
    fireEvent.click(rows[1])
    // F1 / FR-010: the clicked provider stays a draft until the shared Save.
    expect(vi.mocked(updateMemorySettings)).not.toHaveBeenCalled()
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: /save changes/i })) })
    await waitFor(() => expect(vi.mocked(updateMemorySettings)).toHaveBeenCalledTimes(1))
    const body = vi.mocked(updateMemorySettings).mock.calls[0][0]
    expect(body).toMatchObject({ recap_model: 'gpt-6.1-sol', recap_provider: 'openai-chatgpt' })

    // F1/F4 / FR-011/020: a later pick is a draft, not a second autosave.
    // The old autosave-only picker lock is deliberately not the new oracle.
    const primaryPicker = screen.getByTestId('recap-model-trigger')
    fireEvent.click(primaryPicker)
    fireEvent.click(await screen.findByRole('option', { name: /grok-4/ }))
    expect(primaryPicker).toHaveTextContent('grok-4')
    expect(screen.getByText('1 unsaved change')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    expect(vi.mocked(updateMemorySettings)).toHaveBeenCalledTimes(1)
    await act(async () => { acknowledge({ ...DEFAULT_SETTINGS, ...body }) })
    expect(primaryPicker, 'F1/F4: the first Save answer must preserve the newer model draft').toHaveTextContent('grok-4')
    expect(screen.getByText('1 unsaved change')).toBeInTheDocument()
    expect(screen.queryByText(/Saved at/)).not.toBeInTheDocument()
    expect(vi.mocked(updateMemorySettings)).toHaveBeenCalledTimes(1)

    // Discard returns to the FIRST acknowledged pair, preserving the original
    // H6 provider-identity check rather than replacing it with draft identity.
    fireEvent.click(screen.getByRole('button', { name: 'Discard' }))
    expect(primaryPicker).toHaveTextContent('gpt-6.1-sol')
    fireEvent.click(primaryPicker)
    await vi.waitFor(() => optionRowsFor('gpt-6.1-sol'))
    const reopened = optionRowsFor('gpt-6.1-sol')
    expect(reopened).toHaveLength(2)
    expect(reopened[0]).not.toHaveAttribute('data-chosen')
    expect(reopened[1]).toHaveAttribute('data-chosen')
  })

  it('a recap fallback added via the SECOND provider\'s row is SAVED with that provider', async () => {
    vi.mocked(fetchProviders).mockResolvedValue(DUAL_PROVIDERS)
    const saved: Record<string, unknown> = {}
    vi.mocked(updateMemorySettings).mockImplementation(async (body) => {
      Object.assign(saved, body)
      return body
    })

    // One pre-existing fallback — must survive the add untouched.
    vi.mocked(fetchMemorySettings).mockResolvedValue({
      ...DEFAULT_SETTINGS,
      recap_fallback_models: [{ model: 'grok-4', provider: 'grok-local' }],
    })

    renderSection()
    await screen.findByText('Memory & Recap')

    fireEvent.click(screen.getByTestId('recap-fallback-add-trigger'))
    const rows = await vi.waitFor(() => optionRowsFor('gpt-6.1-sol'))
    expect(rows).toHaveLength(2)
    fireEvent.click(rows[1]) // openai-chatgpt's row — NOT the first listing

    await waitFor(() => {
      expect(screen.getByTestId('recap-fallback-row-gpt-6.1-sol')).toBeInTheDocument()
    })

    const saveBtn = screen.getByRole('button', { name: /save changes/i })
    await act(async () => { fireEvent.click(saveBtn) })
    await waitFor(() => expect(vi.mocked(updateMemorySettings)).toHaveBeenCalled())

    const fallbacks = saved['recap_fallback_models'] as Array<{ model: string; provider?: string }>
    expect(fallbacks).toHaveLength(2)
    // Pre-existing entry preserved verbatim...
    expect(fallbacks[0]).toEqual({ model: 'grok-4', provider: 'grok-local' })
    // ...and the new entry carries the CLICKED provider, not the first listing.
    expect(fallbacks[1]).toEqual({ model: 'gpt-6.1-sol', provider: 'openai-chatgpt' })
  })
})

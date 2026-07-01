import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { act } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemorySection } from './MemorySection'
import { fetchMemorySettings, updateMemorySettings, fetchProviders } from '@/lib/api'
import type { Provider } from '@/lib/api'

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

    // Session retention and memory retros are always visible
    expect(screen.getByLabelText('Session retention')).toBeInTheDocument()
    expect(screen.getByLabelText('Memory retrospective retention')).toBeInTheDocument()

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
    const saveBtn = screen.getByRole('button', { name: /save memory settings/i })
    await act(async () => { fireEvent.click(saveBtn) })

    await waitFor(() => {
      expect(vi.mocked(updateMemorySettings)).toHaveBeenCalledWith(
        expect.objectContaining({ auto_recap_enabled: true }),
      )
    })
  })

  it('sends recap_model and recap_fallback_models in the save payload when set', async () => {
    const saved: Record<string, unknown> = {}
    vi.mocked(updateMemorySettings).mockImplementation(async (body) => {
      Object.assign(saved, body)
      return body
    })

    // Seed with a pre-configured recap_model and one fallback
    vi.mocked(fetchMemorySettings).mockResolvedValue({
      ...DEFAULT_SETTINGS,
      recap_model: 'google/gemini-2.5-flash',
      recap_fallback_models: [{ model: 'anthropic/claude-3.5-haiku', provider: 'openrouter' }],
    })

    renderSection()
    await screen.findByText('Memory & Recap')

    const saveBtn = screen.getByRole('button', { name: /save memory settings/i })
    await act(async () => { fireEvent.click(saveBtn) })

    await waitFor(() => expect(vi.mocked(updateMemorySettings)).toHaveBeenCalled())

    expect(saved).toHaveProperty('recap_model', 'google/gemini-2.5-flash')
    expect(saved).toHaveProperty('recap_fallback_models')
    const fallbacks = saved['recap_fallback_models'] as Array<{ model: string; provider?: string }>
    expect(fallbacks).toHaveLength(1)
    expect(fallbacks[0]).toMatchObject({ model: 'anthropic/claude-3.5-haiku' })

    // Removed fields must NOT appear in the payload
    expect(saved).not.toHaveProperty('bootstrap_recap_daily_budget_usd')
    expect(saved).not.toHaveProperty('recap_model_allow_list')
  })

  it('omits recap_model from payload when empty', async () => {
    const saved: Record<string, unknown> = {}
    vi.mocked(updateMemorySettings).mockImplementation(async (body) => {
      Object.assign(saved, body)
      return body
    })

    renderSection()
    await screen.findByText('Memory & Recap')

    const saveBtn = screen.getByRole('button', { name: /save memory settings/i })
    await act(async () => { fireEvent.click(saveBtn) })

    await waitFor(() => expect(vi.mocked(updateMemorySettings)).toHaveBeenCalled())

    // recap_model should be omitted (undefined) when empty
    expect(saved['recap_model']).toBeUndefined()
  })

  it('persists the core fields in the save payload (round-trip)', async () => {
    const saved: Record<string, unknown> = {}
    vi.mocked(updateMemorySettings).mockImplementation(async (body) => {
      Object.assign(saved, body)
      return body
    })

    renderSection()
    await screen.findByText('Memory & Recap')

    const saveBtn = screen.getByRole('button', { name: /save memory settings/i })
    await act(async () => { fireEvent.click(saveBtn) })

    await waitFor(() => expect(vi.mocked(updateMemorySettings)).toHaveBeenCalled())

    // Core fields are always present
    expect(saved).toHaveProperty('auto_recap_enabled')
    expect(saved).toHaveProperty('idle_timeout_minutes')
    expect(saved).toHaveProperty('bootstrap_recap_enabled')
    expect(saved).toHaveProperty('bootstrap_recap_max_per_minute')
    expect(saved).toHaveProperty('session_days')
    expect(saved).toHaveProperty('memory_retros_days')

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

  it('shows an error message when fetchMemorySettings fails', async () => {
    vi.mocked(fetchMemorySettings).mockRejectedValue(new Error('Network error'))

    renderSection()

    await screen.findByText(/failed to load memory settings/i)
  })

  it('populates fields from the fetched settings', async () => {
    vi.mocked(fetchMemorySettings).mockResolvedValue({
      auto_recap_enabled: true,
      idle_timeout_minutes: 45,
      bootstrap_recap_enabled: false,
      bootstrap_recap_max_per_minute: 10,
      recap_model: '',
      recap_fallback_models: [],
      session_days: 60,
      memory_retros_days: 180,
    })

    renderSection()
    await screen.findByText('Memory & Recap')

    // auto_recap_enabled=true means the toggle is checked
    const autoRecapToggle = screen.getByRole('switch', { name: /auto recap/i })
    expect(autoRecapToggle).toHaveAttribute('aria-checked', 'true')

    // idle_timeout_minutes visible
    const idleInput = screen.getByLabelText('Idle timeout')
    expect(idleInput).toHaveValue(45)

    // session_days
    const sessionDaysInput = screen.getByLabelText('Session retention')
    expect(sessionDaysInput).toHaveValue(60)

    // memory_retros_days
    const retrosInput = screen.getByLabelText('Memory retrospective retention')
    expect(retrosInput).toHaveValue(180)
  })
})

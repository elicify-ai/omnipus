import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { act } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemorySection } from './MemorySection'
import { fetchMemorySettings, updateMemorySettings } from '@/lib/api'

// ── Mocks ─────────────────────────────────────────────────────────────────────

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchMemorySettings: vi.fn(),
    updateMemorySettings: vi.fn(),
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
  bootstrap_recap_daily_budget_usd: 0.5,
  recap_model_allow_list: [] as string[],
  session_days: 90,
  memory_retros_days: 365,
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

// ── Tests ─────────────────────────────────────────────────────────────────────

describe('MemorySection', () => {
  beforeEach(() => {
    vi.mocked(fetchMemorySettings).mockResolvedValue({ ...DEFAULT_SETTINGS })
    vi.mocked(updateMemorySettings).mockResolvedValue({ ...DEFAULT_SETTINGS })
  })

  it('renders a loading skeleton while fetching', () => {
    vi.mocked(fetchMemorySettings).mockReturnValue(new Promise(() => {}))
    renderSection()
    // Skeleton is rendered via animate-pulse — check the section heading is absent
    expect(screen.queryByText('Memory & Recap')).toBeNull()
  })

  it('renders all 8 settings fields once data loads', async () => {
    renderSection()

    // Section heading
    await screen.findByText('Memory & Recap')

    // Toggle fields (rendered as buttons with role=switch)
    expect(screen.getByRole('switch', { name: /auto recap/i })).toBeInTheDocument()
    expect(screen.getByRole('switch', { name: /bootstrap recap/i })).toBeInTheDocument()

    // Number fields are hidden when toggles are off; we check for visible ones
    // Session retention and memory retros are always visible
    expect(screen.getByLabelText('Session retention')).toBeInTheDocument()
    expect(screen.getByLabelText('Memory retrospective retention')).toBeInTheDocument()

    // Model allow-list section
    expect(screen.getByText(/recap model allow-list/i)).toBeInTheDocument()
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

  it('shows bootstrap-specific fields only when bootstrap_recap_enabled is true', async () => {
    renderSection()
    await screen.findByText('Memory & Recap')

    // Initially hidden
    expect(screen.queryByLabelText('Bootstrap recap rate limit')).toBeNull()
    expect(screen.queryByLabelText('Bootstrap recap daily budget')).toBeNull()

    // Toggle bootstrap recap on
    const bootstrapToggle = screen.getByRole('switch', { name: /bootstrap recap/i })
    fireEvent.click(bootstrapToggle)

    expect(screen.getByLabelText('Bootstrap recap rate limit')).toBeInTheDocument()
    expect(screen.getByLabelText('Bootstrap recap daily budget')).toBeInTheDocument()
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

  it('persists all 8 fields in the save payload (round-trip)', async () => {
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

    // Verify all 8 fields are present
    expect(saved).toHaveProperty('auto_recap_enabled')
    expect(saved).toHaveProperty('idle_timeout_minutes')
    expect(saved).toHaveProperty('bootstrap_recap_enabled')
    expect(saved).toHaveProperty('bootstrap_recap_max_per_minute')
    expect(saved).toHaveProperty('bootstrap_recap_daily_budget_usd')
    expect(saved).toHaveProperty('recap_model_allow_list')
    expect(saved).toHaveProperty('session_days')
    expect(saved).toHaveProperty('memory_retros_days')
  })

  it('adds and removes a model slug in the allow-list', async () => {
    renderSection()
    await screen.findByText('Memory & Recap')

    // Add a model slug
    const modelInput = screen.getByRole('textbox', { name: /new model slug/i })
    fireEvent.change(modelInput, { target: { value: 'google/gemini-2.5-flash' } })
    fireEvent.click(screen.getByRole('button', { name: /add model/i }))

    expect(screen.getByText('google/gemini-2.5-flash')).toBeInTheDocument()

    // Remove it
    fireEvent.click(screen.getByRole('button', { name: /remove model google\/gemini-2\.5-flash/i }))
    await waitFor(() => {
      expect(screen.queryByText('google/gemini-2.5-flash')).toBeNull()
    })
  })

  it('prevents adding a duplicate model slug', async () => {
    vi.mocked(fetchMemorySettings).mockResolvedValue({
      ...DEFAULT_SETTINGS,
      recap_model_allow_list: ['anthropic/claude-3.5-haiku'],
    })

    renderSection()
    await screen.findByText('Memory & Recap')

    const modelInput = screen.getByRole('textbox', { name: /new model slug/i })
    fireEvent.change(modelInput, { target: { value: 'anthropic/claude-3.5-haiku' } })
    fireEvent.click(screen.getByRole('button', { name: /add model/i }))

    expect(screen.getByText('Already in the list.')).toBeInTheDocument()
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
      bootstrap_recap_daily_budget_usd: 1.5,
      recap_model_allow_list: ['z-ai/glm-5.2'],
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

    // recap_model_allow_list
    expect(screen.getByText('z-ai/glm-5.2')).toBeInTheDocument()
  })
})

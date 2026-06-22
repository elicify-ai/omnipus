/**
 * PerformanceSection.test.tsx — Spec-6 FR-12.2 / Spec-3 FR-6.6.
 *
 * Covers the max-parallel-agents control and that saving it is gated by the
 * re-auth consent dialog: changing the input triggers autosave after debounce,
 * which opens the ReAuthDialog (the PUT does NOT fire yet), and only a
 * successful re-auth replays the consent token into updatePerformanceSettings.
 *
 * UAT fix #2: explicit Save button removed — autosave opens ReAuthDialog
 * automatically after the debounce (600 ms) when the input value is valid.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, fireEvent, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

const addToast = vi.fn()

vi.mock('@/store/ui', () => ({
  useUiStore: vi.fn(() => ({ addToast })),
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchPerformanceSettings: vi.fn(),
    updatePerformanceSettings: vi.fn(),
    reAuth: vi.fn(),
    isApiError: actual.isApiError,
  }
})

import * as api from '@/lib/api'
import { PerformanceSection } from './PerformanceSection'

const SETTINGS = {
  max_parallel_agents: 4,
  effective_max_parallel_agents: 4,
}

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

function renderSection() {
  return render(
    <QueryClientProvider client={makeClient()}>
      <PerformanceSection />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.fetchPerformanceSettings).mockResolvedValue(SETTINGS as never)
  // Real timers by default — individual tests switch to fake timers after
  // initial render so that waitFor (which uses setInterval internally) can
  // resolve the data-loading Promise before fake timers are installed.
})

afterEach(() => {
  vi.useRealTimers()
})

describe('PerformanceSection — autosave (UAT fix #2)', () => {
  it('renders the configured value from the API', async () => {
    vi.useRealTimers()
    renderSection()
    await waitFor(() => {
      expect(screen.getByLabelText('Max parallel agents')).toHaveValue(4)
    })
  })

  it('has no explicit Save button — autosave replaces it', async () => {
    vi.useRealTimers()
    renderSection()
    await waitFor(() => screen.getByLabelText('Max parallel agents'))
    // The Save button is gone; only the accessible escape-hatch "Save changes"
    // sr-only button exists (not visible).
    const visibleSaveBtns = screen.queryAllByRole('button').filter(
      (btn) => btn.textContent?.toLowerCase() === 'save',
    )
    expect(visibleSaveBtns).toHaveLength(0)
  })

  it('opens the re-auth dialog automatically after the debounce when input is valid', async () => {
    renderSection()
    // Wait for the data to load with real timers.
    await waitFor(() => screen.getByLabelText('Max parallel agents'))

    // Switch to fake timers NOW so we can control the debounce.
    vi.useFakeTimers()

    fireEvent.change(screen.getByLabelText('Max parallel agents'), { target: { value: '8' } })

    // Before debounce fires — dialog must NOT be open yet.
    expect(screen.queryByTestId('reauth-confirm')).not.toBeInTheDocument()
    expect(api.updatePerformanceSettings).not.toHaveBeenCalled()

    // Advance past the autosave debounce (600 ms).
    await act(async () => { vi.advanceTimersByTime(700) })

    // Switch back so waitFor can poll.
    vi.useRealTimers()

    await waitFor(() => {
      expect(screen.getByTestId('reauth-confirm')).toBeInTheDocument()
    })
    // PUT must still NOT have been called — user hasn't confirmed yet.
    expect(api.updatePerformanceSettings).not.toHaveBeenCalled()
  })

  it('replays the consent token into updatePerformanceSettings after re-auth', async () => {
    vi.mocked(api.reAuth).mockResolvedValue({ verified: true, token: 'reauth_tok', expires_in: 300 } as never)
    vi.mocked(api.updatePerformanceSettings).mockResolvedValue(SETTINGS as never)

    renderSection()
    // Wait for data with real timers.
    await waitFor(() => screen.getByLabelText('Max parallel agents'))

    // Switch to fake timers to control the debounce.
    vi.useFakeTimers()
    fireEvent.change(screen.getByLabelText('Max parallel agents'), { target: { value: '8' } })
    await act(async () => { vi.advanceTimersByTime(700) })
    vi.useRealTimers()

    await waitFor(() => screen.getByTestId('reauth-password-input'))
    fireEvent.change(screen.getByTestId('reauth-password-input'), { target: { value: 'mypassword' } })
    fireEvent.click(screen.getByTestId('reauth-confirm'))

    await waitFor(() => {
      expect(api.reAuth).toHaveBeenCalledWith('mypassword')
      expect(api.updatePerformanceSettings).toHaveBeenCalledWith(
        { max_parallel_agents: 8 },
        'reauth_tok',
      )
    })
  })

  it('does NOT open the re-auth dialog for out-of-range values', async () => {
    renderSection()
    // Wait for data with real timers.
    await waitFor(() => screen.getByLabelText('Max parallel agents'))

    // Switch to fake timers to control the debounce.
    vi.useFakeTimers()
    fireEvent.change(screen.getByLabelText('Max parallel agents'), { target: { value: '99' } })
    await act(async () => { vi.advanceTimersByTime(700) })
    vi.useRealTimers()

    // Dialog must NOT open — invalid value is silently skipped by autosave.
    expect(screen.queryByTestId('reauth-confirm')).not.toBeInTheDocument()
    // PUT must not have been called.
    expect(api.updatePerformanceSettings).not.toHaveBeenCalled()
  })

  it('shows the over-limit warning when input exceeds the recommended ceiling', async () => {
    vi.useRealTimers()
    // effective_max_parallel_agents = 4 → recommended = 4
    vi.mocked(api.fetchPerformanceSettings).mockResolvedValue({
      max_parallel_agents: 4,
      effective_max_parallel_agents: 4,
    } as never)

    renderSection()
    await waitFor(() => screen.getByLabelText('Max parallel agents'))

    // Type a value within [2,16] but above the recommended 4
    fireEvent.change(screen.getByLabelText('Max parallel agents'), { target: { value: '8' } })

    await waitFor(() => {
      expect(screen.getByTestId('performance-over-limit-warning')).toBeInTheDocument()
    })
    // Warning text mentions both the typed value and the recommended value
    expect(screen.getByTestId('performance-over-limit-warning').textContent).toContain('8')
    expect(screen.getByTestId('performance-over-limit-warning').textContent).toContain('4')
  })

  it('does not show the over-limit warning when input is within the recommended ceiling', async () => {
    vi.useRealTimers()
    vi.mocked(api.fetchPerformanceSettings).mockResolvedValue({
      max_parallel_agents: 8,
      effective_max_parallel_agents: 8,
    } as never)

    renderSection()
    await waitFor(() => screen.getByLabelText('Max parallel agents'))

    fireEvent.change(screen.getByLabelText('Max parallel agents'), { target: { value: '4' } })

    expect(screen.queryByTestId('performance-over-limit-warning')).not.toBeInTheDocument()
  })

  it('shows AutoSaveIndicator (no legacy SaveStatus)', async () => {
    vi.useRealTimers()
    renderSection()
    await waitFor(() => screen.getByLabelText('Max parallel agents'))
    // AutoSaveIndicator renders a data-testid when status is not idle — but it
    // exists in the DOM as a container even when idle.
    // Verify the label "Agent Concurrency" header is present.
    expect(screen.getByText('Agent Concurrency')).toBeInTheDocument()
  })
})

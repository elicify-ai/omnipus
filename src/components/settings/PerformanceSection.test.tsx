/**
 * PerformanceSection.test.tsx — Spec-6 FR-12.2 / Spec-3 FR-6.6.
 *
 * Covers the max-parallel-agents control and that saving it is gated by the
 * re-auth consent dialog: clicking Save opens the ReAuthDialog (the PUT does NOT
 * fire yet), and only a successful re-auth replays the consent token into
 * updatePerformanceSettings.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
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
})

describe('PerformanceSection', () => {
  it('renders the configured value from the API', async () => {
    renderSection()
    await waitFor(() => {
      expect(screen.getByLabelText('Max parallel agents')).toHaveValue(4)
    })
  })

  it('opens the re-auth dialog before saving (does NOT call PUT directly)', async () => {
    renderSection()
    await waitFor(() => screen.getByLabelText('Max parallel agents'))

    fireEvent.change(screen.getByLabelText('Max parallel agents'), { target: { value: '8' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() => {
      expect(screen.getByTestId('reauth-confirm')).toBeInTheDocument()
    })
    expect(api.updatePerformanceSettings).not.toHaveBeenCalled()
  })

  it('replays the consent token into updatePerformanceSettings after re-auth', async () => {
    vi.mocked(api.reAuth).mockResolvedValue({ verified: true, token: 'reauth_tok', expires_in: 300 } as never)
    vi.mocked(api.updatePerformanceSettings).mockResolvedValue(SETTINGS as never)

    renderSection()
    await waitFor(() => screen.getByLabelText('Max parallel agents'))

    fireEvent.change(screen.getByLabelText('Max parallel agents'), { target: { value: '8' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))

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

  it('rejects out-of-range values before opening the dialog', async () => {
    renderSection()
    await waitFor(() => screen.getByLabelText('Max parallel agents'))

    fireEvent.change(screen.getByLabelText('Max parallel agents'), { target: { value: '99' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))

    expect(screen.queryByTestId('reauth-confirm')).not.toBeInTheDocument()
    expect(addToast).toHaveBeenCalledWith(
      expect.objectContaining({ variant: 'error' }),
    )
    expect(api.updatePerformanceSettings).not.toHaveBeenCalled()
  })

  it('shows the over-limit warning when input exceeds the recommended ceiling', async () => {
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
    vi.mocked(api.fetchPerformanceSettings).mockResolvedValue({
      max_parallel_agents: 8,
      effective_max_parallel_agents: 8,
    } as never)

    renderSection()
    await waitFor(() => screen.getByLabelText('Max parallel agents'))

    fireEvent.change(screen.getByLabelText('Max parallel agents'), { target: { value: '4' } })

    expect(screen.queryByTestId('performance-over-limit-warning')).not.toBeInTheDocument()
  })
})

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchDevices: vi.fn(),
  }
})

vi.mock('@/store/ui', () => ({
  useUiStore: vi.fn(() => ({ addToast: vi.fn() })),
}))

vi.mock('@/store/chat', () => ({
  useChatStore: vi.fn((selector: (s: { respondToPairing: () => void }) => unknown) =>
    selector({ respondToPairing: vi.fn() })
  ),
}))

vi.mock('@/store/connection', () => ({
  useConnectionStore: vi.fn((selector: (s: { isConnected: boolean }) => unknown) =>
    selector({ isConnected: true })
  ),
}))

import { fetchDevices } from '@/lib/api'
import { DevicesSection } from './DevicesSection'

function makeClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
}

function renderSection() {
  return render(
    <QueryClientProvider client={makeClient()}>
      <DevicesSection />
    </QueryClientProvider>
  )
}

beforeEach(() => {
  vi.clearAllMocks()
})

// =====================================================================
// Scenario: fetch failure — must surface a real transport-error retry UI,
// not the stale "coming soon, future release" placeholder copy.
// =====================================================================

describe('DevicesSection — fetch error state', () => {
  it('shows a "failed to load" message (not "coming soon") when fetchDevices rejects', async () => {
    vi.mocked(fetchDevices).mockRejectedValue(new Error('network error'))

    renderSection()

    await waitFor(() => {
      expect(screen.getByText(/failed to load devices/i)).toBeInTheDocument()
    })

    expect(screen.queryByText(/coming soon/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/future release/i)).not.toBeInTheDocument()
  })

  it('shows a Retry button that re-invokes fetchDevices on click', async () => {
    vi.mocked(fetchDevices).mockRejectedValue(new Error('network error'))

    renderSection()

    const retryBtn = await screen.findByTestId('devices-retry-btn')
    expect(fetchDevices).toHaveBeenCalledTimes(1)

    vi.mocked(fetchDevices).mockResolvedValueOnce({ pending: [], paired: [] })
    fireEvent.click(retryBtn)

    await waitFor(() => {
      expect(fetchDevices).toHaveBeenCalledTimes(2)
    })

    await waitFor(() => {
      expect(screen.getByText(/no pending requests/i)).toBeInTheDocument()
    })
  })
})

// =====================================================================
// Scenario: successful load — sanity check the real data path still works
// =====================================================================

describe('DevicesSection — success state', () => {
  it('renders pending device requests from a real fetchDevices response', async () => {
    vi.mocked(fetchDevices).mockResolvedValue({
      pending: [
        {
          device_id: 'dev-1',
          device_name: 'Daniel’s iPhone',
          fingerprint: 'abcdef0123456789',
          pairing_code: '123456',
          created_at: '2026-07-01T00:00:00Z',
          expires_at: '2026-07-01T00:10:00Z',
        },
      ],
      paired: [],
    })

    renderSection()

    await waitFor(() => {
      expect(screen.getByText('Daniel’s iPhone')).toBeInTheDocument()
      expect(screen.getByText('123456')).toBeInTheDocument()
    })
  })
})

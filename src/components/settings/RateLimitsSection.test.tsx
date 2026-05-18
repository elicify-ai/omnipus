import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchRateLimits: vi.fn(),
    updateRateLimits: vi.fn(),
  }
})

vi.mock('@/store/ui', () => ({
  useUiStore: vi.fn(() => ({ addToast: vi.fn() })),
}))

vi.mock('@/store/auth', () => ({
  useAuthStore: vi.fn((selector: (s: { role?: string; user?: { username: string } }) => unknown) =>
    selector({ role: 'admin', user: { username: 'testadmin' } }),
  ),
}))

import { fetchRateLimits, updateRateLimits } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { useAuthStore } from '@/store/auth'
import { RateLimitsSection } from './RateLimitsSection'

function makeClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
}

function renderSection() {
  return render(
    <QueryClientProvider client={makeClient()}>
      <RateLimitsSection />
    </QueryClientProvider>
  )
}

const mockAddToast = vi.fn()

// Build a complete RateLimitsResponse mock. All required fields must be present.
function mockRateLimits(overrides: Partial<{
  enabled: boolean
  daily_cost_usd: number
  daily_cost_cap: number
  max_agent_llm_calls_per_hour: number
  max_agent_tool_calls_per_minute: number
}> = {}) {
  return {
    enabled: false,
    daily_cost_usd: 0,
    daily_cost_cap: 0,
    max_agent_llm_calls_per_hour: 0,
    max_agent_tool_calls_per_minute: 0,
    ...overrides,
  }
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(useUiStore).mockReturnValue({ addToast: mockAddToast } as never)
  vi.mocked(useAuthStore).mockImplementation(
    ((selector: (s: { role?: string; user?: { username: string } }) => unknown) =>
      selector({ role: 'admin', user: { username: 'testadmin' } })) as never,
  )
})

// =====================================================================
// Validation — negative values show error
// =====================================================================

describe('RateLimitsSection — negative validation', () => {
  it('typing -5 in cost cap shows error on blur', async () => {
    vi.mocked(fetchRateLimits).mockResolvedValue(mockRateLimits({ daily_cost_cap: 0 }))

    renderSection()

    const inputs = await screen.findAllByRole('spinbutton')
    const costInput = inputs[0]

    fireEvent.change(costInput, { target: { value: '-5' } })
    fireEvent.blur(costInput)

    await waitFor(() => {
      expect(screen.getByText(/non-negative/i)).toBeInTheDocument()
    })
  })

  it('typing -5 in llm calls per hour shows error on blur', async () => {
    vi.mocked(fetchRateLimits).mockResolvedValue(mockRateLimits({ max_agent_llm_calls_per_hour: 0 }))

    renderSection()

    const inputs = await screen.findAllByRole('spinbutton')
    const llmInput = inputs[1]

    fireEvent.change(llmInput, { target: { value: '-5' } })
    fireEvent.blur(llmInput)

    await waitFor(() => {
      expect(screen.getByText(/non-negative integer/i)).toBeInTheDocument()
    })
  })

  it('typing 10.5 in llm calls (integer field) shows error on blur', async () => {
    vi.mocked(fetchRateLimits).mockResolvedValue(mockRateLimits())

    renderSection()

    const inputs = await screen.findAllByRole('spinbutton')
    fireEvent.change(inputs[1], { target: { value: '10.5' } })
    fireEvent.blur(inputs[1])

    await waitFor(() => {
      expect(screen.getByText(/non-negative integer/i)).toBeInTheDocument()
    })
  })

  it('typing 10.5 in tool calls (integer field) shows error on blur', async () => {
    vi.mocked(fetchRateLimits).mockResolvedValue(mockRateLimits())

    renderSection()

    const inputs = await screen.findAllByRole('spinbutton')
    fireEvent.change(inputs[2], { target: { value: '10.5' } })
    fireEvent.blur(inputs[2])

    await waitFor(() => {
      expect(screen.getByText(/non-negative integer/i)).toBeInTheDocument()
    })
  })

  it('invalid value → updateRateLimits is NOT called on blur', async () => {
    vi.mocked(fetchRateLimits).mockResolvedValue(mockRateLimits({ daily_cost_cap: 0 }))

    renderSection()

    const inputs = await screen.findAllByRole('spinbutton')
    fireEvent.change(inputs[0], { target: { value: '-5' } })
    fireEvent.blur(inputs[0])

    await waitFor(() => {
      expect(screen.getByText(/non-negative/i)).toBeInTheDocument()
    })

    expect(updateRateLimits).not.toHaveBeenCalled()
  })
})

// =====================================================================
// Autosave fires on blur with changed value
// =====================================================================

describe('RateLimitsSection — autosave on blur', () => {
  it('changing cost-cap to 25.5 and blurring fires updateRateLimits', async () => {
    vi.mocked(fetchRateLimits).mockResolvedValue(mockRateLimits())
    vi.mocked(updateRateLimits).mockResolvedValue({ saved: true, requires_restart: false })

    renderSection()

    const inputs = await screen.findAllByRole('spinbutton')
    fireEvent.change(inputs[0], { target: { value: '25.5' } })
    fireEvent.blur(inputs[0])

    await waitFor(() => {
      expect(updateRateLimits).toHaveBeenCalledWith(
        expect.objectContaining({ daily_cost_cap_usd: 25.5 })
      )
    })
  })

  it('no Save button is rendered', async () => {
    vi.mocked(fetchRateLimits).mockResolvedValue(mockRateLimits())

    renderSection()

    await screen.findAllByRole('spinbutton')
    expect(screen.queryByRole('button', { name: /save/i })).not.toBeInTheDocument()
  })

  it('shows SaveStatus "Saving…" on blur while mutation is in flight', async () => {
    vi.mocked(fetchRateLimits).mockResolvedValue(mockRateLimits())
    vi.mocked(updateRateLimits).mockImplementation(
      () => new Promise((resolve) => setTimeout(() => resolve({ saved: true, requires_restart: false }), 50))
    )

    renderSection()

    const inputs = await screen.findAllByRole('spinbutton')
    fireEvent.change(inputs[0], { target: { value: '25.5' } })
    fireEvent.blur(inputs[0])

    await waitFor(() => {
      expect(screen.getByText(/saving/i)).toBeInTheDocument()
    })
  })

  it('no mutation if value did not change from server value (blur on unchanged field)', async () => {
    vi.mocked(fetchRateLimits).mockResolvedValue(mockRateLimits({
      daily_cost_cap: 10,
      max_agent_llm_calls_per_hour: 50,
      max_agent_tool_calls_per_minute: 5,
    }))

    renderSection()

    const inputs = await screen.findAllByRole('spinbutton')
    // Blur without changing value
    fireEvent.blur(inputs[0])

    // No mutation should fire since value hasn't changed
    expect(updateRateLimits).not.toHaveBeenCalled()
  })

  it('only changed field is included in the mutation body (partial update)', async () => {
    vi.mocked(fetchRateLimits).mockResolvedValue(mockRateLimits({
      daily_cost_cap: 10,
      max_agent_llm_calls_per_hour: 50,
      max_agent_tool_calls_per_minute: 5,
    }))
    vi.mocked(updateRateLimits).mockResolvedValue({ saved: true, requires_restart: false })

    renderSection()

    const inputs = await screen.findAllByRole('spinbutton')
    fireEvent.change(inputs[0], { target: { value: '20' } })
    fireEvent.blur(inputs[0])

    await waitFor(() => {
      expect(updateRateLimits).toHaveBeenCalledWith({ daily_cost_cap_usd: 20 })
    })
  })

  it('shows error toast when mutation fails', async () => {
    vi.mocked(fetchRateLimits).mockResolvedValue(mockRateLimits())
    vi.mocked(updateRateLimits).mockRejectedValue(new Error('server error'))

    renderSection()

    const inputs = await screen.findAllByRole('spinbutton')
    fireEvent.change(inputs[0], { target: { value: '5' } })
    fireEvent.blur(inputs[0])

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith(
        expect.objectContaining({ variant: 'error' })
      )
    })
  })
})

// =====================================================================
// Non-admin: no Save button
// =====================================================================

describe('RateLimitsSection — non-admin', () => {
  it('does not render a Save button for non-admin', async () => {
    vi.mocked(useAuthStore).mockImplementation(
      ((selector: (s: { role?: string; user?: { username: string } }) => unknown) =>
        selector({ role: 'user', user: { username: 'testuser' } })) as never,
    )
    vi.mocked(fetchRateLimits).mockResolvedValue(mockRateLimits({ daily_cost_cap: 0 }))

    renderSection()

    await screen.findAllByRole('spinbutton')
    expect(screen.queryByRole('button', { name: /save/i })).not.toBeInTheDocument()
  })
})

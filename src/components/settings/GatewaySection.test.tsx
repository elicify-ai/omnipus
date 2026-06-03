/**
 * GatewaySection tests — US-B2 risky controls: auth_mode + bind_address.
 *
 * Covers the highest-blast-radius controls (no login / all-interface exposure):
 * - Standing badge derives from the PERSISTED config value (not local draft).
 * - Badge shows immediately on load when value is already unsafe.
 * - Safe fallback when config is undefined: both controls default to safe value.
 * - auth_mode 'none' → badge; auth_mode 'token' → no badge.
 * - bind_address '0.0.0.0' → badge; bind_address '127.0.0.1' → no badge.
 * - Clicking a risky option opens the AlertDialog (safe cancel button default).
 * - selectedValue drives the active highlight independently of persistedValue.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

// ── Mocks ─────────────────────────────────────────────────────────────────────

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchConfig: vi.fn(),
    updateConfig: vi.fn(),
    fetchGatewayStatus: vi.fn(),
    fetchAgents: vi.fn(),
    rotateGatewayToken: vi.fn(),
  }
})

vi.mock('@/store/ui', () => ({
  useUiStore: vi.fn(() => ({ addToast: vi.fn() })),
}))

// ── Imports after mocks ────────────────────────────────────────────────────────

import { fetchConfig, fetchGatewayStatus, fetchAgents } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { GatewaySection } from './GatewaySection'

// ── Fixtures ──────────────────────────────────────────────────────────────────

function makeConfig(overrides: { auth_mode?: 'none' | 'token'; bind_address?: string } = {}) {
  return {
    gateway: {
      bind_address: overrides.bind_address ?? '127.0.0.1',
      port: 5000,
      auth_mode: overrides.auth_mode ?? 'token',
      token: 'test-token',
      hot_reload: false,
      log_level: 'info',
    },
    agents: { defaults: { default_agent_id: '' } },
    security: {
      policy_mode: 'deny',
      exec_approval: 'ask',
      daily_cost_cap: 10,
      exec_timeout_seconds: 0,
      max_background_seconds: 0,
      enable_deny_patterns: false,
      rate_limits: {
        max_agent_llm_calls_per_hour: null,
        max_agent_tool_calls_per_minute: null,
      },
    },
  }
}

// ── Helpers ───────────────────────────────────────────────────────────────────

function makeClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
}

function renderSection(client = makeClient()) {
  return render(
    <QueryClientProvider client={client}>
      <GatewaySection />
    </QueryClientProvider>
  )
}

const mockAddToast = vi.fn()

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(useUiStore).mockReturnValue({ addToast: mockAddToast } as never)
  vi.mocked(fetchGatewayStatus).mockResolvedValue({ daily_cost: 0, uptime_seconds: 0 } as never)
  vi.mocked(fetchAgents).mockResolvedValue([])
})

// ── auth_mode badge ───────────────────────────────────────────────────────────

describe('GatewaySection — auth_mode risky control (US-B2)', () => {
  it('shows standing badge when persisted auth_mode is "none"', async () => {
    vi.mocked(fetchConfig).mockResolvedValue(makeConfig({ auth_mode: 'none' }) as never)

    renderSection()

    await waitFor(() => {
      // Multiple risky controls render badges; at least one for auth_mode=none
      const badges = screen.getAllByTestId('risky-standing-badge')
      expect(badges.length).toBeGreaterThan(0)
    })
  })

  it('does NOT show standing badge when persisted auth_mode is "token" (safe)', async () => {
    vi.mocked(fetchConfig).mockResolvedValue(makeConfig({ auth_mode: 'token' }) as never)

    renderSection()

    // Wait for the section to render
    await waitFor(() => {
      expect(screen.getByText(/login requirement/i)).toBeInTheDocument()
    })

    // Confirm no auth-mode badge (bind_address is also safe)
    await waitFor(() => {
      expect(screen.queryByTestId('risky-standing-badge')).not.toBeInTheDocument()
    }, { timeout: 2000 })
  })

  it('safe fallback: when config is undefined, auth_mode defaults to safe "token" (no badge)', async () => {
    // Simulate a slow load — config never resolves during this test
    vi.mocked(fetchConfig).mockReturnValue(new Promise(() => {}))

    renderSection()

    // Section shows loading state — no badge
    expect(screen.queryByTestId('risky-standing-badge')).not.toBeInTheDocument()
  })

  it('"Bearer token" option has a Recommended pill (safe option marker)', async () => {
    vi.mocked(fetchConfig).mockResolvedValue(makeConfig({ auth_mode: 'token' }) as never)

    renderSection()

    await waitFor(() => {
      const pills = screen.getAllByTestId('recommended-pill')
      expect(pills.length).toBeGreaterThanOrEqual(1)
    })
  })

  it('clicking "None (no login)" option opens AlertDialog with safe cancel button', async () => {
    vi.mocked(fetchConfig).mockResolvedValue(makeConfig({ auth_mode: 'token' }) as never)

    renderSection()

    await waitFor(() => {
      expect(screen.getByTestId('risky-option-none')).toBeInTheDocument()
    })

    fireEvent.click(screen.getByTestId('risky-option-none'))

    await waitFor(() => {
      // The cancel/safe button must be the default action
      expect(screen.getByText(/keep login on/i)).toBeInTheDocument()
    })
  })

  it('cancelling the dialog leaves auth_mode unchanged (no badge)', async () => {
    vi.mocked(fetchConfig).mockResolvedValue(makeConfig({ auth_mode: 'token' }) as never)

    renderSection()

    await waitFor(() => {
      expect(screen.getByTestId('risky-option-none')).toBeInTheDocument()
    })

    fireEvent.click(screen.getByTestId('risky-option-none'))
    await waitFor(() => {
      expect(screen.getByText(/keep login on/i)).toBeInTheDocument()
    })

    fireEvent.click(screen.getByText(/keep login on/i))

    await waitFor(() => {
      expect(screen.queryByText(/keep login on/i)).not.toBeInTheDocument()
    })

    // Badge must NOT appear — persisted value is still 'token'
    expect(screen.queryByTestId('risky-standing-badge')).not.toBeInTheDocument()
  })

  it('badge appears immediately on load when auth_mode is already "none"', async () => {
    vi.mocked(fetchConfig).mockResolvedValue(makeConfig({ auth_mode: 'none' }) as never)

    renderSection()

    // Badge appears without any user interaction
    await waitFor(() => {
      expect(screen.getAllByTestId('risky-standing-badge').length).toBeGreaterThan(0)
    })
  })
})

// ── bind_address badge ────────────────────────────────────────────────────────

describe('GatewaySection — bind_address risky control (US-B2)', () => {
  it('shows standing badge when persisted bind_address is "0.0.0.0"', async () => {
    vi.mocked(fetchConfig).mockResolvedValue(makeConfig({ bind_address: '0.0.0.0' }) as never)

    renderSection()

    await waitFor(() => {
      const badges = screen.getAllByTestId('risky-standing-badge')
      expect(badges.length).toBeGreaterThan(0)
    })
  })

  it('does NOT show standing badge when bind_address is "127.0.0.1" (safe)', async () => {
    vi.mocked(fetchConfig).mockResolvedValue(makeConfig({ bind_address: '127.0.0.1' }) as never)

    renderSection()

    await waitFor(() => {
      expect(screen.getByText(/bind address/i)).toBeInTheDocument()
    })

    await waitFor(() => {
      expect(screen.queryByTestId('risky-standing-badge')).not.toBeInTheDocument()
    }, { timeout: 2000 })
  })

  it('"Localhost only" option has a Recommended pill', async () => {
    vi.mocked(fetchConfig).mockResolvedValue(makeConfig({ bind_address: '127.0.0.1' }) as never)

    renderSection()

    await waitFor(() => {
      expect(screen.getByTestId('risky-option-127.0.0.1')).toBeInTheDocument()
    })

    const pills = screen.getAllByTestId('recommended-pill')
    expect(pills.length).toBeGreaterThanOrEqual(1)
  })

  it('clicking "All interfaces" option opens AlertDialog', async () => {
    vi.mocked(fetchConfig).mockResolvedValue(makeConfig({ bind_address: '127.0.0.1' }) as never)

    renderSection()

    await waitFor(() => {
      expect(screen.getByTestId('risky-option-0.0.0.0')).toBeInTheDocument()
    })

    fireEvent.click(screen.getByTestId('risky-option-0.0.0.0'))

    await waitFor(() => {
      // Cancel / safe button in the dialog
      expect(screen.getByText(/keep localhost only/i)).toBeInTheDocument()
    })
  })

  it('both controls show badges when both settings are risky simultaneously', async () => {
    vi.mocked(fetchConfig).mockResolvedValue(
      makeConfig({ auth_mode: 'none', bind_address: '0.0.0.0' }) as never
    )

    renderSection()

    await waitFor(() => {
      const badges = screen.getAllByTestId('risky-standing-badge')
      expect(badges).toHaveLength(2)
    })
  })
})

// ── selectedValue drives active highlight (debounce fix) ──────────────────────

describe('GatewaySection — active highlight updates immediately (debounce fix)', () => {
  it('confirming dialog highlights the risky option immediately (before save completes)', async () => {
    vi.mocked(fetchConfig).mockResolvedValue(makeConfig({ auth_mode: 'token' }) as never)

    // updateConfig never resolves — simulates the debounce window
    const { updateConfig } = await import('@/lib/api')
    vi.mocked(updateConfig).mockReturnValue(new Promise(() => {}))

    renderSection()

    await waitFor(() => {
      expect(screen.getByTestId('risky-option-none')).toBeInTheDocument()
    })

    // Click risky option → dialog opens
    fireEvent.click(screen.getByTestId('risky-option-none'))
    await waitFor(() => {
      expect(screen.getByText(/remove login anyway/i)).toBeInTheDocument()
    })

    // Confirm the weakening
    fireEvent.click(screen.getByText(/remove login anyway/i))

    // The "none" button should be highlighted immediately (selectedValue = local draft)
    await waitFor(() => {
      const noneBtn = screen.getByTestId('risky-option-none')
      expect(noneBtn).toHaveAttribute('aria-pressed', 'true')
    })
  })
})

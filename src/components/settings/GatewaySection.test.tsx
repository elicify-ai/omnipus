/**
 * GatewaySection tests — FR-107 + FR-107b (US-4).
 *
 * Covers:
 * - No auth_mode:none option in the UI (FR-107 / US-4 AC1).
 * - No hot-reload toggle in the UI (US-4 AC2).
 * - Persistent unauth banner when auth_mode=none (FR-107b).
 * - Persistent unauth banner when dev_mode_bypass=true (FR-107b).
 * - No banner when auth_mode=token and dev_mode_bypass=false (clean state).
 * - US-B2: bind_address risky control (standing badge for 0.0.0.0).
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

function makeConfig(overrides: {
  auth_mode?: 'none' | 'token'
  bind_address?: string
  dev_mode_bypass?: boolean
} = {}) {
  return {
    gateway: {
      bind_address: overrides.bind_address ?? '127.0.0.1',
      port: 5000,
      auth_mode: overrides.auth_mode ?? 'token',
      token: 'test-token',
      hot_reload: true,
      log_level: 'info',
      dev_mode_bypass: overrides.dev_mode_bypass ?? false,
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

// ── FR-107: no auth_mode:none option ─────────────────────────────────────────

describe('GatewaySection — FR-107: auth_mode:none removed from UI', () => {
  it('does not render "None (no login)" option (auth_mode:none removed)', async () => {
    vi.mocked(fetchConfig).mockResolvedValue(makeConfig({ auth_mode: 'token' }) as never)
    renderSection()
    await waitFor(() => {
      expect(screen.getByText(/bind address/i)).toBeInTheDocument()
    })
    expect(screen.queryByText(/none.*no login/i)).not.toBeInTheDocument()
    expect(screen.queryByTestId('risky-option-none')).not.toBeInTheDocument()
  })

  it('does not show auth mode selector at all (token is always on)', async () => {
    vi.mocked(fetchConfig).mockResolvedValue(makeConfig({ auth_mode: 'token' }) as never)
    renderSection()
    await waitFor(() => {
      expect(screen.getByText(/log level/i)).toBeInTheDocument()
    })
    expect(screen.queryByText(/login requirement/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/bearer token.*login required/i)).not.toBeInTheDocument()
  })
})

// ── US-4 AC2: no hot-reload toggle ────────────────────────────────────────────

describe('GatewaySection — US-4 AC2: no hot-reload toggle', () => {
  it('does not render a hot reload switch', async () => {
    vi.mocked(fetchConfig).mockResolvedValue(makeConfig() as never)
    renderSection()
    await waitFor(() => {
      expect(screen.getByText(/log level/i)).toBeInTheDocument()
    })
    expect(screen.queryByText(/hot reload/i)).not.toBeInTheDocument()
    // Ensure no switch for hot-reload is in the DOM
    // The bind-address risky control is still there but the hot-reload Switch is gone
    expect(screen.queryByRole('switch')).not.toBeInTheDocument()
  })
})

// ── FR-107b: unauth banner ─────────────────────────────────────────────────────

describe('GatewaySection — FR-107b: unauthenticated access banner', () => {
  it('shows persistent banner when auth_mode=none', async () => {
    vi.mocked(fetchConfig).mockResolvedValue(makeConfig({ auth_mode: 'none' }) as never)
    renderSection()
    await waitFor(() => {
      expect(screen.getByTestId('unauth-banner')).toBeInTheDocument()
    })
    expect(screen.getByText(/unauthenticated access is enabled/i)).toBeInTheDocument()
  })

  it('shows persistent banner when dev_mode_bypass=true', async () => {
    vi.mocked(fetchConfig).mockResolvedValue(
      makeConfig({ auth_mode: 'token', dev_mode_bypass: true }) as never
    )
    renderSection()
    await waitFor(() => {
      expect(screen.getByTestId('unauth-banner')).toBeInTheDocument()
    })
    expect(screen.getByText(/unauthenticated access is enabled/i)).toBeInTheDocument()
  })

  it('shows banner when BOTH auth_mode=none and dev_mode_bypass=true', async () => {
    vi.mocked(fetchConfig).mockResolvedValue(
      makeConfig({ auth_mode: 'none', dev_mode_bypass: true }) as never
    )
    renderSection()
    await waitFor(() => {
      expect(screen.getByTestId('unauth-banner')).toBeInTheDocument()
    })
  })

  it('does NOT show banner when auth_mode=token and dev_mode_bypass=false', async () => {
    vi.mocked(fetchConfig).mockResolvedValue(
      makeConfig({ auth_mode: 'token', dev_mode_bypass: false }) as never
    )
    renderSection()
    await waitFor(() => {
      expect(screen.getByText(/log level/i)).toBeInTheDocument()
    })
    expect(screen.queryByTestId('unauth-banner')).not.toBeInTheDocument()
  })

  it('does NOT show banner when dev_mode_bypass is absent (undefined)', async () => {
    const cfg = makeConfig({ auth_mode: 'token' })
    // Remove dev_mode_bypass to simulate old gateway not sending it
    delete (cfg.gateway as Record<string, unknown>).dev_mode_bypass
    vi.mocked(fetchConfig).mockResolvedValue(cfg as never)
    renderSection()
    await waitFor(() => {
      expect(screen.getByText(/log level/i)).toBeInTheDocument()
    })
    expect(screen.queryByTestId('unauth-banner')).not.toBeInTheDocument()
  })
})

// ── US-B2: bind_address risky control ────────────────────────────────────────

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
      expect(screen.getByText(/keep localhost only/i)).toBeInTheDocument()
    })
  })
})

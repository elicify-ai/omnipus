/**
 * GatewaySection tests — FR-107 + FR-107b (US-4).
 *
 * Covers:
 * - No auth_mode:none option in the UI (FR-107 / US-4 AC1).
 * - No hot-reload toggle in the UI (US-4 AC2).
 * - No false banner when auth_mode=none but dev_mode_bypass=false (regression).
 * - Persistent unauth banner when dev_mode_bypass=true (FR-107b).
 * - Banner only when dev_mode_bypass=true (auth_mode is vestigial).
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
    rotateGatewayToken: vi.fn(),
    // O14: GatewaySection now embeds the GodModeControl, which reads god-mode
    // state. Mock it so the section renders deterministically (off + available).
    fetchGodMode: vi.fn(),
  }
})

vi.mock('@/store/ui', () => ({
  useUiStore: vi.fn(() => ({ addToast: vi.fn() })),
}))

// ── Imports after mocks ────────────────────────────────────────────────────────

import { fetchConfig, updateConfig, fetchGatewayStatus, fetchGodMode } from '@/lib/api'
import { ApiError } from '@/lib/api-error'
import { useUiStore } from '@/store/ui'
import { GatewaySection } from './GatewaySection'

// ── Fixtures ──────────────────────────────────────────────────────────────────

function makeConfig(overrides: {
  bind_address?: string
  dev_mode_bypass?: boolean
  preview_enabled?: boolean
} = {}) {
  return {
    gateway: {
      bind_address: overrides.bind_address ?? '127.0.0.1',
      port: 5000,
      token: 'test-token',
      hot_reload: true,
      log_level: 'info',
      dev_mode_bypass: overrides.dev_mode_bypass ?? false,
      preview_enabled: overrides.preview_enabled,
    },
    agents: { defaults: { default_agent_id: '' } },
    security: {
      daily_cost_cap: 10,
      exec_timeout_seconds: 0,
      max_background_seconds: 0,
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
  // `persisted` is required on GodModeStatus as of the D1/D19 fix: it carries the
  // raw sandbox.god_mode intent, independent of `available` (which is frozen at
  // boot). This fixture is state S0 — never armed — so it is false.
  vi.mocked(fetchGodMode).mockResolvedValue({
    enabled: false,
    available: true,
    supported: true,
    persisted: false,
  })
})

// ── FR-107: no auth_mode:none option ─────────────────────────────────────────

describe('GatewaySection — FR-107: auth_mode:none removed from UI', () => {
  it('does not render "None (no login)" option (auth_mode:none removed)', async () => {
    vi.mocked(fetchConfig).mockResolvedValue(makeConfig({}) as never)
    renderSection()
    await waitFor(() => {
      expect(screen.getByText(/bind address/i)).toBeInTheDocument()
    })
    expect(screen.queryByText(/none.*no login/i)).not.toBeInTheDocument()
    expect(screen.queryByTestId('risky-option-none')).not.toBeInTheDocument()
  })

  it('does not show auth mode selector at all (token is always on)', async () => {
    vi.mocked(fetchConfig).mockResolvedValue(makeConfig({}) as never)
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
    // Ensure no switch for hot-reload is in the DOM. (The God-mode switch — O14 —
    // is an expected, separate control, so assert specifically that no switch is
    // labelled for hot-reload rather than that no switch exists at all.)
    const switches = screen.queryAllByRole('switch')
    expect(
      switches.some((s) => /hot.?reload/i.test(s.getAttribute('aria-label') ?? '')),
    ).toBe(false)
  })
})

// ── FR-107b: unauth banner ─────────────────────────────────────────────────────

describe('GatewaySection — FR-107b: unauthenticated access banner', () => {
  // Regression: auth_mode is legacy/vestigial — token auth is enforced regardless
  // of it, so an unset auth_mode (serialized "none") must NOT trigger the banner.
  // Previously this produced a false "unauthenticated access" alarm on every normal
  // install (real backend default-serializes auth_mode:"none" while auth is on).
  it('does NOT show banner when auth_mode=none but dev_mode_bypass=false', async () => {
    vi.mocked(fetchConfig).mockResolvedValue(
      makeConfig({ dev_mode_bypass: false }) as never
    )
    renderSection()
    await waitFor(() => {
      expect(screen.getByText(/log level/i)).toBeInTheDocument()
    })
    expect(screen.queryByTestId('unauth-banner')).not.toBeInTheDocument()
  })

  it('shows persistent banner when dev_mode_bypass=true', async () => {
    vi.mocked(fetchConfig).mockResolvedValue(
      makeConfig({ dev_mode_bypass: true }) as never
    )
    renderSection()
    await waitFor(() => {
      expect(screen.getByTestId('unauth-banner')).toBeInTheDocument()
    })
    expect(screen.getByText(/unauthenticated access is enabled/i)).toBeInTheDocument()
  })

  it('shows banner on dev_mode_bypass=true even when auth_mode=none', async () => {
    vi.mocked(fetchConfig).mockResolvedValue(
      makeConfig({ dev_mode_bypass: true }) as never
    )
    renderSection()
    await waitFor(() => {
      expect(screen.getByTestId('unauth-banner')).toBeInTheDocument()
    })
  })

  it('does NOT show banner when auth_mode=token and dev_mode_bypass=false', async () => {
    vi.mocked(fetchConfig).mockResolvedValue(
      makeConfig({ dev_mode_bypass: false }) as never
    )
    renderSection()
    await waitFor(() => {
      expect(screen.getByText(/log level/i)).toBeInTheDocument()
    })
    expect(screen.queryByTestId('unauth-banner')).not.toBeInTheDocument()
  })

  it('does NOT show banner when dev_mode_bypass is absent (undefined)', async () => {
    const cfg = makeConfig({})
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

// ── US-4 / FR-008: Preview toggle (ADR-044, preview-on-main-listener) ────────
//
// Settings redesign #1519 reconciliation (FR-010): every Gateway value waits
// for Save changes — the old immediate autosave oracle ("toggling calls
// updateConfig") is replaced. Toggling stores nothing and counts in the Save
// bar; Save changes sends the write; the FR-018 saved signal replaces the
// success toast; a refused Save keeps the draft (FR-015/FR-020), it no
// longer reverts the toggle.

describe('GatewaySection — Preview toggle (US-4 / FR-008, one save rule FR-010)', () => {
  beforeEach(() => {
    vi.mocked(updateConfig).mockResolvedValue(makeConfig({}) as never)
  })

  it('renders the Preview toggle reflecting the persisted enabled value', async () => {
    vi.mocked(fetchConfig).mockResolvedValue(makeConfig({ preview_enabled: true }) as never)
    renderSection()

    await waitFor(() => {
      expect(screen.getByRole('switch', { name: /preview server/i })).toBeInTheDocument()
    })
    const toggle = screen.getByRole('switch', { name: /preview server/i })
    expect(toggle).toHaveAttribute('data-state', 'checked')
  })

  it('renders the toggle OFF when the persisted value is preview_enabled=false', async () => {
    vi.mocked(fetchConfig).mockResolvedValue(makeConfig({ preview_enabled: false }) as never)
    renderSection()

    await waitFor(() => {
      const toggle = screen.getByRole('switch', { name: /preview server/i })
      expect(toggle).toHaveAttribute('data-state', 'unchecked')
    })
  })

  it('defaults to ON (checked) when preview_enabled is absent from config', async () => {
    // Semantic default per ADR-044/FR-006: absent/null → enabled.
    const cfg = makeConfig({})
    delete (cfg.gateway as Record<string, unknown>).preview_enabled
    vi.mocked(fetchConfig).mockResolvedValue(cfg as never)
    renderSection()

    await waitFor(() => {
      const toggle = screen.getByRole('switch', { name: /preview server/i })
      expect(toggle).toHaveAttribute('data-state', 'checked')
    })
  })

  it('toggling off stores nothing until Save changes sends { gateway: { preview_enabled: false } } (FR-010)', async () => {
    vi.mocked(fetchConfig).mockResolvedValue(makeConfig({ preview_enabled: true }) as never)
    renderSection()

    await waitFor(() => {
      expect(screen.getByRole('switch', { name: /preview server/i })).toHaveAttribute('data-state', 'checked')
    })

    vi.mocked(updateConfig).mockResolvedValueOnce(makeConfig({ preview_enabled: false }) as never)
    fireEvent.click(screen.getByRole('switch', { name: /preview server/i }))

    // FR-010: the change is NOT stored yet — it waits in the Save bar.
    expect(updateConfig).not.toHaveBeenCalled()
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()

    fireEvent.click(await screen.findByRole('button', { name: /save changes/i }))
    await waitFor(() => {
      expect(updateConfig).toHaveBeenCalledWith({ gateway: { preview_enabled: false } })
    })
    // FR-018: the saved signal replaces the old success toast.
    await screen.findByText(/Saved at/)
  })

  it('toggling on stores nothing until Save changes sends { gateway: { preview_enabled: true } } (FR-010)', async () => {
    vi.mocked(fetchConfig).mockResolvedValue(makeConfig({ preview_enabled: false }) as never)
    renderSection()

    await waitFor(() => {
      expect(screen.getByRole('switch', { name: /preview server/i })).toHaveAttribute('data-state', 'unchecked')
    })

    vi.mocked(updateConfig).mockResolvedValueOnce(makeConfig({ preview_enabled: true }) as never)
    fireEvent.click(screen.getByRole('switch', { name: /preview server/i }))

    expect(updateConfig).not.toHaveBeenCalled()
    await screen.findByText(/1 unsaved change/)

    fireEvent.click(await screen.findByRole('button', { name: /save changes/i }))
    await waitFor(() => {
      expect(updateConfig).toHaveBeenCalledWith({ gateway: { preview_enabled: true } })
    })
  })

  it('a refused Save keeps the drafted value instead of reverting it (FR-015/FR-020)', async () => {
    vi.mocked(fetchConfig).mockResolvedValue(makeConfig({ preview_enabled: true }) as never)
    renderSection()

    await waitFor(() => {
      expect(screen.getByRole('switch', { name: /preview server/i })).toHaveAttribute('data-state', 'checked')
    })

    vi.mocked(updateConfig).mockRejectedValueOnce(new ApiError(400, 'nope'))
    fireEvent.click(screen.getByRole('switch', { name: /preview server/i }))
    fireEvent.click(await screen.findByRole('button', { name: /save changes/i }))

    await waitFor(() => {
      expect(updateConfig).toHaveBeenCalledTimes(1)
    })
    // FR-020 outcome (3), not saved: the draft is kept at its typed value —
    // the old oracle (revert to the persisted state) is replaced — and the
    // bar says the change could not be saved (FR-015).
    await waitFor(() => {
      expect(screen.getByRole('switch', { name: /preview server/i })).toHaveAttribute('data-state', 'unchecked')
    })
    expect(await screen.findByText(/could not be saved/)).toBeInTheDocument()
  })
})

describe('GatewaySection — bind_address risky control (US-B2)', () => {
  // Settings redesign #1519 reconciliation: the old risky-option cards with
  // their click-time AlertDialog and standing/recommended badges are replaced
  // — the wireframe renders Bind address as a select, and FR-016 moves the
  // "Listen on all interfaces?" confirm to Save time, before any write.
  it('shows the persisted all-interfaces address on the Bind address select (FR-059: the control sits in Gateway)', async () => {
    vi.mocked(fetchConfig).mockResolvedValue(makeConfig({ bind_address: '0.0.0.0' }) as never)
    renderSection()
    await waitFor(() => {
      expect(screen.getByLabelText(/bind address/i)).toHaveValue('0.0.0.0')
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

  it('the Bind address select offers localhost only and all interfaces (FR-016)', async () => {
    vi.mocked(fetchConfig).mockResolvedValue(makeConfig({ bind_address: '127.0.0.1' }) as never)
    renderSection()
    const select = await screen.findByLabelText(/bind address/i)
    const options = Array.from(select.querySelectorAll('option')).map((o) => o.textContent)
    // The choices are named in plain words (FR-016's own wording: "All
    // interfaces"), with the address visible beside the name.
    expect(options).toEqual(expect.arrayContaining([expect.stringMatching(/^Localhost only/), expect.stringMatching(/^All interfaces/)]))
  })

  it('choosing All interfaces asks before saving; Cancel stores nothing and keeps the change (FR-016)', async () => {
    vi.mocked(fetchConfig).mockResolvedValue(makeConfig({ bind_address: '127.0.0.1' }) as never)
    renderSection()
    const select = await screen.findByLabelText(/bind address/i)

    fireEvent.change(select, { target: { value: '0.0.0.0' } })
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()

    fireEvent.click(await screen.findByRole('button', { name: /save changes/i }))
    // FR-016: the confirm runs before any write — nothing is stored yet.
    expect(
      await screen.findByText('Listen on all interfaces?'),
      'FR-016: the "Listen on all interfaces?" confirm must open before any write',
    ).toBeInTheDocument()
    expect(updateConfig).not.toHaveBeenCalled()

    // Cancel keeps every change and stores nothing.
    fireEvent.click(screen.getByRole('button', { name: /keep localhost only/i }))
    await waitFor(() => {
      expect(screen.queryByText('Listen on all interfaces?')).not.toBeInTheDocument()
    })
    expect(updateConfig).not.toHaveBeenCalled()
    expect(screen.getByLabelText(/bind address/i)).toHaveValue('0.0.0.0')
    expect(screen.getByText(/1 unsaved change/)).toBeInTheDocument()
  })
})

// D3 (UAT v0.1.1 defects) — hydration must never trigger a spurious PUT.
//
// Root cause: `port` starts at the hardcoded useState default '8080'.
// Before this fix, useAutoSave's `disabled` option here was `!config` —
// but `config` turns truthy in the SAME commit the hydration effect is
// SCHEDULED, one render before the effect's own setState calls actually
// land. So `disabled` flipped false one render too early, useAutoSave
// captured the hardcoded '8080' default as its baseline, and the LATER
// commit where the real persisted port hydrates (5000, per `makeConfig`)
// looked like a genuine edit — firing a spurious `updateConfig` that
// echoes the fetched value straight back.
describe('GatewaySection — D3: hydration must not trigger a spurious PUT', () => {
  it('loading a port that differs from the hardcoded "8080" default never calls updateConfig, even after the debounce window elapses (REVERT-PROOF: fails without the gatewayHydrated gate)', async () => {
    vi.mocked(fetchConfig).mockResolvedValue(makeConfig({}) as never) // port: 5000
    renderSection()

    await waitFor(() => {
      expect(screen.getByDisplayValue('5000')).toBeInTheDocument()
    })

    // PASSIVE idle wait — no interaction at all — comfortably past the
    // 500ms default debounce.
    await new Promise((resolve) => setTimeout(resolve, 900))
    expect(updateConfig).not.toHaveBeenCalled()
  })
})

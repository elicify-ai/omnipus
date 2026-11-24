/**
 * protectionSave.red.test.tsx — Settings redesign #1519, RED
 * (FR-010, FR-011, FR-025, FR-026, FR-027, FR-051; G-16, D-8 rows 3-4).
 *
 * Oracles (spec, never code):
 *   - G-16 / FR-051 (BDD "The God Mode button acts alone"): with 1 unsaved
 *     Auto-approve change, pressing "Turn on God Mode…" and entering the
 *     right password turns God Mode on; the Auto-approve change is still
 *     unsaved and the Save bar still says "1 unsaved change". God Mode never
 *     enters or clears the Save batch.
 *   - FR-010/FR-026/FR-027 (D-8 row 3): a changed Auto-approve stores
 *     nothing until Save changes; the bar counts it as needing the password;
 *     one prompt lists exactly ["Auto-approve"]; then exactly ONE
 *     PUT /security/sandbox-config carries the consent token.
 *   - FR-010/FR-011 (Sandbox drafts): a changed Sandbox mode stores nothing,
 *     and the bar counts "1 unsaved change", "1 needs your password" and
 *     "1 needs a gateway restart".
 *
 * RED on landing: the old sections write on toggle (no Save bar) and God
 * Mode is still a switch, not the FR-051 button; the shell password prompt
 * (FR-026) does not exist, so those cases fail loudly as BLOCKED.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from '@tanstack/react-router'
import type { ReactElement, ReactNode } from 'react'

const LOCAL_APP_STATE = {
  onboarding_complete: true,
  identity: { mode: 'local', edition: 'core', signed_in: true },
}

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAppState: vi.fn(async () => LOCAL_APP_STATE),
    fetchGodMode: vi.fn(),
    setGodMode: vi.fn(),
    reAuth: vi.fn(),
    fetchSandboxConfig: vi.fn(),
    updateSandboxConfig: vi.fn(),
    fetchSandboxStatus: vi.fn(async () => ({ kernel_sandbox_active: true })),
    fetchRateLimits: vi.fn(async () => ({ max_agent_llm_calls_per_hour: 7, max_agent_tool_calls_per_minute: 9 })),
    fetchConfig: vi.fn(),
    updateConfig: vi.fn(),
    fetchGatewayStatus: vi.fn(async () => ({ daily_cost: 0, uptime_seconds: 0 })),
    fetchBuiltinTools: vi.fn(async () => []),
    fetchPromptGuardLevel: vi.fn(async () => ({ level: 'medium' as const, requires_restart: false })),
    updatePromptGuardLevel: vi.fn(async () => ({ requires_restart: false })),
    fetchCredentials: vi.fn(async () => []),
    fetchSkillTrust: vi.fn(async () => ({ level: 'warn_unverified' as const })),
    fetchDoctorResults: vi.fn(async () => null),
    fetchGlobalToolPolicies: vi.fn(async () => ({ policies: {} })),
  }
})

vi.mock('@/store/ui', () => ({ useUiStore: () => ({ addToast: vi.fn() }) }))
vi.mock('@/store/auth', () => ({
  useAuthStore: vi.fn((selector: (s: { role: string }) => unknown) => selector({ role: 'admin' })),
}))
vi.mock('@/hooks/restart', () => ({
  usePendingRestart: () => ({ refetch: vi.fn() }),
}))

import * as api from '@/lib/api'
import { SecuritySection } from './SecuritySection'
import { SandboxSection } from './SandboxSection'
import { GodModeControl } from './GodModeControl'

/** The shell's password prompt is the FR-026 one-per-Save prompt (shell-owned). */
async function loadShellContext(): Promise<{ SettingsShellContext: { Provider: (p: { value: unknown; children: ReactNode }) => ReactNode } }> {
  try {
    return (await vi.importActual('./SettingsShellContext')) as never
  } catch {
    throw new Error(
      'BLOCKED RED (FR-010/FR-026/FR-027, D-8): the Settings shell (SettingsShellContext) does not exist yet — ' +
        'required for the one password prompt per Save (FR-026) and the Save-bar counts (FR-010/FR-011)',
    )
  }
}

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

/**
 * The protection view renders a router Link (the "Open Skills & Tools"
 * callout), so the section mounts under a real router — the app's own routed
 * shell minus the AppShell chrome this test does not exercise.
 */
function renderProtection(ui: ReactElement) {
  const root = createRootRoute({ component: () => ui })
  const router = createRouter({ routeTree: root, history: createMemoryHistory({ initialEntries: ['/'] }) })
  render(
    <QueryClientProvider client={makeClient()}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
}

const GODMODE_OFF = { enabled: false, available: true, supported: true, persisted: false }

beforeEach(() => {
  vi.clearAllMocks()
  // A minimal but complete config: React Query rejects undefined query data,
  // and the section mounts its ["config"] query on every view.
  vi.mocked(api.fetchConfig).mockResolvedValue({
    gateway: { bind_address: '127.0.0.1', port: 5000, token: 'test-token', hot_reload: false, log_level: 'info' },
    agents: { defaults: { default_agent_id: '' } },
    security: {
      exec_timeout_seconds: 0,
      max_background_seconds: 0,
      rate_limits: { max_agent_llm_calls_per_hour: null, max_agent_tool_calls_per_minute: null },
    },
  } as never)
  vi.mocked(api.updateConfig).mockResolvedValue({} as never)
  // mode/applied_mode set the Sandbox mode radio group (the section renders
  // one radio per mode, named "Sandbox mode: <label>", FR-025's visible label).
  vi.mocked(api.fetchSandboxConfig).mockResolvedValue({
    auto_approve: false,
    mode: 'enforce',
    applied_mode: 'enforce',
  } as never)
  vi.mocked(api.updateSandboxConfig).mockResolvedValue({} as never)
  vi.mocked(api.fetchGodMode).mockResolvedValue(GODMODE_OFF as never)
  vi.mocked(api.setGodMode).mockResolvedValue({ enabled: true, restart_required: false } as never)
  vi.mocked(api.reAuth).mockResolvedValue({ verified: true, token: 'reauth_tok', expires_in: 300 } as never)
  // The SPA refuses a state-changing call without the CSRF cookie.
  document.cookie = 'csrf=protection-save-test'
})

describe('G-16 / FR-051 — the God Mode button acts alone', () => {
  it('with 1 unsaved Auto-approve change, turning God Mode on keeps the draft unsaved and the bar at 1', async () => {
    renderProtection(<SecuritySection view="protection" godMode={<GodModeControl />} />)
    // Draft one Auto-approve change (FR-010: nothing is stored).
    const auto = await screen.findByRole('switch', { name: /auto-approve/i }, { timeout: 5000 })
    fireEvent.click(auto)
    expect(api.updateSandboxConfig).not.toHaveBeenCalled()
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()

    // FR-051: God Mode is the "Turn on God Mode…" button with its own
    // password prompt — the old switch (aria-checked) is replaced.
    const god = await screen.findByRole('button', { name: 'Turn on God Mode…' }, { timeout: 5000 })
    await userEvent.setup().click(god)
    await waitFor(() => expect(screen.getByTestId('reauth-password-input')).toBeInTheDocument())
    fireEvent.change(screen.getByTestId('reauth-password-input'), { target: { value: 'right-password' } })
    fireEvent.click(screen.getByTestId('reauth-confirm'))

    await waitFor(() => {
      expect(api.reAuth).toHaveBeenCalledTimes(1)
      expect(api.setGodMode).toHaveBeenCalledWith(true, 'reauth_tok')
    })
    // God Mode never travels in a Save batch (FR-051): its write is its own
    // endpoint, and the Auto-approve draft is untouched.
    expect(api.updateSandboxConfig).not.toHaveBeenCalled()
    expect(screen.getByRole('switch', { name: /auto-approve/i })).toBeChecked()
    expect(
      screen.getByText(/1 unsaved change/),
      'G-16 RED: the bar must still say "1 unsaved change" (God Mode acts alone)',
    ).toBeInTheDocument()
  })
})

describe('FR-010/FR-026/FR-027 — Auto-approve waits for Save changes (D-8 row 3)', () => {
  it('stores nothing on toggle; one prompt lists exactly Auto-approve; one PUT /security/sandbox-config carries the token', async () => {
    const { SettingsShellContext } = await loadShellContext()
    const promptLabels: string[][] = []
    const requestPassword = vi.fn(async (labels: string[]) => {
      promptLabels.push(labels)
      return 'tok-aa'
    })
    renderProtection(
      <SettingsShellContext.Provider
        value={{
          announce: () => {},
          setDraftCount: () => {},
          offerRestart: () => {},
          refreshPendingRestart: () => {},
          requestPassword,
          saveBarVisible: false,
          setSaveBarVisible: () => {},
        }}
      >
        <SecuritySection view="protection" />
      </SettingsShellContext.Provider>,
    )

    const auto = await screen.findByRole('switch', { name: /auto-approve/i }, { timeout: 5000 })
    fireEvent.click(auto)
    // FR-010: a changed Auto-approve stores nothing until Save changes.
    expect(api.updateSandboxConfig).not.toHaveBeenCalled()
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    expect(
      screen.getByText(/1 needs your password/),
      'FR-011: Auto-approve is a Password-marked row (FR-025), so the bar counts it',
    ).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    // FR-026: exactly one prompt, listing by visible label every
    // password-marked setting in the Save and nothing else.
    await waitFor(() => expect(promptLabels.length).toBe(1))
    expect(promptLabels[0]).toEqual(['Auto-approve'])
    // FR-027 (D-8 row 3): exactly one protected write, carrying the token.
    await waitFor(() => {
      expect(api.updateSandboxConfig).toHaveBeenCalledTimes(1)
      expect(api.updateSandboxConfig).toHaveBeenCalledWith({ auto_approve: true }, 'tok-aa')
    })
  })
})

describe('FR-010/FR-011 — Sandbox drafts wait for Save changes', () => {
  it('a changed Sandbox mode stores nothing and the bar counts change, password and restart', async () => {
    render(
      <QueryClientProvider client={makeClient()}>
        <SandboxSection />
      </QueryClientProvider>,
    )
    // Sandbox mode renders as a radio per mode ("Sandbox mode: <label>",
    // FR-025's visible label). Drafting a change = choosing another mode.
    const mode = await screen.findByRole('radio', { name: 'Sandbox mode: Permissive' }, { timeout: 5000 })
    fireEvent.click(mode)
    // FR-010: the draft is not stored — no write of any kind.
    expect(api.updateSandboxConfig).not.toHaveBeenCalled()
    expect(api.updateConfig).not.toHaveBeenCalled()
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    // FR-025/FR-036/FR-037: Sandbox mode is password-marked and
    // restart-gated, so the bar counts all three (FR-011).
    expect(screen.getByText(/1 needs your password/)).toBeInTheDocument()
    expect(screen.getByText(/1 needs a gateway restart/)).toBeInTheDocument()
  })
})

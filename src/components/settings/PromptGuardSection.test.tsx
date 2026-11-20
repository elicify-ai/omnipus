/**
 * PromptGuardSection.test.tsx — Protection's Prompt injection defense under
 * the Settings redesign #1519: the draft-mode Save-bar oracle.
 *
 * Part 11: FR-010 (spec "One save rule") is UNQUALIFIED — a changed value
 * MUST NOT be stored until Save changes is pressed, and the BDD scenario
 * "Defense level applies at once" (US-4 AS6) has the person "select[s] High
 * and save[s]" — the level goes through Save. team-lead's ruling: every path
 * a user can reach waits for Save (the legacy instant-save mode is removed in
 * parallel by frontend-lead). This file therefore renders the section the way
 * the Protection screen does (SecuritySection view="protection": the real
 * Save bar wiring over SettingsShellContext) and the retired autosave
 * assertions are dropped, replaced per FR-010 (useAutoSave removed from
 * Settings, #346 — spec regression table "Fixed by removal"):
 *   - "autosave fires updatePromptGuardLevel on radio click" → replaced by
 *     FR-010 (nothing stores until Save changes);
 *   - "no Save button is rendered" → replaced by FR-010/FR-011 (the bar with
 *     "Save changes" MUST appear within 100 ms of a change);
 *   - SaveStatus "Saving…" / "Saved" → the bar's FR-018 saved signal, owned
 *     by saveBar.counting.test; not duplicated here;
 *   - "no restart badge when requires_restart: false" → kept, restated as
 *     the full US-4 AS6 outcome (no badge, no window, no banner);
 *   - "error toast on save failure" → replaced by FR-020 outcome 3 / FR-015
 *     (a refused write keeps the draft and shows the server's message under
 *     the field).
 *
 * Oracles (spec, never code):
 *   - Wireframe app.js block "Prompt injection defense": three radios — Low
 *     ("Fewest false alarms."), Medium (default) ("Balanced."), High
 *     ("Strictest; some harmless text may be blocked."); the spec's wording
 *     rule ("Wording follows the wireframe (app.js)"; wireframe wins on
 *     wording) makes these strings the rendering oracle.
 *   - FR-012 / BDD "Given Protection with Defense level Medium": the saved
 *     level is the shown, selected value on open.
 *   - FR-010: selecting High stores nothing until Save changes.
 *   - FR-011: the bar counts "1 unsaved change"; FR-025 (Defense level is
 *     NOT a Password row) and FR-037 (Defense level MUST NOT carry a
 *     Needs-restart mark) mean the bar counts no password and no restart
 *     for this change.
 *   - FR-015 + US-3 AS7 ("changes that carry no Password badge … no password
 *     prompt appears"): Save changes sends exactly one write of the changed
 *     level to the section's own prompt-guard write, with no prompt.
 *   - US-4 AS6 / BDD "Defense level applies at once" (G-12): after a save
 *     whose response says requires_restart: false (the hot-reload contract,
 *     spec "Badge corrections" row) — no Needs-restart badge, no "Restart
 *     now?" window, no banner.
 *   - FR-020 outcome 3 / FR-015: a 400 refusal keeps the typed value and
 *     shows the server's message under the field.
 *
 * RED on landing: the redesign shell (SettingsShellContext) does not exist
 * there yet, so every case fails loudly as BLOCKED — not as a collection
 * error.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, within, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from '@tanstack/react-router'
import type { ReactElement, ReactNode } from 'react'
import { ApiError } from '@/lib/api'

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
    fetchPromptGuardLevel: vi.fn(),
    updatePromptGuardLevel: vi.fn(),
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

/**
 * The redesign shell the Protection screen runs on (the Save bar and the
 * one-password-prompt boundary live there). On landing it does not exist
 * yet: fail loudly as BLOCKED for the spec's reason — never as a bare
 * module-resolution error.
 */
async function loadShellContext(): Promise<{ SettingsShellContext: { Provider: (p: { value: unknown; children: ReactNode }) => ReactElement } }> {
  try {
    return (await vi.importActual('./SettingsShellContext')) as never
  } catch {
    throw new Error(
      'BLOCKED RED (FR-010/FR-011, PromptGuardSection part 11): the Settings shell (SettingsShellContext) does not ' +
        'exist yet — the Protection screen cannot draft Defense level on the Save bar, as FR-010 and FR-011 require',
    )
  }
}

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

/** The Protection view renders a router Link, so the section mounts under a router. */
function renderProtection(ui: ReactElement) {
  const root = createRootRoute({ component: () => ui })
  const router = createRouter({ routeTree: root, history: createMemoryHistory({ initialEntries: ['/'] }) })
  render(
    <QueryClientProvider client={makeClient()}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
}

/** The way the Protection screen renders the section: draft mode on the section's Save bar. */
async function renderProtectionView() {
  const { SettingsShellContext } = await loadShellContext()
  const requestPassword = vi.fn(async (_labels: string[]) => 'tok-unused')
  const offerRestart = vi.fn()
  renderProtection(
    <SettingsShellContext.Provider
      value={{
        announce: () => {},
        setDraftCount: () => {},
        offerRestart,
        refreshPendingRestart: () => {},
        requestPassword,
        saveBarVisible: false,
        setSaveBarVisible: () => {},
      }}
    >
      <SecuritySection view="protection" />
    </SettingsShellContext.Provider>,
  )
  return { requestPassword, offerRestart }
}

/** The saved level the gateway answers with; the save flow moves it. */
let savedLevel: 'low' | 'medium' | 'high'

beforeEach(() => {
  vi.clearAllMocks()
  savedLevel = 'medium'
  vi.mocked(api.fetchPromptGuardLevel).mockImplementation(async () => ({ level: savedLevel, requires_restart: false }) as never)
  vi.mocked(api.updatePromptGuardLevel).mockResolvedValue({ requires_restart: false, applied_level: savedLevel } as never)
  // A minimal but complete config: the section mounts its ["config"] query on
  // every view (protectionSave.red.test.tsx precedent).
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
  vi.mocked(api.fetchSandboxConfig).mockResolvedValue({ auto_approve: false, mode: 'enforce', applied_mode: 'enforce' } as never)
  vi.mocked(api.updateSandboxConfig).mockResolvedValue({} as never)
  vi.mocked(api.fetchGodMode).mockResolvedValue({ enabled: false, available: true, supported: true, persisted: false } as never)
  vi.mocked(api.setGodMode).mockResolvedValue({ enabled: true, restart_required: false } as never)
  vi.mocked(api.reAuth).mockResolvedValue({ verified: true, token: 'reauth_tok', expires_in: 300 } as never)
  // State-changing calls need the CSRF cookie planted (ProvidersSection.customModels precedent).
  document.cookie = 'csrf=prompt-guard-draft-test'
})

const promptGuardRadios = () => within(screen.getByRole('radiogroup', { name: /prompt injection defense/i })).getAllByRole('radio')

describe('Protection renders Prompt injection defense (wireframe app.js, wording per the spec wording rule)', () => {
  it('renders the three levels with the wireframe subtitles and the saved level selected', async () => {
    renderProtectionView()
    // Three radios: Low, Medium (default), High (wireframe "Prompt injection defense").
    await screen.findByRole('radiogroup', { name: /prompt injection defense/i }, { timeout: 5000 })
    expect(promptGuardRadios()).toHaveLength(3)
    // The wireframe's per-level wording is the spec's rendering oracle
    // ("Wording follows the wireframe (app.js)"; wireframe wins on wording).
    // The shipped subtitles ("Minimal/Balanced/Aggressive sanitization…")
    // are not the wireframe's — production-side wording drift, listed.
    expect(screen.getByText('Fewest false alarms.')).toBeInTheDocument()
    expect(screen.getByText('Balanced.')).toBeInTheDocument()
    expect(screen.getByText('Strictest; some harmless text may be blocked.')).toBeInTheDocument()
    // FR-012 / BDD "Given Protection with Defense level Medium": the saved
    // level is the shown, selected value on open.
    const medium = promptGuardRadios().find((r) => r.getAttribute('name')?.includes('Medium') || (r as HTMLElement).textContent?.startsWith('Medium'))
    expect(medium, 'the saved level Medium must be the selected one on open').toBeTruthy()
    expect(medium).toBeChecked()
  })
})

describe('FR-010/FR-011 — Defense level drafts on the Protection Save bar', () => {
  it('selecting High stores nothing and counts 1 unsaved change with no restart and no password marks', async () => {
    const { requestPassword } = await renderProtectionView()
    const radios = await screen.findAllByRole('radio', {}, { timeout: 5000 })
    const high = radios.find((r) => (r as HTMLElement).textContent?.startsWith('High'))
    expect(high, 'locator: the High radio by its visible label').toBeTruthy()
    await userEvent.click(high!)
    // FR-010 (unqualified): a changed value MUST NOT be stored until Save
    // changes is pressed. The retired assertion had the click SAVING.
    expect(api.updatePromptGuardLevel, 'FR-010 RED on the old autosave code: the level stored at once').not.toHaveBeenCalled()
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    // FR-025: Defense level is not a Password row; FR-037: it MUST NOT carry
    // a Needs-restart mark — so the bar counts neither for this change.
    expect(screen.queryByText(/needs? your password/)).not.toBeInTheDocument()
    expect(screen.queryByText(/needs? a gateway restart/)).not.toBeInTheDocument()
    expect(requestPassword).not.toHaveBeenCalled()
  })

  it('Save changes sends exactly one write of the changed level, with no password prompt (FR-015, FR-026 negative)', async () => {
    const { requestPassword } = await renderProtectionView()
    const radios = await screen.findAllByRole('radio', {}, { timeout: 5000 })
    await userEvent.click(radios.find((r) => (r as HTMLElement).textContent?.startsWith('High'))!)
    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    // US-3 AS7: changes that carry no Password badge open no prompt.
    expect(requestPassword).not.toHaveBeenCalled()
    // FR-015: the changed value goes to its own endpoint — exactly one
    // prompt-guard write carrying the new level.
    await waitForPromptGuardWrite('high')
  })

  it('after the save, no Needs-restart badge, no "Restart now?" window and no banner appear (US-4 AS6, FR-037)', async () => {
    const { offerRestart } = await renderProtectionView()
    const radios = await screen.findAllByRole('radio', {}, { timeout: 5000 })
    await userEvent.click(radios.find((r) => (r as HTMLElement).textContent?.startsWith('High'))!)
    // The hot-reload contract: the write answers requires_restart: false
    // (spec "Badge corrections" row) unless the reload fails.
    vi.mocked(api.updatePromptGuardLevel).mockResolvedValue({ requires_restart: false, applied_level: 'high' } as never)
    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    await waitForPromptGuardWrite('high')
    // BDD "Defense level applies at once": the row carries no Needs-restart
    // badge, no "Restart now?" window opens, and no banner appears.
    expect(offerRestart, 'US-4 AS6 RED: a saved Defense level must open no restart window').not.toHaveBeenCalled()
    expect(screen.queryByText(/restart now\?/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/needs restart/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/waits? for a gateway restart/)).not.toBeInTheDocument()
  })
})

describe('FR-020 outcome 3 — a refused Defense-level write', () => {
  it('keeps the drafted level and shows the server message under the field (FR-020/FR-015)', async () => {
    await renderProtectionView()
    const radios = await screen.findAllByRole('radio', {}, { timeout: 5000 })
    await userEvent.click(radios.find((r) => (r as HTMLElement).textContent?.startsWith('High'))!)
    vi.mocked(api.updatePromptGuardLevel).mockRejectedValue(new ApiError(400, 'prompt injection level: change refused by the server'))
    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    // FR-020 outcome 3: not saved — the draft is kept and the server's
    // message shows under the field (the retired toast is replaced).
    expect(await screen.findByText(/change refused by the server/)).toBeInTheDocument()
    const high = promptGuardRadios().find((r) => (r as HTMLElement).textContent?.startsWith('High'))
    expect(high, 'FR-020 outcome 3: a refused write keeps its typed value').toBeChecked()
    // US-2 AS7 / states table: the bar says "1 change could not be saved".
    expect(await screen.findByText(/1 change could not be saved/)).toBeInTheDocument()
  })
})

async function waitForPromptGuardWrite(level: string) {
  await vi.waitFor(() => {
    expect(api.updatePromptGuardLevel).toHaveBeenCalledTimes(1)
    expect(api.updatePromptGuardLevel).toHaveBeenCalledWith(level)
  })
}

/**
 * SecuritySection tests.
 *
 * Coverage:
 *   US-B1 — the plain Protection page carries no jargon (#327; the Advanced
 *     layer that used to hold it is gone, see "Moved off the legacy layout")
 *   US-B4 — score deduction links to fix; score=100 reassurance (#330)
 *
 * Retired in Skills & Tools fix round 4 (CHECK round 4, candidates 9, 11, 22):
 * eleven cases that drove SecuritySection's legacy layout, which
 * skills-tools-redesign C-22 row 6 deletes together with its global tool grid
 * and skill trust card (the Skills & Tools page holds both, C-22 row 1). No
 * case with a replacement was dropped without moving it:
 *   US-B3 preset buttons (1)  — presets are retired (FR-022, T-Q6 A; Regression
 *     row "Presets | preset cases ... | Deleted with the feature").
 *   US-B3 system.* flat grid, Mixed pill (2) — the same invariants stay pinned
 *     on the editor itself in ToolPolicyEditor.test.tsx and AgentTools.test.tsx.
 *   #340 SkillTrustSection mounted, three radios (2) — the trust block with
 *     Allow / Ask / Deny is SkillsToolsPage.trust.test.tsx (S-26, S-32); the
 *     Block / Warn / Allow vocabulary is retired (FR-027).
 *   global tool-policy save (1) — "saves directly, no confirmation" is
 *     SkillsToolsPage.autosave.test.tsx (T-Q3 B); its apply/recovery checks are
 *     SaveQueue.states.test.ts (C-16).
 *   MCP tools in the global editor (5) — External tools tab rows (S-03, S-04,
 *     S-45, C-2): src/components/skills/SkillsToolsPage.external.test.tsx; the
 *     spec's test plan also maps S-03 to tests/e2e/skills-tools.spec.ts.
 *
 * Moved off the legacy layout in Skills & Tools fix round 4, Part C item 1
 * (item 11): SettingsSectionBody always passes view="protection" or
 * view="keys" (spec C-22, "The Settings base" table, Global tool grid row),
 * and C-22 "What this landing does" row 6 deletes "SecuritySection's legacy
 * layout" — the layout with no view, with its Advanced disclosure that held
 * the rate limits, the Auto-approve Save bar and the jargon. Every case below
 * renders the view its content lives in; no assertion was weakened:
 *   - the Auto-approve, rate-limit hydration and US-B4 cases render
 *     view="protection" and no longer open an Advanced disclosure (it is gone);
 *   - the credential vault cases render view="keys";
 *   - "does not render the retired Shell command approval or Enable deny
 *     patterns controls" keeps its absence checks on the Protection view, where
 *     the page that held them now lives; its instrument waits for the rows that
 *     sat beside them instead of waiting for the jargon the Advanced layer held;
 *   - RETIRED, one case: "expanding Advanced reveals jargon (Landlock / SSRF)
 *     that was hidden" drove only the removed disclosure (C-22 row 6). Its
 *     other half, "no jargon on the plain page", stays pinned by "shows
 *     Security health header without jargon in the initial render" above, now on
 *     the Protection view; the jargon itself belongs to the Sandbox section
 *     (SandboxSection.test.tsx), outside SecuritySection.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, fireEvent, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useMemo } from 'react'
import type { ReactNode } from 'react'

// ── Mocks (must be hoisted before any import that uses them) ──────────────────

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchConfig: vi.fn(),
    updateConfig: vi.fn(),
    fetchGatewayStatus: vi.fn(),
    fetchCredentials: vi.fn(),
    addCredential: vi.fn(),
    deleteCredential: vi.fn(),
    rotateCredentials: vi.fn(),
    fetchDoctorResults: vi.fn(),
    runDoctor: vi.fn(),
    fetchAppState: vi.fn(),
    // ADR-092: AutoApproveControl mounts in the primary (always-rendered)
    // layer, unlike SandboxSection (Advanced, collapsed by default) — its
    // ['sandbox-config'] and ['sandbox-status'] queries fire on every
    // render, so both need a mock here too, not just inside SandboxSection's
    // own test file.
    fetchSandboxConfig: vi.fn(),
    updateSandboxConfig: vi.fn(),
    fetchSandboxStatus: vi.fn(),
    // #1882: the per-agent rate limits are read and saved through their own route.
    fetchRateLimits: vi.fn(),
    updateRateLimits: vi.fn(),
  }
})

vi.mock('@/store/ui', () => ({
  useUiStore: vi.fn(() => ({ addToast: vi.fn() })),
}))

vi.mock('@/store/auth', () => ({
  useAuthStore: vi.fn((selector: (s: { role: string }) => unknown) =>
    selector({ role: 'admin' }),
  ),
}))

// ── Imports after mocks ────────────────────────────────────────────────────────

import {
  fetchConfig,
  updateConfig,
  fetchGatewayStatus,
  fetchCredentials,
  addCredential,
  deleteCredential,
  fetchDoctorResults,
  rotateCredentials,
  fetchAppState,
  fetchSandboxConfig,
  updateSandboxConfig,
  fetchSandboxStatus,
  fetchRateLimits,
  updateRateLimits,
} from '@/lib/api'
import type { AppState } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { SecuritySection } from './SecuritySection'
import { SettingsShellContext, type SettingsShellApi } from './SettingsShellContext'
import { useSavePasswordPrompt } from './useSavePasswordPrompt'
import { ApiError } from '@/lib/api-error'

// Platform mode (identity.mode: 'platform') pins useStepUp() to 'confirm' —
// ConfirmDialog, no consent token (ADR-0010 WP3). The password-mode (local
// edition) equivalent lives in SecuritySection.password.test.tsx.
const PLATFORM_APP_STATE = {
  onboarding_complete: true,
  identity: { mode: 'platform', edition: 'hosted', signed_in: true },
} as AppState

// ── Fixtures ──────────────────────────────────────────────────────────────────

const MINIMAL_CONFIG = {
  security: {
    exec_timeout_seconds: 0,
    max_background_seconds: 0,
    rate_limits: {
      max_agent_llm_calls_per_hour: null,
      max_agent_tool_calls_per_minute: null,
    },
  },
  gateway: {
    bind_address: '127.0.0.1',
    port: 5000,
    token: 'test-token',
    hot_reload: false,
    log_level: 'info',
  },
  agents: {
    defaults: { default_agent_id: '' },
  },
}

const DOCTOR_RESULT_ISSUES = {
  score: 75,
  checked_at: new Date().toISOString(),
  issues: [
    {
      id: 'auth-none',
      severity: 'high' as const,
      title: 'No login required',
      description: 'Auth mode is none.',
      recommendation: 'Enable bearer token authentication.',
      action_link: '/settings/gateway',
      action_label: 'Go to Gateway settings',
    },
  ],
}

const DOCTOR_RESULT_PERFECT = {
  score: 100,
  checked_at: new Date().toISOString(),
  issues: [],
}

// ── Helper ─────────────────────────────────────────────────────────────────────

function makeClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
}

/** Settings → Protection: the layout SettingsSectionBody passes (view="protection"). */
function renderSection(client = makeClient()) {
  return render(
    <QueryClientProvider client={client}>
      <SecuritySection view="protection" />
    </QueryClientProvider>
  )
}

/** Settings → Keys & secrets: the credential vault (view="keys"). */
function renderKeysSection(client = makeClient()) {
  return render(
    <QueryClientProvider client={client}>
      <SecuritySection view="keys" />
    </QueryClientProvider>
  )
}

/**
 * Settings redesign #1519: the Save bar's one password step per Save lives in
 * the shell (FR-026) — SettingsScreen wires `useSavePasswordPrompt()` into
 * SettingsShellContext, and outside the shell `requestPassword` is undefined,
 * so a password-marked Save would skip its confirmation entirely. This
 * provider supplies the real hook (the real ConfirmDialog in platform mode,
 * the real ReAuthDialog in local mode), so the whole chain from click to PUT
 * is the production chain.
 */
function SettingsShellProvider({ children }: { children: ReactNode }) {
  const { requestPassword, dialog } = useSavePasswordPrompt()
  const api = useMemo<SettingsShellApi>(
    () => ({
      announce: () => {},
      setDraftCount: () => {},
      offerRestart: () => {},
      refreshPendingRestart: () => {},
      requestPassword,
      saveBarVisible: false,
      setSaveBarVisible: () => {},
    }),
    [requestPassword],
  )
  return (
    <SettingsShellContext.Provider value={api}>
      {children}
      {dialog}
    </SettingsShellContext.Provider>
  )
}

function renderSectionWithShell(client = makeClient()) {
  return render(
    <QueryClientProvider client={client}>
      <SettingsShellProvider>
        <SecuritySection view="protection" />
      </SettingsShellProvider>
    </QueryClientProvider>
  )
}

const mockAddToast = vi.fn()

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(useUiStore).mockReturnValue({ addToast: mockAddToast } as never)

  // Default happy-path stubs
  vi.mocked(fetchConfig).mockResolvedValue(MINIMAL_CONFIG as never)
  vi.mocked(fetchGatewayStatus).mockResolvedValue({ daily_cost: 0, uptime_seconds: 0 } as never)
  vi.mocked(fetchCredentials).mockResolvedValue([])
  vi.mocked(fetchDoctorResults).mockResolvedValue(null)
  vi.mocked(fetchAppState).mockResolvedValue(PLATFORM_APP_STATE)
  vi.mocked(fetchSandboxConfig).mockResolvedValue({ auto_approve: false } as never)
  vi.mocked(fetchRateLimits).mockResolvedValue({ enabled: true, max_agent_llm_calls_per_hour: 0, max_agent_tool_calls_per_minute: 0 } as never)
  // Default: a kernel sandbox IS enforcing, so the platform caveat renders
  // muted, not as a caution — individual tests override this to exercise
  // the no-sandbox caution path.
  vi.mocked(fetchSandboxStatus).mockResolvedValue({ kernel_sandbox_active: true } as never)
})

// ── US-B1: Two-layer IA ───────────────────────────────────────────────────────

describe('SecuritySection — US-B1 two-layer IA', () => {
  it('shows Security health header without jargon in the initial render', async () => {
    renderSection()

    // Wait for the component to settle (config loaded)
    await waitFor(() => {
      expect(screen.getByTestId('security-health-header')).toBeInTheDocument()
    })

    // Jargon words must NOT appear on the plain Protection page (the technical
    // layer that held them behind Advanced is the Sandbox section now)
    const bodyText = document.body.textContent ?? ''
    expect(bodyText).not.toMatch(/Landlock/)
    expect(bodyText).not.toMatch(/SSRF/)
    expect(bodyText).not.toMatch(/deny.?regex|deny patterns/i)
    expect(bodyText).not.toMatch(/seccomp/i)
  })

  it('plain toggles are visible at top level without expanding Advanced', async () => {
    renderSection()

    await waitFor(() => {
      expect(screen.getByTestId('plain-toggles')).toBeInTheDocument()
    })

    // Auto-approve is the plain-language, top-level tool-access control.
    await waitFor(() => {
      expect(screen.getByTestId('auto-approve-global-switch')).toBeInTheDocument()
    })
  })

  it('does not render the retired Shell command approval or Enable deny patterns controls', async () => {
    renderSection()

    await waitFor(() => {
      expect(screen.getByTestId('plain-toggles')).toBeInTheDocument()
    })

    // Both wrote fields the backend never read (security.exec_approval,
    // security.enable_deny_patterns) — deleted outright, not just hidden.
    // "Enable deny patterns" used to live in the Advanced layer, which is gone
    // with the legacy layout (C-22 row 6): the Protection view shows its whole
    // content at once. The instrument: the rows that sat beside it — the
    // per-agent rate limits, the prompt-injection defence and the audit log —
    // must be on the page before the absence checks mean anything.
    expect(await screen.findByRole('heading', { name: 'Rate limits per agent' })).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Prompt injection defense' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'View Log' })).toBeInTheDocument()

    expect(screen.queryByText(/shell command approval/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/enable deny patterns/i)).not.toBeInTheDocument()
    expect(screen.queryByLabelText(/enable deny patterns/i)).not.toBeInTheDocument()
  })
})

// ── ADR-092: global Auto-approve switch ───────────────────────────────────────
//
// Settings redesign #1519 reconciliation (item 15 of ta-old-tests-items.md,
// team-lead's FR-010 ruling): the retired instant-save mode is gone — every
// reachable path waits for Save changes, and the old Security view passes the
// same drafts as Protection. The five cases below that clicked the switch and
// expected the retired "Turn Auto-approve on?" confirmation + instant save
// move to the draft-and-Save-bar oracle. Each replaced assertion cites the
// FR (or dataset row) that replaces it:
//   - FR-010 (spec :1914): a toggle stores nothing and opens no dialog —
//     a confirmation opens only when Save changes is pressed;
//   - FR-011 (:1915): the change counts — "1 unsaved change", "1 needs your
//     password";
//   - FR-032 + BDD "Platform mode asks for a confirmation instead"
//     (:1016-1021): Save changes opens a confirmation listing "Auto-approve"
//     with no password field, and no POST /auth/reauth is sent;
//   - D-8 row 3 (:1753): the section's one protected write — exactly one
//     PUT /security/sandbox-config carrying { auto_approve } — and, in
//     platform mode, no consent token (FR-027's one-request rule, platform
//     form: no reauth ran to mint one);
//   - D-11 rows 6 and 9: platform Cancel sends 0 reauth and 0 writes and
//     keeps every draft (FR-029's Cancel rule, with FR-032 substituting the
//     confirmation for the prompt);
//   - a refused write is the FR-015/FR-020 outcome — the bar says "1 change
//     could not be saved", the server's message shows under the row and the
//     draft is kept — replacing the retired per-mutation error toast;
//   - spec :554 (ADR-092): "Auto-approve keeps its password; the help text
//     says each chat can still turn it off with its Auto button." — the help
//     text, not a dialog, carries the explanation now.
//
// Harness: the real chain — SecuritySection (Protection view) + useSaveBar +
// useSavePasswordPrompt + the real ConfirmDialog — with the @/lib/api client
// functions mocked at the API edge and a global fetch recorder (404 default)
// proving no POST /auth/reauth and no other un-mocked state change. The
// platform dialog wording itself is pinned once, by passwordOncePerSave.test
// (D-11 rows 6/9); this file owns the section's side of that boundary.

let fetchCalls: { method: string; path: string }[] = []

/** Recorder at the process edge: every un-mocked request lands here, 404 by default. */
function stubFetchRecorder() {
  fetchCalls = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const isRequest = typeof Request !== 'undefined' && input instanceof Request
      const url = typeof input === 'string' ? input : input instanceof URL ? input.pathname : input.url
      const path = url.replace(/^https?:\/\/[^/]+/, '').split('?')[0]
      const method = (init?.method ?? (isRequest ? input.method : 'GET')).toUpperCase()
      fetchCalls.push({ method, path })
      return new Response(JSON.stringify({ error: 'not stubbed' }), {
        status: 404,
        headers: { 'Content-Type': 'application/json' },
      })
    }),
  )
}

const reauthPosts = () => fetchCalls.filter((c) => c.method === 'POST' && c.path.includes('/auth/reauth'))

describe('SecuritySection — ADR-092 Auto-approve global switch', () => {
  // Only the five rewritten cases need the fetch recorder; the other cases in
  // this describe run on the file-level mocks alone, as before.
  beforeEach(() => {
    stubFetchRecorder()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  /**
   * Toggle Auto-approve and reach the Save bar. The Protection view carries
   * the section's one Save bar in plain view; the legacy layout kept it inside
   * a collapsed Advanced disclosure, which is gone (C-22 row 6), so there is
   * nothing to expand — the counting itself happens at the toggle.
   */
  async function draftAndOpenBar() {
    renderSectionWithShell()
    // useSavePasswordPrompt's platform/local decision comes from the
    // ['app-state'] query — wait for it so Save changes takes the mode this
    // harness pins (fail-toward-the-stronger-control until the mode is known).
    await waitFor(() => expect(fetchAppState).toHaveBeenCalled())

    const toggle = await screen.findByTestId('auto-approve-global-switch')
    await waitFor(() => expect(toggle).toHaveAttribute('aria-checked', 'false'))
    fireEvent.click(toggle)
    // FR-010: nothing is stored by the toggle itself.
    expect(updateSandboxConfig).not.toHaveBeenCalled()

    // FR-011: the change counts, and counts as needing the password
    // (FR-025: Auto-approve is a Password-badged row).
    await screen.findByText('1 unsaved change')
    expect(screen.getByText('1 needs your password')).toBeInTheDocument()
  }

  it('reflects the persisted auto_approve value from sandbox-config', async () => {
    vi.mocked(fetchSandboxConfig).mockResolvedValue({ auto_approve: true } as never)
    renderSection()

    await waitFor(() => {
      expect(screen.getByTestId('auto-approve-global-switch')).toHaveAttribute('aria-checked', 'true')
    })
  })

  it('defaults to off when auto_approve is false', async () => {
    vi.mocked(fetchSandboxConfig).mockResolvedValue({ auto_approve: false } as never)
    renderSection()

    await waitFor(() => {
      expect(screen.getByTestId('auto-approve-global-switch')).toHaveAttribute('aria-checked', 'false')
    })
  })

  it('toggling only drafts: Save changes confirms once ("Auto-approve", no password field) and then sends the one sandbox-config PUT', async () => {
    vi.mocked(fetchSandboxConfig).mockResolvedValue({ auto_approve: false } as never)
    vi.mocked(updateSandboxConfig).mockResolvedValue({ auto_approve: true, saved: true } as never)
    await draftAndOpenBar()

    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))

    // FR-032 + spec :1016-1021 ("Platform mode asks for a confirmation
    // instead"): the confirmation lists "Auto-approve" and has no password
    // field. The retired instant "Turn Auto-approve on?" dialog is gone with
    // the instant-save mode (FR-010 ruling, item 15).
    expect(await screen.findByText('Save these settings?')).toBeInTheDocument()
    expect(screen.getByText('This saves: Auto-approve.')).toBeInTheDocument()
    expect(screen.queryByLabelText(/password/i)).not.toBeInTheDocument()
    // FR-016: the confirm runs before any write.
    expect(updateSandboxConfig).not.toHaveBeenCalled()

    fireEvent.click(screen.getByRole('button', { name: 'Save' }))

    // D-8 row 3: exactly one protected write, carrying the section's one
    // password-marked value. FR-032: platform mode mints no consent token,
    // so the token argument carries no token.
    await waitFor(() => {
      expect(updateSandboxConfig).toHaveBeenCalledTimes(1)
    })
    expect(vi.mocked(updateSandboxConfig).mock.calls[0][0]).toEqual({ auto_approve: true })
    expect(vi.mocked(updateSandboxConfig).mock.calls[0][1]).toBeFalsy()
    // Spec :1021: and no POST /auth/reauth is sent.
    expect(reauthPosts()).toEqual([])
  })

  it('cancelling the platform confirmation sends no reauth and no write, and keeps the draft (D-11 row 9)', async () => {
    vi.mocked(fetchSandboxConfig).mockResolvedValue({ auto_approve: false } as never)
    await draftAndOpenBar()

    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    expect(await screen.findByText('Save these settings?')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /cancel/i }))

    await waitFor(() => {
      expect(screen.queryByText('Save these settings?')).not.toBeInTheDocument()
    })
    // D-11 row 9: platform-mode Cancel — 0 reauth, 0 writes.
    expect(reauthPosts()).toEqual([])
    expect(updateSandboxConfig).not.toHaveBeenCalled()
    // FR-029's Cancel rule (the confirmation substitutes the prompt,
    // FR-032): the draft is kept — the switch still holds the toggled
    // position and the bar still counts it.
    expect(screen.getByTestId('auto-approve-global-switch')).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByText('1 unsaved change')).toBeInTheDocument()
  })

  it('shows an error state when the sandbox-config fetch fails', async () => {
    vi.mocked(fetchSandboxConfig).mockRejectedValue(new Error('network error'))
    renderSection()

    await waitFor(() => {
      // F12 / FR-066, team-lead's ninth-file ruling: name the failed read,
      // retain its injected cause and expose Retry in the actual Callout.
      const callout = screen.getByRole('alert')
      expect(callout).toHaveAttribute('data-slot', 'callout')
      expect(callout).toHaveAttribute('data-variant', 'error')
      expect(callout).toHaveTextContent(/approvals/i)
      expect(callout).toHaveTextContent('network error')
      expect(within(callout).getByRole('button', { name: 'Retry' })).toBeEnabled()
    })
    expect(screen.queryByTestId('auto-approve-global-switch')).not.toBeInTheDocument()
  })

  it('cancelling the platform confirmation shows no toast at all (cancel stays silent)', async () => {
    vi.mocked(fetchSandboxConfig).mockResolvedValue({ auto_approve: false } as never)
    await draftAndOpenBar()

    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    expect(await screen.findByText('Save these settings?')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /cancel/i }))

    await waitFor(() => {
      expect(screen.queryByText('Save these settings?')).not.toBeInTheDocument()
    })
    // FR-020 classifies write outcomes only; a cancelled Save produced no
    // write, so nothing may be announced as an error. The retired re-auth
    // cancel stayed silent for the same reason.
    expect(mockAddToast).not.toHaveBeenCalled()
    expect(reauthPosts()).toEqual([])
    expect(updateSandboxConfig).not.toHaveBeenCalled()
  })

  // Founder feedback (2026-09-24): the old card put the full grouped list of
  // 28 always-ask tools, the workspace path rule, an example and the
  // Windows caveat into ONE long paragraph — "a huge blob of text, not well
  // written". The redesign: one short summary sentence, always visible; the
  // grouped ask-list collapsed by default behind "Always asks when set to
  // Ask" (renamed from "Still asks every time" — UAT defect D-02, founder
  // decision 2026-09-24: the old heading promised something false, since
  // several of these tools are on Allow by default and never ask at all);
  // a small muted platform caveat. Written against the copy, not the
  // implementation, so it fails if the old paragraph comes back.
  //
  // Founder decision (2026-09-24, same day): Auto no longer needs the
  // sandbox — the old "Needs the sandbox — not available on Windows" line
  // (which implied Auto is switched OFF without one) is replaced by a
  // caveat that Auto still works, just checks shell commands by text only.
  //
  // Founder correction (2026-09-24, later same day): the summary sentence's
  // own "and the sandbox" clause is also gone — under the new design Auto
  // runs with no sandbox at all, so claiming tools "stay inside ... the
  // sandbox" is false whenever no sandbox is enforcing. The sentence then
  // said only "they stay inside your workspace." — itself replaced for #920
  // (founder decision D3, 2026-09-27; see the assertion below).
  it('shows a short always-visible summary — not the old long paragraph, and the new no-sandbox caveat, not the old "not available on Windows" line', async () => {
    vi.mocked(fetchSandboxConfig).mockResolvedValue({ auto_approve: false } as never)
    renderSection()

    await screen.findByTestId('auto-approve-global-switch')

    expect(screen.queryByText(/currently applies to shell commands/i)).not.toBeInTheDocument()
    // The old paragraph's exhaustive inline wording must be gone from the
    // always-visible summary — it now lives in the collapsed list instead.
    expect(screen.queryByText(/asking for a mounted folder; installing a skill/i)).not.toBeInTheDocument()
    expect(screen.getByText(/tools set to .ask. run without a prompt when it.s safe/i)).toBeInTheDocument()
    // #920 founder decision D3 (2026-09-27): under Auto, reading, listing and
    // searching run outside the workspace too; only writing keeps the
    // workspace rule. The old "they stay inside your workspace." claim is
    // false for reads and must be gone.
    expect(
      screen.getByText(
        /reading, listing and searching run anywhere except omnipus.s protected files, and writing runs without a prompt only inside your workspace and the folders mounted into it\./i,
      ),
    ).toBeInTheDocument()
    expect(screen.queryByText(/they stay inside your workspace/i)).not.toBeInTheDocument()
    // The old "and the sandbox" clause is gone — it claimed a guarantee
    // that no longer holds once Auto works without an enforcing sandbox.
    expect(screen.queryByText(/workspace and the sandbox/i)).not.toBeInTheDocument()
    // The old "Needs the sandbox — not available on Windows" line is gone.
    expect(screen.queryByText(/needs the sandbox/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/not available on windows/i)).not.toBeInTheDocument()
    expect(
      screen.getByText(/without a kernel sandbox \(for example on windows\), shell commands ask first, except read-only ones and commands an operator rule allows/i),
    ).toBeInTheDocument()
    // The old "checked by reading the command text only" wording described
    // stale behaviour (2026-09-24 shell-no-sandbox-gate rewording: shell
    // commands with no kernel sandbox now ask first) and must never reappear.
    expect(screen.queryByText(/checked by reading the command text only/i)).not.toBeInTheDocument()
  })

  it('shows the no-sandbox caveat as a caution (warning-colored) when the server reports no enforcing kernel sandbox', async () => {
    vi.mocked(fetchSandboxConfig).mockResolvedValue({ auto_approve: false } as never)
    vi.mocked(fetchSandboxStatus).mockResolvedValue({ kernel_sandbox_active: false } as never)
    renderSection()

    await screen.findByTestId('auto-approve-global-switch')

    const caveat = await screen.findByText(
      /without a kernel sandbox \(for example on windows\), shell commands ask first, except read-only ones and commands an operator rule allows/i,
    )
    await waitFor(() => {
      expect(caveat.className).toContain('text-[var(--color-warning)]')
    })
    expect(caveat.className).not.toContain('text-[var(--color-muted)]')
  })

  it('shows the no-sandbox caveat muted (not a caution) when the server reports an enforcing kernel sandbox', async () => {
    vi.mocked(fetchSandboxConfig).mockResolvedValue({ auto_approve: false } as never)
    vi.mocked(fetchSandboxStatus).mockResolvedValue({ kernel_sandbox_active: true } as never)
    renderSection()

    const caveat = await screen.findByText(
      /without a kernel sandbox \(for example on windows\), shell commands ask first, except read-only ones and commands an operator rule allows/i,
    )
    await waitFor(() => {
      expect(caveat.className).toContain('text-[var(--color-muted)]')
    })
    expect(caveat.className).not.toContain('text-[var(--color-warning)]')
  })

  it('the ask-list is collapsed by default and expands to show every group on click', async () => {
    vi.mocked(fetchSandboxConfig).mockResolvedValue({ auto_approve: false } as never)
    renderSection()

    await screen.findByTestId('auto-approve-global-switch')

    // Collapsed: the trigger row is visible, but none of the group labels are.
    const trigger = await screen.findByTestId('auto-approve-ask-list-trigger')
    // UAT defect D-02 (founder decision 2026-09-24): "Still asks every time"
    // promised something false — several of these tools are on Allow by
    // default and never ask. Renamed to name the real condition.
    expect(trigger).toHaveTextContent(/always asks when set to ask/i)
    expect(trigger).not.toHaveTextContent(/still asks every time/i)
    expect(screen.queryByTestId('auto-approve-ask-list-content')).not.toBeInTheDocument()
    expect(screen.queryByText(/sending email/i)).not.toBeInTheDocument()

    fireEvent.click(trigger)

    const content = await screen.findByTestId('auto-approve-ask-list-content')
    // Every one of the eight founder-approved groups must appear once expanded.
    expect(within(content).getByText('Sending email')).toBeInTheDocument()
    expect(within(content).getByText('Deleting tasks, agents or workspaces')).toBeInTheDocument()
    expect(
      within(content).getByText('Installing skills, setting up an environment or publishing a web preview'),
    ).toBeInTheDocument()
    expect(
      within(content).getByText('Changing or testing settings, providers, channels, agents or skills'),
    ).toBeInTheDocument()
    expect(within(content).getByText('Running diagnostics')).toBeInTheDocument()
    expect(
      within(content).getByText('Adding or removing connected (MCP) servers, and MCP tools not marked safe'),
    ).toBeInTheDocument()
    expect(within(content).getByText('Browser scripts and uploads')).toBeInTheDocument()
    expect(
      within(content).getByText('Mounting a folder, or writing files outside your workspace'),
    ).toBeInTheDocument()
    // #920 D3: the old label implied any file outside the workspace asks —
    // false for reading, listing and searching under Auto.
    expect(within(content).queryByText('Mounting a folder, or files outside your workspace')).not.toBeInTheDocument()
  })

  it('toggling opens no explainer dialog — the card’s own help text naming the per-chat Auto button replaces the retired turn-on dialog', async () => {
    vi.mocked(fetchSandboxConfig).mockResolvedValue({ auto_approve: false } as never)
    renderSectionWithShell()
    await waitFor(() => expect(fetchAppState).toHaveBeenCalled())

    const toggle = await screen.findByTestId('auto-approve-global-switch')
    await waitFor(() => expect(toggle).toHaveAttribute('aria-checked', 'false'))
    fireEvent.click(toggle)

    // FR-010 (spec :1019-1021): a confirmation opens only when the person
    // SAVES a change to Auto-approve. The retired "Turn Auto-approve on?"
    // explainer — with its inline summary, D-02 heading pointer and
    // no-sandbox caveat — is gone with the instant-save mode; neither it nor
    // any Save-time dialog may appear on a bare toggle.
    expect(screen.queryByText('Save these settings?')).not.toBeInTheDocument()
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()

    // Spec :554 (ADR-092): "Auto-approve keeps its password; the help text
    // says each chat can still turn it off with its Auto button." The card's
    // help text, not a dialog, carries the per-chat explanation now.
    const help = screen.getByText(/auto button/i)
    expect(help).toHaveTextContent(/chat/i)
  })

  it('a refused save is the FR-015/FR-020 outcome — bar counts it, server message under the row, draft kept — not a toast', async () => {
    const SERVER_REFUSAL = 'Auto-approve could not be saved: the sandbox write was refused.'
    vi.mocked(fetchSandboxConfig).mockResolvedValue({ auto_approve: false } as never)
    vi.mocked(updateSandboxConfig).mockRejectedValue(
      new ApiError(400, SERVER_REFUSAL, { body: JSON.stringify({ error: SERVER_REFUSAL }) }),
    )
    await draftAndOpenBar()

    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    // FR-032: the platform confirmation still precedes the write.
    expect(await screen.findByText('Save these settings?')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))

    // The 400 was the answer to THIS save's write (D-8 row 3: one PUT).
    await waitFor(() => {
      expect(updateSandboxConfig).toHaveBeenCalledTimes(1)
    })
    expect(vi.mocked(updateSandboxConfig).mock.calls[0][0]).toEqual({ auto_approve: true })

    // FR-015: the bar says the change could not be saved. The old oracle —
    // exactly one error toast from the instant-save mutation — is retired
    // with the autosave path it belonged to (FR-010 ruling).
    expect(await screen.findByText('1 change could not be saved')).toBeInTheDocument()
    // FR-020 outcome (3): the server's message shows under the row.
    const alert = screen.getAllByRole('alert').find((el) => el.textContent?.includes(SERVER_REFUSAL))
    expect(alert, 'FR-020: the server refusal must show under the Auto-approve row').toBeDefined()
    // FR-020 outcome (3): the draft is kept — the switch still shows on.
    expect(screen.getByTestId('auto-approve-global-switch')).toHaveAttribute('aria-checked', 'true')
    // Never a saved signal after a refused write.
    expect(screen.queryAllByText(/^Saved\b/)).toEqual([])
  })
})

// ── US-B4: Score-as-control-surface ──────────────────────────────────────────

describe('SecuritySection — US-B4 score control surface', () => {
  it('score = 100 shows "protected" reassurance with no deduction list', async () => {
    vi.mocked(fetchDoctorResults).mockResolvedValue(DOCTOR_RESULT_PERFECT)

    renderSection()

    await waitFor(() => {
      expect(screen.getByTestId('all-clear-panel')).toBeInTheDocument()
    })

    expect(screen.getByTestId('all-clear-panel')).toHaveTextContent(/you're protected/i)
    // Reassurance message
    expect(screen.getByTestId('score-reassurance')).toHaveTextContent(/you're protected/i)
  })

  it('score deduction expands to show an action link to the in-place fix', async () => {
    vi.mocked(fetchDoctorResults).mockResolvedValue(DOCTOR_RESULT_ISSUES)

    renderSection()

    await waitFor(() => {
      expect(screen.getByTestId('issue-card-auth-none')).toBeInTheDocument()
    })

    // Expand the issue card
    fireEvent.click(screen.getByTestId('issue-card-auth-none'))

    await waitFor(() => {
      expect(screen.getByTestId('issue-action-link-auth-none')).toBeInTheDocument()
    })

    const actionLink = screen.getByTestId('issue-action-link-auth-none')
    expect(actionLink).toHaveAttribute('href', '/settings/gateway')
    expect(actionLink).toHaveTextContent(/go to gateway/i)
  })

  it('populated state shows reassurance with count of issues', async () => {
    vi.mocked(fetchDoctorResults).mockResolvedValue(DOCTOR_RESULT_ISSUES)

    renderSection()

    await waitFor(() => {
      expect(screen.getByTestId('score-reassurance')).toBeInTheDocument()
    })

    expect(screen.getByTestId('score-reassurance')).toHaveTextContent(/1 thing could be stronger/i)
  })

  it('Credential Vault shows encryption reassurance line', async () => {
    renderKeysSection()

    await waitFor(() => {
      expect(screen.getByText(/encrypted and stored only on this server/i)).toBeInTheDocument()
    })
  })
})

// ── Credential vault confirmations (ADR-0008 ruling 6, spec FR-OB-040/041) ───
//
// Each of the three vault operations gets its OWN confirmation: deleting and
// re-keying the vault are among the highest-blast-radius operations in the
// product, and "the vault is one control" is a UI grouping, not a licence to
// confirm once.

describe('SecuritySection — credential vault confirmations', () => {
  async function openAddCredentialForm() {
    renderKeysSection()
    fireEvent.click(await screen.findByText('Add key'))
    fireEvent.change(await screen.findByPlaceholderText('e.g. OPENAI_API_KEY'), {
      target: { value: 'MY_KEY' },
    })
    fireEvent.change(screen.getByPlaceholderText('sk-...'), { target: { value: 'sk-secret' } })
    fireEvent.click(screen.getByTestId('add-cred-save'))
  }

  it('set: the confirmation has exactly Cancel and one confirm, and no input', async () => {
    await openAddCredentialForm()

    const dialog = await screen.findByRole('alertdialog')
    expect(dialog).toHaveTextContent('Store this credential?')
    expect(within(dialog).getByRole('button', { name: 'Cancel' })).toHaveTextContent('Cancel')
    expect(within(dialog).getByRole('button', { name: 'Store credential' })).toHaveTextContent('Store credential')
    expect(within(dialog).getAllByRole('button')).toHaveLength(2) // Cancel, confirm
    // No input of any kind — this is a decision, not a credential prompt.
    expect(dialog.querySelectorAll('input, textarea, select')).toHaveLength(0)
    // FR-OB-042: never the destructive variant.
    expect(within(dialog).getByRole('button', { name: 'Store credential' }).className).not.toMatch(/color-error/)
    expect(addCredential).not.toHaveBeenCalled()
  })

  it('set: confirming performs the save', async () => {
    vi.mocked(addCredential).mockResolvedValue(undefined as never)

    await openAddCredentialForm()
    fireEvent.click(await screen.findByRole('button', { name: 'Store credential' }))

    await waitFor(() => {
      expect(vi.mocked(addCredential).mock.calls).toEqual([['MY_KEY', 'sk-secret', undefined]])
    })
  })

  it('set: cancelling performs nothing', async () => {
    await openAddCredentialForm()
    fireEvent.click(await screen.findByRole('button', { name: 'Cancel' }))

    await waitFor(() => {
      expect(screen.queryByRole('alertdialog')).toBeNull()
    })
    expect(addCredential).not.toHaveBeenCalled()
  })

  async function openDeleteConfirmation() {
    vi.mocked(fetchCredentials).mockResolvedValue([{ key: 'MY_KEY' }] as never)
    renderKeysSection()
    fireEvent.click(await screen.findByTestId('delete-cred-MY_KEY'))
  }

  it('delete: the confirmation has exactly Cancel and one confirm, and no input', async () => {
    await openDeleteConfirmation()

    // Wireframe round 4 (app.js "delete-key" dialog): title "Delete this
    // key?", ok "Delete key", destructive: true — FR-056 keeps exactly one
    // confirm before the delete. The old "Remove this credential?" /
    // non-destructive styling is replaced by the wireframe.
    const dialog = await screen.findByRole('alertdialog')
    expect(dialog).toHaveTextContent('Delete this key?')
    expect(within(dialog).getByRole('button', { name: 'Cancel' })).toHaveTextContent('Cancel')
    expect(within(dialog).getByRole('button', { name: 'Delete key' })).toHaveTextContent('Delete key')
    expect(within(dialog).getAllByRole('button')).toHaveLength(2)
    expect(dialog.querySelectorAll('input, textarea, select')).toHaveLength(0)
    expect(within(dialog).getByRole('button', { name: 'Delete key' }).className).toMatch(/color-error/)
    expect(deleteCredential).not.toHaveBeenCalled()
  })

  it('delete: confirming performs the save', async () => {
    vi.mocked(deleteCredential).mockResolvedValue(undefined as never)

    await openDeleteConfirmation()
    // Wireframe ok button "Delete key" (app.js delete-key dialog, FR-056).
    fireEvent.click(await screen.findByRole('button', { name: 'Delete key' }))

    await waitFor(() => {
      expect(vi.mocked(deleteCredential).mock.calls).toEqual([['MY_KEY', undefined]])
    })
  })

  it('delete: cancelling performs nothing', async () => {
    await openDeleteConfirmation()
    fireEvent.click(await screen.findByRole('button', { name: 'Cancel' }))

    await waitFor(() => {
      expect(screen.queryByRole('alertdialog')).toBeNull()
    })
    expect(deleteCredential).not.toHaveBeenCalled()
  })

  async function openRotateConfirmation() {
    renderKeysSection()
    fireEvent.click(await screen.findByTestId('rotate-master-key'))
    fireEvent.change(await screen.findByTestId('rotate-passphrase-input'), {
      target: { value: 'new-pass-phrase' },
    })
    fireEvent.click(screen.getByTestId('rotate-confirm'))
  }

  it('rotate: the confirmation has exactly Cancel and one confirm, and no input', async () => {
    await openRotateConfirmation()

    const dialog = await screen.findByRole('alertdialog')
    expect(dialog).toHaveTextContent('Rotate the master key?')
    expect(within(dialog).getByRole('button', { name: 'Cancel' })).toHaveTextContent('Cancel')
    expect(within(dialog).getByRole('button', { name: 'Rotate master key' })).toHaveTextContent('Rotate master key')
    expect(within(dialog).getAllByRole('button')).toHaveLength(2)
    expect(dialog.querySelectorAll('input, textarea, select')).toHaveLength(0)
    expect(within(dialog).getByRole('button', { name: 'Rotate master key' }).className).not.toMatch(/color-error/)
    expect(rotateCredentials).not.toHaveBeenCalled()
  })

  it('rotate: confirming performs the save', async () => {
    vi.mocked(rotateCredentials).mockResolvedValue(undefined as never)

    await openRotateConfirmation()
    fireEvent.click(await screen.findByRole('button', { name: 'Rotate master key' }))

    await waitFor(() => {
      expect(vi.mocked(rotateCredentials).mock.calls).toEqual([['new-pass-phrase', undefined]])
    })
  })

  it('rotate: cancelling performs nothing', async () => {
    await openRotateConfirmation()
    fireEvent.click(await screen.findByRole('button', { name: 'Cancel' }))

    await waitFor(() => {
      expect(screen.queryByRole('alertdialog')).toBeNull()
    })
    expect(rotateCredentials).not.toHaveBeenCalled()
  })
})

// D3 (UAT v0.1.1 defects) — hydration must never trigger a spurious PUT.
//
// Root cause: the security fields start at hardcoded useState defaults ('').
// Before this fix, useAutoSave's `disabled` option was `!config` — but
// `config` turns truthy in the SAME commit the hydration effect is SCHEDULED,
// one render before the effect's own setState calls actually land. So
// `disabled` flipped false one render too early, useAutoSave captured the
// hardcoded '' default as its baseline, and the LATER commit where the real
// persisted value hydrates looked like a genuine edit — firing a spurious
// save that echoes the fetched value straight back. This does NOT cover
// `GlobalToolPoliciesSection`'s own separate useAutoSave (already gated
// correctly via `isDraftReady` before this fix — confirmed safe, untouched).
//
// #1882 (chief ruling Q1 A, 2026-11-18): the exec-timeout field this probe
// used no longer exists, and the per-agent rate limits are read from
// GET /api/v1/security/rate-limits. The hydration probe is now a distinctive
// pair of values from that route; hydration must send neither a rate-limit
// PUT nor an updateConfig call.
describe('SecuritySection — D3: hydration must not trigger a spurious PUT', () => {
  it('loading rate limits that differ from the hardcoded "" default never saves, even after the debounce window elapses (REVERT-PROOF: fails without the securityHydrated gate)', async () => {
    const HYDRATED_LLM = 37
    const HYDRATED_TOOL = 53
    vi.mocked(fetchRateLimits).mockResolvedValue({
      enabled: true,
      max_agent_llm_calls_per_hour: HYDRATED_LLM,
      max_agent_tool_calls_per_minute: HYDRATED_TOOL,
    } as never)
    renderSection()

    // Wait for the component to settle (config fetched, section rendered).
    await waitFor(() => {
      expect(screen.getByTestId('security-health-header')).toBeInTheDocument()
    })
    // The numeric rate-limit fields render in the Protection view itself — the
    // collapsed Advanced disclosure that used to hold them is gone (C-22 row 6)
    // — so the hydrated values reach the DOM without an expansion step.
    await waitFor(() => {
      expect(screen.getByDisplayValue(String(HYDRATED_LLM))).toBeInTheDocument()
      expect(screen.getByDisplayValue(String(HYDRATED_TOOL))).toBeInTheDocument()
    })

    // PASSIVE idle wait — no interaction at all — comfortably past the
    // 500ms default debounce.
    await new Promise((resolve) => setTimeout(resolve, 900))
    expect(updateRateLimits, 'hydration must not PUT the rate-limit route').not.toHaveBeenCalled()
    expect(updateConfig, 'hydration must not call updateConfig').not.toHaveBeenCalled()
  })
})

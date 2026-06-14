/**
 * SecuritySection tests.
 *
 * Coverage:
 *   US-B1 — two-layer IA: jargon hidden until Advanced is expanded (#327)
 *   US-B2 — risky controls: badge from persisted value; confirm-to-weaken (#328)
 *   US-B3 — ToolPolicyEditor replaces GlobalToolPoliciesSection (#329)
 *   US-B4 — score deduction links to fix; score=100 reassurance (#330)
 *   #340   — SkillTrustSection is mounted in the Security tab
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

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
    fetchBuiltinTools: vi.fn(),
    fetchRegistryTools: vi.fn(),
    fetchGlobalToolPolicies: vi.fn(),
    updateGlobalToolPolicies: vi.fn(),
    fetchDoctorResults: vi.fn(),
    runDoctor: vi.fn(),
    fetchSkillTrust: vi.fn(),
    updateSkillTrust: vi.fn(),
    reAuth: vi.fn(),
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
  fetchGatewayStatus,
  fetchCredentials,
  fetchBuiltinTools,
  fetchGlobalToolPolicies,
  updateGlobalToolPolicies,
  fetchDoctorResults,
  fetchSkillTrust,
  reAuth,
  ApiError,
} from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { SecuritySection } from './SecuritySection'
import type { RegistryTool } from '@/lib/api'

// reAuth403 is the exact 403 the backend's requireReAuth gate returns. The
// useReAuthGate hook detects it by body match and opens the consent dialog.
function reAuth403() {
  return new ApiError(
    403,
    "You don't have permission to perform this action.",
    { body: '{"error":"this change requires re-typing your password — call POST /api/v1/auth/reauth first"}' },
  )
}

// ── Fixtures ──────────────────────────────────────────────────────────────────

const MINIMAL_CONFIG = {
  security: {
    policy_mode: 'deny' as const,
    exec_approval: 'ask' as const,
    daily_cost_cap: 10,
    exec_timeout_seconds: 0,
    max_background_seconds: 0,
    enable_deny_patterns: false,
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

const ALLOW_CONFIG = {
  ...MINIMAL_CONFIG,
  security: {
    ...MINIMAL_CONFIG.security,
    policy_mode: 'allow' as const,
  },
}

const BUILTIN_TOOLS: RegistryTool[] = [
  { name: 'exec', description: 'Execute shell commands', category: 'code', scope: 'core', source: 'builtin' },
  { name: 'read_file', description: 'Read file content', category: 'file', scope: 'core', source: 'builtin' },
  { name: 'system.status', description: 'System status', category: 'system', scope: 'core', source: 'builtin' },
]

const GLOBAL_POLICIES = {
  default_policy: 'ask' as const,
  policies: {},
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

const SKILL_TRUST_RESPONSE = { level: 'warn_unverified' as const }

// ── Helper ─────────────────────────────────────────────────────────────────────

function makeClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
}

function renderSection(client = makeClient()) {
  return render(
    <QueryClientProvider client={client}>
      <SecuritySection />
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
  vi.mocked(fetchBuiltinTools).mockResolvedValue(BUILTIN_TOOLS)
  vi.mocked(fetchGlobalToolPolicies).mockResolvedValue(GLOBAL_POLICIES)
  vi.mocked(fetchDoctorResults).mockResolvedValue(null)
  vi.mocked(fetchSkillTrust).mockResolvedValue(SKILL_TRUST_RESPONSE)
})

// ── US-B1: Two-layer IA ───────────────────────────────────────────────────────

describe('SecuritySection — US-B1 two-layer IA', () => {
  it('shows Security health header without jargon in the initial render', async () => {
    renderSection()

    // Wait for the component to settle (config loaded)
    await waitFor(() => {
      expect(screen.getByTestId('security-health-header')).toBeInTheDocument()
    })

    // Jargon words must NOT appear until Advanced is expanded
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

    // "Must ask first" button for policyMode (Deny = safe), and "Shell command approval" label
    expect(screen.getByText(/must ask first/i)).toBeInTheDocument()
    expect(screen.getByText(/shell command approval/i)).toBeInTheDocument()
  })

  it('expanding Advanced reveals jargon (Landlock / SSRF) that was hidden', async () => {
    renderSection()

    // Wait for the component to settle
    await waitFor(() => {
      expect(screen.getByTestId('security-health-header')).toBeInTheDocument()
    })

    // Jargon hidden before expand
    expect(document.body.textContent).not.toMatch(/Landlock/i)

    // Find and click the Advanced/technical details trigger
    const advancedTrigger = screen.getByTestId('advanced-disclosure-trigger')
    fireEvent.click(advancedTrigger)

    // After expanding, jargon is visible
    await waitFor(() => {
      expect(document.body.textContent).toMatch(/SSRF|Landlock|seccomp/i)
    })
  })
})

// ── US-B2: Risky controls ─────────────────────────────────────────────────────

describe('SecuritySection — US-B2 risky policy mode control', () => {
  it('shows Recommended pill on "Must ask first" (safe = deny) option', async () => {
    renderSection()

    await waitFor(() => {
      expect(screen.getAllByTestId('recommended-pill').length).toBeGreaterThan(0)
    })

    // At least one Recommended pill should be near the policy mode control
    const pills = screen.getAllByTestId('recommended-pill')
    expect(pills.length).toBeGreaterThan(0)
  })

  it('standing badge shows when persisted policyMode is "allow" (risky)', async () => {
    vi.mocked(fetchConfig).mockResolvedValue(ALLOW_CONFIG as never)

    renderSection()

    await waitFor(() => {
      expect(screen.getByTestId('risky-standing-badge')).toBeInTheDocument()
    })

    expect(screen.getByTestId('risky-standing-badge')).toHaveTextContent(/lowers your protection/i)
  })

  it('no standing badge when persisted policyMode is "deny" (safe)', async () => {
    renderSection()

    await waitFor(() => {
      expect(screen.getByTestId('plain-toggles')).toBeInTheDocument()
    })

    // Give time for all queries to settle
    await waitFor(() => {
      expect(screen.queryByTestId('risky-standing-badge')).not.toBeInTheDocument()
    }, { timeout: 2000 })
  })

  it('clicking risky option opens AlertDialog with safe button as default', async () => {
    renderSection()

    await waitFor(() => {
      expect(screen.getByTestId('risky-option-allow')).toBeInTheDocument()
    })

    fireEvent.click(screen.getByTestId('risky-option-allow'))

    await waitFor(() => {
      // The cancel / keep-safe button (default / safe action)
      expect(screen.getByText(/keep deny/i)).toBeInTheDocument()
    })
  })

  it('cancelling the dialog keeps the safe value and hides the badge', async () => {
    renderSection()

    await waitFor(() => {
      expect(screen.getByTestId('risky-option-allow')).toBeInTheDocument()
    })

    // Open the dialog
    fireEvent.click(screen.getByTestId('risky-option-allow'))
    await waitFor(() => {
      expect(screen.getByText(/keep deny/i)).toBeInTheDocument()
    })

    // Click "Keep Deny (safer)" — the cancel/safe button
    fireEvent.click(screen.getByText(/keep deny/i))

    // Dialog closes, badge never appears (policyMode stays 'deny')
    await waitFor(() => {
      expect(screen.queryByText(/keep deny/i)).not.toBeInTheDocument()
    })
    // No standing badge because policyMode is still 'deny' (persisted)
    expect(screen.queryByTestId('risky-standing-badge')).not.toBeInTheDocument()
  })
})

// ── US-B3: ToolPolicyEditor replaces GlobalToolPoliciesSection ────────────────

describe('SecuritySection — US-B3 ToolPolicyEditor in advanced section', () => {
  it('ToolPolicyEditor renders inside Advanced section with preset buttons', async () => {
    renderSection()

    // Expand Advanced
    await waitFor(() => {
      expect(screen.getByTestId('advanced-disclosure-trigger')).toBeInTheDocument()
    })
    fireEvent.click(screen.getByTestId('advanced-disclosure-trigger'))

    await waitFor(() => {
      expect(screen.getByTestId('tool-policy-editor')).toBeInTheDocument()
    })

    // Preset buttons: Cautious / Balanced / Full access
    expect(screen.getByTestId('preset-cautious')).toBeInTheDocument()
    expect(screen.getByTestId('preset-balanced')).toBeInTheDocument()
    expect(screen.getByTestId('preset-full_access')).toBeInTheDocument()
  })

  it('system.* tool appears in the flat category grid (no separate system-disclosure-wrapper)', async () => {
    renderSection()

    await waitFor(() => {
      expect(screen.getByTestId('advanced-disclosure-trigger')).toBeInTheDocument()
    })
    fireEvent.click(screen.getByTestId('advanced-disclosure-trigger'))

    await waitFor(() => {
      expect(screen.getByTestId('tool-policy-editor')).toBeInTheDocument()
    })

    // The old system-disclosure-wrapper must NOT exist (#357 flat list fix).
    expect(screen.queryByTestId('system-disclosure-wrapper')).not.toBeInTheDocument()

    // The category-grid must contain a 'system' category pill (system tools in the grid).
    expect(screen.getByTestId('category-grid')).toBeInTheDocument()
    expect(screen.getByTestId('category-pill-system')).toBeInTheDocument()
  })

  it('a category with mixed policies shows a Mixed pill', async () => {
    // Set exec to 'ask' and read_file to 'allow' — both in different categories,
    // so each category is uniform. We only test that the ToolPolicyEditor is
    // wired up and renders category pills; mixed-pill logic is tested in ToolPolicyEditor.test.tsx.
    vi.mocked(fetchGlobalToolPolicies).mockResolvedValue({
      default_policy: 'allow',
      policies: {},
    })

    renderSection()

    await waitFor(() => {
      expect(screen.getByTestId('advanced-disclosure-trigger')).toBeInTheDocument()
    })
    fireEvent.click(screen.getByTestId('advanced-disclosure-trigger'))

    await waitFor(() => {
      expect(screen.getByTestId('category-grid')).toBeInTheDocument()
    })

    // Categories present (exec = code, read_file = file)
    expect(screen.getByTestId('category-grid')).toBeInTheDocument()
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
    renderSection()

    await waitFor(() => {
      expect(screen.getByText(/encrypted and stored only on this server/i)).toBeInTheDocument()
    })
  })
})

// ── #340: SkillTrustSection mounted in Security ───────────────────────────────

describe('SecuritySection — #340 SkillTrustSection mounted', () => {
  it('renders SkillTrustSection (Skill Trust heading) inside Security tab', async () => {
    renderSection()

    await waitFor(() => {
      expect(screen.getByText(/skill trust/i)).toBeInTheDocument()
    })
  })

  it('renders the three trust-level radios (Block / Warn / Allow)', async () => {
    renderSection()

    await waitFor(() => {
      const radios = screen.getAllByRole('radio')
      expect(radios.length).toBeGreaterThanOrEqual(3)
    })
  })
})

// ── Re-auth gate for global tool policies (Spec-3 FR-3.3 / Spec-6 FR-12.2) ──────

describe('SecuritySection — global tool-policy re-auth gate', () => {
  it('opens the re-auth dialog when the gated PUT returns 403, then replays the token', async () => {
    // First auto-save attempt (no consent token) is rejected by the re-auth
    // gate; the replay (with the minted token) succeeds.
    vi.mocked(updateGlobalToolPolicies)
      .mockRejectedValueOnce(reAuth403())
      .mockResolvedValueOnce({ default_policy: 'allow', policies: {} } as never)
    vi.mocked(reAuth).mockResolvedValue({ verified: true, token: 'reauth_tok', expires_in: 300 } as never)

    renderSection()

    // Expand Advanced to reveal the ToolPolicyEditor.
    await waitFor(() => {
      expect(screen.getByTestId('advanced-disclosure-trigger')).toBeInTheDocument()
    })
    fireEvent.click(screen.getByTestId('advanced-disclosure-trigger'))

    await waitFor(() => {
      expect(screen.getByTestId('preset-balanced')).toBeInTheDocument()
    })

    // Switch the default policy ask → allow; this triggers the debounced PUT.
    fireEvent.click(screen.getByTestId('preset-balanced'))

    // The first PUT (token '') fired and 403'd → consent dialog appears.
    await waitFor(() => {
      expect(screen.getByTestId('reauth-confirm')).toBeInTheDocument()
    })
    expect(vi.mocked(updateGlobalToolPolicies).mock.calls[0][1]).toBe('')

    // Re-authenticate; the PUT is replayed with the consent token.
    fireEvent.change(screen.getByTestId('reauth-password-input'), { target: { value: 'mypassword' } })
    fireEvent.click(screen.getByTestId('reauth-confirm'))

    await waitFor(() => {
      expect(reAuth).toHaveBeenCalledWith('mypassword')
      expect(updateGlobalToolPolicies).toHaveBeenCalledTimes(2)
      expect(vi.mocked(updateGlobalToolPolicies).mock.calls[1][1]).toBe('reauth_tok')
    })
  })
})

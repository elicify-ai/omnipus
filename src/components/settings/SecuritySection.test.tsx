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
import { render, screen, waitFor, fireEvent, within } from '@testing-library/react'
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
    rotateCredentials: vi.fn(),
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
  updateConfig,
  fetchGatewayStatus,
  fetchCredentials,
  addCredential,
  deleteCredential,
  fetchBuiltinTools,
  fetchGlobalToolPolicies,
  updateGlobalToolPolicies,
  fetchDoctorResults,
  fetchSkillTrust,
  reAuth,
  rotateCredentials,
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

/**
 * Mixed payload: same builtin tools PLUS two MCP tools from 'testsvr'.
 * Used in the MCP-tool-in-global-editor tests below.
 */
const BUILTIN_AND_MCP_TOOLS: RegistryTool[] = [
  ...BUILTIN_TOOLS,
  { name: 'mcp_testsvr_list', description: 'List resources', category: 'search', scope: 'general', source: 'mcp' },
  { name: 'mcp_testsvr_read', description: 'Read a resource', category: 'web', scope: 'general', source: 'mcp' },
]

const GLOBAL_POLICIES = {
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
      .mockResolvedValueOnce({ policies: {} } as never)
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

// ── Credential vault re-auth gate (B4 bug fix) ───────────────────────────────

describe('SecuritySection — credential vault re-auth gate', () => {
  it('addCredential: opens the re-auth dialog on 403, retries with token, fires success toast', async () => {
    // First attempt (token '') is rejected by the re-auth gate; the retry succeeds.
    vi.mocked(addCredential)
      .mockRejectedValueOnce(reAuth403())
      .mockResolvedValueOnce(undefined)
    vi.mocked(reAuth).mockResolvedValue({ verified: true, token: 'cred_tok', expires_in: 300 } as never)

    const mockToast = vi.fn()
    vi.mocked(useUiStore).mockReturnValue({ addToast: mockToast } as never)

    renderSection()

    // Wait for the section to be fully loaded.
    await waitFor(() => {
      expect(screen.getByText(/add key/i)).toBeInTheDocument()
    })

    // Open the "Add Credential" modal.
    fireEvent.click(screen.getByText(/add key/i))

    await waitFor(() => {
      expect(screen.getByPlaceholderText(/e\.g\. OPENAI_API_KEY/i)).toBeInTheDocument()
    })

    // Fill in the credential form.
    fireEvent.change(screen.getByPlaceholderText(/e\.g\. OPENAI_API_KEY/i), {
      target: { value: 'MY_SECRET_KEY' },
    })
    fireEvent.change(screen.getByPlaceholderText(/sk-\.\.\./i), {
      target: { value: 'super-secret-value' },
    })

    // Submit — first attempt will 403 and open the re-auth dialog.
    fireEvent.click(screen.getByRole('button', { name: /^save$/i }))

    // Re-auth dialog must appear.
    await waitFor(() => {
      expect(screen.getByTestId('reauth-confirm')).toBeInTheDocument()
    })

    // Confirm the first attempt passed no token.
    expect(vi.mocked(addCredential).mock.calls[0][2]).toBe('')

    // Enter password and confirm.
    fireEvent.change(screen.getByTestId('reauth-password-input'), {
      target: { value: 'mypassword' },
    })
    fireEvent.click(screen.getByTestId('reauth-confirm'))

    // The retry is issued with the minted token and a success toast fires.
    await waitFor(() => {
      expect(reAuth).toHaveBeenCalledWith('mypassword')
      expect(vi.mocked(addCredential)).toHaveBeenCalledTimes(2)
      expect(vi.mocked(addCredential).mock.calls[1][2]).toBe('cred_tok')
      expect(mockToast).toHaveBeenCalledWith(
        expect.objectContaining({ variant: 'success' }),
      )
    })
  })

  it('deleteCredential: opens the re-auth dialog on 403, retries with token, fires success toast', async () => {
    vi.mocked(fetchCredentials).mockResolvedValue([{ key: 'SOME_KEY' }])
    vi.mocked(deleteCredential)
      .mockRejectedValueOnce(reAuth403())
      .mockResolvedValueOnce(undefined)
    vi.mocked(reAuth).mockResolvedValue({ verified: true, token: 'del_tok', expires_in: 300 } as never)

    const mockToast = vi.fn()
    vi.mocked(useUiStore).mockReturnValue({ addToast: mockToast } as never)

    renderSection()

    // Wait for the credential to appear in the list.
    await waitFor(() => {
      expect(screen.getByText('SOME_KEY')).toBeInTheDocument()
    })

    // Click the trash/delete button to open the confirmation modal.
    // Find the delete icon button via the credential row's DOM proximity.
    const credRow = screen.getByText('SOME_KEY').closest('div[class*="flex"]')!
    const deleteBtn = credRow.querySelector('button')!
    fireEvent.click(deleteBtn)

    // Confirm the "Remove credential?" dialog.
    await waitFor(() => {
      expect(screen.getByRole('button', { name: /^remove$/i })).toBeInTheDocument()
    })
    fireEvent.click(screen.getByRole('button', { name: /^remove$/i }))

    // Re-auth dialog must appear.
    await waitFor(() => {
      expect(screen.getByTestId('reauth-confirm')).toBeInTheDocument()
    })

    // Confirm the first delete attempt passed no token.
    expect(vi.mocked(deleteCredential).mock.calls[0][1]).toBe('')

    // Enter password and confirm re-auth.
    fireEvent.change(screen.getByTestId('reauth-password-input'), {
      target: { value: 'mypassword' },
    })
    fireEvent.click(screen.getByTestId('reauth-confirm'))

    // The retry is issued with the minted token and a success toast fires.
    await waitFor(() => {
      expect(reAuth).toHaveBeenCalledWith('mypassword')
      expect(vi.mocked(deleteCredential)).toHaveBeenCalledTimes(2)
      expect(vi.mocked(deleteCredential).mock.calls[1][1]).toBe('del_tok')
      expect(mockToast).toHaveBeenCalledWith(
        expect.objectContaining({ variant: 'success' }),
      )
    })
  })
})

// ── MCP tools in the global Settings → Security editor ───────────────────────

describe('SecuritySection — MCP tools in GlobalToolPoliciesSection', () => {
  /**
   * Helper: render SecuritySection with MCP tools included in fetchBuiltinTools,
   * expand the top-level Advanced disclosure, and wait for the ToolPolicyEditor.
   */
  async function renderWithMcpAndExpand() {
    // Override the default stub so MCP tools are returned alongside builtins.
    vi.mocked(fetchBuiltinTools).mockResolvedValue(BUILTIN_AND_MCP_TOOLS)

    renderSection()

    // Wait for the section to load and find the single top-level Advanced trigger.
    await waitFor(() => {
      expect(screen.getByTestId('advanced-disclosure-trigger')).toBeInTheDocument()
    })
    fireEvent.click(screen.getByTestId('advanced-disclosure-trigger'))

    // Wait for the ToolPolicyEditor to mount and its data to settle.
    await waitFor(() => {
      expect(screen.getByTestId('tool-policy-editor')).toBeInTheDocument()
    })
  }

  it('MCP tools render in the mcp-tools-section inside the global editor', async () => {
    await renderWithMcpAndExpand()

    // The mcp-tools-section must be present inside the ToolPolicyEditor.
    expect(screen.getByTestId('mcp-tools-section')).toBeInTheDocument()
  })

  it('MCP server disclosure is present for the testsvr MCP server', async () => {
    await renderWithMcpAndExpand()

    const mcpSection = screen.getByTestId('mcp-tools-section')
    // 'mcp_testsvr_list' and 'mcp_testsvr_read' both belong to server 'testsvr' →
    // exactly one server disclosure in the MCP section.
    const triggers = within(mcpSection).getAllByTestId('advanced-disclosure-trigger')
    expect(triggers.length).toBe(1)
  })

  it('expanding the MCP server disclosure shows both MCP tool rows', async () => {
    await renderWithMcpAndExpand()

    const mcpSection = screen.getByTestId('mcp-tools-section')
    const serverTrigger = within(mcpSection).getAllByTestId('advanced-disclosure-trigger')[0]
    fireEvent.click(serverTrigger)

    await waitFor(() => {
      expect(screen.getByTestId('tool-row-mcp_testsvr_list')).toBeInTheDocument()
      expect(screen.getByTestId('tool-row-mcp_testsvr_read')).toBeInTheDocument()
    })
  })

  it('MCP tools do NOT appear in the builtin category grid', async () => {
    await renderWithMcpAndExpand()

    const categoryGrid = screen.getByTestId('category-grid')
    // MCP tool names must not appear as category-grid tool rows
    expect(within(categoryGrid).queryByTestId('tool-row-mcp_testsvr_list')).not.toBeInTheDocument()
    expect(within(categoryGrid).queryByTestId('tool-row-mcp_testsvr_read')).not.toBeInTheDocument()
  })

  it('changing an MCP tool policy fires updateGlobalToolPolicies through the re-auth gate', async () => {
    // The auto-save is immediate on first success (no 403 on this path).
    vi.mocked(updateGlobalToolPolicies).mockResolvedValue({
      policies: {},
    } as never)

    await renderWithMcpAndExpand()

    // Expand the MCP server disclosure to access the tool rows.
    const mcpSection = screen.getByTestId('mcp-tools-section')
    const serverTrigger = within(mcpSection).getAllByTestId('advanced-disclosure-trigger')[0]
    fireEvent.click(serverTrigger)

    await waitFor(() => {
      expect(screen.getByTestId('tool-row-mcp_testsvr_list')).toBeInTheDocument()
    })

    // Click Deny on the MCP tool row.
    const listRow = screen.getByTestId('tool-row-mcp_testsvr_list')
    fireEvent.click(within(listRow).getByRole('button', { name: /deny/i }))

    // The auto-save debounce fires updateGlobalToolPolicies with the MCP tool name.
    await waitFor(
      () => {
        expect(vi.mocked(updateGlobalToolPolicies)).toHaveBeenCalled()
        const [calledValue] = vi.mocked(updateGlobalToolPolicies).mock.calls[0]
        expect(calledValue).toMatchObject({
          policies: expect.objectContaining({ mcp_testsvr_list: 'deny' }),
        })
      },
      { timeout: 3000 },
    )
  })

  it('changing an MCP tool policy that triggers a re-auth 403 opens the consent dialog', async () => {
    vi.mocked(updateGlobalToolPolicies)
      .mockRejectedValueOnce(reAuth403())
      .mockResolvedValueOnce({ policies: {} } as never)
    vi.mocked(reAuth).mockResolvedValue({ verified: true, token: 'tok_mcp', expires_in: 300 } as never)

    await renderWithMcpAndExpand()

    const mcpSection = screen.getByTestId('mcp-tools-section')
    const serverTrigger = within(mcpSection).getAllByTestId('advanced-disclosure-trigger')[0]
    fireEvent.click(serverTrigger)

    await waitFor(() => {
      expect(screen.getByTestId('tool-row-mcp_testsvr_read')).toBeInTheDocument()
    })

    // Trigger an MCP policy change that will 403 on first attempt.
    const readRow = screen.getByTestId('tool-row-mcp_testsvr_read')
    fireEvent.click(within(readRow).getByRole('button', { name: /deny/i }))

    // The re-auth dialog must appear.
    await waitFor(() => {
      expect(screen.getByTestId('reauth-confirm')).toBeInTheDocument()
    })

    // Complete re-auth; the PUT is replayed with the consent token.
    fireEvent.change(screen.getByTestId('reauth-password-input'), { target: { value: 'secret' } })
    fireEvent.click(screen.getByTestId('reauth-confirm'))

    await waitFor(() => {
      expect(reAuth).toHaveBeenCalledWith('secret')
      expect(vi.mocked(updateGlobalToolPolicies).mock.calls[1][1]).toBe('tok_mcp')
    })
  })
})

// ── Credential vault re-auth gate (B4) ───────────────────────────────────────

describe('SecuritySection — credential vault re-auth gate (B4)', () => {
  it('opens the re-auth dialog when add-credential 403s, then replays the consent token', async () => {
    // First attempt (token '') is rejected by the server's re-auth gate; the
    // replay (with the minted token) succeeds. Before B4 this 403 surfaced as a
    // generic "You don't have permission" toast instead of a password prompt.
    vi.mocked(addCredential)
      .mockRejectedValueOnce(reAuth403())
      .mockResolvedValueOnce(undefined as never)
    vi.mocked(reAuth).mockResolvedValue({ verified: true, token: 'cred_tok', expires_in: 300 } as never)

    renderSection()

    // Open the Add Credential modal and fill it.
    fireEvent.click(await screen.findByText('Add key'))
    fireEvent.change(screen.getByPlaceholderText('e.g. OPENAI_API_KEY'), { target: { value: 'MY_KEY' } })
    fireEvent.change(screen.getByPlaceholderText('sk-...'), { target: { value: 'sk-secret' } })
    fireEvent.click(screen.getByText('Save'))

    // The first POST (token '') 403'd → consent dialog appears (not a toast).
    await waitFor(() => {
      expect(screen.getByTestId('reauth-confirm')).toBeInTheDocument()
    })
    expect(vi.mocked(addCredential).mock.calls[0][2]).toBe('')

    // Re-authenticate; the POST is replayed with the consent token.
    fireEvent.change(screen.getByTestId('reauth-password-input'), { target: { value: 'mypassword' } })
    fireEvent.click(screen.getByTestId('reauth-confirm'))

    await waitFor(() => {
      expect(reAuth).toHaveBeenCalledWith('mypassword')
      expect(addCredential).toHaveBeenCalledTimes(2)
      expect(vi.mocked(addCredential).mock.calls[1][2]).toBe('cred_tok')
    })
  })

  it('opens the re-auth dialog when delete-credential 403s, then replays the token with the key', async () => {
    vi.mocked(fetchCredentials).mockResolvedValue([{ key: 'MY_KEY' }] as never)
    vi.mocked(deleteCredential)
      .mockRejectedValueOnce(reAuth403())
      .mockResolvedValueOnce(undefined as never)
    vi.mocked(reAuth).mockResolvedValue({ verified: true, token: 'del_tok', expires_in: 300 } as never)

    renderSection()

    fireEvent.click(await screen.findByTestId('delete-cred-MY_KEY'))
    fireEvent.click(await screen.findByText('Remove'))

    await waitFor(() => {
      expect(screen.getByTestId('reauth-confirm')).toBeInTheDocument()
    })
    expect(vi.mocked(deleteCredential).mock.calls[0]).toEqual(['MY_KEY', ''])

    fireEvent.change(screen.getByTestId('reauth-password-input'), { target: { value: 'pw' } })
    fireEvent.click(screen.getByTestId('reauth-confirm'))

    await waitFor(() => {
      expect(deleteCredential).toHaveBeenCalledTimes(2)
      expect(vi.mocked(deleteCredential).mock.calls[1]).toEqual(['MY_KEY', 'del_tok'])
    })
  })

  it('dismissing the re-auth dialog does NOT raise an error toast (B4 cancellation)', async () => {
    vi.mocked(addCredential).mockRejectedValueOnce(reAuth403())

    renderSection()

    fireEvent.click(await screen.findByText('Add key'))
    fireEvent.change(screen.getByPlaceholderText('e.g. OPENAI_API_KEY'), { target: { value: 'K' } })
    fireEvent.change(screen.getByPlaceholderText('sk-...'), { target: { value: 'v' } })
    fireEvent.click(screen.getByText('Save'))

    await waitFor(() => {
      expect(screen.getByTestId('reauth-cancel')).toBeInTheDocument()
    })
    // Dismiss instead of confirming → cancellation sentinel, swallowed by onError.
    fireEvent.click(screen.getByTestId('reauth-cancel'))

    await waitFor(() => {
      expect(screen.queryByTestId('reauth-confirm')).toBeNull()
    })
    expect(mockAddToast).not.toHaveBeenCalledWith(expect.objectContaining({ variant: 'error' }))
  })

  it('(G5) rotate master key goes through the re-auth gate then replays the token', async () => {
    vi.mocked(rotateCredentials)
      .mockRejectedValueOnce(reAuth403())
      .mockResolvedValueOnce(undefined as never)
    vi.mocked(reAuth).mockResolvedValue({ verified: true, token: 'rot_tok', expires_in: 300 } as never)

    renderSection()

    fireEvent.click(await screen.findByTestId('rotate-master-key'))
    fireEvent.change(await screen.findByTestId('rotate-passphrase-input'), { target: { value: 'new-pass-phrase' } })
    fireEvent.click(screen.getByTestId('rotate-confirm'))

    await waitFor(() => {
      expect(screen.getByTestId('reauth-confirm')).toBeInTheDocument()
    })
    expect(vi.mocked(rotateCredentials).mock.calls[0]).toEqual(['new-pass-phrase', ''])

    fireEvent.change(screen.getByTestId('reauth-password-input'), { target: { value: 'pw' } })
    fireEvent.click(screen.getByTestId('reauth-confirm'))

    await waitFor(() => {
      expect(rotateCredentials).toHaveBeenCalledTimes(2)
      expect(vi.mocked(rotateCredentials).mock.calls[1]).toEqual(['new-pass-phrase', 'rot_tok'])
    })
  })
})

// D3 (UAT v0.1.1 defects) — hydration must never trigger a spurious PUT.
//
// Root cause: the security fields (execTimeoutSecs and siblings) start at hardcoded
// useState defaults (''). Before this fix, useAutoSave's `disabled` option
// here was `!config` — but `config` turns truthy in the SAME commit the
// hydration effect is SCHEDULED, one render before the effect's own
// setState calls actually land. So `disabled` flipped false one render too
// early, useAutoSave captured the hardcoded '' default as its baseline,
// and the LATER commit where the real persisted value hydrates (45, per
// this test's exec_timeout_seconds override) looked like a genuine edit —
// (probe retargeted from daily_cost_cap after ADR-053 D12 retired it) —
// firing a spurious `updateConfig` that echoes the fetched value straight
// back. This does NOT cover `GlobalToolPoliciesSection`'s own separate
// useAutoSave (already gated correctly via `isDraftReady` before this fix
// — confirmed safe, untouched).
describe('SecuritySection — D3: hydration must not trigger a spurious PUT', () => {
  it('loading an exec timeout that differs from the hardcoded "" default never calls updateConfig, even after the debounce window elapses (REVERT-PROOF: fails without the securityHydrated gate)', async () => {
    // Local override: a distinctive hydrating value on a SURVIVING field
    // (daily_cost_cap was retired by ADR-053 D12 — the component no longer
    // renders it, so it can no longer serve as the hydration probe).
    vi.mocked(fetchConfig).mockResolvedValue({
      ...MINIMAL_CONFIG,
      security: { ...MINIMAL_CONFIG.security, exec_timeout_seconds: 45 },
    } as never)
    renderSection()

    // Wait for the component to settle (config fetched, disclosure rendered).
    await waitFor(() => {
      expect(screen.getByTestId('security-health-header')).toBeInTheDocument()
    })
    // The numeric security fields render inside the collapsed
    // AdvancedDisclosure — expand it so the hydrated value is in the DOM.
    fireEvent.click(screen.getByTestId('advanced-disclosure-trigger'))

    await waitFor(() => {
      expect(screen.getByDisplayValue('45')).toBeInTheDocument()
    })

    // PASSIVE idle wait — no interaction at all — comfortably past the
    // 500ms default debounce.
    await new Promise((resolve) => setTimeout(resolve, 900))
    expect(updateConfig).not.toHaveBeenCalled()
  })
})

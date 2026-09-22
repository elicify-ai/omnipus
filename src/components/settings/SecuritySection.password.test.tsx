/**
 * SecuritySection.password.test.tsx — Spec-6 FR-12.2 / ADR-022, ADR-0010 WP3.
 *
 * The password-mode counterpart to SecuritySection.test.tsx's credential-vault
 * confirm-mode coverage: with AppState.identity.mode = 'local', useStepUp()
 * picks 'password' and a credential-vault write goes through useStepUp's
 * dialog-first flow — ReAuthDialog (restored verbatim from the engine merge
 * base) opens before anything is sent, and a successful re-auth sends the
 * POST exactly once, carrying the minted consent token. Nothing is sent on
 * dismissal. Representative of all three vault operations this screen gates the
 * same way (set/delete/rotate) — this file exercises the set (add-credential)
 * flow.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

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
    fetchAppState: vi.fn(),
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

import {
  fetchConfig,
  fetchGatewayStatus,
  fetchCredentials,
  addCredential,
  fetchBuiltinTools,
  fetchGlobalToolPolicies,
  fetchDoctorResults,
  fetchSkillTrust,
  fetchAppState,
  reAuth,
} from '@/lib/api'
import type { AppState, RegistryTool } from '@/lib/api'
import { SecuritySection } from './SecuritySection'

// Local mode (identity.mode: 'local') pins useStepUp() to 'password'.
const LOCAL_APP_STATE = {
  onboarding_complete: true,
  identity: { mode: 'local', edition: 'core', signed_in: true },
} as AppState

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

const BUILTIN_TOOLS: RegistryTool[] = [
  { name: 'exec', description: 'Execute shell commands', category: 'code', scope: 'core', source: 'builtin' },
]

const GLOBAL_POLICIES = { policies: {} }
const SKILL_TRUST_RESPONSE = { level: 'warn_unverified' as const }

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

function renderSection() {
  return render(
    <QueryClientProvider client={makeClient()}>
      <SecuritySection />
    </QueryClientProvider>,
  )
}

async function openAddCredentialForm() {
  renderSection()
  fireEvent.click(await screen.findByText('Add key'))
  fireEvent.change(await screen.findByPlaceholderText('e.g. OPENAI_API_KEY'), {
    target: { value: 'MY_KEY' },
  })
  fireEvent.change(screen.getByPlaceholderText('sk-...'), { target: { value: 'sk-secret' } })
  fireEvent.click(screen.getByTestId('add-cred-save'))
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(fetchConfig).mockResolvedValue(MINIMAL_CONFIG as never)
  vi.mocked(fetchGatewayStatus).mockResolvedValue({ daily_cost: 0, uptime_seconds: 0 } as never)
  vi.mocked(fetchCredentials).mockResolvedValue([])
  vi.mocked(fetchBuiltinTools).mockResolvedValue(BUILTIN_TOOLS)
  vi.mocked(fetchGlobalToolPolicies).mockResolvedValue(GLOBAL_POLICIES)
  vi.mocked(fetchDoctorResults).mockResolvedValue(null)
  vi.mocked(fetchSkillTrust).mockResolvedValue(SKILL_TRUST_RESPONSE)
  vi.mocked(fetchAppState).mockResolvedValue(LOCAL_APP_STATE)
})

describe('SecuritySection — password mode (local edition)', () => {
  it('opens ReAuthDialog before the gated POST, then sends it once with the minted token', async () => {
    vi.mocked(addCredential)
      .mockResolvedValueOnce(undefined as never)
    vi.mocked(reAuth).mockResolvedValue({ verified: true, token: 'reauth_tok', expires_in: 300 } as never)

    await openAddCredentialForm()

    // Dialog first: ReAuthDialog opens before anything is sent.
    await waitFor(() => {
      expect(screen.getByTestId('reauth-password-input')).toBeInTheDocument()
    })
    expect(addCredential).not.toHaveBeenCalled()

    fireEvent.change(screen.getByTestId('reauth-password-input'), { target: { value: 'mypassword' } })
    fireEvent.click(screen.getByTestId('reauth-confirm'))

    await waitFor(() => {
      expect(reAuth).toHaveBeenCalledWith('mypassword')
      expect(addCredential).toHaveBeenCalledTimes(1)
      expect(vi.mocked(addCredential).mock.calls[0]).toEqual(['MY_KEY', 'sk-secret', 'reauth_tok'])
    })
  })

  it('dismissing the re-auth dialog sends nothing further', async () => {

    await openAddCredentialForm()

    await waitFor(() => screen.getByTestId('reauth-cancel'))
    fireEvent.click(screen.getByTestId('reauth-cancel'))

    await waitFor(() => {
      expect(screen.queryByTestId('reauth-password-input')).not.toBeInTheDocument()
    })
    expect(addCredential).not.toHaveBeenCalled()
  })
})

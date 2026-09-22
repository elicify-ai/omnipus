/**
 * SandboxSection.password.test.tsx — Spec-6 FR-12.2, ADR-0010 WP3.
 *
 * The password-mode counterpart to SandboxSection.test.tsx's confirm-mode
 * coverage: with AppState.identity.mode = 'local', useStepUp() picks
 * 'password' and a sandbox-config PUT goes through useStepUp's dialog-first
 * flow — ReAuthDialog (restored verbatim from the engine merge base) opens
 * before anything is sent, and a successful re-auth sends the PUT exactly
 * once, carrying the minted consent token. Nothing is sent on dismissal.
 * Representative of all five sandbox-config mutations this screen gates the
 * same way (mode, filesystem model, workspace limit, paths/SSRF, deny
 * patterns) — this file exercises the paths/SSRF save, mirroring the
 * confirm-mode file's equivalent test.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchSandboxStatus: vi.fn(),
    fetchSandboxConfig: vi.fn(),
    updateSandboxConfig: vi.fn(),
    fetchAppState: vi.fn(),
    reAuth: vi.fn(),
  }
})

const mockAddToast = vi.fn()
vi.mock('@/store/ui', () => ({
  useUiStore: vi.fn(() => ({ addToast: mockAddToast })),
}))

import { fetchSandboxStatus, fetchSandboxConfig, updateSandboxConfig, fetchAppState, reAuth } from '@/lib/api'
import { SandboxSection } from './SandboxSection'
import type { SandboxStatus, SandboxConfigResponse, AppState } from '@/lib/api'

// Local mode (identity.mode: 'local') pins useStepUp() to 'password'.
const LOCAL_APP_STATE = {
  onboarding_complete: true,
  identity: { mode: 'local', edition: 'core', signed_in: true },
} as AppState

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

function renderSection() {
  const client = makeClient()
  return render(
    <QueryClientProvider client={client}>
      <SandboxSection />
    </QueryClientProvider>,
  )
}

const baseStatus: SandboxStatus = {
  backend: 'landlock',
  available: true,
  kernel_level: true,
  policy_applied: true,
  seccomp_enabled: true,
  bind_ports_count: 0,
}

const baseConfig: SandboxConfigResponse = {
  mode: 'enforce',
  allowed_paths: ['/a'],
  ssrf: { allow_internal: [] },
  requires_restart: false,
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(fetchSandboxStatus).mockResolvedValue(baseStatus)
  vi.mocked(fetchSandboxConfig).mockResolvedValue(baseConfig)
  vi.mocked(fetchAppState).mockResolvedValue(LOCAL_APP_STATE)
})

describe('SandboxSection — password mode (local edition)', () => {
  it('opens ReAuthDialog before the gated PUT, then sends it once with the minted token', async () => {
    vi.mocked(updateSandboxConfig)
      .mockResolvedValueOnce({ ...baseConfig, allowed_paths: ['/a', '/valid'], requires_restart: true })
    vi.mocked(reAuth).mockResolvedValue({ verified: true, token: 'reauth_tok', expires_in: 300 } as never)

    renderSection()

    await waitFor(() => {
      expect(screen.getByRole('textbox', { name: /new allowed path/i })).toBeInTheDocument()
    })

    fireEvent.change(screen.getByRole('textbox', { name: /new allowed path/i }), { target: { value: '/valid' } })
    fireEvent.click(screen.getByRole('button', { name: /add path/i }))

    // Dialog first: ReAuthDialog opens before anything is sent.
    await waitFor(() => {
      expect(screen.getByTestId('reauth-password-input')).toBeInTheDocument()
    })
    expect(updateSandboxConfig).not.toHaveBeenCalled()

    fireEvent.change(screen.getByTestId('reauth-password-input'), { target: { value: 'mypassword' } })
    fireEvent.click(screen.getByTestId('reauth-confirm'))

    await waitFor(() => {
      expect(reAuth).toHaveBeenCalledWith('mypassword')
      expect(updateSandboxConfig).toHaveBeenCalledTimes(1)
      expect(vi.mocked(updateSandboxConfig).mock.calls[0][1]).toBe('reauth_tok')
    })
  })

  it('dismissing the re-auth dialog sends nothing further', async () => {

    renderSection()
    await waitFor(() => {
      expect(screen.getByRole('textbox', { name: /new allowed path/i })).toBeInTheDocument()
    })

    fireEvent.change(screen.getByRole('textbox', { name: /new allowed path/i }), { target: { value: '/valid' } })
    fireEvent.click(screen.getByRole('button', { name: /add path/i }))

    await waitFor(() => screen.getByTestId('reauth-cancel'))
    fireEvent.click(screen.getByTestId('reauth-cancel'))

    await waitFor(() => {
      expect(screen.queryByTestId('reauth-password-input')).not.toBeInTheDocument()
    })
    expect(updateSandboxConfig).not.toHaveBeenCalled()
  })
})

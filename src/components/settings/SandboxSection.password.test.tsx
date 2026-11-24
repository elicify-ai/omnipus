/**
 * SandboxSection.password.test.tsx — Settings redesign #1519 (FR-010,
 * FR-026, FR-027, FR-028, FR-029).
 *
 * The password-mode (local edition) counterpart to SandboxSection.test.tsx:
 * in the approved design the one password prompt per Save is shell-owned
 * (FR-026), so this file drives the section through the shell's real prompt
 * hook (useSavePasswordPrompt → ReAuthDialog) instead of the retired
 * section-level useStepUp flow. The old oracles — ReAuthDialog opens before
 * the gated PUT and a successful re-auth sends it exactly once with the
 * minted token; dismissal sends nothing — keep their meaning under the new
 * model: a Save containing a password-marked change opens exactly one prompt
 * listing the change by its visible label, one POST /auth/reauth mints the
 * single-use token that the one protected write carries (FR-027), and Cancel
 * stores nothing while keeping every draft (FR-029).
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
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

// Local mode (identity.mode: 'local') — the shell prompt is the password
// (ReAuthDialog) variant, not the platform confirm.
const LOCAL_APP_STATE = {
  onboarding_complete: true,
  identity: { mode: 'local', edition: 'core', signed_in: true },
} as AppState

// The FR-026 prompt is shell-owned: the section only calls
// shell.requestPassword(labels). Both the shell context and the shell's
// prompt hook ship with the redesign, so they are absent from the
// pre-change tree — loaded lazily and failing loudly as BLOCKED RED, never
// as a collection error.
type ShellCtx = typeof import('./SettingsShellContext')
type PromptHook = typeof import('./useSavePasswordPrompt')
let shellModules: { ctx: ShellCtx; prompt: PromptHook } | null = null
async function loadShellModules() {
  if (shellModules) return shellModules
  try {
    const [ctx, prompt] = await Promise.all([
      vi.importActual<ShellCtx>('./SettingsShellContext'),
      vi.importActual<PromptHook>('./useSavePasswordPrompt'),
    ])
    shellModules = { ctx, prompt }
    return shellModules
  } catch {
    throw new Error(
      'BLOCKED RED (FR-010/FR-026/FR-027, D-8 row 4): the Settings shell context and its password prompt ' +
        '(SettingsShellContext / useSavePasswordPrompt) do not exist yet — required for the one password ' +
        'prompt per Save (FR-026) and the consent-token write (FR-027)',
    )
  }
}

/** The shell around one section under test: real prompt hook, one live region owner. */
async function renderSection() {
  const { ctx, prompt } = await loadShellModules()
  const { useSavePasswordPrompt } = prompt
  const { SettingsShellContext } = ctx
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  function ShellHarness({ children }: { children: ReactNode }) {
    const { requestPassword, dialog } = useSavePasswordPrompt()
    return (
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
        {children}
        {dialog}
      </SettingsShellContext.Provider>
    )
  }
  return render(
    <QueryClientProvider client={client}>
      <ShellHarness>
        <SandboxSection />
      </ShellHarness>
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
  vi.mocked(updateSandboxConfig).mockResolvedValue({ ...baseConfig, allowed_paths: ['/a', '/valid'], requires_restart: true })
  vi.mocked(fetchAppState).mockResolvedValue(LOCAL_APP_STATE)
})

describe('SandboxSection — password mode (local edition, the shell FR-026 prompt)', () => {
  it('a Save with a password-marked change opens the one prompt first, then one reauth mints the token the one PUT carries (FR-010, FR-026, FR-027)', async () => {
    vi.mocked(reAuth).mockResolvedValue({ verified: true, token: 'reauth_tok', expires_in: 300 } as never)

    await renderSection()

    await waitFor(() => {
      expect(screen.getByRole('textbox', { name: /new allowed path/i })).toBeInTheDocument()
    })

    // Draft the change (FR-010: nothing is sent while editing).
    fireEvent.change(screen.getByRole('textbox', { name: /new allowed path/i }), { target: { value: '/valid' } })
    fireEvent.click(screen.getByRole('button', { name: /add path/i }))
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    expect(updateSandboxConfig).not.toHaveBeenCalled()
    expect(reAuth).not.toHaveBeenCalled()

    // Save changes: the prompt opens first, listing the password-marked
    // setting by its visible label (FR-026) — nothing is sent before it.
    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    const items = await screen.findByTestId('reauth-items', {}, { timeout: 5000 })
    expect(items).toHaveTextContent('Allowed folders')
    expect(reAuth).not.toHaveBeenCalled()
    expect(updateSandboxConfig).not.toHaveBeenCalled()

    fireEvent.change(screen.getByTestId('reauth-password-input'), { target: { value: 'mypassword' } })
    fireEvent.click(screen.getByTestId('reauth-confirm'))

    // Exactly one POST /auth/reauth (FR-027), then exactly one protected
    // write carrying the minted single-use token (FR-027/FR-015).
    await waitFor(() => {
      expect(reAuth).toHaveBeenCalledTimes(1)
      expect(reAuth).toHaveBeenCalledWith('mypassword')
      expect(updateSandboxConfig).toHaveBeenCalledTimes(1)
      expect(vi.mocked(updateSandboxConfig).mock.calls[0]).toEqual([
        { allowed_paths: ['/a', '/valid'] },
        'reauth_tok',
      ])
    })
  })

  it('cancelling the prompt stores nothing and keeps the draft on the bar (FR-029, FR-010)', async () => {
    await renderSection()
    await waitFor(() => {
      expect(screen.getByRole('textbox', { name: /new allowed path/i })).toBeInTheDocument()
    })

    fireEvent.change(screen.getByRole('textbox', { name: /new allowed path/i }), { target: { value: '/valid' } })
    fireEvent.click(screen.getByRole('button', { name: /add path/i }))
    await screen.findByText(/1 unsaved change/)

    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    await waitFor(() => screen.getByTestId('reauth-cancel'))
    fireEvent.click(screen.getByTestId('reauth-cancel'))

    await waitFor(() => {
      expect(screen.queryByTestId('reauth-password-input')).not.toBeInTheDocument()
    })
    // Cancel stores nothing (FR-029) and the change stays a draft on the bar
    // (FR-010) — it can still be saved afterwards.
    expect(reAuth).not.toHaveBeenCalled()
    expect(updateSandboxConfig).not.toHaveBeenCalled()
    expect(screen.getByText(/1 unsaved change/)).toBeInTheDocument()
    expect(screen.getByText('/valid')).toBeInTheDocument()
  })
})

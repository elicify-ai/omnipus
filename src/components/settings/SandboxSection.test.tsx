import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

// ── Module mocks ──────────────────────────────────────────────────────────────

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchSandboxStatus: vi.fn(),
    fetchSandboxConfig: vi.fn(),
    updateSandboxConfig: vi.fn(),
    reAuth: vi.fn(),
  }
})

const mockAddToast = vi.fn()
vi.mock('@/store/ui', () => ({
  useUiStore: vi.fn(() => ({ addToast: mockAddToast })),
}))

import { fetchSandboxStatus, fetchSandboxConfig, updateSandboxConfig, reAuth, ApiError } from '@/lib/api'
import { SandboxSection } from './SandboxSection'
import type { SandboxStatus, SandboxConfigResponse } from '@/lib/api'

// reAuth403 is the exact 403 the backend's requireReAuth gate returns. The
// useReAuthGate hook detects it by body match and opens the consent dialog.
function reAuth403() {
  return new ApiError(
    403,
    "You don't have permission to perform this action.",
    { body: '{"error":"this change requires re-typing your password — call POST /api/v1/auth/reauth first"}' },
  )
}

// ── Helpers ───────────────────────────────────────────────────────────────────

function makeClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
}

function renderSection() {
  const client = makeClient()
  const utils = render(
    <QueryClientProvider client={client}>
      <SandboxSection />
    </QueryClientProvider>
  )
  return { ...utils, client }
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
  mode: 'permissive',
  allowed_paths: [],
  ssrf: { allow_internal: [] },
  requires_restart: false,
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(fetchSandboxStatus).mockResolvedValue(baseStatus)
  vi.mocked(fetchSandboxConfig).mockResolvedValue(baseConfig)
  vi.mocked(updateSandboxConfig).mockResolvedValue({ ...baseConfig, requires_restart: true })
  // Reset localStorage/sessionStorage
  localStorage.clear()
  sessionStorage.clear()
})

// ── describe: allowed_paths editor ───────────────────────────────────────────

describe('allowed_paths editor', () => {
  it('renders two rows with read-only badges when allowed_paths has two entries', async () => {
    vi.mocked(fetchSandboxConfig).mockResolvedValue({
      ...baseConfig,
      allowed_paths: ['/a', '/b'],
    })

    renderSection()

    await waitFor(() => {
      expect(screen.getByText('/a')).toBeInTheDocument()
      expect(screen.getByText('/b')).toBeInTheDocument()
    })

    const roBadges = screen.getAllByText('read-only')
    expect(roBadges).toHaveLength(2)
  })

  it('shows Filesystem paths the sandbox may read heading', async () => {
    renderSection()

    await waitFor(() => {
      expect(screen.getByText(/Filesystem paths the sandbox may read/i)).toBeInTheDocument()
    })
  })

  it('no Edit button for paths section (autosave — Add/Delete commit immediately)', async () => {
    renderSection()

    await waitFor(() => {
      expect(screen.getByText(/Filesystem paths the sandbox may read/i)).toBeInTheDocument()
    })

    // No edit button — editor is always visible for admin
    expect(screen.queryByRole('button', { name: /^edit$/i })).not.toBeInTheDocument()
  })

  it('typing /c in the path input and clicking Add fires updateSandboxConfig immediately', async () => {
    vi.mocked(fetchSandboxConfig).mockResolvedValue({
      ...baseConfig,
      allowed_paths: ['/a', '/b'],
    })

    renderSection()

    await waitFor(() => {
      expect(screen.getByRole('textbox', { name: /new allowed path/i })).toBeInTheDocument()
    })

    const input = screen.getByRole('textbox', { name: /new allowed path/i })
    fireEvent.change(input, { target: { value: '/c' } })

    const addBtn = screen.getByRole('button', { name: /add path/i })
    fireEvent.click(addBtn)

    await waitFor(() => {
      expect(screen.getByText('/c')).toBeInTheDocument()
    })

    await waitFor(() => {
      expect(updateSandboxConfig).toHaveBeenCalled()
      const [firstArg] = vi.mocked(updateSandboxConfig).mock.calls[0]
      expect(firstArg).toMatchObject({
        allowed_paths: ['/a', '/b', '/c'],
      })
    })
  })

  it('server 400 for relative path displays inline error on the failing row', async () => {
    vi.mocked(fetchSandboxConfig).mockResolvedValue({
      ...baseConfig,
      allowed_paths: ['./foo'],
    })
    vi.mocked(updateSandboxConfig).mockRejectedValue(
      new Error('400: allowed_paths[0]: must be absolute — `./foo` is relative')
    )

    renderSection()

    // Add a new path to trigger a save
    await waitFor(() => {
      expect(screen.getByRole('textbox', { name: /new allowed path/i })).toBeInTheDocument()
    })

    const input = screen.getByRole('textbox', { name: /new allowed path/i })
    fireEvent.change(input, { target: { value: '/valid' } })
    fireEvent.click(screen.getByRole('button', { name: /add path/i }))

    await waitFor(() => {
      const errors = screen.getAllByText(/must be absolute/i)
      expect(errors.length).toBeGreaterThan(0)
    })
  })

  it('delete button fires updateSandboxConfig immediately without Save button', async () => {
    vi.mocked(fetchSandboxConfig).mockResolvedValue({
      ...baseConfig,
      allowed_paths: ['/a', '/b'],
    })

    renderSection()

    await waitFor(() => {
      expect(screen.getByText('/a')).toBeInTheDocument()
    })

    // Delete first path — no Edit mode needed
    const deleteBtn = screen.getByRole('button', { name: /delete path \/a/i })
    fireEvent.click(deleteBtn)

    await waitFor(() => {
      expect(screen.queryByText('/a')).not.toBeInTheDocument()
      expect(screen.getByText('/b')).toBeInTheDocument()
    })

    await waitFor(() => {
      // The re-auth gate's optimistic first attempt passes token '' as the 2nd arg.
      expect(updateSandboxConfig).toHaveBeenCalledWith(
        expect.objectContaining({ allowed_paths: ['/b'] }),
        '',
      )
    })
  })

  it('read-only badge has a tooltip with the correct text', async () => {
    vi.mocked(fetchSandboxConfig).mockResolvedValue({
      ...baseConfig,
      allowed_paths: ['/etc'],
    })

    renderSection()

    await waitFor(() => {
      expect(screen.getByText('/etc')).toBeInTheDocument()
    })

    const badge = screen.getByText('read-only')
    fireEvent.mouseEnter(badge)

    await waitFor(() => {
      expect(
        screen.getByText(/AllowedPaths entries grant read-only access/i)
      ).toBeInTheDocument()
    })
  })
})

// ── describe: SSRF editor ─────────────────────────────────────────────────────

describe('SSRF editor', () => {
  const RFC1918_LIST = ['127.0.0.1', '::1', '10.0.0.0/8', '172.16.0.0/12', '192.168.0.0/16', 'fc00::/7']
  const LOOPBACK_LIST = ['127.0.0.1', '::1']

  it('clicking "Allow RFC1918 + loopback" fires updateSandboxConfig immediately with exact preset list', async () => {
    renderSection()

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /allow rfc1918 \+ loopback/i })).toBeInTheDocument()
    })

    const rfc1918Btn = screen.getByRole('button', { name: /allow rfc1918 \+ loopback/i })
    fireEvent.click(rfc1918Btn)

    await waitFor(() => {
      expect(updateSandboxConfig).toHaveBeenCalled()
      const [firstArg] = vi.mocked(updateSandboxConfig).mock.calls[0]
      expect(firstArg).toMatchObject({
        ssrf: { allow_internal: RFC1918_LIST },
      })
    })
  })

  it('stored list matching preset (order-insensitive) → preset button is highlighted as active on mount', async () => {
    const shuffled = ['::1', '127.0.0.1']
    vi.mocked(fetchSandboxConfig).mockResolvedValue({
      ...baseConfig,
      ssrf: { allow_internal: shuffled },
    })

    renderSection()

    await waitFor(() => {
      const loopbackBtn = screen.getByRole('button', { name: /allow loopback only/i })
      expect(loopbackBtn).toHaveAttribute('aria-pressed', 'true')
    })
  })

  it('stored list with internal.corp (no preset match) → Advanced mode auto-expands', async () => {
    vi.mocked(fetchSandboxConfig).mockResolvedValue({
      ...baseConfig,
      ssrf: { allow_internal: ['internal.corp'] },
    })

    renderSection()

    await waitFor(() => {
      expect(screen.getByText('internal.corp')).toBeInTheDocument()
    })

    const blockAllBtn = screen.getByRole('button', { name: /block all/i })
    const loopbackBtn = screen.getByRole('button', { name: /allow loopback only/i })
    const rfc1918Btn = screen.getByRole('button', { name: /allow rfc1918 \+ loopback/i })
    expect(blockAllBtn).toHaveAttribute('aria-pressed', 'false')
    expect(loopbackBtn).toHaveAttribute('aria-pressed', 'false')
    expect(rfc1918Btn).toHaveAttribute('aria-pressed', 'false')
  })

  it('malformed CIDR entry in Advanced mode → inline error, Add rejected', async () => {
    renderSection()

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /advanced \(custom list\)/i })).toBeInTheDocument()
    })

    const advancedToggle = screen.getByRole('button', { name: /advanced \(custom list\)/i })
    fireEvent.click(advancedToggle)

    const input = screen.getByRole('textbox', { name: /new ssrf allow entry/i })
    fireEvent.change(input, { target: { value: '10.0.0/8' } })
    const addBtn = screen.getByRole('button', { name: /add ssrf entry/i })
    fireEvent.click(addBtn)

    await waitFor(() => {
      expect(
        screen.getByText(/invalid entry — expected hostname, IP, or CIDR/i)
      ).toBeInTheDocument()
    })

    expect(screen.getByText(/invalid entry — expected hostname, IP, or CIDR/i)).toBeInTheDocument()
    // updateSandboxConfig should NOT have been called
    expect(updateSandboxConfig).not.toHaveBeenCalled()
  })

  it('adding 0.0.0.0/0 triggers wildcard confirmation modal; PUT fires only on confirm', async () => {
    renderSection()

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /advanced \(custom list\)/i })).toBeInTheDocument()
    })

    const advancedToggle = screen.getByRole('button', { name: /advanced \(custom list\)/i })
    fireEvent.click(advancedToggle)

    const input = screen.getByRole('textbox', { name: /new ssrf allow entry/i })
    fireEvent.change(input, { target: { value: '0.0.0.0/0' } })
    fireEvent.click(screen.getByRole('button', { name: /add ssrf entry/i }))

    await waitFor(() => {
      expect(screen.getByText('0.0.0.0/0')).toBeInTheDocument()
    })

    // Modal should appear immediately (autosave with wildcard check)
    await waitFor(() => {
      expect(screen.getByRole('dialog')).toBeInTheDocument()
      expect(screen.getByRole('dialog')).toHaveTextContent(/disable ssrf protection/i)
    })

    // PUT should NOT have fired yet
    expect(updateSandboxConfig).not.toHaveBeenCalled()

    // Click Save anyway — PUT should fire
    fireEvent.click(screen.getByRole('button', { name: /save anyway/i }))

    await waitFor(() => {
      expect(updateSandboxConfig).toHaveBeenCalled()
      const [firstArg] = vi.mocked(updateSandboxConfig).mock.calls[0]
      expect(firstArg).toMatchObject({
        ssrf: { allow_internal: expect.arrayContaining(['0.0.0.0/0']) },
      })
    })
  })

  it('cancelling wildcard modal prevents PUT from firing', async () => {
    renderSection()

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /advanced \(custom list\)/i })).toBeInTheDocument()
    })

    const advancedToggle = screen.getByRole('button', { name: /advanced \(custom list\)/i })
    fireEvent.click(advancedToggle)

    const input = screen.getByRole('textbox', { name: /new ssrf allow entry/i })
    fireEvent.change(input, { target: { value: '0.0.0.0/0' } })
    fireEvent.click(screen.getByRole('button', { name: /add ssrf entry/i }))

    await waitFor(() => screen.getByText('0.0.0.0/0'))

    await waitFor(() => {
      expect(screen.getByRole('dialog')).toBeInTheDocument()
    })

    fireEvent.click(screen.getByRole('button', { name: /^cancel$/i }))

    await waitFor(() => {
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    })

    expect(updateSandboxConfig).not.toHaveBeenCalled()
  })

  it('clicking "Block all" preset fires updateSandboxConfig immediately with empty allow_internal', async () => {
    vi.mocked(fetchSandboxConfig).mockResolvedValue({
      ...baseConfig,
      ssrf: { allow_internal: LOOPBACK_LIST },
    })

    renderSection()

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /block all/i })).toBeInTheDocument()
    })

    fireEvent.click(screen.getByRole('button', { name: /block all/i }))

    await waitFor(() => {
      expect(updateSandboxConfig).toHaveBeenCalled()
      const [firstArg] = vi.mocked(updateSandboxConfig).mock.calls[0]
      expect(firstArg).toMatchObject({
        ssrf: { allow_internal: [] },
      })
    })
  })
})

// ── describe: ABI v4 surfaces ─────────────────────────────────────────────────

describe('ABI v4 surfaces', () => {
  it('abi_version=4 + issue_ref → yellow banner visible with issue_ref text', async () => {
    vi.mocked(fetchSandboxStatus).mockResolvedValue({
      ...baseStatus,
      abi_version: 4,
      issue_ref: '#138',
    })

    renderSection()

    await waitFor(() => {
      expect(screen.getByRole('alert')).toBeInTheDocument()
      expect(screen.getByRole('alert')).toHaveTextContent('#138')
    })
  })

  // 60_000 ms timeout: this test uses async waitFor and can be slow under
  // full-suite concurrent load when all 86 test files run in parallel forks.
  it('dismiss button stores sessionStorage key and hides banner', async () => {
    vi.mocked(fetchSandboxStatus).mockResolvedValue({
      ...baseStatus,
      abi_version: 4,
      issue_ref: '#138',
    })

    renderSection()

    await waitFor(() => {
      expect(screen.getByRole('alert')).toBeInTheDocument()
    })

    fireEvent.click(screen.getByRole('button', { name: /dismiss for session/i }))

    await waitFor(() => {
      expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    })

    expect(sessionStorage.getItem('omnipus:abi4-banner-dismissed')).toBe('dismissed')
  }, 60_000)

  it('abi_version=3 → banner NOT rendered', async () => {
    vi.mocked(fetchSandboxStatus).mockResolvedValue({
      ...baseStatus,
      abi_version: 3,
    })

    renderSection()

    await waitFor(() => {
      expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    })
  })

  it('abi_version field absent from response → banner NOT rendered', async () => {
    vi.mocked(fetchSandboxStatus).mockResolvedValue({
      ...baseStatus,
    })

    renderSection()

    await waitFor(() => {
      expect(screen.getByText('landlock')).toBeInTheDocument()
    })

    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('banner contains abi_version in text', async () => {
    vi.mocked(fetchSandboxStatus).mockResolvedValue({
      ...baseStatus,
      abi_version: 5,
      issue_ref: '#200',
    })

    renderSection()

    await waitFor(() => {
      const banner = screen.getByRole('alert')
      expect(banner).toHaveTextContent('Landlock v5')
      expect(banner).toHaveTextContent('#200')
    })
  })

  it('issue_ref from server response appears in banner — not hardcoded', async () => {
    vi.mocked(fetchSandboxStatus).mockResolvedValue({
      ...baseStatus,
      abi_version: 4,
      issue_ref: '#999-CUSTOM',
    })

    renderSection()

    await waitFor(() => {
      expect(screen.getByRole('alert')).toHaveTextContent('#999-CUSTOM')
    })
  })

  it('banner is NOT shown when abi_version=4 but issue_ref is absent', async () => {
    vi.mocked(fetchSandboxStatus).mockResolvedValue({
      ...baseStatus,
      abi_version: 4,
    })

    renderSection()

    await waitFor(() => {
      expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    })
  })

  it('banner is not shown when sessionStorage has dismiss key set', async () => {
    sessionStorage.setItem('omnipus:abi4-banner-dismissed', 'dismissed')

    vi.mocked(fetchSandboxStatus).mockResolvedValue({
      ...baseStatus,
      abi_version: 4,
      issue_ref: '#138',
    })

    renderSection()

    await waitFor(() => {
      expect(screen.getByText(/process sandbox/i)).toBeInTheDocument()
    })

    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})

// ── describe: mode radio (autosave — no Edit button) ─────────────────────────

describe('mode radio', () => {
  it('renders three radio options (Off, Permissive, Enforce) with current value pre-selected — no Edit button', async () => {
    vi.mocked(fetchSandboxConfig).mockResolvedValue({
      ...baseConfig,
      mode: 'permissive',
    })

    renderSection()

    await waitFor(() => {
      const offRadio = screen.getByRole('radio', { name: /sandbox mode: off/i })
      const permissiveRadio = screen.getByRole('radio', { name: /sandbox mode: permissive/i })
      const enforceRadio = screen.getByRole('radio', { name: /sandbox mode: enforce/i })
      expect(offRadio).toBeInTheDocument()
      expect(permissiveRadio).toBeInTheDocument()
      expect(enforceRadio).toBeInTheDocument()
      // Current value (permissive) is pre-selected
      expect(permissiveRadio).toBeChecked()
      expect(offRadio).not.toBeChecked()
      expect(enforceRadio).not.toBeChecked()
    })

    // No Edit button for mode section — autosave
    expect(screen.queryByRole('button', { name: /edit sandbox mode/i })).not.toBeInTheDocument()
  })

  it('selecting enforce when abi_version >= 4 fires the enforce confirmation modal before PUT', async () => {
    vi.mocked(fetchSandboxStatus).mockResolvedValue({
      ...baseStatus,
      abi_version: 4,
      issue_ref: '#138',
    })
    vi.mocked(fetchSandboxConfig).mockResolvedValue({
      ...baseConfig,
      mode: 'permissive',
    })

    renderSection()

    // Radios are always shown for admin — no Edit button needed
    await waitFor(() => {
      expect(screen.getByRole('radio', { name: /sandbox mode: enforce/i })).toBeInTheDocument()
    })
    fireEvent.click(screen.getByRole('radio', { name: /sandbox mode: enforce/i }))

    // Modal should appear before PUT fires
    await waitFor(() => {
      expect(screen.getByRole('dialog')).toBeInTheDocument()
      expect(screen.getByRole('dialog')).toHaveTextContent(/kernel incompatibility/i)
    })
    expect(updateSandboxConfig).not.toHaveBeenCalled()

    // Confirm — now PUT fires
    fireEvent.click(screen.getByRole('button', { name: /save anyway/i }))

    await waitFor(() => {
      expect(updateSandboxConfig).toHaveBeenCalled()
      const [firstArg] = vi.mocked(updateSandboxConfig).mock.calls[0]
      expect(firstArg).toMatchObject({ mode: 'enforce' })
    })
  })

  it('changing mode from off → permissive fires PUT immediately with {mode: "permissive"}', async () => {
    vi.mocked(fetchSandboxConfig).mockResolvedValue({
      ...baseConfig,
      mode: 'off',
    })
    vi.mocked(updateSandboxConfig).mockResolvedValue({ ...baseConfig, mode: 'permissive', requires_restart: true })

    renderSection()

    await waitFor(() => {
      expect(screen.getByRole('radio', { name: /sandbox mode: permissive/i })).toBeInTheDocument()
    })
    fireEvent.click(screen.getByRole('radio', { name: /sandbox mode: permissive/i }))

    await waitFor(() => {
      expect(updateSandboxConfig).toHaveBeenCalled()
      const [firstArg] = vi.mocked(updateSandboxConfig).mock.calls[0]
      expect(firstArg).toMatchObject({ mode: 'permissive' })
    })
  })

  it('cancelling enforce modal reverts radio selection', async () => {
    vi.mocked(fetchSandboxStatus).mockResolvedValue({
      ...baseStatus,
      abi_version: 4,
      issue_ref: '#138',
    })
    vi.mocked(fetchSandboxConfig).mockResolvedValue({
      ...baseConfig,
      mode: 'permissive',
    })

    renderSection()

    await waitFor(() => {
      expect(screen.getByRole('radio', { name: /sandbox mode: enforce/i })).toBeInTheDocument()
    })
    fireEvent.click(screen.getByRole('radio', { name: /sandbox mode: enforce/i }))

    await waitFor(() => {
      expect(screen.getByRole('dialog')).toBeInTheDocument()
    })

    // Cancel — should revert to permissive, PUT not called
    fireEvent.click(screen.getByRole('button', { name: /^cancel$/i }))

    await waitFor(() => {
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    })

    expect(updateSandboxConfig).not.toHaveBeenCalled()

    // Permissive should still be checked
    await waitFor(() => {
      expect(screen.getByRole('radio', { name: /sandbox mode: permissive/i })).toBeChecked()
    })
  })
})

// ── describe: re-auth gate (Spec-6 FR-12.2) ─────────────────────────────────────

describe('sandbox-config re-auth gate', () => {
  it('opens the re-auth dialog when the gated PUT returns 403, then replays the token', async () => {
    vi.mocked(fetchSandboxConfig).mockResolvedValue({
      ...baseConfig,
      allowed_paths: ['/a'],
    })
    // First attempt (no consent token) is rejected by the re-auth gate; the
    // second attempt (with the minted token) succeeds.
    vi.mocked(updateSandboxConfig)
      .mockRejectedValueOnce(reAuth403())
      .mockResolvedValueOnce({ ...baseConfig, allowed_paths: ['/a', '/valid'], requires_restart: true })
    vi.mocked(reAuth).mockResolvedValue({ verified: true, token: 'reauth_tok', expires_in: 300 } as never)

    renderSection()

    await waitFor(() => {
      expect(screen.getByRole('textbox', { name: /new allowed path/i })).toBeInTheDocument()
    })

    fireEvent.change(screen.getByRole('textbox', { name: /new allowed path/i }), { target: { value: '/valid' } })
    fireEvent.click(screen.getByRole('button', { name: /add path/i }))

    // The first PUT (token '') fired and 403'd → consent dialog appears.
    await waitFor(() => {
      expect(screen.getByTestId('reauth-confirm')).toBeInTheDocument()
    })
    expect(vi.mocked(updateSandboxConfig).mock.calls[0][1]).toBe('')

    // Re-authenticate; the PUT is replayed with the consent token.
    fireEvent.change(screen.getByTestId('reauth-password-input'), { target: { value: 'mypassword' } })
    fireEvent.click(screen.getByTestId('reauth-confirm'))

    await waitFor(() => {
      expect(reAuth).toHaveBeenCalledWith('mypassword')
      expect(updateSandboxConfig).toHaveBeenCalledTimes(2)
      expect(vi.mocked(updateSandboxConfig).mock.calls[1][1]).toBe('reauth_tok')
    })
  })
})

// ── describe: re-auth CANCEL reverts optimistic state (bug #142 regression) ──
//
// Wave 1 added an isReAuthCancelled guard to doSaveMode/saveMutation/
// saveDenyPatterns so a dismissed password prompt doesn't surface a spurious
// "save failed" toast. But doSaveMode's cancel branch stopped short of
// reverting the mode that handleModeChange had already flipped optimistically
// — leaving the radio pointing at an unsaved target with zero error
// indicator, and (because handleModeChange early-returns on
// `mode === currentMode`) no way to even re-click the same radio to retry.
// These tests exercise cancellation (not a mutation error) via the
// `reauth-cancel` button and assert the optimistic edit is rolled back.
describe('re-auth cancel reverts optimistic state', () => {
  it('cancelling re-auth on a mode change reverts currentMode to the saved mode (not stuck on the unsaved target)', async () => {
    vi.mocked(fetchSandboxConfig).mockResolvedValue({
      ...baseConfig,
      mode: 'permissive',
    })
    vi.mocked(updateSandboxConfig).mockRejectedValue(reAuth403())

    renderSection()

    await waitFor(() => {
      expect(screen.getByRole('radio', { name: /sandbox mode: off/i })).toBeInTheDocument()
    })
    expect(screen.getByRole('radio', { name: /sandbox mode: permissive/i })).toBeChecked()

    fireEvent.click(screen.getByRole('radio', { name: /sandbox mode: off/i }))

    // Optimistic UI: "off" is shown as selected immediately, before the PUT settles.
    await waitFor(() => {
      expect(screen.getByRole('radio', { name: /sandbox mode: off/i })).toBeChecked()
    })

    // First PUT (token '') 403s on the re-auth gate -> consent dialog opens.
    await waitFor(() => {
      expect(screen.getByTestId('reauth-cancel')).toBeInTheDocument()
    })

    // User dismisses the password prompt instead of confirming.
    fireEvent.click(screen.getByTestId('reauth-cancel'))

    await waitFor(() => {
      expect(screen.queryByTestId('reauth-cancel')).not.toBeInTheDocument()
    })

    // The optimistic "off" selection must revert to the saved mode
    // ("permissive") — this is the core bug-142 assertion.
    await waitFor(() => {
      expect(screen.getByRole('radio', { name: /sandbox mode: permissive/i })).toBeChecked()
      expect(screen.getByRole('radio', { name: /sandbox mode: off/i })).not.toBeChecked()
    })

    // No error toast for a user-initiated cancel, and no retry attempt fired.
    expect(mockAddToast).not.toHaveBeenCalled()
    expect(updateSandboxConfig).toHaveBeenCalledTimes(1)

    // Recovery check: clicking "off" again must still work now that
    // currentMode is genuinely back at "permissive" (no same-value
    // early-return trap left over from the optimistic update).
    vi.mocked(updateSandboxConfig).mockResolvedValueOnce({ ...baseConfig, mode: 'off', requires_restart: true })
    fireEvent.click(screen.getByRole('radio', { name: /sandbox mode: off/i }))
    await waitFor(() => {
      expect(updateSandboxConfig).toHaveBeenCalledTimes(2)
    })
  })

  it('cancelling re-auth on an allowed-path add reverts pathList to the saved server list', async () => {
    vi.mocked(fetchSandboxConfig).mockResolvedValue({
      ...baseConfig,
      allowed_paths: ['/a'],
    })
    vi.mocked(updateSandboxConfig).mockRejectedValue(reAuth403())

    renderSection()

    await waitFor(() => {
      expect(screen.getByText('/a')).toBeInTheDocument()
    })

    fireEvent.change(screen.getByRole('textbox', { name: /new allowed path/i }), { target: { value: '/valid' } })
    fireEvent.click(screen.getByRole('button', { name: /add path/i }))

    // Optimistic add renders immediately.
    await waitFor(() => {
      expect(screen.getByText('/valid')).toBeInTheDocument()
    })

    // First PUT (token '') 403s -> consent dialog opens.
    await waitFor(() => {
      expect(screen.getByTestId('reauth-cancel')).toBeInTheDocument()
    })

    fireEvent.click(screen.getByTestId('reauth-cancel'))

    await waitFor(() => {
      expect(screen.queryByTestId('reauth-cancel')).not.toBeInTheDocument()
    })

    // The optimistic '/valid' row must be reverted — it was never persisted.
    await waitFor(() => {
      expect(screen.queryByText('/valid')).not.toBeInTheDocument()
      expect(screen.getByText('/a')).toBeInTheDocument()
    })

    expect(mockAddToast).not.toHaveBeenCalled()
    expect(updateSandboxConfig).toHaveBeenCalledTimes(1)
  })

  it('cancelling re-auth on a shell-deny-patterns edit reverts the textarea to the saved patterns', async () => {
    vi.mocked(fetchSandboxConfig).mockResolvedValue({
      ...baseConfig,
      shell_deny_patterns: ['^curl\\s'],
    })
    vi.mocked(updateSandboxConfig).mockRejectedValue(reAuth403())

    renderSection()

    await waitFor(() => {
      expect(screen.getByTestId('shell-deny-patterns-textarea')).toHaveValue('^curl\\s')
    })

    fireEvent.change(screen.getByTestId('shell-deny-patterns-textarea'), {
      target: { value: '^curl\\s\n^wget\\s' },
    })

    // Debounced autosave (400ms) fires the gated PUT; it 403s -> dialog opens.
    await waitFor(
      () => {
        expect(screen.getByTestId('reauth-cancel')).toBeInTheDocument()
      },
      { timeout: 3000 },
    )

    fireEvent.click(screen.getByTestId('reauth-cancel'))

    await waitFor(() => {
      expect(screen.queryByTestId('reauth-cancel')).not.toBeInTheDocument()
    })

    // The optimistic '^wget\s' line must be reverted to the saved patterns.
    await waitFor(() => {
      expect(screen.getByTestId('shell-deny-patterns-textarea')).toHaveValue('^curl\\s')
    })

    expect(mockAddToast).not.toHaveBeenCalled()
    // Reverting must not itself re-trigger the debounced autosave (which
    // would silently reopen the re-auth prompt for a no-op save).
    expect(updateSandboxConfig).toHaveBeenCalledTimes(1)
  }, 10_000)

  // SsrfEditor (extracted from SandboxSection in Wave 3, same as
  // AllowedPathsEditor) shares revertPathsSsrfToServer with the allowed-paths
  // editor but had zero re-auth-cancel-revert coverage of its own — the two
  // other tests in this block exercise the mode radio and
  // ShellDenyPatternsEditor, not SsrfEditor's own optimistic add/revert path.
  it('cancelling re-auth on an SSRF allow-internal add reverts ssrfList to the saved server list', async () => {
    // A single-entry list matches no SSRF_PRESETS (lengths are 0/2/6), so
    // Advanced mode auto-expands on mount and '127.0.0.1' is visible without
    // needing to click the "Advanced (custom list)" toggle first.
    vi.mocked(fetchSandboxConfig).mockResolvedValue({
      ...baseConfig,
      ssrf: { allow_internal: ['127.0.0.1'] },
    })
    vi.mocked(updateSandboxConfig).mockRejectedValue(reAuth403())

    renderSection()

    await waitFor(() => {
      expect(screen.getByText('127.0.0.1')).toBeInTheDocument()
    })

    const input = screen.getByRole('textbox', { name: /new ssrf allow entry/i })
    fireEvent.change(input, { target: { value: '10.0.0.0/8' } })
    fireEvent.click(screen.getByRole('button', { name: /add ssrf entry/i }))

    // Optimistic add renders immediately.
    await waitFor(() => {
      expect(screen.getByText('10.0.0.0/8')).toBeInTheDocument()
    })

    // First PUT (token '') 403s -> consent dialog opens.
    await waitFor(() => {
      expect(screen.getByTestId('reauth-cancel')).toBeInTheDocument()
    })

    // User dismisses the password prompt instead of confirming.
    fireEvent.click(screen.getByTestId('reauth-cancel'))

    await waitFor(() => {
      expect(screen.queryByTestId('reauth-cancel')).not.toBeInTheDocument()
    })

    // The optimistic '10.0.0.0/8' entry must be reverted — it was never
    // persisted — while the pre-existing saved entry stays put.
    await waitFor(() => {
      expect(screen.queryByText('10.0.0.0/8')).not.toBeInTheDocument()
      expect(screen.getByText('127.0.0.1')).toBeInTheDocument()
    })

    // No error toast for a user-initiated cancel, and no retry attempt fired.
    expect(mockAddToast).not.toHaveBeenCalled()
    expect(updateSandboxConfig).toHaveBeenCalledTimes(1)
  })

  // handlePresetClick routes through the exact same commitPathsSsrfWithWildcardCheck
  // -> commitPathsSsrf -> saveMutation.mutate machinery as handleAddSsrfEntry
  // above (SandboxSection.tsx:524-530), and saveMutation.onError's
  // isReAuthCancelled branch calls the same revertPathsSsrfToServer() — but
  // had zero re-auth-cancel-revert coverage of its own.
  it('cancelling re-auth on a preset click reverts the active SSRF preset to the saved server state', async () => {
    // Server state matches "Allow loopback only" — advancedOpen stays
    // collapsed at mount (mirrors the aria-pressed test earlier in this file).
    vi.mocked(fetchSandboxConfig).mockResolvedValue({
      ...baseConfig,
      ssrf: { allow_internal: ['127.0.0.1', '::1'] },
    })
    vi.mocked(updateSandboxConfig).mockRejectedValue(reAuth403())

    renderSection()

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /allow loopback only/i })).toHaveAttribute(
        'aria-pressed',
        'true',
      )
    })

    // Click a DIFFERENT preset — optimistically flips the active preset and
    // fires the same commit/save-mutation machinery as the tested add path.
    fireEvent.click(screen.getByRole('button', { name: /allow rfc1918 \+ loopback/i }))

    // Optimistic UI: RFC1918 preset shows pressed immediately, before the PUT settles.
    await waitFor(() => {
      expect(screen.getByRole('button', { name: /allow rfc1918 \+ loopback/i })).toHaveAttribute(
        'aria-pressed',
        'true',
      )
    })

    // First PUT (token '') 403s on the re-auth gate -> consent dialog opens.
    await waitFor(() => {
      expect(screen.getByTestId('reauth-cancel')).toBeInTheDocument()
    })

    // User dismisses the password prompt instead of confirming.
    fireEvent.click(screen.getByTestId('reauth-cancel'))

    await waitFor(() => {
      expect(screen.queryByTestId('reauth-cancel')).not.toBeInTheDocument()
    })

    // The optimistic preset switch must revert — active preset goes back to
    // "Allow loopback only", the never-persisted RFC1918 selection is undone.
    await waitFor(() => {
      expect(screen.getByRole('button', { name: /allow loopback only/i })).toHaveAttribute(
        'aria-pressed',
        'true',
      )
      expect(screen.getByRole('button', { name: /allow rfc1918 \+ loopback/i })).toHaveAttribute(
        'aria-pressed',
        'false',
      )
    })

    // No error toast for a user-initiated cancel, and no retry attempt fired.
    expect(mockAddToast).not.toHaveBeenCalled()
    expect(updateSandboxConfig).toHaveBeenCalledTimes(1)
  })

  // handleDeleteSsrfEntry routes through the exact same commitPathsSsrf ->
  // saveMutation.mutate machinery as handleAddSsrfEntry above
  // (SandboxSection.tsx:532-539), and saveMutation.onError's
  // isReAuthCancelled branch calls the same revertPathsSsrfToServer() — but
  // had zero re-auth-cancel-revert coverage of its own.
  it('cancelling re-auth on an SSRF entry delete restores the deleted entry from the saved server list', async () => {
    // Two entries that match no SSRF_PRESETS (lengths are 0/2/6), so Advanced
    // mode auto-expands on mount and both entries are visible without
    // clicking the "Advanced (custom list)" toggle first.
    vi.mocked(fetchSandboxConfig).mockResolvedValue({
      ...baseConfig,
      ssrf: { allow_internal: ['127.0.0.1', '10.0.0.0/8'] },
    })
    vi.mocked(updateSandboxConfig).mockRejectedValue(reAuth403())

    renderSection()

    await waitFor(() => {
      expect(screen.getByText('127.0.0.1')).toBeInTheDocument()
      expect(screen.getByText('10.0.0.0/8')).toBeInTheDocument()
    })

    fireEvent.click(screen.getByRole('button', { name: /delete ssrf entry 10\.0\.0\.0\/8/i }))

    // Optimistic delete: the entry disappears immediately, before the PUT settles.
    await waitFor(() => {
      expect(screen.queryByText('10.0.0.0/8')).not.toBeInTheDocument()
    })

    // First PUT (token '') 403s on the re-auth gate -> consent dialog opens.
    await waitFor(() => {
      expect(screen.getByTestId('reauth-cancel')).toBeInTheDocument()
    })

    // User dismisses the password prompt instead of confirming.
    fireEvent.click(screen.getByTestId('reauth-cancel'))

    await waitFor(() => {
      expect(screen.queryByTestId('reauth-cancel')).not.toBeInTheDocument()
    })

    // The optimistically deleted '10.0.0.0/8' entry must reappear — the
    // delete was never persisted — while the untouched entry stays put.
    await waitFor(() => {
      expect(screen.getByText('10.0.0.0/8')).toBeInTheDocument()
      expect(screen.getByText('127.0.0.1')).toBeInTheDocument()
    })

    // No error toast for a user-initiated cancel, and no retry attempt fired.
    expect(mockAddToast).not.toHaveBeenCalled()
    expect(updateSandboxConfig).toHaveBeenCalledTimes(1)
  })
})

// ── describe: shell workspace limit (ADR-068 §6) ─────────────────────────────
//
// The defect this control closes (UAT 002) is an operator turning the kernel
// sandbox off, still being blocked by restrict_to_workspace, and finding
// nothing on the page that names the rule. Two things therefore have to hold
// and both are asserted here: the control exists and really writes the
// setting, and — when the gateway does not report the setting — the page says
// so instead of drawing a control that would look live and do nothing.

// Builds a config response carrying restrict_to_workspace. The cast is
// deliberate: the field is not in the generated SandboxConfigResponse type
// yet (contracts/components/schemas/SandboxConfig.yaml does not declare it),
// which is exactly the situation the component reads defensively for.
function configWithWorkspaceLimit(value: unknown): SandboxConfigResponse {
  return { ...baseConfig, restrict_to_workspace: value } as SandboxConfigResponse
}

describe('shell workspace limit', () => {
  it('restrict_to_workspace=true → the "confined" option is pre-selected and no unavailable notice shows', async () => {
    vi.mocked(fetchSandboxConfig).mockResolvedValue(configWithWorkspaceLimit(true))

    renderSection()

    await waitFor(() => {
      expect(screen.getByTestId('sandbox-workspace-limit-on')).toBeChecked()
    })
    expect(screen.getByTestId('sandbox-workspace-limit-off')).not.toBeChecked()
    expect(screen.queryByTestId('workspace-limit-unavailable')).not.toBeInTheDocument()
  })

  it('restrict_to_workspace=false → the "any path" option is pre-selected (false is a real value, not a missing one)', async () => {
    vi.mocked(fetchSandboxConfig).mockResolvedValue(configWithWorkspaceLimit(false))

    renderSection()

    await waitFor(() => {
      expect(screen.getByTestId('sandbox-workspace-limit-off')).toBeChecked()
    })
    expect(screen.getByTestId('sandbox-workspace-limit-on')).not.toBeChecked()
    expect(screen.queryByTestId('workspace-limit-unavailable')).not.toBeInTheDocument()
  })

  it('switching to "any path" PUTs exactly {restrict_to_workspace: false} and nothing else', async () => {
    vi.mocked(fetchSandboxConfig).mockResolvedValue(configWithWorkspaceLimit(true))
    vi.mocked(updateSandboxConfig).mockResolvedValue(configWithWorkspaceLimit(false))

    renderSection()

    await waitFor(() => {
      expect(screen.getByTestId('sandbox-workspace-limit-on')).toBeChecked()
    })
    fireEvent.click(screen.getByTestId('sandbox-workspace-limit-off'))

    await waitFor(() => {
      expect(updateSandboxConfig).toHaveBeenCalledTimes(1)
    })
    // toEqual, not toMatchObject: a partial PUT that also carried `mode` or
    // `filesystem_model` would silently re-save whatever the radios happened
    // to be showing, so the body must contain this key alone.
    expect(vi.mocked(updateSandboxConfig).mock.calls[0][0]).toEqual({ restrict_to_workspace: false })
  })

  it('switching back to "confined" PUTs exactly {restrict_to_workspace: true}', async () => {
    vi.mocked(fetchSandboxConfig).mockResolvedValue(configWithWorkspaceLimit(false))
    vi.mocked(updateSandboxConfig).mockResolvedValue(configWithWorkspaceLimit(true))

    renderSection()

    await waitFor(() => {
      expect(screen.getByTestId('sandbox-workspace-limit-off')).toBeChecked()
    })
    fireEvent.click(screen.getByTestId('sandbox-workspace-limit-on'))

    await waitFor(() => {
      expect(updateSandboxConfig).toHaveBeenCalledTimes(1)
    })
    expect(vi.mocked(updateSandboxConfig).mock.calls[0][0]).toEqual({ restrict_to_workspace: true })
  })

  it('clicking the already-selected option fires no PUT', async () => {
    vi.mocked(fetchSandboxConfig).mockResolvedValue(configWithWorkspaceLimit(true))

    renderSection()

    await waitFor(() => {
      expect(screen.getByTestId('sandbox-workspace-limit-on')).toBeChecked()
    })
    fireEvent.click(screen.getByTestId('sandbox-workspace-limit-on'))

    expect(updateSandboxConfig).not.toHaveBeenCalled()
  })

  // ── Degraded state: the backend does not expose the field ──────────────────

  it('field absent from the response → no radios at all, and an explanation naming the environment variable', async () => {
    // baseConfig is the shipped shape today: no restrict_to_workspace key.
    vi.mocked(fetchSandboxConfig).mockResolvedValue(baseConfig)

    renderSection()

    await waitFor(() => {
      expect(screen.getByTestId('workspace-limit-unavailable')).toBeInTheDocument()
    })

    // Nothing that looks like a working control is drawn.
    expect(screen.queryByTestId('sandbox-workspace-limit-on')).not.toBeInTheDocument()
    expect(screen.queryByTestId('sandbox-workspace-limit-off')).not.toBeInTheDocument()

    // The operator is told why, and where the setting actually lives.
    const notice = screen.getByTestId('workspace-limit-unavailable')
    expect(notice).toHaveTextContent(/cannot be changed from here/i)
    expect(notice).toHaveTextContent('OMNIPUS_AGENTS_DEFAULTS_RESTRICT_TO_WORKSPACE')

    // The heading still renders, so the boundary is at least named on the page
    // even when it cannot be edited — that naming is half the defect.
    expect(screen.getByText('Shell workspace limit')).toBeInTheDocument()
  })

  it('field present but not a boolean → treated as absent, never coerced into a checked radio', async () => {
    // A backend sending the string "true" (or null) must not produce a control
    // showing a value the server never actually asserted.
    vi.mocked(fetchSandboxConfig).mockResolvedValue(configWithWorkspaceLimit('true'))

    renderSection()

    await waitFor(() => {
      expect(screen.getByTestId('workspace-limit-unavailable')).toBeInTheDocument()
    })
    expect(screen.queryByTestId('sandbox-workspace-limit-on')).not.toBeInTheDocument()
    expect(screen.queryByTestId('sandbox-workspace-limit-off')).not.toBeInTheDocument()
  })

  it('null value → treated as absent', async () => {
    vi.mocked(fetchSandboxConfig).mockResolvedValue(configWithWorkspaceLimit(null))

    renderSection()

    await waitFor(() => {
      expect(screen.getByTestId('workspace-limit-unavailable')).toBeInTheDocument()
    })
    expect(screen.queryByTestId('sandbox-workspace-limit-on')).not.toBeInTheDocument()
  })

  // ── Failure paths ──────────────────────────────────────────────────────────

  it('cancelling re-auth reverts the optimistic selection, and the change can be retried afterwards', async () => {
    vi.mocked(fetchSandboxConfig).mockResolvedValue(configWithWorkspaceLimit(true))
    vi.mocked(updateSandboxConfig).mockRejectedValue(reAuth403())

    renderSection()

    await waitFor(() => {
      expect(screen.getByTestId('sandbox-workspace-limit-on')).toBeChecked()
    })
    fireEvent.click(screen.getByTestId('sandbox-workspace-limit-off'))

    // Optimistic: the new choice shows before the PUT settles.
    await waitFor(() => {
      expect(screen.getByTestId('sandbox-workspace-limit-off')).toBeChecked()
    })

    await waitFor(() => {
      expect(screen.getByTestId('reauth-cancel')).toBeInTheDocument()
    })
    fireEvent.click(screen.getByTestId('reauth-cancel'))

    // Reverted to the saved server value — otherwise the radio sits on an
    // unsaved value with no error shown, and the equality guard in the handler
    // means re-clicking it would not even refire onChange.
    await waitFor(() => {
      expect(screen.getByTestId('sandbox-workspace-limit-on')).toBeChecked()
    })
    expect(screen.getByTestId('sandbox-workspace-limit-off')).not.toBeChecked()
    expect(mockAddToast).not.toHaveBeenCalled()
    expect(updateSandboxConfig).toHaveBeenCalledTimes(1)

    // Recovery: the same change must be attemptable again.
    vi.mocked(updateSandboxConfig).mockResolvedValueOnce(configWithWorkspaceLimit(false))
    fireEvent.click(screen.getByTestId('sandbox-workspace-limit-off'))
    await waitFor(() => {
      expect(updateSandboxConfig).toHaveBeenCalledTimes(2)
    })
  })

  it('a server error reverts the selection and raises a toast', async () => {
    vi.mocked(fetchSandboxConfig).mockResolvedValue(configWithWorkspaceLimit(true))
    vi.mocked(updateSandboxConfig).mockRejectedValue(new ApiError(500, 'boom'))

    renderSection()

    await waitFor(() => {
      expect(screen.getByTestId('sandbox-workspace-limit-on')).toBeChecked()
    })
    fireEvent.click(screen.getByTestId('sandbox-workspace-limit-off'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith(
        expect.objectContaining({ variant: 'error' }),
      )
    })
    expect(screen.getByTestId('sandbox-workspace-limit-on')).toBeChecked()
    expect(screen.getByTestId('sandbox-workspace-limit-off')).not.toBeChecked()
  })
})

// ── describe: telling the two boundaries apart ───────────────────────────────

describe('sandbox vs workspace-limit copy', () => {
  it('the workspace limit is presented as a separate boundary, not as part of the sandbox mode group', async () => {
    vi.mocked(fetchSandboxConfig).mockResolvedValue(configWithWorkspaceLimit(true))

    renderSection()

    await waitFor(() => {
      expect(screen.getByTestId('workspace-limit-section')).toBeInTheDocument()
    })

    const section = screen.getByTestId('workspace-limit-section')
    // Its own radio group name — a click here can never move the sandbox mode.
    expect(screen.getByTestId('sandbox-workspace-limit-on')).toHaveAttribute('name', 'workspace-limit')
    // And the copy states the difference rather than leaving the reader to
    // infer it from two similarly-worded switches.
    expect(section).toHaveTextContent(/different boundary from the sandbox above/i)
    expect(section).toHaveTextContent(/turning the sandbox off does not turn this off/i)
  })

  it('the "off" sandbox mode says plainly that it does not lift the workspace limit', async () => {
    renderSection()

    await waitFor(() => {
      expect(screen.getByRole('radio', { name: /sandbox mode: off/i })).toBeInTheDocument()
    })

    // This sentence is the whole fix for the "I turned it off and I am still
    // blocked" report — assert it, so it cannot be edited away silently.
    expect(
      screen.getByText(/does not switch off the shell workspace limit/i),
    ).toBeInTheDocument()
  })
})

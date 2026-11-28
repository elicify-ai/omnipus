/**
 * SandboxSection tests — Settings redesign #1519 reconciliation (FR-010).
 *
 * Covers:
 * - Allowed folders editor (drafts; Add/Delete stage, Save stores — FR-010/FR-011/FR-015).
 * - Internal network addresses editor (presets stage drafts; the wildcard
 *   add asks first, per the wireframe row "asks you to confirm first"; FR-010/FR-016).
 * - ABI v4 surfaces (read oracles, unchanged by the Save bar).
 * - Sandbox mode radios (the kernel warning runs at pick time and gates the
 *   draft; the Enforce draft counts change, password and restart — FR-010/FR-011/FR-025/FR-037).
 * - The section Save bar replaces the retired per-change "Save the sandbox
 *   configuration?" confirm (FR-010; the wireframe draws no such dialog).
 * - Discard returns drafts to the saved values (FR-012), preserving the #142
 *   property that a control never sticks on an unsaved target and a change
 *   can always be re-attempted.
 * - Shell workspace limit (drafts; refused Save keeps the typed value — FR-015/FR-020).
 *
 * The old per-change immediate-save oracles ("Add fires updateSandboxConfig
 * immediately", optimistic write + confirm-cancel revert, error toast +
 * revert) are replaced by the spec's one save rule; every replaced assertion
 * cites the FR that replaces it.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor, within, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

// ── Module mocks ──────────────────────────────────────────────────────────────

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchSandboxStatus: vi.fn(),
    fetchSandboxConfig: vi.fn(),
    updateSandboxConfig: vi.fn(),
    fetchAppState: vi.fn(),
  }
})

const mockAddToast = vi.fn()
vi.mock('@/store/ui', () => ({
  useUiStore: vi.fn(() => ({ addToast: mockAddToast })),
}))

import { fetchSandboxStatus, fetchSandboxConfig, updateSandboxConfig, fetchAppState, ApiError } from '@/lib/api'
import { SandboxSection } from './SandboxSection'
import type { SandboxStatus, SandboxConfigResponse, AppState } from '@/lib/api'

// Platform mode (identity.mode: 'platform') pins useStepUp() to 'confirm' —
// ConfirmDialog, no consent token (ADR-0010 WP3). The password-mode (local
// edition) counterpart lives in SandboxSection.password.test.tsx.
const PLATFORM_APP_STATE = {
  onboarding_complete: true,
  identity: { mode: 'platform', edition: 'hosted', signed_in: true },
} as AppState

// ── Helpers ───────────────────────────────────────────────────────────────────

function makeClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
}

// The shell's password prompt is the FR-026 one-per-Save prompt (shell-owned).
// SettingsShellContext ships with the Save bar, so it is absent from the
// pre-change tree — a Save that must pass the prompt loads it lazily and
// fails loudly as BLOCKED RED, never as a collection error.
let shellCtxModule: (typeof import('./SettingsShellContext')) | null = null
async function loadShellContext() {
  if (shellCtxModule) return shellCtxModule
  try {
    shellCtxModule = await vi.importActual<typeof import('./SettingsShellContext')>('./SettingsShellContext')
    return shellCtxModule
  } catch {
    throw new Error(
      'BLOCKED RED (FR-010/FR-026/FR-027, D-8 row 4): the Settings shell context (SettingsShellContext) does not ' +
        'exist yet — required for the one password prompt per Save (FR-026) and the consent-token write (FR-027)',
    )
  }
}

// FR-026 recorder: one prompt per Save, listing every password-marked value
// of the Save by its visible label; resolves with the single-use token
// (FR-027) that the one protected write must carry.
function promptRecorder() {
  const prompts: string[][] = []
  const requestPassword = vi.fn(async (labels: string[]) => {
    prompts.push(labels)
    return 'reauth-tok'
  })
  return { prompts, requestPassword }
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

/** Like renderSection(), but the section saves under the real shell contract. */
async function renderSectionWithShell(requestPassword: (labels: string[]) => Promise<string | null>) {
  const { SettingsShellContext } = await loadShellContext()
  const client = makeClient()
  return render(
    <QueryClientProvider client={client}>
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
        <SandboxSection />
      </SettingsShellContext.Provider>
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
  vi.mocked(fetchAppState).mockResolvedValue(PLATFORM_APP_STATE)
  // Reset localStorage/sessionStorage
  localStorage.clear()
  sessionStorage.clear()
})

// ── describe: allowed folders editor ──────────────────────────────────────────

describe('allowed folders editor', () => {
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

  // The wireframe renames this block to "Allowed folders" and FR-025 names
  // the row the same way; the old "Filesystem paths the sandbox may read"
  // heading is replaced by the approved wording.
  it('shows the Allowed folders heading (FR-025 names the row)', async () => {
    renderSection()

    await waitFor(() => {
      expect(screen.getByText('Allowed folders')).toBeInTheDocument()
    })
  })

  it('no Edit button for the folders editor (Add/Delete stage drafts — nothing commits until Save changes, FR-010)', async () => {
    renderSection()

    await waitFor(() => {
      expect(screen.getByText('Allowed folders')).toBeInTheDocument()
    })

    // No edit button — the editor is always visible for admin; FR-010 puts
    // the commit point on the Save bar, not behind an Edit mode.
    expect(screen.queryByRole('button', { name: /^edit$/i })).not.toBeInTheDocument()
  })

  it('typing counts nothing until Add; Add stages the draft and stores nothing; Save changes sends exactly the changed field (FR-010, FR-011, FR-015)', async () => {
    vi.mocked(fetchSandboxConfig).mockResolvedValue({
      ...baseConfig,
      allowed_paths: ['/a', '/b'],
    })
    const { prompts, requestPassword } = promptRecorder()
    await renderSectionWithShell(requestPassword)

    await waitFor(() => {
      expect(screen.getByRole('textbox', { name: /new allowed path/i })).toBeInTheDocument()
    })

    // FR-011: text in an "Add …" field counts only after Add is pressed.
    const input = screen.getByRole('textbox', { name: /new allowed path/i })
    fireEvent.change(input, { target: { value: '/c' } })
    expect(screen.queryByText(/unsaved change/)).not.toBeInTheDocument()
    expect(updateSandboxConfig).not.toHaveBeenCalled()

    // Add stages the draft (FR-010: nothing is stored yet).
    fireEvent.click(screen.getByRole('button', { name: /add path/i }))
    expect(await screen.findByText('/c')).toBeInTheDocument()
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    expect(updateSandboxConfig).not.toHaveBeenCalled()

    // Save changes stores it: one prompt (FR-026), one partial write with the
    // consent token (FR-015/FR-027).
    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    await waitFor(() => expect(prompts.length).toBe(1))
    expect(prompts[0]).toEqual(['Allowed folders'])
    await waitFor(() => {
      expect(vi.mocked(updateSandboxConfig).mock.calls[0]).toEqual([
        { allowed_paths: ['/a', '/b', '/c'] },
        'reauth-tok',
      ])
    })
    // FR-018: the saved signal replaces the old success toast.
    await screen.findByText(/Saved at/)
  })

  it.each([
    { survivor: 'none', hasModeDraft: false },
    { survivor: 'one mode change', hasModeDraft: true },
  ])('a refused new path returns to its own Add input; Save-bar survivor: $survivor (FR-015a)', async ({ hasModeDraft }) => {
    vi.mocked(fetchSandboxConfig).mockResolvedValue({
      ...baseConfig,
      allowed_paths: ['/saved'],
    })
    const reason = 'must be absolute — `./foo` is relative'
    vi.mocked(updateSandboxConfig).mockRejectedValue(new ApiError(400, `allowed_paths[1]: ${reason}`))
    const { requestPassword } = promptRecorder()
    await renderSectionWithShell(requestPassword)

    const input = await screen.findByRole('textbox', { name: /new allowed path/i })
    fireEvent.change(input, { target: { value: './foo' } })
    fireEvent.click(screen.getByRole('button', { name: /add path/i }))
    if (hasModeDraft) fireEvent.click(screen.getByRole('radio', { name: /sandbox mode: off/i }))
    // Two distinct changed fields, not two entries in the same field: the
    // count must drop from two to one when the refused path is recovered.
    expect(screen.getByText(hasModeDraft ? '2 unsaved changes' : '1 unsaved change')).toBeVisible()
    expect(updateSandboxConfig).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    await waitFor(() => expect(updateSandboxConfig).toHaveBeenCalledTimes(1))
    expect(vi.mocked(updateSandboxConfig).mock.calls[0]).toEqual([
      hasModeDraft ? { allowed_paths: ['/saved', './foo'], mode: 'off' } : { allowed_paths: ['/saved', './foo'] },
      'reauth-tok',
    ])

    // FR-015a keeps all typed content but moves this unsaved new entry back
    // into its own list's Add input, with the server's literal reason there.
    const addArea = input.parentElement!.parentElement!
    expect(await within(addArea).findByText(reason, { exact: false })).toBeVisible()
    expect(input, 'FR-015a: no typed path is lost during recovery').toHaveValue('./foo')
    expect(screen.queryByRole('button', { name: 'Delete path ./foo' })).toBeNull()
    expect(screen.getAllByRole('button', { name: /^Delete path / }).map(button => button.getAttribute('aria-label'))).toEqual([
      'Delete path /saved',
    ])
    expect(screen.queryByText(/^Saved at /), 'FR-020: a refused value must never look saved').toBeNull()
    if (hasModeDraft) {
      expect(screen.getByRole('radio', { name: /sandbox mode: off/i }), 'FR-015a: the other draft survives recovery').toBeChecked()
      const bar = screen.getByTestId('settings-save-bar')
      expect(within(bar).getByText(/changes? could not be saved/).textContent, 'FR-015a Q2 A: recovered input is excluded; only the mode draft still failed').toBe('1 change could not be saved')
    } else {
      expect(screen.queryByTestId('settings-save-bar'), 'FR-015a Q2 A: no staged draft remains, so the bar closes').toBeNull()
    }
    expect(within(addArea).getByText(reason, { exact: false }), 'FR-015a: the input message remains after the bar settles').toBeVisible()
    expect(updateSandboxConfig, 'FR-010/015a: refusal and recovery never cause an automatic resend').toHaveBeenCalledTimes(1)
  })

  it('Delete stages the draft and stores nothing; Save changes sends exactly the surviving list (FR-010, FR-015)', async () => {
    vi.mocked(fetchSandboxConfig).mockResolvedValue({
      ...baseConfig,
      allowed_paths: ['/a', '/b'],
    })
    const { prompts, requestPassword } = promptRecorder()
    await renderSectionWithShell(requestPassword)

    await waitFor(() => {
      expect(screen.getByText('/a')).toBeInTheDocument()
    })

    // Delete first path — it leaves the draft list at once.
    const deleteBtn = screen.getByRole('button', { name: /delete path \/a/i })
    fireEvent.click(deleteBtn)

    await waitFor(() => {
      expect(screen.queryByText('/a')).not.toBeInTheDocument()
      expect(screen.getByText('/b')).toBeInTheDocument()
    })
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    expect(updateSandboxConfig).not.toHaveBeenCalled()

    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    await waitFor(() => expect(prompts.length).toBe(1))
    expect(prompts[0]).toEqual(['Allowed folders'])
    await waitFor(() => {
      expect(vi.mocked(updateSandboxConfig).mock.calls[0]).toEqual([
        { allowed_paths: ['/b'] },
        'reauth-tok',
      ])
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

// ── describe: internal network addresses editor ───────────────────────────────

describe('internal network addresses editor', () => {
  const RFC1918_LIST = ['127.0.0.1', '::1', '10.0.0.0/8', '172.16.0.0/12', '192.168.0.0/16', 'fc00::/7']
  const LOOPBACK_LIST = ['127.0.0.1', '::1']

  it('clicking "Allow RFC1918 + loopback" stages the preset draft and stores nothing; Save changes sends the exact list (FR-010, FR-015)', async () => {
    const { prompts, requestPassword } = promptRecorder()
    await renderSectionWithShell(requestPassword)

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /allow rfc1918 \+ loopback/i })).toBeInTheDocument()
    })

    // The preset click stages the draft (FR-010: nothing is stored yet).
    fireEvent.click(screen.getByRole('button', { name: /allow rfc1918 \+ loopback/i }))
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    expect(updateSandboxConfig).not.toHaveBeenCalled()

    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    await waitFor(() => expect(prompts.length).toBe(1))
    expect(prompts[0]).toEqual(['Internal network addresses'])
    await waitFor(() => {
      expect(vi.mocked(updateSandboxConfig).mock.calls[0]).toEqual([
        { ssrf: { allow_internal: RFC1918_LIST } },
        'reauth-tok',
      ])
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

  // The wireframe's Add-an-address row: "Adding 0.0.0.0/0 or ::/0 (every
  // address) turns off this protection and asks you to confirm first." The
  // confirm now gates the DRAFT (FR-010: nothing is stored until Save
  // changes), and it still runs before any write (FR-016).
  it('adding 0.0.0.0/0 asks first; confirming stages the draft; only Save changes stores it (FR-010, FR-016)', async () => {
    const { prompts, requestPassword } = promptRecorder()
    await renderSectionWithShell(requestPassword)

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /advanced \(custom list\)/i })).toBeInTheDocument()
    })

    const advancedToggle = screen.getByRole('button', { name: /advanced \(custom list\)/i })
    fireEvent.click(advancedToggle)

    const input = screen.getByRole('textbox', { name: /new ssrf allow entry/i })
    fireEvent.change(input, { target: { value: '0.0.0.0/0' } })
    fireEvent.click(screen.getByRole('button', { name: /add ssrf entry/i }))

    // The confirm runs first — nothing is staged or sent yet (FR-016).
    const dialog = await screen.findByRole('alertdialog')
    expect(dialog).toHaveTextContent(/disable ssrf protection/i)
    expect(screen.queryByText(/unsaved change/)).not.toBeInTheDocument()
    expect(updateSandboxConfig).not.toHaveBeenCalled()

    // Confirming makes it a draft — still nothing stored (FR-010).
    fireEvent.click(screen.getByRole('button', { name: /allow every address/i }))
    expect(await screen.findByText('0.0.0.0/0')).toBeInTheDocument()
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    expect(updateSandboxConfig).not.toHaveBeenCalled()

    // Save changes is the only path to a write (FR-010/FR-027).
    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    await waitFor(() => expect(prompts.length).toBe(1))
    expect(prompts[0]).toEqual(['Internal network addresses'])
    await waitFor(() => {
      expect(vi.mocked(updateSandboxConfig).mock.calls[0]).toEqual([
        { ssrf: { allow_internal: ['0.0.0.0/0'] } },
        'reauth-tok',
      ])
    })
  })

  it('cancelling the wildcard confirm stages nothing and stores nothing (FR-016: Cancel stores nothing)', async () => {
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
      expect(screen.getByRole('alertdialog')).toBeInTheDocument()
    })

    fireEvent.click(screen.getByRole('button', { name: /^cancel$/i }))

    await waitFor(() => {
      expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    })

    // The entry never became a draft and nothing was stored (FR-011: the bar
    // stays hidden with zero changes).
    expect(screen.queryByText(/unsaved change/)).not.toBeInTheDocument()
    expect(updateSandboxConfig).not.toHaveBeenCalled()
  })

  it('clicking "Block all" stages the empty-list draft and stores nothing; Save changes sends it (FR-010, FR-015)', async () => {
    vi.mocked(fetchSandboxConfig).mockResolvedValue({
      ...baseConfig,
      ssrf: { allow_internal: LOOPBACK_LIST },
    })
    const { prompts, requestPassword } = promptRecorder()
    await renderSectionWithShell(requestPassword)

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /block all/i })).toBeInTheDocument()
    })

    fireEvent.click(screen.getByRole('button', { name: /block all/i }))
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    expect(updateSandboxConfig).not.toHaveBeenCalled()

    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    await waitFor(() => expect(prompts.length).toBe(1))
    expect(prompts[0]).toEqual(['Internal network addresses'])
    await waitFor(() => {
      expect(vi.mocked(updateSandboxConfig).mock.calls[0]).toEqual([
        { ssrf: { allow_internal: [] } },
        'reauth-tok',
      ])
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

// ── describe: sandbox mode radios (drafts — FR-010) ───────────────────────────

describe('sandbox mode radios', () => {
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

    // No Edit button for the mode group — the editor is always visible;
    // FR-010 puts the commit point on the Save bar.
    expect(screen.queryByRole('button', { name: /edit sandbox mode/i })).not.toBeInTheDocument()
  })

  it('picking Enforce asks the kernel-compatibility warning first; confirming stages the draft; only Save changes stores it (FR-010, FR-011, FR-016)', async () => {
    vi.mocked(fetchSandboxStatus).mockResolvedValue({
      ...baseStatus,
      abi_version: 4,
      issue_ref: '#138',
    })
    vi.mocked(fetchSandboxConfig).mockResolvedValue({
      ...baseConfig,
      mode: 'permissive',
    })
    const { prompts, requestPassword } = promptRecorder()
    await renderSectionWithShell(requestPassword)

    await waitFor(() => {
      expect(screen.getByRole('radio', { name: /sandbox mode: enforce/i })).toBeInTheDocument()
    })
    fireEvent.click(screen.getByRole('radio', { name: /sandbox mode: enforce/i }))

    // The warning runs at pick time — nothing staged or sent yet (FR-016).
    const dialog = await screen.findByRole('alertdialog')
    expect(dialog).toHaveTextContent(/kernel incompatibility/i)
    expect(screen.queryByText(/unsaved change/)).not.toBeInTheDocument()
    expect(updateSandboxConfig).not.toHaveBeenCalled()

    // Confirming makes it a draft; the bar counts change, password (FR-025)
    // and restart (FR-037) — still nothing stored (FR-010).
    fireEvent.click(screen.getByRole('button', { name: /choose enforce anyway/i }))
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    expect(screen.getByText(/1 needs your password/)).toBeInTheDocument()
    expect(screen.getByText(/1 needs a gateway restart/)).toBeInTheDocument()
    expect(updateSandboxConfig).not.toHaveBeenCalled()

    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    await waitFor(() => expect(prompts.length).toBe(1))
    expect(prompts[0]).toEqual(['Sandbox mode'])
    await waitFor(() => {
      expect(vi.mocked(updateSandboxConfig).mock.calls[0]).toEqual([
        { mode: 'enforce' },
        'reauth-tok',
      ])
    })
  })

  it('changing mode from off → permissive stages the draft and stores nothing; Save changes sends exactly {mode: "permissive"} (FR-010, FR-015)', async () => {
    vi.mocked(fetchSandboxConfig).mockResolvedValue({
      ...baseConfig,
      mode: 'off',
    })
    vi.mocked(updateSandboxConfig).mockResolvedValue({ ...baseConfig, mode: 'permissive', requires_restart: true })
    const { prompts, requestPassword } = promptRecorder()
    await renderSectionWithShell(requestPassword)

    await waitFor(() => {
      expect(screen.getByRole('radio', { name: /sandbox mode: permissive/i })).toBeInTheDocument()
    })
    // Only the Enforce pick asks first — Permissive stages its draft at once.
    fireEvent.click(screen.getByRole('radio', { name: /sandbox mode: permissive/i }))
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    expect(updateSandboxConfig).not.toHaveBeenCalled()

    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    await waitFor(() => expect(prompts.length).toBe(1))
    expect(prompts[0]).toEqual(['Sandbox mode'])
    await waitFor(() => {
      expect(vi.mocked(updateSandboxConfig).mock.calls[0]).toEqual([
        { mode: 'permissive' },
        'reauth-tok',
      ])
    })
  })

  it('cancelling the kernel warning leaves no draft; the change can be re-attempted (FR-011, FR-016; the #142 revert property)', async () => {
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
      expect(screen.getByRole('alertdialog')).toBeInTheDocument()
    })

    // Cancel — the footer's first button (the wireframe names it
    // "Keep Permissive"; the assert is positional so it pins the behaviour,
    // not one wording of it).
    const dialog = screen.getByRole('alertdialog')
    fireEvent.click(within(dialog).getAllByRole('button')[0])

    await waitFor(() => {
      expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    })

    // No draft was staged and nothing was stored (FR-011: the bar stays
    // hidden with zero changes; the radio shows the saved mode).
    expect(screen.queryByText(/unsaved change/)).not.toBeInTheDocument()
    expect(updateSandboxConfig).not.toHaveBeenCalled()
    await waitFor(() => {
      expect(screen.getByRole('radio', { name: /sandbox mode: permissive/i })).toBeChecked()
      expect(screen.getByRole('radio', { name: /sandbox mode: enforce/i })).not.toBeChecked()
    })

    // Recovery: picking Enforce again asks again — the control never sticks
    // on a dead end (the property bug #142 pinned, under the draft model).
    fireEvent.click(screen.getByRole('radio', { name: /sandbox mode: enforce/i }))
    expect(await screen.findByRole('alertdialog')).toHaveTextContent(/kernel incompatibility/i)
    expect(updateSandboxConfig).not.toHaveBeenCalled()
  })
})

// ── describe: the section Save (the per-change confirm is retired — FR-010) ──
//
// ADR-0008 ruling 6's per-change "Save the sandbox configuration?" confirm is
// replaced by the section's one Save bar: a change is a draft (FR-010), and
// the wireframe draws no per-change dialog for this section.

describe('the section Save replaces the per-change confirm (FR-010)', () => {
  it('Add stages a draft with no per-change confirm dialog (FR-010)', async () => {
    vi.mocked(fetchSandboxConfig).mockResolvedValue({
      ...baseConfig,
      allowed_paths: ['/a'],
    })

    renderSection()

    await waitFor(() => {
      expect(screen.getByRole('textbox', { name: /new allowed path/i })).toBeInTheDocument()
    })

    fireEvent.change(screen.getByRole('textbox', { name: /new allowed path/i }), { target: { value: '/valid' } })
    fireEvent.click(screen.getByRole('button', { name: /add path/i }))

    // The draft is staged on the bar; no per-change dialog opens and nothing
    // is stored (FR-010/FR-011).
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(updateSandboxConfig).not.toHaveBeenCalled()
  })

  it('Save changes sends exactly the changed field, carrying the consent token, and shows the saved signal (FR-015, FR-026, FR-027, FR-018)', async () => {
    vi.mocked(fetchSandboxConfig).mockResolvedValue({
      ...baseConfig,
      allowed_paths: ['/a'],
    })
    vi.mocked(updateSandboxConfig).mockResolvedValue({
      ...baseConfig,
      allowed_paths: ['/a', '/valid'],
      requires_restart: true,
    })
    const { prompts, requestPassword } = promptRecorder()
    await renderSectionWithShell(requestPassword)

    await waitFor(() => {
      expect(screen.getByRole('textbox', { name: /new allowed path/i })).toBeInTheDocument()
    })

    fireEvent.change(screen.getByRole('textbox', { name: /new allowed path/i }), { target: { value: '/valid' } })
    fireEvent.click(screen.getByRole('button', { name: /add path/i }))
    fireEvent.click(await screen.findByRole('button', { name: /save changes/i }))

    await waitFor(() => expect(prompts.length).toBe(1))
    expect(prompts[0]).toEqual(['Allowed folders'])
    await waitFor(() => {
      expect(vi.mocked(updateSandboxConfig).mock.calls).toEqual([
        [{ allowed_paths: ['/a', '/valid'] }, 'reauth-tok'],
      ])
    })
    await screen.findByText(/Saved at/)
  })
})

// ── describe: Discard returns drafts to the saved values (FR-012) ────────────
//
// The old suite proved the same user-facing property through the retired
// optimistic-write + confirm-cancel machinery (bug #142): a control must
// never sit on an unsaved target with no way back, and a change must stay
// re-attemptable. Under the draft model that property is Discard (FR-012).

describe('Discard returns the drafts to the saved values (FR-012; the #142 revert property)', () => {
  it('discarding a mode change returns the radio to the saved mode, and the change can be re-attempted', async () => {
    vi.mocked(fetchSandboxConfig).mockResolvedValue({
      ...baseConfig,
      mode: 'permissive',
    })

    renderSection()

    await waitFor(() => {
      expect(screen.getByRole('radio', { name: /sandbox mode: off/i })).toBeInTheDocument()
    })
    expect(screen.getByRole('radio', { name: /sandbox mode: permissive/i })).toBeChecked()

    // The draft shows immediately (FR-010: nothing is stored).
    fireEvent.click(screen.getByRole('radio', { name: /sandbox mode: off/i }))
    await waitFor(() => {
      expect(screen.getByRole('radio', { name: /sandbox mode: off/i })).toBeChecked()
    })
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    expect(updateSandboxConfig).not.toHaveBeenCalled()

    // Discard returns the draft to the last saved value (FR-012).
    fireEvent.click(screen.getByRole('button', { name: /discard/i }))
    await waitFor(() => {
      expect(screen.getByRole('radio', { name: /sandbox mode: permissive/i })).toBeChecked()
      expect(screen.getByRole('radio', { name: /sandbox mode: off/i })).not.toBeChecked()
    })
    expect(screen.queryByText(/unsaved change/)).not.toBeInTheDocument()
    expect(mockAddToast).not.toHaveBeenCalled()
    expect(updateSandboxConfig).not.toHaveBeenCalled()

    // Recovery: the same change is attemptable again.
    fireEvent.click(screen.getByRole('radio', { name: /sandbox mode: off/i }))
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    expect(updateSandboxConfig).not.toHaveBeenCalled()
  })

  it('discarding an allowed-folder add removes the drafted row and keeps the saved list', async () => {
    vi.mocked(fetchSandboxConfig).mockResolvedValue({
      ...baseConfig,
      allowed_paths: ['/a'],
    })

    renderSection()

    await waitFor(() => {
      expect(screen.getByText('/a')).toBeInTheDocument()
    })

    fireEvent.change(screen.getByRole('textbox', { name: /new allowed path/i }), { target: { value: '/valid' } })
    fireEvent.click(screen.getByRole('button', { name: /add path/i }))

    // The drafted row renders at once.
    await waitFor(() => {
      expect(screen.getByText('/valid')).toBeInTheDocument()
    })
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /discard/i }))

    // The drafted '/valid' row is gone — it was never persisted — while the
    // saved '/a' stays put (FR-012).
    await waitFor(() => {
      expect(screen.queryByText('/valid')).not.toBeInTheDocument()
      expect(screen.getByText('/a')).toBeInTheDocument()
    })
    expect(screen.queryByText(/unsaved change/)).not.toBeInTheDocument()
    expect(mockAddToast).not.toHaveBeenCalled()
    expect(updateSandboxConfig).not.toHaveBeenCalled()
  })

  it('discarding an internal-address add removes the drafted entry and keeps the saved list', async () => {
    // A single-entry list matches no presets (lengths are 0/2/6), so Advanced
    // mode auto-expands on mount and '127.0.0.1' is visible without needing
    // to click the "Advanced (custom list)" toggle first.
    vi.mocked(fetchSandboxConfig).mockResolvedValue({
      ...baseConfig,
      ssrf: { allow_internal: ['127.0.0.1'] },
    })

    renderSection()

    await waitFor(() => {
      expect(screen.getByText('127.0.0.1')).toBeInTheDocument()
    })

    const input = screen.getByRole('textbox', { name: /new ssrf allow entry/i })
    fireEvent.change(input, { target: { value: '10.0.0.0/8' } })
    fireEvent.click(screen.getByRole('button', { name: /add ssrf entry/i }))

    await waitFor(() => {
      expect(screen.getByText('10.0.0.0/8')).toBeInTheDocument()
    })
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /discard/i }))

    // The drafted '10.0.0.0/8' entry is gone; the saved entry stays (FR-012).
    await waitFor(() => {
      expect(screen.queryByText('10.0.0.0/8')).not.toBeInTheDocument()
      expect(screen.getByText('127.0.0.1')).toBeInTheDocument()
    })
    expect(screen.queryByText(/unsaved change/)).not.toBeInTheDocument()
    expect(mockAddToast).not.toHaveBeenCalled()
    expect(updateSandboxConfig).not.toHaveBeenCalled()
  })

  it('discarding a preset click returns the active preset to the saved state', async () => {
    // Server state matches "Allow loopback only" — advancedOpen stays
    // collapsed at mount.
    vi.mocked(fetchSandboxConfig).mockResolvedValue({
      ...baseConfig,
      ssrf: { allow_internal: ['127.0.0.1', '::1'] },
    })

    renderSection()

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /allow loopback only/i })).toHaveAttribute(
        'aria-pressed',
        'true',
      )
    })

    // Click a DIFFERENT preset — the draft flips the active preset at once.
    fireEvent.click(screen.getByRole('button', { name: /allow rfc1918 \+ loopback/i }))
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /discard/i }))

    // The active preset returns to the saved state (FR-012); the never-
    // persisted RFC1918 selection is undone.
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
    expect(screen.queryByText(/unsaved change/)).not.toBeInTheDocument()
    expect(mockAddToast).not.toHaveBeenCalled()
    expect(updateSandboxConfig).not.toHaveBeenCalled()
  })
})

describe('Discard restores a drafted delete (FR-012)', () => {
  it('discarding an internal-address delete restores the deleted entry from the saved list', async () => {
    // Two entries that match no presets (lengths are 0/2/6), so Advanced mode
    // auto-expands on mount and both entries are visible.
    vi.mocked(fetchSandboxConfig).mockResolvedValue({
      ...baseConfig,
      ssrf: { allow_internal: ['127.0.0.1', '10.0.0.0/8'] },
    })

    renderSection()

    await waitFor(() => {
      expect(screen.getByText('127.0.0.1')).toBeInTheDocument()
      expect(screen.getByText('10.0.0.0/8')).toBeInTheDocument()
    })

    fireEvent.click(screen.getByRole('button', { name: /delete ssrf entry 10\.0\.0\.0\/8/i }))

    // The drafted delete removes the row at once.
    await waitFor(() => {
      expect(screen.queryByText('10.0.0.0/8')).not.toBeInTheDocument()
    })
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /discard/i }))

    // The deleted entry reappears — the delete was never persisted (FR-012).
    await waitFor(() => {
      expect(screen.getByText('10.0.0.0/8')).toBeInTheDocument()
      expect(screen.getByText('127.0.0.1')).toBeInTheDocument()
    })
    expect(screen.queryByText(/unsaved change/)).not.toBeInTheDocument()
    expect(mockAddToast).not.toHaveBeenCalled()
    expect(updateSandboxConfig).not.toHaveBeenCalled()
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

  it('switching to "any path" stages the draft; Save changes sends exactly {restrict_to_workspace: false} (FR-010, FR-015)', async () => {
    vi.mocked(fetchSandboxConfig).mockResolvedValue(configWithWorkspaceLimit(true))
    vi.mocked(updateSandboxConfig).mockResolvedValue(configWithWorkspaceLimit(false))
    const { prompts, requestPassword } = promptRecorder()
    await renderSectionWithShell(requestPassword)

    await waitFor(() => {
      expect(screen.getByTestId('sandbox-workspace-limit-on')).toBeChecked()
    })
    fireEvent.click(screen.getByTestId('sandbox-workspace-limit-off'))
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    expect(updateSandboxConfig).not.toHaveBeenCalled()

    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    await waitFor(() => expect(prompts.length).toBe(1))
    expect(prompts[0]).toEqual(['Shell workspace limit'])
    await waitFor(() => {
      expect(updateSandboxConfig).toHaveBeenCalledTimes(1)
    })
    // toEqual, not toMatchObject: a partial PUT that also carried `mode` or
    // `filesystem_model` would silently re-save whatever the radios happened
    // to be showing, so the body must contain this key alone.
    expect(vi.mocked(updateSandboxConfig).mock.calls[0][0]).toEqual({ restrict_to_workspace: false })
  })

  it('switching back to "confined" stages the draft; Save changes sends exactly {restrict_to_workspace: true} (FR-010, FR-015)', async () => {
    vi.mocked(fetchSandboxConfig).mockResolvedValue(configWithWorkspaceLimit(false))
    vi.mocked(updateSandboxConfig).mockResolvedValue(configWithWorkspaceLimit(true))
    const { requestPassword } = promptRecorder()
    await renderSectionWithShell(requestPassword)

    await waitFor(() => {
      expect(screen.getByTestId('sandbox-workspace-limit-off')).toBeChecked()
    })
    fireEvent.click(screen.getByTestId('sandbox-workspace-limit-on'))
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    expect(updateSandboxConfig).not.toHaveBeenCalled()

    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    await waitFor(() => {
      expect(updateSandboxConfig).toHaveBeenCalledTimes(1)
    })
    expect(vi.mocked(updateSandboxConfig).mock.calls[0][0]).toEqual({ restrict_to_workspace: true })
  })

  it('clicking the already-selected option stages no draft and fires no PUT (FR-011: the bar stays hidden)', async () => {
    vi.mocked(fetchSandboxConfig).mockResolvedValue(configWithWorkspaceLimit(true))

    renderSection()

    await waitFor(() => {
      expect(screen.getByTestId('sandbox-workspace-limit-on')).toBeChecked()
    })
    fireEvent.click(screen.getByTestId('sandbox-workspace-limit-on'))

    expect(screen.queryByText(/unsaved change/)).not.toBeInTheDocument()
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

  it('discarding a workspace-limit draft returns the saved selection, and the change can be re-attempted (FR-012)', async () => {
    vi.mocked(fetchSandboxConfig).mockResolvedValue(configWithWorkspaceLimit(true))

    renderSection()

    await waitFor(() => {
      expect(screen.getByTestId('sandbox-workspace-limit-on')).toBeChecked()
    })
    fireEvent.click(screen.getByTestId('sandbox-workspace-limit-off'))

    // The draft shows at once (FR-010: nothing is stored).
    await waitFor(() => {
      expect(screen.getByTestId('sandbox-workspace-limit-off')).toBeChecked()
    })
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /discard/i }))

    // Back to the saved value (FR-012) — otherwise the radio would sit on an
    // unsaved value with no error shown.
    await waitFor(() => {
      expect(screen.getByTestId('sandbox-workspace-limit-on')).toBeChecked()
    })
    expect(screen.getByTestId('sandbox-workspace-limit-off')).not.toBeChecked()
    expect(screen.queryByText(/unsaved change/)).not.toBeInTheDocument()
    expect(mockAddToast).not.toHaveBeenCalled()
    expect(updateSandboxConfig).not.toHaveBeenCalled()

    // Recovery: the same change must be attemptable again, and Save stores it.
    vi.mocked(updateSandboxConfig).mockResolvedValueOnce(configWithWorkspaceLimit(false))
    fireEvent.click(screen.getByTestId('sandbox-workspace-limit-off'))
    fireEvent.click(await screen.findByRole('button', { name: /save changes/i }))
    await waitFor(() => {
      expect(updateSandboxConfig).toHaveBeenCalledTimes(1)
    })
  })

  it('a refused Save keeps the drafted selection; the bar says the change could not be saved (FR-015, FR-020 outcome 3)', async () => {
    vi.mocked(fetchSandboxConfig).mockResolvedValue(configWithWorkspaceLimit(true))
    // FR-020 outcome 3 is a 4xx REFUSAL (C-6 outcome table, spec :231): a 400
    // refusal ends "not saved" directly. The old 500 here classified as
    // outcome 4 (unknown → the saved-state check), whose bar line is the
    // different "may not have been saved" wording — a 500 was the wrong seam
    // for the outcome-3 oracle this test pins.
    vi.mocked(updateSandboxConfig).mockRejectedValue(new ApiError(400, 'the workspace limit must be a whole number of megabytes'))
    const { requestPassword } = promptRecorder()
    await renderSectionWithShell(requestPassword)

    await waitFor(() => {
      expect(screen.getByTestId('sandbox-workspace-limit-on')).toBeChecked()
    })
    fireEvent.click(screen.getByTestId('sandbox-workspace-limit-off'))
    fireEvent.click(await screen.findByRole('button', { name: /save changes/i }))

    await waitFor(() => {
      expect(updateSandboxConfig).toHaveBeenCalledTimes(1)
    })
    // FR-015/FR-020: the bar names the failure and the draft is kept at its
    // typed value — the old oracle (revert to the persisted state + an error
    // toast) is replaced by the refused-Save outcome.
    expect(await screen.findByText(/1 change could not be saved/)).toBeInTheDocument()
    expect(screen.getByTestId('sandbox-workspace-limit-off')).toBeChecked()
    expect(screen.getByTestId('sandbox-workspace-limit-on')).not.toBeChecked()
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
    // The copy renders while the config read is still in flight; wait for the
    // control itself so the assertions below see the loaded state.
    await screen.findByTestId('sandbox-workspace-limit-on')

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

// ── F03 on the Sandbox section: a newer draft survives a held saved-state read ─
//
// Settings redesign #1519 (C-6 / F03; FR-010, FR-011, FR-020, FR-012). The
// F03 rule this train pinned on Gateway (a newer edit of the same field
// survives an older in-flight Save) holds on the Sandbox section too, with
// one more place a refill can erase a draft: the saved configuration itself
// changes under the typed draft. FR-020's changed-elsewhere branch (the
// saved-state check reads a THIRD value) makes the check's section read the
// new saved base, and FR-010/FR-011 keep every unsaved change a draft until
// it is explicitly saved or discarded — a draft typed while the Save or its
// saved-state read is pending is a NEWER draft: not sent, so no answer may
// reset it. FR-012 still sends Discard to the last saved value known to the
// screen — the check's read, not the pre-save one.
describe('Sandbox — a newer draft typed while the saved-state check reads keeps (F03/C-6)', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('write unknown and the check read held: the mode drafted meanwhile survives the refill, the untouched values take the read, Discard resets every field (FR-010, FR-011, FR-020, FR-012)', async () => {
    // The saved configuration the section opens on; the field the older Save
    // sends is Allowed folders (holds ['/a']).
    vi.mocked(fetchSandboxConfig).mockResolvedValueOnce({ ...baseConfig, allowed_paths: ['/a'] })

    // C-6's one wire read is GET /config/pending-restart (the file read): the
    // sent key is NOT listed there, so the saved value comes from the section
    // read (C-6, spec :246). Any other wire fetch is a harness bug.
    const wireCalls: string[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: unknown) => {
        const url = String(input)
        wireCalls.push(url)
        if (url.includes('/config/pending-restart')) return { ok: true, status: 200, json: async () => [] }
        throw new Error(`unexpected wire fetch ${url}`)
      }),
    )

    // The Save's answer is lost (FR-020 outcome 4: a connection failure is an
    // unknown result), so the section runs the saved-state check — and the
    // check's section read is the HELD seam: it answers only when released.
    let releaseCheck!: (config: SandboxConfigResponse) => void
    const heldRead = new Promise<SandboxConfigResponse>((resolve) => {
      releaseCheck = resolve
    })
    vi.mocked(updateSandboxConfig).mockRejectedValue(new TypeError('connection lost'))
    vi.mocked(fetchSandboxConfig).mockImplementationOnce(() => heldRead)

    renderSection()

    // The person stages an Allowed folders add — the value the older Save sends.
    await waitFor(() => expect(screen.getByText('/a')).toBeInTheDocument())
    fireEvent.change(screen.getByRole('textbox', { name: /new allowed path/i }), { target: { value: '/draft-a' } })
    fireEvent.click(screen.getByRole('button', { name: /add path/i }))
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()

    // Save: the write rejects (unknown) and the check starts; its read hangs.
    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    await waitFor(() => expect(updateSandboxConfig).toHaveBeenCalledTimes(1))

    // While the check read is pending, the person types a NEWER draft into a
    // field the older Save did not send: Sandbox mode → off.
    fireEvent.click(screen.getByRole('radio', { name: /sandbox mode: off/i }))
    expect(screen.getByRole('radio', { name: /sandbox mode: off/i })).toBeChecked()

    // The read lands now, changed elsewhere: mode enforce (a value the person
    // never typed) and Allowed folders ['/elsewhere'] — a third value for the
    // sent field, neither its draft pair nor the pre-save ['/a'] (FR-020).
    await act(async () => {
      releaseCheck({ ...baseConfig, mode: 'enforce', allowed_paths: ['/elsewhere'] })
    })

    // F03: the newer mode draft SURVIVES the refill — the field keeps it and
    // the bar still counts it alongside the kept sent draft (2 unsaved
    // changes). The sent field keeps its draft and names the third saved
    // value in the exact FR-020 line (a list is shown as its JSON).
    expect(
      await screen.findByText(
        'This setting was changed somewhere else while you were saving. Saved value: ["/elsewhere"].',
      ),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('radio', { name: /sandbox mode: off/i }),
      'F03: the draft typed during the pending read must survive it',
    ).toBeChecked()
    expect(screen.getByRole('radio', { name: /sandbox mode: enforce/i })).not.toBeChecked()
    expect(screen.getByText('/draft-a')).toBeInTheDocument()
    expect(screen.getByText(/2 unsaved changes/)).toBeInTheDocument()

    // FR-012: Discard still resets EVERY field — to the check's read (the
    // last saved value known to the screen), not to the pre-save values.
    fireEvent.click(screen.getByRole('button', { name: /discard/i }))
    await waitFor(() => {
      expect(screen.getByRole('radio', { name: /sandbox mode: enforce/i })).toBeChecked()
      expect(screen.getByText('/elsewhere')).toBeInTheDocument()
      expect(screen.queryByText('/draft-a')).not.toBeInTheDocument()
    })
    expect(screen.queryByText(/unsaved change/)).not.toBeInTheDocument()
    // The check really consulted the C-6 file read, not the running read alone.
    expect(wireCalls.some((u) => u.includes('/config/pending-restart'))).toBe(true)
  })

  it('a newer draft on a field the held Save already sent, with a different value, survives the refill too (F03/C-6; FR-010, FR-011, FR-020, FR-012)', async () => {
    // The first held-read case drives direction one (a field the Save did
    // not send). This case drives direction two (CHECK R4-04, survivors
    // N39/N40): while the saved-state check read is held, the person edits
    // the SENT field itself to a NEWER, different value — Allowed folders
    // was sent as ['/a', '/draft-a'], and '/draft-b' is typed on top. A
    // watcher that preserves only previously unsent fields, or that covers
    // only the mode family, loses this draft.
    vi.mocked(fetchSandboxConfig).mockResolvedValueOnce({ ...baseConfig, allowed_paths: ['/a'] })

    const wireCalls: string[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: unknown) => {
        const url = String(input)
        wireCalls.push(url)
        if (url.includes('/config/pending-restart')) return { ok: true, status: 200, json: async () => [] }
        throw new Error(`unexpected wire fetch ${url}`)
      }),
    )

    let releaseCheck!: (config: SandboxConfigResponse) => void
    const heldRead = new Promise<SandboxConfigResponse>((resolve) => {
      releaseCheck = resolve
    })
    vi.mocked(updateSandboxConfig).mockRejectedValue(new TypeError('connection lost'))
    vi.mocked(fetchSandboxConfig).mockImplementationOnce(() => heldRead)

    renderSection()

    // The person stages the Allowed folders add the older Save will send.
    await waitFor(() => expect(screen.getByText('/a')).toBeInTheDocument())
    fireEvent.change(screen.getByRole('textbox', { name: /new allowed path/i }), { target: { value: '/draft-a' } })
    fireEvent.click(screen.getByRole('button', { name: /add path/i }))
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()

    // Save: the write rejects (unknown) and the check starts; its read hangs.
    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    await waitFor(() => expect(updateSandboxConfig).toHaveBeenCalledTimes(1))

    // While the read is pending, a NEWER, different value on the SENT field.
    fireEvent.change(screen.getByRole('textbox', { name: /new allowed path/i }), { target: { value: '/draft-b' } })
    fireEvent.click(screen.getByRole('button', { name: /add path/i }))
    expect(screen.getByText('/draft-b')).toBeInTheDocument()

    // The read lands, changed elsewhere: Allowed folders ['/elsewhere'] — a
    // third value, neither the sent pair nor the newer draft's list.
    await act(async () => {
      releaseCheck({ ...baseConfig, mode: 'enforce', allowed_paths: ['/elsewhere'] })
    })

    // The sent field names its third saved value in the exact FR-020 line.
    expect(
      await screen.findByText(
        'This setting was changed somewhere else while you were saving. Saved value: ["/elsewhere"].',
      ),
    ).toBeInTheDocument()
    // F03, second direction: BOTH drafts survive the refill — the staged
    // value the Save sent and the newer one typed during the read; the
    // elsewhere list never reaches the field.
    expect(screen.getByText('/draft-a')).toBeInTheDocument()
    expect(
      screen.getByText('/draft-b'),
      'F03: the newer, different draft typed on the sent field must survive the pending read',
    ).toBeInTheDocument()
    expect(screen.queryByText('/elsewhere')).not.toBeInTheDocument()
    expect(screen.getByText(/1 unsaved change/)).toBeInTheDocument()

    // FR-012: Discard resets the field to the check's read.
    fireEvent.click(screen.getByRole('button', { name: /discard/i }))
    await waitFor(() => {
      expect(screen.getByText('/elsewhere')).toBeInTheDocument()
      expect(screen.queryByText('/draft-a')).not.toBeInTheDocument()
      expect(screen.queryByText('/draft-b')).not.toBeInTheDocument()
    })
    expect(screen.queryByText(/unsaved change/)).not.toBeInTheDocument()
    expect(wireCalls.some((u) => u.includes('/config/pending-restart'))).toBe(true)
  })

  it('the other field families keep their newer drafts across the held read too: filesystem model, shell workspace limit and internal network (F03/C-6; FR-010, FR-011, FR-020, FR-012)', async () => {
    // CHECK R4-04, survivors N41/N42/N43: the held-read watcher must not be
    // generalizable from the one field its first case drives. The saved
    // configuration reports the workspace limit, so its radios render; the
    // older Save sends Sandbox mode (off) — and while the check read is
    // held, the person drafts newer values in the THREE families the Save
    // did not send. Each must survive the refill; Discard must reset every
    // family to the read.
    vi.mocked(fetchSandboxConfig).mockResolvedValueOnce({
      ...baseConfig,
      restrict_to_workspace: true,
    } as SandboxConfigResponse)

    const wireCalls: string[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: unknown) => {
        const url = String(input)
        wireCalls.push(url)
        if (url.includes('/config/pending-restart')) return { ok: true, status: 200, json: async () => [] }
        throw new Error(`unexpected wire fetch ${url}`)
      }),
    )

    let releaseCheck!: (config: SandboxConfigResponse) => void
    const heldRead = new Promise<SandboxConfigResponse>((resolve) => {
      releaseCheck = resolve
    })
    vi.mocked(updateSandboxConfig).mockRejectedValue(new TypeError('connection lost'))
    vi.mocked(fetchSandboxConfig).mockImplementationOnce(() => heldRead)

    renderSection()

    await waitFor(() => expect(screen.getByTestId('sandbox-workspace-limit-on')).toBeInTheDocument())
    // The value the older Save sends: Sandbox mode → off.
    fireEvent.click(screen.getByRole('radio', { name: /sandbox mode: off/i }))
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    await waitFor(() => expect(updateSandboxConfig).toHaveBeenCalledTimes(1))

    // While the read is pending: newer drafts in the three unsent families.
    fireEvent.click(screen.getByTestId('sandbox-fs-model-open'))
    fireEvent.click(screen.getByTestId('sandbox-workspace-limit-off'))
    fireEvent.click(screen.getByRole('button', { name: /advanced \(custom list\)/i }))
    fireEvent.change(screen.getByRole('textbox', { name: /new ssrf allow entry/i }), { target: { value: '10.0.0.0/8' } })
    fireEvent.click(screen.getByRole('button', { name: /add ssrf entry/i }))
    expect(screen.getByText('10.0.0.0/8')).toBeInTheDocument()
    expect(screen.getByText(/4 unsaved changes/)).toBeInTheDocument()

    // The read lands, changed elsewhere: every family holds a DIFFERENT
    // saved value than the draft typed over it.
    await act(async () => {
      releaseCheck({
        ...baseConfig,
        mode: 'enforce',
        restrict_to_workspace: true,
        filesystem_model: 'confined',
        ssrf: { allow_internal: ['192.168.0.0/16'] },
      } as SandboxConfigResponse)
    })

    // The SENT field keeps what was sent as its draft on top of the saved
    // read (FR-012/FR-020: sent drafts stay drafts), and names the saved
    // value in the exact FR-020 line (a string is shown raw).
    expect(
      await screen.findByText('This setting was changed somewhere else while you were saving. Saved value: enforce.'),
    ).toBeInTheDocument()
    expect(screen.getByRole('radio', { name: /sandbox mode: off/i })).toBeChecked()
    expect(screen.getByRole('radio', { name: /sandbox mode: enforce/i })).not.toBeChecked()
    // Every family's newer draft survives its refill.
    expect(
      screen.getByTestId('sandbox-fs-model-open'),
      'F03: the filesystem-model draft must survive the pending read',
    ).toBeChecked()
    expect(
      screen.getByTestId('sandbox-workspace-limit-off'),
      'F03: the workspace-limit draft must survive the pending read',
    ).toBeChecked()
    expect(
      screen.getByText('10.0.0.0/8'),
      'F03: the internal-network draft must survive the pending read',
    ).toBeInTheDocument()
    expect(screen.queryByText('192.168.0.0/16')).not.toBeInTheDocument()
    expect(screen.getByText(/4 unsaved changes/)).toBeInTheDocument()

    // FR-012: Discard resets EVERY family — the sent draft included — to
    // the check's read.
    fireEvent.click(screen.getByRole('button', { name: /discard/i }))
    await waitFor(() => {
      expect(screen.getByRole('radio', { name: /sandbox mode: enforce/i })).toBeChecked()
      expect(screen.getByTestId('sandbox-fs-model-confined')).toBeChecked()
      expect(screen.getByTestId('sandbox-workspace-limit-on')).toBeChecked()
      expect(screen.getByText('192.168.0.0/16')).toBeInTheDocument()
    })
    expect(screen.queryByText(/unsaved change/)).not.toBeInTheDocument()
    expect(wireCalls.some((u) => u.includes('/config/pending-restart'))).toBe(true)
  })
})

// ── Retired help text: the section names the rules that still apply (ADR-092) ─
//
// The shell command-text block list is a RETIRED surface — ADR-092 deleted it
// outright (root CLAUDE.md, "Retired surfaces"; the guard
// scripts/check-no-shell-deny-patterns.sh exists because it must stay gone).
// docs/security.md "Shell command rules" is where the surviving control is
// specified: shell command rules are allow/ask/deny decisions for one program
// that live in the `sandbox.command_rules` array inside the configuration
// file, written there by hand — "There is no screen for this". So where the
// Sandbox section tells the reader which protections survive a choice, it
// must name the shell command rules in the configuration file, and must never
// name the deleted "blocked command patterns" (help text that names a deleted
// control sends the reader looking for a setting that does not exist).
describe('retired help text — the section names the shell command rules, never the retired block list (ADR-092)', () => {
  it('the Off mode names the shell command rules in the configuration file and never the retired blocked command patterns', async () => {
    renderSection()

    await waitFor(() => {
      expect(screen.getByRole('radio', { name: /sandbox mode: off/i })).toBeInTheDocument()
    })

    // The Off option's own block (its label and its description).
    const offOption = screen.getByRole('radio', { name: /sandbox mode: off/i }).closest('label')
    expect(offOption, 'ADR-092: the Off description must name the shell command rules').toHaveTextContent(
      /shell command rules/i,
    )
    expect(offOption, 'docs/security.md: the rules live in the configuration file').toHaveTextContent(
      /configuration file/i,
    )
    expect(
      offOption?.textContent,
      'ADR-092: the block list is deleted — no help text may name it',
    ).not.toMatch(/blocked command patterns/i)
  })

  it('the Any path option names the shell command rules in the configuration file and never the retired blocked command patterns', async () => {
    // restrict_to_workspace present, so the workspace-limit radios render
    // (the Any path option is one of them).
    vi.mocked(fetchSandboxConfig).mockResolvedValue(configWithWorkspaceLimit(true))

    renderSection()

    await waitFor(() => {
      expect(screen.getByTestId('sandbox-workspace-limit-off')).toBeInTheDocument()
    })

    const anyPathOption = screen.getByTestId('sandbox-workspace-limit-off').closest('label')
    expect(anyPathOption, 'ADR-092: the Any path description must name the shell command rules').toHaveTextContent(
      /shell command rules/i,
    )
    expect(anyPathOption, 'docs/security.md: the rules live in the configuration file').toHaveTextContent(
      /configuration file/i,
    )
    expect(
      anyPathOption?.textContent,
      'ADR-092: the block list is deleted — no help text may name it',
    ).not.toMatch(/blocked command patterns/i)
  })
})

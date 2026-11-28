// UAT B09 / Settings US-2 scenario 7, FR-010/FR-015/FR-015a/FR-020:
// a refused new path returns to its Add input with the server reason and
// cannot contaminate a later Save. Add only stages; explicit Save writes.
// Real editor, Save bar and shell confirmation; only the HTTP edge is scripted.
import type { ReactNode } from 'react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { SandboxSection } from './SandboxSection'
import { SettingsShellContext } from './SettingsShellContext'
import { useSavePasswordPrompt } from './useSavePasswordPrompt'
import { ApiError, fetchAppState, fetchSandboxConfig, fetchSandboxStatus, updateSandboxConfig } from '@/lib/api'
import type { AppState, SandboxConfigResponse, SandboxStatus } from '@/lib/api'

vi.mock('@/lib/api', async importOriginal => ({
  ...await importOriginal<typeof import('@/lib/api')>(),
  fetchAppState: vi.fn(), fetchSandboxConfig: vi.fn(), fetchSandboxStatus: vi.fn(), updateSandboxConfig: vi.fn(),
}))

const SAVED_PATH = '/srv/office-uat/documents'
const REFUSED_PATH = '/opt/office-uat/install/pdfium'
const VALID_PATH = '/srv/office-uat/reference'
const REFUSAL = `config error: sandbox.allowed_paths entry "${REFUSED_PATH}" would give agents write access to the PDF renderer program folder ${REFUSED_PATH}; agents can never get write access there.`
const SAVED_CONFIG: SandboxConfigResponse = {
  mode: 'permissive', allowed_paths: [SAVED_PATH], ssrf: { allow_internal: [] }, requires_restart: false,
}
const STATUS: SandboxStatus = {
  backend: 'landlock', available: true, kernel_level: true, policy_applied: true, seccomp_enabled: true, bind_ports_count: 0,
}

function SaveShell({ children }: { children: ReactNode }) {
  const { requestPassword, dialog } = useSavePasswordPrompt()
  return <SettingsShellContext.Provider value={{
    announce: () => {}, setDraftCount: () => {}, offerRestart: () => {}, refreshPendingRestart: () => {},
    requestPassword, saveBarVisible: false, setSaveBarVisible: () => {},
  }}>{children}{dialog}</SettingsShellContext.Provider>
}

async function stage(path: string) {
  fireEvent.change(await screen.findByRole('textbox', { name: /new allowed path/i }), { target: { value: path } })
  fireEvent.click(screen.getByRole('button', { name: /add path/i }))
  expect(screen.getByRole('button', { name: `Delete path ${path}` })).toBeVisible()
  expect(screen.queryByRole('alertdialog'), 'FR-010: Add only stages; it does not start a Save').toBeNull()
}

async function saveAndConfirm() {
  fireEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  const confirmation = await screen.findByRole('alertdialog', { name: 'Save these settings?' })
  expect(confirmation).toHaveTextContent('Allowed folders')
  fireEvent.click(within(confirmation).getByRole('button', { name: /^Save$/ }))
  await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
}

beforeEach(() => {
  vi.clearAllMocks()
  localStorage.clear()
  sessionStorage.clear()
  vi.mocked(fetchAppState).mockResolvedValue({
    onboarding_complete: true, identity: { mode: 'platform', edition: 'hosted', signed_in: true },
  } as AppState)
  vi.mocked(fetchSandboxStatus).mockResolvedValue(STATUS)
  vi.mocked(fetchSandboxConfig).mockResolvedValue(SAVED_CONFIG)
})

describe('sandbox path save recovery (Office UAT B09)', () => {
  it('FR-015a B09: recovers a refused new path to the Add input and never resends it in the next valid Save', async () => {
    const accepted = { ...SAVED_CONFIG, allowed_paths: [SAVED_PATH, VALID_PATH], requires_restart: true }
    vi.mocked(updateSandboxConfig)
      .mockRejectedValueOnce(new ApiError(400, REFUSAL))
      .mockImplementationOnce(async () => {
        vi.mocked(fetchSandboxConfig).mockResolvedValue(accepted)
        return accepted
      })
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    const view = render(<QueryClientProvider client={client}><SaveShell><SandboxSection /></SaveShell></QueryClientProvider>)
    try {
      await screen.findByText(SAVED_PATH)
      await stage(REFUSED_PATH)
      expect(screen.getByText('1 unsaved change')).toBeVisible()
      expect(updateSandboxConfig).not.toHaveBeenCalled()
      await saveAndConfirm()
      await waitFor(() => expect(screen.queryAllByText(REFUSAL).length, 'instrument: this editor actually received the refusal').toBeGreaterThan(0))
      expect(vi.mocked(updateSandboxConfig).mock.calls[0]).toEqual([
        { allowed_paths: [SAVED_PATH, REFUSED_PATH] }, '',
      ])
      expect(screen.queryByText(/^Saved at /), 'B09: refusal must never look saved').toBeNull()
      const input = screen.getByRole('textbox', { name: 'New allowed path' })
      expect(input, 'FR-015a: the generic B09 refusal restores this Save\'s new path').toHaveValue(REFUSED_PATH)
      expect(screen.queryByRole('button', { name: `Delete path ${REFUSED_PATH}` }), 'FR-015a: a refused new path leaves the staged list').toBeNull()
      const addArea = input.parentElement!.parentElement!
      expect(within(addArea).getByText(REFUSAL), 'FR-015a: the full generic server reason belongs under the recovered input').toBeVisible()
      expect(screen.getAllByRole('button', { name: /^Delete path / }).map(button => button.getAttribute('aria-label'))).toEqual([
        `Delete path ${SAVED_PATH}`,
      ])

      // No manual trash-button workaround: the rejected entry is already out
      // of the list. The person replaces only the recovered input text.
      await stage(VALID_PATH)
      expect(updateSandboxConfig, 'B09: editing after refusal cannot automatically resend the refused path').toHaveBeenCalledTimes(1)
      expect(screen.queryByRole('button', { name: `Delete path ${REFUSED_PATH}` })).toBeNull()
      await saveAndConfirm()
      await waitFor(() => expect(updateSandboxConfig).toHaveBeenCalledTimes(2))
      expect(vi.mocked(updateSandboxConfig).mock.calls[1], 'B09: the rejected entry must not poison the next save').toEqual([
        { allowed_paths: [SAVED_PATH, VALID_PATH] }, '',
      ])
      expect(screen.queryByRole('button', { name: `Delete path ${REFUSED_PATH}` }), 'B09: a recovered but unadded refusal is not an applied row').toBeNull()
      expect(await screen.findByText(/^Saved at /)).toBeVisible()
    } finally {
      view.unmount()
      client.clear()
    }
  })
})

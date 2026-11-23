// UAT B09; office-preview spec §3.2 and the test-author follow-up brief:
// a refused addition must not contaminate the next otherwise-valid save.
// Real editor, mutation and confirmation; only the HTTP API edge is scripted.
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { SandboxSection } from './SandboxSection'
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

async function addAndConfirm(path: string) {
  fireEvent.change(await screen.findByRole('textbox', { name: /new allowed path/i }), { target: { value: path } })
  fireEvent.click(screen.getByRole('button', { name: /add path/i }))
  const confirmation = await screen.findByRole('alertdialog')
  fireEvent.click(within(confirmation).getByRole('button', { name: 'Save sandbox configuration' }))
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
  it('does not resend a refused path when the person next adds a valid path', async () => {
    vi.mocked(updateSandboxConfig)
      .mockRejectedValueOnce(new ApiError(400, REFUSAL))
      .mockResolvedValueOnce({ ...SAVED_CONFIG, allowed_paths: [SAVED_PATH, VALID_PATH], requires_restart: true })
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    const view = render(<QueryClientProvider client={client}><SandboxSection /></QueryClientProvider>)
    try {
      await screen.findByText(SAVED_PATH)
      await addAndConfirm(REFUSED_PATH)
      await waitFor(() => expect(screen.queryAllByText(REFUSAL).length, 'instrument: this editor actually received the refusal').toBeGreaterThan(0))
      expect(vi.mocked(updateSandboxConfig).mock.calls[0]).toEqual([
        { allowed_paths: [SAVED_PATH, REFUSED_PATH], ssrf: { allow_internal: [] } }, undefined,
      ])

      // No manual trash-button workaround. The next save must contain only
      // the server's saved list plus this new, valid addition.
      await addAndConfirm(VALID_PATH)
      await waitFor(() => expect(updateSandboxConfig).toHaveBeenCalledTimes(2))
      expect(vi.mocked(updateSandboxConfig).mock.calls[1], 'B09: the rejected entry must not poison the next save').toEqual([
        { allowed_paths: [SAVED_PATH, VALID_PATH], ssrf: { allow_internal: [] } }, undefined,
      ])
      expect(screen.queryByRole('button', { name: `Delete path ${REFUSED_PATH}` }), 'a refused path is not an applied row').toBeNull()
    } finally {
      view.unmount()
      client.clear()
    }
  })
})

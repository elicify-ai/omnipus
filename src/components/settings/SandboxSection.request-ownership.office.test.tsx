// SFH-03 / Office B09: a delayed refusal belongs to the request that
// failed, not the next staged edit. Real editor, password confirmation and
// query mutation; only the HTTP API edge is controlled by promise barriers.
import { act } from 'react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { SandboxSection } from './SandboxSection'
import { ApiError, fetchAppState, fetchSandboxConfig, fetchSandboxStatus, reAuth, updateSandboxConfig } from '@/lib/api'
import type { AppState, SandboxConfigResponse, SandboxStatus } from '@/lib/api'
import { deferred } from '../../../tests/chat-history-paging/fixtures'

vi.mock('@/lib/api', async importOriginal => ({
  ...await importOriginal<typeof import('@/lib/api')>(),
  fetchAppState: vi.fn(), fetchSandboxConfig: vi.fn(), fetchSandboxStatus: vi.fn(),
  reAuth: vi.fn(), updateSandboxConfig: vi.fn(),
}))

const SAVED = '/srv/office-request/saved'
const A = '/srv/office-request/path-a'
const B = '/srv/office-request/path-b'
const VALID = '/srv/office-request/accepted'
const REASON_A = `Path ${A} was refused for request A.`
const REASON_B = `Path ${B} was refused for request B.`
const CONFIG: SandboxConfigResponse = {
  mode: 'permissive', allowed_paths: [SAVED], ssrf: { allow_internal: [] }, requires_restart: false,
}
const STATUS: SandboxStatus = {
  backend: 'landlock', available: true, kernel_level: true, policy_applied: true,
  seccomp_enabled: true, bind_ports_count: 0,
}

async function mountEditor() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  const view = render(<QueryClientProvider client={client}><SandboxSection /></QueryClientProvider>)
  await screen.findByText(SAVED)
  expect(vi.mocked(fetchAppState).mock.results).toHaveLength(1)
  return { close: () => { view.unmount(); client.clear() } }
}

async function stage(path: string) {
  const input = await screen.findByRole('textbox', { name: 'New allowed path' })
  fireEvent.change(input, { target: { value: path } })
  fireEvent.click(screen.getByRole('button', { name: 'Add path' }))
  const dialog = await screen.findByRole('dialog', { name: 'Confirm your password' })
  return { input, dialog }
}

async function confirm(dialog: HTMLElement) {
  // Synthetic consent only; this fixture has no real account or password.
  fireEvent.change(within(dialog).getByLabelText('Password'), { target: { value: 'office-test-consent' } })
  fireEvent.click(within(dialog).getByRole('button', { name: 'Confirm' }))
  await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Confirm your password' })).toBeNull())
}

async function refuse(request: ReturnType<typeof deferred<SandboxConfigResponse>>, reason: string, row = 1) {
  const response = `allowed_paths[${row}]: ${reason}`
  await act(async () => { request.reject(new ApiError(400, response)) })
  // Receipt, not an oracle for which draft to restore: the common error
  // status retains the exact response, even when an unrelated newer input
  // must not be labelled with this request's inline error.
  await waitFor(() => expect(screen.getByText('Save failed')).toHaveAttribute('title', response))
}

beforeEach(() => {
  vi.resetAllMocks()
  localStorage.clear()
  sessionStorage.clear()
  vi.mocked(fetchAppState).mockResolvedValue({
    onboarding_complete: true, identity: { mode: 'local', edition: 'core', signed_in: true },
  } as AppState)
  vi.mocked(fetchSandboxStatus).mockResolvedValue(STATUS)
  vi.mocked(fetchSandboxConfig).mockResolvedValue(CONFIG)
  vi.mocked(reAuth)
    .mockResolvedValueOnce({ verified: true, token: 'office-consent-A', expires_in: 300 })
    .mockResolvedValueOnce({ verified: true, token: 'office-consent-B', expires_in: 300 })
})

describe('Office Settings refusal request ownership (SFH-03)', () => {
  it('leaves staged B draft and input alone on A refusal, then restores B on its own refusal without cancelling its confirmation', async () => {
    // Dispatcher decision 2026-11-24: an older failure cannot restore A over
    // newer staged B. A's path and reason belong in the shared save error.
    const first = deferred<SandboxConfigResponse>()
    const second = deferred<SandboxConfigResponse>()
    vi.mocked(updateSandboxConfig).mockImplementationOnce(() => first.promise).mockImplementationOnce(() => second.promise)
    const editor = await mountEditor()
    try {
      await confirm((await stage(A)).dialog)
      await waitFor(() => expect(updateSandboxConfig).toHaveBeenCalledTimes(1))
      expect(vi.mocked(updateSandboxConfig).mock.calls[0]).toEqual([
        { allowed_paths: [SAVED, A], ssrf: { allow_internal: [] } }, 'office-consent-A',
      ])
      const pendingB = await stage(B)
      expect(updateSandboxConfig).toHaveBeenCalledTimes(1)
      expect(pendingB.input).toHaveValue('')
      const draftPaths = () => screen.getAllByRole('button', { name: /^Delete path /, hidden: true }).map(button => button.getAttribute('aria-label'))
      const stagedPaths = draftPaths()
      expect(stagedPaths, 'instrument: B really joined the newer staged draft').toContain(`Delete path ${B}`)

      await refuse(first, REASON_A)
      expect.soft(pendingB.input, 'SFH-03: A refusal must leave the newer staged B input alone').toHaveValue('')
      expect(draftPaths(), 'SFH-03: A refusal cannot roll back any part of the newer staged draft').toEqual(stagedPaths)
      expect(screen.getByText('Save failed')).toHaveAttribute('title', `allowed_paths[1]: ${REASON_A}`)
      expect(pendingB.dialog, 'B confirmation survives A outcome').toBeVisible()
      expect(updateSandboxConfig).toHaveBeenCalledTimes(1)
      await confirm(pendingB.dialog)
      await waitFor(() => expect(updateSandboxConfig).toHaveBeenCalledTimes(2))
      const [bodyB, tokenB] = vi.mocked(updateSandboxConfig).mock.calls[1]
      expect(bodyB.allowed_paths, 'instrument: retained confirmation still sends its own B edit').toContain(B)
      expect(bodyB.ssrf).toEqual({ allow_internal: [] })
      expect(tokenB).toBe('office-consent-B')
      // Keep the empty-input action before B's own refusal; this is not a
      // manual deletion of any failed list row. B must retain its own data.
      fireEvent.change(pendingB.input, { target: { value: '' } })
      const rowB = bodyB.allowed_paths?.indexOf(B) ?? -1
      expect(rowB).toBeGreaterThan(0)
      await refuse(second, REASON_B, rowB)
      expect(pendingB.input, 'SFH-03: A completion must not clear B request recovery metadata').toHaveValue(B)
      expect(screen.getByText(REASON_B)).toBeVisible()
      expect(screen.queryByRole('button', { name: `Delete path ${A}` })).toBeNull()
      expect(screen.queryByRole('button', { name: `Delete path ${B}` })).toBeNull()
      expect(screen.getByText(SAVED)).toBeVisible()
    } finally { editor.close() }
  })

  it('does not overwrite a newer typed draft B when the confirmed A request fails', async () => {
    const first = deferred<SandboxConfigResponse>()
    vi.mocked(updateSandboxConfig).mockImplementationOnce(() => first.promise)
    const editor = await mountEditor()
    try {
      const pendingA = await stage(A)
      await confirm(pendingA.dialog)
      await waitFor(() => expect(updateSandboxConfig).toHaveBeenCalledTimes(1))
      fireEvent.change(pendingA.input, { target: { value: B } })
      expect(pendingA.input).toHaveValue(B)
      await refuse(first, REASON_A)
      expect(pendingA.input, 'SFH-03: refusal of A cannot discard newer unsubmitted input B').toHaveValue(B)
      expect(screen.getByText('Save failed')).toBeVisible()
      expect(screen.getByText('Save failed')).toHaveAttribute('title', `allowed_paths[1]: ${REASON_A}`)
      expect(updateSandboxConfig).toHaveBeenCalledTimes(1)
    } finally { editor.close() }
  })

  it('control: after serial refusal the next valid save contains only the saved path and accepted addition', async () => {
    const first = deferred<SandboxConfigResponse>()
    const saved: SandboxConfigResponse = { ...CONFIG, allowed_paths: [SAVED, VALID] }
    vi.mocked(updateSandboxConfig).mockImplementationOnce(() => first.promise).mockResolvedValueOnce(saved)
    const editor = await mountEditor()
    try {
      await confirm((await stage(A)).dialog)
      await waitFor(() => expect(updateSandboxConfig).toHaveBeenCalledTimes(1))
      await refuse(first, REASON_A)
      expect(screen.getByRole('textbox', { name: 'New allowed path' })).toHaveValue(A)
      expect(screen.getByText(REASON_A)).toBeVisible()
      vi.mocked(fetchSandboxConfig).mockResolvedValue(saved)
      await confirm((await stage(VALID)).dialog)
      await waitFor(() => expect(updateSandboxConfig).toHaveBeenCalledTimes(2))
      expect(vi.mocked(updateSandboxConfig).mock.calls[1]).toEqual([
        { allowed_paths: [SAVED, VALID], ssrf: { allow_internal: [] } }, 'office-consent-B',
      ])
      await screen.findByText(VALID)
      expect(screen.queryByRole('button', { name: `Delete path ${A}` })).toBeNull()
      expect(screen.getByText(SAVED)).toBeVisible()
    } finally { editor.close() }
  })
})
